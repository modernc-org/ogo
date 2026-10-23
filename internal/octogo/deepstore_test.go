// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmitCStoreDepth: a callee storing its parameter through storage it was
// handed is asked how many pointers the store goes through. One, from what the
// callee holds by value -- `h.p = v` for a pointer parameter, `s[1] = v` for a
// slice, `w := h; w.p = v` -- lands in what the caller passed, and the caller
// decides by its argument; any other lands past a pointer loaded out of storage,
// which no rule can name, and is taken to outlive every frame. Read by the store's
// root alone, each of these let `run` put its local's address in the package
// variable g, which a later read found holding a dead frame's bytes.
func TestEmitCStoreDepth(t *testing.T) {
	const head = `type T struct {
	p *int
}

type U struct {
	q *T
	t T
	s []*int
}

func (t *T) set(v *int) { t.p = v }

func fill(t *T, v *int) { t.p = v }

func fillS(s []*int, v *int) { s[1] = v }

var g T

var gs [2]*int

var gu = U{q: &g, s: gs[:]}

func get() *T { return &g }

`
	const call = "local := 42\n\tvar u U\n\tu.q, u.s = &g, gs[:]\n\th := &g\n\t"
	for _, test := range []struct{ callee, call string }{
		{"func put(h *U, v *int) { h.q.p = v }", "put(&u, &local)"},
		{"func put(hh **T, v *int) { (*hh).p = v }", "put(&h, &local)"},
		{"func put(hh **T, v *int) { p := *hh; p.p = v }", "put(&h, &local)"},
		{"func put(h *U, v *int) { q := h.q; q.p = v }", "put(&u, &local)"},
		{"func put(h *U, v *int) { (*h).q.p = v }", "put(&u, &local)"},
		{"func put(hh **T, v *int) { **hh = T{p: v} }", "put(&h, &local)"},
		{"func put(v *int) { q := gu.q; q.p = v }", "put(&local)"},
		{"func put(u U, v *int) { u.q.p = v }", "put(u, &local)"},
		{"func (u *U) put(v *int) { u.q.p = v }", "u.put(&local)"},
		{"func (u *U) put(v *int) { n := u.q; n.p = v }", "u.put(&local)"},
		{"func (u U) put(v *int) { u.q.p = v }", "u.put(&local)"},
		{"func put(u *U, v *int) { u.s[1] = v }", "put(&u, &local)"},
		{"func put(u U, v *int) { u.s[1] = v }", "put(u, &local)"},
		{"func put(u *U, v *int) { copy(u.s, []*int{v, v}) }", "put(&u, &local)"},
		{"func put(u *U, v *int) { n := 0; n, u.q.p = 1, v; gs[0] = nil; _ = n }", "put(&u, &local)"},
		{"func put(u *U, v *int) { for i := 0; i < 1; u.q.p = v { i++ } }", "put(&u, &local)"},
		{"func put(v *int) { n := get(); n.p = v }", "put(&local)"},
		{"func put(hh **T, v *int) { p := *hh; p.set(v) }", "put(&h, &local)"},
		{"func put(u *U, v *int) { fill(u.q, v) }", "put(&u, &local)"},
		{"func put(hh **T, v *int) { fill(*hh, v) }", "put(&h, &local)"},
		{"func put(u *U, v *int) { fillS(u.s, v) }", "put(&u, &local)"},
		{"func put(u U, v *int) { fill(u.q, v) }", "put(u, &local)"},
		{"func put(u *U, v *int) { q := u.q; fill(q, v) }", "put(&u, &local)"},
		{"func put(v *int) { fill(get(), v) }", "put(&local)"},
		// What lands in the caller's own storage is the caller's to decide, and a
		// local it passed keeps the reference in its frame.
		{"func put(h *U, v *int) { h.t.p = v }", "put(&u, &local)"},
		{"func put(t *T, v *int) { w := t; w.p = v }", "var lt T\n\tput(&lt, &local)\n\t_ = lt"},
		{"func put(t *T, v *int) { w := t; w.set(v) }", "var lt T\n\tput(&lt, &local)\n\t_ = lt"},
		{"func put(t *T, v *int) { fill(t, v) }", "var lt T\n\tput(&lt, &local)\n\t_ = lt"},
		{"func put(s []*int, v *int) { fillS(s, v) }", "var ls [2]*int\n\tput(ls[:], &local)\n\t_ = ls"},
		{"func put(u *U, v *int) { fill(&u.t, v) }", "put(&u, &local)"},
		{"func put(v *int) { var lt T; w := &lt; w.p = v; fill(w, v); gs[0] = nil }", "put(&local)"},
	} {
		refused := !strings.Contains(test.callee, "h.t.p") && !strings.Contains(test.call, "var l") &&
			!strings.Contains(test.callee, "&u.t") && !strings.Contains(test.callee, "var lt")
		src := head + test.callee + "\n\nfunc run() {\n\t" + call + test.call + "\n\tprintln(u.q != nil, h != nil)\n}\n\nfunc main() {\n\trun()\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case refused && (err == nil || !strings.Contains(err.Error(), "cannot pass the address of local variable local")):
			t.Errorf("%s: got %v, want the local's address refused", test.callee, err)
		case !refused && err != nil:
			t.Errorf("%s: refused: %v", test.callee, err)
		}
	}
}

