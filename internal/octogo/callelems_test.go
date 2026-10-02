// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestCheckCallResultElemsAcross asks what the checker makes of the steps past
// ANOTHER package's call, `a.Fa()[0]`, `range a.Fp()`, `a.Pick()(x)` -- most of all
// where main declares types of the names a's types have. The walk past a call
// (callChainWalk) began at a function or a variable of this package only, so none
// of it had a type, and once it does, a name it reaches is spelled as a spells it:
// read here bare, `a.A` would be main's A (chainNamed). A program that builds runs
// on the host and prints what Go prints; one Go refuses is refused, in the
// checker's words. call_result_elems.ogo holds the same rows in one package.
//
// The walk asks more than a type, and from another package's call it asked nothing:
// a member that package does not export, a pointer method called on a value with no
// storage, and the address of what has none, `&a.Arr()[0]`, which was taken.
func TestCheckCallResultElemsAcross(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}
	const lib = `type Handler func(n int) int

type A int

type P struct {
	X int
	S string
	y int
}

type C struct{ N int }

func (c *C) Add(n int) { c.N += n }

func (c C) get() int { return c.N }

func inc(n int) int { return n + 1 }

var gc C

var arr [3]int

func Mk() C { return gc }

func Arr() [3]int { return arr }

var Tab = [2]Handler{inc, inc}

var ga = [3]A{1, 2, 3}

var gp = [2]P{{1, "a", 0}, {2, "b", 0}}

func Pick() Handler { return inc }

func Hs() []Handler { return Tab[:] }

func Fa() []A { return ga[:] }

func Fpa() *[3]A { return &ga }

func Fp() []P { return gp[:] }
`
	for _, test := range []struct {
		name, main string
		want       string // the output, or with refuse the message
		refuse     bool
	}{
		{"an element beside main's type of the name", `import "a"

type A string

func main() {
	var x a.A = a.Fa()[0] + a.Fpa()[2]
	var y A = "y"
	println(x, len(a.Fa()), y)
}
`, "4 3 y\n", false},
		{"a range value beside main's type of the name", `import "a"

type A string

func main() {
	n := 0
	for _, v := range a.Fa() {
		var x a.A = v
		n += int(x)
	}
	println(n)
}
`, "6\n", false},
		{"a field of an element beside main's struct of the name", `import "a"

type P struct{ Y int }

func main() {
	for _, p := range a.Fp() {
		println(p.S)
	}
	println(a.Fp()[1].X)
}
`, "a\nb\n2\n", false},
		{"a function value called beside main's type of the name", `import "a"

type Handler func(s string) string

func main() { println(a.Pick()(3), a.Hs()[1](4)) }
`, "4 5\n", false},
		{"an element into main's type of the name", `import "a"

type A string

func main() {
	var x A = a.Fa()[0]
	println(x)
}
`, "cannot use a.Fa()[0] of type a.A as type A in variable declaration", true},
		{"a range value into main's type of the name", `import "a"

type A string

func main() {
	for _, v := range a.Fpa() {
		var x A = v
		println(x)
	}
}
`, "cannot use v of type a.A as type A in variable declaration", true},
		{"a field main's struct of the name has", `import "a"

type P struct{ Y int }

func main() { println(a.Fp()[0].Y) }
`, "type a.P has no field Y", true},
		{"a struct element as a condition", `import "a"

func main() {
	if a.Fp()[0] {
		println(1)
	}
}
`, "non-bool used as if condition: a.Fp()[0] is a struct", true},
		{"a wrong argument through a call's function value", `import "a"

func main() { println(a.Pick()("x")) }
`, `cannot use "x" of type string as type int in argument to a.Pick()`, true},
		{"too many arguments through an element of a call", `import "a"

func main() { println(a.Hs()[0](1, 2)) }
`, "too many arguments in call to a.Hs()[0]", true},
		{"an unexported field of an element", `import "a"

func main() { println(a.Fp()[0].y) }
`, "cannot refer to unexported field y of type a.P", true},
		{"an unexported method of a call's value", `import "a"

func main() { println(a.Mk().get()) }
`, "cannot refer to unexported method get of type a.C", true},
		{"a pointer method of a call's value", `import "a"

func main() { a.Mk().Add(2) }
`, "cannot call pointer method Add on a.C", true},
		{"the address of an element of a call's array", `import "a"

func main() {
	p := &a.Arr()[0]
	println(*p)
}
`, "cannot take address of a.Arr()[0]", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo": &fstest.MapFile{Data: []byte(test.main)},
				"a/a.ogo":  &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if test.refuse {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("got %v, want an error containing %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			var buf bytes.Buffer
			if err := EmitC(pkg, &buf, Checked(), Inline()); err != nil {
				t.Fatalf("EmitC: %v", err)
			}
			if cc == "" {
				return
			}
			dir := t.TempDir()
			csrc, bin := filepath.Join(dir, "main.c"), filepath.Join(dir, "prog")
			if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			// The flags of runCorpus, which says why each is there.
			if out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra", "-Wno-unused-function", "-Wno-format",
				"-I", shim, "-o", bin, csrc, "-lpthread", "-lm").CombinedOutput(); err != nil || len(bytes.TrimSpace(out)) != 0 {
				t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, out, buf.String())
			}
			got, err := exec.Command(bin).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, got)
			}
			if g := strings.ReplaceAll(string(got), "\r\n", "\n"); g != test.want {
				t.Errorf("output:\n got %q\nwant %q", g, test.want)
			}
		})
	}
}
