// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The three programs below are in gofmt's layout, which TestFormatContinuation
// checks against gofmt itself where there is one.

// continuationOperands is every place a binary expression is written across
// lines: the operand after an operator that ends its line is one level in, and
// the levels follow Go's tree, not the grammar's three flat ones.
const continuationOperands = `type P struct {
	x, y int
}

func h(a, b int) int { return a + b }

func k(b bool) int {
	if b {
		return 1
	}
	return 0
}

func two() (int, int) {
	return 1 +
			2,
		3 *
			4
}

func g(a, b, c int, p, q, r bool) int {
	v1 := p ||
		q &&
			r
	v2 := p &&
		q ||
		r
	v3 := p ||
		q ||
		r &&
			p
	v4 := a +
		b*
			c
	v5 := a*
		b +
		c
	v6 := a +
		b -
		c*
			a*
			b
	v7 := (a +
		b) *
		c
	v8 := a == b ||
		b == c &&
			c ==
				a
	v9 := h(a+
		b,
		c)
	v10 := h(
		a+
			b,
		c*
			a,
	)
	v11 := -a +
		-b
	v12 := a<<2 |
		b<<4 |
		c
	v13 := []int{a +
		b, c}
	v14 := P{
		x: a +
			b,
		y: c,
	}
	v15 := a < b && (b < c ||
		c < a) &&
		p
	var v16 = a +
		b
	var v17, v18 = a +
		b, b +
		c
	v19, v20 := a+
		b,
		b+
			c
	arr := [4]int{}
	arr[a+
		b] = c +
		a
	v1 = v1 ||
		p
	a += b +
		c
	if v := a +
		b; v > c &&
		p {
		return v
	}
	for i := a +
		b; i < c &&
		p; i += a +
		b {
		a++
	}
	switch a +
		b {
	case a +
		b, c +
		a:
		a--
	}
	switch {
	case p &&
		q,
		r:
		a--
	}
	for p &&
		q {
		p = false
	}
	ch := make(chan int, 1)
	ch <- a +
		b
	defer h(a+
		b, c)
	println(v1, v2, v3, v4, v5, v6, v7, v8, v9, v10, v11, v12, len(v13), v14.x, v15, v16, v17, v18, v19, v20, arr[0], <-ch)
	println("a" +
		"b" +
		"c")
	if a < b &&
		b < c ||
		p {
		return h(a, b) +
			h(b,
				c) +
			k(p &&
				q)
	}
	return a +
		b
}

func main() {
	x, y := two()
	println(g(1, 2, 3, true, false, true), x, y)
}
`

// continuationLists is the lists, the calls and the selectors: a list indents
// from the first element that begins a line, a return's as a whole where more
// than one value breaks, and a selector on a line of its own takes the arguments
// of its call with it.
const continuationLists = `type P struct {
	x, y int
}

type T struct {
	p P
	n int
}

func (t T) get() T { return t }

func (t T) add(a, b int) T {
	t.n += a + b
	return t
}

func h(a, b int) int { return a + b }

func ap(f func(int) int, a int) int { return f(a) }

func two() (int, int) {
	return h(1,
		2), 3
}

func three() (int, int, int) {
	return 1,
		2,
		3
}

func four() (int, int) {
	return ap(func(v int) int {
			return v
		}, 1), ap(func(v int) int {
			return v + 1
		}, 2)
}

func g(a, b, c int) int {
	s1 := []P{{
		x: 1,
	}, {
		x: 2,
	}}
	s2 := []int{1, 2,
		3, 4}
	s3 := []int{1, 2,
		3, 4,
	}
	s4 := []int{
		1, 2,
		// trailing
	}
	s5 := []int{
		// nothing
	}
	s6 := [][]int{{1,
		2}, {3,
		4}}
	v1 := ap(func(v int) int {
		return v + a
	},
		b)
	v2 := ap(func(v int) int {
		return v + a
	}, b)
	v3 := h(h(a,
		b),
		c)
	v4 := h(a, h(b,
		c))
	var t T
	t2 := t.
		get().
		add(1,
			2).
		get()
	v5 := t.
		p.
		x
	v6 :=
		a +
			b
	v7, v8 :=
		a,
		b
	v9 := h(
		a,
		// comment
		b,
	// last
	)
	x, y :=
		two()
	var arr [8]int
	arr[a] = arr[b+
		c]
	println(len(s1), len(s2), len(s3), len(s4), len(s5), len(s6), v1, v2, v3, v4, t2.n, v5, v6, v7, v8, v9, x, y,
		arr[0])
	println(a,
		b,
		c)
	println(
		a, b)
	{
		a++
		// trailing in a block
	}
	switch a {
	case 1,
		2,
		3:
		a--
	// before a case
	case 4:
	}
	return a
}

func main() {
	x, y := two()
	a, b, c := three()
	d, e := four()
	println(g(1, 2, 3), x, y, a, b, c, d, e)
}
`

