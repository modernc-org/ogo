// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"slices"
)

// gofmt keeps the line breaks of an expression and decides those of everything
// else: a statement stands on a line of its own and so does a declaration, a
// spec, a field and a case, and a block's braces enclose lines. "if c { v = 1 }"
// is three lines to it. The one body it leaves on the line it was written on is a
// FUNCTION's, a declaration's or a literal's, where that is short: no more than
// five statements, none of them with a block of its own, and a hundred columns
// for the header and the statements together; and the one type, a struct or an
// interface of a single short field.
//
// Until 2026-09-28 `ogo fmt` kept every body where it was written, and a
// statement after a semicolon beside the one before it.
//
// markBreaks decides it ahead of everything else, since what begins a line
// decides what is indented and what is aligned: breakBefore holds the tokens that
// begin a line whatever line the source has them on, dropSemi the semicolons
// written between what is then on lines of its own.

// keptLine is what was left on its line for being short, and where the walk
// wrote it: that it is short enough is known only then. The key is what a later
// pass knows it by, a body's opening brace or a type's keyword.
type keptLine struct {
	key             int32
	limit           int // the columns go/printer allows from off to endOff
	line, off       int // where the first token of what is measured landed
	endLine, endOff int // where the last one ended
}

// markBreaks fills breakBefore and dropSemi for the productions of ast.
func (f *formatter) markBreaks(ast []int32) {
	for _, k := range kidsOf(ast) {
		if k.sym == 0 {
			continue
		}
		body := k.ast[2:]
		switch k.sym {
		case SourceFile:
			f.breakItems(body, -1, true)
		case ConstDecl, VarDecl, TypeDecl, ImportDecl:
			if _, rp, ok := f.declParens(body); ok {
				f.breakItems(body, rp, false)
			}
		case FuncDecl, FuncLiteral:
			for _, c := range kidsOf(body) {
				if c.sym == Block && f.keepsLine(k, c) {
					f.keptBody[&c.ast[0]] = true
					// A hundred columns for the header and the statements, which
					// the braces and the blanks around them are not of -- and
					// ninety-nine for a declaration, whose header go/printer
					// measures from a column ahead of the "func".
					lbrace, last := firstIndex(c.ast), lastIndex(k.ast)
					limit := 100 + len(" { ") + len(" }")
					if lbrace+1 == last {
						limit = 100 + len(" {}")
					}
					if k.sym == FuncDecl {
						limit--
					}
					f.keep(lbrace, limit, firstIndex(k.ast), last)
				}
			}
		case Block:
			if !f.keptBody[&k.ast[0]] {
				f.breakItems(body, lastIndex(k.ast), false)
			}
		case SwitchStmt, SelectStmt:
			// An empty select stays "select {}", alone of the statements with
			// braces: go/printer writes it so.
			if last := lastIndex(k.ast); k.sym == SwitchStmt || !f.emptyBraces(last) {
				f.breakItems(body, last, false)
			}
		case CaseClause, CommClause:
			f.breakItems(body, -1, false)
		case StructType, InterfaceType:
			if !f.typeKeepsLine(k) {
				f.breakItems(body, lastIndex(k.ast), false)
				break
			}
			// Thirty columns is go/printer's number for the one field, and that
			// its names count for one, however many and however long, is its
			// arithmetic; a method's type is its signature as a function type's,
			// "func" and all.
			for _, c := range kidsOf(body) {
				first, last := firstIndex(c.ast), lastIndex(c.ast)
				switch c.sym {
				case FieldDecl:
					limit := 30
					for _, t := range kidsOf(c.ast[2:]) {
						if t.sym == Type {
							first, limit = firstIndex(t.ast), 29
						}
					}
					f.keep(firstIndex(k.ast), limit, first, last)
				case MethodSpec:
					if len(c.ast) > 3 && c.ast[3] >= 0 && Symbol(f.p.Token(c.ast[3]).Ch) == LPAREN {
						f.keep(firstIndex(k.ast), 29-len("func"), first+1, last)
						break
					}
					f.keep(firstIndex(k.ast), 30, first, last) // an embedded interface
				}
			}
		case PostfixOp:
			// A label stands on a line of its own, ahead of what it labels.
			kids := kidsOf(body)
			if len(kids) == 2 && kids[0].sym == 0 && Symbol(f.p.Token(kids[0].ast[0]).Ch) == COLON && kids[1].sym == Statement {
				if first := firstIndex(kids[1].ast); first >= 0 {
					f.breakBefore[first] = true
				}
			}
		}
		f.markBreaks(body)
	}
}

// declParens is declGroupParens without the question it answers: the
// parentheses of a grouped declaration, and whether it has any spec.
func (f *formatter) declParens(body []int32) (lp, rp int32, ok bool) {
	lp, rp = -1, -1
	specs := 0
	for _, k := range kidsOf(body) {
		if k.sym != 0 {
			specs++
			continue
		}
		switch Symbol(f.p.Token(k.ast[0]).Ch) {
		case LPAREN:
			if lp < 0 {
				lp = k.ast[0]
			}
		case RPAREN:
			rp = k.ast[0]
		}
	}
	return lp, rp, lp >= 0 && rp >= 0 && specs > 0
}

