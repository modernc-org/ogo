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

// emitRunCase is one program and its expected output. The same table drives
// TestEmitCRun (host: a C compiler + the pthread shim in testdata/hostp2) and
// TestOnBoard (real P2 hardware, when OGO_BOARD_PORT names the serial port).
type emitRunCase struct {
	name string
	src  string
	want string
	// panics marks a program expected to abort through ogo_panic rather than run
	// to completion.
	panics bool
}

var emitRunCases = []emitRunCase{
	{
		name: "arithmetic and control flow",
		src: `func main() {
	x := 17
	x %= 5
	x <<= 3
	x += 2
	println(x)
}
`,
		want: "18\n",
	},
	{
		// A shadowing local whose initializer references the shadowed name reads the
		// OUTER binding (Go evaluates the initializer before the new name is in scope).
		// The emitter must capture the initializer before the same-named C variable
		// shadows it, or C reads the new, uninitialized variable. Both the inferred
		// (`var x = x + 5`) and typed (`var x int = x * 2`) forms are exercised, and
		// the outer x must survive each block unchanged.
		name: "shadowing self-referential initializer reads the outer binding",
		src: `func main() {
	x := 100
	{
		var x = x + 5
		println(x)
	}
	{
		var x int = x * 2
		println(x)
	}
	println(x)
}
`,
		want: "105\n200\n100\n",
	},
	{
		// The same shadowing rule for aggregate copies: `var a [N]T = a` and
		// `var xs []T = xs` copy the OUTER array/slice, so mutating the inner one
		// must not disturb the outer. Both copy paths (array memcpy, slice header)
		// capture the source before the same-named variable shadows it.
		name: "shadowing self-referential copy of an array and a slice",
		src: `func main() {
	a := [3]int{1, 2, 3}
	{
		var a [3]int = a
		a[0] = 9
		println(a[0], a[1], a[2])
	}
	println(a[0])
	xs := []int{4, 5, 6}
	{
		var xs []int = xs
		println(xs[0], xs[1], xs[2])
	}
	println(xs[0])
}
`,
		want: "9 2 3\n1\n4 5 6\n4\n",
	},
	{
		// Indexed array and slice literals ("[]int{2: 5}"): a keyed element places
		// its value at a constant index, gaps zero-fill, and a positional element
		// after an index continues from index+1. A slice's length is the highest
		// index plus one. The emitter expands these to positional C initializers.
		name: "indexed array and slice composite literals",
		src: `func main() {
	a := [5]int{0: 1, 4: 9}
	println(a[0], a[1], a[4])
	xs := []int{2: 5, 4: 9}
	println(len(xs), xs[0], xs[2], xs[4])
	ys := []int{1, 2, 4: 9, 10}
	println(len(ys), ys[0], ys[1], ys[2], ys[4], ys[5])
}
`,
		want: "1 0 9\n5 0 5 9\n6 1 2 0 9 10\n",
	},
	{
		// A constant integer expression is a valid array bound: a literal expression
		// (`[2 + 1]int`), a named constant bound to an expression (`const N = W * H`,
		// itself referencing other constants), and a shift. The emitter folds each to
		// a literal, because C cannot use a const-qualified variable as a bound, and
		// len() reports the folded extent.
		name: "constant-expression array bounds",
		src: `const W = 4
const H = 3
const N = W * H

func main() {
	var g [N]int
	g[N-1] = 9
	var b [2 + 1]int
	b[2] = 7
	var s [1 << 3]int
	s[7] = 3
	println(len(g), g[N-1], len(b), b[2], len(s), s[7], N, W+H)
}
`,
		want: "12 9 3 7 8 3 12 7\n",
	},
	{
		// String equality compares contents, not the { ptr, len } struct C's `==`
		// would reject. Exercised as a value, an if condition, a for condition, a
		// switch (single and multi-value cases), and -- the embedded case -- string
		// comparisons mixed with && / || and int comparisons: every lowering path
		// routes each string comparison through the ogo_string_eq helper.
		name: "string equality and string switch",
		src: `func classify(s string) int {
	switch s {
	case "hi", "hey":
		return 1
	case "bye":
		return 2
	}
	return 0
}

func main() {
	a := "hi"
	println(a == "hi", a != "hi")
	if a == "hi" {
		println(1)
	}
	n := 0
	for a != "" {
		n++
		a = ""
	}
	println(n)
	println(classify("hey"), classify("bye"), classify("x"))
	b := "yes"
	x := 1
	if b == "yes" && x > 0 {
		println(2)
	}
	println(b == "no" || b == "yes", x > 0 && b != "z")
}
`,
		want: "true false\n1\n1\n1 2 0\n2\ntrue true\n",
	},
	{
		// String ordering (< <= > >=) compares lexicographically by unsigned byte,
		// like Go, via the ogo_string_cmp helper against 0 -- a prefix ties on the
		// shorter length. Exercised standalone, with variables, and embedded in a
		// boolean chain (composing with the ogo_string_eq lowering).
		name: "string ordering comparisons",
		src: `func main() {
	println("abc" < "abd", "abd" < "abc", "ab" < "abc")
	a := "cat"
	b := "dog"
	println(a < b, a >= b, a <= "cat")
	if a > "a" && a < "z" {
		println(1)
	}
}
`,
		want: "true false true\ntrue false true\n1\n",
	},
	{
		// Ranging a string iterates runes, not bytes, like Go: the index is each
		// rune's start byte (so it jumps past a multi-byte rune) and the
		// two-variable value is the decoded rune. `é` (é) is two UTF-8 bytes, so
		// the index after it is 3, and a five-rune string counts 5 though it is six
		// bytes -- exercising ogo_decode_rune.
		name: "range over string yields runes",
		src: `func main() {
	for i, c := range "AbC" {
		println(i, int(c))
	}
	for i, c := range "aéz" {
		println(i, int(c))
	}
	n := 0
	for range "héllo" {
		n++
	}
	println(n)
}
`,
		want: "0 65\n1 98\n2 67\n0 97\n1 233\n3 122\n5\n",
	},
	{
		// 64-bit integers: int64/uint64 map to C int64_t/uint64_t. Arithmetic,
		// division (guarded by ogo_nonzero64 so a large divisor is not truncated to
		// 32 bits), conversions to and from int, and printing (%lld/%llu) all work on
		// the 32-bit P2 via flexcc's long long.
		name: "64-bit integer arithmetic",
		src: `func main() {
	var a int64 = 5000000000
	var b int64 = 3
	println(a+b, a*b, a/b, a%b)
	var u uint64 = 18000000000000000000
	println(u, u/2)
	x := 7
	println(int64(x) * 1000000000)
	println(int(a / 1000000000))
}
`,
		want: "5000000003 15000000000 1666666666 2\n18000000000000000000 9000000000000000000\n7000000000\n5\n",
	},
	{
		// The p2 package wraps flexcc/propeller2.h hardware intrinsics. Rev (a pure
		// 32-bit bit reverse) is deterministic on and off target and returns uint32,
		// so its high-bit result prints unsigned. The pin and wait ops compile and
		// run (no-ops off target, real on the board).
		name: "p2 intrinsics",
		src: `import "p2"

func main() {
	println(p2.Rev(1), p2.Rev(0x80000000), p2.Rev(255))
	p2.PinHigh(56)
	p2.PinToggle(56)
	p2.PinLow(56)
	p2.WaitUs(1)
}
`,
		want: "2147483648 1 4278190080\n",
	},
	{
		// An empty struct carries no data but is a real, legal type: it holds
		// methods, can be passed and returned by value, embedded as a field, and
		// stored in arrays/slices. C rejects a struct with no members, so the
		// emitter gives it one hidden byte; that byte stays invisible to OctoGo.
		name: "empty struct type",
		src: `type marker struct{}

func (m marker) tag() int { return 42 }

func use(m marker) int { return m.tag() }

func mk() marker { return marker{} }

type wrap struct {
	m marker
	n int
}

func main() {
	var m marker
	println(m.tag())
	println(use(mk()))
	var a [3]marker
	s := a[:]
	println(len(s))
	w := wrap{marker{}, 7}
	println(w.n)
}
`,
		want: "42\n42\n3\n7\n",
	},
	{
		// A method may leave its receiver unnamed -- "(T)" or "(*T)" -- when the
		// body does not use it, matching Go and reading naturally for a method on a
		// stateless type. The emitter still gives the C parameter a name (flexcc
		// drops an unnamed one's argument slot) and (void)s it. A named receiver on
		// the same type must keep working alongside, value and pointer both.
		name: "unnamed method receiver",
		src: `type counter struct{ n int }

func (counter) kind() int { return 7 }

func (*counter) tag() int { return 9 }

func (c counter) get() int { return c.n }

func (c *counter) bump() { c.n++ }

func main() {
	c := counter{40}
	println(c.kind())
	println(c.tag())
	c.bump()
	c.bump()
	println(c.get())
}
`,
		want: "7\n9\n42\n",
	},
	{
		// break exits the switch: the rest of the case is skipped and execution
		// resumes after the switch. The if/else lowering makes it a forward goto.
		name: "break exits a switch case",
		src: `func main() {
	x := 2
	switch x {
	case 2:
		println(1)
		if x > 0 {
			break
		}
		println(99)
	}
	println(2)
}
`,
		want: "1\n2\n",
	},
	{
		// A break in a switch that sits inside a loop names the switch, not the
		// loop, so the loop runs to completion (0, 1, 2). If it named the loop the
		// output would be just 0.
		name: "break in a switch inside a loop names the switch",
		src: `func main() {
	for i := 0; i < 3; i++ {
		switch {
		case i == 1:
			break
		}
		println(i)
	}
}
`,
		want: "0\n1\n2\n",
	},
	{
		// A break in a loop that sits inside a switch case names the loop, not the
		// switch, so the statement after the loop still runs (8). If it named the
		// switch, 8 would be skipped.
		name: "break in a loop inside a switch names the loop",
		src: `func main() {
	x := 1
	switch x {
	case 1:
		for j := 0; j < 5; j++ {
			if j == 2 {
				break
			}
			println(j)
		}
		println(8)
	}
}
`,
		want: "0\n1\n8\n",
	},
	{
		// Logical && and || combine bools and short-circuit. They bind looser than a
		// comparison and && tighter than ||, so `a && b || c` groups as `(a && b) ||
		// c` -- exercised in a condition, an assignment and a bool result.
		name: "logical operators",
		src: `func between(x int) bool {
	return x > 0 && x < 10
}

func main() {
	x := 5
	a := true
	b := false
	println(a && b, a || b)
	if x > 0 && x < 10 && a {
		println(11)
	}
	if x < 0 || x > 3 {
		println(22)
	}
	if x > 0 && x > 100 || x == 5 {
		println(33)
	}
	println(between(5), between(50))
}
`,
		want: "false true\n11\n22\n33\ntrue false\n",
	},
	{
		name: "slices, arrays and access chains",
		src: `type P struct {
	v [2]int
}

type B struct {
	pts  []P
	grid [2][3]int
}

func main() {
	var b B
	b.pts = make([]P, 2, 2)
	b.pts[1].v[0] = 30
	b.grid[1][2] = 12
	t := b.pts[1:2]
	println(b.pts[1].v[0] + b.grid[1][2] + len(t))
}
`,
		want: "43\n",
	},
	{
		// A named array type resolves to its dimensions at every array site: a local
		// variable, a struct field, a by-value parameter (copied on entry, like any
		// array parameter), a multi-dimensional type and a non-int element.
		name: "named array types",
		src: `type Row [3]int
type Grid [2][2]int
type RGB [3]uint8

type Box struct {
	row Row
	n   int
}

func first(r Row) int {
	return r[0]
}

func main() {
	var r Row
	r[0] = 5
	r[2] = 9
	var b Box
	b.row[1] = 4
	b.n = 8
	var g Grid
	g[1][1] = 7
	var c RGB
	c[0] = 255
	println(r[0]+r[2], len(r))
	println(b.row[1] + b.n)
	println(first(r))
	println(g[1][1])
	println(c[0])
}
`,
		want: "14 3\n12\n5\n7\n255\n",
	},
	{
		// Printing a slice or array renders "[e0 e1 ...]" per element, for any
		// scalar-printable element: a bool as true/false, a string as its bytes, an
		// unsigned width without wrapping (%u), a signed one with its sign.
		name: "print slices of every scalar element type",
		src: `func main() {
	bs := []bool{true, false, true}
	println(bs)
	us := []uint8{1, 2, 3}
	println(us)
	ss := []string{"a", "bc"}
	println(ss)
	var xs [3]int32
	xs[0] = 7
	xs[2] = -9
	println(xs)
	big := []uint{4000000000}
	println(big)
}
`,
		want: "[true false true]\n[1 2 3]\n[a bc]\n[7 0 -9]\n[4000000000]\n",
	},
	{
		// A slice printed only with the no-newline form defines just its print
		// helper -- no unused ogo_println_slice_int, which -Wall -Wextra rejects.
		// print writes no trailing newline, so the following println ends the line.
		name: "print a slice without a newline",
		src: `func main() {
	xs := []int{1, 2, 3}
	print(xs)
	println(9)
}
`,
		want: "[1 2 3]9\n",
	},
	{
		// A composite literal builds a struct value from its fields in declaration
		// order. It may appear anywhere an expression may except the top level of a
		// control-flow header, where its "{" would be the block (see the grammar).
		name: "composite literals",
		src: `type Q struct {
	v int
}

type P struct {
	q Q
	n int
	s string
}

func sum(p P) int {
	return p.q.v + p.n
}

func mk(n int) P {
	return P{Q{n}, n * 2, "made"}
}

func main() {
	p := P{Q{1}, 2, "hi"}
	println(p.q.v, p.n, p.s)
	var z P = P{}
	println(z.q.v, z.n)
	z = P{Q{3}, 4, "set"}
	println(z.q.v, z.n, z.s)
	println(sum(P{Q{5}, 6, "arg"}))
	r := mk(7)
	println(r.q.v, r.n, r.s)
}
`,
		want: "1 2 hi\n0 0\n3 4 set\n11\n7 14 made\n",
	},
	{
		// Fields of a package-scope struct, which resolve through a different type
		// environment than a local's and so are typed on their own path. Every field
		// here is one whose type has to be known to emit it at all: a string and a
		// bool print differently from an int, a slice field is what len reads, and an
		// inferred local takes its type from the field.
		name: "fields of a package-scope struct",
		src: `type Inner struct {
	name string
	on   bool
	xs   []int
}

type Outer struct {
	in Inner
	n  int
}

func (o Outer) sum() int { return o.n }

var g Outer
var gp *Outer

func main() {
	gp = &g
	g.in.name = "pkg"
	g.in.on = true
	g.n = 4
	g.in.xs = make([]int, 2, 2)
	g.in.xs[1] = 6
	q := g.in.name
	println(g.in.name, g.in.on, len(g.in.xs), g.in.xs[1])
	println(q, g.sum(), gp.n)
}
`,
		want: "pkg true 2 6\npkg 4 4\n",
	},
	{
		// Array and slice literals. An array literal is C's own aggregate
		// initialization; a slice literal has no C spelling and lowers the way make
		// does, to a backing array carrying the values plus a { pointer, len, cap }
		// header. "[]T{}" gets no backing array at all -- C has no zero-length one,
		// and an empty slice needs none.
		name: "array and slice literals",
		src: `type P struct {
	x int
	y int
}

func sum(s []int) int {
	t := 0
	for _, v := range s {
		t += v
	}
	return t
}

func main() {
	tab := [4]int{10, 20, 30, 40}
	part := [4]int{1, 2}
	var typed [3]int = [3]int{7, 8, 9}
	xs := []int{5, 6, 7}
	var ts []int = []int{1, 1}
	empty := []int{}
	strs := [2]string{"a", "b"}
	pts := [2]P{P{1, 2}, P{3, 4}}

	tab[0] = 11
	xs[0] = 50

	println(tab[0], tab[3], part[1], part[3], typed[2], len(tab))
	println(xs[0], len(xs), cap(xs), sum(xs), ts[1], len(empty))
	println(strs[1], pts[1].x, pts[0].y)
}
`,
		want: "11 40 2 0 9 4\n50 3 3 63 1 0\nb 3 2\n",
	},
	{
		// A keyed composite literal names its fields, in any order and in any
		// number. C's designated initializers look like the lowering for this and
		// are not one -- flexcc mishandles them -- so the literal is rewritten into
		// declaration order with the omitted fields zeroed, which makes it exactly
		// as compilable as the positional literal it is equivalent to. The zeroed
		// gaps are the interesting part: a struct or array gap has to be written
		// out in full, not as "{0}".
		name: "keyed composite literals",
		src: `type Q struct {
	v int
}

type P struct {
	q Q
	n int
	s string
}

// A struct whose gaps are aggregates, so zeroing them has to be written out in
// full: "{0}" is C's universal zero only at the top level of an initializer.
type Grid struct {
	cell [2]int
	m    [2][2]int
	q    Q
	k    int
}

func nOf(p P) int { return p.n }

var pkg = P{s: "pkg", n: 10}

func main() {
	a := P{n: 1}
	b := P{s: "hi", q: Q{2}}
	c := P{q: Q{3}, n: 4, s: "all"}
	d := P{q: Q{v: 5}}
	var e P = P{n: 6}
	e = P{n: 7}
	n := 8
	g := P{n: n * 2}

	// Only k is named, so both arrays and the nested struct are zeroed gaps.
	var grid Grid = Grid{k: 5}
	grid.cell[1] = 9

	println(a.n, a.q.v, b.q.v, b.s, c.n, c.s, d.q.v)
	println(e.n, g.n, nOf(P{n: 9}), pkg.n, pkg.s)
	println(grid.k, grid.cell[0], grid.cell[1], grid.m[1][1], grid.q.v)
}
`,
		want: "1 0 2 hi 4 all 5\n7 16 9 10 pkg\n5 0 9 0 0\n",
	},
	{
		// A composite literal of a struct that has an array field. flexcc cannot
		// lower a compound literal of one, so this is spelled as a plain brace
		// initializer; the host C compiler accepts either, which is why the target
		// build (TestTargetBuild) is what pins it. The nested "Deep{}" also pins the
		// written-out zero: "{0}" does not nest, so every field and every array
		// extent has to be braced (see zeroBraceC).
		name: "composite literal of a struct with an array field",
		src: `type Cell struct {
	v int
	w int
}

type Deep struct {
	m    [2][3]int
	cs   [2]Cell
	n    int
	name string
}

type Grid struct {
	d    Deep
	name string
}

var top = Grid{Deep{}, "top"}

func main() {
	var d Deep = Deep{}
	d.m[1][2] = 5
	d.cs[1].v = 6
	g := Grid{Deep{}, "g"}
	g.d.n = 7
	top.d.n = 3
	empty := Grid{}
	println(d.m[1][2], d.cs[1].v, g.d.n, g.name, top.d.n, empty.d.n)
}
`,
		want: "5 6 7 g 3 0\n",
	},
	{
		// Copying a struct that holds an array. flexcc miscompiles C's own struct
		// assignment for one, so every copy here lowers to memcpy; the host compiler
		// is fine either way, so TestTargetBuild is what pins it. A copy has to be a
		// copy, not an alias, which is what mutating the source afterwards checks.
		name: "copying a struct that holds an array",
		src: `type Row struct {
	cells [3]int
	n     int
}

type Wrap struct {
	r    Row
	rows []Row
	k    int
}

func main() {
	var src Row
	src.cells[1] = 5
	src.n = 2

	// Every target shape: a plain variable, a declaration, a field, an array
	// element and a slice-field element.
	var a Row = src
	b := src
	var c Row
	c = src

	var w Wrap
	w.r = src
	w.rows = make([]Row, 2, 2)
	w.rows[1] = src

	var arr [2]Row
	arr[1] = src

	src.cells[1] = 99 // a copy is a copy: none of the above may see this

	d := w
	d.r.cells[1] = 7

	e := Row{}
	e = Row{}

	println(a.cells[1], b.cells[1], c.n, w.r.cells[1], w.rows[1].cells[1])
	println(arr[1].cells[1], d.r.cells[1], e.n, src.cells[1])
}
`,
		want: "5 5 2 5 5\n5 7 0 99\n",
	},
	{
		name: "methods on values, pointers and named types",
		src: `type Point struct {
	x int
	y int
}

func (p Point) sum() int {
	return p.x + p.y
}

func (p *Point) scale(k int) {
	p.x = p.x * k
	p.y = p.y * k
}

type Celsius int

func (c Celsius) double() Celsius {
	return c * 2
}

func main() {
	var p Point
	p.x = 3
	p.y = 4
	println(p.sum())
	p.scale(2)
	println(p.x, p.y, p.sum())
	var c Celsius = 21
	println(int(c.double()))
}
`,
		want: "7\n6 8 14\n42\n",
	},
	{
		// A struct crosses the call boundary by value in both directions, so the
		// callee's writes must not be visible to the caller.
		name: "struct passed and returned by value",
		src: `type P struct {
	x int
	y int
}

func addOne(p P) P {
	p.x = p.x + 1
	p.y = p.y + 1
	return p
}

func main() {
	var a P
	a.x = 10
	a.y = 20
	b := addOne(a)
	println(a.x, a.y)
	println(b.x, b.y)
}
`,
		want: "10 20\n11 21\n",
	},
	{
		name: "switch with and without a guard",
		src: `func classify(n int) int {
	switch {
	case n < 0:
		return -1
	case n == 0:
		return 0
	}
	return 1
}

func day(n int) int {
	switch n {
	case 1:
		return 10
	case 2:
		return 20
	default:
		return 99
	}
}

func main() {
	println(classify(-5), classify(0), classify(7))
	println(day(1), day(2), day(5))
}
`,
		want: "-1 0 1\n10 20 99\n",
	},
	{
		name: "append and cap",
		src: `func main() {
	s := make([]int, 0, 4)
	s = append(s, 1)
	s = append(s, 2)
	println(len(s), cap(s), s[0], s[1])
}
`,
		want: "2 4 1 2\n",
	},
	{
		// copy moves min(len(dst), len(src)) elements and returns the count. The
		// last case copies a slice onto a shifted view of itself, which overlaps --
		// memmove handles it, as Go's copy guarantees.
		name: "copy builtin",
		src: `func main() {
	src := []int{1, 2, 3, 4}
	dst := make([]int, 2)
	n := copy(dst, src)
	println(n, dst[0], dst[1])
	s := []int{1, 2, 3, 4, 5}
	copy(s[1:], s)
	println(s[0], s[1], s[2], s[3], s[4])
}
`,
		want: "2 1 2\n1 1 2 3 4\n",
	},
	{
		// min and max over one or more integer arguments, folded left. The last case
		// evaluates a side-effecting argument once (the helper takes it by value), so
		// f prints exactly once.
		name: "min and max builtins",
		src: `func f() int {
	println(-1)
	return 5
}

func main() {
	println(min(3, 8), max(3, 8))
	println(min(9, 4, 7, 1), max(9, 4, 7, 1))
	println(min(42))
	n := max(10, 20)
	println(n)
	println(min(f(), 3))
}
`,
		want: "3 8\n1 9\n42\n20\n-1\n3\n",
	},
	{
		// clear zeroes a slice's elements, its length unchanged; it works over a
		// slice of an array too. A map or a bare array is not a valid argument.
		name: "clear builtin",
		src: `func main() {
	s := []int{1, 2, 3}
	clear(s)
	println(s[0], s[1], s[2], len(s))
	var a [3]int
	a[0] = 7
	a[2] = 9
	clear(a[:])
	println(a[0], a[2])
}
`,
		want: "0 0 0 3\n0 0\n",
	},
	{
		name: "defer captures at the defer, not the return",
		src: `func step(n int) {
	println(n)
}

func f(c int) {
	x := 1
	defer step(x)
	x = 99
	if c > 0 {
		y := 7
		defer step(y)
	}
	defer step(3)
}

func main() {
	f(1)
	println(0)
	f(0)
}
`,
		want: "3\n7\n1\n0\n3\n1\n",
	},
	{
		name: "goroutine hands a value to main",
		src: `func worker(ch chan int, n int) {
	ch <- n * 10
}

func main() {
	var ch chan int
	go worker(ch, 1)
	go worker(ch, 2)
	go worker(ch, 3)
	a := <-ch
	b := <-ch
	c := <-ch
	println(a + b + c)
}
`,
		want: "60\n",
	},
	{
		name: "select takes default, then blocks for a sender",
		src: `func worker(ch chan int) {
	ch <- 7
}

func main() {
	var ch chan int
	x := 0
	select {
	case x = <-ch:
		println(x)
	default:
		println(99)
	}
	go worker(ch)
	select {
	case x = <-ch:
		println(x)
	}
}
`,
		want: "99\n7\n",
	},
	{
		// A var spec may give each of its names its own value, at either scope,
		// with or without a declared type.
		name: "var declarations with a value list",
		src: `var pa, pb = 1, 2
var pc, pd int = 3, 4
var ps, pu = "hi", "yo"

func main() {
	var a, b = 5, 6
	var c, d int = 7, 8
	x := 9
	var e, f = x * 2, x + 1
	var g, _ = 10, 11
	println(pa, pb, pc, pd)
	println(ps, pu)
	println(a, b, c, d)
	println(e, f, g)
}
`,
		want: "1 2 3 4\nhi yo\n5 6 7 8\n18 10 10\n",
	},
	{
		// One VarSpec declaring several names at package scope. The names share a
		// single VarSpecNode, whose resolution gate must be opened once rather
		// than once per name -- doing the latter reported every name after the
		// first as a redeclaration of itself.
		name: "package-scope multi-name var declarations",
		src: `var a, b int
var s, u string
var flag, other bool

func main() {
	a = 10
	b = 32
	println(a, b, a+b)
	println(len(s), len(u))
	flag = true
	println(flag, other)
}
`,
		want: "10 32 42\n0 0\ntrue false\n",
	},
	{
		// `var a, b = f()` at package scope distributes a multi-result call. C
		// forbids the call in a file-scope initializer, so it runs in the
		// synthesized package init (which main enters first); a blank target drops
		// its value but the call still runs.
		name: "package-scope destructuring var",
		src: `func sums(a, b int) (int, int) {
	return a + b, a - b
}

var sum, diff = sums(10, 3)
var _, gap = sums(20, 5)

func main() {
	println(sum, diff, gap)
}
`,
		want: "13 7 15\n",
	},
	{
		name: "package initialization runs before main",
		src: `func five() int {
	return 5
}

var a = 2
var b = a + 3
var c = five()
var ch chan int
var tally int

func init() {
	tally = a + b + c
}

func worker(k chan int) {
	k <- tally
}

func main() {
	go worker(ch)
	println(<-ch)
}
`,
		want: "12\n",
	},
	{
		// A receive in call-argument position, over a channel that is a local
		// rather than a package-level var. Both halves matter: this is the shape
		// that deadlocked on hardware while the assignment form `v := <-ch` and
		// the package-level channel above both ran, because flexcc dropped the
		// _lockrel when it inlined the rendezvous loop into an argument. gcc
		// compiles it correctly, so only the board run guards this.
		name: "local channel received into call arguments",
		src: `func send(k chan int, n int) {
	k <- n
}

func main() {
	var ch chan int
	go send(ch, 4)
	println(<-ch)
}
`,
		want: "4\n",
	},
	{
		// Sustained rendezvous traffic: three pipelines, each a feeder and a
		// worker, so six goroutines and main keep seven cogs polling at once for
		// twenty exchanges apiece. The case above catches the livelock at the
		// first rendezvous; this one catches a poll that starves only under load,
		// which a handful of one-shot exchanges would step over. Verified to hang
		// outright with the pre-test removed from the polling loops.
		name: "sustained channel traffic across pipelines",
		src: `func worker(in chan int, out chan int) {
	for i := 0; i < 20; i++ {
		v := <-in
		out <- v + 1
	}
}

func feeder(c chan int, n int) {
	for i := 0; i < 20; i++ {
		c <- n
	}
}

func main() {
	var a1 chan int
	var a2 chan int
	var b1 chan int
	var b2 chan int
	var c1 chan int
	var c2 chan int

	go feeder(a1, 10)
	go worker(a1, a2)
	go feeder(b1, 20)
	go worker(b1, b2)
	go feeder(c1, 30)
	go worker(c1, c2)

	sum := 0
	for i := 0; i < 20; i++ {
		sum += <-a2
		sum += <-b2
		sum += <-c2
	}
	println(sum)
}
`,
		want: "1260\n",
	},
	{
		// Several local channels each with their own `go`. This is where the
		// rendezvous used to livelock: the poll called _locktry every turn and
		// re-took the lock faster than the cog on the other side could win it, so
		// both sides span forever. It showed up on hardware only, and only once a
		// program had roughly this many of both, so the cases above cannot stand
		// in for it.
		name: "several local channels and spawns",
		src: `func id(n int) int {
	return n
}

func send(k chan int, n int) {
	k <- n
}

func main() {
	var a chan int
	go send(a, 1)
	println(<-a)

	var b chan int
	go send(b, 2)
	println(id(<-b))

	var c chan int
	go send(c, 3)
	println(1 + <-c)

	var d chan int
	go send(d, 4)
	println(<-d)
}
`,
		want: "1\n2\n4\n4\n",
	},
	{
		name: "iota constant groups",
		src: `type Weekday int

const (
	Sunday Weekday = iota
	Monday
	Tuesday
)

const (
	_  = iota
	KB = 1 << (10 * iota)
	MB = 1 << (10 * iota)
)

const (
	A = iota * 2
	B
	C
)

func main() {
	println(int(Sunday), int(Monday), int(Tuesday))
	println(KB, MB)
	println(A, B, C)
}
`,
		want: "0 1 2\n1024 1048576\n0 2 4\n",
	},
	{
		name: "unnamed multiple results",
		src: `func divmod(a int, b int) (int, int) {
	return a / b, a % b
}

func bounds(lo int, hi int) (int, int, bool) {
	return lo, hi, lo <= hi
}

func main() {
	q, r := divmod(17, 5)
	println(q, r)
	x, y, ok := bounds(3, 8)
	println(x, y, ok)
}
`,
		want: "3 2\n3 8 true\n",
	},
	{
		name: "unnamed and blank parameters",
		src: `func const42(int, int) int {
	return 42
}

func first(a int, _ int) int {
	return a
}

func mix(_ int, b bool, c byte) int {
	if b {
		return int(c)
	}
	return 0
}

func main() {
	println(const42(1, 2))
	println(first(8, 3))
	println(mix(9, true, 65))
}
`,
		want: "42\n8\n65\n",
	},
	{
		name: "naked return of named results",
		src: `func inc(n int) (r int) {
	r = n + 1
	return
}

func divmod(a int, b int) (q, r int) {
	q = a / b
	r = a % b
	return
}

func clamp(x int) (r int) {
	r = x
	if x > 10 {
		r = 10
		return
	}
	return
}

func blank() (_ int, y int) {
	y = 7
	return
}

func main() {
	println(inc(41))
	q, r := divmod(17, 5)
	println(q, r)
	println(clamp(4), clamp(20))
	a, b := blank()
	println(a, b)
}
`,
		want: "42\n3 2\n4 10\n0 7\n",
	},
	{
		name: "multiple-value assignment and swap",
		src: `func main() {
	a := 1
	b := 2
	a, b = b, a
	x, y := 10, 20
	p := 0
	q := 0
	r := 0
	p, q, r = 3, 4, 5
	i := 0
	j := 5
	for i < j {
		i, j = i+1, j-1
	}
	println(a, b)
	println(x + y)
	println(p, q, r)
	println(i, j)
}
`,
		want: "2 1\n30\n3 4 5\n3 2\n",
	},
	{
		name: "constant string concatenation folds",
		src: `const Greeting = "hello" + ", " + "world"

func main() {
	println(Greeting)
	println("a" + "b" + "c")
	println(len("foo" + "bar"))
}
`,
		want: "hello, world\nabc\n6\n",
	},
	{
		// The src is a double-quoted Go string because it contains back-quoted
		// raw strings, which a Go raw string cannot hold. Inside it, "\\n" is a
		// literal backslash-n in the OctoGo raw string, and the embedded newline
		// makes a genuine multi-line raw string.
		name: "raw string literals",
		src: "const Path = `C:\\dev\\ogo`\n\n" +
			"func main() {\n" +
			"\tprintln(`raw`)\n" +
			"\tprintln(Path)\n" +
			"\tprintln(`no \\n escape`)\n" +
			"\tprintln(len(`abcde`))\n" +
			"\tprintln(`a` + `b`)\n" +
			"\tprintln(`line1\nline2`)\n" +
			"}\n",
		want: "raw\nC:\\dev\\ogo\nno \\n escape\n5\nab\nline1\nline2\n",
	},
	{
		name: "numeric conversions",
		src: `func main() {
	var b byte = 200
	println(int(b))
	x := 300
	println(int(byte(x)))
	var big int = 70000
	println(int(uint16(big)))
	y := -1
	println(uint32(y))
	s := "hi"
	sum := 0
	for i := range s {
		sum = sum + int(s[i])
	}
	println(sum)
}
`,
		want: "200\n44\n4464\n4294967295\n209\n",
	},
	{
		name: "string indexing and range",
		src: `func main() {
	s := "hello"
	println(s[0])
	println(s[4])
	i := 2
	println(s[i])
	n := 0
	for range s {
		n++
	}
	println(n)
}
`,
		want: "104\n111\n108\n5\n",
	},
	{
		name: "range over integer, slice and array",
		src: `func main() {
	sum := 0
	for i := range 5 {
		sum = sum + i
	}
	s := make([]int, 4, 4)
	for i := range s {
		s[i] = i * i
	}
	total := 0
	for i, v := range s {
		total = total + i + v
	}
	var a [3]int
	a[0] = 10
	a[1] = 20
	a[2] = 30
	asum := 0
	for _, v := range a {
		asum = asum + v
	}
	count := 0
	for range 7 {
		count++
	}
	println(sum)
	println(total)
	println(asum)
	println(count)
}
`,
		want: "10\n20\n60\n7\n",
	},
	{
		name: "three-clause for loops",
		src: `func main() {
	sum := 0
	for i := 0; i < 5; i++ {
		sum = sum + i
	}
	prod := 1
	for i := 1; i < 5; i = i + 1 {
		prod = prod * i
	}
	// each loop scopes its own i
	for i := 0; i < 3; i++ {
	}
	println(sum)
	println(prod)
}
`,
		want: "10\n24\n",
	},
	{
		name: "bool prints as true or false",
		src: `type Flags struct {
	on  bool
	off bool
}

func toggle(a bool) bool {
	return a
}

func main() {
	var x bool
	y := true
	var f Flags
	f.on = true
	println(x)
	println(y)
	println(toggle(false))
	println(5 > 3)
	println(y, x, f.on)
}
`,
		want: "false\ntrue\nfalse\ntrue\ntrue false true\n",
	},
	{
		name: "unsigned prints as unsigned",
		src: `func main() {
	var u uint = 4000000000
	var w uint32 = 4294967295
	var b byte = 65
	var s int = -7
	println(u)
	println(w)
	println(u, s, b)
	println("x", u, "y")
}
`,
		want: "4000000000\n4294967295\n4000000000 -7 65\nx 4000000000 y\n",
	},
	{
		name: "break and continue",
		src: `func main() {
	i := 0
	for {
		i++
		if i > 2 {
			break
		}
	}
	n := 0
	j := 0
	for j < 5 {
		j++
		if j == 2 {
			continue
		}
		n = n + j
	}
	println(i)
	println(n)
}
`,
		want: "3\n13\n",
	},
	{
		name: "index out of range traps",
		src: `func main() {
	s := make([]int, 2, 2)
	i := 5
	println(s[i])
}
`,
		panics: true,
	},
	{
		name: "more goroutines than cogs traps",
		src: `func spin(ch chan int) {
	ch <- 1
}

func main() {
	var ch chan int
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	println(<-ch)
}
`,
		panics: true,
	},
	{
		// A bare block statement introduces its own scope: each block's x is local
		// to it, so the two blocks do not collide.
		name: "block statement scopes its declarations",
		src: `func main() {
	{
		x := 1
		println(x)
	}
	{
		x := 2
		println(x)
	}
}
`,
		want: "1\n2\n",
	},
	{
		// panic("msg") aborts through ogo_panic. smith's oracle relies on this: a
		// generated program panics on a checksum mismatch, implicating the compiler.
		name: "panic aborts",
		src: `func main() {
	panic("boom")
}
`,
		panics: true,
	}}