// commentsBeforeClosers is a comment on a line of its own ahead of a token that
// stands one level out: what is ahead of a closing brace is the last thing of
// what the braces hold, and what is ahead of a "case" or a closing parenthesis is
// the token's where it was written in the token's column.
const commentsBeforeClosers = `const (
	a = 1
	// b = 2, with the specs
)

var (
// only a comment
)

type T struct {
	x int
	// y int
}

type I interface {
	M()
	// N()
}

func h(a, b int) int { return a + b }

func e() {
	// nothing
}

func g(n int) int {
	v := h(
		1,
		2,
		// with the arguments
	)
	w := h(
		1,
		2,
	// with the parenthesis
	)
	switch n {
	// before the first
	case 1:
		n++
		// with the body
	case 2:
		n--
	// with the case
	case 3:
		// with an empty body
	case 4:
	// with the case, the body empty
	case 5:
		n++
		// one of two, with the body
	// two of two, with the case
	case 6:
		n++
	// one of two, with the case
	// two of two, further in
	default:
		n++
		// last, with the body
	}
	switch n {
	case 7:
		// last, with the case's column
	}
	ch := make(chan int, 1)
	select {
	case ch <- 1:
		n++
		// with the body
	case x := <-ch:
		n += x
	// with the case
	default:
		// last
	}
	for n < 10 {
		n++
		// the loop's last
	}
	if n > 3 {
		n++
		// the if's last
	} else {
		n--
		// the else's last
	}
	f := func() {
		n++
		// the literal's last
	}
	f()
	s := []int{
		1,
		// the elements' last
	}
outer:
	for {
		// before a break
		break outer
	}
	return v + w + len(s)
	// after the return
}

func main() {
	e()
	println(g(1), a)
}
`

// TestFormatContinuation pins the indentation of what continues a line, which
// before 2026-09-28 came back at the level of the statement it continued in every
// position but a call's arguments and a literal's body (p2-11's OCTOGO.md: "ogo
// fmt takes the indentation from the second line of an expression"). Each program
// is formatted as it stands, which must change nothing, and with every line's
// indentation taken away, which must give it all back -- but for the comments,
// whose place gofmt reads off the column they were written in.
func TestFormatContinuation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		src     string
		rebuild bool // the layout is a function of the line breaks alone
	}{
		{"operands", continuationOperands, true},
		{"lists", continuationLists, true},
		{"comments", commentsBeforeClosers, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			formatCheck(t, tc.src, tc.src)
			if tc.rebuild {
				var bare []string
				for _, line := range strings.Split(tc.src, "\n") {
					bare = append(bare, strings.TrimLeft(line, "\t"))
				}
				formatCheck(t, strings.Join(bare, "\n"), tc.src)
			}
			gofmtCheck(t, tc.src)
		})
	}
}