// TestEmitCStoreBindingForms: the store a summary records is typed from every form
// that binds the local it goes through -- a range clause's element, a for clause, one
// of a call's several results, a comma-ok form, a receive, a type switch's name in
// its clause, a declaration's written type, a literal, a method's or a function
// value's result -- and a store whose target nothing types is taken to go where no
// rule can name. Each refused row was read as a store into the local's own storage,
// and `put` left run's local address where an element or a result pointed, in
// silence.
func TestEmitCStoreBindingForms(t *testing.T) {
	const head = `type T struct {
	p *int
	n int
}

type U struct {
	q  *T
	hs []*T
	ts [2]*T
}

type W struct{ q *T }

type I interface{ get() int }

func (t *T) get() int { return t.n }

func (u *U) get() int { return 1 }

func (t *T) set(v *int) { t.p = v }

func (u *U) next() *T { return u.q }

func (u *U) two() (*T, int) { return u.q, 1 }

func fill(t *T, v *int) { t.p = v }

func two(u *U) (*T, int) { return u.q, 1 }

func first(u *U) *T { return u.q }

func mk() (T, int) { return T{n: 5}, 1 }

var pick = first

var pick2 = two

var g T

var ch = make(chan *T)

var cht = make(chan T)

`
	const call = "local := 42\n\tvar u U\n\tu.q, u.hs, u.ts[0] = &g, []*T{&g}, &g\n\tvar lt T\n\t"
	for _, test := range []struct {
		callee, call string
		refused      bool
	}{
		{"func put(hs []*T, v *int) { for _, h := range hs { h.p = v } }", "put(u.hs, &local)", true},
		{"func put(u *U, v *int) { for _, h := range u.hs { h.p = v } }", "put(&u, &local)", true},
		{"func put(a *[2]*T, v *int) { for _, h := range a { h.p = v } }", "put(&u.ts, &local)", true},
		{"func put(u *U, v *int) { for i, h := range u.ts { if i == 0 { h.p = v } } }", "put(&u, &local)", true},
		{"func put(hs []*T, v *int) { for _, h := range hs[:1] { h.p = v } }", "put(u.hs, &local)", true},
		{"func put(u *U, v *int) { var h *T; for _, h = range u.hs {}; h.p = v }", "put(&u, &local)", true},
		{"func put(v *int) { for h := range ch { h.p = v; break } }", "put(&local)", true},
		{"func put(hs []*T, v *int) { for _, h := range hs { h.set(v) } }", "put(u.hs, &local)", true},
		{"func put(hs []*T, v *int) { for _, h := range hs { fill(h, v) } }", "put(u.hs, &local)", true},
		{"func put(u *U, v *int) { for h := u.q; h != nil; h = nil { h.p = v } }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { var h *T; for h = u.q; h != nil; h = nil { h.p = v } }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { h, n := two(u); h.p = v; _ = n }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { h, n := u.two(); h.p = v; _ = n }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { h, n := pick2(u); h.p = v; _ = n }", "put(&u, &local)", true},
		{"func put(x I, v *int) { h := x.(*U); h.q.p = v }", "put(&u, &local)", true},
		{"func put(x I, v *int) { if h, ok := x.(*U); ok { h.q.p = v } }", "put(&u, &local)", true},
		{"func put(x I, v *int) { switch h := x.(type) { case *U: h.q.p = v } }", "put(&u, &local)", true},
		{"func put(v *int) { h := <-ch; h.p = v }", "put(&local)", true},
		{"func put(v *int) { h, ok := <-ch; if ok { h.p = v } }", "put(&local)", true},
		{"func put(u *U, v *int) { q := u.next(); q.p = v }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { q := pick(u); q.p = v }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { var ps [2]*T; ps[0] = u.q; ps[0].p = v }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { w := W{q: u.q}; w.q.p = v }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { w := &W{q: u.q}; w.q.p = v }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { ps := [2]*T{u.q, nil}; ps[0].p = v }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { ps := []*T{u.q}; ps[0].p = v }", "put(&u, &local)", true},
		{"func put(u *U, v *int) { { h := W{}; _ = h }; { h := u.q; h.p = v } }", "put(&u, &local)", true},
		// What lands in the caller's own storage, or in this frame's, is the
		// caller's to decide.
		{"func put(x I, v *int) { switch h := x.(type) { case *T: h.p = v } }", "put(&lt, &local)", false},
		{"func put(x I, v *int) { switch h := x.(type) { case *T: h.set(v); fill(h, v) } }", "put(&lt, &local)", false},
		{"func put(x I, v *int) { h := x.(*T); h.p = v }", "put(&lt, &local)", false},
		{"func put(x I, v *int) { if h, ok := x.(*T); ok { h.p = v } }", "put(&lt, &local)", false},
		{"func put(ts []T, v *int) { for i := range ts { ts[i].p = v } }", "var ls [2]T\n\tput(ls[:], &local)\n\t_ = ls", false},
		{"func put(ts []T, v *int) int { s := 0; for _, t := range ts { t.p = v; s += *t.p }; return s }", "var ls [2]T\n\tput(ls[:], &local)\n\t_ = ls", false},
		{"func put(v *int) int { t, n := mk(); t.p = v; return *t.p + n }", "put(&local)", false},
		{"func put(v *int) int { var t T; s := 0; for p := &t; p != nil; p = nil { p.p = v; s += *p.p }; return s }", "put(&local)", false},
		{"func put(v *int) int { t := <-cht; t.p = v; return *t.p }", "put(&local)", false},
		{"func put(v *int) int { p := &T{}; p.p = v; return *p.p }", "put(&local)", false},
		{"func put(v *int) int { var ps [2]*int; ps[0] = v; return *ps[0] }", "put(&local)", false},
	} {
		src := head + test.callee + "\n\nfunc run() {\n\t" + call + test.call + "\n\tprintln(u.q != nil, lt.n)\n}\n\nfunc main() {\n\trun()\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused && (err == nil || !strings.Contains(err.Error(), "cannot pass the address of local variable local")):
			t.Errorf("%s: got %v, want the local's address refused", test.callee, err)
		case !test.refused && err != nil:
			t.Errorf("%s: refused: %v", test.callee, err)
		}
	}
}

