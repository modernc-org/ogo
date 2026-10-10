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

// TestEmitCCallResultSteps asks what a call's result WITH STEPS after it reaches of
// this frame: a field or an element read out of it, an address into it, a slice of
// it, a method returning its receiver called on it, and a call through what a call
// returns or an element holds. frameRefOf answered for a call standing alone, and
// every one of these kept a local where it outlives the function, at every sink
// (2026-10-10). Each beside a control over package storage.
func TestEmitCCallResultSteps(t *testing.T) {
	const head = `type Box struct {
	d []int
	n int
}

type HB struct {
	c     *Box
	inner Box
	arr   [2]*Box
}

type S struct{ p *Box }

var gbp *Box

var gs []int

var gip *int

var gb Box

var gh HB

var ga [4]int

func (b *Box) Self() *Box { return b }

func (h *HB) Self() *HB { return h }

func id(b *Box) *Box { return b }

func idH(h *HB) *HB { return h }

func mkS(b *Box) S { return S{b} }

func mkArr(b *Box) [1]*Box { return [1]*Box{b} }

func idSl(v []int) []int { return v }

func idPs(v []*Box) []*Box { return v }

func pick() func(*Box) *Box { return id }

var tab = [1]func(*Box) *Box{id}

func keep(b *Box) { gbp = b }

func keepS(v []int) { gs = v }

func keepC(h *HB) { gbp = h.c }

func run() {
	var lb Box
	var lh HB
	var a [4]int
	lb.n, lh.inner.n, a[0] = 1, 2, 3
	_, _, _ = lb, lh, a
`
	const tail = `}

func main() {
	run()
	println(gbp != nil, len(gs), gip != nil)
}
`
	for _, test := range []struct {
		stmt string
		want string // "" means the program must be accepted
	}{
		// A value read through what a call returns: what that storage holds.
		{"lh.c = &lb\n\tgbp = idH(&lh).c", "cannot store idH(&lh).c, which reaches local lb in package variable gbp"},
		{"lh.c = &lb\n\tkeep(idH(&lh).c)", "cannot pass idH(&lh).c, which reaches local lb to keep"},
		{"lh.c = &lb\n\tgbp = lh.Self().c", "cannot store lh.Self().c, which reaches local lb"},
		{"lh.c = &lb\n\tgbp = (idH(&lh)).c", "cannot store (idH(&lh)).c, which reaches local lb"},
		{"lh.arr[0] = &lb\n\tgbp = idH(&lh).arr[0]", "cannot store idH(&lh).arr[0], which reaches local lb"},
		{"ps := []*Box{&lb}\n\tgbp = idPs(ps)[0]", "cannot store idPs(ps)[0], which reaches local lb"},
		{"lb.d = a[:]\n\tgs = id(&lb).d", "cannot store id(&lb).d, which reaches local a"},
		// And what a call's result HOLDS, handed to a callee keeping contents.
		{"lh.c = &lb\n\tkeepC(idH(&lh))", "cannot pass idH(&lh), whose contents hold a pointer into local lb to keepC"},
		// A part of a value result: what the value holds.
		{"gbp = mkS(&lb).p", "cannot store mkS(&lb).p, which reaches local lb"},
		{"gbp = mkArr(&lb)[0]", "cannot store mkArr(&lb)[0], which reaches local lb"},
		// An address into, and a slice of, what a call returns: the storage itself.
		{"gbp = &idH(&lh).inner", "cannot store &idH(&lh).inner, which reaches local lh"},
		{"gip = &idH(&lh).inner.n", "cannot store &idH(&lh).inner.n, which reaches local lh"},
		{"gip = &id(&lb).n", "cannot store &id(&lb).n, which reaches local lb"},
		{"gip = &idSl(a[:])[0]", "cannot store &idSl(a[:])[0], which reaches local a"},
		{"gs = idSl(a[:])[1:]", "cannot store idSl(a[:])[1:], which reaches local a"},
		{"keepS(idSl(a[:])[1:])", "cannot pass idSl(a[:])[1:], which reaches local a to keepS"},
		// A method handing back its receiver, called on what a call returns.
		{"gbp = id(&lb).Self()", "cannot store the address of local variable lb in package variable gbp"},
		{"keep(id(&lb).Self())", "cannot pass the address of local variable lb to keep"},
		// A call through what a call returns, or an element holds.
		{"gbp = pick()(&lb)", "cannot store the address of local variable lb in package variable gbp"},
		{"keep(pick()(&lb))", "cannot pass the address of local variable lb to keep"},
		{"gbp = tab[0](&lb)", "cannot store the address of local variable lb in package variable gbp"},
		// Controls: package storage through every shape, and what carries nothing.
		{"lh.c = &gb\n\tgbp = idH(&lh).c\n\tgbp = idH(&gh).c\n\tgbp = &idH(&gh).inner", ""},
		{"gbp = pick()(&gb)\n\tgbp = tab[0](&gb)\n\tgbp = id(&gb).Self()\n\tgbp = mkS(&gb).p", ""},
		{"gip = &id(&gb).n\n\tgs = idSl(ga[:])[1:]\n\tgip = &idSl(ga[:])[0]", ""},
		{"lh.c = &lb\n\tlb.n = idH(&lh).c.n + id(&lb).n + len(idSl(a[:])[1:])", ""},
		{"lh.c = &lb\n\tp := idH(&lh).c\n\tq := pick()(&lb)\n\t_, _ = p, q", ""},
	} {
		t.Run(test.stmt, func(t *testing.T) {
			src := head + "\t" + test.stmt + "\n" + tail
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.want == "" && err != nil:
				t.Errorf("unexpected refusal: %v", err)
			case test.want != "" && err == nil:
				t.Errorf("accepted; want %q", test.want)
			case test.want != "" && !strings.Contains(err.Error(), test.want):
				t.Errorf("got %v\nwant %q", err, test.want)
			}
		})
	}
}
