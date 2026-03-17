// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"fmt"
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
func NewBuildContext(fsys fs.FS, limit int) (r *BuildContext) {
	return &BuildContext{
		fsys:        fsys,
		importTasks: map[string]*importTask{},
		importGraph: map[string]map[string]bool{},
		limit:       limit,
	}
}

// findCycle performs a DFS to see if 'target' is reachable from 'current'.
// It returns the path of the cycle if one exists.
func (bc *BuildContext) findCycle(current, target string, visited map[string]bool) []string {
	if current == target {
		return []string{current}
	}
	visited[current] = true

	for next := range bc.importGraph[current] {
		if !visited[next] {
			if cycle := bc.findCycle(next, target, visited); cycle != nil {
				return append([]string{current}, cycle...)
			}
		}
	}
	return nil
}

func (bc *BuildContext) importPkg(fromPath, importPath string) (r *Package) {
	//TODO cycle detect
	if bc == nil {
		return noPkg
	}

	bc.importsMu.Lock()

	if bc.importGraph[fromPath] == nil {
		bc.importGraph[fromPath] = make(map[string]bool)
	}
	bc.importGraph[fromPath][importPath] = true

	if cycle := bc.findCycle(importPath, fromPath, make(map[string]bool)); cycle != nil {
		bc.importsMu.Unlock()

		// To complete the visual circle for the error message, put 'fromPath' at the front.
		fullCycle := append([]string{fromPath}, cycle...)

		bc.errMu.Lock()
		panic(todo("", fullCycle))
		//TODO bc.errList = append(bc.errList, fmt.Errorf("import cycle not allowed: %s", strings.Join(fullCycle, " -> ")))
		bc.errMu.Unlock()

		return noPkg
	}

	task := bc.importTasks[importPath]
	if task == nil {
		task = &importTask{}
		bc.importTasks[importPath] = task
	}

	bc.importsMu.Unlock()

	task.Lock()

	if task.ready == nil {
		task.ready = make(chan struct{})
		go func() {
			defer close(task.ready)

			dirEntries, err := fs.ReadDir(bc.fsys, importPath)
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

			task.p = bc.NewPackage(importPath, files, bc.fsys)
		}()
	}

	task.Unlock()
	<-task.ready
	return task.p
}

func consolidateErrors(use ErrList, errors ...error) (r ErrList) {
	r = use
	for _, v := range errors {
		switch x := v.(type) {
		case nil:
			// nop
		case ErrList:
			r = append(r, x...)
		default:
			r = append(r, ErrWithPosition{Err: x})
		}
	}
	return r
}

// Build builds the main pacakge consisting of files in 'files' within 'fsys'.
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

	bc := NewBuildContext(fsys, limit)
	main = bc.NewPackage("", files, fsys) // main package has no import path
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
}

// NewPackage returns a newly created Package consisting of files in 'files'
// within 'fsys'.
func (bc *BuildContext) NewPackage(importPath string, files []string, fsys fs.FS) (r *Package) {
	r = &Package{
		Files:      make([]*File, len(files)),
		ImportPath: importPath,
		Scope:      newScope(Universe, PackageScope),
		ctx:        bc,
	}

	defer func() {
	}()

	limiter := newLimiter(bc.limit)
	var wg sync.WaitGroup
	for i, v := range files {
		release := limiter.limit()

		wg.Add(1)
		go func(i int, fn string) {
			defer release()
			defer wg.Done()

			r.Files[i] = r.newFile(fn, fsys)
		}(i, v)
	}
	wg.Wait()
	for _, v := range r.Files {
		bc.errMu.Lock()
		bc.errList = consolidateErrors(bc.errList, v.errList)
		bc.errMu.Unlock()
	}
	if bc.noDeclarationChecks { // Testing support
		return r
	}

	for _, v := range r.Files {
		for _, spec := range v.ImportSpecs {
			bc.importPkg(r.ImportPath, spec.ImportPath)
		}
		// Merge file top level declarations into package scope.
		for _, nm := range slices.Sorted(maps.Keys(v.tld.Nodes)) {
			d := v.tld.Nodes[nm]
			if err := r.Scope.add(d); err != nil {
				bc.errMu.Lock()
				bc.errList.AddErr(d.Name().Position(), "%v", err)
				bc.errMu.Unlock()
			}
		}
	}
	// Check for ... no identifier may be declared in both the file and package block
	for _, v := range r.Files {
		for _, nm := range slices.Sorted(maps.Keys(v.Scope.Nodes)) {
			if ex := r.Scope.Nodes[nm]; ex != nil {
				bc.errMu.Lock()
				d := v.Scope.Nodes[nm]
				bc.errList.AddErr(ex.Name().Position(), "cannot declare %v both in package and file scope (%v:)", nm, d.Name().Position())
				bc.errMu.Unlock()
			}
		}
	}
	// Type check top level declarations.
	for _, v := range r.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.sourceFile(n)
			}
		}
	}
	//TODO Type check function and method bodies
	return r
}

func (p *Package) importPkg(importPath string) (r *Package) {
	if p != nil && p.ctx != nil {
		return p.ctx.importPkg(p.ImportPath, importPath)
	}

	return noPkg
}
