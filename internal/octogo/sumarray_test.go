// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"io"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmitCSummaryArrayElems: a callee reaching an ELEMENT of an array behind its
// parameter -- `p[1]` for a `p *[2]Counter`, `p.arr[1]` for a pointer to a struct
// holding one, a defined array type's, its own receiver's -- and keeping it: a method
// keeping its receiver called on it, plainly, deferred or started on a cog, or its
// address passed on, stored or sent. The summaries followed an index into a SLICE
// only, so every such callee was summarised as keeping nothing, and a caller handing
// it a local array left that array's address in a package variable in silence:
// 8796 on the board where Go prints 7. An address through a parenthesised head,
// `&(*p)[1]`, and a deferred or started method at the end of a chain were unread the
// same way (summaryReach, stmtMethodCalls, the go statement's sink), and so was a
// row of an array of arrays, `p[1][0]`, and a defined row type's method on one,
// `p[1].KeepFirst()` for a `*[2]Row`. Each callee is
// called once on a local, which must be refused, and once on package storage, which
// must not.
func TestEmitCSummaryArrayElems(t *testing.T) {
	const head = `type Counter struct{ N int }

var gp *Counter

var gch chan *Counter

func (c *Counter) Keep() { gp = c }

func (c *Counter) Send() { gch <- c }

func (c *Counter) Bump() { c.N++ }

func keep(c *Counter) { gp = c }

type W2 struct{ arr [2]Counter }

type A [2]Counter

var gcs [2]Counter

var gw2 W2

var ga A

type G [2][2]Counter

type W3 struct{ g [2][2]Counter }

var gg [2][2]Counter

var gw3 W3

var ggx G

type Row [2]Counter

func (r *Row) KeepFirst() { r[0].Keep() }

func (r *Row) BumpFirst() { r[0].Bump() }

var grows [2]Row
`
	for _, test := range []struct {
		callee, kept, ok string
		keepsNone        bool // the callee keeps nothing: both calls build
	}{
		{"func f(p *[2]Counter) { p[1].Keep() }", "f(&cs)", "f(&gcs)", false},
		{"func f(p *[2]Counter) { (*p)[1].Keep() }", "f(&cs)", "f(&gcs)", false},
		{"func f(p *[2]Counter) { keep(&(*p)[1]) }", "f(&cs)", "f(&gcs)", false},
		{"func f(p *[2]Counter) { gp = &(*p)[1] }", "f(&cs)", "f(&gcs)", false},
		{"func f(p *[2]Counter) { gch <- &(*p)[1] }", "f(&cs)", "f(&gcs)", false},
		{"func f(p *[2]Counter) *Counter { return &(*p)[1] }", "gp = f(&cs)", "gp = f(&gcs)", false},
		{"func f(p *[2]Counter) { p[1].Send() }", "f(&cs)", "f(&gcs)", false},
		{"func f(p *[2]Counter) { defer p[1].Keep() }", "f(&cs)", "f(&gcs)", false},
		{"func f(p *[2]Counter) { go p[1].Send() }", "f(&cs)", "f(&gcs)", false},
		{"func f(p *W2) { p.arr[1].Keep() }", "f(&w2)", "f(&gw2)", false},
		{"func f(p *W2) { (p.arr[1]).Keep() }", "f(&w2)", "f(&gw2)", false},
		{"func f(p *W2) { defer p.arr[1].Keep() }", "f(&w2)", "f(&gw2)", false},
		{"func f(p *W2) { go p.arr[1].Send() }", "f(&w2)", "f(&gw2)", false},
		{"func f(p *A) { p[1].Keep() }", "f(&a)", "f(&ga)", false},
		{"func (x *A) K() { x[1].Keep() }", "a.K()", "ga.K()", false},
		{"func (x *W2) K() { x.arr[1].Keep() }", "w2.K()", "gw2.K()", false},
		{"func f(p *[2][2]Counter) { p[1][0].Keep() }", "f(&g2)", "f(&gg)", false},
		{"func f(p *[2][2]Counter) { (*p)[1][0].Keep() }", "f(&g2)", "f(&gg)", false},
		{"func f(p *W3) { p.g[1][0].Keep() }", "f(&w3)", "f(&gw3)", false},
		{"func f(p *[2][2]Counter) { defer p[1][0].Keep() }", "f(&g2)", "f(&gg)", false},
		{"func f(p *[2][2]Counter) { go p[1][0].Send() }", "f(&g2)", "f(&gg)", false},
		{"func (x *G) K() { x[1][0].Keep() }", "gx.K()", "ggx.K()", false},
		{"func f(p *[2]Row) { p[1].KeepFirst() }", "f(&rs)", "f(&grows)", false},
		// Controls: a callee keeping nothing of what it reaches.
		{"func f(p *[2]Counter) { p[1].Bump() }", "f(&cs)", "f(&gcs)", true},
		{"func f(p *W2) { p.arr[1].Bump() }", "f(&w2)", "f(&gw2)", true},
		{"func (x *A) K() { x[1].Bump() }", "a.K()", "ga.K()", true},
		{"func f(p *[2]Counter) { defer p[1].Bump() }", "f(&cs)", "f(&gcs)", true},
		{"func f(p *[2][2]Counter) { p[1][0].Bump() }", "f(&g2)", "f(&gg)", true},
		{"func f(p *[2]Row) { p[1].BumpFirst() }", "f(&rs)", "f(&grows)", true},
	} {
		for _, call := range []string{test.kept, test.ok} {
			src := head + test.callee + `

func run() {
	var cs [2]Counter
	var w2 W2
	var a A
	var g2 [2][2]Counter
	var w3 W3
	var gx G
	var rs [2]Row
	_, _, _, _, _, _, _ = cs, w2, a, g2, w3, gx, rs
	` + call + `
}

func main() {
	run()
}
`
			t.Run(test.callee+"/"+call, func(t *testing.T) {
				fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
				pkg, err := Build(-1, []string{"main.ogo"}, fsys)
				if err == nil {
					err = EmitC(pkg, io.Discard, Checked())
				}
				refuse := !test.keepsNone && call == test.kept
				switch {
				case refuse && err == nil:
					t.Errorf("a reference to this frame left it:\n%s", src)
				case refuse && !strings.Contains(err.Error(), "outlive") && !strings.Contains(err.Error(), "another cog"):
					t.Errorf("refused, but not for its lifetime: %v", err)
				case !refuse && err != nil:
					t.Errorf("refused: %v\n%s", err, src)
				}
			})
		}
	}
}
