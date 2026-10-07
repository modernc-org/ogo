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

// TestEmitCAssertLifetime: what a type assertion hands back is the pointer its
// operand holds, and a header's declaration binds as the statement does. Both were
// read by no lifetime rule: `keepT = r.(*T)` and a type switch's `v` for an r
// holding a local's address, and a callee keeping its parameter through `p :=
// r.(*T)` or through a variable an if or a switch header declares, `if p := r;
// ...`, kept the address in a package variable in silence -- read through after the
// function returned, garbage on the board. The controls keep what is no escape.
func TestEmitCAssertLifetime(t *testing.T) {
	const head = `type T struct{ n int }

func (t *T) get() int { return t.n }

type G interface{ get() int }

var keepT *T

var gt T

func use(p *T) int { return p.n }

`
	for _, test := range []struct {
		name, src string
		refused   bool
	}{
		{"assert", `func run() { t := T{7}; var r G = &t; keepT = r.(*T) }`, true},
		{"assert ok", `func run() { t := T{7}; var r G = &t; p, ok := r.(*T); if ok { keepT = p } }`, true},
		{"assert var", `func run() { t := T{7}; var r G = &t; p := r.(*T); keepT = p }`, true},
		{"type switch", `func run() { t := T{7}; var r G = &t; switch v := r.(type) { case *T: keepT = v } }`, true},
		{"callee assert", `func keep(r G) { keepT = r.(*T) }

func run() { t := T{7}; keep(&t) }`, true},
		{"callee assert ok in if", `func keep(r G) { if p, ok := r.(*T); ok { keepT = p } }

func run() { t := T{7}; keep(&t) }`, true},
		{"callee returns assert", `func pass(r G) *T { return r.(*T) }

func keep(r G) { keepT = pass(r) }

func run() { t := T{7}; keep(&t) }`, true},
		{"callee if header", `func keep(r *T) { if p := r; p != nil { keepT = p } }

func run() { t := T{7}; keep(&t) }`, true},
		{"callee switch header", `func keep(r *T) { switch p, n := r, 1; n { case 1: keepT = p } }

func run() { t := T{7}; keep(&t) }`, true},
		{"callee header store", `func keep(r *T) { if keepT = r; keepT != nil { println(1) } }

func run() { t := T{7}; keep(&t) }`, true},
		{"type switch behind an init", `func run() { t := T{7}; switch r := G(&t); v := r.(type) { case *T: keepT = v } }`, true},
		{"type switch behind an assignment", `func run() { t := T{7}; var r G; switch r = &t; v := r.(type) { case *T: keepT = v } }`, true},
		{"callee type switch behind an init", `func keep(p G) { switch q := p; v := q.(type) { case *T: keepT = v } }

func run() { t := T{7}; keep(&t) }`, true},
		{"callee type switch behind a statement", `func keep(p G) { switch use(nil); v := p.(type) { case *T: keepT = v } }

func run() { t := T{7}; keep(&t) }`, true},
		{"control type switch behind an init", `func keep(p G) { switch q := p; v := q.(type) { case *T: println(v.n) } }

func run() { t := T{7}; keep(&t) }`, false},
		{"control assert read", `func run() { t := T{7}; var r G = &t; println(r.(*T).n) }`, false},
		{"control switch read", `func run() { t := T{7}; var r G = &t; switch v := r.(type) { case *T: println(v.n) } }`, false},
		{"control if header", `func keep(r *T) { if p := r; p != nil { println(use(p)) } }

func run() { t := T{7}; keep(&t) }`, false},
		{"control package", `func run() { var r G = &gt; keepT = r.(*T) }`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := head + test.src + "\n\nfunc main() {\n\trun()\n\tprintln(keepT != nil)\n}\n"
			pkg, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}})
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked(), Inline())
			}
			switch {
			case test.refused && (err == nil || !strings.Contains(err.Error(), "local")):
				t.Fatalf("got %v, want a lifetime refusal", err)
			case !test.refused && err != nil:
				t.Fatalf("refused: %v", err)
			}
		})
	}
}
