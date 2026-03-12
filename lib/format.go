// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"bytes"
	"fmt"
	"io"
)

type formatter struct {
	ast []int32
	p   *Parser
	err error
	out io.Writer
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

type whiteSpace int        // `[ \t\n\r]+`, value == number of newlines
type lineComment []byte    // The line comment `//.*`
type generalComment []byte // The /* ... */ delimited comment.

var (
	lineCommentPrefix    = []byte("//")
	generalCommentPrefix = []byte("/*")
	generalCommentSuffix = []byte("*/")
)

func (f *formatter) parseSep(b []byte) (r []any) {
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
					// ok
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

var (
	nl = []byte{'\n'}
	nl2 = []byte{'\n', '\n'}
	semi = []byte{';'}
	sp = []byte{' '}
)

func (f *formatter) formatSep(sep []any) {
	// var prev any
	for _, v := range sep {
		switch x := v.(type) {
		case whiteSpace:
			// f.w("<ws>")
			switch x {
			case 0:
				f.b(sp)
			case 1:
				f.b(nl)
			default:
				f.b(nl2)
			}
			// f.w("</ws>")
		case lineComment:
			// f.w("<line>")
			b := []byte(x)
			switch {
			case bytes.HasSuffix(b, nl):
				f.b(bytes.TrimRight(b[:len(b)-1], " \t\r"))
				f.b(nl)
			default:
				f.b(bytes.TrimRight(b, " \t\r"))
			}
			// f.w("</line>")
		case generalComment:
			// f.w("<general>")
			b := []byte(x)
			a := bytes.Split(b, nl)
			for i, v := range a {
				if i != 0 {
					f.b(nl)
				}
				f.b(bytes.TrimRight(v, " \t\r"))
			}
			// f.w("</general>")
		default:
			panic(todo("%T", x))
		}
		// prev = v
	}
}

func (f *formatter) w(s string, args ...any) {
	if f.err != nil {
		return
	}

	_, f.err = fmt.Fprintf(f.out, s, args...)
}

func (f *formatter) b(b []byte) {
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

	var walk func(ast []int32, lvl int)
	var injected []byte
	walk = func(ast []int32, lvl int) {
		for len(ast) != 0 && f.err == nil {
			next := int32(1)
			switch n := ast[0]; {
			case n < 0:
				switch Symbol(-n) {
				case Block:
					lvl++
				}
				next = 2 + ast[1]
				walk(ast[2:next], lvl)
			default:
				tok := f.p.Token(n)
				sepBytes := tok.SepBytes()
				srcBytes := tok.SrcBytes()
				if Symbol(tok.Ch) == SEMICOLON && len(srcBytes) == 0 {
					trc("%v: sep=%q src=%q", tok.Position(), tok.Sep(), tok.Src())
					injected = append(injected[:0], sepBytes...)
					break
				}

				if len(injected) != 0 {
					sepBytes = append(injected, sepBytes...)
					injected = injected[:0]
				}
				sep := f.parseSep(sepBytes)
				f.formatSep(sep)
				f.b(srcBytes)
				// f.w("%q", srcBytes)
			}
			ast = ast[next:]
		}
	}

	walk(f.ast, 0)
	return err
}
