// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmitCCheckElision pins which run-time checks a checked build keeps, counted
// in the C of one function, `f` or a method `T_f`. A nil check of a pointer
// parameter or receiver goes where an earlier statement of the function body's own
// list dereferenced it on every path (emitTopStatement), and stays wherever that is
// not sure: after a dereference in a branch, a loop, the lazy operand of && or a
// deferred call, after a label, and for a parameter the function writes, declares
// again or takes the address of. A bound check goes where the index's shape keeps
// it below a constant extent (indexBound) and stays everywhere else. p2-11's
// emulator paid both at every instruction.
func TestEmitCCheckElision(t *testing.T) {
	const decls = `type T struct {
	a, b, c int
	r       [8]int
	big     [256]int
	odd     [255]int
}

func (t *T) ptr() int { return 1 }

var g, g2 T

var c = true

`
	for _, test := range []struct {
		name, body   string // body is the function f, or a method f of *T
		nils, bounds int    // the checks the function's C keeps
	}{
		{"straight line", `func (m *T) f() int {
	m.a = 1
	m.b = m.a + 2
	return m.b + m.c
}`, 1, 0},
		{"a parameter", `func f(m *T) int {
	m.a++
	return m.a
}`, 1, 0},
		{"after a dereference in a branch", `func (m *T) f() {
	if c {
		m.a = 1
	}
	m.b = 2
}`, 2, 0},
		{"after one in an if's header", `func (m *T) f() {
	if m.a > 0 {
		c = false
	}
	m.b = 2
}`, 1, 0},
		{"after the lazy operand of &&", `func (m *T) f() {
	ok := c && m.a > 0
	m.b = 2
	_ = ok
}`, 2, 0},
		{"after the first operand of &&", `func (m *T) f() {
	ok := m.a > 0 && c
	m.b = 2
	_ = ok
}`, 1, 0},
		{"after a loop", `func (m *T) f() {
	for i := 0; i < 2; i++ {
		m.a++
	}
	m.b = 1
}`, 2, 0},
		{"a loop after the check", `func (m *T) f() {
	m.a = 0
	for i := 0; i < 2; i++ {
		m.a++
	}
}`, 1, 0},
		{"after a label a goto reaches", `func (m *T) f() {
	if c {
		goto L
	}
	m.a = 1
L:
	m.b = 2
}`, 2, 0},
		{"a parameter written", `func f(m *T) {
	m.a = 1
	m = &g2
	m.b = 2
}`, 2, 0},
		{"a parameter declared again in a block", `func (m *T) f() {
	m.a = 1
	if c {
		m := &g2
		m.b = 2
	}
	m.c = 3
}`, 3, 0},
		{"a parameter's address taken", `func f(m *T) {
	p := &m
	m.a = 1
	m.b = 2
	_ = p
}`, 2, 0},
		{"after a deferred call", `func (m *T) f() {
	defer println(m.a)
	m.b = 1
}`, 2, 0},
		{"after a pointer method", `func (m *T) f() {
	m.ptr()
	m.b = 1
}`, 1, 0},
		{"a literal's own parameter", `func (m *T) f() int {
	m.a = 1
	h := func(m *T) int { return m.b }
	return h(m)
}`, 2, 0},
		{"a masked index", `func f(x int) int {
	return g.r[x&7] + g.r[(x>>3)&7] + g.r[(x&7)]
}`, 0, 0},
		{"a mask past the extent", `func f(x int) int {
	return g.r[x&8] + g.r[x&-1] + g.r[x&7+1]
}`, 0, 3},
		{"an unsigned remainder", `func f(u uint, i int) int {
	return g.r[u%8] + g.r[i%8] + g.r[u%9]
}`, 0, 2},
		{"a byte index", `func f(b uint8) int {
	return g.big[b] + g.odd[b]
}`, 0, 1},
		{"a shifted halfword", `func f(h uint16) int {
	return g.r[h>>13] + g.r[h>>12]
}`, 0, 1},
		{"a slice keeps its check", `func f(s []int, x int) int {
	return s[x&7]
}`, 0, 1},
		{"a named constant", `const last = 7

func f() int {
	return g.r[last] + g.r[last-1]
}`, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := decls + test.body + "\n\nfunc main() {\n\t_ = &g\n}\n"
			pkg, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}})
			if err != nil {
				t.Fatalf("Build: %v\n%s", err, src)
			}
			var buf bytes.Buffer
			if err := EmitC(pkg, &buf, Checked(), Inline()); err != nil {
				t.Fatalf("EmitC: %v", err)
			}
			body := checkedFuncBody(buf.String())
			if body == "" {
				t.Fatalf("no f in:\n%s", buf.String())
			}
			if g, w := strings.Count(body, "ogo_nil_"), test.nils; g != w {
				t.Errorf("nil checks: got %d, want %d\n%s", g, w, body)
			}
			if g, w := strings.Count(body, "ogo_bound("), test.bounds; g != w {
				t.Errorf("bound checks: got %d, want %d\n%s", g, w, body)
			}
		})
	}
}

// checkedFuncBody is the C definition of the function f, or of the method T_f, and
// of the literals lifted out of the program, `ogo_lit0`.
func checkedFuncBody(c string) string {
	var defs []string
	for _, head := range []string{" T_f(", " f(", " ogo_lit"} {
		for i := 0; ; {
			j := strings.Index(c[i:], head)
			if j < 0 {
				break
			}
			i += j
			nl := strings.IndexByte(c[i:], '\n')
			if nl > 0 && strings.HasSuffix(c[i:i+nl], "{") {
				start := strings.LastIndexByte(c[:i], '\n') + 1
				end := strings.Index(c[i:], "\n}\n")
				defs = append(defs, c[start:i+end+3])
			}
			i += len(head)
		}
	}
	return strings.Join(defs, "")
}
