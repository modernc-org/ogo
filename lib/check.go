package octogo // import "octogo.dev/octogo/lib"

import (
	"os"
	"sync"
)

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

type file struct {
	ast    []int32
	fn     string
	parser Parser
	err    error
}

func newFile(fn string, overlay map[string][]byte) (r *file, err error) {
	r = &file{fn: fn}
	b, ok := overlay[fn]
	if !ok {
		if b, err = os.ReadFile(fn); err != nil {
			r.err = err
			return r, err
		}
	}

	r.ast, r.err = r.parser.Parse(fn, b)
	return r, r.err
}

type pkg struct {
	files []*file
}

func newPackage(limiter limiter, files []string, overlay map[string][]byte) (r *pkg) {
	r = &pkg{
		files: make([]*file, len(files)),
	}
	var wg sync.WaitGroup
	for i, v := range files {
		func() {
			defer limiter.limit()

			wg.Add(1)

			go func(i int, fn string) {
				defer wg.Done()

				r.files[i], _ = newFile(fn, overlay)
			}(i, v)
		}()
	}
	wg.Wait()
	return r
}

type node struct {
	ast []int32 // Valid if .sym != 0
	sym Symbol  // Valid if != 0
	tok int32   // Valid if sym == 0
}

func iterator(ast []int32) func(yield func(node) bool) {
	return func(yield func(node) bool) {
		for len(ast) != 0 {
			switch v := ast[0]; {
			case v < 0:
				// Non-Terminal: [-SymbolID, Size, Children...]
				n := ast[1]
				if !yield(node{ast: ast[2 : 2+n], sym: Symbol(-v)}) {
					return
				}

				ast = ast[2+n:] // Advance past the node
			default:
				// Terminal: Token Index
				if !yield(node{tok: v}) {
					return
				}

				ast = ast[1:] // Advance past the token
			}
		}
	}
}