// The programs below are in gofmt's layout too, and are about its COLUMNS: what
// gofmt aligns is what go/printer ends with a tab, in runs of lines that
// text/tabwriter makes columns of, and what it does not align is what go/printer
// broke the line of with a form feed. TestFormatAlignment checks each against
// gofmt where there is one.

// alignDeclarations is the fields of a struct, the specs of a grouped
// declaration, the keys of a literal and the comments of a list of values.
const alignDeclarations = `type M struct {
	R [8]uint16 // the registers

	now     uint32 // how far
	psw     uint16
	sp      [4]uint16 // the stack pointers
	traps   uint16    // the traps
	waiting bool
	halted  bool // a HALT

	a int
	// a comment line
	bbbbbb string
	cc, dd int             // two names
	P                      // embedded
	e      func(a int) int // a function
}

type P struct {
	x, y int
}

type I interface {
	Get() int // reads
	Set(v int)
	Length() int // measures
	Cap() int    // too
}

const (
	controlReset = iota
	write
	read
	check // compares
	seek
	readCheck  // reads
	driveReset // resets
	lock       // the drive
)

const (
	k1    = 1 // one
	k22   = 22
	k333  = 333  // three
	k4444 = 4444 // four
)

const (
	t1    uint8  = 1  // one
	t22          = 22 // two
	t333  uint16 = 333
	t4444        = 4444 // four
	t5           = 5
)

var (
	v1     int // one
	v22    string
	v333       = 3 // three
	v4444  int = 4 // four
	v5, v6 int = 5, 6
	v7         = []int{
		1,
	}
	v8  = 8  // eight
	v99 = 99 // ninety-nine
)

type (
	A   int
	BB  string // a string
	CCC struct {
		x int
	}
	D bool
)

type order struct {
	function, control, pack, block, words int
	memory                                uint32
	here                                  bool
	p                                     P
}

var once = [...]uint16{
	0o012706, 0o001000, // 1000
	0o012737, 0o001034, 0o000100, // 1004
	0o012737, 0o000300, 0o000102, // 1012
	0o005003,                     // 1020
	0o012737, 0o000100, 0o177546, // 1022
	0o000001,                               // 1030
	0o000000,                               // 1032
	0o005203, 0o005203, 0o005203, 0o005203, // 1034
	0o000002, // 1036
	1,        // short
	0o000002 + 0o000002 + 0o000002 + 0o000002 + 0o000002 + 0o000002 + 0o000002, // long
	2, // short again
}

func f(c, d int) order {
	o := order{
		function: c,
		control:  d,
		pack:     c + d,
		block:    1,
		words:    2,
		memory:   uint32(c&3)<<12 | uint32(d),
		here:     !(c > d),
	}
	o2 := order{
		function: c, // what to do
		control:  d,
		pack:     c + d, // which pack
		block:    1,     // which block
		p: P{
			x: 1,
			y: 2,
		},
		words:  2,
		memory: 3,
	}
	o3 := order{function: c, control: d,
		pack:  1,
		block: 2}
	m := [...]int{
		1:   10,
		20:  200,
		300: 3000,
	}
	o4 := order{
		function: c,

		control: d,
		words:   1,
	}
	o5 := order{
		function: c,
		// the control
		control: d,
		words:   1,
	}
	o6 := order{
		p:     P{x: 1, y: 2},
		words: 1, here: true,
		memory: 3,
		block:  4,
	}
	return order{function: o.pack + o2.pack + o3.pack + m[1] + o4.words + o5.words + o6.words}
}

func main() {
	println(f(1, 2).function, len(once), k1, k22, k333, k4444, t1, t22, t333, t4444, t5, v1, v22, v333, v4444, v5, v6, len(v7), v8, v99)
	println(controlReset, write, read, check, seek, readCheck, driveReset, lock)
}
`