// TestEmitCRecvHandedBackDeep: a method handing back a place past what its
// receiver points at, `return &p.nodes[i]`, hands back one of the receiver's
// contents, not the receiver -- Go's "leaking param: p to result ~r0 level=1".
// Read as the receiver, a linked list over a pool, `n := p.at(i); n.next =
// p.head; p.head = n`, stored the receiver where it outlives every frame and was
// refused for a local pool; what is stored through the result, or kept of it,
// still goes where the receiver's contents go.
func TestEmitCRecvHandedBackDeep(t *testing.T) {
	const head = `type node struct {
	ptr  *int
	next *node
}

type pool struct {
	nodes []node
	head  *node
}

func (p *pool) at(i int) *node { return &p.nodes[i] }

func (p *pool) get() *node { return p.at(1) }

func (p *pool) push() { n := p.at(1); n.next = p.head; p.head = n }

var storage [4]node

var gq *node

`
	for _, test := range []struct {
		decls, run string
		refused    string
	}{
		{"", "lp := pool{storage[:], nil}\n\tlp.push()\n\tprintln(lp.head != nil)", ""},
		{"func (p *pool) put(v *int) { n := p.at(1); n.ptr = v }", "local := 42\n\tlp := pool{storage[:], nil}\n\tlp.put(&local)", "local variable local"},
		{"func put(p *pool, v *int) { n := p.get(); n.ptr = v }", "local := 42\n\tlp := pool{storage[:], nil}\n\tput(&lp, &local)", "local variable local"},
		{"func keep(p *pool) { gq = p.get() }", "var arr [4]node\n\tlp := pool{arr[:], nil}\n\tkeep(&lp)", "local arr"},
		{"func (p *pool) keep() { gq = p.at(1) }", "var arr [4]node\n\tlp := pool{arr[:], nil}\n\tlp.keep()", "local arr"},
	} {
		src := head + test.decls + "\n\nfunc run() {\n\t" + test.run + "\n}\n\nfunc main() {\n\trun()\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)):
			t.Errorf("%s: got %v, want %q refused", test.decls, err, test.refused)
		case test.refused == "" && err != nil:
			t.Errorf("%s: refused: %v", test.decls, err)
		}
	}
}

