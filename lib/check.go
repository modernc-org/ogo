package octogo // import "octogo.dev/octogo/lib"

import (
	"bytes"
	"fmt"
	"go/token"
	"os"
	"strings"
	"sync"
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

// Bind is a (node AST index, visibility AST index) pair.
type Bind struct {
	NodeX    int32
	VisibleX int32
}

// ScopeKind describes the type of a Scope.
type ScopeKind int

// ScopeKind values.
const (
	UniverseScope ScopeKind = iota
	FileScope
	PackageScope
	BlockScope
)

// Scope registers name-Node bindings.
type Scope struct {
	Parent *Scope
	Nodes  map[string]Bind
}

// File represents a single OctoGo source file.
type File struct {
	AST         []int32
	Err         error
	Filename    string
	parser      Parser
	ImportSpecs []*ImportSpecNode
}

func (f *File) tok(x int32) (r Token) {
	return f.parser.Token(x)
}

func (f *File) ch(x int32) (r Symbol) {
	return Symbol(f.parser.Token(x).Ch)
}

func (f *File) err(pos token.Position, s string, args ...any) {
	f.parser.sc.AddErr(pos, s, args...)
}

//lint:ignore U1000 debug helper
func (f *File) walk(ast []int32, lvl int) {
	for len(ast) != 0 {
		next := int32(1)
		switch n := ast[0]; {
		case n < 0:
			fmt.Printf("%s%v\n", strings.Repeat("· ", lvl), Symbol(-n))
			next = 2 + ast[1]
			f.walk(ast[2:next], lvl+1)
		default:
			tok := f.parser.Token(n)
			fmt.Printf("%s%s [%v]\n", strings.Repeat("· ", lvl), tok, Symbol(tok.Ch))
		}
		ast = ast[next:]
	}
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

	if tok := r.parser.tok; tok.Ch != rune(TOK_EOF) {
		r.Err = fmt.Errorf("%v: unexpected %v %q", tok.Position(), Symbol(tok.Ch), tok.Src())
		return r, r.Err
	}

	for n := range iterator(r.AST) {
		switch n.sym {
		case SourceFile:
			r.sourceFile(n)
		default:
			panic(todo("", n.sym, n.tok))
		}
	}

	return r, r.Err
}

func (f *File) sourceFile(n Node) {
	//TODO- f.walk(n.ast, 0)
	for n := range iterator(n.ast) {
		switch n.sym {
		case ImportDecl:
			f.ImportSpecs = append(f.ImportSpecs, f.importDecl(n)...)
		//TODO case ConstDecl:
		//TODO 	f.constDecl(n)
		case 0:
			switch f.ch(n.tok) {
			case TOK_003b:
				// ok
			default:
				panic(todo("", f.parser.Token(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
}

func (f *File) constDecl(n Node) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case ConstSpec:
			f.constSpec(n)
		case 0:
			switch f.ch(n.tok) {
			case TOK_const, TOK_0028, TOK_0029, TOK_003b:
				// ok
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
}

func (f *File) constSpec(n Node) {
	for n := range iterator(n.ast) {
		switch n.sym {
		//TODO case Type:
		//TODO 	// TODO: f.parseType(n)
		//TODO case Expression:
		//TODO 	// TODO: f.expression(n)
		case 0:
			switch f.ch(n.tok) {
			// case identifier:
			// 	// TODO: Bind the identifier to the current scope
			// case TOK_003d: // '='
			// 	// ok
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
}

func (f *File) importDecl(n Node) (r []*ImportSpecNode) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case ImportSpec:
			r = append(r, f.importSpec(n))
		case 0:
			switch f.ch(n.tok) {
			case TOK_import:
				// ok
			case TOK_0028: // '('
				// ok
			case TOK_0029: // ')'
				// ok
			case TOK_003b: // ';'
				// ok
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// ImportSpecNode decribes an import specification.
type ImportSpecNode struct {
	ImplicitQualifier string
	ImportQualifier   int32 // identifier index
	ImportPath        int32 // string_lit index
	IsDotImport       bool
	IsStdLib          bool
}

func (f *File) importSpec(n Node) (r *ImportSpecNode) {
	r = &ImportSpecNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch f.ch(n.tok) {
			case TOK_002e: // '.'
				r.IsDotImport = true
			case identifier:
				r.ImportQualifier = n.tok
			case string_lit:
				r.ImportPath = n.tok
				ip := f.tok(n.tok).SrcBytes()
				if r.ImportQualifier == 0 {
					if x := bytes.LastIndexByte(ip, '/'); x > 0 {
						if base := string(ip[x:]); token.IsIdentifier(base) {
							r.ImplicitQualifier = base
						}
					}
				}
				if x := bytes.IndexByte(ip, '/'); x >= 0 {
					ip = ip[:x]
					if len(ip) != 0 && !bytes.ContainsRune(ip, '.') {
						r.IsStdLib = true
					}
				}
				if r.ImportQualifier == 0 && r.ImplicitQualifier == "" {
					tok := f.tok(n.tok)
					f.err(tok.Position(), "invalid package name: %s", tok.SrcBytes())
				}
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}
