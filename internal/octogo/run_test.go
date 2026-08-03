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
	// to completion. want, when set, is then required to appear in the output
	// rather than to be all of it -- the panic line plus whatever ran before it.
	panics bool
	// backendWarning is a substring of a diagnostic the C backend prints for this
	// program and that has been examined and found harmless. TestTargetBuild fails
	// any other backend output, because the backend warns where it should refuse:
	// it accepts a duplicate declaration in one block with "Redefining x", ignores
	// the second, and builds -- which is how a mixed ":=" silently computed the
	// wrong answer until aa300e2. Listing an exception here keeps it visible rather
	// than swallowed, and each one should say why it is not a defect.
	backendWarning string
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
		// An array is a value: `b := a` copies it (unlike a slice, which shares its
		// backing). C forbids array assignment, so the emitter declares b and copies
		// with memcpy. Mutating the copy must not touch the original -- exercised for a
		// 1-D and a 2-D array -- and len works on the copy.
		name: "array value copy",
		src: `func main() {
	a := [3]int{1, 2, 3}
	b := a
	b[0] = 9
	b[2] = 8
	println(a[0], a[2], b[0], b[2])
	var m [2][2]int
	m[0][0] = 1
	m[1][1] = 4
	n := m
	n[0][0] = 9
	println(m[0][0], n[0][0], n[1][1])
	c := a
	println(len(c), c[1])
}
`,
		want: "1 3 9 8\n1 9 4\n3 2\n",
	},
	{
		// A package-level array with an inferred type and an initializer,
		// `var g = [N]T{...}` -- a file-scope static array. Fewer values than the
		// length zero-fill (pal), it is indexable and mutable, len reports the extent,
		// and it copies by value like any array.
		name: "inferred global array",
		src: `var g = [3]int{5, 6, 7}

var pal = [4]int{1, 2}

func main() {
	println(g[0], g[1], g[2], len(g))
	g[1] = 9
	println(g[1])
	println(pal[0], pal[1], pal[2], pal[3])
	b := g
	b[0] = 100
	println(g[0], b[0])
}
`,
		want: "5 6 7 3\n9\n1 2 0 0\n5 100\n",
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
		// Two struct types that point to each other (A holds *B, B holds *A), and a
		// struct that points to a type declared later in source (Node -> *Leaf). Both
		// work because every struct's forward declaration is emitted before any body,
		// so a field may name a struct not yet defined -- Go imposes no declaration
		// order, and neither does this.
		name: "mutually recursive and forward-referenced structs",
		src: `type A struct {
	v int
	b *B
}

type B struct {
	w int
	a *A
}

func main() {
	var x A
	var y B
	x.v = 1
	x.b = &y
	y.w = 2
	y.a = &x
	println(x.v, x.b.w, x.b.a.v)
	l := Leaf{9}
	n := Node{1, &l}
	println(n.val, n.child.data)
}

type Node struct {
	val   int
	child *Leaf
}

type Leaf struct {
	data int
}
`,
		want: "1 2 1\n1 9\n",
	},
	{
		// A self-referential struct -- a field that is a pointer to the same type --
		// backs linked lists and trees. The emitter emits a tagged, forward-declared
		// typedef (`typedef struct N N; struct N { ... N* next; };`) so the field can
		// name the type, and `nil` lowers to the null pointer 0. Exercised by walking
		// a list and recursively summing a tree, with nil terminators and checks.
		name: "self-referential struct (list and tree)",
		src: `type N struct {
	v    int
	next *N
}

func walk(n *N) int {
	t := 0
	for n != nil {
		t += n.v
		n = n.next
	}
	return t
}

type T struct {
	v int
	l *T
	r *T
}

func total(t *T) int {
	if t == nil {
		return 0
	}
	return t.v + total(t.l) + total(t.r)
}

func main() {
	c := N{3, nil}
	b := N{2, &c}
	a := N{1, &b}
	println(walk(&a))
	var p *N
	println(p == nil, a.next == nil)
	lf := T{1, nil, nil}
	rf := T{3, nil, nil}
	root := T{2, &lf, &rf}
	println(total(&root))
}
`,
		want: "6\ntrue false\n6\n",
	},
	{
		// Struct equality: Go compares structs field by field, which C's == cannot do
		// on the struct value, so the emitter generates a per-type ogo_eq_<T> helper.
		// Exercised with scalar fields, a string field (compared through
		// ogo_string_eq), a nested struct field (compared through its own helper), and
		// both == and != -- as a value, an if condition and mixed into a && chain.
		name: "struct equality",
		src: `type P struct {
	x int
	y int
}

type Named struct {
	p    P
	name string
}

func main() {
	a := P{1, 2}
	b := P{1, 2}
	c := P{1, 3}
	println(a == b, a == c, a != c)
	n1 := Named{P{1, 2}, "hi"}
	n2 := Named{P{1, 2}, "hi"}
	n3 := Named{P{1, 2}, "no"}
	n4 := Named{P{9, 2}, "hi"}
	println(n1 == n2, n1 == n3, n1 == n4)
	if a == b && n1 == n2 {
		println(1)
	}
	e := P{}
	println(e == P{0, 0})
}
`,
		want: "true false true\ntrue false false\n1\ntrue\n",
	},
	{
		// A struct field named `a` or `b` must not collide with the equality helper's
		// parameters, which is why they use the reserved _ogo_ prefix: named `a`/`b`,
		// the helper's `b.b` (parameter b, field b) is miscompiled by flexcc.
		name: "struct equality with fields named a and b",
		src: `type T struct {
	a int
	b int
}

func main() {
	x := T{1, 2}
	y := T{1, 2}
	z := T{1, 9}
	println(x == y, x == z, x != z)
}
`,
		want: "true false true\n",
	},
	{
		// An empty struct carries no data but is a real, legal type: it holds
		// methods, can be passed and returned by value, embedded as a field, and
		// stored in arrays/slices. C rejects a struct with no members, so the
		// emitter gives it one hidden byte; that byte stays invisible to OctoGo.
		name: "empty struct type",
		// The C is valid and the host compiler accepts it: a `marker a[3]` decays to
		// `marker*`, which is exactly the slice header's pointer field. The target's
		// compiler does not follow the tagged forward declaration that a
		// self-referential-capable struct is emitted with, and calls the type
		// unknown. The program's output is checked on real hardware by TestOnBoard,
		// so the warning is noise rather than a defect.
		backendWarning: "incompatible pointer types in parameter passing",
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
		// A method call may follow a call, index or field result: `mk().sum()` calls
		// on a function's struct return, `p.shift().sum()` and
		// `p.shift().shift().sum()` chain method results, `a[i].sum()` calls on an
		// indexed element, and `b.p.sum()` calls through a field. The emitter lowers
		// each into one C expression, wrapping a method call as `T_M(recv, ...)`
		// around the accumulated receiver text and tracking the type reached.
		//
		// A plain field read off a call result (`mk().y`) is deliberately NOT emitted:
		// flexcc miscompiles a nonzero-offset field read of a struct return value
		// (the return temporary is not materialised first). A method call, which
		// passes the whole struct, is unaffected -- so the chains here all end in one.
		name: "call and selector chains",
		src: `type point struct{ x, y int }

func (p point) sum() int { return p.x + p.y }

func (p point) shift() point { return point{p.x + 1, p.y + 1} }

func mk() point { return point{10, 20} }

type box struct{ p point }

func main() {
	println(mk().sum())
	p := point{3, 4}
	println(p.shift().sum())
	println(p.shift().shift().sum())
	var a [2]point
	a[1] = point{5, 6}
	println(a[1].sum())
	var b box
	b.p = point{7, 8}
	println(b.p.sum())
}
`,
		want: "30\n9\n11\n11\n15\n",
	},
	{
		// A composite-literal element value may elide its type when position implies
		// it: `[]P{{1,2},{3,4}}` means `[]P{P{1,2},P{3,4}}`, `O{{5}}` means
		// `O{Inner{5}}`, and a keyed element value `O{i: {7}}` elides too. The
		// emitter fills the elided type from the array/slice element type or the
		// struct field type at that position. Positional and keyed inner forms and an
		// empty `{}` (all-zero) are all exercised.
		name: "type-elided composite literals",
		src: `type pt struct{ x, y int }

type box struct{ p pt }

func main() {
	a := []pt{{1, 2}, {3, 4}}
	println(a[0].x, a[0].y, a[1].x, a[1].y)
	var b [3]pt = [3]pt{{5, 6}, {}, {7, 8}}
	println(b[0].x, b[1].x, b[2].y)
	c := []pt{{x: 9}, {y: 10}}
	println(c[0].x, c[0].y, c[1].y)
	o := box{{11, 12}}
	println(o.p.x, o.p.y)
	k := box{p: {13, 14}}
	println(k.p.x, k.p.y)
}
`,
		want: "1 2 3 4\n5 0 8\n9 0 10\n11 12\n13 14\n",
	},
	{
		// A labeled break or continue names an enclosing loop or switch: "break L"
		// leaves the labeled "for"/"switch" from any depth, and "continue L" begins
		// the labeled "for"'s next iteration. Each lowers to a goto -- to a label
		// after the loop for break, and at the loop body's end (a fall-through re-runs
		// the post and test) for continue.
		name: "labeled break and continue",
		src: `func main() {
	found := -1
outer:
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			if i*10+j == 12 {
				found = i*10 + j
				break outer
			}
		}
	}
	println(found)

	sum := 0
next:
	for i := 1; i <= 3; i++ {
		for j := 0; j < 3; j++ {
			sum += i
			continue next
		}
	}
	println(sum)

	last := -1
loop:
	for i := 0; i < 5; i++ {
		switch i {
		case 3:
			break loop
		default:
			last = i
		}
	}
	println(last)
}
`,
		want: "12\n6\n2\n",
	},
	{
		// A rune literal is its Unicode code point (an int32): 'A' is 65, '\n' is 10,
		// and a non-ASCII 'é' is 233 -- emitted as the numeric value, not a C
		// character constant, so the code point is exact regardless of the target's
		// narrow-char encoding. Runes take part in arithmetic, comparison and switch.
		name: "rune literals",
		src: `func main() {
	c := 'A'
	println(int(c), int(c+1))
	println(int('\n'), int('\t'), int('0'))
	println(int('é'), int('世'))

	r := 'm'
	if r >= 'a' && r <= 'z' {
		println(1)
	}
	switch r {
	case 'a':
		println(10)
	case 'm':
		println(20)
	}
}
`,
		want: "65 66\n10 9 48\n233 19990\n1\n20\n",
	},
	{
		// A short-declared `p := &x` is a pointer, inferred from the address-of just
		// as `var p *int = &x` is from its type. Its dereference reads and writes the
		// pointee (`*p`, `*p = e`), it may point at a struct field or array element,
		// and it may be passed to a pointer parameter.
		name: "pointer to a local variable",
		src: `type point struct{ x, y int }

func inc(p *int) { *p = *p + 1 }

func main() {
	n := 5
	p := &n
	*p = 9
	println(n)
	inc(p)
	println(n)

	var pt point
	q := &pt.y
	*q = 7
	println(pt.y)

	var a [3]int
	r := &a[1]
	*r = 42
	*r = *r + 1
	println(a[1])
}
`,
		want: "9\n10\n7\n43\n",
	},
	{
		// A pointer-receiver method call is a valid call statement at the end of a
		// chain even when it returns nothing: `a[i].inc()`, `s[i].inc()`, `b.c.inc()`
		// mutate an addressable element or field in place. This is the idiom for
		// updating slice/array elements (`for i := range xs { xs[i].update() }`),
		// where ranging by index and calling through &element is how a value method
		// set mutates the backing store.
		name: "void method on an element",
		src: `type counter struct{ n int }

func (c *counter) inc()      { c.n++ }
func (c *counter) add(d int) { c.n += d }

type box struct{ c counter }

func main() {
	var a [3]counter
	for i := range a {
		a[i].inc()
		a[i].add(i)
	}
	println(a[0].n, a[1].n, a[2].n)

	s := make([]counter, 0, 2)
	s = append(s, counter{10})
	s[0].inc()
	println(s[0].n)

	var b box
	b.c.add(7)
	println(b.c.n)
}
`,
		want: "1 2 3\n11\n7\n",
	},
	{
		// Floating point: float64 (C double) and float32 (C float), their literals,
		// arithmetic, a float parameter and result, conversions to and from int, and
		// printing (as %g, concise like Go's fmt). Float division is not guarded --
		// Go's float divide-by-zero is +-Inf/NaN, not a panic -- so a non-integer
		// divisor divides exactly rather than being truncated by the integer guard.
		name: "floating point",
		src: `func sq(x float64) float64 { return x * x }

func main() {
	a := 2.5
	b := 0.5
	println(a+b, a-b, a*b, a/b)
	println(sq(3.0), 10.0/4.0)
	println(int(3.75), float64(9)/2.0)
	var f float32 = 1.5
	println(f * 2.0)
	println(-1.5 + 0.5)
}
`,
		want: "3 2 1.25 5\n9 2.5\n3 4.5\n3\n-1\n",
	},
	{
		// copy(dst []byte, src string) copies a string's bytes into a byte slice,
		// min(len(dst), len(src)) of them, with no allocation -- the destination is
		// the caller's storage. That is exactly what a user-backed buffer needs to
		// append a string (a WriteString) on this allocation-free target: reserve a
		// fixed array, slice it into the buffer, and copy into the free tail. The
		// bytes written are verified by their codes ('H'=72, ' '=32, '!'=33).
		name: "copy string into a byte-slice buffer",
		src: `type buf struct {
	b []byte
	n int
}

func (bf *buf) writeString(s string) { bf.n += copy(bf.b[bf.n:], s) }

func (bf *buf) writeByte(c byte) {
	bf.b[bf.n] = c
	bf.n++
}

func main() {
	var back [32]byte
	bf := buf{back[:], 0}
	bf.writeString("Hi")
	bf.writeByte(' ')
	bf.writeString("P2!")
	println(bf.n, int(back[0]), int(back[2]), int(back[5]))
}
`,
		want: "6 72 32 33\n",
	},
	{
		// Builder is a compiler-known string builder over a caller-owned []byte, the
		// allocation-free answer to strings.Builder. NewBuilder(back[:]) starts a
		// cursor into the backing; WriteString, WriteByte, WriteRune (UTF-8 encoded)
		// and Write([]byte) append into it; Len reports the count; Reset rewinds; and
		// String() returns a zero-copy VIEW (an ogo_string aliasing the written
		// prefix) usable for printing and comparison. A *Builder passes to a function.
		name: "string Builder over a backing array",
		src: `func greet(sb *Builder, who string) {
	sb.WriteString("Hi, ")
	sb.WriteString(who)
	sb.WriteByte('!')
}

func main() {
	var back [64]byte
	sb := NewBuilder(back[:])
	greet(&sb, "P2")
	sb.WriteRune(' ')
	sb.WriteRune('é')
	println(sb.Len())
	println(sb.String())

	sb.Reset()
	ok := []byte{'O', 'K'}
	sb.Write(ok)
	println(sb.String() == "OK", sb.Len())
}
`,
		want: "10\nHi, P2! é\ntrue 2\n",
	},
	{
		// A Unicode function name is valid, as in Go. flexcc (like older C) rejects a
		// Unicode C identifier, so every emitted identifier passes through cIdent,
		// which escapes a non-ASCII name to an ogo_U_ form (here Δ -> ogo_U_394) at
		// both its definition and its calls. ASCII identifiers are unchanged.
		name: "unicode function name",
		src: `func Δ(x int) int { return x * 2 }

func μ(a, b int) int { return a + b }

func main() {
	println(Δ(21))
	println(μ(Δ(10), 2))
}
`,
		want: "42\n22\n",
	},
	{
		// Unicode identifiers reach C escaped (ogo_U_<hex>) in EVERY class: type
		// name (Δ), struct field (π, ω), method receiver (ρ), function parameter
		// (σ), a multiple-assignment target (α, β), a range key/value (ι, ν) and a
		// plain local (τ). flexcc rejects raw Unicode C identifiers, so the escape is
		// what lets these compile on the P2; the host shim confirms they still mean
		// the same thing. Range is over a named slice, not an inline composite
		// literal, to sidestep an unrelated checker quirk.
		name: "unicode in types, fields, methods, params, locals",
		src: `type Δ struct {
	π int
	ω int
}

func (ρ Δ) total() int {
	return ρ.π + ρ.ω
}

func μ(σ int) (int, int) {
	return σ, σ * 2
}

func main() {
	d := Δ{π: 7, ω: 3}
	α, β := μ(5)
	τ := d.total()
	xs := []int{10, 20, 30}
	s := 0
	for ι, ν := range xs {
		s += ι + ν
	}
	println(d.total(), α, β, τ, s)
}
`,
		want: "10 5 10 10 63\n",
	},
	{
		// A goroutine that starts goroutines. Every other case spawns from main
		// alone, so nothing had two cogs claiming pool slots at the same time --
		// and the claim takes a hardware lock precisely because they might.
		name: "a goroutine starts goroutines",
		src: `// A supervisor cog that starts workers of its own, so the cog pool's bookkeeping
// is reached from more than one cog at a time rather than from main alone.
//
// Two supervisors and two leaves each is six goroutines beside main, which fits
// the pool of seven with one to spare. A leaf blocks until main takes its value,
// so a program needing more than the pool holds at once would depend on the
// claim's bounded wait outlasting the drain -- a race to lose one run in ten,
// not a test.

func leaf(id int, out chan int) {
	out <- id * 10
}

func supervisor(base int, out chan int, done chan int) {
	for i := 0; i < 2; i++ {
		go leaf(base+i, out)
	}
	done <- base
}

func main() {
	var out chan int
	var done chan int

	go supervisor(1, out, done)
	go supervisor(3, out, done)

	total := 0
	seen := 0
	for i := 0; i < 4; i++ {
		v := <-out
		total += v
		seen |= 1 << (v / 10)
	}
	a := <-done
	b := <-done
	println("total", total)
	println("seen", seen)
	println("bases", a+b)
}
`,
		want: "total 100\nseen 30\nbases 4\n",
	},
	{
		// Indexing and slicing a string CONSTANT, which emitted C naming something
		// no declaration had ever produced. A string constant is folded to its
		// literal at every use -- a Go constant has no address, so there is nothing
		// to point at -- and both paths read ".str" and ".len" off the name as
		// though a variable stood there.
		//
		// A constant string is the natural place to keep a digit table or a prompt,
		// so this is ordinary code, and len() and range over one always worked --
		// which is exactly why it went unnoticed.
		name: "index and slice a string constant",
		src: `const lit = "hello"
const joined = lit + ", world"

const (
	prompt = "> "
	digits = "0123456789"
)

// atoiPrefix reads leading digits of s, using a constant as a lookup table.
func atoiPrefix(s string) (int, int) {
	n := 0
	i := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < digits[0] || c > digits[9] {
			break
		}
		n = n*10 + int(c-digits[0])
	}
	return n, i
}

func main() {
	println(lit[0], lit[1], lit[4])
	println(lit[1:3], lit[2:], lit[:2], lit[:])
	i := 3
	println(lit[1:i], lit[i:])
	println(joined[5:12], len(joined))
	println(prompt, prompt[0], digits[9:])

	v, k := atoiPrefix("407x")
	println(v, k)

	const local = "world"
	println(local[0], local[1:3], len(local))
}
`,
		want: "104 101 111\nel llo he hello\nel lo\n, world 12\n>  62 9\n407 3\n119 or 5\n",
	},
	{
		// Value-receiver methods returning structs, chained: `p.add(v).scale(2)`,
		// and a method whose receiver is an element of a slice held in a struct,
		// assigned back to itself. It is what an integrator looks like, and it is
		// the shape where a copy has to stay a copy -- the last line steps a body
		// and reads the original, which must not have moved.
		name: "chained value-receiver methods over structs",
		src: `type vec struct {
	x, y int
}

type body struct {
	pos vec
	vel vec
}

type world struct {
	bodies []body
}

func (v vec) add(o vec) vec { return vec{v.x + o.x, v.y + o.y} }

func (v vec) scale(k int) vec { return vec{v.x * k, v.y * k} }

func (b body) step(dt int) body {
	return body{pos: b.pos.add(b.vel.scale(dt)), vel: b.vel}
}

func (w *world) step(dt int) {
	for i := 0; i < len(w.bodies); i++ {
		w.bodies[i] = w.bodies[i].step(dt)
	}
}

func (w *world) at(i int) vec { return w.bodies[i].pos }

func mkWorld(bs []body) world { return world{bodies: bs} }

var back [3]body

func main() {
	back[0] = body{pos: vec{0, 0}, vel: vec{1, 2}}
	back[1] = body{pos: vec{10, 10}, vel: vec{-1, 0}}
	back[2] = body{pos: vec{5, 5}, vel: vec{0, -1}}
	w := mkWorld(back[:])

	w.step(2)
	println(w.at(0).x, w.at(0).y, w.at(1).x, w.at(2).y)

	w.step(3)
	println(w.at(0).x, w.at(0).y, w.at(1).x, w.at(2).y)

	// A method on the result of a method, and on the result of a call.
	v := back[0].pos.add(back[0].vel).scale(2)
	println(v.x, v.y)
	w2 := mkWorld(back[:])
	println(w2.at(2).x)

	// A struct returned by value is a copy: stepping the copy leaves the world.
	c := back[0].step(10)
	println(c.pos.x, back[0].pos.x)
}
`,
		want: "2 4 8 3\n5 10 5 0\n12 24\n5\n15 5\n",
	},
	{
		// Interfaces, on the hardware. An interface value is a data pointer beside a
		// pointer to a static vtable; a call through it is indirect; a concrete value
		// meeting an interface parameter is wrapped where it stands; and one
		// interface value assigned to another is the two words, copied.
		//
		// There is no heap, so the data pointer is the address of the caller's
		// variable rather than a boxed copy -- which is why the interface is a
		// REFERENCE and the variable it was made from has to outlive it.
		//
		// Go's method-set rule is kept: a value of T carries the value-receiver
		// methods and *T carries all of them, so Mutable is satisfied by &gc and not
		// by gc. That rule earns its keep here even though nothing is boxed -- the
		// "&" is where a reference into the caller's storage becomes visible, which
		// is what the lifetime rules are trying to keep legible.
		name: "interfaces dispatched through a static vtable",
		src: `type Shape interface {
	Area() int
	Name() string
}

// Mutable adds a method with a POINTER receiver, so only *counter satisfies it.
type Mutable interface {
	Bump(k int) int
}

type sq struct {
	n int
}

func (s sq) Area() int { return s.n * s.n }

func (s sq) Name() string { return "sq" }

type rect struct {
	w, h int
}

func (r rect) Area() int { return r.w * r.h }

func (r rect) Name() string { return "rect" }

type counter struct {
	n int
}

func (c *counter) Bump(k int) int {
	c.n = c.n + k
	return c.n
}

var gq sq

var gr rect

var gc counter

func describe(s Shape) int { return s.Area() }

func bigger(a Shape, b Shape) string {
	if a.Area() >= b.Area() {
		return a.Name()
	}
	return b.Name()
}

func main() {
	gq.n = 3
	gr.w, gr.h = 2, 5

	// A variable of interface type, and a call through it.
	var s Shape = &gq
	println(s.Name(), s.Area())

	// The same variable, another concrete type: the table changes with it.
	s = &gr
	println(s.Name(), s.Area())

	// A pointer handed to an interface parameter, wrapped where it stands.
	println(describe(&gq), describe(&gr))
	println(bigger(&gq, &gr), bigger(&gr, &gq))

	// An interface value passed on as an interface: the two words, copied.
	println(describe(s))

	// A local concrete value works as well as a package one, as long as the
	// interface does not outlive it.
	var lq sq
	lq.n = 4
	var t Shape = &lq
	println(t.Name(), t.Area(), describe(&lq))

	// A pointer-receiver method is in *counter's method set, not counter's, so
	// the address is what satisfies Mutable -- and what it mutates is the
	// variable, not a copy.
	var m Mutable = &gc
	println(m.Bump(2), m.Bump(3), gc.n)
}
`,
		want: "sq 9\nrect 10\n9 10\nrect rect\n10\nsq 16 16\n2 5 5\n",
	},
	{
		// An interface value in the places a value goes rather than only in a
		// variable of its own: returned from a function, held in a struct field,
		// held in an array walked as a slice, and sent to another cog. Each is a
		// two-word copy of { data, vtable } -- there is nothing to box -- and each
		// asked a different part of the emitter for the type of what a call through
		// it yields. The chain-reached ones (`sc.first.Name()`, `shapes[1].Name()`)
		// went untyped before this, so a string result printed as two integers.
		//
		// The data pointer is a reference, so what these hold are package variables:
		// an interface over a local is what escape analysis already refuses to let
		// outlive its frame.
		name: "an interface value in a return, a field, an element and a channel",
		src: `type Shape interface {
	Area() int
	Name() string
}

type sq struct {
	n int
}

func (s sq) Area() int { return s.n * s.n }

func (s sq) Name() string { return "sq" }

type rect struct {
	w, h int
}

func (r rect) Area() int { return r.w * r.h }

func (r rect) Name() string { return "rect" }

type scene struct {
	first Shape
	count int
}

var gq sq

var gr rect

var shapes [3]Shape

func pick(k int) Shape {
	if k == 0 {
		return &gq
	}
	return &gr
}

func total(xs []Shape) int {
	sum := 0
	for i := 0; i < len(xs); i++ {
		sum += xs[i].Area()
	}
	return sum
}

func feed(ch chan Shape) { ch <- &gr }

func main() {
	gq.n = 3
	gr.w, gr.h = 2, 5

	// Returned from a function, and returned straight back out again.
	a := pick(0)
	b := pick(1)
	println(a.Name(), a.Area(), b.Name(), b.Area())

	// Held in a struct field.
	var sc scene
	sc.first = &gq
	sc.count = 1
	println(sc.first.Name(), sc.first.Area(), sc.count)

	// Held in an array, walked as a slice.
	shapes[0] = &gq
	shapes[1] = &gr
	shapes[2] = pick(0)
	println(total(shapes[:]), shapes[1].Name())

	// Sent across a cog boundary.
	var ch chan Shape
	go feed(ch)
	got := <-ch
	println(got.Name(), got.Area())
}
`,
		want: "sq 9 rect 10\nsq 9 1\n28 rect\nrect 10\n",
	},
	{
		// What an interface holds is a POINTER, so &T{...} is how a value with no
		// variable of its own gets in. Go allocates for it; here it is a temporary of
		// the frame, which is exactly what a local is, so the lifetime rules already
		// cover it. A call's result has no address in Go either and is bound first.
		//
		// A package variable of interface type is the first line's business: an
		// address is not a C constant expression, so its two words are written at
		// package initialization.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "an interface over &T{...}, a bound call result and a package variable",
		src: `type Shape interface {
	Area() int
	Name() string
}

type sq struct {
	n int
}

func (s sq) Area() int { return s.n * s.n }

func (s sq) Name() string { return "sq" }

func mk(k int) sq {
	var q sq
	q.n = k
	return q
}

func use(s Shape) int { return s.Area() }

var gq sq

var g Shape = &gq

func main() {
	gq.n = 2
	println(g.Area(), g.Name())

	// &T{...}: a fresh value with no variable of its own. Go allocates one; here it
	// is a temporary of the frame, which the lifetime rules already cover.
	var a Shape = &sq{4}
	println(a.Area(), use(&sq{6}))

	// A call's result has no address in Go either, so it is bound first.
	t := mk(5)
	var b Shape = &t
	println(b.Area(), use(&t))
}
`,
		want: "4 sq\n16 36\n25 25\n",
	},
	{
		// A type assertion recovers the pointer the interface carries, in both of
		// Go's forms. One vtable is emitted per (concrete type, interface) pair, so
		// the test is a pointer comparison of the second word -- there is no type id
		// to read and no name to compare, and the whole of it folds to one compare.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a type assertion, in both forms",
		src: `type Shape interface {
	Area() int
	Name() string
}

type sq struct {
	n int
}

func (s sq) Area() int { return s.n * s.n }

func (s sq) Name() string { return "sq" }

type rect struct {
	w, h int
}

func (r rect) Area() int { return r.w * r.h }

func (r rect) Name() string { return "rect" }

var gq sq

var gr rect

func widthOf(s Shape) int {
	// The comma-ok form: the value, and whether the assertion held. On failure the
	// value is the zero of its type, as in Go.
	r, ok := s.(*rect)
	if !ok {
		return 0
	}
	return r.w
}

func main() {
	gq.n = 3
	gr.w, gr.h = 2, 5

	var s Shape = &gq
	println(widthOf(s), widthOf(&gr))

	// The one-value form, where the assertion is known to hold.
	q := s.(*sq)
	println(q.n, q.Area())

	// Reaching the concrete type recovers what the interface hid: a field the
	// interface never declared.
	s = &gr
	r, ok := s.(*rect)
	println(ok, r.w, r.h)

	// And the negative case, on the same variable.
	q2, ok2 := s.(*sq)
	if ok2 {
		println(q2.n)
	}
	println(ok2)
}
`,
		want: "0 2\n3 9\ntrue 2 5\nfalse\n",
	},
	{
		// A type switch: the assertion's question asked several times. Each clause
		// binds the name at the type that clause proved -- the concrete pointer where
		// one type was named, the interface value where several were, or none -- so a
		// clause cannot share one declaration with the statement, in C any more than
		// in Go. It lowers to the chain of table comparisons it is.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a type switch over three concrete types",
		src: `type Shape interface {
	Area() int
}

type sq struct {
	n int
}

func (s sq) Area() int { return s.n * s.n }

type rect struct {
	w, h int
}

func (r rect) Area() int { return r.w * r.h }

type circle struct {
	r int
}

func (c circle) Area() int { return 3 * c.r * c.r }

var gq sq

var gr rect

var gc circle

func describe(s Shape) int {
	switch v := s.(type) {
	case *sq:
		// One type named, so v is that pointer: a field the interface never had.
		return v.n
	case *rect, *circle:
		// Several, so v keeps the interface type, as in Go.
		return v.Area()
	default:
		return -1
	}
}

func main() {
	gq.n = 3
	gr.w, gr.h = 2, 5
	gc.r = 2

	println(describe(&gq), describe(&gr), describe(&gc))

	// The bare form, with no name bound.
	var s Shape = &gr
	switch s.(type) {
	case *sq:
		println("sq")
	case *rect:
		println("rect")
	}

	// A nil interface takes the nil case.
	var e Shape
	switch e.(type) {
	case nil:
		println("nil")
	case *sq:
		println("sq")
	}
}
`,
		want: "3 10 12\nrect\nnil\n",
	},
	{
		// A variadic parameter, packed at the call and spread from a slice. Inside
		// the function it IS a []T -- len, cap, range and index all ask a slice --
		// so what the feature costs is the pack, which Go allocates and this target
		// builds as an array of the CALLING function.
		//
		// Every line of this prints what real Go prints for the same program,
		// including cap() of the pack and the empty call.
		name: "variadic parameters, packed and spread",
		src: `func sum(xs ...int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

func tagged(tag string, xs ...int) int {
	println(tag, len(xs), cap(xs))
	return sum(xs...)
}

type acc struct {
	n int
}

func (a *acc) add(xs ...int) int {
	a.n += sum(xs...)
	return a.n
}

var pool [4]int

var ga acc

func main() {
	println(sum(1, 2, 3), sum(), sum(7))

	// A fixed parameter before the variadic one, and forwarding with a spread.
	println(tagged("three", 1, 2, 3))
	println(tagged("none"))

	// A slice over a package array, spread into the call.
	pool[0], pool[1], pool[2], pool[3] = 1, 2, 3, 4
	println(sum(pool[:]...))

	// A method takes one too, and takes its empty pack when none is written.
	println(ga.add(1, 2), ga.add(), ga.add(3))

	// The arguments are ordinary expressions, evaluated where they stand.
	k := 5
	println(sum(k, k*2, sum(1, 1)))
}
`,
		want: "6 0 7\nthree 3 3\n6\nnone 0 0\n0\n10\n3 3 6\n17\n",
	},
	{
		// Function literals. C has no nested functions and this language has no
		// closures to need them, so each literal is LIFTED to a file-scope function
		// of a minted name and the expression becomes that name. What a literal may
		// not do is read a local of the surrounding function: there is no heap to
		// hold a captured frame, and the checker says so where it is written.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "function literals, lifted to file scope",
		src: `type Op func(int, int) int

var table [3]Op

var gk = 10

func apply(op Op, a int, b int) int { return op(a, b) }

func pick(which int) Op {
	if which == 0 {
		return func(a int, b int) int { return a + b }
	}
	return func(a int, b int) int { return a * b }
}

func main() {
	// Bound to a variable and called through it.
	dbl := func(a int) int { return a * 2 }
	println(dbl(21))

	// Handed straight to a parameter, and returned from a function.
	println(apply(func(a int, b int) int { return a - b }, 9, 4))
	println(pick(0)(3, 4), pick(1)(3, 4))

	// Held in an array: a dispatch table written where it is used.
	table[0] = func(a int, b int) int { return a + b }
	table[1] = func(a int, b int) int { return a - b }
	table[2] = func(a int, b int) int { return a * b }
	for i := 0; i < len(table); i++ {
		println(i, table[i](6, 3))
	}

	// A package-level name is not a capture: it is there for every function.
	println(func(a int) int { return a + gk }(5))

	// Called immediately, taking no arguments.
	println(func() int { return 7 }())
}
`,
		want: "42\n5\n7 12\n0 9\n1 3\n2 18\n15\n7\n",
	},
	{
		// A dispatch table: functions in an array, called through the index. It is
		// most of the reason to put functions in an array at all, and it was BROKEN
		// on the P2 until now -- every element called whatever the first one held,
		// with a constant index and a variable one alike, whether the table was
		// filled by assignment or at package initialization.
		//
		// The host C compiler gets the direct form right, so the emit-and-run tests
		// passed and only the board disagreed. ogo now binds the element to a
		// temporary before calling it; see doc/call-through-array-element.c.
		name: "a dispatch table of functions in an array",
		src: `type Op func(int, int) int

func add(a int, b int) int { return a + b }

func sub(a int, b int) int { return a - b }

func mul(a int, b int) int { return a * b }

var built [3]Op

var initialized = [2]Op{add, sub}

func main() {
	built[0] = add
	built[1] = sub
	built[2] = mul

	// A variable index, and a constant one.
	for i := 0; i < len(built); i++ {
		println(i, built[i](6, 3))
	}
	println(built[0](6, 3), built[1](6, 3), built[2](6, 3))

	// A table filled at package initialization rather than by assignment.
	for i := 0; i < len(initialized); i++ {
		println(i, initialized[i](6, 3))
	}

	// Bound to a variable first, which always worked and still has to.
	f := built[2]
	println(f(6, 3))
}
`,
		want: "0 9\n1 3\n2 18\n9 3 18\n0 9\n1 3\n18\n",
	},
	{
		// A binary heap over a caller's array: sift up, sift down, a struct payload
		// and a capacity the pushes are refused at. It is what a priority queue on
		// this target looks like, and it leans on most of what this release changed
		// at once -- element swaps through a slice held in a struct field, a
		// two-result method on that field, an `if r := l + 1; r < h.n && ...` header
		// declaration, and the zero value of a struct returned on the empty path.
		//
		// It found nothing, which is the point of writing it down: every one of
		// those paths was fixed or added this week, and this is the program that
		// says they compose.
		name: "a binary heap over a fixed array",
		src: `type job struct {
	pri int
	id  int
}

type heap struct {
	a []job
	n int
}

func (h *heap) less(i, j int) bool {
	if h.a[i].pri != h.a[j].pri {
		return h.a[i].pri < h.a[j].pri
	}
	return h.a[i].id < h.a[j].id
}

func (h *heap) push(j job) bool {
	if h.n == len(h.a) {
		return false
	}
	h.a[h.n] = j
	i := h.n
	h.n++
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			break
		}
		h.a[i], h.a[p] = h.a[p], h.a[i]
		i = p
	}
	return true
}

func (h *heap) pop() (job, bool) {
	var zero job
	if h.n == 0 {
		return zero, false
	}
	top := h.a[0]
	h.n--
	h.a[0] = h.a[h.n]
	i := 0
	for {
		l := 2*i + 1
		if l >= h.n {
			break
		}
		m := l
		if r := l + 1; r < h.n && h.less(r, l) {
			m = r
		}
		if !h.less(m, i) {
			break
		}
		h.a[i], h.a[m] = h.a[m], h.a[i]
		i = m
	}
	return top, true
}

var back [8]job

func main() {
	h := &heap{a: back[:]}

	pri := [7]int{5, 3, 9, 1, 3, 7, 2}
	for i := 0; i < 7; i++ {
		if ok := h.push(job{pri: pri[i], id: i}); !ok {
			println("full at", i)
		}
	}
	println("n", h.n)

	for {
		j, ok := h.pop()
		if !ok {
			break
		}
		print(j.pri, ":", j.id, " ")
	}
	println()

	// Popping an empty heap reports it and yields the zero job.
	j, ok := h.pop()
	println(ok, j.pri, j.id)

	// Refill past capacity: the ninth push is refused.
	for i := 0; i < 9; i++ {
		if ok := h.push(job{pri: 9 - i, id: i}); !ok {
			println("refused", i)
		}
	}
	k, _ := h.pop()
	println("min", k.pri, k.id, h.n)
}
`,
		want: "n 7\n1:3 2:6 3:1 3:4 5:0 7:5 9:2 \nfalse 0 0\nrefused 8\nmin 2 7 7\n",
	},
	{
		// A conversion to a defined ARRAY type, `row(a)` for `type row [3]int`. It
		// was the one kind of defined type whose name did not name a conversion, so
		// `r := row(a)` was "cannot infer a type" and `sum(row(a))` put the type NAME
		// in the emitted C as though it were a function -- a syntax error from the C
		// compiler about code the reader never wrote.
		//
		// Such a conversion is the operand: a defined type is a typedef of what it
		// stands for, so there is nothing to convert. The declaration unwraps it and
		// becomes the array copy it already knew how to emit, which it has to do by
		// hand because an array is the one representation C has no value type for --
		// every path that reads an array operand reads a NAME and would not see
		// through the conversion otherwise.
		//
		// Still refused, and said so in the source: indexing the conversion where it
		// stands, `row(g)[2]`. C has no cast to an array type, so the value needs a
		// name first.
		name: "a conversion to a defined array type",
		src: `type row [3]int

type line row

func sum(r row) int { return r[0] + r[1] + r[2] }

func sumPlain(a [3]int) int { return a[0] + a[1] + a[2] }

var g [3]int

func main() {
	var a [3]int
	a[0], a[1], a[2] = 1, 2, 3

	// A conversion to a defined array type is the operand: nothing to convert.
	r := row(a)
	println(r[0], r[2], len(r), sum(r))

	// In an argument, where the name used to reach the C compiler as a function.
	println(sum(row(a)))

	// Through a chain of definitions, and from a package array.
	l := line(a)
	println(l[1])
	g[2] = 9
	// Indexing the conversion where it stands, row(g)[2], is still refused: C has
	// no cast to an array type, so the value needs a name first.
	gr := row(g)
	println(gr[2], sum(row(g)))

	// And back to the underlying, which always worked.
	println(sumPlain(r))
}
`,
		want: "1 3 3 6\n6\n2\n9 9\n6\n",
	},
	{
		// `if v, ok := t.get(k); ok` -- the two-value header declaration, which is
		// how Go asks a container whether it has something. The grammar admitted one
		// name before the ":=", so the comma was a parse error and the idiom had to
		// be written as two statements, which also leaked the names into the
		// enclosing scope.
		//
		// IfInit and SwitchGuard take the further names as LhsItems now, the checker
		// declares them into the statement's own scope, and the emitter reuses the
		// destructuring the statement form already had. So they shadow, the else
		// branch sees them, a blank is allowed, and the switch takes both the form
		// with an expression and the one without.
		name: "a two-value declaration in an if or switch header",
		src: `type table struct {
	keys []int
	vals []int
}

func (t *table) get(k int) (int, bool) {
	for i := 0; i < len(t.keys); i++ {
		if t.keys[i] == k {
			return t.vals[i], true
		}
	}
	return 0, false
}

func split(n int) (int, int) { return n / 10, n % 10 }

var kb [3]int

var vb [3]int

func main() {
	kb[0], kb[1], kb[2] = 1, 2, 3
	vb[0], vb[1], vb[2] = 10, 20, 30
	t := &table{keys: kb[:], vals: vb[:]}

	// The idiom, on a method.
	if v, ok := t.get(2); ok {
		println("found", v)
	} else {
		println("missing", v)
	}
	if v, ok := t.get(9); ok {
		println("found", v)
	} else {
		println("missing", v)
	}

	// The names are scoped to the statement, so they may shadow.
	v := 99
	if v, ok := t.get(1); ok {
		println("inner", v)
	}
	println("outer", v)

	// A blank is allowed, and the else branch sees the names.
	if _, ok := t.get(3); ok {
		println("has 3")
	}

	// The same in a switch, both with the expression and without.
	switch q, r := split(37); q {
	case 3:
		println("q3", r)
	default:
		println("other", q, r)
	}

	switch q, r := split(48); {
	case q > 3:
		println("big", q, r)
	default:
		println("small", q, r)
	}
}
`,
		want: "found 20\nmissing 0\ninner 10\nouter 99\nhas 3\nq3 7\nbig 4 8\n",
	},
	{
		// Package variables are initialized in DEPENDENCY order, which is what Go
		// does and what specs.go already claimed. They ran in source order, so a
		// variable whose initializer named one declared below it read a zero:
		// `var top int = mid + 1` printed 1 rather than 11. Silent, and the shape is
		// ordinary -- a table's size derived from a base, a scale derived from it.
		//
		// Written out, the variable also used to keep its initializer where C
		// evaluates one at compile time, so the backend refused the program: "global
		// initializers are evaluated at compile time and therefore must be constant",
		// about C the reader never wrote. Anything that is not a constant expression
		// is assigned at package initialization now, which is where the inferred
		// form beside it already went.
		//
		// The order is the same stable sort the typedef section uses: a step only
		// moves later, never earlier, so a program whose declarations already
		// ordered themselves emits what it emitted before. init() still runs after
		// every variable, and several run in the order written.
		name: "package variables initialize in dependency order",
		src: `// Written in an order that is not the order they must be initialized in: each
// reads one declared below it, and Go initializes them in dependency order.
var top int = mid + 1

var mid int = base * 2

var base int = 5

var scaled int = twice(base)

var sum int = top + mid + scaled

func twice(n int) int { return n * 2 }

var log [4]int

func init() {
	log[0] = top
	log[1] = sum
}

func init() {
	// A second init runs on what the first left.
	log[2] = log[0] + log[1]
	log[3] = 1
}

func main() {
	println(base, mid, top, scaled, sum)
	println(log[0], log[1], log[2], log[3])
}
`,
		want: "5 10 11 10 31\n11 31 42 1\n",
	},
	{
		// min and max over what Go orders, not only over integers. specs.go called
		// them "the smallest of its ordered arguments" and the emitter took integers
		// alone, so a control loop could not clamp a float with them -- which is the
		// reason most programs reach for min and max at all.
		//
		// The helper is one line either way: C's own "<" for the arithmetic types,
		// and for a string the same byte comparison "s < t" already used. Folding a
		// two-argument helper left over the arguments is what keeps each argument
		// evaluated exactly once, which the bump() line pins.
		name: "min and max over ordered arguments",
		src: `type volt float32

func clamp(v, lo, hi float32) float32 { return min(max(v, lo), hi) }

var names [3]string

func main() {
	// Integers, as before, including the variadic fold.
	println(min(4, 2, 7, 1), max(4, 2, 7, 1))
	println(min(-1), max(-1))

	// Floats, which is what a control loop clamps with.
	println(int(clamp(2.5, 0.0, 1.0)*10), int(clamp(-3.0, 0.0, 1.0)*10), int(clamp(0.5, 0.0, 1.0)*10))
	var a float32 = 1.25
	var b float32 = 1.5
	println(int(min(a, b)*100), int(max(a, b)*100))

	// A defined type over a float is a float here too.
	var lo volt = 0.5
	var hi volt = 2.0
	println(int(min(lo, hi)*10), int(max(lo, hi)*10))

	// Strings, ordered by the same byte comparison "<" uses.
	names[0], names[1], names[2] = "pin", "cog", "hub"
	println(min(names[0], names[1], names[2]), max(names[0], names[1], names[2]))
	println(min("", "a"), max("ab", "b"))

	// Each argument is evaluated exactly once, even one that changes something.
	n := 0
	println(min(bump(&n), bump(&n), bump(&n)), n)
}

func bump(p *int) int {
	*p++
	return *p
}
`,
		want: "1 7\n-1 -1\n10 0 5\n125 150\n5 20\ncog pin\n b\n1 3\n",
	},
	{
		// A packet codec: a header of sized fields packed into a byte buffer the
		// caller owns, a payload VIEWED rather than copied out of the wire, and a
		// short buffer refused rather than overrun. The first thing a P2 program
		// that talks to anything needs after framing.
		//
		// It is what found print's spacing: `print(n, " ")` in a loop wrote three
		// spaces between values, because print separated its arguments the way
		// println does. Go's print writes them adjacently -- which is the whole
		// reason to reach for print rather than println -- and specs.go said so
		// already; only the emitter disagreed.
		name: "a packet codec over a caller's buffer",
		src: `type opcode uint8

const (
	opPing opcode = iota + 1
	opRead
	opWrite
)

type header struct {
	op    opcode
	flags uint8
	seq   uint16
}

type packet struct {
	hdr     header
	payload []byte
}

const headerLen = 4

// encode writes p into dst and returns how many bytes it used, and whether it fit.
func encode(dst []byte, p packet) (int, bool) {
	n := headerLen + len(p.payload)
	if n > len(dst) {
		return 0, false
	}
	dst[0] = byte(p.hdr.op)
	dst[1] = p.hdr.flags
	dst[2] = byte(p.hdr.seq >> 8)
	dst[3] = byte(p.hdr.seq)
	for i := 0; i < len(p.payload); i++ {
		dst[headerLen+i] = p.payload[i]
	}
	return n, true
}

// decode reads a packet out of src, viewing rather than copying the payload.
func decode(src []byte) (packet, bool) {
	var p packet
	if len(src) < headerLen {
		return p, false
	}
	p.hdr.op = opcode(src[0])
	p.hdr.flags = src[1]
	p.hdr.seq = uint16(src[2])<<8 | uint16(src[3])
	p.payload = src[headerLen:]
	return p, true
}

func (h header) String(dst []byte) int {
	n := 0
	names := "?PRW"
	if int(h.op) < len(names) {
		dst[n] = names[h.op]
	} else {
		dst[n] = '?'
	}
	n++
	dst[n] = byte('0' + h.flags)
	n++
	dst[n] = byte('0' + h.seq/1000%10)
	n++
	dst[n] = byte('0' + h.seq/100%10)
	n++
	dst[n] = byte('0' + h.seq/10%10)
	n++
	dst[n] = byte('0' + h.seq%10)
	n++
	return n
}

var wire [32]byte
var body [4]byte
var text [8]byte

func main() {
	body[0], body[1], body[2], body[3] = 'a', 'b', 'c', 'd'
	p := packet{hdr: header{op: opWrite, flags: 3, seq: 4097}, payload: body[:]}

	n, ok := encode(wire[:], p)
	println(n, ok)
	println(int(wire[0]), int(wire[1]), int(wire[2]), int(wire[3]))
	println(int(wire[4]), int(wire[7]))

	q, ok := decode(wire[:n])
	println(ok, int(q.hdr.op), int(q.hdr.flags), int(q.hdr.seq), len(q.payload))
	println(int(q.payload[0]), int(q.payload[3]))

	m := q.hdr.String(text[:])
	println(m, text[0] == 'W')
	for i := 0; i < m; i++ {
		print(int(text[i]), " ")
	}
	println()

	// A packet with no payload at all, and the opcode compared against the
	// constants rather than a number.
	var empty [0]byte
	ping := packet{hdr: header{op: opPing, seq: 1}, payload: empty[:]}
	n, ok = encode(wire[:], ping)
	println(n, ok)
	r, ok := decode(wire[:n])
	println(ok, r.hdr.op == opPing, r.hdr.op == opRead, len(r.payload))

	// A short buffer is refused, and a truncated wire does not decode.
	var small [2]byte
	_, ok = encode(small[:], p)
	println(ok)
	_, ok = decode(wire[:2])
	println(ok)
}
`,
		want: "8 true\n3 3 16 1\n97 100\ntrue 3 3 4097 4\n97 100\n6 true\n87 51 52 48 57 55 \n4 true\ntrue true false 0\nfalse\nfalse\n",
	},
	{
		// One cog per element: `go ws[i].run(ch)` and `go p.ws[i].run(ch)`, which is
		// the shape a worker pool takes on this target and was refused -- only a
		// method on a plain VARIABLE could be launched, so a pool had to be copied
		// out to a variable one worker at a time.
		//
		// The receiver is walked here and the value it reaches is what the
		// trampoline carries, so it is evaluated where the go stands, as Go says: the
		// last block writes the element after launching and the cog still reports the
		// old value. A pointer receiver takes the address instead, and the lifetime
		// rule reads it the same way it always did -- the address of a LOCAL array's
		// element is still refused, since the cog may outlive the frame.
		name: "a goroutine per element of a worker pool",
		src: `type worker struct {
	id    int
	base  int
	count int
}

// A value receiver: the cog gets a copy taken where the go statement stands.
func (w worker) run(ch chan int) {
	sum := 0
	for i := 0; i < w.count; i++ {
		sum += w.base + i
	}
	ch <- w.id*1000 + sum
}

type pool struct {
	ws []worker
}

var back [3]worker

func main() {
	var ch chan int

	for i := 0; i < 3; i++ {
		back[i].id = i + 1
		back[i].base = (i + 1) * 10
		back[i].count = i + 2
	}

	// One cog per element of a package array.
	go back[0].run(ch)
	println(<-ch)

	// One cog per element of a slice held in a struct -- a worker pool, which is
	// what this shape is for.
	p := pool{ws: back[:]}
	for i := 1; i < 3; i++ {
		go p.ws[i].run(ch)
	}
	a := <-ch
	b := <-ch
	if a > b {
		a, b = b, a
	}
	println(a, b)

	// The receiver is copied where the go stands, so a later write does not reach
	// the cog that already has it.
	go p.ws[0].run(ch)
	back[0].base = 999
	println(<-ch)
}
`,
		want: "1021\n2063 3126\n1021\n",
	},
	{
		// A deferred method call evaluates its receiver where the defer stands, as
		// Go does -- the receiver is an argument, and the arguments were already
		// captured there. It was read again at the return instead, so
		// `defer ws[0].show()` reported what ws[0] held at the END of the function.
		// Silent: an ordinary value, printed at a plausible time.
		//
		// A LOCAL receiver did not compile at all, "unknown package b". The replay is
		// emitted after the body's block scope has been left, and leaving a scope
		// restores the emitter's type environment, so by then the local's name was
		// typed by nothing. A package-level receiver kept working, which is why the
		// corpus missed both halves of this.
		//
		// The adjustment happens at the capture rather than at the call, which is
		// what keeps the two receiver kinds apart: a value receiver captures a copy
		// and shows the old value, a pointer receiver captures the address and sees
		// the later write. Both are checked here, on one variable.
		//
		// What is CALLED is captured on the same rule when it is a value rather than
		// a name -- a function held in a variable or in a struct field -- since Go
		// evaluates that where the defer stands as well. `defer f()` ran whatever f
		// held at the return, and the field form did not compile.
		name: "a deferred method captures its receiver",
		src: `type worker struct {
	id int
}

func (w worker) show(tag string) { println(tag, w.id) }

func (w *worker) bump() { w.id += 100 }

type pool struct {
	ws []worker
}

type box struct {
	run func()
}

func hi() { println("hi") }

func bye() { println("bye") }

var ws [2]worker

func run() {
	// A local receiver: this did not compile at all, the replay being emitted
	// after the body's scope had been left.
	var b worker
	b.id = 1
	defer b.show("local")

	// A value receiver copies at the defer statement; a pointer receiver keeps
	// the address, so it sees the later write.
	ws[0].id = 2
	defer ws[0].show("array value")
	defer ws[0].bump()

	// A receiver reached through a chain.
	p := pool{ws: ws[:]}
	p.ws[1].id = 3
	defer p.ws[1].show("chain")

	// The argument is captured at the defer, as before.
	tag := "arg"
	defer b.show(tag)

	// What is CALLED is captured too, when it is a value rather than a name: a
	// function held in a variable, and one held in a struct field.
	f := hi
	defer f()
	bx := box{run: hi}
	defer bx.run()
	f = bye
	bx.run = bye

	b.id = 11
	ws[0].id = 22
	p.ws[1].id = 33
	tag = "changed"
}

func main() {
	run()
	println("after", ws[0].id, ws[1].id)
}
`,
		want: "hi\nhi\narg 1\nchain 3\narray value 2\nlocal 1\nafter 122 33\n",
	},
	{
		// A select clause may receive into anything an assignment can write to. The
		// clause read only the head identifier off its target, so `case s.last =
		// <-a:` assigned the received int to s -- the whole struct -- and the C
		// compiler is what caught it, "incompatible types in assignment". An element
		// target said the same about the array. The plain `s.last = <-a` outside a
		// select always worked, which is what makes this a clause bug rather than a
		// receive one; the grammar has carried the selectors and indexes all along
		// (PostfixComm), and only this path dropped them.
		//
		// It writes through the same store a multiple assignment does, so the target
		// shapes are the same set. A ":=" clause still declares, still shadows, and
		// now says so when given a target it cannot declare.
		name: "a select clause receiving into a field or an element",
		src: `type sink struct {
	last  int
	slots []int
}

func produce(ch chan int, v int) { ch <- v }

var back [3]int

func main() {
	var a chan int
	var b chan int
	s := sink{slots: back[:]}
	n := 0
	p := &n

	go produce(a, 10)
	select {
	case s.last = <-a:
	}
	println(s.last)

	go produce(b, 20)
	select {
	case s.slots[1] = <-b:
	}
	println(s.slots[0], s.slots[1], s.slots[2])

	go produce(a, 30)
	select {
	case *p = <-a:
	}
	println(n)

	// A declaring clause is unaffected, and still shadows.
	last := 99
	go produce(b, 40)
	select {
	case last := <-b:
		println(last)
	}
	println(last)

	// Several clauses, one of which stores through a chain.
	go produce(a, 50)
	for done := false; !done; {
		select {
		case s.slots[2] = <-a:
			done = true
		case s.last = <-b:
			done = true
		}
	}
	println(s.last, s.slots[2])
}
`,
		want: "10\n0 20 0\n30\n40\n99\n10 50\n",
	},
	{
		// A compound literal inside a cast, which the target's C compiler cannot do.
		// int(total(xs[:])) is the ordinary spelling: a slice expression handed to a
		// call becomes a compound literal in C, and a conversion becomes a cast
		// around it. flexcc warns "Bad number of parameters in call to total:
		// expected 3 found 1" and generates a call that does not pass the value;
		// through a function pointer, or when the literal is a slice header, it
		// refuses the program, and (int)((S){1, 2, 3}.a) crashes it outright. The
		// literal alone is fine and the cast alone is fine.
		//
		// The operand is bound to a temporary, which puts the literal outside the
		// cast. See doc/complit-arg-in-cast.c, which says how to tell whether the
		// workaround is still needed. Nothing else moved: no program in the corpus
		// emitted the shape, which is why this survived to be found by writing one.
		name: "a compound literal inside a conversion",
		src: `type Word int32

type Point struct {
	x, y int
}

type Sum func([]Word) Word

func total(ws []Word) Word {
	var t Word
	for _, w := range ws {
		t += w
	}
	return t
}

func manhattan(p Point) int {
	n := p.x
	if n < 0 {
		n = -n
	}
	m := p.y
	if m < 0 {
		m = -m
	}
	return n + m
}

func main() {
	var xs [3]Word
	xs[0], xs[1], xs[2] = 10, 20, 30

	// A slice expression handed to a call, inside a conversion.
	println(int(total(xs[:])))
	println(int(total(xs[1:])))
	println(int64(total(xs[:])))

	// A composite literal handed to a call, inside a conversion.
	println(int32(manhattan(Point{-3, 4})))

	// The same through a function value, and a conversion to a defined type.
	var f Sum = total
	println(int(Word(f(xs[:]))))

	// A conversion whose operand only contains the call, deeper in an expression.
	println(int(total(xs[:]) + 1))
}
`,
		want: "60\n50\n60\n7\n60\n61\n",
	},
	{
		// The typedef section in dependency order. It used to be fixed groups --
		// struct forwards, function typedefs, scalar slice headers, the named and
		// struct typedefs, struct slice headers -- and real dependencies cut across
		// them, so each of these named a type C had not seen:
		//
		//	type Scale func(Word) Word     a function type naming a defined type
		//	type Sum func([]Word) Word     ... or a slice of one
		//	type head struct{ tail tail }  a field whose struct is declared below
		//	type head struct{ rows []row } ... or a slice of one
		//	func pair() (Word, Word)       a result struct of defined types
		//
		// A fourth group could not have expressed it: a struct holding a function
		// type needs that typedef BETWEEN two entries of the group it is in. Each
		// declaration now carries what it must see first, and the section is sorted
		// on that -- moving a declaration later, never earlier, so a program whose
		// declarations already ordered themselves emits what it emitted before.
		//
		// A pointer to a struct is the one use that depends on nothing, its forward
		// declaration leading the section; a pointer to anything else names a typedef,
		// which C wants declared first. That is the rule the old scalar/struct slice
		// split was a hand-written approximation of.
		name: "typedefs emitted in dependency order",
		src: `type Word int32

// A function type naming a defined type by value, in the result and in a
// parameter, and one naming a slice of it.
type Scale func(Word) Word
type Sum func([]Word) Word

// A struct whose field is a struct declared further down, and one holding a
// slice of it.
type head struct {
	tail tail
	rows []row
}

type tail struct{ n Word }

type row struct{ v Word }

func twice(w Word) Word { return w * 2 }

func total(ws []Word) Word {
	var t Word
	for _, w := range ws {
		t += w
	}
	return t
}

func pair() (Word, Word) { return 3, 4 }

func main() {
	var s Scale = twice
	println(int(s(21)))

	var ws [3]Word
	ws[0], ws[1], ws[2] = 1, 2, 3
	var f Sum = total
	// Bound to a variable rather than written int(f(ws[:])): a compound literal
	// handed to a call inside a cast is miscounted by the backend, which has
	// nothing to do with the typedefs under test here.
	sum := f(ws[:])
	println(int(sum))

	var rows [2]row
	rows[0].v = 5
	rows[1].v = 6
	h := head{rows: rows[:]}
	h.tail.n = 7
	println(int(h.tail.n), int(h.rows[0].v), int(h.rows[1].v))

	a, b := pair()
	println(int(a), int(b))
}
`,
		want: "42\n6\n7 5 6\n3 4\n",
	},
	{
		// Two silent wrong answers, both from a target that is not a plain variable.
		//
		// `*p++` emitted `*p++`, which C reads as `*(p++)`: the POINTER moves and the
		// load is thrown away, where Go means `(*p)++`. Everything after it in the
		// function then wrote through a pointer one past its variable. C's "++" binds
		// tighter than its unary "*"; "=" and the compound operators do not, which is
		// why only this one shape was wrong.
		//
		// `for i, v = range xs` -- the assigning clause, no ":=" -- declared fresh C
		// variables that shadowed i and v for the loop's length, so the loop ran and
		// the variables it named came out untouched. The counter stays the loop's own
		// now and the clause's variables are written from it at the top of each
		// iteration, which is where Go assigns them: after the loop they hold the last
		// index and element, and a `break` leaves them at the iteration it broke on.
		// A ":=" clause still declares, and still shadows an outer name of its own.
		//
		// `for _, v = range xs` was refused outright ("cannot use _ as value or type"):
		// a blank there is the same discard it is on the left of an "=", not a read.
		name: "increment through a pointer, and an assigning range clause",
		src: `func main() {
	n := 0
	p := &n
	*p++
	*p += 4
	*p--
	println(n)

	xs := []int{5, 6, 7}
	var k, v int
	for k, v = range xs {
	}
	println(k, v)

	for k = range xs {
	}
	println(k)

	var a [3]int
	a[0], a[1], a[2] = 8, 9, 10
	for k, v = range a {
	}
	println(k, v)

	s := "héllo"
	var r rune
	for k, r = range s {
	}
	println(k, int(r))

	for k = range 4 {
	}
	println(k)

	sum := 0
	for k, v = range xs {
		sum += k * v
		if k == 1 {
			break
		}
	}
	println(k, v, sum)

	for _, v = range xs {
	}
	println(v)
}
`,
		want: "4\n2 7\n2\n2 10\n5 111\n3\n1 6 6\n7\n",
	},
	{
		// A struct member named after a type. `type logger struct{...}` beside
		// `type app struct{ logger logger }` is ordinary Go -- C keeps member names
		// in a namespace of their own, and gcc agrees -- but the target's C compiler
		// refuses it: "Unable to combine types", pointed at the line before, with
		// nothing in the OctoGo source to connect it to. It hit a field named after
		// a struct, a defined type, or (worst) any type declared anywhere in the
		// program, which is a name a reader has every reason to pick.
		//
		// The member is renamed in the emitted C instead, and only when it does
		// collide, so a program without one emits exactly what it emitted before.
		// Everything that writes a member name goes through the one function that
		// decides, which is what keeps the declaration and every read agreeing.
		name: "a struct field named after a type",
		src: `type logger struct {
	n int
}

func (l *logger) bump() { l.n++ }

type word int32

type entry struct {
	word word
	tag  string
}

type app struct {
	logger  logger
	entries []entry
}

func main() {
	var back [2]entry
	back[0] = entry{word: 5, tag: "a"}
	back[1] = entry{word: 6, tag: "b"}
	a := app{entries: back[:]}

	a.logger.bump()
	a.logger.bump()
	println(a.logger.n)

	for i := 0; i < len(a.entries); i++ {
		println(int(a.entries[i].word), a.entries[i].tag)
	}

	a.entries[0].word, a.entries[1].word = a.entries[1].word, a.entries[0].word
	println(int(a.entries[0].word), int(a.entries[1].word))

	x := entry{word: 5, tag: "a"}
	y := entry{word: 5, tag: "a"}
	println(x == y, x == a.entries[0])

	p := &a.logger
	p.bump()
	println(a.logger.n)
}
`,
		want: "2\n5 a\n6 b\n6 5\ntrue false\n3\n",
	},
	{
		// Every target shape a multiple assignment can take. Only a bare name was
		// modelled before, so `xs[0], xs[2] = xs[2], xs[0]` -- the swap every sort is
		// written with, and the reason this was found -- did not compile, nor did a
		// field, a pointee, or an element of a slice held in a struct.
		//
		// The values are already bound to temporaries in order, which is what makes a
		// swap a swap; what was missing was the other half, a target that is an
		// lvalue rather than a name. Each target now emits the storage it names, so
		// the shapes the single-target paths already reached are reached here too.
		//
		// `*p, *q = *q, *p` used to compile and write the POINTERS, silently: the
		// leading star was read off the head and then dropped, leaving `p = tmp`.
		name: "multiple assignment to elements, fields and pointees",
		src: `type item struct {
	key  int
	name string
}

type table struct {
	es []item
}

func (t *table) swap(i, j int) { t.es[i], t.es[j] = t.es[j], t.es[i] }

func two() (int, int) { return 6, 7 }

var g, h int

func main() {
	xs := []int{1, 2, 3}
	xs[0], xs[2] = xs[2], xs[0]
	println(xs[0], xs[1], xs[2])

	var back [2]item
	back[0].key, back[0].name = 1, "a"
	back[1].key, back[1].name = 2, "b"
	t := &table{es: back[:]}
	t.swap(0, 1)
	println(t.es[0].key, t.es[0].name, t.es[1].key, t.es[1].name)

	g, h = 3, 4
	g, h = h, g
	println(g, h)

	n, m := 0, 0
	p, q := &n, &m
	*p, *q = 8, 9
	*p, *q = *q, *p
	println(n, m)

	var mat [2][2]int
	mat[0][0], mat[1][1] = 5, 6
	mat[0][0], mat[1][1] = mat[1][1], mat[0][0]
	println(mat[0][0], mat[1][1])

	xs[1], g = two()
	println(xs[1], g)

	xs[0], _ = two()
	println(xs[0])
}
`,
		want: "3 2 1\n2 b 1 a\n4 3\n9 8\n6 5\n6 7\n6\n",
	},
	{
		// A stack machine: opcodes dispatched through a table of function values,
		// operands in a fixed stack, a defined type for each thing that has a unit.
		// The shape a P2 program takes when it interprets anything, and it leans on
		// the whole defined-type family at once.
		//
		// It found two things. A multi-result method whose receiver is a FIELD --
		// `m.st.pop()` -- was "multiple assignment requires a single function call
		// on the right-hand side", of a call: only a method on a plain variable was
		// taken. And a function type naming a struct, `func(m *Machine) bool`, put
		// its typedef ahead of the struct's forward declaration, so C had not seen
		// the name; the forwards are emitted first now, which is all a pointer to a
		// struct needs.
		name: "a stack machine with a dispatch table",
		src: `// A stack machine: opcodes dispatched through a table of function values, operands
// in a fixed stack, and a defined type for each thing that has a unit. The shape a
// P2 program takes when it interprets anything -- a command set, a bytecode, a
// sequencer -- and it leans on the whole defined-type family at once.

type Opcode int

type Word int32

type Stack struct {
	data [16]Word
	sp   int
}

type Op func(m *Machine) bool

type Machine struct {
	st    Stack
	steps int
	fault bool
}

const (
	opPush Opcode = iota
	opAdd
	opMul
	opDup
	opDrop
	opNeg
	opCount
)

var program [12]Opcode

var operand [12]Word

var table [opCount]Op

func (s *Stack) push(v Word) bool {
	if s.sp == len(s.data) {
		return false
	}
	s.data[s.sp] = v
	s.sp++
	return true
}

func (s *Stack) pop() (Word, bool) {
	if s.sp == 0 {
		return 0, false
	}
	s.sp--
	return s.data[s.sp], true
}

func (s *Stack) top() Word {
	if s.sp == 0 {
		return 0
	}
	return s.data[s.sp-1]
}

var pending Word

func doPush(m *Machine) bool { return m.st.push(pending) }

func doAdd(m *Machine) bool {
	b, ok1 := m.st.pop()
	a, ok2 := m.st.pop()
	if !ok1 || !ok2 {
		return false
	}
	return m.st.push(a + b)
}

func doMul(m *Machine) bool {
	b, ok1 := m.st.pop()
	a, ok2 := m.st.pop()
	if !ok1 || !ok2 {
		return false
	}
	return m.st.push(a * b)
}

func doDup(m *Machine) bool {
	v, ok := m.st.pop()
	if !ok {
		return false
	}
	return m.st.push(v) && m.st.push(v)
}

func doDrop(m *Machine) bool {
	_, ok := m.st.pop()
	return ok
}

func doNeg(m *Machine) bool {
	v, ok := m.st.pop()
	if !ok {
		return false
	}
	return m.st.push(-v)
}

func install() {
	table[opPush] = doPush
	table[opAdd] = doAdd
	table[opMul] = doMul
	table[opDup] = doDup
	table[opDrop] = doDrop
	table[opNeg] = doNeg
}

func (m *Machine) run(n int) {
	for i := 0; i < n; i++ {
		op := program[i]
		if int(op) < 0 || int(op) >= int(opCount) {
			m.fault = true
			return
		}
		pending = operand[i]
		// The backend refuses a call written directly on an array element of
		// function type, so the handler is bound first.
		h := table[op]
		if !h(m) {
			m.fault = true
			return
		}
		m.steps++
	}
}

func main() {
	install()

	// 3 4 + 5 * dup + neg   ->  -70
	program[0] = opPush
	operand[0] = 3
	program[1] = opPush
	operand[1] = 4
	program[2] = opAdd
	program[3] = opPush
	operand[3] = 5
	program[4] = opMul
	program[5] = opDup
	program[6] = opAdd
	program[7] = opNeg

	var m Machine
	m.run(8)
	println("result", int(m.st.top()), m.steps, m.fault)

	// Underflow faults rather than running off the end of the stack.
	var u Machine
	program[0] = opAdd
	u.run(1)
	println("underflow", u.fault, u.steps)

	// An opcode outside the table faults too.
	var b Machine
	program[0] = Opcode(99)
	b.run(1)
	println("bad opcode", b.fault)
}
`,
		want: "result -70 8 false\nunderflow true 0\nbad opcode true\n",
	},
	{
		// A call through a VARIABLE holding a function was typed nowhere, so
		// `b := a(0)` -- where a holds a function that returns a function -- was
		// "cannot infer a type for the declaration of b". Only a call of a NAMED
		// function had its result type read, in the checker and in the emitter
		// alike.
		//
		// This is also the workaround the three-deep chain lacked: `chooser()(0)(6)`
		// computes 0 on the target, and until now it could not be broken up either.
		// Bound to variables, as here, it is right on the board.
		name: "a call through a function-valued variable",
		src: `type Fn func(int) int

func dbl(v int) int { return v * 2 }

func neg(v int) int { return -v }

func choose(w int) func(int) int {
	if w == 0 {
		return dbl
	}
	return neg
}

func chooser() func(int) func(int) int { return choose }

func twice(f Fn, v int) int { return f(f(v)) }

func main() {
	// A call through a variable holding a function, one level.
	f := dbl
	n := f(5)
	println("one", n)

	// Two levels: the variable's call yields another function, which is what the
	// inference could not name -- and what a chain the backend refuses has to be
	// broken up into.
	a := chooser()
	b := a(0)
	c := a(1)
	println("two", b(6), c(6))

	// The same through a defined function type.
	var g Fn = choose(0)
	println("named", g(7))

	// A function-valued variable passed on, and called twice inside.
	println("arg", twice(f, 3))
}
`,
		want: "one 10\ntwo 12 -6\nnamed 14\narg 12\n",
	},
	{
		// Calling the result of a call, `choose(0)(5)`, which was "too many arguments
		// in call to choose": the call walk took the LAST argument list as the named
		// callee's, so `choose` was checked against `(5)` rather than against `(0)`.
		//
		// The first list is the named callee's; a later one belongs to a different
		// callee -- the previous call's result -- and says nothing about this
		// signature. The first is still checked, so a genuine arity error is still
		// reported; the names in the later lists are resolved separately, since
		// nothing else reaches them.
		//
		// Two calls deep only. THREE -- `chooser()(0)(6)` -- compiles to valid C that
		// gcc computes correctly and the target computes as 0, at every optimization
		// level including -O0, so it is a backend codegen limit rather than the
		// optimizer defects worked around elsewhere. See specs.go.
		name: "calling the result of a call",
		src: `type Fn func(int) int

type Table struct{ pick func(int) func(int) int }

func dbl(v int) int { return v * 2 }

func neg(v int) int { return -v }

func choose(which int) Fn {
	if which == 0 {
		return dbl
	}
	return neg
}

func choose2(which int) func(int) int {
	if which == 0 {
		return dbl
	}
	return neg
}

func main() {
	println("direct", choose(0)(5), choose(1)(5))

	var t Table
	t.pick = choose2
	println("field", t.pick(0)(7))

	// Through a variable: the target's C compiler refuses a call written directly
	// on an array element of function type ("fns is not a function but is called
	// like one"), though gcc takes it.
	var fns [2]Fn
	fns[0] = dbl
	fns[1] = neg
	f0 := fns[0]
	f1 := fns[1]
	println("array", f0(8), f1(8))

	// The first call's own arguments are still checked and still work.
	f := choose(1)
	println("via var", f(9))
}
`,
		want: "direct 10 -5\nfield 14\narray 16 -8\nvia var -9\n",
	},
	{
		// A defined POINTER type, `type PP *Point`, which was not recognized as a
		// pointer: `var q PP = &p` was refused as "cannot use &p (an address) as PP
		// value", the check believing PP wanted a value. Every site that asked the
		// question asked it as a type assertion, which a defined type fails, so the
		// answer had to come from following the definition instead -- in the checker
		// at six of them, and in the emitter where "->" is chosen over "." and where
		// a pointer's element type is read.
		//
		// Covered: a variable, a parameter written through, a package variable, a
		// function result, a chain of definitions, a pointer to a scalar
		// dereferenced, and comparison against nil.
		name: "a defined pointer type",
		src: `type Point struct {
	x int
	y int
}

type PP *Point

type IP *int

type Chain PP

var pool [3]Point

var head PP

func get(q PP) int { return q.x }

func set(q PP, v int) { q.x = v }

func first() PP { return &pool[0] }

func bump(p IP) { *p = *p + 1 }

func main() {
	var p Point
	p.x = 3
	p.y = 4

	var q PP = &p
	println("read", q.x, q.y)

	q.x = 30
	println("write", p.x)

	println("param", get(&p), get(q))
	set(q, 7)
	println("via param", p.x)

	head = &pool[1]
	head.y = 5
	println("pkg", pool[1].y, head.y)

	r := first()
	r.x = 8
	println("result", pool[0].x, r.x)

	var c Chain = &p
	println("chain", c.x)

	v := 10
	var ip IP = &v
	bump(ip)
	println("scalar", *ip, v)

	println("nil", head == nil, PP(nil) == nil)
}
`,
		want: "read 3 4\nwrite 30\nparam 30 30\nvia param 7\npkg 5 5\nresult 8 8\n" +
			"chain 7\nscalar 11 11\nnil false true\n",
	},
	{
		// A defined FUNCTION type, `type Fn func(int) int`, which was not recognized
		// as a function at all: a call through a variable, parameter or field of one
		// was "cannot call non-function". A callback named once and used everywhere
		// is the reason to write such a type.
		//
		// Four resolutions, each following the definition to what it is defined
		// over: the checker's signature lookup, so the call is checked; the
		// emitter's is-it-a-function test, so a field is called through rather than
		// dispatched to; the result-type lookup behind a chain, keyed by the
		// function typedef that a defined name only stands for; and a `:=` copy,
		// which took the type's name and left the signature behind.
		//
		// The package-level variable exercises a fifth thing, which was wrong for an
		// INLINE function type too: prototypes now precede the globals, so a
		// variable initialized with a function has it declared.
		name: "a defined function type",
		src: `type Fn func(int) int

type Pred func(int) bool

type Chain Fn

type Cmd struct {
	name string
	run  Fn
}

var pkgFn Fn = dbl

func dbl(v int) int { return v * 2 }

func neg(v int) int { return -v }

func even(v int) bool { return v%2 == 0 }

func apply(f Fn, v int) int { return f(v) }

func pick(which int) Fn {
	if which == 0 {
		return dbl
	}
	return neg
}

func count(xs []int, p Pred) int {
	n := 0
	for i := 0; i < len(xs); i++ {
		if p(xs[i]) {
			n++
		}
	}
	return n
}

func main() {
	var f Fn = dbl
	println("var", f(4))

	println("param", apply(neg, 5))

	p0 := pick(0)
	p1 := pick(1)
	println("result", p0(3), p1(3))

	var c Cmd
	c.name = "dbl"
	c.run = dbl
	println("field", c.run(6), c.name)

	var table [2]Cmd
	table[0].run = dbl
	table[1].run = neg
	println("table", table[0].run(7), table[1].run(7))

	var ch Chain = neg
	println("chain", ch(8))

	println("pkg", pkgFn(9))

	var back [4]int
	back[0] = 1
	back[1] = 2
	back[2] = 3
	back[3] = 4
	println("pred", count(back[:], even))

	g := f
	f = neg
	println("copy", g(2), f(2))
}
`,
		want: "var 8\nparam -5\nresult 6 -3\nfield 12 dbl\ntable 14 -7\nchain -8\n" +
			"pkg 18\npred 2\ncopy 4 -2\n",
	},
	{
		// A method or a field on the result of a CONVERSION, `Celsius(5).f()`, which
		// was "unsupported call in expression": the chain walk took its base to be a
		// variable or a function, and a conversion is neither -- it looks like a call
		// of the type's own name. A converted value had to be put in a variable
		// first.
		//
		// The conversion consumes the first step of the chain; what it leaves is a
		// value of that type, which the steps after it walk like any other. Covered
		// here on a defined scalar and a defined struct, over an expression rather
		// than a literal, and nested inside another conversion.
		name: "a method on a conversion result",
		src: `type Celsius int

type Point struct {
	x int
	y int
}

type Named Point

func (c Celsius) f() int { return int(c) + 1 }

func (n Named) sum() int { return n.x + n.y }

func main() {
	println("scalar", Celsius(5).f(), Celsius(0).f())

	var p Point
	p.x = 3
	p.y = 4
	println("struct", Named(p).sum(), Named(p).x, Named(p).y)

	// A conversion of an expression, not just a name.
	v := 6
	println("expr", Celsius(v*2).f())

	// Nested: the argument is itself a conversion.
	println("nested", Celsius(int(Celsius(4))).f())

	// Still fine through a variable, the old spelling.
	c := Celsius(7)
	println("via var", c.f())

	// A conversion that is not a chain base at all.
	println("plain", int(Celsius(9)), int(c))
}
`,
		want: "scalar 6 1\nstruct 7 3 4\nexpr 13\nnested 5\nvia var 8\nplain 9 7\n",
	},
	{
		// A defined type over a STRUCT, `type Named Point`, which was not modelled at
		// all: field access, literals, conversions and methods failed together, the
		// first of them as "unsupported expression node FactorSuffix".
		//
		// One cause behind all four. Every one of them asks the emitter's struct
		// table for the fields, keyed by C type name, and a defined type was not in
		// it. Resolving the name once, after every type is collected, fixes the
		// family and makes declaration ORDER irrelevant -- "Early" here is defined
		// over a struct declared below it.
		//
		// The conversion back, `Point(n)`, needed the struct's own name admitted as a
		// conversion type as well; only the name changes, the representation being
		// the same struct.
		name: "a defined type over a struct",
		src: `// Early is defined over a struct declared further down, so the resolution cannot
// depend on declaration order.
type Early Point

type Point struct {
	x int
	y int
}

type Named Point

type Again Named

type Holder struct {
	p Named
	n int
}

func (n Named) sum() int { return n.x + n.y }

func (n *Named) scale(k int) {
	n.x *= k
	n.y *= k
}

func take(n Named) int { return n.x }

func makeOne(v int) Named { return Named{v, v + 1} }

var pkgNamed = Named{3, 4}

func main() {
	var n Named
	n.x = 1
	n.y = 2
	println("fields", n.x, n.y, n.sum())

	n.scale(3)
	println("scaled", n.x, n.y)

	lit := Named{5, 6}
	keyed := Named{y: 9}
	println("literals", lit.x, lit.y, keyed.x, keyed.y)

	p := Point{7, 8}
	conv := Named(p)
	back := Point(conv)
	println("conv", conv.sum(), back.x)

	var a Again
	a.x = 10
	an := Named(a)
	println("chain", a.x, an.sum())

	var e Early
	e.x = 11
	println("early", e.x)

	println("call", take(lit), makeOne(20).sum())

	var h Holder
	h.p.x = 2
	h.p.y = 3
	h.n = 1
	println("field of struct", h.p.sum(), h.n)

	var arr [2]Named
	arr[1] = lit
	println("array", arr[1].x, len(arr))

	var backing [2]Named
	s := backing[:]
	s[0] = keyed
	println("slice", s[0].y, len(s))

	copyOf := n
	copyOf.x = 99
	println("copy", n.x, copyOf.x)

	println("equal", lit == Named{5, 6}, lit == keyed)
	println("pkg", pkgNamed.sum())
}
`,
		want: "fields 1 2 3\nscaled 3 6\nliterals 5 6 0 9\nconv 15 7\nchain 10 10\n" +
			"early 11\ncall 5 41\nfield of struct 5 1\narray 5 2\nslice 9 2\n" +
			"copy 3 99\nequal true false\npkg 7\n",
	},
	{
		// A slice whose ELEMENT is a defined type. Its header typedef names that
		// type, and was emitted ahead of the typedef declaring it -- C refused the
		// program with "unknown type name 'Celsius'". Slice headers were already
		// split into those that may precede the typedef section and those that must
		// follow it; a defined type is written in that section too, and belonged on
		// the second side of that split.
		//
		// An ARRAY of the same element always worked, which is why nothing noticed.
		name: "a slice of a defined element type",
		src: `type Celsius int

type Name string

type Flag bool

type Row [2]Celsius

var pkgBacking [2]Celsius

var pkgSlice []Celsius

func warmest(xs []Celsius) Celsius {
	m := Celsius(0)
	for i := 0; i < len(xs); i++ {
		if xs[i] > m {
			m = xs[i]
		}
	}
	return m
}

func main() {
	var back [3]Celsius
	s := back[:]
	s[0] = 7
	s[1] = 21
	s[2] = 14
	println("slice", int(s[0]), len(s), cap(s))
	println("warmest", int(warmest(s)))

	lit := []Celsius{1, 30, 2}
	println("literal", int(lit[1]), len(lit))

	var names [2]Name
	names[0] = "ab"
	ns := names[:]
	println("names", len(ns), len(ns[0]), ns[0] == "ab")

	var flags [2]Flag
	fs := flags[:]
	fs[1] = true
	println("flags", fs[0], fs[1])

	var r Row
	r[1] = 9
	rs := r[:]
	println("row", int(rs[1]), len(rs))

	pkgSlice = pkgBacking[:]
	pkgSlice[0] = 5
	println("pkg", int(pkgSlice[0]), len(pkgSlice))

	total := Celsius(0)
	for _, v := range s {
		total += v
	}
	println("range", int(total))
}
`,
		want: "slice 7 3 3\nwarmest 21\nliteral 30 3\nnames 2 2 true\nflags false true\n" +
			"row 9 2\npkg 5 2\nrange 42\n",
	},
	{
		// A composite literal of a DEFINED array or slice type, `Row{1, 2, 3}` for
		// `type Row [3]int`, which was refused as "Row is not a struct type" -- the
		// literal type had to be written out. A defined type behaves as what it is
		// defined over everywhere else, and this was the hole in that.
		//
		// Covered: an array and a slice form, index-keyed values, a chain of
		// definitions (`type Alias List`), a defined byte slice, package scope, an
		// empty literal, and one passed straight to a call -- the shape that needs
		// the backing array hoisted rather than brace-initialized in place.
		name: "composite literal of a defined array or slice type",
		src: `type Row [3]int

type List []int

type Bytes []byte

type Alias List

type Grid [2][3]int

var table = Row{7, 8, 9}

var pkgList = List{4, 5}

func sum(l List) int {
	t := 0
	for i := 0; i < len(l); i++ {
		t += l[i]
	}
	return t
}

func main() {
	r := Row{1, 2, 3}
	println("row", r[0], r[2], len(r))

	var r2 Row = Row{4, 5, 6}
	println("row2", r2[1], len(r2))

	sparse := Row{2: 9}
	println("sparse", sparse[0], sparse[2])

	l := List{1, 2, 3}
	println("list", len(l), cap(l), l[2])

	println("sum", sum(List{10, 20, 30}), sum(l))

	b := Bytes{65, 66}
	println("bytes", len(b), b[0])

	a := Alias{1, 2}
	println("alias", len(a), a[1])

	var g Grid
	g[1][2] = 5
	println("grid", g[1][2], len(g))

	println("pkg", table[0], len(table), pkgList[1], len(pkgList))

	empty := List{}
	println("empty", len(empty))
}
`,
		want: "row 1 3 3\nrow2 5 3\nsparse 0 9\nlist 3 3 3\nsum 60 6\nbytes 2 65\n" +
			"alias 2 2\ngrid 5 2\npkg 7 3 5 2\nempty 0\n",
	},
	{
		// A priority scheduler over a fixed node pool: the no-heap way to keep an
		// ordered queue on this part. Nodes live in a package-level array, a free
		// list threads through them, and the ready queue is a singly linked list in
		// priority order — so every pointer aims at storage that outlives every
		// frame, which is what makes handing one out legal here.
		//
		// It reaches three things the fuzzer cannot generate and the rest of the
		// corpus barely touches: pointers into a package array, threaded and
		// re-threaded through struct fields; a labeled break leaving a nested
		// search; and a deferred call that runs after the result is fixed and must
		// not change it. Output matches real Go, on the host and on the board.
		name: "priority scheduler over a node pool",
		src: `// A priority scheduler over a fixed node pool: the no-heap way to keep an ordered
// queue on this part. Nodes live in a package-level array, a free list threads
// through them, and the ready queue is a singly linked list kept in priority
// order. Everything is a pointer into storage that outlives every frame, which is
// what makes handing one out legal here.

const poolSize = 8

type task struct {
	id   int
	prio int
	next *task
}

var pool [poolSize]task
var free *task
var ready *task
var allocs int
var frees int

// initPool threads every node onto the free list, highest index first so alloc
// hands them out in order.
func initPool() {
	free = nil
	for i := poolSize - 1; i >= 0; i-- {
		pool[i].id = 0
		pool[i].prio = 0
		pool[i].next = free
		free = &pool[i]
	}
}

func alloc() *task {
	if free == nil {
		return nil
	}
	t := free
	free = t.next
	t.next = nil
	allocs++
	return t
}

func release(t *task) {
	t.next = free
	free = t
	frees++
}

// push inserts in priority order, highest first, stable among equals.
func push(t *task) {
	if ready == nil || t.prio > ready.prio {
		t.next = ready
		ready = t
		return
	}
	p := ready
	for p.next != nil && p.next.prio >= t.prio {
		p = p.next
	}
	t.next = p.next
	p.next = t
}

func pop() (*task, bool) {
	if ready == nil {
		return nil, false
	}
	t := ready
	ready = t.next
	t.next = nil
	return t, true
}

// admit allocates a node and queues it, reporting whether the pool had room.
func admit(id int, prio int) bool {
	t := alloc()
	if t == nil {
		return false
	}
	t.id = id
	t.prio = prio
	push(t)
	return true
}

// findFirst returns the id of the first queued task whose priority is in
// [lo, hi], or -1. The labeled break leaves the search from inside the inner
// scan.
func findFirst(lo int, hi int) int {
	found := -1
search:
	for p := ready; p != nil; p = p.next {
		for r := lo; r <= hi; r++ {
			if p.prio == r {
				found = p.id
				break search
			}
		}
	}
	return found
}

// sweep returns whatever is still queued to the pool.
func sweep() {
	for {
		t, ok := pop()
		if !ok {
			return
		}
		release(t)
	}
}

// drain moves as much of the queue into out as fits, returning how many. The
// deferred sweep reclaims the rest, so a caller's short buffer cannot leak nodes
// -- and it runs after the result has been fixed, so it cannot change it.
func drain(out []int) int {
	defer sweep()
	n := 0
	for n < len(out) {
		t, ok := pop()
		if !ok {
			break
		}
		out[n] = t.id
		n++
		release(t)
	}
	return n
}

func main() {
	initPool()

	println("admit", admit(1, 5), admit(2, 9), admit(3, 5), admit(4, 1))
	println("order", ready.id, ready.next.id, ready.next.next.id, ready.next.next.next.id)
	println("find", findFirst(5, 5), findFirst(1, 1), findFirst(6, 8))

	var out [3]int
	moved := drain(out[:])
	println("drain", moved, out[0], out[1], out[2])
	println("counts", allocs, frees, ready == nil)

	// Fill the pool exactly, then one too many.
	initPool()
	ok := true
	for i := 0; i < poolSize; i++ {
		if !admit(i, i) {
			ok = false
		}
	}
	println("full", ok, admit(99, 99))

	// Highest priority first out.
	var all [poolSize]int
	got := drain(all[:])
	println("popped", got, all[0], all[1], all[poolSize-1])
}
`,
		want: "admit true true true true\norder 2 1 3 4\nfind 1 4 -1\ndrain 3 2 1 3\n" +
			"counts 4 4 true\nfull true false\npopped 8 7 6 0\n",
	},
	{
		// A console command loop: a dispatch table of name/handler pairs, a
		// tokenizer over a fixed line buffer, an integer parser, and replies
		// formatted into a caller-owned Builder. The shape of every serial-port
		// monitor on this part, and it found four separate bugs -- the for header
		// and the function-field call below, the Builder's unchecked method set,
		// and a target printf that truncated at 62 characters.
		//
		// Its output is 102 characters, which is what makes the last of those show:
		// print of anything longer used to lose the tail, on the board only and
		// without a word.
		name: "console command loop",
		src: `// A console command loop: a dispatch table of name/handler pairs, a tokenizer
// over a fixed line buffer, an integer parser, and replies formatted into a
// caller-owned buffer. The shape of every serial-port monitor on this part.

type command struct {
	name string
	help string
	run  func(int) int
}

var reg [4]int32

func cmdSet(v int) int {
	reg[0] = int32(v)
	return v
}

func cmdAdd(v int) int {
	reg[0] += int32(v)
	return int(reg[0])
}

func cmdShift(v int) int {
	reg[0] <<= uint(v)
	return int(reg[0])
}

func cmdGet(v int) int { return int(reg[0]) }

// split finds the first space, returning the verb and the rest. A line with no
// space is all verb.
func split(line string) (string, string) {
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' {
			return line[0:i], line[i+1:]
		}
	}
	return line, ""
}

// parseInt reads a decimal integer, optionally signed, reporting whether the
// whole argument was consumed.
func parseInt(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	i := 0
	neg := false
	if s[0] == '-' {
		neg = true
		i = 1
	}
	if i == len(s) {
		return 0, false
	}
	n := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

// writeInt formats a decimal into the builder, digits high to low out of a
// fixed scratch array -- there is no allocation to grow one.
func writeInt(out *Builder, v int) {
	if v < 0 {
		out.WriteByte('-')
		v = -v
	}
	var digits [12]byte
	n := 0
	for {
		digits[n] = byte('0' + v%10)
		n++
		v /= 10
		if v == 0 {
			break
		}
	}
	for ; n > 0; n-- {
		out.WriteByte(digits[n-1])
	}
}

func dispatch(table []command, line string, out *Builder) {
	verb, rest := split(line)
	if verb == "" {
		return
	}
	if verb == "help" {
		for i := 0; i < len(table); i++ {
			out.WriteString(table[i].name)
			out.WriteString(":")
			out.WriteString(table[i].help)
			out.WriteString(" ")
		}
		out.WriteString("\n")
		return
	}
	for i := 0; i < len(table); i++ {
		if table[i].name != verb {
			continue
		}
		arg := 0
		if rest != "" {
			v, ok := parseInt(rest)
			if !ok {
				out.WriteString("bad number: ")
				out.WriteString(rest)
				out.WriteString("\n")
				return
			}
			arg = v
		}
		out.WriteString(verb)
		out.WriteString(" -> ")
		writeInt(out, table[i].run(arg))
		out.WriteString("\n")
		return
	}
	out.WriteString("unknown: ")
	out.WriteString(verb)
	out.WriteString("\n")
}

func main() {
	var table [4]command
	table[0].name = "set"
	table[0].help = "v"
	table[0].run = cmdSet
	table[1].name = "add"
	table[1].help = "v"
	table[1].run = cmdAdd
	table[2].name = "shl"
	table[2].help = "n"
	table[2].run = cmdShift
	table[3].name = "get"
	table[3].help = ""
	table[3].run = cmdGet

	var back [256]byte
	out := NewBuilder(back[:])

	var script [8]string
	script[0] = "set 7"
	script[1] = "add 5"
	script[2] = "shl 2"
	script[3] = "get"
	script[4] = "add -50"
	script[5] = "nope 1"
	script[6] = "add x9"
	script[7] = "help"

	for i := 0; i < len(script); i++ {
		dispatch(table[:], script[i], &out)
	}
	print(out.String())
	println("len", len(out.String()))
}
`,
		want: "set -> 7\nadd -> 12\nshl -> 48\nget -> 48\nadd -> -2\nunknown: nope\n" +
			"bad number: x9\nset:v add:v shl:n get: \nlen 102\n",
	},
	{
		// A three-clause "for" with an EMPTY init clause, `for ; i < n; i++`, which
		// was broken twice over: the checker took it for a conditionless loop -- one
		// that never ends -- and reported everything after it as unreachable, and
		// the emitter dropped the post clause, so once it compiled it looped
		// forever. Such a header carries both semicolons and the post as its own
		// children rather than in a ForRest, which neither walk read.
		name: "for with an empty init clause",
		src: `func main() {
	i := 0
	for ; i < 3; i++ {
	}
	println(i)

	j := 0
	for ; j < 10; j = j + 3 {
	}
	println(j)

	k := 5
	for ; k > 0; k-- {
	}
	println(k)

	sum := 0
	n := 0
	for ; n < 6; n++ {
		if n%2 == 0 {
			continue
		}
		if n == 5 {
			break
		}
		sum += n
	}
	println(sum, n)

	total := 0
	a := 0
	for ; a < 3; a++ {
		for b := 0; b < 2; b++ {
			total += a * b
		}
	}
	println(total, a)
}
`,
		want: "3\n12\n0\n4 5\n3 3\n",
	},
	{
		// A function value held in a struct field, called through an INDEXED
		// element: `table[i].run(arg)`, which is what a dispatch table is. The chain
		// walk took any selector-then-call for a method and gave up when the type
		// had no method of that name, instead of falling through to the field --
		// which the two-step shape `x.run(arg)` had always done.
		name: "calling a function field through an index",
		src: `type command struct {
	name string
	run  func(int) int
}

func dbl(v int) int { return v * 2 }

func neg(v int) int { return -v }

func main() {
	var table [2]command
	table[0].name = "dbl"
	table[0].run = dbl
	table[1].name = "neg"
	table[1].run = neg

	total := 0
	s := table[:]
	for i := 0; i < len(s); i++ {
		total += s[i].run(5)
	}
	println(total)
	println(table[0].run(3), table[1].run(3))
}
`,
		want: "5\n6 -3\n",
	},
	{
		// A fixed-point PID controller driving a first-order plant, Q16.16
		// throughout: a scaled multiply through a 64-bit intermediate, a signed
		// shift back down, saturation on both rails, integral anti-windup, and a
		// derivative over a signed difference. What a motor loop on this part is.
		//
		// `const one = int32(1) << fracBits` is how the scale is written, and it did
		// not compile: a conversion was not accepted in a constant expression at
		// all, package-level or local, for any target type.
		//
		// The tail checks the arithmetic the loop rests on rather than the loop:
		// mul over negatives and the extremes, an int32 product that overflows on
		// the way back down, and that a signed shift of a negative value rounds
		// toward minus infinity while a division by the same power of two does not.
		// Every line matches real Go.
		name: "fixed-point PID controller",
		src: `// A fixed-point PID controller driving a first-order plant, Q16.16 throughout.
// Everything a motor loop does: a scaled multiply through a wider intermediate,
// a signed shift back down, saturation both ways, integral anti-windup, and a
// derivative over a signed difference.

const (
	fracBits = 16
	one      = int32(1) << fracBits
	outMax   = 100 * one
	outMin   = -outMax
)

type PID struct {
	kp, ki, kd int32
	integral   int32
	prevErr    int32
	saturated  int32
}

// mul multiplies two Q16.16 values. The product needs 64 bits before it comes
// back down, which is the whole reason a controller like this is written in
// int64 on a 32-bit part.
func mul(a int32, b int32) int32 {
	p := int64(a) * int64(b)
	return int32(p >> fracBits)
}

func clamp(v int32, lo int32, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (c *PID) step(setpoint int32, measured int32) int32 {
	err := setpoint - measured

	// Anti-windup: the integral only accumulates while the output is off its rail
	// or the error would bring it back.
	if c.saturated == 0 || (err < 0) != (c.integral < 0) {
		c.integral += err
		c.integral = clamp(c.integral, -400*one, 400*one)
	}

	d := err - c.prevErr
	c.prevErr = err

	raw := mul(c.kp, err) + mul(c.ki, c.integral) + mul(c.kd, d)
	out := clamp(raw, outMin, outMax)
	if out != raw {
		c.saturated = 1
	} else {
		c.saturated = 0
	}
	return out
}

func main() {
	var c PID
	c.kp = one / 4       // 0.25
	c.ki = one / 512     // ~0.002
	c.kd = one * 2       // 2.0
	setpoint := 50 * one // the plant should settle here

	plant := int32(0)
	sumOut := int64(0)
	for i := 0; i < 200; i++ {
		u := c.step(setpoint, plant)
		// A first-order plant: it moves a thirty-second of the way toward u.
		plant += (u - plant) >> 5
		sumOut += int64(u)
	}
	println("settled", plant>>fracBits)
	println("error", (setpoint-plant)>>fracBits)
	println("sum", sumOut>>fracBits)

	// The rails, reached from both sides.
	var s PID
	s.kp = 100 * one
	println("hi", s.step(50*one, 0)>>fracBits, s.saturated)
	println("lo", s.step(0, 50*one)>>fracBits, s.saturated)

	// mul over the awkward values: negative, the extremes, and a rounding case.
	println("mul", mul(-one/2, one*3), mul(one/3, one/3), mul(-one, -one))
	var big int32 = 1 << 30
	println("wide", mul(big, one*2), int32(int64(big)*4>>fracBits))

	// A signed shift of a negative value rounds toward minus infinity in both
	// languages, which is not what dividing by a power of two does.
	var n int32 = -33
	println("shift", n>>5, n/32, -33>>5)
}
`,
		want: "settled 10\nerror 39\nsum 2243\nhi 100 1\nlo -100 1\n" +
			"mul -98304 7281 65536\nwide -2147483648 65536\nshift -2 -1 -2\n",
	},
	{
		// A work-queue scheduler over the WHOLE cog pool, retired and restarted:
		// seven workers, three rounds, twenty-one goroutines started and stopped.
		// The dispatcher multiplexes handing out the next job against taking a
		// result back, because sending them all first deadlocks -- a worker holding
		// a finished result cannot take another job. That is what select is for, and
		// it is the shape a user writes rather than the one a feature test writes.
		//
		// What it covers that the contention cases do not: the pool FULL (main plus
		// seven), every slot recycled twice, a struct crossing a channel as the
		// result type, and a 64-bit field inside it wide enough that a truncated one
		// would show.
		//
		// Every assertion is order-independent -- which cog takes which job is
		// unspecified. The bitmask says each job ran exactly once per round, and
		// "bad" counts results naming a worker outside the pool.
		name: "work-queue scheduler over the cog pool",
		src: `const (
	workers = 7
	perRound = 9
	rounds  = 3
	stop    = -1
)

type result struct {
	job int
	sum int
	tag int64
}

func work(n int) int {
	acc := 0
	for i := 1; i <= n; i++ {
		acc += i * i
	}
	return acc
}

func worker(id int, in chan int, out chan result) {
	for {
		j := <-in
		if j == stop {
			return
		}
		var r result
		r.job = j
		r.sum = work(j)
		r.tag = int64(id) << 40 // wide enough that a truncated field would show
		out <- r
	}
}

// run dispatches perRound jobs over a freshly started pool and retires it. Every
// round starts and stops every cog in the pool, so the slots have to come back.
//
// It returns the total, a bitmask of the jobs it saw, and how many results named
// a worker outside the pool -- which no scheduling order may change.
func run(in chan int, out chan result) (int, int, int) {
	for i := 0; i < workers; i++ {
		go worker(i, in, out)
	}
	sent := 0
	got := 0
	total := 0
	seen := 0
	bad := 0
	for sent < perRound || got < perRound {
		if sent < perRound {
			select {
			case in <- sent + 1:
				sent++
			case r := <-out:
				got++
				total += r.sum
				seen |= 1 << r.job
				if r.tag>>40 < 0 || r.tag>>40 >= workers {
					bad++
				}
			}
			continue
		}
		r := <-out
		got++
		total += r.sum
		seen |= 1 << r.job
		if r.tag>>40 < 0 || r.tag>>40 >= workers {
			bad++
		}
	}
	for i := 0; i < workers; i++ {
		in <- stop
	}
	return total, seen, bad
}

func main() {
	var in chan int
	var out chan result

	grand := 0
	allSeen := 0
	bad := 0
	for round := 0; round < rounds; round++ {
		t, s, b := run(in, out)
		grand += t
		allSeen |= s
		if s != 1023-1 {
			bad += 100 // every job 1..9 exactly once, whatever the order
		}
		bad += b
	}
	println("grand", grand)
	println("seen", allSeen)
	println("bad", bad)
}
`,
		want: "grand 2475\nseen 1022\nbad 0\n",
	},
	{
		// len and cap of a struct's ARRAY field, which were refused outright: both
		// resolved an array only through a bare variable name, so `len(r.buf)` fell
		// through to the string/slice header path and failed. The bound is a
		// compile-time constant, so nothing is read to produce it.
		//
		// Covered here: a plain field, one reached through a pointer receiver, a
		// nested one, and a multi-dimensional one (whose len is the outer extent, as
		// for a variable). The slice and string fields beside them already worked and
		// are here so the new path cannot swallow them.
		//
		// Because the bound folds, a parameter or a constant whose ONLY use is a len
		// or a cap is a name the emitted C never mentions, and the host compiler
		// warns -- which this harness fails on. Go counts such a use as a use, C has
		// nothing to count. So `total` also reads a field and `n` is also printed.
		// The real backend is silent either way; this shapes the program, not the
		// language.
		name: "len and cap of an array field",
		src: `const n = 4

type Inner struct {
	small [2]uint8
}

type T struct {
	buf   [n]int
	grid  [2][3]int
	data  []int
	txt   string
	inner Inner
}

// total reads a field as well as measuring one: len and cap fold to compile-time
// constants, so a parameter measured but never read is a parameter the emitted C
// does not mention, and the host compiler says so.
func total(t *T) int { return len(t.buf) + cap(t.buf) + t.buf[3] }

func main() {
	var t T
	t.data = make([]int, 2, 5)
	t.txt = "hey"
	println(len(t.buf), cap(t.buf), n)
	println(len(t.grid), len(t.inner.small), cap(t.inner.small))
	println(len(t.data), cap(t.data), len(t.txt))
	for i := 0; i < len(t.buf); i++ {
		t.buf[i] = i * i
	}
	println(t.buf[0], t.buf[1], t.buf[2], t.buf[3])
	println(total(&t))
}
`,
		want: "4 4 4\n2 2 2\n2 5 3\n0 1 4 9\n17\n",
	},
	{
		// A byte-oriented framing receiver: SLIP-style escaping around a payload
		// with a CRC-8 trailer, driven by a state machine over a method value
		// receiver. The first thing a P2 program that talks to anything needs, and
		// the shape that found the array bound below.
		//
		// A struct field's array bound naming a CONSTANT (`buf [maxFrame]uint8`)
		// did not compile: struct typedefs are emitted before the constants, so the
		// bound was out of reach and the field failed as `unsupported type ""`. A
		// local or package-level array of the same shape worked, which is why it
		// went unnoticed -- nothing in the corpus put one in a struct.
		//
		// Output matches real Go, including the CRC vector.
		name: "framing receiver with escaping and CRC",
		src: `// A byte-oriented framing receiver: SLIP-style escaping around a payload, a
// length byte, and a CRC-8 trailer. This is the first thing a P2 program that
// talks to anything needs.

const (
	frameEnd  uint8 = 0xC0
	frameEsc  uint8 = 0xDB
	escEnd    uint8 = 0xDC
	escEsc    uint8 = 0xDD
	maxFrame        = 16
)

const (
	stIdle = iota
	stData
	stEscape
)

type Receiver struct {
	state   int
	buf     [maxFrame]uint8
	n       int
	frames  int
	dropped int
	crc     uint8
}

// crc8 is the Dallas/Maxim polynomial, the one a 1-Wire or sensor bus uses.
func crc8(sum uint8, b uint8) uint8 {
	sum ^= b
	for i := 0; i < 8; i++ {
		if sum&0x80 != 0 {
			sum = sum<<1 ^ 0x07
		} else {
			sum = sum << 1
		}
	}
	return sum
}

func (r *Receiver) reset() {
	r.state = stData
	r.n = 0
	r.crc = 0
}

func (r *Receiver) store(b uint8) {
	if r.n >= maxFrame {
		r.dropped++
		r.state = stIdle
		return
	}
	r.buf[r.n] = b
	r.n++
	r.crc = crc8(r.crc, b)
}

// feed advances the machine by one byte and reports whether a frame completed.
func (r *Receiver) feed(b uint8) bool {
	switch r.state {
	case stIdle:
		if b == frameEnd {
			r.reset()
		}
		return false
	case stEscape:
		r.state = stData
		switch b {
		case escEnd:
			r.store(frameEnd)
		case escEsc:
			r.store(frameEsc)
		default:
			r.dropped++
			r.state = stIdle
		}
		return false
	}
	switch b {
	case frameEnd:
		if r.n == 0 {
			return false // a repeated delimiter, not an empty frame
		}
		r.state = stIdle
		// The last stored byte is the CRC over the ones before it, so folding it
		// in leaves zero when the frame is intact.
		if r.crc != 0 {
			r.dropped++
			return false
		}
		r.frames++
		return true
	case frameEsc:
		r.state = stEscape
		return false
	}
	r.store(b)
	return false
}

// encode wraps payload in a frame, escaping as it goes, and returns the length
// written into out.
func encode(out []uint8, payload []uint8, corrupt bool) int {
	n := 0
	out[n] = frameEnd
	n++
	var sum uint8 = 0
	for i := 0; i < len(payload); i++ {
		b := payload[i]
		sum = crc8(sum, b)
		if b == frameEnd || b == frameEsc {
			out[n] = frameEsc
			n++
			if b == frameEnd {
				b = escEnd
			} else {
				b = escEsc
			}
		}
		out[n] = b
		n++
	}
	if corrupt {
		sum++
	}
	if sum == frameEnd || sum == frameEsc {
		out[n] = frameEsc
		n++
		if sum == frameEnd {
			sum = escEnd
		} else {
			sum = escEsc
		}
	}
	out[n] = sum
	n++
	out[n] = frameEnd
	n++
	return n
}

func main() {
	var r Receiver
	r.state = stIdle

	var wire [64]uint8
	var payload [8]uint8

	// A plain payload.
	payload[0] = 1
	payload[1] = 2
	payload[2] = 3
	n := encode(wire[:], payload[0:3], false)
	got := 0
	for i := 0; i < n; i++ {
		if r.feed(wire[i]) {
			got++
		}
	}
	println("plain", n, got, r.n, r.buf[0], r.buf[1], r.buf[2])

	// A payload holding both reserved bytes, so the escaping is exercised.
	payload[0] = frameEnd
	payload[1] = 0x42
	payload[2] = frameEsc
	n = encode(wire[:], payload[0:3], false)
	got = 0
	for i := 0; i < n; i++ {
		if r.feed(wire[i]) {
			got++
		}
	}
	println("escaped", n, got, r.n, r.buf[0], r.buf[1], r.buf[2])

	// A corrupted CRC is rejected.
	payload[0] = 9
	payload[1] = 8
	n = encode(wire[:], payload[0:2], true)
	got = 0
	for i := 0; i < n; i++ {
		if r.feed(wire[i]) {
			got++
		}
	}
	println("corrupt", got, r.dropped)

	// A frame longer than the buffer is dropped, and the receiver resynchronises.
	var big [24]uint8
	for i := 0; i < 20; i++ {
		big[i] = uint8(i + 1)
	}
	n = encode(wire[:], big[0:20], false)
	got = 0
	for i := 0; i < n; i++ {
		if r.feed(wire[i]) {
			got++
		}
	}
	payload[0] = 7
	n = encode(wire[:], payload[0:1], false)
	for i := 0; i < n; i++ {
		if r.feed(wire[i]) {
			got++
		}
	}
	println("overrun", got, r.frames, r.dropped, r.buf[0])

	// The CRC itself, over a known vector.
	var sum uint8 = 0
	for i := 0; i < 9; i++ {
		sum = crc8(sum, uint8(48+i))
	}
	println("crc", sum)
}
`,
		want: "plain 6 1 4 1 2 3\nescaped 8 1 4 192 66 219\ncorrupt 0 1\n" +
			"overrun 1 3 2 7\ncrc 79\n",
	},
	{
		// Arithmetic on a type narrower than C's int, which C promotes to int and
		// computes there while Go computes in the operand's own type. `a * 3` with
		// `var a uint8 = 200` is 88 in Go and 600 in C.
		//
		// Storing the result back into a narrow variable truncated it anyway, which
		// is why this only showed for a value that was used without being stored --
		// printed, passed, compared -- and why it went unnoticed: the corpus assigned
		// before it printed. Every operator that can carry a value out of the type is
		// here, on a local, an array element, a struct field, a defined type and a
		// function result.
		name: "arithmetic narrower than int",
		src: `type Byte uint8

type pair struct {
	lo uint8
	hi int16
}

func scale(v uint8) uint8 { return v * 3 }

func main() {
	var a uint8 = 200
	var b uint8 = 100
	println(a+b, b-a, a*3, a<<2, -a, ^a)
	var c int8 = 100
	println(c+c, c*3, c<<2, -c, ^c)
	var u uint16 = 60000
	println(u+u, u*3, -u, ^u)
	var s int16 = 30000
	println(s+s, -s, ^s)

	var arr [2]uint8
	arr[0] = 200
	println(arr[0]*3, -arr[0], ^arr[0])

	var p pair
	p.lo = 200
	p.hi = 30000
	println(p.lo*3, -p.lo, p.hi+p.hi)

	var n Byte = 200
	println(n*3, -n, ^n)

	println(scale(200))

	// Converting to a wider type first computes in the wider one, as in Go.
	println(int32(a)*3, int16(c)*3)

	// A narrow value in a wider context keeps its own arithmetic.
	var w int32 = 1000
	println(w + int32(a*3))
}
`,
		want: "44 156 88 32 56 55\n-56 44 -112 -100 -101\n54464 48928 5536 5535\n" +
			"-5536 -30000 -30001\n88 56 55\n88 56 -5536\n88 56 55\n88\n600 300\n1088\n",
	},
	{
		// Clearing bits in a variable from that same variable -- `x = mask &^ x`,
		// the shape a driver writes -- and the complement's other spellings, across
		// a local, a parameter, a struct field, an array element and a package
		// variable.
		//
		// The target's C compiler miscompiles an AND with the complement of the
		// very variable being assigned to a constant 0, so the emitter spells the
		// complement as an explicit XOR with all ones (emitComplement). Only the
		// board shows it -- every host C compiler gets it right -- which is how it
		// survived until the fuzzer's oracle ran on real hardware.
		//
		// The sized cases pin the other half of that spelling: the all-ones
		// constant carries the operand's own type, so ^uint8(200) is 55 as in Go
		// and not the int -201 a C "~" would leave.
		name: "complement of the variable being assigned",
		src: `type P struct{ f int }

var g int

func clear(x int) int {
	x = 96 &^ x
	return x
}

func main() {
	var x int
	x = 96 &^ x
	println(x)
	x = 3
	x = 5 & ^x
	println(x)
	println(clear(3))

	var a [2]int
	a[0] = 96 &^ a[0]
	println(a[0])
	a[1] = 3
	i := 1
	a[i] = 5 &^ a[i]
	println(a[1])

	var p P
	p.f = 3
	p.f = 5 &^ p.f
	println(p.f)

	g = 3
	g = 5 &^ g
	println(g)

	y := 12
	y &^= 5
	println(y)

	var u8 uint8 = 200
	println(^u8)
	var u uint32 = 0xF0F0F0F0
	var v uint32 = 0xFF00FF00
	println(^u, u&^v, u&^uint32(0xFF00FF00))
	var s16 int16 = 100
	println(^s16, s16&^int16(12))
}
`,
		want: "96\n4\n96\n96\n4\n4\n4\n8\n55\n252645135 15728880 15728880\n-101 96\n",
	},
	{
		// The clause shapes a switch is built from, all in one program: a case
		// listing several values, an empty clause (which selects nothing to run
		// rather than falling into the next one), a default that runs, and a
		// default that is skipped because a later case matched -- Go lets the
		// default sit anywhere, so the if/else lowering cannot assume it is last.
		//
		// The smith fuzzer generates exactly these shapes, so the corpus documents
		// what it relies on.
		name: "switch clause shapes",
		src: `func f(n int) int {
	switch n {
	case 1:
		return 10
	default:
		return 99
	case 2, 3:
		return 20
	}
}

func main() {
	println(f(1), f(2), f(3), f(7))
	x := 5
	switch x {
	case 4:
		println("no")
	case 5:
		println("yes")
	default:
		println("default")
	}
	switch x {
	case 5:
	default:
		println("not reached")
	}
	println("done")
}
`,
		want: "10 20 20 99\nyes\ndone\n",
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
		// A switch guard binds an ordinary variable, which the emitter used to write
		// out by hand rather than declare as one. Three things went wrong, and all
		// three are fixed by declaring it the way every other local is declared.
		//
		// A `v := expr` guard whose initializer names the variable it shadows read
		// the new, uninitialized C variable instead of the outer one, so the case
		// below did not run at all. A Unicode-named guard was declared under its
		// source spelling while every use of it was escaped, which does not compile.
		// And the temporary a non-trivial guard binds was not recorded as a local, so
		// a string-valued guard was compared with C's `==` on the { ptr, len } struct
		// -- which the backend rejects -- rather than by content.
		name: "a switch guard is an ordinary variable",
		src: `func greet() string { return "hi" }

func main() {
	v := 9
	switch v := v + 1 {
	case 10:
		println("inner", v)
	}
	println("outer", v)

	δ := 3
	switch δ {
	case 3:
		println("delta", δ)
	}
	switch ε := δ * 2 {
	case 6:
		println("epsilon", ε)
	}

	switch greet() {
	case "bye":
		println("bye")
	case "hi":
		println("greeting")
	}
}
`,
		want: "inner 10\nouter 9\ndelta 3\nepsilon 6\ngreeting\n",
	},
	{
		// A switch with an init statement. The name is scoped to the whole statement
		// -- the expression switched on, the case expressions and every clause body --
		// and gone afterwards, which is what lets it shadow an outer name while its
		// own initializer still reads that outer one. The scoping is a C block wrapped
		// around the switch, the same one a guard already needed.
		//
		// The expression switched on may be anything, or nothing: it may name what the
		// init declared, name something else entirely, be computed (and so bound to a
		// temporary beside the declaration, inside the one block), or be left out, in
		// which case the switch is on true with the name in scope.
		name: "switch with an init statement",
		src: `func f() int { return 5 }

func main() {
	switch v := f(); v {
	case 4:
		println("four")
	case 5:
		println("five", v)
	default:
		println("other")
	}

	switch v := f(); {
	case v > 9:
		println("big")
	case v > 3:
		println("mid", v)
	default:
		println("small")
	}

	switch v := f(); v * 2 {
	case 10:
		println("ten", v)
	}

	w := 3
	switch v := f(); w {
	case 3:
		println("three", v)
	}

	x := 1
	switch x := x + 1; x {
	case 2:
		println("inner", x)
	}
	println("outer", x)

	for i := 0; i < 3; i++ {
		switch d := i * 2; d {
		case 0:
			println("zero")
			fallthrough
		case 2:
			println("twoish", d)
		default:
			println("rest", d)
		}
	}

	switch s := "hi"; s {
	case "bye":
		println("bye")
	case "hi":
		println("hi", len(s))
	}
}
`,
		want: "five 5\nmid 5\nten 5\nthree 5\ninner 2\nouter 1\nzero\ntwoish 0\ntwoish 2\nrest 4\nhi 2\n",
	},
	{
		// A slice is nil exactly when its backing pointer is null. Comparison
		// (`s == nil`) lowers to a pointer test; the value forms -- `s = nil`,
		// `var u []int = nil` and `return nil` from a slice-returning function -- all
		// yield the zero header {0}, not the integer 0.
		//
		// mk's non-nil arm returns a package-level slice. It used to return one whose
		// backing was its own local, which dangles the moment the frame goes -- this
		// test passed only because the caller read the header before anything reused
		// that storage. Returning such a slice is now refused outright.
		name: "slice nil comparison and value forms",
		src: `var backing = []int{5}

func mk(b bool) []int {
	if b {
		return nil
	}
	return backing
}

func main() {
	var s []int
	println(s == nil, s != nil)
	s = make([]int, 2)
	s[0] = 7
	if s != nil {
		println(s[0])
	}
	println(s == nil, s != nil)
	s = nil
	println(s == nil, len(s))
	var u []int = nil
	println(u == nil, nil == u)
	a := mk(true)
	b := mk(false)
	println(a == nil, b == nil, len(b))
}
`,
		want: "true false\n7\nfalse true\ntrue 0\ntrue true\ntrue false 1\n",
	},
	{
		// nil passed where a slice is expected. The predeclared nil alone emits the
		// null pointer 0, which is not a slice header, so the parameter's type is
		// what identifies it: at a slice parameter it becomes that slice type's zero
		// value. Covered for a plain function, a method (whose receiver takes the
		// first C argument slot, so the parameter indices must not shift), a slice
		// parameter between two scalars, and two slice parameters at once.
		name: "nil as a slice argument",
		src: `type box struct{ n int }

func (b box) size(s []int) int { return b.n + len(s) }

func size(s []int) int            { return len(s) }
func mid(a int, s []int, b int) int { return a + len(s) + b }
func both(x []int, y []int) int     { return len(x) + len(y) }

func main() {
	var b box
	println(size(nil), b.size(nil))
	println(mid(3, nil, 4), both(nil, nil))
	v := make([]int, 2)
	println(size(v), mid(1, v, 1), both(v, nil))
	if size(nil) == 0 {
		println("empty")
	}
}
`,
		want: "0 0\n7 0\n2 4 2\nempty\n",
	},
	{
		// fallthrough continues into the next clause's body without testing its
		// condition. The switch lowers to an if/else chain, so the next body is
		// emitted again at the fallthrough point, in its own C block -- each clause
		// is a scope of its own, and two may declare the same name. Covered: a
		// chain of them, a fallthrough into and out of a default written in the
		// middle (the emitter hoists a default to the trailing else, so source
		// order and emission order differ here), and same-named clause locals.
		name: "switch fallthrough",
		src: `func classify(n int) {
	switch n {
	case 0:
		println("zero")
		fallthrough
	case 1:
		println("one")
	case 2:
		println("two")
		fallthrough
	case 3:
		println("three")
		fallthrough
	default:
		println("rest")
	}
}

func scoped(n int) {
	switch n {
	case 0:
		v := 10
		println(v)
		fallthrough
	default:
		v := 20
		println(v)
		fallthrough
	case 1:
		v := 30
		println(v)
	}
}

func main() {
	classify(0)
	classify(2)
	classify(9)
	scoped(0)
}
`,
		want: "zero\none\ntwo\nthree\nrest\nrest\n10\n20\n30\n",
	},
	{
		// Package-level lookup tables: an array or slice literal initializing a
		// package variable, with the type written or inferred. Each becomes a
		// file-scope static -- for a slice, a static backing array plus a header
		// over it, which is a valid C static initializer (an address constant), so
		// none of these needs a run-time init step.
		name: "package-level table literals",
		src: `type point struct {
	x int
	y int
}

var sizes [4]int = [4]int{1, 2, 4, 8}
var masks = [3]uint8{0x0f, 0xf0, 0xff}
var names []string = []string{"tx", "rx"}
var primes = []int{2, 3, 5, 7}
var sparse [5]int = [5]int{0: 100, 4: 900}
var corners = []point{{1, 2}, {3, 4}}
var empty = []int{}

func main() {
	println(sizes[0], sizes[3], len(sizes))
	println(masks[0], masks[2])
	println(names[0], names[1], len(names))
	println(primes[3], len(primes), cap(primes))
	println(sparse[0], sparse[1], sparse[4])
	println(corners[1].x, corners[1].y, len(corners))
	println(len(empty))
	primes[0] = 11
	println(primes[0])
}
`,
		want: "1 8 4\n15 255\ntx rx 2\n7 4 4\n100 0 900\n3 4 2\n0\n11\n",
	},
	{
		// A multi-dimensional array literal. C spells a nested array the same way,
		// so each element of a rank > 1 array is a braced list of that row's
		// values -- which is what an element having no C value type of its own
		// forces: the emission descends the extents rather than naming an element
		// type. Covered: rank 2 and 3, local and package scope, a row shorter than
		// its extent and an outer index that skips a whole row (both zero-filled),
		// a row written with its own type, and a write through both indices.
		name: "multi-dimensional array literals",
		src: `var grid = [2][3]int{{1, 2, 3}, {4, 5, 6}}
var lut [2][2]uint8 = [2][2]uint8{{10, 20}, {30, 40}}

func main() {
	println(grid[0][0], grid[1][2])
	println(lut[0][1], lut[1][0])

	m := [3][3]int{{1}, {4, 5}}
	println(m[0][0], m[0][2], m[1][1], m[2][2])

	cube := [2][2][2]int{{{1, 2}, {3, 4}}, {{5, 6}, {7, 8}}}
	println(cube[0][0][0], cube[1][0][1], cube[1][1][1])

	sparse := [3][2]int{0: {1, 2}, 2: {5, 6}}
	println(sparse[0][1], sparse[1][0], sparse[2][0])

	typed := [2][2]int{[2]int{7, 8}, {9, 0}}
	typed[1][0] = 99
	println(typed[0][1], typed[1][0])
}
`,
		want: "1 6\n20 30\n1 0 5 0\n1 6 8\n2 0 5\n8 99\n",
	},
	{
		// The bit-clear operator "a &^ b" -- AND NOT -- which a program on this
		// target reaches for whenever it clears bits in a register. C has no such
		// operator, so it lowers to "a & ~(b)", the operand parenthesised because
		// "~" binds tighter than anything an expression operand may contain.
		// Exercised against the unary "^" it used to be mistaken for, at both
		// precedence neighbours, and in a constant expression.
		name: "and-not operator",
		src: `const mask = 0xff &^ 0x0f

func main() {
	x := 0xff
	y := 0x0f
	println(x &^ y)
	println(x &^ (y + 1))
	println(255 &^ 15 &^ 32)
	println(1+12&^4, 3*12&^4)
	println(mask)

	// The unary complement is a different operator and still means what it did.
	println(x & ^y)

	var n int64 = 0xffff
	println(n &^ 0x00ff)

	x &^= 0x0f
	println(x)
}
`,
		want: "240\n239\n208\n9 32\n240\n240\n65280\n240\n",
	},
	{
		// Slicing a row of a multi-dimensional array, "m[i][:]". The row decays to
		// a pointer to its first element and its extent is both the length and the
		// capacity, so the header aliases the array's own storage -- a write
		// through the slice is a write to the array, which the case checks. The
		// row index keeps its bounds check. Slicing the array itself is refused:
		// that would be a slice of arrays, whose element C cannot name here.
		name: "slicing a row of a multi-dimensional array",
		src: `var grid = [2][3]int{{1, 2, 3}, {4, 5, 6}}

func total(s []int) int {
	n := 0
	for _, v := range s {
		n = n + v
	}
	return n
}

func main() {
	r := grid[1][:]
	println(len(r), cap(r), r[0], r[2])
	println(total(grid[0][:]), total(grid[1][:]))

	m := [2][3]int{{1, 2, 3}, {4, 5, 6}}
	sub := m[0][1:3]
	println(len(sub), sub[0], sub[1])

	// The slice aliases the array, so this write is visible through both.
	row := m[0][:]
	row[1] = 99
	println(m[0][1], row[1])

	i := 1
	row2 := m[i][:]
	println(len(row2), row2[0])

	cube := [2][2][2]int{{{1, 2}, {3, 4}}, {{5, 6}, {7, 8}}}
	println(total(cube[1][0][:]))
}
`,
		want: "3 3 4 6\n6 15\n2 2 3\n99 99\n3 4\n11\n",
	},
	{
		// The P2 hardware locks, through the p2 package. Two cogs increment one
		// counter 100 times each; without the lock the read-modify-write would
		// interleave and lose updates. The hardware offers no blocking acquire, so
		// waiting is a spin on TryLock -- which is why TryLock types as bool.
		// Completion is signalled over a channel rather than by polling the
		// counter: a channel's cell is volatile, an ordinary global is not.
		name: "p2 hardware locks",
		src: `import "p2"

var lock int
var counter int
var finished chan int

func bump() {
	for i := 0; i < 100; i++ {
		for !p2.TryLock(lock) {
		}
		counter = counter + 1
		p2.Unlock(lock)
	}
}

func worker() {
	bump()
	finished <- 1
}

func main() {
	lock = p2.NewLock()
	if lock < 0 {
		panic("out of hardware locks")
	}
	go worker()
	bump()
	<-finished
	println(counter)
	p2.FreeLock(lock)
}
`,
		want: "200\n",
	},
	{
		// A package-level name that matches a field of the cog-pool runtime struct
		// used to break the build: the target's C compiler treats a struct as a
		// class and resolved "ogo_cog_pool[i].done" against the file-scope "done"
		// instead of the member, reporting "unknown identifier done in class
		// __anon_...". "done" is about the commonest name there is for a
		// completion channel, so every one of those fields is now ogo_-prefixed.
		// The names below are exactly that set.
		name: "package names matching runtime struct fields",
		src: `var done chan int
var used int
var cog int
var slot int
var args int
var stack int

func worker() {
	used, cog, slot, args, stack = 1, 2, 3, 4, 5
	done <- 1
}

func main() {
	go worker()
	<-done
	println(used, cog, slot, args, stack)
}
`,
		want: "1 2 3 4 5\n",
	},
	{
		// A bare receive statement, "<-ch": the value is discarded but the receive
		// still happens, which on a rendezvous channel is how a program waits for a
		// goroutine. Until this worked the wait had to be spelled "_ = <-ch", or a
		// value bound and ignored.
		name: "bare receive statement",
		src: `var step chan int
var done chan int

func worker() {
	step <- 1
	step <- 2
	done <- 1
}

func main() {
	go worker()
	for i := 0; i < 2; i++ {
		<-step
		println("step")
	}
	if 1 < 2 {
		<-done
	}
	println("finished")
}
`,
		want: "step\nstep\nfinished\n",
	},
	{
		// A mixed short declaration: "a, b := f()" where a is already declared in
		// this scope assigns to it and declares only b, as Go has it. The emitter
		// used to declare every target, so the C had two declarations of a in one
		// block -- which the host compiler rejects outright and the target's accepts
		// with a warning, then ignores, leaving a holding its old value. The last
		// two cases are the ones that keep the fix honest: a ":=" in an inner block
		// *does* introduce a new variable even though the name exists outside, and
		// the emitter cannot tell the two apart on its own.
		name: "mixed short variable declaration",
		src: `func two() (int, int) { return 10, 20 }

func main() {
	a := 99
	a, b := two()
	println(a, b)

	c := 1
	c, d := 5, 6
	println(c, d)

	s := make([]int, 0, 2)
	s, ok := append(s, 7)
	println(len(s), ok, s[0])

	outer := 99
	{
		outer, inner := two()
		println(outer, inner)
	}
	println(outer)
}
`,
		want: "10 20\n5 6\n1 1 7\n10 20\n99\n",
	},
	{
		// Array equality. C would accept "a == b" and mean something else entirely:
		// both operands decay to pointers, so it asks whether they are the same
		// array, which for two distinct ones is always false. That compiled without
		// a murmur from either compiler and quietly answered false, so this compares
		// element by element through a per-type helper, the way struct equality
		// does. The helper takes pointers rather than values, which is also what
		// keeps it clear of the by-value limit that stops a struct with an array
		// field being compared.
		name: "array equality",
		src: `type pt struct {
	x int
	y int
}

var g1 = [2]int{5, 6}
var g2 = [2]int{5, 6}

func main() {
	a := [3]int{1, 2, 3}
	b := [3]int{1, 2, 3}
	c := [3]int{1, 2, 4}
	println(a == b, a == c, a != b, a != c)

	println(g1 == g2)

	s := [2]string{"x", "yy"}
	t := [2]string{"x", "yy"}
	u := [2]string{"x", "zz"}
	println(s == t, s == u)

	p := [2]pt{{1, 2}, {3, 4}}
	q := [2]pt{{1, 2}, {3, 4}}
	r := [2]pt{{1, 2}, {3, 9}}
	println(p == q, p == r)

	m := [2][2]int{{1, 2}, {3, 4}}
	n := [2][2]int{{1, 2}, {3, 4}}
	o := [2][2]int{{1, 2}, {3, 9}}
	println(m == n, m == o)

	if a == b && g1 == g2 {
		println("chained")
	}
}
`,
		want: "true false false true\ntrue\ntrue false\ntrue false\ntrue false\nchained\n",
	},
	{
		// A deferred call in main capturing an argument. Arguments are captured
		// where the defer is written, as Go does, into a temporary declared at
		// function scope -- it has to outlive the block the defer sits in. main was
		// the one function that never declared those temporaries, so any deferred
		// call with a non-literal argument failed to build there. Every case below
		// is in main deliberately; the same shapes in an ordinary function already
		// worked, which is what made the gap easy to miss.
		name: "defer with captured arguments in main",
		src: `func show(n int) { println(n) }

func two(a int, b int) { println(a, b) }

func main() {
	a := 1
	b := 2
	defer show(a)
	defer two(a, b)
	if a > 0 {
		defer show(b)
	}
	if a > 100 {
		defer show(9999) // never armed
	}
	a, b = 8, 9
	println(a, b)
}
`,
		want: "8 9\n2\n1 2\n1\n",
	},
	{
		// An "if" with an init statement. The variable is scoped to the whole
		// statement -- the condition, the "then" block and every "else" branch --
		// and gone afterwards, which is what lets it shadow an outer name of the
		// same type without disturbing it. That scoping is a C block wrapped around
		// the if.
		name: "if with an init statement",
		src: `func f() int { return 5 }

func main() {
	if v := f(); v > 10 {
		println("big", v)
	} else if v > 3 {
		println("mid", v)
	} else {
		println("small", v)
	}

	v := 1
	if v := f(); v > 0 {
		println("inner", v)
	}
	println("outer", v)

	for i := 0; i < 3; i++ {
		if d := i * 2; d > 1 {
			println(d)
		}
	}

	if a := 1; a > 0 {
		if b := a + 1; b > 1 {
			println(a, b)
		}
	}
}
`,
		want: "mid 5\ninner 5\nouter 1\n2\n4\n1 2\n",
	},
	{
		// Go evaluates a call's arguments left to right. C leaves the order
		// unspecified and the two compilers here disagree -- the P2 backend went left
		// to right, the host's gcc right to left -- so the same program answered
		// differently depending on which built it. An argument that can change state
		// is now evaluated into a temporary in source order.
		//
		// The log variable records the order: each call folds its number into it, so
		// 123 means left to right and 321 means right to left.
		name: "call argument evaluation order",
		src: `var log int
var shared int

func t(n int) int {
	log = log*10 + n
	return n
}

func bump() int {
	shared = 9
	return 1
}

func three(a int, b int, c int) int { return a + b + c }
func two(a int, b int) int          { return a*10 + b }

func main() {
	println(three(t(1), t(2), t(3)), log)

	// A pure argument must still see what an earlier one wrote.
	shared = 0
	println(two(bump(), shared))

	// println's own arguments are ordered too.
	log = 0
	println(t(7), log)

	// len and a conversion are calls in shape only, so they change nothing and
	// leave the packed single-printf form alone.
	a := [3]int{4, 5, 6}
	var u uint8 = 7
	println(a[0], len(a), int(u))
}
`,
		want: "6 123\n19\n7 7\n4 3 7\n",
	},
	{
		// Reading a field off a call's struct result, `mk().y`. This was refused
		// rather than emitted, because the target's C compiler miscompiles a field
		// read at a nonzero offset directly off a function's struct return value --
		// the return temporary is not materialised before the offset is applied, and
		// the read yields garbage. Binding the result to a temporary first makes it
		// an ordinary variable, which reads correctly; the temporary is declared
		// before the statement. A method call on the same result always worked,
		// since it passes the whole struct, and still does.
		name: "field of a call result",
		src: `type inner struct{ v int }

type rec struct {
	x int
	y int
	i inner
}

func mk() rec             { return rec{1, 2, inner{9}} }
func at(a int, b int) rec { return rec{a, b, inner{0}} }

func (r rec) sum() int { return r.x + r.y }

type box struct{ d []int }

var gd = []int{7, 8}
var gb box

func pick() box            { return gb }
func (b box) get() []int   { return b.d }

func main() {
	println(mk().x, mk().y)
	println(mk().i.v)
	println(at(3, 4).y)
	println(mk().x + mk().y*2)
	q := mk().y
	println(q)
	println(mk().sum())
	if mk().y > 1 {
		println("yes")
	}

	// Indexing a call result needs the same temporary, and for a slice result it
	// is also what gives the bounds check a base to form its ".len" from.
	gb.d = gd
	println(pick().d[1])
	println(gb.get()[0], gb.get()[1])
}
`,
		want: "1 2\n9\n4\n5\n2\n3\nyes\n8\n7 8\n",
	},
	{
		// A locally declared channel, used across cogs, from a function called
		// repeatedly. Its cell is a file-scope static -- one per declaration site,
		// its lock taken once at package init -- rather than a local of the
		// declaring frame. Both halves of that mattered: the cell used to live on
		// spawn's stack, so `go worker(ch)` handed another cog a pointer into a frame
		// spawn was free to leave, and the lock was re-acquired on every call and
		// never released, so the sixteenth call ran the P2 out of locks.
		name: "local channel across cogs, called repeatedly",
		src: `func worker(ch chan int, n int) { ch <- n }

func spawn(n int) int {
	var ch chan int
	go worker(ch, n)
	return <-ch
}

func decl(n int) int {
	var unused chan int
	if n < 0 {
		<-unused
	}
	return n
}

func main() {
	// Past the seven pool slots, so this leans on slot reuse as well.
	sum := 0
	for i := 0; i < 20; i++ {
		sum = sum + spawn(i)
	}
	println(sum)

	// A second site, exercised well past the lock budget. This one starts no cogs,
	// so it is the leak that is under test here, not the pool.
	total := 0
	for i := 0; i < 20; i++ {
		total = total + decl(i)
	}
	println(total)
}
`,
		want: "190\n190\n",
	},
	{
		// A goroutine's slot is reused once it finishes, so the seven-cog ceiling
		// bounds how many run at once and not how many a program may start. Both
		// halves are load-bearing: a run of 20 sequential spawns, each joined before
		// the next, and then full-pool batches that hand out all seven at a time and
		// take them all back.
		//
		// This only ever failed on hardware. A goroutine that has just handed main
		// its value is a few instructions short of stopping, and ogo_cog_claim used
		// to give up on a slot whose cog still read live rather than wait for it --
		// so the eighth spawn of a program panicked "out of cogs". The host shim's
		// pthread wins that race and clears the flag in time, which is why the board
		// suite is the one that caught this and is the one that guards it.
		name: "goroutine slots are reused",
		src: `func worker(ch chan int, n int) { ch <- n }

func batch(ch chan int, n int) int {
	go worker(ch, n)
	go worker(ch, n)
	go worker(ch, n)
	go worker(ch, n)
	go worker(ch, n)
	go worker(ch, n)
	go worker(ch, n)
	sum := 0
	for i := 0; i < 7; i++ {
		sum = sum + <-ch
	}
	return sum
}

func main() {
	var ch chan int
	sum := 0
	for i := 1; i <= 20; i++ {
		go worker(ch, i)
		sum = sum + <-ch
	}
	println(sum)

	total := 0
	for i := 1; i <= 5; i++ {
		total = total + batch(ch, i)
	}
	println(total)
}
`,
		want: "210\n105\n",
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
		// append with several values -- append(s, a, b, c) -- appends each in turn
		// (the emitter nests the per-element ogo_append_<T> calls). Exercised with an
		// int slice and a string slice, mixed with a single-value append.
		name: "multi-value append",
		src: `func main() {
	s := make([]int, 0, 5)
	s = append(s, 1, 2, 3)
	s = append(s, 4)
	println(len(s), s[0], s[1], s[2], s[3])
	t := make([]string, 0, 3)
	t = append(t, "a", "b", "c")
	println(len(t), t[0], t[2])
}
`,
		want: "4 1 2 3 4\n3 a c\n",
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
		// A break in a communication clause leaves the select, as it does in Go.
		// Both select lowerings are C loop constructs, so a plain C break is that
		// jump -- but the switch context has to be cleared around them, or a select
		// written inside a switch case would emit that switch's end-label goto and
		// leave the switch as well. The second select here is the one that catches
		// it: "in case" must still print.
		name: "break inside a select",
		src: `func worker(ch chan int) {
	ch <- 1
	ch <- 2
}

func main() {
	var ch chan int
	go worker(ch)

	for i := 0; i < 2; i++ {
		select {
		case v := <-ch:
			println(v)
			break
		}
		println("after select")
	}

	n := 1
	switch n {
	case 1:
		select {
		case x := <-ch:
			println(x)
		default:
			println("empty")
			break
		}
		println("in case")
	}
	println("done")
}
`,
		want: "1\nafter select\n2\nafter select\nempty\nin case\ndone\n",
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
		// Sorting and searching a table in place. Recursion had no test at all until
		// this one, which matters most on the target: a cog's stack is a fixed 256
		// longs in its pool slot, so a recursive call chain is bounded by something
		// the program cannot see. A quicksort over seven rows and fib(15) stay well
		// inside it, and the board run is what says so.
		name: "recursive quicksort and binary search",
		src: `type row struct {
	key  int
	name string
}

var tbl [7]row

func less(a row, b row) bool { return a.key < b.key }

func swap(rs []row, i int, j int) {
	t := rs[i]
	rs[i] = rs[j]
	rs[j] = t
}

func partition(rs []row, lo int, hi int) int {
	pivot := rs[hi]
	i := lo
	for j := lo; j < hi; j++ {
		if less(rs[j], pivot) {
			swap(rs, i, j)
			i++
		}
	}
	swap(rs, i, hi)
	return i
}

func quicksort(rs []row, lo int, hi int) {
	if lo >= hi {
		return
	}
	p := partition(rs, lo, hi)
	quicksort(rs, lo, p-1)
	quicksort(rs, p+1, hi)
}

func search(rs []row, key int) (int, bool) {
	lo := 0
	hi := len(rs) - 1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case rs[mid].key == key:
			return mid, true
		case rs[mid].key < key:
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}
	return 0, false
}

func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() {
	tbl[0] = row{5, "e"}
	tbl[1] = row{3, "c"}
	tbl[2] = row{9, "i"}
	tbl[3] = row{1, "a"}
	tbl[4] = row{7, "g"}
	tbl[5] = row{2, "b"}
	tbl[6] = row{8, "h"}
	rs := tbl[:]
	quicksort(rs, 0, len(rs)-1)
	for i := 0; i < len(rs); i++ {
		println(rs[i].key, rs[i].name)
	}
	i, ok := search(rs, 7)
	println(i, ok)
	j, missing := search(rs, 4)
	println(j, missing)
	println(fib(15))
}
`,
		want: "1 a\n2 b\n3 c\n5 e\n7 g\n8 h\n9 i\n4 true\n0 false\n610\n",
	},
	{
		// A bit-banged SPI transmitter and a moving average over a ring of readings:
		// the pin intrinsics driving a protocol, which is what the p2 package is for
		// and what nothing but a board can really judge.
		name: "a bit-banged SPI driver",
		src: `import "p2"

type spi struct {
	clk  int
	mosi int
	cs   int
}

func (s spi) begin() { p2.PinLow(s.cs) }

func (s spi) end() { p2.PinHigh(s.cs) }

func (s spi) writeByte(b byte) {
	for i := 7; i >= 0; i-- {
		if b&(1<<uint(i)) != 0 {
			p2.PinHigh(s.mosi)
		} else {
			p2.PinLow(s.mosi)
		}
		p2.PinHigh(s.clk)
		p2.WaitCycles(1)
		p2.PinLow(s.clk)
	}
}

func (s spi) write(data []byte) int {
	s.begin()
	for i := 0; i < len(data); i++ {
		s.writeByte(data[i])
	}
	s.end()
	return len(data)
}

type avg struct {
	ring  []int
	head  int
	count int
	total int
}

func (a *avg) add(v int) int {
	if a.count == len(a.ring) {
		a.total -= a.ring[a.head]
	} else {
		a.count++
	}
	a.total += v
	a.ring[a.head] = v
	a.head = (a.head + 1) % len(a.ring)
	return a.total / a.count
}

var samples [4]int
var payload [3]byte

func main() {
	bus := spi{0, 1, 2}
	payload[0] = 0xA5
	payload[1] = 0x5A
	payload[2] = 0xFF
	println(bus.write(payload[:]))

	var mean avg = avg{samples[:], 0, 0, 0}
	println(mean.add(10), mean.add(20), mean.add(30))
	println(mean.add(40), mean.add(50), mean.add(60))
}
`,
		want: "3\n10 15 20\n25 35 45\n",
	},
	{
		// A CRC over a byte slice and a little fixed-point arithmetic: the table, bit
		// and unsigned work a protocol or a sensor driver is made of, and a defined
		// type over int32 carrying the arithmetic as methods.
		name: "a CRC table and fixed-point arithmetic",
		src: `const poly uint16 = 0xA001

var table [256]uint16
var built bool

func buildTable() {
	for i := 0; i < 256; i++ {
		var c uint16 = uint16(i)
		for b := 0; b < 8; b++ {
			if c&1 != 0 {
				c = (c >> 1) ^ poly
			} else {
				c = c >> 1
			}
		}
		table[i] = c
	}
	built = true
}

func crc16(data []byte) uint16 {
	if !built {
		buildTable()
	}
	var c uint16 = 0xFFFF
	for i := 0; i < len(data); i++ {
		c = (c >> 8) ^ table[(c^uint16(data[i]))&0xFF]
	}
	return c
}

type fixed int32

func fromInt(n int) fixed          { return fixed(n << 8) }
func (f fixed) mul(g fixed) fixed  { return fixed((int32(f) * int32(g)) >> 8) }
func (f fixed) whole() int         { return int(int32(f) >> 8) }
func (f fixed) frac() int          { return int(int32(f) & 0xFF) }

var msg [5]byte

func main() {
	msg[0] = '1'
	msg[1] = '2'
	msg[2] = '3'
	msg[3] = '4'
	msg[4] = '5'
	println(crc16(msg[:]))
	println(crc16(msg[:1]), crc16(msg[:0]))

	a := fromInt(3)
	b := fromInt(2)
	c := a.mul(b)
	println(c.whole(), c.frac())
	d := a.mul(fixed(128))
	println(d.whole(), d.frac())
}
`,
		want: "42097\n38014 65535\n6 0\n1 128\n",
	},
	{
		// Formatting into a caller-owned buffer with the predeclared Builder, which
		// is how a program without a heap builds a line of output. A *Builder handed
		// to a helper is the shape that makes it useful.
		name: "formatting through a Builder parameter",
		src: `var digits [12]byte

func itoa(sb *Builder, n int) {
	if n == 0 {
		sb.WriteByte('0')
		return
	}
	neg := n < 0
	if neg {
		n = -n
	}
	i := 0
	for n > 0 {
		digits[i] = byte('0' + n%10)
		n = n / 10
		i++
	}
	if neg {
		sb.WriteByte('-')
	}
	for i > 0 {
		i--
		sb.WriteByte(digits[i])
	}
}

var back [64]byte

func main() {
	sb := NewBuilder(back[:])
	sb.WriteString("t=")
	itoa(&sb, 1234)
	sb.WriteString("ms rc=")
	itoa(&sb, -7)
	println(sb.String())
	sb.Reset()
	itoa(&sb, 0)
	sb.WriteRune('!')
	println(sb.String())
}
`,
		want: "t=1234ms rc=-7\n0!\n",
	},
	{
		// The P2's own facilities, which nothing else here exercises: a hardware lock
		// held across three cogs contending for one counter, and the millisecond
		// clock a driver paces itself by. There is no Go to compare against for
		// these, so what the case asserts is what the hardware guarantees -- that
		// every increment lands under the lock, and that waiting moves the clock.
		//
		// It is also what made the host shim's waits and clocks real: they used to
		// return at once and to read CPU time, so a program that paces itself said
		// one thing here and another on the board.
		name: "hardware locks and the millisecond clock",
		src: `import "p2"

type guard struct {
	id int
}

func (g guard) acquire() {
	for !p2.TryLock(g.id) {
		p2.WaitCycles(1)
	}
}

func (g guard) release() { p2.Unlock(g.id) }

var lk guard
var shared int
var done chan int

func bump(n int) {
	for i := 0; i < n; i++ {
		lk.acquire()
		shared = shared + 1
		lk.release()
	}
	done <- 1
}

func main() {
	lk = guard{p2.NewLock()}
	if lk.id < 0 {
		println("no lock")
		return
	}
	start := p2.GetMs()
	go bump(50)
	go bump(50)
	go bump(50)
	for i := 0; i < 3; i++ {
		<-done
	}
	println(shared)
	p2.WaitMs(2)
	println(p2.GetMs()-start >= 2)
	p2.FreeLock(lk.id)
}
`,
		want: "150\ntrue\n",
	},
	{
		// The address of an element of PACKAGE storage outlives every frame, so it is
		// handed out freely -- which is the other side of the refusal a local array's
		// element now gets, and what the refusal's message points the writer at.
		name: "the address of package storage",
		src: `type P struct{ n int }

var arr [3]int
var g P
var pool [2]P

func fromPackageArray() *int { return &arr[1] }

func fromPackageStruct() *P { return &g }

func fromPackagePool(i int) *P { return &pool[i] }

func viaPointer(p *P) *int { return &p.n }

func main() {
	q := fromPackageArray()
	*q = 5
	println(arr[1])
	r := fromPackageStruct()
	r.n = 7
	println(g.n)
	s := fromPackagePool(1)
	s.n = 9
	println(pool[1].n)
	t := viaPointer(&g)
	*t = 11
	println(g.n)
}
`,
		want: "5\n7\n9\n11\n",
	},
	{
		// A linked structure over a fixed node pool, which is how one is built with
		// no heap. It needs "return &p.nodes[i]" from a POINTER receiver, which the
		// escape rules refused: the receiver was declared with no type at all, so it
		// read as an inline value whose address does not outlive the frame. Through a
		// pointer it reaches what the pointer points at, which is the caller's.
		name: "a linked list over a node pool",
		src: `type node struct {
	value int
	next  *node
	used  bool
}

type pool struct {
	nodes []node
	head  *node
}

func (p *pool) alloc(v int) *node {
	for i := 0; i < len(p.nodes); i++ {
		if !p.nodes[i].used {
			p.nodes[i].used = true
			p.nodes[i].value = v
			p.nodes[i].next = nil
			return &p.nodes[i]
		}
	}
	return nil
}

func (p *pool) push(v int) bool {
	n := p.alloc(v)
	if n == nil {
		return false
	}
	n.next = p.head
	p.head = n
	return true
}

func (p *pool) sum() int {
	t := 0
	for n := p.head; n != nil; n = n.next {
		t += n.value
	}
	return t
}

func (p *pool) length() int {
	k := 0
	for n := p.head; n != nil; n = n.next {
		k++
	}
	return k
}

var storage [4]node

func main() {
	var p pool = pool{storage[:], nil}
	for i := 1; i <= 5; i++ {
		if !p.push(i) {
			println("pool full at", i)
			break
		}
	}
	println(p.length(), p.sum())
	for n := p.head; n != nil; n = n.next {
		println(n.value)
	}
}
`,
		want: "pool full at 5\n4 10\n4\n3\n2\n1\n",
	},
	{
		// A state machine driven by two channels, which is what a controller is: a
		// defined type over int with iota constants and a method of its own, a struct
		// sent to another cog, and a select loop that runs until one of the inputs
		// says to stop.
		name: "a state machine over select",
		src: `type state int

const (
	idle state = iota
	running
	stopped
)

type cmd struct {
	op  int
	arg int
}

var cmds chan cmd
var ticks chan int

func (s state) name() string {
	switch s {
	case idle:
		return "idle"
	case running:
		return "running"
	}
	return "stopped"
}

func driver() {
	cmds <- cmd{1, 7}
	ticks <- 1
	ticks <- 2
	cmds <- cmd{2, 0}
	ticks <- 3
	cmds <- cmd{3, 0}
}

func step(s state, c cmd) (state, string) {
	switch {
	case c.op == 1 && s == idle:
		return running, "start"
	case c.op == 2 && s == running:
		return idle, "pause"
	case c.op == 3:
		return stopped, "halt"
	}
	return s, "ignored"
}

func main() {
	go driver()
	s := idle
	count := 0
	for s != stopped {
		select {
		case c := <-cmds:
			next, what := step(s, c)
			println(what, s.name(), "->", next.name())
			s = next
		case n := <-ticks:
			count += n
			println("tick", n, s.name())
		}
	}
	println("done", count, s.name())
}
`,
		want: "start idle -> running\ntick 1 running\ntick 2 running\npause running -> idle\n" +
			"tick 3 idle\nhalt idle -> stopped\ndone 6 stopped\n",
	},
	{
		// A string held in a struct field slices like one held in a variable. It did
		// not: the slice paths had an answer for a slice field and an array field and
		// none for a string one, so "l.line[a:b]" -- the whole of a tokenizer -- was
		// "cannot infer a type".
		//
		// Top-level names that C has already spoken for move out of its way. This
		// program has a function called atoi, one called abs, a package variable
		// called index and a type called union, every one of which is declared by a
		// header the output includes or is a C keyword; before, each was a C compile
		// error naming a collision the program never made.
		name: "a string field slices, and C's names are avoided",
		src: `type union struct{ n int }

type lexer struct {
	line string
	toks []int
	n    int
}

func (l *lexer) split() int {
	i := 0
	l.n = 0
	for i < len(l.line) {
		for i < len(l.line) && l.line[i] == ' ' {
			i++
		}
		if i == len(l.line) {
			break
		}
		start := i
		for i < len(l.line) && l.line[i] != ' ' {
			i++
		}
		if l.n == len(l.toks) {
			return l.n
		}
		l.toks[l.n] = start*100 + i
		l.n++
	}
	return l.n
}

func (l *lexer) text(k int) string {
	t := l.toks[k]
	return l.line[t/100 : t%100]
}

func atoi(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

var tokbuf [4]int
var index int = 3

func main() {
	var l lexer = lexer{"set pin 17", tokbuf[:], 0}
	n := l.split()
	println(n, l.text(0), l.text(1), l.text(2))
	v, ok := atoi(l.text(2))
	println(v, ok)
	w, bad := atoi("12x")
	println(w, bad)
	println(abs(-5), index)
	var u union = union{7}
	println(u.n, l.line[:3], l.line[4:])
}
`,
		want: "3 set pin 17\n17 true\n0 false\n5 3\n7 set pin 17\n",
	},
	{
		// A multi-result call on the right of a destructuring assignment could only
		// be a plain function of this package. A METHOD returning "(value, ok)" is
		// the shape a container wants -- a ring buffer's pop, a lookup -- and it was
		// refused, as was a multi-result call into an imported package. Both are the
		// same two-step callee: a Selector followed by the CallSuffix.
		name: "a method or package call yields several values",
		src: `type ring struct {
	buf   []byte
	head  int
	tail  int
	count int
}

func (r *ring) push(b byte) bool {
	if r.count == len(r.buf) {
		return false
	}
	r.buf[r.tail] = b
	r.tail = (r.tail + 1) % len(r.buf)
	r.count++
	return true
}

func (r *ring) pop() (byte, bool) {
	if r.count == 0 {
		return 0, false
	}
	b := r.buf[r.head]
	r.head = (r.head + 1) % len(r.buf)
	r.count--
	return b, true
}

type T struct{ n int }

func (t T) pair() (int, int)   { return t.n, t.n * 2 }
func (t *T) bump() (int, bool) { t.n++; return t.n, true }

var storage [4]byte

func main() {
	var r ring = ring{storage[:], 0, 0, 0}
	for i := 0; i < 6; i++ {
		if !r.push(byte('a' + i)) {
			println("full at", i)
			break
		}
	}
	for {
		b, ok := r.pop()
		if !ok {
			break
		}
		println(int(b))
	}

	var t T = T{5}
	c, d := t.pair()
	e, ok := t.bump()
	println(c, d, e, ok, t.n)
}
`,
		want: "full at 4\n97\n98\n99\n100\n5 10 6 true 6\n",
	},
	{
		// A const spec binds a list, "const a, b = 1, 2". A spec that omits its
		// expression list repeats the previous spec's positionally, and iota counts
		// SPECS rather than names, so every name on one line sees the same value --
		// which is what makes "h, i = iota, iota * 10" mean what it says.
		name: "const identifier lists",
		src: `const a, b = 1, 2
const s, t = "x", "y"

const (
	c, d = 3, 4
	e, f
	g    = 9
	h, i = iota, iota * 10
	j, k
)

func main() {
	println(a, b, s, t)
	println(c, d, e, f, g)
	println(h, i, j, k)
	const p, q = 5, 6
	println(p, q)
	var arr [b]int
	println(len(arr), arr[0])
}
`,
		want: "1 2 x y\n3 4 3 4 9\n3 30 4 40\n5 6\n2 0\n",
	},
	{
		// A defined type over a channel is a channel: a send, a receive and a select
		// clause all reach it, through a chain of definitions if there is one. It was
		// the one kind left out when a defined type gained the behaviour of what it
		// is defined over -- chanElem keyed on the written "chan T" and found a name
		// instead, so every send on one was "cannot send to non-channel".
		name: "a defined type over a channel",
		src: `type Ch chan int
type Sig chan bool
type Alias Ch

var gch Ch
var sig Sig
var ali Alias

func send(c Ch, n int) { c <- n }

func flag(c Sig) { c <- true }

func viaAlias(c Alias) { c <- 5 }

func main() {
	go send(gch, 7)
	println(<-gch)

	go flag(sig)
	println(<-sig)

	go viaAlias(ali)
	println(<-ali)

	go send(gch, 3)
	select {
	case x := <-gch:
		println("sel", x)
	}
	select {
	case x := <-gch:
		println("sel2", x)
	default:
		println("none")
	}
}
`,
		want: "7\ntrue\n5\nsel 3\nnone\n",
	},
	{
		// A select whose send clause and receive clause both belong to a loop that
		// keeps going until each has fired its share, with the other cog doing the
		// mirror image. The send clause offers a value and waits for it to be taken,
		// so the two sides have to make progress against each other; a select that
		// only ever receives never exercises that.
		name: "a select that both sends and receives",
		src: `var out chan int
var in chan int
var quit chan int

func consumer() {
	for i := 0; i < 4; i++ {
		v := <-out
		in <- v + 1
	}
	quit <- 1
}

func main() {
	go consumer()
	sent := 0
	got := 0
	sum := 0
	for sent < 4 || got < 4 {
		select {
		case out <- sent:
			sent++
		case v := <-in:
			sum += v
			got++
		}
	}
	println(sent, got, sum, <-quit)
}
`,
		want: "4 4 10 1\n",
	},
	{
		// Channels under contention: two consumers drawing from one producer, and a
		// select over two channels fed by two more cogs at once. Every other channel
		// case has one sender and one receiver, so nothing pinned what happens when
		// several cogs reach the same rendezvous together -- which on this target is
		// a hardware lock and a spin, with no scheduler to arbitrate.
		//
		// The totals are order-independent on purpose: which cog wins a rendezvous,
		// and which select case fires when both are ready, are not specified. What
		// is specified is that every value is delivered exactly once.
		name: "channels under contention",
		src: `var work chan int
var done chan int
var a chan int
var b chan int

func worker() {
	for i := 0; i < 3; i++ {
		v := <-work
		done <- v * 2
	}
}

func feed() {
	for i := 1; i <= 6; i++ {
		work <- i
	}
}

func feedA() {
	for i := 0; i < 3; i++ {
		a <- i
	}
}

func feedB() {
	for i := 0; i < 3; i++ {
		b <- 100 + i
	}
}

func main() {
	go worker()
	go worker()
	go feed()
	total := 0
	for i := 0; i < 6; i++ {
		total += <-done
	}
	println("pool", total)

	go feedA()
	go feedB()
	sum := 0
	count := 0
	for count < 6 {
		select {
		case v := <-a:
			sum += v
			count++
		case v := <-b:
			sum += v
			count++
		}
	}
	println("select", sum, count)
}
`,
		want: "pool 42\nselect 306 6\n",
	},
	{
		// The emitter has no scopes of its own: it records a variable's type,
		// extents and provenance in maps keyed by SOURCE name. A declaration inside
		// a block, or in a statement's header, therefore outlived it -- after
		// `{ s := 5 }` shadowing a package-level string, s was still recorded as an
		// int, and the next read of the real s printed the first word of its header
		// as a number. Every shadow here changes the type, which is what makes a
		// stale record show.
		name: "shadowing across scopes",
		src: `var s string = "pkg"
var n int = 7

func param(x int) int {
	{
		x := x * 2
		println("inner", x)
	}
	return x
}

func send(ch chan int) { ch <- 3 }

func main() {
	{
		s := 5
		println(s)
	}
	println(s)

	if s := 1; s > 0 {
		println("if", s)
	}
	println(s)

	for s := 0; s < 2; s++ {
		println("for", s)
	}
	println(s)

	switch s := 42; s {
	case 42:
		println("switch", s)
	}
	println(s)

	xs := []int{1, 2}
	for _, s := range xs {
		println("range", s)
	}
	println(s)

	var ch chan int
	go send(ch)
	select {
	case s := <-ch:
		println("select", s)
	}
	println(s)

	// The other direction, and a container shadowing a scalar.
	{
		n := "inner"
		println(n)
	}
	println(n, n+1)
	{
		n := []int{9, 9}
		println(len(n), n[0])
	}
	println(n)

	println(param(4))
}
`,
		want: "5\npkg\nif 1\npkg\nfor 0\nfor 1\npkg\nswitch 42\npkg\nrange 1\nrange 2\npkg\n" +
			"select 3\npkg\ninner\n7 8\n2 9\n7\ninner 8\n4\n",
	},
	{
		// Every literal form the scanner accepts, together: the integer bases and
		// both octal spellings, digit separators in each of them, the rune escapes,
		// and a raw string. A digit separator in a FLOAT reached the backend as
		// written -- "1_0.5" is not a C float at all, but an integer with an invalid
		// suffix -- while the integer forms had been normalized all along.
		name: "literal forms",
		src: `func main() {
	println(0b1010, 0B1010)
	println(0o17, 0O17, 017)
	println(0xff, 0XFF)
	println(1_000_000, 0b1010_1010, 0x_ff, 1_0.5)
	println(0, 00, 0x0)
	println('a', '\n', '\t', '\\', '\'', '\x41', '\101', 'é', '\U0001F600')
	s := "a\tb\nc\\d\"e\x41\101é"
	println(len(s))
	r := ` + "`" + `raw
line	tab\n` + "`" + `
	println(len(r), len(""))
}
`,
		want: "10 10\n15 15 15\n255 255\n1000000 170 255 10.5\n0 0 0\n" +
			"97 10 9 92 39 65 65 233 128512\n13\n14 0\n",
	},
	{
		// The hexadecimal form of a float literal, whose exponent is a power of two
		// and is required. C has the same syntax, so the text passes through -- but
		// only after the digit separators come out, which is what "0x_1p4" checks.
		name: "hexadecimal float literals",
		src: `const q = 0x1p-2

func main() {
	var a float64 = 0x1p-2
	var b float64 = 0x1.8p1
	var c float64 = 0X2p+3
	var d float64 = 0x_1p4
	println(a == 0.25, b == 3.0, c == 16.0, d == 16.0)
	println(a, b, c, d, q == 0.25)
	println(0x10, 0x1p0 == 1.0)
}
`,
		want: "true true true true\n0.25 3 16 16 true\n16 true\n",
	},
	{
		// The exponent form of a float literal, which the scanner did not recognize
		// at all: "1e3" was a syntax error, and one syntax error made every name in
		// the file read as undefined afterwards. The forms with an empty side, "1."
		// and ".5", come with it -- ".5" being the one that has to be told from a
		// selector's dot, which it is by what follows.
		name: "float literal exponents",
		src: `const big = 1e3
const small = 1.5e-3

type P struct {
	x float64
}

func (p P) get() float64 { return p.x }

func main() {
	var a float64 = 1e3
	var b float64 = 1.5e-3
	var c float64 = 2.5E2
	var d float64 = 1.
	var e float64 = .5
	println(a == 1000.0, b < 0.01, c == 250.0, d == 1.0, e == 0.5)
	println(a, c, d, e)
	println(big == 1000.0, small < 0.01)

	// The shapes a leading dot has to be told from.
	p := P{1.5}
	println(p.x, p.get())
	xs := []float64{.5, 1., 1e1}
	println(xs[0], xs[1], xs[2], xs[0]+.5)

	var f float32 = 1e2
	println(f == 100.0)
}
`,
		want: "true true true true true\n1000 250 1 0.5\ntrue true\n1.5 1.5\n0.5 1 10 1\ntrue\n",
	},
	{
		// Division of two integer constants is integer division, as in Go: 7 / 2 is
		// 3, not 3.5. go/constant's token.QUO is float division whatever the operands
		// are, so every such constant became a float -- which is how a perfectly
		// ordinary "[MB / KB]int" came to be an "invalid array bound".
		name: "constant integer division",
		src: `const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
	GB
)

const half = 7 / 2
const rem = 7 % 2
const exact = 7.0 / 2
const back = GB / MB / KB

func main() {
	println(KB, MB, GB)
	println(half, rem, back)
	println(exact == 3.5, exact > 3)

	var a [MB / KB]int
	a[0] = 5
	println(len(a), a[0])

	var b [half]int
	b[2] = 9
	println(len(b), b[2])
}
`,
		want: "1024 1048576 1073741824\n3 1 1\ntrue true\n1024 5\n3 9\n",
	},
	{
		// Go evaluates a return's expressions, assigns them to the results, and only
		// then runs the defers. They used to run first, so an expression reading what
		// a defer had changed saw the changed value. Binding first is also what gives
		// a named result its point: a defer may still change it, and that change is
		// what the caller sees.
		//
		// A named result of an aggregate type, and a defer's captured argument of
		// one, are zeroed with braces: C has no scalar zero for an aggregate, and
		// "= 0" there is an invalid initializer rather than a warning.
		name: "defers run after the results are bound",
		src: `func mul10(p *int) { *p = *p * 10 }

func show(tag string, v int) { println(tag, v) }

func named() (n int) {
	defer mul10(&n)
	n = 1
	return n + 1
}

func unnamed() int {
	x := 1
	defer mul10(&x)
	return x + 1
}

func two() (a int, b string) {
	defer mul10(&a)
	a = 3
	b = "hi"
	return a + 1, b
}

func naked() (n int) {
	defer mul10(&n)
	n = 7
	return
}

func literal() int {
	x := 1
	defer mul10(&x)
	return 100
}

func tagged(k int) {
	defer show("outer", k)
	if k > 0 {
		defer show("inner", k)
		show("body", k)
	}
}

func main() {
	println(named())
	println(unnamed())
	p, q := two()
	println(p, q)
	println(naked())
	println(literal())
	tagged(3)
}
`,
		want: "20\n2\n40 hi\n70\n100\nbody 3\ninner 3\nouter 3\n",
	},
	{
		// A goroutine's arguments are marshalled through a per-site block, whose
		// fields took the type of each ARGUMENT EXPRESSION rather than of the
		// parameter it is assigned to. So `go sender(1234567890123)` stored the
		// literal as the int it defaults to and the cog received 1912276171 -- a
		// silent truncation of every 64-bit goroutine argument.
		name: "64-bit values across cogs",
		src: `type pair struct {
	x int64
	y uint64
}

var ch chan int64
var uch chan uint64
var pch chan pair

func sender(v int64) { ch <- v }

func usender(v uint64) { uch <- v }

func psender(p pair) { pch <- p }

func worker(v int64, out chan int64) { out <- v * 3 }

func main() {
	go sender(1234567890123)
	println(<-ch)
	go usender(12345678901234567890)
	println(<-uch)
	go psender(pair{-987654321098, 18446744073709551615})
	p := <-pch
	println(p.x, p.y)
	go worker(1234567890123, ch)
	println(<-ch)
	select {
	case v := <-ch:
		println("recv", v)
	default:
		println("none")
	}
	go sender(-1)
	select {
	case v := <-ch:
		println("recv", v)
	}
}
`,
		want: "1234567890123\n12345678901234567890\n-987654321098 18446744073709551615\n3703703670369\nnone\nrecv -1\n",
	},
	{
		// A sweep of 64-bit arithmetic, since a flexcc miscompile of a 64-bit cast
		// was found by accident (see "the most negative value divided by minus
		// one") and only the board shows that class at all. Every value here is
		// wider than 32 bits, so a lowering that quietly works in 32 shows up.
		name: "64-bit arithmetic",
		src: `type Big int64

type rec struct {
	a int64
	b uint64
}

var gs int64 = 1234567890123
var gu uint64 = 12345678901234567890

func add(x int64, y int64) int64    { return x + y }
func mul(x uint64, y uint64) uint64 { return x * y }

func main() {
	var a int64 = 1234567890123
	var b int64 = -987654321098
	println(a+b, a-b, a*3, a/7, a%7)
	println(-a, a>>10, a<<10)

	var u uint64 = 12345678901234567890
	var v uint64 = 1234567890
	println(u+v, u-v, u/v, u%v, u>>13, u<<3)

	println(a > b, a < b, a == b, a != b, u > v)
	println(add(a, b), mul(v, v))

	var r rec = rec{a, u}
	println(r.a, r.b)
	r.a = r.a * 2
	println(r.a)

	var arr [3]int64
	arr[0] = a
	arr[1] = b
	arr[2] = arr[0] + arr[1]
	println(arr[0], arr[1], arr[2])

	var c Big = 9007199254740993
	println(int64(c), int64(c)+1)

	println(int32(a), uint32(u), int64(int32(-5)), uint64(v))
	println(gs, gu, gs*2)
	s := []int64{a, b}
	println(len(s), s[0]+s[1])
}
`,
		want: "246913569025 2222222211221 3703703670369 176366841446 1\n" +
			"-1234567890123 1205632705 1264197519485952\n" +
			"12345678902469135780 12345678900000000000 10000000001 0 1507040881498360 6531710841328785040\n" +
			"true false false true true\n" +
			"246913569025 1524157875019052100\n" +
			"1234567890123 12345678901234567890\n" +
			"2469135780246\n" +
			"1234567890123 -987654321098 246913569025\n" +
			"9007199254740993 9007199254740994\n" +
			"1912276171 3944680146 -5 1234567890\n" +
			"1234567890123 12345678901234567890 2469135780246\n" +
			"2 246913569025\n",
	},
	{
		// The other two operands C and Go disagree on. Go defines the most negative
		// value divided by -1 to be itself, with a remainder of 0 -- the quotient is
		// not representable, so the two's-complement overflow stands. C leaves it
		// undefined, and the host traps on it (SIGFPE), which is a crash where Go
		// prints a number. Guarded per signed value type, alongside the divide-by-
		// zero check the divisor already carried.
		name: "the most negative value divided by minus one",
		src: `func main() {
	var a int32 = -2147483648
	var b int32 = -1
	println(a/b, a%b)

	var c int64 = -9223372036854775808
	var d int64 = -1
	println(c/d, c%d)

	var e int32 = -2147483648
	e /= b
	println(e)
	var f int32 = -2147483648
	f %= b
	println(f)

	var g [2]int32
	g[0] = -2147483648
	i := 0
	g[i] /= b
	println(g[0])

	// A 64-bit conversion of a 64-bit expression is bound to a variable first: the
	// target's C compiler miscompiles the cast otherwise, and only the board shows
	// it.
	var p uint64 = 0
	var q uint64 = 12345678901
	println(int64(p-q), uint64(c/d))

	// Unsigned division has no such case and is untouched, and so is a constant
	// divisor that is neither zero nor -1.
	var u uint32 = 8
	var v uint32 = 3
	var w int32 = -7
	println(u/v, u%v, w/2, w%2)
}
`,
		want: "-2147483648 0\n-9223372036854775808 0\n-2147483648\n0\n-2147483648\n-12345678901 9223372036854775808\n2 2 -3 -1\n",
	},
	{
		// Go defines a shift by a count at least as wide as the value's type: the
		// result is 0, or -1 for an arithmetic right shift of a negative value. C
		// leaves it undefined and both this project's compilers take the count
		// modulo the width, so "x << 40" on an int32 was "x << 8" -- a silent wrong
		// answer. A count that is not a constant already inside the width now goes
		// through a guarded helper; one that is stays a plain C shift.
		name: "a shift by a count at or past the width",
		src: `func main() {
	var x int32 = 1
	var s uint32 = 40
	println(x<<s, x>>s)

	var y int32 = -1024
	println(y>>s, y<<s)

	var u uint32 = 0xF0000000
	println(u>>s, u<<s)

	var v int64 = 1
	var w uint32 = 70
	println(v<<w, v<<40)

	// The compound form is guarded the same way, on a plain variable and on an
	// element whose index can be named twice.
	var a int32 = 1
	a <<= s
	println(a)
	var b [2]int32
	b[0] = -1024
	i := 0
	b[i] >>= s
	println(b[0])

	// A constant count inside the width is left as written.
	var c int32 = 3
	c <<= 2
	println(c, c>>1, c<<29)
}
`,
		want: "0 0\n-1 0\n0 0\n0 1099511627776\n0\n-1\n12 6 -2147483648\n",
	},
	{
		// Go computes a constant expression in arbitrary precision and then converts;
		// C computes it in the type of its operands. Written out as C source, "1 <<
		// 40" is a shift of an int by 40 -- undefined, and 0 in practice -- so a
		// constant whose value does not fit a C int is emitted as that value with a
		// width suffix instead. It printed 0 before, silently.
		name: "a constant too wide for a C int",
		src: `const shift = 1 << 40
const product = 2000000000 * 3

var g int64 = 1 << 40
var h uint64 = 1 << 63
var neg int64 = -1 << 62

func take(v int64) int64 { return v }

func main() {
	var a int64 = 1 << 40
	var b int64 = 2000000000 * 3
	var c uint64 = 1 << 63
	println(a, b, c, g, h)
	println(neg)
	println(int64(shift), int64(product))
	println(take(1 << 40))
	a = 1 << 41
	println(a, a>>1)
	// A negative wide value is spelled as its bit pattern: the target's C compiler
	// folds no unary minus in a global initializer.
	var d int64 = -1 << 40
	var i int64 = -6000000000
	println(d, i, d>>4, -d)
	// The ones C computes the same way are left as written.
	var e int = 1 << 30
	println(e, e/2)
}
`,
		want: "1099511627776 6000000000 9223372036854775808 1099511627776 9223372036854775808\n-4611686018427387904\n1099511627776 6000000000\n1099511627776\n2199023255552 1099511627776\n-1099511627776 -6000000000 -68719476736 1099511627776\n1073741824 536870912\n",
	},
	{
		// A defined type is checked as the type it is defined over -- following a
		// chain of definitions to reach it -- so its values are bounded, converted
		// and compared like that type's, while its own name is what a diagnostic
		// says and what carries its methods. Nothing here used to be checked at all:
		// a variable of a defined type carried no type category, so every check
		// keyed on one was skipped for it.
		name: "a defined type is checked as what it is defined over",
		src: `type Celsius int
type Fahrenheit int
type Chain Celsius
type Name string
type Flag bool
type Small uint8

const room Celsius = 20

func (c Celsius) f() Fahrenheit { return Fahrenheit(int(c)*9/5 + 32) }

func conv(f Fahrenheit) Celsius { return Celsius((int(f) - 32) * 5 / 9) }

func main() {
	var c Celsius = room
	var d Chain = 5
	var s Small = 255
	var n Name = "lab"
	var f Flag = true
	println(int(c), int(d), int(s), n, f)
	println(int(c.f()), int(conv(212)))
	c = c*2 + 1
	s = s - 5
	f = !f
	n = "done"
	println(int(c), int(s), f, n, len(n))
	if !f && n == "done" {
		println("ok")
	}
}
`,
		want: "20 5 255 lab true\n68 100\n41 250 false done 4\nok\n",
	},
	{
		// A conversion between two types of the one representation builds nothing and
		// costs nothing: it is the operand itself. It must also emit no C cast, since
		// C has no cast to a non-scalar type -- `(Name)(s)` on a string was one, which
		// gcc took as an extension and the target's compiler need not have. A scalar
		// conversion keeps its cast, which is what makes a narrowing one truncate.
		name: "a conversion between one representation is free",
		src: `type Name string
type Flag bool
type Celsius int
type List []int

var back [2]int

func main() {
	var s string = "hi"
	var n Name = Name(s)
	println(n, string(n), string(s), len(string(n)))

	var b bool = true
	var f Flag = Flag(b)
	println(f, bool(f), bool(b))

	var c Celsius = 300
	println(int(c), uint8(c), Celsius(7))

	var xs []int = back[:]
	var l List = List(xs)
	println(len(l), cap(l))
}
`,
		want: "hi hi hi 2\ntrue true true\n300 44 7\n2 2\n",
	},
	{
		// A named type is a distinct type, but the same representation as the one it
		// is defined over -- so a value of it prints, indexes, ranges, compares and
		// carries a length exactly as that one does. Every such decision used to read
		// the typedef's name instead, so `type Name string` printed as %d of the
		// first word of its header: a silent wrong answer, the only kind that runs.
		name: "a named type is represented as what it is over",
		src: `type Name string
type Celsius int
type Flag bool
type Ratio float64
type List []int

func (c Celsius) f() int  { return int(c)*9/5 + 32 }
func (n Name) size() int  { return len(n) }
func (l List) total() int {
	t := 0
	for _, v := range l {
		t += v
	}
	return t
}

var back [3]int
var pkgName Name = "pkg"

func main() {
	var n Name = "hello"
	var c Celsius = 20
	var f Flag = true
	var r Ratio = 1.5
	println(n, c, f, r, pkgName)
	println(len(n), n[1], n[1:3])
	println(n == "hello", n != "x", n < "z")
	for i, ch := range n {
		println(i, ch)
	}
	switch n {
	case "hello":
		println("hit")
	default:
		println("miss")
	}

	var l List = back[:]
	l[0] = 1
	l[2] = 9
	println(len(l), cap(l), l[2], l.total())

	println(c.f(), n.size())
}
`,
		want: "hello 20 true 1.5 pkg\n5 101 el\ntrue true true\n0 104\n1 101\n2 108\n3 108\n4 111\nhit\n3 3 9 10\n68 5\n",
	},
	{
		// A short declaration carries over the named type of what initializes it, so
		// the ordinary `p := P{...}` is checked exactly as `var p P = P{...}` is.
		// The four provenances -- a literal, the address of one, a copy, and a call's
		// result -- all reach the same methods and fields here, which is what pins
		// that recording the type did not change what is emitted.
		name: "a short declaration carries a named type",
		src: `type P struct {
	x int
	s string
}

func (p P) get() int { return p.x }
func (p *P) bump()   { p.x++ }

var store = P{40, "store"}

func mk() P   { return P{1, "mk"} }
func mkp() *P { return &store }

func main() {
	a := P{1, "lit"}
	a.bump()
	println(a.get(), a.s)

	b := &P{2, "addr"}
	b.bump()
	println(b.get(), b.s)

	c := a
	c.bump()
	println(a.get(), c.get())

	d := mk()
	d.bump()
	println(d.get(), d.s)

	e := mkp()
	e.bump()
	println(e.get(), store.get(), e.s)
}
`,
		want: "2 lit\n3 addr\n2 3\n2 mk\n41 41 store\n",
	},
	{
		// A named function used as a value: assigned to a variable, passed as an
		// argument, returned as a result, held in an array and in a struct field,
		// and called through every one of them. It lowers to a C function pointer,
		// which costs nothing at run time and allocates nothing -- the function is
		// already there, only its address travels.
		name: "a function used as a value",
		src: `type op struct {
	fn   func(int, int) int
	name string
}

func add(a int, b int) int { return a + b }
func mul(a int, b int) int { return a * b }
func sq(n int) int         { return n * n }

func run(f func(int, int) int, a int, b int) int { return f(a, b) }

func pick(mulIt bool) func(int, int) int {
	if mulIt {
		return mul
	}
	return add
}

var table [2]func(int) int
var chosen func(int, int) int

func apply(f func(int) int, xs []int) {
	for i := 0; i < len(xs); i++ {
		xs[i] = f(xs[i])
	}
}

func main() {
	println(run(add, 3, 4), run(mul, 3, 4))
	g := pick(true)
	h := pick(false)
	println(g(3, 4), h(3, 4))

	table[0] = sq
	table[1] = sq
	println(table[0](5), table[1](6))

	println(chosen == nil)
	chosen = add
	println(chosen != nil, chosen(1, 2))

	var o op = op{mul, "mul"}
	println(o.name, o.fn(6, 7))
	k := o.fn
	println(k(2, 3))

	xs := []int{1, 2, 3}
	apply(sq, xs)
	println(xs[0], xs[1], xs[2])

	ops := [2]func(int, int) int{add, mul}
	for i := 0; i < 2; i++ {
		println(run(ops[i], 10, 20))
	}
}
`,
		want: "7 12\n12 7\n25 36\ntrue\ntrue 3\nmul 42\n6\n1 4 9\n30\n200\n",
	},
	{
		// A function value handed to another cog over a channel. A function pointer
		// names code, not the frame it was made in, so unlike a slice or an address
		// it is always safe to send -- the escape rules have nothing to say about it.
		name: "a function value crosses a channel",
		src: `var ch chan func(int) int
var done chan int

func sq(n int) int  { return n * n }
func neg(n int) int { return -n }

func worker() {
	g := <-ch
	done <- g(7)
}

func main() {
	go worker()
	ch <- sq
	println(<-done)
	go worker()
	ch <- neg
	println(<-done)
}
`,
		want: "49\n-7\n",
	},
	{
		// A package may declare several init functions. Go runs them in the order
		// they are written, each on the state the ones before it left, and none of
		// them is in scope under the name -- so they cannot all be called init in
		// the emitted C, which would be a redefinition. Two of them used to emit
		// two C functions both named init and the program did not compile at all.
		name: "several init functions run in order",
		src: `var order [3]int
var next int
var n int

func mark(k int) {
	order[next] = k
	next++
}

func init() {
	mark(1)
	n = 1
}

func init() {
	mark(2)
	n = n * 3
}

func init() {
	mark(3)
	n = n + 4
}

func main() {
	println(order[0], order[1], order[2], n)
}
`,
		want: "1 2 3 7\n",
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
		want:   "panic: index out of range\n",
	},
	{
		// Slicing was the one indexing form that trapped on nothing: `a[1:9]` over a
		// four-element array produced a length-8 view of storage the array does not
		// own, and `a[3:1]` a length of -2. On a part with no memory protection that
		// is a write into whatever sits next in Hub RAM.
		name: "slice bounds out of range trap",
		src: `func main() {
	var a [4]int
	i := 9
	s := a[1:i]
	println(len(s))
}
`,
		panics: true,
		want:   "panic: slice bounds out of range\n",
	},
	{
		// The bounds that are legal, which the check must leave alone: the whole
		// range, either end omitted, a run-time bound, a re-slice reaching past the
		// length up to the capacity (which Go allows and the check therefore measures
		// against cap, not len), a string, an array row, and a package-level view.
		//
		// The last line is why the bounds go through a helper rather than being
		// checked in place: they are its arguments, so each is evaluated once. The
		// header names low in all three of its fields, and spelled inline that read
		// the counter three times and built a header whose pointer, length and
		// capacity did not agree with each other.
		name: "slice bounds that are in range",
		src: `var arr [6]int
var view = arr[1:3]
var n int

func next() int {
	n++
	return n
}

func main() {
	for i := 0; i < 6; i++ {
		arr[i] = i * 10
	}
	println(len(view), cap(view), view[0], view[1])

	s := make([]int, 3, 5)
	s[0] = 1
	s[2] = 3
	println(len(s[:]), len(s[1:]), len(s[:2]))

	u := s[0:5]
	println(len(u), cap(u))

	i, j := 1, 4
	v := arr[i:j]
	println(len(v), cap(v), v[0])

	str := "hello"
	println(str[1:4], len(str[2:]))

	var m [2][3]int
	m[1][2] = 7
	r := m[1][:]
	println(len(r), r[2])

	w := arr[next():5]
	println(len(w), w[0], n)
}
`,
		want: "2 5 10 20\n3 2 2\n5 5\n3 5 10\nell 3\n3 7\n4 10 1\n",
	},
	{
		// A send clause in a select. It offers its value and waits for a receiver to
		// take it, which is what a send means -- the body runs because the value was
		// delivered, not because it was deposited somewhere.
		//
		// The offer stands across rounds and is taken back only when the receive
		// clause looks ready, since taking a value commits to that clause. The
		// hammer loop drives that path twenty times in both directions, which is
		// where a rendezvous protocol goes wrong if it is going to.
		name: "select with a send clause",
		src: `func drain(ch chan int, done chan int) {
	t := 0
	for i := 0; i < 3; i++ {
		t += <-ch
	}
	done <- t
}

func peer(in chan int, out chan int, done chan int) {
	t := 0
	for i := 0; i < 6; i++ {
		t += <-in
		out <- i
	}
	done <- t
}

func main() {
	var ch chan int
	var done chan int
	go drain(ch, done)
	for i := 1; i <= 3; i++ {
		select {
		case ch <- i:
		}
	}
	println("single", <-done)

	var in chan int
	var out chan int
	var pdone chan int
	go peer(in, out, pdone)
	sent := 0
	got := 0
	for sent < 6 || got < 6 {
		select {
		case in <- 1:
			sent++
		case <-out:
			got++
		}
	}
	println("hammer", sent, got, <-pdone)
}
`,
		want: "single 6\nhammer 6 6 6\n",
	},
	{
		// A select over more than one channel, which is the whole point of the
		// statement and had never been compiled: every case here used a single
		// clause, and the emitted C put each clause's value declaration between the
		// previous clause's closing brace and this one's `else`, which is not C.
		//
		// The first loop multiplexes two feeders until both are drained; the second
		// takes the default with three channels idle.
		name: "select over several channels",
		src: `func feedA(a chan int) {
	a <- 1
	a <- 3
}

func feedB(b chan int) {
	b <- 10
	b <- 30
}

func main() {
	var a chan int
	var b chan int
	go feedA(a)
	go feedB(b)

	sum := 0
	n := 0
	for n < 4 {
		select {
		case x := <-a:
			sum += x
			n++
		case y := <-b:
			sum += y
			n++
		}
	}
	println("mux", sum, n)

	var c chan int
	got := 0
	select {
	case <-a:
		got = 1
	case <-b:
		got = 2
	case <-c:
		got = 3
	default:
		got = 9
	}
	println("default", got)
}
`,
		want: "mux 44 4\ndefault 9\n",
	},
	{
		// A slice literal standing as a value rather than as a variable's
		// initializer: passed to a function, measured by len and cap, assigned, and
		// nested inside another literal's element. It is bound to a local declared
		// before the statement, which is where its backing array comes from -- the
		// same two declarations `s := []int{...}` has always emitted.
		//
		// An array literal is not here: an array is not a C value, so binding one
		// would only move "assignment to expression with array type" into the C.
		name: "a slice literal as a value",
		src: `type P struct {
	x int
	y int
}

func sum(xs []int) int {
	t := 0
	for _, v := range xs {
		t += v
	}
	return t
}

func first(ps []P) int { return ps[0].x }

func main() {
	println("arg", sum([]int{1, 2, 3}))
	println("struct elems", first([]P{{7, 8}, {9, 10}}))
	println("len", len([]int{1, 2}), cap([]int{1, 2, 3}))

	var s []int
	s = []int{4, 5}
	println("assign", len(s), s[1])

	println("nested", sum([]int{sum([]int{1, 2}), 3}))
}
`,
		want: "arg 6\nstruct elems 7\nlen 2 3\nassign 2 5\nnested 6\n",
	},
	{
		// `go x.M(args)`, which was refused: only a plain function could be launched,
		// so a worker with a method had to be wrapped in one. The receiver is simply
		// the first argument -- the trampoline's block carries it like any other, and
		// the cog calls <T>_M(recv, ...).
		//
		// The "copied" line is what says the receiver is evaluated where the go
		// statement stands, as Go evaluates it: the write afterwards is not what the
		// goroutine sees, which makes the answer deterministic rather than a race.
		name: "go on a method",
		src: `type worker struct {
	n    int
	data []int
}

type counter int

var backing [3]int
var shared worker

func (w worker) twice(ch chan int) { ch <- w.n * 2 }

func (w *worker) size(ch chan int) { ch <- len(w.data) }

func (c counter) add(ch chan int, k int) { ch <- int(c) + k }

func main() {
	var ch chan int

	w := worker{21, backing[:]}
	go w.twice(ch)
	println("value", <-ch)

	w.n = 1
	go w.twice(ch)
	w.n = 99
	println("copied", <-ch, w.n)

	shared.data = backing[:]
	go shared.size(ch)
	println("pointer", <-ch)

	var c counter = 5
	go c.add(ch, 3)
	println("named", <-ch)
}
`,
		want: "value 42\ncopied 2 99\npointer 3\nnamed 8\n",
	},
	{
		// A receiver or parameter the body never uses. Go allows both -- an unused
		// parameter is not an unused variable -- and C warns about both, which the
		// harness fails on, so this case tests itself: without the "(void)name;" the
		// emitter writes for each, the compile step reports and the test fails.
		//
		// The receiver already got one when the source left it unnamed or its type
		// was an empty struct; a named one the body ignores is the same situation and
		// did not. A parameter got one only when unnamed.
		name: "an unused receiver or parameter",
		src: `type box struct{ n int }

func (b box) tag() int { return 7 }

func (b *box) ptag() int { return 8 }

func pick(a int, b int) int { return a }

func mix(a int, s string, xs []int, p *box) int { return len(xs) }

// A receiver used only inside a nested scope is used, and keeps its name.
func (b box) deep() int {
	if true {
		return b.n
	}
	return 0
}

func main() {
	var b box
	b.n = 3
	println(b.tag(), b.ptag(), b.deep())
	println(pick(1, 2))
	xs := []int{1, 2}
	println(mix(1, "x", xs, &b))
}
`,
		want: "7 8 3\n1\n2\n",
	},
	{
		// Ranging over a composite literal, `for _, v := range []int{1, 2, 3}`, which
		// is how the idiom is written in Go and did not parse: the grammar keeps a
		// literal out of a header, since its "{" would be the block's. A bracketed
		// type has no such trouble -- a "[" cannot begin a block -- so only the
		// bare-name form is still kept out.
		//
		// The operand is bound to a local first, which is where a slice literal's
		// backing array comes from. Every form of the loop is covered, since each
		// reads the operand differently.
		name: "range over a composite literal",
		src: `type P struct {
	x int
	y int
}

func main() {
	for _, v := range []int{1, 2, 3} {
		println("slice", v)
	}
	for i, v := range [3]int{4, 5, 6} {
		println("array", i, v)
	}
	for i := range []int{7, 8} {
		println("index", i)
	}
	n := 0
	for range []int{1, 2, 3} {
		n++
	}
	println("count", n)
	for _, s := range []string{"a", "bb"} {
		println("string", s, len(s))
	}
	for _, p := range []P{{1, 2}, {3, 4}} {
		println("struct", p.x, p.y)
	}
}
`,
		want: "slice 1\nslice 2\nslice 3\narray 0 4\narray 1 5\narray 2 6\n" +
			"index 0\nindex 1\ncount 3\nstring a 1\nstring bb 2\nstruct 1 2\nstruct 3 4\n",
	},
	{
		// A trailing comma, which is what lets a list be written across lines -- the
		// form gofmt produces and the only readable way to spell a table. Go takes one
		// in a composite literal, a call's arguments, a parameter list and a result
		// list, and this covers all four.
		name: "a trailing comma in a list",
		src: `type P struct {
	x int
	y [2]int
}

var table = []P{
	{1, [2]int{2, 3}},
	{4, [2]int{5, 6}},
}

var keyed = P{
	x: 7,
	y: [2]int{8, 9},
}

func sum(
	a int,
	b int,
	c int,
) (
	int,
	int,
) {
	return a + b + c, a
}

func main() {
	t, first := sum(
		1,
		2,
		3,
	)
	println("call", t, first)
	println("table", table[1].x, table[1].y[0], keyed.y[1])

	xs := []int{
		10,
		20,
	}
	println("slice", len(xs), xs[1])

	m := [2][2]int{
		{1, 2},
		{3, 4},
	}
	println("matrix", m[1][0])
}
`,
		want: "call 6 1\ntable 4 5 9\nslice 2 20\nmatrix 3\n",
	},
	{
		// An array literal as a struct literal's element, `P{1, [2]int{2, 3}}`. An
		// array field's position implies its element type rather than its own, so
		// neither the written form nor the elided one was recognised there, and the
		// refusal said the literal belonged to a variable's initializer -- which is
		// true of a bare one and not of a nested aggregate, whose values C writes in
		// braces exactly where they stand.
		//
		// This is what a lookup table of records looks like, so the package-scope
		// forms matter as much as the local ones: both are laid out statically.
		name: "an array literal inside a struct literal",
		src: `type P struct {
	x int
	y [2]int
	m [2][2]int
}

type Q struct {
	n int
	a [3]int
}

var table = []P{{1, [2]int{2, 3}, [2][2]int{{9, 8}, {7, 6}}}, {4, [2]int{5, 6}, [2][2]int{{1, 2}, {3, 4}}}}

var one = Q{7, [3]int{1}}

func main() {
	a := P{1, [2]int{2, 3}, [2][2]int{{9, 8}, {7, 6}}}
	println("local", a.x, a.y[1], a.m[1][0])

	b := Q{5, [3]int{8, 9}}
	println("partial", b.n, b.a[0], b.a[1], b.a[2])

	println("table", table[0].y[1], table[1].x, table[1].m[0][1])
	println("pkg", one.n, one.a[0], one.a[2])

	c := P{y: [2]int{4, 5}}
	println("keyed", c.x, c.y[0], c.m[0][0])
}
`,
		want: "local 1 3 7\npartial 5 8 9 0\ntable 3 4 2\npkg 7 1 0\nkeyed 0 4 0\n",
	},
	{
		// Reading and writing through a slice-typed field of an indexed element,
		// `s[i].v[j]`. The index before it consumes the prefix -- what an index
		// produces is written, not a string that can be appended to -- so the field's
		// header had nothing left to build its `.len` from and the whole shape was
		// refused. It is bound to a temporary now, and since a header is a view, a
		// write through the temporary lands in the storage the field names: the
		// `write` and `nested` lines read the backing slice to say so.
		//
		// The bound value is what the bounds check measures against, too, so an index
		// past the field's own length traps whatever the element it came from.
		name: "through a slice field of an indexed element",
		src: `type item struct {
	n int
	v []int
}

type outer struct{ list []item }

var b1 = []int{10, 20, 30, 40}
var b2 = []int{7, 8}

func main() {
	s := make([]item, 2, 2)
	s[0].v = b2
	s[1].v = b1

	println("read", s[1].v[0], s[1].v[3], len(s[1].v), cap(s[1].v))

	s[1].v[0] = 99
	println("write", b1[0], s[1].v[0])

	q := s[1].v[1:3]
	println("reslice", len(q), q[0], s[1].v[1:][1])

	println("double", s[1].v[1:][2])

	var a [2]item
	a[1].v = b1
	a[1].v[1] = 21
	println("array", b1[1], a[1].v[1], a[1].v[2:][0])

	var o outer
	o.list = make([]item, 2, 2)
	o.list[1].v = b2
	o.list[1].v[0] = 77
	println("nested", b2[0], o.list[1].v[0], len(o.list[1].v))

	t := 0
	for i := 0; i < len(s[1].v); i++ {
		t += s[1].v[i]
	}
	println("sum", t)
}
`,
		want: "read 10 40 4 4\nwrite 99 99\nreslice 2 20 30\ndouble 40\narray 21 21 30\nnested 77 77 2\nsum 190\n",
	},
	{
		name: "index past a slice field of an indexed element traps",
		src: `type item struct{ v []int }

var b1 = []int{1, 2}

func main() {
	s := make([]item, 2, 2)
	s[1].v = b1
	i := 5
	println(s[1].v[i])
}
`,
		panics: true,
		want:   "panic: index out of range\n",
	},
	{
		// Assigning a slice-typed field of an indexed element, `s[i].v = xs`. The
		// element's other fields already took a value this way; a slice-valued one was
		// left out, because a plain `b.data = ...` target has to reach the shapes that
		// know how to give a `make` its backing array, and the rule keeping it there
		// caught this too. An index is what tells the two apart.
		//
		// What is assigned is the header: the field ends up naming the same storage
		// the right-hand side does, which the first three lines check by writing
		// through one view and reading the other.
		name: "a slice field of an indexed element",
		src: `type item struct {
	n int
	v []int
}

type outer struct{ list []item }

var b1 = []int{1, 2, 3}
var b2 = []int{7, 8}

func main() {
	var a [2]item
	a[1].v = b1
	q := a[1].v
	q[0] = 9
	println(b1[0], len(a[1].v), cap(a[1].v))

	s := make([]item, 2, 2)
	s[0].v = b1
	s[1].v = b2
	s[1].n = 5
	r := s[1].v
	println(len(s[0].v), len(r), r[1], s[1].n)

	s[1].v = b1
	t := s[1].v
	println(len(t), t[2])

	var o outer
	o.list = make([]item, 2, 2)
	o.list[1].v = b2
	u := o.list[1].v
	println(len(u), u[0])
}
`,
		want: "9 3 3\n3 2 8 5\n3 3\n2 7\n",
	},
	{
		// A string byte over 127. Go's byte is unsigned, while the string header
		// carries `const char*`, whose signedness C leaves to the implementation --
		// so a read of s[i] has to be cast or it is negative wherever char is signed.
		// It is on the host compiler and is not on the target's, which is why every
		// existing case agreed on both: they all index ASCII, where the two cannot
		// differ. This one sums the bytes of a two-byte rune, where they do.
		name: "a string byte is unsigned",
		src: `var g = "hé"

func main() {
	s := "hé"
	n := 0
	for i := 0; i < len(s); i++ {
		n += int(s[i])
	}
	println(s[0], s[1], int(s[1]), n)
	println(g[1], s[1:][0], s[0:2][1])
	var c byte = s[1]
	println(c, c > 128)
}
`,
		want: "104 195 195 468\n195 195 195\n195 true\n",
	},
	{
		// Operating on a slice expression's result -- indexing it, and slicing it
		// again. Both had to be written out as two statements, because a header is a
		// value and C has nowhere to put one mid-expression: the step after it wants
		// a base to write `.ptr` and `.len` off. The header is now bound to a
		// temporary before the statement, which is that base, and is exactly the
		// variable a reader used to have to introduce by hand.
		//
		// Only an interior slice step needs one. A chain that merely ends in a slice
		// -- `b.data[1:3]` -- still writes its header straight into place, so nothing
		// that already worked grew a temporary.
		name: "indexing and re-slicing a slice expression",
		src: `type buf struct {
	data []int
	fix  [4]int
}

var pool [8]int

func main() {
	var a [8]int
	for i := 0; i < 8; i++ {
		a[i] = i * 10
	}

	println("array", a[:][1], a[2:][1], a[1:6][2])

	s := make([]int, 6, 8)
	for i := 0; i < 6; i++ {
		s[i] = i + 1
	}
	println("slice", s[1:][0], s[:4][3], s[2:5][1])

	var b buf
	b.data = s
	b.fix[2] = 9
	println("field", b.data[1:][0], b.fix[1:3][1])

	r := a[1:6][1:4]
	println("reslice", len(r), cap(r), r[0], r[2])

	x := a[1:5:6][1:3]
	println("cap bound", len(x), cap(x), x[0])

	str := "hello"
	println("string", str[1:][0], str[1:4][1:][0])

	pool[3] = 7
	println("package", pool[:][3], pool[1:][2])
}
`,
		want: "array 10 30 30\nslice 2 4 4\nfield 2 9\nreslice 3 6 20 40\ncap bound 2 4 20\nstring 101 108\npackage 7 7\n",
	},
	{
		// The index is checked against the slice expression's own length, not the
		// operand's: a[1:3] has two elements however long a is.
		name: "index past a slice expression traps",
		src: `func main() {
	var a [8]int
	i := 5
	println(a[1:3][i])
}
`,
		panics: true,
		want:   "panic: index out of range\n",
	},
	{
		// A loop condition that needs a temporary. An expression can ask for a line
		// to be emitted before the statement it is in -- a field read off a call
		// result, arguments put in order, a bounds-checked slice -- and before the
		// statement is the wrong place when the statement is a loop and the
		// expression is its condition: the value would be computed once and the loop
		// would go on testing it. Every count below says how many times the condition
		// really ran, and each was one before the test moved into the loop body.
		//
		// What that move must not disturb: `continue` still reaching the post step,
		// `break` still leaving this loop, a labeled break still leaving the outer
		// one, and a `break` in a switch still naming the switch.
		name: "a loop condition that needs a temporary",
		src: `type P struct {
	x int
	y int
}

var n int

func mk() P {
	n++
	return P{1, 4}
}

func t(v int) int {
	n = n*10 + v
	return v
}

func pick(a int, b int) int { return a + b }

func main() {
	sum := 0
	for i := 0; i < mk().y; i++ {
		if i == 1 {
			continue
		}
		sum += i
	}
	println("continue", sum, n)

	n = 0
	c := 0
	for i := 0; i < mk().y; i++ {
		c++
		if i == 2 {
			break
		}
	}
	println("break", c, n)

	n = 0
	hits := 0
L:
	for i := 0; i < mk().y; i++ {
		for j := 0; j < 3; j++ {
			hits++
			if i == 1 && j == 1 {
				break L
			}
		}
	}
	println("labeled", hits)

	n = 0
	s := 0
	for i := 0; i < mk().y; i++ {
		switch i {
		case 1:
			s += 10
			break
		default:
			s++
		}
	}
	println("switch", s)

	n = 0
	k := 0
	tot := 0
	for k < mk().y {
		k++
		if k == 2 {
			continue
		}
		tot += k
	}
	println("while", k, tot, n)

	xs := make([]int, 5, 8)
	r := 0
	for i := 0; i < len(xs[1:]); i++ {
		r++
		xs = xs[:len(xs)-1]
	}
	println("reslice", r, len(xs))

	n = 0
	a := 0
	for i := 0; i < pick(t(1), t(2)); i++ {
		a++
	}
	println("args", a, n)
}
`,
		want: "continue 5 5\nbreak 3 3\nlabeled 5\nswitch 13\nwhile 4 8 5\nreslice 2 3\nargs 3 12121212\n",
	},
	{
		// A slice expression's third bound, `a[low:high:max]`, which sets the
		// result's capacity to max less low rather than taking the operand's own.
		// Without a heap this is how a region of a package-level buffer is handed
		// out: appending to a region stops at its own end instead of running on into
		// the next one's storage, which is what the head/tail pair below shows.
		//
		// Every operand shape goes through the same path -- an array, a slice, a
		// struct's slice and array fields, a row of a multi-dimensional array, and a
		// package-level view -- with constant and run-time bounds both.
		name: "slice expression with a capacity bound",
		src: `type buf struct {
	data []int
	fix  [4]int
}

var pool [8]int
var view = pool[2:3:5]

func main() {
	var a [8]int
	for i := 0; i < 8; i++ {
		a[i] = i * 10
	}

	s := a[1:4:6]
	println(len(s), cap(s), s[0], s[2])

	i, j, k := 1, 3, 5
	d := a[i:j:k]
	println(len(d), cap(d), d[0])

	b := a[:]
	c := b[2:5:6]
	println(len(c), cap(c), c[0])

	var t buf
	t.data = make([]int, 4, 8)
	t.fix[2] = 7
	u := t.data[1:2:3]
	v := t.fix[1:3:4]
	println(len(u), cap(u), len(v), cap(v), v[1])

	var m [2][4]int
	m[1][2] = 5
	r := m[1][1:3:4]
	println(len(r), cap(r), r[1])

	head := pool[0:0:2]
	tail := pool[2:2:4]
	head = append(head, 1)
	head = append(head, 2)
	tail = append(tail, 9)
	println(len(head), cap(head), len(tail), cap(tail), head[0], tail[0])

	println(len(view), cap(view))
}
`,
		want: "3 5 10 30\n2 4 10\n3 4 20\n1 2 2 3 7\n2 3 5\n2 2 1 2 1 9\n1 3\n",
	},
	{
		// The capacity bound is checked like the other two: 0 <= low <= high <= max
		// <= cap. Here max reaches past the array, so the region would have handed
		// out storage the array does not own.
		name: "slice capacity bound out of range trap",
		src: `func main() {
	var a [4]int
	i := 9
	s := a[0:2:i]
	println(len(s))
}
`,
		panics: true,
		want:   "panic: slice bounds out of range\n",
	},
	{
		// A package variable initialized from something that needs a temporary. The
		// temporary is requested by the expression and placed before the statement
		// that uses it, which at package scope had nowhere to go: the initializer is
		// run from the synthesized package initializer, not from a statement, so the
		// line declaring it was dropped and the C named a variable it never declared.
		// `var corner = mk().y` -- the field read off a struct return that has to be
		// bound first -- has been broken since that hoisting was introduced.
		name: "package variable initialized through a temporary",
		src: `type point struct {
	x int
	y int
}

var arr [6]int

func mk() point { return point{4, 5} }

func lo() int { return 2 }

var corner = mk().y
var tail = len(arr[lo():5])

func main() {
	println(corner, tail)
}
`,
		want: "5 3\n",
	},
	{
		// The shape the crossing rule endorses: a goroutine writes into a buffer whose
		// backing array is package-level, so it outlives every frame including the one
		// that launched it, and a channel says when the writing is done. Passing a
		// buffer this cog owns instead -- `var a [4]int; go fill(a[:], ch)` -- is
		// refused, and this is what that refusal asks for.
		name: "goroutine fills a package-level buffer",
		src: `var buf [4]int

func fill(s []int, ch chan int) {
	for i := 0; i < len(s); i++ {
		s[i] = i * 3
	}
	ch <- len(s)
}

func main() {
	var ch chan int
	go fill(buf[:], ch)
	n := <-ch
	println(n, buf[0], buf[1], buf[3])
}
`,
		want: "4 0 3 9\n",
	},
	{
		// Eight at once, every one of them blocked on a send nobody receives, so no
		// slot is ever going to come free. This is the other side of "goroutine
		// slots are reused": waiting for a slot must still end in the panic when
		// there is genuinely no cog to be had, rather than spinning forever.
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
		want:   "panic: out of cogs\n",
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
		// Divide by zero, through each of the four lowerings it has: a signed
		// division and remainder, which carry the check inside their guarded helper
		// (ogo_div_<T> / ogo_mod_<T>), and an unsigned one and a 64-bit one, whose
		// divisor goes through ogo_nonzero / ogo_nonzero64 instead. The message was
		// emitted by all of them and exercised at run time by none.
		name: "divide by zero traps",
		src: `func main() {
	var a int = 6
	var b int = 0
	println(a / b)
}
`,
		panics: true,
		want:   "panic: integer divide by zero\n",
	},
	{
		name: "remainder by zero traps",
		src: `func main() {
	var a int = 6
	var b int = 0
	println(a % b)
}
`,
		panics: true,
		want:   "panic: integer divide by zero\n",
	},
	{
		name: "unsigned divide by zero traps",
		src: `func main() {
	var a uint32 = 6
	var b uint32 = 0
	println(a / b)
}
`,
		panics: true,
		want:   "panic: integer divide by zero\n",
	},
	{
		name: "64-bit divide by zero traps",
		src: `func main() {
	var a int64 = 6
	var b int64 = 0
	println(a / b)
}
`,
		panics: true,
		want:   "panic: integer divide by zero\n",
	},
	{
		// append past a slice's capacity. Without a heap there is nowhere to grow
		// into, so it traps rather than reallocating -- the one place OctoGo's append
		// parts company with Go's, and the trap that says so had no run case.
		name: "append past capacity traps",
		src: `func main() {
	var back [2]int
	s := back[:0]
	s = append(s, 1)
	s = append(s, 2)
	s = append(s, 3)
	println(len(s))
}
`,
		panics: true,
		want:   "panic: append: out of capacity\n",
	},
	{
		// Go panics on a negative shift count, where C is undefined. The guarded
		// shift helper is where that is decided.
		name: "a negative shift count traps",
		src: `func main() {
	var x int32 = 1
	var n int32 = -1
	println(x << n)
}
`,
		panics: true,
		want:   "panic: negative shift amount\n",
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
		want:   "panic: boom\n",
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
// TestEmitCRunUnchecked runs the same corpus with the run-time checks off, which is
// what `ogo build --unchecked` emits. Every other run test builds checked, so the
// whole unchecked configuration was only ever pinned by a couple of emission
// goldens -- a lowering that is correct only because a check happens to stand in
// front of it would have gone unnoticed.
//
// A panicking case is skipped: without the checks there is nothing to panic.
func TestEmitCRunUnchecked(t *testing.T) {
	runCorpus(t, nil)
}

func TestEmitCRun(t *testing.T) {
	runCorpus(t, []EmitOption{Checked()})
}

func runCorpus(t *testing.T, opts []EmitOption) {
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

	checked := len(opts) != 0
	for _, test := range emitRunCases {
		if test.panics && !checked {
			continue // the panic is the check; with none, there is nothing to expect
		}
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(test.src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			var buf bytes.Buffer
			if err := EmitC(pkg, &buf, opts...); err != nil {
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
					return
				}
				// The message matters as much as the abort: a panic that says
				// nothing is a bare "signal: aborted", which is what it looked like
				// for as long as ogo_panic did not flush stdout before abort --
				// through a pipe, every buffered byte, the panic line included, was
				// discarded.
				if w := test.want; w != "" && !strings.Contains(strings.ReplaceAll(string(got), "\r\n", "\n"), w) {
					t.Errorf("panic output:\n got %q\nwant it to contain %q", got, w)
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

// TestCrossPkgCompositeLit pins what a composite literal of another package's type
// may say. The type has to be one, and it has to be exported -- an unexported name
// of another package is not nameable at all -- and a keyed element names an exported
// field of it, which is the rule a field read already followed. The positional and
// count checks are the same ones a same-package literal gets; that they report the
// qualified spelling rather than just the type's own name is what is worth pinning.
func TestCrossPkgCompositeLit(t *testing.T) {
	for _, test := range []struct{ name, src, want string }{
		{
			name: "unexported type",
			src:  "h := geo.hidden{}\nprintln(h.n)",
			want: "cannot refer to unexported name geo.hidden",
		},
		{
			name: "not a struct type",
			src:  "p := geo.Count{1}\nprintln(p)",
			want: "invalid composite literal type: geo.Count is not a struct type",
		},
		{
			name: "unknown field",
			src:  "p := geo.Point{Z: 1}\nprintln(p.X)",
			want: "unknown field Z in struct literal of type geo.Point",
		},
		{
			name: "unexported field",
			src:  "p := geo.Point{tag: 1}\nprintln(p.X)",
			want: "cannot refer to unexported field tag of type geo.Point",
		},
		{
			name: "positional fills an unexported field",
			src:  "p := geo.Point{1, 2, 3}\nprintln(p.X)",
			want: "implicit assignment to unexported field tag in struct literal of type geo.Point",
		},
		{
			name: "too many values",
			src:  "p := geo.Vec{1, 2, 3}\nprintln(p.A)",
			want: "too many values in geo.Vec{...}: 3 values but 2 fields",
		},
		{
			name: "mixed forms",
			src:  "p := geo.Point{1, Y: 2}\nprintln(p.X)",
			want: "mixture of field:value and value elements in struct literal",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo": &fstest.MapFile{Data: []byte("import \"geo\"\n\nfunc main() {\n" + test.src + "\n}\n")},
				"geo/geo.ogo": &fstest.MapFile{Data: []byte(`type Point struct {
	X   int
	Y   int
	tag int
}

type Count int

type Vec struct {
	A int
	B int
}

type hidden struct{ n int }
`)},
			}
			_, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				t.Fatalf("Build accepted %q", test.src)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("Build error %q does not contain %q", err, test.want)
			}
		})
	}
}

