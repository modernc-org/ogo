// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"bytes"
	"fmt"
	"go/constant"
	"go/token"
	"io/fs"
	"iter"
	"strconv"
	"strings"
)

var (
	_ TypeNode = (*FunctionType)(nil)
	_ TypeNode = (*TypeNodeArray)(nil)
	_ TypeNode = (*TypeNodeChan)(nil)
	_ TypeNode = (*TypeNodeIdent)(nil)
	_ TypeNode = (*TypeNodeSlice)(nil)
)

var (
	noPos    token.Position
	initName = []byte("init")
)

const (
	unvisited gate = iota // Call open and enter
	resolving             // Cycle detected, report and return
	resolved              // All is said and done, return
)

type gate int8

func (g *gate) state() (r gate) {
	return *g
}

func (g *gate) open() {
	*g = resolving
}

func (g *gate) close() {
	*g = resolved
}

type gater interface {
	state() (r gate)
	open()
	close()
}

// // bitVector is a dynamically growing slice of bits.
// // The zero value (nil slice) is ready to use.
// type bitVector []uint
//
// // set sets the bit at index n to 1.
// // It uses a pointer receiver so it can modify the slice header when growing.
// func (b *bitVector) set(n int32) {
// 	if n < 0 {
// 		return // Or panic, depending on how you want to handle invalid inputs
// 	}
//
// 	word := int(n) / bits.UintSize
// 	bit := int(n) % bits.UintSize
//
// 	// Grow the slice automatically if the index is out of bounds.
// 	// append() natively handles amortized capacity growth.
// 	for word >= len(*b) {
// 		*b = append(*b, 0)
// 	}
//
// 	(*b)[word] |= 1 << bit
// }
//
// // has returns true if the bit at index n is 1.
// // A pointer receiver isn't strictly necessary here, but keeps the method set consistent.
// func (b *bitVector) has(n int32) bool {
// 	if n < 0 {
// 		return false
// 	}
//
// 	word := int(n) / bits.UintSize
// 	bit := int(n) % bits.UintSize
//
// 	// If the required word is beyond our current length, it hasn't been set.
// 	if word >= len(*b) {
// 		return false
// 	}
//
// 	return ((*b)[word] & (1 << bit)) != 0
// }

// Node represents a parse tree within the flat []int32 raw AST.
type Node struct {
	ast []int32 // Valid if .sym != 0
	sym Symbol  // Valid if != 0
	tok int32   // Valid if sym == 0
}

// Pos returns the index of the first token within the node.
// It returns -1 if no token is found.
func (n Node) Pos() int32 {
	if n.sym == 0 {
		// Terminal node: the position is simply the token index
		return n.tok
	}
	// Non-terminal node: recursively find the first token in the AST slice
	return firstIndex(n.ast)
}

// End returns the index of the last token within the node.
// It returns -1 if no token is found.
func (n Node) End() int32 {
	if n.sym == 0 {
		// Terminal node: the end position is the token index itself
		return n.tok
	}
	// Non-terminal node: recursively find the last token in the AST slice
	return lastIndex(n.ast)
}