// TestEmitCEmbeddedPointerStores: a field promoted through an embedded pointer,
// `pp.p` for a `type P struct{ *T }`, is written, read and addressed through what
// that member holds, and a method promoted through any embedded field is called on
// that member -- as the explicit `pp.T.p` and `h.T.set(v)` are. No rule counted the
// pointer the promotion hides, and the summaries named a promoted method on the
// outer type, which declares none: every refused row kept a local's address in g
// or ge, in silence.
func TestEmitCEmbeddedPointerStores(t *testing.T) {
	const head = `type T struct {
	p *int
	n int
}

func (t *T) set(v *int) { t.p = v }

type E struct {
	T
	k int
}

type P struct {
	*T
	k int
}

type Q struct{ P }

func fillp(pp **int, v *int) { *pp = v }

var g T

var ge E

`
	for _, test := range []struct {
		decls, run string
		refused    bool
	}{
		{"", "pp := P{T: &g}\n\tpp.p = &local", true},
		{"", "q := Q{P{T: &g}}\n\tq.p = &local", true},
		{"", "pp := P{T: &g}\n\tq := &pp\n\tq.p = &local", true},
		{"", "pp := P{T: &g}\n\tfillp(&pp.p, &local)", true},
		{"func put(h *P, v *int) { h.p = v }", "pp := P{T: &g}\n\tput(&pp, &local)", true},
		{"func put(h P, v *int) { h.p = v }", "put(P{T: &g}, &local)", true},
		{"func (h P) put(v *int) { h.p = v }", "pp := P{T: &g}\n\tpp.put(&local)", true},
		{"func put(h *P, v *int) { w := h; w.p = v }", "pp := P{T: &g}\n\tput(&pp, &local)", true},
		{"func put(h *P, v *int) { fillp(&h.p, v) }", "pp := P{T: &g}\n\tput(&pp, &local)", true},
		{"func put(h *P, v *int) { h.set(v) }", "pp := P{T: &g}\n\tput(&pp, &local)", true},
		{"func put(q *Q, v *int) { q.set(v) }", "q := Q{P{T: &g}}\n\tput(&q, &local)", true},
		{"func put(h *E, v *int) { h.set(v) }", "put(&ge, &local)", true},
		{"func put(h *E, v *int) { defer h.set(v) }", "put(&ge, &local)", true},
		{"var gn *int\n\nfunc addr(pp P) *int { return &pp.n }", "var t T\n\tgn = addr(P{T: &t})\n\tprintln(local)", true},
		// An embedded value is storage of what it is part of; an embedded pointer's
		// is what it holds, which the checker asked as the address of the local.
		{"", "var le E\n\tle.p = &local\n\tle.set(&local)\n\tprintln(*le.p)", false},
		{"var gn *int", "pp := P{T: &g}\n\tgn = &pp.n\n\tprintln(local)", false},
		{"func addr(pp P) *int { return &pp.n }", "println(*addr(P{T: &g}), local)", false},
		{"func put(h *E, v *int) { h.p = v; h.set(v) }", "var le E\n\tput(&le, &local)\n\tprintln(*le.p)", false},
	} {
		src := head + test.decls + "\n\nfunc run() {\n\tlocal := 42\n\t" + test.run + "\n}\n\nfunc main() {\n\trun()\n\tprintln(g.n, ge.n)\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused && (err == nil || !strings.Contains(err.Error(), "local variable")):
			t.Errorf("%s / %s: got %v, want the local's address refused", test.decls, test.run, err)
		case !test.refused && err != nil:
			t.Errorf("%s / %s: refused: %v", test.decls, test.run, err)
		}
	}
}

