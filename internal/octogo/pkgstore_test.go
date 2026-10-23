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

// TestEmitCSummaryOtherPackageStores asks what a function keeps of a parameter it
// stores into ANOTHER package's variable: all of it, as a store into one of its own
// package's does. The summaries asked isPackageVar of a target's root, which for
// `lib.G` is the qualifier and no variable of this package, so `lib.G = p` recorded
// nothing and `keep(&x)` left x's address in lib, where `lib.G = &x` was refused
// (outlivesByName). Each way a store reaches such a variable, and a control.
func TestEmitCSummaryOtherPackageStores(t *testing.T) {
	const lib = `type H struct{ P *int }

var G *int

var GH H

var GA [2]*int
`
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"a statement", `import "lib"

func keep(p *int) { lib.G = p }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a list", `import "lib"

func keep(p *int) {
	n := 0
	lib.G, n = p, 1
	_ = n
}

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a field", `import "lib"

func keep(p *int) { lib.GH.P = p }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"an element", `import "lib"

func keep(p *int) { lib.GA[1] = p }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"through a callee", `import "lib"

func put(dst **int, p *int) { *dst = p }

func keep(p *int) { put(&lib.G, p) }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a for clause's post", `import "lib"

func keep(p *int) {
	for i := 0; i < 1; lib.G = p {
		i++
	}
}

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"through a local pointer", `import "lib"

func keep(p *int) {
	q := &lib.G
	*q = p
}

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a package variable's address", `import "lib"

var gx int

func keep(p *int) { lib.G = p }

func main() { keep(&gx) }
`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte(test.src)},
				"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refuse && err == nil:
				t.Errorf("a reference to this frame left it:\n%s", test.src)
			case test.refuse && !strings.Contains(err.Error(), "outlive"):
				t.Errorf("refused, but not for its lifetime: %v", err)
			case !test.refuse && err != nil:
				t.Errorf("refused: %v\n%s", err, test.src)
			}
		})
	}
}

// TestCheckAddrOfPointerAcrossPackages: the address of another package's pointer,
// `pp := &lib.L.Head`, is a pointer to a pointer of lib's type, as `var pp
// **lib.Node` declares it. Recorded as one pointer, `*pp` was the struct two steps
// away and the list idiom's `for *pp != nil` was refused for comparing a struct
// with nil; and its mistakes are refused in lib's names.
func TestCheckAddrOfPointerAcrossPackages(t *testing.T) {
	const lib = `type Node struct {
	Val  int
	Next *Node
}

func (n *Node) Get(k int) int { return n.Val + k }

type List struct{ Head *Node }

var G *Node

var L List

var A [3]*Node
`
	walk := "\n\tfor *pp != nil && (*pp).Val < 3 {\n\t\tpp = &(*pp).Next\n\t}\n\tk := 0\n\tif *pp != nil {\n\t\tk = (*pp).Get(1)\n\t}\n\tprintln(k)"
	for _, test := range []struct {
		body, refused string
	}{
		{"pp := &lib.G" + walk, ""},
		{"pp := &lib.L.Head" + walk, ""},
		{"var pp = &lib.A[1]" + walk, ""},
		{"pp := &lib.L.Head\n\t(*pp).M(1)", "type lib.Node has no method M"},
		{"pp := &lib.G\n\tprintln((*pp).Get(\"a\"))", `cannot use "a"`},
		{"pp := &lib.G\n\t**pp = 3", "cannot use 3"},
		{"pp := &lib.A[0]\n\t*pp = 3", "cannot use 3"},
	} {
		src := "import \"lib\"\n\nfunc main() {\n\t" + test.body + "\n}\n"
		fsys := fstest.MapFS{
			"main.ogo":    &fstest.MapFile{Data: []byte(src)},
			"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
		}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)):
			t.Errorf("%s: got %v, want %q refused", test.body, err, test.refused)
		case test.refused == "" && err != nil:
			t.Errorf("%s: refused: %v", test.body, err)
		}
	}
}

// TestCheckStoreAcrossPackages holds the stores into another package's variables
// that were asked nothing: one written without a type, `lib.K = "a"` for a `var K
// = 7`, and every store through a pointer to one, `*lib.PK = 1.5` and `(*lib.PS).F =
// "a"`, whatever lib wrote; and a read through two, `var y int8 = **lib.PPKc` of a
// lib.C. The last row is each form stored and read as Go takes it.
func TestCheckStoreAcrossPackages(t *testing.T) {
	const lib = `type C int8

type T struct {
	F int
	G C
}

func Two() int { return 2 }

var K = 7

var PK = &K

var PPK = &PK

var Kc = C(5)

var PKc = &Kc

var PPKc = &PKc

var R = Two()

var S T

var PS = &S

var A [2]int8

var PA = &A
`
	for _, test := range []struct {
		body, refused string
	}{
		{"lib.K = 1.5", "constant 1.5 truncated to int"},
		{`lib.K = "a"`, `cannot use "a" of type string as type int`},
		{"*lib.PK = 1.5", "constant 1.5 truncated to int"},
		{`**lib.PPK = "a"`, `cannot use "a" of type string as type int`},
		{"*lib.PK += 1.5", "constant 1.5 truncated to int"},
		{`lib.R = "a"`, `cannot use "a" of type string as type int`},
		{"var y string = *lib.PK\n\t_ = y", "cannot use *lib.PK of type int as type string"},
		{"var y int8 = **lib.PPKc\n\t_ = y", "cannot use **lib.PPKc of type lib.C as type int8"},
		{`(*lib.PS).F = "a"`, `cannot use "a" of type string as type int`},
		{"(*lib.PS).G = 300", "constant 300 overflows"},
		{`(*lib.PA)[0] = "a"`, `cannot use "a" of type string as type int8`},
		{"(*lib.PS).F += 1.5", "constant 1.5 truncated to int"},
		{"lib.K = 3\n\t*lib.PK = 4\n\t**lib.PPK += 5\n\t(*lib.PS).F = 6\n\t(*lib.PS).G = lib.C(7)\n\t(*lib.PA)[1] = 8\n\tvar y lib.C = **lib.PPKc\n\tprintln(lib.K, y, lib.S.F, lib.S.G, lib.A[1], lib.R)", ""},
	} {
		src := "import \"lib\"\n\nfunc main() {\n\t" + test.body + "\n}\n"
		fsys := fstest.MapFS{
			"main.ogo":    &fstest.MapFile{Data: []byte(src)},
			"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
		}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		switch {
		case test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)):
			t.Errorf("%s: got %v, want %q refused", test.body, err, test.refused)
		case test.refused == "" && err != nil:
			t.Errorf("%s: refused: %v", test.body, err)
		}
	}
}
