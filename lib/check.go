// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"fmt"
	"go/token"
	"os"
	"strings"
	"sync"

	"go/constant"
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

// lastIndex recursively traverses the flat AST slice to find the last token index.
// It returns -1 if no token is found.
func lastIndex(ast []int32) (last int32) {
	last = -1

	for child := range iterator(ast) {
		if child.sym == 0 {
			// It's a terminal; update our last seen token index
			last = child.tok
		} else {
			// It's a non-terminal; recursively search its children
			if l := lastIndex(child.ast); l != -1 {
				last = l
			}
		}
	}

	return last
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
	Scope *Scope
}

// Cross-package limiter may deadlock.
func newPackage(limit int, files []string, overlay map[string][]byte) (r *Package) {
	r = &Package{
		Files: make([]*File, len(files)),
	}
	limiter := newLimiter(limit)
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
	//TODO check file scope collisions now.
	//TODO merge files .tld into package scope.
	return r
}

// File represents a single OctoGo source file.
type File struct {
	AST         []int32
	Err         error
	Filename    string
	ImportSpecs []*ImportSpecNode
	FileScope   *Scope
	parser      Parser
	tld         *Scope // Later merged into package scope
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

func newFile(fn string, overlay map[string][]byte) (r *File, err error) {
	r = &File{
		Filename:  fn,
		FileScope: newScope(nil, FileScope),
		tld:       newScope(Universe, PackageScope),
	}
	b, ok := overlay[fn]
	if !ok {
		if b, err = os.ReadFile(fn); err != nil {
			r.Err = err
			return r, err
		}
	}

	r.AST, r.Err = r.parser.Parse(fn, b)
	if r.Err = r.parser.sc.Err(); r.Err != nil {
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

	r.Err = r.parser.sc.Err()
	return r, r.Err
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

func (f *File) sourceFile(n Node) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case ImportDecl:
			f.ImportSpecs = append(f.ImportSpecs, f.importDecl(n)...)
		case TopLevelDecl:
			f.topLevelDecl(n)
		case 0:
			switch f.ch(n.tok) {
			case TOK_003b, TOK_EOF: // ';' EOF
				// ok
			default:
				panic(todo("", f.parser.Token(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
}

func (f *File) topLevelDecl(n Node) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case ConstDecl:
			f.constDecl(f.tld, n)
		case VarDecl:
			f.varDecl(f.tld, n)
		case FuncDecl:
			f.funcDecl(f.tld, n)
		case 0:
			switch f.ch(n.tok) {
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
}

// FuncDeclNode describes the FuncDecl production.
type FuncDeclNode struct {
	Name          Token
	ParameterList *ParameterListNode
	Type          *TypeNode
	ReturnList    *ParameterListNode
}

func (f *File) funcDecl(s *Scope, n Node) (r *FuncDeclNode) {
	r = &FuncDeclNode{}
	bs := f.tld.child()
	seenRPar := false
	for n := range iterator(n.ast) {
		switch n.sym {
		case ParameterList:
			switch {
			case seenRPar:
				r.ReturnList = f.parameterList(bs, n)
			default:
				r.ParameterList = f.parameterList(bs, n)
			}
			//TODO declare in bs
		case Block:
			f.block(bs, n)
			//TODO use BlockNode
		case Type:
			r.Type = f.typ(s, n)
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case identifier:
				r.Name = tok
				//TODO declare in s
			case TOK_func, TOK_0028: // "func", '('
				// ok
			case TOK_0029: // ')'
				seenRPar = true
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// BlockNode describes the Block production.
type BlockNode struct {
	List []StatementNode
}

func (f *File) block(s *Scope, n Node) (r *BlockNode) {
	r = &BlockNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Statement:
			r.List = append(r.List, f.statement(s, n))
		case 0:
			switch f.ch(n.tok) {
			case TOK_007b, TOK_007d, TOK_003b: // '{', '}', ';'
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

// StatementNode describes any Statement production.
type StatementNode any

func (f *File) statement(s *Scope, n Node) (r StatementNode) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case VarDecl:
			f.varDecl(s, n)
		case 0:
			switch f.ch(n.tok) {
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// ParameterListNode describes the ParameterList production.
type ParameterListNode struct {
	List []struct {
		Names []Token
		Type  *TypeNode
	}
}

func (f *File) parameterList(s *Scope, n Node) (r *ParameterListNode) {
	r = &ParameterListNode{}
	var item struct {
		Names []Token
		Type  *TypeNode
	}
	for n := range iterator(n.ast) {
		switch n.sym {
		case IdentifierList:
			item.Names = f.identifierList(s, n)
		case Type:
			item.Type = f.typ(s, n)
			r.List = append(r.List, item)
		case 0:
			switch f.ch(n.tok) {
			case TOK_002c: // ','
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

func (f *File) varDecl(s *Scope, n Node) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case VarSpec:
			names, vs := f.varSpec(s, n)
			var valid int32
			if s.Kind != PackageScope {
				valid = lastIndex(n.ast) + 1
			}
			for _, nm := range names {
				if err := s.add(&VarDeclaration{declaration: declaration{name: nm, valid: valid}, VarSpec: vs}); err != nil {
					f.err(vs.Name.Position(), "%v", err)
				}
			}
		case 0:
			switch f.ch(n.tok) {
			case TOK_var, TOK_0028, TOK_003b, TOK_0029: // "var", '(', ';', ')'
				// ok
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
}

// VarSpecNode describes the VarSpec production.
type VarSpecNode struct {
	Expression ExpressionNode
	Name       Token
	TypeNode   *TypeNode
}

func (f *File) varSpec(s *Scope, n Node) (names []Token, r *VarSpecNode) {
	r = &VarSpecNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case IdentifierList:
			names = f.identifierList(s, n)
		case Type:
			r.TypeNode = f.typ(s, n)
		case Expression:
			r.Expression = f.expression(s, n)
		case 0:
			switch f.ch(n.tok) {
			case TOK_003d: // '='
				// ok
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return names, r
}

// TypeNode describes the Type production.
type TypeNode struct {
	Qualifier  Token // Valid if Qualifier.IsValid()
	Name       Token
	Kind       Symbol // TOK_chan, TOK_005b('['), ...
	TypeNode   *TypeNode
	Expression ExpressionNode // [expr]T
}

func (f *File) typ(s *Scope, n Node) (r *TypeNode) {
	r = &TypeNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Type:
			r.TypeNode = f.typ(s, n)
		case Expression:
			r.Expression = f.expression(s, n)
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case identifier:
				switch {
				case r.Name.IsValid():
					r.Qualifier = r.Name
					r.Name = tok
				default:
					r.Name = tok
				}
			case TOK_chan, TOK_005b, TOK_005d: // "chan", '[', ']'
				r.Kind = Symbol(tok.Ch)
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

func (f *File) identifierList(s *Scope, n Node) (r []Token) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case identifier:
				r = append(r, tok)
			case TOK_002c: // ','
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

func (f *File) constDecl(s *Scope, n Node) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case ConstSpec:
			cs := f.constSpec(s, n)
			var valid int32
			if s.Kind != PackageScope {
				panic(todo(""))
			}
			if err := s.add(&ConstDeclaration{declaration: declaration{name: cs.Name, valid: valid}, ConstSpec: cs}); err != nil {
				f.err(cs.Name.Position(), "%v", err)
			}
		case 0:
			switch f.ch(n.tok) {
			case TOK_const /* , TOK_0028, TOK_0029, TOK_003b */ : // "const" '(' ')' ';'
				// ok
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
}

// ConstSpecNode describes the ConstSpec production.
type ConstSpecNode struct {
	Expression ExpressionNode
	Name       Token
	//TODO Type Typ
}

func (f *File) constSpec(s *Scope, n Node) (r *ConstSpecNode) {
	r = &ConstSpecNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			r.Expression = f.expression(s, n)
		case 0:
			switch f.ch(n.tok) {
			case identifier:
				r.Name = f.tok(n.tok)
			case TOK_003d: // '='
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

// ExpressionNode represents any Expression production.
type ExpressionNode any

// BinaryExpression represents a binary operation.
type BinaryExpression struct {
	LHS ExpressionNode
	Op  Symbol
	RHS ExpressionNode
}

func (f *File) expression(s *Scope, n Node) (r ExpressionNode) {
	var relOp Symbol
	for n := range iterator(n.ast) {
		switch n.sym {
		case SimpleExpr:
			switch e := f.simpleExpr(s, n); {
			case r == nil:
				r = e
			default:
				r = &BinaryExpression{LHS: r, Op: relOp, RHS: e}
			}
		case 0:
			switch f.ch(n.tok) {
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

func (f *File) simpleExpr(s *Scope, n Node) (r ExpressionNode) {
	var addOp Symbol
	for n := range iterator(n.ast) {
		switch n.sym {
		case Term:
			switch e := f.term(s, n); {
			case r == nil:
				r = e
			default:
				r = &BinaryExpression{LHS: r, Op: addOp, RHS: e}
			}
		case 0:
			switch f.ch(n.tok) {
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

func (f *File) term(s *Scope, n Node) (r ExpressionNode) {
	var mulOp Symbol
	for n := range iterator(n.ast) {
		switch n.sym {
		case UnaryExpr:
			switch e := f.unaryExpr(s, n); {
			case r == nil:
				r = e
			default:
				r = &BinaryExpression{LHS: r, Op: mulOp, RHS: e}
			}
		case 0:
			switch f.ch(n.tok) {
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

//TODO // UnaryExprNode describes the UnaryExpr production.
//TODO type UnaryExprNode struct {
//TODO 	UnaryOp Symbol
//TODO 	UnaryOp2 []Symbol
//TODO 	Factor ExpressionNode
//TODO }

func (f *File) unaryExpr(s *Scope, n Node) (r ExpressionNode) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case Factor:
			fa := f.factor(s, n)
			switch {
			case r == nil:
				r = fa
			default:
				panic(todo(""))
			}
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// Identifier describes a named Factor.
type Identifier struct {
	Scope *Scope // Appears in scope.
	Name  Token
	Index int32
}

func (f *File) factor(s *Scope, n Node) (r ExpressionNode) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case int_lit:
				if r = constant.MakeFromLiteral(tok.Src(), token.INT, 0); r == constant.Unknown {
					f.err(tok.Position(), "invalid integer literal: %s", tok.Src())
				}
				return r
			case identifier:
				r = &Identifier{Scope: s, Name: tok, Index: n.tok}
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

func (f *File) importDecl(n Node) (r []*ImportSpecNode) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case ImportSpec:
			r = append(r, f.importSpec(n))
		case 0:
			switch f.ch(n.tok) {
			case TOK_import, TOK_0028, TOK_0029, TOK_003b: // "import" '(' ')' ';'
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

// ImportSpecNode decribes the ImportSpec production.
type ImportSpecNode struct {
	ImportQualifier string
	ImportPath      string
	IsDotImport     bool
	IsStdLib        bool
}

func (f *File) importSpec(n Node) (r *ImportSpecNode) {
	r = &ImportSpecNode{}
	var nm Token
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch f.ch(n.tok) {
			case TOK_002e: // '.'
				r.IsDotImport = true
			case identifier:
				nm = f.tok(n.tok)
				r.ImportQualifier = nm.Src()
			case string_lit:
				nm = f.tok(n.tok)
				r.ImportPath = nm.Src()
				if !r.IsDotImport && r.ImportQualifier == "" {
					if x := strings.LastIndexByte(r.ImportPath, '/'); x > 0 {
						if base := r.ImportPath[x:]; token.IsIdentifier(base) {
							r.ImportQualifier = base
						} else {
							f.err(nm.Position(), "invalid package name: %s", r.ImportPath)
						}
					}
				}

				x := strings.IndexByte(r.ImportPath, '/')
				if x < 0 {
					x = len(r.ImportPath)
				}
				if x >= 0 {
					first := r.ImportPath[:x]
					if len(first) != 0 && !strings.ContainsRune(first, '.') {
						if r.ImportQualifier == "" {
							r.ImportQualifier = first
						}
						r.IsStdLib = true
					}
				}
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	if r.ImportQualifier != "" {
		if err := f.FileScope.add(&ImportQualifier{declaration: declaration{name: nm}, Import: r}); err != nil {
			f.err(nm.Position(), "%v", err)
		}
	}
	return r
}