// alignOthers is a field and a spec written across lines, which end a run; an
// embedded field; aliases; and a comment that is not a line comment.
const alignOthers = `type T struct {
	a    int
	bbbb struct {
		x int
	}
	cc     int
	dddddd int
}

type U struct {
	a    int // one
	bbbb func(
		x int,
	) int // two
	cc     int // three
	dddddd int // four
}

type V struct {
	only int // alone
}

type W struct {
	a, b int
	*T
	cc   int
	U        // embedded
	dddd int // named
	V
	e int
}

type (
	A   = int
	BB  = string // an alias
	CCC int
)

const (
	one = 1 // alone in its group
)

const (
	x1   = 1 << 3         // shifted
	x22  = 1<<3 + 2       // tight
	x333 = []int{1, 2}[0] // indexed
)

var (
	g1 = [2]int{
		1, 2,
	}
	g22  int
	g333 = 3
)

var (
	h1        int
	h22       = 2
	h333      string
	h4444, h5 = 4, 5
)

var (
	m1   = 1
	m22  = 22  /* general */
	m333 = 333 // line
)

func f() (int, int) {
	a := 1      // one
	bb := 22    // two
	if a < bb { // cond
		a++ // inc
	} // close
	ccc := 333         // three
	return a + ccc, bb /* both */
}

func main() {
	println(f())
}
`

// alignSections is where a run of trailing comments ends though the next line
// has one: at a case, at what continues an expression, at a parameter, after a
// statement of several lines -- and where it does not, between declarations.
const alignSections = `var (
	a = 1
)                 // the group
var bb = 2        // two
var ccc = 33      // three
func one() int    { return 1 } // a function
func eleven() int { return 11 }

var d = 4              // four
func twelve(a int) int { return 12 } // twelve
func thirteen() int {
	return 13
}         // closing
var e = 5 // five

type T struct{ n int }

func (t T) get() T         { return t }
func (t T) add(a, b int) T { return t } // adds

func p(
	a int, // first
	bbb int, // second
	cc int, // third
) int {
	return a
}

func g(n int, p, q, r bool) int {
	switch n {
	case 1: // one
	case 22: // twenty-two
	case 333: // three
		n++ // inc
	default: // the rest
		n-- // dec
	}
	v := p && // first
		q && // second
		r // third
	var t T
	t2 := t.
		get().     // one
		add(1, 2). // two
		get()      // three
	w := one() + // one
		eleven() // eleven
	println( // opens
		n, // first
		v, // second
		"a long string, longer than forty characters in all", // third
		w,    // fourth
		t2.n, // fifth
	)
	s := []int{ // opens
		1,  // one
		22, // two
	} // closes
	x := 1 // one
	if p { // cond
		x++
	} else if q { // second
		x--
	} else { // last
		x = 0
	}
	yy := 22                 // two
	for i := 0; i < 3; i++ { // loop
		x += i // adds
	}
	zzz := len(s) // three
	return x + yy + zzz
}

func h() (int, int, int) {
	return 1, // one
		22, // two
		333 // three
}

func main() {
	println(g(1, true, false, true), a, bb, ccc, d, e, p(1, 2, 3))
	println(h())
	println(twelve(1), thirteen())
}
`

// TestFormatAlignment pins the columns. Until 2026-09-28 a field's type, a spec's
// value, a one-line function's brace and a trailing comment each had a mechanism
// of its own and its own idea of a run, which agreed with gofmt on the run corpus
// and not on p2-11's sources; a literal's keys were not aligned at all. Each
// program is formatted as it stands, and with every run of blanks made one.
func TestFormatAlignment(t *testing.T) {
	blanks := regexp.MustCompile(` +`)
	for _, tc := range []struct{ name, src string }{
		{"declarations", alignDeclarations},
		{"others", alignOthers},
		{"sections", alignSections},
	} {
		t.Run(tc.name, func(t *testing.T) {
			formatCheck(t, tc.src, tc.src)
			formatCheck(t, blanks.ReplaceAllString(tc.src, " "), tc.src)
			gofmtCheck(t, tc.src)
		})
	}
}

