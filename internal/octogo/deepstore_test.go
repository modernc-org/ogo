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