// TestEmitCDerefPointerToPointerCalls: a method called through a dereference of a
// pointer to a pointer, `(*hh).m()` and `(**hh).m()` for a `**T`, is called on the
// pointer hh points at -- and through a pointer to an interface, `(*q).Keep()`, on
// what that interface value holds. The emitter read the first as a call into a
// package named hh and refused the second ("cannot call pointer method"); the
// summaries named no method of a `**T`, and asked nothing of a method's result
// written `(*h).self()`. The refused rows keep a local's address where it outlives
// the frame; the allowed ones are the same calls on a package variable.
func TestEmitCDerefPointerToPointerCalls(t *testing.T) {
	const head = `type T struct {
	n int
	p *int
}

type Keeper interface{ Keep() }

var gp *T

var g T

func (t *T) keep()      { gp = t }
func (t *T) put(v *int) { t.p = v }
func (t *T) self() *T   { return t }
func (t *T) Keep()      { gp = t }
func (t T) val() int    { return t.n }

`
	const local = "var t T\n\th := &t\n\t"
	for _, test := range []struct {
		decls, run string
		refused    string
	}{
		{"", local + "hh := &h\n\t(*hh).keep()", "cannot call keep on t"},
		{"", local + "hh := &h\n\t(**hh).keep()", "cannot call keep on local t"},
		{"", local + "hh := &h\n\tdefer (*hh).keep()", "cannot call keep on t"},
		{"", "h := &g\n\thh := &h\n\tx := 42\n\t(*hh).put(&x)", "receiver *hh"},
		{"", local + "hh := &h\n\tgp = (*hh).self()", "local variable t"},
		{"", local + "hh := &h\n\tgp = (**hh).self()", "local variable t"},
		{"", local + "hh := &h\n\tx := (**hh).self()\n\tgp = x", "pointer into local t"},
		{"", local + "gp = (*h).self()", "local variable t"},
		{"", local + "var s Keeper = &t\n\tq := &s\n\t(*q).Keep()\n\tprintln(h.n)", "cannot call Keep on t"},
		{"func f(hh **T) { (*hh).keep() }", local + "f(&h)", "pointer into local t"},
		{"func f(hh **T) { (**hh).keep() }", local + "f(&h)", "pointer into local t"},
		{"func f(hh **T) { defer (*hh).keep() }", local + "f(&h)", "pointer into local t"},
		{"func f(hh **T) { go (*hh).keep() }", local + "f(&h)", "pointer into local t"},
		{"func f(hh **T) {\n\tp := *hh\n\tp.keep()\n}", local + "f(&h)", "pointer into local t"},
		{"func f(hh **T) { gp = (*hh).self() }", local + "f(&h)", "pointer into local t"},
		{"func f(hh **T, v *int) { (*hh).put(v) }", "h := &g\n\tx := 42\n\tf(&h, &x)", "local variable x"},
		{"func f(hh **T, v *int) { (**hh).put(v) }", "h := &g\n\tx := 42\n\tf(&h, &x)", "local variable x"},
		{"func f(q *Keeper) { (*q).Keep() }", local + "var s Keeper = &t\n\tf(&s)\n\tprintln(h.n)", "pointer into local t"},
		{"func f(q *Keeper) { defer (*q).Keep() }", local + "var s Keeper = &t\n\tf(&s)\n\tprintln(h.n)", "pointer into local t"},
		{"", "h := &g\n\thh := &h\n\t(*hh).keep()\n\t(**hh).keep()\n\tgp = (*hh).self()\n\tgp = (**hh).self()\n\tx := (*hh).self()\n\ty := (**hh).self()\n\tgp = x\n\tgp = y", ""},
		{"func f(hh **T) { (*hh).keep() }", "h := &g\n\tf(&h)", ""},
		{"func f(hh **T) {\n\tp := *hh\n\tp.keep()\n}", "h := &g\n\tf(&h)", ""},
		{"func f(hh **T) { gp = (**hh).self() }", "h := &g\n\tf(&h)", ""},
		{"func f(hh **T, v *int) { (*hh).put(v) }", "h := &g\n\tf(&h, &g.n)", ""},
		{"func f(hh **T) int { return (*hh).val() + (**hh).val() }", local + "println(f(&h))", ""},
		{"func f(q *Keeper) { (*q).Keep() }", "var s Keeper = &g\n\tf(&s)", ""},
	} {
		src := head + test.decls + "\n\nfunc run() {\n\t" + test.run + "\n}\n\nfunc main() {\n\trun()\n\tprintln(g.n, gp != nil)\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)):
			t.Errorf("%s / %s: got %v, want %q refused", test.decls, test.run, err, test.refused)
		case test.refused == "" && err != nil:
			t.Errorf("%s / %s: refused: %v", test.decls, test.run, err)
		}
	}
}

// TestEmitCArrayViews: a slice of an ARRAY is a view of the storage the array is
// in -- the root's own, or what a pointer on the way points at. The summaries read
// `r.buf[:]` for an `r *R` as r's contents, which is a slice field's answer, so a
// callee handing back, keeping or sending a view of its parameter's array kept
// nothing of the caller's: every refused row kept a slice of a local's array past
// its frame, in silence. And the rule inside a function read an array reached
// through a pointer, a field's or an embedded one, as the root's own storage:
// `gs = ue.buf[:]` for a `UE{ *R }` holding a package variable's address was
// refused as a view of local ue, which Go takes.
func TestEmitCArrayViews(t *testing.T) {
	const head = `type R struct {
	buf [4]byte
	xs  []byte
}

type U struct {
	R
	k int
}

type UE struct{ *R }

type V struct {
	p *R
	k int
}

var gs []byte

var gback [4]byte

var gr = &R{}

var out = make(chan []byte)

func (r *R) bytes() []byte { return r.buf[:] }

func view(r *R) []byte { return r.buf[:] }

func add(r *R) []byte { return append(r.buf[:0], 1) }

`
	for _, test := range []struct {
		decls, run string
		refused    string
	}{
		{"", "var r R\n\tgs = view(&r)", "local variable r"},
		{"func view2(r *R) []byte { return r.buf[1:3] }", "var r R\n\tgs = view2(&r)", "local variable r"},
		{"", "var r R\n\tgs = r.bytes()", "local variable r"},
		{"", "var u U\n\tgs = u.bytes()", "local variable u"},
		{"func viewU(u *U) []byte { return u.buf[:] }", "var u U\n\tgs = viewU(&u)", "local variable u"},
		{"func dview(r *R) []byte { return (*r).buf[:] }", "var r R\n\tgs = dview(&r)", "local variable r"},
		{"", "var r R\n\ts := view(&r)\n\tgs = s", "local s"},
		{"func id(s []byte) []byte { return s }", "var r R\n\tgs = id(view(&r))", "local variable r"},
		{"", "var r R\n\tgs = add(&r)", "local variable r"},
		{"func keep(r *R) { gs = r.buf[:] }", "var r R\n\tkeep(&r)", "local variable r"},
		{"func send(r *R) { out <- r.buf[:2] }", "var r R\n\tsend(&r)", "local variable r"},
		{"", "var r R\n\tue := UE{R: &r}\n\tgs = ue.buf[:]", "local r"},
		{"", "var r R\n\tv := V{p: &r}\n\tgs = v.p.buf[:]", "local r"},
		{"func viewUV(u UE) []byte { return u.buf[:] }", "var r R\n\tgs = viewUV(UE{R: &r})", "local variable r"},
		{"func tail(r *R) []byte { return r.xs[1:] }", "var r R\n\tr.xs = gback[:]\n\tgs = tail(&r)", ""},
		{"", "gs = view(gr)\n\tgs = gr.bytes()\n\tgs = add(gr)", ""},
		{"", "ue := UE{R: gr}\n\tgs = ue.buf[:]\n\tgs = ue.bytes()", ""},
		{"", "v := V{p: gr}\n\tgs = v.p.buf[:]", ""},
		{"func viewUV(u UE) []byte { return u.buf[:] }", "gs = viewUV(UE{R: gr})", ""},
		{"func viewV(v V) []byte { return v.p.buf[:] }", "gs = viewV(V{p: gr})", ""},
		{"", "var r R\n\tr.buf[1] = 2\n\tprintln(len(view(&r)), r.bytes()[1], len(add(&r)))", ""},
	} {
		src := head + test.decls + "\n\nfunc run() {\n\t" + test.run + "\n}\n\nfunc main() {\n\trun()\n\tprintln(len(gs), gback[0])\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)):
			t.Errorf("%s / %s: got %v, want %q refused", test.decls, test.run, err, test.refused)
		case test.refused == "" && err != nil:
			t.Errorf("%s / %s: refused: %v", test.decls, test.run, err)
		}
	}
}