// TestEmitCRun compiles emitted C with a host compiler and runs it, checking what
// the program prints. The golden tests pin the shape of the output; this pins its
// behaviour, which is the only way to catch a lowering that reads correctly and
// computes the wrong thing.
//
// P2 intrinsics are supplied by testdata/hostp2, which backs cogs with pthreads and
// hardware locks with mutexes at the real 8-cog and 16-lock limits. Concurrency in
// particular cannot be checked any other way: a rendezvous needs a second cog, so
// inspecting the generated code proves nothing about whether two of them meet.
//
// Skipped when no C compiler is available, so the suite still runs anywhere.
func TestEmitCRun(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found; skipping the run-the-output tests")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range emitRunCases {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(test.src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			var buf bytes.Buffer
			if err := EmitC(pkg, &buf, Checked()); err != nil {
				t.Fatalf("EmitC: %v", err)
			}

			dir := t.TempDir()
			csrc := filepath.Join(dir, "main.c")
			if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "prog")
			// -Wall -Wextra so a lowering that provokes a diagnostic fails here
			// rather than being discovered on real hardware. -Wno-unused-function
			// because the string print/println helpers are emitted as a pair whenever
			// either is needed, so a program using only one leaves the other unused --
			// harmless (the P2 backend drops it), but clang warns where gcc does not.
			// -Wno-format because int64_t is `long long` on the 32-bit P2 (so %lld/%llu
			// are the correct, verified target formats) but `long` on this 64-bit host,
			// which then warns about %lld; flexcc's (long long) cast miscompiles a
			// 64-bit expression and its PRId64 is non-standard, so %lld is the only
			// target-correct choice. Real int64 output is checked on hardware
			// (TestOnBoard).
			out, err := exec.Command(cc, "-std=gnu11", "-Wall", "-Wextra",
				"-Wno-unused-function", "-Wno-format", "-I", shim,
				"-o", bin, csrc, "-lpthread").CombinedOutput()
			if err != nil {
				t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, out, buf.String())
			}
			if len(bytes.TrimSpace(out)) != 0 {
				t.Errorf("cc warned:\n%s\n--- emitted ---\n%s", out, buf.String())
			}

			got, runErr := exec.Command(bin).CombinedOutput()
			if test.panics {
				if runErr == nil {
					t.Errorf("expected a panic, but the program exited cleanly with %q", got)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("run: %v\n%s", runErr, got)
			}
			if g := strings.ReplaceAll(string(got), "\r\n", "\n"); g != test.want {
				t.Errorf("output:\n got %q\nwant %q\n--- emitted ---\n%s", g, test.want, buf.String())
			}
		})
	}
}
