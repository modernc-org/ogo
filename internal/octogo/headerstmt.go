// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import "slices"

// The init statement of an "if", of a "switch" and of a "for", and a for's post
// statement, are SIMPLE STATEMENTS in Go: beside the declaration and the
// assignment a header is usually written with, an expression standing alone --
// a call, a receive --, a send, and a step. The grammar reads a header as an
// expression first and decides by what follows it, so a statement standing
// there is not in the tree as the statement it is: `if two(); ok` is the
// expression `two()` and an IfInit of a ";" and a condition.
//
// headerStmt puts it back. What it answers with is the statement as the parser
// gives it on a line of its own, an AssignHead and its Postfix, made of the
// header's own tokens, and whatever checks, scans or lowers a statement is
// handed that. So a header's call means what the statement means, in every
// pass, and none of them has a rule of its own for it.
//
// Until 2026-09-29 the if took no such statement and the switch refused one; and
// the for, which took a call, read its second ";" as it reads the first, so that
// `for one(); n < 2; n++` had the condition for its init and no condition: built
// for the target without a word, it never made the call and never ended.

// headerKind is what a header's statement is, where it is none of the forms
// with a rule of their own -- the declaration, the assignment, and the step of
// an if and a switch.
type headerKind int

const (
	headerNone headerKind = iota
	headerExpr            // an expression standing alone: `two()`, `<-ch`
	headerSend            // `ch <- v`
	headerStep            // `n++`, `n--`, `x *= 2`
)

// headerTail classifies what follows a header's leading expression -- the
// children of an IfInit, of a SwitchGuard past its first, of a ForRest, of a
// ForPost past its first -- and answers with the nodes of the statement the two
// make: none for an expression standing alone, the arrow and the value of a
// send, the operator and the value of a step. semi says the tail began with a
// ";", which makes the expression a statement of its own.
func (f *File) headerTail(kids []Node) (kind headerKind, tail []Node) {
	if len(kids) == 0 {
		return headerNone, nil
	}
	first := kids[0]
	switch {
	case first.sym == AssignOp && len(kids) >= 2 && kids[1].sym == Expression:
		return headerStep, kids[:2]
	case first.sym != 0:
		return headerNone, nil
	}
	switch f.ch(first.tok) {
	case SEMICOLON:
		return headerExpr, nil
	case ARROW:
		if len(kids) >= 2 && kids[1].sym == Expression {
			return headerSend, kids[:2]
		}
	case INC, DEC:
		return headerStep, kids[:1]
	}
	return headerNone, nil
}

// headerStmt answers with the statement the expression head and the tail after
// it are, as a Statement node. ok is false for an expression that is no
// statement -- `x + 1`, a literal -- which the caller reports. The statement is
// built once for a header: what the checker records of its parts, by their place
// in the tree, is what the emitter finds there.
func (f *File) headerStmt(head Node, tail []Node) (stmt Node, ok bool) {
	if len(head.ast) == 0 {
		return Node{}, false
	}
	key := &head.ast[0]
	if st, found := f.headerStmts[key]; found {
		return st, st.sym != 0
	}
	if f.headerStmts == nil {
		f.headerStmts = map[*int32]Node{}
	}
	if ast, built := f.buildHeaderStmt(head, tail); built {
		stmt = Node{sym: Statement, ast: ast}
	}
	f.headerStmts[key] = stmt
	return stmt, stmt.sym != 0
}

// rawNode encodes a production of the given children.
func rawNode(sym Symbol, kids ...[]int32) []int32 {
	r := []int32{-int32(sym), 0}
	for _, k := range kids {
		r = append(r, k...)
	}
	r[1] = int32(len(r) - 2)
	return r
}

// rawOf encodes a Node as it stands in a tree.
func rawOf(n Node) []int32 {
	if n.sym == 0 {
		return []int32{n.tok}
	}
	return rawNode(n.sym, n.ast)
}

