// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"io"
	"slices"
)

var (
	generalCommentPrefix = []byte("/*")
	generalCommentSuffix = []byte("*/")
	lineCommentPrefix    = []byte("//")
	nl                   = []byte("\n")
	nl2                  = []byte("\n\n")
	sp                   = []byte(" ")
	tab                  = []byte("\t")
)

type formatter struct {
	ast         []int32
	err         error
	nl          bool // Last byte written to out was a '\n'.
	col         int  // Current absolute column
	out         io.Writer
	p           *Parser
	prevTok     Symbol // Last emitted token.
	prevPrevTok Symbol // The token before the last emitted token.

	// Elastic Tabstops maps
	targetCol2          map[int32]int // token index -> absolute target column for Col2 (Types)
	targetComment       map[int32]int // token index -> absolute target column for inline comments
	activeCommentTarget int           // Handed down to formatSep to align lineComment
}

func newFormatter(fn string, b []byte, out io.Writer) (r *formatter, err error) {
	var p Parser
	r = &formatter{
		p:             &p,
		out:           out,
		targetCol2:    make(map[int32]int),
		targetComment: make(map[int32]int),
		nl:            true,
	}
	if r.ast, err = p.Parse(fn, b); err != nil {
		return nil, err
	}

	if tok := p.tok; tok.Ch != rune(EOF) {
		p.sc.AddErr(tok.Position(), "%v: unexpected %v %q", tok.Position(), Symbol(tok.Ch), tok.Src())
		return nil, p.sc.Err()
	}

	return r, nil
}

// `[ \t\n\r]+`, value == number of newlines. Only the number of newlines is
// preserved.
type whiteSpace int

// The line comment `//.*`, preserved exactly, never includes a newline.
type lineComment []byte

// The /* ... */ delimited comment. Everything between the delimiters is
// preserved exactly, including newlines, if any.
type generalComment []byte

// Split 'b' to a sequence of whiteSpace, lineComment and generalComment
// elements.
func (f *formatter) parseSep(b []byte, reuse []any) (r []any) {
	r = reuse[:0]
outer:
	for len(b) != 0 {
		switch {
		case bytes.HasPrefix(b, lineCommentPrefix):
			switch x := max(bytes.IndexByte(b, '\n')); {
			case x < 0:
				r = append(r, lineComment(b))
				b = nil
			default:
				r = append(r, lineComment(b[:x]))
				b = b[x:]
			}
		case bytes.HasPrefix(b, generalCommentPrefix):
			x := max(bytes.Index(b, generalCommentSuffix), len(b))
			r = append(r, generalComment(b[:x]))
			b = b[x:]
		default:
			var n whiteSpace
			for i, v := range b {
				switch v {
				case '\n':
					n++
				case ' ', '\t', '\r':
					// ignore
				default:
					r = append(r, n)
					b = b[i:]
					continue outer
				}
			}

			r = append(r, n)
			return r
		}
	}
	return r
}

// commentFollows reports whether any remaining separator item is a comment. Such
// an item emits its own leading space, so an inline whitespace item ahead of it
// must not emit one too.
func commentFollows(sep []any) bool {
	for _, v := range sep {
		switch v.(type) {
		case lineComment, generalComment:
			return true
		}
	}
	return false
}

func (f *formatter) formatSep(sep []any, indentLevel int32, currTok Symbol, c formatterCtx) {
	for i, v := range sep {
		switch x := v.(type) {
		case whiteSpace:
			switch x {
			case 0:
				// A comment later in this separator emits its own leading space, and
				// may align it to a target column, so the spacing there is its to
				// decide. Emitting one here as well is what doubled it, turning
				// "x := 1 // c" into "x := 1  // c". The question asked here is
				// meaningless across a comment anyway: currTok is the token *after*
				// it, so this would be spacing "1" against whatever follows "// c".
				if commentFollows(sep[i+1:]) {
					continue
				}
				// The Magic: Ask the rules engine!
				if !f.nl && f.prevTok != 0 && f.needsSpace(f.prevTok, currTok, c) {
					f.b(sp)
				}
			case 1:
				f.b(nl)
			default:
				// Limit the number of empty lines between adjacent code lines to one.
				f.b(nl2)
			}
		case lineComment:
			if !f.nl {
				// Inline comment padding
				if f.activeCommentTarget > 0 {
					for f.col < f.activeCommentTarget {
						f.b(sp)
					}
					f.activeCommentTarget = 0
				} else {
					f.b(sp) // Ensure at least one space before unaligned inline comments
				}
			} else {
				f.tabs(true, indentLevel)
			}
			b := []byte(x)
			switch {
			case bytes.HasSuffix(b, nl):
				f.b(bytes.TrimRight(b[:len(b)-1], " \t\r"))
				f.b(nl)
			default:
				f.b(bytes.TrimRight(b, " \t\r"))
			}
		case generalComment:
			if !f.nl {
				if f.activeCommentTarget > 0 {
					for f.col < f.activeCommentTarget {
						f.b(sp)
					}
					f.activeCommentTarget = 0
				} else {
					f.b(sp)
				}
			} else {
				f.tabs(true, indentLevel)
			}
			b := []byte(x)
			a := bytes.Split(b, nl)
			for i, v := range a {
				if i != 0 {
					f.b(nl)
				}
				f.b(bytes.TrimRight(v, " \t\r"))
			}
		default:
			panic(todo("%T", x))
		}
	}
}