func it(ast []int32) iter.Seq[Node] {
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

	for child := range it(ast) {
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

// firstIndex recursively traverses the flat AST slice to find the first token index.
// It returns -1 if no token is found.
func firstIndex(ast []int32) int32 {
	for child := range it(ast) {
		if child.sym == 0 {
			// Found the first terminal (token)
			return child.tok
		}

		// It's a non-terminal; search its children recursively
		if f := firstIndex(child.ast); f != -1 {
			return f
		}
	}

	return -1
}

// File represents a single OctoGo source file.
type File struct {
	AST               []int32
	Filename          string
	ImportSpecs       []*ImportSpecNode
	InitFuncs         []*FuncDeclNode
	Package           *Package
	Scope             *Scope // Kind: FileScope, Parent: Universe
	errList           ErrList
	hasInvalidImports bool
	inArrayBound      bool              // evaluating an array length: suppress "is not a constant"
	inCaseExpr        bool              // evaluating a switch case expression, where a non-constant operand is legal: suppress "is not a constant"
	localVars         []*VarDeclaration // local variables of the function body being checked, for the unused-variable report
	writeTargets      map[string]bool   // positions of bare "="/":=" assignment-target identifiers in the body: writes, which do not count as uses
	parser            Parser
	tld               *Scope // tld.Nodes are later moved into (*Package).Scope. Kind: PackageScope, Parent: .Scope.
}

//TODO func (f *File) pos(tokIndex int32) (r token.Position) {
//TODO 	if tokIndex >= 0 {
//TODO 		r = f.tok(tokIndex).Position()
//TODO 	}
//TODO 	return r
//TODO }

func (f *File) consolidateErrors(use ErrList) (e ErrList) {
	return consolidateErrors(use, f.errList)
}

func (f *File) tok(x int32) (r Token) {
	return f.parser.Token(x)
}

func (f *File) ch(x int32) (r Symbol) {
	return Symbol(f.parser.Token(x).Ch)
}

func (f *File) err(pos token.Position, s string, args ...any) {
	f.errList.AddErr(pos, s, args...)
}

func (p *Package) newFile(fn string, fsys fs.FS) (f *File) {
	//TODO- Scope := newScope(Universe, FileScope)
	//TODO- r = &File{
	//TODO- 	Filename: fn,
	//TODO- 	Scope:    Scope,
	//TODO- 	Package:  p,
	//TODO- 	tld:      newScope(Scope, PackageScope),
	//TODO- }
	tldScope := newScope(Universe, PackageScope)
	fileScope := newScope(tldScope, FileScope)
	f = &File{
		Filename: fn,
		Scope:    fileScope,
		Package:  p,
		tld:      tldScope,
	}
	b, err := fs.ReadFile(fsys, fn)
	if err != nil {
		f.errList.AddErr(noPos, "%v", err)
		return f
	}

	f.AST, _ = f.parser.Parse(fn, b)
	if f.errList = f.parser.sc.errList; f.errList.Err() != nil {
		return f
	}

	if tok := f.parser.tok; tok.Ch != rune(EOF) {
		f.errList.AddErr(tok.Position(), "%v: unexpected %v %q", tok.Position(), Symbol(tok.Ch), tok.Src())
		return f
	}

	for n := range it(f.AST) {
		switch n.sym {
		case SourceFile:
			f.declareSourceFile(n)
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return f
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

// SourceFile = { ImportDecl ";" } { TopLevelDecl ";" } .
func (f *File) declareSourceFile(n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case ImportDecl:
			f.ImportSpecs = append(f.ImportSpecs, f.declareImportDecl(n)...)
		case TopLevelDecl:
			f.declareTopLevel(n)
		case 0:
			switch f.ch(n.tok) {
			case SEMICOLON, EOF:
				// ok
			default:
				panic(todo("", f.parser.Token(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

func (f *File) sourceFile(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case TopLevelDecl:
			f.topLevel(s, n)
		case ImportDecl:
			// ok
		case 0:
			switch f.ch(n.tok) {
			case SEMICOLON, EOF:
				// ok
			default:
				panic(todo("", f.parser.Token(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

func (f *File) declareTopLevel(n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case ConstDecl:
			f.declareConst(f.tld, n)
		case VarDecl:
			f.declareVar(f.tld, n)
		case FuncDecl:
			f.declareFunc(n)
		case TypeDecl:
			f.declareType(f.tld, n)
		case 0:
			switch f.ch(n.tok) {
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

func (f *File) topLevel(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case ConstDecl:
			f.constDecl(s, n)
		case VarDecl:
			f.varDecl(s, n)
		case FuncDecl:
			f.funcDecl(s, n)
			f.registerMethod(s, n)
		case TypeDecl:
			// Names were bound in phase 1 (declareType); resolve the bodies now
			// that every top-level type name is visible.
			f.typeDecl(s, n)
		case 0:
			switch f.ch(n.tok) {
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

// FunctionType describes the type of a function/method.
type FunctionType struct {
	gate
	Receiver  *ReceiverNode
	Signature *SignatureNode
}

// Type implements TypeNode.
func (t *FunctionType) Type() Typ {
	panic(todo("", origin(1)))
}

// FuncDeclNode describes the FuncDecl production.
//
//	FuncDecl       = "func" [ Receiver ] identifier Signature [ Block ] .
type FuncDeclNode struct {
	gate
	Name Token
	//TODO- ParameterList *ParameterListNode
	Type *FunctionType
	//TODO- ReturnList    *ParameterListNode
	Block *BlockNode
}

func (f *File) declareFunc(n Node) (r *FuncDeclNode) {
	r = &FuncDeclNode{}
	isMethod := false
	for n := range it(n.ast) {
		switch n.sym {
		case Receiver:
			isMethod = true
		case Signature, Block:
			// ok
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				r.Name = tok
				if !isMethod {
					switch {
					case bytes.Equal(r.Name.SrcBytes(), initName):
						f.InitFuncs = append(f.InitFuncs, r)
					default:
						if err := f.tld.add(&FuncDeclaration{declaration: declaration{token: r.Name}, FuncDecl: r}); err != nil {
							f.err(r.Name.Position(), "%v", err)
						}
					}
				}
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

func (f *File) funcDecl(s *Scope, n Node) {
	// Resolve the signature in a child of the package scope: f.tld is emptied
	// once its declarations are merged (and its parent is the universe, not the
	// package), so a parameter or result of a package-level named type would
	// otherwise be reported undefined.
	block := s.child()
	// seenRPar := false

	var fd *FuncDeclNode

	defer func() {
		if fd == nil {
			return
		}

		switch fd.gate {
		case resolving:
			fd.gate.close()
		default:
			panic(todo("", fd.gate))
		}
	}()

	for n := range it(n.ast) {
		switch n.sym {
		case Signature:
			fd.Type.Signature = f.signature(block, n)
		case Receiver:
			// A method receiver. Methods are not entered into the package scope
			// (declareFunc skips them), so this funcDecl pass returns at the
			// method name below without resolving a signature; the receiver only
			// needs to not crash here. Body checking declares the receiver name.
		case Block:
			// ok
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				switch x := s.Declarations[tok.Src()].(type) {
				case nil:
					return
				case *FuncDeclaration:
					if fd = x.FuncDecl; fd == nil {
						return
					}

					switch fd.gate {
					case unvisited:
						fd.Type = &FunctionType{}
						fd.gate.open()
					default:
						// The shared FuncDeclaration for this name was already
						// visited: this is a second (duplicate) declaration of
						// the same function, already reported as a redeclaration
						// when it was added to the package scope. Clear fd so the
						// deferred gate-close leaves the first declaration alone.
						fd = nil
						return
					}
				default:
					// The name is already declared in this scope as a
					// non-function: a redeclaration. Report it and skip.
					f.err(tok.Position(), "%s redeclared in this block, previous declaration at %v", tok.Src(), x.Token().Position())
					return
				}
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

// checkBodies walks a source file's function and method bodies (Phase 4).
func (f *File) checkBodies(pkg *Scope, n Node) {
	for n := range it(n.ast) {
		if n.sym != TopLevelDecl {
			continue
		}
		for n := range it(n.ast) {
			if n.sym == FuncDecl {
				f.checkFuncBody(pkg, n)
			}
		}
	}
}

// checkFuncBody establishes the function scope and checks the body block.
// Parameters share the scope of the top-level body locals, so
// "func f(x int) { var x int }" is a redeclaration.
func (f *File) checkFuncBody(pkg *Scope, n Node) {
	fs := newScope(pkg, BlockScope)
	f.localVars = nil
	f.writeTargets = map[string]bool{}
	var results []retResult
	var body Node
	hasBody := false
	for n := range it(n.ast) {
		switch n.sym {
		case Receiver:
			f.declareReceiver(fs, n)
		case Signature:
			sig := f.signature(fs, n)
			f.declareParamList(fs, sig.Params)
			f.declareParamList(fs, sig.Results)
			results = f.flattenResults(fs, sig)
		case Block:
			f.checkBlock(fs, results, n)
			body, hasBody = n, true
		}
	}
	if !hasBody {
		return
	}
	// A function that declares one or more results must not be able to reach the
	// end of its body without returning a value: the body must end in a
	// terminating statement, otherwise control falls off the end -- "missing
	// return", reported at the closing brace.
	if len(results) != 0 && !f.blockIsTerminating(body) {
		f.err(f.tok(body.End()).Position(), "missing return")
	}
	f.reportUnusedLocals(body)
}

// reportUnusedLocals reports each local variable of the function body that is
// never used. Usage is decided syntactically: a variable is used when its name
// appears as an identifier in a read position -- anywhere other than at its own
// declaration or as a bare assignment target ("x = e", which writes x; "x.f = e",
// "x[i] = e" and "*x = e" read x and so count). A variable only ever assigned,
// never read, is thus reported. The rule remains name-based, so a used variable is
// never falsely reported, at the cost of not distinguishing an unused variable from
// a used one of the same name in a different scope (shadowing).
func (f *File) reportUnusedLocals(body Node) {
	if len(f.localVars) == 0 {
		return
	}
	// Positions that do not count as a use of a variable: its own declaration, and
	// every bare assignment target (a write).
	excluded := map[string]bool{}
	for _, vd := range f.localVars {
		excluded[vd.token.Position().String()] = true
	}
	for pos := range f.writeTargets {
		excluded[pos] = true
	}
	used := map[string]bool{}
	f.collectIdentUses(body, excluded, used)
	for _, vd := range f.localVars {
		if nm := vd.token.Src(); nm != "_" && !used[nm] {
			f.err(vd.token.Position(), "declared and not used: %s", nm)
		}
	}
}

// collectIdentUses records, in used, the source text of every identifier token in
// n's subtree whose position is not a variable declaration (per declared) -- i.e.
// every referencing occurrence of a name.
func (f *File) collectIdentUses(n Node, declared, used map[string]bool) {
	for c := range it(n.ast) {
		if c.sym != 0 {
			f.collectIdentUses(c, declared, used)
			continue
		}
		if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT && !declared[tok.Position().String()] {
			used[tok.Src()] = true
		}
	}
}

// rejectDotImports reports each dot import ("import . \"path\"") in the file. The
// grammar admits the "." form, but OctoGo has no semantics for merging a package's
// exported names into the file scope (which is not even on a body's resolution
// chain), so a dot import is rejected rather than implemented. This is a semantic
// check, not a syntactic one, so it runs in a check phase like the other
// unsupported-feature rejections.
func (f *File) rejectDotImports() {
	for _, is := range f.ImportSpecs {
		if is.IsDotImport {
			f.err(is.ImportPathToken.Position(), "dot imports not supported")
		}
	}
}

// reportUnusedImports reports each import of the file that resolved to a package
// but whose qualifier is never referenced ("imported and not used"). Usage is
// decided syntactically and deliberately generously, mirroring the unused-variable
// report: a qualifier counts as used when its name appears as an identifier
// anywhere in the top-level declarations, so a used import is never falsely
// reported. The trade-off is a false negative when a same-named local, constant or
// type shadows the qualifier (the shadowing occurrence marks the import used). Dot
// and blank imports carry no qualifier and are exempt; an import that failed to
// resolve (missing directory, cycle) is reported at import time, not here.
func (f *File) reportUnusedImports() {
	var candidates []*ImportSpecNode
	for _, is := range f.ImportSpecs {
		if is.resolved && !is.IsDotImport && is.ImportQualifier != "" && is.ImportQualifier != "_" {
			candidates = append(candidates, is)
		}
	}
	if len(candidates) == 0 {
		return
	}
	// Collect every identifier used in the top-level declarations. Import
	// declarations are skipped, so an explicit qualifier ("import x \"path\"")
	// never counts as a use of itself.
	used := map[string]bool{}
	declared := map[string]bool{}
	for n := range it(f.AST) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym == TopLevelDecl {
				f.collectIdentUses(c, declared, used)
			}
		}
	}
	for _, is := range candidates {
		if !used[is.ImportQualifier] {
			f.err(is.ImportPathToken.Position(), "imported and not used: %q", is.ImportPath)
		}
	}
}

// declareReceiver declares a method receiver's name in the function body scope
// so the body may reference it. Receiver = "(" identifier Type ")", so the
// first identifier is the receiver name.
func (f *File) declareReceiver(s *Scope, n Node) {
	for c := range it(n.ast) {
		if c.sym == 0 {
			if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT {
				if err := s.add(&VarDeclaration{declaration: declaration{token: tok}}); err != nil {
					f.err(tok.Position(), "%v", err)
				}
				return
			}
		}
	}
}

// retResult describes one of a function's result values for return checking.
type retResult struct {
	name  string // source type name, for messages (e.g. "int")
	kind  Kind
	known bool // kind is a predeclared type we can check literals against
}

// flattenResults expands a signature's result list into one retResult per
// result value ("(x, y int)" yields two).
func (f *File) flattenResults(s *Scope, sig *SignatureNode) (r []retResult) {
	if sig.Results == nil {
		return nil
	}
	for _, p := range sig.Results.List {
		rt := f.resultType(s, p.TypeNode)
		n := len(p.Names)
		if n == 0 {
			n = 1 // an unnamed result contributes one value
		}
		for range n {
			r = append(r, rt)
		}
	}
	return r
}

// resultType classifies a result's type for return checking. Only predeclared
// types are resolved to a Kind; anything else is left unchecked (known=false).
func (f *File) resultType(s *Scope, tn TypeNode) (r retResult) {
	id, ok := tn.(*TypeNodeIdent)
	if !ok {
		return r
	}
	r.name = id.Name.Src()
	r.kind, r.known = f.typeKind(s, tn)
	return r
}

// typeKind resolves a TypeNode to a predeclared Kind. It reports false for
// composite, named (non-predeclared) or unresolved types.
func (f *File) typeKind(s *Scope, tn TypeNode) (Kind, bool) {
	if id, ok := tn.(*TypeNodeIdent); ok {
		if pt, ok := s.find(id.Name.Src()).(*PredeclaredType); ok {
			return pt.Kind(), true
		}
	}
	return 0, false
}

// elemTypeKind returns the predeclared Kind of the element type of a pointer,
// array or slice type ("*T", "[N]T", "[]T") -- the type reached by a dereference
// "*p" or an index "a[i]". ok is false for any other type, or when the element
// type is not predeclared (e.g. a struct), so an unmodelled element leaves the
// assignment unchecked rather than misreported.
func (f *File) elemTypeKind(s *Scope, tn TypeNode) (Kind, bool) {
	switch x := tn.(type) {
	case *TypeNodePointer:
		return f.typeKind(s, x.TypeNode)
	case *TypeNodeArray:
		return f.typeKind(s, x.TypeNode)
	case *TypeNodeSlice:
		return f.typeKind(s, x.TypeNode)
	}
	return 0, false
}

// declareParamList declares the named parameters or results in list into scope s.
// A named result shares the body scope with the parameters and locals, so it may
// be referenced and assigned in the body; an unnamed entry (an unnamed result)
// declares nothing, and a name shared by a parameter and a result is a
// redeclaration.
func (f *File) declareParamList(s *Scope, list *ParameterListNode) {
	if list == nil {
		return
	}
	for _, p := range list.List {
		// Record each name's type so its uses are checked like a local variable's:
		// a predeclared Kind, whether it is a pointer, and its named (possibly
		// pointed-to) type for field access.
		kind, hasKind := f.typeKind(s, p.TypeNode)
		_, isPtr := p.TypeNode.(*TypeNodePointer)
		typeName, _ := namedTypeToken(p.TypeNode)
		elemKind, hasElemKind := f.elemTypeKind(s, p.TypeNode)
		for _, nm := range p.Names {
			if err := s.add(&VarDeclaration{declaration: declaration{token: nm}, kind: kind, hasKind: hasKind, isPtr: isPtr, typeName: typeName, elemKind: elemKind, hasElemKind: hasElemKind}); err != nil {
				f.err(nm.Position(), "%v", err)
			}
		}
	}
}

// blockIsTerminating reports whether a block's statement list ends in a
// terminating statement, following Go's terminating-statement rules. The analysis
// is simpler than Go's because OctoGo has no break, continue, fallthrough or goto:
// nothing can jump out of an enclosing loop, switch or select, so a conditionless
// "for" loops forever and a switch or select terminates exactly when all of its
// clause bodies do -- no break-target bookkeeping is required.
func (f *File) blockIsTerminating(block Node) bool {
	last, ok := f.lastStatement(block)
	return ok && f.stmtIsTerminating(last)
}

// lastStatement returns the final non-empty statement among a node's direct
// children -- the statements of a block, or of a case or comm clause body. The
// automatic-semicolon and closing-brace tokens are not statements and are
// ignored; a body with no statement returns ok == false.
func (f *File) lastStatement(n Node) (last Node, ok bool) {
	for c := range it(n.ast) {
		if c.sym == Statement && !isEmptyStatement(c) {
			last, ok = c, true
		}
	}
	return last, ok
}

// isEmptyStatement reports whether a statement is the empty statement (a bare
// ";"), which wraps an EmptyStatement child. Skipping it keeps a trailing ";"
// from masking a preceding terminating statement.
func isEmptyStatement(stmt Node) bool {
	for c := range it(stmt.ast) {
		if c.sym == EmptyStatement {
			return true
		}
	}
	return false
}

// stmtIsTerminating reports whether a single statement is terminating: a return;
// a call to the built-in panic; a conditionless "for"; an "if" whose "if" and
// "else" branches both terminate; a switch with a default whose every clause
// terminates; a select whose every clause terminates; or a bare block that
// terminates. Any other statement can be followed by more code and so does not
// terminate.
func (f *File) stmtIsTerminating(stmt Node) bool {
	var blocks []Node
	var switchStmt, selectStmt, head, postfix Node
	var isReturn, isFor, isIf, hasSwitch, hasSelect, hasHead, hasPostfix bool
	exprCount := 0
	for c := range it(stmt.ast) {
		switch c.sym {
		case Block:
			blocks = append(blocks, c)
		case SwitchStmt:
			switchStmt, hasSwitch = c, true
		case SelectStmt:
			selectStmt, hasSelect = c, true
		case AssignHead:
			head, hasHead = c, true
		case Postfix:
			postfix, hasPostfix = c, true
		case Expression:
			exprCount++
		case 0:
			switch f.ch(c.tok) {
			case RETURN:
				isReturn = true
			case FOR:
				isFor = true
			case IF:
				isIf = true
			}
		}
	}
	switch {
	case isReturn:
		return true
	case isFor:
		// A conditionless "for" loops forever; a conditional one falls through
		// when its condition is false. No break exists to exit either.
		return exprCount == 0
	case isIf:
		// Terminating only with both an "if" and an "else" branch (two blocks),
		// each terminating. Without "else" there is one block, which control skips
		// when the condition is false.
		return len(blocks) == 2 && f.blockIsTerminating(blocks[0]) && f.blockIsTerminating(blocks[1])
	case hasSwitch:
		return f.switchIsTerminating(switchStmt)
	case hasSelect:
		return f.selectIsTerminating(selectStmt)
	case len(blocks) == 1 && !hasHead:
		return f.blockIsTerminating(blocks[0]) // a bare block statement
	case hasHead && hasPostfix:
		return f.isPanicCall(head, postfix) // an expression statement
	}
	return false
}

// switchIsTerminating reports whether an expression switch terminates: it must
// have a default clause and every clause body -- the default included -- must end
// in a terminating statement. OctoGo has no break, so none can escape the switch.
func (f *File) switchIsTerminating(n Node) bool {
	hasDefault := false
	for c := range it(n.ast) {
		if c.sym != CaseClause {
			continue
		}
		if f.caseIsDefault(c) {
			hasDefault = true
		}
		if !f.blockIsTerminating(c) {
			return false
		}
	}
	return hasDefault
}

// selectIsTerminating reports whether a select terminates: every communication
// clause body must end in a terminating statement. A select blocks until one
// clause can proceed, so -- unlike a switch -- it needs no default; an empty
// select blocks forever and so terminates vacuously.
func (f *File) selectIsTerminating(n Node) bool {
	for c := range it(n.ast) {
		if c.sym == CommClause && !f.blockIsTerminating(c) {
			return false
		}
	}
	return true
}

// caseIsDefault reports whether a case clause is the "default" clause.
func (f *File) caseIsDefault(caseClause Node) bool {
	_, ok := f.clauseDefaultToken(caseClause)
	return ok
}

// clauseDefaultToken returns the "default" keyword token of a switch case clause
// or a select comm clause when the clause is the default clause -- its head is the
// keyword "default", with no expression or communication operation. The keyword
// lives in the clause's head: a CaseHead for a switch (CaseHead = "case"
// ExpressionList | "default") or a CommHead for a select (CommHead = "case"
// CommOp | "default"). A non-default clause returns ok == false.
func (f *File) clauseDefaultToken(clause Node) (tok Token, ok bool) {
	for head := range it(clause.ast) {
		if head.sym != CaseHead && head.sym != CommHead {
			continue
		}
		for c := range it(head.ast) {
			if c.sym == 0 && f.ch(c.tok) == DEFAULT {
				return f.tok(c.tok), true
			}
		}
	}
	return tok, false
}

// isPanicCall reports whether an expression statement is a direct call to the
// built-in function panic, which does not return. head is the statement's
// AssignHead and postfix its Postfix; a selector, index or method call (an
// indirect call) is not the built-in.
func (f *File) isPanicCall(head, postfix Node) bool {
	if _, direct, isCall := f.callInfo(postfix); !isCall || !direct {
		return false
	}
	id, ok := f.assignHeadIdent(head)
	return ok && id.Src() == "panic"
}

// checkBlock walks the statements of a block, or of a case or comm clause body,
// type-checking each and reporting the first statement made unreachable by a
// preceding terminating statement in the same list. The caller provides the
// scope: a function body shares its parameter scope; a nested block gets a child
// scope. results carries the enclosing function's result types for return
// checking.
func (f *File) checkBlock(s *Scope, results []retResult, n Node) {
	terminated, reported := false, false
	for c := range it(n.ast) {
		if c.sym != Statement || isEmptyStatement(c) {
			continue
		}
		// A statement following a terminating one cannot be reached. Report only
		// the first -- the rest of the list is unreachable too -- but keep checking
		// each, as unreachable code must still be well-formed.
		if terminated && !reported {
			f.err(f.tok(c.Pos()).Position(), "unreachable code")
			reported = true
		}
		f.checkStatement(s, results, c)
		if !terminated {
			terminated = f.stmtIsTerminating(c)
		}
	}
}

// checkStatement handles the statement forms Phase 4 currently understands:
// local variable/constant declarations (reporting redeclarations), return
// statements (operand arity and, for literal operands, result type), "go"
// statements (the launched call's callee and arguments), and nested blocks (if/for
// bodies, in a child scope). Other forms are not yet checked.
func (f *File) checkStatement(s *Scope, results []retResult, stmt Node) {
	var head Node
	isReturn := false
	isGo := false
	condKw := "" // "if"/"for" while the next Expression child is that condition
	for c := range it(stmt.ast) {
		switch c.sym {
		case VarDecl:
			f.declareLocalVar(s, c)
		case ConstDecl:
			// Declare the local constant's name, then evaluate its initializer,
			// mirroring the two top-level passes (declareConst + constDecl).
			f.declareConst(s, c)
			f.constDecl(s, c)
		case TypeDecl:
			// Declare the local type name, then resolve its body, mirroring the
			// two top-level passes (declareType + typeDecl). A local type is
			// visible from its declaration onward (Go block scoping).
			f.declareType(s, c)
			f.typeDecl(s, c)
		case Block:
			f.checkBlock(s.child(), results, c)
		case Statement:
			f.checkStatement(s, results, c)
		case AssignHead:
			head = c
		case Postfix:
			f.checkAssignment(s, head, c)
		case SwitchStmt:
			f.checkSwitch(s, results, c)
		case SelectStmt:
			f.checkSelect(s, results, c)
		case Expression:
			// The only bare Expression child of a statement that is a boolean
			// condition is an "if"/"for" guard; a "<-" receive statement's
			// Expression is not.
			if condKw != "" {
				f.checkCondition(s, condKw, c)
				condKw = ""
			}
		case 0:
			switch f.ch(c.tok) {
			case RETURN:
				isReturn = true
			case GO:
				isGo = true
			case IF, FOR:
				condKw = f.tok(c.tok).Src()
			case DEFER:
				// The grammar still admits "defer" for LL(1) simplicity, but the
				// zero-allocation target cannot accumulate deferred calls.
				f.err(f.tok(c.tok).Position(), "unexpected keyword 'defer'")
			}
		}
	}
	if isReturn {
		f.checkReturn(s, results, stmt)
	}
	if isGo {
		f.checkGoStmt(s, head, stmt)
	}
}

// checkGoStmt checks a "go" statement's launched call. A go statement is
// "go" AssignHead { Selector | Index } CallSuffix: it starts the call on another
// Cog but is otherwise an ordinary call, so its callee is resolved (an undefined
// or non-function callee reported) and its arguments name- and type-checked
// exactly as a call statement's are. The call is not wrapped in a Postfix as a
// call statement's is, so the statement node itself carries the selectors and the
// CallSuffix the call helpers scan for.
func (f *File) checkGoStmt(s *Scope, head, stmt Node) {
	f.checkSelectors(s, head, stmt)
	argList, direct, isCall := f.callInfo(stmt)
	if !isCall {
		return
	}
	id, ok := f.assignHeadIdent(head)
	f.checkCall(s, id, direct && ok, argList)
	if !direct && ok {
		if m, has := f.methodCallMember(stmt); has {
			f.checkMethodCall(s, id, m, argList)
		}
	}
}

// checkReturn verifies a return statement's operand count against the enclosing
// function's results, resolves the names in each operand, and checks each operand
// is assignable to its corresponding result type.
func (f *File) checkReturn(s *Scope, results []retResult, stmt Node) {
	var retTok Token
	var exprs []Node
	for c := range it(stmt.ast) {
		switch c.sym {
		case ExpressionList:
			for e := range it(c.ast) {
				if e.sym == Expression {
					exprs = append(exprs, e)
				}
			}
		case 0:
			if f.ch(c.tok) == RETURN {
				retTok = f.tok(c.tok)
			}
		}
	}

	switch {
	case len(exprs) < len(results):
		f.err(retTok.Position(), "not enough arguments to return")
		return
	case len(exprs) > len(results):
		f.err(retTok.Position(), "too many arguments to return")
		return
	}
	for i, e := range exprs {
		f.checkNames(s, e)
		f.checkReturnValue(s, results[i], e)
	}
}

// checkReturnValue reports a type mismatch when a return operand is not
// assignable to its (predeclared) result type: a literal by the untyped-constant
// rules, and a typed operand (variable, call, method result, field or operator
// expression) by type category. A non-predeclared result, or an operand of
// undetermined type, is left unchecked.
func (f *File) checkReturnValue(s *Scope, rt retResult, e Node) {
	if !rt.known {
		return
	}
	if tok, ok := f.bareLiteral(e); ok {
		var valName string
		var assignable bool
		switch Symbol(tok.Ch) {
		case INT, CHAR:
			valName, assignable = "int", isNumericKind(rt.kind)
		case STRING:
			valName, assignable = "string", rt.kind == PredeclaredString
		default:
			return
		}
		if !assignable {
			f.err(tok.Position(), "cannot use %s of type %s as type %s in return statement", tok.Src(), valName, rt.name)
		}
		return
	}
	if k, ok := f.exprType(s, e); ok && kindCategory(k) != catUnknown && kindCategory(k) != kindCategory(rt.kind) {
		tok := f.tok(e.Pos())
		f.err(tok.Position(), "cannot use %s of type %s as type %s in return statement", tok.Src(), kindName(k), rt.name)
	}
}

// bareLiteral reports whether an expression node is a single int, string or rune
// literal -- no operators, call/index/selector suffix or parentheses -- and
// returns that literal token.
func (f *File) bareLiteral(n Node) (Token, bool) {
	var lit Token
	found, extra := false, false
	for c := range it(n.ast) {
		switch c.sym {
		case SimpleExpr, Term, UnaryExpr, Factor:
			switch t, ok := f.bareLiteral(c); {
			case !ok, found:
				extra = true
			default:
				lit, found = t, true
			}
		case 0:
			switch Symbol(f.tok(c.tok).Ch) {
			case INT, STRING, CHAR:
				if found {
					extra = true
				}
				lit, found = f.tok(c.tok), true
			default:
				extra = true
			}
		default:
			extra = true
		}
	}
	if found && !extra {
		return lit, true
	}
	return Token{}, false
}

// isNumericKind reports whether k is one of the predeclared integer types.
func isNumericKind(k Kind) bool {
	switch k {
	case PredeclaredInt8, PredeclaredInt16, PredeclaredInt32,
		PredeclaredUint8, PredeclaredUint16, PredeclaredUint32, PredeclaredUintptr:
		return true
	}
	return false
}

// intKindRange returns the inclusive minimum and maximum value of a predeclared
// integer type k, as constants, and ok == false for any other Kind. Uintptr uses
// the 32-bit range: a P2 pointer is a 32-bit Hub RAM address.
func intKindRange(k Kind) (lo, hi constant.Value, ok bool) {
	var lo64, hi64 int64
	switch k {
	case PredeclaredInt8:
		lo64, hi64 = -128, 127
	case PredeclaredInt16:
		lo64, hi64 = -32768, 32767
	case PredeclaredInt32:
		lo64, hi64 = -2147483648, 2147483647
	case PredeclaredUint8:
		lo64, hi64 = 0, 255
	case PredeclaredUint16:
		lo64, hi64 = 0, 65535
	case PredeclaredUint32, PredeclaredUintptr:
		lo64, hi64 = 0, 4294967295
	default:
		return nil, nil, false
	}
	return constant.MakeInt64(lo64), constant.MakeInt64(hi64), true
}

// sizedKindName returns the canonical source name of a predeclared integer type,
// used in an overflow message when the target's own type token is not recorded (a
// ":="-inferred variable); an explicitly typed target keeps its written name.
func sizedKindName(k Kind) string {
	switch k {
	case PredeclaredInt8:
		return "int8"
	case PredeclaredInt16:
		return "int16"
	case PredeclaredInt32:
		return "int32"
	case PredeclaredUint8:
		return "uint8"
	case PredeclaredUint16:
		return "uint16"
	case PredeclaredUint32:
		return "uint32"
	case PredeclaredUintptr:
		return "uintptr"
	}
	return "?"
}

// isBoolKind reports whether k is a boolean type (predeclared or untyped).
func isBoolKind(k Kind) bool {
	return k == PredeclaredBool || k == UntypedBool
}

// isFloatTypeName reports whether nm names one of Go's floating-point types,
// which OctoGo reserves but does not support.
func isFloatTypeName(nm string) bool {
	switch nm {
	case "float32", "float64":
		return true
	}
	return false
}

// Broad comparability classes returned by kindCategory.
const (
	catUnknown = iota
	catBool
	catString
	catNumeric
)

// kindCategory groups Kinds into broad comparability classes. It returns
// catUnknown for a Kind that fits none (so it never compares as equal to a known
// category).
func kindCategory(k Kind) int {
	switch {
	case isBoolKind(k):
		return catBool
	case k == PredeclaredString || k == UntypedString:
		return catString
	case isNumericKind(k) || k == UntypedInt || k == UntypedFloat:
		return catNumeric
	}
	return catUnknown
}

// kindName returns a source-like name for k, for use in diagnostics.
func kindName(k Kind) string {
	switch kindCategory(k) {
	case catBool:
		return "bool"
	case catString:
		return "string"
	case catNumeric:
		return "int"
	}
	return "?"
}

// checkCondition resolves the names and operator operands of an "if"/"for"
// condition and reports when its type is known and non-boolean. Both checks are
// conservative: an expression whose type cannot yet be determined (a call,
// selector, or unresolved name) yields no "non-bool" report.
func (f *File) checkCondition(s *Scope, kw string, n Node) {
	f.checkNames(s, n)
	if k, ok := f.exprType(s, n); ok && !isBoolKind(k) {
		f.err(f.tok(n.Pos()).Position(), "non-bool used as %s condition", kw)
	}
}

// exprType conservatively determines the type Kind of an expression, reporting
// ok=false when it cannot (a call/selector result, channel receive, or a name
// that is not a typed variable/constant). It does not itself report errors.
func (f *File) exprType(s *Scope, n Node) (Kind, bool) {
	switch n.sym {
	case Expression:
		// SimpleExpr { RelOp SimpleExpr }: a relational operator yields bool.
		var first Node
		firstSet, hasRel := false, false
		for c := range it(n.ast) {
			switch c.sym {
			case RelOp:
				hasRel = true
			case SimpleExpr:
				if !firstSet {
					first, firstSet = c, true
				}
			}
		}
		switch {
		case hasRel:
			return UntypedBool, true
		case firstSet:
			return f.exprType(s, first)
		}
	case SimpleExpr, Term:
		// Add/mul operators keep the operand's (numeric) kind; use the first.
		for c := range it(n.ast) {
			switch c.sym {
			case Term, UnaryExpr:
				return f.exprType(s, c)
			}
		}
	case UnaryExpr:
		// "!" yields bool; "<-" (receive) has an element type we can't resolve
		// here; the arithmetic unary operators keep the operand's kind.
		var fac Node
		var ops []Node
		facSet := false
		for c := range it(n.ast) {
			switch c.sym {
			case Factor:
				fac, facSet = c, true
			case UnaryOp:
				ops = append(ops, c)
			}
		}
		// A single dereference "*p" of a pointer to a predeclared type has that
		// pointed-to type. Anything more complex falls through to the general
		// handling below, unchanged.
		if facSet && len(ops) == 1 && f.unaryOp(s, ops[0]) == MUL {
			if id, ok := f.exprIdent(fac); ok {
				if d, ok := s.find(id.Src()).(*VarDeclaration); ok && d.isPtr && d.hasElemKind {
					return d.elemKind, true
				}
			}
		}
		for _, c := range ops {
			switch f.unaryOp(s, c) {
			case NOT:
				return UntypedBool, true
			case ARROW:
				return 0, false
			}
		}
		if facSet {
			return f.exprType(s, fac)
		}
	case Factor:
		return f.factorType(s, n)
	}
	return 0, false
}

// factorType determines the Kind of a Factor: a literal, a parenthesized
// expression, or a bare identifier bound to a typed variable or constant. A
// call/index/selector suffix makes the result type unknown.
func (f *File) factorType(s *Scope, n Node) (Kind, bool) {
	var lit Token
	var paren, suffix Node
	var hasLit, hasParen, hasSuffix bool
	for c := range it(n.ast) {
		switch c.sym {
		case Expression:
			paren, hasParen = c, true
		case FactorSuffix:
			suffix, hasSuffix = c, true
		case 0:
			lit, hasLit = f.tok(c.tok), true
		}
	}
	switch {
	case hasSuffix:
		// A call to a named function or a method with a single predeclared result
		// has that result's type; a field selection "v.field" has the field's type;
		// and an index "a[i]" of an array or slice has the element type. A
		// multi-result call is not modelled.
		if k, ok := f.callResultKind(s, lit, hasLit, suffix); ok {
			return k, true
		}
		if k, ok := f.methodResultKind(s, lit, hasLit, suffix); ok {
			return k, true
		}
		if field, ok := f.fieldSelector(suffix); ok && hasLit {
			return f.fieldKind(s, lit, field)
		}
		if hasLit && f.indexSuffix(suffix) {
			if d, ok := s.find(lit.Src()).(*VarDeclaration); ok && d.hasElemKind && !d.isPtr {
				return d.elemKind, true
			}
		}
		return 0, false
	case hasParen:
		return f.exprType(s, paren)
	case hasLit:
		switch Symbol(lit.Ch) {
		case INT, CHAR:
			return UntypedInt, true
		case FLOAT:
			return UntypedFloat, true
		case STRING:
			return UntypedString, true
		case IDENT:
			return f.identKind(s, lit)
		}
	}
	return 0, false
}

// identKind returns the type Kind bound to a name when it is a variable with a
// resolved predeclared type or a constant with a known value type.
func (f *File) identKind(s *Scope, tok Token) (Kind, bool) {
	switch d := s.find(tok.Src()).(type) {
	case *VarDeclaration:
		if d.hasKind {
			return d.kind, true
		}
	case *ConstDeclaration:
		if d.ConstSpec != nil && d.ConstSpec.Value != nil {
			if t := d.ConstSpec.Value.Type(); t != nil {
				return t.Kind(), true
			}
		}
	}
	return 0, false
}

// reportMultipleDefaults reports each default clause after the first among a
// switch's case clauses or a select's comm clauses. A switch or select may have at
// most one default; a second (or later) one is a compile error, reported at that
// clause and pointing at the first. clauseSym selects the clause production
// (CaseClause for a switch, CommClause for a select) and kind names the statement
// in the message.
func (f *File) reportMultipleDefaults(n Node, clauseSym Symbol, kind string) {
	var first Token
	haveFirst := false
	for c := range it(n.ast) {
		if c.sym != clauseSym {
			continue
		}
		tok, ok := f.clauseDefaultToken(c)
		if !ok {
			continue
		}
		if !haveFirst {
			first, haveFirst = tok, true
			continue
		}
		f.err(tok.Position(), "multiple defaults in %s (first at %v)", kind, first.Position())
	}
}

// reportDuplicateCases reports a switch case value that repeats an earlier case in
// the same switch. Each case expression that is a known constant is compared by
// value -- so "case 1" and "case 0x1" collide, as do "case 1" and a "case C" for a
// "const C = 1" -- while a non-constant case (a variable or a call, both allowed in
// a case position) or an ill-formed one is skipped. Only constants of the same
// go/constant kind are compared, so an int/float or int/string pair is never
// misreported as a duplicate. The repeat is reported at its expression, pointing at
// the first occurrence.
func (f *File) reportDuplicateCases(s *Scope, n Node) {
	seen := map[string]token.Position{}
	for clause := range it(n.ast) {
		if clause.sym != CaseClause {
			continue
		}
		for head := range it(clause.ast) {
			if head.sym != CaseHead {
				continue
			}
			for list := range it(head.ast) {
				if list.sym != ExpressionList {
					continue
				}
				for e := range it(list.ast) {
					if e.sym != Expression {
						continue
					}
					cv, ok := f.caseConstValue(s, e)
					if !ok {
						continue
					}
					key := cv.Kind().String() + " " + cv.ExactString()
					pos := f.tok(e.Pos()).Position()
					if first, dup := seen[key]; dup {
						f.err(pos, "duplicate case %s in switch (previous at %v)", cv.String(), first)
						continue
					}
					seen[key] = pos
				}
			}
		}
	}
}

// caseConstValue folds a switch case expression, reporting the errors the fold
// finds -- an undefined name, a division by zero, an operator not defined on its
// operands -- and returns its constant value for duplicate detection. A
// non-constant operand (a variable, a call) is legal in a case, so inCaseExpr
// suppresses the "is not a constant" the folder would otherwise emit; such a case
// folds to an unknown value and, like an ill-formed constant, is reported as
// ok == false and left out of the duplicate comparison.
func (f *File) caseConstValue(s *Scope, e Node) (constant.Value, bool) {
	save := f.inCaseExpr
	f.inCaseExpr = true
	en := f.expression(s, e)
	f.inCaseExpr = save
	if en == nil {
		return nil, false
	}
	cv, ok := en.Value().(untypedConst)
	if !ok || cv.cv == nil || cv.cv.Kind() == constant.Unknown {
		return nil, false
	}
	return cv.cv, true
}

// checkSwitch walks a switch statement in its own implicit block scope: it reports
// a repeated default clause or case value, determines the guard's type, declares a
// "v := expr" guard variable (visible in the clauses but not after the switch),
// checks each case expression is comparable to the guard, and walks each clause
// body in a nested scope.
func (f *File) checkSwitch(s *Scope, results []retResult, n Node) {
	f.reportMultipleDefaults(n, CaseClause, "switch")
	ss := s.child()
	var guardKind Kind
	guardOK := false
	// SwitchGuard precedes the CaseClauses, so the guard is processed first.
	for c := range it(n.ast) {
		switch c.sym {
		case SwitchGuard:
			// Resolve the names in the guard's value expression, reporting an
			// undefined name, a blank read or an ill-typed operator there, just as
			// a case expression's are checked. The guard is evaluated in the outer
			// scope s, before any "v := expr" guard variable is declared.
			if e, ok := f.guardValueExpr(c); ok {
				f.checkNames(s, e)
			}
			guardKind, guardOK = f.switchGuardType(s, c)
			f.declareSwitchGuardVar(ss, guardKind, guardOK, c)
		case CaseClause:
			if guardOK {
				f.checkCaseExprs(ss, guardKind, c)
			}
			f.checkClauseBody(ss.child(), results, c)
		}
	}
	f.reportDuplicateCases(ss, n)
}

// declareSwitchGuardVar declares the variable introduced by a "v := expr" switch
// guard in scope s. Guards without ":=" introduce nothing.
func (f *File) declareSwitchGuardVar(s *Scope, kind Kind, hasKind bool, n Node) {
	var lhs Node
	hasDefine, hasLHS := false, false
	for c := range it(n.ast) {
		switch c.sym {
		case Expression:
			if !hasLHS {
				lhs, hasLHS = c, true
			}
		case 0:
			if f.ch(c.tok) == DEFINE {
				hasDefine = true
			}
		}
	}
	if !hasDefine || !hasLHS {
		return
	}
	if id, ok := f.exprIdent(lhs); ok {
		_ = s.add(&VarDeclaration{declaration: declaration{token: id}, kind: kind, hasKind: hasKind})
	}
}

// switchGuardType resolves the type a switch compares against: the type of the
// guard's value expression (see guardValueExpr).
func (f *File) switchGuardType(s *Scope, n Node) (Kind, bool) {
	if e, ok := f.guardValueExpr(n); ok {
		return f.exprType(s, e)
	}
	return 0, false
}

// guardValueExpr returns a switch guard's value expression -- the operand a plain
// "switch expr" guard switches on, or the right-hand side of a "switch v := expr"
// guard. The left-hand side of a ":=" guard names the guard variable being
// declared, not a value that is read, so it is never returned. A degenerate guard
// with no value expression returns ok == false.
func (f *File) guardValueExpr(n Node) (Node, bool) {
	var exprs []Node
	hasDefine := false
	for c := range it(n.ast) {
		switch c.sym {
		case Expression:
			exprs = append(exprs, c)
		case 0:
			if f.ch(c.tok) == DEFINE {
				hasDefine = true
			}
		}
	}
	switch {
	case hasDefine:
		if len(exprs) >= 2 {
			return exprs[1], true
		}
	case len(exprs) >= 1:
		return exprs[0], true
	}
	return Node{}, false
}

// checkCaseExprs checks every expression of a case clause's CaseHead against the
// switch guard's type. A "default" clause has no expression list and is skipped.
func (f *File) checkCaseExprs(s *Scope, guardKind Kind, n Node) {
	for head := range it(n.ast) {
		if head.sym != CaseHead {
			continue
		}
		for list := range it(head.ast) {
			if list.sym != ExpressionList {
				continue
			}
			for e := range it(list.ast) {
				if e.sym == Expression {
					f.checkCaseExpr(s, guardKind, e)
				}
			}
		}
	}
}

// checkCaseExpr reports a case expression whose type is known and in a different
// comparability class than the switch guard.
func (f *File) checkCaseExpr(s *Scope, guardKind Kind, e Node) {
	k, ok := f.exprType(s, e)
	if !ok {
		return
	}
	if gc, cc := kindCategory(guardKind), kindCategory(k); gc != 0 && cc != 0 && gc != cc {
		f.err(f.tok(e.Pos()).Position(), "cannot use %s of type %s as type %s in case", f.tok(e.Pos()).Src(), kindName(k), kindName(guardKind))
	}
}

// checkSelect walks the communication clauses of a select statement, each in its
// own block scope, declaring the variable a "case v := <-ch" short receive
// introduces. The communication operations themselves -- the channel and the sent
// or received value's type -- are not checked yet.
func (f *File) checkSelect(s *Scope, results []retResult, n Node) {
	f.reportMultipleDefaults(n, CommClause, "select")
	for c := range it(n.ast) {
		if c.sym != CommClause {
			continue
		}
		// Each comm clause is its own block scope. A "case v := <-ch" receive
		// introduces v there, visible in the clause body, before the body is
		// walked. The received value's type is not modelled, so v carries no kind,
		// exactly as "v := <-ch" would outside a select.
		cs := s.child()
		if id, ok := f.commRecvVar(c); ok {
			_ = cs.add(&VarDeclaration{declaration: declaration{token: id}})
		}
		f.checkClauseBody(cs, results, c)
	}
}

// commRecvVar returns the variable a "case v := <-ch" comm clause introduces, when
// the clause is that short-declaration receive. A bare receive "case <-ch", a send
// "case ch <- v", or an "=" receive to an existing variable "case v = <-ch" declares
// nothing and returns ok == false, as does a blank "case _ := <-ch", whose target
// binds no name.
func (f *File) commRecvVar(commClause Node) (Token, bool) {
	for head := range it(commClause.ast) {
		if head.sym != CommHead {
			continue
		}
		for op := range it(head.ast) {
			if op.sym != CommOp {
				continue
			}
			var assignHead Node
			hasHead, hasDefine, hasSuffix := false, false, false
			for c := range it(op.ast) {
				switch c.sym {
				case AssignHead:
					assignHead, hasHead = c, true
				case PostfixComm:
					for pc := range it(c.ast) {
						switch pc.sym {
						case Selector, Index:
							hasSuffix = true // "v.f := <-ch": not a plain short decl
						case 0:
							if f.ch(pc.tok) == DEFINE {
								hasDefine = true
							}
						}
					}
				}
			}
			if hasHead && hasDefine && !hasSuffix {
				if id, ok := f.assignHeadIdent(assignHead); ok && id.Src() != "_" {
					return id, true
				}
			}
		}
	}
	return Token{}, false
}

// checkClauseBody walks the statement body of a case or comm clause in scope s.
// It is the same statement-list walk as a block, so unreachable code within a
// clause is reported too.
func (f *File) checkClauseBody(s *Scope, results []retResult, n Node) {
	f.checkBlock(s, results, n)
}

// declareLocalVar declares the names of a local var declaration in scope s,
// reporting redeclarations. It resolves the declared type enough to record a
// predeclared Kind on each variable (for later type checking) but does not
// evaluate the initializer expression yet.
func (f *File) declareLocalVar(s *Scope, n Node) {
	for n := range it(n.ast) {
		if n.sym != VarSpec {
			continue
		}
		// VarSpec = IdentifierList ( Type [ "=" Expression ] | "=" Expression ) .
		// The IdentifierList precedes the Type, so collect the names and resolve
		// the type's Kind first, then bind the names carrying that Kind.
		var names []Token
		var kind, elemKind Kind
		var hasKind, hasInit, isPtr, hasElemKind bool
		var typeName Token
		var initExpr Node
		for c := range it(n.ast) {
			switch c.sym {
			case IdentifierList:
				names = f.identifierList(s, c)
			case Type:
				// Resolve plain named types and pointers-to-named, struct and
				// interface type literals (so their field/method names are checked
				// for duplicates), and array types (so their length is checked),
				// reporting undefined types. Slices and channels are left
				// unresolved for now: their element expressions are not yet checked.
				if f.simpleNamedType(c) || f.structOrInterfaceType(c) || f.arrayType(c) {
					if tn := f.typ(s, c); tn != nil {
						kind, hasKind = f.typeKind(s, tn)
						_, isPtr = tn.(*TypeNodePointer)
						typeName, _ = namedTypeToken(tn)
						elemKind, hasElemKind = f.elemTypeKind(s, tn)
					}
				}
			case Expression:
				initExpr, hasInit = c, true
			}
		}
		// Resolve the names used in the initializer before binding the new names,
		// so a variable is not visible within its own initializer.
		if hasInit {
			f.checkNames(s, initExpr)
			if hasKind {
				f.checkValueOverflow(s, sizedTarget(kind, typeName), initExpr)
			}
		}
		for _, nm := range names {
			vd := &VarDeclaration{declaration: declaration{token: nm}, kind: kind, hasKind: hasKind, isPtr: isPtr, typeName: typeName, elemKind: elemKind, hasElemKind: hasElemKind}
			if err := s.add(vd); err != nil {
				f.err(nm.Position(), "%v", err)
				continue
			}
			f.localVars = append(f.localVars, vd)
		}
	}
}

// structOrInterfaceType reports whether a Type node is a struct or interface
// type literal, whose field/method names can be checked without evaluating any
// bound or element expression.
func (f *File) structOrInterfaceType(n Node) bool {
	for n := range it(n.ast) {
		switch n.sym {
		case StructType, InterfaceType:
			return true
		}
	}
	return false
}

// arrayType reports whether a Type node denotes an array "[Expression]T" -- it
// carries a bracketed length expression -- as opposed to a slice "[]T". The
// length is checked (constant, non-negative integer) when the type is resolved.
func (f *File) arrayType(n Node) bool {
	for c := range it(n.ast) {
		if c.sym == Expression {
			return true
		}
	}
	return false
}

// simpleNamedType reports whether a Type node denotes a plain named type or a
// pointer to one -- only identifiers, "*" and "." -- so that resolving it will
// not recurse into an array bound or other expression.
func (f *File) simpleNamedType(n Node) bool {
	for n := range it(n.ast) {
		switch n.sym {
		case Type:
			if !f.simpleNamedType(n) {
				return false
			}
		case 0:
			switch f.ch(n.tok) {
			case IDENT, MUL, PERIOD:
				// ok
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

// assignHeadIdent returns the single identifier of an AssignHead when the head
// is a plain name (no "*" and no parenthesized expression) -- the only form that
// can introduce a variable in a short variable declaration.
func (f *File) assignHeadIdent(n Node) (id Token, ok bool) {
	plain := true
	for n := range it(n.ast) {
		switch n.sym {
		case 0:
			if tok := f.tok(n.tok); Symbol(tok.Ch) == IDENT {
				id = tok
			} else {
				plain = false
			}
		default:
			plain = false
		}
	}
	return id, plain && id.IsValid()
}

// hasSelectorOrIndex reports whether a node has a direct Selector or Index child:
// for a Postfix it detects a suffix on the assignment head, for an LhsItem a
// suffix on that item -- i.e. a "base.field" or "base[i]" target whose base is not
// itself the assigned value.
func hasSelectorOrIndex(n Node) bool {
	for c := range it(n.ast) {
		if c.sym == Selector || c.sym == Index {
			return true
		}
	}
	return false
}

// checkAssignment handles a "AssignHead Postfix" statement. Only ":=" introduces
// variables: its plainly-named left-hand operands that are not already declared
// in the current scope are declared here, and it is an error if none of them is
// new (Go short variable declaration semantics). Plain assignments, sends and
// calls declare nothing.
func (f *File) checkAssignment(s *Scope, head, postfix Node) {
	f.checkSelectors(s, head, postfix)

	// A statement that is a bare call ("h(1, 2)") has its CallSuffix directly in
	// the Postfix; an assignment/send carries its call inside a right-hand
	// Expression, which is name-checked (and so call-checked) separately below.
	if argList, direct, isCall := f.callInfo(postfix); isCall {
		id, ok := f.assignHeadIdent(head)
		f.checkCall(s, id, direct && ok, argList)
		if !direct && ok {
			if m, has := f.methodCallMember(postfix); has {
				f.checkMethodCall(s, id, m, argList)
			}
		}
	}

	// lhs holds each target's base identifier; lhsSuffixed[i] reports whether that
	// target carries a "base.field"/"base[i]" selector or index (so the base is not
	// itself the assigned value). The head's suffixes are direct children of the
	// postfix; an LhsItem's are children of the item.
	var lhs []Token
	var lhsSuffixed []bool
	if id, ok := f.assignHeadIdent(head); ok {
		lhs = append(lhs, id)
		lhsSuffixed = append(lhsSuffixed, hasSelectorOrIndex(postfix))
	}

	var op Symbol
	var rhs []Node
	lhsItems := 0 // targets after the head: the LhsItems of "a, b = ...", counted structurally so a non-plain "*p" or "(e)" target counts too
	for n := range it(postfix.ast) {
		if n.sym != PostfixOp {
			continue
		}
		for n := range it(n.ast) {
			switch n.sym {
			case LhsItem:
				lhsItems++
				suffixed := hasSelectorOrIndex(n)
				for c := range it(n.ast) {
					if c.sym == AssignHead {
						if id, ok := f.assignHeadIdent(c); ok {
							lhs = append(lhs, id)
							lhsSuffixed = append(lhsSuffixed, suffixed)
						}
					}
				}
			case Expression:
				rhs = append(rhs, n)
			case 0:
				switch sym := f.ch(n.tok); sym {
				case ASSIGN, DEFINE, ARROW:
					op = sym
				}
			}
		}
	}

	// A bare "="/":=" target ("x", not "x.f"/"x[i]"/"*x", which read x) is a write,
	// not a use; record it so the unused-variable report does not count assigning to
	// a variable as using it. A suffixed target reads its base, so it is left out.
	if op == ASSIGN || op == DEFINE {
		for i, tok := range lhs {
			if !lhsSuffixed[i] {
				f.writeTargets[tok.Position().String()] = true
			}
		}
	}

	// An "=" or ":=" pairs the left-hand targets with the values produced by the
	// single right-hand expression (the grammar admits no value list); their counts
	// must match. The targets are the head plus each LhsItem.
	if op == ASSIGN || op == DEFINE {
		if v, ok := f.rhsValueCount(s, rhs); ok && v != 1+lhsItems {
			f.err(f.tok(head.Pos()).Position(), "assignment mismatch: %s but %s", countUnits(1+lhsItems, "variable"), countUnits(v, "value"))
		}
	}

	// Resolve names used on the right-hand side of "=", ":=" and a send "<-",
	// reporting undefined ones.
	if op == ASSIGN || op == DEFINE || op == ARROW {
		for _, e := range rhs {
			f.checkNames(s, e)
		}
	}
	// A plain "=" also checks each operand is assignable to its target; a receive
	// "y = <-ch" additionally checks the channel's element type against y.
	if op == ASSIGN {
		// Every target must resolve to an assignable variable, just as the
		// right-hand side is name-checked above. A name resolving to nothing is
		// undefined; one resolving to a constant, function or type is not
		// assignable. Both apply to the base variable of a "base.field"/"base[i]"
		// target too -- except that a non-variable base is left to the field/index
		// check, since "cannot assign to base" would misdescribe "base.field = e".
		// The blank identifier is always assignable, and a package qualifier
		// resolves through the file scope, not s, so both are exempt.
		for i, tok := range lhs {
			nm := tok.Src()
			if nm == "_" {
				// A whole "_" target is a legal discard, but a suffixed base
				// ("_.f = e", "_[i] = e") reads "_" and is illegal.
				if lhsSuffixed[i] {
					f.blankRead(tok)
				}
				continue
			}
			switch s.find(nm).(type) {
			case nil:
				if !f.isImportQualifier(s, nm) {
					f.err(tok.Position(), "undefined: %s", nm)
				}
			case *ConstDeclaration, *FuncDeclaration, *TypeDeclaration:
				if !lhsSuffixed[i] {
					f.err(tok.Position(), "cannot assign to %s", nm)
				}
			}
		}
		// A field-target assignment "head.field = e" -- the head ident holds the
		// base variable and the selector is in the postfix, so the plain-target
		// loop below sees only the (struct) head and skips it; check the field here.
		if field, ok := f.fieldSelector(postfix); ok && len(rhs) == 1 {
			if id, idok := f.assignHeadIdent(head); idok {
				f.checkFieldAssign(s, id, field, rhs[0])
			}
		}
		// A dereference "*p = e" or an element "a[i] = e" target -- a single target
		// with a single value. The pointed-to/element type must accept the value,
		// and the base must actually be a pointer/array.
		if lhsItems == 0 && len(rhs) == 1 {
			if base, ok := f.derefAssignTarget(head, postfix); ok {
				f.checkDerefAssign(s, base, rhs[0])
			} else if base, ok := f.indexAssignTarget(head, postfix); ok {
				f.checkIndexAssign(s, base, rhs[0])
			}
		}
		for i, e := range rhs {
			if i < len(lhs) {
				f.checkRecvAssign(s, lhs[i], e)
				f.checkAssignType(s, lhs[i], e)
			}
		}
	}
	// A send "ch <- v" checks that ch is a channel and v matches its element type.
	if op == ARROW {
		if len(lhs) == 1 && len(rhs) == 1 {
			f.checkSend(s, lhs[0], rhs[0])
		}
		return
	}

	if op != DEFINE {
		return
	}

	// ":=" introduces its plainly-named, not-already-declared left operands. When
	// the operands and initializers pair up one-to-one, infer each new variable's
	// predeclared kind from its initializer, so its later uses are type-checked
	// just like an explicitly-typed variable's. A multi-result initializer (fewer
	// initializers than operands) is not modelled, so those kinds stay unknown.
	inferKinds := len(lhs) == len(rhs)
	newCount, nonBlank := 0, 0
	for i, id := range lhs {
		nm := id.Src()
		if nm == "_" {
			continue
		}
		nonBlank++
		if s.Declarations[nm] != nil {
			continue // already declared in this scope: an assignment, not new
		}
		vd := &VarDeclaration{declaration: declaration{token: id}}
		if inferKinds {
			if k, ok := f.exprType(s, rhs[i]); ok {
				vd.kind, vd.hasKind = k, true
			}
		}
		_ = s.add(vd)
		f.localVars = append(f.localVars, vd)
		newCount++
	}
	if nonBlank != 0 && newCount == 0 {
		f.err(f.tok(head.Pos()).Position(), "no new variables on left side of :=")
	}
}

// rhsValueCount returns the number of values the right-hand side of an assignment
// produces, when that number is known. The grammar admits a single right-hand
// expression, which yields one value unless it is a call or a channel receive: a
// direct call to a known function yields its result count, and an expression with
// neither a call nor a receive yields exactly one (the language has no map-index or
// type-assertion comma-ok forms). A method or package call, a receive, an indirect
// call, or a compound expression that merely contains a call is left unknown, so a
// count mismatch there is not reported rather than risk a false one.
func (f *File) rhsValueCount(s *Scope, rhs []Node) (int, bool) {
	if len(rhs) != 1 {
		return 0, false
	}
	e := rhs[0]
	if r, ok := f.directCallResultCount(s, e); ok {
		return r, true
	}
	if !f.exprHasCallOrReceive(e) {
		return 1, true
	}
	return 0, false
}

// directCallResultCount returns the result count of the function called by an
// expression that is exactly a direct call to a named function -- "f(...)" with no
// operator, selector or index around it. Any other expression (an operator
// expression, a method or package call, a call through a variable, a name or a
// literal) returns ok == false, leaving its value count to rhsValueCount.
func (f *File) directCallResultCount(s *Scope, e Node) (int, bool) {
	fac, ok := f.soleFactor(e)
	if !ok {
		return 0, false
	}
	var callee Token
	var suffix Node
	hasCallee, hasSuffix := false, false
	for c := range it(fac.ast) {
		switch c.sym {
		case FactorSuffix:
			suffix, hasSuffix = c, true
		case 0:
			if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT {
				callee, hasCallee = tok, true
			}
		}
	}
	if !hasCallee || !hasSuffix {
		return 0, false
	}
	// The suffix must be exactly a call with no leading selector or index, so the
	// callee is the named function itself (not a method or package call).
	if _, direct, isCall := f.callInfo(suffix); !direct || !isCall {
		return 0, false
	}
	fd, ok := s.find(callee.Src()).(*FuncDeclaration)
	if !ok || fd.FuncDecl == nil || fd.FuncDecl.Type == nil {
		return 0, false
	}
	return len(f.flattenResults(s, fd.FuncDecl.Type.Signature)), true
}

// soleFactor returns the single Factor of an expression that applies no operator --
// an Expression with no relational operator, a SimpleExpr with no additive one, a
// Term with no multiplicative one and a UnaryExpr with no unary one -- i.e. a bare
// operand such as a literal, a name or a call. Any operator (including a unary "-",
// "*" or "<-") yields ok == false.
func (f *File) soleFactor(n Node) (Node, bool) {
	switch n.sym {
	case Expression, SimpleExpr, Term:
		var operand Node
		count := 0
		for c := range it(n.ast) {
			switch c.sym {
			case RelOp, AddOp, MulOp:
				return Node{}, false
			case SimpleExpr, Term, UnaryExpr:
				operand, count = c, count+1
			}
		}
		if count != 1 {
			return Node{}, false
		}
		return f.soleFactor(operand)
	case UnaryExpr:
		var fac Node
		count := 0
		for c := range it(n.ast) {
			switch c.sym {
			case UnaryOp:
				return Node{}, false
			case Factor:
				fac, count = c, count+1
			}
		}
		if count != 1 {
			return Node{}, false
		}
		return fac, true
	case Factor:
		return n, true
	}
	return Node{}, false
}

// exprHasCallOrReceive reports whether an expression contains a call (a CallSuffix)
// or a channel receive (a "<-" operator) anywhere -- the only expression forms that
// can yield more than one value, so an expression with neither yields exactly one.
func (f *File) exprHasCallOrReceive(n Node) bool {
	for c := range it(n.ast) {
		switch c.sym {
		case CallSuffix:
			return true
		case 0:
			if f.ch(c.tok) == ARROW {
				return true
			}
		default:
			if f.exprHasCallOrReceive(c) {
				return true
			}
		}
	}
	return false
}

// countUnits renders a count with its unit noun, pluralised: "1 variable",
// "2 variables", "1 value", "3 values".
func countUnits(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// checkSend checks a send statement "ch <- v": ch must be a channel and the
// value v must match the channel's element type.
func (f *File) checkSend(s *Scope, chTok Token, valNode Node) {
	d, ok := s.find(chTok.Src()).(*VarDeclaration)
	if !ok {
		return // undefined or non-variable operand: not diagnosed here
	}
	elem, hasElem, isChan := f.chanElemOf(s, d)
	if !isChan {
		f.err(chTok.Position(), "invalid operation: cannot send to non-channel")
		return
	}
	vk, vok := f.exprType(s, valNode)
	if !hasElem || !vok {
		return
	}
	if kindCategory(vk) != kindCategory(elem) {
		f.err(f.tok(valNode.Pos()).Position(), "cannot use %s of type %s as type %s in send", f.tok(valNode.Pos()).Src(), kindName(vk), kindName(elem))
	}
}

// checkRecvAssign checks a receive assignment "target = <-ch": when the right-
// hand side is a receive from a channel of known element type, that element
// type must match target's. A receive from a non-channel is reported by
// checkUnaryExpr, so it is skipped here.
func (f *File) checkRecvAssign(s *Scope, target Token, rhs Node) {
	fac, ok := f.receiveFactor(s, rhs)
	if !ok {
		return
	}
	elem, hasElem, isChan := f.exprChan(s, fac)
	if !isChan || !hasElem {
		return
	}
	tk, tok := f.identKind(s, target)
	if tok && kindCategory(tk) != kindCategory(elem) {
		f.err(f.tok(rhs.Pos()).Position(), "cannot assign value received from chan %s to type %s", kindName(elem), kindName(tk))
	}
}

// checkSelectors reports a reference to an unexported member of an imported
// package. For "pkg.member" where pkg is an import qualifier, member must be
// exported (begin with an upper-case letter): "p2.pinLow" is rejected while
// "p2.PinHigh" is allowed. Only the first selector qualifies the package; a
// deeper selector operates on its result, which is not modelled.
func (f *File) checkSelectors(s *Scope, head, postfix Node) {
	id, ok := f.assignHeadIdent(head)
	if !ok {
		return
	}
	if f.isImportQualifier(s, id.Src()) {
		for c := range it(postfix.ast) {
			if c.sym != Selector {
				continue
			}
			if m, ok := f.selectorMember(c); ok && !token.IsExported(m.Src()) {
				f.err(m.Position(), "cannot refer to unexported name %s.%s", id.Src(), m.Src())
			}
			return
		}
		return
	}
	// Not an import qualifier: a "head.field" selection of a struct variable
	// (e.g. a field assignment "p.x = 1").
	if field, ok := f.fieldSelector(postfix); ok {
		f.checkFieldAccess(s, id, field)
	}
}

// isImportQualifier reports whether name denotes a package imported by this
// file. Imports live in the file scope, which is not on a body's block/package
// resolution chain, so a name reachable via s is a shadowing local or package
// declaration -- not an import qualifier.
func (f *File) isImportQualifier(s *Scope, name string) bool {
	if s.find(name) != nil {
		return false
	}
	_, ok := f.Scope.Declarations[name].(*ImportDeclaration)
	return ok
}

// selectorMember returns the member identifier of a Selector node ".name".
func (f *File) selectorMember(n Node) (Token, bool) {
	for c := range it(n.ast) {
		if c.sym == 0 {
			if t := f.tok(c.tok); Symbol(t.Ch) == IDENT {
				return t, true
			}
		}
	}
	return Token{}, false
}

// namedTypeToken returns the name of a named type, following pointers ("*T" and
// "**T" both yield T); ok is false for an anonymous or composite type.
func namedTypeToken(tn TypeNode) (Token, bool) {
	for {
		switch x := tn.(type) {
		case *TypeNodeIdent:
			return x.Name, true
		case *TypeNodePointer:
			tn = x.TypeNode
		default:
			return Token{}, false
		}
	}
}

// structFields returns the set of field names of a named struct type; ok is
// false when the name is not a struct (a predeclared type, an interface, or an
// undefined name), in which case field access is left unchecked.
func (f *File) structFields(s *Scope, typeName Token) (map[string]bool, bool) {
	td, ok := s.find(typeName.Src()).(*TypeDeclaration)
	if !ok || td.TypeSpec == nil {
		return nil, false
	}
	st, ok := td.TypeSpec.TypeNode.(*TypeNodeStruct)
	if !ok {
		return nil, false
	}
	fields := map[string]bool{}
	for _, fld := range st.Fields {
		for _, nm := range fld.Names {
			fields[nm.Src()] = true
		}
	}
	return fields, true
}

// fieldSelector reports whether a FactorSuffix or Postfix is a single field
// selection "x.field" -- exactly one selector, no index and no call -- and
// returns the selected field.
func (f *File) fieldSelector(n Node) (field Token, ok bool) {
	selectors, disqualify := 0, false
	for c := range it(n.ast) {
		switch c.sym {
		case Selector:
			if m, has := f.selectorMember(c); has {
				field, selectors = m, selectors+1
			}
		case Index, CallSuffix:
			disqualify = true
		case PostfixOp:
			for pc := range it(c.ast) {
				if pc.sym == CallSuffix {
					disqualify = true
				}
			}
		}
	}
	return field, selectors == 1 && !disqualify
}

// indexSuffix reports whether a factor suffix is exactly one index "[i]" with no
// selector or call, so "base[i]" reads a single element of base. It is the read
// analogue of fieldSelector.
func (f *File) indexSuffix(n Node) bool {
	indexes, disqualify := 0, false
	for c := range it(n.ast) {
		switch c.sym {
		case Index:
			indexes++
		case Selector, CallSuffix:
			disqualify = true
		}
	}
	return indexes == 1 && !disqualify
}

// checkFieldAccess reports a selection "head.field" when head is a variable of a
// struct type that has no such field.
func (f *File) checkFieldAccess(s *Scope, head, field Token) {
	d, ok := s.find(head.Src()).(*VarDeclaration)
	if !ok || !d.typeName.IsValid() {
		return
	}
	if fields, ok := f.structFields(s, d.typeName); ok && !fields[field.Src()] {
		f.err(field.Position(), "type %s has no field %s", d.typeName.Src(), field.Src())
	}
}

// fieldKind returns the predeclared Kind of "head.field" when head is a variable
// of a struct type whose field has such a type; ok is false otherwise (an
// unknown head, non-struct type, missing field, or non-predeclared field type).
func (f *File) fieldKind(s *Scope, head, field Token) (Kind, bool) {
	d, ok := s.find(head.Src()).(*VarDeclaration)
	if !ok || !d.typeName.IsValid() {
		return 0, false
	}
	td, ok := s.find(d.typeName.Src()).(*TypeDeclaration)
	if !ok || td.TypeSpec == nil {
		return 0, false
	}
	st, ok := td.TypeSpec.TypeNode.(*TypeNodeStruct)
	if !ok {
		return 0, false
	}
	for _, fld := range st.Fields {
		for _, nm := range fld.Names {
			if nm.Src() == field.Src() {
				return f.typeKind(s, fld.TypeNode)
			}
		}
	}
	return 0, false
}

// receiverTypeName returns the base type name of a method Receiver
// "( identifier Type )". Because the grammar is "identifier Type", the receiver
// name is the first identifier and the base type name is the last one in
// traversal order (the Type is a nested node), so "(r *T)" and "(r T)" both
// yield T.
func (f *File) receiverTypeName(recv Node) (name Token) {
	for c := range it(recv.ast) {
		if c.sym == 0 {
			if t := f.tok(c.tok); Symbol(t.Ch) == IDENT {
				name = t
			}
			continue
		}
		if n := f.receiverTypeName(c); n.IsValid() {
			name = n
		}
	}
	return name
}

// registerMethod records a method against its receiver type, with its signature
// resolved so a method call can be checked. A non-method declaration is ignored.
// It runs in phase 3, when every type is in the package scope. The signature is
// resolved in a child of the package scope (like funcDecl), where package-level
// named types used in parameters/results are visible.
func (f *File) registerMethod(s *Scope, n Node) {
	var recvType, method Token
	var sig Node
	hasRecv, hasSig := false, false
	for c := range it(n.ast) {
		switch c.sym {
		case Receiver:
			recvType, hasRecv = f.receiverTypeName(c), true
		case Signature:
			sig, hasSig = c, true
		case 0:
			if t := f.tok(c.tok); Symbol(t.Ch) == IDENT {
				method = t
			}
		}
	}
	if !hasRecv || !method.IsValid() {
		return
	}
	td, ok := s.find(recvType.Src()).(*TypeDeclaration)
	if !ok {
		return
	}
	if td.methods == nil {
		td.methods = map[string]*FuncDeclNode{}
	}
	if prev := td.methods[method.Src()]; prev != nil {
		// A second method of the same name on the same receiver type (a value and
		// a pointer receiver share the base type, so they collide too). Report it
		// and keep the first, so the collision does not silently shadow.
		f.err(method.Position(), "method %s.%s redeclared, previous declaration at %v", recvType.Src(), method.Src(), prev.Name.Position())
		return
	}
	fd := &FuncDeclNode{Name: method, Type: &FunctionType{}}
	if hasSig {
		fd.Type.Signature = f.signature(s.child(), sig)
	}
	td.methods[method.Src()] = fd
}

// methodCallMember reports whether a FactorSuffix or Postfix is a method call
// "x.member(...)" -- exactly one selector, a call, and no index -- and returns
// the member.
func (f *File) methodCallMember(n Node) (member Token, ok bool) {
	selectors, hasCall, disqualify := 0, false, false
	for c := range it(n.ast) {
		switch c.sym {
		case Selector:
			if m, has := f.selectorMember(c); has {
				member, selectors = m, selectors+1
			}
		case Index:
			disqualify = true
		case CallSuffix:
			hasCall = true
		case PostfixOp:
			for pc := range it(c.ast) {
				if pc.sym == CallSuffix {
					hasCall = true
				}
			}
		}
	}
	return member, selectors == 1 && hasCall && !disqualify
}

// checkMethodCall checks a call "head.member(...)" when head is a variable of a
// named type. It reports a member that is no method of the type (and, for a
// struct, no field either -- a field of function type is not modelled, so it is
// accepted); for a real method it checks the argument count and types against
// the method's signature.
func (f *File) checkMethodCall(s *Scope, head, member Token, argList Node) {
	d, ok := s.find(head.Src()).(*VarDeclaration)
	if !ok || !d.typeName.IsValid() {
		return
	}
	td, ok := s.find(d.typeName.Src()).(*TypeDeclaration)
	if !ok {
		return
	}
	fd := td.methods[member.Src()]
	if fd == nil {
		if fields, isStruct := f.structFields(s, d.typeName); isStruct && fields[member.Src()] {
			return
		}
		f.err(member.Position(), "type %s has no method %s", d.typeName.Src(), member.Src())
		return
	}
	if fd.Type == nil {
		return
	}
	var args []Node
	for a := range it(argList.ast) {
		if a.sym == Expression {
			args = append(args, a)
		}
	}
	f.checkArgs(s, member, fd.Type.Signature, args)
}

// methodResultKind returns the predeclared Kind of a method call
// "head.member(...)" when head is a variable of a named type whose method has a
// single known predeclared result -- the method analogue of callResultKind.
func (f *File) methodResultKind(s *Scope, head Token, hasHead bool, suffix Node) (Kind, bool) {
	if !hasHead {
		return 0, false
	}
	member, ok := f.methodCallMember(suffix)
	if !ok {
		return 0, false
	}
	d, ok := s.find(head.Src()).(*VarDeclaration)
	if !ok || !d.typeName.IsValid() {
		return 0, false
	}
	td, ok := s.find(d.typeName.Src()).(*TypeDeclaration)
	if !ok {
		return 0, false
	}
	fd := td.methods[member.Src()]
	if fd == nil || fd.Type == nil {
		return 0, false
	}
	if results := f.flattenResults(s, fd.Type.Signature); len(results) == 1 && results[0].known {
		return results[0].kind, true
	}
	return 0, false
}

// checkNames walks an expression and reports every bare identifier that does not
// resolve to a declaration ("undefined: X"). It descends through operators and
// parentheses but skips a Factor bearing a call/index/selector suffix, whose
// operands and members are not modelled yet.
func (f *File) checkNames(s *Scope, n Node) {
	switch n.sym {
	case Factor:
		f.checkFactorNames(s, n)
	case UnaryExpr:
		f.checkUnaryExpr(s, n)
	case Expression:
		f.checkComparison(s, n)
	case SimpleExpr:
		f.checkBinary(s, n, Term, AddOp)
	case Term:
		f.checkBinary(s, n, UnaryExpr, MulOp)
	}
}

// checkComparison recurses into an Expression's operands and, for each
// relational operator, checks the two operands are comparable: operands of
// different classes are "mismatched types", and an ordering operator ("<" etc.)
// is not defined on bool. A comparison's own result type is bool.
func (f *File) checkComparison(s *Scope, n Node) {
	var operands, relOps []Node
	for c := range it(n.ast) {
		switch c.sym {
		case SimpleExpr:
			f.checkNames(s, c)
			operands = append(operands, c)
		case RelOp:
			relOps = append(relOps, c)
		}
	}
	for i, op := range relOps {
		if i+1 < len(operands) {
			f.checkRelOp(s, op, operands[i], operands[i+1])
		}
	}
}

// checkRelOp reports an incompatible pair of operands of a relational operator.
// The logical operators "&&" and "||" are recognized by the grammar but not
// supported, so any use is rejected here regardless of its operands.
func (f *File) checkRelOp(s *Scope, opNode, lNode, rNode Node) {
	if op := Symbol(f.tok(opNode.Pos()).Ch); op == LAND || op == LOR {
		f.err(f.tok(opNode.Pos()).Position(), "unexpected token '%s'", f.tok(opNode.Pos()).Src())
		return
	}
	lk, lok := f.exprType(s, lNode)
	rk, rok := f.exprType(s, rNode)
	lc, rc := kindCategory(lk), kindCategory(rk)
	if !lok || !rok || lc == catUnknown || rc == catUnknown {
		return
	}
	pos := f.tok(opNode.Pos()).Position()
	if lc != rc {
		f.err(pos, "mismatched types %s and %s", kindName(lk), kindName(rk))
		return
	}
	// Same class: ordering operators are undefined on bool.
	switch Symbol(f.tok(opNode.Pos()).Ch) {
	case EQL, NEQ:
		// equality is defined on every class
	default:
		if lc == catBool {
			f.err(pos, "invalid operation: operator %s not defined on %s", f.tok(opNode.Pos()).Src(), kindName(lk))
		}
	}
}

// checkBinary recurses into a SimpleExpr's or Term's operands and checks each
// binary operator (operandSym is Term/UnaryExpr, opSym is AddOp/MulOp).
func (f *File) checkBinary(s *Scope, n Node, operandSym, opSym Symbol) {
	var operands, ops []Node
	for c := range it(n.ast) {
		switch c.sym {
		case operandSym:
			f.checkNames(s, c)
			operands = append(operands, c)
		case opSym:
			ops = append(ops, c)
		}
	}
	for i, op := range ops {
		if i+1 < len(operands) {
			f.checkBinOp(s, op, operands[i], operands[i+1])
		}
	}
}

// checkBinOp reports operands an arithmetic or bitwise operator is not defined
// for: "+" wants two numeric or two string operands, the rest want numeric.
// Pointer arithmetic ("ptr + 1") is never defined.
func (f *File) checkBinOp(s *Scope, opNode, lNode, rNode Node) {
	if f.exprIsPointer(s, lNode) || f.exprIsPointer(s, rNode) {
		f.err(f.tok(opNode.Pos()).Position(), "invalid operation: operator %s not defined on pointer", f.tok(opNode.Pos()).Src())
		return
	}
	lk, lok := f.exprType(s, lNode)
	rk, rok := f.exprType(s, rNode)
	lc, rc := kindCategory(lk), kindCategory(rk)
	if !lok || !rok || lc == catUnknown || rc == catUnknown {
		return
	}
	var op Symbol
	switch opNode.sym {
	case AddOp:
		op = f.addOp(s, opNode)
	case MulOp:
		op = f.mulOp(s, opNode)
	}
	pos := f.tok(opNode.Pos()).Position()
	sym := f.tok(opNode.Pos()).Src()
	switch {
	case lc != rc:
		f.err(pos, "invalid operation: operator %s not defined on %s and %s", sym, kindName(lk), kindName(rk))
	case !binaryAllowed(op, lc):
		f.err(pos, "invalid operation: operator %s not defined on %s", sym, kindName(lk))
	}
}

// binaryAllowed reports whether a binary operator is defined on operand class c.
// Only "+" accepts strings (concatenation); every other operator wants numbers.
func binaryAllowed(op Symbol, c int) bool {
	if op == ADD {
		return c == catNumeric || c == catString
	}
	return c == catNumeric
}

// checkAssignType reports when the right-hand side of an assignment is of a
// different type class than the target variable. Both must be known.
func (f *File) checkAssignType(s *Scope, lhsTok Token, rhsNode Node) {
	lk, lok := f.identKind(s, lhsTok)
	rk, rok := f.exprType(s, rhsNode)
	lc, rc := kindCategory(lk), kindCategory(rk)
	if !lok || !rok || lc == catUnknown || rc == catUnknown {
		return
	}
	if lc != rc {
		f.err(f.tok(rhsNode.Pos()).Position(), "cannot use %s of type %s as type %s in assignment", f.tok(rhsNode.Pos()).Src(), kindName(rk), kindName(lk))
		return
	}
	// Same type class: a constant assigned to a sized integer variable may still
	// overflow it, e.g. "x = 300" where x is uint8.
	if d, ok := s.find(lhsTok.Src()).(*VarDeclaration); ok && d.hasKind {
		f.checkValueOverflow(s, sizedTarget(d.kind, d.typeName), rhsNode)
	}
}

// checkFieldAssign reports a type mismatch in a field assignment "head.field =
// rhs": the right-hand side's type category must match the struct field's. It is
// the struct-field analogue of checkAssignType.
func (f *File) checkFieldAssign(s *Scope, head, field Token, rhsNode Node) {
	lk, lok := f.fieldKind(s, head, field)
	rk, rok := f.exprType(s, rhsNode)
	lc, rc := kindCategory(lk), kindCategory(rk)
	if !lok || !rok || lc == catUnknown || rc == catUnknown {
		return
	}
	if lc != rc {
		f.err(f.tok(rhsNode.Pos()).Position(), "cannot use %s of type %s as type %s in assignment", f.tok(rhsNode.Pos()).Src(), kindName(rk), kindName(lk))
		return
	}
	// Same category: a constant may still overflow a sized integer field. The
	// field records no type token at the assignment site, so its canonical name
	// is used, as for a ":="-inferred variable.
	f.checkValueOverflow(s, sizedTarget(lk, Token{}), rhsNode)
}

// derefAssignTarget reports the base identifier of a dereference assignment target
// "*base = e". The head must be exactly one "*" applied to a plain identifier (a
// multi-star "**p" or a parenthesized operand is not modelled) and the postfix must
// carry no further selector or index, so the whole target is the single dereference.
func (f *File) derefAssignTarget(head, postfix Node) (base Token, ok bool) {
	if hasSelectorOrIndex(postfix) {
		return Token{}, false
	}
	stars := 0
	for c := range it(head.ast) {
		if c.sym != 0 {
			return Token{}, false
		}
		switch tok := f.tok(c.tok); Symbol(tok.Ch) {
		case MUL:
			stars++
		case IDENT:
			base = tok
		default:
			return Token{}, false
		}
	}
	return base, stars == 1 && base.IsValid()
}

// indexAssignTarget reports the base identifier of an element assignment target
// "base[i] = e": the head is a plain identifier and the postfix carries exactly one
// index and no selector or call, so the target is a single element of base.
func (f *File) indexAssignTarget(head, postfix Node) (base Token, ok bool) {
	id, ok := f.assignHeadIdent(head)
	if !ok {
		return Token{}, false
	}
	indexes, disqualify := 0, false
	for c := range it(postfix.ast) {
		switch c.sym {
		case Index:
			indexes++
		case Selector:
			disqualify = true
		case PostfixOp:
			for pc := range it(c.ast) {
				if pc.sym == CallSuffix {
					disqualify = true
				}
			}
		}
	}
	return id, indexes == 1 && !disqualify
}

// checkDerefAssign checks a dereference assignment target "*base = rhs". The base
// must be a pointer variable; a known scalar cannot be dereferenced ("cannot
// indirect"). When it is a pointer to a predeclared type, the right-hand side must
// be assignable to that pointed-to type. An undefined base is reported here because
// a dereference head carries no plain identifier for the general target loop to see.
func (f *File) checkDerefAssign(s *Scope, base Token, rhsNode Node) {
	if f.blankRead(base) { // "*_ = e" reads "_" as the dereferenced pointer
		return
	}
	d, ok := s.find(base.Src()).(*VarDeclaration)
	if !ok {
		if s.find(base.Src()) == nil && !f.isImportQualifier(s, base.Src()) {
			f.err(base.Position(), "undefined: %s", base.Src())
		}
		return
	}
	if !d.isPtr {
		if _, known := f.identKind(s, base); known { // a known scalar is not a pointer
			f.err(base.Position(), "invalid operation: cannot indirect %s", base.Src())
		}
		return
	}
	if d.hasElemKind {
		f.checkElemAssignType(s, d.elemKind, rhsNode)
	}
}

// checkIndexAssign checks an element assignment target "base[i] = rhs". A scalar
// variable cannot be indexed ("cannot index"). When base is an array or slice of a
// predeclared element type, the right-hand side must be assignable to that element
// type. The base identifier's definedness is already checked by the general target
// loop, since an index head is a plain identifier.
func (f *File) checkIndexAssign(s *Scope, base Token, rhsNode Node) {
	d, ok := s.find(base.Src()).(*VarDeclaration)
	if !ok {
		return
	}
	if _, known := f.identKind(s, base); known { // a scalar variable cannot be indexed
		f.err(base.Position(), "invalid operation: cannot index %s", base.Src())
		return
	}
	if d.hasElemKind && !d.isPtr {
		f.checkElemAssignType(s, d.elemKind, rhsNode)
	}
}

// checkElemAssignType reports a type-category mismatch between the element or
// pointed-to kind of a "*p"/"a[i]" assignment target and the right-hand side. It is
// the analogue of checkAssignType and checkFieldAssign for a kind that is carried by
// no single left-hand variable token.
func (f *File) checkElemAssignType(s *Scope, elem Kind, rhsNode Node) {
	rk, rok := f.exprType(s, rhsNode)
	lc, rc := kindCategory(elem), kindCategory(rk)
	if !rok || lc == catUnknown || rc == catUnknown {
		return
	}
	if lc != rc {
		f.err(f.tok(rhsNode.Pos()).Position(), "cannot use %s of type %s as type %s in assignment", f.tok(rhsNode.Pos()).Src(), kindName(rk), kindName(elem))
		return
	}
	// Same category: a constant may still overflow a sized integer element. The
	// element type is not written at the assignment site, so its canonical name
	// is used, as for a ":="-inferred variable.
	f.checkValueOverflow(s, sizedTarget(elem, Token{}), rhsNode)
}

// checkUnaryExpr resolves the names in a UnaryExpr's factor and checks that each
// unary operator is defined for its operand's type. Operators are applied
// nearest-factor-first (right to left). The address-of and channel operators
// ("&", "<-") are not modelled and stop the type check.
func (f *File) checkUnaryExpr(s *Scope, n Node) {
	var ops []Node
	var fac Node
	facSet := false
	for c := range it(n.ast) {
		switch c.sym {
		case Factor:
			fac, facSet = c, true
			f.checkNames(s, c)
		case UnaryOp:
			ops = append(ops, c)
		}
	}
	if !facSet || len(ops) == 0 {
		return
	}
	// The innermost operator (nearest the factor) applies first. "*" (pointer
	// dereference) and "<-" (channel receive) require the factor to be a pointer
	// or channel respectively; a known value of any other type is an error, and
	// an as-yet-undetermined type is left alone. Their result types are not
	// modelled, so the check stops here in either case.
	switch inner := ops[len(ops)-1]; f.unaryOp(s, inner) {
	case MUL:
		if _, known := f.exprType(s, fac); known && !f.exprIsPointer(s, fac) {
			name := "operand"
			if id, ok := f.exprIdent(fac); ok {
				name = id.Src()
			}
			f.err(f.tok(inner.Pos()).Position(), "invalid operation: cannot indirect %s", name)
		}
		return
	case ARROW:
		if _, _, isChan := f.exprChan(s, fac); !isChan {
			if _, known := f.exprType(s, fac); known {
				f.err(f.tok(inner.Pos()).Position(), "invalid operation: cannot receive from non-channel")
			}
		}
		return
	}
	k, ok := f.exprType(s, fac)
	for i := len(ops) - 1; i >= 0 && ok; i-- {
		k, ok = f.checkUnaryOp(s, ops[i], k)
	}
}

// exprIsPointer reports whether expression n has pointer type. It is
// deliberately shallow: it recognizes a bare variable declared "*T" (the form
// that occurs where a pointer operand must be diagnosed) and leaves every
// other shape to the conservative default of "not known to be a pointer".
func (f *File) exprIsPointer(s *Scope, n Node) bool {
	if id, ok := f.exprIdent(n); ok {
		if d, ok := s.find(id.Src()).(*VarDeclaration); ok {
			return d.isPtr
		}
	}
	return false
}

// chanElemOf reports whether variable d has channel type "chan T" and, if so,
// T's predeclared Kind. It reads the declared type resolved during Phase 3,
// which is where every channel in the language's current use is declared (all
// at package scope).
func (f *File) chanElemOf(s *Scope, d *VarDeclaration) (elem Kind, hasElem, isChan bool) {
	if d == nil || d.VarSpec == nil {
		return 0, false, false
	}
	ct, ok := d.VarSpec.TypeNode.(*TypeNodeChan)
	if !ok {
		return 0, false, false
	}
	elem, hasElem = f.typeKind(s, ct.TypeNode)
	return elem, hasElem, true
}

// exprChan reports whether expression n is a bare channel variable and, if so,
// its element Kind. Like exprIsPointer it is deliberately shallow.
func (f *File) exprChan(s *Scope, n Node) (elem Kind, hasElem, isChan bool) {
	if id, ok := f.exprIdent(n); ok {
		if d, ok := s.find(id.Src()).(*VarDeclaration); ok {
			return f.chanElemOf(s, d)
		}
	}
	return 0, false, false
}

// receiveFactor reports whether expression n is exactly a receive "<-ch" and,
// if so, returns the channel operand's Factor node. It unwraps the single-
// operand expression layers down to the UnaryExpr and requires its sole unary
// operator to be "<-".
func (f *File) receiveFactor(s *Scope, n Node) (Node, bool) {
	for n.sym == Expression || n.sym == SimpleExpr || n.sym == Term {
		var next Node
		found, multi := false, false
		for c := range it(n.ast) {
			switch c.sym {
			case SimpleExpr, Term, UnaryExpr:
				if found {
					multi = true
				}
				next, found = c, true
			case RelOp, AddOp, MulOp:
				multi = true
			}
		}
		if !found || multi {
			return Node{}, false
		}
		n = next
	}
	if n.sym != UnaryExpr {
		return Node{}, false
	}
	var fac Node
	facSet, arrow, ops := false, false, 0
	for c := range it(n.ast) {
		switch c.sym {
		case Factor:
			fac, facSet = c, true
		case UnaryOp:
			ops++
			if f.unaryOp(s, c) == ARROW {
				arrow = true
			}
		}
	}
	if facSet && arrow && ops == 1 {
		return fac, true
	}
	return Node{}, false
}

// checkUnaryOp reports when a unary operator is not defined for operand kind k
// and returns the result kind (ok=false stops further checking).
func (f *File) checkUnaryOp(s *Scope, opNode Node, k Kind) (Kind, bool) {
	op := f.unaryOp(s, opNode)
	pos := f.tok(opNode.Pos()).Position()
	sym := f.tok(opNode.Pos()).Src()
	switch op {
	case NOT:
		if !isBoolKind(k) {
			f.err(pos, "invalid operation: operator %s not defined on %s", sym, kindName(k))
			return 0, false
		}
		return PredeclaredBool, true
	case ADD, SUB, XOR, TILDE:
		if kindCategory(k) != catNumeric {
			f.err(pos, "invalid operation: operator %s not defined on %s", sym, kindName(k))
			return 0, false
		}
		return k, true
	default:
		// "*", "&" and "<-" involve pointer and channel types not modelled yet.
		return 0, false
	}
}

// blankRead reports, and returns true for, a use of the blank identifier as a
// value. "_" binds no variable, so reading it is illegal wherever a name is
// resolved as a value: an expression operand, a call or "go" callee, or the base
// of a "_.f"/"_[i]"/"*_" target. Its legal positions -- a whole "="/":=" target
// (a discard), a declaration name, a blank import -- resolve no value and never
// call this. Go reports the same, distinct from "undefined".
func (f *File) blankRead(tok Token) bool {
	if tok.Src() != "_" {
		return false
	}
	f.err(tok.Position(), "cannot use _ as value or type")
	return true
}

// checkFactorNames resolves a Factor's identifier. A parenthesized expression is
// recursed into; a bare identifier is resolved and reported when undefined; a
// literal or a suffixed identifier (call/index/selector) is left alone.
func (f *File) checkFactorNames(s *Scope, n Node) {
	var id Token
	var suffix Node
	hasID, hasSuffix := false, false
	for c := range it(n.ast) {
		switch c.sym {
		case Expression:
			f.checkNames(s, c)
		case FactorSuffix:
			suffix, hasSuffix = c, true
		case 0:
			if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT {
				id, hasID = tok, true
			}
		}
	}
	// Reading the blank identifier -- as an operand, argument, initializer,
	// condition or return value, whether bare or with a call, selector or index
	// suffix ("_()", "_.f", "_[i]") -- is illegal.
	if hasID && f.blankRead(id) {
		return
	}
	if hasSuffix {
		if argList, direct, isCall := f.callInfo(suffix); isCall {
			f.checkCall(s, id, direct && hasID, argList)
			if !direct && hasID {
				if m, ok := f.methodCallMember(suffix); ok {
					f.checkMethodCall(s, id, m, argList)
				}
			}
		} else if field, ok := f.fieldSelector(suffix); ok && hasID {
			f.checkFieldAccess(s, id, field)
		}
	}
	if hasID && s.find(id.Src()) == nil {
		switch id.Src() {
		case "make", "new":
			// The Go dynamic-allocation builtins have no place on a
			// zero-allocation, no-GC target; reported even in call position,
			// where an ordinary undefined name would be left to the callee check.
			f.err(id.Position(), "dynamic allocation not supported")
		default:
			if !hasSuffix {
				f.err(id.Position(), "undefined: %s", id.Src())
			}
		}
	}
}

// callResultKind returns the type of a suffixed factor when it is a direct call
// to a named function whose sole result is a predeclared type. A selector,
// index, or a call with zero or several results yields an unknown type.
func (f *File) callResultKind(s *Scope, callee Token, hasCallee bool, suffix Node) (Kind, bool) {
	if _, direct, isCall := f.callInfo(suffix); !hasCallee || !direct || !isCall {
		return 0, false
	}
	fd, ok := s.find(callee.Src()).(*FuncDeclaration)
	if !ok || fd.FuncDecl == nil || fd.FuncDecl.Type == nil {
		return 0, false
	}
	if results := f.flattenResults(s, fd.FuncDecl.Type.Signature); len(results) == 1 && results[0].known {
		return results[0].kind, true
	}
	return 0, false
}

// callInfo inspects a FactorSuffix or a Postfix for a call. isCall is true when
// a CallSuffix is present; direct is true when no selector or index precedes it
// (so the operand is itself the callee); argList is the call's ArgumentList
// node (a zero Node for an empty argument list).
func (f *File) callInfo(n Node) (argList Node, direct, isCall bool) {
	direct = true
	for c := range it(n.ast) {
		switch c.sym {
		case Selector, Index:
			direct = false
		case CallSuffix:
			isCall, argList = true, f.callArgList(c)
		case PostfixOp:
			for pc := range it(c.ast) {
				if pc.sym == CallSuffix {
					isCall, argList = true, f.callArgList(pc)
				}
			}
		}
	}
	return argList, direct, isCall
}

// callArgList returns the ArgumentList of a CallSuffix, or a zero Node when the
// call has no arguments.
func (f *File) callArgList(callSuffix Node) (r Node) {
	for c := range it(callSuffix.ast) {
		if c.sym == ArgumentList {
			return c
		}
	}
	return r
}

// checkCall resolves the names in a call's arguments and, for a direct call
// (the callee is a bare name, not a selector or index), checks the callee: an
// unresolved name is reported "undefined", a name resolving to a variable or
// constant is reported "cannot call non-function", and a resolved function's
// parameters are checked against the arguments. A call through a selector or
// index -- a package-qualified call or a method call -- is left to its own checks.
func (f *File) checkCall(s *Scope, callee Token, direct bool, argList Node) {
	var args []Node
	for a := range it(argList.ast) {
		if a.sym == Expression {
			args = append(args, a)
			f.checkNames(s, a)
		}
	}
	if !direct {
		return
	}
	// A call or "go" statement whose callee is the blank identifier ("_()") reads
	// "_" as a value; report that rather than "undefined: _". (An expression-form
	// call is caught earlier, in checkFactorNames.)
	if f.blankRead(callee) {
		return
	}
	switch d := s.find(callee.Src()).(type) {
	case *FuncDeclaration:
		if d.FuncDecl != nil && d.FuncDecl.Type != nil {
			f.checkArgs(s, callee, d.FuncDecl.Type.Signature, args)
		}
	case *VarDeclaration, *ConstDeclaration:
		// The callee is a value, not a function: "x()" where x is a variable or a
		// constant. (A type callee -- "T(x)" -- is an explicit conversion, which
		// the language requires for mixed numeric types and which is left to its
		// own, separate check.)
		f.err(callee.Position(), "cannot call non-function %s", callee.Src())
	case nil:
		// A direct call to a name that resolves to nothing. The predeclared
		// functions (len, cap, append, ...) are not modelled yet -- see the
		// Universe init TODO -- so exempt their names rather than misreport a
		// legitimate builtin call as undefined.
		if !isBuiltinFuncName(callee.Src()) {
			f.err(callee.Position(), "undefined: %s", callee.Src())
		}
	}
}

// isBuiltinFuncName reports whether name is one of Go's predeclared function
// names. OctoGo does not register these in the Universe yet, so a direct call to
// one must not be reported as undefined; registering them, with signatures for
// argument checking, is separate work.
func isBuiltinFuncName(name string) bool {
	switch name {
	case "append", "cap", "clear", "close", "complex", "copy", "delete",
		"imag", "len", "make", "max", "min", "new", "panic", "print",
		"println", "real", "recover":
		return true
	}
	return false
}

// checkArgs checks already name-resolved arguments against a signature: first
// arity, then -- when the count matches -- each argument's type against its
// parameter's. name is the callee/method name used in messages. Conservative:
// an argument whose type is not yet determined (a call, selector, or unresolved
// name) or a parameter of a non-predeclared type is left unchecked.
func (f *File) checkArgs(s *Scope, name Token, sig *SignatureNode, args []Node) {
	params := f.flattenParams(s, sig)
	switch {
	case len(args) < len(params):
		f.err(name.Position(), "not enough arguments in call to %s", name.Src())
	case len(args) > len(params):
		f.err(name.Position(), "too many arguments in call to %s", name.Src())
	default:
		for i, arg := range args {
			p := params[i]
			if !p.known {
				continue
			}
			ak, aok := f.exprType(s, arg)
			if aok && kindCategory(ak) != catUnknown && kindCategory(ak) != kindCategory(p.kind) {
				f.err(f.tok(arg.Pos()).Position(), "cannot use %s of type %s as type %s in argument to %s", f.tok(arg.Pos()).Src(), kindName(ak), p.name, name.Src())
				continue
			}
			// Same type class: a constant argument may still overflow a sized
			// integer parameter, e.g. passing 300 for a uint8 parameter.
			f.checkValueOverflow(s, p, arg)
		}
	}
}

// flattenParams expands a signature's parameters into one retResult per
// parameter (each name in an "IdentifierList Type" group is one parameter),
// mirroring flattenResults.
func (f *File) flattenParams(s *Scope, sig *SignatureNode) (r []retResult) {
	if sig == nil || sig.Params == nil {
		return nil
	}
	for _, p := range sig.Params.List {
		pt := f.resultType(s, p.TypeNode)
		for range len(p.Names) {
			r = append(r, pt)
		}
	}
	return r
}

// exprIdent returns the single identifier of an expression that is exactly a
// bare name, e.g. the "v" of a "v := expr" switch guard.
func (f *File) exprIdent(n Node) (Token, bool) {
	var id Token
	found, extra := false, false
	for c := range it(n.ast) {
		switch c.sym {
		case Expression, SimpleExpr, Term, UnaryExpr, Factor:
			switch t, ok := f.exprIdent(c); {
			case !ok, found:
				extra = true
			default:
				id, found = t, true
			}
		case 0:
			if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT {
				if found {
					extra = true
				}
				id, found = tok, true
			} else {
				extra = true
			}
		default:
			extra = true
		}
	}
	if found && !extra {
		return id, true
	}
	return Token{}, false
}

// SignatureNode describes the Signature production.
//
//	Signature      = "(" [ ParameterList ] ")" [ Type | "(" ParameterList ")" ] .
type SignatureNode struct {
	Params  *ParameterListNode
	Results *ParameterListNode
}

func (f *File) signature(s *Scope, n Node) (r *SignatureNode) {
	r = &SignatureNode{}
	// Signature = "(" [ ParameterList ] ")" [ Type | "(" ParameterList ")" ] .
	// The first ")" separates parameters from results, so a ParameterList seen
	// after it is the result list.
	seenRPar := false
	for n := range it(n.ast) {
		switch n.sym {
		case ParameterList:
			switch {
			case seenRPar:
				r.Results = f.parameterList(s, n)
			default:
				r.Params = f.parameterList(s, n)
			}
		case Type:
			// A single unnamed result: Signature = "(" [...] ")" Type .
			r.Results = &ParameterListNode{List: []ParameterDeclNode{{TypeNode: f.typ(s, n)}}}
		case 0:
			switch f.ch(n.tok) {
			case LPAREN:
				// ok
			case RPAREN:
				seenRPar = true
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// ReceiverNode describes the Receiver production.
//
//	Receiver       = "(" identifier Type ")" .
type ReceiverNode struct {
	Name     Token
	TypeNode TypeNode
}

//TODO func (f *File) receiver(s *Scope, n Node) (r *ReceiverNode) {
//TODO 	r = &ReceiverNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case Type:
//TODO 			r.Type = f.typ(s, n)
//TODO 		case 0:
//TODO 			switch tok := f.tok(n.tok); f.ch(n.tok) {
//TODO 			case IDENT:
//TODO 				r.Name = tok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// BlockNode describes the Block production.
//
//	Block = "{" { Statement ";" } [ Statement ] "}" .
type BlockNode struct {
	List []StatementNode
}

//TODO func (f *File) block(s *Scope, n Node) (r *BlockNode) {
//TODO 	r = &BlockNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case Statement:
//TODO 			r.List = append(r.List, f.statement(s, n))
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case LBRACE, RBRACE, SEMICOLON:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// StatementNode describes the Statement production.
//
//	Statement = VarDecl
//		| ConstDecl
//		| TypeDecl
//		| "if" Expression Block [ "else" Block ]
//		| "for" [ Expression ] Block
//		| "return" [ Expression ]
//		| "go" AssignHead { Selector | Index } CallSuffix
//		| SwitchStmt
//		| SelectStmt
//		| "<-" Expression
//		| AssignHead Postfix
//		| EmptyStatement .
type StatementNode any

// StatementNodeAssignment describes the Statement production case
//
//	AssignHead Postfix
//
// when the PostfixOpNodeExpression.Symbol is ASSIGN.
type StatementNodeAssignment struct {
	AssignHead *AssignHeadNode
	Postfix    *PostfixNode
}

// StatementNodeShortVarDecl describes the Statement production case
//
//	AssignHead Postfix
//
// when the PostfixOpNodeExpression.Symbol is DEFINE.
type StatementNodeShortVarDecl struct {
	AssignHead *AssignHeadNode
	Postfix    *PostfixNode
}

// StatementNodeSend describes the Statement production case
//
//	AssignHead Postfix
//
// when the PostfixOpNodeExpression.Symbol is ARROW
type StatementNodeSend struct {
	AssignHead *AssignHeadNode
	Postfix    *PostfixNode
}

// StatementNodeCall describes the Statement production case
//
//	AssignHead Postfix
//
// when the PostfixOpNode has CallSuffix
type StatementNodeCall struct {
	AssignHead *AssignHeadNode
	Postfix    *PostfixNode
}

// StatementNodeIf describes the Statement production case
//
//	"if" Expression Block [ "else" Block ]
type StatementNodeIf struct {
	Expression ExpressionNode
	Block      *BlockNode
	ElseBlock  *BlockNode
}

// StatementNodeFor describes the Statement production case
//
//	"for" [ Expression ] Block
type StatementNodeFor struct {
	Expression ExpressionNode
	Block      *BlockNode
}

// StatementNodeReturn describes the Statement production case
//
//	"return" [ Expression ]
type StatementNodeReturn struct {
	Expression ExpressionNode
}

// StatementNodeReceive describes the Statement production case
//
//	"<-" Expression
type StatementNodeReceive struct {
	Expression ExpressionNode
}

// StatementNodeGo describes the Statement production case
//
//	"go" AssignHead { Selector | Index } CallSuffix
type StatementNodeGo struct {
	AssignHead *AssignHeadNode
	List       []SelectorOrIndex
	CallSuffix *CallSuffixNode
}

//TODO func (f *File) statement(s *Scope, n Node) (r StatementNode) {
//TODO 	var ah *AssignHeadNode
//TODO 	var ifImplicitBlock *Scope
//TODO 	var goStatement *StatementNodeGo
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case VarDecl:
//TODO 			f.varDecl(s, n)
//TODO 		case AssignHead:
//TODO 			ah = f.assignHead(s, n)
//TODO 		case Postfix:
//TODO 			p := f.postfix(s, n)
//TODO 			switch x := p.PostfixOp.(type) {
//TODO 			case *PostfixOpNodeExpression:
//TODO 				switch x.AssignOp {
//TODO 				case ASSIGN:
//TODO 					r = &StatementNodeAssignment{AssignHead: ah, Postfix: p}
//TODO 				case DEFINE:
//TODO 					//TODO var names []Token
//TODO 					if !ah.isIdentifierOnly() {
//TODO 						f.err(f.pos(n.Pos()), "invalid LHS in short variable declaration")
//TODO 						break
//TODO 					}
//TODO
//TODO 					r = &StatementNodeShortVarDecl{AssignHead: ah, Postfix: p}
//TODO 					//TODO check p.LHSItemList
//TODO 					//TODO declare
//TODO 					//TODO valid := n.End() + 1
//TODO 					//TODO for _, nm := range names {
//TODO 					//TODO 	if err := s.add(&VarDeclaration{declaration: declaration{name: nm, valid: valid}, VarSpec: vs}); err != nil {
//TODO 					//TODO 		f.err(vs.Name.Position(), "%v", err)
//TODO 					//TODO 	}
//TODO 					//TODO }
//TODO 				default:
//TODO 					panic(todo("", origin(1)))
//TODO 				}
//TODO 			case *PostfixOpNodeSend:
//TODO 				r = &StatementNodeSend{AssignHead: ah, Postfix: p}
//TODO 			case *PostfixOpNodeCall:
//TODO 				r = &StatementNodeCall{AssignHead: ah, Postfix: p}
//TODO 			default:
//TODO 				panic(todo("%T", x))
//TODO 			}
//TODO 		case Expression:
//TODO 			expr := f.expression(s, n)
//TODO 			switch x := r.(type) {
//TODO 			case *StatementNodeIf:
//TODO 				x.Expression = expr
//TODO 			case *StatementNodeFor:
//TODO 				x.Expression = expr
//TODO 			case *StatementNodeReturn:
//TODO 				x.Expression = expr
//TODO 			case *StatementNodeReceive:
//TODO 				x.Expression = expr
//TODO 			default:
//TODO 				panic(todo("%T", x))
//TODO 			}
//TODO 		case Block:
//TODO 			b := f.block(s.child(), n)
//TODO 			switch x := r.(type) {
//TODO 			case *StatementNodeIf:
//TODO 				switch {
//TODO 				case x.Block == nil:
//TODO 					x.Block = b
//TODO 				default:
//TODO 					x.ElseBlock = b
//TODO 				}
//TODO 				s = s.Parent
//TODO 			case *StatementNodeFor:
//TODO 				x.Block = b
//TODO 				s = s.Parent
//TODO 			default:
//TODO 				panic(todo("%T", x))
//TODO 			}
//TODO 		case SwitchStmt:
//TODO 			r = f.switchStmt(s.child(), n)
//TODO 		case SelectStmt:
//TODO 			r = f.selectStmt(s.child(), n)
//TODO 		case CallSuffix:
//TODO 			goStatement.CallSuffix = f.callSuffix(s, n)
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case FOR:
//TODO 				s = s.child()
//TODO 				r = &StatementNodeFor{}
//TODO 			case IF:
//TODO 				s = s.child()
//TODO 				ifImplicitBlock = s
//TODO 				r = &StatementNodeIf{}
//TODO 			case ELSE:
//TODO 				s = ifImplicitBlock
//TODO 			case RETURN:
//TODO 				r = &StatementNodeReturn{}
//TODO 			case GO:
//TODO 				goStatement = &StatementNodeGo{}
//TODO 				r = goStatement
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// SelectStmtNode describes the SelectStmt production.
//
//	SelectStmt  = "select" "{" { CommClause } "}" .
type SelectStmtNode struct {
	List []*CommClauseNode
}

//TODO func (f *File) selectStmt(s *Scope, n Node) (r *SelectStmtNode) {
//TODO 	r = &SelectStmtNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case CommClause:
//TODO 			r.List = append(r.List, f.commClause(s, n))
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case SELECT, LBRACE, RBRACE:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// CommClauseNode describes the CommClause production.
//
//	CommClause  = CommHead ":" { Statement ";" } .
type CommClauseNode struct {
	CommHead *CommHeadNode
	List     []StatementNode
}

//TODO func (f *File) commClause(s *Scope, n Node) (r *CommClauseNode) {
//TODO 	r = &CommClauseNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case CommHead:
//TODO 			r.CommHead = f.commHead(s, n)
//TODO 		case Statement:
//TODO 			r.List = append(r.List, f.statement(s, n))
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case COLON, SEMICOLON:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// CommHeadNode describes the CommHead production.
//
//	CommHead    = "case" CommOp | "default" .
type CommHeadNode struct {
	CommOp  CommOpNode
	Default bool
}

//TODO func (f *File) commHead(s *Scope, n Node) (r *CommHeadNode) {
//TODO 	r = &CommHeadNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case CommOp:
//TODO 			r.CommOp = f.commOp(s, n)
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case CASE:
//TODO 				// ok
//TODO 			case DEFAULT:
//TODO 				r.Default = true
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// CommOpNode describes the CommOp production.
//
//	CommOp      = "<-" Expression
//		| AssignHead PostfixComm .
type CommOpNode any

// CommOpNodeReceive describes the CommOp production case
//
//	"<-" Expression
type CommOpNodeReceive struct {
	Expression ExpressionNode
}

// CommOpNodeAssign describes the CommOp production case
//
//	| AssignHead PostfixComm .
type CommOpNodeAssign struct {
	AssignHead  *AssignHeadNode
	PostfixComm *PostfixCommNode
}

//TODO func (f *File) commOp(s *Scope, n Node) (r CommOpNode) {
//TODO 	var comm *CommOpNodeAssign
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case Expression:
//TODO 			r = &CommOpNodeReceive{Expression: f.expression(s, n)}
//TODO 		case AssignHead:
//TODO 			comm = &CommOpNodeAssign{AssignHead: f.assignHead(s, n)}
//TODO 			r = comm
//TODO 		case PostfixComm:
//TODO 			comm.PostfixComm = f.postfixCom(s, n)
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case ARROW:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// PostfixCommNode describes the PostfixComm production.
//
//	PostfixComm = { Selector | Index } ( "=" "<-" Expression | "<-" Expression ) .
type PostfixCommNode struct {
	Assign     Symbol
	Expression ExpressionNode
}

//TODO func (f *File) postfixCom(s *Scope, n Node) (r *PostfixCommNode) {
//TODO 	r = &PostfixCommNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case Expression:
//TODO 			r.Expression = f.expression(s, n)
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case ASSIGN:
//TODO 				r.Assign = ASSIGN
//TODO 			case ARROW:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// SwitchStmtNode describes the SwitchStmt production.
//
//	SwitchStmt = "switch" [ SwitchGuard ] "{" { CaseClause } "}" .
type SwitchStmtNode struct {
	SwitchGuard *SwitchGuardNode
	List        []*CaseClauseNode
}

//TODO func (f *File) switchStmt(s *Scope, n Node) (r *SwitchStmtNode) {
//TODO 	r = &SwitchStmtNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case SwitchGuard:
//TODO 			r.SwitchGuard = f.switchGuard(s, n)
//TODO 		case CaseClause:
//TODO 			r.List = append(r.List, f.caseClause(s, n))
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case SWITCH, LBRACE, RBRACE:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// CaseClauseNode describes the CaseClause production.
//
//	CaseClause = CaseHead ":" { Statement ";" } [ Statement ] .
type CaseClauseNode struct {
	CaseHead *CaseHeadNode
	List     []StatementNode
}

//TODO func (f *File) caseClause(s *Scope, n Node) (r *CaseClauseNode) {
//TODO 	r = &CaseClauseNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case CaseHead:
//TODO 			r.CaseHead = f.caseHead(s, n)
//TODO 		case Statement:
//TODO 			r.List = append(r.List, f.statement(s, n))
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case COLON, SEMICOLON:
//TODO 				//  ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// CaseHeadNode describes the CaseHead production.
//
//	CaseHead   = "case" ExpressionList | "default" .
type CaseHeadNode struct {
	List    []ExpressionNode
	Default bool
}

//TODO func (f *File) caseHead(s *Scope, n Node) (r *CaseHeadNode) {
//TODO 	r = &CaseHeadNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case ExpressionList:
//TODO 			r.List = f.expressionList(s, n)
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case CASE:
//TODO 				// ok
//TODO 			case DEFAULT:
//TODO 				r.Default = true
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

//TODO func (f *File) expressionList(s *Scope, n Node) (r []ExpressionNode) {
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case Expression:
//TODO 			r = append(r, f.expression(s, n))
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case COMMA:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// SwitchGuardNode describes the SwitchGuard production.
//
//	SwitchGuard = Expression [ ":=" Expression ] .
type SwitchGuardNode struct {
	Expression    ExpressionNode
	RHSExpression ExpressionNode
}

//TODO func (f *File) switchGuard(s *Scope, n Node) (r *SwitchGuardNode) {
//TODO 	r = &SwitchGuardNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case Expression:
//TODO 			switch expr := f.expression(s, n); {
//TODO 			case r.Expression == nil:
//TODO 				r.Expression = expr
//TODO 			default:
//TODO 				r.RHSExpression = expr
//TODO 			}
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case DEFINE:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// SelectorOrIndex represents a *SelectorNode or *IndexNode.
type SelectorOrIndex any

// PostfixNode describes the Postfix production.
//
//	Postfix    = { Selector | Index } PostfixOp .
type PostfixNode struct {
	List      []SelectorOrIndex
	PostfixOp PostfixOpNode
}

//TODO func (f *File) postfix(s *Scope, n Node) (r *PostfixNode) {
//TODO 	r = &PostfixNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case PostfixOp:
//TODO 			r.PostfixOp = f.postfixOp(s, n)
//TODO 		case Selector:
//TODO 			r.List = append(r.List, f.selector(s, n))
//TODO 		case Index:
//TODO 			r.List = append(r.List, f.index(s, n))
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// IndexNode describes the Index production.
//
//	Index        = "[" Expression "]" .
type IndexNode struct {
	Expression ExpressionNode
}

//TODO func (f *File) index(s *Scope, n Node) (r *IndexNode) {
//TODO 	r = &IndexNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case Expression:
//TODO 			r.Expression = f.expression(s, n)
//TODO 		case 0:
//TODO 			switch f.ch(n.tok) {
//TODO 			case LBRACK, RBRACK:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// PostfixOpNode describes the PostfixOp production.
//
//	PostfixOp  = CallSuffix
//		| "<-" Expression
//		| { "," LhsItem } ( "=" | ":=" ) Expression .
type PostfixOpNode any

// PostfixOpNodeCall describes the PostfixOp production case
//
//	CallSuffix
type PostfixOpNodeCall struct {
	CallSuffix *CallSuffixNode
}

// PostfixOpNodeExpression describes the PostfixOp production case
//
//	{ "," LhsItem } ( "=" | ":=" ) Expression .
type PostfixOpNodeExpression struct {
	List       []*LHSItemNode
	AssignOp   Symbol
	Expression ExpressionNode
}

// PostfixOpNodeSend describes the PostfixOp production case
//
//	"<-" Expression
type PostfixOpNodeSend struct {
	Expression ExpressionNode
}

//TODO func (f *File) postfixOp(s *Scope, n Node) (r PostfixOpNode) {
//TODO 	var list []*LHSItemNode
//TODO 	var assignOp Symbol
//TODO 	var send *PostfixOpNodeSend
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case CallSuffix:
//TODO 			r = &PostfixOpNodeCall{CallSuffix: f.callSuffix(s, n)}
//TODO 		case LhsItem:
//TODO 			list = append(list, f.lhsItem(s, n))
//TODO 		case Expression:
//TODO 			expr := f.expression(s, n)
//TODO 			switch {
//TODO 			case send != nil:
//TODO 				send.Expression = expr
//TODO 			default:
//TODO 				r = &PostfixOpNodeExpression{List: list, AssignOp: assignOp, Expression: expr}
//TODO 			}
//TODO 		case 0:
//TODO 			switch sym := f.ch(n.tok); sym {
//TODO 			case ASSIGN, DEFINE:
//TODO 				assignOp = sym
//TODO 			case ARROW:
//TODO 				send = &PostfixOpNodeSend{}
//TODO 				r = send
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// LHSItemNode describes the LhsItem production.
//
//	LhsItem    = AssignHead { Selector | Index } .
type LHSItemNode struct {
	AssignHead *AssignHeadNode
	List       []SelectorOrIndex
}

//TODO func (f *File) lhsItem(s *Scope, n Node) (r *LHSItemNode) {
//TODO 	r = &LHSItemNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case AssignHead:
//TODO 			r.AssignHead = f.assignHead(s, n)
//TODO 		case Selector:
//TODO 			r.List = append(r.List, f.selector(s, n))
//TODO 		case Index:
//TODO 			r.List = append(r.List, f.index(s, n))
//TODO 		case 0:
//TODO 			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// AssignHeadNode describes the AssignHead production.
//
//	AssignHead = { "*" } ( identifier | "(" Expression ")" ) .
type AssignHeadNode struct {
	Stars      []Token
	Identifier Token
	Expression ExpressionNode
}

//TODO func (n *AssignHeadNode) isIdentifierOnly() bool {
//TODO 	return len(n.Stars) == 0 || n.Identifier.IsValid()
//TODO }

//TODO func (f *File) assignHead(s *Scope, n Node) (r *AssignHeadNode) {
//TODO 	r = &AssignHeadNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case Expression:
//TODO 			r.Expression = f.expression(s, n)
//TODO 		case 0:
//TODO 			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
//TODO 			case IDENT:
//TODO 				r.Identifier = tok
//TODO 			case MUL:
//TODO 				r.Stars = append(r.Stars, tok)
//TODO 			case LPAREN, RPAREN:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// ParameterDeclNode is one "IdentifierList Type" group of a ParameterList.
type ParameterDeclNode struct {
	Names    []Token
	TypeNode TypeNode
}

// ParameterListNode describes the ParameterList production.
//
//	ParameterList  = IdentifierList Type { "," [ IdentifierList Type ] } .
type ParameterListNode struct {
	List []ParameterDeclNode
}

func (f *File) parameterList(s *Scope, n Node) (r *ParameterListNode) {
	r = &ParameterListNode{}
	var item ParameterDeclNode
	for n := range it(n.ast) {
		switch n.sym {
		case IdentifierList:
			item.Names = f.identifierList(s, n)
		case Type:
			item.TypeNode = f.typ(s, n)
			r.List = append(r.List, item)
			item = ParameterDeclNode{}
		case 0:
			switch f.ch(n.tok) {
			case COMMA:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// TypeSpecNode describes the TypeSpec production.
//
//	TypeSpec = identifier [ "=" ] Type .
type TypeSpecNode struct {
	Name     Token
	TypeNode TypeNode
	gate     gate // cycle-detection state (phase 5)
}

// typeSpecName returns the declared name of a TypeSpec ("identifier [ "=" ]
// Type"), without resolving its body.
func (f *File) typeSpecName(n Node) (name Token) {
	for c := range it(n.ast) {
		if c.sym == 0 {
			if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT {
				return tok
			}
		}
	}
	return name
}

// typeSpecBody resolves a TypeSpec's underlying type into r.TypeNode. It must be
// called after the type name is in scope so that a self-referential body (e.g.
// "type T struct { next *T }") resolves the name rather than reporting it
// undefined.
func (f *File) typeSpecBody(s *Scope, n Node, r *TypeSpecNode) {
	for c := range it(n.ast) {
		switch c.sym {
		case Type:
			r.TypeNode = f.typ(s, c)
		case 0:
			// the identifier and an optional "=" (alias); already handled
		default:
			panic(todo("", f.tok(c.Pos()).Position(), c.sym))
		}
	}
}

func (f *File) declareType(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case TypeSpec:
			// Declare the type name only. Bodies are resolved in a later pass
			// (typeDecl), after every top-level type name is in scope, so that a
			// type may reference any other -- forward, mutual or self.
			ts := &TypeSpecNode{Name: f.typeSpecName(n)}
			var valid int32
			if s.Kind != PackageScope {
				valid = n.End() + 1
			}
			if err := s.add(&TypeDeclaration{declaration: declaration{token: ts.Name, valid: valid}, TypeSpec: ts}); err != nil {
				f.err(ts.Name.Position(), "%v", err)
			}
		case 0:
			switch f.ch(n.tok) {
			case TYPE, LPAREN, SEMICOLON, RPAREN:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

// checkTypeCycles reports every top-level type in this source file whose value
// representation contains itself, directly or through other types, and would
// therefore have infinite size -- forbidden on a target with no heap. It runs
// after all type bodies are resolved (phase 5).
func (f *File) checkTypeCycles(s *Scope, n Node) {
	for tld := range it(n.ast) {
		if tld.sym != TopLevelDecl {
			continue
		}
		for td := range it(tld.ast) {
			if td.sym != TypeDecl {
				continue
			}
			for spec := range it(td.ast) {
				if spec.sym != TypeSpec {
					continue
				}
				if cd, ok := s.find(f.typeSpecName(spec).Src()).(*TypeDeclaration); ok {
					f.checkTypeCycle(s, cd)
				}
			}
		}
	}
}

// checkTypeCycle walks the value-containment edges of a named type. Re-entering
// a type still being walked (gate == resolving) is an invalid recursive type.
func (f *File) checkTypeCycle(s *Scope, cd *TypeDeclaration) {
	ts := cd.TypeSpec
	if ts == nil {
		return
	}
	switch ts.gate.state() {
	case resolved:
		return
	case resolving:
		f.err(ts.Name.Position(), "invalid recursive type %s", ts.Name.Src())
		ts.gate.close()
		return
	}
	ts.gate.open()
	f.walkTypeCycle(s, ts.TypeNode)
	ts.gate.close()
}

// walkTypeCycle follows the edges of tn that contribute to a value's size: a
// named type's underlying type, a struct's fields, and an array's element. A
// pointer, slice, channel or interface is a reference of fixed size and breaks
// the chain, so those are not followed.
func (f *File) walkTypeCycle(s *Scope, tn TypeNode) {
	switch x := tn.(type) {
	case *TypeNodeIdent:
		if cd, ok := s.find(x.Name.Src()).(*TypeDeclaration); ok {
			f.checkTypeCycle(s, cd)
		}
	case *TypeNodeStruct:
		for _, field := range x.Fields {
			f.walkTypeCycle(s, field.TypeNode)
		}
	case *TypeNodeArray:
		f.walkTypeCycle(s, x.TypeNode)
	}
}

// typeDecl resolves the underlying types of a top-level type declaration's
// specs (the second pass of type checking). Every top-level type name is in
// scope by now, so a body may reference any type -- including types declared
// later or the type itself.
func (f *File) typeDecl(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case TypeSpec:
			if td, ok := s.find(f.typeSpecName(n).Src()).(*TypeDeclaration); ok {
				f.typeSpecBody(s, n, td.TypeSpec)
			}
		case 0:
			switch f.ch(n.tok) {
			case TYPE, LPAREN, SEMICOLON, RPAREN:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

// VarDecl = "var" ( VarSpec | "(" { VarSpec ";" } [ VarSpec ] ")" ) .
func (f *File) declareVar(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case VarSpec:
			names, vs := f.declareVarSpec(s, n)
			var valid int32
			if s.Kind != PackageScope {
				valid = n.End() + 1
			}
			for _, nm := range names {
				if err := s.add(&VarDeclaration{declaration: declaration{token: nm, valid: valid}, VarSpec: vs}); err != nil {
					f.err(nm.Position(), "%v", err)
				}
			}
		case 0:
			switch f.ch(n.tok) {
			case VAR, LPAREN, SEMICOLON, RPAREN:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

func (f *File) varDecl(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case VarSpec:
			f.varSpec(s, n)
		case 0:
			switch f.ch(n.tok) {
			case VAR, LPAREN, RPAREN, SEMICOLON:
				// ok (the "(" ... ")" of a grouped "var ( ... )" declaration)
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

// VarSpecNode describes the VarSpec production.
//
//	VarSpec = IdentifierList ( Type [ "=" Expression ] | "=" Expression ) .
type VarSpecNode struct {
	gate
	Expression ExpressionNode
	Name       Token
	TypeNode   TypeNode
}

func (f *File) declareVarSpec(s *Scope, n Node) (names []Token, r *VarSpecNode) {
	r = &VarSpecNode{}
	for n := range it(n.ast) {
		switch n.sym {
		case IdentifierList:
			names = f.identifierList(s, n)
		case Expression:
			if len(names) > 1 {
				f.err(names[1].Position(), "only one variable can be initialized")
			}
		case Type:
			// ok
		case 0:
			switch f.ch(n.tok) {
			case ASSIGN:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return names, r
}

func (f *File) varSpec(s *Scope, n Node) {
	var names []Token
	var varDecls []*VarDeclaration
	var typ TypeNode

	defer func() {
		// Record the resolved type on each declared variable (like a local
		// variable or a parameter) so package-level variables are type-checked:
		// a predeclared Kind, whether it is a pointer, and its named type for
		// field access. typ is nil for an inferred type ("var x = expr").
		kind, hasKind := f.typeKind(s, typ)
		_, isPtr := typ.(*TypeNodePointer)
		typeName, _ := namedTypeToken(typ)
		elemKind, hasElemKind := f.elemTypeKind(s, typ)
		for _, vd := range varDecls {
			if vd == nil {
				continue
			}
			vd.kind, vd.hasKind, vd.isPtr, vd.typeName = kind, hasKind, isPtr, typeName
			vd.elemKind, vd.hasElemKind = elemKind, hasElemKind
			switch vs := vd.VarSpec; vs.gate {
			case resolving:
				vs.TypeNode = typ
				vs.gate.close()
			default:
				panic(todo("", vs.gate))
			}
		}
	}()

	for n := range it(n.ast) {
		switch n.sym {
		case IdentifierList:
			names = f.identifierList(s, n)
			for _, nmTok := range names {
				nm := nmTok.Src()
				switch x := s.Declarations[nm].(type) {
				case nil:
					varDecls = append(varDecls, nil)
					continue
				case *VarDeclaration:
					switch vs := x.VarSpec; vs.gate {
					case unvisited:
						varDecls = append(varDecls, x)
						vs.gate.open()
					default:
						// The name is already declared in this scope: a
						// redeclaration. Report it and skip re-resolving, which
						// would otherwise hit the gate in a non-unvisited state.
						f.err(nmTok.Position(), "%s redeclared in this block, previous declaration at %v", nm, x.Token().Position())
						varDecls = append(varDecls, nil)
					}
				default:
					// The name is already declared in this scope as something
					// other than a variable (a function or type): a
					// redeclaration. Report it and skip, mirroring the
					// *VarDeclaration branch.
					f.err(nmTok.Position(), "%s redeclared in this block, previous declaration at %v", nm, x.Token().Position())
					varDecls = append(varDecls, nil)
				}
			}
		case Type:
			typ = f.typ(s, n)
		case Expression:
			// A global var's initializer is a run-time value evaluated at
			// startup, not necessarily a constant: it may reference another var
			// or call a function. Resolve the names it uses (like a local var's
			// initializer) rather than folding it as a constant, which would
			// wrongly report a non-constant initializer or panic on a call.
			f.checkNames(s, n)
		case 0:
			switch f.ch(n.tok) {
			case ASSIGN:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

// TypeNode describes the Type production.
//
//	Type = [ identifier "." ] identifier
//		| "chan" Type
//		| "[" [ Expression ] "]" Type
//		| "*" Type
//		| InterfaceType
//		| StructType .
type TypeNode interface {
	gater
	Type() Typ
}

// TypeNodeIdent describes the Type production case
//
//	[ identifier "." ] identifier
type TypeNodeIdent struct {
	gate
	Qualifier Token // fmt in fmt.Print
	//TODO-? ResolutionScope *Scope // Identifier appears in ResolutionScope.
	Name  Token // Print in fmt.Print
	Index int32 // Index into the flat []int32 AST of the containing file.
}

// Type implements TypeNode.
func (t *TypeNodeIdent) Type() Typ {
	panic(todo("", origin(1)))
}

// TypeNodeChan describes the Type production case
//
//	| "chan" Type
type TypeNodeChan struct {
	gate
	TypeNode TypeNode // T in chan T
}

// Type implements TypeNode.
func (t *TypeNodeChan) Type() Typ {
	panic(todo("", origin(1)))
}

// TypeNodeArray describes the Type production case
//
//	| "[" Expression "]" Type
type TypeNodeArray struct {
	gate
	Expression ExpressionNode
	TypeNode   TypeNode // T in [expr]T
}

// Type implements TypeNode.
func (t *TypeNodeArray) Type() Typ {
	panic(todo("", origin(1)))
}

// TypeNodeSlice describes the Type production case
//
//	| "[" "]" Type
type TypeNodeSlice struct {
	gate
	TypeNode TypeNode // T in []T
}

// Type implements TypeNode.
func (t *TypeNodeSlice) Type() Typ {
	panic(todo("", origin(1)))
}

// TypeNodePointer describes the Type production case
//
//	| "*" Type
type TypeNodePointer struct {
	gate
	TypeNode TypeNode // T in *T
}

// Type implements TypeNode.
func (t *TypeNodePointer) Type() Typ {
	panic(todo("", origin(1)))
}

// TypeNodeStruct describes the StructType production.
//
//	StructType = "struct" "{" { FieldDecl ";" } [ FieldDecl ] "}" .
type TypeNodeStruct struct {
	gate
	Fields []ParameterDeclNode // each FieldDecl's names and type
}

// Type implements TypeNode.
func (t *TypeNodeStruct) Type() Typ {
	panic(todo("", origin(1)))
}

// MethodSpecNode describes the MethodSpec production.
//
//	MethodSpec = identifier "(" [ ParameterList ] ")" [ Type | "(" ParameterList ")" ] .
type MethodSpecNode struct {
	Name    Token
	Params  *ParameterListNode
	Results *ParameterListNode
}

// TypeNodeInterface describes the InterfaceType production.
//
//	InterfaceType = "interface" "{" { MethodSpec ";" } [ MethodSpec ] "}" .
type TypeNodeInterface struct {
	gate
	Methods []MethodSpecNode
}

// Type implements TypeNode.
func (t *TypeNodeInterface) Type() Typ {
	panic(todo("", origin(1)))
}

// arrayBound evaluates an array length expression n and enforces the P2 array
// constraints: the length must be a compile-time constant ("non-constant array
// bound"), an integer ("invalid array bound", e.g. a string), and non-negative
// ("array bound must be non-negative"). It returns the evaluated expression so
// the caller can record it on the TypeNodeArray. A zero-allocation target has no
// dynamic arrays, so every one of these is a hard error.
func (f *File) arrayBound(s *Scope, n Node) ExpressionNode {
	save := f.inArrayBound
	f.inArrayBound = true
	n0 := len(f.errList)
	e := f.expression(s, n)
	f.inArrayBound = save
	reported := len(f.errList) > n0

	pos := f.tok(n.Pos()).Position()
	cv, _ := e.Value().(untypedConst)
	switch {
	case cv.cv == nil || cv.cv.Kind() == constant.Unknown:
		// A non-constant bound. When factor already reported a more specific
		// cause (an undefined name), do not pile on.
		if !reported {
			f.err(pos, "non-constant array bound")
		}
	case cv.cv.Kind() != constant.Int:
		f.err(pos, "invalid array bound")
	case constant.Sign(cv.cv) < 0:
		f.err(pos, "array bound must be non-negative")
	}
	return e
}

func (f *File) typ(s *Scope, n Node) (r TypeNode) {
	var ident TypeNodeIdent
	for n := range it(n.ast) {
		switch n.sym {
		case Type:
			switch x := r.(type) {
			case *TypeNodeChan:
				x.TypeNode = f.typ(s, n)
			case *TypeNodePointer:
				x.TypeNode = f.typ(s, n)
			case *TypeNodeArray:
				x.TypeNode = f.typ(s, n)
			case *TypeNodeSlice:
				x.TypeNode = f.typ(s, n)
			default:
				panic(todo("%T", x))
			}
		case StructType:
			r = f.structType(s, n)
		case InterfaceType:
			r = f.interfaceType(s, n)
		case Signature:
			// A function type "func" Signature: resolve the signature's parameter
			// and result types (reported here if undefined).
			if ft, ok := r.(*FunctionType); ok {
				ft.Signature = f.signature(s, n)
			}
		case Expression:
			r = &TypeNodeArray{Expression: f.arrayBound(s, n)}
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				switch {
				case ident.Name.IsValid():
					panic(todo("", origin(1)))
					// ident.Qualifier = ident.Name
					// ident.Name = tok
				default:
					nm := tok.Src()
					switch {
					case isFloatTypeName(nm):
						// Reserved but unsupported: OctoGo's zero-allocation
						// hardware target has no floating-point types.
						f.err(tok.Position(), "floating-point types not supported")
					default:
						switch s.find(nm).(type) {
						case *PredeclaredType, *TypeDeclaration:
							ident.Name = tok
							ident.Index = n.tok
							r = &ident
						case nil:
							f.err(tok.Position(), "undefined: %s", nm)
						default:
							f.err(tok.Position(), "%s is not a type", nm)
						}
					}
				}
			case CHAN:
				r = &TypeNodeChan{}
			case FUNC:
				r = &FunctionType{}
			case MUL:
				r = &TypeNodePointer{}
			case LBRACK:
				// ok
			case RBRACK:
				if r == nil {
					r = &TypeNodeSlice{}
				}
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// StructType = "struct" "{" { FieldDecl ";" } [ FieldDecl ] "}" .
func (f *File) structType(s *Scope, n Node) (r *TypeNodeStruct) {
	r = &TypeNodeStruct{}
	for n := range it(n.ast) {
		switch n.sym {
		case FieldDecl:
			r.Fields = append(r.Fields, f.fieldDecl(s, n))
		case 0:
			switch f.ch(n.tok) {
			case STRUCT, LBRACE, RBRACE, SEMICOLON:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}

	// A struct's field names must be unique; the blank identifier may repeat.
	seen := map[string]bool{}
	for _, fld := range r.Fields {
		for _, nm := range fld.Names {
			name := nm.Src()
			if name == "_" {
				continue
			}
			if seen[name] {
				f.err(nm.Position(), "field %s redeclared", name)
				continue
			}
			seen[name] = true
		}
	}
	return r
}

// FieldDecl = "*" [ identifier "." ] identifier
//
//	| identifier [ "." identifier | { "," identifier } Type ] .
func (f *File) fieldDecl(s *Scope, n Node) (r ParameterDeclNode) {
	for n := range it(n.ast) {
		switch n.sym {
		case Type:
			r.TypeNode = f.typ(s, n)
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				r.Names = append(r.Names, tok)
			case COMMA, PERIOD, MUL:
				// ok: multiple names, or an embedded/qualified field
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// InterfaceType = "interface" "{" { MethodSpec ";" } [ MethodSpec ] "}" .
func (f *File) interfaceType(s *Scope, n Node) (r *TypeNodeInterface) {
	r = &TypeNodeInterface{}
	for n := range it(n.ast) {
		switch n.sym {
		case MethodSpec:
			r.Methods = append(r.Methods, f.methodSpec(s, n))
		case 0:
			switch f.ch(n.tok) {
			case INTERFACE, LBRACE, RBRACE, SEMICOLON:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}

	// An interface's method names must be unique.
	seen := map[string]bool{}
	for _, m := range r.Methods {
		if !m.Name.IsValid() {
			continue
		}
		name := m.Name.Src()
		if name == "_" {
			continue
		}
		if seen[name] {
			f.err(m.Name.Position(), "method %s redeclared", name)
			continue
		}
		seen[name] = true
	}
	return r
}

// MethodSpec = identifier "(" [ ParameterList ] ")" [ Type | "(" ParameterList ")" ] .
//
// The signature part mirrors Signature: the first ")" separates parameters from
// results.
func (f *File) methodSpec(s *Scope, n Node) (r MethodSpecNode) {
	seenRPar := false
	for n := range it(n.ast) {
		switch n.sym {
		case ParameterList:
			switch {
			case seenRPar:
				r.Results = f.parameterList(s, n)
			default:
				r.Params = f.parameterList(s, n)
			}
		case Type:
			r.Results = &ParameterListNode{List: []ParameterDeclNode{{TypeNode: f.typ(s, n)}}}
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				r.Name = tok
			case LPAREN:
				// ok
			case RPAREN:
				seenRPar = true
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

func (f *File) identifierList(s *Scope, n Node) (r []Token) {
	for n := range it(n.ast) {
		switch n.sym {
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				r = append(r, tok)
			case COMMA:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// ConstDecl = "const" ( ConstSpec | "(" { ConstSpec ";" } [ ConstSpec ] ")" ) .
func (f *File) declareConst(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case ConstSpec:
			cs := f.declareConstSpec(s, n)
			var valid int32
			if s.Kind != PackageScope {
				valid = n.End() + 1
			}
			if err := s.add(&ConstDeclaration{declaration: declaration{token: cs.Name, valid: valid}, ConstSpec: cs}); err != nil {
				f.err(cs.Name.Position(), "%v", err)
			}
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case CONST, LPAREN, RPAREN, SEMICOLON:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

func (f *File) constDecl(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case ConstSpec:
			f.constSpec(s, n)
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case CONST, LPAREN, RPAREN, SEMICOLON:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
}

// ConstSpecNode describes the ConstSpec production.
//
//	ConstSpec = identifier [ Type ] "=" Expression .
type ConstSpecNode struct {
	Expression ExpressionNode
	Name       Token
	Value      Value
	TypeNode   TypeNode
	node       Node // the ConstSpec AST node, for on-demand evaluation
	gate       gate // evaluation state: order-independence and cycle detection
}

func (f *File) declareConstSpec(s *Scope, n Node) (r *ConstSpecNode) {
	r = &ConstSpecNode{node: n}
	for n := range it(n.ast) {
		switch n.sym {
		case 0:
			switch f.ch(n.tok) {
			case IDENT:
				r.Name = f.tok(n.tok)
			case ASSIGN:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		case Type:
			// A typed const's declared type is resolved later, in constSpec;
			// the declaration pass only binds the name (like declareVarSpec).
		case Expression:
			// ok
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// constSpec drives evaluation of the constant named by a ConstSpec. The actual
// work is on demand (resolveConst) so a constant may reference another declared
// later in source; a name that resolved to something else is a redeclaration,
// already reported.
func (f *File) constSpec(s *Scope, n Node) {
	if cd, ok := s.find(f.constSpecName(n).Src()).(*ConstDeclaration); ok {
		f.resolveConst(s, cd)
	}
}

// constSpecName returns the identifier a ConstSpec declares.
func (f *File) constSpecName(n Node) (name Token) {
	for c := range it(n.ast) {
		if c.sym == 0 {
			if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT {
				return tok
			}
		}
	}
	return name
}

// resolveConst evaluates a constant's value on demand. It is idempotent (a
// second call returns immediately once resolved) and order-independent: a
// reference to a constant declared later triggers that constant's evaluation.
// The gate turns a definition cycle into a reported error and an unknown value
// rather than unbounded recursion.
func (f *File) resolveConst(s *Scope, cd *ConstDeclaration) {
	cs := cd.ConstSpec
	if cs == nil {
		return
	}
	switch cs.gate.state() {
	case resolved:
		return
	case resolving:
		f.err(cs.Name.Position(), "constant definition cycle for %s", cs.Name.Src())
		cs.Value = untypedConst{constant.MakeUnknown()}
		cs.gate.close()
		return
	}
	cs.gate.open()
	var exprPos token.Position
	for c := range it(cs.node.ast) {
		switch c.sym {
		case Expression:
			exprPos = f.tok(c.Pos()).Position()
			cs.Expression = f.expression(s, c)
			cs.Value = f.evalConstExpr(cs.Expression)
		case Type:
			cs.TypeNode = f.typ(s, c)
		}
	}
	if cs.Value == nil {
		cs.Value = untypedConst{constant.MakeUnknown()}
	}
	f.checkConstOverflow(s, cs, exprPos)
	cs.gate.close()
}

// checkConstOverflow reports a typed integer constant whose value does not fit in
// its declared type, e.g. "const x int8 = 200" -> "constant 200 overflows int8".
// The declared type supplies the target range and the source name used in the
// message (so an alias reads "overflows int", not "overflows int32"); a constant
// with no declared type (an untyped constant), a non-integer value, or a
// non-integer target type is not range-checked. pos points at the initializer.
func (f *File) checkConstOverflow(s *Scope, cs *ConstSpecNode, pos token.Position) {
	id, ok := cs.TypeNode.(*TypeNodeIdent)
	if !ok {
		return
	}
	k, ok := f.typeKind(s, cs.TypeNode)
	if !ok {
		return
	}
	uc, ok := cs.Value.(untypedConst)
	if !ok {
		return
	}
	if !pos.IsValid() {
		pos = cs.Name.Position()
	}
	f.reportOverflow(pos, uc.cv, k, id.Name.Src())
}

// checkValueOverflow reports a constant value used where the sized integer type
// dst is required -- a var initializer, an assignment right-hand side, or a call
// argument -- whose value does not fit dst, e.g. "var x int8 = 200" -> "constant
// 200 overflows int8". n is the source value, already name-checked by its caller;
// constValue folds it only to read the value. A non-integer target, a
// non-constant n, or a non-integer constant is left alone.
func (f *File) checkValueOverflow(s *Scope, dst retResult, n Node) {
	if _, _, ok := intKindRange(dst.kind); !ok {
		return
	}
	if cv, ok := f.constValue(s, n); ok {
		f.reportOverflow(f.tok(n.Pos()).Position(), cv, dst.kind, dst.name)
	}
}

// reportOverflow reports "constant CV overflows NAME" when cv is an integer
// constant outside the inclusive range of the sized integer type kind. A
// non-integer kind, or a nil or non-integer cv, is ignored. name is the type as
// written in source.
func (f *File) reportOverflow(pos token.Position, cv constant.Value, kind Kind, name string) {
	lo, hi, ok := intKindRange(kind)
	if !ok || cv == nil || cv.Kind() != constant.Int {
		return
	}
	if constant.Compare(cv, token.LSS, lo) || constant.Compare(cv, token.GTR, hi) {
		f.err(pos, "constant %s overflows %s", cv, name)
	}
}

// constValue folds an already name-checked expression to its integer constant
// value for a range check, returning ok == false when it is not a known integer
// constant (a variable, a call, a receive, or a non-integer or ill-formed
// constant). The fold serves only to read the value: n is analysed by its caller,
// so any diagnostic the fold would add here -- including the "is not a constant"
// the folder emits for a run-time operand, which is legal in these positions --
// is discarded. A file's bodies are checked serially, so trimming its error list
// back is safe.
func (f *File) constValue(s *Scope, n Node) (constant.Value, bool) {
	n0 := len(f.errList)
	e := f.expression(s, n)
	f.errList = f.errList[:n0]
	if e == nil {
		return nil, false
	}
	uc, ok := e.Value().(untypedConst)
	if !ok || uc.cv == nil || uc.cv.Kind() != constant.Int {
		return nil, false
	}
	return uc.cv, true
}

// sizedTarget builds the overflow-report descriptor for a var or assignment
// target of predeclared type kind: its type as written in source (typeName), or,
// for a ":="-inferred variable that records no type token, the type's canonical
// name.
func sizedTarget(kind Kind, typeName Token) retResult {
	name := typeName.Src()
	if name == "" {
		name = sizedKindName(kind)
	}
	return retResult{name: name, kind: kind}
}

// ExpressionNode represents the Expression production or any of its
// constituents.
type ExpressionNode interface {
	Type() Typ
	Value() Value
}

type expression struct {
	typer
	valuer
}

// BinaryExpressionNode represents a binary operation, ie. an operator and its
// two operands, ie. one of
//
//	Expression     = SimpleExpr [ RelOp SimpleExpr ] .
//	SimpleExpr     = Term { AddOp Term } .
//	Term           = UnaryExpr { MulOp UnaryExpr } .
type BinaryExpressionNode struct {
	expression
	LHS ExpressionNode
	Op  Symbol
	RHS ExpressionNode
}

func (f *File) expression(s *Scope, n Node) (r ExpressionNode) {
	var op Symbol
	for n := range it(n.ast) {
		switch n.sym {
		case SimpleExpr:
			switch e := f.simpleExpr(s, n); {
			case r == nil:
				r = e
			default:
				r = f.foldCompare(r, op, e)
			}
		case RelOp:
			op = f.relOp(s, n)
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// relOp returns the operator symbol of a RelOp node.
func (f *File) relOp(s *Scope, n Node) (r Symbol) {
	for n := range it(n.ast) {
		if n.sym == 0 {
			switch sym := f.ch(n.tok); sym {
			case EQL, NEQ, LSS, LEQ, GTR, GEQ, LAND, LOR:
				r = sym
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		}
	}
	return r
}

// relOpTok maps a relational operator symbol to the go/token operator that
// go/constant uses. It returns token.ILLEGAL for the unsupported logical
// operators && and ||, which have no constant meaning.
func relOpTok(op Symbol) token.Token {
	switch op {
	case EQL:
		return token.EQL
	case NEQ:
		return token.NEQ
	case LSS:
		return token.LSS
	case LEQ:
		return token.LEQ
	case GTR:
		return token.GTR
	case GEQ:
		return token.GEQ
	}
	return token.ILLEGAL
}

// canCompareConst reports whether go/constant can evaluate "x op y" for
// operands of kind k: equality is defined for every kind, ordering only for
// numbers and strings (never bool).
func canCompareConst(k constant.Kind, op token.Token) bool {
	if op == token.EQL || op == token.NEQ {
		return true
	}
	return k == constant.Int || k == constant.Float || k == constant.String
}

// foldCompare folds a constant comparison "lhs op rhs" to an untyped boolean
// constant. Non-constant operands, the unsupported && / || operators, operands
// of differing kinds, or an ordering comparison of a kind that does not support
// it all yield an unknown constant, so that constant evaluation never panics.
func (f *File) foldCompare(lhs ExpressionNode, op Symbol, rhs ExpressionNode) ExpressionNode {
	lc, lok := lhs.Value().(untypedConst)
	rc, rok := rhs.Value().(untypedConst)
	if t := relOpTok(op); t != token.ILLEGAL && lok && rok &&
		lc.cv != nil && rc.cv != nil && lc.cv.Kind() == rc.cv.Kind() &&
		lc.cv.Kind() != constant.Unknown && canCompareConst(lc.cv.Kind(), t) {
		return untypedConst{constant.MakeBool(constant.Compare(lc.cv, t, rc.cv))}
	}
	return untypedConst{constant.MakeUnknown()}
}

// binaryOpTok maps an OctoGo binary operator symbol to the go/token operator
// that go/constant uses for constant folding. It returns token.ILLEGAL for a
// symbol it does not handle.
func binaryOpTok(op Symbol) token.Token {
	switch op {
	case ADD:
		return token.ADD
	case SUB:
		return token.SUB
	case MUL:
		return token.MUL
	case QUO:
		return token.QUO
	case AND:
		return token.AND
	case OR:
		return token.OR
	case XOR:
		return token.XOR
	case SHL:
		return token.SHL
	case SHR:
		return token.SHR
	default:
		return token.ILLEGAL
	}
}

// foldBinary evaluates "lhs op rhs". When both operands are untyped constants
// the result is folded to a constant (opTok locates the operator for any
// diagnostic); otherwise a BinaryExpressionNode is returned for later (Phase 4)
// checking.
func (f *File) foldBinary(lhs ExpressionNode, op Symbol, opTok Token, rhs ExpressionNode) ExpressionNode {
	lc, lok := lhs.Value().(untypedConst)
	rc, rok := rhs.Value().(untypedConst)
	if lok && rok && lc.cv != nil && rc.cv != nil {
		if v, ok := f.foldConstBinaryOp(opTok, lc.cv, op, rc.cv); ok {
			return untypedConst{v}
		}
	}
	return &BinaryExpressionNode{LHS: lhs, Op: op, RHS: rhs}
}

// foldConstBinaryOp folds "lhs op rhs" over two constants, reporting a diagnostic
// for a degenerate operation rather than accepting it or crashing: division by
// zero, a negative or too-large shift count, and an operator not defined for the
// operands' kinds (e.g. subtracting strings). A reported operation yields an
// unknown constant so evaluation continues; a subsequent array-bound or const
// check that sees the unknown will not pile on a second, vaguer error. An unknown
// operand -- a value propagating a prior error -- never triggers a report. ok is
// false only when op does not fold to a constant at all, so the caller builds an
// expression node instead.
func (f *File) foldConstBinaryOp(opTok Token, lhs constant.Value, op Symbol, rhs constant.Value) (v constant.Value, ok bool) {
	switch op {
	case QUO:
		// Division of two numbers by a zero divisor. Requiring both operands
		// numeric keeps a non-numeric operand (e.g. "s" / 0) on the general
		// "operator not defined" path below, and skips the report when either is
		// unknown -- an unknown propagates a prior error.
		if isNumericConst(lhs) && isNumericConst(rhs) && constant.Sign(rhs) == 0 {
			f.err(opTok.Position(), "invalid operation: division by zero")
			return constant.MakeUnknown(), true
		}
	case SHL, SHR:
		// constant.Shift requires an integer value and count. An unknown operand
		// propagates a prior error, so leave it unmodelled without a report.
		if lhs.Kind() == constant.Unknown || rhs.Kind() == constant.Unknown {
			return constant.MakeUnknown(), true
		}
		if lhs.Kind() != constant.Int {
			f.reportBadConstOp(opTok, lhs, nil)
			return constant.MakeUnknown(), true
		}
		if rhs.Kind() != constant.Int {
			// A non-integer shift count (e.g. a float): leave the result
			// unmodelled rather than fold.
			return constant.MakeUnknown(), true
		}
		if constant.Sign(rhs) < 0 {
			f.err(opTok.Position(), "invalid operation: negative shift count %s", rhs)
			return constant.MakeUnknown(), true
		}
		n, exact := constant.Uint64Val(rhs)
		if !exact {
			f.err(opTok.Position(), "invalid operation: shift count too large")
			return constant.MakeUnknown(), true
		}
		return constant.Shift(lhs, binaryOpTok(op), uint(n)), true
	}
	t := binaryOpTok(op)
	if t == token.ILLEGAL {
		return nil, false
	}
	return f.constBinaryOp(opTok, lhs, rhs, t), true
}

// constBinaryOp evaluates a non-shift constant binary operation. go/constant
// panics when the operator is not defined for the operands' kinds (subtracting
// strings, a bitwise operator on floats, arithmetic on bools); recover from that
// and report it as a diagnostic, yielding an unknown constant. An unknown operand
// never panics, so a prior error does not produce a spurious report here.
func (f *File) constBinaryOp(opTok Token, lhs, rhs constant.Value, t token.Token) (v constant.Value) {
	defer func() {
		if recover() != nil {
			f.reportBadConstOp(opTok, lhs, rhs)
			v = constant.MakeUnknown()
		}
	}()
	return constant.BinaryOp(lhs, t, rhs)
}

// reportBadConstOp reports that opTok's operator is not defined on the given
// constant operands' kinds. rhs is nil for a unary operator.
func (f *File) reportBadConstOp(opTok Token, lhs, rhs constant.Value) {
	switch {
	case rhs == nil || lhs.Kind() == rhs.Kind():
		f.err(opTok.Position(), "invalid operation: operator %s not defined on %s", opTok.Src(), constDesc(lhs))
	default:
		f.err(opTok.Position(), "invalid operation: operator %s not defined on %s and %s", opTok.Src(), constDesc(lhs), constDesc(rhs))
	}
}

// isNumericConst reports whether cv is an integer or floating-point constant.
func isNumericConst(cv constant.Value) bool {
	switch cv.Kind() {
	case constant.Int, constant.Float:
		return true
	}
	return false
}

// constDesc names an untyped constant's kind for an operator diagnostic.
func constDesc(cv constant.Value) string {
	switch cv.Kind() {
	case constant.Bool:
		return "bool"
	case constant.String:
		return "string"
	case constant.Int:
		return "int"
	case constant.Float:
		return "float"
	default:
		return "untyped constant"
	}
}

//TODO func (f *File) relOp(s *Scope, n Node) (r Symbol) {
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case 0:
//TODO 			switch sym := f.ch(n.tok); sym {
//TODO 			case EQL, NEQ, LSS, LEQ, GTR, GEQ:
//TODO 				r = sym
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

func (f *File) simpleExpr(s *Scope, n Node) (r ExpressionNode) {
	var op Symbol
	var opTok Token
	for n := range it(n.ast) {
		switch n.sym {
		case Term:
			switch e := f.term(s, n); {
			case r == nil:
				r = e
			default:
				r = f.foldBinary(r, op, opTok, e)
			}
		case AddOp:
			op = f.addOp(s, n)
			opTok = f.tok(n.Pos())
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

func (f *File) addOp(s *Scope, n Node) (r Symbol) {
	for n := range it(n.ast) {
		switch n.sym {
		case 0:
			switch sym := f.ch(n.tok); sym {
			case ADD, SUB, OR, XOR:
				r = sym
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// Term        = Factor { Factor } ․
func (f *File) term(s *Scope, n Node) (r ExpressionNode) {
	var op Symbol
	var opTok Token
	for n := range it(n.ast) {
		switch n.sym {
		case UnaryExpr:
			switch e := f.unaryExpr(s, n); {
			case r == nil:
				r = e
			default:
				r = f.foldBinary(r, op, opTok, e)
			}
		case MulOp:
			op = f.mulOp(s, n)
			opTok = f.tok(n.Pos())
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

func (f *File) mulOp(s *Scope, n Node) (r Symbol) {
	for n := range it(n.ast) {
		switch n.sym {
		case 0:
			switch sym := f.ch(n.tok); sym {
			case MUL, QUO, SHL, SHR, AND:
				r = sym
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// UnaryExprNode describes the UnaryExpr production.
//
//	UnaryExpr  = { UnaryOp } Factor .
type UnaryExprNode struct {
	expression
	List   []Symbol
	Factor ExpressionNode
}

func (f *File) unaryExpr(s *Scope, n Node) (r ExpressionNode) {
	var ops []Symbol
	var opToks []Token
	for n := range it(n.ast) {
		switch n.sym {
		case Factor:
			r = f.factor(s, n)
		case UnaryOp:
			ops = append(ops, f.unaryOp(s, n))
			opToks = append(opToks, f.tok(n.Pos()))
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	// A UnaryOp binds tighter the closer it is to the Factor, so apply the
	// operators right to left.
	for i := len(ops) - 1; i >= 0; i-- {
		r = f.foldUnary(ops[i], opToks[i], r)
	}
	return r
}

func (f *File) unaryOp(s *Scope, n Node) (r Symbol) {
	for n := range it(n.ast) {
		switch n.sym {
		case 0:
			switch sym := f.ch(n.tok); sym {
			case ADD, SUB, NOT, XOR, MUL, AND, ARROW, TILDE:
				r = sym
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// foldUnary evaluates a constant unary operation ("+x", "-x", "^x", "!x"). Other
// unary operators (pointer "*"/"&", receive "<-", "~") and non-constant operands
// yield a UnaryExprNode for later (Phase 4) checking.
func (f *File) foldUnary(op Symbol, opTok Token, e ExpressionNode) ExpressionNode {
	if c, ok := e.Value().(untypedConst); ok && c.cv != nil {
		var t token.Token
		switch op {
		case ADD:
			t = token.ADD
		case SUB:
			t = token.SUB
		case XOR:
			t = token.XOR
		case NOT:
			t = token.NOT
		}
		if t != token.ILLEGAL {
			return untypedConst{f.constUnaryOp(opTok, t, c.cv)}
		}
	}
	return &UnaryExprNode{List: []Symbol{op}, Factor: e}
}

// constUnaryOp evaluates a constant unary operation. go/constant panics when the
// operator is not defined for the operand's kind (negating a string,
// complementing a float, "!" on a number); recover from that and report it as a
// diagnostic, yielding an unknown constant. An unknown operand never panics, so a
// prior error does not produce a spurious report here.
func (f *File) constUnaryOp(opTok Token, t token.Token, cv constant.Value) (v constant.Value) {
	defer func() {
		if recover() != nil {
			f.reportBadConstOp(opTok, cv, nil)
			v = constant.MakeUnknown()
		}
	}()
	return constant.UnaryOp(t, cv, 0)
}

//TODO- // FactorNodeIdent describes the Factor production case
//TODO- //
//TODO- //	identifier [ FactorSuffix ]
//TODO- type FactorNodeIdent struct {
//TODO- 	ResolutionScope *Scope // The identifier appears in ResolutionScope.
//TODO- 	Name            Token
//TODO- 	Index           int32 // Index into the flat []int32 AST of the containing file.
//TODO- 	FactorSuffix    *FactorSuffixNode
//TODO- }

// FactorNodeParen describes the Factor production case
//
//	"(" Expression ")"
type FactorNodeParen struct {
	Expression ExpressionNode
}

// FactorNode describes the Factor production.
//
//	Factor     = identifier [ FactorSuffix ]
//		| int_lit
//		| string_lit
//		| rune_lit
//		| "(" Expression ")" .
func (f *File) factor(s *Scope, n Node) (r ExpressionNode) {
	//TODO 	var ident *FactorNodeIdent
	for n := range it(n.ast) {
		switch n.sym {
		case Expression:
			// A parenthesized "( Expression )": its constant value is the value
			// of the inner expression.
			r = f.expression(s, n)
		case FactorSuffix:
			// A call, selector or index applied to the operand: its result is
			// not a compile-time constant. A problematic operand identifier has
			// already been reported above; in an array bound the "non-constant
			// array bound" diagnostic is emitted by arrayBound.
			r = untypedConst{constant.MakeUnknown()}
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case INT:
				if r = (untypedConst{constant.MakeFromLiteral(tok.Src(), token.INT, 0)}); r.Type() == nil {
					f.err(tok.Position(), "invalid integer literal: %s", tok.Src())
				}
			case FLOAT:
				if r = (untypedConst{constant.MakeFromLiteral(tok.Src(), token.FLOAT, 0)}); r.Type() == nil {
					f.err(tok.Position(), "invalid floating-point literal: %s", tok.Src())
				}
			case CHAR:
				if r = (untypedConst{constant.MakeFromLiteral(tok.Src(), token.CHAR, 0)}); r.Type() == nil {
					f.err(tok.Position(), "invalid rune literal: %s", tok.Src())
				}
			case STRING:
				if r = (untypedConst{constant.MakeFromLiteral(tok.Src(), token.STRING, 0)}); r.Type() == nil {
					f.err(tok.Position(), "invalid string literal: %s", tok.Src())
				}
			case IDENT:
				nm := tok.Src()
				switch d := s.find(nm); x := d.(type) {
				case *ConstDeclaration:
					// Evaluate on demand so a forward reference resolves; a cycle
					// is reported there and yields an unknown value.
					f.resolveConst(s, x)
					if x.ConstSpec != nil && x.ConstSpec.Value != nil {
						r = x.ConstSpec.Value.Expr()
					} else {
						r = untypedConst{constant.MakeUnknown()}
					}
				case nil:
					f.err(tok.Position(), "undefined: %s", nm)
					r = untypedConst{constant.MakeUnknown()}
				default:
					// A non-constant name (var, func, type, ...) used where a
					// constant expression is required. In an array bound the
					// contextual "non-constant array bound" diagnostic is emitted by
					// arrayBound instead; in a switch case a non-constant operand is
					// legal (the guard is compared to it at run time), so stay silent
					// in both.
					if !f.inArrayBound && !f.inCaseExpr {
						f.err(tok.Position(), "%s is not a constant", nm)
					}
					r = untypedConst{constant.MakeUnknown()}
				}
			case LPAREN, RPAREN:
				// The delimiters of a parenthesized expression.
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// FactorSuffixNode describes the FactorSuffix production.
//
//	FactorSuffix = { Selector | Index } [ CallSuffix ] .
type FactorSuffixNode struct {
	List       []SelectorOrIndex
	CallSuffix *CallSuffixNode
}

//TODO func (f *File) factorSuffix(s *Scope, n Node) (r *FactorSuffixNode) {
//TODO 	r = &FactorSuffixNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case CallSuffix:
//TODO 			r.CallSuffix = f.callSuffix(s, n)
//TODO 		case Selector:
//TODO 			r.List = append(r.List, f.selector(s, n))
//TODO 		case 0:
//TODO 			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// SelectorNode describes the Selector production.
//
//	Selector     = "." ( identifier | "(" "type" ")" ) .
type SelectorNode struct {
	Name       Token
	TypeSwitch bool
}

//TODO func (f *File) selector(s *Scope, n Node) (r *SelectorNode) {
//TODO 	r = &SelectorNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case 0:
//TODO 			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
//TODO 			case PERIOD, LPAREN, RPAREN:
//TODO 				// ok
//TODO 			case IDENT:
//TODO 				r.Name = tok
//TODO 			case TYPE:
//TODO 				r.TypeSwitch = true
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

// CallSuffixNode describes the CallSuffix production.
//
//	CallSuffix = "(" [ ArgumentList ] ")" .
type CallSuffixNode struct {
	List []ExpressionNode
}

//TODO func (f *File) callSuffix(s *Scope, n Node) (r *CallSuffixNode) {
//TODO 	r = &CallSuffixNode{}
//TODO 	for n := range it(n.ast) {
//TODO 		switch n.sym {
//TODO 		case ArgumentList:
//TODO 			r.List = append(r.List, f.expressionList(s, n))
//TODO 		case 0:
//TODO 			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
//TODO 			case LPAREN, RPAREN:
//TODO 				// ok
//TODO 			default:
//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
//TODO 			}
//TODO 		default:
//TODO 			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
//TODO 		}
//TODO 	}
//TODO 	return r
//TODO }

func (f *File) declareImportDecl(n Node) (r []*ImportSpecNode) {
	for n := range it(n.ast) {
		switch n.sym {
		case ImportSpec:
			is := f.declareImportSpec(n)
			r = append(r, is)
			// A dot import is only recorded here (is.IsDotImport); it is rejected
			// later by the semantic import checks (rejectDotImports), so that a
			// parse-only pass (noDeclarationChecks) does not see the diagnostic,
			// consistent with the other unsupported-feature rejections.
		case 0:
			switch f.ch(n.tok) {
			case IMPORT, LPAREN, RPAREN, SEMICOLON:
				// ok
			default:
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

// ImportSpecNode decribes the ImportSpec production.
//
//	ImportSpec = [ "." | identifier ] string_lit .
type ImportSpecNode struct {
	ImportPath      string
	ImportPathToken Token
	ImportQualifier string
	IsDotImport     bool
	IsStdLib        bool
	resolved        bool // the import path named a package that loaded without error, so an unused-import report is warranted
}

func (f *File) declareImportSpec(n Node) (r *ImportSpecNode) {
	r = &ImportSpecNode{}
	var tok Token
	for n := range it(n.ast) {
		switch n.sym {
		case 0:
			switch f.ch(n.tok) {
			case PERIOD:
				r.IsDotImport = true
			case IDENT:
				tok = f.tok(n.tok)
				r.ImportQualifier = tok.Src()
			case STRING:
				tok = f.tok(n.tok)
				r.ImportPathToken = tok
				var err error
				r.ImportPath, err = strconv.Unquote(tok.Src())
				if err != nil || !isValidImportPath(r.ImportPath) {
					f.err(tok.Position(), "invalid import path: %s", r.ImportPath)
					f.hasInvalidImports = true
					break
				}

				if !r.IsDotImport && r.ImportQualifier == "" {
					if x := strings.LastIndexByte(r.ImportPath, '/'); x > 0 {
						if base := r.ImportPath[x+1:]; token.IsIdentifier(base) {
							r.ImportQualifier = base
						} else {
							f.err(tok.Position(), "invalid package name: %s", r.ImportPath)
							f.hasInvalidImports = true
							break
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
				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	if r.ImportQualifier != "" {
		if err := f.Scope.add(&ImportDeclaration{declaration: declaration{token: tok, name: r.ImportQualifier}, Import: r}); err != nil {
			f.err(tok.Position(), "%v", err)
		}
	}
	return r
}

// Import paths must be slash-separated, entirely lower-case ASCII letters, the
// '_' character c and digits, and must not begin with a "." or "/" or end with
// a "/". Import paths without dots in their first segment are reserved for the
// standard library.
func isValidImportPath(s string) bool {
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return false
	}

	for _, v := range strings.Split(s, "/") {
		for _, c := range v {
			switch {
			case c >= 'a' && c <= 'z' || c == '_' || c >= '0' && c <= '9':
				// ok
			default:
				return false
			}
		}
	}
	return true
}
