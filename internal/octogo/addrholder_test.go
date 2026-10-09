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

// TestEmitCAddrThroughHolder: an address whose steps pass through a pointer or a
// slice a local holds, `&h.p.n`, reaches what h holds, not h's own storage. It was
// refused as the address of local h by the checker and the emitter alike, wherever
// h.p pointed -- a package variable's address included. What h holds decides now
// (addrCrossing, addrThroughRef): a local's address is refused at every sink, a
// package variable's taken.
func TestEmitCAddrThroughHolder(t *testing.T) {
	const head = `type T struct{ n int }

type HP struct{ p *T }

type HS struct{ s []int }

var gt = T{n: 7}

var garr [2]int

var keepN *int

var cn = make(chan *int)

`
	for _, test := range []struct {
		name, src string
		refused   bool
	}{
		{"store package", `func run() { h := HP{&gt}; keepN = &h.p.n }`, false},
		{"return package", `func get() *int { h := HP{&gt}; return &h.p.n }

func run() { keepN = get() }`, false},
		{"slice package", `func run() { h := HS{garr[:]}; keepN = &h.s[1] }`, false},
		{"callee package", `func keep(h HP) { keepN = &h.p.n }

func run() { keep(HP{&gt}) }`, false},
		{"store local", `func run() { t := T{n: 1}; h := HP{&t}; keepN = &h.p.n }`, true},
		{"return local", `func get() *int { t := T{n: 1}; h := HP{&t}; return &h.p.n }

func run() { keepN = get() }`, true},
		{"slice local", `func run() { var a [2]int; h := HS{a[:]}; keepN = &h.s[1] }`, true},
		{"send local", `func run() { t := T{n: 1}; h := HP{&t}; cn <- &h.p.n }`, true},
		{"go local", `func use(p *int) { keepN = p }

func run() { t := T{n: 1}; h := HP{&t}; go use(&h.p.n) }`, true},
		{"callee local", `func keep(h HP) { keepN = &h.p.n }

func run() { t := T{n: 1}; keep(HP{&t}) }`, true},
		{"array field", `type HA struct{ a [2]int }

func run() { h := HA{}; keepN = &h.a[1] }`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := head + test.src + "\n\nfunc main() {\n\trun()\n\tprintln(keepN != nil)\n}\n"
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