// TestEmitCParenHeadListStores: a store through a pointer no variable holds -- a
// dereference of an expression, `(*gt.np).q`, or stars over parentheses, `*(t.pp)`
// -- is asked as a single target, as a list's head and after another target, and
// the three agree. The list forms were refused, `*(t.pp), k = v, 1` "only
// assignment to a simple variable" and `k, *(t.pp) = 1, v` "unsupported target",
// and asking them found the single form's hole: a callee storing its parameter
// through stars over a parenthesised chain, `*(t.pp) = v`, `**(&t.pp) = v`, was
// summarised as storing nothing, so `put(&gt, &x)` kept a local's address in gp.
// A parenthesised chain, `(t.p)` and `(t.np).q`, is asked the same way: it was
// refused in every form, and a list's item of one, read for a callee's summary
// before its locals had types, would have been nobody's.
func TestEmitCParenHeadListStores(t *testing.T) {
	const head = `type N struct{ q *int }

type T struct {
	p  *int
	pp **int
	np *N
}

var g int
var gp *int
var gn N
var gt = T{pp: &gp, np: &gn}

func get() *T { return &gt }

`
	const local = "x := 1\n\t_ = x\n\t"
	for _, test := range []struct {
		decls, run, target, value string
		refused                   string
	}{
		{"", local, "*(gt.pp)", "&x", "local variable x"},
		{"", local, "*(gt.pp)", "&g", ""},
		{"", local, "(*gt.np).q", "&x", "local variable x"},
		{"", local, "(*gt.np).q", "&g", ""},
		{"", local, "(*get()).p", "&x", "local variable x"},
		{"", local, "(*get()).p", "&g", ""},
		{"", local, "*(get()).pp", "&x", "local variable x"},
		{"", local, "*(get().pp)", "&g", ""},
		{"", local, "(*get().np).q", "&x", "local variable x"},
		{"", local + "var t T\n\tt.pp = &gp\n\t", "*(t.pp)", "&x", "local variable x"},
		{"", local + "var t T\n\tt.np = &gn\n\t", "(*t.np).q", "&x", "local variable x"},
		{"", local + "pt := &gt\n\t", "*(pt).pp", "&x", "local variable x"},
		{"", local + "pt := &gt\n\t", "**(pt).pp", "x", ""},
		{"func put(t *T, v *int) { STORE }", local + "put(&gt, &x)", "*(t.pp)", "v", "local variable x"},
		{"func put(t *T, v *int) { STORE }", local + "put(&gt, &x)", "**(&t.pp)", "v", "local variable x"},
		{"func put(t *T, v *int) { STORE }", local + "put(&gt, &x)", "*((t.pp))", "v", "local variable x"},
		{"func put(t *T, v *int) { STORE }", "put(&gt, &g)", "*(t.pp)", "v", ""},
		{"func put(t *T, v *int) { STORE }", local + "put(&gt, &x)", "(*t.np).q", "v", "local variable x"},
		{"func put(t *T, v *int) { STORE }", "put(&gt, &g)", "(*t.np).q", "v", ""},
		{"func put(t *T, v *int) { STORE }", local + "put(&gt, &x)", "*(t).pp", "v", "local variable x"},
		{"func put(t *T, v *int) { STORE }", local + "var lt T\n\tput(&lt, &x)", "(*t).p", "v", ""},
		{"func put(t *T, v *int) { STORE }", local + "put(&gt, &x)", "(*t).p", "v", "local variable x"},
		{"func put(v *int) { STORE }", local + "put(&x)", "(*get()).p", "v", "local variable x"},
		{"func put(v *int) { STORE }", "put(&g)", "(*get()).p", "v", ""},
		{"func put(v *int) { STORE }", local + "put(&x)", "*(get().pp)", "v", "local variable x"},
		{"", local, "(gt.p)", "&x", "local variable x"},
		{"", local, "(gt.p)", "&g", ""},
		{"", local + "var t T\n\t", "(t.p)", "&x", ""},
		{"", local, "(gt.np).q", "&x", "local variable x"},
		{"", local, "(gt.np).q", "&g", ""},
		{"", local + "t := T{np: &gn}\n\t", "(t.np).q", "&x", "local variable x"},
		{"", local, "((gt.p))", "&x", "local variable x"},
		{"func put(t *T, v *int) { STORE }", local + "put(&gt, &x)", "(t.p)", "v", "local variable x"},
		{"func put(t *T, v *int) { STORE }", local + "var lt T\n\tput(&lt, &x)", "(t.p)", "v", ""},
		{"func put(t *T, v *int) { STORE }", "put(&gt, &g)", "(t.p)", "v", ""},
		{"func put(t *T, v *int) { STORE }", local + "put(&gt, &x)", "(t.np).q", "v", "local variable x"},
		{"func put(t *T, v *int) { STORE }", "put(&gt, &g)", "(t.np).q", "v", ""},
		{"func put(t T, v *int) { STORE }", local + "put(gt, &x)", "(t.np).q", "v", "local variable x"},
		{"func put(t T, v *int) { STORE }", local + "put(gt, &x)", "(t.p)", "v", ""},
	} {
		for _, form := range []string{"%s = %s", "%s, k = %s, 1", "k, %s = 1, %s"} {
			store := "k := 0\n\t" + fmt.Sprintf(form, test.target, test.value) + "\n\t_ = k"
			decls, run := strings.Replace(test.decls, "STORE", store, 1), test.run
			if test.decls == "" {
				run += store
			}
			src := head + decls + "\n\nfunc run() {\n\t" + run + "\n}\n\nfunc main() {\n\trun()\n\tprintln(g, gp != nil)\n}\n"
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch where := fmt.Sprintf(form, test.target, test.value); {
			case test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)):
				t.Errorf("%s / %s: got %v, want %q refused", where, test.run, err, test.refused)
			case test.refused == "" && err != nil:
				t.Errorf("%s / %s: refused: %v", where, test.run, err)
			}
		}
	}
}

