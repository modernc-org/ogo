// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

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
	out         io.Writer
	p           *Parser
	prevTok     Symbol // Last emitted token.
	prevPrevTok Symbol // The token before the last emitted token.
}

func newFormatter(fn string, b []byte, out io.Writer) (r *formatter, err error) {
	var p Parser
	r = &formatter{
		p:   &p,
		out: out,
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

func (f *formatter) formatSep(sep []any, indentLevel int32, currTok Symbol, c formatterCtx) {
	for _, v := range sep {
		switch x := v.(type) {
		case whiteSpace:
			switch x {
			case 0:
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
			f.tabs(f.nl, indentLevel)
			b := []byte(x)
			switch {
			case bytes.HasSuffix(b, nl):
				f.b(bytes.TrimRight(b[:len(b)-1], " \t\r"))
				f.b(nl)
			default:
				f.b(bytes.TrimRight(b, " \t\r"))
			}
		case generalComment:
			f.tabs(f.nl, indentLevel)
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

func (f *formatter) needsSpace(prev, curr Symbol, c formatterCtx) bool {
	switch {

	// 1. Punctuation stripping
	case prev == ARROW:
		// Distinguish between send (`rateChan <- 100`) and receive (`= <-rateChan`)
		// If the token before the arrow was an identifier or a closing bracket, it's a send operation.
		if f.prevPrevTok == IDENT || f.prevPrevTok == RBRACK || f.prevPrevTok == RPAREN {
			return true // Send needs space after
		}

		return false // Receive does not need space after
	// No space before opening parenthesis if it follows an identifier (function calls/decls)
	case curr == LPAREN && prev == IDENT:
		return false
	// No space after opening brackets, no space before closing brackets
	case prev == LPAREN || prev == LBRACK || curr == RPAREN || curr == RBRACK:
		return false
	// No space before comma or semicolon, but space after
	case curr == COMMA || curr == SEMICOLON || curr == COLON:
		return false
	// Selectors: no space around dot (e.g., p2.PinHigh)
	case prev == PERIOD || curr == PERIOD:
		return false

	// 2. Operator Spacing
	// Assigns and RelOps always get spaces
	case isAssignOp(curr) || isRelOp(curr):
		return true
	// AddOp always gets a space
	case isAddOp(curr) || isAddOp(prev):
		return true
	// MulOp ONLY gets a space if it's NOT sharing an expression with an AddOp
	case isMulOp(curr) || isMulOp(prev):
		return !c.hasAddOp
	}

	// 3. Fallback: Default to true for separating keywords and identifiers
	return true
}

func isAssignOp(s Symbol) bool {
	switch s {
	case ASSIGN, DEFINE:
		return true
	}

	return false
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
	case MUL, QUO, SHL, SHR, AND:
		return true
	}

	return false
}

func (f *formatter) tabs(enable bool, n int32) {
	for ; enable && n > 0; n-- {
		f.b(tab)
	}
}

func (f *formatter) b(b []byte) {
	f.nl = bytes.HasSuffix(b, nl)
	if f.err != nil {
		return
	}

	_, f.err = f.out.Write(b)
}

// containsNode does a shallow search of the immediate child nodes
// to see if they match the target symbol.
func containsNode(ast []int32, target Symbol) bool {
	for len(ast) > 0 {
		n := ast[0]
		if n < 0 {
			// It's a Non-Terminal
			if Symbol(-n) == target {
				return true
			}

			// Skip over this node's children to get to the next sibling
			ast = ast[2+ast[1]:]
		} else {
			// It's a Terminal (Token Index)
			ast = ast[1:]
		}
	}
	return false
}

type formatterCtx struct {
	indentLevel       int32
	undentLBraceIndex int32
	undentRBraceIndex int32
	hasAddOp          bool // True if the current SimpleExpr contains an AddOp (+, -)
	inParams          bool // True if we are inside a ParameterList or CallSuffix
}

// FormatFile writes the formatted version of 'b' to 'w', assuming it comes
// from file named 'fn' and returns an error, if any.
func FormatFile(fn string, b []byte, w io.Writer) (err error) {
	f, err := newFormatter(fn, b, w)
	if err != nil {
		return err
	}

	var seps []any
	var syntheticSep []byte

	var walk func(ast []int32, c formatterCtx)
	walk = func(ast []int32, c formatterCtx) {
		for len(ast) != 0 && f.err == nil {
			next := int32(1)
		outer:
			switch n := ast[0]; {
			case n < 0:
				next = 2 + ast[1]
				switch Symbol(-n) {
				case Block:
					c.indentLevel++
					c.undentLBraceIndex = firstIndex(ast[:next])
					c.undentRBraceIndex = lastIndex(ast[:next])
				case CaseHead, CommHead:
					c.indentLevel++
				case ParameterList, CallSuffix:
					c.inParams = true
				case SimpleExpr:
					// Lookahead: If the child nodes contain an AddOp, set the flag.
					if containsNode(ast[2:next], AddOp) {
						c.hasAddOp = true
					}
				case StructType:
					c.indentLevel++
					c.undentLBraceIndex = firstIndex(ast[:next])
					c.undentRBraceIndex = lastIndex(ast[:next])
				}
				walk(ast[2:next], c)
			default:
				tok := f.p.Token(n)
				sep := tok.SepBytes()
				src := tok.SrcBytes()
				var indentDelta int32
				switch Symbol(tok.Ch) {
				case SEMICOLON:
					// Synthetic tokens from semicolon injection have empty src.
					if len(src) == 0 {
						// Keep the sep for later prepending it to the sep of the next token.
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

				// Prepend the previous synthetic token sep, if any.
				if len(syntheticSep) != 0 {
					sep = append(syntheticSep, sep...)
					syntheticSep = syntheticSep[:0]
				}

				seps = f.parseSep(sep, seps)
				// Ensure we always evaluate spacing if there's no explicit white space token
				if len(seps) == 0 {
					seps = append(seps, whiteSpace(0))
				} else if _, isWS := seps[len(seps)-1].(whiteSpace); !isWS {
					seps = append(seps, whiteSpace(0))
				}
				f.formatSep(seps, c.indentLevel, Symbol(tok.Ch), c)
				f.tabs(f.nl, c.indentLevel+indentDelta)

				// Finally emit the token text.
				f.b(src)
				f.prevPrevTok = f.prevTok
				f.prevTok = Symbol(tok.Ch)
			}
			ast = ast[next:]
		}
	}

	walk(f.ast, formatterCtx{undentRBraceIndex: -1})
	return err
}