// TestFormatBlankLines pins the blank lines gofmt puts in and takes out: one
// between two top-level declarations of different kinds and ahead of a comment on
// a line of its own, none after a declaration with a comment trailing it, and none
// at the end of the file -- where a comment was written TWICE, the walk having
// written what stands ahead of the end and the flush after it writing it again.
func TestFormatBlankLines(t *testing.T) {
	const in = `import "p2"
var a = 1
const b = 2
var c = 3
// before d, a line of its own
var d = 4
var e = 5 // trails e
const f = 6
var g = 7 // trails g
// before h
const h = 8
func one() int { return 1 }
func two() int { return 2 }
var i = 9
func three() int {
	return 3
}
var j = 10
type T int
func (t T) m() int { return int(t) }
/* a general comment */
var k = 11
var l = 12 /* trails l */
const m = 13
const (
	n = 14
)
const o = 15
var p = 16

// after a blank line
const q = 17
// doc of r

var r = 18
func main() {
	p2.WaitMs(1)
	var s = 1
	const t = 2
	// inside
	var u = 3
	println(a, b, c, d, e, f, g, h, i, j, k, l, m, n, o, p, q, r, s, t, u, one(), two(), three(), T(1).m())
}
`
	const want = `import "p2"

var a = 1

const b = 2

var c = 3

// before d, a line of its own
var d = 4
var e = 5 // trails e
const f = 6

var g = 7 // trails g
// before h
const h = 8

func one() int { return 1 }
func two() int { return 2 }

var i = 9

func three() int {
	return 3
}

var j = 10

type T int

func (t T) m() int { return int(t) }

/* a general comment */
var k = 11
var l = 12 /* trails l */
const m = 13
const (
	n = 14
)
const o = 15

var p = 16

// after a blank line
const q = 17

// doc of r

var r = 18

func main() {
	p2.WaitMs(1)
	var s = 1
	const t = 2
	// inside
	var u = 3
	println(a, b, c, d, e, f, g, h, i, j, k, l, m, n, o, p, q, r, s, t, u, one(), two(), three(), T(1).m())
}
`
	formatCheck(t, in, want)
	gofmtCheck(t, want)
	formatCheck(t, "var x = 1\n\n// the last line\n\n\n", "var x = 1\n\n// the last line\n")
	formatCheck(t, "var x = 1\n// the last line", "var x = 1\n\n// the last line\n")
}

// TestFormatResultParens pins the parentheses gofmt drops: those around one
// result with no name.
func TestFormatResultParens(t *testing.T) {
	const in = `type F func(a int) (int)

type G func() (int, error)

type H func() (n int)

type I interface {
	M() (int)
	N(a int) (b int)
}

func f() (int) { return 1 }

func g() (int, int) { return 1, 2 }

func h() ([]int) { return nil }

func k() (func() (int)) { return f }

func main() {
	var x func() (F)
	println(f(), x == nil)
}
`
	const want = `type F func(a int) int

type G func() (int, error)

type H func() (n int)

type I interface {
	M() int
	N(a int) (b int)
}

func f() int { return 1 }

func g() (int, int) { return 1, 2 }

func h() []int { return nil }

func k() func() int { return f }

func main() {
	var x func() F
	println(f(), x == nil)
}
`
	formatCheck(t, in, want)
	gofmtCheck(t, want)
}

