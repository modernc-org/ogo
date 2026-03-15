// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"io/fs"
	"sync"
)

// BuildContext coordinates creating a package tree.
type BuildContext struct {
	limit int
}

// NewBuildContext returns a newly created BuildContext. 'limit' is the maximum
// desired concurrency for individual package building when > 0.
func NewBuildContext(limit int) (r *BuildContext) {
	return &BuildContext{
		limit: limit,
	}
}

// Package represents a single OctoGo package.
type Package struct {
	Files []*File
	Scope *Scope
	ctx   *BuildContext
}

// NewPackage returns a newly created Package consisting of files in 'files'
// within 'fsys'.
func (c *BuildContext) NewPackage(files []string, fsys fs.FS) (r *Package) {
	r = &Package{
		Files: make([]*File, len(files)),
		ctx:   c,
	}
	limiter := newLimiter(c.limit)
	var wg sync.WaitGroup
	for i, v := range files {
		func() {
			defer limiter.limit()

			wg.Add(1)

			go func(i int, fn string) {
				defer wg.Done()

				r.Files[i] = newFile(fn, fsys)
			}(i, v)
		}()
	}
	wg.Wait()
	//TODO check Files for .Err and hasInvalidImports
	//TODO check file scope collisions now.
	//TODO merge files .tld into package scope.
	return r
}

// Build builds the main pacakge consisting of files in 'files' within 'fsys'.
// 'limit' is the maximum desired concurrency for individual package building
// when > 0.
func Build(limit int, files []string, fsys fs.FS) (err error) {
	bc := NewBuildContext(limit)
	main := bc.NewPackage(files, fsys)
	panic(todo("", main != nil))
}