// TestEmitCRecvSelfStores: a method storing its receiver's address into the
// receiver's own storage, an Init threading a free list through a pool, leaves what
// it is called on holding a pointer into itself. The same store written in the
// function marked the local, and so did a function storing through its parameter,
// `initW(&w)`; the method's summary had no slot for it -- "into its own receiver,
// the receiver goes nowhere" -- so `w.Init(); g = w.free` kept a dead frame's
// address in a package variable, in silence, directly, through a method calling it
// on its receiver, a function calling it on its parameter and a method handing its
// receiver to one. Go moves w to the heap in each.
func TestEmitCRecvSelfStores(t *testing.T) {
	const head = `type T struct {
	next *T
	v    int
}

type W struct {
	free *T
	pool [2]T
}

func (w *W) Init() {
	w.pool[0].v = 7
	w.free = &w.pool[0]
}

func (w *W) alloc() *T {
	t := w.free
	w.free = t.next
	return t
}

func (w *W) first() *T { return w.free }

func (w *W) reset() { w.Init() }

func initW(w *W) { w.Init() }

func (w *W) viaFunc() { initW(w) }

func setup(w *W) { w.free = &w.pool[1] }

func (w *W) viaSetup() { setup(w) }

var g *T
var gw W
`
	for _, test := range []struct {
		run, refused string
	}{
		{"var w W\n\tw.Init()\n\tg = w.free", "pointer into local w"},
		{"var w W\n\tw.Init()\n\tg = w.alloc()", "pointer into local w"},
		{"var w W\n\tw.Init()\n\tg = w.first()", "pointer into local w"},
		{"var w W\n\tpw := &w\n\tpw.Init()\n\tg = w.free", "pointer into local w"},
		{"var w W\n\tw.reset()\n\tg = w.free", "pointer into local w"},
		{"var w W\n\tinitW(&w)\n\tg = w.free", "pointer into local w"},
		{"var w W\n\tw.viaFunc()\n\tg = w.free", "pointer into local w"},
		{"var w W\n\tw.viaSetup()\n\tg = w.free", "pointer into local w"},
		{"var w W\n\tw.Init()\n\tlw := w\n\tg = lw.free", "pointer into local w"},
		{"var w W\n\tw.Init()\n\tx := w.alloc()\n\tprintln(x.v)", ""},
		{"var w W\n\tw.Init()\n\tprintln(w.first().v)", ""},
		{"gw.Init()\n\tg = gw.free", ""},
		{"gw.reset()\n\tg = gw.alloc()", ""},
	} {
		src := head + "\nfunc run() {\n\t" + test.run + "\n}\n\nfunc main() {\n\trun()\n\tprintln(g != nil)\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)):
			t.Errorf("%s: got %v, want %q refused", test.run, err, test.refused)
		case test.refused == "" && err != nil:
			t.Errorf("%s: refused: %v", test.run, err)
		}
	}
}

