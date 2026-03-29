// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"fmt"
	"go/token"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
)

var (
	noPkg = &Package{Scope: newScope(Universe, PackageScope)}
)

type importTask struct {
	sync.Mutex
	p     *Package
	ready chan struct{}
}

// BuildContext coordinates creating a package tree.
type BuildContext struct {
	errMu     sync.Mutex
	importsMu sync.Mutex

	errList     ErrList
	fsys        fs.FS
	importTasks map[string]*importTask // import path: importTask
	importGraph map[string]map[string]bool
	limit       int

	noDeclarationChecks bool
}

// NewBuildContext returns a newly created BuildContext. 'limit' is the maximum
// desired concurrency for individual package building when > 0.
func NewBuildContext(fsys fs.FS, limit int) (c *BuildContext) {
	return &BuildContext{
		fsys:        fsys,
		importTasks: map[string]*importTask{},
		importGraph: map[string]map[string]bool{},
		limit:       limit,
	}
}

func (c *BuildContext) syncErr(pos token.Position, s string, args ...any) {
	c.errMu.Lock()

	defer c.errMu.Unlock()

	c.err(pos, s, args...)
}

func (c *BuildContext) err(pos token.Position, s string, args ...any) {
	c.errList.AddErr(pos, s, args...)
}

// findCycle performs a DFS to see if 'target' is reachable from 'current'.
// It returns the path of the cycle if one exists.
func (c *BuildContext) findCycle(current, target string, visited map[string]bool) []string {
	if current == target {
		return []string{current}
	}
	visited[current] = true

	for next := range c.importGraph[current] {
		if !visited[next] {
			if cycle := c.findCycle(next, target, visited); cycle != nil {
				return append([]string{current}, cycle...)
			}
		}
	}
	return nil
}

func (c *BuildContext) importPkg(fromPath, importPath string, importPathToken Token) (p *Package) {
	if c == nil {
		return noPkg
	}

	c.importsMu.Lock()

	if c.importGraph[fromPath] == nil {
		c.importGraph[fromPath] = make(map[string]bool)
	}
	c.importGraph[fromPath][importPath] = true

	if cycle := c.findCycle(importPath, fromPath, make(map[string]bool)); cycle != nil {
		c.importsMu.Unlock()

		// To complete the visual circle for the error message, put 'fromPath' at the front.
		fullCycle := append([]string{fromPath}, cycle...)
		c.syncErr(importPathToken.Position(), "import cycle not allowed: %s", strings.Join(fullCycle, " -> "))
		return noPkg
	}

	task := c.importTasks[importPath]
	if task == nil {
		task = &importTask{}
		c.importTasks[importPath] = task
	}

	c.importsMu.Unlock()

	task.Lock()

	if task.ready == nil {
		task.ready = make(chan struct{})
		go func() {
			defer close(task.ready)

			dirEntries, err := fs.ReadDir(c.fsys, importPath)
			if err != nil {
				task.p = noPkg
				return
			}

			var files []string
			for _, v := range dirEntries {
				if v.IsDir() {
					continue
				}

				switch nm := v.Name(); path.Ext(nm) {
				case ".ogo":
					if !strings.HasSuffix(nm, "_test.ogo") {
						files = append(files, path.Join(importPath, nm))
					}
				}
			}

			task.p = c.NewPackage(importPath, files, c.fsys)
		}()
	}

	task.Unlock()
	<-task.ready
	return task.p
}

func consolidateErrors(use ErrList, errors ...error) (e ErrList) {
	e = use
	for _, v := range errors {
		switch x := v.(type) {
		case nil:
			// nop
		case ErrList:
			e = append(e, x...)
		default:
			e = append(e, ErrWithPosition{Err: x})
		}
	}
	return e
}

