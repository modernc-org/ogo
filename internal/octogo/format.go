// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"io"
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
	case (curr == LBRACE || prev == LBRACE || curr == RBRACE) && c.inLiteralBraces:
		return false
	case prev == ARROW:
		if prevPrev == IDENT || prevPrev == RBRACK || prevPrev == RPAREN {
			return true
		}
		return false
	case curr == LPAREN && prev == IDENT:
		return false
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
	case curr == LBRACK:
		return c.inType
	case curr == COMMA || curr == SEMICOLON || curr == COLON:
		return false
	// The ':' of a slice expression binds tight on both sides -- "s[0:1]", not
	// "s[0: 1]". Scoped to an index so a case clause's ':' is left alone.
	case prev == COLON && c.inIndex:
		return false
	case prev == PERIOD || curr == PERIOD:
		return false
	// Unambiguous unary operators never need a space after them
	case prev == NOT || prev == TILDE:
		return false
	case prev == ADD || prev == SUB || prev == MUL || prev == AND || prev == XOR:
		// Check the token BEFORE the operator to determine its context
		switch prevPrev {

		// If preceded by a literal, closing punctuation, or an identifier:
		case INT, STRING, CHAR, RPAREN, RBRACK, IDENT:
			// The IDENT Ambiguity: If we are inside a parameter list or type declaration,
			// and preceded by an identifier, '*' is a pointer! (e.g., var a *int)
			if prev == MUL && prevPrev == IDENT && (c.inParams || c.inType) {
				return false
			}

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
	case MUL, QUO, REM, SHL, SHR, AND:
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

type formatterCtx struct {
	indentLevel       int32
	undentLBraceIndex int32
	undentRBraceIndex int32
	indentSepForIndex int32
	hasAddOp          bool // True if the current SimpleExpr contains an AddOp (+, -)
	inParams          bool // True if we are inside a ParameterList or CallSuffix
	inType            bool
	inIndex           bool // True inside an Index, where ':' binds tight ("s[0:1]")
	// inLiteralBraces is true inside a composite literal, whose braces are spaced
	// the opposite way to a block's: "P{1, 2}", not "P { 1, 2 }". It stays set
	// across the elements, because the space after "{" is decided while emitting
	// the first of them, and is cleared by every construct that owns braces of its
	// own -- a block, a switch, a struct type -- so a function literal given as an
	// element is spaced as itself again.
	inLiteralBraces bool
}

// fieldMeasurement holds absolute column widths for a single FieldDecl or MethodSpec
type fieldMeasurement struct {
	startTokIdx  int32
	col2StartIdx int32 // The token index where Col2 (Type) starts
	col1Width    int
	col2Width    int
	lastTokIdx   int32 // Used to attach inline comment alignment
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
	return m
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
				case CaseClause, CommClause:
					c.indentLevel++
				case SwitchStmt, SelectStmt:
					// Flag the closing '}' for an extra separator indent
					c.indentSepForIndex = lastIndex(ast[:next])
					c.inLiteralBraces = false
				case ParameterList, CallSuffix:
					c.inParams = true
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
				case CompositeLit:
					c.inLiteralBraces = true
				case StructType, InterfaceType:
					c.indentLevel++
					c.undentLBraceIndex = firstIndex(ast[:next])
					c.undentRBraceIndex = lastIndex(ast[:next])
					c.inLiteralBraces = false

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

									// Safely detect true blank lines to break the block
									hasBlankLine := false
									if !isFirst {
										seps := f.parseSep(startTok.SepBytes(), nil)
										for _, sep := range seps {
											if ws, ok := sep.(whiteSpace); ok && ws >= 2 {
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
									if m.col2Width > current.maxCol2 {
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

				switch Symbol(tok.Ch) {
				case SEMICOLON:
					if len(src) == 0 {
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

				sepIndent := c.indentLevel
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
