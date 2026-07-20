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
		// Several local channels each with their own `go`, which is where flexcc's
		// FCACHE used to lift the rendezvous poll into Cog RAM and stop it seeing
		// the sender's writes -- a silent hang, on hardware only, and only once a
		// program had roughly this many of both. One channel, or one spawn, was
		// not enough to trigger it, so the cases above cannot stand in for this.
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
			// rather than being discovered on real hardware.
			out, err := exec.Command(cc, "-std=gnu11", "-Wall", "-Wextra", "-I", shim,
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