// TestEmitCMultiPackage builds a program spread over two packages -- a main package
// importing a local "greet" package -- and runs it on the host shim. It checks that
// import resolution, cross-package calls (greet.Hello(...)), and a package function's
// result type all work when the whole program is emitted into one translation unit.
// multiPkgProgram is the multi-package program every layer of the pipeline is run
// over: the host C compiler (TestEmitCMultiPackage), the real backend
// (TestTargetBuildMultiPkg) and the board (TestOnBoardMultiPkg). It used to be
// inline in the first of those, so a program spanning packages was never compiled
// by flexcc and never ran on hardware -- and a package boundary is exactly where
// the lowering is about nothing but names.
// multiPkgWant is what that program prints, on every one of the three.
const multiPkgWant = "300\nLOUD\n50\n6\n5\n45\n6 1000\n200\n207\n3 100\n4 9\n" +
	"6 13\n0 8\n0 0\n2 7\n2 8\n40\n"

var multiPkgProgram = map[string]string{
	"main.ogo": `import "greet"

// A private helper of main's, same name as one in greet: with per-package name
// mangling the two do not collide in the single translation unit.
func scale(n int) int { return n + 1 }

// main's own Point, same name (and method) as greet's: per-package mangling of
// types and methods keeps them distinct in the single translation unit.
type Point struct{ x, y int }

func (p Point) sum() int { return p.x + p.y }

// A package global with the same name as greet's, likewise namespaced.
var base int = 5

// A package constant with the same name as greet's: per-package mangling keeps the
// two from colliding in the single translation unit (both emit a distinct C name).
const K = 3

func main() {
println(greet.Hello(3))
msg := greet.Loud("hi")
println(msg)
println(greet.Twice(21) + 8)
println(scale(5))
p := Point{2, 3}
println(p.sum())
println(greet.PointSum())
base = base + 1
println(base, greet.Base())
// A direct read and write of another package's exported variable, resolved to
// its mangled global -- not routed through a getter/setter.
println(greet.Total)
greet.Total = greet.Total + 7
println(greet.Total)
// A same-named constant in each package, and a cross-package read of greet's
// (a folded integer constant inlines its value).
println(K, greet.K)
// A variable of an imported package's type: declared, its exported fields
// written and read, and its exported method called -- all resolving to greet's
// mangled typedef greet_Vec.
var v greet.Vec
v.A = 4
v.B = 5
println(v.A, v.Sum())
// A composite literal of an imported package's type, which is the other way to
// make one of those values: positional, keyed, and empty, plus a nested one and
// a table of them at package scope.
w := greet.Vec{6, 7}
println(w.A, w.Sum())
k := greet.Vec{B: 8}
println(k.A, k.B)
e := greet.Vec{}
println(e.A, e.B)
pair := greet.Pair{greet.Vec{1, 2}, greet.Vec{3, 4}}
println(pair.Lo.B, pair.Hi.Sum())
println(unit.Sum(), vecs[1].A)
// A goroutine launched on an imported package's function: it resolves to the
// same mangled name an ordinary call into that package does.
var ch chan int
go greet.Send(ch, 20)
println(<-ch)
}

// Package-scope values of an imported type, laid out statically.
var unit = greet.Vec{A: 1, B: 1}
var vecs = []greet.Vec{{9, 9}, {8, 8}}
`,
	"greet/greet.ogo": `type Point struct{ x, y int }

func (p Point) sum() int { return p.x*10 + p.y }

func PointSum() int {
p := Point{4, 5}
return p.sum()
}

var base int = 1000

// Total is an exported variable read and written directly from main.
var Total int = 200

// K is an exported constant, same name as main's, read directly from main.
const K = 100

// Vec is an exported type with exported fields and an exported method, used from
// main through a var declaration (var v greet.Vec).
type Vec struct {
A int
B int
}

func (v Vec) Sum() int { return v.A + v.B }

// Pair holds two Vecs, so a literal of it nests literals of another package's type.
type Pair struct {
Lo Vec
Hi Vec
}

// Send is launched as a goroutine from main, which is the qualified callee form.
func Send(ch chan int, n int) { ch <- n * 2 }

func Base() int { return base }

func Hello(n int) int { return scale(n) * 100 }

func Twice(n int) int { return n * 2 }

func scale(n int) int { return n }
`,
	"greet/loud.ogo": `func Loud(s string) string {
if len(s) > 0 {
	return "LOUD"
}
return s
}
`,
}

func TestEmitCMultiPackage(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for name, src := range multiPkgProgram {
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}
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
	out, err := exec.Command(cc, "-std=gnu11", "-Wall", "-Wextra",
		"-Wno-unused-function", "-Wno-format", "-I", shim, "-o", bin, csrc, "-lpthread").CombinedOutput()
	if err != nil {
		t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, out, buf.String())
	}
	if len(bytes.TrimSpace(out)) != 0 {
		t.Errorf("cc warned:\n%s\n--- emitted ---\n%s", out, buf.String())
	}
	got, runErr := exec.Command(bin).CombinedOutput()
	if runErr != nil {
		t.Fatalf("run: %v\n%s", runErr, got)
	}
	if g := strings.ReplaceAll(string(got), "\r\n", "\n"); g != multiPkgWant {
		t.Errorf("output:\n got %q\nwant %q\n--- emitted ---\n%s", g, multiPkgWant, buf.String())
	}
}