// buildHeaderStmt is headerStmt without its memory.
func (f *File) buildHeaderStmt(head Node, tail []Node) ([]int32, bool) {
	// The expression is one operand: a level with an operator in it is a value
	// nothing reads.
	n := head
	for _, want := range []Symbol{SimpleExpr, Term, UnaryExpr} {
		kids := slices.Collect(it(n.ast))
		if len(kids) != 1 || kids[0].sym != want {
			return nil, false
		}
		n = kids[0]
	}
	var ops []Node
	var factor Node
	for c := range it(n.ast) {
		switch c.sym {
		case UnaryOp:
			ops = append(ops, c)
		case Factor:
			factor = c
		default:
			return nil, false
		}
	}
	if factor.sym == 0 {
		return nil, false
	}
	opTok := func(op Node) (int32, bool) {
		for c := range it(op.ast) {
			if c.sym == 0 {
				return c.tok, true
			}
		}
		return 0, false
	}
	// A receive standing alone, `<-ch`: the arrow and what it receives from.
	if len(ops) != 0 && len(tail) == 0 {
		if tok, isTok := opTok(ops[0]); isTok && f.ch(tok) == ARROW {
			var unary [][]int32
			for _, op := range ops[1:] {
				unary = append(unary, rawOf(op))
			}
			unary = append(unary, rawOf(factor))
			operand := rawNode(Expression, rawNode(SimpleExpr, rawNode(Term, rawNode(UnaryExpr, unary...))))
			return append([]int32{tok}, operand...), true
		}
	}
	// Anything else begins as a statement does: stars, then a name or a
	// parenthesised expression, then the steps from it.
	var headKids []int32
	for _, op := range ops {
		tok, isTok := opTok(op)
		if !isTok || f.ch(tok) != MUL {
			return nil, false
		}
		headKids = append(headKids, tok)
	}
	fk := slices.Collect(it(factor.ast))
	if len(fk) == 0 {
		return nil, false
	}
	var suffix []Node
	var lit []int32
	switch {
	case fk[0].sym == 0 && f.ch(fk[0].tok) == IDENT:
		headKids = append(headKids, fk[0].tok)
		suffix = fk[1:]
	case len(fk) >= 3 && fk[0].sym == 0 && f.ch(fk[0].tok) == LPAREN && fk[1].sym == Expression &&
		fk[2].sym == 0 && f.ch(fk[2].tok) == RPAREN:
		if len(fk) == 3 && len(ops) == 0 && len(tail) == 0 {
			// `(two())` and `(<-ch)`: a call and a receive may stand in
			// parentheses, and are the statement without them.
			return f.buildHeaderStmt(fk[1], nil)
		}
		headKids = append(headKids, fk[0].tok)
		headKids = append(headKids, rawOf(fk[1])...)
		headKids = append(headKids, fk[2].tok)
		suffix = fk[3:]
	case fk[0].sym == FuncLiteral && len(ops) == 0 && len(tail) == 0:
		// A literal called where it stands, `func() { ... }()`.
		lit = rawOf(fk[0])
		suffix = fk[1:]
	default:
		return nil, false
	}
	var steps []int32
	for _, s := range suffix {
		if s.sym != FactorSuffix {
			return nil, false
		}
		for c := range it(s.ast) {
			steps = append(steps, rawOf(c)...)
		}
	}
	if lit != nil {
		return append(lit, steps...), true
	}
	if len(tail) != 0 {
		var op []int32
		for _, t := range tail {
			op = append(op, rawOf(t)...)
		}
		steps = append(steps, rawNode(PostfixOp, op)...)
	}
	if len(steps) == 0 {
		return rawNode(AssignHead, headKids), true // as the parser writes a head alone
	}
	return append(rawNode(AssignHead, headKids), rawNode(Postfix, steps)...), true
}

// parenStmt answers with the statement a statement written in parentheses is,
// `(two())` or `(<-ch)`: Go allows a call and a receive to stand so. ok is false
// for any other statement.
func (f *File) parenStmt(stmt Node) (Node, bool) {
	// The parser writes no Postfix where a head has nothing after it.
	kids := slices.Collect(it(stmt.ast))
	switch {
	case len(kids) == 1 && kids[0].sym == AssignHead:
	case len(kids) == 2 && kids[0].sym == AssignHead && kids[1].sym == Postfix && len(kids[1].ast) == 0:
	default:
		return Node{}, false
	}
	hk := slices.Collect(it(kids[0].ast))
	if len(hk) != 3 || hk[0].sym != 0 || f.ch(hk[0].tok) != LPAREN || hk[1].sym != Expression {
		return Node{}, false
	}
	return f.headerStmt(hk[1], nil)
}

