// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"fmt"
	"go/token"
	"iter"
	"os"
	"strings"
	"sync"

	"go/constant"
)

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

func iterator(ast []int32) iter.Seq[Node] {
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

// firstIndex recursively traverses the flat AST slice to find the first token index.
// It returns -1 if no token is found.
func firstIndex(ast []int32) int32 {
	for child := range iterator(ast) {
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

func (f *File) pos(tokIndex int32) (r token.Position) {
	if tokIndex >= 0 {
		r = f.tok(tokIndex).Position()
	}
	return r
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

	if tok := r.parser.tok; tok.Ch != rune(EOF) {
		r.parser.sc.AddErr(tok.Position(), "%v: unexpected %v %q", tok.Position(), Symbol(tok.Ch), tok.Src())
		r.Err = r.parser.sc.Err()
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
			case SEMICOLON, EOF:
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
//
//	FuncDecl       = "func" [ Receiver ] identifier "(" [ ParameterList ] ")" [ Type | "(" ParameterList ")" ] [ Block ] .
type FuncDeclNode struct {
	Name          Token
	Receiver      *ReceiverNode
	ParameterList *ParameterListNode
	Type          TypeNode
	ReturnList    *ParameterListNode
	Block         *BlockNode
}

func (f *File) funcDecl(s *Scope, n Node) (r *FuncDeclNode) {
	r = &FuncDeclNode{}
	bs := f.tld.child()
	seenRPar := false
	for n := range iterator(n.ast) {
		switch n.sym {
		case Receiver:
			r.Receiver = f.receiver(s, n) //TODO declare receiver name in bs
		case ParameterList:
			switch {
			case seenRPar:
				r.ReturnList = f.parameterList(bs, n)
			default:
				r.ParameterList = f.parameterList(bs, n)
			}
			//TODO declare in bs
		case Block:
			r.Block = f.block(bs, n)
		case Type:
			r.Type = f.typ(s, n)
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				r.Name = tok
				//TODO declare in s
			case FUNC, LPAREN:
				// ok
			case RPAREN:
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

// ReceiverNode describes the Receiver production.
//
//	Receiver       = "(" identifier Type ")" .
type ReceiverNode struct {
	Name Token
	Type TypeNode
}

func (f *File) receiver(s *Scope, n Node) (r *ReceiverNode) {
	r = &ReceiverNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Type:
			r.Type = f.typ(s, n)
		case 0:
			switch tok := f.tok(n.tok); f.ch(n.tok) {
			case IDENT:
				r.Name = tok
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
//
//	Block = "{" { Statement ";" } [ Statement ] "}" .
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
			case LBRACE, RBRACE, SEMICOLON:
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

func (f *File) statement(s *Scope, n Node) (r StatementNode) {
	var ah *AssignHeadNode
	var ifImplicitBlock *Scope
	var goStatement *StatementNodeGo
	for n := range iterator(n.ast) {
		switch n.sym {
		case VarDecl:
			f.varDecl(s, n)
		case AssignHead:
			ah = f.assignHead(s, n)
		case Postfix:
			p := f.postfix(s, n)
			switch x := p.PostfixOp.(type) {
			case *PostfixOpNodeExpression:
				switch x.AssignOp {
				case ASSIGN:
					r = &StatementNodeAssignment{AssignHead: ah, Postfix: p}
				case DEFINE:
					//TODO var names []Token
					if !ah.isIdentifierOnly() {
						f.err(f.pos(n.Pos()), "invalid LHS in short variable declaration")
						break
					}

					r = &StatementNodeShortVarDecl{AssignHead: ah, Postfix: p}
					//TODO check p.LHSItemList
					//TODO declare
					//TODO valid := n.End() + 1
					//TODO for _, nm := range names {
					//TODO 	if err := s.add(&VarDeclaration{declaration: declaration{name: nm, valid: valid}, VarSpec: vs}); err != nil {
					//TODO 		f.err(vs.Name.Position(), "%v", err)
					//TODO 	}
					//TODO }
				default:
					panic(todo(""))
				}
			case *PostfixOpNodeSend:
				r = &StatementNodeSend{AssignHead: ah, Postfix: p}
			case *PostfixOpNodeCall:
				r = &StatementNodeCall{AssignHead: ah, Postfix: p}
			default:
				panic(todo("%T", x))
			}
		case Expression:
			expr := f.expression(s, n)
			switch x := r.(type) {
			case *StatementNodeIf:
				x.Expression = expr
			case *StatementNodeFor:
				x.Expression = expr
			case *StatementNodeReturn:
				x.Expression = expr
			case *StatementNodeReceive:
				x.Expression = expr
			default:
				panic(todo("%T", x))
			}
		case Block:
			b := f.block(s.child(), n)
			switch x := r.(type) {
			case *StatementNodeIf:
				switch {
				case x.Block == nil:
					x.Block = b
				default:
					x.ElseBlock = b
				}
				s = s.Parent
			case *StatementNodeFor:
				x.Block = b
				s = s.Parent
			default:
				panic(todo("%T", x))
			}
		case SwitchStmt:
			r = f.switchStmt(s.child(), n)
		case SelectStmt:
			r = f.selectStmt(s.child(), n)
		case CallSuffix:
			goStatement.CallSuffix = f.callSuffix(s, n)
		case 0:
			switch f.ch(n.tok) {
			case FOR:
				s = s.child()
				r = &StatementNodeFor{}
			case IF:
				s = s.child()
				ifImplicitBlock = s
				r = &StatementNodeIf{}
			case ELSE:
				s = ifImplicitBlock
			case RETURN:
				r = &StatementNodeReturn{}
			case GO:
				goStatement = &StatementNodeGo{}
				r = goStatement
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// SelectStmtNode describes the SelectStmt production.
//
//	SelectStmt  = "select" "{" { CommClause } "}" .
type SelectStmtNode struct {
	List []*CommClauseNode
}

func (f *File) selectStmt(s *Scope, n Node) (r *SelectStmtNode) {
	r = &SelectStmtNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case CommClause:
			r.List = append(r.List, f.commClause(s, n))
		case 0:
			switch f.ch(n.tok) {
			case SELECT, LBRACE, RBRACE:
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

// CommClauseNode describes the CommClause production.
//
//	CommClause  = CommHead ":" { Statement ";" } .
type CommClauseNode struct {
	CommHead *CommHeadNode
	List     []StatementNode
}

func (f *File) commClause(s *Scope, n Node) (r *CommClauseNode) {
	r = &CommClauseNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case CommHead:
			r.CommHead = f.commHead(s, n)
		case Statement:
			r.List = append(r.List, f.statement(s, n))
		case 0:
			switch f.ch(n.tok) {
			case COLON, SEMICOLON:
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

// CommHeadNode describes the CommHead production.
//
//	CommHead    = "case" CommOp | "default" .
type CommHeadNode struct {
	CommOp  CommOpNode
	Default bool
}

func (f *File) commHead(s *Scope, n Node) (r *CommHeadNode) {
	r = &CommHeadNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case CommOp:
			r.CommOp = f.commOp(s, n)
		case 0:
			switch f.ch(n.tok) {
			case CASE:
				// ok
			case DEFAULT:
				r.Default = true
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

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

func (f *File) commOp(s *Scope, n Node) (r CommOpNode) {
	var comm *CommOpNodeAssign
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			r = &CommOpNodeReceive{Expression: f.expression(s, n)}
		case AssignHead:
			comm = &CommOpNodeAssign{AssignHead: f.assignHead(s, n)}
			r = comm
		case PostfixComm:
			comm.PostfixComm = f.postfixCom(s, n)
		case 0:
			switch f.ch(n.tok) {
			case ARROW:
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

// PostfixCommNode describes the PostfixComm production.
//
//	PostfixComm = { Selector | Index } ( "=" "<-" Expression | "<-" Expression ) .
type PostfixCommNode struct {
	Assign     Symbol
	Expression ExpressionNode
}

func (f *File) postfixCom(s *Scope, n Node) (r *PostfixCommNode) {
	r = &PostfixCommNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			r.Expression = f.expression(s, n)
		case 0:
			switch f.ch(n.tok) {
			case ASSIGN:
				r.Assign = ASSIGN
			case ARROW:
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

// SwitchStmtNode describes the SwitchStmt production.
//
//	SwitchStmt = "switch" [ SwitchGuard ] "{" { CaseClause } "}" .
type SwitchStmtNode struct {
	SwitchGuard *SwitchGuardNode
	List        []*CaseClauseNode
}

func (f *File) switchStmt(s *Scope, n Node) (r *SwitchStmtNode) {
	r = &SwitchStmtNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case SwitchGuard:
			r.SwitchGuard = f.switchGuard(s, n)
		case CaseClause:
			r.List = append(r.List, f.caseClause(s, n))
		case 0:
			switch f.ch(n.tok) {
			case SWITCH, LBRACE, RBRACE:
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

// CaseClauseNode describes the CaseClause production.
//
//	CaseClause = CaseHead ":" { Statement ";" } [ Statement ] .
type CaseClauseNode struct {
	CaseHead *CaseHeadNode
	List     []StatementNode
}

func (f *File) caseClause(s *Scope, n Node) (r *CaseClauseNode) {
	r = &CaseClauseNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case CaseHead:
			r.CaseHead = f.caseHead(s, n)
		case Statement:
			r.List = append(r.List, f.statement(s, n))
		case 0:
			switch f.ch(n.tok) {
			case COLON, SEMICOLON:
				//  ok
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// CaseHeadNode describes the CaseHead production.
//
//	CaseHead   = "case" ExpressionList | "default" .
type CaseHeadNode struct {
	List    []ExpressionNode
	Default bool
}

func (f *File) caseHead(s *Scope, n Node) (r *CaseHeadNode) {
	r = &CaseHeadNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case ExpressionList:
			r.List = f.expressionList(s, n)
		case 0:
			switch f.ch(n.tok) {
			case CASE:
				// ok
			case DEFAULT:
				r.Default = true
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

func (f *File) expressionList(s *Scope, n Node) (r []ExpressionNode) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			r = append(r, f.expression(s, n))
		case 0:
			switch f.ch(n.tok) {
			case COMMA:
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

// SwitchGuardNode describes the SwitchGuard production.
//
//	SwitchGuard = Expression [ ":=" Expression ] .
type SwitchGuardNode struct {
	Expression    ExpressionNode
	RHSExpression ExpressionNode
}

func (f *File) switchGuard(s *Scope, n Node) (r *SwitchGuardNode) {
	r = &SwitchGuardNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			switch expr := f.expression(s, n); {
			case r.Expression == nil:
				r.Expression = expr
			default:
				r.RHSExpression = expr
			}
		case 0:
			switch f.ch(n.tok) {
			case DEFINE:
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

// SelectorOrIndex represents a *SelectorNode or *IndexNode.
type SelectorOrIndex any

// PostfixNode describes the Postfix production.
//
//	Postfix    = { Selector | Index } PostfixOp .
type PostfixNode struct {
	List      []SelectorOrIndex
	PostfixOp PostfixOpNode
}

func (f *File) postfix(s *Scope, n Node) (r *PostfixNode) {
	r = &PostfixNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case PostfixOp:
			r.PostfixOp = f.postfixOp(s, n)
		case Selector:
			r.List = append(r.List, f.selector(s, n))
		case Index:
			r.List = append(r.List, f.index(s, n))
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

// IndexNode describes the Index production.
//
//	Index        = "[" Expression "]" .
type IndexNode struct {
	Expression ExpressionNode
}

func (f *File) index(s *Scope, n Node) (r *IndexNode) {
	r = &IndexNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			r.Expression = f.expression(s, n)
		case 0:
			switch f.ch(n.tok) {
			case LBRACK, RBRACK:
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

func (f *File) postfixOp(s *Scope, n Node) (r PostfixOpNode) {
	var list []*LHSItemNode
	var assignOp Symbol
	var send *PostfixOpNodeSend
	for n := range iterator(n.ast) {
		switch n.sym {
		case CallSuffix:
			r = &PostfixOpNodeCall{CallSuffix: f.callSuffix(s, n)}
		case LhsItem:
			list = append(list, f.lhsItem(s, n))
		case Expression:
			expr := f.expression(s, n)
			switch {
			case send != nil:
				send.Expression = expr
			default:
				r = &PostfixOpNodeExpression{List: list, AssignOp: assignOp, Expression: expr}
			}
		case 0:
			switch sym := f.ch(n.tok); sym {
			case ASSIGN, DEFINE:
				assignOp = sym
			case ARROW:
				send = &PostfixOpNodeSend{}
				r = send
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// LHSItemNode describes the LhsItem production.
//
//	LhsItem    = AssignHead { Selector | Index } .
type LHSItemNode struct {
	AssignHead *AssignHeadNode
	List       []SelectorOrIndex
}

func (f *File) lhsItem(s *Scope, n Node) (r *LHSItemNode) {
	r = &LHSItemNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case AssignHead:
			r.AssignHead = f.assignHead(s, n)
		case Selector:
			r.List = append(r.List, f.selector(s, n))
		case Index:
			r.List = append(r.List, f.index(s, n))
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

// AssignHeadNode describes the AssignHead production.
//
//	AssignHead = { "*" } ( identifier | "(" Expression ")" ) .
type AssignHeadNode struct {
	Stars      []Token
	Identifier Token
	Expression ExpressionNode
}

func (n *AssignHeadNode) isIdentifierOnly() bool {
	return len(n.Stars) == 0 || n.Identifier.IsValid()
}

func (f *File) assignHead(s *Scope, n Node) (r *AssignHeadNode) {
	r = &AssignHeadNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			r.Expression = f.expression(s, n)
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				r.Identifier = tok
			case MUL:
				r.Stars = append(r.Stars, tok)
			case LPAREN, RPAREN:
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

// ParameterListNode describes the ParameterList production.
//
//	ParameterList  = IdentifierList Type { "," [ IdentifierList Type ] } .
type ParameterListNode struct {
	List []struct {
		Names []Token
		Type  TypeNode
	}
}

func (f *File) parameterList(s *Scope, n Node) (r *ParameterListNode) {
	r = &ParameterListNode{}
	var item struct {
		Names []Token
		Type  TypeNode
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
			case COMMA:
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
				valid = n.End() + 1
			}
			for _, nm := range names {
				if err := s.add(&VarDeclaration{declaration: declaration{name: nm, valid: valid}, VarSpec: vs}); err != nil {
					f.err(vs.Name.Position(), "%v", err)
				}
			}
		case 0:
			switch f.ch(n.tok) {
			case VAR, LPAREN, SEMICOLON, RPAREN:
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
//
//	VarSpec = IdentifierList ( Type [ "=" Expression ] | "=" Expression ) .
type VarSpecNode struct {
	Expression ExpressionNode
	Name       Token
	TypeNode   TypeNode
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
			case ASSIGN:
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
//
//	Type = [ identifier "." ] identifier
//		| "chan" Type
//		| "[" [ Expression ] "]" Type
//		| "*" Type
//		| InterfaceType
//		| StructType .
type TypeNode any

// TypeNodeIdent describes the Type production case
//
//	[ identifier "." ] identifier
type TypeNodeIdent struct {
	Qualifier       Token  // foo in fmt.Print
	ResolutionScope *Scope // Identifier appears in ResolutionScope.
	Name            Token  // Print in fmt.Print
	Index           int32  // Index into the flat []int32 AST of the containing file.
}

// TypeNodeChan describes the Type production case
//
//	| "chan" Type
type TypeNodeChan struct {
	Type TypeNode
}

// TypeNodeArray describes the Type production case
//
//	| "[" Expression "]" Type
type TypeNodeArray struct {
	Expression ExpressionNode
	Type       TypeNode
}

// TypeNodeSlice describes the Type production case
//
//	| "[" "]" Type
type TypeNodeSlice struct {
	Type TypeNode
}

func (f *File) typ(s *Scope, n Node) (r TypeNode) {
	var ident TypeNodeIdent
	for n := range iterator(n.ast) {
		switch n.sym {
		case Type:
			switch x := r.(type) {
			case *TypeNodeChan:
				x.Type = f.typ(s, n)
			case *TypeNodeArray:
				x.Type = f.typ(s, n)
			case *TypeNodeSlice:
				x.Type = f.typ(s, n)
			default:
				panic(todo("%T", x))
			}
		case Expression:
			r = &TypeNodeArray{Expression: f.expression(s, n)}
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				switch {
				case ident.Name.IsValid():
					ident.Qualifier = ident.Name
					ident.Name = tok
				default:
					ident = TypeNodeIdent{ResolutionScope: s, Name: tok, Index: n.tok}
					r = &ident
				}
			case CHAN:
				r = &TypeNodeChan{}
			case LBRACK:
				// ok
			case RBRACK:
				if r == nil {
					r = &TypeNodeSlice{}
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

func (f *File) identifierList(s *Scope, n Node) (r []Token) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case IDENT:
				r = append(r, tok)
			case COMMA:
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
				valid = n.End() + 1
			}
			if err := s.add(&ConstDeclaration{declaration: declaration{name: cs.Name, valid: valid}, ConstSpec: cs}); err != nil {
				f.err(cs.Name.Position(), "%v", err)
			}
		case 0:
			switch f.ch(n.tok) {
			case CONST /* , LPAREN, RPAREN, SEMICOLON */ :
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
//
//	ConstSpec = identifier [ Type ] "=" Expression .
type ConstSpecNode struct {
	Expression ExpressionNode
	Name       Token
	Type       TypeNode
}

func (f *File) constSpec(s *Scope, n Node) (r *ConstSpecNode) {
	r = &ConstSpecNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			r.Expression = f.expression(s, n)
		case Type:
			r.Type = f.typ(s, n)
		case 0:
			switch f.ch(n.tok) {
			case IDENT:
				r.Name = f.tok(n.tok)
			case ASSIGN:
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

// ExpressionNode represents the Expression production or any of its
// constituents.
type ExpressionNode any

// BinaryExpressionNode represents a binary operation, ie. an operator and its
// two operands, ie. one of
//
//	Expression     = SimpleExpr [ RelOp SimpleExpr ] .
//	SimpleExpr     = Term { AddOp Term } .
//	Term           = UnaryExpr { MulOp UnaryExpr } .
type BinaryExpressionNode struct {
	LHS ExpressionNode
	Op  Symbol
	RHS ExpressionNode
}

func (f *File) expression(s *Scope, n Node) (r ExpressionNode) {
	var op Symbol
	for n := range iterator(n.ast) {
		switch n.sym {
		case SimpleExpr:
			switch e := f.simpleExpr(s, n); {
			case r == nil:
				r = e
			default:
				r = &BinaryExpressionNode{LHS: r, Op: op, RHS: e}
			}
		case RelOp:
			op = f.relOp(s, n)
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

func (f *File) relOp(s *Scope, n Node) (r Symbol) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch sym := f.ch(n.tok); sym {
			case EQL, NEQ, LSS, LEQ, GTR, GEQ:
				r = sym
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
	var op Symbol
	for n := range iterator(n.ast) {
		switch n.sym {
		case Term:
			switch e := f.term(s, n); {
			case r == nil:
				r = e
			default:
				r = &BinaryExpressionNode{LHS: r, Op: op, RHS: e}
			}
		case AddOp:
			op = f.addOp(s, n)
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

func (f *File) addOp(s *Scope, n Node) (r Symbol) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch sym := f.ch(n.tok); sym {
			case ADD, SUB, OR, XOR:
				r = sym
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
	var op Symbol
	for n := range iterator(n.ast) {
		switch n.sym {
		case UnaryExpr:
			switch e := f.unaryExpr(s, n); {
			case r == nil:
				r = e
			default:
				r = &BinaryExpressionNode{LHS: r, Op: op, RHS: e}
			}
		case MulOp:
			op = f.mulOp(s, n)
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

func (f *File) mulOp(s *Scope, n Node) (r Symbol) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch sym := f.ch(n.tok); sym {
			case MUL, QUO, SHL, SHR, AND:
				r = sym
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// UnaryExprNode describes the UnaryExpr production.
//
//	UnaryExpr  = { UnaryOp } Factor .
type UnaryExprNode struct {
	List   []Symbol
	Factor ExpressionNode
}

func (f *File) unaryExpr(s *Scope, n Node) (r ExpressionNode) {
	var ue *UnaryExprNode
	for n := range iterator(n.ast) {
		switch n.sym {
		case Factor:
			fa := f.factor(s, n)
			switch {
			case ue != nil:
				ue.Factor = fa
			default:
				r = fa
			}
		case UnaryOp:
			if ue == nil {
				ue = &UnaryExprNode{}
				r = ue
			}
			ue.List = append(ue.List, f.unaryOp(s, n))
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

func (f *File) unaryOp(s *Scope, n Node) (r Symbol) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch sym := f.ch(n.tok); sym {
			case ADD, SUB, NOT, XOR, MUL, AND, ARROW, TILDE:
				r = sym
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// FactorNode describes the Factor production.
//
//	Factor     = identifier [ FactorSuffix ]
//		| int_lit
//		| string_lit
//		| rune_lit
//		| "(" Expression ")" .
type FactorNode any

// FactorNodeIdent describes the Factor production case
//
//	identifier [ FactorSuffix ]
type FactorNodeIdent struct {
	ResolutionScope *Scope // The identifier appears in ResolutionScope.
	Name            Token
	Index           int32 // Index into the flat []int32 AST of the containing file.
	FactorSuffix    *FactorSuffixNode
}

// FactorNodeParen describes the Factor production case
//
//	"(" Expression ")"
type FactorNodeParen struct {
	Expression ExpressionNode
}

func (f *File) factor(s *Scope, n Node) (r ExpressionNode) {
	var ident *FactorNodeIdent
	for n := range iterator(n.ast) {
		switch n.sym {
		case Expression:
			r = &FactorNodeParen{Expression: f.expression(s, n)}
		case FactorSuffix:
			ident.FactorSuffix = f.factorSuffix(s, n)
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case INT:
				if r = constant.MakeFromLiteral(tok.Src(), token.INT, 0); r == constant.Unknown {
					f.err(tok.Position(), "invalid integer literal: %s", tok.Src())
				}
			case IDENT:
				ident = &FactorNodeIdent{ResolutionScope: s, Name: tok, Index: n.tok}
				r = ident
			case LPAREN, RPAREN:
				// ok
			case STRING:
				if r = constant.MakeFromLiteral(tok.Src(), token.STRING, 0); r == constant.Unknown {
					f.err(tok.Position(), "invalid string literal: %s", tok.Src())
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

// FactorSuffixNode describes the FactorSuffix production.
//
//	FactorSuffix = { Selector | Index } [ CallSuffix ] .
type FactorSuffixNode struct {
	List       []SelectorOrIndex
	CallSuffix *CallSuffixNode
}

func (f *File) factorSuffix(s *Scope, n Node) (r *FactorSuffixNode) {
	r = &FactorSuffixNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case CallSuffix:
			r.CallSuffix = f.callSuffix(s, n)
		case Selector:
			r.List = append(r.List, f.selector(s, n))
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

// SelectorNode describes the Selector production.
//
//	Selector     = "." ( identifier | "(" "type" ")" ) .
type SelectorNode struct {
	Name       Token
	TypeSwitch bool
}

func (f *File) selector(s *Scope, n Node) (r *SelectorNode) {
	r = &SelectorNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case PERIOD, LPAREN, RPAREN:
				// ok
			case IDENT:
				r.Name = tok
			case TYPE:
				r.TypeSwitch = true
			default:
				panic(todo("", f.tok(n.tok), f.ch(n.tok)))
			}
		default:
			panic(todo("", n.sym))
		}
	}
	return r
}

// CallSuffixNode describes the CallSuffix production.
//
//	CallSuffix = "(" [ ArgumentList ] ")" .
type CallSuffixNode struct {
	List []ExpressionNode
}

func (f *File) callSuffix(s *Scope, n Node) (r *CallSuffixNode) {
	r = &CallSuffixNode{}
	for n := range iterator(n.ast) {
		switch n.sym {
		case ArgumentList:
			r.List = append(r.List, f.expressionList(s, n))
		case 0:
			switch tok := f.tok(n.tok); Symbol(tok.Ch) {
			case LPAREN, RPAREN:
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

func (f *File) importDecl(n Node) (r []*ImportSpecNode) {
	for n := range iterator(n.ast) {
		switch n.sym {
		case ImportSpec:
			r = append(r, f.importSpec(n))
		case 0:
			switch f.ch(n.tok) {
			case IMPORT, LPAREN, RPAREN, SEMICOLON:
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
//
//	ImportSpec = [ "." | identifier ] string_lit .
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
			case PERIOD:
				r.IsDotImport = true
			case IDENT:
				nm = f.tok(n.tok)
				r.ImportQualifier = nm.Src()
			case STRING:
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
		if err := f.FileScope.add(&ImportDeclaration{declaration: declaration{name: nm}, Import: r}); err != nil {
			f.err(nm.Position(), "%v", err)
		}
	}
	return r
}
