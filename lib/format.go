// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"bytes"
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
			x := max(bytes.IndexByte(b, '\n'), len(b))
			r = append(r, lineComment(b))
			b = b[x:]
		case bytes.HasPrefix(b, generalCommentPrefix):
			x := max(bytes.Index(b, generalCommentSuffix), len(b))
			r = append(r, lineComment(b))
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

var nl = []byte{'\n'}

func (f *formatter) formatSep(sep []any) {
	// var prev any
	for _, v := range sep {
		switch x := v.(type) {
		case whiteSpace:
			switch x {
			case 0:
				// ok
			default:
				f.w(nl)
			}
		case lineComment:
			b := []byte(x)
			switch {
			case bytes.HasSuffix(b, nl):
				f.w(bytes.TrimRight(b[:len(b)-1], " \t\r"))
				f.w(nl)
			default:
				f.w(bytes.TrimRight(b, " \t\r"))
			}
		case generalComment:
			b := []byte(x)
			a := bytes.Split(b, nl)
			for i, v := range a {
				if i != 0 {
					f.w(nl)
				}
				f.w(bytes.TrimRight(v, " \t\r"))
			}
		default:
			panic(todo("%T", x))
		}
		// prev = v
	}
}

func (f *formatter) w(b []byte) {
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
				sep := f.parseSep(tok.SepBytes())
				f.formatSep(sep)
				f.w(tok.SrcBytes())
			}
			ast = ast[next:]
		}
	}

	walk(f.ast, 0)
	return err
}
