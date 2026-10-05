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

// TestEmitCBuiltinShadowLifetime: a function, a parameter or a package variable of
// the program's named like a builtin is judged by the lifetime rules as what it is.
// Each keeps the address it is given, so a local's is refused; the controls hand it
// package storage and compile. Taken for the builtins, copy and print were calls
// keeping nothing, and len a read.
func TestEmitCBuiltinShadowLifetime(t *testing.T) {
	const tail = `
func main() {
	f()
	println(g != nil)
}
`
	for _, test := range []struct {
		name, decl, body string
	}{
		{"own copy", "func copy(p *int) { g = p }", "copy(&x)"},
		{"own copy of two slices", "func copy(dst, src []*int) int {\n\tg = src[0]\n\treturn 1\n}", "var d [1]*int\n\tcopy(d[:], []*int{&x})"},
		{"own append", "func append(s []*int, p *int) []*int {\n\tg = p\n\treturn s\n}", "var xs []*int\n\txs = append(xs, &x)\n\t_ = xs"},
		{"own min", "func min(p, q *int) *int {\n\tg = p\n\treturn q\n}", "_ = min(&x, nil)"},
		{"own print", "func print(p *int) { g = p }", "print(&x)"},
		{"package variable copy", "var copy = func(p *int) { g = p }", "copy(&x)"},
	} {
		for _, local := range []bool{true, false} {
			name := test.name
			x := "x := 5"
			if !local {
				name += " (package storage)"
				x = "x = 5"
			}
			t.Run(name, func(t *testing.T) {
				src := "var g *int\n\n" + test.decl + "\n\nfunc f() {\n\t" + x + "\n\t" + test.body + "\n}\n" + tail
				if !local {
					src += "\nvar x int\n"
				}
				fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
				pkg, err := Build(-1, []string{"main.ogo"}, fsys)
				if err == nil {
					err = EmitC(pkg, io.Discard, Checked(), Inline())
				}
				switch {
				case local && (err == nil || !strings.Contains(err.Error(), "declare x at package scope")):
					t.Fatalf("got %v, want a lifetime refusal\n%s", err, src)
				case !local && err != nil:
					t.Fatalf("refused: %v\n%s", err, src)
				}
			})
		}
	}
}
