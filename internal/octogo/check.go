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
	inArrayBound      bool // evaluating an array length: suppress "is not a constant"
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
		case TypeDecl:
			// Type names and bodies are handled in declareTopLevel/declareType;
			// deep type resolution (Typ, recursion checks) is not implemented yet.
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
	block := f.tld.child()
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
			//		case Receiver:
			//			r.Receiver = f.receiver(s, n) //TODO declare receiver name in bs
			//		case ParameterList:
			//			switch {
			//			case seenRPar:
			//				r.ReturnList = f.parameterList(bs, n)
			//			default:
			//				r.ParameterList = f.parameterList(bs, n)
			//			}
			//			//TODO declare in bs
			//		case Block:
			//			r.Block = f.block(bs, n)
			//		case Type:
			//			r.Type = f.typ(s, n)
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
						panic(todo("", fd.gate))
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
	var results []retResult
	for n := range it(n.ast) {
		switch n.sym {
		case Signature:
			sig := f.signature(fs, n)
			f.declareParams(fs, sig)
			results = f.flattenResults(fs, sig)
		case Block:
			f.checkBlock(fs, results, n)
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

// declareParams declares a signature's parameter names in scope s.
func (f *File) declareParams(s *Scope, sig *SignatureNode) {
	if sig.Params == nil {
		return
	}
	for _, p := range sig.Params.List {
		for _, nm := range p.Names {
			if err := s.add(&VarDeclaration{declaration: declaration{token: nm}}); err != nil {
				f.err(nm.Position(), "%v", err)
			}
		}
	}
}

// checkBlock walks the statements of a block. The caller provides the scope: a
// function body shares its parameter scope; a nested block gets a child scope.
// results carries the enclosing function's result types for return checking.
func (f *File) checkBlock(s *Scope, results []retResult, n Node) {
	for n := range it(n.ast) {
		if n.sym == Statement {
			f.checkStatement(s, results, n)
		}
	}
}

// checkStatement handles the statement forms Phase 4 currently understands:
// local variable/constant declarations (reporting redeclarations), return
// statements (operand arity and, for literal operands, result type), and nested
// blocks (if/for bodies, in a child scope). Other forms are not yet checked.
func (f *File) checkStatement(s *Scope, results []retResult, stmt Node) {
	var head Node
	isReturn := false
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
			case IF, FOR:
				condKw = f.tok(c.tok).Src()
			}
		}
	}
	if isReturn {
		f.checkReturn(results, stmt)
	}
}

// checkReturn verifies a return statement's operand count against the enclosing
// function's results and, for literal operands returned to a predeclared result
// type, that the literal is assignable.
func (f *File) checkReturn(results []retResult, stmt Node) {
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
		f.checkReturnValue(results[i], e)
	}
}