// gofmtCheck fails where src is not what gofmt makes of it. It checks nothing on
// a machine with no gofmt.
func gofmtCheck(t *testing.T, src string) {
	t.Helper()
	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		return
	}
	goSrc := filepath.Join(t.TempDir(), "x.go")
	if err := os.WriteFile(goSrc, []byte("package main\n\n"+src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(gofmt, goSrc).Output()
	if err != nil {
		t.Fatalf("gofmt: %v", err)
	}
	if g := string(bytes.TrimPrefix(out, []byte("package main\n\n"))); g != src {
		t.Errorf("the expectation is not gofmt's layout:\n%s", firstDiff(src, g))
	}
}

// TestFormatLineBreaks pins the lines gofmt makes of what is written on one: a
// statement stands on a line of its own, and so does a declaration, a spec, a
// field and a case, and a block's braces enclose lines. Until 2026-09-28 `ogo
// fmt` kept a body on the line it was written on, "if c { v = 1 }", and a
// statement after a semicolon beside the one before it.
//
// The one body gofmt leaves on its line is a function's where it is short, and
// the one type a struct or an interface of a single short field; the second
// program is go/printer's arithmetic for "short", a case either side of each
// number: thirty columns for a field, its names counting for one and a method's
// signature for four more than it has; a hundred for a function literal's header
// and statements and ninety-nine for a declaration's.
func TestFormatLineBreaks(t *testing.T) {
	const in = `type P struct{ x, y int }

type x int

type Q struct{ id int; data [4]int }

type R struct{ n int /* count */ }

type S struct{ veryLongFieldNameHere [16][]func(a, b int) (int, error) }

type S2 struct{ f [16][]func(a, b int) (int, bool) }

type S3 struct{ f [16][]func(a, b int) (int, bool, x) }

type I interface{ M() int }

type J interface{ M() int; N() }

type E struct{}

const ( a = 1; b = 2 )

var ( c = 3 )

var d = 4; var e = 5

func one() int { return 1 }

func two(v int) int { v++; v--; return v }

func six(v int) int { v++; v++; v++; v++; v++; return v }

func blk(v int) int { if v > 0 { return 1 }; return 0 }

func lit() func() int { return func() int { return 1 } }

func lit2() func() int { return func() int { if a > 0 { return 1 }; return 0 } }

func empty() {}

func long(aaaaaaaaaaaaaaaaaaaaaaaa, bbbbbbbbbbbbbbbbbbbbbbbbbbbbb int) int { return aaaaaaaaaaaaaaaaaaaaaaaa + bbbbbbbbbbbbbbbbbbbbbbbbbbbbb*2 }

func fits(aaaaaaaaaaaaaaaaaaaaaaaa, bbbbbbbbbbbbbbbbbbbbbbbbbbbb int) int { return aaaaaaaaaaaaaaaaaaaaaaaa + bbbbbbbbbbbbbbbbbbbbbbbbbbbb*2 }

func g(v int, ch chan int) int {
	if v > 0 { v = 1 }
	if v > 1 { v = 2 } else { v = 3 }
	if v > 1 { v = 2 } else if v > 0 { v = 4 } else { v = 3 }
	for v < 10 { v++ }
	for {}
	for i := 0; i < 3; i++ { v += i; v-- }
	switch v { case 1: v = 2; case 3, 4: v = 5; v++; default: }
	switch { }
	select { case x := <-ch: v = x; default: v = 0 }
	{ v++ }
	{}
	a := 1; b := 2; v += a + b;
	f := func() { v++ }
	h := func() { if v > 0 { v++ } }
	k := func() {}
	f(); h(); k()
outer: for { break outer }
	go func() { v++ }()
	defer func() { if v > 0 { v-- } }()
	x := struct{ a int; b string }{1, "b"}
	y := struct{ a int }{1}
	return v + x.a + y.a
}

func main() { println(one(), two(1), six(1), blk(1), lit()(), lit2()(), a, b, c, d, e); empty(); println(g(1, nil), long(1, 2), fits(1, 2)) }
`
	const want = `type P struct{ x, y int }

type x int

type Q struct {
	id   int
	data [4]int
}

type R struct {
	n int /* count */
}

type S struct {
	veryLongFieldNameHere [16][]func(a, b int) (int, error)
}

type S2 struct {
	f [16][]func(a, b int) (int, bool)
}

type S3 struct {
	f [16][]func(a, b int) (int, bool, x)
}

type I interface{ M() int }

type J interface {
	M() int
	N()
}

type E struct{}

const (
	a = 1
	b = 2
)

var (
	c = 3
)

var d = 4
var e = 5

func one() int { return 1 }

func two(v int) int { v++; v--; return v }

func six(v int) int {
	v++
	v++
	v++
	v++
	v++
	return v
}

func blk(v int) int {
	if v > 0 {
		return 1
	}
	return 0
}

func lit() func() int { return func() int { return 1 } }

func lit2() func() int {
	return func() int {
		if a > 0 {
			return 1
		}
		return 0
	}
}

func empty() {}

func long(aaaaaaaaaaaaaaaaaaaaaaaa, bbbbbbbbbbbbbbbbbbbbbbbbbbbbb int) int {
	return aaaaaaaaaaaaaaaaaaaaaaaa + bbbbbbbbbbbbbbbbbbbbbbbbbbbbb*2
}

func fits(aaaaaaaaaaaaaaaaaaaaaaaa, bbbbbbbbbbbbbbbbbbbbbbbbbbbb int) int {
	return aaaaaaaaaaaaaaaaaaaaaaaa + bbbbbbbbbbbbbbbbbbbbbbbbbbbb*2
}

func g(v int, ch chan int) int {
	if v > 0 {
		v = 1
	}
	if v > 1 {
		v = 2
	} else {
		v = 3
	}
	if v > 1 {
		v = 2
	} else if v > 0 {
		v = 4
	} else {
		v = 3
	}
	for v < 10 {
		v++
	}
	for {
	}
	for i := 0; i < 3; i++ {
		v += i
		v--
	}
	switch v {
	case 1:
		v = 2
	case 3, 4:
		v = 5
		v++
	default:
	}
	switch {
	}
	select {
	case x := <-ch:
		v = x
	default:
		v = 0
	}
	{
		v++
	}
	{
	}
	a := 1
	b := 2
	v += a + b
	f := func() { v++ }
	h := func() {
		if v > 0 {
			v++
		}
	}
	k := func() {}
	f()
	h()
	k()
outer:
	for {
		break outer
	}
	go func() { v++ }()
	defer func() {
		if v > 0 {
			v--
		}
	}()
	x := struct {
		a int
		b string
	}{1, "b"}
	y := struct{ a int }{1}
	return v + x.a + y.a
}

func main() {
	println(one(), two(1), six(1), blk(1), lit()(), lit2()(), a, b, c, d, e)
	empty()
	println(g(1, nil), long(1, 2), fits(1, 2))
}
`
	formatCheck(t, in, want)
	gofmtCheck(t, want)

	const limits = `type x int

type tyyyyyyyyyyyyyyyyyyyyyyyy int

type A28 struct{ f [4]tyyyyyyyyyyyyyyyyyyyyyyyy }

type tyyyyyyyyyyyyyyyyyyyyyyyyy int

type A29 struct{ f [4]tyyyyyyyyyyyyyyyyyyyyyyyyy }

type tyyyyyyyyyyyyyyyyyyyyyyyyyy int

type A30 struct{ f [4]tyyyyyyyyyyyyyyyyyyyyyyyyyy }

type tyyyyyyyyyyyyyyyyyyyyyyyyyyy int

type A31 struct{ f [4]tyyyyyyyyyyyyyyyyyyyyyyyyyyy }

type emmmmmmmmmmmmmmmmmmmmmmmmmmmm int

type E29 struct{ emmmmmmmmmmmmmmmmmmmmmmmmmmmm }

type emmmmmmmmmmmmmmmmmmmmmmmmmmmmm int

type E30 struct{ emmmmmmmmmmmmmmmmmmmmmmmmmmmmm }

type emmmmmmmmmmmmmmmmmmmmmmmmmmmmmm int

type E31 struct{ emmmmmmmmmmmmmmmmmmmmmmmmmmmmmm }

type I28 interface{ M(pppppppppppppppppp int) int }

type I29 interface{ M(ppppppppppppppppppp int) int }

type I30 interface{ M(pppppppppppppppppppp int) int }

type I31 interface{ M(ppppppppppppppppppppp int) int }

func f99(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { return 1 }

func f100(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { return 1 }

func f101(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { return 1 }

func g99(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) {}

func g100(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) {}

func g101(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) {}

func h100(v, qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { v++; return v }

func h101(v, qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { v++; return v }

var l100 = func(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { return 1 }

var l101 = func(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { return 1 }

type K24 interface{ M(pppppppppppppp int) int }

type L24 interface{ MuchLongerName(pppppppppppppp int) int }

type K25 interface{ M(ppppppppppppppp int) int }

type L25 interface{ MuchLongerName(ppppppppppppppp int) int }

type K26 interface{ M(pppppppppppppppp int) int }

type L26 interface{ MuchLongerName(pppppppppppppppp int) int }

type K27 interface{ M(ppppppppppppppppp int) int }

type L27 interface{ MuchLongerName(ppppppppppppppppp int) int }

func main() {}
`
	const wantLimits = `type x int

type tyyyyyyyyyyyyyyyyyyyyyyyy int

type A28 struct{ f [4]tyyyyyyyyyyyyyyyyyyyyyyyy }

type tyyyyyyyyyyyyyyyyyyyyyyyyy int

type A29 struct{ f [4]tyyyyyyyyyyyyyyyyyyyyyyyyy }

type tyyyyyyyyyyyyyyyyyyyyyyyyyy int

type A30 struct {
	f [4]tyyyyyyyyyyyyyyyyyyyyyyyyyy
}

type tyyyyyyyyyyyyyyyyyyyyyyyyyyy int

type A31 struct {
	f [4]tyyyyyyyyyyyyyyyyyyyyyyyyyyy
}

type emmmmmmmmmmmmmmmmmmmmmmmmmmmm int

type E29 struct{ emmmmmmmmmmmmmmmmmmmmmmmmmmmm }

type emmmmmmmmmmmmmmmmmmmmmmmmmmmmm int

type E30 struct{ emmmmmmmmmmmmmmmmmmmmmmmmmmmmm }

type emmmmmmmmmmmmmmmmmmmmmmmmmmmmmm int

type E31 struct {
	emmmmmmmmmmmmmmmmmmmmmmmmmmmmmm
}

type I28 interface {
	M(pppppppppppppppppp int) int
}

type I29 interface {
	M(ppppppppppppppppppp int) int
}

type I30 interface {
	M(pppppppppppppppppppp int) int
}

type I31 interface {
	M(ppppppppppppppppppppp int) int
}

func f99(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { return 1 }

func f100(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int {
	return 1
}

func f101(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int {
	return 1
}

func g99(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) {}

func g100(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) {
}

func g101(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) {
}

func h100(v, qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int {
	v++
	return v
}

func h101(v, qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int {
	v++
	return v
}

var l100 = func(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int { return 1 }

var l101 = func(qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq int) int {
	return 1
}

type K24 interface{ M(pppppppppppppp int) int }

type L24 interface{ MuchLongerName(pppppppppppppp int) int }

type K25 interface{ M(ppppppppppppppp int) int }

type L25 interface{ MuchLongerName(ppppppppppppppp int) int }

type K26 interface {
	M(pppppppppppppppp int) int
}

type L26 interface {
	MuchLongerName(pppppppppppppppp int) int
}

type K27 interface {
	M(ppppppppppppppppp int) int
}

type L27 interface {
	MuchLongerName(ppppppppppppppppp int) int
}

func main() {}
`
	formatCheck(t, limits, wantLimits)
	gofmtCheck(t, wantLimits)

	// An empty select is the one statement whose braces stay together.
	formatCheck(t, "func main() {\n\tselect {}\n}\n", "func main() {\n\tselect {}\n}\n")
}
