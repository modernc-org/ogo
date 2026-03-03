package octogo // import "octogo.dev/octogo/lib"

import (
	"fmt"
	"os"
	"sync"
	"strings"
)

// Node represents a parse tree.
type Node struct {
	ast []int32 // Valid if .sym != 0
	sym Symbol  // Valid if != 0
	tok int32   // Valid if sym == 0
}

func iterator(ast []int32) func(yield func(Node) bool) {
	return func(yield func(Node) bool) {
		for len(ast) != 0 {
			switch v := ast[0]; {
			case v < 0:
				// Non-Terminal: [-SymbolID, Size, Children...]
				n := ast[1]
				if !yield(Node{ast: ast[2 : 2+n], sym: Symbol(-v)}) {
					return
				}

				ast = ast[2+n:] // Advance past the node
			default:
				// Terminal: Token Index
				if !yield(Node{tok: v}) {
					return
				}

				ast = ast[1:] // Advance past the token
			}
		}
	}
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
	Files []*File
}

func newPackage(limiter limiter, files []string, overlay map[string][]byte) (r *Package) {
	r = &Package{
		Files: make([]*File, len(files)),
	}
	var wg sync.WaitGroup
	for i, v := range files {
		func() {
			defer limiter.limit()

			wg.Add(1)

			go func(i int, fn string) {
				defer wg.Done()

				r.Files[i], _ = newFile(fn, overlay)
			}(i, v)
		}()
	}
	wg.Wait()
	return r
}

// Scope registers name-Node bindings.
type Scope struct {
	Parent *Scope
	Nodes  map[string]interface{}
}

// File represents a single OctoGo source file.
type File struct {
	AST      []int32
	Err      error
	Filename string
	parser   Parser
}

func newFile(fn string, overlay map[string][]byte) (r *File, err error) {
	r = &File{Filename: fn}
	b, ok := overlay[fn]
	if !ok {
		if b, err = os.ReadFile(fn); err != nil {
			r.Err = err
			return r, err
		}
	}

	if r.AST, r.Err = r.parser.Parse(fn, b); r.Err != nil {
		return r, r.Err
	}

	walk[Symbol](&r.parser, r.AST, 0)
	for n := range iterator(r.AST) {
		switch n.sym {
		case SourceFile:
			r.sourceFile(n)
		default:
			panic(todo("", n.sym))
		}
	}

	return r, r.Err
}

func (f *File) sourceFile(n Node) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case PackageClause:
			f.packageClause(n)
		case 0:
			trc("", f.parser.Token(n.tok))
		default:
			panic(todo("", n.sym))
		}
	}
}

func (f *File) packageClause(n Node) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			trc("", f.parser.Token(n.tok))
		default:
			panic(todo("", n.sym))
		}
	}
}

func walk[S ~int32](p interface{ Token(int32) Token }, ast []int32, lvl int) {
	for len(ast) != 0 {
		next := int32(1)
		switch n := ast[0]; {
		case n < 0:
			fmt.Printf("%s%v\n", strings.Repeat("· ", lvl), S(-n))
			next = 2 + ast[1]
			walk[S](p, ast[2:next], lvl+1)
		default:
			tok := p.Token(n)
			fmt.Printf("%s%s [%v]\n", strings.Repeat("· ", lvl), tok, S(tok.Ch))
		}
		ast = ast[next:]
	}
}