// breakItems puts every production of body on a line of its own, and closer,
// what closes the body, where there is one. The semicolons between them go. The
// first production of a file begins no line but the first.
func (f *formatter) breakItems(body []int32, closer int32, file bool) {
	items := 0
	for _, k := range kidsOf(body) {
		switch {
		case k.sym == 0:
			if tok := f.p.Token(k.ast[0]); Symbol(tok.Ch) == SEMICOLON && len(tok.SrcBytes()) != 0 {
				f.dropSemi[k.ast[0]] = true
			}
		case k.sym == SwitchGuard || k.sym == ForHeader:
			// what a statement has ahead of its braces
		default:
			first := firstIndex(k.ast)
			if first < 0 {
				continue // an empty statement
			}
			items++
			if !file || items > 1 {
				f.breakBefore[first] = true
			}
		}
	}
	if closer >= 0 {
		f.breakBefore[closer] = true
	}
}

// emptyBraces reports whether the closing brace at rbrace closes braces with
// nothing between them on one line, not a comment either.
func (f *formatter) emptyBraces(rbrace int32) bool {
	return rbrace > 0 && Symbol(f.p.Token(rbrace-1).Ch) == LBRACE && len(bytes.TrimSpace(f.p.Token(rbrace).SepBytes())) == 0 &&
		f.p.Token(rbrace-1).Position().Line == f.p.Token(rbrace).Position().Line
}

// keepsLine reports whether the body of the function fn, a declaration or a
// literal, stays on the line it was written on, as far as can be said ahead of
// writing it: the function is written on one line, the body has five statements
// at most and none of them breaks a line itself, and no pass before this one
// found it too long.
func (f *formatter) keepsLine(fn, body kid) bool {
	first, last := firstIndex(fn.ast), lastIndex(fn.ast)
	if first < 0 || last < 0 || f.p.Token(first).Position().Line != f.endLine(last) {
		return false
	}
	if f.broken[firstIndex(body.ast)] {
		return false
	}
	stmts := 0
	for _, k := range kidsOf(body.ast[2:]) {
		if k.sym == Statement && firstIndex(k.ast) >= 0 {
			stmts++
		}
	}
	return stmts <= 5 && !f.breaksLine(body.ast[2:])
}

// breaksLine reports whether something of body is written across lines
// whatever the source does: a block, a switch, a select, a type or a function
// literal that does not keep its line.
func (f *formatter) breaksLine(body []int32) bool {
	for _, k := range kidsOf(body) {
		switch k.sym {
		case 0:
			continue
		case Block, SwitchStmt, SelectStmt:
			return true
		case StructType, InterfaceType:
			if !f.typeKeepsLine(k) {
				return true
			}
		case FuncLiteral:
			for _, c := range kidsOf(k.ast[2:]) {
				switch {
				case c.sym == Block:
					if !f.keepsLine(k, c) {
						return true
					}
				case c.sym != 0 && f.breaksLine(c.ast[2:]):
					return true
				}
			}
			continue
		}
		if f.breaksLine(k.ast[2:]) {
			return true
		}
	}
	return false
}

// keep notes that the tokens first to last are left on their line, where they
// take limit columns at most.
func (f *formatter) keep(key int32, limit int, first, last int32) {
	r := &keptLine{key: key, limit: limit}
	f.keptFirst[first] = append(f.keptFirst[first], r)
	f.keptLast[last] = append(f.keptLast[last], r)
	f.kept = append(f.kept, r)
}

// typeKeepsLine reports whether a struct or an interface type stays on its
// line, as far as can be said ahead of writing it: it has no field at all, or it
// is written on one line with no comment in it and has one field, which no pass
// before this one found too long.
func (f *formatter) typeKeepsLine(t kid) bool {
	var fields []kid
	for _, k := range kidsOf(t.ast[2:]) {
		if k.sym == FieldDecl || k.sym == MethodSpec {
			fields = append(fields, k)
		}
	}
	first, last := firstIndex(t.ast), lastIndex(t.ast)
	for i := first + 1; i <= last; i++ {
		if sep := f.p.Token(i).SepBytes(); bytes.Contains(sep, lineCommentPrefix) || bytes.Contains(sep, generalCommentPrefix) {
			return false
		}
	}
	if len(fields) == 0 {
		return f.p.Token(first).Position().Line == f.endLine(last)
	}
	if len(fields) != 1 || f.p.Token(first).Position().Line != f.endLine(last) {
		return false
	}
	return !f.broken[first] && !f.breaksLine(fields[0].ast[2:])
}

// spansLines reports whether the tokens first to last are written across lines:
// the source has them so, or a line is broken between them.
func (f *formatter) spansLines(first, last int32) bool {
	if first < 0 || last < 0 {
		return false
	}
	if f.p.Token(first).Position().Line != f.endLine(last) {
		return true
	}
	i, _ := slices.BinarySearch(f.breakToks, first+1)
	return i < len(f.breakToks) && f.breakToks[i] <= last
}

// tooLong lists what was left on its line and written past what go/printer
// allows.
func (f *formatter) tooLong() (r []int32) {
	for _, k := range f.kept {
		if k.line != k.endLine || k.endOff-k.off > k.limit {
			r = append(r, k.key)
		}
	}
	return r
}
