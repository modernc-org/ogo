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

// TestCheckImportedCallArgs asks what a call into another package's METHODS and
// function values is checked against. A method of another package's type was
// asked whether it exists and is exported, and its arguments not at all --
// `c.Add(5)` for an Add taking a struct, `c.Plus(1)` for one of two parameters --
// whether called on a variable, a package variable, a call's result, an element or
// a field, promoted or an interface's (checkImportedMethodArgs, and the chain walk
// for what a chain reaches). A call through a function value bound to such a
// method or function, `add := c.Add` or `f := lib.Use`, was not checked either:
// the signature was carried across spelled `lib.T`, a name whose qualifier is this
// file's and whose name is the library's token, and every rule resolving it looked
// it up in the library's file, where lib names nothing (identFile). And a method
// of a type defined over a Kind, `lib.Count`, was asked nothing at all, existence
// and export included.
//
// Of 30 such programs Go refuses, 28 were taken, each reaching the C compiler. The
// valid forms of all of them run, and print what Go prints.
//
// A METHOD VALUE of a package variable of another package's type, `inc := mc.Add`
// for a `var mc a.Counter` of main's, is the form the rules for a method value take
// (reportUnsupportedFuncValue, methodValueParts): a pointer-receiver method of a
// package-level variable that is no pointer, its receiver bound at compile time.
// It was refused as no method at all, the lookup asking main's scope for a.Counter.
func TestCheckImportedCallArgs(t *testing.T) {
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
	const lib = `type T struct{ V int }

type Counter struct{ N int }

func (c *Counter) Add(t T) int { c.N += t.V; return c.N }

func (c Counter) Plus(n int, s string) int { return c.N + n + len(s) }

type Box struct {
	F func(T) int
	Counter
}

type Count int

func (k Count) Scale(f float64) int { return int(float64(k) * f) }

func (k Count) hidden() {}

func (c *Counter) hidden() {}

type Adder interface{ Add(t T) int }

func Use(t T) int { return t.V }

var C Counter

var B Box

func Mk() *Counter { return &C }
`
	for _, test := range []struct {
		name, main string
		want       string // the output, or with refuse the message
		refuse     bool
	}{
		{"every valid form", `import "a"

type W struct{ c a.Counter }

func main() {
	var c a.Counter
	p := &c
	println(c.Add(a.T{V: 1}), c.Plus(1, "x"), p.Add(a.T{V: 2}))
	println(a.C.Add(a.T{V: 3}), a.Mk().Add(a.T{V: 4}), a.Mk().Plus(1, "abc"))
	var b a.Box
	b.F = a.Use
	println(b.Add(a.T{V: 6}), b.F(a.T{V: 7}), a.B.Add(a.T{V: 8}))
	add := a.C.Add
	f := a.Use
	println(add(a.T{V: 9}), f(a.T{V: 10}))
	var k a.Count = 3
	println(k.Scale(2.5))
	xs := []a.Counter{{}}
	var cs [2]a.Counter
	var w W
	println(xs[0].Add(a.T{V: 12}), cs[1].Add(a.T{V: 13}), w.c.Add(a.T{V: 15}))
	var ad a.Adder = &a.C
	println(ad.Add(a.T{V: 18}))
}
`, "1 3 3\n3 7 11\n6 7 8\n16 10\n7\n12 13 15\n34\n", false},
		{"a method value of a package variable", `import "a"

var mc a.Counter

func main() {
	inc := mc.Add
	var h func(a.T) int = mc.Add
	println(inc(a.T{V: 30}), h(a.T{V: 31}), mc.N, a.B.N)
}
`, "30 61 61 0\n", false},
		{"a call through such a method value", `import "a"

var mc a.Counter

func main() {
	inc := mc.Add
	println(inc(5))
}
`, "cannot use 5 (untyped int constant) as a.T value in argument to inc", true},
		{"a method value of a local", `import "a"

func main() {
	var l a.Counter
	inc := l.Add
	println(inc(a.T{V: 1}))
}
`, "cannot take l.Add as a value: a method value binds the address of its receiver", true},
		{"a method value of a value receiver", `import "a"

var mc a.Counter

func main() {
	p := mc.Plus
	println(p(1, "x"))
}
`, "cannot take mc.Plus as a value: a method value copies its receiver", true},
		{"a method value of a pointer variable", `import "a"

var mc a.Counter

var mp = &mc

func main() {
	inc := mp.Add
	println(inc(a.T{V: 1}))
}
`, "cannot take mp.Add as a value: its receiver is a pointer", true},
		{"a method value of an unexported method", `import "a"

var mc a.Counter

func main() {
	h := mc.hidden
	h()
}
`, "cannot refer to unexported method hidden of type a.Counter", true},
		{"a variable's method", `import "a"

func main() {
	var c a.Counter
	println(c.Add(5))
}
`, "cannot use 5 (untyped int constant) as a.T value in argument to Add", true},
		{"a method of two parameters given one", `import "a"

func main() {
	var c a.Counter
	println(c.Plus(1))
}
`, "not enough arguments in call to Plus", true},
		{"a method's second argument", `import "a"

func main() {
	var c a.Counter
	println(c.Plus(1, 2))
}
`, "cannot use 2 of type int as type string in argument to Plus", true},
		{"a package variable's method", `import "a"

func main() { println(a.C.Add(5)) }
`, "cannot use 5 (untyped int constant) as a.T value in argument to Add", true},
		{"a call's result's method", `import "a"

func main() { println(a.Mk().Plus(1)) }
`, "not enough arguments in call to Plus", true},
		{"a promoted method", `import "a"

func main() {
	var b a.Box
	println(b.Add(5))
}
`, "cannot use 5 (untyped int constant) as a.T value in argument to Add", true},
		{"a package variable's promoted method", `import "a"

func main() { println(a.B.Add(5)) }
`, "cannot use 5 (untyped int constant) as a.T value in argument to Add", true},
		{"a function field", `import "a"

func main() {
	var b a.Box
	b.F = a.Use
	println(b.F(5))
}
`, "cannot use 5 (untyped int constant) as a.T value in argument to F", true},
		{"an element's method", `import "a"

func main() {
	xs := []a.Counter{{}}
	println(xs[0].Add(5))
}
`, "cannot use 5 (untyped int constant) as a.T value in argument to Add", true},
		{"a field's method", `import "a"

type W struct{ c a.Counter }

func main() {
	var w W
	println(w.c.Add(5))
}
`, "cannot use 5 (untyped int constant) as a.T value in argument to Add", true},
		{"an interface's method", `import "a"

func main() {
	var ad a.Adder = &a.C
	println(ad.Add())
}
`, "not enough arguments in call to Add", true},
		{"a method value of a variable", `import "a"

func main() {
	add := a.C.Add
	println(add(5))
}
`, "cannot use 5 (untyped int constant) as a.T value in argument to add", true},
		{"a function value", `import "a"

func main() {
	f := a.Use
	println(f(5))
}
`, "cannot use 5 (untyped int constant) as a.T value in argument to f", true},
		{"a method of a defined type over a Kind", `import "a"

func main() {
	var k a.Count = 3
	println(k.Scale("x"))
}
`, `cannot use "x" of type string as type float64 in argument to Scale`, true},
		{"a method such a type does not have", `import "a"

func main() {
	var k a.Count = 3
	println(k.Nosuch(1))
}
`, "type a.Count has no method Nosuch", true},
		{"an unexported method of such a type", `import "a"

func main() {
	var k a.Count = 3
	k.hidden()
}
`, "cannot refer to unexported method hidden of type a.Count", true},
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
