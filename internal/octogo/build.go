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
	"sync"
)

var (
	noPkg = &Package{Scope: newScope(Universe, PackageScope)}
)

// BuildContext coordinates creating a package tree.
type BuildContext struct {
	importsMu sync.Mutex

	errList ErrList
	fsys    fs.FS
	imports map[string]*Package // import path: package
	limit   int

	noDeclarationChecks bool
}

// NewBuildContext returns a newly created BuildContext. 'limit' is the maximum
// desired concurrency for individual package building when > 0.
func NewBuildContext(fsys fs.FS, limit int) (r *BuildContext) {
	return &BuildContext{
		fsys:    fsys,
		imports: map[string]*Package{},
		limit:   limit,
	}
}

func (bc *BuildContext) importPkg(importPath string) (r *Package) {
	if bc == nil {
		return noPkg
	}

	bc.importsMu.Lock()

	defer bc.importsMu.Unlock()

	if r, ok := bc.imports[importPath]; ok {
		return r
	}

	des, err := fs.ReadDir(bc.fsys, importPath)
	if err != nil {
		r = noPkg
		bc.imports[importPath] = r
		return
	}

	panic(todo("", importPath, des))
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
	main = bc.NewPackage(files, fsys)
	return main, nil
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
func (bc *BuildContext) NewPackage(files []string, fsys fs.FS) (r *Package) {
	r = &Package{
		Files: make([]*File, len(files)),
		Scope: newScope(Universe, PackageScope),
		ctx:   bc,
	}

	defer func() {
	}()

	limiter := newLimiter(bc.limit)
	var wg sync.WaitGroup
	for i, v := range files {
		func() {
			defer limiter.limit()

			wg.Add(1)

			go func(i int, fn string) {
				defer wg.Done()

				r.Files[i] = r.newFile(fn, fsys)
			}(i, v)
		}()
	}
	wg.Wait()
	for _, v := range r.Files {
		consolidateErrors(bc.errList, v.errList)
	}
	if bc.noDeclarationChecks { // Testing support
		return r
	}

	for _, v := range r.Files {
		for _, v := range v.ImportSpecs {
			bc.importPkg(v.ImportPath)
		}
		for _, nm := range slices.Sorted(maps.Keys(v.tld.Nodes)) {
			r.Scope.add(v.tld.Nodes[nm])
		}
	}
	for _, v := range r.Files {
		for _, nm := range slices.Sorted(maps.Keys(v.Scope.Nodes)) {
			if ex := r.Scope.Nodes[nm]; ex != nil {
				panic(todo(""))
			}
		}
	}
	for _, v := range r.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.sourceFile(n)
			}
		}
	}
	//TODO type check functions and methods
	return r
}

func (p *Package) importPkg(importPath string) (r *Package) {
	if p != nil && p.ctx != nil {
		return p.ctx.importPkg(importPath)
	}

	return noPkg
}
