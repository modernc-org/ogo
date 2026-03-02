package octogo // import "octogo.dev/octogo/lib"

import (
	"os"
	"sync"
)

type ctx struct {
	limiter chan struct{}
	files   []*file
}

func newCtx(limit int) (r *ctx) {
	r = &ctx{}
	if limit > 0 {
		r.limiter = make(chan struct{}, limit)
	}
	return r
}

func (c *ctx) limit() func() {
	if c.limiter == nil {
		return func() {}
	}

	c.limiter <- struct{}{}
	return func() { <-c.limiter }
}

type file struct {
	ast    []int32
	fn     string
	parser Parser
	err    error
}

func newFile(fn string) (r *file, err error) {
	r = &file{fn: fn}
	b, err := os.ReadFile(fn)
	if err != nil {
		r.err = err
		return r, err
	}

	r.ast, r.err = r.parser.Parse(fn, b)
	return r, r.err
}

func check(c *ctx, files []string) {
	var wg sync.WaitGroup
	c.files = make([]*file, len(files))
	for i, v := range files {
		func() {
			defer c.limit()

			wg.Add(1)

			go func(i int, fn string) {
				defer wg.Done()

				c.files[i], _ = newFile(fn)
			}(i, v)
		}()
	}
	panic(todo(""))
}
