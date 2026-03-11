// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"io"
)

type formatter struct {
	ast []int32
	p *Parser
}

func newFormatter(fn string, b []byte) (r *formatter, err error) {
	var p Parser
	r = &formatter{
		p: &p,
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

func formatFile(fn string, b []byte, w io.Writer) (err error) {
	f, err := newFormatter(fn, b)
	if err != nil {
		return err
	}

	_ = f
	panic(todo(""))
}