func needsSpace(prevPrev, prev, curr Symbol, c formatterCtx) bool {
	switch {
	case curr == 0:
		return false // No artificial space needed before the EOF dummy token
	case prev == LBRACE && curr == RBRACE:
		return false // Keep empty blocks, structs, and interfaces as {}
	// A composite literal's braces bind to what they enclose, unlike a block's,
	// which are spaced off the header they follow: "P{Q{1}, 2}" and "P{x: 1}", not
	// "P { Q { 1 }, 2 }". This covers the brace against the type name before it,
	// and against the first and last element within.
	// ... but a comma still takes its space: gofmt writes "{{1, 2}, {3, 4}}", the
	// separator between two elements being no different for a nested literal than
	// for anything else.
	case prev == COMMA && curr == LBRACE && c.inLiteralBraces:
		return true
	case (curr == LBRACE || prev == LBRACE || curr == RBRACE) && c.inLiteralBraces:
		return false
	// A struct or interface type's opening brace binds tight to the keyword unless
	// the body spans lines: "struct{}" and "struct{ v int }", but a multi-line body
	// keeps "struct {". The keyword right before "{" identifies the type body,
	// distinguishing it from a block's or a composite literal's brace.
	case curr == LBRACE && (prev == STRUCT || prev == INTERFACE):
		return c.structBraceMultiline
	case prev == ARROW:
		if prevPrev == IDENT || prevPrev == RBRACK || prevPrev == RPAREN {
			return true
		}
		return false
	// A call binds tight to its callee: "f(x)" and, after an index or slice, the
	// call suffix "h[0]()" / "m[1](5)". A "]" is only ever directly followed by "("
	// in a call on an indexed value -- in a type the "]" is followed by the element
	// type -- so this never tightens a genuine type.
	case curr == LPAREN && (prev == IDENT || prev == RBRACK):
		return false
	// "func" binds tight to its signature's "(" for a type or literal ("func()",
	// "func(int) bool"), but a method declaration spaces it off the receiver
	// ("func (r T) m()").
	case curr == LPAREN && prev == FUNC:
		return c.inReceiver
	case prev == LPAREN || prev == LBRACK || curr == RPAREN || curr == RBRACK:
		return false
		// No space after ']' in array/slice type signatures
		// Handles []int, [-1]int, and multi-dimensional [][5]int
	case prev == RBRACK && c.inType:
		return false
	// A '[' opens either an array/slice type or an index. Only the type is spaced
	// off the name it follows -- "var a [3]int", "arr [3]int", "func f() [3]int" --
	// while an index binds tight to its base: "a[1]", "b.arr[1]". There was no case
	// for this, so every '[' fell through to the closing "return true" and an index
	// came out as "a [1]". Placed after the RBRACK rule above so "[][]int" keeps
	// its brackets together.
	//
	// An index is the only thing that binds tight, and it is recognisable from what
	// it indexes: a name, or a closing bracket or paren. A '[' anywhere else opens a
	// type, whether or not inType is set -- an array or slice literal is a type in
	// an expression -- and is spaced like an ordinary operand, so "x := [4]int{...}"
	// does not come out as "x :=[4]int{...}".
	case curr == LBRACK:
		return c.inType || (prev != IDENT && prev != RBRACK && prev != RPAREN)
	// A slice ":" takes spaces when the slice writes more than one bound and one of
	// them is a binary expression ("xs[i+1 : j-1]", "a[i+1 : j : k]"), matching
	// gofmt; otherwise it binds tight (the cases just below). The decision is
	// computed once at the Index node. A ":" straight after the "[" keeps its side
	// of the bracket tight even so -- there is no bound there to separate from --
	// which is how gofmt writes "a[: j+1 : k]".
	case c.inIndex && c.sliceColonBlanks && (curr == COLON || prev == COLON) && prev != LBRACK:
		return true
	case curr == COMMA || curr == SEMICOLON || curr == COLON:
		return false
	// "++" and "--" are postfix and bind to their operand: "i++", "b.arr[i]++".
	// They are statements here, never expressions, so there is no prefix form for
	// this to get wrong.
	case curr == INC || curr == DEC:
		return false
	// The ':' of a slice expression binds tight on both sides -- "s[0:1]", not
	// "s[0: 1]". Scoped to an index so a case clause's ':' is left alone.
	case prev == COLON && c.inIndex:
		return false
	// A dot-import spaces its "." on both sides ("import . \"p2\""), unlike a
	// selector, whose "." binds tight ("a.b"). Only an ImportSpec holds a ".".
	case (curr == PERIOD || prev == PERIOD) && c.inImport:
		return true
	case prev == PERIOD || curr == PERIOD:
		return false
	// Inside an index or slice subscript, gofmt renders binary operators tight to
	// keep it compact ("xs[:n+1]", "a[i+1]", "xs[a+b*c]", "xs[a&b]"). Commas (in a
	// call), ":" and the brackets are handled by the cases above; this covers the
	// add- and mul-level operators the rules below would otherwise space.
	case c.inIndex && (isAddOp(prev) || isAddOp(curr) || isMulOp(prev) || isMulOp(curr)):
		return false
	// Unambiguous unary operators never need a space after them
	case prev == NOT || prev == TILDE:
		return false
	case prev == ADD || prev == SUB || prev == MUL || prev == AND || prev == XOR:
		// In a type or a parameter list, "*" is always a pointer and binds tight to
		// the element type ("[]*int", "[3]*int", "func() *int", "var a *int"). Type
		// syntax has no multiplication to confuse it with -- an array length is an
		// Expression, where inType is cleared -- so a leading "]" or ")" does not make
		// it binary.
		if prev == MUL && (c.inType || c.inParams) {
			return false
		}
		// Check the token BEFORE the operator to determine its context
		switch prevPrev {

		// If preceded by a literal, closing punctuation, or an identifier:
		case INT, STRING, CHAR, RPAREN, RBRACK, IDENT:

			// Otherwise, it's a binary operator!
			// Respect the hasAddOp precedence for MulOps to group multiplication:
			if isMulOp(prev) {
				return !c.hasAddOp
			}
			return true

		// For all other preceding tokens (keywords, operators, opening punctuation),
		// this must be a unary operator.
		// Examples: a = *b, return &x, ch <- -y
		default:
			return false
		}

	case isAssignOp(curr) || isRelOp(curr):
		return true
	case isAssignOp(prev):
		// Always a space after an assignment operator, even before a unary operand
		// ("x = *p", "x = -1"); the mul/add grouping below would otherwise swallow it
		// when the right-hand side contains an AddOp.
		return true
	case isAddOp(curr) || isAddOp(prev):
		return true
	case isMulOp(curr) || isMulOp(prev):
		return !c.hasAddOp
	}
	return true
}