// checkReturnValue reports a type mismatch when a literal return operand is not
// assignable to its (predeclared) result type. Non-literal operands and
// non-predeclared results are left unchecked pending typed expression support.
func (f *File) checkReturnValue(rt retResult, e Node) {
	tok, ok := f.bareLiteral(e)
	if !ok || !rt.known {
		return
	}
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
		facSet := false
		for c := range it(n.ast) {
			switch c.sym {
			case Factor:
				fac, facSet = c, true
			case UnaryOp:
				switch f.unaryOp(s, c) {
				case NOT:
					return UntypedBool, true
				case ARROW:
					return 0, false
				}
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
	var paren Node
	var hasLit, hasParen, hasSuffix bool
	for c := range it(n.ast) {
		switch c.sym {
		case Expression:
			paren, hasParen = c, true
		case FactorSuffix:
			hasSuffix = true
		case 0:
			lit, hasLit = f.tok(c.tok), true
		}
	}
	switch {
	case hasSuffix:
		return 0, false // call/index/selector result
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

// checkSwitch walks a switch statement in its own implicit block scope: it
// determines the guard's type, declares a "v := expr" guard variable (visible in
// the clauses but not after the switch), checks each case expression is
// comparable to the guard, and walks each clause body in a nested scope.
func (f *File) checkSwitch(s *Scope, results []retResult, n Node) {
	ss := s.child()
	var guardKind Kind
	guardOK := false
	// SwitchGuard precedes the CaseClauses, so the guard is processed first.
	for c := range it(n.ast) {
		switch c.sym {
		case SwitchGuard:
			guardKind, guardOK = f.switchGuardType(s, c)
			f.declareSwitchGuardVar(ss, guardKind, guardOK, c)
		case CaseClause:
			if guardOK {
				f.checkCaseExprs(ss, guardKind, c)
			}
			f.checkClauseBody(ss.child(), results, c)
		}
	}
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

// switchGuardType resolves the type a switch compares against: the right-hand
// side of a "v := expr" guard, or the sole guard expression otherwise.
func (f *File) switchGuardType(s *Scope, n Node) (Kind, bool) {
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
	case hasDefine && len(exprs) >= 2:
		return f.exprType(s, exprs[1])
	case len(exprs) >= 1:
		return f.exprType(s, exprs[0])
	}
	return 0, false
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
// own block scope. The communication operations are not checked yet.
func (f *File) checkSelect(s *Scope, results []retResult, n Node) {
	for n := range it(n.ast) {
		if n.sym == CommClause {
			f.checkClauseBody(s.child(), results, n)
		}
	}
}

// checkClauseBody walks the statement body of a case or comm clause in scope s.
func (f *File) checkClauseBody(s *Scope, results []retResult, n Node) {
	for n := range it(n.ast) {
		if n.sym == Statement {
			f.checkStatement(s, results, n)
		}
	}
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
		var kind Kind
		var hasKind, hasInit, isPtr bool
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
		}
		for _, nm := range names {
			if err := s.add(&VarDeclaration{declaration: declaration{token: nm}, kind: kind, hasKind: hasKind, isPtr: isPtr}); err != nil {
				f.err(nm.Position(), "%v", err)
			}
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

// checkAssignment handles a "AssignHead Postfix" statement. Only ":=" introduces
// variables: its plainly-named left-hand operands that are not already declared
// in the current scope are declared here, and it is an error if none of them is
// new (Go short variable declaration semantics). Plain assignments, sends and
// calls declare nothing.
func (f *File) checkAssignment(s *Scope, head, postfix Node) {
	f.checkSelectors(s, head, postfix)

	var lhs []Token
	if id, ok := f.assignHeadIdent(head); ok {
		lhs = append(lhs, id)
	}

	var op Symbol
	var rhs []Node
	for n := range it(postfix.ast) {
		if n.sym != PostfixOp {
			continue
		}
		for n := range it(n.ast) {
			switch n.sym {
			case LhsItem:
				for n := range it(n.ast) {
					if n.sym == AssignHead {
						if id, ok := f.assignHeadIdent(n); ok {
							lhs = append(lhs, id)
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

	newCount, nonBlank := 0, 0
	for _, id := range lhs {
		nm := id.Src()
		if nm == "_" {
			continue
		}
		nonBlank++
		if s.Declarations[nm] != nil {
			continue // already declared in this scope: an assignment, not new
		}
		_ = s.add(&VarDeclaration{declaration: declaration{token: id}})
		newCount++
	}
	if nonBlank != 0 && newCount == 0 {
		f.err(f.tok(head.Pos()).Position(), "no new variables on left side of :=")
	}
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
	if !ok || !f.isImportQualifier(s, id.Src()) {
		return
	}
	for c := range it(postfix.ast) {
		if c.sym != Selector {
			continue
		}
		if m, ok := f.selectorMember(c); ok && !token.IsExported(m.Src()) {
			f.err(m.Position(), "cannot refer to unexported name %s.%s", id.Src(), m.Src())
		}
		return
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
	if !lok || !rok || lc == catUnknown || rc == catUnknown || lc == rc {
		return
	}
	f.err(f.tok(rhsNode.Pos()).Position(), "cannot use %s of type %s as type %s in assignment", f.tok(rhsNode.Pos()).Src(), kindName(rk), kindName(lk))
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

// checkFactorNames resolves a Factor's identifier. A parenthesized expression is
// recursed into; a bare identifier is resolved and reported when undefined; a
// literal or a suffixed identifier (call/index/selector) is left alone.
func (f *File) checkFactorNames(s *Scope, n Node) {
	var id Token
	hasID, hasSuffix := false, false
	for c := range it(n.ast) {
		switch c.sym {
		case Expression:
			f.checkNames(s, c)
		case FactorSuffix:
			hasSuffix = true
		case 0:
			if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT {
				id, hasID = tok, true
			}
		}
	}
	if hasID && !hasSuffix && s.find(id.Src()) == nil {
		f.err(id.Position(), "undefined: %s", id.Src())
	}
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
}

func (f *File) typeSpec(s *Scope, n Node) (r *TypeSpecNode) {
	r = &TypeSpecNode{}
	for n := range it(n.ast) {
		switch n.sym {
		case Type:
			r.TypeNode = f.typ(s, n)
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				r.Name = tok
			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

func (f *File) declareType(s *Scope, n Node) {
	for n := range it(n.ast) {
		switch n.sym {
		case TypeSpec:
			ts := f.typeSpec(s, n)
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
			case VAR:
				// ok
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
		for _, vd := range varDecls {
			if vd == nil {
				continue
			}
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
					panic(todo("%p.%v[%q]==%T", s, s.Kind, nm, x))
				}
			}
		case Type:
			typ = f.typ(s, n)
		case Expression:
			e := f.expression(s, n)
			if len(varDecls) != 0 && varDecls[0] != nil {
				varDecls[0].VarSpec.Expression = e
			}
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
			//TODO 			case *TypeNodeSlice:
			//TODO 				x.Type = f.typ(s, n)
			default:
				panic(todo("%T", x))
			}
		case StructType:
			r = f.structType(s, n)
		case InterfaceType:
			r = f.interfaceType(s, n)
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
}

func (f *File) declareConstSpec(s *Scope, n Node) (r *ConstSpecNode) {
	r = &ConstSpecNode{}
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
		case Expression:
			// ok
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
}

func (f *File) constSpec(s *Scope, n Node) {
	var cs *ConstSpecNode
	for n := range it(n.ast) {
		switch n.sym {
		case Expression:
			cs.Expression = f.expression(s, n)
			switch {
			case cs.TypeNode != nil:
				panic(todo("", origin(1)))
			default:
				cs.Value = f.evalConstExpr(cs.Expression)
			}
		//TODO 		case Type:
		//TODO 			r.Type = f.typ(s, n)
		case 0:
			switch f.ch(n.tok) {
			case IDENT:
				name := f.tok(n.tok)
				d := s.find(name.Src())
				if cd, ok := d.(*ConstDeclaration); ok {
					cs = cd.ConstSpec
				}
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
				panic(todo("", f.tok(n.Pos()).Position(), n.sym))
				r = &BinaryExpressionNode{LHS: r, Op: op, RHS: e}
			}
		//TODO 		case RelOp:
		//TODO 			op = f.relOp(s, n)
		//TODO 		case 0:
		//TODO 			switch f.ch(n.tok) {
		//TODO 			default:
		//TODO 				panic(todo("", f.tok(n.tok).Position(), f.ch(n.tok)))
		//TODO 			}
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	return r
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
// the result is folded to a constant; otherwise a BinaryExpressionNode is
// returned for later (Phase 4) checking.
func (f *File) foldBinary(lhs ExpressionNode, op Symbol, rhs ExpressionNode) ExpressionNode {
	lc, lok := lhs.Value().(untypedConst)
	rc, rok := rhs.Value().(untypedConst)
	if lok && rok && lc.cv != nil && rc.cv != nil {
		switch op {
		case SHL, SHR:
			if n, ok := constant.Uint64Val(rc.cv); ok {
				return untypedConst{constant.Shift(lc.cv, binaryOpTok(op), uint(n))}
			}
		default:
			if t := binaryOpTok(op); t != token.ILLEGAL {
				return untypedConst{constant.BinaryOp(lc.cv, t, rc.cv)}
			}
		}
	}
	return &BinaryExpressionNode{LHS: lhs, Op: op, RHS: rhs}
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
	for n := range it(n.ast) {
		switch n.sym {
		case Term:
			switch e := f.term(s, n); {
			case r == nil:
				r = e
			default:
				r = f.foldBinary(r, op, e)
			}
		case AddOp:
			op = f.addOp(s, n)
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
	for n := range it(n.ast) {
		switch n.sym {
		case UnaryExpr:
			switch e := f.unaryExpr(s, n); {
			case r == nil:
				r = e
			default:
				r = f.foldBinary(r, op, e)
			}
		case MulOp:
			op = f.mulOp(s, n)
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
	for n := range it(n.ast) {
		switch n.sym {
		case Factor:
			r = f.factor(s, n)
		case UnaryOp:
			ops = append(ops, f.unaryOp(s, n))
		default:
			panic(todo("", f.tok(n.Pos()).Position(), n.sym))
		}
	}
	// A UnaryOp binds tighter the closer it is to the Factor, so apply the
	// operators right to left.
	for i := len(ops) - 1; i >= 0; i-- {
		r = f.foldUnary(ops[i], r)
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
func (f *File) foldUnary(op Symbol, e ExpressionNode) ExpressionNode {
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
			return untypedConst{constant.UnaryOp(t, c.cv, 0)}
		}
	}
	return &UnaryExprNode{List: []Symbol{op}, Factor: e}
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
		//TODO 		case Expression:
		//TODO 			r = &FactorNodeParen{Expression: f.expression(s, n)}
		//TODO 		case FactorSuffix:
		//TODO 			ident.FactorSuffix = f.factorSuffix(s, n)
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
					r = x.ConstSpec.Value.Expr()
				case nil:
					f.err(tok.Position(), "undefined: %s", nm)
					r = untypedConst{constant.MakeUnknown()}
				default:
					// A non-constant name (var, func, type, ...) used where a
					// constant expression is required. In an array bound the
					// contextual "non-constant array bound" diagnostic is emitted
					// by arrayBound instead, so stay silent here.
					if !f.inArrayBound {
						f.err(tok.Position(), "%s is not a constant", nm)
					}
					r = untypedConst{constant.MakeUnknown()}
				}
			//TODO 			case LPAREN, RPAREN:
			//TODO 				// ok
			//TODO 			case STRING:
			//TODO 				if r = constant.MakeFromLiteral(tok.Src(), token.STRING, 0); r == constant.Unknown {
			//TODO 					f.err(tok.Position(), "invalid string literal: %s", tok.Src())
			//TODO 				}
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
			if is.IsDotImport {
				p := f.Package.importPkg(is.ImportPathToken, is.ImportPath)
				for k, v := range p.Scope.Declarations {
					panic(todo("%q: %T", k, v))
				}
			}
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