// Build builds the main package consisting of files in 'files' within 'fsys'.
// 'limit' is the maximum desired concurrency for individual package building
// when > 0.
//
// 'files' must be base names within fsys. Build resolves and import paths
// a/b/c as paths a/b/c within fsys.
func Build(limit int, files []string, fsys fs.FS) (main *Package, err error) {
	for _, v := range files {
		if path.Base(v) != v {
			return noPkg, fmt.Errorf("not a base name: %s", v)
		}
	}

	c := NewBuildContext(fsys, limit)

	var errs ErrList

	defer func() {
		for _, v := range c.importTasks {
			errs = v.p.consolidateErrors(errs)
		}
		if main != nil {
			errs = main.consolidateErrors(errs)
		}
		errs = consolidateErrors(errs, c.errList)
		//TODO sort and dedup errors with same file & line, lower column wins
		err = errs.Err()
	}()

	main = c.NewPackage("", files, fsys) // main package has no import path
	return main, nil
}

type limiter chan struct{}

func newLimiter(limit int) limiter {
	if limit > 0 {
		return make(limiter, limit)
	}

	return nil
}

func (n limiter) limit() func() {
	if n == nil {
		return func() {}
	}

	n <- struct{}{}
	return func() { <-n }
}

// Package represents a single OctoGo package.
type Package struct {
	Files      []*File
	ImportPath string
	Scope      *Scope
	ctx        *BuildContext
	//TODO typeLiterals map[some-id]TypeNode
}

// NewPackage returns a newly created Package consisting of files in 'files'
// within 'fsys'.
func (c *BuildContext) NewPackage(importPath string, files []string, fsys fs.FS) (p *Package) {
	p = &Package{
		Files:      make([]*File, len(files)),
		ImportPath: importPath,
		Scope:      newScope(Universe, PackageScope),
		ctx:        c,
	}

	// Phase 1: Local Scope Population (Parallel)
	limiter := newLimiter(c.limit)
	var wg sync.WaitGroup
	for i, v := range files {
		release := limiter.limit()

		wg.Add(1)
		go func(i int, fn string) {
			defer release()
			defer wg.Done()

			p.Files[i] = p.newFile(fn, fsys)
		}(i, v)
	}
	wg.Wait()
	if c.noDeclarationChecks { // Testing support
		return p
	}

	// Phase 2: Package Scope Merging (Serial)
	for _, f := range p.Files {
		for _, spec := range f.ImportSpecs {
			c.importPkg(p.ImportPath, spec.ImportPath, spec.ImportPathToken)
		}
		// Merge file top level declarations into package scope.
		for _, nm := range slices.Sorted(maps.Keys(f.tld.Declarations)) {
			d := f.tld.Declarations[nm]
			if err := p.Scope.add(d); err != nil {
				c.syncErr(d.Token().Position(), "%v", err)
			}
		}
		f.tld.Declarations = nil
		f.Scope.Parent = p.Scope // Rewire/repair the scope hierarchy (Block->File->Package->Universe)
	}
	// Ensure "no identifier may be declared in both the file and package block".
	for _, v := range p.Files {
		for _, nm := range slices.Sorted(maps.Keys(v.Scope.Declarations)) {
			if ex := p.Scope.Declarations[nm]; ex != nil {
				d := v.Scope.Declarations[nm]
				c.err(ex.Token().Position(), "cannot declare %v both in package and file scope (%v:)", nm, d.Token().Position())
			}
		}
	}

	// Phase 3: Top-Level Type & Constant Evaluation (Serial)
	for _, v := range p.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.sourceFile(p.Scope, n)
			}
		}
	}

	// Phase 4: Body Checking & Hardware Constraints (Parallel)
	//TODO

	// Phase 5: Deep Initialization Cycle Detection (Serial)
	//TODO
	return p
}

func (p *Package) consolidateErrors(use ErrList) (e ErrList) {
	e = use
	for _, v := range p.Files {
		e = v.consolidateErrors(e)
	}
	return e
}

func (p *Package) importPkg(importPathToken Token, importPath string) (r *Package) {
	if p != nil && p.ctx != nil {
		return p.ctx.importPkg(p.ImportPath, importPath, importPathToken)
	}

	return noPkg
}