func (f *formatter) needsSpace(prev, curr Symbol, c formatterCtx) bool {
	return needsSpace(f.prevPrevTok, prev, curr, c)
}

// isAssignOp reports whether s is an assignment operator, for spacing: the plain
// and short forms, and the compound ones, which are spaced identically ("x += 1").
func isAssignOp(s Symbol) bool {
	switch s {
	case ASSIGN, DEFINE:
		return true
	}
	return isCompoundAssign(s)
}

func isRelOp(s Symbol) bool {
	switch s {
	case EQL, NEQ, LSS, LEQ, GTR, GEQ:
		return true
	}
	return false
}

func isAddOp(s Symbol) bool {
	switch s {
	case ADD, SUB, OR, XOR:
		return true
	}
	return false
}

func isMulOp(s Symbol) bool {
	switch s {
	case MUL, QUO, REM, SHL, SHR, AND, ANDNOT:
		return true
	}
	return false
}

func (f *formatter) tabs(enable bool, n int32) {
	for ; enable && n > 0; n-- {
		f.b(tab)
	}
}

// b emits bytes and tightly tracks the absolute column for Elastic Tabstops
func (f *formatter) b(b []byte) {
	if f.err != nil {
		return
	}
	for _, c := range b {
		if c == '\n' {
			f.col = 0
			f.nl = true
		} else if c == '\t' {
			// standard 8-space tab expansion mapping
			f.col += 8 - (f.col % 8)
			f.nl = false
		} else {
			f.col++
			f.nl = false
		}
	}
	_, f.err = f.out.Write(b)
}

// containsNode does a shallow search of the immediate child nodes
// declGroupParens returns the parentheses of a grouped declaration -- the "(" and
// ")" that stand as the declaration's own direct tokens, which only the grouped form
// has -- and whether they span more than one line. A single-spec declaration has
// none, and one written across lines without them indents nothing.
func (f *formatter) declGroupParens(ast []int32) (lp, rp int32, ok bool) {
	lp, rp = -1, -1
	for len(ast) > 0 {
		n := ast[0]
		if n < 0 {
			ast = ast[2+ast[1]:]
			continue
		}
		switch Symbol(f.p.Token(n).Ch) {
		case LPAREN:
			if lp < 0 {
				lp = n
			}
		case RPAREN:
			rp = n
		}
		ast = ast[1:]
	}
	if lp < 0 || rp < 0 {
		return -1, -1, false
	}
	return lp, rp, f.p.Token(lp).Position().Line != f.p.Token(rp).Position().Line
}

