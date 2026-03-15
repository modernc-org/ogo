// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"fmt"
	"io/fs"
	"path"
	"sync"
)

var (
	noPkg = &Package{Scope: newScope(Universe, PackageScope)}
)

// BuildContext coordinates creating a package tree.
type BuildContext struct {
	importsMu sync.Mutex

	fsys    fs.FS
	imports map[string]*Package // import path: package
	limit   int
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

// Package represents a single OctoGo package.
type Package struct {
	Err        error
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
		ctx:   bc,
	}
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
	var errList ErrList
	for _, v := range r.Files {
		consolidateErrors(errList, v.Err)
	}
	if r.Err = errList.Err(); r.Err != nil {
		return r
	}

	//TODO check file scope collisions now.
	//TODO merge files .tld into package scope.
	return r
}

func (p *Package) importPkg(importPath string) (r *Package) {
	if p != nil && p.ctx != nil {
		return p.ctx.importPkg(importPath)
	}

	return noPkg
}

// Build builds the main pacakge consisting of files in 'files' within 'fsys'.
// 'limit' is the maximum desired concurrency for individual package building
// when > 0.
//
// 'files' must be base names within fsys. Build resolves and import paths
// a/b/c as paths a/b/c within fsys.
func Build(limit int, files []string, fsys fs.FS) (err error) {
	for _, v := range files {
		if path.Base(v) != v {
			return fmt.Errorf("not a base name: %s", v)
		}
	}

	bc := NewBuildContext(fsys, limit)
	main := bc.NewPackage(files, fsys)
	panic(todo("", main != nil))
}