// headerBindingsIn answers with the statement a header DECLARES or ASSIGNS by,
// `if p := r; ...`, `if p, n = r, 1; ...`, `switch v := f(); ...`, as the statement
// the same words are on a line of their own, for the passes that read a body's
// statements by shape -- the summaries above all. Those read no header's binding,
// so `func keep(r *T) { if p := r; p != nil { g = p } }` was summarised as keeping
// nothing, and `keep(&t)` left a local's address in a package variable in silence.
// The checker and the emitter lower a header's declaration by a rule of their own,
// and are not handed these.
func (f *File) headerBindingsIn(n Node) []Node {
	var head Node
	var init []Node
	switch n.sym {
	case IfStmt:
		for k := range it(n.ast) {
			switch {
			case k.sym == Expression && head.sym == 0:
				head = k
			case k.sym == IfInit && head.sym != 0:
				init = slices.Collect(it(k.ast))
			}
		}
	case SwitchGuard:
		kids := slices.Collect(it(n.ast))
		if len(kids) < 2 || kids[0].sym != Expression {
			return nil
		}
		head, init = kids[0], kids[1:]
	default:
		return nil
	}
	if head.sym == 0 || len(init) == 0 {
		return nil
	}
	key := &head.ast[0]
	if st, found := f.headerBindings[key]; found {
		if st.sym == 0 {
			return nil
		}
		return []Node{st}
	}
	if f.headerBindings == nil {
		f.headerBindings = map[*int32]Node{}
	}
	var st Node
	if tail, ok := f.bindingTail(init); ok {
		if ast, built := f.buildHeaderStmt(head, tail); built {
			st = Node{sym: Statement, ast: ast}
		}
	}
	f.headerBindings[key] = st
	if st.sym == 0 {
		return nil
	}
	return []Node{st}
}

// bindingTail is the tail of a statement's PostfixOp for a header's declaration or
// assignment -- the further targets, the operator and the values -- read off the
// header's init up to its ";" or its tag, with the values in the ExpressionList a
// statement writes them in. ok is false for any other init.
func (f *File) bindingTail(init []Node) (tail []Node, ok bool) {
	var values []int32
	op := -1
	for i, k := range init {
		if k.sym == SwitchTag || k.sym == 0 && f.ch(k.tok) == SEMICOLON {
			break
		}
		switch {
		case op < 0 && k.sym == 0 && (f.ch(k.tok) == DEFINE || f.ch(k.tok) == ASSIGN):
			op = i
			tail = append(tail, k)
		case op < 0:
			tail = append(tail, k) // "," and the further LhsItems
		default:
			values = append(values, rawOf(k)...) // the values and the "," between them
		}
	}
	if op < 0 || len(values) == 0 {
		return nil, false
	}
	return append(tail, Node{sym: ExpressionList, ast: values}), true
}

// headerStmtsIn answers with the statements standing alone in the header n is
// the production of -- an IfStmt, a SwitchGuard, a ForHeader or a ForPost -- for
// a pass that reads statements: an if's init, a switch's, a for's init and its
// post. None for a header with none, and for any other production.
func (f *File) headerStmtsIn(n Node) (out []Node) {
	add := func(head Node, tail []Node) {
		if st, ok := f.headerStmt(head, tail); ok {
			out = append(out, st)
		}
	}
	kids := slices.Collect(it(n.ast))
	switch n.sym {
	case IfStmt:
		var head Node
		for _, k := range kids {
			switch {
			case k.sym == Expression && head.sym == 0:
				head = k
			case k.sym == IfInit && head.sym != 0:
				if kind, tail := f.headerTail(slices.Collect(it(k.ast))); kind == headerExpr || kind == headerSend {
					add(head, tail)
				}
			}
		}
	case SwitchGuard:
		if g, ok := f.switchGuardParts(n.ast); ok && g.hasStmt {
			add(g.stmtHead, g.stmtTail)
		}
	case ForHeader:
		if len(kids) >= 2 && kids[0].sym == Expression && kids[1].sym == ForRest {
			if kind, tail := f.headerTail(slices.Collect(it(kids[1].ast))); kind != headerNone {
				add(kids[0], tail)
			}
		}
	case ForPost:
		if len(kids) != 0 && kids[0].sym == Expression {
			switch kind, tail := f.headerTail(kids[1:]); {
			case len(kids) == 1:
				add(kids[0], nil)
			case kind == headerSend:
				add(kids[0], tail)
			}
		}
	}
	return out
}

// errHeaderStmt reports an expression standing in a header as a statement that
// is none.
func (f *File) errHeaderStmt(head Node) {
	f.err(f.tok(head.Pos()).Position(), "%s evaluated but not used", f.sourceSpan(head.Pos(), head.End()))
}