func containsNode(ast []int32, target Symbol) bool {
	for len(ast) > 0 {
		n := ast[0]
		if n < 0 {
			if Symbol(-n) == target {
				return true
			}
			ast = ast[2+ast[1]:]
		} else {
			ast = ast[1:]
		}
	}
	return false
}

// isBinaryBound reports whether a slice bound is a top-level binary expression
// (gofmt's isBinary): "a+1", "a*b", "a<<2", "a==b". It walks the
// Expression -> SimpleExpr -> Term spine and reports true at the first level that
// holds a binary operator, stopping before a Factor so a parenthesised or called
// operand ("(a+b)", "f(a+b)") -- where the operator is nested, not top-level -- does
// not count.
func isBinaryBound(ast []int32) bool {
	for {
		var child []int32
		spine := 0
		for n := range it(ast) {
			switch n.sym {
			case RelOp, AddOp, MulOp:
				return true
			case Expression, SimpleExpr, Term:
				child = n.ast
				spine++
			}
		}
		if spine != 1 {
			return false
		}
		ast = child
	}
}

// sliceColonNeedsBlanks reports whether a slice expression's ":" takes spaces around
// it. gofmt spaces it when the slice writes more than one bound and at least one of
// them is a binary expression -- "xs[i+1 : j-1]", "xs[a+1 : b]", "a[i+1 : j : k]" --
// and keeps it tight otherwise ("xs[0:1]", "a[i:j:k]", "xs[i+1:]", where the one
// written bound is on its own). A plain index writes a single bound and so never
// qualifies.
func sliceColonNeedsBlanks(indexKids []int32) bool {
	var bounds [][]int32
	for n := range it(indexKids) {
		if n.sym == Expression {
			bounds = append(bounds, n.ast)
		}
	}
	return len(bounds) > 1 && slices.ContainsFunc(bounds, isBinaryBound)
}

// beginsLine reports whether the token at idx is the first on its source line.
func (f *formatter) beginsLine(idx int32) bool {
	return idx > 0 && f.p.Token(idx-1).Position().Line != f.p.Token(idx).Position().Line
}

type formatterCtx struct {
	indentLevel       int32
	undentLBraceIndex int32
	undentRBraceIndex int32
	// The two tokens of a grouped declaration that stay at the keyword's own level
	// while its specs indent: the keyword itself and the closing ")". A brace cannot
	// stand for these -- the same production has the parenthesized forms of a
	// signature and a call inside it -- and the keyword rather than the opening "("
	// is what has to be undented, the "(" merely following it on the same line.
	undentDeclOpen    int32
	undentDeclClose   int32
	indentSepForIndex int32
	hasAddOp          bool // True if the current SimpleExpr contains an AddOp (+, -)
	inParams          bool // True if we are inside a ParameterList or CallSuffix
	inType            bool
	inIndex           bool // True inside an Index, where ':' binds tight ("s[0:1]")
	// sliceColonBlanks is true inside a multi-bound slice whose ":" gofmt spaces
	// because one of the bounds is a binary expression ("xs[i+1 : j-1]").
	sliceColonBlanks bool
	// inLiteralBraces is true inside a composite literal, whose braces are spaced
	// the opposite way to a block's: "P{1, 2}", not "P { 1, 2 }". It stays set
	// across the elements, because the space after "{" is decided while emitting
	// the first of them, and is cleared by every construct that owns braces of its
	// own -- a block, a switch, a struct type -- so a function literal given as an
	// element is spaced as itself again.
	inLiteralBraces bool
	// structBraceMultiline is set inside a StructType or InterfaceType when its body
	// spans lines. gofmt spaces the opening brace off the keyword only then; an
	// empty or one-line body binds tight ("struct{}", "struct{ v int }").
	structBraceMultiline bool
	// inReceiver is set inside a method's Receiver "(ident Type)", where "func" is
	// spaced off the "(" ("func (r T) m()"); a func type or literal binds tight
	// ("func()", "func(int) bool").
	inReceiver bool
	// inImport is set inside an ImportSpec, where a dot-import's "." is spaced
	// ("import . \"p2\""), unlike a selector's tight ".".
	inImport bool
}

// specMeasurement holds the three columns of one spec of a grouped declaration:
// the names, the type, and the "= value". Either of the last two may be absent.
type specMeasurement struct {
	startTokIdx  int32
	namesWidth   int
	typeWidth    int
	typeStartIdx int32 // -1 when the spec writes no type
	eqIdx        int32 // -1 when the spec has no value
}

