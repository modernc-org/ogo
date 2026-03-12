// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

var (
	generalCommentPrefix = []byte("/*")
	generalCommentSuffix = []byte("*/")
	lineCommentPrefix    = []byte("//")
	nl                   = []byte("\n")
	nl2                  = []byte("\n\n")
	sp                   = []byte(" ")
)

type formatter struct {
	ast []int32
	err error
	nl  bool // Last byte written to out was a '\n'.
	out io.Writer
	p   *Parser
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

// `[ \t\n\r]+`, value == number of newlines, only the number of newlines is
// preserved.
type whiteSpace int

// The line comment `//.*`, preserved exactly, never includes a newline.
type lineComment []byte

// The /* ... */ delimited comment. Everything between the delimiters is
// preserved, including newlines, if any.
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

func (f *formatter) formatSep(sep []any, indentLevel int32) {
	for _, v := range sep {
		switch x := v.(type) {
		case whiteSpace:
			switch x {
			case 0:
				f.b(sp)
			case 1:
				f.b(nl)
			default:
				f.b(nl2)
			}
		case lineComment:
			if f.nl && indentLevel != 0 {
				f.w("%s", strings.Repeat("\t", int(indentLevel)))
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
			if f.nl && indentLevel != 0 {
				f.w("%s", strings.Repeat("\t", int(indentLevel)))
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

//lint:ignore U1000 debug helper
func (f *formatter) w(s string, args ...any) {
	if f.err != nil {
		return
	}

	_, f.err = fmt.Fprintf(f.out, s, args...)
}

func (f *formatter) b(b []byte) {
	f.nl = bytes.HasSuffix(b, nl)
	if f.err != nil {
		return
	}

	_, f.err = f.out.Write(b)
}

func formatFile(fn string, b []byte, w io.Writer) (err error) {
	f, err := newFormatter(fn, b, w)
	if err != nil {
		return err
	}

	type ctx struct {
		indentLevel int32
		undentRBraceIndex int32
	}
	var walk func(ast []int32, c ctx)
	var syntheticSep []byte
	var seps []any
	walk = func(ast []int32, c ctx) {
		for len(ast) != 0 && f.err == nil {
			next := int32(1)
		outer:
			switch n := ast[0]; {
			case n < 0:
				next = 2 + ast[1]
				switch Symbol(-n) {
				case Block:
					c.indentLevel++
					c.undentRBraceIndex = lastIndex(ast[:next])
				case CaseHead, CommHead:
					c.indentLevel++
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
				f.formatSep(seps, c.indentLevel)
				if tabs := c.indentLevel + indentDelta; f.nl && tabs != 0 {
					f.w("%s", strings.Repeat("\t", int(tabs)))
				}
				f.b(src)
			}
			ast = ast[next:]
		}
	}

	walk(f.ast, ctx{undentRBraceIndex: -1})
	return err
}