// TestEmitCArrayPtrViews holds the lifetime rules to a slice of what a POINTER to
// an array points at -- `h.p[1:]` of a field holding one, `(*h.p)[1:]`,
// `(**ppa)[1:]`, `(*ps[0])[1:]`, `pick(&h)[1:]` and a reslice of any: the view is
// of what the pointer reaches, and was asked of the root, which a struct holding
// the address of a local array is no view of. Each was returned, stored, passed to
// a callee keeping it and sent over a dead frame's array -- `104 836` for `5 6` on a
// P2-EDGE -- and `(*ppa)[1:]` of a ppa pointing at a pa holding a package array's
// address was refused as a view of pa. A callee's summary read `return (*h.p)[1:]`
// as handing back nothing, and `g = viewP(&h)` was taken.
func TestEmitCArrayPtrViews(t *testing.T) {
	const head = `type A [3]int16

type H struct{ p *A }

type W struct{ h H }

type G struct{ s []int16 }

var ga = A{1, 2, 3}
var g []int16
var gs [1][]int16
var gh G
var gc = make(chan []int16, 1)

func pick(h *H) *A { return h.p }

func keep(s []int16) { g = s }

func viewP(h *H) []int16 { return (*h.p)[1:] }

func viewPP(pp **A) []int16 { return (**pp)[1:] }
`
	for _, test := range []struct {
		run, refused string
	}{
		{"h := H{p: &a}\n\treturn h.p[1:]", "cannot return h.p[1:], which views local a"},
		{"h := H{p: &a}\n\tg = viewP(&h)\n\treturn nil", "pointer into local a"},
		{"pa := &a\n\treturn viewPP(&pa)", "pointer into local a"},
		{"h := H{p: &ga}\n\tg = viewP(&h)\n\treturn nil", ""},
		{"h := H{p: &a}\n\tg = (*h.p)[1:]\n\treturn nil", "cannot store (*h.p)[1:], which views local a"},
		{"var h H\n\th.p = &a\n\tkeep(h.p[:2])\n\treturn nil", "cannot pass h.p[:2], which views local a"},
		{"pa := &a\n\tppa := &pa\n\treturn (**ppa)[1:]", "which views local a"},
		{"ps := [1]*A{&a}\n\tgs[0] = (*ps[0])[1:]\n\treturn nil", "which views local a"},
		{"w := W{h: H{p: &a}}\n\tgh.s = w.h.p[1:]\n\treturn nil", "which views local a"},
		{"h := H{p: &a}\n\treturn pick(&h)[1:]", "which views local a"},
		{"h := H{p: &a}\n\ts := (*h.p)[1:]\n\tg = s\n\treturn nil", "cannot store a slice backed by local s"},
		{"h := H{p: &a}\n\treturn (*h.p)[1:][1:]", "which views local a"},
		{"h := H{p: &a}\n\tgc <- h.p[1:]\n\treturn nil", "cannot send h.p[1:], which views local a"},
		{"h := H{p: &ga}\n\treturn h.p[1:]", ""},
		{"pa := &ga\n\tppa := &pa\n\treturn (*ppa)[1:]", ""},
		{"h := H{p: &ga}\n\treturn (*pick(&h))[1:]", ""},
		{"h := H{p: &a}\n\tprintln(len(h.p[1:]))\n\treturn nil", ""},
		{"h := H{p: &a}\n\ts := h.p[1:]\n\tprintln(s[0])\n\treturn nil", ""},
	} {
		src := head + "\nfunc run() []int16 {\n\tvar a A\n\t_ = a\n\t" + test.run + "\n}\n\nfunc main() {\n\tprintln(len(run()))\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)):
			t.Errorf("%s: got %v, want %q refused", test.run, err, test.refused)
		case test.refused == "" && err != nil:
			t.Errorf("%s: refused: %v", test.run, err)
		}
	}
}