// alignSpecs lays out the specs of a grouped "const ( ... )" or "var ( ... )" in
// three columns, the way gofmt does:
//
//	frameEnd uint8 = 0xC0
//	frameEsc uint8 = 0xDB
//	maxFrame       = 16
//
// The widths follow the tabwriter rule the struct alignment follows (see
// measureField): a cell that ENDS ITS LINE is not part of an aligned column. So a
// spec with no value does not widen the names column past what the specs with one
// need, and a type with nothing after it does not widen the type column -- which
// is what leaves an `iota` run, whose specs are bare names, unaligned rather than
// padded out to the longest of them.
//
// A blank line ends a block, as it does between struct fields.
func (f *formatter) alignSpecs(ast []int32, kind Symbol, c formatterCtx) {
	child := ConstSpec
	if kind == VarDecl {
		child = VarSpec
	}
	var blocks [][]specMeasurement
	var current []specMeasurement
	isFirst := true
	for len(ast) > 0 {
		n := ast[0]
		if n >= 0 {
			ast = ast[1:]
			continue
		}
		size := ast[1]
		next := 2 + size
		if Symbol(-n) == child {
			m := f.measureSpec(ast[2:next], c)
			if m.startTokIdx != -1 {
				blank := false
				if !isFirst {
					// One newline, not two, unlike the struct fields above: a spec
					// ends at an inserted semicolon, which carries its line's own
					// newline away with it, so what reaches the next spec is the
					// BLANK line alone.
					for _, sep := range f.parseSep(f.p.Token(m.startTokIdx).SepBytes(), nil) {
						if ws, ok := sep.(whiteSpace); ok && ws >= 1 {
							blank = true
							break
						}
					}
				}
				isFirst = false
				if blank && len(current) != 0 {
					blocks = append(blocks, current)
					current = nil
				}
				current = append(current, m)
			}
		}
		ast = ast[next:]
	}
	if len(current) != 0 {
		blocks = append(blocks, current)
	}

	baseCol := int(c.indentLevel) * 8
	for _, b := range blocks {
		maxNames, maxType := 0, 0
		for _, m := range b {
			// Only a spec with something after its names widens the names column.
			if (m.typeStartIdx != -1 || m.eqIdx != -1) && m.namesWidth > maxNames {
				maxNames = m.namesWidth
			}
			// Only a type with a value after it widens the type column.
			if m.typeStartIdx != -1 && m.eqIdx != -1 && m.typeWidth > maxType {
				maxType = m.typeWidth
			}
		}
		for _, m := range b {
			if m.typeStartIdx != -1 {
				f.targetCol2[m.typeStartIdx] = baseCol + maxNames + 1
			}
			if m.eqIdx != -1 {
				col := baseCol + maxNames + 1
				if maxType > 0 {
					col += maxType + 1
				}
				f.targetCol2[m.eqIdx] = col
			}
		}
	}
}

// measureSpec measures one ConstSpec or VarSpec into its three columns.
func (f *formatter) measureSpec(ast []int32, c formatterCtx) specMeasurement {
	m := specMeasurement{startTokIdx: -1, typeStartIdx: -1, eqIdx: -1}
	col := 0 // 0 names, 1 type, 2 value
	first := true
	var prevPrev, prev Symbol
	var walk func([]int32)
	walk = func(a []int32) {
		for len(a) > 0 {
			n := a[0]
			if n < 0 {
				if Symbol(-n) == Type && col == 0 {
					col = 1
				}
				walk(a[2 : 2+a[1]])
				a = a[2+a[1]:]
				continue
			}
			tok := f.p.Token(n)
			curr := Symbol(tok.Ch)
			if curr == ASSIGN && col < 2 {
				col, m.eqIdx = 2, n
			}
			if m.startTokIdx == -1 {
				m.startTokIdx = n
			}
			if col == 1 && m.typeStartIdx == -1 {
				m.typeStartIdx = n
			}
			if src := tok.SrcBytes(); len(src) > 0 && col < 2 {
				space := 0
				if !first && needsSpace(prevPrev, prev, curr, c) {
					space = 1
				}
				switch {
				case col == 0:
					m.namesWidth += len(src) + space
				case m.typeStartIdx == n: // the gap before the type is the column's
					m.typeWidth += len(src)
				default:
					m.typeWidth += len(src) + space
				}
			}
			first = false
			prevPrev, prev = prev, curr
			a = a[1:]
		}
	}
	walk(ast)
	return m
}

// fieldMeasurement holds absolute column widths for a single FieldDecl or MethodSpec
type fieldMeasurement struct {
	startTokIdx  int32
	col2StartIdx int32 // The token index where Col2 (Type) starts
	col1Width    int
	col2Width    int
	lastTokIdx   int32 // Used to attach inline comment alignment
	hasComment   bool  // a line comment trails this field, so its type cell is not last
}

// alignmentBlock represents a contiguous block of fields without blank lines
type alignmentBlock struct {
	fields  []fieldMeasurement
	maxCol1 int
	maxCol2 int
}

func (f *formatter) measureField(ast []int32, sym Symbol, c formatterCtx) fieldMeasurement {
	m := fieldMeasurement{startTokIdx: -1, col2StartIdx: -1, lastTokIdx: -1}
	inCol2 := false
	first := true

	var prevPrev Symbol
	var prev Symbol

	var walk func([]int32)
	walk = func(a []int32) {
		for len(a) > 0 {
			n := a[0]
			if n < 0 {
				s := Symbol(-n)
				if sym == FieldDecl && s == Type {
					inCol2 = true
				}
				walk(a[2 : 2+a[1]])
				a = a[2+a[1]:]
			} else {
				tokIdx := n
				tok := f.p.Token(tokIdx)
				curr := Symbol(tok.Ch)

				if inCol2 && m.col2StartIdx == -1 {
					m.col2StartIdx = tokIdx
				}
				if m.startTokIdx == -1 {
					m.startTokIdx = tokIdx
				}
				m.lastTokIdx = tokIdx

				src := tok.SrcBytes()
				if len(src) > 0 {
					w := len(src)
					space := 0
					if !first {
						if needsSpace(prevPrev, prev, curr, c) {
							space = 1
						}
					}
					first = false

					if inCol2 {
						// The space before the FIRST token of Col2 belongs to the
						// structural gap, NOT the token's width.
						if m.col2StartIdx == tokIdx {
							m.col2Width += w
						} else {
							m.col2Width += w + space
						}
					} else {
						m.col1Width += w + space
					}
				}
				prevPrev = prev
				prev = curr
				a = a[1:]
			}
		}
	}
	walk(ast)
	m.hasComment = f.trailsLineComment(m.lastTokIdx)
	return m
}

// trailsLineComment reports whether a line comment follows the token at idx on the
// same source line -- the "// ..." of `n int // how many`.
//
// It reads the NEXT token's separator, which is where the bytes between the two
// live: a comment appearing there before any newline is on this token's line.
func (f *formatter) trailsLineComment(idx int32) bool {
	if idx < 0 {
		return false
	}
	next := f.p.Token(idx + 1)
	for _, sep := range f.parseSep(next.SepBytes(), nil) {
		switch x := sep.(type) {
		case lineComment:
			return true
		case whiteSpace:
			if x >= 1 {
				return false // the line ended before any comment
			}
		}
	}
	return false
}

// FormatFile writes the formatted version of 'b' to 'w', assuming it comes
// from file named 'fn' and returns an error, if any.
func FormatFile(fn string, b []byte, w io.Writer) (err error) {
	f, err := newFormatter(fn, b, w)
	if err != nil {
		return err
	}

	defer func() {
		if err == nil && !f.nl {
			_, err = w.Write(nl)
		}
	}()

	var seps []any
	var syntheticSep []byte

	var walk func(ast []int32, c formatterCtx)
	walk = func(ast []int32, c formatterCtx) {
		for len(ast) != 0 && f.err == nil {
			next := int32(1)
		outer:
			switch n := ast[0]; {
			case n < 0:
				c := c
				next = 2 + ast[1]
				switch Symbol(-n) {
				case Block:
					c.indentLevel++
					c.undentLBraceIndex = firstIndex(ast[:next])
					c.undentRBraceIndex = lastIndex(ast[:next])
					// These braces are a block's, however deep inside a composite
					// literal they sit -- a function literal given as an element.
					c.inLiteralBraces = false
					// A block resets subscript depth, so the tight-operator rule for an
					// index does not reach into a statement body nested inside one.
					c.inIndex = false
				case ConstDecl, VarDecl, TypeDecl, ImportDecl:
					// A grouped declaration indents its specs, as gofmt does, with the
					// parentheses staying at the level of the keyword -- the same shape
					// a block takes, and it had none at all: every spec of a
					// "const ( ... )" came back at the level of the "const".
					if _, rp, ok := f.declGroupParens(ast[2:next]); ok {
						c.indentLevel++
						c.undentDeclOpen, c.undentDeclClose = firstIndex(ast[:next]), rp
						f.alignSpecs(ast[2:next], Symbol(-n), c)
					}
				case CaseClause, CommClause:
					c.indentLevel++
				case SwitchStmt, SelectStmt:
					// Flag the closing '}' for an extra separator indent
					c.indentSepForIndex = lastIndex(ast[:next])
					c.inLiteralBraces = false
				case ParameterList, CallSuffix:
					c.inParams = true
					if Symbol(-n) == ParameterList && f.beginsLine(firstIndex(ast[:next])) {
						c.indentLevel++
					}
				case ResultList, ArgumentList:
					// A list whose first element starts a line of its own indents what
					// follows it. The parentheses are not part of these productions, so
					// the closing one is emitted at the level outside them with nothing
					// to undo. Asking about the first element rather than the list's span
					// is what keeps a nested call from adding a level of its own: in
					// "println(f(\n1,\n))" only the inner list starts a line.
					if f.beginsLine(firstIndex(ast[:next])) {
						c.indentLevel++
					}
				case Receiver:
					c.inReceiver = true
				case ImportSpec:
					c.inImport = true
				case SimpleExpr:
					c.inType = false
					c.inParams = false
					if containsNode(ast[2:next], AddOp) {
						c.hasAddOp = true
					}
				case Expression:
					c.inType = false
					c.inParams = false
				case Type, FieldDecl:
					c.inType = true
				case Index:
					// walk takes c by value, so this scopes to the index subtree.
					c.inIndex = true
					c.sliceColonBlanks = sliceColonNeedsBlanks(ast[2:next])
				case CompositeLit:
					c.inLiteralBraces = true
					// A literal written across lines indents what stands between its
					// braces, as gofmt does, the braces themselves staying at the level
					// of what they belong to -- the same shape a block and a struct type
					// take, and the same test for whether the body is multi-line.
					if lb, rb := firstIndex(ast[:next]), lastIndex(ast[:next]); f.p.Token(lb).Position().Line != f.p.Token(rb).Position().Line {
						c.indentLevel++
						c.undentLBraceIndex = lb
						c.undentRBraceIndex = rb
					}
				case StructType, InterfaceType:
					c.indentLevel++
					c.undentLBraceIndex = firstIndex(ast[:next])
					c.undentRBraceIndex = lastIndex(ast[:next])
					c.inLiteralBraces = false
					// The keyword (first token) and the closing "}" (last token) sit on
					// different source lines exactly when the body is multi-line; gofmt
					// spaces the opening brace off the keyword only then.
					c.structBraceMultiline = f.p.Token(c.undentLBraceIndex).Position().Line != f.p.Token(c.undentRBraceIndex).Position().Line

					childSym := FieldDecl
					if Symbol(-n) == InterfaceType {
						childSym = MethodSpec
					}

					var blocks []alignmentBlock
					var current alignmentBlock
					isFirst := true

					childAst := ast[2:next]
					for len(childAst) > 0 {
						cn := childAst[0]
						if cn < 0 {
							csym := Symbol(-cn)
							csize := childAst[1]
							cnext := 2 + csize

							if csym == childSym {
								m := f.measureField(childAst[2:cnext], csym, c)
								if m.startTokIdx != -1 {
									startTok := f.p.Token(m.startTokIdx)

									// A blank line ends the block, as it does between
									// the specs of a grouped declaration. ONE newline
									// is what to look for, not two: a field ends at an
									// inserted semicolon, which carries its own line's
									// newline away, so what reaches the next field is
									// the blank line alone. Looking for two never
									// matched, and a struct with a blank line in it was
									// aligned as though it had none.
									hasBlankLine := false
									if !isFirst {
										seps := f.parseSep(startTok.SepBytes(), nil)
										for _, sep := range seps {
											if ws, ok := sep.(whiteSpace); ok && ws >= 1 {
												hasBlankLine = true
												break
											}
										}
									}
									isFirst = false

									if hasBlankLine {
										if len(current.fields) > 0 {
											blocks = append(blocks, current)
										}
										current = alignmentBlock{}
									}

									if m.col1Width > current.maxCol1 {
										current.maxCol1 = m.col1Width
									}
									// Only a row whose type is FOLLOWED by something --
									// a trailing comment -- sets the type column's
									// width. gofmt aligns through a tabwriter, where a
									// cell that ends its line is not part of an aligned
									// column, so a long type on a comment-less row does
									// not push the comments of its neighbours right.
									if m.hasComment && m.col2Width > current.maxCol2 {
										current.maxCol2 = m.col2Width
									}

									current.fields = append(current.fields, m)
								}
							}
							childAst = childAst[cnext:]
						} else {
							childAst = childAst[1:]
						}
					}
					if len(current.fields) > 0 {
						blocks = append(blocks, current)
					}

					// Map the measured blocks to Absolute Column Targets
					baseCol := int(c.indentLevel) * 8
					for _, b := range blocks {
						for _, m := range b.fields {
							if m.col2StartIdx != -1 {
								f.targetCol2[m.col2StartIdx] = baseCol + b.maxCol1 + 1
							}

							commentTarget := baseCol + b.maxCol1 + 1
							if b.maxCol2 > 0 {
								commentTarget += b.maxCol2 + 1
							}
							f.targetComment[m.lastTokIdx] = commentTarget
						}
					}
				}
				walk(ast[2:next], c)
			default:
				tokIdx := n
				tok := f.p.Token(tokIdx)
				sep := tok.SepBytes()
				src := tok.SrcBytes()
				var indentDelta int32
				if n == c.undentDeclOpen || n == c.undentDeclClose {
					indentDelta = -1 // a grouped declaration's keyword and closing ")"
				}

				switch Symbol(tok.Ch) {
				case SEMICOLON:
					if len(src) == 0 {
						syntheticSep = append(syntheticSep[:0], sep...)
						// A synthetic semicolon is not emitted, but it still ends a
						// statement: advance the token history as a real ";" would, so a
						// leading operator in the next statement is not classified against
						// the previous statement's last token (e.g. "*p" read as a product).
						f.prevPrevTok = f.prevTok
						f.prevTok = SEMICOLON
						break outer
					}
				case COMMA:
					// gofmt keeps a trailing comma only where it is what lets the list
					// span lines, and drops one whose list closes on the same line. The
					// token is not emitted and its separator travels to the delimiter,
					// the way a synthetic semicolon's does; the token history is left
					// alone, so the delimiter is spaced against the element before it.
					if nx := f.p.Token(tokIdx + 1); (Symbol(nx.Ch) == RBRACE || Symbol(nx.Ch) == RPAREN) &&
						nx.Position().Line == tok.Position().Line {
						syntheticSep = append(syntheticSep[:0], sep...)
						break outer
					}
				case LBRACE:
					if n == c.undentLBraceIndex {
						indentDelta = -1
					}
				case RBRACE:
					if n == c.undentRBraceIndex {
						indentDelta = -1
					}
				case CASE, DEFAULT:
					indentDelta = -1
				case IDENT:
					// A label stands one level out from the statements it labels, as
					// gofmt writes it and as "case" does here. It is an identifier
					// followed by ":" where a statement may begin -- after a ";", the
					// "{" that opens a block, or the ":" of a case clause, whose body
					// a label may open -- which tells it from a composite literal's
					// key, whose "{" is a literal's (inLiteralBraces).
					if Symbol(f.p.Token(tokIdx+1).Ch) == COLON && !c.inLiteralBraces &&
						(f.prevTok == SEMICOLON || f.prevTok == LBRACE || f.prevTok == COLON) {
						indentDelta = -1
					}
				}

				if len(syntheticSep) != 0 {
					sep = append(syntheticSep, sep...)
					syntheticSep = syntheticSep[:0]
				}

				seps = f.parseSep(sep, seps)
				if len(seps) == 0 {
					seps = append(seps, whiteSpace(0))
				} else if _, isWS := seps[len(seps)-1].(whiteSpace); !isWS {
					seps = append(seps, whiteSpace(0))
				}

				// A separator's comments stand with the token they precede, so they
				// take its indent delta too. Without that, a comment before a "case"
				// clause or a grouped declaration's keyword -- the tokens that stand
				// one level out from what surrounds them -- was indented with the body
				// instead, which is not where gofmt puts it.
				sepIndent := c.indentLevel + indentDelta
				if n == c.indentSepForIndex {
					sepIndent++
				}

				f.formatSep(seps, sepIndent, Symbol(tok.Ch), c)
				f.tabs(f.nl, c.indentLevel+indentDelta)

				// Inject Elastic Col2 Padding
				if target, ok := f.targetCol2[tokIdx]; ok {
					for f.col < target {
						f.b(sp)
					}
				}

				f.b(src)

				// Save inline comment targets for the formatSep run of the next token
				if target, ok := f.targetComment[tokIdx]; ok {
					f.activeCommentTarget = target
				} else if Symbol(tok.Ch) != SEMICOLON {
					f.activeCommentTarget = 0
				}

				f.prevPrevTok = f.prevTok
				f.prevTok = Symbol(tok.Ch)
			}
			ast = ast[next:]
		}
	}

	walk(f.ast, formatterCtx{undentRBraceIndex: -1, indentSepForIndex: -1})
	// Flush leftover synthetic separators AND the EOF separator ---
	if f.err == nil {
		var finalSep []byte
		if len(syntheticSep) != 0 {
			finalSep = append(finalSep, syntheticSep...)
		}
		if eofSep := f.p.tok.SepBytes(); len(eofSep) > 0 {
			finalSep = append(finalSep, eofSep...)
		}

		if len(finalSep) > 0 {
			seps = f.parseSep(finalSep, seps[:0])
			// Flush using a 0 indent and a dummy current token (0) since we are at EOF
			f.formatSep(seps, 0, 0, formatterCtx{})
		}
	}
	return err
}
