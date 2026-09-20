// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"io"
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
		// A unary sign beside a binary operator, in every spelling gofmt writes
		// tight -- `v%-1`, `v*-2`, `v&^-5`, `a[v*-1+7]` -- and the two it does not:
		// `- -v` and `v - -4`, where the pair would run into `--`. This case is in
		// the corpus for the FORMATTER as much as for the compiler
		// (TestFormatMatchesGofmt reads these programs): `ogo fmt` used to write
		// `- -v` as `--v`, which the parser then refused, so formatting a valid
		// program produced one that would not build.
		name: "a unary sign beside a binary operator",
		src: `func g(a, b int) int { return a - b }

func main() {
	v := 7
	w := 3
	a := [8]int{1, 2, 3, 4, 5, 6, 7, 8}
	println(v%-1, v*-2, v+-3, v&^-5, v/-6, v|-2)
	println(g(v%-1, v*-2), g(v&^-5, v>>1))
	println(a[v-1], a[-v+7], a[v*-1+7])
	println(- -v, -(-v), - -w+1)
	println(v - -4)
	x := v % -1
	y := v * -2
	println(x, y, v-(-w))
}
`,
		want: "0 -14 4 4 -1 -1\n14 1\n7 1 1\n7 7 4\n11\n0 -14 10\n",
	},
	{
		// `x % 1` is ZERO in Go, whatever x is: a remainder is smaller than its
		// divisor. The target's C compiler answers x instead, for every integer type
		// up to 32 bits (doc/modulo-by-one-returns-the-dividend.c), so the operation
		// is written as the multiplication by zero it is. Every type, both
		// spellings, the compound form, and a loop where the divisor is what a
		// constant folded to.
		name: "modulo by one is zero",
		src: `type Narrow int8

var seed int = -118

func main() {
	v := seed
	neg := -1
	println("int", v%1, v%neg, v%2, v%3)
	var z Narrow = -118
	var negN Narrow = -1
	println("int8", int(z%1), int(z%negN))
	var u uint32 = 118
	println("uint32", int(u%1), int(u%7))
	var w int64 = -118
	println("int64", int(w%1), int(w%3))
	x := seed
	x %= 1
	y := seed
	y = y % 1
	println("assigned", x, y)
	var c int16 = 300
	c %= 1
	println("int16", int(c), 118%1)
	n := 0
	for i := 0; i < 3; i++ {
		n += i % 1
	}
	println("loop", n)
}
`,
		want: "int 0 0 0 -1\nint8 0 0\nuint32 0 6\nint64 0 -1\nassigned 0 0\nint16 0 0\nloop 0\n",
	},
	{
		// The bitwise complement of a 64-bit value: the target's C compiler computes
		// `~` wrong in its HIGH word (doc/complement64-high-word.c), which reached
		// `x &^= K` for a wide constant K -- the one complement the emitter still
		// spelled with `~`, every other having taken the long form for years. On a
		// P2 it left x unchanged where Go clears the bits. Found by the oracle
		// fuzzer the day int64 entered the generator.
		name: "the complement of a 64-bit constant",
		src: `var seed int64 = 3869336025161645586

func main() {
	v := -seed
	var z int64 = -9223372036854775808
	println("unary", int(^v>>32), int(^v))
	var w int64 = -3869336025161645586
	println("expr", int((z&^w)>>32), int((z&^1)>>32))
	z &^= -3869336025161645586
	println("assigned", int(z>>32), int(z))
	var q int64 = -1
	q &^= 6635603843458558513
	println("positive", int(q>>32), int(q))
	var u uint64 = 0xF0F0F0F0F0F0F0F0
	u &^= 0xFF00FF00FF00FF00
	println("unsigned", int(u>>32), int(u))
	var n int32 = -118
	n &^= 1431655765
	println("narrow", int(n))
}
`,
		want: "unary 900899997 1080147473\nexpr 0 -2147483648\nassigned 0 0\npositive -1544971914 409966030\nunsigned 15728880 15728880\nnarrow -1431655798\n",
	},
	{
		// Calling what a call RETURNED, `pick()(3)`. The value is the second call's,
		// which is what neither side knew: the checker counted the first callee's
		// results ("2 variables but pick2 returns 1 value", of a second call that
		// returns exactly two) and the emitter could not type the declaration --
		// while `var a int = pick1()(4)`, which asks nothing, always worked. Every
		// position: printed, declared, assigned, an argument, destructured, and
		// discarded, with a method value as the returned function too.
		name: "calling what a call returned",
		src: `type Counter struct {
	n int32
}

func (c *Counter) Next() (int32, bool) {
	c.n++
	return c.n, c.n <= 2
}

var g Counter

func dbl(v int) int { return v * 2 }

func two(v int) (int, bool) { return v * 2, true }

func pick1() func(int) int { return dbl }

func pick2() func(int) (int, bool) { return two }

func nexter() func() (int32, bool) { return g.Next }

func main() {
	println(pick1()(3))
	a := pick1()(4)
	println(a, dbl(pick1()(5)))
	b, ok := pick2()(6)
	println(b, ok)
	var c int
	c, ok = pick2()(7)
	println(c, ok)
	v, more := nexter()()
	println(v, more)
	pick2()(8)
}
`,
		want: "6\n8 20\n12 true\n14 true\n1 true\n",
	},
	{
		// A METHOD value of several results, `m := g.Next`, which was refused: the
		// value could not be typed and the lift said so. It takes the same void
		// wrapper a plain function value of several results takes, with the receiver
		// bound into it -- so it is still one word, and still points at something
		// that returns nothing (see funcSigCParts). Held in a package variable, a
		// struct field and a local, passed as an argument, and taken again on
		// another cog, which is where a struct returned through a pointer would have
		// taken the program down.
		name: "a method value with several results",
		src: `type Counter struct {
	n int32
}

func (c *Counter) Next() (int32, bool) {
	c.n++
	if c.n > 3 {
		return 0, false
	}
	return c.n, true
}

var g Counter
var held func() (int32, bool)
var ch chan int32

type Box struct {
	fn func() (int32, bool)
}

var box Box

func drain(f func() (int32, bool)) int32 {
	sum := int32(0)
	for {
		v, ok := f()
		if !ok {
			return sum
		}
		sum += v
	}
}

func worker(c chan int32) {
	m := g.Next
	v, _ := m()
	c <- v
	w, _ := held()
	c <- w
}

func main() {
	held = g.Next
	box.fn = g.Next
	a, ok := held()
	println("pkg", a, ok)
	b, ok2 := box.fn()
	println("field", b, ok2)
	g.n = 0
	println("drain", drain(g.Next))
	g.n = 0
	go worker(ch)
	println("cog", <-ch, <-ch)
}
`,
		want: "pkg 1 true\nfield 2 true\ndrain 6\ncog 1 2\n",
	},
	{
		// A function value of SEVERAL results, in every position it can be held in:
		// a package variable, a struct field, a local, an argument, a function's
		// result, and on another cog. What such a value points at is a void WRAPPER
		// that writes the results through a leading out parameter, because a
		// function POINTER returning a struct is what the target's C compiler cannot
		// match against the function assigned to it -- it warned on every such
		// assignment -- and calling through one on a spawned cog corrupts the
		// program outright (doc/struct-return-through-pointer-on-cog.c). The cog is
		// what the last three lines exercise.
		name: "a function value with several results",
		src: `type Box struct {
	fn func(int32) (int32, bool)
	n  int
}

var box Box
var pkgFn func(int32) (int32, bool)
var ch chan int32

func two(v int32) (int32, bool) { return v * 2, true }

func none(v int32) (int32, bool) { return 0, false }

func pick(which int) func(int32) (int32, bool) {
	if which == 0 {
		return two
	}
	return none
}

func apply(f func(int32) (int32, bool), v int32) int32 {
	r, ok := f(v)
	if !ok {
		return -1
	}
	return r
}

func worker(c chan int32) {
	f := pick(0)
	v, _ := f(4)
	c <- v
	g := box.fn
	w, _ := g(5)
	c <- w
	h := pkgFn
	x, _ := h(6)
	c <- x
}

func main() {
	pkgFn = two
	box.fn = two
	a, ok := pkgFn(3)
	println("pkg", a, ok)
	b, ok2 := box.fn(3)
	println("field", b, ok2)
	local := two
	c, ok3 := local(7)
	println("local", c, ok3)
	println("arg", apply(two, 5), apply(local, 6))
	picked := pick(1)
	d, ok4 := picked(8)
	println("picked", d, ok4)
	go worker(ch)
	println("cog", <-ch, <-ch, <-ch)
	local(9)
}
`,
		want: "pkg 6 true\nfield 6 true\nlocal 14 true\narg 10 12\npicked 0 false\ncog 8 10 12\n",
	},
	{
		// An interface method with SEVERAL results, `Next() (int32, bool)`, which was
		// refused outright. The values travel in the struct a direct call to such a
		// method already returns -- but the slot WRITES THROUGH a trailing pointer
		// rather than returning it, because a struct with padding comes back wrong
		// from a call through a function pointer on a spawned cog, and takes the
		// program down with it (doc/struct-return-through-pointer-on-cog.c). So the
		// case is run on both: on this cog through `drain`, and on another through
		// `feed`, which is where the fault would show.
		name: "an interface method with several results",
		src: `type Source interface {
	Next() (int32, bool)
	Name() string
}

type Sweep struct {
	Data [3]int32
	At   int
}

func (s *Sweep) Next() (int32, bool) {
	if s.At >= len(s.Data) {
		return 0, false
	}
	v := s.Data[s.At]
	s.At++
	return v, true
}

func (s *Sweep) Name() string { return "sweep" }

var src Sweep
var iface Source
var ch chan int32

func drain(s Source) int32 {
	sum := int32(0)
	for {
		v, ok := s.Next()
		if !ok {
			return sum
		}
		sum += v
	}
}

func feed(c chan int32) {
	for {
		v, ok := iface.Next()
		if !ok {
			c <- -1
			return
		}
		c <- v
	}
}

func main() {
	src.Data = [3]int32{2, 3, 4}
	iface = &src
	println(drain(iface), iface.Name())
	src.At = 0
	go feed(ch)
	sum := int32(0)
	for {
		v := <-ch
		if v < 0 {
			break
		}
		sum += v
	}
	println("cog", sum)
	src.At = 1
	v, ok := iface.Next()
	println(v, ok, src.At)
}
`,
		want: "9 sweep\ncog 9\n3 true 2\n",
	},
	{
		// A compound assignment as the post statement of a three-clause for --
		// `i += 2`, `i /= 2`, `i <<= 1`, `i &^= 4`, `f += 0.5` -- which the grammar
		// did not admit: ForPost took "=", ":=", "++" and "--" and nothing else, so
		// the commonest way to step by two was "expected '{'". It is lowered as the
		// compound assignment statement is, guards included, minus the terminator.
		name: "a compound assignment as a for post statement",
		src: `func main() {
	n := 0
	for i := 0; i < 10; i += 2 {
		n++
	}
	for i := 64; i > 0; i /= 2 {
		n += 10
	}
	for i := 1; i < 100; i <<= 1 {
		n += 100
	}
	for i := 15; i != 0; i &^= 1 << 2 {
		n += 1000
		if n > 10000 {
			break
		}
	}
	for i := 10; i > 0; i -= 3 {
		n += 10000
	}
	for f := 0.5; f < 2; f += 0.5 {
		n += 100000
	}
	println(n)
}
`,
		want: "350775\n",
	},
	{
		// A float constant -- of a defined type with methods, an untyped one, a
		// float32 one, a negative one -- in every position: sent, appended, a
		// deferred argument, a switch tag and a case, in literals at both levels, a
		// method's receiver alone and at the head of a chain, converted, compared,
		// on either side of a variable, a variadic argument, a range and a
		// three-clause bound, a compound target, a field, through a pointer, a
		// select, printed. A float constant is now inlined at each use and declares
		// nothing, and `(+Neg).Abs()` drops its unary plus, which the target's C
		// compiler cannot lower on a double (doc/unary-plus-float.c). The values are
		// exact in float32, so the same output is right on the board, where float64
		// is 32 bits wide. The main is split in three for the cog's 480 longs.
		name: "a float constant in every position",
		src: `type Temp float64

type F32 float32

const Boil Temp = 100.5
const Neg Temp = -2.5
const Pi = 3.25
const Two = 2.0
const Half = .5
const E2 = 1e2
const K32 F32 = 0.75
const Big = 1 << 40
const Z = 3.5
const Zi = 7 / 2

type P struct {
	t Temp
	n int
}

var gt = Boil * 2
var garr = [2]Temp{Neg, Boil}
var gp = P{t: Neg, n: int(Two)}
var gf float64 = Big
var gf32 F32 = K32

func (t Temp) Int() int { return int(t) }

func (t Temp) Half() Temp { return t / 2 }

func (t Temp) Abs() Temp {
	if t < 0 {
		return -t
	}
	return t
}

func take(ts ...Temp) Temp {
	s := Temp(0)
	for _, t := range ts {
		s += t
	}
	return s
}

func deferred(t Temp) { printf("deferred %v\n", t) }

func takesF(f float64) float64 { return f * 2 }

func sender(ch chan Temp) { ch <- Boil }

func worker(ch chan Temp, t Temp) { ch <- t.Half() }

func partA() {
	var ch chan Temp
	go sender(ch)
	got := <-ch
	printf("recv %v %d\n", got, got.Int())
	go worker(ch, Neg)
	printf("worker %v\n", <-ch)
	var ts []Temp = make([]Temp, 0, 4)
	ts = append(ts, Boil, Neg)
	ts = append(ts, Temp(Two))
	printf("append %d %v %v %v\n", len(ts), ts[0], ts[1], ts[2])
	defer deferred(Neg)
	switch Boil {
	case Neg:
		println("switch: neg")
	case Boil:
		println("switch: boil")
	}
	switch v := Neg; v {
	case Boil + Neg:
		println("case: sum")
	case Neg:
		println("case: neg")
	}
	neg := -Boil
	printf("unary %v %v %v\n", neg, (+Neg).Abs(), -Neg)
	p := P{t: Boil, n: int(Two)}
	arr := [2]Temp{Boil, Neg}
	printf("lits %v %d %v %v %v %v %v %d %v\n", p.t, p.n, arr[0], arr[1], gt, garr[0], gp.t, gp.n, gf32)
}

func partB() {
	printf("conv %v %v %d %d %v %v %v\n", float32(Boil), float64(Neg), int(Two), int64(Boil*2), Temp(3), uint8(Two), F32(Pi))
	var i int = Two
	var n int = 3 * Two
	printf("untyped %d %d %v %v %v %v %v\n", i, n, gf/1073741824, Z, Zi, 7/2.0, float64(7)/2)
	x := [4]int{1, 2, 3, 4}
	ms := make([]int, 2)
	printf("index %d %d %d %d\n", x[Two], x[Two:][0], 1<<Two, len(ms))
	printf("cmp %t %t %t %t %t %t\n", Boil > Neg, Boil == Temp(100.5), Neg < 0, Pi > 3, K32 < 1, Half*2 == Two)
	var t Temp = 7
	var g F32 = K32
	printf("mixed %v %v %v %v %v %v %t\n", t*3, 3*t, t/4, 14/t, g*2, 2*g, g == K32)
	m := 3
	printf("order %v %v %v %v\n", float64(m)*Pi, Pi*float64(m), Boil*Temp(m), Temp(m)*Boil)
	printf("take %v %v\n", take(Boil, Neg), takesF(Pi)+takesF(Two)+takesF(2))
}

func partC() {
	twice := Boil + Boil
	for i := range 3 {
		v := Temp(i) * Boil
		if v == twice {
			println("range", i)
		}
	}
	cnt := 0
	for f := Half; f < Two; f = f + Half {
		cnt++
	}
	println("for", cnt)
	var x Temp = Boil
	x += Neg
	x -= Two
	x *= 2
	x /= 4
	printf("compound %v %d\n", x, x.Int())
	p := P{}
	p.t = Boil
	p.t += Neg
	pt := &p.t
	*pt = *pt + Two
	*pt *= Two
	printf("field %v %v\n", p.t, gp.t.Abs())
	var ch chan Temp
	go sender(ch)
	select {
	case v := <-ch:
		printf("select %v\n", v.Half())
	}
	printf("chain %d %v %d %v\n", Boil.Int(), Boil.Half(), Boil.Half().Int(), Neg.Abs().Half())
	printf("%v %v %v %.2f %d\n", Boil, Neg, K32, Pi, Boil.Int())
	printf("%v %v %v\n", E2, Half, -Pi)
}

func main() {
	partA()
	partB()
	partC()
}
`,
		want: "recv 100.5 100\nworker -1.25\nappend 3 100.5 -2.5 2\nswitch: boil\ncase: neg\nunary -100.5 2.5 2.5\nlits 100.5 2 100.5 -2.5 201 -2.5 -2.5 2 0.75\ndeferred -2.5\nconv 100.5 -2.5 2 201 3 2 3.25\nuntyped 2 6 1024 3.5 3 3.5 3.5\nindex 3 3 4 2\ncmp true true true true true false\nmixed 21 21 1.75 2 1.5 1.5 true\norder 9.75 9.75 301.5 301.5\ntake 98 14.5\nrange 2\nfor 3\ncompound 48 48\nfield 200 2.5\nselect 50.25\nchain 100 50.25 50 1.25\n100.5 -2.5 0.75 3.25 100\n100 0.5 -3.25\n",
	},
	{
		// Untyped constant arithmetic as Go defines it. `7 / 2.0` is 3.5, the
		// untyped FLOAT kind winning over the int one whichever side it is on; the
		// first operand used to decide, so `7 / 2.0` was 3 and `2 * 3.5` a double
		// printed as an int. A constant expression is evaluated EXACTLY: 0.1 + 0.2 is
		// three tenths, so `0.1+0.2 == 0.3` is true, as is `1/3.0*3 == 1`; handed to
		// C as written, both were computed in doubles and false. A constant beside
		// a float32 operand is a float32, `f == 0.3` comparing two of them, where C
		// promoted f and compared it with the double 0.3. And an integral float
		// constant serves where an integer is wanted -- an index, a shift count, a
		// make size -- spelled as the integer it is: `1 << Two` handed the shift
		// helper a double, which the target's C compiler converts to its int64_t by
		// the bits, and shifted by that.
		name: "untyped constant arithmetic is exact and takes the wider kind",
		src: `type Temp float64

const Z = 7 / 2.0
const W = 7.0 / 2
const X = 0.1 + 0.2
const F float32 = 0.1
const Two = 2.0
const Neg Temp = -2.5

func (t Temp) Abs() Temp {
	if t < 0 {
		return -t
	}
	return t
}

func main() {
	x := 7 / 2.0
	r := 1 + 'a'
	var f float64 = 7 / 2
	var g float64 = Z * 2
	printf("%v %v %v %v %v %v %v %v\n", Z, W, x, f, g, 9/2.0, 2*3.5, 1<<2.0)
	printf("%T %v %T %v %T\n", r, r, 7/2.0, 7/2.0, 1<<2.0)
	println(0.1+0.2 == 0.3, 1/3.0*3 == 1, X == 0.3, X+0.1 == 0.4, 0.3 == 0.1+0.2)
	var s float32 = 0.3
	var t float32 = 0.1
	println(s == 0.3, F*3 == 0.3, t*3 == 0.3, t+0.2 == 0.3, s > 0.29, 0.3 == s)
	a := [4]int{1, 2, 3, 4}
	m := make([]int, Two)
	println(a[Two], a[Two:][0], len(a[:Two]), 1<<Two, 16>>Two, len(m))
	println((+Neg).Abs(), -Neg, +Neg)
}
`,
		want: "3.5 3.5 3.5 3 7 4.5 7 4\nint32 98 float64 3.5 int\ntrue true true true true\ntrue true true true true true\n3 3 2 4 4 2\n2.5 2.5 -2.5\n",
	},
	{
		// A string constant -- of a defined type with methods, and a plain one -- in
		// every position: sent, appended, a deferred argument, a switch tag and a
		// case, indexed, sliced, ranged over, in literals, a method's receiver alone
		// and at the head of a chain, compared, concatenated with a literal, spread
		// into a []byte, handed to the strings package, printed. A string constant
		// has no C symbol, and the positions that render a name -- the receiver, the
		// chain head, the switch tag -- named it: `Start.Len()` reached the C backend
		// as an undeclared symbol, `Start.Twice().Len()` was refused, and `append(b,
		// Start...)` was refused for the defined type. The main is split in three:
		// one function's locals live in the cog's 480 longs.
		name: "a string constant in every position",
		src: `import "strings"

type Cmd string

const Start Cmd = "start"
const Stop Cmd = "stop"
const S = "abc"

func (c Cmd) Len() int { return len(c) }

func (c Cmd) Is(s string) bool { return string(c) == s }

func (c Cmd) Twice() Cmd { return c }

type P struct {
	c Cmd
	s string
}

func take(cs ...Cmd) int {
	n := 0
	for _, c := range cs {
		n += c.Len()
	}
	return n
}

func deferred(c Cmd) { println("deferred", string(c)) }

func sender(ch chan Cmd) { ch <- Start }

func main() {
	var ch chan Cmd
	go sender(ch)
	got := <-ch
	println("recv", string(got), got.Len())
	var cs []Cmd = make([]Cmd, 0, 4)
	cs = append(cs, Start, Stop)
	println("append", len(cs), string(cs[0]), string(cs[1]))
	defer deferred(Stop)
	switch Start {
	case Stop:
		println("switch: stop")
	case Start:
		println("switch: start")
	}
	switch S {
	case "abc":
		println("switch S: abc")
	}
	println("len/index/slice", len(S), S[1], S[1:3], len(Start), Start[0], string(Start[1:]))
	for i, r := range S {
		if r == 'c' {
			println("range", i)
		}
	}
	partB()
}

func partB() {
	p := P{c: Start, s: S}
	arr := [2]Cmd{Start, Stop}
	println("lits", string(p.c), p.s, string(arr[0]), string(arr[1]))
	println("methods", Start.Len(), Stop.Is("stop"), Start.Twice().Len(), string(Start.Twice()), take(Start, Stop))
	println("cmp", S == "abc", Start == "start", Start < Stop, S+"x" == "abcx", string(Start)+"!" == "start!")
	partC()
}

func partC() {
	println("strings", strings.HasPrefix(S, "ab"), strings.Index(S, "c"), strings.Contains(string(Start), "tar"), strings.HasSuffix(S, "bc"))
	var b []byte = make([]byte, 0, 16)
	b = append(b, S...)
	b = append(b, Start...)
	println("bytes", len(b), b[0], b[3], b[3] == byte(Start[0]) && b[7] == byte(Start[4]))
	printf("%s %v %s %d\n", S, Start, S, len(S))
}
`,
		want: "recv start 5\nappend 2 start stop\nswitch: start\nswitch S: abc\nlen/index/slice 3 98 bc 5 115 tart\nrange 2\nlits start abc start stop\nmethods 5 true 5 start 9\ncmp true true true true true\nstrings true 2 true true\nbytes 8 97 115 true\nabc start abc 3\ndeferred stop\n",
	},
	{
		// A 64-bit constant in every position the language offers: sent on a
		// channel, appended, a deferred call's argument, a switch tag and a case,
		// under a unary operator, in struct and array literals, converted, compared,
		// under compound assignment, printed. Such a constant has no C symbol (see
		// emitConstSpecName), and this sweep is what found the positions still
		// naming one -- the switch tag among them -- and the fold of a uint64
		// constant expression computing as signed: `uint32(U >> 40)` for a
		// `const U uint64 = 1 << 63` was 4286578688 where Go gives 8388608.
		name: "a 64-bit constant in every position",
		src: `type Q int64

const One Q = 1 << 32
const Two Q = 2 << 32
const U uint64 = 1 << 63

type P struct {
	q Q
	n int
}

func (q Q) Int() int {
	return int(q >> 32)
}

func take(qs ...Q) int {
	n := 0
	for _, q := range qs {
		n += q.Int()
	}
	return n
}

func deferred(q Q) {
	println("deferred", q.Int())
}

func sender(c chan Q) {
	c <- One
}

func main() {
	var ch chan Q
	go sender(ch)
	got := <-ch
	println("recv", got.Int())
	var s []Q = make([]Q, 0, 4)
	s = append(s, One, Two)
	s = append(s, Q(3<<32))
	println("append", len(s), s[0].Int(), s[1].Int(), s[2].Int())
	defer deferred(Two)
	switch One {
	case Two:
		println("switch: two")
	case One:
		println("switch: one")
	}
	switch v := Two; v {
	case One + One:
		println("case: one+one")
	}
	neg := -One
	println("unary", neg.Int(), (^One)+One+One, (+One).Int())
	p := P{q: One, n: One.Int()}
	arr := [2]Q{One, Two}
	println("lits", p.q.Int(), p.n, arr[0].Int(), arr[1].Int())
	println("conv", float64(One)/4294967296, int64(One), uint64(Two)>>32, uint32(U>>40), U/3, U%7, uint32(U/(1<<33)))
	println("cmp", One < Two, One == Q(1<<32), U > 1<<62, U>>1 > 1<<62, One > 0, take(One, Two))
	for i := range 3 {
		v := Q(i) << 32
		if v == Two {
			println("range", i)
		}
	}
	var x Q = One
	x += Two
	x -= One
	x *= 2
	x /= Two
	println("compound", x.Int(), x%One == 0)
	printf("%d %v %d\n", One, Two, U)
}
`,
		want: "recv 1\nappend 3 1 2 3\nswitch: one\ncase: one+one\nunary -1 4294967295 1\nlits 1 1 1 2\nconv 1 4294967296 2 8388608 3074457345618258602 1 1073741824\ncmp true true true false true 3\nrange 2\ncompound 0 false\n4294967296 8589934592 9223372036854775808\ndeferred 2\n",
	},
	{
		// A method called on a 64-bit CONSTANT, alone and at the head of a chain:
		// `One.Int()`, `Two.Add(One).Half().Int()`. Such a constant has no C symbol
		// -- it is inlined at each use (see emitConstSpecName) -- and the receiver
		// positions named it anyway, so `One.Div(x)` in a test file was "Unknown
		// symbol 'One'" to the C backend. Found by running ogo test on a fixed-point
		// package. A pointer method on a constant is refused in Go's words.
		name: "a method called on a 64-bit constant",
		src: `type Q int64

const One Q = 1 << 32
const Two Q = 2 << 32
const Small int32 = 5

type N int32

const Ten N = 10

func (q Q) Int() int {
	return int(q >> 32)
}

func (q Q) Add(r Q) Q {
	return q + r
}

func (n N) Double() N {
	return n * 2
}

func (q Q) Half() Q {
	return q / 2
}

func main() {
	sum := One + Two
	println(One.Int(), Two.Add(One).Int(), One.Add(Two).Half().Int(), Ten.Double(), Q(3<<32).Int(), sum.Int())
}
`,
		want: "1 3 1 20 3 3\n",
	},
	{
		// A package constant named by NOTHING but the package initializer: a
		// package variable's non-constant initializer, `K * y`, is assigned there.
		// A package constant is declared only where a body names it (see
		// pkgConstDecl), and the initializer's text is rendered after the bodies,
		// so it had to be brought forward to be scanned -- without that, `K` was
		// undeclared to the C compiler in the one function that used it.
		name: "a package constant named only in the package initializer",
		src: `const K = 3
const L = 7

var y = 4
var x = K * y
var z = [2]int{L * y, K}

func main() {
	println(x, z[0], z[1])
}
`,
		want: "12 28 3\n",
	},
	{
		// Package-level slice and array literals whose elements are constants of
		// every spelling: a negative literal, a shift, a conversion, a named
		// constant and arithmetic over one, the most negative int64, a folded string
		// concatenation, a signed float. Only a bare literal used to pass the
		// static-initializer test, so `[]int64{-5}` was refused at package level.
		// The backing arrays are file-scope initializers, where the C backend takes
		// no unary minus, no `static const` symbol and no non-constant expression --
		// which is what the spellings emitted for them are measured against here.
		name: "package-level slice literals with constant elements of every spelling",
		src: `const K = 40
const W = 1 << 40

type P struct {
	x, y int64
	s    string
}

var a = []int64{-5, 1 << 40, int64(7) * 3, K, W, -9223372036854775808, 4294967296 - 1}
var b = []int32{-1, 2, -2147483648, 'x', K << 2}
var c = []uint8{255, 1 << 7, 'a'}
var d = []P{{-1, W, "neg"}, {K, -K, "k"}}
var e = [2]int64{-5, 1 << 40}
var f = []string{"a", "b" + "c"}
var g = []bool{true, false, true}
var h = []float64{-1.5, 2}

func main() {
	println(a[0], a[1], a[2], a[3], a[4], a[5], a[6], len(a))
	println(b[0], b[1], b[2], b[3], b[4])
	println(c[0], c[1], c[2], d[0].x, d[0].y, d[0].s, d[1].x, d[1].y, d[1].s)
	println(e[0], e[1], f[0], f[1], g[0], g[1], g[2], int(h[0]*2), int(h[1]))
}
`,
		want: "-5 1099511627776 21 40 1099511627776 -9223372036854775808 4294967295 7\n-1 2 -2147483648 120 160\n255 128 97 -1 1099511627776 neg 40 -40 k\n-5 1099511627776 a bc true false true -3 2\n",
	},
	{
		// An integer converting to a float rounds a tie to EVEN, as IEEE 754 and Go
		// do, from every source width. The target's C compiler rounds a tie away
		// from zero -- float32(16777217) was 16777218 on the board -- so the
		// compiler does the conversion itself (ogoU2f). Every value is read back
		// through the exact float-to-integer conversion, and none converts back out
		// of range, which Go leaves to the implementation. The expected line is
		// amd64 Go's: the 386 backend mis-rounds int64(float32(123456789012345)).
		name: "an integer converts to a float rounding ties to even",
		src: `// Integer-to-float conversion at the ties, from every source width, read back
// through the exact float-to-integer conversion so that no float is printed.
// Every expected value is what round-to-nearest-even gives, which is what Go
// gives; the toolchain's own conversion rounds a tie away from zero. No value
// converts back out of range: Go leaves that result to the implementation.
func main() {
	i32s := []int32{16777215, 16777216, 16777217, 16777218, 16777219, 16777220, -16777217, -16777219, 33554433, 33554434, 33554435, 33554438, 2147483647, -2147483648}
	for _, v := range i32s {
		printf("%d ", int64(float32(v)))
	}
	printf("\n")
	u32s := []uint32{16777217, 33554434, 4294967295, 4294967040, 4294967167}
	for _, v := range u32s {
		printf("%d ", int64(float32(v)))
	}
	printf("\n")
	i64s := []int64{16777217, -16777217, 1099511627777, -1099511627777, 3298534883328, 4503599627370497, 9223372036854775807, -9223372036854775808, 123456789012345, -98765432109876543, 8589934591, 8589934593}
	for _, v := range i64s {
		printf("%d ", int64(float32(v)))
	}
	printf("\n")
	u64s := []uint64{16777217, 9223372036854775808, 9223372587209064448, 9223372587209064449, 18446742974197923840, 18446742699320016896, 18446743249075830784, 4294967297}
	for _, v := range u64s {
		printf("%d ", uint64(float32(v)))
	}
	printf("\n")
	ints := []int{16777217, 33554434, -16777217, 2147483520}
	for _, v := range ints {
		printf("%d ", int(float32(v)))
	}
	printf("\n")
	// float64 targets, with values exact in 32 bits as well so that the host and
	// the target agree
	printf("%d %d %d %d\n", int64(float64(int32(16777216))), int64(float64(int64(-4194304))), int64(float64(uint32(4194304))), int64(float64(int(65536))))
	// a narrow integer converts exactly and keeps its cast
	var b uint8 = 255
	var h int16 = -32768
	printf("%d %d %d\n", int(float32(b)), int(float32(h)), int(float64(h)))
	// arithmetic on the converted values
	a := float32(16777217)
	c := float32(int32(3))
	printf("%d %d %t\n", int64(a*c), int64(a+c), float32(16777217) == float32(16777216))
}
`,
		want: "16777215 16777216 16777216 16777218 16777220 16777220 -16777216 -16777220 33554432 33554432 33554436 33554440 2147483648 -2147483648 \n16777216 33554432 4294967296 4294967040 4294967040 \n16777216 -16777216 1099511627776 -1099511627776 3298534883328 4503599627370496 -9223372036854775808 -9223372036854775808 123456788103168 -98765435851243520 8589934592 8589934592 \n16777216 9223372036854775808 9223373136366403584 9223373136366403584 18446742974197923840 18446742974197923840 18446742974197923840 4294967296 \n16777216 33554432 -16777216 2147483520 \n16777216 -4194304 4194304 65536\n255 -32768 -32768\n50331648 16777220 true\n",
	},
	{
		// Every dereference of a pointer to an array, and a store through any
		// pointer, on LIVE pointers: the check must cost nothing but the check.
		// The struct-valued element stores are the ones the C backend drops when
		// they are made through the guard's call (doc/ptr-to-array-through-call.c),
		// which is why such a pointer is checked by a statement of its own and
		// dereferenced plainly; `*pa = [4]int{...}` is the array copy that used to
		// be emitted as a C assignment to an array; `(*p)++` used to be refused.
		// `for i := range none` and `len(none)` dereference nothing, in Go too.
		name: "stores through live pointers, and every dereference of a pointer to an array",
		src: `type R struct {
	s string
	n int
}

type H struct {
	q *[2]R
	a [3]int
}

func main() {
	var arr [2]R
	pr := &arr
	pr[1] = R{"x", 7}
	pr[0] = pr[1]
	var rows [2]R
	h := H{q: &rows}
	h.q[0] = R{"field", 9}
	h.q[1].n = 4
	ph := &h
	ph.q[1].s = "deep"
	var nums [4]int
	pa := &nums
	*pa = [4]int{1, 2, 3, 4}
	pa[1] += 10
	pa[2]++
	(*pa)[3] = 40
	sum := 0
	for _, v := range pa {
		sum += v
	}
	x := 5
	p := &x
	*p = 6
	*p += 1
	(*p)++
	y := 0
	*p, y = y, *p
	for i := range pa {
		nums[i] += i
	}
	s := pa[1:3]
	b := *pa
	b[0] = 100
	var none *[4]int
	for i := range none {
		sum += i
	}
	println(none == nil)
	println(arr[0].s, arr[0].n, arr[1].s, arr[1].n, rows[0].s, rows[0].n, rows[1].s, rows[1].n)
	println(nums[0], nums[1], nums[2], nums[3], sum, x, y, len(s), s[0], b[0], nums[0], len(pa), len(none))
}
`,
		want: "true\nx 7 x 7 field 9 deep 4\n1 13 6 43 63 0 8 2 13 100 1 4 4\n",
	},
	{
		// The nil check missed two shapes since it shipped: a STORE through a
		// pointer -- `*p = v`, on the board a silent write into hub address 0, the
		// boot area -- and every dereference of a pointer to an ARRAY, which had
		// been left out because the C backend drops a store made through the guard's
		// call into an element of one. Each of the following is one such shape, and
		// each panics before it runs on; `len(p)` and the index-only `for i := range
		// p` do not, as in Go, and stand in the case above.
		name: "a store through a nil pointer panics",
		src: `func main() {
	var p *int
	println("before")
	*p = 5
	println("after")
}
`,
		want:   "before\npanic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a compound store through a nil pointer panics",
		src: `func main() {
	var p *int
	*p += 5
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "an increment through a nil pointer panics",
		src: `func main() {
	var p *int
	(*p)++
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a store through a nil pointer in a multiple assignment panics",
		src: `func main() {
	var p *int
	x := 1
	*p, x = x, 2
	println("after", x)
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "an index read through a nil pointer to an array panics",
		src: `func main() {
	var pa *[4]int
	println(pa[2])
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "an index store through a nil pointer to an array panics",
		src: `func main() {
	var pa *[4]int
	pa[2] = 1
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a compound index store through a nil pointer to an array panics",
		src: `func main() {
	var pa *[4]int
	pa[1] += 1
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a range with a value over a nil pointer to an array panics",
		src: `func main() {
	var pa *[4]int
	for i, v := range pa {
		println(i, v)
	}
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "slicing a nil pointer to an array panics",
		src: `func main() {
	var pa *[4]int
	s := pa[1:3]
	println(len(s))
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "copying the array out of a nil pointer to one panics",
		src: `func main() {
	var pa *[4]int
	b := *pa
	println(b[0])
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "assigning the whole array through a nil pointer to one panics",
		src: `func main() {
	var pa *[4]int
	*pa = [4]int{1, 2, 3, 4}
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "an index read through a nil pointer-to-array field panics",
		src: `type H struct {
	q *[4]int
}

func main() {
	var h H
	ph := &h
	println(ph.q[1])
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "an index store through a nil pointer-to-array field panics",
		src: `type H struct {
	q *[4]int
}

func main() {
	var h H
	h.q[1] = 3
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a written-out dereference of a nil pointer to an array panics",
		src: `func main() {
	var pa *[4]int
	(*pa)[0] = 1
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		// A 64-bit constant expression is folded by the compiler and emitted as ONE
		// literal, because the target's C compiler mis-folds nearly every one it is
		// given inside a function body: `int64(5) + 1` printed 4294967296000006 on
		// the board, `int64(1000) * 1000` 4294967302, `uint64(5) + 1` 4294967302,
		// `Ticks(3) * 4` 4294967308, `int64(-3) * int64(7)` 51539607531 -- while the
		// same expressions with a variable operand, and a lone literal, are right.
		// See doc/int64-constant-fold.c. A 64-bit named constant is inlined at each
		// use for the same reason (K, W), and a 32-bit one keeps its symbol.
		//
		// The comparisons are the other half: a negative constant too wide for an
		// int was spelled as its unsigned bit pattern, which made `v > -4294967295`
		// an unsigned comparison -- false for a v of 5, on every C compiler.
		name: "a 64-bit constant expression is folded, not left to the C compiler",
		src: `const K int64 = 5
const W = 1 << 40

type Ticks int64

func main() {
	y := int64(4294967301)
	v := int64(5)
	var u uint64 = 3
	println(int64(5)+1, int64(1000)*1000, int64(3)*4/2, int64(7)%3, 2*int64(3)-1)
	println(uint64(5)+1, uint64(1)<<40+1, uint64(7)/2, uint64(9)%4)
	println(K+1, K*K, int64(W)/2, int64(W)+1, Ticks(3)*4, Ticks(W)+Ticks(1))
	println(int64(-2147483648)+1, int64(-5)*3, int64(-5)-1, int64(-4294967295)+1)
	println(v > -4294967295, v < -4294967295, y > 4294967296, v == 5)
	println(int64(5)+y, y-int64(5), int64(1000)*y, y/int64(4), u+uint64(4)*2, uint64(4)*u)
	println(int64(1)<<40+1, int64(1)<<40>>3, int64(1)<<32|1)
	a := int64(5) + 1
	var b int64 = int64(1000) * 1000
	c := uint64(3) * 4
	printf("%d %d %d %d\n", a, b, c, int64(-3)*int64(7))
}
`,
		want: "6 1000000 6 1 5\n6 1099511627777 3 1\n6 25 549755813888 1099511627777 12 1099511627777\n-2147483647 -15 -6 -4294967294\ntrue false true true\n4294967306 4294967296 4294967301000 1073741825 11 12\n1099511627777 137438953472 4294967297\n6 1000000 12 -21\n",
	},
	{
		// The most negative int, and negative constants wider than an int, in every
		// position: a declaration, a conversion, a slice literal, an array literal, a
		// comparison, an argument, a builtin, arithmetic. `-2147483648` was written
		// as a minus applied to "2147483648U" -- an unsigned, whose negation is
		// itself -- so `var a int64 = -2147483648` was 2147483648 on the host, and
		// `int64(-2147483648) + 1` was garbage on the board.
		name: "the most negative int and negative wide constants in every position",
		src: `type Ticks int64

func take(v int64) int64 { return v }

func main() {
	var a int64 = -2147483648
	b := int64(-2147483648)
	c := -2147483648
	var d int32 = -2147483648
	e := []int64{-2147483648, -2147483649, -4294967295, -4294967296, 2147483648}
	var t Ticks = -2147483648
	println(a, b, c, d, e[0], e[1], e[2], e[3], e[4], t)
	v := int64(-5)
	println(v < -2147483648, v > -2147483648, a == -2147483648, take(-2147483648), a-1, a*2, -a)
	println(int64(-2147483648)*2, a/(-2147483648), a%(-2147483647))
	printf("%d %d %d %d\n", a, -2147483648, int64(-2147483648)+1, take(-2147483648)-take(-4294967295))
	var m [2]int64 = [2]int64{-2147483648, -2147483648 * 2}
	println(m[0], m[1], min(a, -2147483647), max(v, -2147483648))
}
`,
		want: "-2147483648 -2147483648 -2147483648 -2147483648 -2147483648 -2147483649 -4294967295 -4294967296 2147483648 -2147483648\nfalse true true -2147483648 -2147483649 -4294967296 2147483648\n-4294967296 1 -1\n-2147483648 -2147483648 -2147483647 2147483647\n-2147483648 -4294967296 -2147483648 -5\n",
	},
	{
		// The same constants as PACKAGE-level initializers, which take another path:
		// zeroed at file scope and assigned at package init.
		name: "negative wide constants as package-level initializers",
		src: `type Ticks int64

var g int64 = -4294967295
var h = [2]int64{-4294967295, -2147483648}
var k uint64 = 1 << 63
var m int64 = int64(-5) * 3
var n = int64(5) + 1
var t Ticks = Ticks(3) * 4
var s = [3]int64{int64(1) << 40, -1 << 40, 4294967295}
var p int32 = -2147483648

func main() {
	println(g, h[0], h[1], k, m, n, t, s[0], s[1], s[2], p)
	println(g < 0, h[1] == -2147483648, m*2, n+g)
}
`,
		want: "-4294967295 -4294967295 -2147483648 9223372036854775808 -15 6 12 1099511627776 -1099511627776 4294967295 -2147483648\ntrue true -30 -4294967289\n",
	},
	{
		// A float converting to a 64-bit integer, or to a 32-bit unsigned one. The
		// target's C compiler converts the first by REINTERPRETING THE FLOAT'S BITS
		// -- `int64(three)` for a 3.0 printed 1077936128, which is 0x40400000 --
		// and clamps the second at 2147483647, at every optimisation level, so
		// these go through helpers built on the 32-bit signed conversion, which it
		// gets right (ogoF2u32 and the two beside it). Every value is exact in a
		// 32-bit float, which float64 is on the target, so the host and the board
		// convert the same numbers; the edges are 2^31, 2^32 - 256, the largest
		// float below 2^63 and one above it. The host C compiler is right about all
		// of it, so only the board saw this. See doc/float-to-int64.c.
		name: "a float converts to a 64-bit or an unsigned integer by value",
		src: `type Ticks int64
type Raw uint32

func main() {
	// Every value here is exact in a 32-bit float, which float64 is on the target,
	// so the host and the board convert the same numbers.
	three := 3.0
	frac := -12345.75
	var f32 float32 = 36000000
	big := 3000000000.0
	wide := 3298534883328.0        // 3 * 2^40
	neg := -343597383680.0         // -5 * 2^36
	top := 9223371487098961920.0   // (2^24 - 1) * 2^39, the largest below 2^63
	over := 9223373136366403584.0  // (2^23 + 1) * 2^40, above 2^63
	edge := 2147483648.0
	limit := 4294967040.0 // 2^32 - 256
	half := 2.5
	println(int64(three), int64(frac), int64(f32), int64(big), int64(wide), int64(neg), int64(top))
	println(uint64(three), uint64(f32), uint64(big), uint64(wide), uint64(top), uint64(over))
	println(uint32(three), uint32(f32), uint32(big), uint32(edge), uint32(limit), uint(big), uintptr(edge))
	println(int64(half), int64(-half), uint64(half), uint32(half), int32(big-1e9), int(frac), int16(frac), int8(frac))
	var t Ticks = Ticks(three * 4.0)
	var r Raw = Raw(big)
	println(t, r, int64(f32)*2, uint64(f32)+1, int64(three) == 3, uint32(edge) > 2147483647)
	printf("%d %d %d\n", int64(three*1000000.0), int64(36.0*1000000.0), uint32(3.0*1000000000.0))
}
`,
		want: "3 -12345 36000000 3000000000 3298534883328 -343597383680 9223371487098961920\n3 36000000 3000000000 3298534883328 9223371487098961920 9223373136366403584\n3 36000000 3000000000 2147483648 4294967040 3000000000 2147483648\n2 -2 2 2 2000000000 -12345 -12345 -57\n12 3000000000 72000000 36000001 true true\n3000000 36000000 3000000000\n",
	},
	{
		// A divisor that folds to a constant needs no guard, however it is spelled,
		// and a divisor that does not is guarded at the LEVEL's width, resolved past
		// a type definition. Both used to go wrong in the checked build only, which
		// is the default:
		//
		//   - only a bare literal was left unguarded, so `x / (1 << 32)` on an int64
		//     level went through the 32-bit guard, which truncated the constant to
		//     its low word -- zero -- and panicked "integer divide by zero" in a
		//     program dividing by 2^32; `x / (1 << 31)` divided by the int's most
		//     negative value instead. The lock-in detector this was found in scales
		//     a CORDIC angle by exactly `* 36000 / (1 << 32)`;
		//   - the guard's width was read from the divisor's own type NAME, so a
		//     `type U uint64` -- spelled "U", not "uint64_t" -- took the 32-bit
		//     guard: `a / b` panicked for a b whose low word is zero and silently
		//     divided by that word for any other, and a `type F float64` had its
		//     divisor truncated to an int, 2.5 to 2.
		//
		// The compound forms and the named-constant divisors are here because they
		// take other paths; `y % (N * 2)` also no longer pays for a check on every
		// pass of a loop.
		name: "a constant divisor of any spelling, and a defined 64-bit or float one",
		src: `type U uint64
type Q int64
type F float64

const N = 64

func main() {
	x := int64(1) << 40
	y := uint64(1) << 40
	var q Q = 1 << 40
	var a, b U = 1 << 40, 1<<33 + 5
	var f, g F = 5, 2.5
	println(x/(1<<32), x%(1<<32), x/(1<<31), y/(1<<32), y%(1<<33), q/(1<<32), q%(1<<31))
	println(y/N, y%(N*2), x/(N/2), x%(N+1))
	println(a/b, a%b, a/(1<<33))
	x /= (1 << 32)
	y %= (1 << 33)
	q /= N * 2
	println(x, y, q)
	printf("%f %f\n", f/g, f/(1<<32))
}
`,
		want: "256 0 512 256 0 256 0\n17179869184 0 34359738368 16\n127 8589933957 128\n256 0 8589934592\n2.000000 0.000000\n",
	},
	{
		// The same backend miscompile with the indexed array field as the TARGET
		// rather than the operand, where it is a silent NO-OP: a histogram bin
		// accumulated nothing at all. A ++ on the same element is unaffected, and
		// so is the same statement through a value, which is what made it look
		// like something other than a compiler fault.
		//
		// Written as a histogram because that is the shape it costs: any per-bin
		// or per-channel total kept in an array field, updated through a pointer.
		// Expected output taken from the same program compiled by Go.
		name: "a compound assignment INTO an array field through a pointer",
		src: `type Hist struct {
	bins [4]int32
	n    int32
}

func (h *Hist) Add(v int32) {
	h.bins[v%4] += v
	h.n++
}

func main() {
	var h Hist
	p := &h
	for i := int32(1); i <= 8; i++ {
		p.Add(i)
	}
	println(h.bins[0], h.bins[1], h.bins[2], h.bins[3], h.n)

	// The same statement written straight onto a pointer variable, with a
	// constant index and with one read out of a field.
	q := &h
	q.bins[1] *= 2
	q.bins[q.n%4] += 7
	println(q.bins[0], q.bins[1], q.bins[2], q.bins[3])
}
`,
		want: "12 6 8 10 8\n19 12 8 10\n",
	},
	{
		// A ring buffer's accumulator, which is a compound assignment whose right
		// operand indexes an array field through a pointer receiver. The backend
		// miscompiles exactly that -- see doc/compound-call-index.c -- because a
		// checked build wraps the dereference in the nil guard and so hands it a
		// pointer a CALL returned. It answered with garbage, or with nothing
		// subtracted at all, and said nothing.
		//
		// THE HOST COMPILER GETS THIS RIGHT, so the board half of this case is the
		// half that tests anything. It is written as a moving average because that
		// is what found it: the average disagreed with the same program in Go.
		name: "a compound assignment reading an array field through a pointer",
		src: `type Mean struct {
	ring  [4]int32
	at    int
	count int
	sum   int32
}

func (m *Mean) Push(v int32) int32 {
	m.sum -= m.ring[m.at]
	m.ring[m.at] = v
	m.sum += v
	m.at = (m.at + 1) % 4
	if m.count < 4 {
		m.count++
	}
	return m.sum / int32(m.count)
}

// The same shapes off a pointer VARIABLE and a plain local, which the backend
// gets right, so a regression here would be ours rather than its.
func spot() {
	var m Mean
	m.ring[1] = 30
	p := &m
	x := int32(1000)
	x -= p.ring[1]
	println("x", x)
	p.sum = 1000
	p.sum -= p.ring[1]
	println("sum", p.sum)
}

func main() {
	var m Mean
	for i := 0; i < 6; i++ {
		println(i, m.Push(int32(i+1)*10))
	}
	spot()
}
`,
		want: "0 10\n1 15\n2 20\n3 25\n4 35\n5 45\nx 970\nsum 970\n",
	},
	{
		// A nil dereference panics rather than using address zero. It used not to:
		// address zero on this target is ordinary Hub RAM, not a trap, so a READ
		// through a nil pointer yielded whatever lives at 0 and a WRITE stored into
		// the boot area, both silently, where Go panics for each.
		//
		// The write is the one worth a case of its own -- reading garbage is bad and
		// corrupting the boot area is worse -- but a test can only observe the first
		// panic, so the read is what this asserts and the write has its own case
		// below it.
		name: "a nil pointer dereference panics",
		src: `type P struct{ a int }

var np *P

func main() {
	println("before")
	println(np.a)
	println("unreachable")
}
`,
		panics: true,
		want:   "panic: nil pointer dereference\n",
	},
	{
		name: "a nil pointer write panics",
		src: `type P struct{ a int }

var np *P

func main() {
	println("before")
	np.a = 1
	println("unreachable")
}
`,
		panics: true,
		want:   "panic: nil pointer dereference\n",
	},
	{
		// The written-out dereference takes the check too, which is a separate
		// emission path from the "p.f" shorthand: the star and the name are emitted
		// as unrelated tokens, so the one place that knows this is a dereference is
		// where the shape is still visible.
		name: "a nil written-out dereference panics",
		src: `var ni *int

func main() {
	println("before")
	println(*ni)
	println("unreachable")
}
`,
		panics: true,
		want:   "panic: nil pointer dereference\n",
	},
	{
		// A variadic whose ELEMENT is a string or a struct. Neither compiled: a call
		// packs its trailing arguments into an array of this frame, and an array
		// INITIALIZER wants its aggregates braced rather than written as compound
		// literals -- `(ogo_string){"a", 1}` and `(P){9}` were both refused inside
		// the braces, the target's compiler naming the compound literal's own
		// anonymous type.
		//
		// The variadic case further down covers the SHAPES -- pack, spread, empty, a
		// fixed parameter before it, a method -- and every one of them uses an int
		// element, which has nothing to brace. That is why the feature looked whole.
		// The host compiler accepts a compound literal there too, so only the board
		// answered for it.
		name: "a variadic of strings and of structs",
		src: `func count(xs ...string) int {
	n := 0
	for _, s := range xs {
		n += len(s)
	}
	return n
}

type P struct{ a int }

func firsts(ps ...P) int {
	if len(ps) == 0 {
		return -1
	}
	return ps[0].a + len(ps)
}

func joined(sep string, xs ...string) int {
	return len(sep)*100 + count(xs...)
}

func lens(xs ...[]int) int {
	n := 0
	for _, x := range xs {
		n += len(x)
	}
	return n
}

var pool [3]int

func main() {
	println("strings", count("a", "bb", "ccc"), count(), count("x"))
	println("structs", firsts(P{9}, P{8}), firsts(), firsts(P{4}))
	println("fwd", joined("--", "ab", "c"))

	// Every argument above is a LITERAL, which is the one thing an array
	// initializer's braces take. A VARIABLE of an aggregate type is the spelling
	// that did not compile, and it is not particular to structs: a string, a
	// struct and a slice each drew a diagnostic about C the program never wrote.
	s := "bb"
	p := P{7}
	xs := pool[:]
	println("vars", count(s, "ccc"), firsts(p, P{8}), lens(xs, pool[:]))
}
`,
		want: `strings 6 0 1
structs 11 -1 5
fwd 203
vars 5 9 6
`,
	},
	{
		// An INTERFACE element, the last element type a variadic could not take. A
		// concrete value handed to an interface parameter is wrapped where it stands
		// -- the two words the parameter is, the value's address and the table for
		// that pair -- and the pack did not wrap, storing the raw pointer where the
		// two words go. So a variadic of interfaces did not compile at all.
		//
		// Two concrete types in one pack is the case worth running rather than only
		// building: each element carries its OWN table, so a pack that wrapped with
		// one table for all of them would compile and dispatch to the wrong method.
		name: "a variadic of interfaces",
		src: `type Shape interface {
	Area() int
	Name() string
}

type Sq struct{ s int }

func (q *Sq) Area() int    { return q.s * q.s }
func (q *Sq) Name() string { return "sq" }

type Rect struct{ w, h int }

func (r *Rect) Area() int    { return r.w * r.h }
func (r *Rect) Name() string { return "rect" }

func total(ss ...Shape) int {
	t := 0
	for _, s := range ss {
		t += s.Area()
	}
	return t
}

func names(ss ...Shape) int {
	n := 0
	for _, s := range ss {
		n += len(s.Name())
	}
	return n
}

func fwd(ss ...Shape) int { return total(ss...) }

var gq = Sq{3}

var gr = Rect{2, 5}

func main() {
	println(total(&gq, &gr), names(&gq, &gr))

	// An interface VARIABLE is already the two words, and is copied as it stands
	// rather than wrapped a second time.
	var s Shape = &gr
	println(total(s, &gq))

	// The empty pack, and forwarding one on with a spread.
	println(total(), fwd(&gq, &gr))
}
`,
		want: "19 6\n19\n0 19\n",
	},
	{
		// The ELEMENT axis of a variadic, swept in one program. Twice now a defect
		// here has been a spelling the table never varied: first every element was an
		// `int`, which has nothing to brace, and then every argument was a LITERAL,
		// which is the one thing an array initializer's braces take. Both looked whole
		// because the SHAPES -- pack, spread, empty, a fixed parameter before it, a
		// method -- were covered thoroughly and the element was not.
		//
		// So this is the guard rather than another case: each element type, and for
		// each of them the two spellings that differed. The kinds with a history of
		// their own -- a string, a struct, a slice, an interface -- are covered by the
		// two cases above; these are the rest.
		name: "a variadic of every element type",
		src: `type P struct{ a int }

type Loc P

type Cel int

type List []int

func vBool(xs ...bool) int {
	n := 0
	for _, x := range xs {
		if x {
			n++
		}
	}
	return n
}

func vF(xs ...float32) int {
	n := 0
	for _, x := range xs {
		n += int(x)
	}
	return n
}

func vLoc(xs ...Loc) int {
	n := 0
	for _, x := range xs {
		n += x.a
	}
	return n
}

func vCel(xs ...Cel) int {
	n := 0
	for _, x := range xs {
		n += int(x)
	}
	return n
}

func vList(xs ...List) int {
	n := 0
	for _, x := range xs {
		n += len(x)
	}
	return n
}

func vPtr(xs ...*P) int {
	n := 0
	for _, x := range xs {
		n += x.a
	}
	return n
}

var gp = P{5}

func main() {
	b := true
	f := float32(2.5)
	l := Loc{6}
	c := Cel(8)
	li := List{1, 2}
	pp := &gp

	// A literal and a variable of each: the pair that differed, the literal being
	// the only thing an array initializer's braces took.
	println(vBool(true, b), vF(1.5, f))
	println(vLoc(Loc{2}, l), vCel(3, c))
	println(vList(List{9}, li), vPtr(&gp, pp))
}
`,
		want: "2 3\n8 11\n3 10\n",
	},
	{
		// Four channel ELEMENT types the table did not otherwise reach. The rest are
		// well covered -- an array element, an interface element, a defined channel
		// type, channels in struct fields and in an array of structs all have cases
		// of their own -- and a sweep of twelve element types found nothing wrong,
		// which is why only the uncovered four are kept rather than all twelve.
		//
		// The defined SLICE element is the one worth having: the rendezvous copies
		// the element by its C type, and a defined type is read by two different
		// names depending on who is asking, which is where this week's defects were.
		name: "channel elements a case did not reach",
		src: `type List []int
type P struct{ a, b int }

var gp = P{5, 6}
var l = List{21, 22}

var cl chan List
var cp chan *P
var cf chan float32
var cb chan byte
var done chan int

func send() {
	cl <- l
	cp <- &gp
	cf <- 1.5
	cb <- 200
	done <- 1
}

func main() {
	go send()
	v := <-cl
	println("defined-slice", len(v), v[1])
	p := <-cp
	println("pointer", p.a, p.b)
	f := <-cf
	println("float32", f*2)
	b := <-cb
	println("byte", int(b))
	<-done
}
`,
		want: `defined-slice 2 22
pointer 5 6
float32 3
byte 200
`,
	},
	{
		// An interface in every position one can stand in. Two of them did not
		// compile at all: a literal put whatever was written straight into an
		// interface-typed slot, where the two words {data, table} belong, so
		// `Box{&gr}` was refused as "expected _struct__Shape but got pointer to
		// _struct__Rect" and `[2]Shape{&gq, &gr}` likewise -- both accepted by Go.
		// A brace initializer wants the members braced rather than a compound
		// literal, which is why building the value the ordinary way did not fit
		// here and ifaceBraceC exists.
		//
		// The rest were already right and are kept because this is the table's only
		// pass over the positions together: the vtable is per (concrete, interface)
		// pair, so which position built the value decides which table it carries.
		//
		// The four functions have multi-line bodies rather than one-liners because
		// gofmt ALIGNS the braces of adjacent one-line functions and ogo fmt does
		// not yet; the gofmt ratchet counts programs rather than excusing them.
		name: "an interface in every position",
		src: `type Shape interface{ area() int }

type Sq struct{ s int }
type Rect struct{ w, h int }

func (q *Sq) area() int {
	return q.s * q.s
}

func (r *Rect) area() int {
	return r.w * r.h
}

var gq = Sq{3}
var gr = Rect{2, 5}

type Box struct{ in Shape }

func take(s Shape) int {
	return s.area()
}

func give() Shape {
	return &gq
}

func main() {
	var s Shape = &gq
	println("var", s.area())
	println("arg", take(&gr))
	println("ret", give().area())

	b := Box{&gr}
	println("field", b.in.area())

	s = &gr
	println("reassign", s.area())

	var z Shape
	println("nil", z == nil, s != nil)
	s2 := s
	println("eq", s == s2)

	if r, ok := s.(*Rect); ok {
		println("assert ok", r.w)
	}
	if _, ok := s.(*Sq); !ok {
		println("assert not")
	}

	for _, v := range [2]Shape{&gq, &gr} {
		switch t := v.(type) {
		case *Sq:
			println("switch Sq", t.s)
		case *Rect:
			println("switch Rect", t.w)
		}
	}
}
`,
		want: `var 9
arg 10
ret 9
field 10
reassign 10
nil true true
eq true
assert ok 2
assert not
switch Sq 3
switch Rect 2
`,
	},
	{
		// A method on a defined SLICE type, reached through every way of making one.
		// The short form was the odd one out: `d := List{1, 2, 3}` recorded the
		// variable as the slice HEADER's type rather than as a List, so `d.sum()`
		// had nothing to hang off and the emitter read it as a package
		// qualification -- "unknown package \"d\"", which names neither the type nor
		// the method and sends the reader looking for an import.
		//
		// The others always worked, which is what made it worth fixing rather than
		// documenting: the same program is accepted or refused depending on which
		// spelling introduced the variable, and Go accepts them all.
		name: "a method on a defined slice type",
		src: `type List []int

func (l List) total() int {
	t := 0
	for _, v := range l {
		t += v
	}
	return t
}

var back = [4]int{9, 9, 9, 9}
var pkg = List{5, 6}

func main() {
	d := List{1, 2, 3}
	println("short", d.total(), len(d), d[1])

	var m List = make(List, 2, 4)
	m[0] = 4
	m[1] = 5
	println("make", m.total())

	var l List = back[:]
	println("sliceexpr", l.total())

	var v List = List{7, 8}
	println("var-lit", v.total())

	println("pkg", pkg.total())
}
`,
		want: `short 6 3 2
make 9
sliceexpr 36
var-lit 15
pkg 11
`,
	},
	{
		// make over a DEFINED slice type. It was refused three layers deep: the
		// checker read only the "[]T" shape and called a type name "dynamic
		// allocation not supported"; then, once it read the name, the bare-type-name
		// rule called the argument a value; then the emitter's make path wanted the
		// declared type to be "[]T" as well.
		//
		// The variable keeps its OWN name as its C type rather than the slice
		// header's, which is what the method line tests: resolve the name away and
		// d.total() has nothing to hang off. That is also why append had to learn to
		// look through a defined type -- it read the written name and refused.
		//
		// The Alias line is the chain, "type Alias List" over "type List []int", and
		// the plain line is the control: the "[]T" spelling must keep working.
		name: "make over a defined slice type",
		src: `type List []int
type Alias List

func (l List) total() int {
	t := 0
	for _, v := range l {
		t += v
	}
	return t
}

func main() {
	var d List = make(List, 2, 4)
	d[0] = 7
	d[1] = 8
	println("make", len(d), cap(d), d[0], d[1])

	d = append(d, 9)
	println("append", len(d), cap(d), d[2])

	println("method", d.total())

	var a Alias = make(Alias, 1, 3)
	a[0] = 5
	println("chain", len(a), cap(a), a[0])

	var p []int = make([]int, 2, 2)
	println("plain", len(p), cap(p))
}
`,
		want: `make 2 4 7 8
append 3 4 9
method 24
chain 1 3 5
plain 2 2
`,
	},
	{
		// Mixed-width arithmetic in the shapes a device protocol actually uses, as
		// against the operator-at-a-time cases elsewhere in this table. Each line is
		// somewhere a 32-bit target can quietly differ from Go:
		//
		//	the split and reassembly of a wide value, where the low half has to be
		//	  masked through uint32 -- unmasked, a low word with its top bit set
		//	  sign-extends and poisons the whole result. The -1 line is the one that
		//	  catches it: every half is 0xFFFFFFFF there.
		//	a counter difference across WRAPAROUND, which is how elapsed time is
		//	  measured against a 32-bit cycle counter that laps every few seconds.
		//	a widening before a multiply, beside the same multiply left narrow --
		//	  the last line overflows on purpose, and the two must disagree exactly
		//	  as Go has them disagree. The narrow one is bound to a variable rather
		//	  than written as int64(raw*3300) only because ogo fmt does not yet
		//	  tighten a binary operand inside a conversion the way gofmt does; the
		//	  arithmetic is identical either way.
		//
		// Every value was taken from real Go. Verified on a P2-EDGE as well as on
		// the host: this is emulated 64-bit arithmetic on the target, so the host
		// compiler agreeing with Go says little about the backend that ships.
		name: "mixed-width protocol arithmetic",
		src: `func main() {
	v := int64(0x0000123456789ABC)
	hi := int32(v >> 32)
	lo := int32(v & 0xFFFFFFFF)
	println(hi, lo, int64(hi)<<32|int64(uint32(lo)) == v)

	w := int64(-1)
	whi := int32(w >> 32)
	wlo := int32(w & 0xFFFFFFFF)
	println(whi, wlo, int64(whi)<<32|int64(uint32(wlo)) == w)

	var t0 uint32 = 0xFFFFFF00
	var t1 uint32 = 0x00000100
	println(t1-t0, int32(t1-t0))

	var raw int32 = 2000000
	println(int64(raw)*3300/65536, int32(int64(raw)*3300/65536))
	narrow := raw * 3300
	println(int64(narrow) / 65536)
}
`,
		want: `4660 1450744508 true
-1 -1 true
512 512
100708 100708
-30363
`,
	},
	{
		// A named slice type where it is NOT a literal's initializer. Each line was
		// its own defect, and all four share one cause: a defined type was read by
		// the name written rather than by what it is defined over, so every table
		// keyed on the slice header's own C name missed it.
		//
		//	var zero List   emitted "List zero = 0;", a scalar assigned to a struct,
		//	                which the target's C compiler refuses outright -- a
		//	                variable of a named slice type could not be DECLARED
		//	                without an initializer at all.
		//	b.in[0]         was refused, "cannot index b.in", for a field Go indexes.
		//	Box{List{...}}  emitted "Box b = {{1, 2, 3}}", filling the header's own
		//	                pointer, length and capacity with 1, 2 and 3.
		//
		// Found by sweeping a matrix of six literal kinds against eight syntactic
		// positions after the initializer case turned up on its own: the lesson of
		// that one was that a construct correct in one position can be broken in
		// another, so the positions got enumerated rather than guessed at.
		name: "a named slice type outside an initializer",
		src: `type List []int
type Box struct{ in List }

func take(l List) int { return len(l) }

func main() {
	var zero List
	println("zero", len(zero), cap(zero), zero == nil)

	var v List
	v = List{7, 8, 9}
	println("assign", len(v), v[0])

	b := Box{List{1, 2, 3}}
	println("field", len(b.in), b.in[0], b.in[2])

	var bz Box
	println("field-zero", len(bz.in))

	println("arg", take(List{4, 5}))
}
`,
		want: `zero 0 0 true
assign 3 7
field 3 1 3
field-zero 0
arg 2
`,
	},
	{
		// A literal of a NAMED SLICE type, in every position one can stand in. The
		// typed var was a miscompile: a brace initializer cannot fill a slice, which
		// is a header pointing at storage, and filling one anyway wrote the elements
		// into the header's own fields -- "var b List = List{7, 8, 9}" gave a length
		// of 8, a capacity of 9 and a data pointer of 7, so b[0] read address 7.
		//
		// The other four spellings took other paths and were always right, which is
		// how it survived: the broken one names the type twice, and nobody writes the
		// type twice. Found by checking a README claim ("type List []int as a slice")
		// rather than by a test, so the positions are all here now.
		//
		// The array line is the control: a brace initializer IS right for one, so the
		// fix has to keep taking that path.
		name: "a literal of a named slice type",
		src: `type List []int
type Row [3]int

func take(l List) int { return len(l) }

var pkg = List{1, 2, 3}

func main() {
	a := List{4, 5, 6}
	println("short", len(a), a[0])
	var b List = List{7, 8, 9}
	println("var", len(b), b[0], cap(b))
	var c = List{1, 1, 1}
	println("infer", len(c), c[0])
	println("arg", take(List{2, 2}))
	println("pkg", len(pkg), pkg[0])
	var r Row = Row{1, 2, 3}
	println("row", len(r), r[2])
	var s List = List{}
	println("empty", len(s))
}
`,
		want: `short 3 4
var 3 7 3
infer 3 1
arg 2
pkg 3 1
row 3 3
empty 0
`,
	},
	{
		// printf's flags, width and precision. Every line was taken from real Go
		// (GOARCH=386) rather than written by hand, because half the point is the
		// places C and Go would disagree if the spec were just handed through.
		//
		// The two string verbs are where they do. fmt measures a string's width in
		// RUNES -- "héllo" is five of them in six bytes, so %4s pads it by nothing
		// and a byte count would have padded by nothing at width 4 but wrongly at 5 --
		// and %.1s truncates to one rune, not one byte, which would have cut "é" in
		// half. Neither can borrow C's padding anyway: a string here carries a length
		// and no terminator, and the target's printf truncates "%.*s" at 62
		// characters silently.
		//
		// "%#x" and "%08.3f" are absent because they are REFUSED, the target's printf
		// ignoring both flags -- doc/printf-flags-ignored.c. This case is why they
		// are: it passed on the host with both in it, and the board printed "ff" and
		// "   1.500".
		//
		// The two-digit widths are here for the other direction: a '0' is a flag only
		// at the FRONT of a spec, so "%10.3f" and "%20d" have to keep their width
		// whole rather than lose a zero to the flag scan.
		//
		// The %T line takes a path of its own. A statically known type name is
		// normally folded into the surrounding literal and costs no call; a width
		// cannot be folded, so that case has to fall through to a printf instead --
		// and matching Go here needs the three names to be predeclared ones, since a
		// defined type prints unqualified where Go writes "main.".
		name: "printf width and precision",
		src: `func main() {
	printf("[%6.2f][%-8.3f][%10.3f][%20d]\n", 3.14159, 2.5, 1.5, 7)
	printf("[%5d][%-5d][%05d][%+d][% d]\n", 42, 42, 42, 42, 42)
	printf("[%8s][%-8s][%.2s][%6.2s]\n", "abc", "abc", "abcdef", "abcdef")
	var u uint32 = 255
	printf("[%4x][%04X]\n", u, u)
	printf("[%6t][%-6t]|\n", true, false)
	printf("[%3c][%-3c][%.2c][%5.2c]|\n", 'A', 'B', 'C', 'D')
	printf("[%4s][%.1s]\n", "héllo", "héllo")
	printf("[%3c]\n", 'é')
	printf("[%6T][%-8T][%T]|\n", 1, "x", true)
}
`,
		want: `[  3.14][2.500   ][     1.500][                   7]
[   42][42   ][00042][+42][ 42]
[     abc][abc     ][ab][    ab]
[  ff][00FF]
[  true][false ]|
[  A][B  ][C][    D]|
[héllo][h]
[  é]
[   int][string  ][bool]|
`,
	},
	{
		// The comma-ok type assertion accepts an interface EXPRESSION as its
		// operand, not only a name: "if p, ok := rs[i].(*A); ok" is how a dispatch
		// loop tests an element. The checker refused it first ("2 variables but 1
		// value", its shape test seeing an assertion only on a name) and the
		// emitter after that ("multiple assignment requires a single function call
		// on the right-hand side").
		//
		// The operand is bound to a temporary, which an assertion needs anyway --
		// it reads the operand TWICE, once to test the table and once to take the
		// data word -- and which makes an operand with a side effect evaluated
		// once: "calls" below is 1, as in Go.
		//
		// The one-value form on an expression, "p := rs[i].(*A)", is a different
		// path and is still refused; bind the operand to a variable first.
		//
		// Every line matches real Go.
		name: "a comma-ok assertion on an expression",
		src: `type R interface{ v() int }

type N interface{ nm() string }

type A struct{ n int }

func (a *A) v() int { return a.n }

func (a *A) nm() string { return "a" }

type B struct{ m int }

func (b *B) v() int { return b.m }

type Box struct{ r R }

var calls int

func mk(r R) R {
	calls++
	return r
}

func main() {
	var x A
	var y B
	x.n = 4
	y.m = 9
	var rs []R = make([]R, 2)
	rs[0] = &x
	rs[1] = &y

	for i := 0; i < 2; i++ {
		if p, ok := rs[i].(*A); ok {
			println("A", p.n)
		} else {
			println("not A")
		}
		if q, ok := rs[i].(N); ok {
			println("N", q.nm())
		} else {
			println("not N")
		}
	}

	var bx Box
	bx.r = &y
	p, ok := bx.r.(*B)
	println("field", ok, p.m)

	r2, ok2 := mk(rs[0]).(*A)
	println("call", ok2, r2.n, calls)

	// The ONE-VALUE form takes an expression too. It runs through the expression
	// emitter rather than the assignment path, so its binding goes to the
	// statement prologue -- which is carried into a loop body, so an operand that
	// changes per iteration is bound per iteration.
	one := rs[0].(*A)
	println("one", one.n)
	fld := bx.r.(*B)
	println("one field", fld.m)
	for i := 0; i < 2; i++ {
		it := rs[0].(N)
		println("one loop", i, it.nm())
	}
}
`,
		want: "A 4\nN a\nnot A\nnot N\nfield true 9\ncall true 4 1\n" +
			"one 4\none field 9\none loop 0 a\none loop 1 a\n",
	},
	{
		// Interface values compared, and nil as an interface value.
		//
		// Comparing two of them was a SILENT WRONG ANSWER: an interface is a struct
		// here and is registered as one, but with no fields -- its words are the
		// data pointer and the table, not anything the source declared -- so the
		// struct helper compared NOTHING and returned whatever was in the return
		// register. Two interfaces holding different pointers came out equal. Go
		// compares the dynamic type AND the value, which is what the two words are.
		//
		// nil in the other three positions did not compile at all, each in its own
		// way: "i == nil" called the struct helper with the null POINTER constant,
		// "i = nil" assigned 0 to a two-word struct, and "return nil" returned it.
		// Only "var i I" (no initializer) was right, which is why the gap held: the
		// common spelling of the zero interface was the one that worked.
		//
		// Every line matches real Go.
		name: "interface equality and nil",
		src: `type I interface{ m() int }

type J interface{ m() int }

type T struct{ n int }

func (t *T) m() int { return t.n }

type U struct{ n int }

func (u *U) m() int { return u.n }

var a T
var b T
var c U

func pick(k int) I {
	switch k {
	case 0:
		return &a
	case 1:
		return &c
	}
	return nil
}

func main() {
	a.n, b.n, c.n = 1, 2, 3

	var i I
	println(i == nil, i != nil)
	i = &a
	println(i == nil, i != nil)
	i = nil
	println(i == nil, i != nil)

	var z I = nil
	println(z == nil)

	var x I = &a
	var y I = &a
	var w I = &b
	var v I = &c
	println(x == y, x == w, x == v, x != w)

	println(nil == i, pick(0) == nil, pick(2) == nil, pick(1) != nil)
	println(pick(0) == x, pick(1) == v)

	var q J = x
	println(q.m(), x.m())
}
`,
		want: "true false\nfalse true\ntrue false\ntrue\n" +
			"true false false true\ntrue false true true\ntrue true\n1 1\n",
	},
	{
		// nil written into a FIELD, for the two types whose nil is a whole struct
		// rather than a word. Assigning it to a plain variable was right and
		// assigning it to a field was not -- the branch that knew about it asked
		// only about a bare name -- so "h.s = nil" and "h.i = nil" emitted "= 0"
		// against a three-word header and a two-word interface.
		//
		// The indexed base is here because it reaches the field by a different
		// path again. Every line matches real Go.
		name: "nil written into a field",
		src: `type I interface{ m() int }

type T struct{ n int }

func (t *T) m() int { return t.n }

type holder struct {
	i I
	s []int
}

var g T
var back [2]int

func main() {
	var h holder
	println(h.i == nil, h.s == nil)
	h.i = &g
	h.s = back[:1]
	println(h.i == nil, h.s == nil, h.i.m())
	h.i = nil
	h.s = nil
	println(h.i == nil, h.s == nil)

	var arr [2]holder
	arr[0].i = &g
	println(arr[0].i == nil, arr[1].i == nil)
	arr[0].i = nil
	println(arr[0].i == nil)
}
`,
		want: "true true\nfalse false 0\ntrue true\nfalse true\ntrue\n",
	},
	{
		// A constant that does not fit a signed int but DOES fit an unsigned one --
		// 0xFFFFFFFF, and anything else above 2^31 -- is written with a U suffix, so
		// it and the expression around it stay 32 bits wide.
		//
		// It used to be written LL, which made "m ^ 0xFFFFFFFF" for a uint32 m a
		// long long, and the TARGET C compiler refuses the printf that feeds: "Bad
		// number of parameters in call to _basic_print_unsigned: expected 4 found
		// 5", a 64-bit argument taking two slots where %u wants one. The build
		// failed outright, so it was never a wrong answer -- but only on the target:
		// gcc accepts the same C, so the host suite was green and nothing said
		// anything until the program was built for a board.
		//
		// The uint64 and int64 lines are here so the widening that IS needed is not
		// lost with it. Every line matches real Go.
		name: "a constant that fits only an unsigned int",
		src: `func main() {
	var m uint32 = 0xF0F0F0F0
	println(m&^0x0F0F0F0F, m|0x0F0F0F0F, m^0xFFFFFFFF)

	var h uint32 = 2166136261
	h ^= 0xFF
	h *= 16777619
	println(h)

	var p uint32 = 1
	println(p + 4294967294)

	var u uint64 = 4294967295
	println(u+1, u*2)

	var i int64 = 4294967295
	println(i*2, i+1)

	var n uint32 = 4042322160
	println(n, n/2)
}
`,
		want: "4042322160 4294967295 252645135\n" +
			"2047574606\n" +
			"4294967295\n" +
			"4294967296 8589934590\n" +
			"8589934590 4294967296\n" +
			"4042322160 2021161080\n",
	},
	{
		// An interface-to-interface question -- "case N:" in a type switch, and the
		// assertion "r.(N)" -- asks which concrete types satisfy BOTH interfaces,
		// and asked it with a direct method lookup that a PROMOTED method is
		// invisible to. A sensor embedding a base gets v() from the base, so it was
		// not counted as implementing R, so no candidate was left to test and the
		// question was emitted as a constant 0: the N case was skipped and the
		// assertion answered false, silently, for a value that satisfies both.
		//
		// needVTable already resolved through the embedding chain, so the two
		// disagreed about the same fact. This is the third copy of "does this type
		// implement that interface"; 81d50b7 fixed the checker's and needVTable's.
		//
		// plain, which embeds the same base and declares no nm(), is here so the
		// case can be seen to be answered rather than merely taken. Every line
		// matches real Go.
		name: "an interface case reached through embedding",
		src: `type R interface{ v() int }

type N interface{ nm() string }

type base struct{ n int }

func (b *base) v() int { return b.n }

type sensor struct{ base }

func (s *sensor) nm() string { return "s1" }

type plain struct{ base }

func main() {
	sn := sensor{base{5}}
	pl := plain{base{7}}
	var rs []R = make([]R, 2)
	rs[0] = &sn
	rs[1] = &pl
	for i := 0; i < 2; i++ {
		r := rs[i]
		switch t := r.(type) {
		case N:
			println("N", t.nm())
		default:
			println("plain", r.v())
		}
		q, ok := r.(N)
		if ok {
			println("assert", q.nm())
		} else {
			println("assert no")
		}
	}
}
`,
		want: "N s1\nassert s1\nplain 7\nassert no\n",
	},
	{
		// A type switch may switch on any interface EXPRESSION, not only on a name:
		// "switch t := shapes[i].(type)" is how a dispatch loop is written, and it
		// used to fail with "cannot infer the type of the switch guard variable"
		// (an index) or "b.r has no field" (a field), neither of which named the
		// real limit -- everything below the guard reads the operand by name, once
		// per case.
		//
		// The operand is now bound to a temporary, which is also what makes it
		// evaluated ONCE however many cases test it: "calls" below is 1, as in Go.
		//
		// Every line matches real Go.
		name: "a type switch on an expression",
		src: `type R interface{ v() int }

type A struct{ n int }

func (a *A) v() int { return a.n }

type B struct{ m int }

func (b *B) v() int { return b.m }

type Box struct{ r R }

var calls int

func pick(rs []R, i int) R {
	calls++
	return rs[i]
}

func main() {
	var x A
	var y B
	x.n = 4
	y.m = 9
	var rs []R = make([]R, 2)
	rs[0] = &x
	rs[1] = &y

	for i := 0; i < 2; i++ {
		switch t := rs[i].(type) {
		case *A:
			println("A", t.n)
		case *B:
			println("B", t.m)
		default:
			println("other")
		}
	}

	var bx Box
	bx.r = &y
	switch t := bx.r.(type) {
	case *A:
		println("box A", t.n)
	case *B:
		println("box B", t.m)
	}

	switch t := pick(rs, 0).(type) {
	case *A:
		println("picked A", t.n)
	case *B:
		println("picked B", t.m)
	}
	println("calls", calls)

	switch rs[1].(type) {
	case *A:
		println("bare A")
	case *B:
		println("bare B")
	}
}
`,
		want: "A 4\nB 9\nbox B 9\npicked A 4\ncalls 1\nbare B\n",
	},
	{
		// A defer written inside an if runs only if that branch did, which a runtime
		// flag records -- and the flag has to guard the WHOLE call. It was written as
		// a statement prefix, "if (flag) f(...);", on the assumption that a call is
		// one C statement. println of several arguments is one printf per argument,
		// so the flag guarded the first and let the rest run: a branch that never
		// executed still printed the tail of its deferred println, from capture
		// temporaries that were never written. "println(f(1))" printed a bare " 0"
		// between its two real lines.
		//
		// The golden for nested defer deferred a call of ONE statement, so it agreed
		// with the emitter either way; only running the program showed it.
		//
		// Every line matches real Go.
		name: "a defer inside a branch that did not run",
		src: `func f(n int) int {
	defer println("exit", n)
	if n > 2 {
		defer println("big", n, "x")
		return n * 10
	}
	defer println("small", n)
	return n
}

func g(n int) {
	if n == 0 {
		defer println("zero", n, n+1, n+2)
	}
	println("g done", n)
}

func main() {
	println(f(1))
	println(f(5))
	g(0)
	g(1)
}
`,
		want: "small 1\nexit 1\n1\nbig 5 x\nexit 5\n50\ng done 0\nzero 0 1 2\ng done 1\n",
	},
	{
		// A shift by a count that is not a compile-time constant, over every integer
		// width and every width of count. It goes through the guarded helper, which
		// is what makes a shift mean in C what it means in Go -- a count at or past
		// the value's width gives 0, or -1 for an arithmetic right shift of a
		// negative value, where C would take the count modulo the width.
		//
		// Nothing here had a test of its own, and TWO backend faults were living in
		// the gap. The 64-bit left shift came back wrong for every variable count,
		// its helper casting a 64-bit expression back to a 64-bit type; and a
		// 64-bit value written as an EXPRESSION with a narrower count -- the
		// "(s<<62)>>n32" line -- had the count passed in one slot where two were
		// wanted, so the callee read its high word out of the frame and shifted by
		// garbage, or panicked on a count that came out negative. Both are worked
		// around in shiftHelperDef and shiftCountC.
		//
		// Every line matches real Go, which is where the expected output came from,
		// and every line has been read off a P2-EDGE.
		name: "shift by a variable count",
		src: `func main() {
	var v int64 = 81985529216486895
	var w int64 = -81985529216486895
	var u uint64 = 18364758544493064720
	var n32 int32 = 3
	var n64 int64 = 3
	var nu uint = 3
	var s int64 = 1

	println(v<<n32, v<<n64, v<<nu)
	println(v>>n32, w>>n32, u>>n32)

	var k int = 63
	println(v<<k, w>>k, u>>k)
	k = 64
	println(v<<k, w>>k, u>>k)
	k = 100
	println(v<<k, w>>k, u>>k)

	println((s<<62)>>n32, (v+v)>>n32, (u+u)>>n32)

	var c int64 = 1
	c <<= n32
	c >>= n32
	println(c)

	var e uint32 = 3000000000
	var f uint8 = 200
	println(e<<n32, e>>n32, f<<n32, f>>n32)
}
`,
		want: "655884233731895160 655884233731895160 655884233731895160\n" +
			"10248191152060861 -10248191152060862 2295594818061633090\n" +
			"-9223372036854775808 -1 1\n" +
			"0 -1 0\n" +
			"0 -1 0\n" +
			"576460752303423488 20496382304121723 2285346626909572228\n" +
			"1\n" +
			"2525163520 375000000 64 25\n",
	},
	{
		// int and uint are types of their OWN, distinct from int32 and uint32 even
		// though all four are 32 bits wide here, while byte and rune are ALIASES of
		// uint8 and int32 and so mix with them freely. A rune literal defaults to
		// rune where an integer literal defaults to int, and a constant written
		// through a conversion is a TYPED constant that types what it is combined
		// with. "%T" reports what each of those decided; every line matches real Go,
		// which is where the expected output came from.
		name: "int is not int32",
		src: `const (
	fracBits = 16
	one      = int32(1) << fracBits
	half     = one / 2
	typedU   = uint16(40000)
)

func take32(v int32) int32 { return v }

func takeInt(v int) int { return v }

func takeU(v uint) uint { return v }

func takeByte(v byte) byte { return v }

func takeRune(v rune) rune { return v }

func take16(v uint16) uint16 { return v }

func main() {
	scale := 50 * one
	printf("%T %T %T\n", scale, half, typedU)
	println(take32(scale), take32(half), take16(typedU))

	r := 'A'
	n := 65
	var u uint = 65
	printf("%T %T %T\n", r, n, u)
	println(takeRune(r), takeInt(n), takeU(u))

	var b byte = 'z'
	var u8 uint8 = b
	var i32 int32 = r
	println(takeByte(u8), take32(i32), takeRune(i32))

	var cnt uint = 3
	var v32 int32 = 5
	var v64 int64 = 5
	println(v32<<cnt, v64<<cnt, n<<cnt, b>>1)

	var w8 int8 = 100
	var w64 uint64 = 1 << 40
	println(w8+27, w64/2, takeInt(fracBits))
}
`,
		want: "int32 int32 uint16\n" +
			"3276800 32768 40000\n" +
			"int32 int uint\n" +
			"65 65 65\n" +
			"122 65 65\n" +
			"40 40 520 61\n" +
			"127 549755813888 16\n",
	},
	{
		// A ":="-inferred value takes the type of the operand that HAS one, not the
		// type of whichever operand comes first. Writing the untyped constant on the
		// left used to name the variable's type after it: "b := 1 + v" for an int64
		// v was declared int and truncated 1099511627777 to 1, "d := 2 * f" dropped a
		// float64's fraction, and "w := 1 + u" wrapped a uint32 past 2^31 to a
		// negative. Each pair below writes the same operation both ways round; every
		// line matches real Go.
		name: "an inferred value takes the type of the typed operand",
		src: `const (
	fracBits = 16
	one      = int32(1) << fracBits
)

func take32(v int32) int32 { return v }

func main() {
	var v int64 = 1 << 40
	println(v+1, 1+v)

	var f float64 = 1.7
	println(f*2, 2*f)

	var g float32 = 0.5
	println(g+1, 1+g)

	var u uint32 = 3000000000
	println(u+1, 1+u)

	// A typed constant types the expression the same way a variable does, and an
	// untyped one (fracBits) still contributes no type of its own.
	scale := 50 * one
	println(take32(scale), take32(fracBits*one))

	// A shift keeps the type being SHIFTED, whatever the count is typed as.
	var cnt uint = 3
	println(v<<cnt, 1<<cnt)
}
`,
		want: "1099511627777 1099511627777\n" +
			"3.4 3.4\n" +
			"1.5 1.5\n" +
			"3000000001 3000000001\n" +
			"3276800 1048576\n" +
			"8796093022208 8\n",
	},
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
		// The predeclared Builder held in a struct FIELD, which a parser that owns
		// its own line buffer is written with. Two things stood in the way, and
		// neither was about the field itself.
		//
		// The Builder TYPEDEF was emitted after the struct typedefs, on the stated
		// grounds that it embeds the string and byte-slice types -- it embeds
		// neither, its helpers do -- so `struct Line { ogo_builder sb; }` named a
		// type C had not seen and the program did not compile at all.
		//
		// And the method set is the COMPILER's rather than a declaration's, which
		// the variable path knew and the field path did not: `l.sb.Len()` was "type
		// Builder has no method Len", of a method it certainly has.
		name: "a Builder in a struct field",
		src: `var back [32]byte

type Line struct {
	sb Builder
	n  int
}

func (l *Line) Add(c byte) {
	l.sb.WriteByte(c)
	l.n++
}

func (l *Line) Text() string {
	return l.sb.String()
}

func main() {
	var l Line
	l.sb = NewBuilder(back[:])
	l.Add('a')
	l.Add('b')
	l.Add('c')
	println(l.Text(), l.n, l.sb.Len())
	l.sb.Reset()
	l.sb.WriteString("xy")
	println(l.sb.String(), l.sb.Len())
}
`,
		want: "abc 3 3\nxy 2\n",
	},
	{
		// A package ARRAY literal whose elements are not constant. C evaluates a
		// static initializer at compile time, so a call in one is not a program the
		// backend will take -- and it said so about generated C the program never
		// wrote ("global initializers ... must be constant"), which is worse than
		// any refusal. The table is zeroed and filled at package initialization
		// instead, which the scalar and struct forms already did.
		//
		// A calibration table computed from a conversion is what this is for, and
		// what found it.
		name: "a package array literal with computed elements",
		src: `type Fix int32

func FromInt(n int) Fix {
	return Fix(n << 8)
}

type Point struct {
	raw Fix
	val Fix
}

var scale = FromInt(2)

var curve = [3]Point{
	{FromInt(0), FromInt(10)},
	{FromInt(100), scale},
	{FromInt(200), FromInt(60)},
}

var plain = [2]int{seed(), 5}

var mixed = [4]int{1, seed(), 3, 4}

func seed() int {
	return 7
}

func main() {
	println(int(curve[0].val>>8), int(curve[1].val>>8), int(curve[2].raw>>8))
	println(plain[0], plain[1])
	println(mixed[0], mixed[1], mixed[3])
}
`,
		want: "10 2 200\n7 5\n1 7 4\n",
	},
	{
		// A METHOD called on a PARENTHESISED expression, which fixed-point code is
		// written in: `(raw - lo).Div(span)`. Every other receiver shape worked --
		// a variable, a parenthesised variable, a field, an element, a call's
		// result -- and so did binding the arithmetic to a variable first, so the
		// workaround was accepted while the plain spelling drew "this form is not
		// supported yet".
		//
		// The receiver needs no name: a value receiver is passed by value, so the
		// expression is the argument. A chain wraps, each call becoming the
		// receiver of the next.
		name: "a method on a parenthesised expression",
		src: `type Fix int32

func (a Fix) Add(b Fix) Fix {
	return a + b
}

func (a Fix) Scale(n int) Fix {
	return a * Fix(n)
}

func (a Fix) Int() int {
	return int(a)
}

type P struct{ x, y int }

func (p P) Sum() int {
	return p.x + p.y
}

func (p *P) Bump() int {
	p.x++
	return p.x
}

func mk(n int) Fix {
	return Fix(n)
}

func main() {
	var x, y Fix = 5, 3
	println((x - y).Int())
	println((x - y).Add(10).Int())
	println((x + y).Scale(3).Int())
	println((x - y).Add(1).Scale(2).Int())

	// The shapes that already worked, so the last-resort placement is held to.
	q := P{1, 2}
	println((x).Int(), mk(9).Int(), q.Sum())
	d := x - y
	println(d.Int())
	p := &P{3, 4}
	println(p.Sum(), (*p).Sum(), (p).Sum())

	// The parenthesised expression may itself be a POINTER. A value method takes
	// what it points at, a pointer method takes it as it stands -- and the call is
	// typed by the METHOD's result, not by the address it is called on.
	v := P{1, 2}
	println((&v).Sum(), (&P{5, 6}).Sum(), (&P{5, 6}).Bump())

	// Left to right, which needs the effect analysis to see this call shape: the
	// second of these changes what the third reads.
	w := P{1, 2}
	println((&w).Sum(), (&w).Bump(), (&w).Sum())
}
`,
		want: "2\n12\n24\n6\n5 9 3\n2\n7 7 7\n3 11 6\n3 2 4\n",
	},
	{
		// `string(r)` for a rune the program COMPUTES, which is what
		// `for _, r := range s { print(string(r)) }` needs -- about as ordinary as
		// Go gets, and refused outright until now.
		//
		// A rune's UTF-8 is at most four bytes, so the storage is four bytes of the
		// frame hoisted beside the statement. That bound is the whole argument: a
		// byte SLICE's length is the slice's, so that conversion is still refused.
		//
		// The result is a VIEW of those four bytes, so the lifetime rules police it
		// exactly as they police a slice over a local array -- see the refusals in
		// TestEmitCRuneStringEscape. Everything here keeps it inside the block it
		// was minted in.
		name: "a computed rune converts to a string",
		src: `func take(s string) int { return len(s) }

func main() {
	s := "h\u00e9llo, \u4e16\u754c"
	for _, r := range s {
		print(string(r))
	}
	println()

	var r rune = 'A'
	r++
	t := string(r)
	println(t, len(t), take(string(r)))
	println(string(r) == "B", string(r) > "A")

	switch string(r) {
	case "B":
		println("matched")
	}

	// Every rune of the string, converted back and measured: the bytes add up to
	// the string's own length.
	n := 0
	for _, c := range s {
		n += len(string(c))
	}
	println(n, len(s))

	// A byte and a computed code point, the other two spellings of the operand.
	b := byte('z')
	i := 0x1F600
	println(string(b), string(rune(i)), len(string(rune(i))))
}
`,
		want: "h\u00e9llo, \u4e16\u754c\nB 1 1\ntrue true\nmatched\n14 14\nz \U0001f600 4\n",
	},
	{
		// printf against Go's fmt, which is what it is meant to be: every verb the
		// format accepts, at each type's extremes, with the widths, the alignments
		// and the flags. Generated, and split three ways because the board's
		// harness does not carry many more lines than this in one program.
		//
		// It found one defect: `%+d` of an UNSIGNED value dropped the sign, C's "+"
		// flag applying to its signed conversions only. Everything else -- %x of a
		// negative, %c of a multi-byte rune, %.3s counting RUNES, %T, the zero and
		// left-align flags -- already matched.
		name: "printf verbs on signed integers",
		src: `var v_int8_0 int8 = -128
var v_int8_1 int8 = -1
var v_int8_2 int8 = 127
var v_int16_0 int16 = -32768
var v_int16_1 int16 = -1
var v_int16_2 int16 = 32767
var v_int32_0 int32 = -2147483648
var v_int32_1 int32 = -1
var v_int32_2 int32 = 2147483647
var v_int64_0 int64 = -9223372036854775808
var v_int64_1 int64 = -1
var v_int64_2 int64 = 9223372036854775807

func f_int8() {
	printf("[%d]\n", v_int8_0)
	printf("[%v]\n", v_int8_0)
	printf("[%T]\n", v_int8_0)
	printf("[%8d]\n", v_int8_0)
	printf("[%-8d|]\n", v_int8_0)
	printf("[%08d]\n", v_int8_0)
	printf("[%+d]\n", v_int8_0)
	printf("[%x]\n", v_int8_0)
	printf("[%X]\n", v_int8_0)
	printf("[%d]\n", v_int8_1)
	printf("[%v]\n", v_int8_1)
	printf("[%T]\n", v_int8_1)
	printf("[%8d]\n", v_int8_1)
	printf("[%-8d|]\n", v_int8_1)
	printf("[%08d]\n", v_int8_1)
	printf("[%+d]\n", v_int8_1)
	printf("[%x]\n", v_int8_1)
	printf("[%X]\n", v_int8_1)
	printf("[%d]\n", v_int8_2)
	printf("[%v]\n", v_int8_2)
	printf("[%T]\n", v_int8_2)
	printf("[%8d]\n", v_int8_2)
	printf("[%-8d|]\n", v_int8_2)
	printf("[%08d]\n", v_int8_2)
	printf("[%+d]\n", v_int8_2)
	printf("[%x]\n", v_int8_2)
	printf("[%X]\n", v_int8_2)
}

func f_int16() {
	printf("[%d]\n", v_int16_0)
	printf("[%v]\n", v_int16_0)
	printf("[%T]\n", v_int16_0)
	printf("[%8d]\n", v_int16_0)
	printf("[%-8d|]\n", v_int16_0)
	printf("[%08d]\n", v_int16_0)
	printf("[%+d]\n", v_int16_0)
	printf("[%x]\n", v_int16_0)
	printf("[%X]\n", v_int16_0)
	printf("[%d]\n", v_int16_1)
	printf("[%v]\n", v_int16_1)
	printf("[%T]\n", v_int16_1)
	printf("[%8d]\n", v_int16_1)
	printf("[%-8d|]\n", v_int16_1)
	printf("[%08d]\n", v_int16_1)
	printf("[%+d]\n", v_int16_1)
	printf("[%x]\n", v_int16_1)
	printf("[%X]\n", v_int16_1)
	printf("[%d]\n", v_int16_2)
	printf("[%v]\n", v_int16_2)
	printf("[%T]\n", v_int16_2)
	printf("[%8d]\n", v_int16_2)
	printf("[%-8d|]\n", v_int16_2)
	printf("[%08d]\n", v_int16_2)
	printf("[%+d]\n", v_int16_2)
	printf("[%x]\n", v_int16_2)
	printf("[%X]\n", v_int16_2)
}

func f_int32() {
	printf("[%d]\n", v_int32_0)
	printf("[%v]\n", v_int32_0)
	printf("[%T]\n", v_int32_0)
	printf("[%8d]\n", v_int32_0)
	printf("[%-8d|]\n", v_int32_0)
	printf("[%08d]\n", v_int32_0)
	printf("[%+d]\n", v_int32_0)
	printf("[%x]\n", v_int32_0)
	printf("[%X]\n", v_int32_0)
	printf("[%d]\n", v_int32_1)
	printf("[%v]\n", v_int32_1)
	printf("[%T]\n", v_int32_1)
	printf("[%8d]\n", v_int32_1)
	printf("[%-8d|]\n", v_int32_1)
	printf("[%08d]\n", v_int32_1)
	printf("[%+d]\n", v_int32_1)
	printf("[%x]\n", v_int32_1)
	printf("[%X]\n", v_int32_1)
	printf("[%d]\n", v_int32_2)
	printf("[%v]\n", v_int32_2)
	printf("[%T]\n", v_int32_2)
	printf("[%8d]\n", v_int32_2)
	printf("[%-8d|]\n", v_int32_2)
	printf("[%08d]\n", v_int32_2)
	printf("[%+d]\n", v_int32_2)
	printf("[%x]\n", v_int32_2)
	printf("[%X]\n", v_int32_2)
}

func f_int64() {
	printf("[%d]\n", v_int64_0)
	printf("[%v]\n", v_int64_0)
	printf("[%T]\n", v_int64_0)
	printf("[%8d]\n", v_int64_0)
	printf("[%-8d|]\n", v_int64_0)
	printf("[%08d]\n", v_int64_0)
	printf("[%+d]\n", v_int64_0)
	printf("[%x]\n", v_int64_0)
	printf("[%X]\n", v_int64_0)
	printf("[%d]\n", v_int64_1)
	printf("[%v]\n", v_int64_1)
	printf("[%T]\n", v_int64_1)
	printf("[%8d]\n", v_int64_1)
	printf("[%-8d|]\n", v_int64_1)
	printf("[%08d]\n", v_int64_1)
	printf("[%+d]\n", v_int64_1)
	printf("[%x]\n", v_int64_1)
	printf("[%X]\n", v_int64_1)
	printf("[%d]\n", v_int64_2)
	printf("[%v]\n", v_int64_2)
	printf("[%T]\n", v_int64_2)
	printf("[%8d]\n", v_int64_2)
	printf("[%-8d|]\n", v_int64_2)
	printf("[%08d]\n", v_int64_2)
	printf("[%+d]\n", v_int64_2)
	printf("[%x]\n", v_int64_2)
	printf("[%X]\n", v_int64_2)
}

func main() {
	f_int8()
	f_int16()
	f_int32()
	f_int64()
}
`,
		want: "[-128]\n[-128]\n[int8]\n[    -128]\n[-128    |]\n[-0000128]\n[-128]\n[-80]\n[-80]\n[-1]\n[-1]\n[int8]\n[      -1]\n[-1      |]\n[-0000001]\n[-1]\n[-1]\n[-1]\n[127]\n[127]\n[int8]\n[     127]\n[127     |]\n[00000127]\n[+127]\n[7f]\n[7F]\n[-32768]\n[-32768]\n[int16]\n[  -32768]\n[-32768  |]\n[-0032768]\n[-32768]\n[-8000]\n[-8000]\n[-1]\n[-1]\n[int16]\n[      -1]\n[-1      |]\n[-0000001]\n[-1]\n[-1]\n[-1]\n[32767]\n[32767]\n[int16]\n[   32767]\n[32767   |]\n[00032767]\n[+32767]\n[7fff]\n[7FFF]\n[-2147483648]\n[-2147483648]\n[int32]\n[-2147483648]\n[-2147483648|]\n[-2147483648]\n[-2147483648]\n[-80000000]\n[-80000000]\n[-1]\n[-1]\n[int32]\n[      -1]\n[-1      |]\n[-0000001]\n[-1]\n[-1]\n[-1]\n[2147483647]\n[2147483647]\n[int32]\n[2147483647]\n[2147483647|]\n[2147483647]\n[+2147483647]\n[7fffffff]\n[7FFFFFFF]\n[-9223372036854775808]\n[-9223372036854775808]\n[int64]\n[-9223372036854775808]\n[-9223372036854775808|]\n[-9223372036854775808]\n[-9223372036854775808]\n[-8000000000000000]\n[-8000000000000000]\n[-1]\n[-1]\n[int64]\n[      -1]\n[-1      |]\n[-0000001]\n[-1]\n[-1]\n[-1]\n[9223372036854775807]\n[9223372036854775807]\n[int64]\n[9223372036854775807]\n[9223372036854775807|]\n[9223372036854775807]\n[+9223372036854775807]\n[7fffffffffffffff]\n[7FFFFFFFFFFFFFFF]\n",
	},
	{
		// The half that found the bug. `%+d` of an unsigned value is printed
		// through the SIGNED conversion, which carries the same digits and honours
		// the flag; a uint64 has no signed type wide enough and is refused instead,
		// so it is absent here.
		name: "printf verbs on unsigned integers",
		src: `var v_uint8_0 uint8 = 0
var v_uint8_1 uint8 = 129
var v_uint8_2 uint8 = 255
var v_uint16_0 uint16 = 0
var v_uint16_1 uint16 = 33000
var v_uint16_2 uint16 = 65535
var v_uint32_0 uint32 = 0
var v_uint32_1 uint32 = 2147483649
var v_uint32_2 uint32 = 4294967295
var v_uint64_0 uint64 = 0
var v_uint64_1 uint64 = 9223372036854775809
var v_uint64_2 uint64 = 18446744073709551615

func f_uint8() {
	printf("[%d]\n", v_uint8_0)
	printf("[%v]\n", v_uint8_0)
	printf("[%T]\n", v_uint8_0)
	printf("[%8d]\n", v_uint8_0)
	printf("[%-8d|]\n", v_uint8_0)
	printf("[%08d]\n", v_uint8_0)
	printf("[%+d]\n", v_uint8_0)
	printf("[%x]\n", v_uint8_0)
	printf("[%X]\n", v_uint8_0)
	printf("[%8x]\n", v_uint8_0)
	printf("[%08x]\n", v_uint8_0)
	printf("[%d]\n", v_uint8_1)
	printf("[%v]\n", v_uint8_1)
	printf("[%T]\n", v_uint8_1)
	printf("[%8d]\n", v_uint8_1)
	printf("[%-8d|]\n", v_uint8_1)
	printf("[%08d]\n", v_uint8_1)
	printf("[%+d]\n", v_uint8_1)
	printf("[%x]\n", v_uint8_1)
	printf("[%X]\n", v_uint8_1)
	printf("[%8x]\n", v_uint8_1)
	printf("[%08x]\n", v_uint8_1)
	printf("[%d]\n", v_uint8_2)
	printf("[%v]\n", v_uint8_2)
	printf("[%T]\n", v_uint8_2)
	printf("[%8d]\n", v_uint8_2)
	printf("[%-8d|]\n", v_uint8_2)
	printf("[%08d]\n", v_uint8_2)
	printf("[%+d]\n", v_uint8_2)
	printf("[%x]\n", v_uint8_2)
	printf("[%X]\n", v_uint8_2)
	printf("[%8x]\n", v_uint8_2)
	printf("[%08x]\n", v_uint8_2)
}

func f_uint16() {
	printf("[%d]\n", v_uint16_0)
	printf("[%v]\n", v_uint16_0)
	printf("[%T]\n", v_uint16_0)
	printf("[%8d]\n", v_uint16_0)
	printf("[%-8d|]\n", v_uint16_0)
	printf("[%08d]\n", v_uint16_0)
	printf("[%+d]\n", v_uint16_0)
	printf("[%x]\n", v_uint16_0)
	printf("[%X]\n", v_uint16_0)
	printf("[%8x]\n", v_uint16_0)
	printf("[%08x]\n", v_uint16_0)
	printf("[%d]\n", v_uint16_1)
	printf("[%v]\n", v_uint16_1)
	printf("[%T]\n", v_uint16_1)
	printf("[%8d]\n", v_uint16_1)
	printf("[%-8d|]\n", v_uint16_1)
	printf("[%08d]\n", v_uint16_1)
	printf("[%+d]\n", v_uint16_1)
	printf("[%x]\n", v_uint16_1)
	printf("[%X]\n", v_uint16_1)
	printf("[%8x]\n", v_uint16_1)
	printf("[%08x]\n", v_uint16_1)
	printf("[%d]\n", v_uint16_2)
	printf("[%v]\n", v_uint16_2)
	printf("[%T]\n", v_uint16_2)
	printf("[%8d]\n", v_uint16_2)
	printf("[%-8d|]\n", v_uint16_2)
	printf("[%08d]\n", v_uint16_2)
	printf("[%+d]\n", v_uint16_2)
	printf("[%x]\n", v_uint16_2)
	printf("[%X]\n", v_uint16_2)
	printf("[%8x]\n", v_uint16_2)
	printf("[%08x]\n", v_uint16_2)
}

func f_uint32() {
	printf("[%d]\n", v_uint32_0)
	printf("[%v]\n", v_uint32_0)
	printf("[%T]\n", v_uint32_0)
	printf("[%8d]\n", v_uint32_0)
	printf("[%-8d|]\n", v_uint32_0)
	printf("[%08d]\n", v_uint32_0)
	printf("[%+d]\n", v_uint32_0)
	printf("[%x]\n", v_uint32_0)
	printf("[%X]\n", v_uint32_0)
	printf("[%8x]\n", v_uint32_0)
	printf("[%08x]\n", v_uint32_0)
	printf("[%d]\n", v_uint32_1)
	printf("[%v]\n", v_uint32_1)
	printf("[%T]\n", v_uint32_1)
	printf("[%8d]\n", v_uint32_1)
	printf("[%-8d|]\n", v_uint32_1)
	printf("[%08d]\n", v_uint32_1)
	printf("[%+d]\n", v_uint32_1)
	printf("[%x]\n", v_uint32_1)
	printf("[%X]\n", v_uint32_1)
	printf("[%8x]\n", v_uint32_1)
	printf("[%08x]\n", v_uint32_1)
	printf("[%d]\n", v_uint32_2)
	printf("[%v]\n", v_uint32_2)
	printf("[%T]\n", v_uint32_2)
	printf("[%8d]\n", v_uint32_2)
	printf("[%-8d|]\n", v_uint32_2)
	printf("[%08d]\n", v_uint32_2)
	printf("[%+d]\n", v_uint32_2)
	printf("[%x]\n", v_uint32_2)
	printf("[%X]\n", v_uint32_2)
	printf("[%8x]\n", v_uint32_2)
	printf("[%08x]\n", v_uint32_2)
}

func f_uint64() {
	printf("[%d]\n", v_uint64_0)
	printf("[%v]\n", v_uint64_0)
	printf("[%T]\n", v_uint64_0)
	printf("[%8d]\n", v_uint64_0)
	printf("[%-8d|]\n", v_uint64_0)
	printf("[%08d]\n", v_uint64_0)
	printf("[%x]\n", v_uint64_0)
	printf("[%X]\n", v_uint64_0)
	printf("[%8x]\n", v_uint64_0)
	printf("[%08x]\n", v_uint64_0)
	printf("[%d]\n", v_uint64_1)
	printf("[%v]\n", v_uint64_1)
	printf("[%T]\n", v_uint64_1)
	printf("[%8d]\n", v_uint64_1)
	printf("[%-8d|]\n", v_uint64_1)
	printf("[%08d]\n", v_uint64_1)
	printf("[%x]\n", v_uint64_1)
	printf("[%X]\n", v_uint64_1)
	printf("[%8x]\n", v_uint64_1)
	printf("[%08x]\n", v_uint64_1)
	printf("[%d]\n", v_uint64_2)
	printf("[%v]\n", v_uint64_2)
	printf("[%T]\n", v_uint64_2)
	printf("[%8d]\n", v_uint64_2)
	printf("[%-8d|]\n", v_uint64_2)
	printf("[%08d]\n", v_uint64_2)
	printf("[%x]\n", v_uint64_2)
	printf("[%X]\n", v_uint64_2)
	printf("[%8x]\n", v_uint64_2)
	printf("[%08x]\n", v_uint64_2)
}

func main() {
	f_uint8()
	f_uint16()
	f_uint32()
	f_uint64()
}
`,
		want: "[0]\n[0]\n[uint8]\n[       0]\n[0       |]\n[00000000]\n[+0]\n[0]\n[0]\n[       0]\n[00000000]\n[129]\n[129]\n[uint8]\n[     129]\n[129     |]\n[00000129]\n[+129]\n[81]\n[81]\n[      81]\n[00000081]\n[255]\n[255]\n[uint8]\n[     255]\n[255     |]\n[00000255]\n[+255]\n[ff]\n[FF]\n[      ff]\n[000000ff]\n[0]\n[0]\n[uint16]\n[       0]\n[0       |]\n[00000000]\n[+0]\n[0]\n[0]\n[       0]\n[00000000]\n[33000]\n[33000]\n[uint16]\n[   33000]\n[33000   |]\n[00033000]\n[+33000]\n[80e8]\n[80E8]\n[    80e8]\n[000080e8]\n[65535]\n[65535]\n[uint16]\n[   65535]\n[65535   |]\n[00065535]\n[+65535]\n[ffff]\n[FFFF]\n[    ffff]\n[0000ffff]\n[0]\n[0]\n[uint32]\n[       0]\n[0       |]\n[00000000]\n[+0]\n[0]\n[0]\n[       0]\n[00000000]\n[2147483649]\n[2147483649]\n[uint32]\n[2147483649]\n[2147483649|]\n[2147483649]\n[+2147483649]\n[80000001]\n[80000001]\n[80000001]\n[80000001]\n[4294967295]\n[4294967295]\n[uint32]\n[4294967295]\n[4294967295|]\n[4294967295]\n[+4294967295]\n[ffffffff]\n[FFFFFFFF]\n[ffffffff]\n[ffffffff]\n[0]\n[0]\n[uint64]\n[       0]\n[0       |]\n[00000000]\n[0]\n[0]\n[       0]\n[00000000]\n[9223372036854775809]\n[9223372036854775809]\n[uint64]\n[9223372036854775809]\n[9223372036854775809|]\n[9223372036854775809]\n[8000000000000001]\n[8000000000000001]\n[8000000000000001]\n[8000000000000001]\n[18446744073709551615]\n[18446744073709551615]\n[uint64]\n[18446744073709551615]\n[18446744073709551615|]\n[18446744073709551615]\n[ffffffffffffffff]\n[FFFFFFFFFFFFFFFF]\n[ffffffffffffffff]\n[ffffffffffffffff]\n",
	},
	{
		// The non-integer half. `%.3s` is a precision in RUNES, so "héllo" gives
		// "hél" and never half a character; the float values are exactly
		// representable in 32 bits, float64 being 32-bit on this target.
		name: "printf verbs on strings, bools, floats and runes",
		src: `var s0 string = "h\u00e9llo"
var s1 string = ""
var b0 bool = true
var b1 bool = false
var f0 float64 = 2.5
var f1 float64 = -0.25
var r0 rune = 0x4E16

func main() {
	printf("[%s]\n", s0)
	printf("[%s]\n", s1)
	printf("[%v]\n", s0)
	printf("[%v]\n", s1)
	printf("[%T]\n", s0)
	printf("[%T]\n", s1)
	printf("[%10s|]\n", s0)
	printf("[%10s|]\n", s1)
	printf("[%-10s|]\n", s0)
	printf("[%-10s|]\n", s1)
	printf("[%.3s]\n", s0)
	printf("[%.3s]\n", s1)
	printf("[%t]\n", b0)
	printf("[%t]\n", b1)
	printf("[%v]\n", b0)
	printf("[%v]\n", b1)
	printf("[%T]\n", b0)
	printf("[%T]\n", b1)
	printf("[%f]\n", f0)
	printf("[%f]\n", f1)
	printf("[%.2f]\n", f0)
	printf("[%.2f]\n", f1)
	printf("[%8.2f]\n", f0)
	printf("[%8.2f]\n", f1)
	printf("[%-8.2f|]\n", f0)
	printf("[%-8.2f|]\n", f1)
	printf("[%+.1f]\n", f0)
	printf("[%+.1f]\n", f1)
	printf("[%v]\n", f0)
	printf("[%v]\n", f1)
	printf("[%T]\n", f0)
	printf("[%T]\n", f1)
	printf("[%c]\n", r0)
	printf("[%d]\n", r0)
	printf("[%v]\n", r0)
	printf("[%T]\n", r0)
	printf("100%%\n")
}
`,
		want: "[héllo]\n[]\n[héllo]\n[]\n[string]\n[string]\n[     héllo|]\n[          |]\n[héllo     |]\n[          |]\n[hél]\n[]\n[true]\n[false]\n[true]\n[false]\n[bool]\n[bool]\n[2.500000]\n[-0.250000]\n[2.50]\n[-0.25]\n[    2.50]\n[   -0.25]\n[2.50    |]\n[-0.25   |]\n[+2.5]\n[-0.2]\n[2.5]\n[-0.25]\n[float64]\n[float64]\n[世]\n[19990]\n[19990]\n[int32]\n100%\n",
	},
	{
		// Integer conversions, every sized type to every other, at three values
		// apiece chosen to expose truncation and sign: each type's extremes and a
		// value with its high bit set. Nested conversions, conversions through
		// DEFINED types and back, a conversion of a call's result and of a
		// parenthesised expression come with them -- 1152 in all, generated for the
		// same reason as the arithmetic matrix below it: the interesting part is the
		// cross, and hand-written cases keep covering the diagonal.
		//
		// Unlike that one, this found NOTHING. It is kept because the class has a
		// history -- the "int is not int32" arc, and three silently truncating
		// miscompiles before it -- and because it is the cheap half of re-checking
		// the backend after a regeneration, which is when a conversion is most
		// likely to start lying.
		//
		// Float printing is deliberately not compared: float64 IS 32-bit on this
		// target (see specs.go), so the digits past the seventh are the host's and
		// not the board's, and %g's six are the honest answer here.
		name: "integer conversions",
		src: `type Small int8
type Wide int64
type UWide uint64
type Mid uint16

var v_int8_0 int8 = -128
var v_int8_1 int8 = -1
var v_int8_2 int8 = 127
var v_int16_0 int16 = -32768
var v_int16_1 int16 = -1
var v_int16_2 int16 = 32767
var v_int32_0 int32 = -2147483648
var v_int32_1 int32 = -1
var v_int32_2 int32 = 2147483647
var v_int64_0 int64 = -9223372036854775808
var v_int64_1 int64 = -1
var v_int64_2 int64 = 9223372036854775807
var v_uint8_0 uint8 = 0
var v_uint8_1 uint8 = 129
var v_uint8_2 uint8 = 255
var v_uint16_0 uint16 = 0
var v_uint16_1 uint16 = 33000
var v_uint16_2 uint16 = 65535
var v_uint32_0 uint32 = 0
var v_uint32_1 uint32 = 2147483649
var v_uint32_2 uint32 = 4294967295
var v_uint64_0 uint64 = 0
var v_uint64_1 uint64 = 9223372036854775809
var v_uint64_2 uint64 = 18446744073709551615

func id64(x int64) int64 { return x }

func c_int8() {
	println(int8(v_int8_0), int16(v_int8_0), int32(v_int8_0), int64(v_int8_0), uint8(v_int8_0), uint16(v_int8_0), uint32(v_int8_0), uint64(v_int8_0))
	println(int8(v_int8_0 + 1), int16(v_int8_0 + 1), int32(v_int8_0 + 1), int64(v_int8_0 + 1), uint8(v_int8_0 + 1), uint16(v_int8_0 + 1), uint32(v_int8_0 + 1), uint64(v_int8_0 + 1))
	println(int64(int8(int32(v_int8_0))), int64(uint8(uint32(v_int8_0))), int64(int16(uint8(v_int8_0))))
	println(int64(Small(v_int8_0)), int64(Wide(v_int8_0)), uint64(UWide(v_int8_0)), int64(Mid(v_int8_0)))
	println(int64(int8(Wide(v_int8_0))), uint64(uint32(UWide(v_int8_0))), int64(Mid(Small(v_int8_0))))
	println(int8(id64(int64(v_int8_0))), int16((int32(v_int8_0))), uint8(int64(v_int8_0)+0))
	println(int8(v_int8_1), int16(v_int8_1), int32(v_int8_1), int64(v_int8_1), uint8(v_int8_1), uint16(v_int8_1), uint32(v_int8_1), uint64(v_int8_1))
	println(int8(v_int8_1 + 1), int16(v_int8_1 + 1), int32(v_int8_1 + 1), int64(v_int8_1 + 1), uint8(v_int8_1 + 1), uint16(v_int8_1 + 1), uint32(v_int8_1 + 1), uint64(v_int8_1 + 1))
	println(int64(int8(int32(v_int8_1))), int64(uint8(uint32(v_int8_1))), int64(int16(uint8(v_int8_1))))
	println(int64(Small(v_int8_1)), int64(Wide(v_int8_1)), uint64(UWide(v_int8_1)), int64(Mid(v_int8_1)))
	println(int64(int8(Wide(v_int8_1))), uint64(uint32(UWide(v_int8_1))), int64(Mid(Small(v_int8_1))))
	println(int8(id64(int64(v_int8_1))), int16((int32(v_int8_1))), uint8(int64(v_int8_1)+0))
	println(int8(v_int8_2), int16(v_int8_2), int32(v_int8_2), int64(v_int8_2), uint8(v_int8_2), uint16(v_int8_2), uint32(v_int8_2), uint64(v_int8_2))
	println(int8(v_int8_2 + 1), int16(v_int8_2 + 1), int32(v_int8_2 + 1), int64(v_int8_2 + 1), uint8(v_int8_2 + 1), uint16(v_int8_2 + 1), uint32(v_int8_2 + 1), uint64(v_int8_2 + 1))
	println(int64(int8(int32(v_int8_2))), int64(uint8(uint32(v_int8_2))), int64(int16(uint8(v_int8_2))))
	println(int64(Small(v_int8_2)), int64(Wide(v_int8_2)), uint64(UWide(v_int8_2)), int64(Mid(v_int8_2)))
	println(int64(int8(Wide(v_int8_2))), uint64(uint32(UWide(v_int8_2))), int64(Mid(Small(v_int8_2))))
	println(int8(id64(int64(v_int8_2))), int16((int32(v_int8_2))), uint8(int64(v_int8_2)+0))
}

func c_int16() {
	println(int8(v_int16_0), int16(v_int16_0), int32(v_int16_0), int64(v_int16_0), uint8(v_int16_0), uint16(v_int16_0), uint32(v_int16_0), uint64(v_int16_0))
	println(int8(v_int16_0 + 1), int16(v_int16_0 + 1), int32(v_int16_0 + 1), int64(v_int16_0 + 1), uint8(v_int16_0 + 1), uint16(v_int16_0 + 1), uint32(v_int16_0 + 1), uint64(v_int16_0 + 1))
	println(int64(int8(int32(v_int16_0))), int64(uint8(uint32(v_int16_0))), int64(int16(uint8(v_int16_0))))
	println(int64(Small(v_int16_0)), int64(Wide(v_int16_0)), uint64(UWide(v_int16_0)), int64(Mid(v_int16_0)))
	println(int64(int8(Wide(v_int16_0))), uint64(uint32(UWide(v_int16_0))), int64(Mid(Small(v_int16_0))))
	println(int8(id64(int64(v_int16_0))), int16((int32(v_int16_0))), uint8(int64(v_int16_0)+0))
	println(int8(v_int16_1), int16(v_int16_1), int32(v_int16_1), int64(v_int16_1), uint8(v_int16_1), uint16(v_int16_1), uint32(v_int16_1), uint64(v_int16_1))
	println(int8(v_int16_1 + 1), int16(v_int16_1 + 1), int32(v_int16_1 + 1), int64(v_int16_1 + 1), uint8(v_int16_1 + 1), uint16(v_int16_1 + 1), uint32(v_int16_1 + 1), uint64(v_int16_1 + 1))
	println(int64(int8(int32(v_int16_1))), int64(uint8(uint32(v_int16_1))), int64(int16(uint8(v_int16_1))))
	println(int64(Small(v_int16_1)), int64(Wide(v_int16_1)), uint64(UWide(v_int16_1)), int64(Mid(v_int16_1)))
	println(int64(int8(Wide(v_int16_1))), uint64(uint32(UWide(v_int16_1))), int64(Mid(Small(v_int16_1))))
	println(int8(id64(int64(v_int16_1))), int16((int32(v_int16_1))), uint8(int64(v_int16_1)+0))
	println(int8(v_int16_2), int16(v_int16_2), int32(v_int16_2), int64(v_int16_2), uint8(v_int16_2), uint16(v_int16_2), uint32(v_int16_2), uint64(v_int16_2))
	println(int8(v_int16_2 + 1), int16(v_int16_2 + 1), int32(v_int16_2 + 1), int64(v_int16_2 + 1), uint8(v_int16_2 + 1), uint16(v_int16_2 + 1), uint32(v_int16_2 + 1), uint64(v_int16_2 + 1))
	println(int64(int8(int32(v_int16_2))), int64(uint8(uint32(v_int16_2))), int64(int16(uint8(v_int16_2))))
	println(int64(Small(v_int16_2)), int64(Wide(v_int16_2)), uint64(UWide(v_int16_2)), int64(Mid(v_int16_2)))
	println(int64(int8(Wide(v_int16_2))), uint64(uint32(UWide(v_int16_2))), int64(Mid(Small(v_int16_2))))
	println(int8(id64(int64(v_int16_2))), int16((int32(v_int16_2))), uint8(int64(v_int16_2)+0))
}

func c_int32() {
	println(int8(v_int32_0), int16(v_int32_0), int32(v_int32_0), int64(v_int32_0), uint8(v_int32_0), uint16(v_int32_0), uint32(v_int32_0), uint64(v_int32_0))
	println(int8(v_int32_0 + 1), int16(v_int32_0 + 1), int32(v_int32_0 + 1), int64(v_int32_0 + 1), uint8(v_int32_0 + 1), uint16(v_int32_0 + 1), uint32(v_int32_0 + 1), uint64(v_int32_0 + 1))
	println(int64(int8(int32(v_int32_0))), int64(uint8(uint32(v_int32_0))), int64(int16(uint8(v_int32_0))))
	println(int64(Small(v_int32_0)), int64(Wide(v_int32_0)), uint64(UWide(v_int32_0)), int64(Mid(v_int32_0)))
	println(int64(int8(Wide(v_int32_0))), uint64(uint32(UWide(v_int32_0))), int64(Mid(Small(v_int32_0))))
	println(int8(id64(int64(v_int32_0))), int16((int32(v_int32_0))), uint8(int64(v_int32_0)+0))
	println(int8(v_int32_1), int16(v_int32_1), int32(v_int32_1), int64(v_int32_1), uint8(v_int32_1), uint16(v_int32_1), uint32(v_int32_1), uint64(v_int32_1))
	println(int8(v_int32_1 + 1), int16(v_int32_1 + 1), int32(v_int32_1 + 1), int64(v_int32_1 + 1), uint8(v_int32_1 + 1), uint16(v_int32_1 + 1), uint32(v_int32_1 + 1), uint64(v_int32_1 + 1))
	println(int64(int8(int32(v_int32_1))), int64(uint8(uint32(v_int32_1))), int64(int16(uint8(v_int32_1))))
	println(int64(Small(v_int32_1)), int64(Wide(v_int32_1)), uint64(UWide(v_int32_1)), int64(Mid(v_int32_1)))
	println(int64(int8(Wide(v_int32_1))), uint64(uint32(UWide(v_int32_1))), int64(Mid(Small(v_int32_1))))
	println(int8(id64(int64(v_int32_1))), int16((int32(v_int32_1))), uint8(int64(v_int32_1)+0))
	println(int8(v_int32_2), int16(v_int32_2), int32(v_int32_2), int64(v_int32_2), uint8(v_int32_2), uint16(v_int32_2), uint32(v_int32_2), uint64(v_int32_2))
	println(int8(v_int32_2 + 1), int16(v_int32_2 + 1), int32(v_int32_2 + 1), int64(v_int32_2 + 1), uint8(v_int32_2 + 1), uint16(v_int32_2 + 1), uint32(v_int32_2 + 1), uint64(v_int32_2 + 1))
	println(int64(int8(int32(v_int32_2))), int64(uint8(uint32(v_int32_2))), int64(int16(uint8(v_int32_2))))
	println(int64(Small(v_int32_2)), int64(Wide(v_int32_2)), uint64(UWide(v_int32_2)), int64(Mid(v_int32_2)))
	println(int64(int8(Wide(v_int32_2))), uint64(uint32(UWide(v_int32_2))), int64(Mid(Small(v_int32_2))))
	println(int8(id64(int64(v_int32_2))), int16((int32(v_int32_2))), uint8(int64(v_int32_2)+0))
}

func c_int64() {
	println(int8(v_int64_0), int16(v_int64_0), int32(v_int64_0), int64(v_int64_0), uint8(v_int64_0), uint16(v_int64_0), uint32(v_int64_0), uint64(v_int64_0))
	println(int8(v_int64_0 + 1), int16(v_int64_0 + 1), int32(v_int64_0 + 1), int64(v_int64_0 + 1), uint8(v_int64_0 + 1), uint16(v_int64_0 + 1), uint32(v_int64_0 + 1), uint64(v_int64_0 + 1))
	println(int64(int8(int32(v_int64_0))), int64(uint8(uint32(v_int64_0))), int64(int16(uint8(v_int64_0))))
	println(int64(Small(v_int64_0)), int64(Wide(v_int64_0)), uint64(UWide(v_int64_0)), int64(Mid(v_int64_0)))
	println(int64(int8(Wide(v_int64_0))), uint64(uint32(UWide(v_int64_0))), int64(Mid(Small(v_int64_0))))
	println(int8(id64(int64(v_int64_0))), int16((int32(v_int64_0))), uint8(int64(v_int64_0)+0))
	println(int8(v_int64_1), int16(v_int64_1), int32(v_int64_1), int64(v_int64_1), uint8(v_int64_1), uint16(v_int64_1), uint32(v_int64_1), uint64(v_int64_1))
	println(int8(v_int64_1 + 1), int16(v_int64_1 + 1), int32(v_int64_1 + 1), int64(v_int64_1 + 1), uint8(v_int64_1 + 1), uint16(v_int64_1 + 1), uint32(v_int64_1 + 1), uint64(v_int64_1 + 1))
	println(int64(int8(int32(v_int64_1))), int64(uint8(uint32(v_int64_1))), int64(int16(uint8(v_int64_1))))
	println(int64(Small(v_int64_1)), int64(Wide(v_int64_1)), uint64(UWide(v_int64_1)), int64(Mid(v_int64_1)))
	println(int64(int8(Wide(v_int64_1))), uint64(uint32(UWide(v_int64_1))), int64(Mid(Small(v_int64_1))))
	println(int8(id64(int64(v_int64_1))), int16((int32(v_int64_1))), uint8(int64(v_int64_1)+0))
	println(int8(v_int64_2), int16(v_int64_2), int32(v_int64_2), int64(v_int64_2), uint8(v_int64_2), uint16(v_int64_2), uint32(v_int64_2), uint64(v_int64_2))
	println(int8(v_int64_2 + 1), int16(v_int64_2 + 1), int32(v_int64_2 + 1), int64(v_int64_2 + 1), uint8(v_int64_2 + 1), uint16(v_int64_2 + 1), uint32(v_int64_2 + 1), uint64(v_int64_2 + 1))
	println(int64(int8(int32(v_int64_2))), int64(uint8(uint32(v_int64_2))), int64(int16(uint8(v_int64_2))))
	println(int64(Small(v_int64_2)), int64(Wide(v_int64_2)), uint64(UWide(v_int64_2)), int64(Mid(v_int64_2)))
	println(int64(int8(Wide(v_int64_2))), uint64(uint32(UWide(v_int64_2))), int64(Mid(Small(v_int64_2))))
	println(int8(id64(int64(v_int64_2))), int16((int32(v_int64_2))), uint8(int64(v_int64_2)+0))
}

func c_uint8() {
	println(int8(v_uint8_0), int16(v_uint8_0), int32(v_uint8_0), int64(v_uint8_0), uint8(v_uint8_0), uint16(v_uint8_0), uint32(v_uint8_0), uint64(v_uint8_0))
	println(int8(v_uint8_0 + 1), int16(v_uint8_0 + 1), int32(v_uint8_0 + 1), int64(v_uint8_0 + 1), uint8(v_uint8_0 + 1), uint16(v_uint8_0 + 1), uint32(v_uint8_0 + 1), uint64(v_uint8_0 + 1))
	println(int64(int8(int32(v_uint8_0))), int64(uint8(uint32(v_uint8_0))), int64(int16(uint8(v_uint8_0))))
	println(int64(Small(v_uint8_0)), int64(Wide(v_uint8_0)), uint64(UWide(v_uint8_0)), int64(Mid(v_uint8_0)))
	println(int64(int8(Wide(v_uint8_0))), uint64(uint32(UWide(v_uint8_0))), int64(Mid(Small(v_uint8_0))))
	println(int8(id64(int64(v_uint8_0))), int16((int32(v_uint8_0))), uint8(int64(v_uint8_0)+0))
	println(int8(v_uint8_1), int16(v_uint8_1), int32(v_uint8_1), int64(v_uint8_1), uint8(v_uint8_1), uint16(v_uint8_1), uint32(v_uint8_1), uint64(v_uint8_1))
	println(int8(v_uint8_1 + 1), int16(v_uint8_1 + 1), int32(v_uint8_1 + 1), int64(v_uint8_1 + 1), uint8(v_uint8_1 + 1), uint16(v_uint8_1 + 1), uint32(v_uint8_1 + 1), uint64(v_uint8_1 + 1))
	println(int64(int8(int32(v_uint8_1))), int64(uint8(uint32(v_uint8_1))), int64(int16(uint8(v_uint8_1))))
	println(int64(Small(v_uint8_1)), int64(Wide(v_uint8_1)), uint64(UWide(v_uint8_1)), int64(Mid(v_uint8_1)))
	println(int64(int8(Wide(v_uint8_1))), uint64(uint32(UWide(v_uint8_1))), int64(Mid(Small(v_uint8_1))))
	println(int8(id64(int64(v_uint8_1))), int16((int32(v_uint8_1))), uint8(int64(v_uint8_1)+0))
	println(int8(v_uint8_2), int16(v_uint8_2), int32(v_uint8_2), int64(v_uint8_2), uint8(v_uint8_2), uint16(v_uint8_2), uint32(v_uint8_2), uint64(v_uint8_2))
	println(int8(v_uint8_2 + 1), int16(v_uint8_2 + 1), int32(v_uint8_2 + 1), int64(v_uint8_2 + 1), uint8(v_uint8_2 + 1), uint16(v_uint8_2 + 1), uint32(v_uint8_2 + 1), uint64(v_uint8_2 + 1))
	println(int64(int8(int32(v_uint8_2))), int64(uint8(uint32(v_uint8_2))), int64(int16(uint8(v_uint8_2))))
	println(int64(Small(v_uint8_2)), int64(Wide(v_uint8_2)), uint64(UWide(v_uint8_2)), int64(Mid(v_uint8_2)))
	println(int64(int8(Wide(v_uint8_2))), uint64(uint32(UWide(v_uint8_2))), int64(Mid(Small(v_uint8_2))))
	println(int8(id64(int64(v_uint8_2))), int16((int32(v_uint8_2))), uint8(int64(v_uint8_2)+0))
}

func c_uint16() {
	println(int8(v_uint16_0), int16(v_uint16_0), int32(v_uint16_0), int64(v_uint16_0), uint8(v_uint16_0), uint16(v_uint16_0), uint32(v_uint16_0), uint64(v_uint16_0))
	println(int8(v_uint16_0 + 1), int16(v_uint16_0 + 1), int32(v_uint16_0 + 1), int64(v_uint16_0 + 1), uint8(v_uint16_0 + 1), uint16(v_uint16_0 + 1), uint32(v_uint16_0 + 1), uint64(v_uint16_0 + 1))
	println(int64(int8(int32(v_uint16_0))), int64(uint8(uint32(v_uint16_0))), int64(int16(uint8(v_uint16_0))))
	println(int64(Small(v_uint16_0)), int64(Wide(v_uint16_0)), uint64(UWide(v_uint16_0)), int64(Mid(v_uint16_0)))
	println(int64(int8(Wide(v_uint16_0))), uint64(uint32(UWide(v_uint16_0))), int64(Mid(Small(v_uint16_0))))
	println(int8(id64(int64(v_uint16_0))), int16((int32(v_uint16_0))), uint8(int64(v_uint16_0)+0))
	println(int8(v_uint16_1), int16(v_uint16_1), int32(v_uint16_1), int64(v_uint16_1), uint8(v_uint16_1), uint16(v_uint16_1), uint32(v_uint16_1), uint64(v_uint16_1))
	println(int8(v_uint16_1 + 1), int16(v_uint16_1 + 1), int32(v_uint16_1 + 1), int64(v_uint16_1 + 1), uint8(v_uint16_1 + 1), uint16(v_uint16_1 + 1), uint32(v_uint16_1 + 1), uint64(v_uint16_1 + 1))
	println(int64(int8(int32(v_uint16_1))), int64(uint8(uint32(v_uint16_1))), int64(int16(uint8(v_uint16_1))))
	println(int64(Small(v_uint16_1)), int64(Wide(v_uint16_1)), uint64(UWide(v_uint16_1)), int64(Mid(v_uint16_1)))
	println(int64(int8(Wide(v_uint16_1))), uint64(uint32(UWide(v_uint16_1))), int64(Mid(Small(v_uint16_1))))
	println(int8(id64(int64(v_uint16_1))), int16((int32(v_uint16_1))), uint8(int64(v_uint16_1)+0))
	println(int8(v_uint16_2), int16(v_uint16_2), int32(v_uint16_2), int64(v_uint16_2), uint8(v_uint16_2), uint16(v_uint16_2), uint32(v_uint16_2), uint64(v_uint16_2))
	println(int8(v_uint16_2 + 1), int16(v_uint16_2 + 1), int32(v_uint16_2 + 1), int64(v_uint16_2 + 1), uint8(v_uint16_2 + 1), uint16(v_uint16_2 + 1), uint32(v_uint16_2 + 1), uint64(v_uint16_2 + 1))
	println(int64(int8(int32(v_uint16_2))), int64(uint8(uint32(v_uint16_2))), int64(int16(uint8(v_uint16_2))))
	println(int64(Small(v_uint16_2)), int64(Wide(v_uint16_2)), uint64(UWide(v_uint16_2)), int64(Mid(v_uint16_2)))
	println(int64(int8(Wide(v_uint16_2))), uint64(uint32(UWide(v_uint16_2))), int64(Mid(Small(v_uint16_2))))
	println(int8(id64(int64(v_uint16_2))), int16((int32(v_uint16_2))), uint8(int64(v_uint16_2)+0))
}

func c_uint32() {
	println(int8(v_uint32_0), int16(v_uint32_0), int32(v_uint32_0), int64(v_uint32_0), uint8(v_uint32_0), uint16(v_uint32_0), uint32(v_uint32_0), uint64(v_uint32_0))
	println(int8(v_uint32_0 + 1), int16(v_uint32_0 + 1), int32(v_uint32_0 + 1), int64(v_uint32_0 + 1), uint8(v_uint32_0 + 1), uint16(v_uint32_0 + 1), uint32(v_uint32_0 + 1), uint64(v_uint32_0 + 1))
	println(int64(int8(int32(v_uint32_0))), int64(uint8(uint32(v_uint32_0))), int64(int16(uint8(v_uint32_0))))
	println(int64(Small(v_uint32_0)), int64(Wide(v_uint32_0)), uint64(UWide(v_uint32_0)), int64(Mid(v_uint32_0)))
	println(int64(int8(Wide(v_uint32_0))), uint64(uint32(UWide(v_uint32_0))), int64(Mid(Small(v_uint32_0))))
	println(int8(id64(int64(v_uint32_0))), int16((int32(v_uint32_0))), uint8(int64(v_uint32_0)+0))
	println(int8(v_uint32_1), int16(v_uint32_1), int32(v_uint32_1), int64(v_uint32_1), uint8(v_uint32_1), uint16(v_uint32_1), uint32(v_uint32_1), uint64(v_uint32_1))
	println(int8(v_uint32_1 + 1), int16(v_uint32_1 + 1), int32(v_uint32_1 + 1), int64(v_uint32_1 + 1), uint8(v_uint32_1 + 1), uint16(v_uint32_1 + 1), uint32(v_uint32_1 + 1), uint64(v_uint32_1 + 1))
	println(int64(int8(int32(v_uint32_1))), int64(uint8(uint32(v_uint32_1))), int64(int16(uint8(v_uint32_1))))
	println(int64(Small(v_uint32_1)), int64(Wide(v_uint32_1)), uint64(UWide(v_uint32_1)), int64(Mid(v_uint32_1)))
	println(int64(int8(Wide(v_uint32_1))), uint64(uint32(UWide(v_uint32_1))), int64(Mid(Small(v_uint32_1))))
	println(int8(id64(int64(v_uint32_1))), int16((int32(v_uint32_1))), uint8(int64(v_uint32_1)+0))
	println(int8(v_uint32_2), int16(v_uint32_2), int32(v_uint32_2), int64(v_uint32_2), uint8(v_uint32_2), uint16(v_uint32_2), uint32(v_uint32_2), uint64(v_uint32_2))
	println(int8(v_uint32_2 + 1), int16(v_uint32_2 + 1), int32(v_uint32_2 + 1), int64(v_uint32_2 + 1), uint8(v_uint32_2 + 1), uint16(v_uint32_2 + 1), uint32(v_uint32_2 + 1), uint64(v_uint32_2 + 1))
	println(int64(int8(int32(v_uint32_2))), int64(uint8(uint32(v_uint32_2))), int64(int16(uint8(v_uint32_2))))
	println(int64(Small(v_uint32_2)), int64(Wide(v_uint32_2)), uint64(UWide(v_uint32_2)), int64(Mid(v_uint32_2)))
	println(int64(int8(Wide(v_uint32_2))), uint64(uint32(UWide(v_uint32_2))), int64(Mid(Small(v_uint32_2))))
	println(int8(id64(int64(v_uint32_2))), int16((int32(v_uint32_2))), uint8(int64(v_uint32_2)+0))
}

func c_uint64() {
	println(int8(v_uint64_0), int16(v_uint64_0), int32(v_uint64_0), int64(v_uint64_0), uint8(v_uint64_0), uint16(v_uint64_0), uint32(v_uint64_0), uint64(v_uint64_0))
	println(int8(v_uint64_0 + 1), int16(v_uint64_0 + 1), int32(v_uint64_0 + 1), int64(v_uint64_0 + 1), uint8(v_uint64_0 + 1), uint16(v_uint64_0 + 1), uint32(v_uint64_0 + 1), uint64(v_uint64_0 + 1))
	println(int64(int8(int32(v_uint64_0))), int64(uint8(uint32(v_uint64_0))), int64(int16(uint8(v_uint64_0))))
	println(int64(Small(v_uint64_0)), int64(Wide(v_uint64_0)), uint64(UWide(v_uint64_0)), int64(Mid(v_uint64_0)))
	println(int64(int8(Wide(v_uint64_0))), uint64(uint32(UWide(v_uint64_0))), int64(Mid(Small(v_uint64_0))))
	println(int8(id64(int64(v_uint64_0))), int16((int32(v_uint64_0))), uint8(int64(v_uint64_0)+0))
	println(int8(v_uint64_1), int16(v_uint64_1), int32(v_uint64_1), int64(v_uint64_1), uint8(v_uint64_1), uint16(v_uint64_1), uint32(v_uint64_1), uint64(v_uint64_1))
	println(int8(v_uint64_1 + 1), int16(v_uint64_1 + 1), int32(v_uint64_1 + 1), int64(v_uint64_1 + 1), uint8(v_uint64_1 + 1), uint16(v_uint64_1 + 1), uint32(v_uint64_1 + 1), uint64(v_uint64_1 + 1))
	println(int64(int8(int32(v_uint64_1))), int64(uint8(uint32(v_uint64_1))), int64(int16(uint8(v_uint64_1))))
	println(int64(Small(v_uint64_1)), int64(Wide(v_uint64_1)), uint64(UWide(v_uint64_1)), int64(Mid(v_uint64_1)))
	println(int64(int8(Wide(v_uint64_1))), uint64(uint32(UWide(v_uint64_1))), int64(Mid(Small(v_uint64_1))))
	println(int8(id64(int64(v_uint64_1))), int16((int32(v_uint64_1))), uint8(int64(v_uint64_1)+0))
	println(int8(v_uint64_2), int16(v_uint64_2), int32(v_uint64_2), int64(v_uint64_2), uint8(v_uint64_2), uint16(v_uint64_2), uint32(v_uint64_2), uint64(v_uint64_2))
	println(int8(v_uint64_2 + 1), int16(v_uint64_2 + 1), int32(v_uint64_2 + 1), int64(v_uint64_2 + 1), uint8(v_uint64_2 + 1), uint16(v_uint64_2 + 1), uint32(v_uint64_2 + 1), uint64(v_uint64_2 + 1))
	println(int64(int8(int32(v_uint64_2))), int64(uint8(uint32(v_uint64_2))), int64(int16(uint8(v_uint64_2))))
	println(int64(Small(v_uint64_2)), int64(Wide(v_uint64_2)), uint64(UWide(v_uint64_2)), int64(Mid(v_uint64_2)))
	println(int64(int8(Wide(v_uint64_2))), uint64(uint32(UWide(v_uint64_2))), int64(Mid(Small(v_uint64_2))))
	println(int8(id64(int64(v_uint64_2))), int16((int32(v_uint64_2))), uint8(int64(v_uint64_2)+0))
}

func main() {
	c_int8()
	c_int16()
	c_int32()
	c_int64()
	c_uint8()
	c_uint16()
	c_uint32()
	c_uint64()
}
`,
		want: "-128 -128 -128 -128 128 65408 4294967168 18446744073709551488\n-127 -127 -127 -127 129 65409 4294967169 18446744073709551489\n-128 128 128\n-128 -128 18446744073709551488 65408\n-128 4294967168 65408\n-128 -128 128\n-1 -1 -1 -1 255 65535 4294967295 18446744073709551615\n0 0 0 0 0 0 0 0\n-1 255 255\n-1 -1 18446744073709551615 65535\n-1 4294967295 65535\n-1 -1 255\n127 127 127 127 127 127 127 127\n-128 -128 -128 -128 128 65408 4294967168 18446744073709551488\n127 127 127\n127 127 127 127\n127 127 127\n127 127 127\n0 -32768 -32768 -32768 0 32768 4294934528 18446744073709518848\n1 -32767 -32767 -32767 1 32769 4294934529 18446744073709518849\n0 0 0\n0 -32768 18446744073709518848 32768\n0 4294934528 0\n0 -32768 0\n-1 -1 -1 -1 255 65535 4294967295 18446744073709551615\n0 0 0 0 0 0 0 0\n-1 255 255\n-1 -1 18446744073709551615 65535\n-1 4294967295 65535\n-1 -1 255\n-1 32767 32767 32767 255 32767 32767 32767\n0 -32768 -32768 -32768 0 32768 4294934528 18446744073709518848\n-1 255 255\n-1 32767 32767 32767\n-1 32767 65535\n-1 32767 255\n0 0 -2147483648 -2147483648 0 0 2147483648 18446744071562067968\n1 1 -2147483647 -2147483647 1 1 2147483649 18446744071562067969\n0 0 0\n0 -2147483648 18446744071562067968 0\n0 2147483648 0\n0 0 0\n-1 -1 -1 -1 255 65535 4294967295 18446744073709551615\n0 0 0 0 0 0 0 0\n-1 255 255\n-1 -1 18446744073709551615 65535\n-1 4294967295 65535\n-1 -1 255\n-1 -1 2147483647 2147483647 255 65535 2147483647 2147483647\n0 0 -2147483648 -2147483648 0 0 2147483648 18446744071562067968\n-1 255 255\n-1 2147483647 2147483647 65535\n-1 2147483647 65535\n-1 -1 255\n0 0 0 -9223372036854775808 0 0 0 9223372036854775808\n1 1 1 -9223372036854775807 1 1 1 9223372036854775809\n0 0 0\n0 -9223372036854775808 9223372036854775808 0\n0 0 0\n0 0 0\n-1 -1 -1 -1 255 65535 4294967295 18446744073709551615\n0 0 0 0 0 0 0 0\n-1 255 255\n-1 -1 18446744073709551615 65535\n-1 4294967295 65535\n-1 -1 255\n-1 -1 -1 9223372036854775807 255 65535 4294967295 9223372036854775807\n0 0 0 -9223372036854775808 0 0 0 9223372036854775808\n-1 255 255\n-1 9223372036854775807 9223372036854775807 65535\n-1 4294967295 65535\n-1 -1 255\n0 0 0 0 0 0 0 0\n1 1 1 1 1 1 1 1\n0 0 0\n0 0 0 0\n0 0 0\n0 0 0\n-127 129 129 129 129 129 129 129\n-126 130 130 130 130 130 130 130\n-127 129 129\n-127 129 129 129\n-127 129 65409\n-127 129 129\n-1 255 255 255 255 255 255 255\n0 0 0 0 0 0 0 0\n-1 255 255\n-1 255 255 255\n-1 255 65535\n-1 255 255\n0 0 0 0 0 0 0 0\n1 1 1 1 1 1 1 1\n0 0 0\n0 0 0 0\n0 0 0\n0 0 0\n-24 -32536 33000 33000 232 33000 33000 33000\n-23 -32535 33001 33001 233 33001 33001 33001\n-24 232 232\n-24 33000 33000 33000\n-24 33000 65512\n-24 -32536 232\n-1 -1 65535 65535 255 65535 65535 65535\n0 0 0 0 0 0 0 0\n-1 255 255\n-1 65535 65535 65535\n-1 65535 65535\n-1 -1 255\n0 0 0 0 0 0 0 0\n1 1 1 1 1 1 1 1\n0 0 0\n0 0 0 0\n0 0 0\n0 0 0\n1 1 -2147483647 2147483649 1 1 2147483649 2147483649\n2 2 -2147483646 2147483650 2 2 2147483650 2147483650\n1 1 1\n1 2147483649 2147483649 1\n1 2147483649 1\n1 1 1\n-1 -1 -1 4294967295 255 65535 4294967295 4294967295\n0 0 0 0 0 0 0 0\n-1 255 255\n-1 4294967295 4294967295 65535\n-1 4294967295 65535\n-1 -1 255\n0 0 0 0 0 0 0 0\n1 1 1 1 1 1 1 1\n0 0 0\n0 0 0 0\n0 0 0\n0 0 0\n1 1 1 -9223372036854775807 1 1 1 9223372036854775809\n2 2 2 -9223372036854775806 2 2 2 9223372036854775810\n1 1 1\n1 -9223372036854775807 9223372036854775809 1\n1 1 1\n1 1 1\n-1 -1 -1 -1 255 65535 4294967295 18446744073709551615\n0 0 0 0 0 0 0 0\n-1 255 255\n-1 -1 18446744073709551615 65535\n-1 4294967295 65535\n-1 -1 255\n",
	},
	{
		// Mixed-type integer arithmetic, every sized type against every operator,
		// with the constant on each side in turn. Generated rather than written:
		// the space is 312 expressions and the interesting part is the CROSS of
		// type, operator and operand position, which is exactly what hand-written
		// cases keep missing.
		//
		// It exists because two silent defects lived in that cross. The backend
		// types `4 * u` signed, so a division, a shift and an ordering comparison
		// downstream all took the signed branch (the value is right, only the type
		// is wrong -- see the case below). And a guarded division read the type of
		// its LEFT operand, so `3 / b` for a uint64 b was typed int, chose the
		// 32-bit zero-guard, saw zero in a divisor whose low word is zero, and
		// panicked in a program that divides by 0x1000000000000000.
		//
		// Both were invisible on the host, whose C compiler is correct here; this
		// runs on the board, where they were not.
		name: "mixed-type integer arithmetic",
		src: `var a_uint8 uint8 = 0xF0
var b_uint8 uint8 = 0x0F
var a_uint16 uint16 = 0xF000
var b_uint16 uint16 = 0x0F00
var a_uint32 uint32 = 0x30000000
var b_uint32 uint32 = 0x10000000
var a_uint64 uint64 = 0x3000000000000000
var b_uint64 uint64 = 0x1000000000000000
var a_int8 int8 = 100
var b_int8 int8 = 7
var a_int16 int16 = 30000
var b_int16 int16 = 7
var a_int32 int32 = 2000000000
var b_int32 int32 = 7
var a_int64 int64 = 4000000000000000000
var b_int64 int64 = 7

func t_uint8() {
	println(uint64(a_uint8 + b_uint8), uint64(3 + b_uint8), uint64(a_uint8 + 3))
	println(uint64(a_uint8 - b_uint8), uint64(3 - b_uint8), uint64(a_uint8 - 3))
	println(uint64(a_uint8 * b_uint8), uint64(3 * b_uint8), uint64(a_uint8 * 3))
	println(uint64(a_uint8 / b_uint8), uint64(3 / b_uint8), uint64(a_uint8 / 3))
	println(uint64(a_uint8 % b_uint8), uint64(3 % b_uint8), uint64(a_uint8 % 3))
	println(uint64(a_uint8 & b_uint8), uint64(3 & b_uint8), uint64(a_uint8 & 3))
	println(uint64(a_uint8 | b_uint8), uint64(3 | b_uint8), uint64(a_uint8 | 3))
	println(uint64(a_uint8 ^ b_uint8), uint64(3 ^ b_uint8), uint64(a_uint8 ^ 3))
	println(uint64(a_uint8 &^ b_uint8), uint64(3 &^ b_uint8), uint64(a_uint8 &^ 3))
	println(uint64(a_uint8 << 2), uint64(a_uint8 << uint(1)))
	println(uint64(a_uint8 >> 2), uint64(a_uint8 >> uint(1)))
	println(a_uint8 >= 4*b_uint8, 4*b_uint8 <= a_uint8, a_uint8 >= b_uint8*4)
	println(uint64(3 / b_uint8), uint64(3 % b_uint8), uint64(2 * b_uint8 / 3))
}

func t_uint16() {
	println(uint64(a_uint16 + b_uint16), uint64(3 + b_uint16), uint64(a_uint16 + 3))
	println(uint64(a_uint16 - b_uint16), uint64(3 - b_uint16), uint64(a_uint16 - 3))
	println(uint64(a_uint16 * b_uint16), uint64(3 * b_uint16), uint64(a_uint16 * 3))
	println(uint64(a_uint16 / b_uint16), uint64(3 / b_uint16), uint64(a_uint16 / 3))
	println(uint64(a_uint16 % b_uint16), uint64(3 % b_uint16), uint64(a_uint16 % 3))
	println(uint64(a_uint16 & b_uint16), uint64(3 & b_uint16), uint64(a_uint16 & 3))
	println(uint64(a_uint16 | b_uint16), uint64(3 | b_uint16), uint64(a_uint16 | 3))
	println(uint64(a_uint16 ^ b_uint16), uint64(3 ^ b_uint16), uint64(a_uint16 ^ 3))
	println(uint64(a_uint16 &^ b_uint16), uint64(3 &^ b_uint16), uint64(a_uint16 &^ 3))
	println(uint64(a_uint16 << 2), uint64(a_uint16 << uint(1)))
	println(uint64(a_uint16 >> 2), uint64(a_uint16 >> uint(1)))
	println(a_uint16 >= 4*b_uint16, 4*b_uint16 <= a_uint16, a_uint16 >= b_uint16*4)
	println(uint64(3 / b_uint16), uint64(3 % b_uint16), uint64(2 * b_uint16 / 3))
}

func t_uint32() {
	println(uint64(a_uint32 + b_uint32), uint64(3 + b_uint32), uint64(a_uint32 + 3))
	println(uint64(a_uint32 - b_uint32), uint64(3 - b_uint32), uint64(a_uint32 - 3))
	println(uint64(a_uint32 * b_uint32), uint64(3 * b_uint32), uint64(a_uint32 * 3))
	println(uint64(a_uint32 / b_uint32), uint64(3 / b_uint32), uint64(a_uint32 / 3))
	println(uint64(a_uint32 % b_uint32), uint64(3 % b_uint32), uint64(a_uint32 % 3))
	println(uint64(a_uint32 & b_uint32), uint64(3 & b_uint32), uint64(a_uint32 & 3))
	println(uint64(a_uint32 | b_uint32), uint64(3 | b_uint32), uint64(a_uint32 | 3))
	println(uint64(a_uint32 ^ b_uint32), uint64(3 ^ b_uint32), uint64(a_uint32 ^ 3))
	println(uint64(a_uint32 &^ b_uint32), uint64(3 &^ b_uint32), uint64(a_uint32 &^ 3))
	println(uint64(a_uint32 << 2), uint64(a_uint32 << uint(1)))
	println(uint64(a_uint32 >> 2), uint64(a_uint32 >> uint(1)))
	println(a_uint32 >= 4*b_uint32, 4*b_uint32 <= a_uint32, a_uint32 >= b_uint32*4)
	println(uint64(3 / b_uint32), uint64(3 % b_uint32), uint64(2 * b_uint32 / 3))
}

func t_uint64() {
	println(uint64(a_uint64 + b_uint64), uint64(3 + b_uint64), uint64(a_uint64 + 3))
	println(uint64(a_uint64 - b_uint64), uint64(3 - b_uint64), uint64(a_uint64 - 3))
	println(uint64(a_uint64 * b_uint64), uint64(3 * b_uint64), uint64(a_uint64 * 3))
	println(uint64(a_uint64 / b_uint64), uint64(3 / b_uint64), uint64(a_uint64 / 3))
	println(uint64(a_uint64 % b_uint64), uint64(3 % b_uint64), uint64(a_uint64 % 3))
	println(uint64(a_uint64 & b_uint64), uint64(3 & b_uint64), uint64(a_uint64 & 3))
	println(uint64(a_uint64 | b_uint64), uint64(3 | b_uint64), uint64(a_uint64 | 3))
	println(uint64(a_uint64 ^ b_uint64), uint64(3 ^ b_uint64), uint64(a_uint64 ^ 3))
	println(uint64(a_uint64 &^ b_uint64), uint64(3 &^ b_uint64), uint64(a_uint64 &^ 3))
	println(uint64(a_uint64 << 2), uint64(a_uint64 << uint(1)))
	println(uint64(a_uint64 >> 2), uint64(a_uint64 >> uint(1)))
	println(a_uint64 >= 4*b_uint64, 4*b_uint64 <= a_uint64, a_uint64 >= b_uint64*4)
	println(uint64(3 / b_uint64), uint64(3 % b_uint64), uint64(2 * b_uint64 / 3))
}

func t_int8() {
	println(uint64(a_int8 + b_int8), uint64(3 + b_int8), uint64(a_int8 + 3))
	println(uint64(a_int8 - b_int8), uint64(3 - b_int8), uint64(a_int8 - 3))
	println(uint64(a_int8 * b_int8), uint64(3 * b_int8), uint64(a_int8 * 3))
	println(uint64(a_int8 / b_int8), uint64(3 / b_int8), uint64(a_int8 / 3))
	println(uint64(a_int8 % b_int8), uint64(3 % b_int8), uint64(a_int8 % 3))
	println(uint64(a_int8 & b_int8), uint64(3 & b_int8), uint64(a_int8 & 3))
	println(uint64(a_int8 | b_int8), uint64(3 | b_int8), uint64(a_int8 | 3))
	println(uint64(a_int8 ^ b_int8), uint64(3 ^ b_int8), uint64(a_int8 ^ 3))
	println(uint64(a_int8 &^ b_int8), uint64(3 &^ b_int8), uint64(a_int8 &^ 3))
	println(uint64(a_int8 << 2), uint64(a_int8 << uint(1)))
	println(uint64(a_int8 >> 2), uint64(a_int8 >> uint(1)))
	println(a_int8 >= 4*b_int8, 4*b_int8 <= a_int8, a_int8 >= b_int8*4)
	println(uint64(3 / b_int8), uint64(3 % b_int8), uint64(2 * b_int8 / 3))
}

func t_int16() {
	println(uint64(a_int16 + b_int16), uint64(3 + b_int16), uint64(a_int16 + 3))
	println(uint64(a_int16 - b_int16), uint64(3 - b_int16), uint64(a_int16 - 3))
	println(uint64(a_int16 * b_int16), uint64(3 * b_int16), uint64(a_int16 * 3))
	println(uint64(a_int16 / b_int16), uint64(3 / b_int16), uint64(a_int16 / 3))
	println(uint64(a_int16 % b_int16), uint64(3 % b_int16), uint64(a_int16 % 3))
	println(uint64(a_int16 & b_int16), uint64(3 & b_int16), uint64(a_int16 & 3))
	println(uint64(a_int16 | b_int16), uint64(3 | b_int16), uint64(a_int16 | 3))
	println(uint64(a_int16 ^ b_int16), uint64(3 ^ b_int16), uint64(a_int16 ^ 3))
	println(uint64(a_int16 &^ b_int16), uint64(3 &^ b_int16), uint64(a_int16 &^ 3))
	println(uint64(a_int16 << 2), uint64(a_int16 << uint(1)))
	println(uint64(a_int16 >> 2), uint64(a_int16 >> uint(1)))
	println(a_int16 >= 4*b_int16, 4*b_int16 <= a_int16, a_int16 >= b_int16*4)
	println(uint64(3 / b_int16), uint64(3 % b_int16), uint64(2 * b_int16 / 3))
}

func t_int32() {
	println(uint64(a_int32 + b_int32), uint64(3 + b_int32), uint64(a_int32 + 3))
	println(uint64(a_int32 - b_int32), uint64(3 - b_int32), uint64(a_int32 - 3))
	println(uint64(a_int32 * b_int32), uint64(3 * b_int32), uint64(a_int32 * 3))
	println(uint64(a_int32 / b_int32), uint64(3 / b_int32), uint64(a_int32 / 3))
	println(uint64(a_int32 % b_int32), uint64(3 % b_int32), uint64(a_int32 % 3))
	println(uint64(a_int32 & b_int32), uint64(3 & b_int32), uint64(a_int32 & 3))
	println(uint64(a_int32 | b_int32), uint64(3 | b_int32), uint64(a_int32 | 3))
	println(uint64(a_int32 ^ b_int32), uint64(3 ^ b_int32), uint64(a_int32 ^ 3))
	println(uint64(a_int32 &^ b_int32), uint64(3 &^ b_int32), uint64(a_int32 &^ 3))
	println(uint64(a_int32 << 2), uint64(a_int32 << uint(1)))
	println(uint64(a_int32 >> 2), uint64(a_int32 >> uint(1)))
	println(a_int32 >= 4*b_int32, 4*b_int32 <= a_int32, a_int32 >= b_int32*4)
	println(uint64(3 / b_int32), uint64(3 % b_int32), uint64(2 * b_int32 / 3))
}

func t_int64() {
	println(uint64(a_int64 + b_int64), uint64(3 + b_int64), uint64(a_int64 + 3))
	println(uint64(a_int64 - b_int64), uint64(3 - b_int64), uint64(a_int64 - 3))
	println(uint64(a_int64 * b_int64), uint64(3 * b_int64), uint64(a_int64 * 3))
	println(uint64(a_int64 / b_int64), uint64(3 / b_int64), uint64(a_int64 / 3))
	println(uint64(a_int64 % b_int64), uint64(3 % b_int64), uint64(a_int64 % 3))
	println(uint64(a_int64 & b_int64), uint64(3 & b_int64), uint64(a_int64 & 3))
	println(uint64(a_int64 | b_int64), uint64(3 | b_int64), uint64(a_int64 | 3))
	println(uint64(a_int64 ^ b_int64), uint64(3 ^ b_int64), uint64(a_int64 ^ 3))
	println(uint64(a_int64 &^ b_int64), uint64(3 &^ b_int64), uint64(a_int64 &^ 3))
	println(uint64(a_int64 << 2), uint64(a_int64 << uint(1)))
	println(uint64(a_int64 >> 2), uint64(a_int64 >> uint(1)))
	println(a_int64 >= 4*b_int64, 4*b_int64 <= a_int64, a_int64 >= b_int64*4)
	println(uint64(3 / b_int64), uint64(3 % b_int64), uint64(2 * b_int64 / 3))
}

func main() {
	t_uint8()
	t_uint16()
	t_uint32()
	t_uint64()
	t_int8()
	t_int16()
	t_int32()
	t_int64()
}
`,
		want: "255 18 243\n225 244 237\n16 45 208\n16 0 80\n0 3 0\n0 3 0\n255 15 243\n255 12 243\n240 0 240\n192 224\n60 120\ntrue true true\n0 3 10\n65280 3843 61443\n57600 61699 61437\n0 11520 53248\n16 0 20480\n0 3 0\n0 0 0\n65280 3843 61443\n65280 3843 61443\n61440 3 61440\n49152 57344\n15360 30720\ntrue true true\n0 3 2560\n1073741824 268435459 805306371\n536870912 4026531843 805306365\n0 805306368 2415919104\n3 0 268435456\n0 3 0\n268435456 0 0\n805306368 268435459 805306371\n536870912 268435459 805306371\n536870912 3 805306368\n3221225472 1610612736\n201326592 402653184\nfalse false false\n0 3 178956970\n4611686018427387904 1152921504606846979 3458764513820540931\n2305843009213693952 17293822569102704643 3458764513820540925\n0 3458764513820540928 10376293541461622784\n3 0 1152921504606846976\n0 3 0\n1152921504606846976 0 0\n3458764513820540928 1152921504606846979 3458764513820540931\n2305843009213693952 1152921504606846979 3458764513820540931\n2305843009213693952 3 3458764513820540928\n13835058055282163712 6917529027641081856\n864691128455135232 1729382256910270464\nfalse false false\n0 3 768614336404564650\n107 10 103\n93 18446744073709551612 97\n18446744073709551548 21 44\n14 0 33\n2 3 1\n4 3 0\n103 7 103\n99 4 103\n96 0 100\n18446744073709551504 18446744073709551560\n25 50\ntrue true true\n0 3 4\n30007 10 30003\n29993 18446744073709551612 29997\n13392 21 24464\n4285 0 10000\n5 3 0\n0 3 0\n30007 7 30003\n30007 4 30003\n30000 0 30000\n18446744073709540544 18446744073709546080\n7500 15000\ntrue true true\n0 3 4\n2000000007 10 2000000003\n1999999993 18446744073709551612 1999999997\n1115098112 21 1705032704\n285714285 0 666666666\n5 3 2\n0 3 0\n2000000007 7 2000000003\n2000000007 4 2000000003\n2000000000 0 2000000000\n18446744073119617024 18446744073414584320\n500000000 1000000000\ntrue true true\n0 3 4\n4000000000000000007 10 4000000000000000003\n3999999999999999993 18446744073709551612 3999999999999999997\n9553255926290448384 21 12000000000000000000\n571428571428571428 0 1333333333333333333\n4 3 1\n0 3 0\n4000000000000000007 7 4000000000000000003\n4000000000000000007 4 4000000000000000003\n4000000000000000000 0 4000000000000000000\n16000000000000000000 8000000000000000000\n1000000000000000000 2000000000000000000\ntrue true true\n0 3 4\n",
	},
	{
		// A backend defect, measured on a P2-EDGE: flexcc types `4 * u` -- a signed
		// constant on the LEFT of an unsigned operand -- as SIGNED. The product's
		// VALUE is right, so nothing looks wrong until a signedness-sensitive
		// operation reads it, and then a division, a right shift and an ordering
		// comparison each take the signed branch and answer wrongly.
		//
		// The same expression written the other way round, `u * 4`, was right all
		// along. That is why it went unnoticed, and it is the reason to probe an
		// operand order both ways whenever a type can be lost.
		//
		// The emitter now spells a constant operand of an unsigned level unsigned,
		// `4u * u`, which settles the non-commutative shapes too -- a subtraction
		// cannot be fixed by reordering.
		name: "unsigned arithmetic with a constant on the left",
		src: `func main() {
	var u uint32 = 0x30000000
	var v uint32 = 0x10000000

	// Each of these read the signed branch of an operation the constant had
	// wrongly typed.
	println(4 * u / 3)
	println(4 * u >> 1)
	println(v >= (4 * u))

	// The operands the other way round, which was always right.
	println(u * 4 / 3)
	println(u * 4 >> 1)

	// A subtraction, which reordering could not have fixed.
	d := 100 - u
	println(d >> 1)

	// Signed arithmetic means what it always did.
	var i int = 7
	println(4*i/3, -2*i, 100-i)
}
`,
		want: "1073741824\n1610612736\nfalse\n1073741824\n1610612736\n1744830514\n9 -14 93\n",
	},
	{
		// WaitUntil waits until the system counter REACHES a value, where
		// WaitCycles waits a duration. That is the difference between a loop that
		// keeps time and one that drifts, and the p2 package had only the second --
		// so the drift-free control loop, which is what a periodic sampler is, could
		// not be written at all. flexcc had the intrinsic (_waitcnt) all along.
		//
		// The third line is the point: four periods of work still take four
		// periods, because the body's time is absorbed by the wait rather than
		// added to it. With WaitCycles in its place the total would exceed the
		// upper bound, which is what makes that bound worth asserting.
		//
		// Boolean rather than measured output: the host shim's counter and the
		// board's run at different rates, and only the relations are the same.
		name: "WaitUntil keeps a period",
		src: `import "p2"

func main() {
	// The deadline is reached, not merely approached.
	t0 := p2.GetCt()
	p2.WaitUntil(t0 + 1600000)
	waited := p2.GetCt() - t0
	println(waited >= 1600000)

	// A deadline already PAST returns at once rather than waiting a whole
	// counter wrap, which is 27 seconds at 160 MHz.
	t1 := p2.GetCt()
	p2.WaitUntil(t1 - 1000000)
	past := p2.GetCt() - t1
	println(past < 1000000)

	// Four periods of a body that takes real time still take four periods.
	period := uint32(1600000)
	start := p2.GetCt()
	next := start + period
	for i := 0; i < 4; i++ {
		p2.WaitUntil(next)
		next += period
		p2.WaitCycles(400000)
	}
	total := p2.GetCt() - start
	println(total >= 4*period, total < 4*period+800000)

	// GetUs completes the GetMs/GetSec family.
	u0 := p2.GetUs()
	p2.WaitCycles(1600000)
	elapsed := p2.GetUs() - u0
	println(elapsed > 0)
}
`,
		want: "true\ntrue\ntrue true\ntrue\n",
	},
	{
		// The accepting side of the block-lifetime rule, which exists because Go's
		// loop variable is per ITERATION (since 1.22) and a body-scoped local has
		// been per iteration since 1.0. Keeping a reference to either past the
		// iteration needs a cell per iteration, of a count known only at run time --
		// a heap -- so it is refused. Everything here keeps nothing past the block
		// the storage belongs to, and means exactly what Go means.
		name: "references that do not outlive their block",
		src: `type Box struct {
	p *int
	d []int
}

func put(b *Box, p *int) { b.p = p }

func (b *Box) set(p *int) { b.p = p }

func peek(p *int) int { return *p }

var pkg = [4]int{1, 2, 3, 4}

func run(n int) int {
	// A parameter's address into a body local: the parameter outlives the block.
	var pp *int
	pp = &n
	*pp += 10

	// A pointer, a slice and a struct field, all within one block.
	x := 5
	var p *int
	p = &x
	var buf [4]int
	buf[0] = 3
	var s []int
	s = buf[:]
	var b Box
	b.d = buf[:]

	sum := 0
	for i := 0; i < 3; i++ {
		// An OUTER variable's address stored into an INNER target: the reference
		// dies first, so it never outlives what it points at.
		var q *int
		q = &x

		// A keeper and a method whose target lives in this same block, and a
		// callee that only reads: none of them keeps the address past the
		// iteration.
		var local Box
		put(&local, &i)
		local.set(&i)
		sum += *q + *local.p + peek(&i)
	}
	return n + *p + s[0] + b.d[0] + pkg[0] + sum
}

func main() {
	println(run(5))
}
`,
		want: "48\n",
	},
	{
		// The line an interface method call draws for the lifetime rules. Which
		// function it reaches is the TABLE's answer at run time, so there is no
		// callee to look an escape summary up by -- and nothing was asked, which
		// made an interface the way around every rule a direct call obeys. The
		// summaries of all the implementations are unioned instead.
		//
		// Both halves run here: a frame-backed slice may cross an interface whose
		// implementations keep nothing, and storage that outlives the call may
		// cross one that keeps.
		name: "a frame slice crosses an interface that keeps nothing",
		src: `type Reader interface {
	sum(d []int) int
	size() int
}

type Keeper interface{ keep(d []int) }

type Acc struct{ n int }

func (a *Acc) sum(d []int) int {
	t := 0
	for _, v := range d {
		t += v
	}
	a.n = t
	return t
}

func (a *Acc) size() int { return a.n }

type Store struct{ d []int }

func (s *Store) keep(d []int) { s.d = d }

var acc Acc

var st Store

var back = [3]int{7, 8, 9}

func main() {
	var local [4]int
	local[0], local[1] = 3, 4

	var r Reader = &acc
	println(r.sum(local[:]), r.size())

	var k Keeper = &st
	k.keep(back[:])
	println(st.d[0], len(st.d))
}
`,
		want: "7 7\n7 3\n",
	},
	{
		// A package array of strings whose elements are constant but not WRITTEN as
		// bare literals. Each was emitted as a compound literal, `(ogo_string){...}`,
		// which the target's compiler rejects in a file-scope initializer ("Bad
		// constant expression") though the host's accepts it -- so the program did
		// not build at all, while the same array of plain literals did. The element
		// takes braces when it FOLDS to a constant string, which is what C cares
		// about; asking how it was spelled missed the concatenation.
		name: "a constant concatenation as an array element",
		src: `const pre = "x"

var parts = [3]string{pre + "y", "a" + "b", "plain"}

var one = [1]string{pre}

func main() {
	println(parts[0], parts[1], parts[2], one[0])
	println(len(parts[0]), len(parts[1]))
}
`,
		want: "xy ab plain x\n2 2\n",
	},
	{
		// A CONSTANT rune converted to a string, which Go makes a constant string
		// and this refused outright as "a string conversion needs allocation" --
		// true of the run-time conversion and of nothing here, which allocates and
		// copies nothing. `println(string('A'))` was an error.
		//
		// The encoding is Go's: one to four UTF-8 bytes, and "\uFFFD" for a value
		// that is no code point -- a surrogate half, a negative, or one past
		// U+10FFFF, the last of which Go converts rather than refusing (writing
		// rune(1 << 40) is what fails, at that conversion). string(rune(0)) is a
		// string of LENGTH ONE holding a NUL, which is why the emitted literal
		// carries its length beside its bytes rather than relying on the
		// terminator.
		name: "a constant rune converts to a string",
		src: `var arr = [2]string{string('a'), string('b')}

type Box struct{ s string }

var b = Box{string('q')}

var greet = "hi" + string('!')

func take(s string) int { return len(s) }

func main() {
	println(string('A'), string(rune(66)), string(67))
	println(string(0xE9), string(0x4E16), string(rune(0x1F600)))
	println(len(string(rune(0))), len(string('A')), len(string(0xE9)), len(string(0x4E16)))
	println(len(string(rune(0x1F600))))

	// No code point: each of the three ways, all "\uFFFD".
	println(string(rune(0xD800)) == "\uFFFD", string(-1) == "\uFFFD", string(1<<40) == "\uFFFD")

	// Every position a string may stand in.
	s := string('A')
	println(s, len(s), string('z') > string('a'))
	println(arr[0], arr[1], b.s, greet)
	println(take(string('k')))
	var t string = string('T')
	t = string('U')
	println(t, "a"+string('b')+"c", string('x')+string('y'))
	sw := string('c')
	switch sw {
	case "c":
		println("matched")
	}
}
`,
		want: "A B C\n\u00e9 \u4e16 \U0001f600\n1 1 2 3\n4\ntrue true true\nA 1 true\na b q hi!\n1\nU abc xy\nmatched\n",
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
		// Package constants used ABOVE their declarations, which Go's package block
		// allows in any order: a chain three deep sizing an array, a signature's
		// array bounds, a struct field's bound, a float32 constant and a string
		// concatenation.
		// The emitter took the constants in source order, each folding only what
		// the ones before it had recorded, so every one of these stopped at
		// `unsupported type ""` or reached the target's C compiler as an unknown
		// symbol or a syntax error.
		name: "package constants used above their declarations",
		src: `func cells(a [Rows]int) int { return len(a) }

func offset(n int, a [Top]int) int { return len(a) + n }

type grid struct {
	cells [Wide]uint8
}

var table [Deep]int

const Deep = Mid + 1

const Mid = Low * 2

const Low = 3

const Rows = Low + Mid

const Top = Base + 1

const Base = 1

const Wide = Low << 2

const gain = step * 3

const step float32 = 0.1

const model = family + "-" + series

const family = "gx"

const series = "7"

func main() {
	var g grid
	var a [9]int
	var b [2]int
	table[Deep-1] = Low
	g.cells[Wide-1] = Mid
	println(len(table), table[6], cells(a), offset(10, b), len(g.cells), g.cells[11], gain == 0.3, model)
}
`,
		want: "7 3 9 12 12 6 true gx-7\n",
	},
	{
		// len and cap are constants where Go makes them ones -- of a constant string,
		// and of an array or a pointer to one reached with no call and no receive --
		// in a constant declaration, an array bound and a shift. Neither was ever a
		// constant here: the declarations and bounds were refused, and `1 <<
		// len(msg) >> 10` into a uint8 took the untyped 1 as a uint8, as a shift by a
		// variable does, printing 0 for Go's 4. And a length that is a constant is
		// all len is: `len(*pa)` of a nil pa is Go's 6, not a nil panic.
		name: "len and cap of an array or a constant string are constants",
		src: `type frame struct {
	head [3]byte
	body [12]byte
}

var fr frame

var pa *[6]int

var grid [5][7]int

var lut = [...]uint16{0, 3212, 6393, 9512}

const msg = "hello, world"

const (
	frameLen = len(fr.head) + len(fr.body)
	lutLen   = len(lut)
)

var shadow [lutLen * 2]int32

func cells(g [5][7]int) int {
	const n = len(g) * len(g[0])
	return n
}

func main() {
	var b [len(msg)]byte
	copy(b[:], msg)
	var u8 uint8 = 1 << len(msg) >> 10
	var z uint16 = 1 << len(fr.body) >> 11
	var f32 float32 = 1 << len(lut)
	shadow[lutLen*2-1] = 7
	println(frameLen, lutLen, len(shadow), shadow[7], len(b), b[len(msg)-1], cells(grid), len(pa), len(*pa), u8, z, f32)
	println(lut[1], fr.head[0], pa == nil)
}
`,
		want: "15 4 8 7 12 100 35 6 6 4 2 16\n3212 0 true\n",
	},
	{
		// A package constant asked for from inside a function literal whose
		// parameter is named like an operand of the constant's own initializer. The
		// checker evaluated the constant in the scope that asked, so B's C was the
		// parameter and `var buf [B]byte` was refused as a non-constant array bound.
		// A package constant names what the package scope names, wherever the
		// question comes from.
		name: "a package constant asked for where its operand is shadowed",
		src: `var scaled = func(C int) int {
	var buf [B]byte
	buf[0] = byte(C)
	return len(buf)*C + int(buf[0])
}

const B = C + 1

const C = 3

func main() {
	println(scaled(10), C)
}
`,
		want: "50 3\n",
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
		// The elements of a slice or an array literal are an array initializer, and
		// the target's compiler refuses a struct VALUE there -- a string and a slice
		// header are structs -- reading it as the first member of the first element
		// (doc/array-init-struct-value.c). `[]string{s1, s2}` and `[][]int{a, b}` did
		// not build for the target at all; each such value is braced out member by
		// member now, as a user struct's long was.
		name: "string and slice values as the elements of a literal",
		src: `type Pt struct {
	x, y int
}

var ga, gb = "pkg", "vars"

var gnames = []string{ga, gb}

func word(i int) string {
	if i > 0 {
		return "yes"
	}
	return "no"
}

func main() {
	s1, s2 := "a", "bc"
	names := []string{s1, s2, "lit", word(1)}
	println(len(names), names[0], names[1], names[2], names[3])
	var fixed [2]string = [2]string{word(0), s2}
	println(fixed[0], fixed[1])
	a, b := []int{1}, []int{2, 3}
	rows := [][]int{a, b}
	println(len(rows), rows[1][1], len(rows[0]))
	rows[0][0] = 9
	println(a[0])
	grid := [2][]int{b, a}
	println(grid[0][1], grid[1][0])
	pa, pb := []Pt{{1, 2}}, []Pt{{3, 4}, {5, 6}}
	pts := [][]Pt{pa, pb}
	println(pts[1][1].y)
	nested := [][]Pt{[]Pt{{7, 8}}, pb}
	println(nested[0][0].x, nested[1][0].y)
	println(gnames[0], gnames[1])
}
`,
		want: "4 a bc lit yes\nno bc\n2 3 1\n9\n3 9\n6\n7 4\npkg vars\n",
	},
	{
		// A row may leave its type out where the literal gives it, as Go allows:
		// `[][]int{{1, 2}, {3}}`. Each row is what `[]int{1, 2}` is -- a backing
		// array of this frame and a header over it -- evaluated in the order
		// written, at any depth, in an array of slices as in a slice of them. It was
		// "unsupported operand '{'".
		name: "type-elided rows of a slice of slices",
		src: `type Pt struct {
	x, y int
}

var calls int

func v(k int) int {
	calls = calls*10 + k
	return k
}

func sum(rows [][]int) int {
	t := 0
	for _, r := range rows {
		for _, x := range r {
			t += x
		}
	}
	return t
}

func main() {
	rows := [][]int{{v(1), v(2)}, {v(3)}, {}, {v(4), v(5), v(6)}}
	println(len(rows), len(rows[2]), calls, rows[3][2])
	calls = 0
	println(sum([][]int{{v(7)}, {v(8), v(9)}}), calls)
	names := [][]string{{"x"}, {"y", "z"}}
	names[1][0] = "w"
	println(names[1][0], names[1][1])
	var grid [2][]int = [2][]int{{v(1)}, {v(2), v(3)}}
	grid[1] = append(grid[1][:1], 7)
	println(grid[1][1], len(grid[0]))
	deep := [][][]int{{{1}, {2, 3}}, {{4}}}
	pts := [][]Pt{{{1, 2}}, {{3, 4}, {5, 6}}}
	cube := [2][2][]int{{{7}, {8}}, {{9}, {}}}
	println(deep[0][1][1], pts[1][0].x, cube[0][1][0], len(cube[1][1]))
}
`,
		want: "4 0 123456 6\n24 789\nw z\n7 1\n3 3 8 0\n",
	},
	{
		// The same rows in a package variable's initializer are static objects of
		// the program, and the table holding them is filled at initialization. A
		// package slice of slices was refused in either spelling ("a package slice
		// literal's elements must be constant") until the target could take its
		// rows in an initializer at all.
		name: "package tables of slices",
		src: `type Pt struct {
	x, y int
}

var table = [][]int{{1, 2}, {3}}

var spelled = [][]int{[]int{4}, []int{5, 6}}

var names = [][]string{{"a"}, {"b", "c"}}

var deep = [][][]int{{{1}, {2, 3}}, {{4}}}

var pts = [][]Pt{{{1, 2}}, {{3, 4}, {5, 6}}}

var cube = [2][2][]int{{{7}, {8}}, {{9}, {}}}

func main() {
	println(len(table), table[0][1], table[1][0], spelled[1][1], names[1][1])
	table[1][0] = 9
	println(table[1][0])
	println(deep[0][1][1], deep[1][0][0], pts[1][1].y, cube[1][0][0], len(cube[1][1]))
}
`,
		want: "2 2 3 6 c\n9\n3 4 6 9 0\n",
	},
	{
		// An element with its type elided where the element is a pointer, `{1, "a"}`
		// of a `[]*P`, is `&P{1, "a"}`, as Go reads it: a temporary of this frame in
		// a function, the package's own object in a package variable's initializer,
		// at any depth. It was "a type-elided composite literal element is only
		// supported for a struct element type yet".
		name: "type-elided addresses in a slice of pointers",
		src: `type P struct {
	n    int
	name string
}

var gps = []*P{{3, "c"}, {4, "d"}}

var garr = [2]*P{{5, "e"}, nil}

func total(ps []*P) int {
	t := 0
	for _, p := range ps {
		t += p.n
	}
	return t
}

func main() {
	ps := []*P{{1, "a"}, {n: 2}}
	ps[1].n++
	println(ps[0].name, ps[1].n, total(ps), total([]*P{{10, ""}, {20, ""}}))
	println(gps[1].name, gps[0].n, garr[0].name, garr[1] == nil)
	gps[0].n = 30
	println(total(gps))
	grid := [][]*P{{{7, "g"}}, {{8, "h"}, {9, "i"}}}
	println(grid[1][1].name)
}
`,
		want: "a 3 4 30\nd 3 e true\n34\ni\n",
	},
	{
		// A struct type written out in its literal, as Go allows: a package
		// variable's initializer and element, a signal value, a channel element, a
		// result, an argument, an operand of == and of a switch, an element beside
		// its elided form, behind an address and asserted back out of an interface.
		// One typedef per shape, so every mention of a shape is the same C type.
		name: "anonymous struct literals",
		src: `var cfg = struct {
	baud, pin int
	name      string
}{115200, 62, "uart"}

var table = [2]struct{ k, v int }{{1, 10}, {2, 20}}

var nums [4]int

var sig chan struct{}

var events chan struct{ id, code int }

func pair() struct{ a, b int } {
	return struct{ a, b int }{3, 4}
}

func area(r struct{ w, h int }) int { return r.w * r.h }

func signal() {
	sig <- struct{}{}
}

func report() {
	events <- struct{ id, code int }{7, 42}
}

func main() {
	println(cfg.baud, cfg.pin, cfg.name, table[1].k, table[1].v)
	p := struct {
		x, y int
		name string
	}{1, 2, "pt"}
	q := struct{ x, y int }{y: 5}
	println(p.x, p.y, p.name, q.x, q.y)
	go signal()
	<-sig
	go report()
	ev := <-events
	println(ev.id, ev.code)
	println(pair().a, struct{ n int }{7}.n, area(struct{ w, h int }{3, 5}))
	var r struct{ a, b int } = pair()
	if r == (struct{ a, b int }{3, 4}) {
		println("equal")
	}
	if r != struct{ a, b int }{1, 1} {
		println("differ")
	}
	pts := []struct{ x, y int }{{1, 2}, struct{ x, y int }{3, 4}}
	println(len(pts), pts[1].x)
	pp := &struct{ v int }{9}
	pp.v++
	println(pp.v)
	nums[1], nums[2] = 5, 6
	n := struct {
		a [2]int
		s []int
	}{[2]int{1, 2}, nums[1:3]}
	println(n.a[1], len(n.s), n.s[1])
	switch q {
	case struct{ x, y int }{0, 4}:
		println("four")
	case struct{ x, y int }{0, 5}:
		println("five")
	}
	var any interface{} = &struct{ n int }{5}
	if a, ok := any.(*struct{ n int }); ok {
		println("asserted", a.n)
	}
}
`,
		want: "115200 62 uart 2 20\n1 2 pt 0 5\n7 42\n3 7 15\nequal\ndiffer\n2 3\n10\n2 2 6\nfive\nasserted 5\n",
	},
	{
		// A struct type written out and a declared struct of the same fields are
		// types Go assigns between either way, and the target's compiler refused to
		// mix their C types. The unnamed one's typedef names the declared struct now:
		// a declaration, an assignment, an argument, a result, a send, append and ==,
		// a struct holding an array among them.
		name: "an unnamed struct type and a declared one of the same fields",
		src: `type P struct {
	x    int
	name string
}

type Grid struct {
	cells [3]int
	n     int
}

var ch chan P

func takeP(p P) int { return p.x }

func takeAnon(a struct {
	x    int
	name string
}) string {
	return a.name
}

func mkAnon() struct {
	x    int
	name string
} {
	return P{9, "nine"}
}

func mkP() P {
	return struct {
		x    int
		name string
	}{8, "eight"}
}

func main() {
	a := struct {
		x    int
		name string
	}{1, "one"}
	var p P = a
	a = p
	p = struct {
		x    int
		name string
	}{2, "two"}
	println(takeP(a), takeAnon(p), mkAnon().name, mkP().x, p.x, a.x)
	ps := []P{a, p}
	ps = append(ps[:1], a)
	println(len(ps), ps[1].name)
	println(p == a, a == P{2, "two"}, p == struct {
		x    int
		name string
	}{2, "two"})
	g := struct {
		cells [3]int
		n     int
	}{[3]int{1, 2, 3}, 3}
	var gg Grid = g
	gg.cells[0] = 7
	g = gg
	println(g.cells[0], g.n)
	go func() {
		ch <- struct {
			x    int
			name string
		}{5, "five"}
	}()
	r := <-ch
	printf("%v %+v\n", r, a)
}
`,
		want: "1 two nine 8 2 1\n2 one\nfalse false true\n7 3\n{5 five} {x:1 name:one}\n",
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
		// A method called on a struct FIELD or on a CALL RESULT whose type is a
		// defined type over a scalar. Both were refused by the checker, which had
		// only the field's or the result's Kind to go on -- and a Kind is what a
		// defined type resolves THROUGH, so "type Celsius int" carries int's and
		// nothing of its own. "type int has no method F" named a type the program
		// never wrote, of a method it had declared.
		//
		// Reaching the same value through a local always worked, which is what made
		// the shape look supported; the last line pins that the two agree.
		name: "a method on a field or a call result of a defined type",
		src: `type Celsius int32

type Name string

type Reading struct {
	t Celsius
	n Name
}

type Box struct {
	inner Reading
}

func (c Celsius) F() int32 { return int32(c)*9/5 + 32 }

func (c Celsius) hot() bool { return c > 30 }

func (n Name) size() int { return len(n) }

func mk() Celsius { return Celsius(25) }

func (r Reading) temp() Celsius { return r.t }

var g Reading

var box Box

var pool [2]Reading

func main() {
	g.t, g.n = 40, "probe"
	println(g.t.F(), g.t.hot(), g.n.size())

	// One level deeper, and through an element.
	box.inner.t = 10
	pool[1].t = 100
	println(box.inner.t.F(), pool[1].t.F())

	// On a call result, direct and through a method.
	println(mk().F(), mk().hot(), g.temp().F())

	// The long way round agrees with the short.
	v := g.t
	println(v.F() == g.t.F())
}
`,
		want: "104 true 5\n50 212\n77 false 104\ntrue\n",
	},
	{
		// `(&v).m()`, the written-out address form of a method call. Go admits it for
		// any addressable v and it means what `v.m()` means -- a value receiver copies
		// what the pointer points at, a pointer receiver is what `v.m()` already takes
		// the address for -- so the shorthand IS the lowering, the same equivalence
		// `(*p).m()` is emitted through.
		//
		// The DEFER line is the one worth having. A defer captures its receiver where
		// it stands, and the capture is keyed on the head's sole identifier, which a
		// parenthesised head does not have: without the address form being taught to
		// the capture too, the deferred call would compile and read the receiver at
		// the RETURN instead -- printing 10 here where Go prints 0. Not a refusal, a
		// wrong answer.
		//
		// Only the call form is admitted. `(&v)[i]` is not `v[i]` -- for a slice v the
		// first is illegal Go -- so it stays refused.
		name: "a method call written out through an address",
		src: `type Counter struct {
	n int
}

type Celsius int32

func (c *Counter) inc(by int) { c.n += by }

func (c Counter) get() int { return c.n }

func (c Counter) show(tag int) { println("show", tag, c.n) }

func (c *Celsius) bump() { *c += 5 }

func (c Celsius) F() int32 { return int32(c)*9/5 + 32 }

var g Counter

func deferred() {
	defer (&g).inc(3)
	defer (&g).show(1)
	(&g).inc(10)
	println("in deferred", g.n)
}

func main() {
	var c Counter
	(&c).inc(3)
	(&c).inc(4)
	println(c.n, (&c).get())

	// A defined type over a scalar, both receiver forms.
	var t Celsius = 20
	(&t).bump()
	println(int32(t), (&t).F(), t.F())

	// The shorthand and the written-out form are the same call.
	c.inc(1)
	(&c).inc(1)
	println(c.n)

	deferred()
	println("after", g.n)
}
`,
		want: "7 7\n25 77 77\n9\nin deferred 10\nshow 1 0\nafter 13\n",
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

	// Truncation is a RUN-TIME conversion, of a variable. Written as a constant,
	// int(3.75) is refused -- Go's rule is that a constant converts only where it
	// is representable in the target, and this case used to spell it that way.
	t := 3.75
	println(int(t), float64(9)/2.0)
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
		// A setter taking a POINTER TO the struct rather than a receiver -- the same
		// store, one that the crossing summary had no way to state. leakRecv says
		// "into the receiver"; a plain function has none, so `func fill(p *H, d
		// []int) { p.d = d }` carried nothing to its callers and `fill(&g, a[:])`
		// left a header over a dead frame in a package variable.
		//
		// Everything here is the accepting side of that rule: the storage the chain
		// ends in dies with the reference, so nothing is refused. The second call is
		// the one that needs the summary to be PER PARAMETER -- the frame-backed
		// argument goes to `scratch`, which is only measured, while the parameter
		// that IS stored gets package backing.
		name: "a setter taking a pointer to the struct",
		src: `type H struct {
	d []int
	n int
}

var back = [4]int{5, 6, 7, 8}

func fill(p *H, d []int) { p.d = d }

func outer(p *H, d []int) { fill(p, d) }

func two(p *H, keep, scratch []int) {
	p.d = keep
	p.n = len(scratch)
}

func (t *H) inner(d []int) { t.d = d }

func pass(p *H, d []int) { p.inner(d) }

func main() {
	var a [4]int
	a[0], a[1] = 1, 2
	var local H
	outer(&local, a[:])
	println(local.d[0], local.d[1], len(local.d))

	var scratch [3]int
	two(&local, back[:], scratch[:])
	println(local.d[0], local.n)

	// A METHOD called on a pointer parameter, the receiver whose type the
	// summary could not resolve before it read the type as written.
	pass(&local, a[:])
	println(local.d[1])
}
`,
		want: "1 2 4\n5 3\n2\n",
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
		// Two producer cogs sending to ONE channel, both live at once, drained by
		// main -- the multi-producer path, where the rendezvous lock actually has
		// to serialise two senders rather than shepherd one. The interleaving is
		// nondeterministic but the SUM is not, so the want is stable whatever order
		// the two cogs win the lock in. Every other two-cog case here sends on
		// SEPARATE channels; this is the one that contends for a single one.
		name: "two producers fan in to one channel",
		src: `// Fan-in: two producer cogs both send to one channel; main drains both. The
// order is nondeterministic but the SUM is not, so the checksum is stable
// whatever the interleaving -- which is the point of testing two producers on
// one hardware-lock rendezvous.
var ch chan int

const perProducer = 20

func producerA() {
	for i := 0; i < perProducer; i++ {
		ch <- 100 + i
	}
}

func producerB() {
	for i := 0; i < perProducer; i++ {
		ch <- 1000 + i*2
	}
}

func main() {
	go producerA()
	go producerB()
	sum := 0
	count := 0
	for i := 0; i < 2*perProducer; i++ {
		sum += <-ch
		count++
	}
	println(sum, count)
}
`,
		want: "22570 40\n",
	},
	{
		// A three-stage pipeline across three cogs: a source, a filter that is BOTH
		// a consumer and a producer (it receives on one channel and sends on
		// another), and main. The chained stage is the new shape -- every other
		// worker here only produces or only consumes -- and it is the shape a real
		// sample-then-process firmware has. Board-verified against Go.
		name: "a three-cog pipeline, source filter and sink",
		src: `// A 3-stage DSP pipeline across three cogs: a source generates samples, a
// filter stage smooths them (a 2-tap moving sum) and rescales, and main
// aggregates. Each stage is its own cog, chained through channels -- the shape a
// real sampling-and-processing firmware has.
var raw chan int
var filtered chan int

const nSamples = 12

func source() {
	// A deterministic "signal": a ramp with a periodic spike.
	for i := 0; i < nSamples; i++ {
		v := i * 3
		if i%4 == 0 {
			v += 50
		}
		raw <- v
	}
}

func filter() {
	prev := 0
	for i := 0; i < nSamples; i++ {
		x := <-raw
		// 2-tap moving sum, then a rescale that can overflow int if unlucky.
		y := (x + prev) * 2
		prev = x
		filtered <- y
	}
}

func main() {
	go source()
	go filter()
	sum := 0
	mx := 0
	for i := 0; i < nSamples; i++ {
		y := <-filtered
		sum += y
		if y > mx {
			mx = y
		}
	}
	println(sum, mx)
}
`,
		want: "1326 202\n",
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
		// The program the bytes package exists for: a command line arriving as bytes
		// in a receive buffer, cut on a space, compared with a name, parsed and
		// dispatched. string(rx[:n]) is a copy the target cannot make, so before
		// bytes every one of these steps was a loop the program wrote itself. Every
		// line is what real Go prints for the same program.
		name: "a command parser over a byte buffer",
		src: `import "bytes"

var calls int

type dev struct {
	led   int
	speed int
	n     int
}

func (d *dev) set(v int) int {
	calls = calls*10 + 1
	d.led = v
	d.n++
	return d.led
}

var board dev

var ledName = [3]byte{'l', 'e', 'd'}

var speedName = [5]byte{'s', 'p', 'e', 'e', 'd'}

var sep = [1]byte{' '}

var slash = [1]byte{'/'}

func atoi(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func doLed(arg []byte) int {
	calls = calls*10 + 2
	v, ok := atoi(arg)
	if !ok {
		return -1
	}
	return board.set(v)
}

func doSpeed(arg []byte) int {
	calls = calls*10 + 3
	head, tail, found := bytes.Cut(arg, slash[:])
	num, okn := atoi(head)
	den, okd := atoi(tail)
	if !found || !okn || !okd || den == 0 {
		return -1
	}
	board.speed = num / den
	return board.speed
}

func dispatch(line []byte) (int, bool) {
	line = bytes.TrimSpace(line)
	name, arg, _ := bytes.Cut(line, sep[:])
	arg = bytes.TrimSpace(arg)
	switch {
	case bytes.Equal(name, ledName[:]):
		return doLed(arg), true
	case bytes.Equal(name, speedName[:]):
		return doSpeed(arg), true
	}
	return 0, false
}

func main() {
	var rx [32]byte
	inputs := [5]string{"  led 7 ", "speed 10/3", "led x", "nope", "speed 4/0"}
	for i := 0; i < len(inputs); i++ {
		n := copy(rx[:], inputs[i])
		line := rx[:n]
		v, ok := dispatch(line)
		printf("%d %d %t %q %d\n", i, v, ok, bytes.TrimSpace(line), bytes.IndexByte(line, ' '))
	}
	printf("%d %d %d %d\n", board.led, board.speed, board.n, calls)
}
`,
		want: `0 7 true "led 7" 0
1 3 true "speed 10/3" 5
2 -1 true "led x" 3
3 0 false "nope" -1
4 -1 true "speed 4/0" 5
7 3 1 21323
`,
	},
	{
		// An ARRAY declared by a clause: `switch a := [2]int{1, 2}; len(a)` and `for
		// b := [3]int{...}; ...`, which were "cannot infer the type of the switch
		// guard variable" and "... of a for-loop init variable" -- an array has no C
		// value type, and the clauses asked for one where the statement form
		// declares the array with its extents and fills it. The `if` form always
		// worked, which is what said the two were a gap rather than a rule. A named
		// array type takes parentheses in a header, as it does in Go.
		name: "an array declared by a switch or for clause",
		src: `type grid [2][2]int

func mk(n int) [3]int {
	var r [3]int
	r[0], r[1], r[2] = n, n+1, n+2
	return r
}

var a = [2]int{9, 9}

func main() {
	switch a := (grid{{1, 2}, {3, 4}}); len(a) {
	case 2:
		println("grid", a[0][0], a[1][1])
	}
	switch a := mk(5); a[2] {
	case 7:
		println("call", a[0], a[1], a[2])
	default:
		println("call-default", a[0])
	}
	for i, b := 0, [2]int{3, 4}; i < 2; i++ {
		println("multi", i, b[i])
	}
	for b := [3]int{1, 2, 3}; b[0] < 3; b[0] += 2 {
		s := 0
		for _, v := range b {
			s += v
		}
		println("range", b[0], s)
	}
	println("outer", a[0], a[1])
	if a := mk(1); a[0] == 1 {
		println("if", a[0], a[2])
	}
}
`,
		want: `grid 1 4
call 5 6 7
multi 0 3
multi 1 4
range 1 6
outer 9 9
if 1 3
`,
	},
	{
		// A NAMED array result. An array is written through the caller's storage
		// rather than returned, so the name the signature gives it had nothing
		// declared for it: the body's `r[0] = n` named nothing and `return r` was
		// "an array result must be returned as a variable or an array literal",
		// which is what r is. It is a variable of the frame now, copied out by a
		// return that names it and by a bare one -- through a method, a whole
		// assignment, recursion and a defer, and beside a literal's own r. Every
		// line is what real Go prints.
		name: "a named array result",
		src: `type grid [2][3]int

func mk(n int) (r [2]int) {
	r[0] = n
	r[1] = n * 2
	return r
}

func naked(n int) (r [3]int) {
	for i := 0; i < len(r); i++ {
		r[i] = n + i
	}
	return
}

func early(n int) (r [2]int) {
	if n < 0 {
		return
	}
	r[0] = n
	return r
}

func twoDim(n int) (g grid) {
	g[1][2] = n
	return g
}

func deferred(n int) (r [2]int) {
	defer func(v int) { println("defer", v) }(n)
	r[0] = n
	return r
}

func lit(n int) (r [2]int) {
	return [2]int{n, n + 1}
}

type box struct{ base int }

var pkgArr = [2]int{8, 9}

func (b *box) pair(n int) (r [2]int) {
	r[0] = b.base
	r[1] = n
	return r
}

func whole(n int) (r [2]int) {
	r = [2]int{n, n + 1}
	return r
}

func fromVar() (r [2]int) {
	r = pkgArr
	r[0]++
	return
}

func rec(n int) (r [3]int) {
	if n > 0 {
		r = rec(n - 1)
	}
	r[0] = r[0] + n
	return
}

func inner(n int) (r [2]int) {
	f := func(v int) int {
		r := v * 3
		return r
	}
	r[0] = f(n)
	return r
}

func first() {
	a := mk(3)
	b := naked(10)
	c := early(-1)
	d := early(4)
	g := twoDim(7)
	e := deferred(5)
	f := lit(8)
	println(a[0], a[1], b[0], b[1], b[2])
	println(c[0], c[1], d[0], d[1])
	println(g[0][0], g[1][2], e[0], e[1], f[0], f[1])
}

func second() {
	var b box
	b.base = 2
	p := b.pair(5)
	w := whole(1)
	v := fromVar()
	c := rec(3)
	i := inner(4)
	println(p[0], p[1], w[0], w[1])
	println(v[0], v[1], c[0], c[1], c[2])
	println(i[0], i[1], pkgArr[0])
}

func main() {
	first()
	second()
}
`,
		want: `defer 5
3 6 10 11 12
0 0 4 0
0 7 5 0 8 9
2 5 1 2
9 9 6 0 0
12 0 8
`,
	},
	{
		// A literal uses the constants and the types of the function around it, as
		// Go's does: they were "undefined" in one. The C constants are declared again
		// in the lifted function, in a block of their own around the body, so its own
		// `n := 2` after reading n reads the function's first (h). And the function
		// keeps its names after a literal is lifted: it lost its local types, the
		// target of a forward goto and a string constant the literal redeclared
		// (after). An unused local constant, which Go takes, compiles for the host too.
		name: "a literal using its function's constants and types",
		src: `func consts() {
	const n = 4
	const (
		a = iota
		b
	)
	const s = "x"
	const t = s + s
	const big = 1 << 40
	const unused = 7
	f := func(v int) int {
		var arr [n]int
		arr[n-1] = v
		return arr[n-1]*n + a + b + len(t)
	}
	g := func() int64 { return big + 1 }
	h := func() int {
		y := n
		n := 2
		return n*10 + y
	}
	k := func(n int) int { return n }
	println(f(1), g(), h(), k(9), n)
}

func types() {
	type pair struct{ a, b int }
	type celsius int
	type deg = celsius
	const boil celsius = 100
	less := func(x, y pair) bool { return x.a < y.a }
	mk := func(a int) pair { return pair{a, a * 2} }
	warm := func(d deg) deg { return d + boil }
	own := func() int {
		type pair struct{ c int }
		return pair{3}.c
	}
	p := mk(5)
	println(less(mk(1), mk(2)), warm(1), own(), p.a, p.b)
}

func after() {
	type pair struct{ a, b int }
	const s = "outer"
	i := 0
	f := func() string {
		const s = "inner"
		return s
	}
	if f() == "inner" {
		goto done
	}
	i = 5
done:
	q := pair{1, 2}
	println(f(), s, i, q.a+q.b)
}

func main() {
	consts()
	types()
	after()
}
`,
		want: "7 1099511627777 24 9 4\ntrue 101 3 5 10\ninner outer 0 3\n",
	},
	{
		// A literal's names are its own: a local it declares, a loop's or a clause's
		// variable, a parameter or a result it names, a field it selects and a key it
		// writes, each named as a variable of main is. The checker took every such
		// name for a capture of main's and refused the program. The package x beside
		// main's x is the other half: a literal reading x reads main's in Go, and the
		// package's here, silently, until the capture was asked of the lookup itself
		// (lit_scope.ogo has the refusals).
		name: "a literal's own names beside the function's",
		src: `type P struct{ x, y int }

func (p *P) bump() { p.x++ }

var x = 10

var arr = [3]int{1, 2, 3}

func gx() int { return x }

func main() {
	x := 1
	i, v := 100, 200
	p := P{1, 2}
	f := func(q P) int {
		x := q.x
		var y int = 3
		for i := 0; i < 2; i++ {
			y += i
		}
		for i, v := range arr {
			y += i * v
		}
		if x := y; x > 0 {
			y++
		}
		switch x := y + 1; x {
		case 13:
			y--
		}
		p := P{x: x, y: y}
		p.bump()
		return p.x*100 + p.y
	}
	g := func(x int) (y int) {
		y = x * 2
		return
	}
	h := func() int {
		x := 5
		{
			x++
		}
		ptr := &x
		*ptr += 10
		return x
	}
	println(f(p), g(21), h(), x, i, v, p.x, p.y, gx())
}
`,
		want: "213 42 16 1 100 200 1 2 10\n",
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
		// Struct embedding: a field written as a bare type name puts its own fields
		// and methods in the outer type, without naming it. In C it is an ordinary
		// member named after the type, and what promotion costs is the members the
		// source did not write -- `d.n` is `d.middle.base.n`, `d.Get()` is
		// `base_Get(&d.middle.base)`. Two levels deep here, since one level can be
		// right by accident.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "struct embedding, fields and methods promoted",
		src: `type base struct {
	n int
}

func (b base) Get() int { return b.n }

func (b *base) Bump(k int) int {
	b.n += k
	return b.n
}

type middle struct {
	base
	m int
}

type derived struct {
	middle
	d int
}

var gd derived

func main() {
	// A promoted field, at one level and at two.
	gd.n = 1
	gd.m = 2
	gd.d = 3
	println(gd.n, gd.m, gd.d)

	// The embedded field may still be named explicitly.
	gd.middle.base.n = 7
	println(gd.n, gd.middle.base.n)

	// A promoted method, by value and by pointer receiver.
	println(gd.Get(), gd.Bump(2), gd.n)

	// A local, not just a package variable.
	var d derived
	d.n = 5
	println(d.Get(), d.n)
}
`,
		want: "1 2 3\n7 7\n7 9 9\n5 5\n",
	},
	{
		// A function literal after "go" and after "defer". Both take a declared
		// function -- a cog's entry point is generated per function, and a deferred
		// call is replayed by name at every return -- and a lifted literal IS one, so
		// what this needed was the grammar line admitting it and the lift.
		//
		// Every line of this prints what real Go prints for the same program,
		// deferred order included.
		name: "a function literal after go and defer",
		src: `var ch chan int

var done chan int

func work(k int) {
	ch <- k * 2
}

func main() {
	// A deferred literal runs at every return, in LIFO order.
	defer func() {
		println("second deferred")
	}()
	defer func() {
		println("first deferred")
	}()

	// A cog started from a literal: what it shares, it shares through a channel.
	go func() {
		ch <- 21
		done <- 1
	}()
	println(<-ch)
	<-done

	// The named form still works beside it.
	go work(5)
	println(<-ch)

	// A literal with PARAMETERS, called where it stands: a literal cannot capture,
	// so this is how a value reaches a goroutine written in place.
	go func(c chan int, k int) { c <- k * 3 }(ch, 4)
	println(<-ch)

	println("body done")
}
`,
		want: "21\n10\n12\nbody done\nfirst deferred\nsecond deferred\n",
	},
	{
		// A method value: a method taken as a value, with its receiver bound. Go
		// carries the receiver in the value, which needs a representation that costs
		// about a quarter of the time of EVERY call through a function value on this
		// part (doc/funcval-cost.c). Here the receiver is bound at compile time
		// instead -- the value is lifted to a function of its own -- so it stays an
		// ordinary one-word function pointer and costs nothing that anything else
		// pays. What cannot be bound is refused: a value receiver (Go copies it) and
		// a receiver that is not a package-level variable.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a method value with its receiver bound",
		src: `type counter struct {
	n int
}

func (c *counter) Bump(k int) int {
	c.n += k
	return c.n
}

func (c *counter) Reset() { c.n = 0 }

func (c counter) Get() int { return c.n }

type Op func(int) int

var gc counter

var gd counter

var table [2]Op

func apply(f Op, k int) int { return f(k) }

func main() {
	// Bound to a variable and called through it: the receiver is bound, so it is
	// the same object every call.
	f := gc.Bump
	println(f(2), f(3), gc.n)

	// Handed to a parameter, and held in a dispatch table beside a plain function.
	println(apply(gc.Bump, 10))
	table[0] = gc.Bump
	table[1] = gd.Bump
	println(table[0](1), table[1](100), gc.n, gd.n)

	// A method with no result, and the same value written twice.
	r := gc.Reset
	r()
	r2 := gc.Reset
	r2()
	println(gc.n)
}
`,
		want: "2 5 5\n15\n16 100 16 100\n0\n",
	},
	{
		// A METHOD EXPRESSION, T.M or (*T).M: the method as a function whose first
		// parameter is the receiver. It is lifted to a C function of its own that
		// calls the method, so a call of the expression and its use as a value go
		// the ways a function's do. Called, bound to a variable, handed on, held in
		// a slice and a struct field, launched with go and deferred -- a promoted
		// method, a value method through the pointer form, results of every count.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "method expressions",
		src: `type Counter struct {
	n int
}

func (c Counter) Get() int { return c.n }

func (c *Counter) Add(k int) int {
	c.n += k
	return c.n
}

type Celsius int

func (c Celsius) Double() Celsius { return 2 * c }

func apply(f func(Counter) int, c Counter) int { return f(c) }

func main() {
	c := Counter{3}
	get := Counter.Get
	add := (*Counter).Add
	println(get(c), add(&c, 4), c.n)
	println(Counter.Get(c), (*Counter).Add(&c, 1))
	println(apply(Counter.Get, c))
	pget := (*Counter).Get
	println(pget(&c))
	d := Celsius.Double
	println(d(21))
	ops := []func(Counter) int{Counter.Get}
	println(ops[0](c))
}
`,
		want: "3 7 7\n7 8\n8\n8\n42\n8\n",
	},
	{
		name: "method expressions called, bound, launched and deferred",
		src: `type Point struct {
	x, y int
}

func (p Point) Scaled(k int) Point { return Point{p.x * k, p.y * k} }

func (p Point) Parts() (int, int) { return p.x, p.y }

func (p *Point) Move(dx int) { p.x += dx }

type Inner struct {
	v int
}

func (i Inner) Get() int { return i.v }

func (i *Inner) Set(v int) { i.v = v }

type Outer struct {
	Inner
	tag string
}

type Celsius int

func (c Celsius) F() int { return int(c)*9/5 + 32 }

type Op struct {
	name string
	fn   func(*Point, int)
}

var done chan int

type Worker struct {
	n int
}

func (w *Worker) Run(k int) { done <- w.n * k }

var gw Worker

func main() {
	p := Point{1, 2}
	(*Point).Move(&p, 5)
	Point.Scaled(p, 3)
	q := Point.Scaled(p, 2)
	println(p.x, q.x, q.y)
	a, b := Point.Parts(q)
	parts := Point.Parts
	c, d := parts(p)
	println(a, b, c, d)
	scale := Point.Scaled
	r := scale(p, 10)
	println(r.x, r.y)
	o := Outer{Inner{7}, "t"}
	println(Outer.Get(o))
	(*Outer).Set(&o, 9)
	get := (*Outer).Get
	println(get(&o), o.v)
	println(Celsius.F(100))
	ops := []Op{{"move", (*Point).Move}}
	ops[0].fn(&p, 1)
	println(ops[0].name, p.x)
	gw.n = 6
	go (*Worker).Run(&gw, 7)
	println(<-done)
	defer println("deferred", Point.Scaled(p, 2).x)
	defer (*Point).Move(&p, 100)
}
`,
		want: "6 12 4\n12 4 6 2\n60 20\n7\n9 9\n212\nmove 7\n42\ndeferred 14\n",
	},
	{
		// The receivers a method expression can have beyond a plain struct: an
		// alias, a method promoted through an embedded POINTER -- whose pointer
		// methods are in the value's method set, so W.Scale is legal -- a defined
		// array type, a defined string type; and a variadic method, and one whose
		// result is a struct, which reaches a function value through a wrapper.
		// calls records the order of every call.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "method expressions of every kind of receiver",
		src: `var calls int

type Point struct {
	X, Y int
}

func (pt Point) Sum() int {
	calls = calls*10 + 1
	return pt.X + pt.Y
}

func (pt *Point) Scale(k int) {
	calls = calls*10 + 2
	pt.X *= k
	pt.Y *= k
}

func (pt Point) Add(xs ...int) int {
	calls = calls*10 + 3
	s := pt.X
	for _, x := range xs {
		s += x
	}
	return s
}

func (pt Point) Swap() Point {
	calls = calls*10 + 4
	return Point{pt.Y, pt.X}
}

type A = Point

type W struct {
	*Point
	Tag int
}

type Row [3]int

func (r *Row) Set(i, v int) {
	calls = calls*10 + 5
	r[i] = v
}

func (r *Row) Total() int {
	calls = calls*10 + 6
	return r[0] + r[1] + r[2]
}

type Name string

func (n Name) Len() int {
	calls = calls*10 + 7
	return len(n)
}

func main() {
	pt := Point{1, 2}
	println(A.Sum(pt), calls)
	(*A).Scale(&pt, 2)
	println(pt.X, pt.Y, calls)
	println(Point.Add(pt, 1, 2, 3), Point.Add(pt), calls)
	add := Point.Add
	println(add(pt, 10, 20), calls)
	sw := Point.Swap(pt)
	println(sw.X, sw.Y, Point.Swap(pt).X, calls)
	swf := Point.Swap
	println(swf(pt).Y, calls)
	w := W{&pt, 7}
	println(W.Sum(w), calls)
	W.Scale(w, 3)
	println(pt.X, pt.Y, calls)
	var r Row
	(*Row).Set(&r, 1, 5)
	set := (*Row).Set
	set(&r, 2, 6)
	println((*Row).Total(&r), calls)
	println(Name.Len("hello"), calls)
	nl := Name.Len
	println(nl(Name("ab")), calls)
}
`,
		want: "3 1\n2 4 12\n8 2 1233\n32 12333\n4 2 4 1233344\n2 12333444\n6 123334441\n6 12 1233344412\n11 688798604\n5 -1701948545\n2 160383741\n",
	},
	{
		// A PROMOTED method of several results, destructured and forwarded by a
		// return: the results are the embedded type's method's, which nothing
		// asked for -- the outer type has no function of the name -- so `return
		// o.Two()` was refused, "a return supplying every result needs a call
		// whose results are exactly int, int".
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a promoted method's several results",
		src: `type Holder struct {
	n int
}

func (h Holder) Two() (int, int) { return h.n, h.n * 2 }

func (h *Holder) Inc() int {
	h.n++
	return h.n
}

type Outer struct {
	Holder
	tag int
}

func pair() (int, int) {
	var o Outer
	o.n = 4
	return o.Two()
}

func main() {
	var o Outer
	o.n = 3
	a, b := o.Two()
	println(a, b)
	c, d := pair()
	println(c, d)
	x := o.Inc()
	println(x, o.n)
}
`,
		want: "3 6\n4 8\n4 4\n",
	},
	{
		// An anonymous struct type, written where a type is wanted rather than
		// declared with a name of its own. Go gives two of them the same identity
		// when their fields match, so the typedef is minted once per SHAPE -- which
		// is what makes the assignment on the third line legal, and what stops a
		// typedef per mention.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "anonymous struct types",
		src: `// A package-level one, and a field of a named struct.
var origin struct {
	x, y int
}

type frame struct {
	at struct {
		x, y int
	}
	n int
}

var gf frame

func shift(p *struct {
	x, y int
}, dx int) {
	p.x += dx
}

func main() {
	// A local, its fields written and read.
	var p struct {
		x, y int
	}
	p.x, p.y = 3, 4
	println(p.x, p.y)

	// Two anonymous structs with the same fields are the SAME type, so one is
	// assignable to the other.
	origin = p
	println(origin.x, origin.y)

	// As a struct field, at any depth.
	gf.at.x = 7
	gf.n = 1
	println(gf.at.x, gf.n)

	// Through a pointer parameter.
	shift(&p, 10)
	println(p.x)

	// An array of them.
	var pts [2]struct {
		x, y int
	}
	pts[0].x = 1
	pts[1].x = 2
	println(pts[0].x + pts[1].x)
}
`,
		want: "3 4\n3 4\n7 1\n13\n3\n",
	},
	{
		// A deferred print, with arguments. Go evaluates a deferred call's arguments
		// AT THE DEFER and runs the call at the return, so the values printed are the
		// ones that were there then -- which is the whole point of deferring a print
		// and was exactly what could not be expressed before: the print path renders
		// per-type printf calls of its own and did not consult the captured
		// temporaries, so it was refused rather than made to lie.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a deferred print, with arguments",
		src: `var g int

func f() int {
	g++
	return g
}

func one() {
	x := 1
	// Go evaluates a deferred call's arguments AT THE DEFER, so this prints 1
	// even though x is 99 by the time it runs.
	defer println("one:", x)
	x = 99
	println("body:", x)
}

func two() {
	s := "before"
	b := true
	defer println(s, b, f())
	s = "after"
	b = false
	println("f is now", g)
}

func main() {
	one()
	two()
	println("g", g)
}
`,
		want: "body: 99\none: 1\nf is now 1\nbefore true 1\ng 1\n",
	},
	{
		// The p2 package's named pin-configuration constants. They exist because the
		// hex they replace is unforgiving in a way that looks like working code: the
		// gopher example was written with 0x140006, which is the DAC range and the
		// mode and NO OUTPUT ENABLE -- it compiles, runs, and drives nothing.
		// `p2.DAC990R3V | p2.DACDitherPWM | p2.OutputEnable` cannot be written with a
		// bit missing without the name of the missing bit being absent from the line.
		name: "the p2 package's pin-configuration constants",
		src: `import "p2"

// A CONST declaration takes them, which is where a pin mode belongs.
const mode = p2.DAC990R3V | p2.OutputEnable

func main() {
	println(p2.DAC990R3V | p2.DACDitherPWM | p2.OutputEnable)
	println(p2.DAC600R2V, p2.DAC124R3V, p2.DAC75R2V)
	println(p2.DACNoise, p2.DACDitherRnd, p2.OutputEnable)
	println(mode)
}
`,
		want: "1310790\n1376256 1441792 1507328\n2 4 64\n1310784\n",
	},
	{
		// Two shapes this compiler documented as broken until they were measured
		// again, both fixed by the temporary that doc/call-through-array-element.c
		// describes -- binding an intermediate rather than calling straight through
		// it, which was the manual workaround both notes prescribed.
		//
		//   - THREE CALLS DEEP, chooser()(0)(6), computed 0 on the board at every
		//     optimization level while the host was right. Silently.
		//   - A call written directly on an ARRAY ELEMENT of function type,
		//     fns[0](8), which the target's C compiler refused outright.
		//
		// Pinned here because the first was a silent wrong answer, which is the kind
		// that comes back unnoticed.
		name: "three calls deep, and a call on an array element",
		src: `type Op func(int) int

type Pick func(int) Op

func add6(a int) int { return a + 6 }

func mul6(a int) int { return a * 6 }

func pick(k int) Op {
	if k == 0 {
		return add6
	}
	return mul6
}

func chooser() Pick { return pick }

var fns [2]Op

func main() {
	// Three calls deep, the documented one.
	println(chooser()(0)(6))
	println(chooser()(1)(6))

	// The same, broken up, which the note says always worked.
	a := chooser()
	b := a(0)
	println(b(6))

	// A call written directly on an array element of function type.
	fns[0] = add6
	fns[1] = mul6
	println(fns[0](8), fns[1](8))
}
`,
		want: "12\n36\n12\n14 48\n",
	},
	{
		// A channel held in a struct field. A channel is already a POINTER to its
		// rendezvous cell -- it has to be, or handing one to a goroutine would hand
		// it a copy -- so a field holding one needs no new representation, and a copy
		// of the struct shares the channel exactly as a copy of a channel does in Go.
		//
		// The one rule is where the cell comes from, and it is the rule a channel
		// variable already obeys: the DECLARATION owns it. So a struct TYPE allocates
		// nothing, two variables of one type have a channel each, and a copy shares.
		//
		// Every line of this prints what real Go prints for the same program, with
		// the make() calls Go needs and this target does not.
		name: "a channel held in a struct field",
		src: `type ports struct {
	tx   chan int
	rx   chan int
	name string
}

type Ch chan int

type named struct {
	c Ch
	n int
}

var p ports

var q ports

var nm named

func worker() {
	v := <-p.tx
	p.rx <- v * 2
}

func tag() {
	nm.c <- nm.n
}

func main() {
	// Two variables of one struct type have a channel each: the declaration owns
	// the cell, so p.tx and q.tx are different channels.
	go worker()
	p.tx <- 21
	println(<-p.rx)

	// A copy shares the channel it was copied from, which is what a copy of a
	// channel does in Go too.
	r := p
	go worker()
	r.tx <- 5
	println(<-r.rx)

	// A defined type over a channel, held in a field.
	nm.n = 7
	go tag()
	println(<-nm.c)

	// The other variable's channels are its own and were never used.
	println(q.name == "")
}
`,
		want: "42\n10\n7\ntrue\n",
	},
	{
		// A select over channels held in STRUCT FIELDS, which is what a driver's
		// ports look like: several channels belonging to one thing. All three clause
		// shapes on a field -- a receive binding a value, a send, and a bare receive.
		//
		// Written so the two machines cannot disagree: the second select has no
		// default, so it waits rather than depending on whether the other side has
		// been scheduled yet. With a default there it printed "neither" under Go,
		// whose goroutine had not run, and "b 2" on the board, whose cog genuinely
		// had -- both right, and useless as a test.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a select over channels in struct fields",
		src: `type ports struct {
	a chan int
	b chan int
}

var p ports

var done chan int

func feedA() { p.a <- 1 }

func feedB() { p.b <- 2 }

func drain() {
	v := <-p.a
	done <- v
}

func main() {
	// Nothing is ready, so the default clause runs. A select over two fields of one
	// struct is what a driver's ports look like.
	select {
	case v := <-p.a:
		println("a", v)
	case v := <-p.b:
		println("b", v)
	default:
		println("neither")
	}

	// With no default the select waits, so this is the same on either machine
	// however the two schedulers happen to run.
	go feedB()
	select {
	case v := <-p.a:
		println("a", v)
	case v := <-p.b:
		println("b", v)
	}

	// A send clause on a field.
	go drain()
	select {
	case p.a <- 7:
		println("sent")
	}
	println("drained", <-done)

	// A bare receive clause on a field, with no value bound.
	go feedA()
	select {
	case <-p.a:
		println("bare")
	}
}
`,
		want: "neither\nb 2\nsent\ndrained 7\nbare\n",
	},
	{
		// A select's SEND clause, over the element types it could not carry. The
		// blocking `ch <- v` handled both of these and the clause handled neither:
		// an INTERFACE element took the raw pointer where the two words go, and an
		// ARRAY element was bound with `elem tmp = arr`, which is not C -- and even
		// past that, the offer helper took a parameter of array type and stored it
		// with an assignment, where the blocking send crosses by pointer and memcpys.
		//
		// The idle clause is there so this is a real select rather than a send
		// written inside braces: nothing ever sends on it, so which clause runs is
		// still determined.
		name: "a select sending an interface and an array",
		src: `type I interface {
	n() int
}

type P struct {
	a int
}

func (p *P) n() int { return p.a }

type Row [2]int

var gp P

var arr [2]int

var rv Row

var ci chan I

var ca chan [2]int

var cr chan Row

var idle chan int

var done chan int

func drainI() { v := <-ci; done <- v.n() }

func drainA() { v := <-ca; done <- v[0]*10 + v[1] }

func drainR() { v := <-cr; done <- v[0]*10 + v[1] }

func main() {
	gp.a = 7
	arr[0], arr[1] = 3, 4
	rv[0], rv[1] = 5, 6

	go drainI()
	select {
	case ci <- &gp:
		println("sent iface")
	case <-idle:
		println("idle")
	}
	println("got", <-done)

	go drainA()
	select {
	case ca <- arr:
		println("sent array")
	case <-idle:
		println("idle")
	}
	println("got", <-done)

	go drainR()
	select {
	case cr <- rv:
		println("sent Row")
	case <-idle:
		println("idle")
	}
	println("got", <-done)
}
`,
		want: "sent iface\ngot 7\nsent array\ngot 34\nsent Row\ngot 56\n",
	},
	{
		// An ARRAY of structs holding channels: a worker per element, each with
		// channels of its own. On a part with eight cores that is the shape the
		// hardware suggests, and it is what completes the feature -- sends, receives
		// and a select, through a constant index and a variable one.
		//
		// The array's declaration owns a cell per element per field, which is the
		// same rule a channel variable obeys, applied once per element.
		//
		// Every line of this prints what real Go prints for the same program, with
		// the make() calls Go needs and this target does not.
		name: "an array of structs holding channels",
		src: `type worker struct {
	cmd  chan int
	done chan int
}

var ws [2]worker

func run0() {
	v := <-ws[0].cmd
	ws[0].done <- v * 10
}

func run1() {
	v := <-ws[1].cmd
	ws[1].done <- v * 100
}

func main() {
	// One worker per element, each with channels of its own: the array's
	// declaration owns a cell per element per field, so ws[0] and ws[1] rendezvous
	// with different cogs and never with each other.
	go run0()
	go run1()
	ws[0].cmd <- 1
	ws[1].cmd <- 2
	println(<-ws[0].done, <-ws[1].done)

	// A variable index, both directions. What the field IS never varies with the
	// index; only which cell it names does.
	go run0()
	go run1()
	for i := 0; i < 2; i++ {
		ws[i].cmd <- i + 3
	}
	for i := 0; i < 2; i++ {
		println(i, <-ws[i].done)
	}

	// A select over two elements' channels.
	go run0()
	ws[0].cmd <- 5
	select {
	case v := <-ws[0].done:
		println("got", v)
	case v := <-ws[1].done:
		println("other", v)
	}
}
`,
		want: "10 200\n0 30\n1 400\ngot 50\n",
	},
	{
		// An array as a function result. C cannot return one, and the obvious
		// workaround -- wrapping it in a struct, as a multi-result function's results
		// are -- is a shape this backend refuses to assign. So it travels through an
		// OUT PARAMETER: the caller owns the storage and the callee fills it, which
		// is the answer C has always had.
		//
		// The declaration IS the storage, so binding one costs a call and no copy.
		// What that leaves is that the call is a statement rather than a value:
		// TestEmitCArrayResultABI pins the forms that refuses.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "an array as a function result",
		src: `type Row [3]int

func mk(base int) [3]int {
	var r [3]int
	r[0] = base
	r[1] = base + 1
	r[2] = base + 2
	return r
}

func grid() [2][3]int {
	var g [2][3]int
	g[0][0] = 1
	g[1][2] = 6
	return g
}

type box struct {
	n int
}

func (b box) triple() [3]int {
	var r [3]int
	r[0] = b.n
	r[1] = b.n * 2
	r[2] = b.n * 3
	return r
}

var gb box

func main() {
	a := mk(10)
	println(a[0], a[1], a[2])

	// A multi-dimensional result travels as one block.
	g := grid()
	println(g[0][0], g[1][2])

	// A method's result, and a second call into a different variable: each call
	// writes the storage its caller gave it.
	gb.n = 5
	t := gb.triple()
	b := mk(100)
	println(t[2], b[0], a[0])

	// Declared with its type written out rather than inferred.
	var c [3]int = mk(7)
	println(c[1])
}
`,
		want: "10 11 12\n1 6\n15 100 10\n8\n",
	},
	{
		// A method on a defined type over a channel, which is what makes a channel a
		// named thing with behaviour rather than a bare pipe. Two such types over the
		// same element have methods of their own, which is the reason it needed a C
		// name of its own: it used to be answered for by the cell's, so Ch and Gate
		// would have shared one method namespace.
		//
		// Every line of this prints what real Go prints for the same program, with
		// the make() calls Go needs and this target does not.
		name: "a method on a defined channel type",
		src: `type Ch chan int

// A method on a defined type over a channel, which is what makes a channel a
// named thing with behaviour rather than a bare pipe.
func (c Ch) Send(v int) { c <- v }

func (c Ch) Recv() int { return <-c }

// A second defined type over the SAME element: its methods are its own, which is
// why the type needs a C name of its own.
type Gate chan int

func (g Gate) Open() { g <- 1 }

func (g Gate) Wait() int { return <-g }

var c Ch

var g Gate

func worker() {
	v := c.Recv()
	c.Send(v * 2)
}

func opener() { g.Open() }

func main() {
	go worker()
	c.Send(21)
	println(c.Recv())

	go opener()
	println(g.Wait())
}
`,
		want: "42\n1\n",
	},
	{
		name: "a conversion to a defined array type, indexed where it stands",
		src: `type Row [3]int

type Grid [2]Row

type Pt struct {
	x int
	y int
}

type Pts [2]Pt

var r [3]int

var g [2]Row

var ps [2]Pt

func main() {
	r[0] = 10
	r[1] = 20
	r[2] = 30
	// The conversion names the same storage, so it is read straight through.
	println(Row(r)[0], Row(r)[2])
	println(len(Row(r)), cap(Row(r)))

	sum := 0
	for i, v := range Row(r) {
		sum += i * v
	}
	println(sum)

	g[1][2] = 7
	println(Grid(g)[1][2])

	ps[1].y = 9
	println(Pts(ps)[1].y)

	// It is a value, not a variable: what is converted is unaffected by anything
	// done with it, and the element read is the operand's own.
	q := Row(r)
	q[0] = 99
	println(r[0], q[0], Row(r)[0])
}
`,
		want: "10 30\n3 3\n80\n7\n9\n10 99 10\n",
	},
	{
		// A defined type over a STRUCT is a different type from the struct it was
		// defined over, so every way across between them wants a conversion. This is
		// the escape hatch the refusal leaves, so what it produces is worth pinning:
		// the same fields, and a COPY of them rather than another name for the same
		// storage.
		name: "a conversion between a defined struct type and its base",
		src: `type Pt struct {
	x int
	y int
}

type Loc Pt

func takePt(p Pt) int { return p.x*10 + p.y }

func takeLoc(l Loc) int { return l.x*100 + l.y }

func asPt(l Loc) Pt { return Pt(l) }

func main() {
	var l Loc
	l.x = 1
	l.y = 2
	var p Pt
	p.x = 3
	p.y = 4

	// Each direction across is a conversion, and carries the fields over.
	println(takePt(Pt(l)), takeLoc(Loc(p)))

	// It is a value, not another name for l's storage.
	var q Pt = Pt(l)
	q.y = 9
	println(q.x, q.y, l.x, l.y)

	l2 := Loc(q)
	println(takeLoc(l2))

	// A converted value compares as the type it was converted to.
	println(asPt(l) == Pt(l), Pt(l) == p)
}
`,
		want: "12 304\n1 9 1 2\n109\ntrue false\n",
	},
	{
		// Types named like identifiers the emitted RUNTIME declares: the goroutine
		// pool's `int slot`, the float formatter's width and precision, a string
		// header's and a slice helper's parameters. The backend cannot parse a
		// declarator named like a typedef, so each was a syntax error in generated C,
		// or "Unable to combine types". EmitC spells a type the runtime collides with
		// ogo_T_<name> -- and %T still says what the program wrote.
		name: "types named like the runtime's own identifiers",
		src: `type slot struct {
	id  int
	val int
}

type width uint8

type prec int32

type s []int

type val float32

var done chan slot

func worker(n int) {
	done <- slot{id: n, val: n * n}
}

func main() {
	go worker(7)
	r := <-done
	var w width = 12
	var p prec = -3
	xs := s{1, 2, 3}
	xs = append(xs[:2], r.val)
	v := val(2.5)
	msg := "collide"
	printf("%d %d %d %d %v %6.2f|%s %T %T %T\n", r.id, r.val, w, p, xs, v, msg[1:4], r, w, xs)
}
`,
		want: "7 49 12 -3 [1 2 49]   2.50|oll main.slot main.width main.s\n",
	},
	{
		name: "a name C has spoken for, in every position",
		src: `// Every identifier here is a C keyword or an unshadowable macro. They are
// ordinary OctoGo identifiers, so a program is entitled to them; the emitter
// renames them rather than handing C a declaration it cannot parse.
type Shape interface {
	static() int
	long() int
}

type Sq struct {
	double int
}

func (s *Sq) static() int { return s.double * 2 }

func (s *Sq) long() int { return s.double + 1 }

// A method reached through an interface is a VTABLE FIELD, so the name has to be
// renamed identically where the table is built and where it is read.
func do(char int) int { return char * 10 }

var register = 3

var printf = 4

func main() {
	q := Sq{double: 5}
	var sh Shape = &q
	println(sh.static(), sh.long())
	println(q.static(), do(register), printf)

	union := 7
	println(union)

	// An ordinary library FUNCTION is not renamed: a local of that name shadows
	// the header's declaration, which is all C needs.
	memcpy := 2
	var a [2]int
	a[0] = 9
	b := a
	println(memcpy, b[0])
}
`,
		want: "10 6\n10 30 4\n7\n2 9\n",
	},
	{
		name: "an array parameter is a copy",
		src: `func mutate(a [3]int) int {
	a[0] = 99
	return a[0]
}

func main() {
	var a [3]int
	a[0] = 1
	println(mutate(a), a[0])
}
`,
		want: "99 1\n",
	},
	{
		// A SUFFIX applied to a type assertion's result where it stands,
		// "e.(*P).foo()". The assertion is a value like any other, so what follows
		// applies to IT -- reading the suffix against the operand instead is what
		// answered "type any has no method foo".
		//
		// Every shape the suffix can take: a method call in an expression and as a
		// statement, a field, a nested field, an element, len, and each of the
		// writable ones as an assignment target. Plus the same chain over a
		// different dynamic type, which is what checks the assertion rather than
		// just the suffix.
		name: "a suffix applied to a type assertion",
		src: `type T interface{ foo() int }

type Inner struct{ n int }

type P struct {
	n  int
	in Inner
	xs []int
}

func (p *P) foo() int { return p.n }

func (p *P) bump() { p.n++ }

type Q int

func (q *Q) foo() int { return int(*q) * 100 }

func main() {
	q := P{3, Inner{7}, []int{1, 2, 3}}
	var e any = &q

	// A field, a nested field, an element, and len through the assertion.
	println(e.(*P).n, e.(*P).in.n, e.(*P).xs[1], len(e.(*P).xs))

	// Writing through one.
	e.(*P).n = 9
	e.(*P).in.n = 8
	e.(*P).xs[0] = 5
	println(q.n, q.in.n, q.xs[0])

	// A statement call, and an interface assertion's method.
	e.(*P).bump()
	println(q.n, e.(T).foo())

	// The same chain over a different dynamic type.
	z := Q(2)
	var e2 any = &z
	println(e2.(T).foo())
}
`,
		want: "3 7 2 3\n9 8 5\n10 10\n200\n",
	},
	{
		// Assigning an interface value to a variable of ANOTHER interface type --
		// widening, which Go allows when the target's method set is a subset of the
		// source's. The two words are the same pointer viewed through a different
		// table, so what is stored is the data unchanged beside the table for the
		// pair the value turned out to hold.
		//
		// Exercised in every position one can stand: a variable, an argument, a
		// result, a package variable, and widening to the empty interface then
		// narrowing back by assertion. The last pair is what checks the TABLE rather
		// than the test -- a different dynamic type through the same widening must
		// come back with its own.
		name: "assigning one interface to another",
		src: `type T interface{ foo() int }

type U interface{ bar() int }

type Z interface {
	T
	U
}

type X int

func (x *X) foo() int { return int(*x) }
func (x *X) bar() int { return int(*x) * 10 }

type Y int

func (y *Y) foo() int { return int(*y) + 100 }

func takeT(t T) int { return t.foo() }

func toAny(t T) any { return t }

var pkgX X

var global any

func main() {
	x := X(3)
	var z Z = &x

	// Widening: Z has both methods, so it may be used where T or U is wanted.
	var t T = z
	var u U = z
	println(t.foo(), u.bar())

	// As an argument, and as a result.
	println(takeT(z))
	a := toAny(z)
	b := a.(T)
	println(b.foo())

	// Widening to the empty interface, then narrowing back by assertion.
	var e any = z
	zz := e.(Z)
	println(zz.foo(), zz.bar())

	// A package variable may hold one whose data is a package variable.
	pkgX = 7
	var pz Z = &pkgX
	global = pz
	gt := global.(T)
	println(gt.foo())

	// A different dynamic type through the same widening.
	y := Y(1)
	var t2 T = &y
	var e2 any = t2
	t3 := e2.(T)
	println(t3.foo())
}
`,
		want: "3 30\n3\n3\n3 30\n7\n101\n",
	},
	{
		// An interface EMBEDDING others, "type Z interface { T; U }", which
		// contributes their methods to its own. Exercised where it is not merely a
		// rename: OVERLAPPING sets, where two embedded interfaces declare the same
		// method and it must become ONE vtable slot rather than two; TRANSITIVE
		// embedding; and a FORWARD reference, since declarations are collected in
		// source order and an interface may embed one written after it.
		//
		// Then the two things a method set is for: a case naming an embedded
		// interface, and an assertion to one.
		name: "an interface embedding others",
		src: `type T interface{ foo() int }

type U interface{ bar() int }

// Overlapping method sets: both embed T, so foo() appears twice and must become
// one slot.
type A interface {
	T
	baz() int
}

type B interface {
	T
	U
}

// Embedding one that is itself embedded, and a forward reference to a type
// declared LATER.
type C interface {
	B
	Late
}

type Late interface{ late() int }

type X int

func (x *X) foo() int  { return int(*x) }
func (x *X) bar() int  { return int(*x) * 10 }
func (x *X) baz() int  { return int(*x) * 100 }
func (x *X) late() int { return int(*x) * 1000 }

func main() {
	x := X(2)
	var a A = &x
	println(a.foo(), a.baz())
	var b B = &x
	println(b.foo(), b.bar())
	var c C = &x
	println(c.foo(), c.bar(), c.late())

	var e any = &x
	switch t := e.(type) {
	case C:
		println("C", t.late())
	default:
		println("none")
	}
	d := e.(B)
	println(d.foo(), d.bar())
}
`,
		want: "2 200\n2 20\n2 20 2000\nC 2000\n2 20\n",
	},
	{
		// A type ASSERTION to an interface, "v.(T)", in both forms. It asks the same
		// question a type switch case for T asks -- the method set -- of one type,
		// and is written without a star for the same reason: "*T" would be a pointer
		// TO the interface.
		//
		// The result is another interface VALUE rather than the pointer that went
		// in, so it is two words built from two: the data carries over, and the table
		// becomes the one for the asserted interface and whatever concrete type the
		// operand turned out to hold. The last line is what checks that pairing --
		// the same assertion over a different dynamic type must pick the other table.
		name: "a type assertion to an interface",
		src: `type T interface{ foo() int }

type U interface{ bar() int }

type Both interface {
	foo() int
	bar() int
}

type X int

func (x *X) foo() int { return int(*x) }
func (x *X) bar() int { return int(*x) * 10 }

type Y int

func (y *Y) foo() int { return int(*y) + 100 }

func main() {
	x := X(3)
	y := Y(4)
	var e any = &x
	var f any = &y

	// The one-value form, which holds.
	t := e.(T)
	println(t.foo())

	// Asserting to an interface with a LARGER method set, then using both of them.
	b := e.(Both)
	println(b.foo(), b.bar())

	// And narrowing that one further, which asserts against its own tables.
	t2 := b.(T)
	println(t2.foo())

	// The comma-ok form, both ways.
	u, ok := e.(U)
	println(ok, u.bar())
	u2, ok2 := f.(U)
	println(ok2)
	_ = u2

	// The same assertion over a different dynamic type picks the other table.
	t3, ok3 := f.(T)
	println(ok3, t3.foo())
}
`,
		want: "3\n3 30\n3\ntrue 30\nfalse\ntrue 104\n",
	},
	{
		// A type switch case naming an INTERFACE, "case T:", which matches on the
		// METHOD SET rather than on identity. The clause order is what decides
		// between two interfaces one type satisfies, so Both must precede T here and
		// the answers differ if it does not -- which is the property a wrong
		// lowering would lose.
		//
		// What makes it decidable is that the program is closed: the emitter knows
		// every type and method, so "implements T" is a list of table comparisons it
		// can write out. Exercised against a type implementing both interfaces, one
		// implementing a single one, one implementing neither, a nil operand, a
		// NON-empty operand interface, a concrete case beside interface ones, and
		// two interfaces in one case.
		name: "a type switch case naming an interface",
		src: `type T interface{ foo() }

type U interface{ bar() }

type Both interface {
	foo()
	bar()
}

type X int // foo + bar
type Y int // foo only
type Z int // neither

func (*X) foo() { println("X.foo") }
func (*X) bar() { println("X.bar") }
func (*Y) foo() { println("Y.foo") }
func (*Z) other() {}

func which(v any) {
	switch t := v.(type) {
	case Both:
		print("Both: ")
		t.foo()
	case T:
		print("T: ")
		t.foo()
	case U:
		println("U")
	case nil:
		println("nil")
	default:
		println("none")
	}
}

// A non-empty operand interface, with an interface case over it.
func narrow(s T) {
	switch s.(type) {
	case Both:
		println("narrow: Both")
	case T:
		println("narrow: T")
	default:
		println("narrow: none")
	}
}

// A concrete case beside an interface one, and several types in one case.
func mixed(v any) {
	switch v.(type) {
	case *Y:
		println("mixed: Y")
	case T, U:
		println("mixed: T or U")
	default:
		println("mixed: none")
	}
}

func main() {
	x := X(0)
	y := Y(0)
	z := Z(0)
	which(&x)
	which(&y)
	which(&z)
	var n any
	which(n)
	narrow(&x)
	narrow(&y)
	mixed(&x)
	mixed(&y)
	mixed(&z)
}
`,
		want: "Both: X.foo\nT: Y.foo\nnone\nnil\nnarrow: Both\nnarrow: T\nmixed: T or U\nmixed: Y\nmixed: none\n",
	},
	{
		// An interface written where a type is WANTED rather than declared with one
		// of its own -- "interface{ area() int }" as a parameter, and the empty
		// "interface{}" that "any" spells. Everything the interface machinery does is
		// keyed by a name, so giving the shape one is the whole of what it needed.
		//
		// The identities are the part worth running: "any" and "interface{}" are ONE
		// type, and so are two anonymous interfaces with the same method set, so a
		// value passes between them. That falls out of keying the minted name by the
		// method set rather than by where it was written -- and it is what would
		// break if two spellings each minted their own.
		name: "an interface type written where a type is wanted",
		src: `type Shape interface {
	area() int
}

type Sq int

type Circ int

func (s *Sq) area() int   { return int(*s) * int(*s) }
func (c *Circ) area() int { return 3 * int(*c) * int(*c) }

// An interface written where a type is wanted, rather than declared with one of
// its own: as a parameter, and as the empty one that "any" spells.
func measure(s interface{ area() int }) int { return s.area() }

func kind(e any) int {
	switch t := e.(type) {
	case *Sq:
		return t.area()
	case *Circ:
		return t.area()
	}
	return -1
}

func hold(e interface{}) any { return e }

func main() {
	q := Sq(4)
	c := Circ(2)
	println(measure(&q), measure(&c))
	println(kind(&q), kind(&c))

	// "any" and "interface{}" are one type, so a value passes between them.
	var a any = &q
	var b interface{} = hold(a)
	p := b.(*Sq)
	println(p.area())

	// Two anonymous interfaces of the same method set are one type too.
	var m1 interface{ area() int } = &q
	var m2 interface{ area() int } = m1
	println(m2.area())

	// A named interface is unaffected.
	var s Shape = &c
	println(s.area())
}
`,
		want: "16 12\n16 12\n16\n16\n12\n",
	},
	{
		// `len` and `cap` of an ARRAY reached through a chain: a ROW of a
		// multi-dimensional one, `len(m[0])`, a struct's array field, that field's
		// row, a row through a pointer, a row of a DEFINED array type, and a field
		// reached past an index. Every one of them is a compile-time constant, and
		// what makes one answer serve them all is that the chain walk reports the
		// extents still remaining -- one index into a [2][3]int leaves a [3]int.
		//
		// A SLICE reached the same way is not a constant, and is here to pin that it
		// still reads its header's length rather than an extent it does not have.
		name: "len and cap of an array reached through a chain",
		src: `type Row [3]int

type G struct {
	rows [2][3]int
	data []int
}

func main() {
	var m [2][3]int
	m[1][2] = 5
	println(len(m), len(m[0]), cap(m[1]), m[1][2])

	var z [2][3][4]int
	z[0][1][2] = 7
	println(len(z), len(z[0]), len(z[0][1]), z[0][1][2])

	var g G
	g.rows[1][0] = 9
	g.data = []int{1, 2, 3}
	println(len(g.rows), len(g.rows[0]), len(g.data), g.rows[1][0])

	p := &m
	println(len(p), len(p[0]), p[1][2])

	var n [2]Row
	n[0][1] = 4
	println(len(n), len(n[0]), n[0][1])

	gs := []G{{}, {}}
	gs[1].rows[0][2] = 6
	println(len(gs[0].rows), len(gs[0].rows[1]), gs[1].rows[0][2])
}
`,
		want: "2 3 3 5\n2 3 4 7\n2 3 3 9\n2 3 5\n2 3 4\n2 3 6\n",
	},
	{
		// SLICING an array whose element is an array. `[][2]int` was already a type a
		// literal could make, but the language's own idiom for a heapless slice -- a
		// package-scope backing array, sliced where it is used -- was refused for this
		// one element type, on a belief that had stopped being true: that a slice of
		// arrays has no element type C can name. It does, and by the same typedef the
		// literal has always been built over, `typedef int ogo_arr_2_int[2]`, so a
		// slice made by slicing and one made by a literal are one C type.
		//
		// Every base a slice expression takes is here, because they resolve the
		// element in four different places: a variable, a pointer to one, a struct
		// field, and a row reached through a chain. The struct field was the one that
		// did not refuse -- it named the header after the INNERMOST type, built an
		// ogo_slice_int over an int(*)[2], and flexcc only warned, so the build
		// succeeded and every later use of the result was refused for a reason that
		// named C rather than the program.
		name: "slicing an array whose element is an array",
		src: `type Row [2]int

type Grid struct {
	g [3][2]int
}

var m [4][2]int

var d [3][2][2]int

var rows [3]Row

var gr Grid

var pool [8][2]int

func total(rs [][2]int) int {
	n := 0
	for _, r := range rs {
		n += r[0] + r[1]
	}
	return n
}

func main() {
	for i := 0; i < 4; i++ {
		m[i][0] = i * 10
		m[i][1] = i
	}
	// The idiom: a package-scope backing array, sliced where it is used.
	xs := m[:]
	println(len(xs), cap(xs), xs[2][0], total(xs))

	// Bounded, and with a capacity bound.
	a := m[1:3]
	b := m[0:1:4]
	println(len(a), a[0][0], len(b), cap(b))

	// A slice is a view, not a copy: writing through it is seen in the backing.
	xs[3][1] = 99
	println(m[3][1])

	// A row of a 3-D array is itself a slice of arrays, which is where the old
	// advice -- slice a row instead -- ran out.
	d[1][0][0] = 5
	d[1][1][0] = 6
	r := d[1][:]
	println(len(r), r[0][0], r[1][0])

	// A defined array type as the element, and a struct field as the base.
	rows[2][0] = 7
	gr.g[1][1] = 8
	println(len(rows[:]), rows[:][2][0], len(gr.g[:]), gr.g[:][1][1])

	// Appending a row onto a slice over another backing array.
	ys := pool[:0]
	ys = append(ys, xs[1])
	ys = append(ys, r[0])
	println(len(ys), ys[0][0], ys[1][0])
}
`,
		want: "4 4 20 66\n2 10 1 4\n99\n2 5 6\n3 7 3 8\n2 10 5\n",
	},
	{
		// An ARRAY LITERAL in the two positions that hoist nothing to point at, an
		// append and a channel send. C has a value form for one -- the compound
		// literal `(Row){1, 2}` -- and a literal of a DEFINED array type has always
		// emitted exactly that. The unnamed spelling of the same value had no name to
		// write and was refused for want of one rather than for want of a form, so
		// `ch <- Row{1, 2}` compiled and `ch <- [2]int{1, 2}` did not.
		//
		// Both spellings are here, in both positions, because the pair is the whole
		// point: the same value written two ways must reach the same place.
		name: "an array literal in an append and a channel send",
		src: `type Row [2]int

var pool [4][2]int

var rpool [4]Row

var ch chan [2]int

var rch chan Row

func feed() {
	ch <- [2]int{1, 2}
	rch <- Row{3, 4}
}

func main() {
	xs := pool[:0]
	xs = append(xs, [2]int{10, 11})
	xs = append(xs, [2]int{20, 21})
	println(len(xs), cap(xs), xs[0][1], xs[1][0])

	rs := rpool[:0]
	rs = append(rs, Row{30, 31})
	println(len(rs), rs[0][1])

	go feed()
	a := <-ch
	b := <-rch
	println(a[0], a[1], b[0], b[1])
}
`,
		want: "2 4 11 20\n1 31\n1 2 3 4\n",
	},
	{
		// A composite literal whose ELEMENTS are slices -- a table of rows, which is
		// how a program states one without a heap. It rendered each element as a
		// compound literal, `(ogo_slice_int){r0, 3, 3}`, and the target's compiler
		// refuses one inside an ARRAY initializer while accepting it inside a STRUCT
		// initializer. So the shape did not compile at all, though the host compiler
		// took the same C.
		//
		// A slice expression, a conversion of one, and a slice-of-slices rather than
		// an array of them, since each reaches the element by its own route.
		name: "a composite literal whose elements are slices",
		src: `type L []int

var r0 [3]int

var r1 [2]int

var r2 [4]int

var table = [3][]int{r0[:], r1[:], r2[:]}

func widths(rows [][]int) int {
	n := 0
	for _, r := range rows {
		n = n*10 + len(r)
	}
	return n
}

func main() {
	r0[0], r1[0], r2[0] = 10, 20, 30
	println(len(table), widths(table[:]))

	local := [2][]int{r1[:], r2[:]}
	rows := [][]int{r2[:], r0[:]}
	println(len(local), local[0][0], len(rows), rows[0][0])

	// A conversion to a defined slice type renders what its operand renders, so it
	// needs the same spelling.
	named := [2]L{L(r0[:]), L(r2[:])}
	println(len(named), len(named[1]))
}
`,
		want: "3 324\n2 20 2 30\n2 4\n",
	},
	{
		// Indexing an element of an array whose element is a DEFINED slice type.
		// `named[0][0]` over a `[2]L` was refused where the unnamed `[2][]int`
		// spelling of the same thing indexed twice without trouble: the chain walker
		// classified an element type as a slice by the header's own C name and not
		// through a definition.
		//
		// Every OTHER way of reaching it worked -- len, a copy into a local, a range
		// -- which is what made the shape look supported. The write through the
		// double index is here because it is a different path from the read and lands
		// in the backing array either way.
		name: "indexing an element of an array of a defined slice type",
		src: `type L []int

type B struct {
	rows [2]L
}

var r0 [3]int

var r1 [4]int

var named [2]L

var b B

func total(rows []L) int {
	n := 0
	for _, r := range rows {
		n += len(r)
	}
	return n
}

func main() {
	r0[0], r0[1], r0[2] = 1, 2, 3
	r1[0], r1[3] = 40, 43

	named[0] = L(r0[:])
	named[1] = L(r1[:])
	println(named[0][2], named[1][3], len(named[0]), len(named[1]))

	named[0][1] = 22
	println(r0[1])

	b.rows[1] = L(r1[:])
	println(b.rows[1][0], total(named[:]))
}
`,
		want: "3 43 3 4\n22\n40 7\n",
	},
	{
		// A PARAMETER of multi-dimensional array type. The one-dimensional form has
		// always worked -- an array parameter is received as a pointer and copied
		// into a local, since a parameter of array type miscompiles on this target --
		// and the helper that recognised one returned false for any rank above 1, so
		// `func take(x [3][2]int)` was refused as an unsupported type.
		//
		// A rank above one has no element type C can write inline: `int (*)[2]` puts
		// the parameter's name in the middle of the declarator. The ROW's generated
		// typedef names it, the same one a `[][2]int` element is given.
		//
		// The last line pins that the parameter is a COPY, as Go copies it: the
		// pointer is how it travels, not what it means.
		name: "a parameter of multi-dimensional array type",
		src: `type R [2]int

func sum2(x [3][2]int) int {
	n := 0
	for i := 0; i < 3; i++ {
		n = n*10 + x[i][0] + x[i][1]
	}
	return n
}

func sumR(x [3]R) int { return x[0][0] + x[2][1] }

func deep(x [2][2][2]int) int { return x[1][1][1] }

func mutate(x [2][2]int) int {
	x[0][0] = 99
	return x[0][0]
}

var m [3][2]int

var rs [3]R

var d [2][2][2]int

var mm [2][2]int

func main() {
	m[0][0], m[0][1] = 1, 2
	m[1][0], m[1][1] = 3, 4
	m[2][0], m[2][1] = 5, 6
	rs[0][0], rs[2][1] = 7, 8
	d[1][1][1] = 9
	mm[0][0] = 1

	println(sum2(m), sumR(rs), deep(d))
	println(mutate(mm), mm[0][0])
}
`,
		want: "381 15 9\n99 1\n",
	},
	{
		// METHODS on a defined ARRAY type. An array carries no C type -- its extents
		// live in their own map and nowhere else -- so nothing said which type a
		// variable of one was, and therefore which methods it had: `g.set(0, 3)` was
		// read as a package qualification and reported as `unknown package "g"`. The
		// shape's name now travels with its extents.
		//
		// A value receiver of array type is received as a POINTER and copied, exactly
		// as an array parameter is, since a parameter of array type corrupts
		// unrelated code on this target. The clobber line is what pins that the copy
		// is real: Go's value receiver leaves the caller's array alone.
		name: "methods on a defined array type",
		src: `type Row [2]int

type Grid [2][2]int

func (r Row) sum() int { return r[0] + r[1] }

func (r Row) at(i int) int { return r[i] }

func (r Row) clobber() int {
	r[0] = 99
	return r[0]
}

func (r *Row) set(i, v int) { r[i] = v }

func (r *Row) scale(k int) {
	r[0] *= k
	r[1] *= k
}

func (g Grid) total() int {
	n := 0
	for i := 0; i < 2; i++ {
		n += g[i][0] + g[i][1]
	}
	return n
}

var pg Row

var gr Grid

func main() {
	// A package-level receiver, both forms.
	pg.set(0, 3)
	pg.set(1, 4)
	println(pg.sum(), pg.at(1))
	pg.scale(10)
	println(pg[0], pg[1], pg.sum())

	// A local one, and through a pointer to it.
	var v Row
	v.set(0, 5)
	p := &v
	p.set(1, 6)
	println(v.sum(), v.at(0), (&v).sum())

	// The VALUE receiver is a copy: writing to it leaves the caller's array alone.
	println(v.clobber(), v[0])

	// And a multi-dimensional defined array type.
	gr[0][0], gr[0][1] = 1, 2
	gr[1][0], gr[1][1] = 3, 4
	println(gr.total())
}
`,
		want: "7 4\n30 40 70\n11 5 11\n99 5\n10\n",
	},
	{
		// COPYING an array reached through a chain -- a struct field, a nested one,
		// an element of an array of arrays, a field then an index. `b := a` over a
		// whole array variable has always been the memcpy Go's copy is; every longer
		// route to an array fell through to the type inference instead, which types
		// no array operand, and reported "cannot infer a type" of a field whose type
		// the program had written down.
		//
		// A FIELD keeps the type's NAME, so the copy carries its method set -- the
		// field's own declaration still knows it. A route through an INDEX cannot: an
		// array of a defined array type is flattened to its extents, so `[2]Row` is a
		// [2][2]int by then. The copy is by shape either way, which is what Go's copy
		// is; only the method set differs.
		name: "copying an array reached through a chain",
		src: `type Row [2]int

type I struct {
	g [3]int
}

type H struct {
	f     Row
	rows  [2][2]int
	inner I
}

func (r Row) sum() int { return r[0] + r[1] }

var h H

var pool [2]Row

var m [2][3]int

func main() {
	h.f[0], h.f[1] = 3, 4
	h.rows[1][0] = 8
	h.inner.g[2] = 5
	pool[1][0] = 9
	m[0][2] = 6

	x := h.f
	x[0] = 99
	println(x[0], h.f[0], x.sum())

	y := h.inner.g
	z := pool[1]
	w := h.rows[1]
	v := m[0]
	println(y[2], z[0], w[0], v[2])

	// Each is a copy: writing to it leaves the source alone.
	z[0] = 1
	println(z[0], pool[1][0])
}
`,
		want: "99 3 103\n5 9 8 6\n1 9\n",
	},
	{
		// ASSIGNING an array reached through a chain -- the same routes the
		// declaration above copies from, on the right of an `=` rather than a `:=`.
		// The assignment knew two sources, an array variable and a dereferenced
		// pointer to one, and anything longer fell past both to the ordinary path,
		// which emitted `d = h.f;`. That is not C -- gcc says "assignment to
		// expression with array type" -- and it is exactly the wrong output the plain
		// `a = b` shape was already a memcpy to avoid. flexcc accepts it as an
		// extension and copies, so the BOARD was right and silent while the emitted C
		// was not C, which is why only a host build could see it.
		//
		// Both targets are here, a plain variable and a field, because they are two
		// paths that reach the same source resolution.
		name: "assigning an array reached through a chain",
		src: `type Row [2]int

type Inner struct {
	g [2]int
}

type Sprite struct {
	body  Row
	grid  [2][2]int
	inner Inner
}

var sheet [2]Sprite

var scratch Row

var blank Sprite

func main() {
	sheet[0].body[0] = 3
	sheet[0].body[1] = 4
	sheet[1].grid[1][0] = 8
	sheet[1].grid[1][1] = 9
	sheet[1].inner.g[0] = 5

	// A field, reached directly.
	scratch = blank.body
	println(scratch[0], scratch[1])

	// A field of an ELEMENT, a field then an INDEX, and a nested field.
	scratch = sheet[0].body
	println(scratch[0], scratch[1])

	var row [2]int
	row = sheet[1].grid[1]
	println(row[0], row[1])

	row = sheet[1].inner.g
	println(row[0], row[1])

	// The same sources into a FIELD target.
	blank.body = sheet[0].body
	blank.inner.g = sheet[1].grid[1]
	println(blank.body[0], blank.inner.g[1])

	// Each is a copy: writing to the destination leaves the source alone.
	scratch[0] = 99
	blank.body[1] = 77
	println(scratch[0], sheet[0].body[0], blank.body[1], sheet[0].body[1])
}
`,
		want: "0 0\n3 4\n8 9\n5 0\n3 9\n99 3 77 4\n",
	},
	{
		// A whole ARRAY written over through a target that is not a plain variable
		// or a whole field -- a ROW of an array of arrays, an element of a slice of
		// them, an array-typed field of an element, an element of an array-typed
		// field. Writing a row is how a table of rows is filled, and none of it
		// compiled: `m[1] = r` was refused outright ("a multi-dimensional array must
		// be indexed in every dimension" -- there was no lowering, and typing the
		// target as the ELEMENT would have written one int over a row), the chain
		// targets were "only simple and field assignment targets are supported yet",
		// and the slice element emitted `xs.ptr[i] = (ogo_arr_2_int){7, 8}`, which is
		// not C.
		//
		// All four are the memcpy `a = b` and `s.a = b` already were, reached through
		// a target those two shapes cannot name, so the lowering sits on the tail and
		// each site only says how big the array is. That the tail carries it is also
		// what refuses `m[1]++` and `m[1] += r`, which Go rejects: no operator applies
		// to an array.
		name: "writing a whole array through an index",
		src: `type Row [2]int

type Frame struct {
	head Row
	rows [2][2]int
}

var table [3][2]int

var frames [2]Frame

var back [3]Row

func mkRow() [2]int { return [2]int{5, 6} }

func main() {
	// A ROW of an array of arrays: one index into two dimensions.
	table[0] = [2]int{1, 2}
	table[2] = table[0]
	println(table[0][0], table[2][1])

	// The same target reached through a slice of arrays, and through a pointer to
	// the array. The pointer's source is a call, which writes through the target
	// rather than copying into it.
	xs := back[:]
	xs[1] = Row{7, 8}
	p := &table
	p[1] = mkRow()
	println(back[1][0], table[1][0], table[1][1])

	// An array-typed FIELD of an element, and an element of an array-typed field.
	frames[0].head = Row{3, 4}
	frames[1].rows[1] = [2]int{9, 10}
	println(frames[0].head[1], frames[1].rows[1][0])

	// Each is a copy: writing to the destination leaves the source alone.
	table[2][0] = 99
	println(table[2][0], table[0][0])
}
`,
		want: "1 2\n7 5 6\n4 9\n99 1\n",
	},
	{
		// A method on an array-typed FIELD. The same method on a struct-typed field
		// has always worked, in both statement and expression position, so this was
		// the array case alone: the chain walk reaches an array with no C value type,
		// and the dispatch keyed on that type being non-empty. The field's DEFINED
		// name travels with its extents now, which is what the method set hangs off.
		//
		// The last line is the one that matters: a value receiver is a COPY, so a
		// method writing to it leaves the field alone.
		name: "a method on an array-typed struct field",
		src: `type Row [2]int

type I struct {
	g Row
}

type H struct {
	f     Row
	inner I
}

func (r Row) sum() int { return r[0] + r[1] }

func (r Row) pair() (int, int) { return r[0], r[1] }

func (r Row) clobber() int {
	r[0] = 99
	return r[0]
}

func (r *Row) set(i, v int) { r[i] = v }

var h H

var hs [2]H

func main() {
	h.f[0], h.f[1] = 3, 4
	h.inner.g[0], h.inner.g[1] = 5, 6

	println(h.f.sum(), h.inner.g.sum())
	h.f.set(0, 10)
	h.inner.g.set(1, 20)
	println(h.f.sum(), h.inner.g.sum())

	// Through a pointer to the struct, and through an array of structs.
	p := &h
	hs[1].f[0] = 7
	println(p.f.sum(), hs[1].f.sum())

	// A multi-result method on such a field, which takes another path.
	a, b := h.f.pair()
	println(a, b)

	println(h.f.clobber(), h.f[0])
}
`,
		want: "7 11\n14 25\n14 7\n10 4\n99 10\n",
	},
	{
		// THE ADDRESS of an array reached through a chain, bound to a variable.
		// `p := &a` over a bare array variable worked, and handing `&h.f` to a
		// PARAMETER worked -- the parameter's type says what it is -- but a
		// DECLARATION has only the inference to go on, and it read a bare name only.
		//
		// The pointer ALIASES, which is the whole difference from the copy beside it
		// in these cases: writing through it is seen in the field, and a
		// pointer-receiver method through it writes there too.
		name: "the address of an array reached through a chain",
		src: `type Row [2]int

type I struct {
	g Row
}

type H struct {
	f     Row
	inner I
}

func (r *Row) set(i, v int) { r[i] = v }

func (r Row) sum() int { return r[0] + r[1] }

func take(p *Row) int { return p[0] }

var h H

var pool [2][2]int

var rows [2]Row

func main() {
	h.f[0], h.f[1] = 3, 4
	h.inner.g[0] = 5
	pool[1][0] = 6
	rows[1][0] = 7

	p := &h.f
	p[0] = 30
	println(p[0], h.f[0], len(p), p.sum())

	// A nested field, and an element of an array of arrays.
	q := &h.inner.g
	r := &pool[1]
	s := &rows[1]
	println(q[0], r[0], s[0])

	// It is the pointer Go would pass, so it goes where a *Row goes.
	println(take(p), take(&h.inner.g))

	p.set(1, 40)
	println(h.f[1], h.f.sum())
}
`,
		want: "30 30 2 34\n5 6 7\n30 5\n40 70\n",
	},
	{
		// A pointer to an ARRAY is the one pointer an index applies to: Go's `p[i]`
		// abbreviates `(*p)[i]`, and so do `len(p)`, `range p` and `p[lo:hi]`. It is
		// how an array is passed by reference without a slice header, which is what
		// the by-value refusals used to point at.
		//
		// C spells the type `int (*p)[3]`, the name in the middle of the declarator,
		// so the pointee takes a generated typedef and the name comes back out in
		// front of it. Every line here reads or writes the SAME array through the
		// pointer, so a missing dereference does not merely print the wrong number --
		// it indexes the array at the wrong rank, which is what the type without the
		// dereference surface did.
		name: "a pointer to an array",
		src: `type Row [3]int

var g [3]int

func fill(p *[3]int) {
	for i := range p {
		p[i] = i * 2
	}
}

func total(p *Row) int {
	n := 0
	for _, v := range p {
		n += v
	}
	return n
}

func global() *[3]int { return &g }

func main() {
	var a [3]int
	fill(&a)
	println(a[0], a[1], a[2], len(a))

	p := &a
	p[0] = 9
	p[1]++
	println(a[0], a[1], len(p), cap(p))

	// The pointer is a value: copying it aliases the same array.
	q := p
	q[2] = 5
	println(a[2])

	// Slicing through the pointer views the same storage.
	s := p[1:]
	s[0] = 8
	println(a[1], len(s), cap(s))

	// The dereference COPIES the array, as assigning one does.
	b := *p
	b[0] = 0
	println(a[0], b[0])

	var r Row
	r[0], r[1], r[2] = 1, 2, 3
	println(total(&r))

	g[1] = 7
	println(global()[1])
}
`,
		want: "0 2 4 3\n9 3 3 3\n5\n8 2 2\n9 0\n6\n7\n",
	},
	{
		// The DEREFERENCE written out, `(*p)`, carrying a suffix. Go's `p.x` and,
		// for a pointer to an array, `p[i]` abbreviate it, and most code writes the
		// short form -- but for a pointer to a SLICE or a STRING the long form is
		// the only one there is, `p[i]` being illegal on those, so without it those
		// two types have no element access at all.
		//
		// Every kind of pointee is exercised, since what the dereference reaches
		// decides how the suffix is emitted: a struct's field is a selector, a
		// slice's element goes through its header, an array's is direct, a string's
		// is a byte. A method call is the one suffix NOT emitted as a chain -- Go
		// defines `p.m()` as the same call, so it is emitted as that.
		name: "a dereference written out, carrying a suffix",
		src: `type Inner struct {
	v int
}

type P struct {
	x  int
	xs []int
	in Inner
}

func (p *P) get() int { return p.x }

func (p *P) bump() { p.x++ }

func main() {
	q := P{4, []int{1, 2, 3}, Inner{6}}
	p := &q
	println((*p).x, (*p).xs[1], (*p).in.v, len((*p).xs))
	(*p).x = 9
	(*p).x++
	(*p).x += 2
	(*p).xs[0] = 5
	(*p).in.v = 3
	println(q.x, q.xs[0], q.in.v)
	println((*p).get())
	(*p).bump()
	println(q.x, (*(p)).x)

	// A pointer to a SLICE: the written-out form is the only one, since an index
	// on the pointer itself is not an operation Go has.
	xs := []int{1, 5, 9}
	ps := &xs
	println((*ps)[1], len(*ps), cap(*ps))
	(*ps)[2] = 4
	(*ps)[2]++
	s := (*ps)[1:]
	println(xs[2], len(s), s[0])
	for i, v := range *ps {
		println(i, v)
	}

	// A pointer to an ARRAY reaches the same storage both ways.
	a := [3]int{1, 5, 9}
	pa := &a
	println((*pa)[1], len(*pa), pa[1])
	(*pa)[0] = 3
	b := *pa
	b[1] = 0
	println(a[0], a[1], b[1])
	for i, v := range *pa {
		println(i, v)
	}

	str := "hey"
	pstr := &str
	println((*pstr)[1], len(*pstr))
}
`,
		want: "4 2 6 3\n12 5 3\n12\n13 13\n5 3 3\n5 2 5\n0 1\n1 5\n2 5\n5 3 5\n3 5 0\n0 3\n1 5\n2 9\n101 3\n",
	},
	{
		// The pointer where it is not the base of the expression: a STRUCT FIELD of
		// one, which the dereference has to reach part-way along a chain rather than
		// at its start. Plus the element types whose C declarator differs -- a
		// multi-dimensional pointee, a byte one and a string one.
		name: "a pointer to an array as a struct field",
		src: `type Grid struct {
	cells *[2][3]int
	tag   int
}

func mark(g *Grid) {
	g.cells[1][2] = 6
}

func main() {
	var m [2][3]int
	pm := &m
	pm[0][1] = 4
	println(pm[0][1], len(pm))

	g := Grid{cells: &m, tag: 1}
	mark(&g)
	println(m[1][2], g.cells[0][1], g.tag)

	var bs [4]uint8
	pb := &bs
	pb[3] = 200
	println(bs[3], len(pb))

	var one [1]string
	po := &one
	po[0] = "hi"
	println(po[0], len(po))
}
`,
		want: "4 2\n6 4 1\n200 4\nhi 1\n",
	},
	{
		name: "assigning one array to another copies it",
		src: `func main() {
	var a [3]int
	var b [3]int
	b[0] = 5
	a = b
	b[0] = 9
	println(a[0], b[0])
}
`,
		want: "5 9\n",
	},
	{
		name: "an array literal as a value",
		src: `type Row [3]int

var g [3]int

// An array parameter is a copy, so what the callee writes stays there.
func take(a [3]int) int {
	a[0] = 99
	return a[1]
}

func main() {
	var a [3]int
	a = [3]int{1, 2, 3}
	println(a[0], a[1], a[2])

	println(take([3]int{4, 5, 6}), take(a), a[0])

	// Copies, so a later write to the source does not reach the target.
	var b [3]int
	b[0] = 7
	a = b
	b[0] = 8
	println(a[0], b[0])

	g = a
	a[0] = 0
	println(g[0], a[0])

	var r Row
	r = Row{7, 8, 9}
	println(r[1])

	var m [2][2]int
	var n [2][2]int
	n[1][1] = 4
	m = n
	n[1][1] = 6
	println(m[1][1], n[1][1])
}
`,
		want: "1 2 3\n5 2 1\n7 8\n7 0\n8\n4 6\n",
	},
	{
		name: "an array field written over",
		src: `type S struct {
	a [3]int
	n int
}

func main() {
	var s S
	s.a = [3]int{1, 2, 3}
	println(s.a[0], s.a[2])

	var b [3]int
	b[1] = 4
	s.a = b
	b[1] = 9
	println(s.a[1], b[1])

	// The whole struct, which carries the array with it.
	var t S
	t = s
	s.a[0] = 77
	println(t.a[0], s.a[0])
}
`,
		want: "1 3\n4 9\n0 77\n",
	},
	{
		// A literal of a DEFINED array type, in the positions that declare no variable
		// to hold it -- a channel send and an append. It reads exactly like a struct
		// literal, a name and a brace, and went through the struct walk, which knows
		// nothing about indexes or rows. So `ch <- Row{1: 5}` sent ZEROS and
		// `append(xs, Row{2: 7})` appended them, silently and with a working binary,
		// while `r := Row{1: 5}` was right all along -- the declaration form goes to
		// the array walk, and the positions that hoist nothing to point at are exactly
		// the ones that did not. A defined MULTI-dimensional type was refused outright
		// there, its rows being "a type-elided composite literal element".
		name: "a defined array type's literal where nothing declares it",
		src: `type Row [3]int

type Grid [2][2]int

var ch chan Row

var back [4]Row

func send() { ch <- Row{1: 5} }

func main() {
	// An INDEXED literal of a defined array type, where nothing declares a variable
	// to hold it: a channel send and an append.
	go send()
	r := <-ch
	println(r[0], r[1], r[2])

	xs := back[:0]
	xs = append(xs, Row{2: 7})
	xs = append(xs, Row{1, 2, 3})
	println(xs[0][1], xs[0][2], xs[1][0])

	// A literal of a defined MULTI-dimensional array type, whose rows nest.
	g := Grid{{1, 2}, {3, 4}}
	println(g[0][1], g[1][0])
}
`,
		want: "0 5 0\n0 7 1\n2 3\n",
	},
	{
		// An aggregate VARIABLE as a composite literal's element -- an array where the
		// position takes one, and a struct that HOLDS an array. Building a table from
		// named rows did not compile in any spelling: an array element was "an element
		// of a [2]int literal must itself be a literal", a slice literal's and a struct
		// field's emitted an initializer C rejects, and a struct holding an array was
		// refused outright as an ABI boundary it is not.
		//
		// C copies no array in an initializer, and the target's C compiler copies no
		// array-holding struct by assignment at all, so each such element is zeroed at
		// its position and copied in after the declaration -- the same memcpy every
		// other copy of one takes. At FILE scope there is no "afterwards" in C, so the
		// copies become steps of the package initializer, ordered against the variables
		// they read; the table declared above its rows is what pins that.
		//
		// The last line checks it is a copy. A lowering that aliased instead would pass
		// every other line here.
		name: "an aggregate variable as a literal's element",
		src: `type Row [2]int

type Buf struct {
	xs [2]int
	n  int
}

type Wrap struct {
	b Buf
}

var r0 = [2]int{1, 2}

var r1 = Row{3, 4}

var buf = Buf{[2]int{5, 6}, 7}

var pool = [2][2]int{{8, 9}, {10, 11}}

// A table built at package scope from rows declared BELOW it: the copies become
// steps of the package initializer and are ordered against what they read.
var table = [2][2]int{later, r0}

var later = [2]int{12, 13}

func main() {
	println(table[0][0], table[1][1])

	// An array VALUE as an element of an array literal, a slice literal, and a
	// struct literal, positional and keyed, mixed with literals written in place.
	t := [3][2]int{r0, {20, 21}, pool[1]}
	println(t[0][0], t[1][1], t[2][0])

	xs := [][2]int{r1, r0}
	println(xs[0][0], xs[1][1])

	b := Buf{r0, 9}
	c := Buf{n: 8, xs: pool[0]}
	println(b.xs[1], b.n, c.xs[0], c.n)

	// A struct that HOLDS an array, as an element: the target's C compiler cannot
	// copy one by assignment, so this is the memcpy every copy of one takes.
	ws := []Buf{buf}
	w := Wrap{buf}
	println(ws[0].xs[0], ws[0].n, w.b.xs[1])

	// Each is a copy: writing to the literal's storage leaves the source alone.
	t[0][0] = 99
	println(t[0][0], r0[0])
}
`,
		want: "12 2\n1 21 10\n3 2\n2 9 8 8\n5 7 6\n99 1\n",
	},
	{
		// A conversion to an ARRAY type, as a VALUE. It changes nothing about the
		// value -- the typedef stands for the same storage, and Go admits one between
		// array types only where the underlying types are identical -- so it is an
		// array wherever one may stand. It was not one anywhere: `c = Col(r)` fell
		// past every copy path and emitted `c = r;`, which is not C, and the return,
		// the comparison and a literal's element each refused it for want of an array
		// they were looking straight at.
		//
		// The DECLARATION form always worked, `d := Col(r)`, because the chain walk
		// has seen through such a conversion since it gained arrayConvChain -- so the
		// one shape a reader reaches for first was the one that was fine.
		//
		// The unnamed spelling is here because the grammar admits it only
		// parenthesised, and it is a different Factor the unwrap has to know.
		name: "a conversion to an array type as a value",
		src: `type Row [2]int

type Col [2]int

type Grid [2][2]int

type H struct {
	c Col
}

var r = Row{1, 2}

var g = [2][2]int{{3, 4}, {5, 6}}

var c Col

var h H

func take(x Col) int { return x[0] + x[1] }

func mkCol() Col { return Col(r) }

func main() {
	// A conversion between array types changes nothing about the value, so it IS an
	// array wherever one may stand: assigned, into a field, returned, compared, an
	// element of a literal, and one value of a multiple assignment.
	c = Col(r)
	h.c = Col(r)
	println(c[0], h.c[1])

	d := mkCol()
	println(d[0], d[1], take(Col(r)))

	println(Col(r) == c, Col(r) == Col{9, 9})

	t := [1]Col{Col(r)}
	println(t[0][0])

	var n int
	n, c = 7, Col(r)
	println(n, c[1])

	// The unnamed spelling of the same conversion, which the grammar admits only
	// parenthesised, and a multi-dimensional one.
	var u [2]int
	u = ([2]int)(r)
	var gr Grid
	gr = Grid(g)
	println(u[0], gr[1][1])
}
`,
		want: "1 2\n1 2 3\ntrue false\n1\n7 2\n1 6\n",
	},
	{
		// A CALL returning an array, as a composite literal's element. It is not a
		// value to copy FROM -- the result travels through an out parameter -- so it
		// is not the copy the other deferred elements are: the element IS the storage
		// the callee fills, and the call writes through it, which is what that ABI is
		// for. A method's result is the same call one step along.
		//
		// At PACKAGE scope this is still refused, and not for a reason that lives
		// here: no package variable can be initialized from an array-returning call
		// at all, `var d = mk()` included.
		name: "a call's array result as a literal's element",
		src: `type Row [2]int

type Buf struct {
	xs [2]int
	n  int
}

type T struct{ n int }

func (t T) row() [2]int { return [2]int{t.n, t.n + 1} }

func mkRow() Row { return Row{7, 8} }

func mk(k int) [2]int { return [2]int{k, k * 2} }

var t = T{5}

func main() {
	// A call's array RESULT as a literal's element: the callee fills storage the
	// caller owns, and the element IS that storage, so the call writes through it.
	d := []Row{mkRow(), mkRow()}
	e2 := [2][2]int{mk(3), {1, 2}}
	b := Buf{mk(4), 9}
	m := [][2]int{t.row()}
	println(d[0][0], d[1][1])
	println(e2[0][0], e2[0][1], e2[1][1])
	println(b.xs[0], b.xs[1], b.n)
	println(m[0][0], m[0][1])
}
`,
		want: "7 8\n3 6 2\n4 8 9\n5 6\n",
	},
	{
		// A package ARRAY variable whose initializer is not a LITERAL -- a call's
		// result, another array, a field, a method's result, a dereferenced pointer.
		// Only a literal was taken: `var d = mk()` was "cannot infer a type for the
		// package variable" (an array has no assignable C value type, so inferCType
		// answers no for every one of these) and the form with the type written was
		// "a package array initializer must be an array literal". A LOCAL takes all
		// of them.
		//
		// C admits neither a call nor an array copy in a static initializer, so the
		// storage stays a file-scope table -- zeroed, which is the right starting
		// value -- and only the FILL moves, to a step of the package initializer
		// ordered against what it reads. That is what lets `early` be declared above
		// the `src` it copies.
		name: "a package array variable filled at init",
		src: `type Row [2]int

type H struct {
	f [2]int
}

type T struct{ n int }

func (t T) row() [2]int { return [2]int{t.n, t.n + 1} }

func mk() [2]int { return [2]int{7, 8} }

func mkGrid() [2][2]int { return [2][2]int{{1, 2}, {3, 4}} }

var h = H{[2]int{5, 6}}

var t = T{9}

// A package ARRAY variable filled from every source a local takes: a call, another
// array, a field, a method's result, a dereferenced pointer -- with and without the
// type written, and named or not.
var fromCall = mk()

var typedCall [2]int = mk()

var namedCall Row = mk()

var fromMethod = t.row()

var fromField = h.f

var grid = mkGrid()

var p = &src

var fromDeref = *p

// Declared ABOVE what it reads: a package's variables are initialized in dependency
// order, not source order, and these copies are steps of that same ordering.
var early = src

var src = [2]int{3, 4}

func main() {
	println(fromCall[0], typedCall[1], namedCall[0])
	println(fromMethod[0], fromMethod[1], fromField[1])
	println(grid[0][0], grid[1][1])
	println(fromDeref[0], early[1], src[0])
}
`,
		want: "7 8 7\n9 10 6\n1 4\n3 4 3\n",
	},
	{
		// A channel variable DECLARED with its type and initialized from another
		// names the same channel -- which is what a copy of a channel value means in
		// Go, and what `c := ch` and `c = ch` have always done here. The typed
		// declaration wrote the alias and then gave the variable a private cell one
		// line later, so the receive on it waited on a channel nobody could send to
		// and the program HUNG. A hang is what makes this worth a case of its own: it
		// builds, it runs, and it says nothing.
		//
		// A channel FIELD a declaration's literal fills is the same bug one level
		// down, and the nested literal is it two levels down. What still mints a cell
		// is a declaration that fills nothing -- `var w W`, `W{}`, `W{In{}}` -- which
		// is where this language's channel-is-storage rule lives.
		name: "a channel declared from another names the same channel",
		src: `type Ch chan int

type In struct {
	cmd chan int
}

type W struct {
	cmd chan int
	n   int
}

type Deep struct {
	in In
}

var a Ch

var b chan int

var c chan int

var done chan int

func sendA() { a <- 1 }

func sendB() { b <- 2 }

func sendC() { c <- 3 }

func recvOn(w W) {
	v := <-w.cmd
	done <- v
}

func main() {
	// A channel variable DECLARED with a type and initialized from another names
	// the same channel, as Go's copy of a channel value does. It used to be given a
	// private cell one line after the alias was written, and the receive on it
	// never returned.
	var x chan int = a
	go sendA()
	println(<-x)

	// The two spellings that always aliased, beside it.
	y := b
	go sendB()
	println(<-y)

	var z chan int
	z = c
	go sendC()
	println(<-z)

	// A channel FIELD the declaration's literal fills, positionally and by key, and
	// one filled through a nested literal.
	var w W = W{b, 4}
	var v W = W{cmd: c, n: 5}
	var d Deep = Deep{In{a}}
	println(w.n, v.n)

	go sendB()
	go recvOn(w)
	println(<-done)

	go sendC()
	println(<-v.cmd)

	go sendA()
	println(<-d.in.cmd)
}
`,
		want: "1\n2\n3\n4 5\n2\n3\n1\n",
	},
	{
		// The channel a SEND names, two fields deep. The send's model carried one
		// field and looked its name up on the HEAD's type, so `p.in.cmd <- v` was read
		// as `p.cmd`: refused outright where the outer struct had no such field, and
		// checked against the wrong element type where it had one. Both callers of the
		// helper dropped the flag it returned saying there had not been exactly one
		// field, which is what let the wrong answer through.
		//
		// The flat and indexed spellings are here beside it because they always
		// worked, and a change to how a chain is read is exactly what would break
		// them. The select clause is the second production that admits a send.
		name: "a send on a channel two fields deep",
		src: `type In struct {
	cmd chan int
}

type Ports struct {
	in  In
	tx  chan int
	tag int
}

var p Ports

var ws [2]Ports

var done chan int

// The channel a send names may be two fields deep, one field deep, or reached
// through an index. All three are the same channel to the program and were three
// different answers to the checker: it looked the LAST name up on the HEAD's type,
// so ` + "`" + `p.in.cmd` + "`" + ` was read as ` + "`" + `p.cmd` + "`" + `.
func deep() { p.in.cmd <- 1 }

func flat() { p.tx <- 2 }

func elem() { ws[1].tx <- 3 }

func viaSelect() {
	select {
	case p.in.cmd <- 4:
	case v := <-done:
		println(v)
	}
}

func main() {
	go deep()
	println(<-p.in.cmd)

	go flat()
	println(<-p.tx)

	go elem()
	println(<-ws[1].tx)

	// A send written in a select clause takes the same chain.
	go viaSelect()
	println(<-p.in.cmd)
}
`,
		want: "1\n2\n3\n4\n",
	},
	{
		// A deferred method whose receiver is an ARRAY. Go evaluates a deferred
		// call's receiver where the DEFER stands, so a value receiver sees what the
		// array held then -- and it was not captured at all: an array variable has no
		// C type, and the capture asked varType, which answers nothing for one, so it
		// was skipped and the receiver read at the RETURN. `defer g.show()` printed
		// what g held at the end of the function. It built, it ran, and it printed a
		// plausible wrong answer.
		//
		// The slot holds a COPY, which is what the capture is; C assigns no array, so
		// it is a memcpy, and the slot zeroes with braces because `Row r = 0` is not
		// an initializer. A POINTER receiver captures the ADDRESS and so does see the
		// later writes, which is the same rule read the other way and is why the
		// third defer here answers 91 rather than 2.
		name: "a deferred method on an array receiver",
		src: `type Row [3]int

type H struct {
	r Row
}

func (r Row) Show() { println(r[0], r[1], r[2]) }

func (r *Row) Bump() { r[0]++ }

var g = Row{1, 2, 3}

var h = H{Row{4, 5, 6}}

// A deferred method captures its receiver where the DEFER stands, so a value
// receiver sees what the array held then. A POINTER receiver captures the address
// and so sees the later writes, which is the same rule read the other way.
func run() {
	defer g.Show()
	defer h.r.Show()
	defer g.Bump()
	g = Row{90, 91, 92}
	h.r = Row{93, 94, 95}
}

func main() {
	run()
	println(g[0], h.r[0])
}
`,
		want: "4 5 6\n1 2 3\n91 93\n",
	},
	{
		// A method whose receiver is an ELEMENT of an array of a defined array type,
		// `pool[1].sum()`. An array of one is resolved to its extents when the
		// declaration is read -- a `[2]Row` is a [2][2]int by then -- so the element's
		// NAME, which is the only thing carrying its method set, was gone before any
		// walk reached the element. The same method on a local, a field or a struct
		// element all worked, which is what made this look like a corner rather than
		// a dropped fact.
		//
		// Two indexes in is here because it is what tells a name from a shape: the
		// elements of a `[2][2]Row` are `[2]Row`s, and only the SECOND index reaches a
		// Row.
		//
		// The DEFERRED calls are the point of the last block. A deferred method
		// captures its receiver where the defer stands, and an array receiver was not
		// captured at all -- read at the RETURN instead, printing what the array held
		// then. That was wrong for a package array and for an array field before this
		// spelling existed, so enabling the element would have made a third wrong
		// answer out of one lowering.
		name: "a method on an array element",
		src: `type Row [2]int

type H struct {
	rows [2]Row
	r    Row
}

func (r Row) Sum() int { return r[0] + r[1] }

func (r Row) Add(n int) int { return r[0] + n }

func (r *Row) Set(i, v int) { r[i] = v }

func (r Row) Show() { println(r[0], r[1]) }

var pool = [2]Row{{1, 2}, {3, 4}}

var cube = [2][2]Row{{{1, 2}, {3, 4}}, {{5, 6}, {7, 8}}}

var h = H{[2]Row{{5, 6}, {7, 8}}, Row{9, 10}}

var g = Row{1, 2}

func take(r Row) int { return r.Sum() }

// A deferred method captures its receiver where the defer STANDS, so a value
// receiver sees what the array held then and not what it holds at the return.
func deferred() {
	defer g.Show()
	defer h.r.Show()
	defer pool[0].Show()
	g = Row{90, 90}
	h.r = Row{91, 91}
	pool[0] = Row{92, 92}
}

func main() {
	// A method on an ELEMENT of an array of a defined array type, and on one two
	// indexes in -- which is what tells the element's name from its extents.
	println(pool[1].Sum(), pool[1].Add(10), cube[1][0].Sum())

	// The same through a slice of them, with a pointer receiver, and reached through
	// a field.
	xs := pool[:]
	xs[0].Set(1, 20)
	println(xs[0][1], h.rows[1].Sum())

	// A declaration typed from such a call, on the array itself and on an element.
	a := g.Sum()
	b := pool[1].Sum()

	// A COPY of an element keeps the type, and so does a range value over either
	// container.
	r := pool[1]
	t := r.Sum()
	for _, v := range pool {
		t += v.Sum()
	}
	for _, v := range xs {
		t += v.Sum()
	}
	println(a, b, t, take(pool[1]))

	deferred()
	println(g[0], h.r[0], pool[0][0])
}
`,
		want: "7 13 11\n20 15\n3 7 63 7\n1 20\n9 10\n1 2\n90 91 92\n",
	},
	{
		// An ARRAY receiver launched on a cog. `go ws[i].run()` for a struct element
		// was enabled deliberately -- one cog per element is the worker-pool shape --
		// and the array spellings were all "unsupported receiver in a go statement",
		// including `go g.run()` on the array itself. The lookups asked varType and
		// isUserType, neither of which answers for an array.
		//
		// A value receiver crosses as a COPY, which is what a goroutine's receiver
		// is, and the copy is a memcpy: C assigns no array into the cog's argument
		// slot. A POINTER receiver crosses as the address and writes the array the
		// spawner named, which the last block checks.
		name: "an array receiver on a cog",
		src: `type Row [2]int

type H struct {
	r Row
}

var done chan int

func (r Row) Send() { done <- r[0] + r[1] }

func (r *Row) Bump() {
	r[0]++
	done <- r[0]
}

var pool = [2]Row{{1, 2}, {3, 4}}

var g = Row{5, 6}

var h = H{Row{7, 8}}

func main() {
	// An ARRAY receiver launched on a cog: an element of an array of a defined array
	// type, the array itself, and one reached through a field. A value receiver
	// crosses as a COPY, which is what a goroutine's receiver is.
	go pool[1].Send()
	println(<-done)

	go g.Send()
	println(<-done)

	go h.r.Send()
	println(<-done)

	// A POINTER receiver crosses as the address, so the cog writes the array the
	// spawner named.
	go g.Bump()
	println(<-done, g[0])
}
`,
		want: "7\n11\n15\n6 6\n",
	},
	{
		// A MULTI-RESULT method whose receiver is reached through an INDEX,
		// `a, b := ps[1].two()`. The call shape a destructuring assignment recognises
		// was a run of SELECTORS -- which admits `m.st.pop()` and not one element in
		// -- so this was "multiple assignment requires a single function call on the
		// right-hand side", of a call. It had nothing to do with arrays: a plain
		// STRUCT element was refused the same way.
		//
		// Two rules met here. The chain walk refuses a multi-result call because such
		// a call is not a value and cannot CONTINUE a chain -- true, except as the
		// LAST step, which is exactly where a destructure wants one; its value is the
		// result struct. And the call shape is widened where the destructure asks for
		// it rather than in directCall, whose every other caller wants the narrow one.
		name: "a multi-result method on an element",
		src: `type Row [2]int

type P struct {
	x int
	y int
}

type H struct {
	ps [2]P
}

func (p P) Two() (int, int) { return p.x, p.y }

func (p P) Add(n int) (int, int) { return p.x + n, p.y - n }

func (p *P) Swap() (int, int) {
	p.x, p.y = p.y, p.x
	return p.x, p.y
}

func (r Row) Both() (int, int) { return r[0], r[1] }

var ps = [3]P{{1, 2}, {3, 4}, {5, 6}}

var h = H{[2]P{{7, 8}, {9, 10}}}

var pool = [2]Row{{11, 12}, {13, 14}}

func main() {
	// A multi-result method whose receiver is reached through an INDEX. The call
	// shape a destructuring assignment takes was a run of SELECTORS, so an element
	// of any kind -- a plain struct one included -- was "multiple assignment
	// requires a single function call on the right-hand side", of a call.
	a, b := ps[1].Two()
	println(a, b)

	// Through a slice of them, and with a field on the way to the index.
	xs := ps[:]
	c, d := xs[2].Two()
	e2, f := h.ps[1].Two()
	println(c, d, e2, f)

	// With arguments, with a POINTER receiver -- which writes the element the
	// program named -- and on an array element.
	g2, i := ps[0].Add(5)
	j, k := ps[2].Swap()
	l, m := pool[1].Both()
	println(g2, i, j, k, ps[2].x, l, m)

	// Assigned rather than declared, which is a different path to the same call.
	var n, o int
	n, o = ps[1].Two()
	println(n, o)
}
`,
		want: "3 4\n5 6 9 10\n6 -3 6 5 6 13 14\n3 4\n",
	},
	{
		// A method whose RECEIVER is an array and whose RESULT is one -- a type
		// returning its own type, which is how such a method is usually written.
		// `d := g.doubled()` was "cannot infer a type for the declaration" and the
		// assigned form emitted C the host compiler rejects: an array result travels
		// through an out parameter, and the lookup deciding whether the call IS one
		// asked varType, which answers nothing for an array. The same method on a
		// STRUCT receiver, and a plain function with an array result, both worked.
		//
		// Still refused on an ELEMENT receiver, `pool[1].doubled()`: that path takes
		// a two-step suffix and the chain is three.
		name: "a method returning an array on an array receiver",
		src: `type Row [2]int

type Grid [2][2]int

func (r Row) Doubled() Row { return Row{r[0] * 2, r[1] * 2} }

func (r *Row) Swapped() Row { return Row{r[1], r[0]} }

func (g Grid) Flat() Row { return Row{g[0][0], g[1][1]} }

var g = Row{3, 4}

var grid = Grid{{1, 2}, {3, 4}}

func twice(r Row) Row { return r.Doubled() }

func main() {
	// A method whose RECEIVER is an array and whose RESULT is one -- a type
	// returning its own type, which is how such a method is usually written. The
	// call was not recognised at all: an array variable has no C type, and the
	// lookup asked for one.
	d := g.Doubled()
	println(d[0], d[1])

	// Assigned rather than declared, returned from a function, on a pointer
	// receiver, and on a multi-dimensional array.
	var e2 Row
	e2 = g.Doubled()
	f := twice(g)
	s := g.Swapped()
	fl := grid.Flat()
	println(e2[1], f[0], s[0], s[1], fl[0], fl[1])
}
`,
		want: "6 8\n8 6 4 3 1 4\n",
	},
	{
		name: "an array literal returned",
		src: `// The literal binds to a temporary of this frame, and the copy into the
// caller's storage IS the return, so the frame outliving it is not in question.
func mk() [3]int { return [3]int{1, 2, 3} }

func main() {
	r := mk()
	println(r[0], r[1], r[2])
}
`,
		want: "1 2 3\n",
	},
	{
		// An array RETURNED through a chain. The return knew two sources, an array
		// variable and an array literal, so returning a row of a table, a field, a
		// nested field or a dereferenced pointer failed with "an array result must be
		// returned as a variable or an array literal" -- of a value the program had
		// written the type of. Returning a row is how a table is read.
		//
		// The local case belongs here for the reason the returned literal does: the
		// memcpy into the caller's storage IS the return, so a source in this frame
		// does not outlive it.
		name: "returning an array reached through a chain",
		src: `type Row [2]int

type Inner struct {
	g [2]int
}

type Cal struct {
	head  Row
	rows  [2][2]int
	inner Inner
}

var cal = Cal{Row{1, 2}, [2][2]int{{3, 4}, {5, 6}}, Inner{[2]int{7, 8}}}

func row(i int) [2]int { return cal.rows[i] }

func head() [2]int { return cal.head }

func nested() [2]int { return cal.inner.g }

func through(p *[2]int) [2]int { return *p }

func local() [2]int {
	c := Cal{Row{9, 10}, [2][2]int{{0, 0}, {0, 0}}, Inner{[2]int{0, 0}}}
	return c.head
}

func main() {
	a := row(1)
	b := head()
	c := nested()
	d := through(&cal.rows[0])
	e := local()
	println(a[0], a[1])
	println(b[0], c[1], d[0], e[1])

	// The result is a copy: writing to it leaves the table alone.
	a[0] = 99
	println(a[0], cal.rows[1][0])
}
`,
		want: "5 6\n1 8 3 10\n99 5\n",
	},
	{
		name: "parentheses where the parser needs them",
		src: `type Row [3]int

type Nums []int

type P struct {
	x int
	y int
}

func dbl(n int) int { return n * 2 }

var q Row

func main() {
	// A parenthesised expression carrying a suffix. Ordinary Go, and rejected
	// here until the Factor rule's parenthesised alternative gained one.
	var a [3]int
	a[1] = 5
	var s P
	s.y = 4
	println((a)[1], (s).y, (dbl)(21))

	xs := []int{7, 8, 9}
	println((xs)[2])

	// A literal of a bracketed type, read where it stands. The literal binds to a
	// temporary and the steps read that -- an array has no C value to index.
	println([]int{1, 2, 3}[1], [3]int{4, 5, 6}[2])
	println([2]P{{1, 2}, {3, 4}}[1].y)

	v := []int{10, 20, 30}[1]
	w := [3]int{40, 50, 60}[2]
	println(v, w)

	// A conversion to an unnamed composite type, which the grammar can only spell
	// parenthesised. Between a defined type and what it is defined over nothing
	// about the value changes, so the operand is the answer.
	q[0] = 11
	q[2] = 13
	var b [3]int = ([3]int)(q)
	println(b[0], ([3]int)(q)[2])

	var ns Nums = []int{1, 2, 3}
	ys := ([]int)(ns)
	println(len(ys), ys[1])

	// Every paren layer peels, not just one, and two hoisted literals in one
	// expression each get their own temporary.
	println(((a))[1], (((s))).y)
	println([]int{1, 2, 3}[1] + []int{4, 5}[1])

	// A literal indexed inside a loop is bound each time round.
	for i := 0; i < 3; i++ {
		println([]int{7, 8, 9}[i])
	}

	var z [2]int
	println(z == [2]int{0, 0}, z == [2]int{1, 1})
}
`,
		want: "5 4 42\n9\n2 6\n4\n20 60\n11 13\n3 2\n" +
			"5 4\n7\n7\n8\n9\ntrue false\n",
	},
	{
		name: "a for header with two names",
		src: `func main() {
	for i, j := 0, 9; i < j; i, j = i+1, j-1 {
		println(i, j)
	}

	// A multiple assignment cannot be C's third clause, so the post statements go
	// at the end of the body behind a label -- and continue must jump to that label
	// rather than skip them, or the loop never ends.
	n := 0
	for i, j := 0, 6; i < j; i, j = i+1, j-1 {
		if i == 1 {
			continue
		}
		n += i * 10
		n += j
	}
	println(n)

	// Simultaneous, so a swap alternates rather than duplicating.
	a, b := 1, 2
	for k := 0; k < 3; k++ {
		a, b = b, a
	}
	println(a, b)

	// Nested: the inner continue is the INNER loop's post, not the outer one's.
	total := 0
	for p, q := 0, 3; p < q; p, q = p+1, q-1 {
		for r, s := 0, 2; r < s; r, s = r+1, s-1 {
			if r == 0 {
				continue
			}
			total++
		}
		total += 100
	}
	println(total)

	// Three names, and a labeled continue OUT of an inner loop into one: the
	// label lands before the post, so falling through it runs the post.
	for i, j, k := 0, 9, 100; i < j; i, j, k = i+1, j-1, k+1 {
		println(i, j, k)
	}

	outer := 0
L:
	for i, j := 0, 4; i < j; i, j = i+1, j-1 {
		for k := 0; k < 3; k++ {
			if k == 1 {
				continue L
			}
			outer++
		}
	}
	println(outer)

	// The assigning form, "=" rather than ":=", which writes variables that exist.
	p := 0
	q := 0
	for p, q = 0, 5; p < q; p, q = p+1, q-1 {
		println(p, q)
	}
}
`,
		want: "0 9\n1 8\n2 7\n3 6\n4 5\n30\n2 1\n200\n" +
			"0 9 100\n1 8 101\n2 7 102\n3 6 103\n4 5 104\n2\n0 5\n1 4\n2 3\n",
	},
	{
		name: "a bracketed literal used as a value",
		src: `type Row [3]int

type P struct {
	x int
	y int
}

func main() {
	// An array literal as a comparison operand: it has no C value, so it binds to
	// a temporary and the per-type helper compares the two.
	var a [3]int
	a[0] = 1
	println(a == [3]int{1, 0, 0}, a == [3]int{9, 0, 0})
	println([3]int{1, 0, 0} == a, a != [3]int{1, 0, 0})

	var r Row
	r[1] = 5
	println(r == Row{0, 5, 0})

	// A literal read through more than one step, which needs the chain walker to
	// type it rather than the element rule alone.
	x := [2]P{{1, 2}, {3, 4}}[1].y
	y := []P{{5, 6}, {7, 8}}[0].x
	println(x, y)
}
`,
		want: "true false\ntrue false\ntrue\n4 5\n",
	},
	{
		name: "a struct-returning call handed on by value",
		src: `type S struct {
	n  int
	ok bool
}

func mk(n int) S {
	var s S
	s.n = n * 2
	s.ok = n > 0
	return s
}

func (s S) flag() bool { return s.ok }

// Each of these hands a struct-returning call somewhere BY VALUE without going
// through the ordinary argument path: the equality helper, a value receiver, and a
// literal's element. The target loses a sub-word member across such a handoff, so
// each is bound to a temporary first.
func main() {
	println(mk(3) == mk(3), mk(3) == mk(-5))
	println(mk(3).flag(), mk(-5).flag())
	xs := []S{mk(3), mk(-5)}
	println(xs[0].ok, xs[1].ok)
	println(mk(3).n, mk(-5).ok)
}
`,
		want: "true false\ntrue false\ntrue false\n6 false\n",
		// The one position still warning is the slice literal, `[]S{mk(3), mk(-5)}`:
		// "mixing pointer and integer types". Its values are checked right here on
		// the board and are right. It is not bound like the others because a
		// literal's elements are a DECLARATION initializer, which has no prologue to
		// hoist into -- a real fix, not a one-liner. The equality and the value
		// receiver beside it were the same warning family and were genuinely broken;
		// see doc/return-nonword-struct.c for which positions were which.
		backendWarning: "mixing pointer and integer types",
	},
	{
		name: "a struct with a sub-word field, in every position",
		src: `type S struct {
	n  int
	ok bool
}

type Maker interface{ make1(n int) S }

type M struct{}

func (m *M) make1(n int) S { return mk(n) }

var ch chan S

func mk(n int) S {
	var s S
	s.n = n * 2
	s.ok = n > 0
	return s
}

func take(s S) bool { return s.ok }

func send() { ch <- mk(3) }

// The target loses a struct member narrower than a machine word in some positions
// and warns about more of them than it breaks (doc/return-nonword-struct.c). Each
// one is exercised here so the BOARD says which, rather than the diagnostic.
func main() {
	x := mk(3)
	println(x.n, x.ok)

	println(take(mk(3)), take(mk(-5)))

	var mm M
	var i Maker = &mm
	v := i.make1(3)
	println(v.n, v.ok)

	f := mk
	w := f(3)
	println(w.n, w.ok)

	go send()
	c := <-ch
	println(c.n, c.ok)
}
`,
		want: "6 true\n" + "true false\n" + "6 true\n" + "6 true\n" + "6 true\n",
		// The one position left warning is `f := mk` -- a function VALUE whose
		// result is a struct with a sub-word member. That is the diagnostic
		// doc/funcptr-nonword-struct.c measured and found cosmetic, and the values
		// it produces are checked right here on the board. The other three that used
		// to warn were real, and are fixed rather than recorded.
		backendWarning: "incompatible pointer types in assignment",
	},
	{
		name: "returning a call that returns a struct",
		src: `type S struct {
	n  int
	ok bool
}

func mk(n int) S {
	var s S
	s.n = n * 2
	s.ok = n > 0
	return s
}

// The call's result returned straight out. On the target, returning a struct that
// holds anything narrower than a machine word DIRECTLY from a call loses that
// member, so the call is bound to a temporary first; see doc/return-nonword-struct.c.
func fwd(n int) S { return mk(n) }

func main() {
	a := fwd(3)
	b := fwd(-5)
	println(a.n, a.ok, b.n, b.ok)
}
`,
		want: "6 true -10 false\n",
	},
	{
		name: "a multi-result call forwarded as a return",
		src: `var log int

func two() (int, int) { return 1, 2 }

func flags(n int) (int, bool) { return n * 2, n > 0 }

// One call supplying every result. Both functions return the same C struct --
// result structs are keyed by the result types -- so the call IS the return value.
func fwd() (int, int) { return two() }

func fwdFlags(n int) (int, bool) { return flags(n) }

func through(f func() (int, int)) (int, int) { return f() }

func withDefer() (int, int) {
	// Go evaluates the operand and only then runs the defers, so the call is bound
	// before they run rather than emitted after them.
	defer func() { log = 9 }()
	return two()
}

func main() {
	a, b := fwd()
	println(a, b)

	v, ok := fwdFlags(3)
	w, no := fwdFlags(-5)
	println(v, ok, w, no)

	c, d := through(two)
	println(c, d)

	e, f := withDefer()
	println(e, f, log)
}
`,
		want: "1 2\n6 true -10 false\n1 2\n1 2 9\n",
	},
	{
		name: "two result lists that spell one struct name",
		src: `// The result struct is named after the result TYPES, which two different lists
// can spell alike once a type name contains an underscore: (a_b, int) and
// (a, b_int) both read as a_b_int. The second function used to get the first's
// struct, and an int64 result came back truncated.
type a int64

type b_int int8

type a_b int

func f() (a_b, int) { return 1, 2 }

func g() (a, b_int) { return 1234567890123, 7 }

func main() {
	p, q := f()
	r, s := g()
	println(int(p), q, int64(r), int(s))
}
`,
		want: "1 2 1234567890123 7\n",
	},
	{
		name: "a range clause assigning into struct fields",
		src: `type S struct {
	i int
	v int
}

var s S

var t S

// An assigning clause writes variables that already exist. A struct FIELD is a
// place to write like any other -- the field path renders it as an lvalue -- so
// the loop copies its counter and element into one each iteration.
func main() {
	xs := []int{5, 6, 7}
	for s.i, s.v = range xs {
		println(s.i, s.v)
	}
	// After the loop they hold the last pair, as Go leaves them.
	println(s.i, s.v)

	// break leaves them at the iteration it broke on.
	for t.i, t.v = range xs {
		if t.i == 1 {
			break
		}
	}
	println(t.i, t.v)

	// The key alone.
	for s.i = range xs {
	}
	println(s.i)
}
`,
		want: "0 5\n1 6\n2 7\n2 7\n1 6\n2\n",
	},
	{
		// A struct field of ARRAY type as the target: the element is copied in, as
		// into an array variable. It was refused, "a range target must be a
		// variable or a struct field" -- an array field has no C value type, and
		// the field test asked for one. The element is a copy, so writing the
		// target in the body leaves the operand alone.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a range clause assigning into array fields",
		src: `type Pair [2]int

type Rec struct {
	last Pair
	rows [2][3]int
	n    int
}

var g Rec

var table = [3]Pair{{1, 2}, {3, 4}, {5, 6}}

var ptrs [2]*int

var gx, gy = 10, 20

func main() {
	var r Rec
	for r.n, r.last = range table {
		r.last[0] += 100
	}
	println(r.n, r.last[0], r.last[1], table[2][0])
	for _, g.last = range [2]Pair{{7, 8}, {9, 10}} {
	}
	println(g.last[0], g.last[1])
	grid := [2][2][3]int{{{1, 2, 3}, {4, 5, 6}}, {{7, 8, 9}, {10, 11, 12}}}
	var i int
	for i, r.rows = range grid {
		println(i, r.rows[1][2])
	}
	ptrs = [2]*int{&gx, &gy}
	var w struct{ p *int }
	for _, w.p = range ptrs {
	}
	println(*w.p)
}
`,
		want: "2 105 6 5\n9 10\n0 6\n1 12\n20\n",
	},
	{
		name: "a channel whose element is an array",
		src: `type T struct {
	v [3]int
}

var ch chan [3]int

var done chan int

var t T

var gw [3]int

var deep chan [2][3]int

// The rendezvous cannot copy an array BY VALUE -- C has no array assignment, and a
// parameter of a typedef'd array type miscompiles here -- so the cell holds the
// array and the helpers take a pointer both ways. A receive therefore has no
// expression: it writes into storage the receiver already owns, or into a temporary
// bound for it.
func send() {
	var a [3]int
	a[0] = 7
	a[2] = 9
	ch <- a
	a[0] = 99
	ch <- a
	a[0] = 1
	ch <- a
	<-done
	a[0] = 3
	a[2] = 8
	ch <- a
	a[0] = 4
	ch <- a
}

func main() {
	go send()

	v := <-ch
	println(v[0], v[2])

	var w [3]int
	w = <-ch
	println(w[0], w[2])

	t.v = <-ch
	println(t.v[0], t.v[2])

	done <- 1

	// A select clause receives one too: its temporary is declared with the
	// element's extents and the try-receive fills it, then the clause's variable is
	// copied out of that.
	select {
	case u := <-ch:
		println(u[0], u[2])
	}
	select {
	case gw = <-ch:
		println(gw[0], gw[2])
	}
	select {
	case z := <-ch:
		println("got", z[0])
	default:
		println("none")
	}

	// A MULTI-DIMENSIONAL element. The copy is by size and names no element type,
	// which is what makes every rank work: a [2][3]int decays to a pointer to its
	// ROW, not to an int, so a helper naming the innermost element mismatches it.
	go send3()
	m := <-deep
	println(m[1][2], m[0][0])
}

func send3() {
	var a [2][3]int
	a[1][2] = 7
	a[0][0] = 4
	deep <- a
}
`,
		want: "7 9\n99 9\n1 9\n3 8\n4 8\nnone\n7 4\n",
	},
	{
		name: "a slice whose element is an array",
		src: `type Row [2]int

type T struct {
	rows [][2]int
}

func first(xs [][2]int) int { return xs[0][0] }

// C cannot spell an array inline where the slice header's pointer goes, so the
// element gets a typedef. The helpers that would take it BY VALUE take a pointer
// instead: a function parameter of array type corrupts unrelated code on this
// target (doc/array-param-corrupts.c), which is what made this look impossible.
func main() {
	xs := make([][2]int, 3)
	xs[0][1] = 7
	xs[2][0] = 4
	println(xs[0][1], xs[2][0], len(xs))

	sum := 0
	for i, v := range xs {
		sum += i + v[0] + v[1]
	}
	println(sum)

	// append copies the element in, so writing the source afterwards does not
	// reach it.
	as := make([][2]int, 0, 4)
	var r [2]int
	r[0] = 5
	r[1] = 6
	as = append(as, r)
	r[0] = 99
	println(len(as), as[0][0], as[0][1])

	bs := make([][2]int, 1)
	copy(bs, as)
	println(bs[0][0], bs[0][1])

	println(first(xs[2:]))
	cs := xs[1:]
	println(len(cs), cs[1][0])

	// A literal: the backing is declared with the element's own extents, the
	// target refusing a brace group for a typedef'd array element.
	ls := [][2]int{{1, 2}, {3, 4}, {5, 6}}
	println(len(ls), ls[0][0], ls[1][1], ls[2][0])

	ys := []Row{{11, 12}, {13, 14}}
	println(ys[1][0], ys[0][1])

	var t T
	t.rows = [][2]int{{3, 4}}
	println(t.rows[0][1])

	zs := make([][2][3]int, 2)
	zs[1][0][2] = 9
	println(zs[1][0][2])
}
`,
		want: "7 4 3\n14\n1 5 6\n5 6\n4\n2 4\n3 1 4 5\n13 12\n4\n9\n",
	},
	{
		name: "an array of slices",
		src: `// Each element is a slice HEADER, which is an ordinary C value, so the flat
// static layout has somewhere to put it. A slice of ARRAYS is the other way round
// and works too, its element reached through a pointer to an array -- see "a slice
// whose element is an array".
func main() {
	var m [2][]int
	m[0] = []int{1, 2, 3}
	m[1] = []int{9}
	println(m[0][1], len(m[0]), m[1][0], len(m[1]))

	sum := 0
	for i := 0; i < 2; i++ {
		for j := 0; j < len(m[i]); j++ {
			sum += m[i][j]
		}
	}
	println(sum)
}
`,
		want: "2 3 9 1\n15\n",
	},
	{
		name: "a call returning an array, read where it stands",
		src: `type T struct {
	n int
}

func (t T) row() [2]int {
	var a [2]int
	a[0] = t.n
	a[1] = t.n * 2
	return a
}

func mk(k int) [3]int {
	var a [3]int
	a[0] = k
	a[1] = k + 1
	a[2] = k + 2
	return a
}

type S struct {
	v [3]int
}

var g [3]int

func fwd(k int) [3]int { return mk(k) }

func take(a [3]int, b [3]int) int { return a[0] + b[1] }

// An array result travels through an out parameter -- C cannot return one -- so
// the call is a statement with no expression to index. It is bound to a temporary
// and the steps read that; two calls in one expression get one temporary each.
func main() {
	println(mk(4)[1], mk(10)[2])

	x := mk(7)[0]
	println(x)

	sum := 0
	for i, v := range mk(1) {
		sum += i * v
	}
	println(sum)

	var t T
	t.n = 8
	println(t.row()[0], t.row()[1])

	// Handed on WHOLE. The caller owns the storage, so where the target IS storage
	// -- a variable, a global, a struct field, this function's own out parameter --
	// the call writes through it and nothing is copied.
	var b [3]int
	b = mk(4)
	println(b[0], b[1], b[2])

	g = mk(10)
	println(g[2])

	var s S
	s.v = mk(7)
	println(s.v[1])

	// An argument is not storage the callee owns, so it binds to a temporary; two
	// calls in one call get one each.
	println(take(mk(1), mk(10)))

	c := fwd(20)
	println(c[1])

	println(take(b, b), b[0])
}
`,
		want: "5 12\n7\n8\n8 16\n4 5 6\n12\n8\n12\n21\n9 4\n",
	},
	{
		name: "go through a function value",
		src: `type T struct {
	fn func(int)
}

var done chan int

func a(n int) { done <- n }

func b(n int) { done <- n * 100 }

func two(x int, y int) { done <- x + y }

func none() { done <- 9 }

// A cog's entry point is generated per function, so a value has no name to
// generate one against: the trampoline is generated against the function TYPE and
// the pointer travels in the argument block with the arguments.
func main() {
	var g func(int) = a
	go g(7)
	println(<-done)

	// Go evaluates the callee at the "go", so reassigning after it changes nothing.
	h := a
	go h(3)
	h = b
	println(<-done)

	k := two
	go k(3, 4)
	println(<-done)

	n := none
	go n()
	println(<-done)

	// Held in a struct field, which used to take the method path and emit a call to
	// a name nothing declared.
	var t T
	t.fn = b
	go t.fn(5)
	println(<-done)
}
`,
		want: "7\n3\n7\n9\n500\n",
	},
	{
		name: "a multi-result function as a value",
		src: `type Ops struct {
	dm func(int, int) (int, int)
}

func divmod(a int, b int) (int, int) { return a / b, a % b }

// The same signature as divmod, which is the point: both return the ONE result
// struct their result types name, so a variable of that function type can hold
// either.
func swap(a int, b int) (int, int) { return b, a }

func flags(n int) (int, bool) { return n * 2, n > 0 }

func nm(n int) (int, string) { return n + 1, "hi" }

func narrow(n int) (int8, int16) { return int8(n), int16(n * 2) }

func apply(f func(int, int) (int, int), a int, b int) int {
	q, r := f(a, b)
	return q + r
}

func main() {
	f := divmod
	q, r := f(17, 5)
	println(q, r)

	f = swap
	x, y := f(1, 2)
	println(x, y)

	// A written function type, and results of two different types.
	var g func(int) (int, bool) = flags
	n, ok := g(3)
	println(n, ok)

	// Held in a struct field.
	var o Ops
	o.dm = divmod
	a, b := o.dm(9, 4)
	println(a, b)

	// Passed as a parameter.
	println(apply(divmod, 17, 5), apply(swap, 1, 2))

	// A string result and narrow ones: the member kinds the backend diagnostic
	// recorded below is about. It fires for a result struct whose members are not
	// all machine words, so what those return is checked on the board here.
	var t func(int) (int, string) = nm
	c, u := t(7)
	println(c, u)

	var w func(int) (int8, int16) = narrow
	p, v := w(3)
	println(p, v)
}
`,
		want: "3 2\n2 1\n6 true\n2 1\n5 3\n8 hi\n3 6\n",
		// The types are identical -- both spelled by the same typedef -- and the
		// values this returns are checked on real hardware right here, for a bool, a
		// string and two narrow ints. The target's compiler unifies a result struct
		// of machine words and calls anything else "unknown type", so a result list
		// of plain ints is silent and a mixed one is not. It is the diagnostic that
		// is wrong, not the code; doc/funcptr-nonword-struct.c has the measurements
		// and the cast that would silence it, with why that was declined.
		backendWarning: "incompatible pointer types in assignment",
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

func take16(v uint16) int { return int(v) }
func chain(v uint8) uint8 { return v * 3 / 2 }

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

	// Two operations in a row, the first of which overflows: Go wraps after EACH
	// one, in the operand's type, so k*3 is 54464 before it is halved. A
	// parenthesised intermediate, (k*3)/2, was always right; the flat chain
	// wrapped only its total and printed 60000 -- found by a fixed-point probe
	// program diffed against Go on the board, in what a driver's byte arithmetic
	// looks like. Printed, compared, passed, converted, stored, and on a defined
	// type.
	var k uint16 = 40000
	var j uint16 = 30000
	println(k*3/2, k*3%7, k+j-k, a*a/a, a*3/2, s*3/2, -s*2/3, c*3/2, c*c/7, -c*2/3)
	println(k*3 > 50000, k*3/2 == 27232, take16(k*3/2), chain(a), uint32(k*3/2), int(c*3/2))
	x := k*3/2 + 1
	y := c*3/2 - 1
	println(x, y, n*3/2, n*n/n)
}
`,
		want: "44 156 88 32 56 55\n-56 44 -112 -100 -101\n54464 48928 5536 5535\n" +
			"-5536 -30000 -30001\n88 56 55\n88 56 -5536\n88 56 55\n88\n600 300\n1088\n" +
			"27232 4 30000 0 44 12232 1845 22 2 18\ntrue true 27232 44 27232 22\n27233 21 44 0\n",
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

// The field's backing array is the package's too: one make allocated in a
// function, main included, is that function's storage.
var xsBack [2]int

func main() {
	gp = &g
	g.in.name = "pkg"
	g.in.on = true
	g.n = 4
	g.in.xs = xsBack[:]
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
		println("aliased", δ)
	}
	switch ε := δ * 2 {
	case 6:
		println("linked", ε)
	}

	switch greet() {
	case "bye":
		println("bye")
	case "hi":
		println("greeting")
	}
}
`,
		want: "inner 10\nouter 9\naliased 3\nlinked 6\ngreeting\n",
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
		// "1 true 7": the ok of a two-result append is a BOOL. It printed 1 until
		// 2026-08-08 -- the checker always typed it bool, and only the emitter said
		// int -- and this golden was written from the implementation and agreed with
		// it. Every other ok in the language prints true.
		want: "10 20\n5 6\n1 true 7\n10 20\n99\n",
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
		// The same comparison with an operand that is not a bare VARIABLE -- a row of
		// an array of arrays, a struct field, a nested row, a dereferenced pointer.
		// The case above compares only variables and literals, which is exactly the
		// two shapes the operand reader knew, and an operand it declined was not
		// refused: the comparison fell through to C's own "==", which asks whether
		// the two decayed pointers are equal. So `table[0] == table[1]` was FALSE for
		// two identical rows, in C that draws no warning from either compiler,
		// comparing two pointers being an ordinary thing to write.
		//
		// The reader now takes every shape the copy does. That the last line puts the
		// comparison in an `if` is deliberate: a condition is where such a comparison
		// is usually written, and it is the position a wrong answer is least visible
		// in.
		name: "comparing arrays reached through a chain",
		src: `type Row [2]int

type H struct {
	f    [2]int
	rows [2][2]int
}

var table = [3][2]int{{1, 2}, {1, 2}, {3, 4}}

var named = [2]Row{{5, 6}, {5, 6}}

var a = H{[2]int{1, 2}, [2][2]int{{7, 8}, {9, 10}}}

var b = H{[2]int{1, 2}, [2][2]int{{7, 8}, {0, 0}}}

func main() {
	// A ROW of an array of arrays, on both sides and on one.
	println(table[0] == table[1], table[0] == table[2])
	println(table[0] != table[1], table[0] != table[2])

	// A field, a nested row, a field of a defined array type.
	println(a.f == b.f, a.rows[0] == b.rows[0], a.rows[1] == b.rows[1])
	println(named[0] == named[1])

	// A dereferenced pointer, and a mix of a chain with a variable and a literal.
	p := &table
	v := [2]int{1, 2}
	println(p[0] == v, a.f == v, a.f == [2]int{1, 2}, table[2] == v)

	// Inside a condition, which is where such a comparison is usually written.
	hits := 0
	for i := 0; i < 3; i++ {
		if table[i] == v {
			hits++
		}
	}
	println(hits)
}
`,
		want: "true false\nfalse true\ntrue true false\ntrue\ntrue true true false\n2\n",
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
		name: "range over a channel",
		src: `type samp struct {
	n  int32
	id int32
}

var a chan int32
var b chan samp
var c chan int32
var work [2]chan int32
var rest chan int32
var quiet chan int32

func feedA() {
	for i := int32(1); i <= 4; i++ {
		a <- i * i
	}
	close(a)
}

func feedB() {
	b <- samp{n: 3, id: 1}
	b <- samp{n: 5, id: 2}
	close(b)
}

func feedC() {
	for i := int32(0); i < 5; i++ {
		c <- i
	}
	close(c)
}

func feedWork() {
	work[1] <- 7
	work[1] <- 8
	close(work[1])
}

func feedRest() {
	rest <- 10
	rest <- 20
	close(rest)
}

func feedQuiet() {
	quiet <- 1
	quiet <- 2
	quiet <- 3
	close(quiet)
}

func main() {
	go feedA()
	sum := int32(0)
	for v := range a {
		sum += v
	}
	println("a", sum)
	go feedB()
	for s := range b {
		println("b", s.n, s.id)
	}
	// break and continue inside the loop
	go feedC()
	seen := int32(0)
	for v := range c {
		if v == 0 {
			continue
		}
		if v == 4 {
			break
		}
		seen += v
	}
	println("c", seen)
	// an element of a bank of channels
	go feedWork()
	total := int32(0)
	for v := range work[1] {
		total += v
	}
	println("work", total)
	// The ASSIGNING clause writes the program's own variable, and Go writes it only
	// on a receive that succeeded -- so it holds the LAST value, not the zero the
	// closed channel yields after it.
	var last int32
	go feedRest()
	for last = range rest {
	}
	println("last", last)
	go feedQuiet()
	n := 0
	for range quiet {
		n++
	}
	println("quiet", n)
}
`,
		want: "a 30\nb 3 1\nb 5 2\nc 6\nwork 15\nlast 20\nquiet 3\n",
	},
	{
		name: "close and the comma-ok receive",
		src: `type samp struct {
	n  int32
	ok bool
}

var ch chan int32
var st chan samp
var q chan int32

func feed() {
	for i := int32(1); i <= 3; i++ {
		ch <- i
	}
	close(ch)
}

func feedStruct() {
	st <- samp{n: 7, ok: true}
	close(st)
}

func main() {
	go feed()
	for {
		v, more := <-ch
		if !more {
			println("drained")
			break
		}
		println("got", v)
	}
	// A closed channel yields the element's zero at once and for ever, so a
	// blocking receive past the end reads it rather than waiting.
	println(<-ch, <-ch)
	go feedStruct()
	s, more := <-st
	println(s.n, s.ok, more)
	z, more2 := <-st
	println(z.n, z.ok, more2)
	// The assignment form, and a channel closed before anything was sent.
	var v int32
	var ok bool
	close(q)
	v, ok = <-q
	println(v, ok)
}
`,
		want: "got 1\ngot 2\ngot 3\ndrained\n0 0\n7 true true\n0 false false\n0 false\n",
	},
	{
		// Directional channel types, as a pipeline is written: a producer given a
		// named send-only type, a stage holding one end of each kind, a consumer
		// taking a named receive-only type, and an accessor handing out a
		// receive-only view. Directions are the checker's; at run time the three
		// spellings are one cell.
		name: "directional channels: a pipeline",
		src: `type Source <-chan int

type Sink chan<- int

type Stage struct {
	in  <-chan int
	out chan<- int
	k   int
}

var raw chan int
var mid chan int
var done chan int

func producer(out Sink, n int) {
	for i := 1; i <= n; i++ {
		out <- i
	}
	close(out)
}

func (s *Stage) run() {
	for v := range s.in {
		s.out <- v * s.k
	}
	close(s.out)
}

func results() <-chan int { return done }

func sum(in Source) int {
	t := 0
	for v := range in {
		t += v
	}
	return t
}

var st Stage

func main() {
	go producer(raw, 4)
	st = Stage{in: raw, out: mid, k: 10}
	go st.run()
	var ro <-chan int = mid
	in := ro
	println(sum(in))
	var src Source = done
	println(src == results(), ro == mid)
	go func() {
		done <- 7
	}()
	select {
	case v := <-results():
		println("result", v)
	}
}
`,
		want: "100\ntrue true\nresult 7\n",
	},
	{
		// A channel received from a channel of channels is a channel, however it is
		// received -- a range, a comma-ok, a select clause, a plain ":=" -- and so is
		// a conversion to a defined channel type. A request carrying its reply
		// channel is how a server is asked for an answer. Each was "cannot send to
		// non-channel".
		name: "a channel of reply channels, and a conversion to a channel type",
		src: `type Pipe chan int

var reqs chan chan<- int
var stop chan chan<- int
var reply chan int
var sig chan int

func server(q <-chan chan<- int) {
	n := 100
	for r := range q {
		n++
		r <- n
	}
}

func once(q <-chan chan<- int) {
	r, ok := <-q
	if ok {
		r <- -1
	}
}

func selectOne(q <-chan chan<- int) {
	select {
	case r := <-q:
		r <- -2
	}
}

func plain(q <-chan chan<- int) {
	r := <-q
	r <- -3
}

func echo(in <-chan int, out chan<- int) {
	out <- <-in + 1
}

func main() {
	go server(reqs)
	for i := 0; i < 3; i++ {
		reqs <- reply
		println("reply", <-reply)
	}
	close(reqs)
	go once(stop)
	stop <- reply
	println(<-reply)
	go selectOne(stop)
	stop <- reply
	println(<-reply)
	go plain(stop)
	stop <- reply
	println(<-reply)
	p := Pipe(sig)
	go echo(sig, reply)
	p <- 9
	println(<-reply)
}
`,
		want: "reply 101\nreply 102\nreply 103\n-1\n-2\n-3\n10\n",
	},
	{
		// A CLOSED channel is always ready, and a select has to know it: the poll's
		// non-blocking receive reported "nothing yet" for one, so a select with no
		// default polled for ever and a select WITH one took the default -- a silent
		// wrong answer, where Go takes the receive and its zero. The receive, the
		// comma-ok receive and `for range` all knew; the select's own half did not.
		name: "a select on a closed channel",
		src: `var c chan int
var d chan int

func feed() {
	c <- 7
	close(c)
}

func main() {
	select {
	case v := <-d:
		println("d", v)
	default:
		println("default")
	}
	go feed()
	for i := 0; i < 3; i++ {
		select {
		case v := <-c:
			println("recv", v)
		case w := <-d:
			println("other", w)
		}
	}
	select {
	case v := <-c:
		println("again", v)
	default:
		println("default")
	}
}
`,
		want: "default\nrecv 7\nrecv 0\nrecv 0\nagain 0\n",
	},
	{
		// A written-out dereference applies to the WHOLE target, not to its head:
		// `*h.p = v` is `*(h.p) = v` and `*a[i] = v` is `*(a[i]) = v`, C's precedence
		// and Go's alike. Every target that reached its pointer through a chain used
		// to read the star as the head's -- the field path wrote `(*h) = v`, which is
		// not C at all, and the index path dropped the star for `a[i] = v`, valid C
		// that stores into the POINTER. The double dereference was the one that
		// worked, and only because the single-star nil check is what replaced the
		// target with the head.
		name: "an assignment through a pointer reached by a chain",
		src: `type inner struct {
	p *int
}

type outer struct {
	in inner
}

type pt struct {
	x int
	y int
}

type holder struct {
	p  *int
	pp **int
	ps []*int
	sp *pt
}

var n int
var m int
var q pt
var gp *int
var a [2]*int
var back [2]*int
var h holder
var o outer

func main() {
	h.p = &n
	*h.p = 5
	println("field", n, *h.p)
	a[0] = &m
	*a[0] = 7
	println("elem", m, *a[0])
	o.in.p = &n
	*o.in.p = 11
	println("deep", n)
	*h.p += 4
	*h.p++
	println("compound", n)
	gp = &n
	h.pp = &gp
	**h.pp = 21
	println("double", n)
	h.sp = &q
	*h.sp = pt{3, 4}
	println("struct", q.x, q.y)
	h.ps = back[:]
	h.ps[1] = &m
	*h.ps[1] = 9
	println("slice", m)
}
`,
		want: "field 5 5\nelem 7 7\ndeep 11\ncompound 16\ndouble 21\nstruct 3 4\nslice 9\n",
	},
	{
		// An interface compares with a CONCRETE value, which is how a sentinel is
		// recognised -- the pattern an exported error variable exists for. Go
		// converts the concrete side to the interface and compares the same two
		// words, so this is the pair comparison written out rather than read out.
		// Before, the operand was compared as it stood: two words against one, which
		// the target's C compiler only WARNED about, so `ogo build` wrote a binary
		// for it.
		name: "an interface compared with a concrete value",
		src: `type Shape interface {
	Area() int
}

type Sq struct{ s int }

func (q *Sq) Area() int { return q.s * q.s }

type Rect struct {
	w int
	h int
}

func (r *Rect) Area() int { return r.w * r.h }

type box struct {
	s Shape
}

var a Sq
var b Sq
var r Rect
var pool [2]Shape
var bx box

func same(x Shape, y Shape) bool { return x == y }

func main() {
	a.s = 2
	b.s = 3
	r.w, r.h = 2, 2
	var s Shape = &a
	println(s == &a, &a == s, s != &a, s != &b)
	// a different concrete type of the same area: the TABLE tells them apart
	println(s == &r, s != &r)
	println(s == nil, s != nil, same(s, &a))
	bx.s = &r
	pool[0] = &a
	pool[1] = &r
	println(bx.s == &r, pool[0] == &a, pool[1] == &a)
	var z Shape
	println(z == &a, z != &a)
}
`,
		want: "true true false true\nfalse true\nfalse true true\ntrue true false\nfalse true\n",
	},
	{
		// error, the predeclared interface. It is interface{ Error() string } under a
		// name the universe holds, so everything the interface machinery does works
		// for it -- and with no heap a sentinel is a package-level variable whose
		// address is returned, there being nowhere to make one at run time.
		name: "the predeclared error interface",
		src: `type parseErr struct {
	at int
}

func (e *parseErr) Error() string { return "bad input" }

type rangeErr struct {
	lo int
	hi int
}

func (e *rangeErr) Error() string { return "out of range" }

func (e *rangeErr) Retryable() bool { return false }

// An interface EMBEDDING error, which is how a richer failure type is written.
type Failer interface {
	error
	Retryable() bool
}

var errParse parseErr
var errRange rangeErr

var errs [3]error
var ch chan error
var done chan int

type job struct {
	id  int
	err error
}

func parse(s string) (int, error) {
	if len(s) == 0 {
		return 0, &errParse
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, &errParse
		}
		n = n*10 + int(c) - int('0')
	}
	if n > 999 {
		return 0, &errRange
	}
	return n, nil
}

func report(s string) {
	n, err := parse(s)
	if err != nil {
		println(s, "->", err.Error(), err == &errRange)
		return
	}
	println(s, "->", n)
}

func classify(err error) string {
	switch e := err.(type) {
	case *rangeErr:
		_ = e
		return "range"
	case *parseErr:
		return "parse"
	}
	if err == nil {
		return "none"
	}
	return "unknown"
}

func take() {
	e := <-ch
	println("chan", e.Error())
	done <- 1
}

func main() {
	report("123")
	report("")
	report("1000")
	println(classify(&errParse), classify(&errRange), classify(nil))
	var e error
	println(e == nil)
	e = &errRange
	println(e == nil, e.Error())
	// an array with a nil element among the errors, and a struct field
	errs[0] = &errParse
	errs[2] = &errRange
	for i := 0; i < 3; i++ {
		if errs[i] == nil {
			println(i, "nil")
			continue
		}
		println(i, errs[i].Error())
	}
	var j job
	j.err = &errRange
	println(j.id, j.err == &errRange)
	// the embedding interface, and error widened back out of it
	var f Failer = &errRange
	println(f.Error(), f.Retryable())
	var w error = f
	println(w.Error())
	// through a channel
	go take()
	ch <- &errParse
	<-done
	// the comma-ok assertion back to the concrete type
	if r, ok := w.(*rangeErr); ok {
		r.hi = 9
		println("asserted", r.hi)
	}
}
`,
		want: "123 -> 123\n -> bad input false\n1000 -> out of range true\n" +
			"parse range none\ntrue\nfalse out of range\n" +
			"0 bad input\n1 nil\n2 out of range\n0 true\n" +
			"out of range false\nout of range\nchan bad input\nasserted 9\n",
	},
	{
		// The nil check reaches the chain's dereference as it reaches a variable's:
		// address zero on this target is the boot area, and a store there is the one
		// dereference that would say nothing.
		name: "a store through a nil pointer field panics",
		src: `type holder struct {
	p *int
}

var h holder

func main() {
	*h.p = 5
	println(1)
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		// A send CLAUSE on a closed channel panics, as the blocking send does and as
		// Go does from inside a select -- and Go panics whether or not another clause
		// is ready, so the offer asks on the way in. It used to offer a value nothing
		// could take and poll until the other clause fired, or for ever.
		name: "a select send clause on a closed channel panics",
		src: `var c chan int
var d chan int

func main() {
	close(c)
	select {
	case c <- 1:
		println("sent")
	case v := <-d:
		println("got", v)
	}
}
`,
		want:   "panic: send on closed channel",
		panics: true,
	},
	{
		// A NIL channel parks whoever touches it, as in Go: the receive blocks for
		// ever, so the goroutine never delivers and main's default fires. Before the
		// guard, address 0 was dereferenced as a cell and the receive "succeeded"
		// with garbage -- got 0 -- on the board.
		name: "a receive from a nil channel blocks for ever",
		src: `var real chan int32

func bad() {
	var c chan int32 = nil
	v := <-c
	real <- v
}

func main() {
	go bad()
	n := int32(0)
	for i := 0; i < 100000; i++ {
		n++
	}
	select {
	case v := <-real:
		println("got", v)
	default:
		println("nil recv blocked", n)
	}
}
`,
		want: "nil recv blocked 100000\n",
	},
	{
		// A nil channel in a select DISABLES that clause -- the standard Go idiom
		// for switching an arm off. Before the guard the nil arm read address 0,
		// an always-ready case of zeroes that starved the live channel: 0, not 85.
		name: "a nil channel disables its select clause",
		src: `var live chan int32

func producer() {
	live <- 42
	live <- 43
}

func main() {
	go producer()
	var off chan int32 = nil
	sum := int32(0)
	for i := 0; i < 2; i++ {
		select {
		case v := <-off:
			sum += v * 1000
		case v := <-live:
			sum += v
		}
	}
	println(sum)
}
`,
		want: "85\n",
	},
	{
		// Go panics; this used to fall silent on the board -- no message, no exit.
		name: "close of a nil channel panics",
		src: `func main() {
	var c chan int32 = nil
	close(c)
	println("survived")
}
`,
		want:   "panic: close of nil channel",
		panics: true,
	},
	{
		// The dispatch-table idiom in every position a table lives: an array, a
		// struct field, a slice view -- each element called as a STATEMENT, which
		// used to be the emitter catch-all's refusal while the same call in value
		// position worked. The element is bound to a temporary before the call
		// (doc/call-through-array-element.c).
		name: "a handler table dispatches as a statement",
		src: `var count int

func bump(n int) { count += n }

type dev struct {
	tab [2]func(int)
}

var tab [2]func(int)

var d dev

func main() {
	tab[0] = bump
	tab[0](5)
	d.tab[1] = bump
	d.tab[1](6)
	hs := tab[:]
	hs[0](7)
	println(count)
}
`,
		want: "18\n",
	},
	{
		// A MULTI-RESULT call through a table element: the results travel through
		// the function value's leading out parameter, and the element is bound
		// first. Refused before as "multiple assignment requires a single function
		// call" -- of a call.
		name: "a multi-result call through a table element",
		src: `func two() (int, int) { return 3, 4 }

var tab [1]func() (int, int)

func main() {
	tab[0] = two
	tab[0]()
	a, b := tab[0]()
	println(a, b)
}
`,
		want: "3 4\n",
	},
	{
		// A framed serial protocol end to end -- CRC16-CCITT, COBS encode/decode,
		// and a handler table dispatching verified frames, one of them corrupted in
		// flight. The uplink a real device speaks: byte slices, bit twiddling,
		// function values and the element-call statement, composed.
		name: "a framed protocol end to end",
		src: `// Round 16: a framed serial protocol, end to end -- build telemetry frames,
// CRC16-CCITT them, COBS-encode for the wire, then decode, verify and dispatch
// through a table of handlers. The shape of a device's uplink.

const maxFrame = 32

var wire [128]byte

var wireLen int

func crc16(data []byte) uint16 {
	crc := uint16(0xffff)
	for i := 0; i < len(data); i++ {
		crc ^= uint16(data[i]) << 8
		for b := 0; b < 8; b++ {
			top := crc >= 0x8000
			crc <<= 1
			if top {
				crc ^= 0x1021
			}
		}
	}
	return crc
}

// cobsEncode writes src as a COBS frame into dst, returning the encoded length.
// Zero bytes never appear in the output; a trailing 0 delimits the frame.
func cobsEncode(dst []byte, src []byte) int {
	code := byte(1)
	codeAt := 0
	w := 1
	for i := 0; i < len(src); i++ {
		if src[i] == 0 {
			dst[codeAt] = code
			code = 1
			codeAt = w
			w++
		} else {
			dst[w] = src[i]
			w++
			code++
		}
	}
	dst[codeAt] = code
	dst[w] = 0
	return w + 1
}

// cobsDecode reverses cobsEncode, stopping at the delimiter; -1 on a malformed
// frame.
func cobsDecode(dst []byte, src []byte) int {
	w := 0
	i := 0
	for i < len(src) && src[i] != 0 {
		code := int(src[i])
		i++
		for k := 1; k < code; k++ {
			if i >= len(src) || src[i] == 0 {
				return -1
			}
			dst[w] = src[i]
			w++
			i++
		}
		if code < 255 && i < len(src) && src[i] != 0 {
			dst[w] = 0
			w++
		}
	}
	return w
}

// A frame: type, sequence, two 16-bit payload words, then crc16 over all of it.
func buildFrame(dst []byte, typ byte, seq byte, a uint16, b uint16) int {
	dst[0] = typ
	dst[1] = seq
	dst[2] = byte(a >> 8)
	dst[3] = byte(a)
	dst[4] = byte(b >> 8)
	dst[5] = byte(b)
	c := crc16(dst[:6])
	dst[6] = byte(c >> 8)
	dst[7] = byte(c)
	return 8
}

var tempSum uint32

var pressLast uint16

var badFrames int

func onTemp(seq byte, a uint16, b uint16) {
	tempSum += uint32(a) + uint32(b)
}

func onPress(seq byte, a uint16, b uint16) {
	pressLast = a ^ b
}

type handler func(seq byte, a uint16, b uint16)

var handlers [3]handler

func send(typ byte, seq byte, a uint16, b uint16, corrupt bool) {
	var raw [maxFrame]byte
	n := buildFrame(raw[:], typ, seq, a, b)
	if corrupt {
		raw[3] ^= 0x40
	}
	var enc [maxFrame + 2]byte
	en := cobsEncode(enc[:], raw[:n])
	for i := 0; i < en; i++ {
		wire[wireLen] = enc[i]
		wireLen++
	}
}

func main() {
	handlers[1] = onTemp
	handlers[2] = onPress

	send(1, 1, 0x1234, 0x0056, false)
	send(2, 2, 0xff00, 0x00ff, false)
	send(1, 3, 0x0100, 0x0001, true)
	send(2, 4, 0xaaaa, 0x5555, false)
	send(1, 5, 0, 0, false)

	println("wire bytes", wireLen)

	// Decode frame by frame: scan to each delimiter.
	start := 0
	frames := 0
	for start < wireLen {
		end := start
		for wire[end] != 0 {
			end++
		}
		stop := end + 1
		var dec [maxFrame]byte
		n := cobsDecode(dec[:], wire[start:stop])
		if n == 8 {
			c := uint16(dec[6])<<8 | uint16(dec[7])
			if c == crc16(dec[:6]) && int(dec[0]) < len(handlers) && handlers[dec[0]] != nil {
				handlers[dec[0]](dec[1], uint16(dec[2])<<8|uint16(dec[3]), uint16(dec[4])<<8|uint16(dec[5]))
				frames++
			} else {
				badFrames++
			}
		} else {
			badFrames++
		}
		start = stop
	}
	println("frames", frames, "bad", badFrames)
	println("tempSum", tempSum)
	println("pressLast", pressLast)
	println("crc smoke", crc16(wire[:10]))
}
`,
		want: "wire bytes 50\nframes 4 bad 1\ntempSum 4746\npressLast 65535\ncrc smoke 45629\n",
	},
	{
		// Embedding DEFINED NON-STRUCT types -- Go promotes a defined type's methods
		// whatever its underlying type is, and this used to be refused at the
		// declaration. Value and pointer receivers promote, promotion reaches
		// through two levels, an interface is satisfied by a promoted method, a
		// method value binds the embedded receiver, and a defined ARRAY type's
		// elements and method are reachable through its member.
		name: "embedding defined non-struct types",
		src: `// Embedding defined non-struct types, in every position promotion reaches.

type celsius int32

func (c celsius) doubled() int32 { return int32(c) * 2 }

func (c *celsius) bump(by int32) { *c += celsius(by) }

type triple [3]int32

func (t triple) sum() int32 { return t[0] + t[1] + t[2] }

type reading struct {
	celsius
	n int32
}

type station struct {
	reading
	id int32
}

type bank struct {
	triple
	tag int32
}

type doubler interface {
	doubled() int32
}

func take(c celsius) int32 { return int32(c) + 1 }

var gr reading

func main() {
	var r reading
	r.celsius = 21
	r.n = 1

	// The field is a value of its type: read, written, passed, operated on.
	println(int32(r.celsius), take(r.celsius), int32(r.celsius+1))

	// Value- and pointer-receiver methods promote.
	println(r.doubled())
	r.bump(4)
	println(int32(r.celsius))

	// Promotion reaches through TWO levels.
	var s station
	s.celsius = 10
	s.id = 7
	s.bump(5)
	println(s.doubled(), int32(s.reading.celsius), s.id)

	// A method value binds the embedded receiver -- under this language's
	// method-value rules: a pointer-receiver method of a package-level variable.
	gr.celsius = 8
	f := gr.bump
	f(3)
	println(int32(gr.celsius))

	// An interface satisfied by a PROMOTED method of a non-struct embed.
	var d doubler = &r
	println(d.doubled())

	// A defined ARRAY type embeds; its method and its elements are reachable.
	var b bank
	b.triple[0] = 1
	b.triple[1] = 2
	b.triple[2] = 3
	b.tag = 9
	println(b.sum(), b.triple[1], b.tag)
}
`,
		want: "21 22 22\n42\n25\n30 15 7\n11\n50\n6 2 9\n",
	},
	{
		// An embedded INTERFACE promotes its method set, dispatching through
		// whatever the field holds: the plain form, the predeclared error --
		// struct{ error } wrapping a cause -- and a multi-result method promoted
		// through two levels of struct embedding. The field itself stays
		// readable and writable by its name.
		name: "an embedded interface promotes its method set",
		src: `type writer interface {
	write(v int32) int32
}

type reader interface {
	read() (int32, bool)
}

type sink struct {
	total int32
}

func (s *sink) write(v int32) int32 {
	s.total += v
	return s.total
}

func (s *sink) read() (int32, bool) {
	return s.total, s.total < 10
}

type oops struct{}

func (o *oops) Error() string { return "boom" }

type logger struct {
	writer
	id int32
}

type inner struct {
	reader
}

type outer struct {
	inner
	tag int32
}

type failer struct {
	error
	code int32
}

var sk sink

var bad oops

func main() {
	var lg logger
	lg.writer = &sk
	lg.id = 3
	println(lg.write(5), lg.writer.write(2), lg.id)

	var o outer
	o.reader = &sk
	v, ok := o.read()
	println(v, ok, o.tag)

	var f failer
	f.error = &bad
	f.code = 7
	println(f.Error(), f.code)
}
`,
		want: "5 7 3\n7 true 0\nboom 7\n",
	},
	{
		// A call through a NIL interface panics, as Go's does: address zero here
		// is ordinary Hub RAM, and unguarded the call went through garbage and
		// returned it, silently -- measured on the board before the guard.
		name: "a call through a nil interface panics",
		src: `type writer interface {
	write(v int32) int32
}

func main() {
	var w writer
	println("start")
	println(w.write(1))
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		// A type SATISFIES an interface through a method promoted from an embedded
		// interface: the thunk loads the field and dispatches through its vtable,
		// so whatever the field holds at call time answers -- the error-wrapping
		// idiom (struct{ error } passed AS error), a vtable mixing declared and
		// dispatched slots, and a multi-result method forwarding its out
		// parameter.
		name: "a wrapper satisfies through its embedded interface",
		src: `type speaker interface {
	say() int32
	loud() int32
}

type voice interface {
	say() int32
}

type reader interface {
	read() (int32, bool)
}

type oops struct{}

func (o *oops) Error() string { return "boom" }

type failer struct {
	error
	code int32
}

type quiet struct {
	n int32
}

func (q *quiet) say() int32 { return q.n }

type mixed struct {
	voice
	k int32
}

func (m *mixed) loud() int32 { return m.k * 100 }

type src struct {
	n int32
}

func (s *src) read() (int32, bool) {
	s.n++
	return s.n, s.n < 3
}

type wrap struct {
	reader
	tag int32
}

var bad oops

var f failer

var qq = quiet{n: 6}

var mx mixed

var ss src

var w wrap

func report(e error) string { return e.Error() }

func main() {
	// A wrapper passed AS the interface its embedded field supplies: the
	// error-wrapping idiom.
	f.error = &bad
	f.code = 9
	var e error = &f
	println(report(e), e.Error(), f.code)

	// A vtable of both kinds: one slot declared on the type, one dispatched
	// through the embedded interface.
	mx.voice = &qq
	mx.k = 3
	var s speaker = &mx
	println(s.say(), s.loud())

	// A multi-result method satisfied through the field: the out parameter
	// forwards through the dispatching thunk.
	w.reader = &ss
	var r reader = &w
	a, ok1 := r.read()
	b, ok2 := r.read()
	c, ok3 := r.read()
	println(a, ok1, b, ok2, c, ok3, w.tag)
}
`,
		want: "boom boom 9\n6 300\n1 true 2 true 3 false 0\n",
	},
	{
		// A deadline scheduler's machinery, composed: comparator FUNCTION VALUES
		// (one picked at run time, and a METHOD VALUE whose receiver's state is
		// flipped between sorts), struct swaps through indices, a wrap-safe
		// uint32 deadline compare and binary search across the wrap point, and
		// nil-guarded function fields. Round-17 probe; it found nothing, which is
		// the point of writing it down -- this is the program that says those
		// pieces compose.
		name: "a deadline scheduler: comparators, wrap math, function fields",
		src: `// Round 17b: comparators chosen at run time, a method value as one, nil function
// fields guarded, and a wrap-safe binary search over the sorted table.

type task struct {
	deadline uint32
	id       int32
	run      func(t *task) int32
}

const n = 6

var tasks [n]task

func hit(t *task) int32 { return t.id }

func byDeadline(a *task, b *task) bool {
	return int32(a.deadline-b.deadline) < 0
}

func byID(a *task, b *task) bool { return a.id < b.id }

type order struct {
	reverse bool
}

func (o *order) cmp(a *task, b *task) bool {
	if o.reverse {
		return b.id < a.id
	}
	return a.id < b.id
}

var ord order

func sortTasks(less func(a *task, b *task) bool) {
	for i := 1; i < n; i++ {
		for j := i; j > 0; j-- {
			if less(&tasks[j], &tasks[j-1]) {
				tasks[j], tasks[j-1] = tasks[j-1], tasks[j]
			} else {
				break
			}
		}
	}
}

// find returns the index of the first task whose deadline is not before d,
// wrap-safe, over the byDeadline-sorted table.
func find(d uint32) int {
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if int32(tasks[mid].deadline-d) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func ids() {
	for i := 0; i < n; i++ {
		print(tasks[i].id, " ")
	}
	println()
}

func main() {
	base := uint32(0xffffffa0)
	for i := int32(0); i < n; i++ {
		tasks[i].deadline = base + uint32(i)*0x30
		tasks[i].id = i + 1
		if i != 4 {
			tasks[i].run = hit
		}
	}
	// Scramble, then sort with a comparator picked at run time.
	tasks[0], tasks[5] = tasks[5], tasks[0]
	tasks[1], tasks[3] = tasks[3], tasks[1]

	pick := byDeadline
	if tasks[0].id == 0 {
		pick = byID
	}
	sortTasks(pick)
	ids()

	println(find(base), find(base+0x31), find(0x20), find(0xffffff00))

	// A method value as the comparator, both directions.
	sortTasks(ord.cmp)
	ids()
	ord.reverse = true
	sortTasks(ord.cmp)
	ids()

	// A nil function field is skippable, and calling the set ones works.
	total := int32(0)
	for i := 0; i < n; i++ {
		if tasks[i].run != nil {
			total += tasks[i].run(&tasks[i])
		}
	}
	println(total)
}
`,
		want: "1 2 3 4 5 6 \n0 2 3 0\n1 2 3 4 5 6 \n6 5 4 3 2 1 \n16\n",
	},
	{
		// A send clause beside a DEFAULT: the gated non-blocking send offers only
		// when a receiver has announced itself on the cell, so the default is
		// answerable -- refused before, since the standing offer could not know.
		// The first select runs before the consumer exists, so the default is
		// certainly taken once: whether the loop's selects ever find no receiver
		// parked is up to which thread runs first, and `idle > 0` printed false
		// about one host run in a hundred until 2026-09-19.
		name: "a non-blocking send reaches a parked receiver",
		src: `var ch chan int32

func consumer() {
	p := int32(0)
	for i := 0; i < 3; i++ {
		p += <-ch
	}
	done <- p
}

var done chan int32

func main() {
	idle := 0
	// Before the consumer is started no receiver can be parked, so the default
	// is the only arm that can run -- which makes the default's coverage
	// deterministic rather than a matter of which thread runs first.
	select {
	case ch <- int32(99):
		println("unreachable")
	default:
		idle++
	}
	go consumer()
	sent := 0
	for sent < 3 {
		select {
		case ch <- int32(sent + 1):
			sent++
		default:
			idle++
		}
	}
	println(<-done, sent, idle > 0)
}
`,
		want: "6 3 true\n",
	},
	{
		// TWO send clauses in one select, each gated on its own channel's parked
		// receiver, so no two offers ever stand at once -- the shape the old
		// refusal called unfair.
		name: "two send clauses feed two sinks",
		src: `var a chan int32

var b chan int32

func sink(c chan int32, out chan int32) {
	t := int32(0)
	for i := 0; i < 4; i++ {
		t += <-c
	}
	out <- t
}

var ra chan int32

var rb chan int32

func main() {
	go sink(a, ra)
	go sink(b, rb)
	na, nb := int32(0), int32(0)
	for na+nb < 8 {
		select {
		case a <- na + 1:
			na++
		case b <- nb + 10:
			nb++
		}
	}
	println(<-ra, <-rb, na, nb)
}
`,
		want: "10 46 4 4\n",
	},
	{
		// Nobody ever receives: every pass takes the default, and nothing is sent.
		name: "a non-blocking send with no receiver takes the default",
		src: `var ch chan int32

func main() {
	tried := 0
	sent := 0
	for i := 0; i < 5; i++ {
		select {
		case ch <- int32(i):
			sent++
		default:
			tried++
		}
	}
	println(sent, tried)
}
`,
		want: "0 5\n",
	},
	{
		// A select RECEIVE arm announces itself like a blocking receiver does,
		// which is what lets a gated send in another cog's select see it: two
		// selects pairing, neither blocking.
		name: "two selects pair through the waiting count",
		src: `var ch chan int32

var done chan int32

func consumer() {
	got := int32(0)
	for n := 0; n < 3; {
		select {
		case v := <-ch:
			got += v
			n++
		}
	}
	done <- got
}

func main() {
	go consumer()
	sent := int32(0)
	idle := 0
	for sent < 3 {
		select {
		case ch <- sent + 5:
			sent++
		default:
			idle++
		}
	}
	println(<-done, sent, idle >= 0)
}
`,
		want: "18 3 true\n",
	},
	{
		// The mixed form, ping-ponging with a partner cog: the send arm fires
		// when the partner parks on its receive, the receive arm drains the
		// replies, and the default keeps the loop turning.
		name: "send, receive and default in one select",
		src: `var in chan int32

var out chan int32

func partner() {
	for i := 0; i < 3; i++ {
		out <- <-in * 2
	}
}

func main() {
	go partner()
	sent, got, idle := int32(0), int32(0), 0
	for got != 12 {
		select {
		case in <- sent + 1:
			sent++
		case v := <-out:
			got += v
		default:
			idle++
		}
	}
	println(sent, got, idle > 0)
}
`,
		want: "3 12 true\n",
	},
	{
		// Forwarding a call's results into a VARIADIC callee, Go's special case:
		// the results past the fixed parameters become the pack. All of the pack,
		// a fixed parameter taking the first result, and an EMPTY pack when the
		// results exactly cover the fixed parameters.
		name: "forwarded results feed a variadic pack",
		src: `func two() (int, int) { return 3, 4 }

func three() (int, int, int) { return 5, 6, 7 }

func sum(xs ...int) int {
	t := 0
	for _, v := range xs {
		t += v
	}
	return t
}

func lead(a int, xs ...int) int {
	t := a * 100
	for _, v := range xs {
		t += v
	}
	return t
}

func exact(a int, b int, xs ...int) int { return a*10 + b + len(xs) }

func main() {
	println(sum(two()))
	println(sum(three()))
	println(lead(two()))
	println(lead(three()))
	println(exact(two()))
}
`,
		want: "7\n18\n304\n513\n34\n",
	},
	{
		// fmt applies %q ELEMENT-WISE to a slice: rune-quoted integers with Go's
		// escapes (a tab, an emoji), quoted strings, and [] for an empty one --
		// beside a scalar %q on the same line.
		name: "%q of a slice prints element-wise",
		src: `func main() {
	xs := [3]int32{104, 105, 33}
	printf("%q\n", xs[:])
	ys := [3]int32{104, 9, 128512}
	printf("%q\n", ys[:])
	ss := [2]string{"hi", "a\"b"}
	printf("%q\n", ss[:])
	var empty []int32
	printf("%q\n", empty)
	printf("%q %q\n", xs[:1], "tail")
}
`,
		want: "['h' 'i' '!']\n['h' '\\t' '😀']\n[\"hi\" \"a\\\"b\"]\n[]\n['h'] \"tail\"\n",
	},
	{
		// `type A = B` is another NAME for B, not a type: literals written via the
		// alias, identity in both directions, methods and fields through it, a
		// predeclared target, a defined ARRAY target with its elements and method,
		// an interface target satisfied and called, an alias of an alias, and
		// equality across the two spellings.
		name: "a type alias is another name, not a type",
		src: `type point struct {
	x, y int32
}

func (p point) sum() int32 { return p.x + p.y }

type spot = point

type MyInt = int32

type row [3]int32

func (r row) total() int32 { return r[0] + r[1] + r[2] }

type line = row

type writer interface {
	write(v int32) int32
}

type sink struct {
	n int32
}

func (s *sink) write(v int32) int32 {
	s.n += v
	return s.n
}

type out = writer

type spot2 = spot

func take(p point) int32 { return p.sum() }

func give() spot { return spot{x: 5, y: 6} }

var sk sink

func main() {
	// A literal via the alias, fields, methods, identity both ways.
	s := spot{x: 1, y: 2}
	var p point = s
	println(s.sum(), take(s), p.x)

	// Through a signature, and back.
	g := give()
	println(g.sum())

	// Alias of a predeclared type.
	var m MyInt = 21
	var i int32 = m
	println(m*2, i)

	// Alias of a defined array type: elements and the method.
	var l line
	l[0], l[1], l[2] = 7, 8, 9
	println(l.total(), l[1])

	// Alias of an interface: satisfaction and the call.
	var w out = &sk
	println(w.write(4), w.write(3))

	// An alias of an alias.
	t := spot2{x: 10, y: 20}
	println(t.sum(), s == spot{x: 1, y: 2})
}
`,
		want: "3 3 1\n11\n42 21\n24 8\n4 7\n30 true\n",
	},
	{
		// TYPE declarations inside functions: literals and equality on a local
		// struct, a local scalar's conversion and arithmetic, a local array's
		// elements and len, a local ALIAS of a package type with its methods, a
		// local type SHADOWING a package one, the same name as two types in two
		// functions, and a self-referential local struct linked on the stack.
		name: "types declared inside functions",
		src: `type outer struct {
	n int32
}

func (o outer) twice() int32 { return o.n * 2 }

func pairEq() int32 {
	type pair struct {
		a, b int32
	}
	p := pair{a: 3, b: 4}
	q := pair{a: 3, b: 4}
	if p == q {
		p.b++
	}
	return p.a + p.b
}

func sameName() int32 {
	// The same NAME as pairEq's local type, a different type in a different
	// function.
	type pair struct {
		x int32
	}
	type mint int32
	type row [3]int32

	var r row
	r[0], r[1], r[2] = 5, 6, 7

	v := mint(10)
	w := v * 2

	p := pair{x: int32(w)}
	return p.x + r[2] + int32(len(r))
}

func shadowed() int32 {
	// A local type SHADOWS a package one; the package type comes back after.
	type outer struct {
		m int32
	}
	o := outer{m: 9}
	return o.m
}

func aliased() int32 {
	// A local ALIAS of a package type: the methods come through.
	type big = outer
	b := big{n: 21}
	return b.twice()
}

func linked() int32 {
	// Self-referential local struct through a pointer, linked on the stack.
	type node struct {
		v    int32
		next *node
	}
	c := node{v: 3}
	b := node{v: 2, next: &c}
	a := node{v: 1, next: &b}
	t := int32(0)
	for p := &a; p != nil; p = p.next {
		t = t*10 + p.v
	}
	return t
}

func main() {
	println(pairEq(), sameName(), shadowed(), aliased(), linked())
	o := outer{n: 4}
	println(o.twice())
}
`,
		want: "8 30 9 42 123\n8\n",
	},
	{
		// goto, in the two shapes it is written for: a state machine hopping
		// between labels -- backward and forward, out of a loop mid-iteration --
		// and the error-exit jump to a trailing label past the happy path.
		name: "goto: a state machine and an error exit",
		src: `func machine(start int32) int32 {
	state := start
	acc := int32(0)
idle:
	if state == 0 {
		acc += 1
		state = 1
		goto run
	}
	goto done
run:
	acc += 10
	state = 2
	for i := 0; i < 3; i++ {
		if i == 2 {
			goto flush
		}
		acc += 100
	}
flush:
	acc += 1000
	if state == 2 {
		state = 3
		goto idle
	}
done:
	return acc
}

func errexit(v int32) int32 {
	r := int32(0)
	if v < 0 {
		goto fail
	}
	r = v * 2
	if r > 100 {
		goto fail
	}
	return r
fail:
	return -1
}

func main() {
	println(machine(0), machine(5))
	println(errexit(7), errexit(-3), errexit(60))
}
`,
		want: "1211 0\n14 -1 -1\n",
	},
	{
		// Package variables of COMPOSITE type -- a struct, a slice, an array --
		// whose initializers read other package variables, written out of
		// dependency order. The dependency walk reaches inside every literal, and
		// a slice with non-constant elements is filled at package init (a scalar
		// slice and a struct-element one both go through a temp array and a
		// memcpy) rather than refused. Board-verified against Go.
		name: "composite package variables initialize in dependency order",
		src: `// package vars of COMPOSITE type whose initializers read other package vars,
// written out of dependency order.
type Config struct {
	scale int
	off   int
}

var cfg = Config{scale: base * 2, off: base + 1}
var xs = []int{base, base * 3, tail}
var arr = [3]int{tail, base, tail + base}
var derived = cfg.scale + xs[1] + arr[2]
var base = seed()
var tail = base + 100

func seed() int { return 5 }

func main() {
	println(cfg.scale, cfg.off)
	println(xs[0], xs[1], xs[2])
	println(arr[0], arr[1], arr[2])
	println(derived, base, tail)
}
`,
		want: "10 6\n5 15 105\n105 5 110\n135 5 105\n",
	},
	{
		// A driver shape: init() starts a worker cog, the package configuration it
		// and main share is dependency-ordered (count reads scale reads mkScale
		// reads offset, written out of order), and main consumes what the worker
		// produces over a package channel. It brings the initialization order and
		// the concurrency runtime together on the one program -- the config has to
		// be settled before the cog init() spawns reads it. Board-verified against
		// Go (round 19).
		name: "a driver: init spawns a worker over dependency-ordered config",
		src: `var ch chan int

var scale = mkScale()
var offset = 3
var count = scale - 4

func mkScale() int { return offset*2 + 2 }

func producer() {
	for i := 0; i < count; i++ {
		ch <- i*scale + offset
	}
}

func init() {
	go producer()
}

func main() {
	sum := 0
	for i := 0; i < count; i++ {
		v := <-ch
		sum += v
	}
	println(sum, scale, offset, count)
}
`,
		want: "60 8 3 4\n",
	},
	{
		// A multi-value initializer -- `var a, b = f()` -- is ordered exactly as a
		// single-variable one: the group runs after what f's body reads (w), a
		// variable reading the group's names runs after it (c), and an all-blank
		// `var _, _ = mark()` still orders after the variable its callee writes
		// (trail). The group's steps used to carry no dependencies and no targets
		// at all, so f ran against a zero w and c floated above the group.
		// Board-verified against Go before it was pinned.
		name: "a multi-value initializer orders with the rest",
		src: `func f() (int, int) { return w + 1, w * 2 }

func g() int { return 10 }

func mark() (int, int) {
	trail = trail + 5
	return 0, 0
}

var a, b = f()
var w = g()
var c = a + b
var _, _ = mark()
var trail = zero()

func zero() int { return 0 }

func main() { println(a, b, w, c, trail) }
`,
		want: "11 20 10 31 5\n",
	},
	{
		// The dependency behind the initialization order runs through CODE, as
		// Go's does: through a function's body (a via f), a method's (m1 via
		// T.m, on a receiver that is itself a step), a write inside a callee
		// (setW ordering a2 after w), and past a block-scoped shadow that must
		// not hide the later read (blockShadow). The case above pins the
		// variable-to-variable half; this one pins the half that reads bodies.
		// Board-verified against Go before it was pinned.
		name: "initialization order runs through functions and methods",
		src: `type T struct{ n int }

func (t T) m() int { return b * t.n }

func f() int { return b * 2 }

func setW() int {
	w = 40
	return 4
}

func blockShadow() int {
	{
		b := 100
		_ = b
	}
	return b + 1
}

func mkT() T { return T{n: c} }

func gg() int { return 3 }

var a = f()
var b = gg()
var m1 = q.m()
var q = mkT()
var c = 5 + b
var s = blockShadow()
var a2 = setW()
var w = 10

func main() {
	println(a, b, m1, q.n, c, s, a2, w)
}
`,
		want: "6 3 24 8 8 4 4 40\n",
	},
	{
		// Round-18 probe, clean on the board: append-with-CRC into a byte arena, a
		// byte-by-byte scan recovery that steps over corruption and keeps the
		// highest sequence, the arena-full path through a goto, and the
		// array-field struct passed the way this target takes it -- by pointer,
		// with recovery through an out parameter.
		name: "an EEPROM-style record journal",
		src: `// Round 18: an EEPROM-style record journal over a byte array. Records are
// appended with a magic byte, a sequence number, a length and a CRC; recovery
// scans the whole arena and keeps the highest-sequence valid record, stepping
// over corruption byte by byte, the way real log recovery does.

const arenaSize = 128

var arena [arenaSize]byte

var writePos int

func crc8(bs []byte) byte {
	c := byte(0)
	for i := 0; i < len(bs); i++ {
		c ^= bs[i]
		for b := 0; b < 8; b++ {
			if c&0x80 != 0 {
				c = c<<1 ^ 0x31
			} else {
				c <<= 1
			}
		}
	}
	return c
}

type record struct {
	seq  byte
	vals [3]byte
}

// appendRecord writes [0xA5 seq len v0 v1 v2 crc]; false when the arena is full.
func appendRecord(r *record) bool {
	need := 4 + len(r.vals)
	if writePos+need > arenaSize {
		goto full
	}
	arena[writePos] = 0xa5
	arena[writePos+1] = r.seq
	arena[writePos+2] = byte(len(r.vals))
	for i := 0; i < len(r.vals); i++ {
		arena[writePos+3+i] = r.vals[i]
	}
	arena[writePos+need-1] = crc8(arena[writePos : writePos+need-1])
	writePos += need
	return true
full:
	return false
}

// recoverBest walks the arena and leaves the highest-sequence valid record in
// out, reporting whether any was found.
func recoverBest(out *record) bool {
	found := false
	i := 0
	for i < arenaSize {
		if arena[i] != 0xa5 {
			i++
			continue
		}
		if i+3 > arenaSize {
			break
		}
		n := int(arena[i+2])
		total := 4 + n
		if n != 3 || i+total > arenaSize {
			i++
			continue
		}
		if crc8(arena[i:i+total-1]) != arena[i+total-1] {
			i++
			continue
		}
		if !found || arena[i+1] >= out.seq {
			out.seq = arena[i+1]
			for k := 0; k < 3; k++ {
				out.vals[k] = arena[i+3+k]
			}
			found = true
		}
		i += total
	}
	return found
}

func main() {
	r1 := record{seq: 1, vals: [3]byte{10, 20, 30}}
	r2 := record{seq: 2, vals: [3]byte{40, 50, 60}}
	r3 := record{seq: 3, vals: [3]byte{70, 80, 90}}
	ok1 := appendRecord(&r1)
	ok2 := appendRecord(&r2)
	ok3 := appendRecord(&r3)
	println(ok1, ok2, ok3, writePos)

	// Corrupt the LATEST record's payload; recovery must fall back to seq 2.
	arena[writePos-3] ^= 0xff
	var r record
	found := recoverBest(&r)
	println(found, r.seq, r.vals[0], r.vals[1], r.vals[2])

	// Fill the arena to the brim; the append that no longer fits says so.
	n := 0
	for {
		rn := record{seq: byte(4 + n), vals: [3]byte{byte(n), 0, 0}}
		if !appendRecord(&rn) {
			break
		}
		n++
	}
	println(n, writePos)

	// A fresh valid record wins recovery again.
	arena[0] = 0
	var rr record
	found2 := recoverBest(&rr)
	println(found2, rr.seq)
}
`,
		want: "true true true 21\ntrue 2 40 50 60\n15 126\ntrue 18\n",
	},
	{
		// The digital input path: an insertion-sorted median window (an ARRAY
		// copied into a scratch array and sorted), a two-threshold trigger with
		// edge counting, deterministic spike noise that never survives the median.
		name: "median-of-5 and a hysteresis trigger",
		src: `// Round 18b: sensor conditioning -- a median-of-5 window over a noisy ramp, a
// hysteresis threshold on the filtered value, and edge counting. The shape of
// every digital input path.

var window [5]int32

var wn int

func push(v int32) int32 {
	window[wn%5] = v
	wn++
	if wn < 5 {
		return v
	}
	var s [5]int32
	s = window
	for i := 1; i < 5; i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s[2]
}

type trigger struct {
	high, low int32
	on        bool
	edges     int32
}

func (t *trigger) feed(v int32) {
	if !t.on && v >= t.high {
		t.on = true
		t.edges++
	} else if t.on && v <= t.low {
		t.on = false
	}
}

func noise(i int32) int32 {
	// A deterministic "noise": big spikes every 7th sample.
	if i%7 == 3 {
		return 500
	}
	if i%7 == 5 {
		return -400
	}
	return (i % 3) - 1
}

func main() {
	t := trigger{high: 60, low: 40}
	sum := int32(0)
	for i := int32(0); i < 60; i++ {
		raw := i*2 + noise(i)
		f := push(raw)
		t.feed(f)
		sum += f
	}
	println(sum, t.edges, t.on)

	// The spikes never made it through the median.
	println(push(1000), push(1001))
}
`,
		want: "3816 2 true\n116 618\n",
	},
	{
		name: "a struct packaging a bank of channels",
		src: `const nw = 3

type bank struct {
	q    [nw]chan int32
	out  chan int32
	name string
}

var b bank

func worker(id int32) {
	// A field element bound to a name, which is how a driver reads once the
	// channel it serves is picked by index.
	in := b.q[id]
	for i := 0; i < 2; i++ {
		v := <-in
		b.out <- v*10 + id
	}
}

func main() {
	b.name = "bank"
	for i := int32(0); i < nw; i++ {
		go worker(i)
	}
	sum := int32(0)
	for round := int32(0); round < 2; round++ {
		for i := int32(0); i < nw; i++ {
			b.q[i] <- 1 + i + round*10
		}
		for i := int32(0); i < nw; i++ {
			sum += <-b.out
		}
		println("round", round, sum)
	}
	println(b.name, sum)
}
`,
		want: "round 0 63\nround 1 426\nbank 426\n",
	},
	{
		name: "a bank of channels, one per worker",
		src: `const nw = 3

type req struct {
	op int32
	a  int32
	b  int32
}

var q [nw]chan req
var reply chan int32

func apply(r req) int32 {
	if r.op == 0 {
		return r.a + r.b
	}
	return r.a * r.b
}

func worker(id int32) {
	// The element is bound to a name and worked through, which is how a driver
	// reads once the channel it serves is picked by index.
	in := q[id]
	for i := 0; i < 2; i++ {
		r := <-in
		reply <- apply(r) + id*100
	}
}

func main() {
	for i := int32(0); i < nw; i++ {
		go worker(i)
	}
	sum := int32(0)
	for round := int32(0); round < 2; round++ {
		// Every worker is given one request, then every reply is taken. The
		// replies arrive in whatever order the cogs get there, so the SUM is what
		// this can be checked by.
		for i := int32(0); i < nw; i++ {
			q[i] <- req{op: round, a: 10 + i, b: 3}
		}
		for i := int32(0); i < nw; i++ {
			sum += <-reply
		}
		println("round", round, sum)
	}
	// A LOCAL array of channels owns a cell per element too, on the same rule: the
	// declaration owns it.
	var local [2]chan int32
	go pair(local[0], local[1])
	local[0] <- 4
	println("local", <-local[1])
	println("sum", sum)
}

func pair(in chan int32, out chan int32) { out <- <-in * 5 }
`,
		want: "round 0 342\nround 1 741\nlocal 20\nsum 741\n",
	},
	{
		name: "a channel bound to a name is that channel",
		src: `type ports struct {
	tx chan int32
	rx chan int32
}

var up chan int32
var down chan int32
var p ports

func echo() {
	// A driver binds the package cells to locals and works through those, which
	// is the ordinary shape when the channel is a global.
	in := up
	out := down
	for i := 0; i < 3; i++ {
		v := <-in
		out <- v * 10
	}
}

func relay() {
	c := p.tx
	d := p.rx
	for i := 0; i < 2; i++ {
		d <- <-c + 1
	}
}

func main() {
	p.tx = up
	p.rx = down
	go echo()
	send := up
	recv := down
	for i := int32(1); i <= 3; i++ {
		send <- i
		println("echo", i, <-recv)
	}
	go relay()
	for i := int32(7); i <= 8; i++ {
		p.tx <- i
		println("relay", i, <-p.rx)
	}
	x := up
	go relay2(x)
	x <- 4
	println("sel", <-down)
}

func relay2(c chan int32) {
	v := <-c
	down <- v + 100
}
`,
		want: "echo 1 10\necho 2 20\necho 3 30\nrelay 7 8\nrelay 8 9\nsel 104\n",
	},
	{
		name: "a print evaluates every argument before it writes anything",
		src: `var ch chan int32

func f(n int32) int32 {
	println("  side", n)
	return n * 2
}

func worker() { ch <- 7 }

func main() {
	println("A", f(1))
	println("B", f(2), f(3))
	print("C", f(4))
	println()
	printf("D %d %d\n", f(5), f(6))
	// The receive is an argument too: nothing may be written before it completes,
	// which on hardware is the difference between a line and a hang mid-line.
	go worker()
	println("E", <-ch)
}
`,
		want: "  side 1\nA 2\n  side 2\n  side 3\nB 4 6\n  side 4\nC8\n" +
			"  side 5\n  side 6\nD 10 12\nE 7\n",
	},
	{
		name: "a deferred builtin reads its captured arguments",
		src: `var a [2]int
var b [2]int
var c [2]int

func f() {
	xs := a[:]
	ys := b[:]
	zs := c[:]
	// The arguments are evaluated HERE, so the deferred copy must use b's header
	// and not the one ys holds at the return.
	defer copy(xs, ys)
	ys = zs
	println(xs[0], xs[1], ys[0], ys[1])
}

func g() {
	xs := a[:]
	defer clear(xs)
	println(xs[0], xs[1])
}

func main() {
	a[0], a[1] = 1, 2
	b[0], b[1] = 3, 4
	c[0], c[1] = 9, 9
	f()
	println(a[0], a[1])
	g()
	println(a[0], a[1])
}
`,
		want: "1 2 9 9\n3 4\n3 4\n0 0\n",
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
		// A struct returned through a function POINTER, on every path one can be
		// called through: an interface's slot, a function value in a variable, in a
		// struct field, from a call (`pick()(3)`), and a method value -- used, thrown
		// away, and carried on by a chain (`st.Process(f).val`), in a loop's
		// condition, and on a spawned cog. That last one is the shape that found
		// it: the target's C compiler miscompiles a struct WITH PADDING returned
		// through a pointer on a cog, and Frame is one (int, int, byte). A method
		// of several results had long been routed around it, through a trailing
		// out parameter; a single struct result took the raw path and a
		// three-cog pipeline printed nothing at all. Every struct result travels
		// through the parameter now (outResultOf); the direct call is unchanged.
		name: "a struct result through a function pointer",
		src: `type Frame struct {
	seq int
	val int
	tag byte
}

type Stage interface{ Process(f Frame) Frame }

type Scale struct{ k int }

func (s *Scale) Process(f Frame) Frame { f.val *= s.k; return f }

var sc = Scale{3}
var st Stage = &sc

func build(n int) Frame { return Frame{n, n * n, 'x'} }

// Big is a 64-bit result off what a function VALUE returns: mk(7).Big() is a
// chain whose head is a call through a variable, which the chain typer did not
// walk, so a println of it printed the int64 in halves.
func (f Frame) Big() int64 { return int64(f.val) * 1000000000 }

type Holder struct{ fn func(int) Frame }

type Maker func(int) Frame

func pick() func(int) Frame { return build }

func worker(out chan Frame) {
	for i := 0; i < 3; i++ {
		out <- st.Process(build(i + 1))
	}
}

func main() {
	var ch chan Frame
	go worker(ch)
	for i := 0; i < 3; i++ {
		f := <-ch
		println(f.seq, f.val, string(rune(f.tag)))
	}
	println(st.Process(build(4)).val, st.Process(build(5)).seq)
	st.Process(build(6))
	mk := build
	println(mk(7).val, mk(7).Big(), mk(2).Big() > 1<<32)
	mk(8)
	var named Maker = build
	println(named(3).Big(), named(4).val)
	h := Holder{build}
	println(h.fn(9).val)
	println(pick()(10).val)
	step := sc.Process
	println(step(build(11)).val)
	g := step(build(12))
	println(g.seq, g.val)
	for i := 0; i < 2; i++ {
		if st.Process(build(i)).val > 0 {
			println("positive", i)
		}
	}
}
`,
		want: "1 3 x\n2 12 x\n3 27 x\n48 5\n49 49000000000 false\n9000000000 16\n81\n100\n363\n12 432\npositive 1\n",
	},
	{
		// The C undefined-behaviour boundaries that the emitter lowers through
		// GUARDED helpers rather than the bare operator: a shift count at or past
		// the operand width (Go shifts all bits out -- 0 for <<, 0 or -1 for a
		// signed >>; C leaves it undefined and x86 would mask the count), driven
		// by a RUNTIME variable so it reaches the helper's width branch, and
		// INT_MIN / -1 and INT_MIN % -1 (an overflowing divide, UB in C, defined
		// wrapping in Go). Board-verified identical to Go, which is what exercises
		// the ogo_shl/ogo_shr/ogo_div branches a constant fold would never reach.
		name: "shift counts past the width, and the minimum over minus one",
		src: `func main() {
	// Shift counts at and beyond the operand width, via RUNTIME variables so the
	// compiler cannot fold them. Go defines these fully (count >= width -> all bits
	// out); C leaves shift-by->=width UNDEFINED.
	var s32 uint = 32
	var s40 uint = 40
	var x int32 = 1
	var y int32 = -1
	println(int32(x<<s32), int32(x<<s40), int32(y>>s32), int32(y>>s40))
	var ux uint32 = 0xFFFFFFFF
	println(uint32(ux<<s32), uint32(ux>>s32), uint32(ux<<s40))
	var s64 uint = 64
	var s70 uint = 70
	var b int64 = 1
	var nb int64 = -1
	println(int64(b<<s64), int64(nb>>s64), int64(b<<s70))
	// INT_MIN / -1 and INT_MIN % -1: overflow, UB in C, defined-wrapping in Go.
	var mn int32 = -2147483648
	var neg int32 = -1
	println(int32(mn/neg), int32(mn%neg))
	var mn64 int64 = -9223372036854775808
	var neg64 int64 = -1
	println(int64(mn64/neg64), int64(mn64%neg64))
}
`,
		want: "0 0 -1 -1\n0 0 0\n0 -1 0\n-2147483648 0\n-9223372036854775808 0\n",
	},
	{
		// An untyped constant shifted by a count that is not constant takes the type
		// of where the shift stands -- a declaration, a store through a field, an
		// element or a chain, an argument, a variadic pack, an append, a result, a
		// conversion, an interface method's parameter, min/max, a switch case, and
		// the typed operand beside it -- and defaults to int where nothing gives it
		// one. The count's type is never it. Every one of these computed a 32-bit 1,
		// or one of the COUNT's type: all but the explicitly converted spelling
		// printed 0 for 1 << 40, and `v := 1 << c` for a uint c made v a uint.
		// Go's answers, board-verified.
		name: "an untyped constant shifted takes its type from where it stands",
		src: `type Mask uint64

type P struct {
	a int64
	m Mask
}

type Inner struct{ m uint64 }

type Outer struct {
	in  Inner
	arr [2]int64
}

type Box struct{ v int64 }

func (b *Box) Set(v int64) { b.v = v }

type Setter interface{ Set(v int64) }

var s uint = 40

var g int64 = 1 << s

func take(v int64) int64 { return v }

func sum(xs ...int64) int64 {
	t := int64(0)
	for _, x := range xs {
		t += x
	}
	return t
}

func ret(n uint) int64 { return 1 << n }

func decls() {
	var a, b int64 = 1 << s, 2 << s
	var m Mask = 1 << s
	m |= 1 << (s + 1)
	var d int64 = 1.0 << s
	var u uint64 = ^(1 << s)
	var n int64 = -(1 << s)
	var t uint = 31
	var w uint32 = (1 << t) >> 30
	println(g, a, b, m, d, u, n, w)
}

func stores() {
	var p P
	p.a = 1 << s
	p.m |= 1 << s
	pp := &p
	pp.a += 1 << s
	var arr [2]uint64
	arr[1] |= 1 << s
	var o [2]Outer
	o[1].in.m = 1 << s
	o[1].arr[0] |= 1 << (s + 2)
	var x, y int64
	x, y = 1<<s, 2<<s
	println(p.a, p.m, arr[1], o[1].in.m, o[1].arr[0], x, y)
}

func calls() {
	var b Box
	var st Setter = &b
	st.Set(1 << s)
	var back [2]int64
	xs := back[:0]
	xs = append(xs, 1<<s)
	var i64 int64 = 7
	println(take(1<<s), sum(1<<s, 2<<s), ret(s), int64(1<<s), b.v, xs[0], max(i64, 1<<s))
}

func partners() {
	var x int64 = 5
	var b uint8
	var t uint = 8
	println(x+1<<s, 1<<s+x, x&(1<<s) != 0, x == 1<<s, b == 1<<t)
	switch x + 1<<s {
	case 1<<s + 5:
		println("case")
	}
	if x < 1<<s {
		println("less")
	}
	lit := []int64{1 << s, 2 << s}
	println(lit[1], 1<<s*x)
}

func defaults() {
	var c uint = 31
	var c8 int8 = 31
	v := 1 << c
	w := 1 << c8
	println(v, w, v < 0, (1<<c)/3)
}

func main() {
	decls()
	stores()
	calls()
	partners()
	defaults()
}
`,
		want: "1099511627776 1099511627776 2199023255552 3298534883328 1099511627776 18446742974197923839 -1099511627776 2\n2199023255552 1099511627776 1099511627776 1099511627776 4398046511104 1099511627776 2199023255552\n1099511627776 3298534883328 1099511627776 1099511627776 1099511627776 1099511627776 1099511627776\n1099511627781 1099511627781 false false true\ncase\nless\n2199023255552 5497558138880\n-2147483648 -2147483648 true -715827882\n",
	},
	{
		// Probe round 20, the bit-packing half: telemetry fields of odd widths packed
		// LSB-first into uint64 words by two writers that must agree, read back with
		// sign extension, and checksummed. Every bit position past 31 is an untyped
		// constant shifted in a uint64 context -- `v&(1<<i)`, `words[w] |= 1 << off`
		// -- which is how a register or a frame is written, and before the shift
		// typing fix both writers dropped those bits. Go's answers, board-verified.
		name: "probe round 20: bit-packed telemetry",
		src: `// Bit-packed telemetry: samples of odd widths packed LSB-first into uint64 words,
// two writers (a bit-at-a-time one and a masked one) that must agree, a reader
// that sign-extends signed fields, and a rotate-xor checksum over the words.

type field struct {
	width  uint
	signed bool
}

var layout = [6]field{{12, false}, {9, true}, {1, false}, {17, true}, {25, false}, {63, true}}

type packer struct {
	words [8]uint64
	bit   uint
}

func (p *packer) putSlow(v uint64, width uint) {
	for i := uint(0); i < width; i++ {
		if v&(1<<i) != 0 {
			p.words[p.bit/64] |= 1 << (p.bit % 64)
		}
		p.bit++
	}
}

func (p *packer) putFast(v uint64, width uint) {
	if width < 64 {
		v &= 1<<width - 1
	}
	off := p.bit % 64
	w := p.bit / 64
	p.words[w] |= v << off
	if off+width > 64 {
		p.words[w+1] |= v >> (64 - off)
	}
	p.bit += width
}

type reader struct {
	words *[8]uint64
	bit   uint
}

func (r *reader) get(width uint) uint64 {
	var v uint64
	for i := uint(0); i < width; i++ {
		if r.words[r.bit/64]&(1<<(r.bit%64)) != 0 {
			v |= 1 << i
		}
		r.bit++
	}
	return v
}

func signExtend(v uint64, width uint) int64 {
	shift := 64 - width
	return int64(v<<shift) >> shift
}

func rotl(x uint64, k uint) uint64 { return x<<k | x>>(64-k) }

func checksum(words *[8]uint64, n int) uint64 {
	var h uint64 = 0xcbf29ce484222325
	for i := 0; i < n; i++ {
		h = rotl(h^words[i], 13) * 0x100000001b3
	}
	return h
}

var samples = [4][6]int64{
	{4095, -256, 1, -65536, 33554431, -4611686018427387904},
	{0, 255, 0, 65535, 1, 4611686018427387903},
	{1234, -1, 1, -1, 16777216, -1},
	{2048, 100, 0, 12345, 999999, 123456789012345},
}

func main() {
	var slow, fast packer
	for _, s := range samples {
		for i, f := range layout {
			slow.putSlow(uint64(s[i]), f.width)
			fast.putFast(uint64(s[i]), f.width)
		}
	}
	println(slow.bit, fast.bit, slow.words == fast.words)
	used := int((slow.bit + 63) / 64)
	for i := 0; i < used; i++ {
		printf("%016x\n", fast.words[i])
	}
	printf("sum %016x\n", checksum(&fast.words, used))
	r := reader{words: &fast.words}
	bad := 0
	for _, s := range samples {
		for i, f := range layout {
			v := r.get(f.width)
			var got int64
			if f.signed {
				got = signExtend(v, f.width)
			} else {
				got = int64(v)
			}
			if got != s[i] {
				bad++
				println("mismatch", i, got, s[i])
			}
		}
	}
	println("bad", bad)
	var flags uint64
	for _, n := range []uint{0, 31, 32, 33, 62, 63} {
		flags |= 1 << n
	}
	printf("%016x %v %v\n", flags, flags&(1<<32) != 0, flags&(1<<34) != 0)
	flags &^= 1 << 63
	printf("%016x %d\n", flags, int64(flags)>>60)
}
`,
		want: "508 508 true\nffffffc000300fff\n4000000000000000\n8000005fffe7f800\n9fffffffffffffff\ne000001ffffffd34\n1fffffffffffffff\n20f423f181c8c900\n00000e0910c1bbef\nsum 817feaac2f699df1\nbad 0\nc000000380000001 true false\n4000000380000001 4\n",
	},
	{
		// Probe round 20, the fixed-point half: Q32.32 over int64 with a 128-bit
		// product from 32-bit halves, a bit-at-a-time division, a digit-recurrence
		// square root and integer-only decimal rendering -- a calibration whose 16
		// fraction bits are too few. The division sets quotient bits with `q |= 1 <<
		// uint(i)` for a uint64 q, and before the shift typing fix lost every one past
		// bit 31: 355/113 divided by -6.75 came out -0.020976728. Go's answers,
		// board-verified.
		name: "probe round 20: Q32.32 fixed point",
		src: `// Q32.32 fixed point over int64: a full 128-bit product built from 32-bit halves,
// a bit-at-a-time division, a square root by digit recurrence, and decimal
// rendering through integer arithmetic only -- the arithmetic a torque calibration
// or a PI loop with a wide integrator does when 16 fraction bits are too few.

type Q32 int64

const qOne Q32 = 1 << 32

func fromInt(i int32) Q32 { return Q32(i) << 32 }

func fromRatio(n, d int32) Q32 { return Q32(int64(n)<<32) / Q32(d) }

func mulU(a, b uint64) (hi, lo uint64) {
	aHi, aLo := a>>32, a&0xffffffff
	bHi, bLo := b>>32, b&0xffffffff
	ll := aLo * bLo
	lh := aLo * bHi
	hl := aHi * bLo
	hh := aHi * bHi
	mid := ll>>32 + lh&0xffffffff + hl&0xffffffff
	lo = ll&0xffffffff | mid<<32
	hi = hh + lh>>32 + hl>>32 + mid>>32
	return hi, lo
}

func (a Q32) Mul(b Q32) Q32 {
	neg := (a < 0) != (b < 0)
	ua, ub := uint64(a), uint64(b)
	if a < 0 {
		ua = uint64(-a)
	}
	if b < 0 {
		ub = uint64(-b)
	}
	hi, lo := mulU(ua, ub)
	r := hi<<32 | lo>>32
	if neg {
		return -Q32(r)
	}
	return Q32(r)
}

func (a Q32) Div(b Q32) Q32 {
	neg := (a < 0) != (b < 0)
	n, d := uint64(a), uint64(b)
	if a < 0 {
		n = uint64(-a)
	}
	if b < 0 {
		d = uint64(-b)
	}
	// (n << 32) / d, one quotient bit at a time over the 96-bit dividend.
	var q, rem uint64
	for i := 95; i >= 0; i-- {
		rem <<= 1
		if i >= 32 && n&(1<<uint(i-32)) != 0 {
			rem |= 1
		}
		if rem >= d {
			rem -= d
			if i < 64 {
				q |= 1 << uint(i)
			}
		}
	}
	if neg {
		return -Q32(q)
	}
	return Q32(q)
}

func (a Q32) Sqrt() Q32 {
	// Digit recurrence over the 64-bit radicand scaled by 2^32, so the root is Q32.
	hi, lo := uint64(a)>>32, uint64(a)<<32
	var root, rem uint64
	for i := 0; i < 64; i++ {
		rem = rem<<2 | hi>>62
		hi = hi<<2 | lo>>62
		lo <<= 2
		root <<= 1
		test := root<<1 | 1
		if rem >= test {
			rem -= test
			root |= 1
		}
	}
	return Q32(root)
}

func show(label string, a Q32) {
	neg := a < 0
	if neg {
		a = -a
	}
	whole := int64(a >> 32)
	frac := uint64(a) & 0xffffffff
	// Nine decimal digits of the fraction, truncated.
	digits := uint64(0)
	for i := 0; i < 9; i++ {
		frac *= 10
		digits = digits*10 + frac>>32
		frac &= 0xffffffff
	}
	sign := ""
	if neg {
		sign = "-"
	}
	printf("%s %s%d.%09d\n", label, sign, whole, digits)
}

func main() {
	a := fromRatio(355, 113)
	b := fromInt(-7) + qOne/4
	show("a", a)
	show("b", b)
	show("a*b", a.Mul(b))
	show("a/b", a.Div(b))
	show("b/a", b.Div(a))
	show("sqrt2", fromInt(2).Sqrt())
	show("sqrtA", a.Sqrt())
	big := fromInt(40000)
	show("big*big/big", big.Mul(big).Div(big))
	var acc Q32
	gain := fromRatio(1, 3)
	for i := int32(1); i <= 50; i++ {
		acc += fromInt(i).Mul(gain)
	}
	show("acc", acc)
	hi, lo := mulU(0xffffffffffffffff, 0xfffffffffffffffe)
	printf("%016x %016x\n", hi, lo)
	printf("%016x\n", uint64(a.Mul(a).Sqrt()))
}
`,
		want: "a 3.141592920\nb -6.750000000\na*b -21.205752211\na/b -0.465421173\nb/a -2.148591549\nsqrt2 1.414213562\nsqrtA 1.772453926\nbig*big/big 40000.000000000\nacc 424.999999901\nfffffffffffffffd 0000000000000002\n00000003243f6f01\n",
	},
	{
		// Signed overflow wraps (two's complement) at EVERY width, as Go defines
		// and the P2 does -- add, subtract, multiply, shift and negate, driven
		// past the boundary of int8/int16/int32/int64 and their unsigned twins.
		// The sub-32-bit types are where the target has to truncate after each
		// operation (its word is 32-bit), which is where a narrowing fault would
		// show. Board-verified identical to Go; the host shim models it -fwrapv.
		name: "signed overflow wraps at every width",
		src: `func main() {
	// int8 overflow: add, sub, mul, shift, negate at the boundary
	var a8 int8 = 127
	var b8 int8 = 100
	println(int8(a8+1), int8(b8*2), int8(a8<<1), int8(-a8), int8(a8-(-a8)))
	var c8 int8 = -128
	println(int8(c8-1), int8(c8*(-1)), int8(c8<<1))
	// uint8 wrap (defined mod 256)
	var u8 uint8 = 255
	println(uint8(u8+1), uint8(u8*2), uint8(u8<<1), uint8(u8+u8))
	// int16
	var a16 int16 = 32767
	var b16 int16 = 30000
	println(int16(a16+1), int16(b16*2), int16(a16<<1), int16(-a16))
	var u16 uint16 = 65535
	println(uint16(u16+1), uint16(u16*3), uint16(u16<<2))
	// int32
	var a32 int32 = 2147483647
	var b32 int32 = 100000
	println(int32(a32+1), int32(b32*b32), int32(a32<<1), int32(-a32))
	var u32 uint32 = 4294967295
	println(uint32(u32+1), uint32(u32*7), uint32(u32<<3))
	// int64
	var a64 int64 = 9223372036854775807
	var b64 int64 = 3037000500
	println(int64(a64+1), int64(b64*b64), int64(a64<<1), int64(-a64))
	var u64 uint64 = 18446744073709551615
	println(uint64(u64+1), uint64(u64*5), uint64(u64<<4))
}
`,
		want: "-128 -56 -2 -127 -2\n127 -128 0\n0 254 254 254\n-32768 -5536 -2 -32767\n0 65533 65532\n-2147483648 1410065408 -2 -2147483647\n0 4294967289 4294967288\n-9223372036854775808 -9223372036709301616 -2 -9223372036854775807\n0 18446744073709551611 18446744073709551600\n",
	},
	{
		// Signed integer overflow is two's-complement WRAPPING in Go, and on the
		// P2 (its soft 64-bit routines wrap, verified on the board). An
		// overflowing int64 product feeding a modulo -- `z * K % 3` -- is where
		// that matters: the wrapped product's remainder, not a saturated or
		// undefined one. The host shim is compiled -fwrapv to model the target
		// (C leaves the overflow undefined, and the host gcc otherwise folds a
		// different answer); this pins that host and board agree with Go. Found
		// by the smith oracle at a 2000-seed sweep (seeds 964, 1398).
		name: "an overflowing int64 product feeds a modulo",
		src: `type D int64

func main() {
	var z D = 9223372036854775807
	z &= -411001605719289423
	// The int64 product overflows, which Go defines as two's-complement
	// wrapping; the modulo of the wrapped product is what must survive.
	println(int64(z*6929118014461741803%3), int64((z*6929118014461741803)%3))
	var w int64 = 8812370431135486385
	println(int64(w * 6929118014461741803 % 3))
	// A pure overflowing sum and product, each folded into a small modulus.
	var a int64 = 9000000000000000000
	println(int64((a + a) % 7))
	println(int64((a * a) % 5))
}
`,
		want: "2 2\n2\n-5\n-3\n",
	},
	{
		// A 64-bit result that is not a plain variable is returned through a
		// temporary. The target's C compiler returns a garbage high word for
		// `return (int64_t)n;` -- a widening conversion as the whole operand,
		// signed or unsigned, however nested, at every optimization level -- while
		// `int64_t r = n; return r;` is right, and so is the same conversion inside
		// a larger expression (doc/return-widening-cast.c). fib(20) printed
		// 282076272138861 for 6765 on the board, found by a driver probe diffed
		// against Go: the base case of every recursive function that widens its
		// argument is written `return int64(n)`. The host has never had the fault,
		// so the board is what this case is for; every line matches real Go.
		name: "a 64-bit result returned through a temporary",
		src: `type Ticks int64

func wide(n int) int64 { return int64(n) }

func wideU(n uint8) uint64 { return uint64(n) }

func chain(n int) int64 { return int64(int32(n)) }

func ticks(n int) Ticks { return Ticks(n) * 1000 }

func neg(r int64) int64 { return -r }

func sum(a, b int64) int64 { return a + b }

func big(v int) int64 { return int64(v) * 1000000000 }

func viaVar(n int) int64 {
	r := int64(n)
	return r
}
func fib(n int) int64 {
	if n < 2 {
		return int64(n)
	}
	a := n - 1
	b := n - 2
	return fib(a) + fib(b)
}

func pair(n int) (int64, int) { return int64(n), n }

func main() {
	println(wide(5), wide(-3), wideU(200), chain(-7), int64(ticks(3)))
	println(neg(wide(9)), sum(wide(1), wide(2)), big(3), viaVar(-7))
	println(fib(20), fib(30))
	a, b := pair(-4)
	var c int64 = wide(7)
	println(a, b, c+1, wide(3)*wide(4), sum(1<<40, 1))
}
`,
		want: "5 -3 200 -7 3000\n-9 3 3000000000 -7\n6765 832040\n-4 -4 8 12 1099511627777\n",
	},
	{
		// A go statement COPIES an array argument, as Go does at the go statement:
		// the slot held a pointer to the caller's array before, so a write after
		// the go statement reached the cog (109 for Go's 10) and a caller returning
		// first left the cog a dangling frame. The goroutine waits on a gate so the
		// caller's write is certain to come first; an array literal, which did not
		// compile as a go argument at all, and a defined two-dimensional array
		// type take the same path. Every line matches real Go.
		name: "a go statement copies an array argument",
		src: `type Grid [2][3]int

func worker(ids [4]int, gate chan int, out chan int) {
	<-gate
	s := 0
	for _, v := range ids {
		s += v
	}
	out <- s
}

func rows(g Grid, out chan int) {
	out <- g[0][0]*100 + g[1][2]
}

var gate chan int
var ch chan int

func main() {
	arr := [4]int{1, 2, 3, 4}
	go worker(arr, gate, ch)
	arr[0] = 100
	gate <- 1
	println(<-ch)
	go worker([4]int{5, 6, -2, 101}, gate, ch)
	gate <- 1
	println(<-ch)
	var g Grid
	g[0][0] = 7
	g[1][2] = 9
	go rows(g, ch)
	g[1][2] = 0
	println(<-ch)
}
`,
		want: "10\n110\n709\n",
	},
	{
		// Two rewrites in the target compiler's optimizer disturbed the carry a
		// 64-bit add or subtract reads: an ADD of a small negative immediate was
		// exchanged for a SUB (the carry inverted), and two immediate adds of one
		// value were merged with the first one's carry still read. On the board
		// every line came out wrong, each wrong value off by 2^32 or twice that --
		// `z + -1` was -4293967297, `z + y + 1000 + 2000` for -1 and 1 was
		// 4294970296 -- from v0.34.0, when ogo build stopped passing the
		// -Ono-inline-small that had hidden both. The backend carries the fix
		// (internal/optimize_ir.c.diff, flexprop#109, doc/add-immediate-carry.c);
		// this is what fails if a regeneration loses it. Found by the fuzzer's
		// seed 111 on the board. Every line matches Go.
		name: "a 64-bit addition or subtraction of a constant keeps its carry",
		src: `func id(v int64) int64 { return v }

func idu(v uint64) uint64 { return v }

const K = -1

const Mask32 = 0xFFFFFFFF

func chain(z, y int64) int64 { return z + y + 1000 + 2000 }

func ones(z, y int64) int64 { return z + y + 1 + 1 }

func down(z, y uint64) uint64 { return z + y - 5 - 7 }

func accumulate(a, b int64) int64 {
	d := a + b
	d += 10
	d += 20
	return d
}

func main() {
	z := id(1000000)
	println(z+-1, z - -1, z+(-5), z+0xFFFFFFFF, z-0xFFFFFFFF, z+4294967040)
	println(z+K, z-K, z+Mask32, z-Mask32)
	w := z
	w += -1
	println(w)
	w = z
	w -= -300
	println(w)
	u := idu(12345)
	println(u+0xFFFFFFFF, u-0xFFFFFF00, u+18446744073709551615, u-18446744073709551615)
	println(z+3000000000+3000000000, z-1-4294967296, z+2000000000+2000000000)
	var arr [2]int64
	arr[1] = z
	arr[1] += 0xFFFFFFFF
	println(arr[1])
	n := id(-31)
	println(n+4492952664366759589-6812432519738288466, n+4294967295)
	println(chain(-1, 1), chain(5, 6), ones(-1, 1), ones(-2, 3), down(18446744073709551615, 1))
	a, b := id(-1), id(1)
	c := a + b + 10 + 20
	println(c, accumulate(a, b))
}
`,
		want: "999999 1000001 999995 4295967295 -4293967295 4295967040\n999999 1000001 4295967295 -4293967295\n999999\n1000300\n4294979640 18446744069414596921 12344 12346\n6001000000 -4293967297 4001000000\n4295967295\n-2319479855371528908 4294967264\n3000 3011 2 3 18446744073709551604\n30 30\n",
	},
	{
		// The target compiler turns each of these if bodies into conditional
		// instructions, and its optimizer then took the second body's read of
		// the variable for a copy of the first body's write -- both under "not
		// equal", though a compare between them had re-set the flags. With the
		// first condition false nothing had been written, and the update started
		// from a stale register: on the board every line here was wrong, the
		// first 811733765. Old -- v0.33.0 got it wrong too. The backend carries
		// the fix (internal/optimize_ir.c.diff, flexprop#110,
		// doc/conditional-load-dropped.c). Found by the fuzzer's board sweep,
		// seeds 391, 525 and 793. Every line matches Go.
		name: "two conditional updates of a package variable in a row",
		src: `type Acc struct {
	sum, n int
}

var g int

var h uint32

var arr [4]int

var acc Acc

func id(v int) int { return v }

func main() {
	b1 := id(0) != 0
	b2 := id(1) != 0
	g = id(326842928)
	if b1 {
		g = g ^ 1364946277
	}
	if b2 {
		g = g ^ 811733764
	}
	println(g)
	h = uint32(id(1000))
	if b1 {
		h += 7
	}
	if b2 {
		h += 9
	}
	println(h)
	arr[2] = id(50)
	if b1 {
		arr[2] |= 1
	}
	if b2 {
		arr[2] |= 4
	}
	println(arr[2])
	acc.sum = id(10)
	if b1 {
		acc.sum -= 3
	}
	if b2 {
		acc.sum -= 5
	}
	println(acc.sum)
	g = id(5)
	if b1 {
		g++
	}
	if b2 {
		g++
	}
	if b2 {
		g *= 3
	}
	println(g)
}
`,
		want: "588851508\n1009\n54\n5\n18\n",
	},
	{
		// %T of an ARRAY, which had no C type to be named by and was "cannot tell the
		// type of this argument" in every shape -- unnamed, defined, of a defined
		// element, a field, a conversion, a literal, through a pointer, a call's
		// result, with a width. And %T of an argument that DOES something: the type
		// is known where the argument is written, so it was folded into the format
		// and the argument dropped with it -- `printf("%T\n", tick())` never called
		// tick, and a receive would not have received. Go evaluates every argument.
		name: "%T of an array, and of an argument with effects",
		src: `type Buf [4]byte

type Row [3]int16

type rec struct {
	b   Buf
	raw [2]uint32
}

type pt struct{ x, y int }

var calls int

func tick() int {
	calls++
	return calls
}

func mk() [2]float32 {
	calls += 10
	return [2]float32{1, 2}
}

func mkPt() pt {
	calls += 100
	return pt{1, 2}
}

var ch chan int

func send() { ch <- 7 }

func main() {
	var a [4]byte
	var x Buf
	y := Buf{}
	z := Buf(a)
	var grid [2][3]int
	var rows [2]Row
	var r rec
	p := &x
	p[0] = 1
	printf("%T %T %T %T %T\n", a, x, y, z, grid)
	printf("%T %T %T %T\n", rows, rows[1], r.b, r.raw)
	printf("%T %T %T %T\n", Buf(a), [3]bool{}, *p, mk())
	printf("%8T|%-10T|\n", x, a)
	printf("%T\n", tick())
	printf("[%T]\n", mkPt())
	printf("%-6T|\n", tick())
	printf("%T %d\n", mk(), tick())
	go send()
	printf("%T\n", <-ch)
	println(calls, a[0], y[0], z[0], grid[0][0], rows[0][0], r.raw[0], x[0])
}
`,
		want: "[4]uint8 main.Buf main.Buf main.Buf [2][3]int\n[2]main.Row main.Row main.Buf [2]uint32\nmain.Buf [3]bool main.Buf [2]float32\nmain.Buf|[4]uint8  |\nint\n[main.pt]\nint   |\n[2]float32 123\nint\n123 0 0 0 0 0 0 1\n",
	},
	{
		// An array declared from a conversion to a defined array type -- `b :=
		// Buf(a)`, the var form, at package scope, from a literal, from another
		// defined array type, from a call -- and then a method called on it. The
		// declaration copies the operand and recorded the operand's shape, which has
		// no name, and a method set is found by the name: `b.Sum()` read b as a
		// package qualifier, "unknown package b".
		name: "a method on an array declared from a conversion",
		src: `type Buf [4]byte

type Row [4]byte

func (b *Buf) Sum() int { return int(b[0]) + int(b[3]) }

func (b Buf) First() byte { return b[0] }

func mk() [4]byte { return [4]byte{9, 8, 7, 6} }

func take(b Buf) int { return int(b[1]) }

var pa = [4]byte{5, 0, 0, 5}

var pb = Buf(pa)

func main() {
	var a [4]byte
	a[0], a[3] = 1, 2
	var b1 = Buf(a)
	b2 := Buf([4]byte{1, 2, 3, 4})
	r := Row{3, 0, 0, 3}
	b3 := Buf(r)
	b4 := Buf(mk())
	b5 := Buf(a)
	p := &b5
	b5[3] = 7
	println(b1.Sum(), b2.Sum(), b2.First(), b3.Sum(), b4.Sum(), b4.First(), p.Sum(), pb.Sum(), pb.First())
	println(take(b2), b1 == Buf(a), b5 == b1)
}
`,
		want: "3 5 1 6 15 9 8 10 5\n2 true false\n",
	},
	{
		// A signed comparison of two values the target's C compiler knows, more than
		// 2^31 apart: its optimizer decided the comparison from the sign of their
		// 32-bit difference, which overflows, and so `i < 2147483647` for i = -7 was
		// false, and so was a saturation check inlined with a constant argument and
		// `m < 1` for the most negative int32 -- five of these ten, with nothing to
		// say so. doc/signed-compare-overflow.c; fixed in the backend (flexprop#111).
		name: "a signed comparison of two values far apart",
		src: `const lo = -2000000000

const hi = 2000000000

func notSaturated(v int32) bool { return v < 2147483647 }

func atFloor(v int32) bool { return v <= -2147483647 }

func main() {
	var i int32 = -7
	var m int32 = -2147483647 - 1
	v := 500000000
	println(notSaturated(-7), atFloor(1), i < 2147483647, i <= 2147483646, m < 1, 5 > m)
	println(v > lo, v < hi, -v > lo, notSaturated(2147483647))
}
`,
		want: "true false true true true true\ntrue true true false\n",
	},
	{
		// What an operand needs ahead of itself -- a call's result bound to a
		// temporary, a pointer's nil check -- ran before the whole statement, including
		// for an operand Go evaluates only sometimes: the right of && and ||, an
		// else-if's test, a case's. Every call below ran whatever the tests before it
		// said (the first line counted 1111 where Go counts 0), and `n.vals != nil &&
		// n.vals[0] != v` panicked on the nil it guards against. A test behind an init
		// statement had it run ahead of the init: p was checked before it was declared.
		name: "an operand evaluated only when Go evaluates it",
		src: `type Pair struct {
	x, y int
	ok   bool
}

type node struct {
	vals *[4]int
	next *node
}

var calls int

func arr(v int) [4]int {
	calls++
	return [4]int{v, v + 1, v + 2, v + 3}
}

func pair(v int) Pair {
	calls += 10
	return Pair{v, v * 2, v > 0}
}

var backing = [3]int{7, 8, 9}

func tail() []int {
	calls += 100
	return backing[1:]
}

var rows [2][4]int

func row(i int) *[4]int {
	calls += 1000
	return &rows[i]
}

func find(n *node, v int) bool {
	for n != nil && (n.vals == nil || n.vals[0] != v) {
		n = n.next
	}
	return n != nil && n.vals != nil && n.vals[1] == v+1
}

func main() {
	no, yes := false, true
	println(no && arr(1)[0] == 1, yes || pair(1).ok, no && tail()[0] == 8, yes || row(0)[1] == 0, calls)
	println(yes && arr(1)[2] == 3 || pair(2).y == 4, no || arr(5)[0] == 5 && pair(3).ok, calls)
	if no {
		println("a")
	} else if arr(2)[0] == 3 {
		println("b")
	} else if pair(4).x == 4 {
		println("c", calls)
	}
	switch {
	case yes:
		println("d", calls)
	case arr(3)[0] == 3:
		println("e")
	}
	switch 9 {
	case arr(9)[0], pair(9).x:
		println("f", calls)
	case tail()[1]:
		println("g")
	default:
		println("h")
	}
	if p := row(1); p != nil && p[2] == 0 {
		println("i", calls)
	}
	switch q := row(0); {
	case q == nil || q[3] == 0 && pair(0).ok:
		println("j")
	default:
		println("k", calls)
	}
	var vs = [4]int{3, 4, 0, 0}
	n := &node{next: &node{vals: &vs}}
	println(find(n, 3), find(n, 5), find(&node{}, 1), find(nil, 1))
	k := 0
	for k < 3 && arr(k)[0] < 2 {
		k++
	}
	println(k, calls)
}
`,
		want: "false true false true 0\ntrue true 12\nc 23\nd 23\nf 24\ni 1024\nk 2034\ntrue false false false\n2 2037\n",
	},
	{
		// A case of a bool switch is compared with the tag as a whole, and C binds
		// == tighter than && and as tight as another ==: `switch done { case ok &&
		// ready: }` tested (done == ok) && ready. And `switch true` compared the
		// cases with a bare `true`, which is not 1 to the target's C compiler: no
		// case of it matched on the board, and the host compiler did not know the
		// name. The old compiler printed "b d" of these five on a P2-EDGE.
		name: "a bool switch compares its tag with the whole case",
		src: `func main() {
	done, ok, ready := false, true, false
	n := 3
	switch done {
	case ok && ready:
		println("a")
	}
	switch done {
	case ok || ready, n > 5:
		println("b")
	default:
		println("c")
	}
	switch n > 2 {
	case (n == 3) == ok:
		println("d")
	}
	switch false {
	case n < 5 && n > 4:
		println("e")
	}
	switch true {
	case n == 4, ok != ready:
		println("f")
	}
}
`,
		want: "a\nb\nd\ne\nf\n",
	},
	{
		// A package variable initialized from an element of an array a call returns
		// stopped the compiler: the call is bound to a temporary and remembered by
		// its token, in a map only a function body had made -- "assignment to entry
		// in nil map". With the map made, typing the variable bound the call too and
		// the step reused that temporary, never declaring it.
		name: "a package variable from an element of a call's array",
		src: `type P struct {
	x, y int
	ok   bool
}

var calls int

func mk(v int) [4]int {
	calls++
	return [4]int{v, v + 1, v + 2, v + 3}
}

func pair() P {
	calls += 10
	return P{1, 2, true}
}

func two(v int) (int, int) { return v, v * 2 }

var no = false

var elem = mk(1)[1]

var typed int = mk(2)[3]

var test = mk(3)[0] == 3

var lazy = no && mk(4)[0] == 4

var a, b = two(mk(5)[2])

var s = []int{mk(6)[0], 2}

var arr = [2]int{mk(7)[1], 3}

var st = P{x: mk(8)[2]}

var n = len(mk(9))

var x1, x2 = mk(10)[0], pair().y

var (
	u = mk(11)[0]
	t = u + mk(12)[1]
)

func main() {
	println(elem, typed, test, lazy, a, b, s[0], arr[0], st.x, n)
	println(x1, x2, u, t, calls)
}
`,
		want: "2 5 true false 7 14 6 8 10 4\n10 2 11 24 21\n",
	},
	{
		// A fallthrough repeats the next case's body, and the temporaries a body
		// binds a call's array result to were remembered for the whole function by
		// the call's position -- so the repeated copy of `println(mk(2)[2])` named the
		// first copy's temporary, declared in another block, and the program did not
		// compile ("an array result cannot be read through this suffix" here). The
		// memo belongs to the statement that declares them.
		name: "a fallthrough into a body indexing a call's array",
		src: `type P struct {
	x, y int
}

var calls int

func mk(v int) [4]int {
	calls++
	return [4]int{v, v + 1, v + 2, v + 3}
}

func pair(v int) P {
	calls += 10
	return P{v, v * 2}
}

func step(n int) {
	switch n {
	case 1:
		println("one", mk(1)[1])
		fallthrough
	case 2:
		println("two", mk(2)[2], pair(2).y)
		fallthrough
	default:
		if mk(3)[0] == 3 {
			println("three", pair(3).x)
		}
	}
}

func main() {
	step(1)
	step(2)
	step(5)
	println(calls)
}
`,
		want: "one 2\ntwo 4 4\nthree 3\ntwo 4 4\nthree 3\nthree 3\n56\n",
	},
	{
		// `*x` of a pointer to an array where x is not a variable -- a call's result,
		// a field, an element. A copy of it was `T a = *pick();` or `a = *h.p;`,
		// which is not C: the host's compiler refused it, and the target's took it and
		// read garbage, 251 for a 9 on a P2-EDGE. The return, the literal elements, the
		// comparisons and the range were refused; an argument and a declared variable
		// worked but checked nothing for nil. Each call below runs once.
		name: "an array through a pointer that is not a variable",
		src: `type Buf [4]byte

func (b Buf) Sum() int {
	s := 0
	for _, v := range b {
		s += int(v)
	}
	return s
}

type holder struct {
	p *[4]byte
	q *Buf
}

type withArr struct {
	a [4]byte
	n int
}

var calls int

var rows [2][4]byte

var bufs [2]Buf

var ptrs [2]*[4]byte

func pick(i int) *[4]byte {
	calls++
	return &rows[i]
}

func pickBuf() *Buf {
	calls++
	return &bufs[1]
}

func take(a [4]byte) int { return int(a[0]) + int(a[3]) }

func give() [4]byte { return *pick(1) }

func main() {
	rows[0] = [4]byte{1, 2, 3, 4}
	rows[1] = [4]byte{5, 6, 7, 8}
	bufs[1] = Buf{9, 9, 9, 9}
	ptrs[1] = &rows[0]
	h := holder{p: &rows[1], q: &bufs[1]}
	a := *pick(0)
	var b [4]byte = *h.p
	c := *ptrs[1]
	var d [4]byte
	d = *pick(1)
	println(a[3], b[0], c[1], d[2], take(*pick(0)), give()[1], calls)
	w := withArr{a: *h.p, n: 2}
	m := [2][4]byte{*ptrs[1], *pick(1)}
	e := *pickBuf()
	println(w.a[3], w.n, m[0][0], m[1][3], e.Sum(), (*h.q).Sum(), calls)
	println(a == *ptrs[1], *h.p == *pick(1), d != *pick(0), calls)
	s := 0
	for _, v := range *pick(1) {
		s += int(v)
	}
	a, d = *h.p, *ptrs[1]
	rows[0][0] = 100
	println(s, a[0], d[0], c[0], calls)
}
`,
		want: "4 5 2 7 5 6 4\n8 2 1 8 36 36 6\ntrue true true 8\n26 5 1 1 9\n",
	},
	{
		name: "an array through a nil pointer a call returns panics",
		src: `func none() *[4]int { return nil }

func take(a [4]int) int { return a[0] }

func main() {
	println("before")
	println(take(*none()))
	println("after")
}
`,
		want:   "before\npanic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a copy of an array through a nil pointer field panics",
		src: `type holder struct {
	p *[4]int
}

func main() {
	var h holder
	a := *h.p
	println("after", a[0])
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		// len and cap of an array are constants only while the operand holds no
		// call: `len(grid[idx()])` is not one, and Go evaluates the operand -- idx
		// runs, and the index is checked. The extent was emitted and idx never ran.
		// And every operand of pointer-to-array type that is not a variable was
		// refused -- a call's result, a field, an element, a dereference of any --
		// as was `cap` of a call's array, which `len` took.
		name: "len and cap of an operand holding a call evaluate it",
		src: `type holder struct {
	p *[6]byte
}

var calls int

var grid [3][5]int

var rows [2][6]byte

var ptrs [2]*[6]byte

func idx() int {
	calls++
	return 1
}

func pick() *[6]byte {
	calls += 10
	return &rows[0]
}

func mk() [4]int {
	calls += 100
	return [4]int{}
}

func main() {
	h := holder{p: &rows[1]}
	var none holder
	println(h.p[0], none.p == nil, len(grid[idx()]), cap(grid[idx()]), calls)
	println(len(pick()), cap(pick()), len(*pick()), cap(*pick()), calls)
	println(len(h.p), cap(h.p), len(*h.p), len(none.p), cap(*none.p), calls)
	println(len(ptrs[1]), cap(ptrs[idx()]), len(*ptrs[0]), calls)
	println(len(mk()), cap(mk()), calls)
	n := cap(h.p) + len(grid[idx()])
	if len(pick()) == 6 || cap(mk()) == 4 {
		println(n, calls)
	}
}
`,
		want: "0 true 5 5 2\n6 6 6 6 42\n6 6 6 6 6 42\n6 6 6 43\n4 4 243\n11 254\n",
	},
	{
		name: "len of an evaluated dereference of a nil pointer panics",
		src: `var ptrs [2]*[4]int

var calls int

func idx() int {
	calls++
	return 0
}

func main() {
	println(len(ptrs[idx()]), calls)
	println(len(*ptrs[idx()]))
	println("after")
}
`,
		want:   "4 1\npanic: nil pointer dereference",
		panics: true,
	},
	{
		// Go evaluates the calls of an expression left to right, and C leaves the
		// order of an operator's operands and of a helper's arguments open. Each
		// line below is the order the calls of one statement ran in. The old
		// compiler ran six of the fourteen out of order on a P2-EDGE -- a shift by a
		// count that is a call evaluated the count first, and an operand bound to a
		// temporary ahead of the statement (a call's array, struct, slice or
		// interface result) ran before every operand left of it -- and ten on the
		// host, whose compiler takes a helper's arguments right to left.
		name: "the calls of an expression run left to right",
		src: `type P struct{ a, b int }

type Mer interface{ M(v int) int }

type Q struct{ n int }

func (q *Q) M(v int) int {
	trace = trace*10 + 7
	return q.n + v
}

var trace int

func f(n int) int {
	trace = trace*10 + n
	return n
}

func f64(n int) int64 {
	trace = trace*10 + n
	return int64(n)
}

func fs(n int) string {
	trace = trace*10 + n
	return names[n%2]
}

var names = [2]string{"ab", "cd"}

func fa(n int) [2]int {
	trace = trace*10 + n
	return [2]int{n, n}
}

func fp(n int) P {
	trace = trace*10 + n
	return P{n, n}
}

var backing = [4]int{1, 2, 3, 4}

func fsl(n int) []int {
	trace = trace*10 + n
	return backing[:]
}

var qv = Q{1}

var gq = &qv

func getM(n int) Mer {
	trace = trace*10 + n
	return gq
}

var tbl [8]int

func show() int {
	t := trace
	trace = 0
	return t
}

func main() {
	x := f(7) / f(2)
	a := show()
	x += f(9) % f(4)
	b := show()
	x += f(1) << uint(f(2))
	c := show()
	x += f(8) >> uint(f(1))
	d := show()
	y := f64(5) / f64(2)
	e := show()
	println(a, b, c, d, e, x, y)
	s := fs(1) < fs(2)
	g := show()
	x = min(f(3), f(1)) + max(f(1), f(2), f(3))
	h := show()
	x += f(1) + fa(2)[f(3)%2]
	i := show()
	x += f(1) + fp(2).b
	j := show()
	x += f(1) + fsl(2)[f(3)]
	k := show()
	x += f(1) + getM(2).M(f(3))
	l := show()
	tbl[f(1)] = f(2) + f(3)
	m := show()
	x += -f(1) + f(2)*(f(3)-f(4))
	n := show()
	if f(1) < f(2) && f(3) == 3 || f(4) > f(5) {
		x++
	}
	o := show()
	println(g, h, i, j, k, l, m, n, o, s, x, tbl[1])
}
`,
		want: "72 94 12 81 52 12 2\n12 31123 123 12 123 1237 123 1234 123 false 18 5\n",
	},
	{
		// `&T{...}` in a package variable's initializer is a value Go allocates,
		// and the compound literal it was written as lives in ogo_pkg_init's frame,
		// which is gone when the function returns: `var defaults = &Config{...}`
		// pointed into a dead frame, and read what the next call left there -- 32764
		// for a 1 on the host, a crash for a linked list. So did a literal's field,
		// an array or slice element and a struct field holding one. A package
		// variable of interface type made from one was refused outright. Each is a
		// static object of the program now, filled where the initializer runs.
		name: "a package variable holding the address of a literal",
		src: `type Config struct {
	name  string
	rate  int
	table [3]int
}

type node struct {
	v    int
	next *node
}

type holder struct {
	cfg *Config
	n   int
}

type Shape interface{ Area() int }

type Rect struct{ w, h int }

func (r *Rect) Area() int { return r.w * r.h }

var calls int

func seed() int {
	calls++
	return 40
}

var defaults = &Config{name: "p2", rate: 9600, table: [3]int{1, 2, 3}}

var list = &node{v: 1, next: &node{v: 2, next: &node{v: 3}}}

var h = holder{cfg: &Config{name: "h", rate: seed()}, n: 2}

var refs = [2]*Config{&Config{rate: 7}, nil}

var shapes = []*Rect{&Rect{2, 3}, &Rect{4, 5}}

var s Shape = &Rect{6, 7}

func clobber(a, b, c, d int) int {
	var buf [32]int
	for i := range buf {
		buf[i] = a*i + b - c + d
	}
	return buf[3] + buf[31]
}

func main() {
	_ = clobber(7, 8, 9, 10)
	sum := 0
	for n := list; n != nil; n = n.next {
		sum += n.v
	}
	defaults.rate *= 2
	println(defaults.name, defaults.rate, defaults.table[2], sum)
	println(h.cfg.name, h.cfg.rate, h.n, refs[0].rate, refs[1] == nil)
	println(shapes[0].Area(), shapes[1].Area(), s.Area(), calls)
}
`,
		want: "p2 19200 3 6\nh 40 2 7 true\n6 20 42 1\n",
	},
	{
		// A keyed struct literal is emitted in FIELD order, which is the order C
		// evaluates its values in, so `Hdr{kind: rd(), size: rd()}` over a struct
		// declaring size first read the stream the wrong way round -- 7 3 for 3 7 on
		// a P2-EDGE. And the values an append adds are arguments of nested helper
		// calls, which the host's compiler evaluates last first.
		name: "a keyed literal and an append evaluate their values in order",
		src: `type Hdr struct {
	size int
	kind byte
	id   string
}

type Frame struct {
	hdr  Hdr
	crc  uint16
	data []byte
}

var stream = [8]byte{3, 7, 1, 2, 9, 4, 5, 6}

var pos int

func rd() byte {
	b := stream[pos]
	pos++
	return b
}

var names = [2]string{"a", "b"}

func name() string {
	return names[pos%2]
}

var buf [8]byte

func main() {
	h := Hdr{kind: rd(), size: int(rd()), id: name()}
	println(h.kind, h.size, h.id, pos)
	p := &Hdr{id: name(), kind: rd(), size: int(rd())}
	println(p.kind, p.size, p.id, pos)
	f := Frame{crc: uint16(rd())<<8 | uint16(rd()), hdr: Hdr{size: 1}}
	println(f.crc, f.hdr.size, pos)
	pos = 0
	s := buf[:0]
	s = append(s, rd(), rd(), rd())
	println(s[0], s[1], s[2], len(s), pos)
}
`,
		want: "3 7 a 2\n1 2 a 4\n2308 1 6\n3 7 1 3 3\n",
	},
	{
		// More of Go's left-to-right order, measured per statement as the calls ran.
		// A multiple assignment binds its values to temporaries before the stores,
		// which read their indexes after them -- `sl[f(1)], sl[f(2)] = f(3), f(4)`
		// ran 3412 on a P2-EDGE; a method call's arguments were bound ahead of the
		// statement and its receiver was not -- `getQ(1).M(f(2), f(3))` ran 2317;
		// and slice bounds and copy's arguments go through helpers the host's
		// compiler evaluates right to left.
		name: "the calls of a store, a slice and a method call run left to right",
		src: `type P struct{ x int }

type Q struct{ n int }

func (q *Q) M(a, b int) int {
	trace = trace*10 + 7
	return q.n + a + b
}

func (q *Q) One(a int) int {
	trace = trace*10 + 8
	return q.n + a
}

type Mer interface{ M(a, b int) int }

var trace int

func f(n int) int {
	trace = trace*10 + n
	return n
}

var gp = P{}

func getP(n int) *P {
	trace = trace*10 + n
	return &gp
}

var gq = Q{1}

func getQ(n int) *Q {
	trace = trace*10 + n
	return &gq
}

var grid [4][4]int

var sl = []int{0, 1, 2, 3, 4, 5, 6, 7}

var str = "abcdefgh"

var dst [8]int

func show(tag string) {
	println(tag, trace)
	trace = 0
}

func add(a, b int) int { return a + b }

func main() {
	var m Mer = &gq
	fv := add
	x := grid[f(1)][f(2)]
	show("index2")
	s := sl[f(1):f(2)]
	show("slice")
	c := str[f(1):f(3)]
	show("strslice")
	b := str[f(1)] + str[f(2)]
	show("strindex")
	n := copy(dst[f(1):], sl[f(2):])
	show("copy")
	grid[f(1)][f(2)] = f(3)
	show("store2")
	sl[f(1)], sl[f(2)] = f(3), f(4)
	show("multistore")
	y := getQ(1).One(f(2))
	show("recv1")
	z := getQ(1).M(f(2), f(3))
	show("recv2")
	w := m.M(f(1), f(2))
	show("iface2")
	v := fv(f(1), f(2))
	show("funcval")
	u := gq.M(f(1), f(2))
	show("method2")
	println(x, len(s), c, b, n, gp.x, grid[1][2], sl[1], y, z, w, v, u)
}
`,
		want: "index2 12\nslice 12\nstrslice 13\nstrindex 12\ncopy 12\nstore2 123\nmultistore 1234\nrecv1 128\nrecv2 1237\niface2 127\nfuncval 12\nmethod2 127\n0 1 bc 197 6 0 3 3 3 6 4 3 4\n",
	},
	{
		// A store through a call's pointer or slice result -- `dev().ctrl = v`, a
		// register behind an accessor -- was "only simple and field assignment
		// targets are supported yet", and `out := append(buf[:0], src...)`, the
		// reuse-a-buffer idiom, was "cannot infer a type for the declaration of out".
		name: "a store through a call's result, and append to a slice expression",
		src: `type Reg struct {
	ctrl  uint32
	data  [4]byte
	next  *Reg
	count int
}

type Bus struct{ regs [2]Reg }

func (b *Bus) reg(i int) *Reg { return &b.regs[i] }

var bus Bus

var calls int

func dev() *Reg {
	calls++
	return &bus.regs[0]
}

var table = []int{1, 2, 3}

func rows() []int {
	calls += 10
	return table
}

var raw [6]byte

func frame() *[6]byte {
	calls += 100
	return &raw
}

var backing [16]byte

var src = []byte{7, 8, 9}

func main() {
	bus.regs[0].next = &bus.regs[1]
	dev().ctrl = 0x80
	dev().ctrl |= 1
	dev().count++
	dev().data[2] = 5
	dev().next.count += 3
	bus.reg(1).data = [4]byte{1, 2, 3, 4}
	rows()[1] = 20
	frame()[5] = 6
	println(bus.regs[0].ctrl, bus.regs[0].count, bus.regs[0].data[2], bus.regs[1].count, bus.regs[1].data[3])
	println(table[1], raw[5], calls)
	buf := backing[:]
	out := append(buf[:0], src...)
	more := append(backing[4:4], 1, 2)
	println(len(out), out[2], len(more), more[1], backing[5], cap(more))
}
`,
		want: "129 1 5 3 4\n20 6 115\n3 9 2 2 2 12\n",
	},
	{
		// Every read through a pointer that is NOT a variable -- a field of a
		// pointer element, of a pointer field, of a call's result; a written-out
		// dereference of one; a value receiver reached through one; an array through
		// a pointer field, indexed, copied, ranged, and as a pointer element indexed
		// again -- and every store through one. None was checked for nil: only a
		// pointer VARIABLE's dereference took the check, at its chain's base, and the
		// rest read address zero on the board where Go panics (the cases below).
		// This one reads and writes through them all with the pointers set, each call
		// counted once -- `rows[idx()].p[i]`, whose check is a statement of its own,
		// binds the pointer first rather than running idx twice.
		name: "reads and stores through pointers that are not variables",
		src: `type U struct{ a [3]int }

type Buf [4]byte

func (b Buf) Sum() int {
	s := 0
	for _, v := range b {
		s += int(v)
	}
	return s
}

type T struct {
	x  int
	s  []int
	q  *T
	u  *U
	pb *Buf
}

func (t T) Val() int { return t.x }

func (t *T) Ptr() int { return t.x + 100 }

var inner = T{x: 2}

var uu = U{a: [3]int{4, 5, 6}}

var bb = Buf{1, 2, 3, 4}

var backing = [2]int{7, 8}

var gt = T{x: 1, q: &inner, u: &uu, pb: &bb}

var ps = [2]*T{&gt, &inner}

var sl = []*T{&inner, &gt}

var calls int

func getT() *T {
	calls++
	return &gt
}

func idx() int {
	calls += 10
	return 1
}

type box struct{ t *T }

var bx = box{t: &inner}

var raw = [6]byte{0, 1, 2, 3, 4, 5}

var ptrs = [2]*[6]byte{nil, &raw}

type row struct{ p *[4]int }

var quad = [4]int{9, 8, 7, 6}

var rows = [2]row{{nil}, {&quad}}

func main() {
	gt.s = backing[:]
	p := &gt
	println(p.Val(), ps[1].x, sl[1].x, gt.q.x, bx.t.x, getT().x, calls)
	v := *getT()
	w := *gt.q
	z := *ps[1]
	println(v.x, w.x, z.x, ps[0].Val(), ps[0].Ptr(), gt.q.Val(), calls)
	println(gt.q.q == nil, len(gt.s), len(getT().s), gt.u.a[1], calls)
	x := gt.u.a
	println(x[2], ptrs[1][3], rows[idx()].p[0], rows[idx()].p[idx()], gt.pb.Sum(), calls)
	gt.q.x = 5
	ps[1].x++
	gt.u.a[1] = 50
	getT().x += 10
	rows[idx()].p[2] = 70
	s := 0
	for _, e := range gt.u.a {
		s += e
	}
	println(inner.x, uu.a[1], gt.x, quad[2], s, calls)
}
`,
		want: "1 2 1 2 2 1 1\n1 2 2 1 101 2 2\ntrue 2 2 5 3\n6 3 9 8 10 33\n6 50 11 70 60 44\n",
	},
	{
		// A slice literal standing in a package variable's initializer -- an element
		// of a struct literal, indexed -- beside anything not constant stopped the
		// compiler ("assignment to entry in nil map"): a body's literal binds to a
		// temporary of the frame through tables a package variable has none of, and
		// had it been made the header would have pointed into ogo_pkg_init's dead
		// frame. Beside constants only, the literal was refused instead. Each is a
		// static object of the program now, filled where the variable is, so a
		// configuration table of slices reads as Go's does.
		name: "a package literal holding a slice literal",
		src: `type Config struct {
	name  string
	rate  int
	pins  []int
	names []string
	dev   *Dev
}

type Dev struct{ id int }

var d = Dev{7}

var calls int

func pin(n int) int {
	calls++
	return n * 2
}

var cfg = Config{name: "p2", pins: []int{1, 2, 3}, names: []string{"a", "b"}, dev: &d}

var dyn = Config{rate: pin(1), pins: []int{pin(2), pin(3)}, dev: &d}

var first = []int{pin(5), 9}[0]

var table = []int{pin(10), 1, 2}

var third = [3]int{pin(20), 1, 2}[2]

var empty = Config{pins: []int{}}

func main() {
	println(cfg.name, len(cfg.pins), cfg.pins[2], cfg.names[1], cfg.dev.id)
	println(dyn.rate, dyn.pins[0], dyn.pins[1], len(dyn.names), dyn.dev.id)
	println(first, len(table), table[0], third, len(empty.pins), calls)
	cfg.pins[0] = 100
	dyn.pins = append(dyn.pins[:1], 8)
	println(cfg.pins[0], dyn.pins[1], cap(dyn.pins))
}
`,
		want: "p2 3 3 b 7\n2 4 6 0 7\n10 3 20 2 0 6\n100 8 2\n",
	},
	{
		// Go evaluates a composite literal's values in the order written. A value
		// needing a statement ahead of the literal -- a slice literal, a struct or an
		// array a call returns, a field of a pointer a call returns -- ran before
		// every value written before it (`W{n: f(1), xs: []int{f(2)}}` ran 21, on a
		// P2-EDGE too), and C leaves the order of the rest of an initializer open.
		// Each line is the order the calls of one literal ran in, nested rows and a
		// package literal included.
		name: "a composite literal evaluates its values in order",
		src: `type P struct{ a, b int }

type W struct {
	n  int
	xs []int
	p  P
	r  [2]int
}

var trace int

func f(n int) int {
	trace = trace*10 + n
	return n
}

func mkA(n int) [2]int {
	trace = trace*10 + n
	return [2]int{n, n + 1}
}

func mkP(n int) P {
	trace = trace*10 + n
	return P{n, n * 2}
}

var backing = [4]int{1, 2, 3, 4}

func mkS(n int) []int {
	trace = trace*10 + n
	return backing[:n]
}

type Q struct{ x int }

var gq = Q{9}

func getQ(n int) *Q {
	trace = trace*10 + n
	return &gq
}

func show() int {
	t := trace
	trace = 0
	return t
}

var pw = W{n: f(1), xs: []int{f(2), f(3)}, p: mkP(4)}

var porder = show()

func main() {
	w := W{n: f(1), xs: []int{f(2), f(3)}}
	a := show()
	w2 := W{n: f(1), p: mkP(2), r: mkA(3), xs: mkS(4)}
	b := show()
	x := W{xs: []int{f(1)}, p: P{a: getQ(2).x}, n: f(3)}
	c := show()
	arr := [2]int{f(1), mkA(2)[f(3)%2]}
	d := show()
	s := []int{f(1), getQ(2).x, f(3)}
	e := show()
	ws := [2]W{{n: f(1), xs: []int{f(2)}}, {r: mkA(3), n: f(4)}}
	g := show()
	rs := [][2]int{mkA(1), mkA(2)}
	h := show()
	m := [2][2]int{{f(1), f(2)}, {f(3), f(4)}}
	i := show()
	println(porder, a, b, c, d, e, g, h, i)
	println(pw.n, pw.xs[1], pw.p.b, w.xs[1], w2.p.b, w2.r[1], len(w2.xs))
	println(x.p.a, x.n, arr[1], s[1], ws[1].r[0], ws[1].n, rs[1][1], m[1][0])
}
`,
		want: "1234 123 1234 123 123 123 1234 12 1234\n1 3 8 3 4 4 4\n9 3 3 9 3 4 3 3\n",
	},
	{
		// A value list binds each value to a temporary in order, as statements, but a
		// value needing a statement ahead of the whole statement -- an array a call
		// returns -- had it placed before every value before it: `a, b := f(1),
		// mkA(2)[0]` ran mkA first, on a P2-EDGE too. Each line is the order the
		// calls of one statement ran in.
		name: "a value list evaluates its values in order",
		src: `type P struct{ a, b int }

var trace int

func f(n int) int {
	trace = trace*10 + n
	return n
}

func mkA(n int) [2]int {
	trace = trace*10 + n
	return [2]int{n, n + 1}
}

func mkP(n int) P {
	trace = trace*10 + n
	return P{n, n * 2}
}

var backing = [4]int{1, 2, 3, 4}

func mkS(n int) []int {
	trace = trace*10 + n
	return backing[:n]
}

var arr [4]int

func show() int {
	t := trace
	trace = 0
	return t
}

func main() {
	a, b := f(1), mkA(2)[0]
	o1 := show()
	c, d, e := f(1), mkP(2).a, mkS(3)[0]
	o2 := show()
	var x int
	x, arr[f(1)] = mkA(2)[0], f(3)
	o3 := show()
	var g, h = f(1), mkA(2)[1]
	o4 := show()
	var i, j int = mkA(1)[0], f(2)
	o5 := show()
	a, b = mkA(1)[1], f(2)
	o6 := show()
	println(o1, o2, o3, o4, o5, o6)
	println(a, b, c, d, e, x, arr[1], g, h, i, j)
}
`,
		want: "12 123 123 12 12 12\n2 2 1 2 1 2 3 1 3 1 2\n",
	},
	{
		// An embedded POINTER, `struct{ *Inner }`: Go promotes the pointee's fields
		// and methods through the pointer, a value receiver reading what it points
		// at and a pointer receiver taking the pointer as it is. It was refused
		// outright ("embed Inner by value"), the spec claiming Go refuses it too --
		// Go refuses only a pointer to a pointer or to an interface. Every promoted
		// position: field read and write, both receivers, through a pointer to the
		// outer, two levels of pointer embeds, a method of the outer reading a
		// promoted field, an interface satisfied by a promoted method, a method
		// value, an element, an argument, a call's result, a mixed struct embedding
		// one type by value and another by pointer, the pointer compared, replaced
		// and copied, and a deferred call.
		name: "an embedded pointer promotes through the pointer",
		src: `type Inner struct {
	v int
	u int
}

func (i Inner) Val() int { return i.v }

func (i *Inner) Set(n int) { i.v = n }

func (i *Inner) Ptr() int { return i.v + 100 }

type Outer struct {
	*Inner
	n int
}

type Deep struct {
	*Outer
	m int
}

type Mixed struct {
	Inner
	*Deep
	tag int
}

func (o Outer) Sum() int { return o.v + o.n }

type Valuer interface{ Val() int }

type Setter interface{ Set(n int) }

var in = Inner{v: 1, u: 7}

var in2 = Inner{v: 2}

var outs = [2]Outer{{&in, 1}, {&in2, 2}}

var po = Outer{&in2, 8}

func take(o Outer) int { return o.v + o.n }

func give() Outer { return Outer{&in2, 9} }

func main() {
	o := Outer{&in, 5}
	k := Outer{Inner: &in2, n: 6}
	d := Deep{&o, 3}
	p := &o
	println(o.v, k.v, o.u, o.n, o.Val(), o.Ptr(), k.Val(), o.Sum(), d.Sum())
	o.v = 10
	o.Set(11)
	println(in.v, o.v, p.v, p.Val(), p.Ptr(), d.v, d.Val(), d.n, d.m)
	p.Set(12)
	d.Set(13)
	q := &d
	q.Set(14)
	println(in.v, q.v, q.Val(), o.Inner == &in, o.Inner.v, po.Inner == &in2)
	o.Inner = &in2
	var s Valuer = &o
	var t Setter = &d
	t.Set(15)
	println(o.v, s.Val(), in.v, in2.v, outs[1].v, outs[0].Val(), take(o), give().v, give().Val())
	outs[1].Set(16)
	f := po.Set
	f(17)
	g := po.Ptr
	println(in2.v, outs[1].Ptr(), g())
	m := Mixed{Inner{v: 20}, &d, 4}
	println(m.Inner.v, m.Deep.v, m.n, m.m, m.tag, m.Deep.Val(), m.Deep.Sum())
	m.Deep.Set(21)
	m.Inner.Set(22)
	println(in.v, m.Inner.v, m.Deep.Outer.n)
	o2 := o
	o2.v = 23
	println(in2.v, o2 == o, o2.n)
	sum := 0
	for _, x := range outs {
		sum += x.v + x.Val()
	}
	println(sum)
	defer po.Set(24)
	println(po.v)
}
`,
		want: "1 2 7 5 1 101 2 6 6\n11 11 11 11 111 11 11 5 3\n14 14 14 true 14 true\n15 15 14 15 15 14 20 15 15\n17 117 117\n20 17 5 3 4 17 22\n14 22 5\n23 true 5\n74\n23\n",
	},
	{
		// A promoted read through a nil embedded pointer panics, as any read through
		// a nil pointer does; a pointer receiver is called with the nil, as in Go,
		// and panics in the method.
		name: "a promoted method through a nil embedded pointer panics",
		src: `type Inner struct{ v int }

func (i Inner) Val() int { return i.v }

func (i *Inner) Ptr() int { return 7 }

type Outer struct {
	*Inner
	n int
}

func main() {
	var z Outer
	println(z.Inner == nil, z.Ptr())
	println(z.Val())
	println("after")
}
`,
		want:   "true 7\npanic: nil pointer dereference",
		panics: true,
	},
	{
		// Go evaluates a deferred call's receiver where the defer stands: a value
		// receiver is a copy taken then, a pointer receiver the address. A PROMOTED
		// method's receiver was not captured at all -- the member was read at the
		// return, so `defer bv.Show()` showed what bv held then (31 for Go's 3), and
		// on a local the replay found no such name ("unknown package lv"). By value
		// and by pointer, on package and local variables, through a pointer to the
		// outer.
		name: "a deferred promoted method captures its receiver at the defer",
		src: `type Inner struct{ v int }

func (i Inner) Show() { println("show", i.v) }

func (i *Inner) Bump() { i.v++ }

func (i *Inner) Ptr() int { return i.v + 100 }

type ByVal struct {
	Inner
	m int
}

type ByPtr struct {
	*Inner
	m int
}

var bv = ByVal{Inner{v: 3}, 4}

var in = Inner{v: 7}

var bp = ByPtr{&in, 4}

func f() {
	defer bv.Show()
	defer bp.Show()
	defer bv.Bump()
	defer bp.Bump()
	bv.v = 30
	in.v = 70
}

func g() {
	lv := ByVal{Inner{v: 5}, 1}
	li := Inner{v: 8}
	lp := ByPtr{&li, 2}
	pv := &lv
	defer lv.Show()
	defer lp.Show()
	defer pv.Show()
	defer lv.Bump()
	defer lp.Bump()
	defer pv.Bump()
	lv.v = 50
	li.v = 80
	defer end(lv.v, li.v)
}

func end(a, b int) { println("end", a, b) }

func main() {
	f()
	println(bv.v, in.v)
	g()
}
`,
		want: "show 7\nshow 3\n31 71\nend 50 80\nshow 5\nshow 8\nshow 5\n",
	},
	{
		// `ps[i].x` read `ps[i]->x` unchecked; each of these read address zero.
		name: "a field read through a nil pointer element panics",
		src: `type T struct{ x int }

var ps [2]*T

func main() {
	println("before")
	println(ps[0].x)
	println("after")
}
`,
		want:   "before\npanic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a field read through a nil pointer field panics",
		src: `type T struct {
	x int
	q *T
}

var gt T

func main() {
	println(gt.q.x)
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a field read through a nil pointer a call returns panics",
		src: `type T struct{ x int }

func get() *T { return nil }

func main() {
	println(get().x)
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a written-out dereference of a nil pointer a call returns panics",
		src: `type T struct{ x int }

func get() *T { return nil }

func main() {
	v := *get()
	println("after", v.x)
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		// `p.Val()` for a value receiver reads what p points at, `T_Val(*p)`, and the
		// read was unchecked -- for a pointer variable too.
		name: "a value method called through a nil pointer panics",
		src: `type T struct{ x int }

func (t T) Val() int { return t.x }

func (t *T) Ptr() int { return 7 }

func main() {
	var p *T
	println(p.Ptr())
	println(p.Val())
	println("after")
}
`,
		want:   "7\npanic: nil pointer dereference",
		panics: true,
	},
	{
		// `ptrs[i][j]` asked for the pointer's check as a statement with no operand,
		// `ogo_nil_..._ptr();`, and did not compile.
		name: "an index through a nil pointer to an array in an element panics",
		src: `var ptrs [2]*[6]byte

func main() {
	println(len(ptrs[1]))
	println(ptrs[1][3])
	println("after")
}
`,
		want:   "6\npanic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a store into a field of a nil pointer field panics",
		src: `type T struct {
	x int
	q *T
}

var gt T

func main() {
	gt.q.x = 5
	println("after")
}
`,
		want:   "panic: nil pointer dereference",
		panics: true,
	},
	{
		// A 64-bit unary minus is emitted as a subtraction from zero. With its
		// small-function inliner on, the target's C compiler miscompiles a 64-bit
		// negation whose result meets an addition or subtraction in the same
		// expression: `-x - 3` for an x of 5 was 4294967288, the high word never
		// negated, while `-x` alone, `-(x + 3)` and `0 - x - 3` were right and a
		// negation bound to a variable first was folded back into the fault
		// (doc/negate64-then-add.c). Found by a 64-bit arithmetic probe diffed
		// against Go, as the dividend `-big - 3` of a division. The host has never
		// had the fault; every line matches real Go.
		name: "a 64-bit negation beside an addition",
		src: `func id(v int64) int64 { return v }

func neg3(x int64) int64 { return -x - 3 }

func main() {
	big := id(1 << 40)
	five := id(5)
	var u uint64 = 1 << 40
	a := -big - 3
	b := -big + 3
	c := -five - 3
	d := 3 - -five
	println(a, b, c, d, neg3(big), neg3(five))
	e := -big - big
	f := -five*2 - 1
	g := -(big + 3)
	h := -u - 3
	i := -u + u
	j := -(-big) - 3
	println(e, f, g, h, i, j)
	var acc int64
	for _, k := range [3]int64{1, 2, -7} {
		n := -big - 3
		acc += n / k
	}
	println(acc)
}
`,
		want: "-1099511627779 -1099511627773 -8 8 -1099511627779 -8\n-2199023255552 -11 -1099511627779 18446742974197923837 0 1099511627773\n-1492194351986\n",
	},
	{
		// Go takes the complement of a typed unsigned constant within the type's
		// width, so ^uint32(0) is the type's maximum -- the CRC idiom. The
		// checker folded it as the untyped ^0, which is -1, and then refused it
		// as overflowing the very type it was written in.
		name: "a complement of a typed unsigned constant",
		src: `type U uint32

const top16 uint16 = ^uint16(0) >> 4

var crcTable [8]uint32

func init() {
	for i := range crcTable {
		c := uint32(i)
		for k := 0; k < 8; k++ {
			low := c & 1
			c >>= 1
			if low != 0 {
				c ^= 0xedb88320
			}
		}
		crcTable[i] = c
	}
}

func crc(data []byte) uint32 {
	c := ^uint32(0)
	for _, b := range data {
		t := byte(c) ^ b
		idx := t & 7
		sh := c >> 8
		c = crcTable[idx] ^ sh
	}
	return ^c
}

func main() {
	println(^uint32(0), ^uint8(1), ^uint64(0)>>1, ^int32(0), ^0, top16, ^U(0))
	var b byte = ^byte(0)
	var d int8 = ^int8(127)
	println(b, d, ^uint16(0xff00), ^uint64(1)>>60)
	msg := [3]byte{1, 2, 3}
	printf("%08x %08x\n", crc(msg[:]), crc(msg[:0]))
}
`,
		want: "4294967295 254 9223372036854775807 -1 -1 4095 4294967295\n255 -128 255 15\n88f826f5 00000000\n",
	},
	{
		// The target's C compiler stops converting a constant argument to its
		// parameter's type after an argument that is an arithmetic expression of
		// 64-bit type, so the 3 of `mix(-m, 3)` went out as one word of the two an
		// int64_t parameter is and the callee read garbage: -192 for
		// -5260211717565488541 on a P2-EDGE (doc/call-arg-after-expr.c). A
		// constant to a 64-bit parameter is now spelled at the parameter's width
		// in every position -- a function, a method, a function value, a deferred
		// call, and an interface's method, whose slot is a function pointer and
		// through one the backend converts no constant at all: `i.mix(m, 3)` was
		// refused outright. Found by a hashing probe diffed against Go.
		name: "a constant argument after a 64-bit expression",
		src: `type Mixer struct {
	k int64
}

type Mixing interface {
	mix(a, b int64) int64
}

func (x Mixer) mix(a, b int64) int64 { return a*31 + b + x.k }

func mix(a, b int64) int64 { return a*31 + b }

func mix3(a, b, c int64) int64 { return a*31 + b*7 + c }

func umix(a, b uint64) uint64 { return a*31 + b }

func id(v int64) int64 { return v }

func show(a, b int64) { println("deferred", a, b) }

func main() {
	m := id(7)
	var u uint64 = 7
	x := Mixer{k: 1}
	f := mix
	defer show(-m, 3)
	println(mix(-m, 3), mix(m+1, 3), mix(m*2, 3+4), mix(-m, -3), mix3(-m, 3, 4))
	println(umix(u*2, 3), x.mix(-m, 3), f(-m, 3), f(m, 3), mix(m, 3), mix(3, -m))
	println(mix(-m, 1<<40), mix(-m, -1<<40))
	var i Mixing = &x
	println(i.mix(-m, 3), i.mix(m, 3), i.mix(3, -m))
}
`,
		want: "-214 251 441 -220 -192\n437 -213 -214 220 220 86\n1099511627559 -1099511627993\n-213 221 87\ndeferred -7 3\n",
	},
	{
		// The case above, for ZERO. The target's C compiler narrows every spelling
		// of zero but a cast -- `0LL`, `0ULL`, `(0)` -- to one word after a 64-bit
		// expression argument, so `mix(-m, 0)` was warned about and miscompiled
		// exactly as `mix(-m, 3)` had been, while no other value measured does
		// that. A zero to a 64-bit parameter is spelled `(int64_t)0` now. Found by
		// the fuzzer's board sweep, seed 140, whose build carried the warning.
		name: "a zero argument after a 64-bit expression",
		src: `type Mixer struct {
	k int64
}

type Mixing interface {
	mix(a, b int64) int64
}

func (x Mixer) mix(a, b int64) int64 { return a*31 + b + x.k }

func mix(a, b int64) int64 { return a*31 + b }

func mix3(a, b, c int64) int64 { return a*31 + b*7 + c }

func umix(a, b uint64) uint64 { return a*31 + b }

func id(v int64) int64 { return v }

func show(a, b int64) { println("deferred", a, b) }

const Zero = 0

func main() {
	m := id(7)
	var u uint64 = 7
	x := Mixer{k: 1}
	f := mix
	defer show(-m, 0)
	println(mix(-m, 0), mix(m+1, 0), mix(m*2, 3-3), mix(-m, Zero), mix3(-m, 0, 4), mix3(-m, 4, 0))
	println(umix(u*2, 0), x.mix(-m, 0), f(-m, 0), f(m, 0), mix(m, 0), mix(0, -m))
	var i Mixing = &x
	println(i.mix(-m, 0), i.mix(m, 0), i.mix(0, -m))
}
`,
		want: "-217 248 434 -217 -213 -189\n434 -216 -217 217 217 -7\n-216 218 -6\ndeferred -7 0\n",
	},
	{
		// An integer constant too wide for a float32 to hold exactly, standing
		// where a float is wanted. Spelled as the integer it was written as, the
		// target's C compiler got it wrong in every position that does not convert
		// it itself: in an initializer it read -2147483648 and -3000000000 as
		// POSITIVE, and a wider negative value was written there as its bit pattern,
		// positive everywhere; as an argument, a value sent or appended, a long long
		// constant went to the float parameter as two words -- "Bad number of
		// parameters" and garbage, or through a function value a refused build; and a
		// deferred call captured the constant into an int. Every line of this was
		// wrong on the board or did not build. A declaration, an assignment, a return
		// and an operand were right, and are not what this pins.
		name: "a wide integer constant where a float is wanted",
		src: `type S struct {
	f float32
	d float64
}

type T struct{ k float32 }

func (t T) add(x float32) float32 { return t.k + x }

type Adder interface {
	add(x float32) float32
}

const C = -3000000000

var pkgF float32 = -2147483648

var pkgH float64 = 4294967296

var pkgS = []float32{-2147483648, -3000000000, -9223371487098961920, 3000000000, 4294967296}

var pkgT = S{f: -3000000000, d: -2147483648}

var ch chan float32

func id(x float32) float32 { return x }

func two(a, b float32) float32 { return a + b }

func sum(xs ...float32) float32 {
	var t float32
	for _, x := range xs {
		t += x
	}
	return t
}

func show(a, b float32) { println("defer", int64(a), int64(b)) }

func sendNeg(c chan float32) { c <- -3000000000 }

func main() {
	defer show(-3000000000, 4294967296)
	println("pkg", int64(pkgF), int64(pkgH), int64(pkgS[0]), int64(pkgS[1]), int64(pkgS[2]))
	println("pkg", int64(pkgS[3]), int64(pkgS[4]), int64(pkgT.f), int64(pkgT.d))
	s := []float32{-2147483648, -3000000000, 4294967296}
	t := S{f: -2147483648, d: -3000000000}
	a := [2]float64{-4294967296, -3000000000}
	println("lit", int64(s[0]), int64(s[1]), int64(s[2]), int64(t.f), int64(t.d), int64(a[0]), int64(a[1]))
	println("call", int64(id(-2147483648)), int64(id(-3000000000)), int64(id(4294967296)), int64(two(1, -3000000000)), int64(two(-3000000000, 9000000000)))
	s = make([]float32, 0, 4)
	s = append(s, -3000000000, 4294967296)
	u1, u2 := sum(-3000000000), sum(1, 4294967296)
	println("append", int64(s[0]), int64(s[1]), int64(u1), int64(u2))
	go sendNeg(ch)
	println("send", int64(<-ch))
	f := id
	var v T
	var i Adder = &v
	println("value", int64(f(-3000000000)), int64(f(4294967296)), int64(v.add(-3000000000)), int64(i.add(4294967296)), int64(id(C)))
}
`,
		want: "pkg -2147483648 4294967296 -2147483648 -3000000000 -9223371487098961920\npkg 3000000000 4294967296 -3000000000 -2147483648\nlit -2147483648 -3000000000 4294967296 -2147483648 -3000000000 -4294967296 -3000000000\ncall -2147483648 -3000000000 4294967296 -3000000000 5999999488\nappend -3000000000 4294967296 -3000000000 4294967296\nsend -3000000000\nvalue -3000000000 4294967296 -3000000000 4294967296 -3000000000\ndefer -3000000000 4294967296\n",
	},
	{
		// A conversion whose operand renders a compound literal binds the operand
		// to a temporary first (see doc/complit-arg-in-cast.c), and a float going
		// to a 64-bit or unsigned integer is converted by a helper written as its
		// name beside the operand. Together they made `ogo_f2i64_ogo_t2` of
		// `int64(sum(1, 2))` -- a variadic call packs its values in a compound
		// literal -- and the C compiler refused the build, "Expected multiple
		// values". A struct literal argument is the other way to get there.
		name: "a conversion of a variadic call's result",
		src: `type S struct{ a, b int32 }

func sum(xs ...float32) float32 {
	var t float32
	for _, x := range xs {
		t += x
	}
	return t
}

func isum(xs ...int32) int32 {
	var t int32
	for _, x := range xs {
		t += x
	}
	return t
}

func total(s S) float64 { return float64(s.a + s.b) }

func main() {
	println(int64(sum(1, 2)), uint64(sum(3, 4)), uint32(sum(5)), int32(sum(6)), int8(sum(7)))
	println(float32(isum(1, 2)), float64(isum(5)), int64(isum(6, 7)), uint16(isum(8)))
	println(int64(total(S{3, 4})), uint32(total(S{a: 5})), int16(total(S{b: 9})))
	var f float32 = 2.5
	println(int64(sum(f, f, f)), uint64(sum(f)))
}
`,
		want: "3 7 5 6 7\n3 5 13 8\n7 5 9\n7 2\n",
	},
	{
		// A deferred call evaluates its arguments where the defer stands, into
		// temporaries replayed at the return. A constant argument's temporary took
		// the type the constant defaults to, an int, rather than its parameter's --
		// so `defer wide(-3000000000, 1<<63+5, ...)` showed 1294967296 and 5 on the
		// board, `-(1 << 33)` showed 0, and a method's int64 and float32 arguments
		// both came out wrong, with no diagnostic anywhere. A bare integer literal
		// is replayed as written and was right. The temporary takes the
		// parameter's type now, as a goroutine's argument block already did.
		name: "a deferred call's constant argument takes its parameter's type",
		src: `const Big = 1 << 40

type Meter struct{ base int64 }

func (m Meter) at(d int64, f float32) { println("method", m.base+d, int64(f)) }

func wide(a int64, b uint64, c uint32, d int8) { println("wide", a, b, c, d) }

func shifted(a int64, b uint32, c uint64) { println("shifted", a, b, c) }

func run() {
	m := Meter{base: 1}
	defer m.at(-3000000000, -4294967296)
	defer shifted(-(1 << 33), 1<<31, Big+1)
	defer wide(-3000000000, 1<<63+5, 3000000000+1, -100)
	println("run")
}

func main() {
	run()
}
`,
		want: "run\nwide -3000000000 9223372036854775813 3000000001 -100\nshifted -8589934592 2147483648 1099511627777\nmethod -2999999999 -4294967296\n",
	},
	{
		// An array literal whose length is "...", the length being what the literal
		// supplies: positional, indexed, mixed, empty, of arrays, of a defined element
		// type and of structs, at package scope and in a function, and standing
		// where an array is wanted -- a declaration of that length, an argument, a
		// comparison, a range, a slice expression and an if header. Every line
		// matches Go.
		name: "an array literal of length ...",
		src: `type Celsius float32

type Pair struct {
	a, b int
}

var table = [...]uint8{3, 1, 4, 1, 5, 9, 2, 6}

var names = [...]string{2: "two", 0: "zero"}

var mixed = [...]int{1, 4: 9, 7}

var grid = [...][2]int{{1, 2}, {3, 4}, {5, 6}}

var temps = [...]Celsius{21.5, -3}

var pairs = [...]Pair{{1, 2}, {a: 3}}

func count(xs [0]string) int { return len(xs) }

func sum(xs [5]int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

func main() {
	println(len(table), len(names), len(mixed), len(grid), len(temps), len(pairs))
	println(table[7], names[2], names[1] == "", mixed[4], mixed[5], grid[2][1], pairs[1].a)
	a := [...]int{10, 20, 30}
	var b [3]int = [...]int{10, 20, 30}
	println(a == b, cap(a), sum([...]int{1, 2, 3, 4, 5}))
	empty := [...]string{}
	println(count(empty), temps[1] < 0)
	for i, v := range [...]byte{'o', 'g', 'o'} {
		print(i, v, " ")
	}
	println()
	s := table[2:5]
	println(len(s), cap(s), s[0])
	if x := [...]int{7, 8}; x[1] == 8 {
		println("header", len(x))
	}
}
`,
		want: "8 3 6 3 2 2\n6 two true 9 7 6 3\ntrue 3 15\n0 true\n0111 1103 2111 \n3 6 4\nheader 2\n",
	},
	{
		// A deferred function literal taking arguments. A literal captures nothing
		// of the scope around it, so its arguments are the one way a value reaches
		// it; they are evaluated where the defer stands, as Go says -- q holds
		// the Pt of before p.x = 100, pv the address so *pv reads 50 -- each into
		// a temporary of its PARAMETER's type, so the 3 handed to an int64 is
		// stored as one. Only the parameterless form was accepted before.
		name: "a deferred function literal with arguments",
		src: `type Pt struct {
	x, y int
}

func work(n int) int {
	total := 0
	twice := n * 2
	defer func(k int, name string) {
		println("done", name, k)
	}(twice, "work")
	for i := 0; i < n; i++ {
		total += i
	}
	return total
}

func wide(m int64) {
	defer func(a int64, b int64) {
		println("wide", a*31+b)
	}(-m, 3)
	println("in", m)
}

func main() {
	p := Pt{1, 2}
	v := 5
	defer func(q Pt, pv *int, b bool) {
		println("last", q.x, q.y, *pv, b)
	}(p, &v, v > 3)
	p.x = 100
	v = 50
	println(work(4), work(0))
	if v > 10 {
		defer func(s string) { println("branch", s) }("taken")
	}
	wide(7)
	defer func() { println("plain") }()
}
`,
		want: "done work 8\ndone work 0\n6 0\nin 7\nwide -214\nplain\nbranch taken\nlast 1 2 50 true\n",
	},
	{
		// The comma-ok receive in a select clause, `case v, ok := <-ch:`, whose ok
		// is false for the zero a closed channel yields -- the way a select tells a
		// closed producer from a sent zero. The clause did not parse before. Both
		// the declaring and the assigning form, and a closed channel winning over a
		// default with ok false, as in Go.
		name: "a comma-ok receive in a select clause",
		src: `func produce(ch chan int, n int) {
	for i := 1; i <= n; i++ {
		sq := i * i
		ch <- sq
	}
	close(ch)
}

func main() {
	var ch chan int
	go produce(ch, 3)
	sum := 0
	open := true
	for open {
		select {
		case v, ok := <-ch:
			if !ok {
				open = false
				println("closed", v, ok, sum)
			} else {
				sum += v
				println("got", v, ok)
			}
		}
	}
	var last int
	var more bool
	var ch2 chan int
	go produce(ch2, 2)
	for i := 0; i < 3; i++ {
		select {
		case last, more = <-ch2:
			println("assigned", last, more)
		}
	}
	select {
	case w, ok := <-ch2:
		println("drained", w, ok)
	default:
		println("default")
	}
}
`,
		want: "got 1 true\ngot 4 true\ngot 9 true\nclosed 0 false 14\nassigned 1 true\nassigned 4 true\nassigned 0 false\ndrained 0 false\n",
	},
	{
		// A float prints as Go prints one: print, println, %v and %g write the
		// fewest significant digits that read back to the same float32 --
		// 0.33333334, not the 0.333333 of C's %g, which is a different number --
		// and every float verb is laid out from the exact digits with Go's
		// rounding and padding, since the target's printf is wrong past the
		// seventh digit (%.7e of a third printed 3.3333335e-01 on a P2-EDGE). %g
		// was refused before, %v printed C's, and the '0' flag was refused.
		name: "a float prints in the shortest form",
		src: `func id(f float32) float32 { return f }

func main() {
	x := id(1.5)
	third := id(1) / id(3)
	big := id(123456789)
	tiny := id(0.000012345)
	mil := id(1000000)
	tenth := id(0.1)
	neg := id(-2.75)
	zero := id(0)
	hun := id(100000)
	println(x, third, big, tiny, mil, tenth, neg, zero, hun)
	printf("%v %v %v %v %v\n", x, third, big, tiny, mil)
	printf("%g %g %g %g %g %g\n", tenth, neg, zero, hun, id(65536), id(3.4028235e38))
	printf("%.3g %.1g %.4g %.2g %.5g %.0g\n", third, x, id(1234.56), id(123), id(100), neg)
	printf("%10g|%-10g|%+g|% g|%G|%E|%e\n", x, third, x, neg, tiny, tiny, big)
	var d float64 = 2.5
	d2 := d * 2
	half := d / 2
	println(d, d2)
	printf("%v %g %.2g\n", d, half, d)
	print(third, " ", mil, "\n")
	printf("%.7e %f %.10f %08.2f|%-8.1f|%+.2e\n", third, id(123456.789), tenth, neg, x, big)
	printf("%.0f %.1f %f %e %08.3g|%6.2f|%-6.0e|\n", id(2.5), id(0.25), zero, zero, neg, third, hun)
}
`,
		want: "1.5 0.33333334 1.2345679e+08 1.2345e-05 1e+06 0.1 -2.75 0 100000\n1.5 0.33333334 1.2345679e+08 1.2345e-05 1e+06\n0.1 -2.75 0 100000 65536 3.4028235e+38\n0.333 2 1235 1.2e+02 100 -3\n       1.5|0.33333334|+1.5|-2.75|1.2345E-05|1.234500E-05|1.234568e+08\n2.5 5\n2.5 1.25 2.5\n0.33333334 1e+06\n3.3333334e-01 123456.789062 0.1000000015 -0002.75|1.5     |+1.23e+08\n2 0.2 0.000000 0.000000e+00 -0002.75|  0.33|1e+05 |\n",
	},
	{
		// An array argument to a deferred call -- a function, a method, a literal
		// -- is captured where the defer stands, as Go captures it: the deferred
		// call sees 1 2 3 whatever the body wrote into the array afterwards. The
		// capture is an array temporary filled by memcpy, passed at the replay as
		// the pointer an array parameter is. It was refused before ("cannot infer
		// the type of a deferred call argument").
		name: "an array argument to a deferred call",
		src: `type Log struct {
	n int
}

func (l *Log) show(a [3]int, tag string) {
	l.n++
	println("log", tag, a[0], a[1], a[2], l.n)
}

func show(a [3]int, n int) { println("deferred", a[0], a[1], a[2], n) }

func grid(g [2][2]byte) { println("grid", g[0][0], g[0][1], g[1][0], g[1][1]) }

func fill(a *[3]int, v int) {
	for i := range a {
		a[i] = v
	}
}

func main() {
	a := [3]int{1, 2, 3}
	var l Log
	defer show(a, len(a))
	defer l.show(a, "method")
	defer func(b [3]int, k int) { println("literal", b[0], b[1], b[2], k) }(a, 7)
	g := [2][2]byte{{1, 2}, {3, 4}}
	defer grid(g)
	a[0] = 100
	g[1][1] = 40
	fill(&a, 9)
	println("body", a[0], a[2], g[1][1])
}
`,
		want: "body 9 9 40\ngrid 1 2 3 4\nliteral 1 2 3 7\nlog method 1 2 3 1\ndeferred 1 2 3 3\n",
	},
	{
		// min and max of floats follow Go's rules: NaN if any argument is one, and
		// negative zero below positive zero. The helpers were `a < b ? a : b`,
		// which answered the other argument for a NaN -- min(nan, a) printed 0.1
		// on a P2-EDGE where Go prints NaN, a silent wrong answer found by a
		// float32 arithmetic probe diffed against Go -- and whichever zero came
		// first for the zeros.
		name: "min and max of floats follow Go's rules",
		src: `func id(f float32) float32 { return f }

func main() {
	a := id(0.1)
	b := id(0.2)
	z := id(0)
	nz := z * -1
	inf := id(1e38) * id(10)
	nan := inf - inf
	println(min(a, b), max(a, b), min(b, a, -a), max(-b, a, b))
	println(min(nan, a), min(a, nan), max(nan, a), max(a, nan), min(a, b, nan), max(nan, nan))
	println(min(z, nz), min(nz, z), max(z, nz), max(nz, z), min(nz, nz), max(z, z))
	println(min(inf, a), max(-inf, a), min(-inf, inf), max(inf, nan), min(nz, a), max(nz, -a))
	var d float64 = 2.5
	e := d * 2
	println(min(d, e), max(d, e), min(e, d, 1), max(d, e, 7.5))
	x := id(3)
	y := id(-3)
	println(min(x, y) == y, max(x, y) == x, min(x, x) == x, min(z, nz) == nz)
}
`,
		want: "0.1 0.2 -0.1 0.2\nNaN NaN NaN NaN NaN NaN\n-0 -0 0 0 -0 0\n0.1 0.1 -Inf NaN -0 -0\n2.5 5 1 7.5\ntrue true true true\n",
	},
	{
		// min and max compute in the type of ALL their arguments, as an operator's
		// operands are typed: a typed argument decides wherever it is written. The
		// first argument used to decide outright, so a constant written first typed
		// the helper and converted the rest -- min(7, big) truncated an int64 to 3,
		// max(5, u) read a uint32 as a negative int, min(3, f) cut a float32 to 2 --
		// while the same call with the constant second was right. Go's answers,
		// board-verified.
		name: "min and max take their type from every argument",
		src: `type celsius float32

func main() {
	// A constant written FIRST, beside typed arguments of every kind.
	var big int64 = 1<<40 + 3
	println(min(7, big), max(7, big), max(9, 8, big))
	lo := min(7, big)
	var wide int64 = lo
	println(wide)
	var u uint32 = 4000000000
	println(max(5, u), min(5, u))
	var f float32 = 2.5
	println(min(3, f), max(1, f))
	var c celsius = 21.5
	println(max(20, c), min(30, c))
	var i8 int8 = -100
	println(min(3, i8, -5), max(-128, i8))

	// No typed argument: the widest constant decides.
	println(min(1, 2.5), max(2, 1.5))

	// An untyped shift among them takes their type as well.
	var s uint = 40
	println(min(1<<s, big), max(1<<(s+1), big))
}
`,
		want: "7 1099511627779 1099511627779\n7\n4000000000 5\n2.5 2.5\n21.5 21.5\n-100 -100\n1 2\n1099511627776 2199023255552\n",
	},
	{
		// Functions and variables named like the C library's: the math names are
		// MACROS in the target's headers (`#define sqrt(x) __builtin_sqrt(x)`, and
		// ceil is an object-like one too), so a declaration of one was a syntax
		// error there -- "unexpected __builtin_sqrt" -- in a program the host
		// compiled; they are renamed everywhere now, as the keywords are. The
		// stdlib names are declared by the headers and renamed at file scope.
		name: "names the C library has spoken for, declared by the program",
		src: `func sqrt(x int) int { return x * x }

func floor(x float32) float32 { return x - 1 }

func ceil(x float32) float32 { return x + 1 }

func round(x int) int { return x + 10 }

func trunc(s string) string { return s[:2] }

func pow(a, b int) int { return a * b }

func exp(x int) int { return x + 1 }

func log(s string) { println("log:", s) }

func sin(x int) int { return -x }

func cos(x int) int { return x }

func mod(a, b int) int { return a % b }

func copysign(a, b int) int { return a + b }

func memcpy(n int) int { return n * 2 }

func strlen(s string) int { return len(s) * 2 }

func exit(code int) int { return code + 1 }

func main() {
	log("start")
	println(sqrt(7), floor(2.5), ceil(2.5), round(1), trunc("hello"), pow(3, 4), exp(1), sin(2), cos(3))
	println(mod(7, 3), copysign(1, 2), memcpy(21), strlen("abc"), exit(0))
	var fabs, printf, putchar, atoi, abs = 1, 2, 3, 4, 5
	println(fabs+printf+putchar+atoi+abs, min(abs, atoi), max(fabs, printf))
}
`,
		want: "log: start\n49 1.5 3.5 11 he 12 2 -2 3\n1 3 42 6 1\n15 4 2\n",
	},
	{
		// `f(g())`, Go's special case: a call of several results as the whole
		// argument list. The inner call is bound to its result struct ahead of the
		// statement and its fields are the arguments -- a function, a method, a
		// nested pair, 64-bit results, a condition. It was "not enough arguments".
		name: "a call's results passed as another call's arguments",
		src: `func divmod(a, b int) (int, int) { return a / b, a % b }

func swap(a, b int) (int, int) { return b, a }

func add3(a, b, c int) int { return a + b + c }

func three() (int, int, int) { return 1, 2, 3 }

func pair() (string, bool) { return "x", true }

func show(s string, ok bool) { println(s, ok) }

func sum(a, b int) int { return a + b }

func wide() (int64, int64) { return 1 << 40, 3 }

func mix(a, b int64) int64 { return a*31 + b }

type Pt struct {
	x, y int
}

func (p Pt) parts() (int, int) { return p.x, p.y }

func main() {
	println(sum(divmod(17, 5)), add3(three()), sum(swap(swap(1, 2))))
	show(pair())
	q, r := swap(divmod(9, 4))
	p := Pt{4, 9}
	println(q, r, mix(wide()), sum(swap(divmod(7, 2))), sum(p.parts()))
	if sum(divmod(20, 6)) == 5 {
		println("five")
	}
}
`,
		want: "5 6 3\nx true\n1 2 34084860461059 4 13\nfive\n",
	},
	{
		// Arrays are comparable wherever they come from: a call returning one is
		// bound to a temporary and compared by the per-type helper, against a
		// literal, a variable or another call. It was refused as "a value of
		// another type".
		name: "an array-returning call compared",
		src: `func mk3(n int) [3]int { return [3]int{n, n + 1, n + 2} }

func grid(n byte) [2][2]byte { return [2][2]byte{{n, n}, {n, n + 1}} }

func main() {
	t := mk3(9)
	println(mk3(1) == [3]int{1, 2, 3}, mk3(1) == mk3(1), mk3(1) != mk3(2), t == mk3(9), mk3(9) == t, [3]int{1, 2, 3} != mk3(1))
	println(grid(1) == [2][2]byte{{1, 1}, {1, 2}}, grid(1) == grid(2), grid(3) != grid(3))
	n := 0
	if mk3(n) == [3]int{0, 1, 2} {
		n++
	}
	println(n)
}
`,
		want: "true true true true true false\ntrue false false\n1\n",
	},
	{
		// len of an array-returning call is the callee's declared extent, the call
		// still running as Go runs it -- it was refused, "len is only supported for
		// strings, arrays and slices yet". And len of a CONSTANT string is folded:
		// the header field read off the compound literal the constant is spelled
		// as was valid C the target's compiler refused, "request for member len in
		// something not an object" -- found by a text probe, as msg[len(msg)-2].
		name: "len of a call's array and of a constant string",
		src: `const msg = "AT+CFG\r\n"

var calls int

func mk(n int) [4]int {
	calls++
	return [4]int{n, n, n, n}
}

func grid(n byte) [2][3]byte {
	calls++
	return [2][3]byte{{n, n, n}, {n, n, n}}
}

func main() {
	println(len(mk(1)), len(grid(0)), calls)
	if len(mk(2)) == 4 {
		calls += 10
	}
	println(calls, len(msg), msg[len(msg)-2] == '\r', msg[len(msg)-1], msg[:len(msg)-2], len(msg+"x"), len("héllo"))
}
`,
		want: "4 2 2\n13 8 true 10 AT+CFG 9 6\n",
	},
	{
		// A parameter or a variable named like a package constant is what its name
		// means: the constant folders resolved the NAME to the constant, so tag's
		// parameter prefix was main's "AT+" and a library's strings.TrimPrefix cut
		// three bytes where seven were asked for -- a silent wrong answer found by
		// a text probe diffed against Go. A block-scope constant of the name still
		// folds, and a variable after that block is a variable again.
		name: "a local named like a constant is the local",
		src: `const n = 3

const prefix = "AT+"

const ratio = 2.5

const big = 1 << 20

func twice(n int) int { return n * 2 }

func mix(a, b int64) int64 { return a*31 + b }

func wide(n int) int64 { return mix(int64(n), 3) }

func tag(prefix string) string { return prefix }

func scale(ratio float32) float32 { return ratio * 2 }

func widen(big int) int64 { return int64(big) * 2 }

func main() {
	println(n, prefix, len(prefix), ratio, big, twice(21), wide(10), tag("zz"), len(tag("zzz")), scale(1.5), widen(5))
	n := 8
	prefix := "var"
	ratio := float32(0.5)
	println(n*2, twice(n), prefix, len(prefix), tag(prefix), len(tag(prefix)), ratio*4, scale(ratio), mix(int64(n), int64(n)))
	{
		const prefix = "in"
		const n = 100
		println(prefix, len(prefix), tag(prefix), n, twice(n), mix(n, 1))
	}
	println(prefix, n, len(prefix))
}
`,
		want: "3 AT+ 3 2.5 1048576 42 313 zz 3 3 10\n16 16 var 3 var 3 2 1 256\nin 2 in 100 200 3101\nvar 8 3\n",
	},
	{
		// append wraps a concrete value into the two words an interface element is,
		// as an assignment, a parameter and a literal element do. The raw pointer
		// went out unwrapped and neither compiler accepted it, so filling a device
		// table the ordinary way did not build.
		name: "a concrete value appended to a slice of interfaces",
		src: `type Shape interface {
	Area() int
}

type Sq struct {
	s int
}

func (q *Sq) Area() int { return q.s * q.s }

var a = Sq{s: 3}

var b = Sq{s: 4}

func main() {
	var backing [4]Shape
	shapes := backing[:0]
	shapes = append(shapes, &a)
	shapes = append(shapes, &b, &a)
	var s Shape = &b
	shapes = append(shapes, s)
	total := 0
	for i := range shapes {
		total += shapes[i].Area()
	}
	println(len(shapes), cap(shapes), total, shapes[1].Area(), shapes[3].Area())
}
`,
		want: "4 4 50 16 16\n",
	},
	{
		// A method of several results reached through an interface: on a variable,
		// on a slice element -- the device-table shape -- on a package array's, on
		// a struct field's, forwarded through a return, and forwarded as another
		// call's arguments. Only the plain variable worked: the rest reported a
		// count mismatch, and `return s.Read()` made C out of a void call, the slot
		// writing its results through a parameter rather than returning them.
		name: "an interface method of several results, in every position",
		src: `type devErr struct {
	msg string
}

func (e *devErr) Error() string { return e.msg }

var ErrOffline = devErr{msg: "offline"}

type Sensor interface {
	Read() (int32, error)
	Name() string
}

type Thermo struct {
	cal    int32
	online bool
}

func (t *Thermo) Read() (int32, error) {
	if !t.online {
		return 0, &ErrOffline
	}
	return t.cal * 2, nil
}

func (t *Thermo) Name() string { return "thermo" }

type Bus struct {
	active Sensor
}

func sum(v int32, err error) int32 {
	if err != nil {
		return -1
	}
	return v
}

var th = Thermo{cal: 21, online: true}

var off = Thermo{cal: 9}

var bus Bus

var pool [2]Sensor

func viaSlice(devs []Sensor) (int32, error) { return devs[0].Read() }

func viaField(u *Bus) (int32, error) { return u.active.Read() }

func main() {
	var backing [3]Sensor
	devs := backing[:0]
	devs = append(devs, &th)
	devs = append(devs, &off)
	total := int32(0)
	fails := 0
	for i := range devs {
		v, err := devs[i].Read()
		if err != nil {
			fails++
			continue
		}
		total += v
	}
	println(len(devs), total, fails, devs[0].Name(), devs[1].Name())
	bus.active = &th
	pool[0] = &off
	v, err := bus.active.Read()
	w, err2 := pool[0].Read()
	println(v, err == nil, w, err2 == &ErrOffline, err2.Error())
	a, e1 := viaSlice(devs)
	b, e2 := viaField(&bus)
	println(a, e1 == nil, b, e2 == nil)
	var s Sensor = &off
	println(sum(s.Read()), sum(th.Read()), sum(devs[0].Read()))
}
`,
		want: "2 42 1 thermo thermo\n42 true 0 true offline\n42 true 42 true\n-1 42 42\n",
	},
	{
		// An array-returning method on every receiver -- a variable, a package
		// array's element, a struct field, a slice element -- declared, assigned,
		// passed, indexed, measured and returned. Only a plain variable worked; the
		// rest could not even be typed. And the call ran ONCE per occurrence: each
		// path that asked about it minted a temporary and emitted the call, so
		// `d.triple()[0]` ran the method three times, which a counting method makes
		// visible and any method with side effects would answer differently for.
		name: "an array-returning method on every receiver",
		src: `type Dev struct {
	id    int
	calls int
}

func (d *Dev) triple() [3]int {
	d.calls++
	return [3]int{d.id, d.calls, 7}
}

type Rack struct {
	slot Dev
}

func take(t [3]int) int { return t[1] }

var pool [2]Dev

var rack Rack

var backing [2]Dev

func fromPool() [3]int { return pool[1].triple() }

func main() {
	var d Dev
	d.id = 1
	println(d.triple()[0], d.triple()[1], d.calls)
	pool[1].id = 2
	t := pool[1].triple()
	var u [3]int
	u = pool[1].triple()
	n := pool[1].calls
	println(t[0], u[1], n)
	println(take(pool[1].triple()), len(pool[1].triple()), pool[1].calls)
	println(pool[1].triple()[0], pool[1].triple()[1], pool[1].calls)
	rack.slot.id = 3
	r := rack.slot.triple()
	println(r[0], rack.slot.triple()[1], rack.slot.calls)
	ds := backing[:]
	ds[0].id = 4
	s := ds[0].triple()
	println(s[0], ds[0].triple()[1], ds[0].calls, fromPool()[0], pool[1].calls)
}
`,
		want: "1 2 2\n2 2 2\n3 3 4\n2 6 6\n3 2 2\n4 2 2 2 7\n",
	},
	{
		// The result of a call indexed and sliced where it stands: a string's byte
		// and its substring, a slice's element and its reslice, and a slice of a
		// slice indexed again. Indexing a string result emitted the index on the
		// header struct, which the C compiler refused; slicing any result was
		// "unsupported call in expression"; and binding one was "cannot infer a
		// type", the typing walks modelling a chain from a NAME only. The counter
		// holds each to one call, as Go has it.
		name: "the result of a call indexed and sliced",
		src: `var calls int

var backing = [5]int{10, 20, 30, 40, 50}

func name() string {
	calls++
	return "abcdef"
}

func rows() []int {
	calls++
	return backing[:4]
}

func main() {
	println(name()[0], name()[1:3], calls)
	s := name()[2:]
	println(s, len(s), calls)
	r := rows()[1:3]
	println(len(r), r[0], calls)
	println(rows()[2], len(rows()[1:]), calls)
	println(name()[1:4][1], rows()[1:][0], calls)
}
`,
		want: "97 bc 2\ncdef 4 3\n2 20 4\n30 3 6\n99 20 8\n",
	},
	{
		// The other half of "a struct field named after a type": the backend
		// resolves an identifier as a typedef name before reading the declarator,
		// so `reading reading` as a local, a parameter, a receiver, a range or a
		// loop variable is a syntax error there, as is a vtable member for a method
		// named `Sample() Sample`. gcc takes all of them
		// (doc/member-named-like-type.c).
		name: "a local, a parameter and a method named after a type",
		src: `// typename: a local, a parameter and a receiver named after a type -- ordinary Go,
// which the target's C compiler cannot parse unless the name is moved out of the
// way. Every kind of binding is here.

type reading struct {
	v int
}

type level int

type pair struct {
	a, b int
}

func (reading *reading) get() int { return reading.v }

func take(reading reading, level level) int { return reading.v + int(level) }

func two() (int, int) { return 3, 4 }

var samples [3]reading

func main() {
	var reading reading
	reading.v = 5
	println(reading.v, reading.get(), take(reading, 2))

	level := level(7)
	println(int(level) + 1)

	for i, reading := range samples {
		reading.v = i
		println(i, reading.v)
	}

	pair := pair{a: 1, b: 2}
	println(pair.a, pair.b)

	if reading := 9; reading > 5 {
		println("if", reading)
	}

	switch level := 3; level {
	case 3:
		println("switch", level)
	}

	a, level2 := two()
	println(a, level2)

	for reading := 0; reading < 2; reading++ {
		println("for", reading)
	}
}
`,
		want: "5 5 7\n8\n0 0\n1 1\n2 2\n1 2\nif 9\nswitch 3\n3 4\nfor 0\nfor 1\n",
	},
	{
		// The sampling loop of a real instrument: a worker cog reads every device
		// through an interface -- a method of several results, a struct result, a
		// string -- and sends what it read back over channels. A struct returned
		// through a function pointer on a cog is where a silent wrong answer once
		// lived (doc/struct-return-through-pointer-on-cog.c), which is why the
		// interface's out-parameter form is exercised on a cog and not only in main.
		name: "a cog polling a device table through an interface",
		src: `// cogiface: a worker cog polling a device table through an interface -- the shape
// the sampling loop of a real instrument takes -- with multi-result methods, a
// struct result, and results returned over channels.

type devErr struct {
	msg string
}

func (e *devErr) Error() string { return e.msg }

var ErrOffline = devErr{msg: "offline"}

type Sample struct {
	ch    uint8
	ok    bool
	raw   int16
	value int32
}

type Sensor interface {
	Read() (int32, error)
	Sample() Sample
	Name() string
}

type Thermo struct {
	id     uint8
	cal    int32
	online bool
}

func (t *Thermo) Read() (int32, error) {
	if !t.online {
		return 0, &ErrOffline
	}
	return t.cal * 2, nil
}

func (t *Thermo) Sample() Sample {
	return Sample{ch: t.id, ok: t.online, raw: int16(t.cal), value: t.cal * 10}
}

func (t *Thermo) Name() string { return "thermo" }

type Press struct {
	id  uint8
	raw int16
}

func (p *Press) Read() (int32, error) { return int32(p.raw) * 3, nil }

func (p *Press) Sample() Sample {
	return Sample{ch: p.id, ok: true, raw: p.raw, value: int32(p.raw) * 30}
}

func (p *Press) Name() string { return "press" }

var t1 = Thermo{id: 1, cal: 21, online: true}

var t2 = Thermo{id: 2, cal: 9}

var p1 = Press{id: 3, raw: 7}

var devs [3]Sensor

// poller runs on a cog: it reads every device through the interface and sends
// each result back, then the count.
func poller(vals chan int32, fails chan int32, done chan int32) {
	var bad int32
	for i := range devs {
		v, err := devs[i].Read()
		if err != nil {
			bad++
			continue
		}
		vals <- v
	}
	fails <- bad
	done <- 1
}

// sampler runs on a cog and sends STRUCTS back, the shape a function pointer on a
// cog once returned wrong.
func sampler(out chan Sample, done chan int32) {
	for i := range devs {
		out <- devs[i].Sample()
	}
	done <- 1
}

// namer sends the interface's own strings back.
func namer(out chan string, done chan int32) {
	for i := range devs {
		out <- devs[i].Name()
	}
	done <- 1
}

func main() {
	devs[0] = &t1
	devs[1] = &t2
	devs[2] = &p1

	var vals chan int32
	var fails chan int32
	var done chan int32
	go poller(vals, fails, done)
	a := <-vals
	b := <-vals
	bad := <-fails
	<-done
	println("read", a, b, bad)

	var samples chan Sample
	var sdone chan int32
	go sampler(samples, sdone)
	var total int32
	var chans int
	for i := 0; i < 3; i++ {
		s := <-samples
		total += s.value
		chans += int(s.ch)
		if i == 1 {
			println("sample1", s.ch, s.ok, s.raw, s.value)
		}
	}
	<-sdone
	println("samples", total, chans)

	var names chan string
	var ndone chan int32
	go namer(names, ndone)
	n0 := <-names
	n1 := <-names
	n2 := <-names
	<-ndone
	println(n0, n1, n2, len(n2))
}
`,
		want: "read 42 21 1\nsample1 2 false 9 90\nsamples 510 6\nthermo thermo press 5\n",
	},
	{
		// The quoted and hex verbs a protocol logger prints with: %q of a string, a
		// byte slice and a rune, %x and %X of either, %s of a byte slice, %U of a
		// rune. Every escape Go writes is here, including a byte that is not valid
		// UTF-8 -- which Go escapes and a pass-through would not -- and a rune above
		// ASCII, which it does not.
		name: "the quoted and hex verbs over bytes",
		src: `var raw = [6]byte{'A', 'B', 13, 10, 200, 0}

var line = [4]byte{'h', 'i', '!', 9}

func main() {
	msg := "AT+CFG=1\r\n"
	printf("%q\n", msg)
	printf("%q %q\n", "plain", "")
	printf("%q\n", "tab\there \"quoted\" back\\slash")
	printf("%q\n", "héllo")
	printf("%x %X\n", "abc", "abc")
	printf("%x|%X|\n", raw[:4], raw[:])
	printf("%q\n", raw[:4])
	printf("%q\n", raw[:])
	printf("%s|%s|\n", line[:3], raw[:2])
	printf("%q %q %q %q\n", 'A', '\n', rune(233), rune(0))
	printf("%q %q\n", '\'', '\\')
	printf("%U %U %U\n", 'A', rune(233), rune(0x1F600))
	printf("%x %q %s\n", msg, msg[3:6], msg[0:3])
}
`,
		want: "\"AT+CFG=1\\r\\n\"\n\"plain\" \"\"\n\"tab\\there \\\"quoted\\\" back\\\\slash\"\n\"héllo\"\n616263 616263\n41420d0a|41420D0AC800|\n\"AB\\r\\n\"\n\"AB\\r\\n\\xc8\\x00\"\nhi!|AB|\n'A' '\\n' 'é' '\\x00'\n'\\'' '\\\\'\nU+0041 U+00E9 U+1F600\n41542b4346473d310d0a \"CFG\" AT+\n",
	},
	{
		// A comparison with a constant between an int and a uint32 in value, which C
		// spells as an unsigned int, `2560000000U`. Against a negative constant C
		// then compares unsigned -- `hz*16 > -1` was false under the host's compiler
		// (the target's folds constants wider than C allows and happened to agree
		// with Go) -- and against an int64 variable the target's compiler compared
		// unsigned too, warning, so `v < hz*16` for v = -5 was false on the board.
		name: "a comparison with a constant past an int",
		src: `const hz = 160000000

const big = 3000000000

func main() {
	var v int64 = -5
	var u64 uint64 = 5
	var w int64 = 3000000000
	println(hz*16 > -1, -1 < hz*16, 3000000000 > -2, hz*16 != -1294967296, 2560000000-hz*16 == 0)
	println(v < hz*16, v < 3000000000, v < big, u64 < 3000000000, w == 3000000000, w > big-1)
	switch w {
	case 3000000000:
		println("case wide")
	}
}
`,
		want: "true true true true true\ntrue true true true true true\ncase wide\n",
	},
	{
		// The same, for a constant whose VALUE fits a C int and whose way there does
		// not: `hz * 16 / 1000000` is 2560 by way of 2560000000, which C computes in
		// int and wraps -- the board printed -1734 -- and `one * one / 3` printed 0.
		// Every position the expression can stand in, a package initializer and an
		// array bound among them, and an int64 one past 64 bits: `3 << 62 >> 61` is 6,
		// which the 64-bit fold, wrapping, spelled -2.
		name: "a constant whose intermediate value is too wide for a C int",
		src: `const hz = 160000000

const one = 1 << 16

const baud = 230400

var pkgDiv = hz * 16 / 1000000

var pkgQ int64 = 3 << 62 >> 61

const divisor = hz * 16 / baud

var table [3 << 62 >> 61]int

func id(v int) int { return v }

func ret() int32 { return one * one / 3 }

func main() {
	a := hz * 16 / 1000000
	var b int32 = one * one / 3
	c := id(hz * 8 / 1000)
	d := hz*16/1000000 == 2560
	var e uint32 = 1 << 32 / 1000
	f := -(one * one) / 3
	var g int64 = hz * hz / hz
	h := 1<<40>>38 + 1
	arr := [8]int{7, 6, 5, 4, 3, 2, 1, 0}
	i := arr[one*one/1073741824]
	switch hz * 16 / 1000000 {
	case 2560:
		println("case ok")
	}
	var q int64 = 3 << 62 >> 61
	table[5] = 9
	println(a, b, c, d, e, f, g, h, i, q, pkgDiv, pkgQ, divisor, len(table), table[5], ret())
	// A literal past an int is an unsigned int in C, not a long long, so it wraps
	// at 32 bits: 4294967295U * 2 / 4 is 1073741823 there.
	var ux uint32 = 0xFFFFFFFF * 2 / 4
	sx := 0x80000000 * 2 / 4
	println(ux, sx, 3000000000-2000000000)
}
`,
		want: "case ok\n2560 1431655765 1280000 true 4294967 -1431655765 160000000 5 3 6 2560 6 11111 6 9 1431655765\n2147483647 1073741824 1000000000\n",
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
	// The most negative value, which has no literal of its own in C and whose two
	// spellings as an expression the target's compiler does not treat alike: with
	// its inliner on, "(-9223372036854775807LL - 1)" came out with one too many
	// in its high word, and "(-1 - 9223372036854775807LL)" did not
	// (doc/int64-min-spelling.c). In a local, beside a variable, through a call,
	// compared, and as the top bit of an unsigned.
	var mn int64 = -1 << 63
	var top uint64 = 1 << 63
	println(mn, mn+1, take(mn), mn < 0, mn == -1<<63, top, top>>63, top+1)
}
`,
		want: "1099511627776 6000000000 9223372036854775808 1099511627776 9223372036854775808\n-4611686018427387904\n1099511627776 6000000000\n1099511627776\n2199023255552 1099511627776\n-1099511627776 -6000000000 -68719476736 1099511627776\n1073741824 536870912\n-9223372036854775808 -9223372036854775807 -9223372036854775808 true true 9223372036854775808 1 9223372036854775809\n",
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
		// Ranging an array of ARRAYS, whose value is a row. The loop declared the
		// value with the array's innermost element type -- `int row = table[i]` for a
		// [3][2]int -- which no C compiler accepts. The same loop over a SLICE of
		// rows was always right, because a slice's element type is the row's typedef
		// and the value inject reads exactly that registry to know it has an array
		// to copy; an array container handed it the innermost element instead.
		//
		// The value is a COPY, as Go's range value is, which the third block checks:
		// a lowering that aliased the row would pass every other line here.
		name: "range over an array of arrays",
		src: `type Row [2]int

var table = [3][2]int{{1, 2}, {3, 4}, {5, 6}}

var named = [2]Row{{7, 8}, {9, 10}}

var cube = [2][2][2]int{{{1, 2}, {3, 4}}, {{5, 6}, {7, 8}}}

func main() {
	sum := 0
	for i, row := range table {
		sum += i*100 + row[0] + row[1]
	}
	println(sum)

	// A defined row type, and a rank above two: the value is an array either way.
	nsum := 0
	for _, row := range named {
		nsum += row[0] + row[1]
	}
	csum := 0
	for _, plane := range cube {
		for _, row := range plane {
			csum += row[0] + row[1]
		}
	}
	println(nsum, csum)

	// Writing to the value leaves the table alone.
	for _, row := range table {
		row[0] = 99
	}
	println(table[0][0], table[1][0])

	// The assigning form, whose value variable is declared outside the loop and so
	// still holds the last row afterwards.
	var last [2]int
	var at int
	for at, last = range table {
	}
	println(at, last[0], last[1])
}
`,
		want: "321\n34 36\n1 3\n2 5 6\n",
	},
	{
		// Ranging an array reached through a CHAIN -- an array-typed field, one of a
		// defined array type, a nested field, an element of an array of arrays, and a
		// chain that starts at a slice. The operand walk took a bare name, a pointer
		// to an array and that dereference written out, so every one of these fell
		// past it to the integer case and was reported as "ranging an integer yields
		// only the index" -- of an array whose type the program had written down.
		// Iterating a struct's own buffer is ordinary Go.
		//
		// The chain is bound to a POINTER first, so the operand is evaluated once
		// however many iterations follow, which is what Go does; the last block reads
		// an index the body then changes, and would follow it otherwise. The
		// index-only form binds nothing: it reaches no element, so there is no base
		// to name and a temporary bound anyway is one the C compiler calls unused.
		//
		// `range xs[1].xs` is why the array case is asked before the slice one: the
		// operand's C type there comes back as the SLICE the chain starts from, and
		// the loop ranged the header rather than the field.
		name: "range over an array reached through a chain",
		src: `type Row [3]int

type Inner struct {
	xs [3]int
}

type Buf struct {
	xs    [3]int
	r     Row
	rows  [2][3]int
	inner Inner
}

var pool = [2][3]int{{1, 2, 3}, {4, 5, 6}}

var b = Buf{[3]int{1, 2, 3}, Row{4, 5, 6}, [2][3]int{{7, 8, 9}, {1, 1, 1}}, Inner{[3]int{2, 2, 2}}}

var bufs [2]Buf

func sum3(xs [3]int) int { return xs[0] + xs[1] + xs[2] }

func main() {
	// An array-typed FIELD, a field of a defined array type, a nested field, and an
	// element of an array of arrays -- every route the copy already reads from.
	n := 0
	for _, v := range b.xs {
		n += v
	}
	for _, v := range b.r {
		n += v
	}
	for _, v := range b.inner.xs {
		n += v
	}
	for _, v := range pool[1] {
		n += v
	}
	println(n)

	// The index-only form, which reaches no element and so needs no base.
	m := 0
	for i := range b.xs {
		m += b.xs[i]
	}
	println(m)

	// A field whose value is a row, and a chain that starts at a SLICE: the
	// operand's own shape decides, not the type of what the chain starts from.
	t := 0
	for _, row := range b.rows {
		t += sum3(row)
	}
	bufs[0] = b
	bufs[1] = b
	xs := bufs[:]
	for _, v := range xs[1].xs {
		t += v
	}
	println(t)

	// The operand is evaluated ONCE, as Go evaluates it: the row is chosen when the
	// loop starts and does not follow i.
	i := 0
	got := 0
	for _, v := range pool[i] {
		i = 1
		got += v
	}
	println(got, i)
}
`,
		want: "42\n6\n33\n6 1\n",
	},
	{
		// A multiple assignment that moves ARRAYS. Every value of one is bound to a
		// temporary first -- which is what makes `a, b = b, a` a swap -- and an array
		// has no C value type to declare a temporary of, so inferCType answered no
		// and the whole statement was "cannot infer the type of a value in a multiple
		// assignment". That took out `table[i], table[j] = table[j], table[i]`, the
		// swap every sort of a table of rows is written with.
		//
		// The temporary is the copy `b := a` already is, and each target then takes
		// its own: declared and copied into for a ":=", memcpy'd for an "=". The sort
		// is here rather than a bare swap because a swap that aliased instead of
		// copying still prints two plausible numbers, and a sort does not.
		name: "a multiple assignment moving arrays",
		src: `type H struct {
	f [2]int
}

var table = [4][2]int{{4, 4}, {2, 2}, {3, 3}, {1, 1}}

var h H

var a = [2]int{7, 8}

func main() {
	// The swap every sort of a table of rows is written with.
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			if table[j][0] < table[i][0] {
				table[i], table[j] = table[j], table[i]
			}
		}
	}
	println(table[0][0], table[1][0], table[2][0], table[3][0])

	// Declared targets, a field target, a literal value, a mixed list and a blank.
	p, q := table[0], table[3]
	p[0] = 99
	println(p[0], q[0], table[0][0])

	h.f, a = a, [2]int{5, 6}
	println(h.f[0], a[0])

	var n int
	var r [2]int
	n, r = 7, table[2]
	println(n, r[0])

	var s [2]int
	_, s = table[0], table[1]
	println(s[0])
}
`,
		want: "1 2 3 4\n99 4 1\n7 5\n7 3\n2\n",
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
	},
	{
		// printf is print/println's formatted sibling: the same compiler magic over a
		// CONSTANT format, which is what lets every verb be checked against its
		// argument here rather than going wrong at run time. Output was diffed against
		// the same program written for Go's fmt.Printf, and matches it line for
		// line -- %T of a defined type included, "main.Celsius": the package clause
		// this language lacks is what Go spells there, so it is spelled here too.
		name: "printf formats every verb as Go does",
		src: `type Celsius int

// String makes Celsius a Stringer: %v and %s print what it returns, as fmt's do,
// while %d prints the number and println never calls it, as Go's do not.
func (c Celsius) String() string {
	if c < 0 {
		return "cold"
	}
	return "warm"
}

type Pt struct{ X, Y int }

func (p *Pt) String() string { return "pt" }

type perr struct{ msg string }

func (e *perr) Error() string { return e.msg }

var errBusy = perr{"busy"}

func main() {
	n := 42
	s := "hi"
	b := true
	var f float64 = 1.5
	var u uint8 = 7
	var c Celsius = 3
	var big int64 = -5000000000
	var ub uint32 = 4000000000
	printf("d=%d s=%s t=%t f=%f u=%d c=%d\n", n, s, b, f, u, c)
	printf("big=%d ub=%d hex=%x HEX=%X 100%%\n", big, ub, 255, 255)
	var i8 int8 = -1
	var i32 int32 = -2147483648
	var i64 int64 = -1099511627776
	printf("neg: %x %X | %x %x %x | %x %x\n", -255, -255, i8, i32, i64, 0, ub)
	printf("T: %T %T %T %T %T %T\n", n, s, b, f, c, ub)
	xs := []int{1, 2, 3}
	var a [2]string
	a[0] = "hi"
	a[1] = "yo"
	printf("v: %v %v %v %v %v\n", n, s, b, f, xs)
	printf("arr: %v\n", a)
	printf("c: %c%c%c %c %c\n", 'H', 'i', '!', 'é', 955)
	printf("o: %o %o %o %5o %-5o| b: %b %b %b %b\n", 8, -8, u, u, u, 5, -5, u, 0)
	printf("e: %e %.2e %8.3e\n", 12345.678, 0.00025, -1e10)
	printf("exp: %v %v\n", 1e10, 1e-7)
	println(1e10, 1e-7, 2.5e8, 123456.0)
	p := Pt{1, 2}
	var err error = &errBusy
	var none error
	printf("str: %v %s %d %-6v| %5s %v %s %v %s %v\n", c, Celsius(-4), c, c, c, &p, &p, err, err, none)
	println(c)
	printf("no verbs\n")
}
`,
		want: `d=42 s=hi t=true f=1.500000 u=7 c=3
big=-5000000000 ub=4000000000 hex=ff HEX=FF 100%
neg: -ff -FF | -1 -80000000 -10000000000 | 0 ee6b2800
T: int string bool float64 main.Celsius uint32
v: 42 hi true 1.5 [1 2 3]
arr: [hi yo]
c: Hi! é λ
o: 10 -10 7     7 7    | b: 101 -101 111 0
e: 1.234568e+04 2.50e-04 -1.000e+10
exp: 1e+10 1e-07
1e+10 1e-07 2.5e+08 123456
str: warm cold 3 warm  |  warm pt pt busy busy <nil>
3
no verbs
`,
	},
	{
		// %T of an INTERFACE is the one verb answered at run time: the vtable leads
		// with the name of the type it was built for, so the dynamic type costs one
		// pointer read. A value carrying no table carries no type, which prints <nil>
		// as Go's does. The argument is bound to a temporary because the table is read
		// twice and mk() must not run twice.
		name: "%T reports an interface's dynamic type",
		src: `type Shape interface {
	area() int
}

type Sq int

func (s *Sq) area() int {
	return int(*s) * int(*s)
}

type Circ int

func (c *Circ) area() int {
	return 3 * int(*c) * int(*c)
}

var q = Sq(4)

var c2 = Circ(2)

var calls int

func mk(which int) Shape {
	calls++
	if which == 0 {
		return &q
	}
	return &c2
}

func main() {
	for i := 0; i < 2; i++ {
		sh := mk(i)
		printf("%T area=%d\n", sh, sh.area())
	}
	printf("%T\n", mk(0))
	println(calls)
	var nilf Shape
	printf("nil=%T\n", nilf)
}
`,
		want: `*main.Sq area=16
*main.Circ area=12
*main.Sq
3
nil=<nil>
`,
	},
	{
		// print and println give a pointer, a func value and an interface the form
		// Go's BUILTIN println gives them -- an address, the interface as its two
		// words. printf follows fmt instead, which prints those differently, so %v
		// declines them and %T answers for the type. The 0x is written out because C
		// suppresses %#x's prefix for a zero value, where Go prints 0x0. Only the nil
		// forms are asserted: a real address is not the same twice.
		name: "a pointer, func and interface print as an address",
		src: `type Shape interface {
	area() int
}

func main() {
	var p *int
	var f func()
	var sh Shape
	println(p)
	println(f)
	println(sh)
	printf("%T %T\n", p, sh)
}
`,
		want: `0x0
0x0
(0x0,0x0)
*int <nil>
`,
	},
	{
		// append(s, xs...) -- the SPREAD form. It parsed and type-checked from the
		// day append shipped and the ellipsis was then IGNORED, so the whole slice
		// was emitted where one element belonged: ogo_append_int(buf, src). Both C
		// compilers refuse that, so it was a build break rather than a wrong answer,
		// but it was a build break in the user's C.
		//
		// One memmove rather than a loop, which is also what makes the overlapping
		// case right: append(o, o...) is legal Go and copies a region onto one that
		// runs into it. Every line here was diffed against the same program run by
		// Go.
		name: "append spreads a slice",
		src: `type pt struct {
	x int
	y int
}

func main() {
	var back [16]byte
	buf := back[:0]
	buf = append(buf, "hi"...)
	buf = append(buf, ", "...)
	src := []byte{119, 111}
	buf = append(buf, src...)
	buf = append(buf, 33)
	println(len(buf), buf[0], buf[4], buf[6])

	var ib [8]int
	is := ib[:0]
	is = append(is, []int{7, 8}...)
	println(len(is), is[0], is[1])

	var none []int
	is = append(is, none...)
	println(len(is))

	var ov [8]int
	o := ov[:2]
	o[0] = 5
	o[1] = 6
	o = append(o, o...)
	println(len(o), o[0], o[1], o[2], o[3])

	var pb [4]pt
	ps := pb[:0]
	ps = append(ps, []pt{{1, 2}, {3, 4}}...)
	println(len(ps), ps[0].x, ps[1].y)
}
`,
		want: "7 104 119 33\n2 7 8\n2\n4 5 6 5 6\n2 1 4\n",
	},
	{
		// The spread traps on overflow like the single-value form, and is ALL OR
		// NOTHING: a partial append would leave the caller nothing to read it from,
		// ok being one bool for the call.
		name: "a spread past capacity traps",
		src: `func main() {
	var back [3]byte
	buf := back[:0]
	buf = append(buf, "toolong"...)
	println(len(buf))
}
`,
		panics: true,
		want:   "panic: append: out of capacity\n",
	},
	{
		// The ok form of the spread, in both its shapes: a whole slice and a string
		// onto a []byte. The last one does not fit, so nothing of it is appended.
		name: "the two-result append spreads too",
		src: `func main() {
	var small [4]byte
	s := small[:0]
	s, ok := append(s, "ab"...)
	println(len(s), ok)

	s2, ok2 := append(s, []byte{99}...)
	println(len(s2), ok2)

	s3, ok3 := append(s2, "xyz"...)
	println(len(s3), ok3)
}
`,
		want: "2 true\n3 true\n3 false\n",
	},
	{
		// A string literal is DECODED and RE-QUOTED for C rather than passed through.
		// Go and C share the common escapes and part company on the rest, and the
		// passthrough was wrong wherever they do: C's "\x" has no length limit, so
		// "a\xffb" read there as an 'a' and ONE escape of value 0xffb -- a warning
		// from the compiler, a two-byte string, and a program that then could not
		// find its own 'b'. Go's "\u2028" is three UTF-8 bytes here and a universal
		// character name there.
		//
		// The bytes are what is emitted now: printable ASCII as itself, the escapes
		// both languages spell alike as themselves, everything else as three-digit
		// octal, which C caps at three digits so it always ends where written.
		name: "string escapes are re-quoted for C",
		src: "func main() {\n" +
			"\tbad := \"a\\xffb\"\n" +
			"\tprintln(len(bad), bad[0], bad[1], bad[2])\n" +
			"\tu := \"\\u2028x\"\n" +
			"\tprintln(len(u), u[0], u[1], u[2], u[3])\n" +
			"\toct := \"\\101\\1027\"\n" +
			"\tprintln(len(oct), oct[0], oct[1], oct[2])\n" +
			"\tprintln(\"tab\\there\\nnl\")\n" +
			"}\n",
		want: "3 97 255 98\n4 226 128 168 120\n3 65 66 55\ntab\there\nnl\n",
	},
	{
		// A method PROMOTED from an embedded field satisfies an interface, as it does
		// in Go. It always satisfied a direct call -- b.get() reached A's get -- and
		// the interface check read the type's OWN methods only, so one method-set
		// question was answered two different ways: a method you could call was not
		// a method you could put behind the interface it was written for.
		//
		// The vtable thunk is what the fix has to reach: a promoted method takes the
		// EMBEDDED sub-object as its receiver, not the whole struct, so the thunk
		// walks the field path in. Two levels deep, a value receiver, and an outer
		// method OVERRIDING the promoted one (the shallowest wins, as in Go) are all
		// here, and every line was diffed against the same program run by Go.
		name: "an embedded type's method satisfies an interface",
		src: `type Getter interface {
	get() int
}

type A struct {
	n int
}

func (a *A) get() int {
	return a.n
}

type V struct {
	v int
}

func (v V) get() int {
	return v.v
}

type B struct {
	A
}

type C struct {
	B
	k int
}

type D struct {
	A
}

func (d *D) get() int {
	return d.n * 10
}

type W struct {
	V
}

func main() {
	var b B
	b.n = 7
	var g Getter = &b
	println(g.get())

	var c C
	c.n = 9
	c.k = 1
	g = &c
	println(g.get(), c.k)

	var d D
	d.n = 4
	g = &d
	println(g.get(), d.A.get())

	var w W
	w.v = 3
	g = &w
	println(g.get())

	switch t := g.(type) {
	case *W:
		println("W", t.get())
	default:
		println("other")
	}
	println(g.(*W).get())
}
`,
		want: "7\n9 1\n40 4\n3\nW 3\n3\n",
	},
	{
		// A conversion to an INTERFACE type, in every position one can stand in bar
		// the two that need a second cog -- a channel send and a `go` argument,
		// which the refusal table exercises instead. Every one was refused --
		// "cannot convert to Shape" -- because the conversion emitter compares
		// REPRESENTATIONS, and a `Quad*` operand is not the two-word interface
		// struct. What it needed instead was the pair the interface machinery
		// already builds at an assignment, an argument and a return.
		//
		// `any(x)` is the same conversion under the name the universe holds rather
		// than a declaration, and is the ordinary way a program says "as an
		// interface". Interface-to-interface is here too, `Shape(n)` narrowing a
		// wider one.
		//
		// The output is byte-identical to the same program built by Go.
		name: "a conversion to an interface type",
		src: `type Shape interface {
	area() int
}

type Named interface {
	area() int
	name() string
}

type Quad struct {
	w int
	h int
}

func (q *Quad) area() int    { return q.w * q.h }
func (q *Quad) name() string { return "quad" }

type Box struct {
	s Shape
}

var gq = Quad{3, 4}
var gr = Quad{5, 6}
var g Shape

func area(s Shape) int { return s.area() }

func mk() Shape { return Shape(&gq) }

func main() {
	s := Shape(&gq)
	println(s.area())
	var v Shape = Shape(&gr)
	println(v.area())
	g = Shape(&gq)
	println(g.area())
	println(area(Shape(&gr)))
	println(mk().area())
	println(Shape(&gq).area())
	b := Box{Shape(&gr)}
	println(b.s.area())
	t := []Shape{Shape(&gq), Shape(&gr)}
	println(t[0].area())
	println(t[1].area())
	var arr [2]Shape = [2]Shape{Shape(&gr), Shape(&gq)}
	println(arr[0].area())
	println(arr[1].area())
	var n Named = &gq
	w := Shape(n)
	println(w.area())
	println(w.area() == area(n))
	a := any(&gr)
	if p, ok := a.(*Quad); ok {
		println(p.w)
		println(p.h)
	}
	switch x := any(&gq).(type) {
	case *Quad:
		println(x.name())
	}
}
`,
		want: "12\n30\n12\n30\n12\n12\n30\n12\n30\n30\n12\n12\ntrue\n5\n6\nquad\n",
	},
	{
		// Every expression of POINTER type may become an interface value, not just
		// the three shapes that could: a call's result, a pointer field, an element of
		// an array of pointers, and a call whose result comes from any of those. Go
		// accepts all of them -- `var s Shape = New()` is how a constructor is used --
		// and each was refused with "an interface holds a pointer: write the address of
		// a variable", advice that does not apply to a value already pointing at one.
		//
		// As an ARGUMENT it was not even refused: the raw pointer went where the two
		// words belong and the target's C compiler reported "expected _struct__Shape
		// but got pointer to _struct__Quad" about generated code.
		//
		// The values are deliberately distinct -- 12, 30, 49 -- so a table or a data
		// word reaching the wrong one shows. Byte-identical to the same program under
		// Go.
		name: "any pointer expression as an interface value",
		src: `type Shape interface {
	Area() int
}

type Quad struct {
	W int
	H int
}

func (q *Quad) Area() int { return q.W * q.H }

type Round struct {
	R int
}

func (c *Round) Area() int { return c.R * c.R }

type Box struct {
	p *Quad
	s Shape
}

var small = Quad{3, 4}
var big = Quad{5, 6}
var disc = Round{7}
var ptrs [2]*Quad
var box Box
var g Shape

func get() *Quad { return &small }

func pick(n int) *Quad {
	if n == 0 {
		return &small
	}
	return ptrs[1]
}

func take(s Shape) int { return s.Area() }

func mk() Shape { return get() }

func main() {
	ptrs[0] = &small
	ptrs[1] = &big
	box.p = &big
	var s Shape = get()
	println(s.Area())
	var t Shape = box.p
	println(t.Area())
	var u Shape = ptrs[1]
	println(u.Area())
	println(take(get()))
	println(take(box.p))
	println(take(ptrs[0]))
	g = get()
	println(g.Area())
	g = box.p
	println(g.Area())
	println(mk().Area())
	println(take(pick(1)))
	v := []Shape{get(), box.p, ptrs[0], &disc}
	println(v[0].Area())
	println(v[1].Area())
	println(v[3].Area())
	box.s = box.p
	println(box.s.Area())
	var arr [2]Shape = [2]Shape{get(), &disc}
	println(arr[0].Area())
	println(arr[1].Area())
	if q, ok := t.(*Quad); ok {
		println(q.W)
	}
}
`,
		want: "12\n30\n30\n12\n30\n12\n12\n30\n12\n30\n12\n30\n49\n30\n12\n49\n5\n",
	},
	{
		// An INTERFACE-typed parameter crossing to a cog. `go show(&q)` for a
		// `show(Shape)` had never worked in ANY spelling: the argument block holds each
		// value as its parameter's type, and the raw pointer was stored in a slot of
		// interface type, which the target's C compiler refused -- "expected
		// _struct__Shape but got pointer to _struct__Quad", about generated code the
		// program never wrote. Every other position wrapped the two words; this one
		// alone did not.
		//
		// All five ways a value gets there are here, because what decides the table is
		// the pair (concrete type, interface) and each spelling reaches it differently:
		// an address, a call's result, a pointer field, an interface WIDENED from a
		// wider one (which needs a temporary, so the prologue has to land inside this
		// block and not wherever it is next flushed), and an interface copied as it
		// stands. Byte-identical to the same program under Go.
		name: "an interface argument crossing to a cog",
		src: `type Shape interface {
	Area() int
}

type Quad struct {
	W int
	H int
}

func (q *Quad) Area() int { return q.W * q.H }

type Round struct {
	R int
}

func (c *Round) Area() int { return c.R * c.R }

type Named interface {
	Area() int
	Name() string
}

func (q *Quad) Name() string { return "quad" }

type Box struct {
	p *Quad
}

var big = Quad{5, 6}
var small = Quad{3, 4}
var disc = Round{7}
var box Box

func get() *Quad { return &small }

func work(s Shape, ch chan int) { ch <- s.Area() }

func main() {
	box.p = &big
	var ch chan int
	go work(&big, ch)
	println(<-ch)
	go work(get(), ch)
	println(<-ch)
	go work(box.p, ch)
	println(<-ch)
	var n Named = &big
	go work(n, ch)
	println(<-ch)
	var s Shape = &disc
	go work(s, ch)
	println(<-ch)
}
`,
		want: "30\n12\n30\n30\n49\n",
	},
	{
		// An indexed literal's INDEX is any constant expression, which is what Go
		// says and what the folder already computes everywhere else. It was read as a
		// SOLE token instead -- a literal or a bare name -- so `N + 1:`, `1 << 2:` and
		// a qualified `geo.K:` were each refused as "not a non-negative integer
		// constant" about one that is. A non-constant index is still refused, which is
		// what the folder answering no means.
		name: "a constant expression as a literal index",
		src: `const N = 2

func main() {
	println(N) // read once: folded into every index below, gcc warns on an unused one
	xs := []int{N + 1: 9, 5}
	println(len(xs), xs[3], xs[4])
	ys := []int{1 << 2: 7}
	println(len(ys), ys[4])
	a := [6]int{N * 2: 3}
	println(a[4])
	zs := []string{N: "hi"}
	println(len(zs), zs[2])
}
`,
		want: "2\n5 9 5\n5 7\n3\n3 hi\n",
	},
	{
		// Two things Go does that this could not, and one is why the other was hard
		// to see.
		//
		// A conversion between two DISTINCT struct types of identical layout, `B(a)`,
		// was refused as "cannot convert to B". C has no cast between struct types, so
		// it is lowered as a copy -- memcpy into a temporary of the target's type,
		// which is exactly what the layouts being identical licenses, and the one form
		// that works for a struct holding an array.
		//
		// And a struct VALUE standing as an array or slice literal's ELEMENT --
		// `[]B{b}`, a variable, a call's result, a conversion -- reached the target's C
		// compiler, which refuses a non-braced aggregate inside an array initializer:
		// "expected int but got _struct__B", about generated code the program never
		// wrote. Its members braced is the spelling it takes, recursively, since a
		// nested struct, a string and a slice are each aggregates too.
		//
		// The values are all distinct so a member or a table reaching the wrong one
		// shows. Byte-identical to the same program under Go.
		name: "a struct conversion, and a struct value as a literal element",
		src: `type Inner struct {
	N int
}

type A struct {
	X int
	S string
	I Inner
}

type B struct {
	X int
	S string
	I Inner
}

type Rows struct {
	V [2]int
	N int
}

type Cols struct {
	V [2]int
	N int
}

type Held struct {
	X int
	S []int
}

type Shape interface {
	Area() int
}

func (i *Inner) Area() int { return i.N * 10 }

type Boxed struct {
	X int
	S Shape
	P *Inner
}

var ga = A{1, "a", Inner{2}}
var gb = B{3, "b", Inner{4}}
var pool = [3]int{7, 8, 9}
var held = Held{5, pool[:]}
var innr = Inner{4}
var boxed = Boxed{9, &innr, &innr}

func mkB() B { return B{6, "m", Inner{7}} }

func takeB(b B) int { return b.X + b.I.N }

func toB(a A) B { return B(a) }

func main() {
	b := B(ga)
	println(b.X, b.S, b.I.N)
	println(takeB(B(ga)))
	println(toB(A{8, "t", Inner{9}}).X)
	var c B
	c = B(ga)
	println(c.X)
	r := Rows{}
	r.V[0] = 3
	r.V[1] = 4
	r.N = 5
	q := Cols(r)
	println(q.V[0], q.V[1], q.N)

	bs := []B{gb, mkB(), B(ga)}
	println(bs[0].X, bs[1].X, bs[2].X)
	println(bs[0].S, bs[1].S, bs[2].S)
	println(bs[0].I.N, bs[1].I.N, bs[2].I.N)
	arr := [2]B{gb, B(ga)}
	println(arr[0].X, arr[1].X)
	hs := []Held{held}
	println(hs[0].X, hs[0].S[0], len(hs[0].S))
	box := struct2{gb}
	println(box.b.X)
	bx := []Boxed{boxed}
	println(bx[0].X, bx[0].S.Area(), bx[0].P.N)
}

type struct2 struct {
	b B
}
`,
		want: "1 a 2\n3\n8\n1\n3 4 5\n3 6 1\nb m a\n4 7 2\n3 1\n5 7 3\n3\n9 40 4\n",
	},
	{
		// A package variable's initializer may name one declared BELOW it. Go's
		// package block has no order -- the variables are initialized in DEPENDENCY
		// order, whatever the source order -- and every one of these was refused with
		// "cannot infer a type for the package variable c", because the pass that types
		// them walks the file in source order and typed each as it arrived.
		//
		// The ordering was already right: the initializers are topologically sorted
		// into the synthesized package init, and had been since that was written. Only
		// the TYPES were bound to source order, which is why the very example the
		// ordering's own comment gives -- `var a = b + 1` above b -- did not compile.
		//
		// Every kind of dependency is here, since each is typed by a different path: a
		// scalar chain, a slice of a later ARRAY (whose extents live in an environment
		// of their own), the address of a later variable, a call's result, a field of a
		// later struct value, and len of a later array. Byte-identical to the same
		// program under Go.
		name: "a package variable initialized from a later one",
		src: `type S struct {
	N int
}

func f() int { return 7 }

func mk() S { return S{f() + 1} }

var c = b * 10

var b = a + 1

var a = 5

var gsl = pool[:]

var pool = [3]int{9, 8, 7}

var p = &q

var q = 5

var y = x + 1

var x = f()

var t = s.N

var s = mk()

var n = len(pool)

func main() {
	println(a, b, c)
	println(gsl[0], len(gsl), n)
	println(*p, q)
	println(x, y)
	println(s.N, t)
}
`,
		want: "5 6 60\n9 3 3\n5 5\n7 8\n8 8\n",
	},
	{
		// A package variable initialized from a MEMBER of a later one -- a field, a
		// field of a call's result, an element. What each depends on is read off the
		// identifiers its initializer mentions, and the member name is not one of them:
		// it names a field, not a variable. Dropping it is what stopped `var a = s.a`
		// being reported as referring to itself once that list also decided whether the
		// initializers CYCLE -- and this pins the other half, that dropping it did not
		// lose the dependency on s, which still has to be initialized first.
		name: "a package variable initialized from a member of a later one",
		src: `type S struct {
	a int
	b int
}

func mk() S { return S{4, 5} }

var x = s.a

var s = mk()

var y = t.b

var t = S{6, 7}

var z = u[1]

var u = [2]int{8, 9}

func main() {
	println(x, y, z)
	println(s.a, t.b, u[1])
}
`,
		want: "4 7 9\n4 7 9\n",
	},
	{
		// A channel operand is an EXPRESSION, so a call that returns one names a
		// channel exactly as a variable does. With no heap that is how a package
		// hands one out: make() has nothing to allocate, so a channel is declared
		// once and an accessor over the bank is what other code calls.
		//
		// Both directions, since the two used to resolve the channel by different
		// routes -- a receive walks the expression and a send asked the declaration
		// -- and a send to a call was the half that had no answer.
		name: "a channel from a call",
		src: `var q [3]chan int

func qof(i int) chan int { return q[i] }

func worker() {
	v := <-qof(0)
	qof(1) <- v * 10
}

func main() {
	go worker()
	qof(0) <- 7
	println(<-qof(1))
	select {
	case v := <-qof(2):
		println("two", v)
	default:
		println("none")
	}
}
`,
		want: "70\nnone\n",
	},
	{
		// The same through a METHOD, a parenthesised operand and a POINTER to a
		// channel. A parenthesised channel was refused before this whether it held a
		// call or a plain name, and `*p <- v` emitted a call to `ogo_chan_send_`, a
		// helper of no element type at all -- the pointer read as the channel,
		// because the two share a C type-name prefix.
		//
		// The pointer is written both as a variable and as a FIELD, `*h.p <- v`,
		// where the "*" binds looser than the selector and so applies to the field
		// rather than to the struct. That pair is here because the receive side of it
		// already worked: `<-*h.p` renders the expression whole, while the send
		// resolved its channel from the head alone and refused a chain it could not
		// name -- the two directions disagreeing about the same operand.
		name: "a channel through a method, parentheses and a pointer",
		src: `type bank struct {
	in  chan int
	out chan int
}

type holder struct {
	p *chan int
}

var b bank
var h holder
var done chan int

func (k *bank) In() chan int {
	return k.in
}

func (k *bank) Out() chan int {
	return k.out
}

func worker() {
	v := <-b.In()
	p := &b.out
	*p <- v + 1
	(b.out) <- v + 2
	*h.p <- v + 3
	done <- 1
}

func main() {
	h.p = &b.out
	go worker()
	b.In() <- 10
	println(<-b.Out())
	println(<-(b.out))
	println(<-*h.p)
	<-done
}
`,
		want: "11\n12\n13\n",
	},
	{
		// Every channel operand is evaluated ONCE, as Go evaluates it: a receive, a
		// comma-ok receive, a range, a select clause and a send are five operations
		// and five calls. The select was the one that re-ran it -- its poll rendered
		// the operand afresh every round, so the count depended on how long the
		// select happened to wait, and a clause over a bank polled a different
		// channel each time.
		name: "a channel operand is evaluated once",
		src: `var q [3]chan int
var done chan int
var calls int

func qof(i int) chan int {
	calls++
	return q[i]
}

func feed() {
	q[0] <- 1
	q[0] <- 2
	close(q[0])
	q[2] <- 3
	<-q[1]
	done <- 1
}

func main() {
	go feed()
	println("recv", <-qof(0))
	println("calls", calls)
	v, ok := <-qof(0)
	println("commaok", v, ok, "calls", calls)
	for w := range qof(0) {
		println("range", w)
	}
	println("calls", calls)
	select {
	case x := <-qof(2):
		println("select", x)
	}
	println("calls", calls)
	qof(1) <- 9
	println("calls", calls)
	<-done
}
`,
		want: "recv 1\ncalls 1\ncommaok 2 true calls 2\ncalls 3\nselect 3\ncalls 4\ncalls 5\n",
	},
	{
		// The math package in the SHAPES a program reaches it through, rather than
		// for its values -- TestMathMatchesGo runs every function against Go's own.
		// A call is substituted with the C library's, so what needs checking here is
		// everything that is not a plain call: a constant in a constant expression,
		// a function taken as a VALUE (which needs a definition to point at, there
		// being none for a substituted call), and the two functions with real OctoGo
		// bodies, which are emitted like any other and may be taken as values as
		// they stand.
		name: "the math package",
		src: `import "math"

const quarter = math.Pi / 4

var back [3]float64

func rms(xs []float64) float64 {
	sum := 0.0
	for _, v := range xs {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(xs)))
}

func main() {
	printf("%.4f %.4f\n", math.Sqrt(2.0), math.Sin(quarter))
	f := math.Sqrt
	g := math.Round
	printf("%.4f %.4f\n", f(9.0), g(-2.5))
	back[0] = 3.0
	back[1] = 4.0
	back[2] = 0.0
	printf("%.4f\n", rms(back[:]))
	printf("%.4f %.4f\n", math.Trunc(-3.7), math.Mod(7.5, 3.0))
}
`,
		want: "1.4142 0.7071\n3.0000 -3.0000\n2.8868\n-3.0000 1.5000\n",
	},
	{
		// An array reached through a call's POINTER result, the way a device behind
		// an accessor is read. Every other field of the result read as Go reads it,
		// while the array ones were refused: `x := dev().rx` "cannot infer a type",
		// `len(dev().rx)` "len is only supported for ...", `buf = dev().rx` and the
		// value range likewise, and the index-only range bound a temporary it never
		// read (a C error under -Werror). The call runs once per occurrence, which
		// `calls` counts through every shape: len and cap, index, an index that is
		// itself such a read, the three copies, a slice of it, copy and range, an
		// argument, a comparison, a field of a field, a defined array type with a
		// method (`hold().b.Sum()`), a pointer to one, an array of arrays, a
		// method's pointer result, and the two stores.
		name: "an array reached through a call's pointer result",
		src: `type Pos struct{ x, y int }

type Dev struct {
	rx    [4]byte
	name  string
	pos   Pos
	list  []int
	grid  [2][3]int
	other [4]byte
}

type Buf [4]byte

func (b Buf) Sum() int {
	s := 0
	for _, v := range b {
		s += int(v)
	}
	return s
}

type Holder struct {
	b  Buf
	pb *Buf
}

type Bus struct{ devs [2]Dev }

func (b *Bus) port(i int) *Dev {
	calls += 1000
	return &b.devs[i]
}

var backing = [5]int{1, 2, 3, 4, 5}

var gd = Dev{rx: [4]byte{1, 2, 3, 4}, name: "dev0", pos: Pos{5, 6}, other: [4]byte{1, 2, 3, 4}}

var gb = Buf{9, 8, 7, 6}

var gh = Holder{b: Buf{9, 8, 7, 6}, pb: &gb}

var bus Bus

var calls int

func dev() *Dev {
	calls++
	return &gd
}

func hold() *Holder {
	calls += 100
	return &gh
}

func take(a [4]byte) int { return int(a[0]) + int(a[3]) }

func main() {
	gd.list = backing[:]
	gd.grid[1][2] = 42
	bus.devs[1].rx = [4]byte{4, 3, 2, 1}
	var buf [4]byte
	println(len(dev().rx), cap(dev().rx), dev().rx[1], dev().rx[dev().rx[0]], calls)
	x := dev().rx
	var y [4]byte = dev().rx
	buf = dev().rx
	s := dev().rx[1:3]
	println(x[2], y[3], buf[1], len(s), s[0], len(dev().rx[1:]), cap(dev().rx[:2]), calls)
	n := copy(buf[:], dev().rx[:])
	sum := 0
	for i, b := range dev().rx {
		sum += i * int(b)
	}
	for i := range dev().rx {
		sum += i
	}
	println(n, buf[3], sum, take(dev().rx), dev().rx == gd.other, dev().rx != buf, calls)
	println(dev().name, len(dev().name), dev().name[1], dev().name[1:3], dev().pos.x, dev().list[2], len(dev().list), calls)
	println(dev().grid[1][2], len(dev().grid), len(dev().grid[1]), hold().b.Sum(), hold().pb.Sum(), hold().b[1], len(hold().pb), hold().pb[2], calls)
	b := hold().b
	r := dev().grid[1]
	w := bus.port(1).rx
	println(b.Sum(), r[2], len(dev().grid[dev().rx[0]]), w[0], bus.port(1).rx[3], len(bus.port(0).rx), calls)
	dev().rx = [4]byte{5, 6, 7, 8}
	dev().rx[2] = 9
	println(gd.rx[0], gd.rx[2], calls)
}
`,
		want: "4 4 2 2 5\n3 4 2 2 2 3 4 11\n4 4 26 5 true false 17\ndev0 4 101 ev 5 3 5 24\n42 2 3 30 30 8 4 7 527\n30 42 3 4 1 4 3630\n5 9 3632\n",
	},
	{
		// Go evaluates `none().rx` -- len of it is not constant, the operand holding
		// a call -- and the read through the nil result panics.
		name: "len of an array through a nil pointer a call returns panics",
		src: `type Dev struct{ rx [4]byte }

func none() *Dev { return nil }

func main() {
	println("before")
	println(len(none().rx))
	println("after")
}
`,
		want:   "before\npanic: nil pointer dereference",
		panics: true,
	},
	{
		// A POINTER to an array a call returns, `pick() *[4]int`. Indexing it, len of
		// it and the written-out dereference read as Go reads them; the rest was
		// refused: a slice of it -- `len(pick()[1:])` "len is only supported for
		// ...", `pick()[1:][0]` "unsupported call in expression", `s := pick()[1:3]`
		// "cannot infer a type" -- the store through the dereference ("only a run
		// of fields and indexes may stand between them"), and the ranges: the
		// value form "ranging an integer yields only the index", the index-only
		// form `int t = pick();` (a C error). Each call runs once.
		name: "a pointer to an array a call returns",
		src: `var a = [4]int{1, 2, 3, 4}

var calls int

func pick() *[4]int {
	calls++
	return &a
}

func main() {
	println(len(pick()[1:]), pick()[1:][0], cap(pick()[2:]), calls)
	s := pick()[1:3]
	println(len(s), s[1], calls)
	*pick() = [4]int{5, 6, 7, 8}
	println(a[0], a[3], calls)
	pick()[2] = 9
	println(a[2], len(pick()), calls)
	for i, v := range pick() {
		println(i, v)
	}
	println(calls)
}
`,
		want: "3 2 2 3\n2 3 4\n5 8 5\n9 4 7\n0 5\n1 6\n2 9\n3 8\n8\n",
	},
	{
		// The same pointer reached every other way but a variable: an element of an
		// array of pointers (with a call in the index, which runs once), a method's
		// result, a struct field; a pointer to an array of ROWS, whose slice is a
		// slice of rows; a pointer to a DEFINED array type; and the slice as a copy
		// source and destination, an append spread and an argument.
		name: "a pointer to an array that is not a variable",
		src: `type Row [3]int

type Dev struct {
	pa *[4]int
	pm *[2][3]int
	pr *Row
}

var a = [4]int{1, 2, 3, 4}

var m = [2][3]int{{1, 2, 3}, {4, 5, 6}}

var r = Row{7, 8, 9}

var d = Dev{&a, &m, &r}

var ptrs = [2]*[4]int{&a, &a}

var calls int

func pick() *[4]int {
	calls++
	return &a
}

func pm() *[2][3]int {
	calls += 10
	return &m
}

func pr() *Row {
	calls += 100
	return &r
}

func idx() int {
	calls += 1000
	return 1
}

func take(s []int) int { return len(s) + s[0] }

func main() {
	println(len(ptrs[1][1:]), ptrs[idx()][2:][0], take(ptrs[0][:3]), calls)
	for i, v := range ptrs[1] {
		println(i, v)
	}
	*ptrs[idx()] = [4]int{4, 3, 2, 1}
	println(a[0], a[3], calls)
	rows := pm()[1:]
	println(len(rows), rows[0][2], len(pm()[:1]), calls)
	for i, row := range pm() {
		println(i, row[0])
	}
	pr()[1] = 80
	println(len(pr()[1:]), pr()[1:][0], r[1], calls)
	s := pr()[:2]
	println(len(s), s[1], calls)
	println(len(d.pm[1:]), d.pm[1:][0][1], len(d.pr[:1]), d.pr[1:][1], calls)
	for i, row := range d.pm {
		println(i, row[2])
	}
	*d.pr = Row{1, 1, 1}
	println(r[0], r[2])
	var b [4]int
	n := copy(pick()[:2], []int{9, 9})
	println(n, a[0], a[1], copy(b[:], pick()[1:]), b[0], calls)
	var back [4]int
	xs := append(back[:0], pick()[2:]...)
	println(len(xs), xs[1], take(pick()[1:]), calls)
	t := ptrs[idx()][1:3]
	println(len(t), t[0], calls)
}
`,
		want: "3 3 4 1000\n0 1\n1 2\n2 3\n3 4\n4 1 2000\n1 6 1 2020\n0 1\n1 4\n2 80 80 2330\n2 80 2430\n1 5 1 9 2430\n0 3\n1 6\n1 1\n2 9 9 3 9 2432\n2 1 12 2434\n2 9 3434\n",
	},
	{
		// Go's own verdicts on a nil pointer to an array a call returns: len of it
		// is its extent, the index-only range counts without reading through it,
		// and slicing it reads through it and panics.
		name: "a slice of a nil pointer to an array a call returns panics",
		src: `func none() *[4]int { return nil }

func main() {
	println("before", len(none()))
	for i := range none() {
		println("idx", i)
	}
	println("still")
	s := none()[1:]
	println("after", len(s))
}
`,
		want:   "before 4\nidx 0\nidx 1\nidx 2\nidx 3\nstill\npanic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a value range over a nil pointer to an array a call returns panics",
		src: `func none() *[4]int { return nil }

func main() {
	println("before")
	for i, v := range none() {
		println(i, v)
	}
	println("after")
}
`,
		want:   "before\npanic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a store through a nil pointer to an array a call returns panics",
		src: `func none() *[4]int { return nil }

func main() {
	println("before")
	*none() = [4]int{1, 2, 3, 4}
	println("after")
}
`,
		want:   "before\npanic: nil pointer dereference",
		panics: true,
	},
	{
		name: "a slice of a nil pointer-to-array field panics",
		src: `type Dev struct{ pa *[4]int }

var d Dev

func main() {
	println("before", len(d.pa))
	s := d.pa[:2]
	println("after", len(s))
}
`,
		want:   "before 4\npanic: nil pointer dereference",
		panics: true,
	},
	{
		// Go's package block has no order. Every read of a package slice written
		// ABOVE the slice's declaration, for each shape the declaration takes: a
		// slice literal (the one the pre-pass dropped -- "len is only supported for
		// ...", "cannot infer a type for the package variable"), a slice of strings
		// with an element measured, a written type with a make, and a slice of an
		// array. The last line pins that the reads are views of the slice, not copies.
		name: "a package variable reads a slice declared below it",
		src: `var n = len(xs) + cap(xs)

var first = xs[0]

var tail = xs[1:]

var m = len(names) + len(names[1])

var last = names[len(names)-1]

var xs = []int{1, 2, 3}

var names = []string{"a", "bb", "ccc"}

var w int = len(ws)

var ws []int = make([]int, 2, 8)

var vs = back[:2]

var back = [4]int{4, 5, 6, 7}

var v = len(vs) + vs[1]

func main() {
	println(n, first, len(tail), tail[0], m, last)
	println(w, len(ws), cap(ws), len(vs), v)
	xs[0] = 9
	println(first, xs[0], tail[1])
}
`,
		want: "6 1 2 2 5 ccc\n2 2 8 2 7\n1 9 3\n",
	},
	{
		// The comparison operators bind alike and to the left, as in Go: `a < b ==
		// c` is `(a < b) == c`. The checker paired each comparison with the operand
		// written beside it and refused the first line ("mismatched types int and
		// bool"); the fold is written out in C, `(a < b) == c`, which the host's C
		// compiler otherwise reports (-Wparentheses). Chains over strings, structs
		// and arrays (each a helper call), with && and ||, as a loop condition, and
		// negated; the calls count in Go's order.
		name: "a chain of comparisons binds to the left",
		src: `type P struct{ x, y int }

var calls int

func f(n int) int {
	calls++
	return n
}

func s(n int) string {
	calls += 10
	if n == 0 {
		return "a"
	}
	return "b"
}

func main() {
	a, b, c := 1, 2, true
	println(f(1) < f(2) == (f(3) < f(4)), calls)
	println(f(2) < f(1) != (f(3) < f(4)), calls)
	println(f(1) == f(1) == true, a < b == c == true, calls)
	println(s(0) == "a" != c, s(1) < s(0) == false, calls)
	println(a < b == c && f(5) == 5, a > b == c || f(6) > 0, calls)
	println(P{1, 2} == P{1, 2} == c, [2]int{1, 2} != [2]int{1, 3} == true, calls)
	println(1 < 2 == true, 3 == 4 != (a < b), calls)
	for i := 0; i < 3 == true; i++ {
		calls += 100
	}
	println(calls)
	ok := a < b == c
	println(ok, !(a < b == c) == false)
}
`,
		want: "true 4\ntrue 8\ntrue true 10\nfalse true 40\ntrue true 42\ntrue true 42\ntrue true 42\n342\ntrue true\n",
	},
	{
		// An ARRAY field promoted through an embedded struct, by value and by
		// pointer -- the driver shape, registers behind a channel -- in every array
		// shape: len and cap, an element read and written, a slice, both ranges, a
		// copy, an argument, a whole-array store and a comparison; two levels of
		// embedding, an array of rows, a pointer to the outer and a call returning
		// one. All were refused ("has no field fifo", "cannot infer a type", "len is
		// only supported for ...") while a promoted scalar read as Go reads it.
		name: "an array field promoted through an embedded struct or pointer",
		src: `type Regs struct {
	fifo [8]byte
	head int
	grid [2][3]int
}

type ByPtr struct {
	*Regs
	id int
}

type ByVal struct {
	Regs
	id int
}

type Outer struct {
	ByVal
	tag int
}

var r Regs

var p = ByPtr{&r, 1}

var v ByVal

var o Outer

var calls int

func take(a [8]byte) int { return int(a[0]) + int(a[7]) }

func pp() *ByPtr {
	calls++
	return &p
}

func main() {
	r.fifo[0] = 5
	r.fifo[7] = 9
	v.Regs.fifo[0] = 6
	v.Regs.fifo[7] = 8
	println(len(p.fifo), cap(p.fifo), len(v.fifo), len(p.grid), len(p.grid[1]), len(o.fifo))
	p.fifo[1] = 7
	v.fifo[1] = 3
	o.fifo[2] = 4
	println(p.fifo[0], p.fifo[1], r.fifo[1], v.fifo[1], v.Regs.fifo[1], o.fifo[2], o.ByVal.Regs.fifo[2])
	s := p.fifo[1:3]
	t := v.fifo[:2]
	println(len(s), cap(s), s[1], len(t), t[0])
	n := 0
	for i, b := range p.fifo {
		n += i * int(b)
	}
	for i := range v.fifo {
		n += i
	}
	println(n)
	x := p.fifo
	y := v.fifo
	x[0] = 1
	y[0] = 2
	println(x[0], r.fifo[0], y[0], v.Regs.fifo[0], take(p.fifo), take(v.fifo), take(o.fifo))
	p.fifo = [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	v.fifo = p.fifo
	o.fifo = v.fifo
	println(r.fifo[7], v.Regs.fifo[3], o.fifo[5], p.fifo == v.fifo, p.fifo != r.fifo, o.fifo == r.fifo)
	p.grid[1][2] = 11
	println(p.grid[1][2], r.grid[1][2], len(pp().fifo), pp().fifo[3], pp().grid[1][2], calls)
	for i, row := range p.grid {
		println(i, row[2])
	}
	q := &p
	q.fifo[6] = 60
	println(len(q.fifo), q.fifo[6], r.fifo[6], q.head)
}
`,
		want: "8 8 8 2 3 8\n5 7 7 3 3 4 4\n2 7 0 2 6\n98\n1 5 2 6 14 14 0\n8 4 6 true false true\n11 11 8 4 11 3\n0 0\n1 11\n8 60 60 0\n",
	},
	{
		// A method of SEVERAL results called on a CALL's result -- the registers
		// behind an accessor, popped -- was "multiple assignment requires a single
		// function call on the right-hand side", while the same method on an
		// element or a field was fine. The `:=`, `=` and if-init forms, a method of
		// three results, a value receiver on the result, and a method promoted
		// through an embedded pointer of the result; the calls count in Go's order.
		name: "a multi-result method called on a call's result",
		src: `type Regs struct {
	fifo [8]byte
	head int
	tail int
}

type Chan struct {
	*Regs
	id int
}

type Bus struct {
	regs  [3]Regs
	chans [3]Chan
}

var bus Bus

var calls int

func (b *Bus) reg(i int) *Regs {
	calls++
	return &b.regs[i]
}

func (b *Bus) ch(i int) *Chan {
	calls += 10
	return &b.chans[i]
}

func (r *Regs) pop() (byte, bool) {
	if r.head == r.tail {
		return 0, false
	}
	v := r.fifo[r.tail]
	r.tail++
	return v, true
}

func (r *Regs) bounds() (int, int, int) {
	return r.head, r.tail, len(r.fifo)
}

func (c Chan) which() (int, bool) { return c.id, c.Regs != nil }

func main() {
	bus.regs[2].fifo[0] = 42
	bus.regs[2].fifo[1] = 43
	bus.regs[2].head = 2
	bus.chans[1] = Chan{&bus.regs[2], 7}
	v, ok := bus.reg(2).pop()
	println(v, ok, calls)
	var w byte
	w, ok = bus.reg(2).pop()
	println(w, ok, calls)
	if x, ok := bus.reg(2).pop(); !ok {
		println("empty", x, calls)
	}
	h, t, n := bus.reg(2).bounds()
	println(h, t, n, calls)
	id, has := bus.ch(1).which()
	println(id, has, calls)
	bus.regs[2].tail = 0
	y, ok2 := bus.ch(1).pop()
	println(y, ok2, bus.regs[2].tail, calls)
	h, t, n = bus.ch(1).bounds()
	println(h, t, n, calls)
}
`,
		want: "42 true 1\n43 true 2\nempty 0 3\n2 2 8 4\n7 true 14\n42 true 1 24\n2 1 8 34\n",
	},
	{
		// Go evaluates a deferred call's receiver where the defer stands. A receiver
		// that is a CALL's result -- `defer getRegs().Show()`, `defer getPort().Reset()`
		// promoted through an embedded pointer -- was left to the replay, which ran
		// the call at the return: a call count 100 short, `defer pick(i).Show()`
		// showing the register picked at the return, and with an argument in the
		// inner call the compiler stopped ("index out of range"). A pointer receiver,
		// a value receiver (a copy taken at the defer), a promoted method, and a
		// receiver whose choice depends on a variable changed after the defer.
		name: "a deferred call's receiver that is a call's result is evaluated at the defer",
		src: `type Regs struct {
	count int
	rx    [4]byte
}

func (r *Regs) Reset() { r.count = 0 }

func (r *Regs) Show() { println("show", r.count) }

type Pos struct{ x, y int }

func (p Pos) At() { println("at", p.x, p.y) }

func (p *Pos) Bump() { p.x++ }

type Port struct {
	*Regs
	id int
}

var regs = Regs{count: 5}

var other = Regs{count: 7}

var port = Port{&regs, 9}

var pos = Pos{1, 2}

var calls int

func getPort() *Port {
	calls += 100
	return &port
}

func getRegs() *Regs {
	calls++
	return &regs
}

func pick(i int) *Regs {
	calls += 10
	if i == 0 {
		return &regs
	}
	return &other
}

func getPos() *Pos {
	calls += 1000
	return &pos
}

func f() {
	defer getRegs().Show()
	println("f", calls)
	regs.count = 6
	defer getPort().Reset()
	println("f", calls)
	regs.count = 8
}

func g() {
	i := 0
	defer pick(i).Show()
	i = 1
	defer pick(i).Show()
	regs.count = 1
	other.count = 2
	println("g", calls)
}

func h() {
	defer getPos().At()
	defer getPos().Bump()
	pos.x = 10
	println("h", calls, pos.x)
}

func main() {
	f()
	println(regs.count, calls)
	g()
	h()
	println(pos.x, calls)
}
`,
		want: "f 1\nf 101\nshow 0\n0 101\ng 121\nshow 2\nshow 1\nh 2121 10\nat 1 2\n11 2121\n",
	},
	{
		// A frame parser over an embedded reader, the domain probe that found the
		// deferred receiver fault: an interface satisfied through an embedded
		// pointer, a table of handler function values, a labeled continue out of a
		// range, a comparison chain in a switch, a deferred method on an accessor
		// result beside a deferred println, a method value from a package variable,
		// a copy and a slice of a promoted array through the accessor, and interface
		// identity. Measured against Go with the calls counted in order.
		name: "a frame parser over an embedded reader",
		src: `type Frame struct {
	kind byte
	len  int
	body [6]byte
}

type Regs struct {
	rx    [8]byte
	count int
}

func (r *Regs) Read() (byte, bool) {
	if r.count >= len(r.rx) {
		return 0, false
	}
	v := r.rx[r.count]
	r.count++
	return v, true
}

func (r *Regs) Reset() { r.count = 0 }

type Port struct {
	*Regs
	id   int
	seen [3]int
}

type Reader interface {
	Read() (byte, bool)
	Reset()
}

type Handler func(f *Frame) int

var regs = Regs{rx: [8]byte{1, 0x80, 2, 3, 4, 5, 6, 7}}

var port = Port{&regs, 9, [3]int{}}

var handlers = [2]Handler{sum, first}

var calls int

func sum(f *Frame) int {
	calls++
	n := 0
	for i := 0; i < f.len && i < len(f.body); i++ {
		n += int(f.body[i])
	}
	return n
}

func first(f *Frame) int {
	calls += 10
	if f.len == 0 {
		return -1
	}
	return int(f.body[0])
}

func getPort() *Port {
	calls += 100
	return &port
}

func parse(r Reader, f *Frame) bool {
	k, ok := r.Read()
	if !ok {
		return false
	}
	f.kind = k & 0x7f
	f.len = 0
	for f.len < len(f.body) {
		b, ok := r.Read()
		if !ok || b == 0x80 {
			break
		}
		f.body[f.len] = b
		f.len++
	}
	return true
}

func classify(f *Frame) string {
	switch {
	case f.len == 0:
		return "empty"
	case f.kind > 1 && f.len > 2 == (f.body[0] > 1):
		return "long"
	}
	return "short"
}

func main() {
	var f Frame
	var r Reader = getPort()
	n := 0
outer:
	for parse(r, &f) {
		n++
		for i, h := range handlers {
			if h(&f) < 0 {
				println("skip", i)
				continue outer
			}
			getPort().seen[i] += h(&f)
		}
		println(f.kind, f.len, classify(&f), calls)
		if n > 3 {
			break
		}
	}
	println(n, port.seen[0], port.seen[1], regs.count, calls)
	r.Reset()
	defer getPort().Reset()
	defer println("deferred", port.count)
	b, ok := getPort().Read()
	println(b, ok, getPort().count, calls)
	read := port.Read
	c, ok2 := read()
	println(c, ok2, calls)
	x := getPort().rx
	x[0] = 42
	println(x[0], regs.rx[0], len(getPort().rx[3:]), getPort().rx[3:][1], calls)
	var q Reader = port.Regs
	if q == r {
		println("same")
	}
	m := map0(getPort().seen[:])
	println(m, calls)
}

func map0(xs []int) int {
	t := 0
	for _, v := range xs {
		t += v
	}
	return t
}
`,
		want: "skip 1\n2 5 long 434\n2 25 3 8 434\n1 true 1 734\n128 true 734\n42 1 5 4 1034\n28 1134\ndeferred 0\n",
	},
	{
		// Two cog workers over the accessor shapes: each takes a register block from
		// an accessor call as a `go` argument (evaluated at the go statement), copies
		// its array, and sends scalars through a channel main polls with a default
		// arm; a deferred call with an array argument read through the accessor, a
		// comparison chain over two accessor reads, and a copy beside the live array.
		// The busy-wait counts no calls: how often it spins is the schedule's.
		// Measured against Go (whose twin makes the channel) with the calls counted.
		name: "cog workers over accessor shapes",
		src: `type Sample struct {
	ch  int
	sum int
}

type Regs struct {
	raw  [3]int16
	seq  int
	done bool
}

type Board struct {
	regs [3]Regs
}

var out chan Sample

var board Board

var calls int

func (b *Board) reg(i int) *Regs {
	calls++
	return &b.regs[i]
}

func worker(r *Regs, ch int, out chan Sample, n int) {
	for i := 0; i < n; i++ {
		r.seq++
		v := r.raw
		v[0] += int16(i)
		out <- Sample{ch, sum(v)}
	}
	r.done = true
}

func sum(v [3]int16) int {
	t := 0
	for _, x := range v {
		t += int(x)
	}
	return t
}

func show(tag string, v [3]int16) { println(tag, v[0], v[1], v[2]) }

func main() {
	board.regs[0].raw = [3]int16{1, 2, 3}
	board.regs[1].raw = [3]int16{10, 20, 30}
	board.regs[2].raw = [3]int16{5, 6, 7}
	go worker(board.reg(0), 0, out, 2)
	go worker(board.reg(1), 1, out, 3)
	println(calls)
	defer show("deferred", board.reg(2).raw)
	board.regs[2].raw[2] = 99
	total := 0
	got := 0
	for got < 5 {
		select {
		case s := <-out:
			total += s.sum * (s.ch + 1)
			got++
		default:
		}
	}
	println(got, total, calls)
	for i := 0; i < 2; i++ {
		for !board.regs[i].done {
		}
	}
	println(board.reg(0).seq, board.reg(1).seq, board.reg(0).done && board.reg(1).done == true, calls)
	v := board.reg(2).raw
	v[0] = 8
	show("copy", v)
	show("live", board.reg(2).raw)
	println(calls)
}
`,
		want: "2\n5 379 3\n2 3 true 7\ncopy 8 6 99\nlive 5 6 99\n9\ndeferred 5 6 7\n",
	},
	{
		// A goroutine's receiver in every shape a program writes it: a call's result
		// (with an argument changed after the launch, as Go evaluates it at the go
		// statement), a method promoted through an embedded pointer on a call's
		// result and on a package variable (which emitted a call to a Port_run nothing
		// declares), a local pointer and a pointer parameter (refused as "the address
		// of local variable r"), a value receiver on a call's result, and an array
		// argument read through an accessor. Launched in two batches of at most
		// seven, the cogs a program has beside main.
		name: "a goroutine launched on a call's result, a promoted method or a local pointer",
		src: `type Regs struct {
	id  int
	raw [3]int
}

type Pos struct{ x, y int }

func (p Pos) show(out chan int) { out <- p.x*10 + p.y }

type Port struct {
	*Regs
	tag int
}

func (r *Regs) run(out chan int) {
	out <- r.id
}

func (r *Regs) twice(out chan int) {
	out <- r.id * 2
}

var regs = [2]Regs{{1, [3]int{1, 2, 3}}, {2, [3]int{4, 5, 6}}}

var port = Port{&regs[1], 7}

var pos = Pos{3, 4}

var out chan int

var calls int

func pick(i int) *Regs {
	calls += 10
	return &regs[i]
}

func getPort() *Port {
	calls += 100
	return &port
}

func getPos() *Pos {
	calls += 1000
	return &pos
}

func total(a [3]int) int { return a[0] + a[1] + a[2] }

func send(out chan int, n int) { out <- n }

func launch(r *Regs) { go r.run(out) }

func main() {
	i := 0
	go pick(i).run(out)
	i = 1
	go pick(i).twice(out)
	go getPort().run(out)
	go port.run(out)
	println(calls)
	sum := 0
	for k := 0; k < 4; k++ {
		sum += <-out
	}
	println(sum, calls)
	go send(out, total(pick(1).raw))
	r := &regs[0]
	go r.twice(out)
	launch(&regs[1])
	go getPos().show(out)
	pos.x = 9
	go pos.show(out)
	for k := 0; k < 5; k++ {
		sum += <-out
	}
	println(sum, calls)
}
`,
		want: "120\n9 120\n156 1130\n",
	},
	{
		// A level of a NARROW type wraps at every operation, as Go computes it. A
		// chain with a guarded shift (a variable count) wrapped only its total, so a
		// constant-count shift after the helper ran in C's int and a right shift read
		// the bits Go had dropped: `1 << s << 7 >> 2` on an int16 was 16384 for Go's
		// 0. Silent, and hidden whenever the chain ended in a left shift. int8,
		// uint8, int16 and uint16; shifts, products, divisions and remainders after
		// a guarded shift; declarations, stores through a field and an accessor, a
		// comparison, and a sum of two such levels.
		name: "a narrow shift chain wraps at every step",
		src: `type Regs struct {
	i16 int16
	u8  uint8
}

var regs Regs

func reg() *Regs { return &regs }

func main() {
	var s uint = 9
	var t uint = 4
	var v int16 = 1 << s << 7 >> 2
	var w uint8 = 1 << t << 4 >> 1
	var x int8 = 1 << t << 3 >> 1
	var y uint16 = 1 << s << 7 >> 3
	println(v, w, x, y)
	var a int16 = 300
	var b uint16 = 300
	var c uint8 = 200
	println(a<<s>>s, b<<t*3/7, c<<t>>2, a<<t%1000, b<<s/3, c<<s>>s)
	reg().i16 = 1 << s << 7 >> 2
	regs.u8 = 1 << t << 4 >> 1
	println(reg().i16, regs.u8, 1<<s<<7>>2 == v, a<<s>>s < 0, int(a<<s>>s))
	d := a << s >> 1
	e := c << t << 1 >> 1
	println(d, e)
	f := a<<s + a<<t>>1
	println(f)
}
`,
		want: "0 0 -64 0\n44 2057 32 800 7509 0\n0 0 true false 44\n11264 0\n24928\n",
	},
	{
		// A compound assignment through a call's result in the forms that take a
		// guard -- a shift by a variable count, a division by a variable -- which
		// write the target twice: refused ("needs a target that can be named twice;
		// this one is evaluated") although the call is bound first and the rest of
		// the target repeats nothing. A field, an array element by a constant index
		// (an index holding a call still refuses), and the unguarded forms beside
		// them; each call runs once.
		name: "a guarded compound assignment through a call's result",
		src: `type Regs struct {
	u8  uint8
	i32 int32
	arr [3]int32
	row [2]uint8
}

var regs = Regs{u8: 200, i32: -7, arr: [3]int32{-9, 40, 7}}

var calls int

func reg() *Regs {
	calls++
	return &regs
}

func idx() int {
	calls += 10
	return 1
}

func main() {
	var s uint = 3
	var d uint8 = 3
	var e int32 = -1
	reg().u8 <<= s
	reg().i32 >>= s
	println(regs.u8, regs.i32, calls)
	reg().u8 /= d
	reg().i32 /= e
	reg().arr[1] %= 7
	reg().row[1] = 9
	reg().row[1] <<= s
	reg().row[0] = 250
	reg().row[0] >>= s
	println(regs.u8, regs.i32, regs.arr[1], regs.row[0], regs.row[1], calls)
	reg().u8 += 1 << s
	reg().u8 *= 3
	reg().i32 -= 100
	reg().arr[2] += 5
	reg().i32 <<= 2
	reg().u8 >>= 1
	reg().arr[0] /= e
	println(regs.u8, regs.i32, regs.arr[0], regs.arr[2], calls, s, d)
}
`,
		want: "64 -1 2\n21 1 5 31 72 9\n43 -396 9 12 16 3 3\n",
	},
	{
		// A device driver over accessors, the domain probe that found the three fixes
		// before it: registers behind a channel through an embedded pointer, a ring
		// buffer in a promoted array, accessors returning pointers to both, a
		// multi-result method on one, an interface over the channel, a comparison
		// chain, a package-level order table, copies and slices of the buffer, and
		// pointer identity through the embed. Measured against Go with the calls
		// counted in order.
		name: "a device driver over accessors",
		src: `type Regs struct {
	ctrl   uint32
	status uint32
	fifo   [8]byte
	head   int
	tail   int
}

type Chan struct {
	*Regs
	id    int
	gain  int16
	taps  [4]int32
	name  string
}

type Bus struct {
	regs  [3]Regs
	chans [3]Chan
	order []int
}

type Sampler interface {
	Sample() int32
	Name() string
}

var bus Bus

var order = []int{2, 0, 1}

var weights = [4]int32{1, -2, 3, -4}

var calls int

func (b *Bus) reg(i int) *Regs {
	calls++
	return &b.regs[i]
}

func (b *Bus) ch(i int) *Chan {
	calls += 10
	return &b.chans[i]
}

func (r *Regs) push(v byte) bool {
	next := (r.head + 1) % len(r.fifo)
	if next == r.tail {
		return false
	}
	r.fifo[r.head] = v
	r.head = next
	return true
}

func (r *Regs) pop() (byte, bool) {
	if r.head == r.tail {
		return 0, false
	}
	v := r.fifo[r.tail]
	r.tail = (r.tail + 1) % len(r.fifo)
	return v, true
}

func (c *Chan) Sample() int32 {
	var acc int32
	for i, t := range c.taps {
		acc += t * weights[i]
	}
	return acc * int32(c.gain)
}

func (c *Chan) Name() string { return c.name }

func (c *Chan) pending() int {
	n := c.head - c.tail
	if n < 0 {
		n += len(c.fifo)
	}
	return n
}

func setup() {
	for i := range bus.chans {
		bus.chans[i].Regs = &bus.regs[i]
		bus.chans[i].id = i
		bus.chans[i].gain = int16(i + 1)
		bus.chans[i].name = "ch"
		for j := range bus.chans[i].taps {
			bus.chans[i].taps[j] = int32(i*4 + j)
		}
	}
	bus.order = order[:]
}

func drain(r *Regs) int {
	sum := 0
	for {
		v, ok := r.pop()
		if !ok {
			return sum
		}
		sum += int(v)
	}
}

func main() {
	setup()
	for i := 0; i < 10; i++ {
		if !bus.reg(1).push(byte(i * 3)) {
			println("full at", i)
		}
	}
	println(bus.reg(1).head, bus.reg(1).tail, bus.ch(1).pending(), len(bus.ch(1).fifo), calls)
	println(drain(bus.reg(1)), bus.ch(1).pending(), bus.reg(1).fifo[2], calls)
	var total int32
	for _, i := range bus.order {
		var s Sampler = bus.ch(i)
		total += s.Sample()
		println(s.Name(), bus.ch(i).id, s.Sample() > 0 == (i != 0), calls)
	}
	println(total, calls)
	bus.ch(2).fifo[0] = 200
	bus.ch(2).head = 1
	v, ok := bus.reg(2).pop()
	println(v, ok, bus.ch(2).pending(), calls)
	x := bus.ch(0).taps
	x[0] = 99
	println(x[0], bus.ch(0).taps[0], len(bus.ch(0).taps[1:]), bus.ch(0).taps[1:][2], calls)
	for i, t := range bus.ch(2).taps {
		if t > 9 && i < 3 {
			println("tap", i, t)
		}
	}
	bus.regs[0].fifo = [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	bus.ch(0).fifo[7] = 0
	s := bus.reg(0).fifo[2:5]
	println(len(s), s[0], cap(s), bus.reg(0).fifo == bus.ch(0).fifo, bus.reg(0).fifo != bus.reg(1).fifo, calls)
	println(bus.ch(0).Regs == bus.reg(0), bus.ch(1).Regs == bus.reg(0), bus.chans[2].ctrl == bus.reg(2).ctrl, calls)
}
`,
		want: "full at 7\nfull at 8\nfull at 9\n7 0 7 8 32\n63 0 6 44\nch 2 false 64\nch 0 true 84\nch 1 false 104\n-112 104\n200 true 0 135\n99 0 3 3 175\ntap 2 10\n3 3 6 true true 209\ntrue false true 232\n",
	},
	{
		// A guarded compound assignment -- a shift by a variable count, a signed
		// division by a variable, the operators whose C and Go answers differ -- names
		// its target twice, "t = ogo_shl_T(t, n)", and was therefore refused for every
		// target that is not a name or a field path through one: "needs a target that
		// can be named twice; this one is evaluated". In a checked build, the default,
		// that was every field through a POINTER, whose C text carries the nil check
		// -- `f.sum /= f.n` in a method on *Filter -- besides any element of a field,
		// any field of an element, and an index that calls something. The target's
		// address is named once instead. Each call runs once, the target's before the
		// value's; a shift past the width and the most negative value over -1 take
		// Go's answers through the address as they do through a name.
		name: "a guarded compound assignment through any target",
		src: `type Filter struct {
	sum   int32
	n     int32
	shift uint
	wide  int64
	mask  uint64
}

func (f *Filter) Avg() {
	f.sum /= f.n
}

func (f *Filter) Scale() {
	f.sum >>= f.shift
	f.wide <<= f.shift
	f.mask >>= f.shift
}

type Inner struct {
	u8  uint8
	arr [4]uint8
}

type Regs struct {
	u8  uint8
	i32 [4]int32
	xs  []uint8
	m   [2][4]int32
	pp  *Inner
	ins [2]Inner
}

var regs Regs
var bank [3]Regs
var ptrs [2]*Regs
var other Regs
var inner Inner
var arr [4]uint8
var back [4]uint8
var calls int

func reg() *Regs {
	calls = calls*10 + 1
	return &regs
}

func idx() int {
	calls = calls*10 + 2
	return 2
}

func cnt() uint {
	calls = calls*10 + 3
	return 3
}

func div() int32 {
	calls = calls*10 + 4
	return -3
}

func main() {
	// The everyday shape: a method on a pointer, whose every field is behind the
	// nil check of a checked build.
	f := Filter{sum: 1000, n: 8, shift: 2, wide: 1 << 40, mask: 1 << 63}
	f.Avg()
	f.Scale()
	println(f.sum, f.wide, f.mask)

	// A field and an element through a pointer, a chain and an element's field.
	regs.u8, regs.pp, regs.xs = 3, &inner, back[:]
	regs.i32[2], regs.m[1][2], regs.xs[2] = -40, 41, 200
	regs.ins[1].u8, regs.ins[1].arr[2], inner.u8, inner.arr[2] = 3, 5, 6, 7
	bank[1].u8, other.u8, other.i32[2], arr[2] = 9, 10, -2147483648, 3
	ptrs[1] = &other
	p := &regs
	var s uint = 3
	var d int32 = 3
	var m1 int32 = -1
	var big uint = 40
	i := 2
	p.u8 <<= s
	p.i32[i] /= d
	p.m[i-1][i] %= d
	p.xs[i] >>= s
	p.pp.u8 <<= s
	p.pp.arr[i] <<= s
	p.ins[i-1].u8 <<= s
	regs.ins[i-1].arr[i] <<= s
	bank[i-1].u8 <<= s
	ptrs[i-1].u8 <<= big
	ptrs[i-1].i32[i] /= m1
	println(regs.u8, regs.i32[2], regs.m[1][2], regs.xs[2], inner.u8, inner.arr[2])
	println(regs.ins[1].u8, regs.ins[1].arr[2], bank[1].u8, other.u8, other.i32[2])

	// An index that calls something runs once, the target's calls before the
	// value's, left to right.
	arr[idx()] <<= cnt()
	println(arr[2], calls)
	calls = 0
	reg().i32[idx()] /= div()
	println(regs.i32[2], calls)
	calls = 0
	reg().m[idx()-1][idx()] >>= cnt()
	println(regs.m[1][2], calls)
	calls = 0
	reg().ins[idx()-1].arr[idx()] <<= cnt()
	println(regs.ins[1].arr[2], calls)
	calls = 0
	ptrs[idx()-1].i32[idx()] %= div()
	println(other.i32[2], calls)
}
`,
		want: "31 4398046511104 2305843009213693952\n24 -13 2 25 48 56\n24 40 72 0 -2147483648\n24 23\n4 124\n0 1223\n64 1223\n-2 224\n",
	},
	{
		// A for loop's post statement had a lowering of its own, thinner than the
		// statement's, and silent where it differed. It passed no target type, so the
		// guard of `<<=`, `>>=`, `/=` and `%=` was decided from the type of the
		// target's leading NAME -- a struct's for `reg.mask <<= n`, an array's for
		// `table[2] <<= n`, none for `p.acc /= d` -- and the operator went out as C's
		// own: a shift past the width took the count modulo it (1 << 40 was 256), the
		// most negative value over -1 trapped. A plain `=` did not reach the statement
		// lowering at all, so `total = 1 << n` for a uint64 shifted an int and gave 0.
		// The post clause now builds the statement's own assignment tail.
		name: "a for post statement is lowered as the statement is",
		src: `type Reg struct {
	mask uint32
	acc  int32
	wide uint64
	bits [4]uint32
}

var reg Reg
var table [4]uint32
var total uint64

func main() {
	p := &reg
	var big uint = 40
	var n uint = 40
	var m1 int32 = -1
	var local uint32 = 1
	reg.mask, reg.acc, reg.bits[2], table[2] = 1, -2147483648, 1, 1

	// A shift past the operand's width is 0 in Go and the count modulo the width in
	// C: through a field, a pointer, an element and an element of a field.
	for i := 0; i < 1; reg.mask <<= big {
		i++
	}
	for i := 0; i < 1; p.bits[2] <<= big {
		i++
	}
	for i := 0; i < 1; table[2] <<= big {
		i++
	}
	for i := 0; i < 1; local <<= big {
		i++
	}
	println(reg.mask, reg.bits[2], table[2], local)

	// The most negative value over -1 wraps in Go and traps in C.
	for i := 0; i < 1; p.acc /= m1 {
		i++
	}
	println(reg.acc)

	// An untyped constant shift takes its type from the target, here 64 bits.
	for i := 0; i < 1; total = 1 << n {
		i++
	}
	for i := 0; i < 1; reg.wide = 1 << n {
		i++
	}
	for i := 0; i < 1; p.wide |= 1 << (n + 1) {
		i++
	}
	println(total, reg.wide)
}
`,
		want: "0 0 0 0\n-2147483648\n1099511627776 3298534883328\n",
	},
	{
		// The same in the form that panics: a remainder by a zero divisor in a post
		// statement was C's own `%=`, which does not panic on the target and is a
		// SIGFPE on the host.
		name: "a for post statement's division by zero panics",
		src: `type Reg struct {
	acc int32
}

var reg Reg

func main() {
	p := &reg
	var zero int32
	reg.acc = 7
	for i := 0; i < 1; p.acc %= zero {
		i++
		println("body", i)
	}
	println("not reached")
}
`,
		want:   "body 1\npanic: integer divide by zero",
		panics: true,
	},
	{
		// A post statement whose lowering needs a statement of its own -- a field of a
		// struct a call returned, an operand the backend wants bound first, the hoisted
		// address of a guarded target with a call in it -- has no place in C's third
		// clause, an expression, and was refused: "a for-loop post statement may not
		// need a temporary; compute the value in the loop body instead". It goes to the
		// end of the body, where a multiple assignment's already did: a continue and a
		// labelled continue still run it, it reads the loop's variables and not the
		// body's, and its calls run once per iteration. Every loop that a skipped post
		// would spin counts its iterations, so a regression prints rather than hangs.
		name: "a for post statement that needs a temporary",
		src: `type P struct {
	x int
	y int
}

type W struct {
	ring [4]int32
	sum  int32
	mask uint32
}

var w W
var calls int

func mk(n int) P {
	calls++
	return P{n, n + 1}
}

func win() *W {
	calls += 10
	return &w
}

func slot() int {
	calls += 100
	return 1
}

func main() {
	spins := 0
	// A value that needs a temporary, a field of a struct a call returned; the
	// continue must still reach it.
	for i := 0; i < 5; i = mk(i).y {
		spins++
		if spins > 40 {
			println("the post was skipped")
			break
		}
		if i%2 == 1 {
			continue
		}
		println("even", i)
	}
	// A labelled continue from an inner loop.
outer:
	for i := 0; i < 3; i = mk(i).y {
		spins++
		if spins > 80 {
			println("the post was skipped")
			break
		}
		for j := 0; j < 3; j++ {
			if j == 1 {
				continue outer
			}
			println("ij", i, j)
		}
	}
	// A body variable named as one the post reads: the post reads the loop's.
	step := 1
	for i := 0; i < 3; i = mk(i).x + step {
		step := 40
		println("step", i, step)
	}
	// The accumulator of a ring buffer through a pointer, whose operand the
	// backend wants bound first, and a guarded target with calls in it.
	w.ring = [4]int32{5, 6, 7, 8}
	w.sum, w.mask = 100, 1
	p := &w
	var s uint = 3
	for k := 0; k < 4; p.sum -= p.ring[k-1] {
		k++
	}
	println(w.sum, calls)
	calls = 0
	for k := 0; k < 2; win().ring[slot()] <<= s {
		k++
	}
	println(w.ring[1], calls)
	calls = 0
	for k := 0; k < 2; win().mask <<= s {
		k++
		if k == 1 {
			continue
		}
	}
	println(w.mask, calls)
}
`,
		want: "even 0\neven 2\neven 4\nij 0 0\nij 1 0\nij 2 0\nstep 0 40\nstep 1 40\nstep 2 40\n74 11\n384 220\n64 20\n",
	},
	{
		// Two faults of a post that stands at the end of the body, both silent and both
		// as old as the placement. The post read the BODY's variables there -- `for i, j
		// := 0, 0; i < 6; i, j = i+s, j+1 { s := 10 ... }` stepped by ten -- where Go's
		// post clause belongs to the loop's scope; the body now has a block of its own.
		// And an inner loop cleared the label a continue jumps to without putting it
		// back, so a continue AFTER an inner loop was a plain C continue, skipped the
		// post, and the loop never ended. The iterations are counted for that reason.
		name: "a post at the end of the body keeps the loop's scope and its continue",
		src: `func main() {
	// The post reads the loop's s, not the body's.
	s := 1
	n := 0
	for i, j := 0, 0; i < 6; i, j = i+s, j+1 {
		s := 10
		n += s + j
	}
	println(n, s)

	// A continue after an inner loop still runs the post.
	spins, m := 0, 0
	for i, j := 0, 0; i < 4; i, j = i+1, j+2 {
		spins++
		if spins > 40 {
			println("the post was skipped")
			break
		}
		for k := 0; k < 2; k++ {
			m += 100
		}
		if i == 1 {
			continue
		}
		m += j
	}
	println(m, spins)

	// And one after an inner range loop and an inner loop with a post of its own.
	xs := [3]int{1, 2, 3}
	spins, m = 0, 0
	for i, j := 0, 10; i < 3; i, j = i+1, j-1 {
		spins++
		if spins > 40 {
			println("the post was skipped")
			break
		}
		for _, x := range xs {
			m += x
		}
		for a, b := 0, 0; a < 2; a, b = a+1, b+1 {
			if b == 0 {
				continue
			}
			m += 1000
		}
		if i != 1 {
			continue
		}
		m += j
	}
	println(m, spins)
}
`,
		want: "75 1\n810 4\n3027 3\n",
	},
	{
		// A multiple assignment binds every value to a temporary before any target is
		// written, and typed the temporary from the VALUE -- which for an untyped
		// constant is `int`, the default the typing gives it, where Go gives it the
		// target's type. So `lo, hi = 0, 1<<40` stored 0 in an int64, `a, b =
		// -9000000000000, 5` stored -2043514880, a uint64 lost `1<<63`: in a variable,
		// a field, an element and through a pointer, in the statement and in a loop's
		// clauses. Silent on the target; the host's C compiler refused the ones it
		// could see, which is how it was found. A constant's temporary now takes its
		// target's type.
		name: "a multiple assignment of a constant wider than an int",
		src: `type R struct {
	a, b int64
	u    uint64
	t    [2]int64
}

var r R
var ga, gb int64
var gu uint64

func main() {
	p := &r
	var la, lb int64
	ga, gb = -9000000000000, 5
	la, lb = 1<<40, 7
	gu, gb = 1<<63, gb+1
	println(ga, gb, gu, la, lb)
	r.a, r.b = 1<<40, -(1 << 41)
	p.u, p.t[1] = 0xF000000000000000, 1<<41
	r.t[0], la = la+1, 1<<42
	println(r.a, r.b, r.u, r.t[0], r.t[1], la)

	// A float target takes the constant as a float, and an untyped shift by a
	// variable is computed in its target's type.
	var f float32
	var n uint = 40
	f, lb = 1<<24, 1<<n
	println(f, lb)

	// The loop clauses' form of the same statement.
	var lo, hi int64
	for lo, hi = 0, 1<<40; lo < 2; lo, hi = lo+1, 1<<41 {
		println(lo, hi)
	}
	println(lo, hi)
}
`,
		want: "-9000000000000 6 9223372036854775808 1099511627776 7\n1099511627776 -2199023255552 17293822569102704640 1099511627777 2199023255552 4398046511104\n1.6777216e+07 1099511627776\n0 1099511627776\n1 2199023255552\n2 2199023255552\n",
	},
	{
		// A for loop's INIT clause, the post clause's neighbour and as thin. A plain
		// `=` there was `lhs = rhs` as C reads it, so `for total = 1 << n; ...` for a
		// uint64 shifted an int and stored 0. Several names were stored one after
		// another, so `for a, b = b, a; ...` left both holding b. And a declared name
		// shadowed what its neighbour read -- `for c, d := 1, c; ...` gave d the new
		// c, C's `int c = 1; int d = c;` -- as `for e := e + 1; ...` read the e it
		// was declaring. All silent. The clause now takes the statement's lowering,
		// assigns at once, and captures a value before the name that shadows it.
		name: "a for init clause is lowered as the statement is",
		src: `type R struct {
	wide uint64
}

var r R
var total uint64

func main() {
	p := &r
	var n uint = 40
	var l uint64
	k := 0
	// An untyped shift takes its type from the target, as in the statement.
	for total = 1 << n; k < 1; k++ {
	}
	for l = 1 << n; k < 2; k++ {
	}
	for p.wide = 1 << (n + 1); k < 3; k++ {
	}
	println(total, l, r.wide)

	// Several names are assigned at once.
	a, b := 1, 2
	for a, b = b, a; k < 4; k++ {
		println("swap", a, b)
	}
	x, y := 10, 20
	for x, y = y, x+y; k < 5; k++ {
		println("fib", x, y)
	}
	for l, k = 1<<(n+2), 5; k < 6; k++ {
	}
	println(l)

	// A declared name does not shadow what its neighbour, or its own value, reads.
	c := 5
	for c, d := 1, c; k < 7; k++ {
		println("shadow", c, d)
	}
	e := 5
	for e := e + 1; e < 8; e++ {
		println("self", e)
	}
	println(c, e)
}
`,
		want: "1099511627776 1099511627776 2199023255552\nswap 2 1\nfib 20 30\n4398046511104\nshadow 1 5\nself 6\nself 7\n5 5\n",
	},
	{
		// A declaration that gives a name another KIND. The emitter keeps what it knows
		// of a name in maps keyed by the source name, one each for types, arrays and
		// slices, restored when a block ends; and a declaration wrote the map of its
		// own kind and left the OUTER name's entry in the others. So a slice shadowing
		// the array it views, `buf := buf[2:5]` -- the everyday way to take a window --
		// was still an array to len, cap, range and the bounds check: len(buf) was the
		// array's, silently, and `if buf := buf[2:]; len(buf) == 2` skipped its body.
		// The same held for a package array under a local slice, since a lookup that
		// missed the local maps fell through to the package's, and for a parameter.
		// An array shadowing a slice emitted C that did not compile, and a loop
		// variable named as a block constant was folded away and the loop never ran.
		//
		// And a value that reads the name it shadows was read as the NEW kind, the
		// name being recorded before the value was rendered: `s := len(s)`, `p := *p`
		// and `v := v.n + 1` were refused, `var a, b = b, a` gave both the old b, and
		// `var count [3]int = [3]int{count, 2, 3}` was refused by gcc and BUILT by
		// flexcc. The value is now bound first, in the outer view.
		name: "a declaration that gives a name another kind",
		src: `type V struct {
	n     int
	items [3]int
}

var gbuf [8]uint8
var gxs []int
var gback [6]int
var count int = 4

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// A parameter array shadowed by a slice of itself in a nested block.
func tail(data [4]int) int {
	out := 0
	if data[0] > 0 {
		data := data[1:]
		out = len(data)*100 + sum(data)
	}
	return out + len(data)
}

// A parameter of one kind named as a package variable of another.
func byParam(gbuf []uint8, count [3]int) int {
	return len(gbuf)*100 + len(count)*10 + int(gbuf[0]) + count[2]
}

func main() {
	for i := range gbuf {
		gbuf[i] = uint8(i + 1)
	}
	for i := range gback {
		gback[i] = (i + 1) * 10
	}
	gxs = gback[:4]
	arr := [4]int{10, 20, 30, 40}
	xs := arr[:3]
	v := V{n: 2, items: [3]int{1, 2, 3}}
	p := &v
	s := "hello"

	// A slice shadowing the array it views: len, cap, range and an index are the
	// slice's.
	{
		arr := arr[1:3]
		t := 0
		for _, a := range arr {
			t += a
		}
		println(len(arr), cap(arr), t, arr[1], sum(arr))
	}
	if arr := arr[2:]; len(arr) == 2 {
		println("if", arr[0])
	}
	switch arr := arr[1:]; len(arr) {
	case 3:
		println("switch", arr[0])
	}
	// The same over a package array, and a loop that consumes its shadow.
	{
		gbuf := gbuf[2:5]
		println(len(gbuf), gbuf[0])
	}
	total := 0
	for gbuf := gbuf[:]; len(gbuf) > 0; gbuf = gbuf[2:] {
		total += int(gbuf[0])
	}
	println(total, len(gbuf), tail(arr), byParam(gbuf[2:5], [3]int{1, 2, 3}))
	// An array shadowing a slice, local and package.
	{
		xs := [2]int{5, 6}
		gxs := [2]int{7, 8}
		println(len(xs), xs[1], len(gxs), gxs[1])
	}
	println(len(xs), len(gxs))

	// A value that reads the name it shadows, of another kind.
	{
		s := len(s)
		p := *p
		v := v.n + 1
		p.n = 9
		println(s*2, p.n, v)
	}
	println(s, p.n, v.n)
	// The same through var, where a list of values reads the names beside it.
	{
		a, b := 5, 7
		{
			var a, b = b, a
			var s int = len(s)
			var gbuf []uint8 = gbuf[2:5]
			var count [3]int = [3]int{count, 2, 3}
			println(a, b, s, len(gbuf), len(count), count[0])
		}
		println(a, b)
	}
	// A loop variable named as a block constant is a variable.
	const k = 3
	n := 0
	for k := 0; k < 2; k++ {
		n += k + 1
	}
	println(n, k, count)
}
`,
		want: "2 3 50 30 50\nif 30\nswitch 20\n3 3\n16 8 394 336\n2 6 2 8\n3 4\n10 9 3\nhello 2 2\n7 5 5 3 3 4\n5 7\n3 3 4\n",
	},
	{
		// The bounds check of a slice that shadows its array is the slice's: arr[2] of
		// a two-element window panics. It was checked against the array's four, and
		// read the element past the window's end in silence.
		name: "an index past a slice that shadows its array panics",
		src: `func main() {
	arr := [4]int{10, 20, 30, 40}
	i := 2
	{
		arr := arr[1:3]
		println(len(arr), arr[1])
		println(arr[i])
	}
	println("not reached")
}
`,
		want:   "2 30\npanic: index out of range",
		panics: true,
	},
	{
		// The name a type switch binds, and the one an assertion declares, may shadow a
		// name of another kind as any declaration may: `switch v := sh.(type)` under an
		// array v was "v has no field s", the array's entry outliving the binding. The
		// type switch has a scope of its own now, which is also what gives the outer v
		// back after it; its hand-written restore knew only about types.
		name: "a type switch binding that shadows an array",
		src: `type Shape interface {
	Area() int
}

type Sq struct {
	s int
}

func (q *Sq) Area() int {
	return q.s * q.s
}

type Rect struct {
	w, h int
}

func (r *Rect) Area() int {
	return r.w * r.h
}

var sq = Sq{3}
var rc = Rect{2, 5}

func show(sh Shape) {
	v := [3]int{1, 2, 3}
	xs := v[:2]
	switch v := sh.(type) {
	case *Sq:
		println("sq", v.Area(), v.s)
	case *Rect:
		println("rect", v.Area(), v.w)
	}
	switch xs := sh.(type) {
	case *Sq, *Rect:
		println("either", xs.Area())
	}
	println(len(v), v[2], len(xs))
	if xs, ok := sh.(*Rect); ok {
		println("assert", xs.h)
	}
	println(len(xs))
}

func main() {
	show(&sq)
	show(&rc)
}
`,
		want: "sq 9 3\neither 9\n3 3 2\n2\nrect 10 2\neither 10\n3 3 2\nassert 5\n2\n",
	},
	{
		// A send to a channel a chain reaches. An index BETWEEN two fields,
		// `bus.ports[i].ch <- v`, was "cannot send to non-channel": the check flattened
		// the chain to a run of fields and two flags. A channel behind a pointer-typed
		// field was refused the same way. Once accepted, the first parked its cog for
		// ever: the channels of an array of structs held in a struct FIELD were never
		// allocated. And the channel and the value are two arguments of one C call,
		// whose order is the C compiler's -- the host called val before pick, where Go
		// evaluates the channel first; the digits record the order.
		//
		// Every line prints what real Go prints, given the channels it must make.
		name: "a send to a channel in a field of an element",
		src: `type Port struct {
	ch chan int
}

type Bus struct {
	ports [2]Port
	peer  *Port
}

var bus Bus
var spare Port
var qs [2]chan int
var done chan int
var calls int

func pick(i int) int {
	calls = calls*10 + 3
	return i
}

func val(v int) int {
	calls = calls*10 + 4
	return v
}

func drain(ch chan int) {
	v := <-ch
	done <- v
}

func main() {
	bus.peer = &spare
	// The channel is evaluated before the value, as Go evaluates them.
	go drain(qs[1])
	qs[pick(1)] <- val(6)
	println(<-done, calls)
	// An index between two fields, over channels that a struct's array field holds.
	calls = 0
	go drain(bus.ports[1].ch)
	bus.ports[pick(1)].ch <- val(7)
	println(<-done, calls)
	p := &bus
	go drain(bus.ports[0].ch)
	p.ports[0].ch <- 8
	println(<-done)
	// A channel behind a pointer-typed field.
	go drain(spare.ch)
	bus.peer.ch <- 9
	println(<-done)
}
`,
		want: "6 34\n7 34\n8\n9\n",
	},
	{
		// A select's SEND clause took a channel variable or a field of one, and its
		// grammar took no call: `case bus.port(i).ch <- v` was a syntax error, and an
		// element, `case qs[i] <- v`, was refused by the emitter. The clause's channel
		// is now put back together as the expression it spells and resolved as a
		// receive clause's operand is, so every shape of one works -- a call's result,
		// a method's, an element, an element's field, a dereference, a parenthesised
		// head -- and each operand is evaluated once, in source order, where the
		// select stands: the digits record that.
		//
		// The plain send at the end is the statement the clause is written as, over the
		// same bank of ports (see the case above for the three faults it met).
		//
		// Every line prints what real Go prints for the same program, given the
		// channels it must make.
		name: "a select send clause on any channel expression",
		src: `type Port struct {
	ch chan int
}

type Bus struct {
	ports [2]Port
}

var bus Bus
var qs [2]chan int
var direct chan int
var done chan int
var calls int

func (b *Bus) port(i int) *Port {
	calls = calls*10 + 1
	return &b.ports[i]
}

func port(i int) *Port {
	calls = calls*10 + 2
	return &bus.ports[i]
}

func pick(i int) int {
	calls = calls*10 + 3
	return i
}

func val(v int) int {
	calls = calls*10 + 4
	return v
}

func drain(ch chan int) {
	v := <-ch
	done <- v
}

func main() {
	go drain(bus.ports[0].ch)
	select {
	case port(0).ch <- val(5):
		println("call")
	}
	println(<-done, calls)
	calls = 0
	go drain(qs[1])
	select {
	case qs[pick(1)] <- val(6):
		println("index")
	}
	println(<-done, calls)
	calls = 0
	go drain(bus.ports[1].ch)
	select {
	case bus.port(pick(1)).ch <- val(7):
		println("method")
	}
	println(<-done, calls)
	calls = 0
	go drain(bus.ports[1].ch)
	select {
	case bus.ports[pick(1)].ch <- val(8):
		println("chain")
	}
	println(<-done, calls)
	// Every operand is evaluated once, in source order, where the select stands.
	calls = 0
	go drain(bus.ports[0].ch)
	select {
	case v := <-port(1).ch:
		println("recv", v)
	case port(0).ch <- val(9):
		println("send")
	}
	println(<-done, calls)
	pch := &direct
	go drain(direct)
	select {
	case *pch <- 10:
		println("deref")
	}
	println(<-done)
	p := &bus.ports[0]
	go drain(p.ch)
	select {
	case (p).ch <- 11:
		println("paren")
	}
	println(<-done)
	// A plain send through an element's field: the index stands between two fields,
	// and the channel is one of a bank that its struct's declaration allocates.
	calls = 0
	go drain(bus.ports[1].ch)
	bus.ports[pick(1)].ch <- val(12)
	println(<-done, calls)
}
`,
		want: "call\n5 24\nindex\n6 34\nmethod\n7 314\nchain\n8 34\nsend\n9 224\nderef\n10\nparen\n11\n12 34\n",
	},
	{
		// An else-if that carries an init statement, `} else if b := f(); b > 0 {`. It
		// was read as a plain else-if: the init was dropped and the declared NAME stood
		// as the condition, `else if (b)` -- C that does not compile where b is new,
		// and silently wrong where the name already meant something, `else if a := a *
		// 2; a == 8`, which tested the outer a. Such an else-if is a whole if statement
		// standing in the else, and is emitted as one: its init runs only when the
		// tests before it failed, its name reaches the rest of the chain, a call
		// destructured there works, and a defer in its branch is the branch's.
		name: "an else-if that carries an init statement",
		src: `func f(v int) int {
	println("f", v)
	return v
}

func pair(v int) (int, bool) {
	return v * 3, v > 1
}

func withDefer(n int) {
	if a := n; a > 5 {
		defer println("deferred first", a)
	} else if b := a * 2; b > 5 {
		defer println("deferred second", b)
	}
	println("body", n)
}

func main() {
	// The init of an else-if is evaluated only when the tests before it failed, and
	// its name is in scope for the rest of the chain.
	if a := f(1); a > 5 {
		println("first", a)
	} else if b := f(a + 1); b == 2 {
		println("second", a, b)
	} else {
		println("else", a, b)
	}
	if a := f(9); a > 5 {
		println("first", a)
	} else if b := f(a + 1); b == 2 {
		println("second", a, b)
	}
	// A name the else-if declares may shadow the one before it, and read it.
	if a := f(4); a > 5 {
		println("x")
	} else if a := a * 2; a == 8 {
		println("shadow", a)
	}
	// A call destructured in an else-if, and a third link.
	if a := 0; a > 5 {
		println("x")
	} else if v, ok := pair(a); ok {
		println("pair", v)
	} else if w := v + 7; w == 7 {
		println("third", v, ok, w)
	}
	withDefer(1)
	withDefer(3)
	withDefer(9)
}
`,
		want: "f 1\nf 2\nsecond 1 2\nf 9\nfirst 9\nf 4\nshadow 8\nthird 0 false 7\nbody 1\nbody 3\ndeferred second 6\nbody 9\ndeferred first 9\n",
	},
	{
		// An if's and a switch's init statement with a value for EACH name, `if a, b :=
		// x, y; a < b`, which the grammar did not admit: it took one value, the
		// destructuring of a call, and a second was a syntax error. It is the
		// statement `a, b := x, y` inside the block that scopes the names: every value
		// is read before any name is declared, so a swap swaps and an outer name is
		// still the outer one in its own initializer; the digits record that the
		// values run in source order.
		name: "an if or switch init with a value for each name",
		src: `var calls int

func f(v int) int {
	calls = calls*10 + v
	return v
}

func pair() (int, bool) {
	return 7, true
}

func main() {
	if a, b := f(1), f(2); a < b {
		println("if", a, b)
	}
	if a, b, c := 1<<3, "two", 3.5; a == 9 {
		println(a, b, c)
	} else if d, e := a+1, b; d == 9 {
		println(d, e, c)
	}
	x, y := 5, 7
	if x, y := y, x; x > y {
		println("swapped", x, y)
	}
	switch a, b := f(3), f(4); a + b {
	case 7:
		println("switch", a, b)
	}
	switch p, q := x*2, y; {
	case p > q:
		println("tagless", p, q)
	}
	if v, ok := pair(); ok {
		println("pair", v)
	}
	println(x, y, calls)
}
`,
		want: "if 1 2\n9 two 3.5\nswapped 7 5\nswitch 3 4\ntagless 10 7\npair 7\n5 7 1234\n",
	},
	{
		// A bare return inside a function literal that is WRITTEN in main. The flag
		// that makes main's own bare return `return 0;` was neither saved nor cleared
		// when a literal was lifted out of main, so the literal's was `return 0;` too
		// -- in a void C function, which the host's compiler refuses.
		name: "a bare return in a function literal written in main",
		src: `func main() {
	defer func(k int) {
		if k == 1 {
			return
		}
		println("deferred", k)
	}(2)
	f := func(k int) {
		if k == 1 {
			return
		}
		println("value", k)
	}
	f(1)
	f(3)
}
`,
		want: "value 3\ndeferred 2\n",
	},
	{
		// A function LITERAL whose results are a struct, or several, taken as a
		// value: bound, passed, held in a field and in a table. Such a value points
		// at a wrapper writing the results through an out parameter, as a named
		// function's does (funcValueWrapper); the literal itself was taken, and the
		// target's compiler only warned -- `h := func() T { return T{N: 9} }`
		// printed 0 for h().N on a P2-EDGE. And a literal of several results was
		// lifted with no return type at all, its results recorded as none.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "function literals of struct and several results as values",
		src: `type T struct {
	N  int
	OK bool
}

type Op struct {
	f func(int) T
}

func apply(f func(int) T, k int) T { return f(k) }

func main() {
	two := func(k int) (int, bool) { return k * 2, k > 1 }
	a, b := two(3)
	println(a, b)
	h := func() T { return T{N: 9, OK: true} }
	println(h().N, h().OK)
	var g func(int) T = func(k int) T { return T{N: k, OK: k > 2} }
	println(g(5).N, g(1).OK)
	println(apply(func(k int) T { return T{N: k + 1} }, 6).N)
	o := Op{f: func(k int) T { return T{N: k * 3} }}
	println(o.f(4).N)
	fs := []func(int) T{func(k int) T { return T{N: -k} }}
	println(fs[0](7).N)
}
`,
		want: "6 true\n9 true\n5 false\n7\n12\n-7\n",
	},
	{
		// A function literal of several results called where it stands:
		// destructured, and as a statement whose results are dropped. The first
		// was "multiple assignment requires a single function call on the
		// right-hand side", the second refused by name -- both for want of the
		// result struct, which the literal was lifted without.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a function literal of several results called where it stands",
		src: `func main() {
	v, ok := func() (int, bool) { return 7, true }()
	println(v, ok)
	a, b := func(k int) (int, int) { return k, k * k }(5)
	println(a, b)
	func() (int, bool) { return 1, false }()
}
`,
		want: "7 true\n5 25\n",
	},
	{
		// Two function types that C spells alike: `func() T` is `void (*)(T*)`,
		// its result travelling through the out parameter, and so is `func(*T)`.
		// They were one typedef carrying the first one's results, and the call
		// through a `func(*T)` handed it an out parameter it does not take.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "two function types of one C shape",
		src: `type T struct {
	N int
}

func inc(p *T) { p.N++ }

func mk() T { return T{N: 9} }

func main() {
	t := T{N: 4}
	h := mk
	println(h().N)
	var f func(*T) = inc
	f(&t)
	println(t.N)
}
`,
		want: "9\n5\n",
	},
	{
		// A call through what a call returns, `pick()(x)`: as a statement for a
		// function of no results, which was "only <pkg>.<Func>(args) ... call
		// statements are supported yet" -- the chain typer has no head for a
		// function's name -- and deferred, which compiled for no function at all:
		// the callee's call is Go's to run at the defer, and the replay rendered it
		// at the return into a temporary it could not see. And a deferred call
		// through a function VALUE whose results travel through an out parameter,
		// `f := pickMk(); defer f(9)`, was called without one.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a call through what a call returns, as a statement and deferred",
		src: `var calls int

type T struct {
	N int
}

func inc(p *T) {
	calls = calls*10 + 1
	p.N++
}

func two(k int) (int, bool) {
	calls = calls*10 + 2
	return k, k > 0
}

func mk(k int) T {
	calls = calls*10 + 3
	return T{N: k}
}

func pick() func(*T) {
	calls = calls*10 + 4
	return inc
}

func pickTwo() func(int) (int, bool) {
	calls = calls*10 + 5
	return two
}

func pickMk() func(int) T {
	calls = calls*10 + 6
	return mk
}

var t T

// Each deferred call's function is a CALL's result: that call runs where the
// defer stands, and what it returned is called at the return.
func run() {
	defer pick()(&t)
	defer pickMk()(8)
	defer pickTwo()(3)
	f := pickMk()
	defer f(9)
	println("before", calls, t.N)
}

func main() {
	pick()(&t)
	println(t.N, calls)
	calls = 0
	pickTwo()(3)
	pickMk()(8)
	println(calls)
	calls = 0
	println(pickMk()(9).N, calls)
	calls = 0
	v, ok := pickTwo()(-1)
	println(v, ok, calls)
	calls = 0
	run()
	println("after", calls, t.N)
}
`,
		want: "1 41\n5263\n9 63\n-1 false 52\nbefore 4656 1\nafter 46563231 2\n",
	},
	{
		// Two go statements: a method promoted through an embedded POINTER, which
		// hands the goroutine that pointer -- it was refused for handing it the
		// local w -- and a function a CALL returns, evaluated at the go statement as
		// Go evaluates it, which was "only `go f(args)` ... is supported yet".
		//
		// Every line of this prints what real Go prints for the same program.
		name: "go through an embedded pointer and through what a call returns",
		src: `var done chan int

type Holder struct {
	n int
}

func (h *Holder) Run(k int) { done <- h.n * k }

type PW struct {
	*Holder
	tag int
}

var gh = Holder{n: 3}

var calls int

func work(k int) { done <- k * 100 }

func pick() func(int) {
	calls++
	return work
}

func pick2(base int) func(int) {
	calls += base
	return work
}

func main() {
	w := PW{&gh, 1}
	go w.Run(5)
	println(<-done)
	go pick()(7)
	println(<-done, calls)
	go pick2(10)(2)
	println(<-done, calls)
}
`,
		want: "15\n700 1\n200 11\n",
	},
	{
		// A value that binds a call ahead of the statement, sent on a channel, sent
		// in a select and appended: each was bound TWICE -- the call ran twice on a
		// P2-EDGE, `ch <- len(name())` answering calls == 11 where Go says 1. The send
		// asked whether its element was an interface only after rendering the value
		// for that question, and threw the rendering away.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "a sent, selected or appended value is evaluated once",
		src: `var calls int

var ch chan int

func name() string {
	calls = calls*10 + 1
	return "abc"
}

// Each value binds its call ahead of the statement, and was bound twice: the
// send, the select send and the appended element each asked a question that
// rendered the value, threw the text away and rendered it again.
func main() {
	go func() {
		ch <- len(name())
	}()
	v := <-ch
	println(v, calls)
	calls = 0
	go func() {
		select {
		case ch <- len(name()) + 1:
		}
	}()
	v = <-ch
	println(v, calls)
	calls = 0
	var xs []int = make([]int, 0, 2)
	xs = append(xs, len(name()))
	println(xs[0], len(xs), calls)
}
`,
		want: "3 1\n4 1\n3 1 1\n",
	},
	{
		// A value of one interface type where another is wanted, from anything that
		// has one -- an element, a field, a call's result, not only a variable -- in
		// every position a value stands: a declaration, an assignment, an argument
		// (among others that do something), a return, a conversion, a literal, a
		// list, a variadic pack, a defer and a go statement. calls records the order.
		//
		// Every line of this prints what real Go prints for the same program.
		name: "an interface value where another interface is wanted, in every position",
		src: `type Shape interface {
	Area() int
	Name() string
}

type Named interface {
	Name() string
}

type Sq struct {
	S int
}

func (q *Sq) Area() int { return q.S * q.S }

func (q *Sq) Name() string { return "sq" }

type NH struct {
	n Named
	k int
}

var gq = Sq{2}

var calls int

var done chan int

func get() Shape {
	calls = calls*10 + 1
	return &gq
}

func k() int {
	calls = calls*10 + 2
	return 10
}

func nameOf(n Named) string { return n.Name() }

func two(n Named, x int) int { return len(n.Name()) + x }

func ret() Named { return get() }

func names(ns ...Named) int {
	t := 0
	for _, n := range ns {
		t += len(n.Name())
	}
	return t
}

func show(n Named) {
	if n == nil {
		println("show nil")
		return
	}
	println("show", n.Name())
}

func work(n Named) { done <- len(n.Name()) }

// A value of Shape where a Named is wanted -- an element, a field, a call's result,
// not only a variable -- in a declaration, an assignment, an argument, a return, a
// literal, a list, a variadic pack, a defer and a go statement.
func run() {
	var s0 Shape = &gq
	var shapes [2]Shape
	shapes[1] = s0
	h := NH{n: get(), k: 1}
	var n Named = shapes[1]
	var m Named
	m = h.n
	println(n.Name(), m.Name(), Named(get()).Name(), calls)
	calls = 0
	println(nameOf(get()), two(get(), k()), two(&gq, k()), ret().Name(), calls)
	calls = 0
	ns := [2]Named{shapes[1], get()}
	var a, b Named
	a, b = s0, get()
	println(ns[1].Name(), a.Name(), b.Name(), names(s0, get(), shapes[1]), calls)
	calls = 0
	defer show(get())
	defer show(&gq)
	defer show(nil)
	go work(get())
	println(<-done, calls)
}

func main() {
	run()
	println("end", calls)
}
`,
		want: "sq sq sq 11\nsq 12 12 sq 11221\nsq sq sq 6 111\n2 11\nshow nil\nshow sq\nshow sq\nend 11\n",
	},
	{
		// The predeclared nil in a list assignment takes the type of its target, as
		// it does alone. Each of these was "cannot infer the type of a value in a
		// multiple assignment".
		//
		// Every line of this prints what real Go prints for the same program.
		name: "nil in a list assignment",
		src: `type Named interface {
	Name() string
}

type T struct {
	n int
}

func (t *T) Name() string { return "t" }

var gt T

// The predeclared nil in a list assignment takes the type of the target in its
// position, as it does alone: each of these was "cannot infer the type of a value
// in a multiple assignment".
func main() {
	x := 5
	p := &x
	xs := []int{1, 2}
	var f func()
	var n Named = &gt
	var k int
	p, k = nil, 1
	println(p == nil, k)
	xs, p = nil, &x
	println(xs == nil, len(xs), *p)
	f, k = nil, 2
	println(f == nil, k)
	n, k = nil, 3
	println(n == nil, k)
	n, p = &gt, nil
	println(n.Name(), p == nil)
}
`,
		want: "true 1\ntrue 0 5\ntrue 2\ntrue 3\nt true\n",
	},
	{
		name: "a method called on a parenthesized receiver",
		src: `type Counter struct {
	n int
}

func (c *Counter) Inc(k int) { c.n += k }

func (c *Counter) Run(k int, done chan bool) {
	c.n += k
	done <- true
}

func (c Counter) Get() int { return c.n }

var gc Counter

var done chan bool

func deferred() {
	defer (&gc).Inc(100)
	println("before", (&gc).Get())
}

// A parenthesized receiver is the receiver it holds, (&v).m() being v.m(): in a
// call, a defer and a go statement, the last of which was refused.
func main() {
	p := &gc
	(&gc).Inc(1)
	(gc).Inc(2)
	(*p).Inc(3)
	println((&gc).Get(), (gc).Get(), (*p).Get(), (gc).n)
	go (&gc).Run(10, done)
	<-done
	go (*p).Run(20, done)
	<-done
	deferred()
	println(gc.n)
}
`,
		want: "6 6 6 6\nbefore 36\n136\n",
	},
	{
		name: "conversions to pointer types",
		src: `type Shape interface {
	Area() int
}

type Sq struct {
	S int
}

func (q *Sq) Area() int { return q.S * q.S }

func (q *Sq) Set(n int) { q.S = n }

func (q *Sq) Run(n int, done chan bool) {
	q.S = n
	done <- true
}

// Sq2 has Sq's underlying type and none of its methods.
type Sq2 struct {
	S int
}

func (q *Sq2) Twice() { q.S *= 2 }

type Celsius int

var _ Shape = (*Sq)(nil)

var gq Sq

var done chan bool

var calls int

func pick(p *Sq) *Sq {
	calls++
	return p
}

func typed() {
	var p *int = (*int)(nil)
	var s Shape = (*Sq)(nil)
	println(p == nil, s != nil, (*Sq)(nil) == nil)
}

func views() {
	x := 7
	var c Celsius = 30
	pc := (*Celsius)(&x)
	*pc = 8
	px := (*int)(&c)
	*px += 1
	q := (*Sq2)(&gq)
	q.Twice()
	println(x, c, gq.S, (*Sq)(&gq).Area())
}

func stmts() {
	(*Sq)(&gq).Set(3)
	(*Sq2)(&gq).Twice()
	go (*Sq)(&gq).Run(5, done)
	<-done
	println(gq.S)
	defer (*Sq)(pick(&gq)).Set(9)
	gq.S = 4
	println(gq.S, calls)
}

// A conversion to a pointer type: the typed nil, a pointer taken as one to another
// type of its underlying type, and a method called on the result where it stands
// -- in a call, a go statement, and a defer, which converts where it stands.
func main() {
	gq.S = 2
	typed()
	views()
	stmts()
	println(gq.S, calls)
}
`,
		want: "true true true\n8 31 4 16\n5\n4 1\n9 1\n",
	},
	{
		name: "a slice's elements are what they point at",
		src: `var gx, gy int

var gp *int

var gback [4]*int

var gps = gback[:0]

var gb [2]int

var gs []int

func sum(ps []*int) int {
	t := 0
	for i := range ps {
		t += *ps[i]
	}
	return t
}

// A slice's elements carry what they point at, and its backing array is no
// element's: ranging over a scratch slice of pointers to package variables,
// reading one out of a slice declared in a list, and spreading one into a
// package slice were refused as if each element pointed into the scratch slice.
func main() {
	gx, gy = 3, 4
	s := []*int{&gx, &gy}
	for _, e := range s {
		gp = e
	}
	println(*gp)
	a, b := []*int{&gy}, []*int{&gx}
	gp = a[0]
	println(*gp, *b[0])
	gps = append(gps, s...)
	println(len(gps), sum(gps))
	var rows [1][]int
	rows[0] = gb[:]
	for _, r := range rows[:] {
		gs = r
	}
	gs[1] = 7
	println(gb[1])
}
`,
		want: "4\n4 3\n2 7\n7\n",
	},
	{
		name: "an element of a slice of pointers is a pointer",
		src: `type Sq struct {
	S int
}

func (q *Sq) Area() int { return q.S * q.S }

var gx, gy = 3, 4

var gback = [2]*int{&gx, &gy}

var garr [2]*int

var gsq = Sq{5}

var gsqs = []*Sq{&gsq}

func sum(ps []*int) int {
	t := 0
	for _, p := range ps {
		t += *p
	}
	return t
}

// An element of a slice or an array of pointers is a pointer, read by index or
// ranged over: *p was "cannot indirect p".
func main() {
	garr = gback
	ps := gback[:]
	println(sum(ps))
	p := ps[0]
	*p = 7
	for i, e := range garr {
		println(i, *e)
	}
	q := gsqs[0]
	q.S = 6
	for _, e := range gsqs {
		println(e.Area(), e.S)
	}
	e := garr[1]
	*e += 1
	println(gx, gy, *e)
}
`,
		want: "7\n0 7\n1 4\n36 6\n7 5 5\n",
	},
	{
		name: "an interface made from a pointer points where the pointer does",
		src: `type Saver interface {
	Save()
}

type Box struct {
	n int
}

var gbox *Box

var gb, gb2 Box

var gsv Saver

func (b *Box) Save() { gbox = b }

func keep(p *Box) {
	var s Saver = p
	s.Save()
}

func store(p *Box) {
	var s Saver = p
	gsv = s
}

// An interface made from a pointer points where the pointer does: made from a
// pointer parameter it holds the caller's storage, which a method may keep and a
// package variable may hold -- both were refused as if it pointed at the parameter.
func main() {
	gb.n, gb2.n = 1, 2
	keep(&gb)
	println(gbox.n)
	store(&gb2)
	gsv.Save()
	println(gbox.n)
	p := &gb
	var s Saver = p
	s.Save()
	gsv = s
	println(gbox.n, gbox == &gb)
}
`,
		want: "1\n2\n1 true\n",
	},
	{
		name: "a typed package slice from a slice value",
		src: `var gx, gy = 3, 4

var gback = [4]*int{&gx, &gy}

var gps []*int = gback[:2]

var gall []*int = gback[:]

var nums = [5]int{1, 2, 3, 4, 5}

var mid []int = nums[1:4]

var other []int = mid

func mk() []int { return nums[:2] }

var fromCall []int = mk()

// A package slice declared with its type written, from a slice of a package
// array, another package slice or a call's result: only the inferred form, var s =
// back[:0], was accepted.
func main() {
	println(len(gps), cap(gps), *gps[1], len(gall))
	println(len(mid), mid[0], cap(mid), len(other), other[2])
	mid[1] = 30
	println(nums[2], len(fromCall), fromCall[1])
}
`,
		want: "2 4 4 4\n3 2 4 3 4\n30 2 2\n",
	},
	{
		name: "declarations using types declared below them",
		src: `var gp = &Pt{1, 2}

var gr = Row{7, 8, 9}

var gq = Q{Pt: Pt{3, 4}, tag: "q"}

var gl = L{5, 6}

var gz = A{9, 1}

const K = len(Row{}) + N

func (p *Pt) Sum() int { return p.x + p.y }

var gs Shape = &gq.Pt

// Declarations may use types declared below them, as Go's package block allows:
// a variable's literal was "Pt is not a struct type", and a type over a later one
// -- an alias of an alias, an array of an array -- was "unsupported type".
func main() {
	var g Grid
	g[1][2] = 5
	var c C1 = 4
	println(gp.Sum(), gr[2], gq.tag, gq.Pt.Sum(), len(gl), K, gs.Sum(), gz.x)
	println(g[1][2], c, len(g))
}

type Shape interface {
	Sum() int
}

type Q struct {
	Pt
	tag string
}

type A = B

type B = Pt

type C1 T2

type Grid [2]Row

type L []int

type Row [N]int

type T2 int

const N = 3

type Pt struct {
	x, y int
}
`,
		want: "3 9 q 7 2 6 7 9\n5 4 2\n",
	},
	{
		// A function literal called where it stands, as a statement of its own, which
		// the grammar had no production for: a statement could not begin with "func".
		// It is lifted as a literal is anywhere and called by name; arguments are how a
		// value reaches it, a result is thrown away as a call statement's is, and a
		// return inside it leaves the literal and not the loop around the statement.
		name: "a function literal called as a statement",
		src: `type P struct {
	x, y int
}

var total int

func main() {
	func() {
		println("called")
	}()
	func(n int, s string) {
		total += n
		println(s, total)
	}(5, "added")
	func(a, b int) int {
		total += a * b
		return total
	}(3, 4)
	func() P {
		total++
		return P{total, 2}
	}()
	for i := 0; i < 2; i++ {
		func(k int) {
			if k == 1 {
				return
			}
			println("loop", k)
		}(i)
	}
	println(total)
}
`,
		want: "called\nadded 5\nloop 0\n18\n",
	}, {
		// A name declared in the header of an if, a for or a switch was typed by its
		// value's KIND alone, and the kind of `&x` is x's: `if p := &gx; *p > 4` was
		// "cannot indirect p (variable of type int)", and the loop that walks a list by
		// pointer, `for n := &nodes[0]; n != nil; n = n.next`, lost the pointer the same
		// way. A struct, a function value or a method's receiver declared there carried
		// no type at all, so nothing read off it was checked. The headers now ask what
		// a statement's `x := e` asks (inferHeaderVar).
		name: "a name declared in a statement header keeps its type",
		src: `type Node struct {
	v    int
	next *Node
}

type Acc struct {
	sum int
}

func (a *Acc) add(n int) int {
	a.sum += n
	return a.sum
}

var nodes [3]Node
var acc Acc
var gx int

func dbl(n int) int {
	return n * 2
}

func pick() func(int) int {
	return dbl
}

func main() {
	nodes[0] = Node{1, &nodes[1]}
	nodes[1] = Node{2, &nodes[2]}
	nodes[2] = Node{3, nil}
	gx = 5
	total := 0
	for n := &nodes[0]; n != nil; n = n.next {
		total += n.v
	}
	println(total)
	if p := &gx; *p > 4 {
		*p = 9
	}
	println(gx)
	if a := &acc; a.add(3) > 2 {
		println(a.add(4), acc.sum)
	}
	switch p := &gx; {
	case *p == 9:
		*p++
	}
	println(gx)
	if f := pick(); f(4) == 8 {
		println(f(5))
	}
	if n, q := nodes[1], &nodes[2]; n.v < q.v {
		q.v += n.v
		println(n.v, q.v, nodes[2].v)
	}
	for p, i := &nodes[0], 0; i < 2; p, i = p.next, i+1 {
		println(i, p.v)
	}
	switch a, k := &acc, 2; a.add(k) {
	case 9:
		println("nine")
	default:
		println("other", acc.sum)
	}
}
`,
		want: "6\n9\n7 7\n10\n10\n2 5 5\n0 1\n1 2\nnine\n",
	}, {
		// HeaderFactor had dropped the suffix from three of Factor's alternatives, so in
		// the header of an if, a for or a switch nothing could follow a parenthesised
		// expression, a literal of a bracketed type or a function literal: `if
		// (&p).M() == 7 {`, `switch (v).(type) {`, `for i := range (arr)[1:] {` and `if
		// func() bool { ... }() {` were each a syntax error, as was the `range
		// []int{...}[1:]` recorded among the open items. With the grammar's two
		// productions in step, the type switch and the two-value assertion look through
		// the parentheses of their operand, which the expression form already did.
		name: "a suffix after a parenthesis, a literal or a function literal in a header",
		src: `type P struct {
	x int
	a [3]int
}

type I interface {
	M() int
}

func (p *P) M() int {
	return p.x
}

var arr [4]int
var p P
var calls int

func bump(k int) int {
	calls = calls*10 + k
	return k
}

func main() {
	arr[1], arr[2], arr[3] = 5, 6, 7
	p.x = 7
	var iv I = &p
	n := 0
	if (arr)[1] == 5 {
		n++
	}
	if (&p).M() == 7 {
		n++
	}
	if func() bool { return bump(1) == 1 }() {
		n++
	}
	if [3]int{1, 2, 3}[bump(2)-1] == 2 {
		n++
	}
	if v := []int{4, 5, 6}[1]; v == 5 {
		n++
	}
	for i := range (arr)[1:] {
		n += i
	}
	for _, v := range (arr)[2:] {
		n += v
	}
	for i := func() int { return bump(3) }(); i < 5; i++ {
		n++
	}
	for i := 0; i < (arr)[1]; i += (arr)[1] - 3 {
		n++
	}
	switch (iv).(type) {
	case *P:
		n += 100
	}
	switch x := (iv).(type) {
	case *P:
		n += x.x
	}
	switch (arr)[1] {
	case 5:
		n += 1000
	}
	switch func() int { return bump(4) }() {
	case 4:
		n += 10000
	}
	if v, ok := (iv).(*P); ok {
		n += v.a[0] + 1
	}
	println(n, calls)
}
`,
		want: "11134 1234\n",
	}, {
		// `[]int{1, 2, 3}[1:]` was refused -- "a []int literal cannot be read through
		// this suffix", or "cannot infer a type" where it was declared from -- since the
		// walk that reads a literal through its suffix leaves a slice step that ENDS a
		// chain to the fixed shapes, none of which reads a literal. It is claimed now;
		// a row of the literal slices as a variable's does; and the value is typed from
		// the literal, so a declaration, a range, len and cap all take it. The elements
		// run before the bounds, in order. Slicing an ARRAY literal stays refused, in
		// Go's words: it is not addressable.
		name: "a slice literal sliced",
		src: `type P struct {
	x, y int
}

var calls int

func f(k int) int {
	calls = calls*10 + k
	return k
}

func sum(xs []int) int {
	t := 0
	for _, v := range xs {
		t += v
	}
	return t
}

func ranged() int {
	n := 0
	for i, v := range []int{5, 6, 7}[1:] {
		n += i*10 + v
	}
	for i := range []int{5, 6, 7}[:2] {
		n += i + 1
	}
	for _, q := range []P{{1, 2}, {3, 4}, {5, 6}}[1:] {
		n += q.x
	}
	for k := 0; k < 3; k++ {
		v := []int{k, k + 1, k + 2}[1:]
		n += v[0]
	}
	return n
}

func declared() {
	s := []int{1, 2, 3, 4, 5}[1:4]
	println(len(s), cap(s), s[0], s[2])
	t := []int{1, 2, 3, 4}[1:2:3]
	println(len(t), cap(t), t[0])
	u := []int{1, 2, 3, 4, 5}[1:][1:][0]
	println(u, len([]string{"a", "b", "c"}[1:]), cap([]int{1, 2, 3, 4}[1:2]))
	w := []string{"ab", "cde"}[1][1:]
	println(w, []P{{1, 2}, {3, 4}, {5, 6}}[1:][1].y)
	r := [][2]int{{1, 2}, {3, 4}}[1][:]
	println(len(r), r[0], r[1])
}

func main() {
	println(ranged())
	declared()
	println(sum([]int{f(1), f(2), f(3)}[f(1):f(3)]), calls)
	a := append([]int{1, 2, 3}[:1], 9)
	println(len(a), cap(a), a[0], a[1])
	if len([]int{1, 2, 3}[1:]) == 2 {
		println("two")
	}
	switch []int{4, 5, 6}[1:][1] {
	case 6:
		println("six")
	}
}
`,
		want: "40\n3 4 2 4\n1 2 2\n3 2 3\nde 6\n2 3 4\n5 12313\n2 3 1 9\ntwo\nsix\n",
	}, {
		// A string LITERAL indexed and sliced where it stands, `"0123456789abcdef"[n&15]`,
		// which is how a digit is looked up. The grammar gave a string literal no suffix,
		// so every one of these was a syntax error; a named constant has always indexed.
		// The first step is emitted as a string constant's is, the literal standing where
		// a variable's bytes and length would, and what follows a slice is walked from its
		// header.
		name: "a string literal indexed and sliced",
		src: `var g = "hello"[1:3]
var gb = "abc"[1]
var tab = [2]byte{"xy"[0], "xy"[1]}
var buf [8]byte
var calls int

func bump(k int) int {
	calls = calls*10 + k
	return k
}

func hex(n int) {
	for i := 7; i >= 0; i-- {
		buf[i] = "0123456789abcdef"[n&15]
		n >>= 4
	}
}

func show(s string) int {
	println(s)
	return len(s)
}

func main() {
	println(g, gb, tab[0], tab[1])
	hex(0xbeef)
	for _, b := range buf {
		print(b, " ")
	}
	println()
	n := show("hello"[bump(1):bump(3)]) + show("hello"[1:][1:])
	n += int("hello"[1:][0]) + int("abc"[bump(2)]) + len("hello"[2:])
	if "abc"[0] == 'a' {
		n++
	}
	switch "abc"[1] {
	case 'b':
		n += 10
	}
	for i, r := range "héllo"[1:] {
		n += i + int(r)
	}
	var a [4]int
	a["abc"[1]-'a'] = 7
	println(n, calls, a[1], "yz"[1:] == "z", "abc"[1:] < "bd", ` + "`" + `raw\n` + "`" + `[3])
}
`,
		want: "el 98 120 121\n48 48 48 48 98 101 101 102 \nel\nllo\n788 132 7 true true 92\n",
	}, {
		// The integer edges Go defines and C leaves undefined -- signed overflow, the
		// most negative value divided by -1, a shift by the width or more, narrow
		// arithmetic, conversions -- measured against Go on a P2-EDGE on 2026-09-18 and
		// matching on every line, with the operands read out of tables through a loop
		// index and, in a second program, received from another cog. The host build passes
		// -fwrapv, so only the target's compiler can disagree here: this is the battery a
		// backend regeneration is checked against. A signed compare decided by an
		// overflowing difference was a real fault of it once (doc/signed-compare-overflow.c).
		name: "integer edges: overflow, division, shifts and conversions",
		src: `// Integer edges: what Go defines and C leaves undefined. Every operand comes out of
// a table through a loop index, so nothing here is a constant to the C compiler.
var vals = [6]int{2147483647, -2147483648, 1, -1, 2, 0}
var vals64 = [6]int64{9223372036854775807, -9223372036854775808, 1, -1, 2, 0}
var uvals = [4]uint32{4294967295, 0, 1, 2147483648}
var uvals64 = [3]uint64{18446744073709551615, 0, 1}
var small = [4]int8{127, -128, 1, -1}
var usmall = [3]uint8{255, 0, 1}
var counts = [7]uint{0, 1, 31, 32, 33, 63, 64}

func wrap32() {
	for i := 0; i < 1; i++ {
		max, min, one, neg, two := vals[i], vals[i+1], vals[i+2], vals[i+3], vals[i+4]
		println("wrap32", max+one, min-one, max*two, min*neg, -min, max+max, min+min)
		println("cmp32", max+one > max, min-one < min, max*two > max, -min < 0, max+1 > max)
		println("div32", min/neg, min%neg, neg/two, neg%two, min/two, min%two, -7/2, -7%2)
		x := max
		x++
		y := min
		y--
		z := max
		z += one
		w := min
		w *= two
		println("inc32", x, y, z, w)
		println("sub32", min-one > 0, min-one, max-neg, max-neg < 0)
		println("mul32", max*max, min*min, max*min, (max+one)-one == max, max*two/two)
	}
}

func wrap64() {
	for i := 0; i < 1; i++ {
		max, min, one, neg, two := vals64[i], vals64[i+1], vals64[i+2], vals64[i+3], vals64[i+4]
		println("wrap64", max+one, min-one, max*two, min*neg, -min)
		println("cmp64", max+one > max, min-one < min, -min < 0, max+1 > max)
		println("div64", min/neg, min%neg, neg/two, neg%two, min/two)
		println("mul64", max*max, min*min, max*min, (max+one)-one == max)
	}
}

func unsigned() {
	for i := 0; i < 1; i++ {
		max, zero, one, half := uvals[i], uvals[i+1], uvals[i+2], uvals[i+3]
		println("u32", max+one, zero-one, max*max, half*2, half+half, -one, -max)
		println("ucmp", max+one > max, zero-one > zero, half*2 < half, int32(half), int32(max), int(half))
		m64, z64, o64 := uvals64[i], uvals64[i+1], uvals64[i+2]
		println("u64", m64+o64, z64-o64, m64*m64, -o64, int64(m64), uint32(m64), uint16(m64), uint8(m64))
	}
}

func shifts() {
	for i := 0; i < 1; i++ {
		max, min, one := vals[i], vals[i+1], vals[i+2]
		umax := uvals[i]
		for _, c := range counts {
			println("shl", c, one<<c, max<<c, min<<c, umax<<c, int64(one)<<c, uint64(umax)<<c)
			println("shr", c, max>>c, min>>c, umax>>c, int64(min)>>c, uint64(umax)>>c, (-7)>>c)
		}
		var s8 int8 = small[i]
		var u8 uint8 = usmall[i]
		println("shl8", s8<<1, s8<<7, s8<<8, u8<<1, u8<<8, s8>>1, u8>>1, int8(-128)>>7)
	}
}

func narrow() {
	for i := 0; i < 1; i++ {
		a, b, one, neg := small[i], small[i+1], small[i+2], small[i+3]
		println("i8", a+one, b-one, a*a, b*neg, -b, a+b, a*2, ^a, ^b, b/neg, b%neg)
		println("i8cmp", a+one < a, b-one > b, -b < 0, a+one > 0)
		var c int8 = a
		c++
		var d int8 = b
		d--
		var e int8 = b
		e = -e
		println("i8inc", c, d, e)
		x, z, o := usmall[i], usmall[i+1], usmall[i+2]
		println("u8", x+o, z-o, x*x, -o, x+x, ^z, x/o, x%o)
		var s16 int16 = 32767
		var u16 uint16 = 65535
		s16 += int16(one)
		u16 += uint16(one)
		println("16", s16, u16, int16(a)*int16(a)*4, uint16(x)*uint16(x))
	}
}

func conversions() {
	for i := 0; i < 1; i++ {
		max, min, neg := vals[i], vals[i+1], vals[i+3]
		m64, mn64 := vals64[i], vals64[i+1]
		umax, half := uvals[i], uvals[i+3]
		println("conv", int32(m64), int32(mn64), int8(max), int8(min), uint8(neg), uint32(neg), uint64(neg), int16(m64))
		println("conv2", int(umax), int32(half), int64(umax), uint16(umax), int8(umax), uint32(m64), uint32(mn64))
		println("conv3", uint(neg), uint64(mn64), int8(half), int64(int32(umax)), uint32(int8(neg)))
	}
}

func main() {
	wrap32()
	wrap64()
	unsigned()
	shifts()
	narrow()
	conversions()
}
`,
		want: "wrap32 -2147483648 2147483647 -2 -2147483648 -2147483648 -2 0\ncmp32 false false false true false\ndiv32 -2147483648 0 0 -1 -1073741824 0 -3 -1\ninc32 -2147483648 2147483647 -2147483648 0\nsub32 true 2147483647 -2147483648 true\nmul32 1 0 -2147483648 true -1\nwrap64 -9223372036854775808 9223372036854775807 -2 -9223372036854775808 -9223372036854775808\ncmp64 false false true false\ndiv64 -9223372036854775808 0 0 -1 -4611686018427387904\nmul64 1 0 -9223372036854775808 true\nu32 0 4294967295 1 0 0 4294967295 1\nucmp false true true -2147483648 -1 -2147483648\nu64 0 18446744073709551615 1 18446744073709551615 -1 4294967295 65535 255\nshl 0 1 2147483647 -2147483648 4294967295 1 4294967295\nshr 0 2147483647 -2147483648 4294967295 -2147483648 4294967295 -7\nshl 1 2 -2 0 4294967294 2 8589934590\nshr 1 1073741823 -1073741824 2147483647 -1073741824 2147483647 -4\nshl 31 -2147483648 -2147483648 0 2147483648 2147483648 9223372034707292160\nshr 31 0 -1 1 -1 1 -1\nshl 32 0 0 0 0 4294967296 18446744069414584320\nshr 32 0 -1 0 -1 0 -1\nshl 33 0 0 0 0 8589934592 18446744065119617024\nshr 33 0 -1 0 -1 0 -1\nshl 63 0 0 0 0 -9223372036854775808 9223372036854775808\nshr 63 0 -1 0 -1 0 -1\nshl 64 0 0 0 0 0 0\nshr 64 0 -1 0 -1 0 -1\nshl8 -2 -128 0 254 0 63 127 -1\ni8 -128 127 1 -128 -128 -1 -2 -128 127 -128 0\ni8cmp true true true false\ni8inc -128 127 -128\nu8 0 255 1 255 254 255 255 0\n16 -32768 0 -1020 65025\nconv -1 0 -1 0 255 4294967295 18446744073709551615 -1\nconv2 -1 -2147483648 4294967295 65535 -1 4294967295 0\nconv3 4294967295 9223372036854775808 0 -1 4294967295\n",
	}, {
		// The floating point edges in float32, which every float is on this target:
		// rounding per operation, the special values and their comparisons, conversions
		// each way and how each prints, measured against Go on a P2-EDGE on 2026-09-18.
		// One line was wrong there: `float32(math.Sqrt(2))` printed 1, the integer square
		// root -- the untyped constant went to the target's sqrt builtin as an int, which
		// the host's libm converts and the builtin computes on (doc/sqrt-of-an-int.c).
		// Every argument of a math intrinsic is converted to its parameter's type now.
		name: "float edges: rounding, the special values, conversions and printing",
		src: `import "math"

// Floating point edges in float32, which is what every float is on this target:
// rounding per operation, the special values, conversions each way and how they
// print. Every operand comes out of a table through a loop index, so nothing is a
// constant to the C compiler.
var f32 = [8]float32{0.1, 0.2, 0.3, 1e38, 1e-45, 3, -2.5, 0}
var ints = [6]int{16777217, -16777217, 2147483647, -2147483648, 7, -7}
var ints64 = [3]int64{9007199254740993, -9007199254740993, 1 << 62}

func rounding() {
	for i := 0; i < 1; i++ {
		a, b, c := f32[i], f32[i+1], f32[i+2]
		println("r32", a+b, a+b == c, a*b, a/b, a-b, (a+b)*c, a+b+c, c-a-b, a*b*c/a)
		var acc float32
		for k := 0; k < 10; k++ {
			acc += a
		}
		println("acc", acc, acc == 1, acc-1, acc*10, acc/3)
		d := c
		d++
		d *= a
		d -= b
		d /= c
		println("compound", d, -d, d > 0, d == d)
	}
}

func specials() {
	for i := 0; i < 1; i++ {
		big, tiny, three, neg, zero := f32[i+3], f32[i+4], f32[i+5], f32[i+6], f32[i+7]
		inf := big * big
		nan := zero / zero
		println("inf", inf, -inf, big*10, tiny/big, tiny/2, -zero, zero/three, three/zero, neg/zero, big+big, -big-big)
		println("nan", nan, nan == nan, nan != nan, nan < three, nan > three, nan >= nan, three == three, inf == inf, inf > big, -inf < -big)
		println("ord", zero == -zero, zero < -zero, -zero < zero, neg < zero, big < inf, inf-inf, inf*zero, inf+inf, inf/inf, inf*neg)
		println("cmp", three > neg, neg < three, three >= three, neg <= neg, three != neg, big*2 > big, tiny > zero, tiny/4 > zero)
		m := three
		if nan < m {
			m = nan
		}
		println("minmax", m, math.Abs(float64(neg)), math.Abs(float64(-inf)), float32(math.Abs(float64(nan))) != nan)
	}
}

func conversions() {
	for i := 0; i < 1; i++ {
		a, b, c, d, e, f := ints[i], ints[i+1], ints[i+2], ints[i+3], ints[i+4], ints[i+5]
		println("i2f", float32(a), float32(b), float32(c), float32(d), float32(e), float32(f), float32(a)-float32(a-1), float32(c)+1 == float32(c))
		println("i2f2", float32(e)/2, float32(f)/2, float32(e)/float32(f), float32(e)*float32(f), float32(e)/3)
		g, h, k := ints64[i], ints64[i+1], ints64[i+2]
		println("i64f", float32(g), float32(h), float32(k), int64(float32(k)) == k, int64(float32(g)) == g, float32(k)/float32(g))
		neg, three, tiny := f32[i+6], f32[i+5], f32[i+4]
		println("f2i", int(neg), int(three), int32(neg*2), int64(neg*3), int(three/2), int(-three/2), uint32(three), uint8(three), int8(neg), int(tiny), int(neg*1e6))
		println("f2i2", int64(three*1e9), int32(three*1e6), int(neg/3), int(-three/3), uint32(three*1e9), uint64(three*1e18), int64(float32(1e18)))
		x := float64(neg)
		println("trunc", math.Trunc(x), math.Floor(x), math.Ceil(x), math.Round(x), math.Trunc(-x/2), math.Floor(-x/2), math.Ceil(-x/2), math.Round(-x/2), math.Round(x/5), math.Round(-x/5))
	}
}

func printing() {
	for i := 0; i < 1; i++ {
		println("p32", f32[i], f32[i+1], f32[i+2], f32[i+3], f32[i+4], f32[i+5], f32[i+6], f32[i+7])
		var one, two, half, hund, small, large float32 = 1, 2.5, -0.5, 100, 1e-7, 123456789
		println("pmix", one, two, -half*0, hund, small, large, one/3, one/7, two*two*two*two, hund*hund*hund*hund*hund)
		println("pmath", float32(math.Sqrt(2)), float32(math.Pi), float32(math.Pow(2, 10)), float32(math.Mod(-7, 3)), float32(math.Sqrt(float64(f32[i+5]))), float32(math.Pow(float64(f32[i+5]), 0.5)))
		var v float32 = 1.5
		v = v * 1e10
		println("big", v, v*1e10, v*1e10*1e10, v*1e10*1e10*1e10, v/1e30, v/1e30/1e30)
	}
}

func main() {
	rounding()
	specials()
	conversions()
	printing()
}
`,
		want: "r32 0.3 true 0.020000001 0.5 -0.1 0.09 0.6 1.4901161e-08 0.060000006\nacc 1.0000001 false 1.1920929e-07 10.000001 0.33333337\ncompound -0.23333335 0.23333335 false true\ninf +Inf -Inf +Inf 0 0 -0 0 +Inf -Inf 2e+38 -2e+38\nnan NaN false true false false false true true true true\nord true false false true true NaN NaN +Inf NaN -Inf\ncmp true true true true true true true false\nminmax 3 2.5 +Inf true\ni2f 1.6777216e+07 -1.6777216e+07 2.1474836e+09 -2.1474836e+09 7 -7 0 true\ni2f2 3.5 -3.5 -1 -49 2.3333333\ni64f 9.007199e+15 -9.007199e+15 4.611686e+18 true false 512\nf2i -2 3 -5 -7 1 -1 3 3 -2 0 -2500000\nf2i2 3000000000 3000000 0 -1 3000000000 2999999884200771584 999999984306749440\ntrunc -2 -3 -2 -3 1 1 2 1 -1 1\np32 0.1 0.2 0.3 1e+38 1e-45 3 -2.5 0\npmix 1 2.5 0 100 1e-07 1.2345679e+08 0.33333334 0.14285715 39.0625 1e+10\npmath 1.4142135 3.1415927 1024 -1 1.7320508 1.7320508\nbig 1.5e+10 1.5e+20 1.5000001e+30 +Inf 1.5e-20 0\n",
	}, {
		// A range over an ARRAY iterates a copy of it, taken once before the loop: Go
		// evaluates the range expression once, and an array's value is a copy. The loop
		// read the live array, so `for i, v := range arr { arr[i+1] = 99 }` saw its own
		// writes, 1 99 99 where Go reads 1 2 3, and so did a loop assigning the whole
		// array, one writing through a slice of it, one calling a method that writes it,
		// and one over an array of structs. The copy is made where the body can write the
		// array (rangeBodyMayWrite); a pointer operand is read live, as in Go, and a slice
		// is live with its length taken once. Beside them: the index-only form over a
		// literal holding a call, which is evaluated once, and a deferred call writing a
		// named result after the return has set it.
		name: "a range over an array iterates a copy",
		src: `type H struct {
	xs [3]int
	n  int
}

type P struct {
	x int
}

var garr = [3]int{1, 2, 3}
var gh = H{xs: [3]int{1, 2, 3}}
var pool = [2][3]int{{1, 2, 3}, {4, 5, 6}}
var ps = [2]P{{1}, {2}}
var calls int

func clobber() {
	garr[1] = 77
	pool[1][1] = 77
}

func (h *H) set(v int) {
	h.xs[1] = v
}

func inc(p *int) {
	*p++
}

func named() (r int) {
	defer inc(&r)
	return 1
}

func bump() int {
	calls++
	return calls
}

func viaArr(a [3]int) int {
	t := 0
	for i, v := range a {
		if i == 0 {
			a[1] = 50
		}
		t += v
	}
	return t + a[1]
}

func viaPtr(p *[3]int) int {
	t := 0
	for i, v := range p {
		if i == 0 {
			p[1] = 50
		}
		t += v
	}
	return t
}

func main() {
	// A range over an ARRAY iterates a copy taken once: the body's writes are not seen.
	arr := [3]int{1, 2, 3}
	for i, v := range arr {
		if i < 2 {
			arr[i+1] = 99
		}
		print(v, " ")
	}
	println("|", arr[1], arr[2])
	arr = [3]int{1, 2, 3}
	for i, v := range arr {
		arr = [3]int{7, 7, 7}
		print(i, v, " ")
	}
	println("|", arr[0])
	arr = [3]int{1, 2, 3}
	s := arr[:]
	for i, v := range arr {
		if i == 0 {
			s[1] += 9
			arr[2]++
		}
		print(v, " ")
	}
	println("|", arr[1], arr[2])
	// Over a SLICE the elements are live, and the length is taken once.
	for i, v := range s {
		if i < 2 {
			s[i+1] = 50 + i
		}
		print(v, " ")
	}
	println("|", s[1], s[2])
	// A field, a row, a package array, and what a call may write.
	h := H{xs: [3]int{1, 2, 3}}
	t := 0
	for i, v := range h.xs {
		if i == 0 {
			h.set(9)
		}
		t += v
	}
	for i, v := range pool[1] {
		if i == 0 {
			clobber()
		}
		t += v
	}
	for i, v := range gh.xs {
		if i == 0 {
			gh.set(9)
		}
		t += v
	}
	println(t, h.xs[1], pool[1][1], gh.xs[1])
	// Through a POINTER the array is read live, as Go reads it; through its
	// dereference it is a value again.
	garr = [3]int{1, 2, 3}
	p := &garr
	for i, v := range p {
		if i == 0 {
			garr[1] = 9
		}
		print(v, " ")
	}
	garr = [3]int{1, 2, 3}
	for i, v := range *p {
		if i == 0 {
			garr[1] = 9
		}
		print(v, " ")
	}
	println("|", viaArr(arr), viaPtr(&garr))
	// An array of structs: a copy too.
	for i, q := range ps {
		ps[1-i].x = 40
		print(q.x, " ")
	}
	println("|", ps[0].x, ps[1].x)
	// The index-only form sees no element; a literal operand holding a call is
	// still evaluated, once; a deferred call writes a named result after the return.
	n := 0
	for i := range [3]int{bump(), bump(), bump()} {
		arr[i] = 0
		n += i
	}
	for range []int{bump(), bump()} {
		n++
	}
	println(n, calls, named())
}
`,
		want: "1 2 3 | 99 99\n01 12 23 | 7\n1 2 3 | 11 4\n1 50 51 | 50 51\n27 9 77 9\n1 9 3 1 2 3 | 152 54\n1 2 | 40 40\n5 5 2\n",
	}, {
		// One Go level of binary operators associates to the left as one, and C binds
		// some of them at different strengths: | below ^ below + and - in the additive
		// level, & below the shifts below * / % in the multiplicative one. Written out in a
		// row, `a | b ^ c` was C's `a | (b ^ c)`, 7 for Go's 5, and `a & b << 2` was
		// `a & (b << 2)`, 4 for 8 -- twelve of thirty-one such expressions swept were
		// wrong, silently. A level that mixes them is written left-nested now
		// (cPrecMixed). And `a &^ b << d` was not C at all: the shift chain wrote the
		// Go operator verbatim.
		name: "operators of one Go level that C binds differently",
		src: `// One Go level of operators associates to the left as one; C binds | below ^ below
// + and -, and & below the shifts below * / %. Every operand comes out of a table,
// so nothing folds.
var vals = [6]int{6, 3, 2, 1, 12, 5}
var uvals = [3]uint32{0xF0F0, 0x0FF0, 3}

func main() {
	for i := 0; i < 1; i++ {
		a, b, c, d, e := vals[i], vals[i+1], vals[i+2], vals[i+3], vals[i+4]
		println("mul", a&b<<2, a<<2&b, a&b<<1>>1, a*b&e, a&e*b, a/b&e, a&e/b, a%b&e, a&e%b, e>>1&b, e&b>>1, a&b*c<<1)
		println("add", a|b^c, a^b|c, a|b+c, a+b|c, a^b+c, a+b^c, a|b-c, a-b|c, a^b-c, a-b^c, a|b^c|a, a^b|c^a, a+b|c^e-a)
		println("andnot", a&^b+c, a+b&^c, a&^b|c, a|b&^c, a&^b<<d, a<<d&^b, a&^b&c, a&b&^c, a^b&^c, a&^b<<c>>d, e>>d&^b<<c)
		println("shift", a+b<<c, a<<b+c, a-b>>d, a*b<<c, a<<c*b, a>>d+d, a<<b>>d, -a<<c, a<<c-b, a<<c&e, a&e<<c)
		println("cmp", a&c == c, a|b == a, a^b == 5, a&c != 0, b<<c == a*2, a|b^c == 5, a&b<<1 == 4)
		println("unary", ^a&b, ^(a & b), -a&b, -(a & b), ^a+b, ^(a + b), -a<<c, ^a<<c, ^(a << c), ^a|b^c)
		x, y, z := uvals[i], uvals[i+1], uvals[i+2]
		println("uns", x&y|z, x|y&z, x^y&z, x&^y|z, x>>z+z, x+y>>z, x<<z-y, x-y<<z, x&y<<z, x<<z&y, ^x>>z, x>>z<<z, x|y^z, x^y|z, x|3^1, x&3<<1)
		println("cmp2", x&y == y, x|y > x, x^y != x, x>>z == x/8, x<<z == x*8, x|y^z == 0xFFF0)
		g := a
		g += b << c
		g &= a + b
		g |= c << d
		g ^= a & b
		g <<= d + d
		g &^= b | c
		g %= a + b
		println("compound", g)
	}
}
`,
		want: "mul 8 0 2 0 12 0 1 0 1 2 0 8\nadd 5 7 9 11 7 11 5 3 3 1 7 1 1\nandnot 6 7 6 7 8 12 0 0 7 8 16\nshift 18 50 5 72 72 4 24 -24 21 8 16\ncmp true false true true true true true\nunary 1 -3 2 -2 -4 -10 -24 -28 -25 -7\nuns 243 61680 61680 61443 7713 62190 489360 29040 1920 1920 536863201 61680 65523 65283 61682 0\ncmp2 false true true true true false\ncompound 6\n",
	}, {
		// The control-flow statements whose C lowering is easy to get wrong, measured
		// against Go on the host and a P2-EDGE on 2026-09-18: break inside a switch or
		// a select inside a loop leaves the switch; continue there runs the loop's post;
		// fallthrough runs the next clause whatever its case; labeled break and continue
		// across a switch and an inner loop; goto forward and backward; several values
		// per case, evaluated in order until one matches; a switch init beside a loop's
		// post. And a mixed chain of && and || in an argument, which the host compiler
		// warned about until it was grouped as a condition's is.
		name: "control flow: break, continue, fallthrough, labels and goto",
		src: `var trace int

func t(k int) {
	trace = trace*10 + k
}

func main() {
	// break inside a switch inside a loop leaves the switch, not the loop.
	for i := 0; i < 4; i++ {
		switch i {
		case 1:
			break
		case 2:
			t(2)
		}
		t(i)
	}
	println(trace)
	trace = 0
	// continue inside a switch continues the loop, running its post statement.
	for i := 0; i < 4; i++ {
		switch {
		case i%2 == 0:
			continue
		}
		t(i)
	}
	println(trace)
	trace = 0
	// fallthrough runs the next clause's body, whatever its case; break inside a
	// clause ends the switch.
	for i := 0; i < 4; i++ {
		switch i {
		case 0:
			t(0)
			fallthrough
		case 1:
			t(1)
			if i == 1 {
				break
			}
			t(9)
		case 2:
			t(2)
			fallthrough
		default:
			t(7)
		}
	}
	println(trace)
	trace = 0
	// Labeled break and continue across a switch and an inner loop.
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			switch {
			case j == 1:
				continue outer
			case i == 2:
				break outer
			}
			t(i*3 + j)
		}
		t(8)
	}
	println(trace)
	trace = 0
	// goto forward and backward, and a label on a block.
	i := 0
again:
	i++
	if i < 3 {
		goto again
	}
	t(i)
	if i == 3 {
		goto done
	}
	t(5)
done:
	t(6)
	println(trace)
	trace = 0
	// A switch with no condition and several values per case; a case expression
	// with a call is evaluated in order and only until one matches.
	for i := 0; i < 5; i++ {
		switch i {
		case 0, 1:
			t(1)
		case pick(2), pick(3):
			t(2)
		default:
			t(0)
		}
	}
	println(trace, calls)
	trace = 0
	// break from a select's default inside a loop leaves the select.
	for i := 0; i < 2; i++ {
		select {
		default:
			if i == 0 {
				break
			}
			t(i)
		}
		t(4)
	}
	println(trace)
	trace = 0
	// A switch's init and a loop's post together.
	for i := 0; i < 3; i++ {
		switch k := i * 2; {
		case k > 2:
			continue
		case k == 2:
			t(k)
			fallthrough
		default:
			t(3)
		}
		t(i)
	}
	println(trace)
	// A mixed chain of && and || in an argument, grouped as in a condition.
	a, b, c := trace > 0, calls > 3, trace < 0
	println(a && b || c, a || b && c, !a && b || !c, a || c && a || b)
}

var calls int

func pick(k int) int {
	calls++
	return k
}
`,
		want: "1223\n13\n191277\n3\n36\n11220 5\n414\n30231\ntrue true true true\n",
	}, {
		// String semantics measured against Go on a P2-EDGE on 2026-09-18: length and
		// indexing in bytes, comparison by content, slicing across a rune, a range over
		// a multibyte string and over one holding bytes that are not UTF-8 -- U+FFFD,
		// one byte at a time -- the conversions of a rune, a byte and out-of-range
		// values to a string, and the copies a string variable makes. All matched. The
		// program prints bytes that are not UTF-8, which is what found the board scripts
		// dropping such lines: GNU grep suppresses them under a UTF-8 locale.
		name: "string semantics: bytes, runes, comparison, slicing and conversions",
		src: `var strs = [6]string{"héllo", "abc", "abd", "", "ab", "\xff\xfeA"}
var runes = [6]rune{'a', 'é', 0x10FFFF, 0x110000, -1, 0xD800}
var bytes = [3]byte{65, 200, 255}

func lengths() {
	for i := 0; i < 1; i++ {
		h, abc, abd, empty, ab, bad := strs[i], strs[i+1], strs[i+2], strs[i+3], strs[i+4], strs[i+5]
		println("len", len(h), len(abc), len(empty), len(bad), h[1], h[2], bad[0], bad[2])
		println("cmp", abc < abd, abc == abd, abc > empty, ab < abc, abc <= abc, abd >= abc, ab != abc, empty == "", h > abc)
		println("slice", h[1:3], h[:2], h[3:], abc[1:2], abc[3:], empty[:], h[1:3] == "\xc3\xa9", len(h[1:]))
	}
}

func ranges() {
	for i := 0; i < 1; i++ {
		h, abc, bad := strs[i], strs[i+1], strs[i+5]
		for i, r := range h {
			print(i, ":", r, " ")
		}
		println()
		for i, r := range bad {
			print(i, ":", r, " ")
		}
		println()
		for i := range abc {
			print(i, ":", abc[i], " ")
		}
		println()
		n := 0
		for range h {
			n++
		}
		println("count", n, len(h))
	}
}

func conversions() {
	for i := 0; i < 1; i++ {
		a, e, max, over, neg, sur := runes[i], runes[i+1], runes[i+2], runes[i+3], runes[i+4], runes[i+5]
		println("rune", string(a), string(e), len(string(e)), len(string(max)), string(over), string(neg), string(sur), len(string(over)))
		b1, b2, b3 := bytes[i], bytes[i+1], bytes[i+2]
		println("byte", string(b1), len(string(b2)), string(rune(b2)), len(string(rune(b3))), string(b1) == "A")
		h, abc := strs[i], strs[i+1]
		println("conv", string(rune(a)+1), string(rune(65+i)), rune(abc[1]), int(h[1]), uint8(h[1]), string(abc[1]), string(rune(abc[1])))
	}
}

func values() {
	for i := 0; i < 1; i++ {
		h, abc, abd := strs[i], strs[i+1], strs[i+2]
		println("concat", "x"+"y"+"z", "a"+"" == "a", ""+"" == "", len("x"+"héllo"))
		println("idx", abc[0], abc[len(abc)-1], h[len(h)-1], "z"[0], abc[1] == 'b', h[0] < h[1])
		s := abc
		s = s[1:]
		t := s
		s = abd
		println("assign", s, t, len(s), len(t), s == abd, t == "bc")
	}
}

func main() {
	lengths()
	ranges()
	conversions()
	values()
}
`,
		want: "len 6 3 0 3 195 169 255 65\ncmp true false true true true true true true true\nslice é h\xc3 llo b   true 5\n0:104 1:233 3:108 4:108 5:111 \n0:65533 1:65533 2:65 \n0:97 1:98 2:99 \ncount 5 6\nrune a é 2 4 � � � 3\nbyte A 2 È 2 true\nconv b A 98 195 195 b b\nconcat xyz true true 7\nidx 97 99 111 122 true true\nassign abd bc 3 2 true true\n",
	}, {
		// Channel semantics measured against Go on a P2-EDGE on 2026-09-18, three runs:
		// a range over a channel ends when the producer closes it; a receive from a
		// closed channel yields the zero value at once, with ok false, for an int, a
		// struct and a string alike; a select with a default arm takes it when nobody is
		// sending or receiving, for a receive and a send. All matched.
		name: "channel semantics: close, the comma-ok receive, range and a default arm",
		src: `type Msg struct {
	id  int
	val int
	tag string
}

var ch chan int
var done chan int
var msgs chan Msg
var strs chan string
var idle chan int

func produce(n int) {
	for i := 1; i <= n; i++ {
		ch <- i * i
	}
	close(ch)
	for i := 0; i < 2; i++ {
		msgs <- Msg{i, i * 10, "m"}
	}
	close(msgs)
	strs <- "héllo"
	strs <- ""
	close(strs)
	done <- 1
}

func main() {
	go produce(4)
	sum := 0
	for v := range ch {
		sum += v
	}
	v, ok := <-ch
	w := <-ch
	println("range", sum, v, ok, w)
	for m := range msgs {
		println("msg", m.id, m.val, m.tag)
	}
	m, ok2 := <-msgs
	println("closed", m.id, m.val, len(m.tag), ok2)
	a, ok3 := <-strs
	b := <-strs
	c, ok4 := <-strs
	println("strs", a, len(a), ok3, len(b), b == "", len(c), ok4)
	// select with a default arm when nobody is sending or receiving.
	polls := 0
	for i := 0; i < 3; i++ {
		select {
		case x := <-idle:
			println("got", x)
		default:
			polls++
		}
	}
	select {
	case idle <- 1:
		println("sent")
	default:
		polls += 10
	}
	println("polls", polls, <-done)
}
`,
		want: "range 30 0 false 0\nmsg 0 0 m\nmsg 1 10 m\nclosed 0 0 0 false\nstrs héllo 6 true 0 true 0 false\npolls 13 1\n",
	}, {
		// Value semantics measured against Go on the host and on a P2-EDGE
		// (2026-09-18): an array, a struct and a struct holding both copy on
		// assignment, through a pointer, into a parameter, out of a result, into and
		// out of an element and a field, and to a range's value; a slice aliases;
		// the swaps; the two-phase multiple assignment, whose index operands are
		// fixed before any store (C5, C6 -- the second was the fault, see
		// fixTargetAddrs); and equality of arrays, of arrays of arrays and of structs.
		name: "value semantics: copies, aliases, swaps, the two phases of a multiple assignment and equality",
		src: `type P struct {
	x, y int
}

type Q struct {
	p P
	a [3]int
}

type M [2][2]int

var calls int

func idx(k int) int {
	calls = calls*10 + k
	return k
}

func val(k int) int {
	calls = calls*10 + k
	return k * 100
}

func setP(p P) int {
	p.x = 99
	return p.x
}

func setA(a [3]int) int {
	a[0] = 99
	return a[0]
}

func setQ(q *Q) {
	q.p.x = 7
	q.a[2] = 7
}

func mkP(k int) P {
	return P{k, k + 1}
}

func mkA(k int) [3]int {
	return [3]int{k, k + 1, k + 2}
}

func part1() {
	// array and struct assignment copies
	a := [3]int{1, 2, 3}
	b := a
	b[0] = 9
	println("A1", a[0], b[0])
	p := P{1, 2}
	q := p
	q.x = 9
	println("A2", p.x, q.x)
	// nested struct holding an array copies the array
	var u Q
	u.a = a
	u.p = p
	w := u
	w.a[1] = 9
	w.p.y = 9
	a[2] = 8
	println("A3", u.a[0], u.a[1], u.a[2], u.p.y, w.a[1], w.p.y)
	// array of arrays
	m := M{{1, 2}, {3, 4}}
	n := m
	n[0][0] = 9
	row := m[1]
	row[1] = 9
	println("A4", m[0][0], n[0][0], m[1][1], row[1])
	// through pointers
	pp := &p
	r := *pp
	r.y = 9
	println("A5", p.y, r.y)
	pa := &a
	c := *pa
	c[1] = 9
	println("A6", a[1], c[1])
	*pp = q
	println("A7", p.x, p.y)
	*pa = b
	println("A8", a[0], a[1], a[2])
}

func part2() {
	// a slice aliases, a copy does not
	a := [3]int{1, 2, 3}
	s := a[:]
	b := a
	s[0] = 9
	a = [3]int{7, 8, 9}
	println("B1", a[0], b[0], s[0], s[2])
	// callee copies
	p := P{1, 2}
	println("B2", setP(p), p.x)
	println("B3", setA(a), a[0])
	var u Q
	u.a = a
	setQ(&u)
	println("B4", u.p.x, u.a[2], a[2])
	// results are fresh values
	q := mkP(5)
	q.x = 1
	r := mkP(5)
	println("B5", q.x, r.x, mkP(3).y)
	c := mkA(1)
	c[0] = 9
	d := mkA(1)
	println("B6", c[0], d[0], mkA(4)[2])
	// elements and fields are copies
	arr := [2]P{{1, 1}, {2, 2}}
	e := arr[0]
	e.x = 9
	arr[1] = e
	e.y = 8
	println("B7", arr[0].x, arr[1].x, arr[1].y, e.y)
	// range value is a copy
	sum := 0
	for _, v := range arr {
		v.x = 0
		sum += v.x + v.y
	}
	println("B8", arr[0].x, arr[1].x, sum)
	for i := range arr {
		arr[i].x = 0
	}
	println("B9", arr[0].x, arr[1].x)
}

func part3() {
	// swaps
	a, b := [2]int{1, 2}, [2]int{3, 4}
	a, b = b, a
	println("C1", a[0], b[0])
	p, q := P{1, 2}, P{3, 4}
	p, q = q, p
	println("C2", p.x, q.x)
	a[0], a[1] = a[1], a[0]
	println("C3", a[0], a[1])
	p.x, p.y = p.y, p.x
	println("C4", p.x, p.y)
	// index operands are evaluated before any assignment, left to right
	arr := [4]int{0, 0, 0, 0}
	i := 0
	arr[i], i = 5, 1
	println("C5", arr[0], arr[1], i)
	i, arr[i] = 2, 6
	println("C6", arr[1], arr[2], i)
	calls = 0
	arr[idx(1)], arr[idx(2)] = val(3), val(4)
	println("C7", arr[1], arr[2], calls)
	calls = 0
	arr[idx(3)] += val(1)
	println("C8", arr[3], calls)
	// equality of arrays and structs
	c := [2]int{1, 2}
	d := c
	println("C9", a == b, c == d, c != d, p == q, p == P{4, 3})
	m := M{{1, 2}, {3, 4}}
	n := m
	n[1][1] = 0
	println("C10", m == n, m == M{{1, 2}, {3, 4}})
}

func main() {
	part1()
	part2()
	part3()
}
`,
		want: "A1 1 9\nA2 1 9\nA3 1 2 3 2 9 9\nA4 1 9 4 9\nA5 2 9\nA6 2 9\nA7 9 2\nA8 9 2 3\nB1 7 1 7 9\nB2 99 1\nB3 99 7\nB4 7 7 9\nB5 1 5 4\nB6 9 1 6\nB7 1 9 1 8\nB8 1 9 2\nB9 0 0\nC1 3 1\nC2 3 1\nC3 4 3\nC4 4 3\nC5 5 0 1\nC6 6 0 2\nC7 300 400 1234\nC8 100 31\nC9 false true false false true\nC10 false true\n",
	}, {
		// Every place a multiple assignment fixes ahead of its stores, measured
		// against Go (2026-09-18): a pointer and a field through it, a pointer and
		// its pointee, a slice and its element, a pointer to an array and its
		// element, a struct and a field through its pointer field, that field and
		// what it reaches, a struct and its slice field's element, the slice field
		// and its element, a struct and its array field's element (no fix: the
		// element lies inside the struct); three targets; a nested index; the
		// destructured call and the comma-ok receive; an array element's whole row;
		// the for clauses, init and post. Then the shapes that bind nothing: the
		// swaps over an array, a slice and a slice field, a struct and then its
		// field, a pointee and then a field through the pointer.
		name: "a multiple assignment fixes each target's place before it stores",
		src: `type P struct {
	x, y int
}

type Q struct {
	p *P
	s []int
	a [2]int
}

func two() (int, int) {
	return 2, 8
}

func part1() {
	q, r := P{1, 2}, P{3, 4}
	p := &q
	p, p.x = &r, 5
	println("D1", q.x, r.x, p.x)
	p = &q
	p, *p = &r, P{9, 9}
	println("D2", q.x, r.x)
	a, b := [3]int{1, 2, 3}, [3]int{4, 5, 6}
	s, t := a[:], b[:]
	s, s[0] = t, 9
	println("D3", a[0], b[0], s[0])
	pa := &a
	pa, pa[1] = &b, 8
	println("D4", a[1], b[1], pa[1])
	var u, w Q
	u.p, w.p = &q, &r
	u, u.p.x = w, 7
	println("D5", q.x, r.x, u.p.x)
	u.p = &q
	u.p, u.p.x = &r, 6
	println("D6", q.x, r.x)
	u.s, w.s = a[:], b[:]
	u, u.s[2] = w, 5
	println("D7", a[2], b[2])
	u.s = a[:]
	u.s, u.s[2] = b[:], 4
	println("D8", a[2], b[2])
	u.a = [2]int{1, 2}
	w.a = [2]int{3, 4}
	u, u.a[0] = w, 9
	println("D9", u.a[0], u.a[1])
}

func part2() {
	arr := [4]int{0, 0, 0, 0}
	m := [2][2]int{{0, 0}, {0, 0}}
	i := 1
	arr[i], i, arr[i] = 1, 2, 3
	println("E1", arr[1], arr[2], i)
	i = 0
	i, m[i][i] = 1, 9
	println("E2", m[0][0], m[0][1], m[1][1], i)
	i = 0
	i, arr[i] = two()
	println("E3", arr[0], arr[2], i)
	rows := [2][2]int{{1, 2}, {3, 4}}
	j := 0
	j, rows[j] = 1, [2]int{7, 7}
	println("E4", rows[0][0], rows[1][0], j)
	var ch chan int
	close(ch)
	ok := [3]bool{true, true, true}
	k := 1
	k, ok[k] = <-ch
	println("E5", ok[0], ok[1], ok[2], k)
	// for clauses
	n := 0
	for i, arr[i] = 0, 5; i < 2; i, arr[i] = i+1, 6 {
		n++
	}
	println("E8", arr[0], arr[1], arr[2], i, n)
}

func part3() {
	// the swaps that bind nothing: a store beside a target moves it not
	a := [3]int{1, 2, 3}
	a[0], a[2] = a[2], a[0]
	s := a[:]
	s[0], s[1] = s[1], s[0]
	var u Q
	u.s = s
	u.s[1], u.s[2] = u.s[2], u.s[1]
	println("F1", a[0], a[1], a[2])
	// a struct target and its field, in order: no place to fix
	q, r := P{1, 2}, P{3, 4}
	q, q.x = r, 5
	println("F2", q.x, q.y)
	p := &q
	*p, p.x = r, 9
	println("F3", q.x, q.y)
	ok := [3]bool{true, true, true}
	k := 2
	k, ok[k] = 0, false
	println("F4", ok[0], ok[1], ok[2], k)
}

func main() {
	part1()
	part2()
	part3()
}
`,
		want: "D1 5 3 3\nD2 9 3\nD3 9 4 4\nD4 8 5 5\nD5 7 3 3\nD6 6 3\nD7 5 6\nD8 4 6\nD9 9 4\nE1 3 0 2\nE2 9 0 0 1\nE3 8 0 2\nE4 7 3 1\nE5 true false true 0\nE8 6 6 5 2 2\nF1 2 1 3\nF2 5 4\nF3 9 4\nF4 true true false 0\n",
	}, {
		// The comma-ok receive's second target in a select may be a field, an
		// element or a field through a pointer, as the statement form's may; the
		// grammar took a bare name there until 2026-09-18. Matched against Go.
		name: "a select's comma-ok flag stored in a field, an element and through a pointer",
		src: `type R struct {
	val int
	ok  bool
}

var ch chan int
var done chan int

func produce() {
	ch <- 7
	close(ch)
	done <- 1
}

func main() {
	go produce()
	var r R
	flags := [3]bool{false, false, false}
	vals := [3]int{0, 0, 0}
	var pr *R = &r
	n := 0
	for n < 3 {
		select {
		case r.val, r.ok = <-ch:
			println("field", r.val, r.ok)
		}
		select {
		case vals[n], flags[n] = <-ch:
			println("element", vals[0], vals[1], vals[2], flags[0], flags[1], flags[2])
		}
		select {
		case pr.val, pr.ok = <-ch:
			println("through", r.val, r.ok)
		}
		n++
	}
	println("done", <-done)
}
`,
		want: "field 7 true\nelement 0 0 0 false false false\nthrough 0 false\nfield 0 false\nelement 0 0 0 false false false\nthrough 0 false\nfield 0 false\nelement 0 0 0 false false false\nthrough 0 false\ndone 1\n",
	}, {
		// Constant expressions computed in arbitrary precision, measured against Go
		// (2026-09-18): a constant beyond 64 bits (`1 << 100`) folded into values
		// that fit, one only a uint64 holds (`1<<64 - 1`), intermediates past 64
		// bits (`1 << 63 >> 62`, `(1 << 64) / 4`, `1 << 62 * 4 / 8`), and each of
		// them where a float is wanted: a declaration, an assignment, a conversion,
		// a field, a literal element, a package variable, a result, an argument and
		// a comparison. Until then `1 << 100` was a run-time shift of an int64 by
		// 100 in a `static const int`, which the host's compiler refused and the
		// target's computed as 0.
		name: "constants beyond 64 bits fold into what fits, and into floats",
		src: `const (
	huge = 1 << 100
	m    = 1 << 63 >> 62
	d    = (1 << 64) / 4
	w    = 1 << 62 * 4 / 8
	maxU = 1<<64 - 1
	big  = 9223372036854775807 + 1 - 1
	prod = 4294967296 * 4294967296 / 4294967296
	half = huge / 2 >> 99
	neg  = -1 << 100 >> 99
	c    = 1 << 200 >> 199
	rem  = (1<<70 + 5) % 1000
	mask = maxU >> 60
	sub  = 1<<64 - 1<<63
)

type S struct {
	f float32
}

var pf float32 = huge

func avog() float32 {
	return 602214076000000000000000
}

func take(f float32) float32 {
	return f / 2
}

func floats() {
	var f float32 = 1 << 100
	var g float32 = 1 << 40
	var h float64 = 3 << 62 >> 61
	var k float32 = huge
	k = huge / 2
	m := float32(huge)
	n := float64(huge >> 90)
	var s S
	s.f = huge
	arr := [2]float32{huge, 1 << 70}
	println("F1", f, g, h, k, m, n)
	println("F2", pf, avog(), take(huge), arr[0], arr[1], s.f, huge > 1<<99, k < huge)
}

func main() {
	floats()
	x := huge >> 98
	var u uint64 = maxU
	var b int64 = big
	var p int64 = prod
	var f float32 = 1 << 100
	var g float32 = huge
	var arr [1 << 40 >> 38]int
	var dd, ww int64 = d, w
	println("W1", x, m, dd, ww, u, b, p)
	arr[1] = 3
	println("W2", half, neg, c, rem, mask, len(arr), huge/(1<<98), arr[1])
	println("W3", f, g, f == g, uint64(sub))
	println("W4", huge > 1<<99, huge == 1<<100, maxU > 1<<63)
}
`,
		want: "F1 1.2676506e+30 1.0995116e+12 6 6.338253e+29 1.2676506e+30 1024\nF2 1.2676506e+30 6.0221406e+23 6.338253e+29 1.2676506e+30 1.1805916e+21 1.2676506e+30 true true\nW1 4 2 4611686018427387904 2305843009213693952 18446744073709551615 9223372036854775807 4294967296\nW2 1 -2 2 429 15 4 4 3\nW3 1.2676506e+30 1.2676506e+30 true 9223372036854775808\nW4 true true true\n",
	}, {
		// Constant semantics measured against Go on the host and a P2-EDGE
		// (2026-09-18): integer and float division of constants, truncation toward
		// zero, concatenation, comparison, rune constants, iota in its idioms, a
		// typed constant's method, an untyped constant taking the other operand's
		// type (`x / 3.0` for an int x is 3), wrapping of a sized variable holding a
		// constant, constant shifts and masks in a variable's context, and a
		// constant as an array bound and an index. All matched.
		name: "constant semantics: division, iota, typed and untyped constants, runes and bounds",
		src: `const (
	big  = 1 << 40
	huge = 1 << 100
	q    = 7 / 2
	f    = 7 / 2.0
	r    = 7.0 / 2
	neg  = -7 / 2
	rem  = -7 % 2
	z    = 1<<62>>60 + (1+2)*(3+4)%5
	s    = "a" + "b" + "héllo"
	t    = 3 > 2 && !false
	c    = 'x'
	big64 int64 = 1 << 40
	u32   uint32 = 1<<32 - 1
	max   = 1<<31 - 1
	ratio = 2.5
	n     = len("abc") + len(s)
)

const (
	a0 = iota * 10
	a1
	a2
)

const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
	GB
)

type Weekday int

const (
	Sunday Weekday = iota
	Monday
	Tuesday
)

func (d Weekday) Next() Weekday {
	return (d + 1) % 3
}

func part1() {
	x := big >> 30
	y := huge >> 98
	println("A1", x, y, q, neg, rem, z)
	println("A2", f, r, f == r, f*2)
	println("A3", s, len(s), s[1], t, c, c+1, n)
	var b byte = c
	var r32 rune = c + 1
	println("A4", b, r32, string(r32), string(rune(c)))
	v := u32
	w := max
	println("A5", big64>>39, u32, v+1, max, w+1 < 0)
	println("A6", a0, a1, a2, KB, MB, GB)
	println("A7", Sunday, Monday.Next(), Tuesday.Next(), Tuesday.Next() == Sunday)
}

func part2() {
	// untyped constants take the type of the other operand
	x := 10
	y := x / 3.0
	var f32 float32 = 10
	g := f32 * ratio
	h := ratio * 2
	var i8 int8 = 100
	j := i8 + 27
	var k uint8 = 255
	k++
	var l int32 = max
	l++
	println("B1", y, g, h, j, k, l)
	// constant shifts and masks in variable context
	m := x << 3 & 0xf0 | 1
	o := uint16(1)<<15 | 3
	var p uint64 = 1<<63 | 1
	println("B2", m, o, p, p>>60, -x>>1, uint32(-x)>>28)
	// a rune is an int32
	ch := 'a'
	ch += 2
	str := string(ch)
	println("B3", ch, string(ch), str, len(str), 'z'-'a', 'é')
	// a constant expression as an array bound and an index
	var arr [q * 2]int
	arr[q] = 5
	arr[len(arr)-1] = 6
	println("B4", len(arr), arr[3], arr[5], cap(arr[:q]))
}

func main() {
	part1()
	part2()
}
`,
		want: "A1 1024 4 3 -3 -1 5\nA2 3.5 3.5 true 7\nA3 abhéllo 8 98 true 120 121 11\nA4 120 121 y x\nA5 2 4294967295 0 2147483647 true\nA6 0 10 20 1024 1048576 1073741824\nA7 0 2 0 true\nB1 3 25 5 127 0 -2147483648\nB2 81 32771 9223372036854775809 8 -5 15\nB3 99 c c 1 25 233\nB4 6 5 6 6\n",
	}, {
		// Method and interface semantics measured against Go on the host and a
		// P2-EDGE (2026-09-18): a value receiver works on a copy and a pointer
		// receiver on the variable, through a pointer, an element, a field and an
		// embedded field; a method value binds a pointer receiver's address; a
		// method called through an interface sees the pointee's later changes; a
		// type switch, a comma-ok assertion writing through its pointer, interface
		// equality and nil. All matched.
		name: "method semantics: value and pointer receivers, promotion, method values and interfaces",
		src: `type Counter struct {
	n int
}

func (c Counter) Get() int {
	return c.n
}

func (c Counter) Bump() int {
	c.n++
	return c.n
}

func (c *Counter) Inc() {
	c.n++
}

func (c *Counter) Ptr() *Counter {
	return c
}

type Pair struct {
	Counter
	m int
}

type Wrap struct {
	c *Counter
}

type Shape interface {
	Area() int
}

type Sq struct {
	s int
}

func (q *Sq) Area() int {
	return q.s * q.s
}

type Rect struct {
	w, h int
}

func (r *Rect) Area() int {
	return r.w * r.h
}

var gc Counter

func part1() {
	// a value receiver works on a copy; a pointer receiver on the variable
	c := Counter{1}
	println("A1", c.Bump(), c.Bump(), c.n)
	c.Inc()
	c.Inc()
	println("A2", c.n, c.Get())
	p := &c
	println("A3", p.Bump(), p.Get(), c.n)
	p.Inc()
	println("A4", c.n, p.Ptr().n)
	// through an element and a field
	arr := [2]Counter{{5}, {6}}
	arr[0].Inc()
	println("A5", arr[0].n, arr[0].Bump(), arr[0].n, arr[1].Get())
	var w Wrap
	w.c = &c
	w.c.Inc()
	println("A6", c.n, w.c.Get())
	// promoted through embedding
	var pr Pair
	pr.n = 10
	pr.Inc()
	println("A7", pr.n, pr.Get(), pr.Bump(), pr.n, pr.Counter.n)
	pp := &pr
	pp.Inc()
	println("A8", pr.n, pp.Get())
}

func part2() {
	// a method value on a package variable: a value receiver is copied when the
	// value is made, a pointer receiver's address is taken then
	gc.n = 1
	inc := gc.Inc
	gc.n = 5
	println("B1", gc.n)
	inc()
	println("B2", gc.n, gc.Get())
	// a method expression-like call through the interface
	var s Shape
	q := Sq{3}
	s = &q
	println("B3", s.Area())
	q.s = 4
	println("B4", s.Area())
	r := Rect{2, 3}
	s = &r
	println("B5", s.Area())
	// a type switch and assertion
	shapes := [2]Shape{&q, &r}
	total := 0
	for i := 0; i < 2; i++ {
		switch v := shapes[i].(type) {
		case *Sq:
			total += v.s
		case *Rect:
			total += v.w
		}
	}
	println("B6", total)
	if rr, ok := shapes[1].(*Rect); ok {
		rr.h = 10
	}
	println("B7", r.h, shapes[1].Area())
	_, isSq := shapes[1].(*Sq)
	println("B8", isSq, shapes[0] == Shape(&q), shapes[0] == shapes[1])
	var none Shape
	println("B9", none == nil, s != nil)
}

func main() {
	part1()
	part2()
}
`,
		want: "A1 2 2 1\nA2 3 3\nA3 4 3 3\nA4 4 4\nA5 6 7 6 6\nA6 5 5\nA7 11 11 12 11 11\nA8 12 12\nB1 5\nB2 6 6\nB3 9\nB4 16\nB5 6\nB6 6\nB7 10 20\nB8 false true false\nB9 true true\n",
	}, {
		// Defer semantics measured against Go on the host and a P2-EDGE
		// (2026-09-18): deferred calls run last in, first out, after the body and
		// after an early return; their arguments and a value receiver are evaluated
		// where the defer is written, a pointer receiver's pointee is read when the
		// call runs; a defer in a block of the function runs at its return. All
		// matched.
		name: "defer semantics: order, the arguments at the defer, the receivers and an early return",
		src: `type Counter struct {
	n int
}

func (c Counter) Show(tag string) {
	println(tag, c.n)
}

func (c *Counter) ShowP(tag string) {
	println(tag, c.n)
}

var calls int

func mark(k int) int {
	calls = calls*10 + k
	return k
}

func show(tag string, v int) {
	println(tag, v)
}

func order() {
	defer show("first deferred", 1)
	defer show("second deferred", 2)
	if calls == 0 {
		defer show("in a block", 3)
	}
	println("order body")
}

func args() {
	x := 1
	defer show("x at defer", x)
	x = 2
	calls = 0
	defer show("marks", mark(1)+mark(2))
	mark(3)
	println("args body", x, calls)
}

func receivers() {
	c := Counter{1}
	defer c.Show("value receiver at defer")
	defer c.ShowP("pointer receiver at return")
	p := &c
	defer p.Show("value through pointer at defer")
	defer p.ShowP("pointer at return")
	c.n = 9
	println("receivers body", c.n)
}

func results() (r int) {
	defer show("deferred in results", 7)
	r = 5
	return r * 2
}

func early(n int) int {
	defer show("early", n)
	if n > 2 {
		return n
	}
	println("early body", n)
	return -n
}

func main() {
	order()
	args()
	receivers()
	println("results", results())
	println("early", early(1), early(3))
}
`,
		want: "order body\nin a block 3\nsecond deferred 2\nfirst deferred 1\nargs body 2 123\nmarks 3\nx at defer 1\nreceivers body 9\npointer at return 9\nvalue through pointer at defer 1\npointer receiver at return 9\nvalue receiver at defer 1\ndeferred in results 7\nresults 10\nearly body 1\nearly 1\nearly 3\nearly -1 3\n",
	}, {
		// Slice semantics measured against Go on the host and a P2-EDGE
		// (2026-09-18): a view's length and capacity, growth up to the capacity,
		// re-slicing within it and the three-index form, aliasing through every
		// view, copy with a shorter destination and overlapping ranges and from a
		// string, pointers into a slice of structs, a row of a two-dimensional
		// array, a range that evaluates its header once, a header copied before an
		// append -- and nil against empty: `[]int{}` and `make([]int, 0)` are not
		// nil, local or package-level, where `var s []int` and a nil slice re-sliced
		// are. The two were nil until then (the fault), and the make's zero-length
		// backing array did not compile on the host.
		name: "slice semantics: views, growth, copy, aliasing, and nil against empty",
		src: `var pe = []int{}
var pm []string = make([]string, 0)
var ps = []P{}
var pn []int

type P struct {
	x, y int
}

func sum(s []int) int {
	t := 0
	for _, v := range s {
		t += v
	}
	return t
}

func fill(s []int, v int) {
	for i := range s {
		s[i] = v
	}
}

func part1() {
	var backing [8]int
	s := backing[:4]
	println("A1", len(s), cap(s), sum(s))
	s = append(s, 5)
	s = append(s, 6, 7)
	println("A2", len(s), cap(s), backing[4], backing[5], backing[6], sum(s))
	t := s[2:5]
	println("A3", len(t), cap(t), t[0], t[2])
	t[0] = 9
	println("A4", s[2], backing[2])
	u := s[1:3:4]
	println("A5", len(u), cap(u), u[0], u[1])
	u = append(u, 8)
	println("A6", len(u), cap(u), s[3], backing[3])
	w := t[1:]
	println("A7", len(w), cap(w), w[0])
	x := t[:cap(t)]
	println("A8", len(x), cap(x), x[len(x)-1])
	fill(s[:2], 1)
	println("A9", backing[0], backing[1], backing[2], sum(s))
}

func part2() {
	// nil and empty slices
	var n []int
	println("B1", len(n), cap(n), n == nil, sum(n))
	for range n {
		println("never")
	}
	e := []int{}
	println("B2", len(e), cap(e), e == nil)
	// a literal, its capacity and growth up to it
	lit := []int{1, 2, 3}
	println("B3", len(lit), cap(lit), sum(lit))
	// copy: shorter destination, overlapping ranges
	var a [6]int
	for i := range a {
		a[i] = i + 1
	}
	dst := a[:3]
	k := copy(dst, a[3:])
	println("B4", k, a[0], a[1], a[2], a[3])
	k = copy(a[1:], a[:4])
	println("B5", k, a[0], a[1], a[2], a[3], a[4], a[5])
	k = copy(a[:], a[2:])
	println("B6", k, a[0], a[1], a[2], a[3], a[4], a[5])
	var bs [4]byte
	k = copy(bs[:], "héllo")
	println("B7", k, bs[0], bs[1], bs[2], bs[3])
	// a slice of structs and pointers into it
	ps := [3]P{{1, 2}, {3, 4}, {5, 6}}
	sp := ps[:]
	p := &sp[1]
	p.x = 30
	sp[2].y = 60
	println("B8", ps[1].x, ps[2].y, sp[1].x)
	// slices of slices
	rows := [2][3]int{{1, 2, 3}, {4, 5, 6}}
	r := rows[1][:]
	r[0] = 40
	rr := rows[0][1:]
	println("B9", rows[1][0], len(rows[0][1:]), rr[1])
	// range over a slice evaluates the header once
	s := lit[:]
	c := 0
	for i, v := range s {
		if i == 0 {
			s = s[:1]
		}
		c = c*10 + v
	}
	println("B10", c, len(s))
	// a slice sent by value shares its backing, a header copy does not follow appends
	h := lit[:2]
	h2 := h
	h = append(h, 9)
	println("B11", len(h), len(h2), lit[2], h2[1])
}

func isNil(s []int) bool {
	return s == nil
}

func count(s []P) int {
	return len(s)
}

func part3() {
	var n []int
	e := []int{}
	m := make([]int, 0)
	var arr [2]int
	z := arr[:0]
	nz := n[:0]
	println("N1", n == nil, e == nil, m == nil, z == nil, nz == nil)
	n = []int{}
	println("N2", n == nil, isNil([]int{}), isNil(nil), isNil(m), len(m), cap(m))
	n = nil
	println("N3", n == nil, len(n), pe == nil, pm == nil, ps == nil, pn == nil, len(pe), cap(pm), count(ps), count([]P{}))
	for range pe {
		println("never")
	}
	es := []string{}
	println("N4", es == nil, len(es), len(pm))
}

func main() {
	part1()
	part2()
	part3()
}
`,
		want: "A1 4 8 0\nA2 7 8 5 6 7 18\nA3 3 6 0 5\nA4 9 9\nA5 2 3 0 9\nA6 3 3 8 8\nA7 2 5 8\nA8 6 6 0\nA9 1 1 9 37\nB1 0 0 true 0\nB2 0 0 false\nB3 3 3 6\nB4 3 4 5 6 4\nB5 4 4 4 5 6 4 6\nB6 4 5 6 4 6 4 6\nB7 4 104 195 169 108\nB8 30 60 30\nB9 40 2 3\nB10 123 1\nB11 3 2 9 2\nN1 true false false false true\nN2 false false true false 0 0\nN3 true 0 false false false true 0 0 0 0\nN4 false 0 0\n",
	}, {
		// Embedding semantics measured against Go on the host and a P2-EDGE
		// (2026-09-18): an outer field shadows the embedded one of its name and
		// the embedded one stays reachable by the path, a method at a shallower
		// depth wins, a pointer receiver promoted through an embedded value writes
		// the outer variable, an embedded pointer's fields and methods reach the
		// pointee, two levels of embedding, promoted methods satisfying an
		// interface, equality, whole-embedded-field assignment, keyed and
		// positional literals, through a pointer and in an array. All matched.
		name: "embedding semantics: shadowed fields, promotion depth, pointer embedding and interfaces",
		src: `type A struct {
	x, y int
}

func (a A) Name() string {
	return "A"
}

func (a A) Sum() int {
	return a.x + a.y
}

func (a *A) Inc() {
	a.x++
}

type B struct {
	A
	x int
}

func (b B) Name() string {
	return "B"
}

type C struct {
	*A
	tag int
}

type D struct {
	B
	z int
}

type Namer interface {
	Name() string
}

type Summer interface {
	Sum() int
}

func describe(n Namer) string {
	return n.Name()
}

func main() {
	b := B{A{1, 2}, 30}
	println("E1", b.x, b.A.x, b.y, b.Name(), b.A.Name(), b.Sum())
	b.Inc()
	b.x++
	println("E2", b.x, b.A.x, b.Sum())
	b2 := B{A: A{2, 2}, x: 31}
	println("E3", b == b2, b.A == b2.A, b2.x)
	b.A = A{5, 5}
	println("E4", b.x, b.A.x, b.Sum())
	var a A = A{7, 8}
	c := C{&a, 1}
	c.Inc()
	c.y = 9
	println("E5", a.x, a.y, c.x, c.Sum(), c.Name(), c.tag)
	d := D{B{A{1, 1}, 2}, 3}
	d.Inc()
	println("E6", d.x, d.B.x, d.A.x, d.B.A.x, d.y, d.z, d.Name(), d.Sum())
	var n Namer = &b
	var s Summer = &d
	println("E7", describe(n), describe(&d), describe(&c), s.Sum())
	pd := &d
	pd.z = 4
	pd.B.x = 5
	pd.Inc()
	println("E8", d.z, d.x, d.A.x)
	arr := [2]B{{A{1, 2}, 3}, {A{4, 5}, 6}}
	arr[1].Inc()
	arr[0].x = 9
	println("E9", arr[1].A.x, arr[0].x, arr[0].Sum(), arr[1].Name())
}
`,
		want: "E1 30 1 2 B A 3\nE2 31 2 4\nE3 true true 31\nE4 31 5 10\nE5 8 9 8 17 A 1\nE6 2 2 2 2 1 3 B 3\nE7 B B A 3\nE8 4 5 3\nE9 5 9 3 B\n",
	}, {
		// Function value semantics measured against Go on the host and a P2-EDGE
		// (2026-09-18): the nil zero value and comparison with it, a defined
		// function type, values in an array and a struct field called through the
		// element and the field, a function returning a function called where it
		// stands, a literal as an argument and called where it stands, a method
		// value on a package variable. All matched.
		name: "function value semantics: nil, arrays and fields of them, results, literals and a method value",
		src: `type Op func(int, int) int

type Calc struct {
	op   Op
	name string
}

func add(a, b int) int { return a + b }

func sub(a, b int) int { return a - b }

func mul(a, b int) int { return a * b }

func pick(k int) Op {
	if k == 0 {
		return add
	}
	if k == 1 {
		return sub
	}
	return mul
}

func apply(f Op, a, b int) int {
	return f(a, b)
}

func twice(f func(int) int, x int) int {
	return f(f(x))
}

func double(x int) int { return x * 2 }

type Acc struct {
	total int
}

func (a *Acc) Add(v int) {
	a.total += v
}

func each(s []int, f func(int)) {
	for _, v := range s {
		f(v)
	}
}

var acc Acc

func main() {
	var f Op
	println("F1", f == nil)
	f = add
	println("F2", f == nil, f(2, 3), apply(f, 4, 5), apply(sub, 4, 5))
	ops := [3]Op{add, sub, mul}
	total := 0
	for i := 0; i < 3; i++ {
		total = total*100 + ops[i](7, 3)
	}
	println("F3", total, pick(2)(6, 7), apply(pick(1), 6, 7))
	calcs := [2]Calc{{add, "add"}, {mul, "mul"}}
	for _, c := range calcs {
		println("F4", c.name, c.op(3, 4))
	}
	calcs[0].op = sub
	println("F5", calcs[0].op(3, 4), calcs[0].name)
	println("F6", twice(double, 5), twice(func(x int) int { return x + 1 }, 5))
	each([]int{1, 2, 3}, acc.Add)
	println("F7", acc.total)
	g := acc.Add
	g(10)
	println("F8", acc.total)
	f = nil
	println("F9", f == nil, ops[1] == nil)
	lit := func(a, b int) int { return a*10 + b }
	println("F10", lit(1, 2), apply(lit, 3, 4), func(x int) int { return -x }(5))
}
`,
		want: "F1 true\nF2 false 5 9 -1\nF3 100421 42 -1\nF4 add 7\nF4 mul 12\nF5 -1 add\nF6 20 7\nF7 6\nF8 16\nF9 true false\nF10 12 34 -5\n",
	}, {
		// Switch semantics measured against Go on the host and a P2-EDGE
		// (2026-09-18): a tagless switch, a string switch with a case list and a
		// default in the middle, an init statement, cases evaluated in order and
		// only until one matches, a fallthrough chain past a default, a type switch
		// with a list of types and a nil case, continue and break to a label from
		// inside a switch, and an init with no tag. All matched.
		name: "switch semantics: tagless, lists, default anywhere, evaluation order, fallthrough, type lists and labels",
		src: `type Shape interface {
	Area() int
}

type Sq struct{ s int }

func (q *Sq) Area() int { return q.s * q.s }

type Rect struct{ w, h int }

func (r *Rect) Area() int { return r.w * r.h }

type Tri struct{ b, h int }

func (t *Tri) Area() int { return t.b * t.h / 2 }

var calls int

func f(k int) int {
	calls = calls*10 + k
	return k
}

func classify(x int) string {
	switch {
	case x < 0:
		return "neg"
	case x == 0:
		return "zero"
	case x < 10:
		return "small"
	}
	return "big"
}

func kind(s string) int {
	switch s {
	case "a", "b":
		return 1
	case "":
		return 0
	default:
		return 2
	case "z":
		return 26
	}
}

func main() {
	println("G1", classify(-1), classify(0), classify(5), classify(50))
	println("G2", kind("a"), kind("b"), kind(""), kind("q"), kind("z"))
	switch v := f(2) * 2; v {
	case 1, 2, 3:
		println("G3 low", v)
	case f(4), f(5):
		println("G3 mid", v, calls)
	default:
		println("G3 none")
	}
	calls = 0
	switch f(1) {
	case f(2), f(1):
		println("G4", calls)
	case f(3):
		println("G4 wrong")
	}
	n := 0
	switch {
	case n == 0:
		n += 1
		fallthrough
	case n == 100:
		n += 10
		fallthrough
	default:
		n += 100
	case n == 200:
		n += 1000
	}
	println("G5", n)
	shapes := [4]Shape{&Sq{2}, &Rect{2, 3}, &Tri{4, 5}, nil}
	for i := 0; i < 4; i++ {
		switch v := shapes[i].(type) {
		case *Sq, *Rect:
			println("G6 quad", v.Area())
		case nil:
			println("G6 nil")
		default:
			println("G6 other", v.Area())
		}
	}
outer:
	for i := 0; i < 3; i++ {
		switch i {
		case 1:
			continue outer
		case 2:
			break outer
		}
		println("G7", i)
	}
	switch x := 5; {
	case x > 3:
		println("G8", x)
	}
}
`,
		want: "G1 neg zero small big\nG2 1 1 0 2 26\nG3 mid 4 24\nG4 121\nG5 111\nG6 quad 4\nG6 quad 6\nG6 other 10\nG6 nil\nG7 0\nG8 5\n",
	}, {
		// Pointer semantics measured against Go on the host and a P2-EDGE
		// (2026-09-18): pointers to elements, fields and through a pointer to a
		// pointer, equality of pointers, a pointer to a literal, pointers handed
		// through calls, a copy of a pointee, a chain walked to nil and written
		// through, a package pointer at package storage, a nil pointer through a
		// pointer to it. All matched. Storing a local's address in package storage
		// and the address of a loop variable outside its loop are refused, as
		// designed.
		name: "pointer semantics: elements, fields, pointees, chains, calls and nil",
		src: `type P struct {
	x, y int
}

type Node struct {
	val  int
	next *Node
}

var gp *P
var gs P

func setX(p *P, v int) {
	p.x = v
}

func ptrOf(p *P) *P {
	return p
}

func main() {
	// pointers to elements, fields and pointees
	arr := [3]P{{1, 1}, {2, 2}, {3, 3}}
	p := &arr[1]
	q := &p.y
	*q = 20
	p.x = 10
	println("H1", arr[1].x, arr[1].y, *q, p == &arr[1], q == &arr[1].y, p == &arr[0])
	pp := &p
	(*pp).x = 11
	(*pp).y = 21
	println("H2", arr[1].x, arr[1].y, *pp == p, **pp == arr[1])
	*pp = &arr[2]
	println("H3", p.x, p == &arr[2])
	// a pointer to a composite literal, and pointers through calls
	lp := &P{7, 8}
	setX(lp, 70)
	setX(ptrOf(lp), 71)
	println("H4", lp.x, lp.y, ptrOf(lp) == lp, ptrOf(lp).y)
	gs = *lp
	gp = &gs
	gp.y = 80
	println("H5", lp.y, gs.y, gp == &gs, gp == lp)
	// a copy of the pointee is independent
	cp := *lp
	cp.x = 0
	println("H6", lp.x, cp.x)
	// pointer chains
	n3 := Node{3, nil}
	n2 := Node{2, &n3}
	n1 := Node{1, &n2}
	sum := 0
	for n := &n1; n != nil; n = n.next {
		sum = sum*10 + n.val
	}
	n1.next.next.val = 9
	println("H7", sum, n3.val, n1.next.next == &n3, n2.next.next == nil)
	// nil pointers compare, and a pointer to a nil pointer
	var np *P
	npp := &np
	println("H10", np == nil, *npp == nil, npp != nil)
	*npp = lp
	println("H11", np == lp, np.x)
}
`,
		want: "H1 10 20 20 true true false\nH2 11 21 true true\nH3 3 true\nH4 71 8 true 8\nH5 8 80 true false\nH6 71 0\nH7 123 9 true true\nH10 true true true\nH11 true 71\n",
	}, {
		// `**pp == arr[1]` compared two structs as C scalars and `println(!*bp)`
		// printed a pointer: the type of a unary expression applied only the FIRST
		// operator to the operand's type, so `**pp` was a pointer and `!*bp` its
		// operand's pointer type. Each operator of a run is applied now, innermost
		// first (2026-09-18). Measured against Go, on the host and a P2-EDGE.
		name: "a run of unary operators is typed operator by operator",
		src: `type P struct {
	x, y int
}

func main() {
	arr := [2]P{{1, 2}, {3, 4}}
	p := &arr[1]
	pp := &p
	ppp := &pp
	v := **pp
	v.x = 9
	w := ***ppp
	println("D1", v.x, w.x, arr[1].x, **pp == arr[1], ***ppp == v, ***ppp != v)
	**pp = P{5, 6}
	w2 := ***ppp
	println("D2", arr[1].x, w2.y, (*pp).x, (*p).y)
	n := 7
	np := &n
	npp := &np
	m := **npp + 1
	**npp = -**npp
	k := -*np + ^*np
	println("D3", m, n, k, *&n, -*&n, **npp == *np, &*np == np, *&*np)
	b := true
	bp := &b
	println("D4", !*bp, !!*bp, *bp && !*bp)
	q := &*p
	q.x = 8
	println("D5", arr[1].x, q == p, q.y, *&arr[0] == arr[0])
	more()
}

func neg(b *bool) bool {
	return !*b
}

func flip(p **int) int {
	**p = -**p
	return **p
}

func more() {
	x := 3
	xp := &x
	xpp := &xp
	f := !(*xp > 2)
	g := -*xp * 2
	h := ^*xp & 0xf
	var arr [2]bool
	arr[0] = !*&arr[1]
	c := !*&arr[0] == false
	println("D6", f, g, h, arr[0], c, neg(&arr[1]), flip(xpp), x, -**xpp, !!!*&arr[0])
}
`,
		want: "D1 9 3 3 true false true\nD2 5 6 5 6\nD3 8 -7 13 -7 7 true true -7\nD4 false true false\nD5 8 true 6 true\nD6 false -6 12 true true true -3 -3 3 false\n",
	}, {
		// Composite literal semantics measured against Go on the host and a P2-EDGE
		// (2026-09-18): `[...]T` lengths, keyed and positional struct literals with
		// the rest zeroed, nested literals with the inner types elided in an array,
		// a slice and a two-dimensional array, a call as an element, pointers as
		// elements, an indexed array literal in a field, sparse indexed literals,
		// zero literals compared, variables as elements. All matched.
		name: "composite literal semantics: nesting, elision, keys, sparse indexes, ... and zero values",
		src: `type P struct {
	x, y int
}

type Q struct {
	p    P
	tags [2]string
	ok   bool
}

type Line struct {
	a, b P
}

var pk = [...]int{1, 2, 3, 4}
var gl = Line{a: P{1, 2}, b: P{y: 5}}
var gq = []Q{{P{1, 1}, [2]string{"a", "b"}, true}, {ok: false}}

func mk(k int) P {
	return P{k, k * 2}
}

func main() {
	println("L1", len(pk), pk[3], gl.a.y, gl.b.x, gl.b.y, len(gq), gq[0].tags[1], gq[1].ok, gq[1].p.x)
	lines := [2]Line{{P{1, 2}, P{3, 4}}, {a: P{5, 6}}}
	println("L2", lines[0].b.x, lines[1].a.y, lines[1].b.x)
	rows := [][2]int{{1, 2}, {3, 4}, {5, 6}}
	println("L3", len(rows), rows[2][1], rows[1][0])
	ps := []P{{1, 2}, {3, 4}, mk(5)}
	println("L4", len(ps), ps[2].y, ps[1].x)
	p7 := P{7, 8}
	pps := []*P{&p7, &ps[1]}
	pps[0].x = 70
	println("L5", len(pps), pps[0].x, pps[1].y, p7.x)
	q := Q{p: P{y: 3}, tags: [2]string{1: "z"}}
	println("L6", q.p.x, q.p.y, q.tags[0] == "", q.tags[1], q.ok)
	nested := [2][2]P{{{1, 1}, {2, 2}}, {{3, 3}, {4, 4}}}
	println("L7", nested[1][0].x, nested[0][1].y)
	sparse := [6]int{1: 10, 4: 40}
	println("L8", sparse[0], sparse[1], sparse[4], sparse[5], len(sparse))
	autos := [...]string{"x", "y", "z"}
	println("L9", len(autos), autos[2])
	empty := P{}
	println("L10", empty.x, empty == P{0, 0}, Q{}.ok, Line{}.a == P{})
	k := 3
	dyn := [3]int{k, k * 2, mk(k).y}
	println("L11", dyn[0], dyn[1], dyn[2])
}
`,
		want: "L1 4 4 2 0 5 2 b false 0\nL2 3 6 0\nL3 3 6 3\nL4 3 10 3\nL5 2 70 4 70\nL6 0 3 true z false\nL7 3 2\nL8 0 10 40 0 6\nL9 3 z\nL10 0 true false true\nL11 3 6 6\n",
	}, {
		// `P{1, 2}.x`, `Q{}.tags[i]`, `Line{}.a == P{}`, `P{1, 2}.Sum()`,
		// `P{1, 2}.Scaled(3).x`, `len(Q{}.tags)` and the parenthesised `(P{1,
		// 2}).Sum()` an if header takes: the grammar gave a named literal no suffix
		// until 2026-09-18. The literal's elements and a method's arguments are
		// evaluated in order, once. Measured against Go on the host and a P2-EDGE.
		name: "a struct literal read through a suffix",
		src: `type P struct {
	x, y int
}

func (p P) Sum() int {
	return p.x + p.y
}

func (p P) Scaled(k int) P {
	return P{p.x * k, p.y * k}
}

type Q struct {
	p    P
	tags [2]string
	rows [2][2]int
	ok   bool
}

type Line struct {
	a, b P
}

var calls int

func f(k int) int {
	calls = calls*10 + k
	return k
}

func main() {
	println("M1", P{1, 2}.x, P{1, 2}.y, P{y: 5}.y, P{}.x, Q{}.ok, Q{ok: true}.ok)
	println("M2", Q{tags: [2]string{"a", "b"}}.tags[1], Q{rows: [2][2]int{{1, 2}, {3, 4}}}.rows[1][0], Line{b: P{3, 4}}.b.y)
	println("M3", P{1, 2}.Sum(), P{1, 2}.Scaled(3).x, P{1, 2}.Scaled(3).Sum(), Line{}.a == P{}, Line{a: P{1, 1}}.a != P{})
	calls = 0
	i := 1
	println("M4", Q{tags: [2]string{"c", "d"}}.tags[i], P{f(1), f(2)}.Sum(), P{f(3), 0}.Scaled(f(4)).x, calls)
	s := P{7, 8}.Scaled(2)
	t := Q{p: P{9, 9}}.p
	u := Line{P{1, 2}, P{3, 4}}.b.x + Line{}.a.y
	println("M5", s.x, s.y, t.x, u, len(Q{}.tags), len(Q{}.rows[0]))
	if (P{1, 2}).Sum() == 3 && (Q{ok: true}).ok {
		println("M6 ok")
	}
	var arr [3]int
	arr[P{1, 1}.Sum()] = 5
	println("M7", arr[2], P{1, 2} == P{1, 2}, P{1, 2}.Scaled(1) == P{1, 2})
}
`,
		want: "M1 1 2 5 0 false true\nM2 b 3 4\nM3 3 3 9 true true\nM4 d 3 12 1234\nM5 14 16 9 3 2 2\nM6 ok\nM7 5 true true\n",
	}, {
		// printf measured against Go's fmt.Printf on the host and a P2-EDGE
		// (2026-09-18): every integer width and sign under %d %x %X %o %b %c %U %q
		// %v with widths, '-', '+', ' ' and '0' and a precision; strings under %s
		// with widths and rune-counted precisions, %q with Go's escapes, %x of a
		// string and a byte slice, %v of slices; bools, %%, a String() method under
		// %v, %s and %d, and %T of a defined type, "main.Celsius"; float32 values
		// under %f %e %E %g %G with widths, flags and precisions, ties rounding to
		// even. All matched.
		name: "printf against fmt: integers, strings and floats under every verb, flag, width and precision",
		src: `type Celsius int

func (c Celsius) String() string {
	return "C!"
}

type Plain int

func ints() {
	var i8 int8 = -128
	var u8 uint8 = 255
	var i16 int16 = -32768
	var u16 uint16 = 65535
	var i32 int32 = -2147483648
	var u32 uint32 = 4294967295
	var i64 int64 = -9223372036854775808
	var u64 uint64 = 18446744073709551615
	n := -42
	m := 42
	printf("A1 %d %d %d %d %d %d %d %d\n", i8, u8, i16, u16, i32, u32, i64, u64)
	printf("A2 [%5d] [%-5d] [%05d] [%+d] [%+d] [% d] [% d]\n", n, n, n, m, n, m, n)
	printf("A3 [%x] [%X] [%o] [%b] [%x] [%o]\n", m, m, m, m, n, n)
	printf("A4 [%8x] [%-8X] [%08x] [%x] [%X] [%o]\n", u32, u32, u16, u64, u8, u16)
	printf("A5 [%c] [%c] [%c] [%U] [%U] [%q] [%q]\n", 65, 0x4e16, 0x1f600, 0x41, 0x1f600, 65, 0x4e16)
	printf("A6 [%v] [%v] [%v] [%d] [%3c]\n", n, u64, i8, Plain(7), 'z')
	printf("A7 [%.3d] [%6.3d] [%-6.3d] [%+.3d] [%.0d]\n", 7, 7, -7, 7, 0)
}

func strs() {
	s := "héllo"
	ba := [3]byte{'h', 'i', '!'}
	b := ba[:]
	printf("B1 [%s] [%10s] [%-10s] [%.2s] [%8.3s] [%-8.1s]\n", s, s, s, s, s, s)
	printf("B2 [%q] [%q] [%q] [%q]\n", "a\"b\\c\n\t", "", "\x01\x7f", "é")
	printf("B3 [%x] [%X] [%x] [%s] [%q]\n", "hi", "hi", b, b, b)
	printf("B4 [%v] [%v] [%v]\n", s, b, []string{"a", "b"})
	printf("B5 [%t] [%t] [%v] [%5t] [%-6t]\n", true, false, true, true, false)
	printf("B6 [%%] [%d%%] [%s]\n", 50, "%d")
	var c Celsius = 3
	printf("B7 [%v] [%s] [%d] [%T] [%T] [%T] [%T]\n", c, c, c, c, 1, "s", 2.5)
}

func floats() {
	var f float32 = 3.14159265
	var g float32 = -0.000123456
	var h float32 = 1e20
	var z float32 = 0
	var t float32 = 2.5
	var u float32 = 3.5
	printf("C1 [%f] [%.2f] [%8.3f] [%-8.3f] [%08.3f] [%+.1f] [% .1f]\n", f, f, f, f, f, f, f)
	printf("C2 [%e] [%E] [%.3e] [%g] [%G] [%.3g] [%g]\n", f, f, g, g, h, f, h)
	printf("C3 [%v] [%v] [%v] [%v] [%g] [%.0f] [%.0f]\n", f, g, z, h, z, t, u)
	printf("C4 [%10.4f] [%-10.2e] [%010.2f] [%.1f] [%.1f]\n", g, f, -f, float32(0.25), float32(0.35))
	var arr [3]float32 = [3]float32{1, 0.5, 1e-7}
	printf("C5 [%v] [%v] [%.1f]\n", arr[0], arr[2], arr[1])
}

func main() {
	ints()
	strs()
	floats()
}
`,
		want: "A1 -128 255 -32768 65535 -2147483648 4294967295 -9223372036854775808 18446744073709551615\nA2 [  -42] [-42  ] [-0042] [+42] [-42] [ 42] [-42]\nA3 [2a] [2A] [52] [101010] [-2a] [-52]\nA4 [ffffffff] [FFFFFFFF] [0000ffff] [ffffffffffffffff] [FF] [177777]\nA5 [A] [世] [😀] [U+0041] [U+1F600] ['A'] ['世']\nA6 [-42] [18446744073709551615] [-128] [7] [  z]\nA7 [007] [   007] [-007  ] [+007] []\nB1 [héllo] [     héllo] [héllo     ] [hé] [     hél] [h       ]\nB2 [\"a\\\"b\\\\c\\n\\t\"] [\"\"] [\"\\x01\\x7f\"] [\"é\"]\nB3 [6869] [6869] [686921] [hi!] [\"hi!\"]\nB4 [héllo] [[104 105 33]] [[a b]]\nB5 [true] [false] [true] [ true] [false ]\nB6 [%] [50%] [%d]\nB7 [C!] [C!] [3] [main.Celsius] [int] [string] [float64]\nC1 [3.141593] [3.14] [   3.142] [3.142   ] [0003.142] [+3.1] [ 3.1]\nC2 [3.141593e+00] [3.141593E+00] [-1.235e-04] [-0.000123456] [1E+20] [3.14] [1e+20]\nC3 [3.1415927] [-0.000123456] [0] [1e+20] [0] [2] [4]\nC4 [   -0.0001] [3.14e+00  ] [-000003.14] [0.2] [0.3]\nC5 [1] [1e-07] [0.5]\n",
	}, {
		// %v of a slice or an array whose element type is DEFINED printed nothing:
		// "printing a slice or array of "Celsius" is not supported yet", and a
		// Stringer element is printed through its String() as fmt prints it,
		// "[C! C!]", where println prints the values (2026-09-18).
		name: "printf %v of slices and arrays of defined element types, a Stringer among them",
		src: `type Celsius int

func (c Celsius) String() string {
	return "C!"
}

type Plain int

type Name string

type Flag bool

func main() {
	cs := [2]Celsius{1, 2}
	ps := [2]Plain{3, 4}
	ns := [2]Name{"a", "b"}
	fs := [2]Flag{true, false}
	printf("V1 %v %v\n", cs[:], cs)
	printf("V2 %v %v\n", ps[:], ps)
	printf("V3 %v %v %s\n", ns[:], ns, ns[0])
	printf("V4 %v %v\n", fs[:], fs)
	var bs [3]byte = [3]byte{1, 2, 3}
	printf("V6 %v %x %X %s\n", bs, bs[:], bs[:], "x")
}
`,
		want: "V1 [C! C!] [C! C!]\nV2 [3 4] [3 4]\nV3 [a b] [a b] a\nV4 [true false] [true false]\nV6 [1 2 3] 010203 010203 x\n",
	}, {
		// A slice or an array of floats printed by println, print and %v -- refused
		// until 2026-09-18 -- in Go's shortest form, "[1 0.5 1e-07]"; elements with
		// String() through a value, a pointer and an interface, a nil interface
		// element "<nil>" under %s too, empty slices and a zero-length array.
		// Measured against fmt.Println, fmt.Print and fmt.Printf.
		name: "println and printf of float slices, and Stringer and interface elements",
		src: `type Celsius int

func (c Celsius) String() string {
	if c < 0 {
		return "cold"
	}
	return "warm"
}

type Temp float32

type Shape interface {
	Area() int
	String() string
}

type Sq struct {
	s int
}

func (q *Sq) Area() int {
	return q.s * q.s
}

func (q *Sq) String() string {
	return "sq"
}

type Box struct {
	w int
}

func (b Box) String() string {
	return "box"
}

var calls int

func mk() [2]Celsius {
	calls++
	return [2]Celsius{-1, 1}
}

func main() {
	fs := [3]float32{1, 0.5, 1e-7}
	ts := []Temp{1.5, -2}
	println(fs[:], len(fs))
	println(ts)
	printf("F1 %v %v %v\n", fs, fs[1:], ts)
	var sq Sq
	shapes := [3]Shape{&sq, nil, &sq}
	printf("F2 %v %s\n", shapes, shapes[:2])
	boxes := []Box{{1}, {2}}
	mm := mk()
	printf("F3 %v %s %v\n", boxes, boxes, mm)
	ps := []*Sq{&sq}
	printf("F4 %v %d\n", ps, calls)
	var empty []Celsius
	printf("F5 %v %v %s\n", empty, []Celsius{}, [0]Celsius{})
	print(fs[:2], "\n")
}
`,
		want: "[1 0.5 1e-07] 3\n[1.5 -2]\nF1 [1 0.5 1e-07] [0.5 1e-07] [1.5 -2]\nF2 [sq <nil> sq] [sq <nil>]\nF3 [box box] [box box] [cold warm]\nF4 [sq] 1\nF5 [] [] []\n[1 0.5]\n",
	}, {
		// fmt formats what String() or Error() returns under %x, %X and %q as well
		// as %v and %s. Until 2026-09-18 %x, %X and %q of a Stringer printed the
		// integer it holds -- 41 and 'A' where Go prints 7761726d and "warm" --
		// silently, and refused an error; a nil error prints fmt's complaint for
		// each verb, and a slice of either prints element by element.
		name: "printf formats a Stringer and an error under %x, %X and %q as fmt does",
		src: `type Celsius int

func (c Celsius) String() string {
	return "warm"
}

type Err struct {
	code int
}

func (e *Err) Error() string {
	return "boom"
}

func main() {
	var c Celsius = 65
	printf("S1 [%x] [%X] [%q] [%d] [%c] [%v] [%s]\n", c, c, c, c, c, c, c)
	var err error = &Err{1}
	printf("S2 [%v] [%s] [%q] [%x] [%X]\n", err, err, err, err, err)
	var none error
	printf("S3 [%v] [%s] [%q] [%x]\n", none, none, none, none)
	elems()
}

type Shape interface {
	Area() int
	String() string
}

type Sq struct {
	s int
}

func (q *Sq) Area() int {
	return q.s * q.s
}

func (q *Sq) String() string {
	return "sq"
}

func elems() {
	cs := [2]Celsius{1, 2}
	var sq Sq
	shapes := []Shape{&sq, nil}
	printf("E1 [%x] [%X] [%q] [%v] [%s]\n", cs, cs[:], cs, shapes, shapes)
	printf("E2 [%x] [%q]\n", shapes, shapes)
}
`,
		want: "S1 [7761726d] [7761726D] [\"warm\"] [65] [A] [warm] [warm]\nS2 [boom] [boom] [\"boom\"] [626f6f6d] [626F6F6D]\nS3 [<nil>] [%!s(<nil>)] [%!q(<nil>)] [%!x(<nil>)]\nE1 [[7761726d 7761726d]] [[7761726D 7761726D]] [[\"warm\" \"warm\"]] [[sq <nil>]] [[sq <nil>]]\nE2 [[7371 <nil>]] [[\"sq\" <nil>]]\n",
	}, {
		// fmt asks a value for Error() before String(), and a concrete type is asked
		// as an interface is. Until 2026-09-18 a concrete type was asked for String()
		// alone: %v of a `type Code int` with an Error() method printed the number it
		// holds, silently, a type with both printed its String(), and %s of the
		// first was refused. A promoted Error() and a pointer receiver's count too.
		name: "printf asks a concrete type for Error() before String()",
		src: `type Code int

func (c Code) Error() string { return "code error" }

type Both int

func (b Both) Error() string  { return "the error" }
func (b Both) String() string { return "the string" }

type Fail struct {
	why string
}

func (f Fail) Error() string { return f.why }

type PtrErr struct {
	n int
}

func (p *PtrErr) Error() string { return "ptr error" }

type Wrap struct {
	Fail
	extra int
}

func main() {
	printf("%v|%s|%q|%x\n", Code(3), Code(4), Code(5), Code(6))
	printf("%v %s|\n", Both(1), Both(2))
	printf("%v|%s\n", Fail{"disk"}, Fail{"net"})
	pe := &PtrErr{1}
	printf("%v|%s\n", pe, pe)
	printf("%v\n", []Code{1, 2})
	printf("%v\n", [2]Both{1, 2})
	printf("%v\n", Wrap{Fail{"inner"}, 3})
	c7 := Code(7)
	var err error = &c7
	printf("%v\n", err)
	println(Code(5))
}
`,
		want: "code error|code error|\"code error\"|636f6465206572726f72\nthe error the error|\ndisk|net\nptr error|ptr error\n[code error code error]\n[the error the error]\ninner\ncode error\n5\n",
	}, {
		// A deferred printf's arguments are captured at the defer, the format among
		// them, and the replay read argument i from slot i: `defer printf("alone
		// %v|\n", s)` built without a word and printed its own format where s
		// belonged, and a verb whose argument differed in type from its neighbour's
		// was refused. And a deferred print's call argument was evaluated again at
		// every return, the replay hoisting it anew beside what the defer captured.
		name: "a deferred printf reads its own arguments",
		src: `type Celsius int

func (c Celsius) String() string { return "C!" }

var calls int

func f() int {
	calls++
	return calls * 10
}

func run(x int, s string, c Celsius) {
	defer printf("deferred %v %d %s %q %T %x|\n", x, x, s, s, c, x)
	defer printf("more %v %v|\n", c, f())
	defer printf("width [%5d] [%-4s] [%6.2f]\n", x, s, 1.5)
	defer printf("alone %v|\n", s)
	x = 99
	s = "changed"
	printf("body %d\n", calls)
}

func early(x int) int {
	defer printf("early %d %d|\n", x, f())
	defer println("early", f(), x)
	if x > 0 {
		return x + 1
	}
	return 0
}

func main() {
	run(1, "orig", 5)
	printf("after %d\n", calls)
	printf("early returned %d, calls %d\n", early(1), calls)
}
`,
		want: "body 1\nalone orig|\nwidth [    1] [orig] [  1.50]\nmore C! 10|\ndeferred 1 1 orig \"orig\" main.Celsius 1|\nafter 1\nearly 30 1\nearly 1 20|\nearly returned 2, calls 3\n",
	}, {
		// A value method through a nil pointer panics copying its receiver out, and
		// fmt prints that panic as <nil>, unpadded, under every verb; a pointer
		// method is called with the nil, as Go calls it. %v of the first panicked
		// with "nil pointer dereference" here.
		name: "printf of a nil pointer whose type has String() on a value receiver",
		src: `type V struct {
	n int
}

func (v V) String() string { return "value method" }

type P struct {
	n int
}

func (p *P) String() string { return "pointer method" }

func main() {
	var v *V
	var p *P
	printf("%v|%s|%v|%8v|%-7s|%q\n", v, v, p, v, v, v)
	vs := []*V{nil, &V{1}}
	printf("%v %s\n", vs, vs)
	w := &V{2}
	printf("%v %x\n", w, w)
}
`,
		want: "<nil>|<nil>|pointer method|<nil>|<nil>|<nil>\n[<nil> value method] [<nil> value method]\nvalue method 76616c7565206d6574686f64\n",
	}, {
		// %v and %+v of a struct, as fmt prints one: field by field, the names
		// before them under %+v, an embedded field named by its type; a pointer to
		// one as &{...}, a nil one as <nil>; slices and arrays of them element by
		// element. Inside, fmt asks a field for Error() or String() only when the
		// field is exported -- Temp prints C! where temp prints 22.5 -- and prints
		// a nil pointer or interface field as <nil>.
		name: "printf prints a struct field by field",
		src: `type Celsius float32

func (c Celsius) String() string { return "C!" }

type Code int

func (c Code) Error() string { return "code" }

type Point struct {
	X, Y int
}

type Named struct {
	Name  string
	Pos   Point
	tags  []string
	ok    bool
	Temp  Celsius
	temp  Celsius
	Err   error
	err   error
	Ptr   *Point
	Grid  [2][2]int8
	Code  Code
	big   int64
	u     uint32
}

type Inner struct {
	A int
}

type Outer struct {
	Inner
	B string
}

func mk() Point { return Point{7, 8} }

func main() {
	p := Point{1, 2}
	printf("%v %+v\n", p, p)
	printf("%v %+v\n", &p, &p)
	var np *Point
	printf("%v %+v\n", np, mk())
	var n Named
	n.Name, n.Pos, n.tags, n.ok = "n", Point{3, 4}, []string{"a", "b"}, true
	n.Temp, n.temp, n.Code, n.big, n.u = 21.5, 22.5, 3, -1<<40, 4000000000
	n.Grid[1][0] = -5
	c9 := Code(9)
	n.Err = &c9
	printf("%v\n", n)
	printf("%+v\n", n)
	o := Outer{Inner{5}, "b"}
	printf("%v %+v\n", o, o)
	ps := []Point{{1, 2}, {3, 4}}
	printf("%v %+v\n", ps, ps)
	arr := [2]Point{{5, 6}, {7, 8}}
	printf("%v\n", arr)
	pps := []*Point{&p, nil}
	printf("%v\n", pps[1:])
	printf("%v|%+v\n", struct{}{}, struct{ a, b int }{1, 2})
}
`,
		want: "{1 2} {X:1 Y:2}\n&{1 2} &{X:1 Y:2}\n<nil> {X:7 Y:8}\n{n {3 4} [a b] true C! 22.5 code <nil> <nil> [[0 0] [-5 0]] code -1099511627776 4000000000}\n{Name:n Pos:{X:3 Y:4} tags:[a b] ok:true Temp:C! temp:22.5 Err:code err:<nil> Ptr:<nil> Grid:[[0 0] [-5 0]] Code:code big:-1099511627776 u:4000000000}\n{{5} b} {Inner:{A:5} B:b}\n[{1 2} {3 4}] [{X:1 Y:2} {X:3 Y:4}]\n[{5 6} {7 8}]\n[<nil>]\n{}|{a:1 b:2}\n",
	}, {
		// The shapes a struct's fields take: slices of slices of structs, an
		// anonymous struct, a nil func, pointers with a pointer-receiver String()
		// -- called nil or not, as Go calls it -- arrays of structs, runes and
		// bytes as numbers, a string with a tab. A struct printed by a deferred
		// printf, and one returned by a method call.
		name: "printf prints the fields of a struct as fmt does",
		src: `type Temp struct {
	c float32
}

func (t *Temp) String() string { return "temp" }

type Pt struct {
	x, y int
}

func (p Pt) Scaled(k int) Pt { return Pt{p.x * k, p.y * k} }

type Rec struct {
	Name  string
	Rows  [][]Pt
	Anon  struct{ a, b int }
	F     float32
	Fn    func(int) int
	T     *Temp
	Ts    [2]Temp
	R     rune
	B     byte
	Bytes []byte
	Q     string
}

var global = Rec{Name: "g", F: 0.1}

var row0 = [1]Pt{{1, 2}}

var row1 = [2]Pt{{3, 4}, {5, 6}}

var rowsBack [2][]Pt

func show(r *Rec) {
	defer printf("deferred %v\n", r.Anon)
	printf("%+v\n", r)
}

func main() {
	p := Pt{1, 2}
	printf("%v %+v\n", p.Scaled(3), p.Scaled(-1))
	var r Rec
	r.Name = "rec"
	rowsBack[0], rowsBack[1] = row0[:], row1[:]
	r.Rows = rowsBack[:]
	r.Anon.a, r.Anon.b = 7, 8
	r.F = 1.5
	r.R, r.B = 'x', 'y'
	r.Bytes = []byte{104, 105}
	r.Q = "a b\tc"
	show(&r)
	printf("%v\n", global)
	rs := []Rec{global}
	printf("%v\n", len(rs))
	var t Temp
	r.T = &t
	printf("%v\n", r.T)
	var tp *Temp
	printf("%v %+v\n", tp, struct{ P *Temp }{nil})
}
`,
		want: "{3 6} {x:-1 y:-2}\n&{Name:rec Rows:[[{x:1 y:2}] [{x:3 y:4} {x:5 y:6}]] Anon:{a:7 b:8} F:1.5 Fn:<nil> T:temp Ts:[{c:0} {c:0}] R:120 B:121 Bytes:[104 105] Q:a b\tc}\ndeferred {7 8}\n{g [] {0 0} 0.1 <nil> temp [{0} {0}] 0 0 [] }\n1\ntemp\ntemp {P:temp}\n",
	}, {
		// The embedded strings package measured against Go's on the host and a
		// P2-EDGE (2026-09-18): Contains, ContainsAny, ContainsRune, Count
		// (overlapping, empty, multi-byte), Index, LastIndex, IndexAny, IndexByte,
		// LastIndexByte, IndexRune (U+FFFD for an invalid byte, a negative rune),
		// HasPrefix, HasSuffix, Compare, Cut, CutPrefix, CutSuffix, TrimPrefix,
		// TrimSuffix and TrimSpace over Unicode's spaces. One fault, below.
		name: "the strings package against Go: searching, counting, cutting and trimming",
		src: `import "strings"

var s = "h\u00e9llo, w\u00f6rld"

func search1() {
	println("S1", strings.Contains(s, "w\u00f6"), strings.Contains(s, ""), strings.Contains("", ""), strings.Contains("", "a"))
	println("S2", strings.ContainsAny(s, "xyz\u00f6"), strings.ContainsAny(s, ""), strings.ContainsAny("", ""), strings.ContainsRune(s, '\u00f6'), strings.ContainsRune(s, 'q'))
	println("S3", strings.Count("aaaa", "aa"), strings.Count("h\u00e9llo", ""), strings.Count("", ""), strings.Count("abc", "d"), strings.Count("\u00e9\u00e9\u00e9", "\u00e9"))
	println("S4", strings.Index(s, "l"), strings.Index(s, ""), strings.Index(s, "w\u00f6rld"), strings.Index("", "a"), strings.Index("abc", "abcd"))
	println("S5", strings.LastIndex(s, "l"), strings.LastIndex(s, ""), strings.LastIndex("aaa", "aa"), strings.LastIndex("", ""), strings.LastIndex("abc", "x"))
}

func search2() {
	println("S6", strings.IndexAny(s, "w\u00f6"), strings.IndexAny(s, ""), strings.IndexAny("", "a"), strings.IndexAny("\xffa", "a"))
	println("S7", strings.IndexByte(s, 'w'), strings.IndexByte(s, 0xc3), strings.LastIndexByte(s, 'l'), strings.LastIndexByte(s, 'z'))
	println("S8", strings.IndexRune(s, '\u00f6'), strings.IndexRune(s, 'l'), strings.IndexRune("a\xffb", 0xfffd), strings.IndexRune("a\ufffdb", 0xfffd), strings.IndexRune(s, -1))
	println("S9", strings.HasPrefix(s, "h\u00e9"), strings.HasPrefix(s, ""), strings.HasPrefix("", "a"), strings.HasSuffix(s, "rld"), strings.HasSuffix(s, "xh\u00e9llo"))
	println("S10", strings.Compare("a", "b"), strings.Compare("b", "a"), strings.Compare("", ""), strings.Compare("ab", "a"), strings.Compare("\u00e9", "z"))
}

func cutting1() {
	before, after, found := strings.Cut("key=value=x", "=")
	println("C1", before, after, found)
	before, after, found = strings.Cut("novalue", "=")
	println("C2", before, len(after), found)
	before, after, found = strings.Cut("abc", "")
	println("C3", len(before), after, found)
}

func cutting2() {
	rest, ok := strings.CutPrefix("prefix-body", "prefix-")
	println("C4", rest, ok)
	rest, ok = strings.CutPrefix("body", "prefix-")
	println("C5", rest, ok)
	rest, ok = strings.CutSuffix("name.ogo", ".ogo")
	println("C6", rest, ok)
	rest, ok = strings.CutSuffix("name.go", ".ogo")
	println("C7", rest, ok)
}

func cutting3() {
	println("C8", strings.TrimPrefix("aaab", "a"), strings.TrimPrefix("b", "a"), strings.TrimSuffix("baaa", "a"), strings.TrimSuffix("", ""))
	print("C9 [", strings.TrimSpace("  \t hi there \n\r "), "]\n")
	print("C10 [", strings.TrimSpace("\u00a0\u2003x\u3000\u0085"), "]\n")
	print("C11 [", strings.TrimSpace(""), "][", strings.TrimSpace(" \t "), "][", strings.TrimSpace("\xffa\xff"), "]\n")
}

func main() {
	search1()
	search2()
	cutting1()
	cutting2()
	cutting3()
}
`,
		want: "S1 true true true false\nS2 true false false true false\nS3 2 6 1 0 3\nS4 3 0 8 -1 -1\nS5 12 14 1 0 -1\nS6 8 -1 -1 1\nS7 8 1 12 -1\nS8 9 3 1 1 -1\nS9 true true false true false\nS10 -1 1 0 1 1\nC1 key value=x true\nC2 novalue 0 false\nC3 0 abc true\nC4 body true\nC5 body false\nC6 name true\nC7 name.go false\nC8 aab b baa \nC9 [hi there]\nC10 [x]\nC11 [][][\xffa\xff]\n",
	}, {
		// TrimSpace found the end of the text from each rune's width, and an
		// invalid byte ranges as U+FFFD, three bytes when valid: "\xffa\xff" was
		// cut to s[0:5] of three bytes, "slice bounds out of range" (2026-09-18).
		// With the other edges of the package, measured against Go.
		name: "strings.TrimSpace of a string ending in an invalid byte",
		src: `import "strings"

func part1() {
	print("T1 [", strings.TrimSpace(""), "][", strings.TrimSpace(" \t "), "][", strings.TrimSpace("a"), "][", strings.TrimSpace(" \u00e9 "), "]\n")
	print("T2 [", strings.TrimSpace("\u2028x\u2029"), "][", strings.TrimSpace("x\u200b"), "][", strings.TrimSpace("\u1680\u205f!"), "]\n")
}

func part2() {
	before, after, found := strings.Cut("a\u00e9b\u00e9c", "\u00e9")
	println("T3", before, after, found, strings.Count("\u00e9\u00e9", "\u00e9\u00e9"), strings.Count("abab", "ab"))
	println("T4", strings.Index("a\u00e9\u00e9b", "\u00e9b"), strings.LastIndex("\u00e9a\u00e9", "\u00e9"), strings.LastIndexByte("\u00e9", 0xa9), strings.IndexByte("", 'a'))
}

func part3() {
	println("T5", strings.ContainsRune("a\xff", 0xfffd), strings.ContainsRune("a\ufffd", 0xfffd), strings.ContainsAny("\xff", "\ufffd"), strings.IndexAny("ab\u00e9", "\u00e9b"))
	println("T6", strings.Compare("a\xff", "a\u00e9"), strings.Compare("abc", "abd"), strings.Compare("abc", "abcd"), strings.HasPrefix("\u00e9", "\xc3"))
}

func part4() {
	r1, ok1 := strings.CutPrefix("abc", "")
	r2, ok2 := strings.CutSuffix("abc", "")
	r3, ok3 := strings.CutSuffix("", "a")
	print("T8 [", strings.TrimSpace("a\xff "), "][", strings.TrimSpace("\xff"), "][", strings.TrimSpace(" \xff"), "][", strings.TrimSpace("\xef\xbf\xbd "), "][", strings.TrimSpace(" \xc3"), "]\n")
	println("T7", r1, ok1, r2, ok2, len(r3), ok3, strings.TrimPrefix("abc", "abc"), len(strings.TrimSuffix("abc", "abc")))
}

func main() {
	part1()
	part2()
	part3()
	part4()
}
`,
		want: "T1 [][][a][é]\nT2 [x][x​][!]\nT3 a béc true 1 2\nT4 3 3 1 -1\nT5 true true true 1\nT6 1 -1 -1 true\nT8 [a\xff][\xff][\xff][�][\xc3]\nT7 abc true abc true 0 false  0\n",
	}, {
		// fmt applies every verb to a slice or an array element by element, flags,
		// width and precision included: `%d` of []int{1, 2} is "[1 2]", `%#x` of
		// an array "[0xa 0xff -0x10]", `%8.2f` pads each float, `%3c` each rune.
		// Every verb but %v and %q refused a slice until 2026-09-18. The byte forms
		// print a []byte and a byte array whole, and a Stringer element prints its
		// value under %d. Measured against fmt on the host and a P2-EDGE.
		name: "printf applies every verb to a slice or an array element by element",
		src: `type Celsius int

func (c Celsius) String() string {
	return "warm"
}

type Level uint8

func ints() {
	xs := []int{1, -2, 300}
	arr := [3]int{10, 255, -16}
	i8 := []int8{-128, 127}
	u64 := []uint64{0, 18446744073709551615}
	printf("I1 %d %v %5d %-4d| %+d %05d\n", xs, xs, xs, arr, xs, arr)
	printf("I2 %x %X %o %b %#x\n", arr, arr[:2], xs, i8, arr)
	printf("I3 %d %x %d %d\n", i8, u64, u64, []int{})
	var none []int
	levels := []Level{1, 200}
	cs := []Celsius{1, 2}
	printf("I4 %d %d %d %v\n", none, levels, cs, cs)
}

func runes() {
	rs := []rune{'a', 0x4e16, 0x1f600}
	printf("R1 %c %U %q %3c %d\n", rs, rs, rs, rs[:2], rs)
}

func strs() {
	ss := []string{"a", "b\"c", ""}
	bs := []byte{'h', 'i', 1}
	ba := [3]byte{'o', 'k', 255}
	printf("S1 %s %q %5s %-3s|\n", ss, ss, ss[:2], ss)
	printf("S2 %s %x %X %q %d %v\n", bs, bs, bs, bs, bs, bs)
	printf("S3 %s %x %X %q %d %v\n", ba, ba, ba, ba, ba, ba)
	flags := []bool{true, false}
	printf("S4 %t %6t %v\n", flags, flags, flags)
}

func floats() {
	fs := []float32{1.5, -0.25, 1e+20}
	fa := [2]float32{3.25, 0}
	printf("F1 %f %.1f %8.2f %e %g %G %v\n", fs, fs, fa, fs, fs, fa, fa)
}

func main() {
	ints()
	runes()
	strs()
	floats()
}
`,
		want: "I1 [1 -2 300] [1 -2 300] [    1    -2   300] [10   255  -16 ]| [+1 -2 +300] [00010 00255 -0016]\nI2 [a ff -10] [A FF] [1 -2 454] [-10000000 1111111] [0xa 0xff -0x10]\nI3 [-128 127] [0 ffffffffffffffff] [0 18446744073709551615] []\nI4 [] [1 200] [1 2] [warm warm]\nR1 [a 世 😀] [U+0061 U+4E16 U+1F600] ['a' '世' '😀'] [  a   世] [97 19990 128512]\nS1 [a b\"c ] [\"a\" \"b\\\"c\" \"\"] [    a   b\"c] [a   b\"c    ]|\nS2 hi\x01 686901 686901 \"hi\\x01\" [104 105 1] [104 105 1]\nS3 ok\xff 6f6bff 6F6BFF \"ok\\xff\" [111 107 255] [111 107 255]\nS4 [true false] [  true  false] [true false]\nF1 [1.500000 -0.250000 100000002004087734272.000000] [1.5 -0.2 100000002004087734272.0] [    3.25     0.00] [1.500000e+00 -2.500000e-01 1.000000e+20] [1.5 -0.25 1e+20] [3.25 0] [3.25 0]\n",
	}, {
		// The math package's limits and its IEEE questions, measured against Go on
		// the host and a P2-EDGE (2026-09-18): Go's integer limit constants, int and
		// uint 32 bits wide; MaxFloat32 and the subnormal SmallestNonzeroFloat32;
		// Inf, NaN, IsNaN, IsInf of each sign, Signbit of -0 and of the infinities.
		// None of them existed before.
		name: "math: the limits of the integer types and of float32, Inf, NaN, IsNaN, IsInf and Signbit",
		src: `import "math"

func limits() {
	println("L1", math.MaxInt8, math.MinInt8, math.MaxInt16, math.MinInt16, math.MaxInt32, math.MinInt32)
	var i64 int64 = math.MaxInt64
	var m64 int64 = math.MinInt64
	var u64 uint64 = math.MaxUint64
	var u32 uint32 = math.MaxUint32
	println("L2", i64, m64, u64, u32, math.MaxUint8, math.MaxUint16, math.MaxInt, math.MinInt)
	var u uint = math.MaxUint
	x := 40000
	println("L3", u, x > math.MaxInt16, int8(math.MaxInt8), uint16(math.MaxUint16), int64(math.MinInt64) < 0, math.MaxUint32 == 1<<32-1)
}

func floats() {
	inf := math.Inf(1)
	ninf := math.Inf(-1)
	nan := math.NaN()
	one := 1.0
	println("F1", math.IsNaN(nan), math.IsNaN(one), math.IsNaN(inf), math.IsInf(inf, 1), math.IsInf(inf, -1), math.IsInf(ninf, 0), math.IsInf(nan, 0))
	println("F2", math.Signbit(-one), math.Signbit(one), math.Signbit(math.Copysign(0, -1)), math.Signbit(0), math.Signbit(ninf))
	var mf float32 = math.MaxFloat32
	var sf float32 = math.SmallestNonzeroFloat32
	println("F3", mf > 3.4e38, mf*2 > mf, sf > 0, sf/2 == 0, math.IsInf(float64(mf*2), 1), inf > float64(mf))
	printf("F4 %v %v %v %v %v\n", inf, ninf, nan, mf, sf)
}

func main() {
	limits()
	floats()
}
`,
		want: "L1 127 -128 32767 -32768 2147483647 -2147483648\nL2 9223372036854775807 -9223372036854775808 18446744073709551615 4294967295 255 65535 2147483647 -2147483648\nL3 4294967295 true 127 65535 true true\nF1 true false false true false true false\nF2 true false true false true\nF3 true true true true true true\nF4 +Inf -Inf NaN 3.4028235e+38 1e-45\n",
	}, {
		// %v under a flag, a width and a precision is the type's default verb under
		// them -- %d, %g, %s, %t, element by element for a slice or an array -- with
		// '+' dropped, fmt reading it as the struct-field form. Refused until
		// 2026-09-18; measured against fmt on the host and a P2-EDGE.
		name: "printf %v under flags, widths and precisions",
		src: `var u uint32 = 4000000000
var f float32 = 3.25
var s = "h\u00e9llo"
var r rune = 0xe9
var xs = []int{1, -2}
var fs = []float32{0.5, -1.25}
var ss = []string{"a", "bc"}
var arr = [2]int8{7, -8}
var bs = []byte{1, 255}
var big int64 = -123456789012

func v0() {
	printf("V0 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", -42, -42, -42, -42, -42, -42, -42, -42, -42, -42)
}

func v1() {
	printf("V1 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", u, u, u, u, u, u, u, u, u, u)
}

func v2() {
	printf("V2 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", f, f, f, f, f, f, f, f, f, f)
}

func v3() {
	printf("V3 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", s, s, s, s, s, s, s, s, s, s)
}

func v4() {
	printf("V4 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", true, true, true, true, true, true, true, true, true, true)
}

func v5() {
	printf("V5 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", r, r, r, r, r, r, r, r, r, r)
}

func v6() {
	printf("V6 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", xs, xs, xs, xs, xs, xs, xs, xs, xs, xs)
}

func v7() {
	printf("V7 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", fs, fs, fs, fs, fs, fs, fs, fs, fs, fs)
}

func v8() {
	printf("V8 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", ss, ss, ss, ss, ss, ss, ss, ss, ss, ss)
}

func v9() {
	printf("V9 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", arr, arr, arr, arr, arr, arr, arr, arr, arr, arr)
}

func v10() {
	printf("V10 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", bs, bs, bs, bs, bs, bs, bs, bs, bs, bs)
}

func v11() {
	printf("V11 [%5v][%-5v][%+5v][%05v][%-05v][%+.2v][%8.3v][%.1v][% 4v][%-+6v]\n", big, big, big, big, big, big, big, big, big, big)
}

func main() {
	v0()
	v1()
	v2()
	v3()
	v4()
	v5()
	v6()
	v7()
	v8()
	v9()
	v10()
	v11()
}
`,
		want: "V0 [  -42][-42  ][  -42][-0042][-42  ][-42][    -042][-42][ -42][-42   ]\nV1 [4000000000][4000000000][4000000000][4000000000][4000000000][4000000000][4000000000][4000000000][ 4000000000][4000000000]\nV2 [ 3.25][3.25 ][ 3.25][03.25][3.25 ][3.2][    3.25][3][ 3.25][3.25  ]\nV3 [héllo][héllo][héllo][héllo][héllo][hé][     hél][h][héllo][héllo ]\nV4 [ true][true ][ true][0true][true ][true][    true][true][true][true  ]\nV5 [  233][233  ][  233][00233][233  ][233][     233][233][ 233][233   ]\nV6 [[    1    -2]][[1     -2   ]][[    1    -2]][[00001 -0002]][[1     -2   ]][[01 -02]][[     001     -002]][[1 -2]][[   1   -2]][[1      -2    ]]\nV7 [[  0.5 -1.25]][[0.5   -1.25]][[  0.5 -1.25]][[000.5 -1.25]][[0.5   -1.25]][[0.5 -1.2]][[     0.5    -1.25]][[0.5 -1]][[ 0.5 -1.25]][[0.5    -1.25 ]]\nV8 [[    a    bc]][[a     bc   ]][[    a    bc]][[0000a 000bc]][[a     bc   ]][[a bc]][[       a       bc]][[a b]][[   a   bc]][[a      bc    ]]\nV9 [[    7    -8]][[7     -8   ]][[    7    -8]][[00007 -0008]][[7     -8   ]][[07 -08]][[     007     -008]][[7 -8]][[   7   -8]][[7      -8    ]]\nV10 [[    1   255]][[1     255  ]][[    1   255]][[00001 00255]][[1     255  ]][[01 255]][[     001      255]][[1 255]][[   1  255]][[1      255   ]]\nV11 [-123456789012][-123456789012][-123456789012][-123456789012][-123456789012][-123456789012][-123456789012][-123456789012][-123456789012][-123456789012]\n",
	}, {
		// Goroutine semantics measured against Go on the host and a P2-EDGE, three
		// runs (2026-09-18): three workers ranging over a job channel a fourth cog
		// feeds and closes, results gathered by id; a `go` statement's arguments
		// evaluated where it is written, a later store to one unseen; ping-pong
		// between two cogs; a range over a channel its producer closes and the
		// comma-ok receive after; a select polling with a default until a value
		// comes. All matched.
		name: "goroutine semantics: fan-out and fan-in, the go statement's arguments, ping-pong, close and polling",
		src: `type Job struct {
	id, n int
}

type Result struct {
	id, sum int
}

var results chan Result
var jobs chan Job
var ping chan int
var pong chan int
var nums chan int
var done chan int
var one chan int

var calls int

func arg(k int) int {
	calls = calls*10 + k
	return k
}

func worker() {
	for j := range jobs {
		s := 0
		for i := 1; i <= j.n; i++ {
			s += i
		}
		results <- Result{j.id, s}
	}
	done <- 1
}

func echo(k int, tag int) {
	for i := 0; i < k; i++ {
		v := <-ping
		pong <- v*10 + tag
	}
}

func produce(from, to int) {
	for i := from; i <= to; i++ {
		nums <- i * i
	}
	close(nums)
}

func feed(n int) {
	for i := 1; i <= n; i++ {
		jobs <- Job{i, i * 10}
	}
	close(jobs)
}

func late(v int) {
	for i := 0; i < 1000; i++ {
		calls = calls
	}
	one <- v
}

func fanOut() {
	for w := 0; w < 3; w++ {
		go worker()
	}
	go feed(6)
	var byID [7]int
	for i := 0; i < 6; i++ {
		r := <-results
		byID[r.id] = r.sum
	}
	for w := 0; w < 3; w++ {
		<-done
	}
	println("G1", byID[1], byID[2], byID[3], byID[4], byID[5], byID[6])
}

func pingPong() {
	calls = 0
	x := 5
	go echo(arg(3), x)
	x = 9
	total := 0
	for i := 1; i <= 3; i++ {
		ping <- i
		total = total*100 + <-pong
	}
	println("G2", total, calls, x)
}

func drain() {
	go produce(arg(1), arg(4))
	sum, count := 0, 0
	for v := range nums {
		sum += v
		count++
	}
	v, ok := <-nums
	println("G3", sum, count, v, ok)
}

func poll() {
	go late(7)
	polls := 0
	got := 0
	for got == 0 {
		select {
		case v := <-one:
			got = v
		default:
			polls++
		}
	}
	println("G4", got, polls >= 0)
}

func main() {
	fanOut()
	pingPong()
	drain()
	poll()
	println("G5", calls)
}
`,
		want: "G1 55 210 465 820 1275 1830\nG2 152535 3 9\nG3 30 4 0 false\nG4 7 true\nG5 314\n",
	}, {
		// A struct holding an array was not comparable -- "struct comparison with an
		// array field is not supported: the backend cannot pass a struct with an
		// array field by value" -- where Go compares it field by field (2026-09-18).
		// Its helper takes the operands by pointer now; nested, in an array and
		// through a pointer. Measured against Go on the host and a P2-EDGE.
		name: "comparing structs that hold arrays",
		src: `type Pkt struct {
	hdr [4]byte
	n   int
}

type Frame struct {
	p    Pkt
	tags [2]string
}

func main() {
	a := Pkt{[4]byte{1, 2, 3, 4}, 5}
	b := a
	c := a
	c.hdr[3] = 9
	println("E1", a == b, a == c, a != c, b == Pkt{[4]byte{1, 2, 3, 4}, 5})
	f := Frame{a, [2]string{"x", "y"}}
	g := f
	g.tags[1] = "z"
	println("E2", f == f, f == g, f.p == g.p)
	arr := [2]Pkt{a, c}
	arr2 := arr
	println("E3", arr == arr2, arr[0] == arr[1])
	p := &a
	println("E4", *p == b, *p != c)
}
`,
		want: "E1 true false true true\nE2 true false true\nE3 true false\nE4 true true\n",
	}, {
		// The same, wider: a switch over such a struct (whose case literal crashed
		// the checker, "TODO ... CompositeLit"), an embedded one, a string array
		// field, a float array field holding a NaN, arrays of them, a
		// two-dimensional array field, pointees.
		name: "comparing structs that hold arrays: switch, embedding, NaN and arrays of them",
		src: `type Pkt struct {
	hdr [4]byte
	n   int
}

type Frame struct {
	Pkt
	tags [2]string
	vals [2]float32
}

type Grid struct {
	cells [2][2]int
}

var zero float32

func sw(p *Pkt) int {
	switch *p {
	case Pkt{[4]byte{1, 2, 3, 4}, 5}:
		return 1
	case Pkt{}:
		return 2
	}
	return 3
}

func main() {
	a := Pkt{[4]byte{1, 2, 3, 4}, 5}
	var z Pkt
	o := Pkt{[4]byte{9}, 5}
	println("W1", sw(&a), sw(&z), sw(&o), a == Pkt{[4]byte{1, 2, 3, 4}, 5}, Pkt{} == z)
	f := Frame{a, [2]string{"x", "y"}, [2]float32{1, 2}}
	g := f
	println("W2", f == g, f.Pkt == g.Pkt, f.tags == g.tags)
	g.vals[1] = zero / zero
	h := g
	println("W3", f == g, g == h, g != h)
	frames := [2]Frame{f, f}
	fr2 := frames
	println("W4", frames == fr2, frames[0] == frames[1])
	fr2[1].hdr[0] = 7
	println("W5", frames == fr2, frames[1] == fr2[1])
	g1 := Grid{[2][2]int{{1, 2}, {3, 4}}}
	g2 := g1
	g2.cells[1][1] = 0
	println("W6", g1 == g1, g1 == g2)
	pa, pb := &f, &g
	println("W7", *pa == *pb, *pa == f, *pb != h)
}
`,
		want: "W1 1 2 3 true true\nW2 true true true\nW3 false false true\nW4 true true\nW5 false false\nW6 true false\nW7 false true true\n",
	}, {
		// Operands whose address the by-pointer helper takes: conversions between a
		// struct holding an array and a type defined over it, elements of an array
		// of them indexed by a variable, and a literal on either side.
		name: "comparing structs that hold arrays: conversions and indexed elements",
		src: `type Pkt struct {
	hdr [2]byte
	n   int
}

type Tagged Pkt

func main() {
	a := Pkt{[2]byte{1, 2}, 3}
	t := Tagged(a)
	u := Tagged{[2]byte{1, 2}, 3}
	println(t == u, Tagged(a) == u, u == Tagged(a), Pkt(u) == a, Pkt(t) != Pkt{})
	ps := [2]Pkt{a, a}
	i := 1
	println(ps[i] == ps[0], ps[0] == Pkt{[2]byte{1, 2}, 3})
}
`,
		want: "true true true true true\ntrue true\n",
	}, {
		// A switch compared its tag with each case by C's ==: a struct or an
		// interface tag did not compile, and an ARRAY tag compared where the two
		// arrays are, so `switch a { case [2]int{1, 2}: }` took the default,
		// silently, on the host and the board (2026-09-18). Each is compared as the
		// expression form compares it now: several values in a case, a tag computed
		// by a call and by an init statement, nil and another interface as cases.
		name: "a switch on a struct, an array or an interface",
		src: `type P struct {
	x, y int
}

type Pkt struct {
	hdr [2]byte
	n   int
}

type Shape interface {
	Area() int
}

type Sq struct {
	s int
}

func (q *Sq) Area() int {
	return q.s * q.s
}

var calls int

func mk(k int) P {
	calls = calls*10 + k
	return P{k, k}
}

var s1, s2 Sq

func structs() {
	for i := 0; i < 3; i++ {
		switch mk(i) {
		case P{0, 0}, P{2, 2}:
			println("S1 even", i)
		case P{1, 1}:
			println("S1 one", i)
		}
	}
	switch p := mk(5); p {
	case P{5, 5}:
		println("S2 five", calls)
	default:
		println("S2 other")
	}
	pk := Pkt{[2]byte{1, 2}, 3}
	other := pk
	switch pk {
	case Pkt{}:
		println("S3 zero")
	case other:
		println("S3 same")
	}
}

func arrays() {
	a := [3]int{1, 2, 3}
	b := [3]int{1, 2, 4}
	for _, v := range [2][3]int{a, b} {
		switch v {
		case [3]int{1, 2, 4}:
			println("A1 b")
		case a:
			println("A1 a")
		}
	}
	names := [2]string{"x", "y"}
	switch names {
	case [2]string{"x", "z"}, [2]string{"x", "y"}:
		println("A2 xy")
	}
}

func ifaces() {
	var sh Shape = &s1
	var other Shape = &s2
	var none Shape
	for _, cur := range [3]Shape{sh, other, none} {
		switch cur {
		case nil:
			println("I1 nil")
		case sh:
			println("I1 s1")
		case &s2:
			println("I1 s2")
		}
	}
}

func main() {
	structs()
	arrays()
	ifaces()
}
`,
		want: "S1 even 0\nS1 one 1\nS1 even 2\nS2 five 125\nS3 same\nA1 a\nA1 b\nA2 xy\nI1 s1\nI1 s2\nI1 nil\n",
	}, {
		// `[2]Shape{sh, other}` of interface VARIABLES was emitted as an array
		// initializer of the two structs as they stand, which the target's compiler
		// refuses ("expected pointer to void but got _struct__Shape") and the host's
		// takes. Braced as their two words now (2026-09-18).
		name: "an array literal of interface values",
		src: `type Shape interface {
	Area() int
}

type Sq struct {
	s int
}

func (q *Sq) Area() int {
	return q.s * q.s
}

var s1, s2 Sq

func main() {
	var sh Shape = &s1
	var other Shape = &s2
	arr := [2]Shape{sh, other}
	n := 0
	for _, cur := range arr {
		n += cur.Area() + 1
	}
	println(n)
}
`,
		want: "2\n",
	}, {
		// Function semantics measured against Go on the host and a P2-EDGE
		// (2026-09-19): variadic calls with none, several and a spread slice, which
		// the callee writes through; a two-result call forwarded as the arguments of
		// another; named results returned bare and swapped; a deferred call changing
		// a named result through its address after the return set it; recursion and
		// mutual recursion; a method on a defined function type calling its own
		// receiver -- "cannot call non-function o" until then -- through a literal, a
		// package variable and a converted function.
		name: "function semantics: variadics, named results, a deferred result change, recursion and methods on a function type",
		src: `type Op func(int) int

func (o Op) Twice(x int) int {
	return o(o(x))
}

type Counter struct {
	n int
}

func (c *Counter) Inc() {
	c.n++
}

func (c Counter) Get() int {
	return c.n
}

func sum(xs ...int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

func zeroFirst(xs ...int) {
	if len(xs) > 0 {
		xs[0] = 0
	}
}

func pair() (int, int) {
	return 3, 4
}

func add(a, b int) int {
	return a + b
}

func named() (x, y int) {
	x = 1
	y = 2
	return
}

func swapped() (x, y int) {
	x, y = 1, 2
	return y, x
}

func inc(p *int) {
	*p = *p + 1
}

func deferred() (r int) {
	defer inc(&r)
	r = 5
	return r * 2
}

func fact(n int) int {
	if n <= 1 {
		return 1
	}
	return n * fact(n-1)
}

func even(n int) bool {
	if n == 0 {
		return true
	}
	return odd(n - 1)
}

func odd(n int) bool {
	if n == 0 {
		return false
	}
	return even(n - 1)
}

var step Op = func(x int) int { return x + 3 }

func main() {
	s := []int{1, 2, 3}
	println("F1", sum(), sum(1), sum(1, 2, 3), sum(s...), sum(s[1:]...))
	zeroFirst(s...)
	zeroFirst()
	zeroFirst(7, 8)
	println("F2", s[0], s[1], add(pair()))
	a, b := named()
	c, d := swapped()
	println("F3", a, b, c, d, deferred())
	println("F4", fact(10), even(10), odd(7), even(7))
	double := Op(func(x int) int { return x * 2 })
	println("F5", double.Twice(3), step.Twice(1), Op(fact).Twice(3))
	step = double
	println("F6", step(21), step.Twice(1))
	var ctr Counter
	ctr.Inc()
	ctr.Inc()
	println("F7", ctr.Get(), ctr.n)
}
`,
		want: "F1 0 1 6 6 5\nF2 0 2 7\nF3 1 2 2 1 11\nF4 3628800 true true false\nF5 12 7 720\nF6 42 4\nF7 2 2\n",
	}, {
		// `(*p)(x)` was emitted as `p(x)`, a call of the pointer itself, which
		// neither C compiler takes (2026-09-19). The pointee is bound and called as a
		// variable of its type is: in an expression, nested, as a statement, with
		// two results destructured, from another cog, and deferred -- where Go
		// evaluates the function value at the defer, so a later store through the
		// pointer is not what runs.
		name: "a call through a pointer to a function value",
		src: `type Op func(int) int

type Two func(int) (int, int)

type Act func(int)

var total int

func record(x int) {
	total = total*10 + x
}

func record2(x int) {
	total = total*10 + x + 5
}


func split(x int) (int, int) {
	return x / 10, x % 10
}

func twice(x int) int {
	return x * 2
}

var done chan int

var a Act = record

func worker(p *Act) {
	(*p)(9)
	done <- 1
}

func run(p *Act) {
	defer (*p)(3)
	(*p)(1)
	*p = record2
	(*p)(2)
	*p = record
}

func main() {
	ap := &a
	(*ap)(4)
	run(ap)
	var t Two = split
	tp := &t
	q, r := (*tp)(57)
	var o Op = twice
	op := &o
	s := (*op)((*op)(3)) + (*op)(1)
	go worker(ap)
	<-done

	println(total, q, r, s)
}
`,
		want: "41739 5 7 14\n",
	}, {
		// A method on a defined function type calls its receiver, and one on a
		// pointer to it calls through it (2026-09-19).
		name: "a method on a function type, by value and by pointer",
		src: `// COMPILE

// A method on a defined function type calls its receiver, as a parameter of that
// type is called -- the http.HandlerFunc pattern.

type Handler func(int) int

func (h Handler) Serve(x int) int {
	return h(x) + h(x+1)
}

type Pred func(int) bool

func (p *Pred) Not(x int) bool {
	return !(*p)(x)
}

func main() {
	var h Handler = func(x int) int { return x * 2 }
	var even Pred = func(x int) bool { return x%2 == 0 }
	println(h.Serve(3), even.Not(3))
}
`,
		want: "14 true\n",
	}, {
		// Interface semantics measured against Go on the host and a P2-EDGE
		// (2026-09-19): an interface embedding two others, a value assigned from
		// one to another, comma-ok assertions to an interface and to a concrete
		// type, type switches with interface cases and nil, methods promoted
		// through an embedded pointer and an embedded value, interface equality
		// across dynamic types, and the empty interface. One fault: `_, ok :=
		// x.(Reader)` built the value it discards, a variable the host's compiler
		// refused as set and unused.
		name: "interface semantics: embedding, assertions to an interface, type switches and method sets",
		src: `type Reader interface {
	Read() int
}

type Writer interface {
	Write(v int) int
}

type ReadWriter interface {
	Reader
	Writer
}

type Namer interface {
	Name() string
}

type Dev struct {
	val int
	id  string
}

func (d *Dev) Read() int {
	return d.val
}

func (d *Dev) Write(v int) int {
	d.val = v
	return v * 2
}

func (d *Dev) Name() string {
	return d.id
}

type ROnly struct {
	n int
}

func (r *ROnly) Read() int {
	return r.n
}

type Wrapped struct {
	*Dev
	extra int
}

type Plain struct {
	Dev
}

func use(rw ReadWriter) int {
	return rw.Write(rw.Read() + 1)
}

func describe(r Reader) string {
	switch v := r.(type) {
	case ReadWriter:
		return "rw"
	case Namer:
		return v.Name()
	case nil:
		return "nil"
	}
	return "r"
}

func main() {
	d := Dev{5, "dev"}
	var rw ReadWriter = &d
	println("I1", use(rw), d.val, rw.Read())
	var r Reader = rw
	w, ok := r.(Writer)
	println("I2", ok, w.Write(9), d.val)
	ro := ROnly{3}
	r = &ro
	_, ok = r.(Writer)
	n, isNamer := r.(Namer)
	println("I3", ok, isNamer, n == nil, describe(r), describe(&d), describe(nil))
	wr := Wrapped{&d, 1}
	var rw2 ReadWriter = &wr
	println("I4", rw2.Read(), use(rw2), d.val, wr.Name())
	var pl Plain
	pl.id = "plain"
	var nm Namer = &pl
	println("I5", nm.Name(), describe(&pl))
	var r2 Reader = &d
	var r3 Reader = &d
	var r4 Reader = &ro
	println("I6", r2 == r3, r2 == r4, r2 != nil, Reader(rw) == r2)
	rw3, ok3 := r2.(ReadWriter)
	println("I7", ok3, rw3.Read(), rw3 == rw)
	var e interface{} = &d
	_, isR := e.(Reader)
	_, isRO := e.(*ROnly)
	dd, isD := e.(*Dev)
	println("I8", isR, isRO, isD, dd.id)
}
`,
		want: "I1 12 6 6\nI2 true 18 9\nI3 false false true r rw nil\nI4 9 20 10 dev\nI5 plain rw\nI6 true false true true\nI7 true 10 true\nI8 true false true dev\n",
	}, {
		// Every comma-ok form with its value discarded: assertions to a concrete
		// type and to an interface, declared and assigned, receives from open and
		// closed channels of scalars and structs, in a select too (2026-09-19).
		name: "comma-ok forms with a blank value",
		src: `type Reader interface {
	Read() int
}

type Dev struct {
	v int
}

func (d *Dev) Read() int {
	return d.v
}

type Pair struct {
	a, b int
}

var ch chan int
var pch chan Pair
var done chan int

func feed() {
	ch <- 4
	close(ch)
	pch <- Pair{1, 2}
	close(pch)
	done <- 1
}

func main() {
	d := Dev{3}
	var r Reader = &d
	var e interface{} = &d
	_, ok1 := r.(*Dev)
	_, ok2 := e.(Reader)
	_, ok3 := e.(*Dev)
	var ok4 bool
	_, ok4 = e.(Reader)
	println("B1", ok1, ok2, ok3, ok4)
	go feed()
	_, ok5 := <-ch
	_, ok6 := <-ch
	var ok7 bool
	select {
	case _, ok7 = <-pch:
	}
	_, ok8 := <-pch
	println("B2", ok5, ok6, ok7, ok8, <-done)
	switch v := e.(type) {
	case Reader:
		println("B3 reader")
	case *Dev:
		println("B3 dev", v.v)
	}
}
`,
		want: "B1 true true true true\nB2 true false true false 1\nB3 reader\n",
	}, {
		// The same in every position an interface VALUE fills an array element:
		// a package array and slice of interface variables, a local slice with
		// nil among them, calls, and a struct field beside them. The target's
		// compiler refused the four array and slice forms (2026-09-18).
		name: "interface values as array and slice elements, local and package-level",
		src: `type Shape interface {
	Area() int
}

type Sq struct {
	s int
}

func (q *Sq) Area() int {
	return q.s * q.s
}

type Box struct {
	sh Shape
	n  int
}

var s1 = Sq{2}
var s2 = Sq{3}
var g1 Shape = &s1
var g2 Shape = &s2
var pkgArr = [2]Shape{g1, g2}
var pkgSl = []Shape{g2, g1}

var calls int

func pick(k int) Shape {
	calls = calls*10 + k
	if k == 1 {
		return g1
	}
	return g2
}

func main() {
	sh, other := g1, g2
	sl := []Shape{sh, other, nil}
	boxes := [2]Box{{sh, 1}, {other, 2}}
	b := Box{other, 3}
	calls = 0
	calc := [3]Shape{pick(1), pick(2), pick(1)}
	sum := 0
	for _, x := range sl {
		if x != nil {
			sum = sum*100 + x.Area()
		}
	}
	println(sum, len(sl), sl[2] == nil, boxes[0].sh.Area(), boxes[1].sh.Area(), b.sh.Area(), b.n)
	println(calc[0].Area(), calc[1].Area(), calc[2].Area(), calls)
	println(pkgArr[0].Area(), pkgArr[1].Area(), pkgSl[0].Area(), pkgSl[1].Area(), pkgArr[0] == g1)
}
`,
		want: "409 3 true 4 9 9 3\n4 9 4 121\n4 9 9 4 true\n",
	}, {
		// `%v` of an INTERFACE value prints what it holds, as fmt does: "&{3 4}" for a
		// pointer to a struct, the dynamic value's String() where it has one, its
		// address for a pointer to anything else, and "<nil>" for a value holding
		// nothing or holding a nil pointer. Refused until now ("%T prints its type"),
		// there being no per-type formatter to reach for. The value's TABLE says which
		// type it holds, so the print is a chain testing it against every table the
		// program makes for that interface -- which is known only after the last body
		// is emitted, since the store that makes one may be written anywhere
		// (mintIfacePrinters). An interface DECLARING Error() or String() is unchanged:
		// fmt calls that, and so did this.
		name: "%v of an interface value",
		src: `type P struct{ x, y int }

type Inner struct{ a, b int }

type Outer struct {
	tag string
	in  Inner
}

type Named struct {
	n string
	k int
}

type Nums []int

type Counter int

type Shape interface{ Area() int }

func (p *P) Area() int { return p.x * p.y }

func (o *Outer) Area() int { return o.in.a }

func (n *Named) Area() int { return len(n.n) }

func (n *Named) String() string { return n.n }

func (n *Nums) Area() int { return len(*n) }

func (c *Counter) Area() int { return int(*c) }

var gp = P{3, 4}

var go1 = Outer{"t", Inner{1, 2}}

var gn = Named{"hi", 9}

var gnums = Nums{7, 8}

var gc Counter = 5

var nilp *P

func pick(k int) Shape {
	if k == 0 {
		return &gp
	}
	return &go1
}

func main() {
	var s Shape = &gp
	printf("%v|%+v\n", s, s)
	var t Shape = &go1
	printf("%v|%+v\n", t, t)
	var u Shape = &gn
	printf("%v|%+v\n", u, u)
	var v Shape = &gnums
	printf("%v|%+v\n", v, v)
	var w Shape
	printf("%v|%+v\n", w, w)
	printf("%v|%v\n", pick(0), pick(1))
	var any interface{} = &gp
	printf("%v|%+v\n", any, any)
	var z Shape = nilp
	printf("%v|%v\n", z, z == nil)
	var y Shape = &gc
	printf("%d|%d\n", y.Area(), gnums.Area())
}
`,
		want: "&{3 4}|&{x:3 y:4}\n&{t {1 2}}|&{tag:t in:{a:1 b:2}}\nhi|hi\n&[7 8]|&[7 8]\n<nil>|<nil>\n&{3 4}|&{t {1 2}}\n&{3 4}|&{x:3 y:4}\n<nil>|false\n5|2\n",
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
	runCorpus(t, nil, nil)
}

func TestEmitCRun(t *testing.T) {
	runCorpus(t, []EmitOption{Checked()}, nil)
}

// TestEmitCRunRenamedTypes runs every corpus program that declares a type with EVERY
// main-package type spelled ogo_T_<name> in C -- the spelling EmitC gives a type whose
// name the emitted runtime uses for an identifier of its own. That spelling is rare in
// practice, so without this a place that writes a type's C name without asking
// typeMangle would be found by the one program that collides, as a C compile error or
// a wrong answer; here it is found by the whole corpus.
func TestEmitCRunRenamedTypes(t *testing.T) {
	runCorpus(t, []EmitOption{Checked(), renameAllTypes()}, func(test emitRunCase) bool {
		return strings.HasPrefix(test.src, "type ") || strings.Contains(test.src, "\ntype ") || strings.Contains(test.src, "\ttype ")
	})
}

func runCorpus(t *testing.T, opts []EmitOption, keep func(emitRunCase) bool) {
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
		if keep != nil && !keep(test) {
			continue
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
			//
			// -fwrapv defines signed integer overflow as two's-complement wrapping,
			// which is what Go's spec guarantees and what the P2 target does: its
			// soft 64-bit routines wrap, verified on the board. Without it the host
			// gcc exploits the overflow as undefined and computes a different
			// result -- `x * K % 3` for an overflowing int64 product folded to a
			// value that disagrees with both Go and the hardware. The shim exists
			// to model the target off-board, so it must model the target's wrapping;
			// this is fidelity, not a workaround (the emitted C run on the P2 is
			// already correct). Found by the smith oracle at a wide seed sweep.
			out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra",
				"-Wno-unused-function", "-Wno-format", "-I", shim,
				// -lm: the host's math functions live in libm, where the target's
				// are compiler builtins and need no library at all.
				"-o", bin, csrc, "-lpthread", "-lm").CombinedOutput()
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
		{
			// An ARRAY type of another package is a literal type too, and its values
			// are checked against its element type.
			name: "an array element of the wrong type",
			src:  "r := geo.Row{1, \"x\"}\nprintln(r[0])",
			want: "cannot use \"x\" of type string as type int8 in array or slice literal",
		},
		{
			name: "an array element out of range",
			src:  "r := geo.Row{1, 300}\nprintln(r[0])",
			want: "constant 300 overflows int8",
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

type Row [2]int8
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

// TestCrossPkgInterface pins what an IMPORTED interface reports. Until the
// method-set questions were asked by the WRITTEN name, none of these was reached:
// the checker did not recognise "geo.Shape" as an interface at all -- it resolved the
// bare "Shape" in this package's scope, found nothing, and the pointer-ness rule
// spoke instead ("cannot use &pq (an address) as geo.Shape value"). So an imported
// interface accepted nothing and refused everything, both for that one reason.
//
// The accepted forms are exercised by multiPkgProgram, which runs on the host shim,
// through the real backend and on the board. What is pinned here is that each
// refusal still happens, and that it names the type as the program spelled it.
func TestCrossPkgInterface(t *testing.T) {
	const geoSrc = `type Quad struct {
	W int
	H int
}

func (q *Quad) Area() int { return q.W * q.H }

type Plain struct{ N int }

type Shape interface {
	Area() int
}
`
	for _, test := range []struct{ name, src, want string }{
		{
			name: "a type that does not implement it",
			src:  "var s geo.Shape = &pp\nprintln(s.Area())",
			want: "cannot use &pp (variable of type *geo.Plain) as geo.Shape value in variable declaration: geo.Plain does not implement geo.Shape (missing method Area)",
		},
		{
			name: "a value where the pointer goes",
			src:  "var s geo.Shape = pq\nprintln(s.Area())",
			want: "cannot use pq (variable of type geo.Quad) as geo.Shape value in variable declaration: an interface holds a pointer here; write &pq",
		},
		{
			name: "an impossible assertion to a qualified type",
			src:  "var s geo.Shape = &pq\nq := s.(*geo.Plain)\nprintln(q.N)",
			want: "impossible type assertion: s.(*geo.Plain): *geo.Plain does not implement geo.Shape (missing method Area)",
		},
		{
			name: "an impossible case naming a qualified type",
			src:  "var s geo.Shape = &pq\nswitch s.(type) {\ncase *geo.Plain:\nprintln(1)\n}",
			want: "impossible type switch case: s.(type) case *geo.Plain: *geo.Plain does not implement geo.Shape (missing method Area)",
		},
		{
			name: "the same qualified case twice",
			src:  "var s geo.Shape = &pq\nswitch s.(type) {\ncase *geo.Quad:\nprintln(1)\ncase *geo.Quad:\nprintln(2)\n}",
			want: "duplicate case *geo.Quad in type switch",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			main := "import \"geo\"\n\nvar pq geo.Quad\nvar pp geo.Plain\n\nfunc main() {\n" + test.src + "\n}\n"
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte(main)},
				"geo/geo.ogo": &fstest.MapFile{Data: []byte(geoSrc)},
			}
			if _, err := Build(-1, []string{"main.ogo"}, fsys); err == nil {
				t.Fatalf("Build accepted %q", test.src)
			} else if !strings.Contains(err.Error(), test.want) {
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
	"6 13\n0 8\n0 0\n2 7\n2 8\n40\n105 200\n20 48\n7 4\n3 9\n30\n" +
	"400 4\ngreet\n5\n103\nre\ntrue\ngreet!hi\ngreet: hi\ngreet\n" +
	"30\n30\n30\n5\n1 10\n14 true 14 true\n6\nsizer\n9\n42\n5 10 10 true\n2 2 2 2 MM 2\n100 50 50 9.75 19.5 4 true\n100 -1\n" +
	"20 4 10 4 2\n105 2 20 383\n16 6\n[8 9]\n10 5 6 14 7\n16 9\nchain.Reg chain.Lamp\n" +
	"9 4 9\n9 7\n6 3\n8 16 9\n9 9 18\n11 22 6 8 28 17\n12 true\n0 chain: off true\nchain: off 7\ncur\n20 107 128\n6 42\n2 7 6\n" +
	"1649267441664 2199023255552 1099511627776 35184372088832 true\n" +
	"17 gx-7 true 10 19\n" +
	"2 7 56 9 3 8 false 2 1 2 6 3 true 7\n" +
	"[2]greet.Row [2]greet.Reader greet.Row\n" +
	"123 p2 9 3 2 3\n1 1 2 1 102 3\n2 1 4 3 4 4 5 134\n21 10 100 200 7 0\n1 5 1 2 4 1 3 21 1 2 12 12 123 123 11\n10 21 110 21 14 true 3 42\n10 3 4\n6 10 2\n6 11\n6 5 8 6\ntrue 110 6 106\n7 5\ntrue true\n" +
	"1234567891 1 3 8 14 30 39\n"

var multiPkgProgram = map[string]string{
	"main.ogo": `import "chain"
import "greet"
import "initord/ideps"
import "initord/itrace"
import "lib"

// A private helper of main's, same name as one in greet: with per-package name
// mangling the two do not collide in the single translation unit.
func scale(n int) int { return n + 1 }

// main's own Point, same name (and method) as greet's: per-package mangling of
// types and methods keeps them distinct in the single translation unit.
type Point struct{ x, y int }

// An alias of another package's type: that type, fields and methods.
type LD = lib.Dev

func (p Point) sum() int { return p.x + p.y }

// A package global with the same name as greet's, likewise namespaced.
var base int = 5

// A package constant with the same name as greet's: per-package mangling keeps the
// two from colliding in the single translation unit (both emit a distinct C name).
const prefix = "AT+"

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
// A constant of an imported package used in a CONST declaration of this one,
// which needs its VALUE at compile time -- the other package emits a symbol,
// and C evaluates a file-scope initializer before there is one.
println(limit, wide)
// A QUALIFIED conversion, greet.T(x): a type name spelled where a call looks
// like it stands, which is why every one of these was refused ("cannot infer a
// type") until the conversion was looked for before the function. One per kind
// of target -- a defined scalar, with a method called on the result; a defined
// array; a defined slice; and an interface.
c := greet.Celsius(20)
println(int(c), greet.Celsius(24).Double())
var ra [2]int
ra[0] = 3
ra[1] = 4
row := greet.Row(ra)
println(row[0]+row[1], greet.Row(ra)[1])
ls := greet.L(pool[:])
println(len(ls), ls[0])
sh = greet.Shape(&quad)
println(sh.Area())
// Another package's STRING constant. Every other constant type crossed the
// boundary -- an integer one emits a C "static const", which is a name -- and a
// string is inlined at each use, so there was no symbol to read and every one of
// these reported "greet is not a value with fields or elements", of a package,
// about a constant that is there.
// A package variable of greet initialized from one declared in ANOTHER FILE of
// that package -- a forward reference whichever file is emitted first, and both
// directions are covered so it is one regardless.
println(greet.Doubled, greet.FromLoud)
println(greet.Tag)
println(len(greet.Tag))
println(greet.Tag[0])
println(greet.Tag[1:3])
println(greet.Tag == "greet")
println(greet.Tag + "!" + greet.Prompt)
println(banner)
println(stored)
// Another package's INTERFACE, used with NO conversion at all: a declaration, an
// assignment, an argument, a return, and the two ways back out -- an assertion
// and a type switch, each naming the qualified concrete type. Every one of these
// was refused ("cannot use &quad (an address) as greet.Shape value"), so the only
// way into an imported interface was the conversion above.
var sh2 greet.Shape = &quad
println(sh2.Area())
sh = &quad
println(area(sh))
println(mkShape().Area())
if q, ok := sh2.(*greet.Quad); ok {
	println(q.W)
	println(greet.Tail("AT+CFG=1", "AT+CFG="), greet.Wide(5))
greet.Meters[0] = &greet.G1
mv, mok := greet.Meters[0].Read()
var rd greet.Reader = &greet.G1
lv, lok := rd.Read()
println(mv, mok, lv, lok)
}
switch x := sh2.(type) {
case *greet.Quad:
	println(x.H)
}
switch sh2.(type) {
case greet.Sizer:
	println("sizer")
default:
	println("other")
}

chained()
errors()
qualifiedCases()
initOrder()
untypedShifts()
crossFileConsts()
qualifiedArrays()
libShapes()
initTrace()
}

// The whole initialization order, traced (measured against Go 2026-09-18): in
// each package the variables in dependency order whatever order they are
// declared in, then its init functions in the order written; a package whole
// before any package importing it; main last. Every step appends its digit.
var ordM2 = itrace.Mark(9) + ordM1
var ordM1 = itrace.Mark(8) + ideps.Sum()

func init() { itrace.Trace = itrace.Trace*10 + 1 }

func initTrace() {
	println(itrace.Trace, itrace.A1, itrace.A2, ideps.B1, ideps.B2, ordM1, ordM2)
}

// Another package's defined ARRAY type where a type is written: a variable, a
// parameter, an imported function's result, a struct field and its literal, a
// pointer indexed, a package array of them with elided literals, a range, a copy
// and a comparison. Each was "unsupported type" or refused by the checker; only a
// conversion to one worked.
type rowSlot struct {
	id  int
	row greet.Row
}

var rows = [2]greet.Row{{1, 2}, {3, 4}}

func rowTotal(r greet.Row) int { return r[0] * r[1] }

func qualifiedArrays() {
	var r greet.Row
	r[1] = 5
	f := greet.Fill(7)
	s := rowSlot{id: 1, row: greet.Row{8, 9}}
	p := &r
	p[0] = 2
	t := 0
	for i, v := range rows[1] {
		t += i + v
	}
	c := r
	c[0] = 6
	var byMeter [len(greet.Meters)]int
	var byRow [len(rows[0]) * 3]bool
	byMeter[1] = 3
	byRow[5] = true
	var raw [2]int
	raw[0], raw[1] = 3, 4
	conv := greet.Row(raw)
	println(len(r), r.Sum(), rowTotal(f), s.row[1], rows[1][0], t, c == r, r[0], s.id, len(byMeter), len(byRow), byMeter[1], byRow[5], conv.Sum())
	printf("%T %T %T\n", rows, greet.Meters, conv)
}

// Another package's constants and array bounds built from constants in a LATER file
// of that package.
func crossFileConsts() {
	var a [16]uint8
	a[0] = 3
	println(greet.Slots, greet.Model, greet.Gain == 0.375, greet.BufLen(), greet.FrameCap(a))
}

// An untyped constant shifted by a variable takes the type of where it stands, which
// here is a type of another package: a variable of it, a store into one of that
// package's variables, an argument, a result, and an operand beside one.
func untypedShifts() {
	var s uint = 40
	chain.Set(s)
	var m chain.Mask = 1 << (s + 1)
	chain.Flags |= 1 << (s - 1)
	println(chain.Flags, m, chain.Bit(s), chain.Top, m&(1<<(s+1)) != 0)
}

// The second half of main, as a function of its own for the reason errors is. The
// pool of cog RAM registers is sized by the largest frame in the program, and with
// the backend's small-function inliner on -- it is, since the regeneration of
// 2026-08-29 let -Ono-inline-small go -- an inlined call's arguments and locals join
// the caller's: main's frame grew from 100 registers to 127, and the whole cog area
// from 454 longs to 481, one past the 480 the P2 has. A split is the whole fix, as
// the README says it is, and what it exercises is unchanged: a package importing
// ANOTHER package (every import path is read against the directory being BUILT, so
// chain finds greet beside itself rather than under itself), and the rest of what a
// program can reach in chain from main.
func chained() {
// A package importing ANOTHER package. Every import path is read against the
// directory being BUILT, so chain finds greet beside itself rather than under
// itself -- which is the layout a program of several packages actually has.
println(chain.Via(4))

// A goroutine running an IMPORTED package's function, rendezvousing on that
// package's channels from this one. Both directions: the send names the channel
// through its package and so does the receive.
go greet.Answer()
greet.Relay <- 14
println(<-greet.Ack)
// A method called on an imported package's 64-bit CONSTANT, alone and at the head
// of a chain, and on the result of an imported function: each is a chain whose
// head is an import qualifier, which had no lowering -- "unsupported call in
// expression" for all three -- and the constant has no C symbol to name (see
// emitConstSpecName). Found by sweeping the positions a constant can stand in.
println(chain.Huge.Int(), chain.Huge.Twice().Int(), chain.Sum(chain.Huge, chain.Huge).Int(), chain.Huge > 0)
// The same for a STRING constant of an imported defined type: the receiver, the
// chain, a local inferred from it (which used to lose the type and find no
// method), and a conversion of the chain's result.
u := chain.Unit
up := chain.Unit.Upper()
println(chain.Unit.Len(), chain.Unit.Upper().Len(), u.Len(), up.Len(), string(chain.Unit.Upper()), len(chain.Unit))
// The FLOAT counterparts: methods on an imported float constant and on a chain
// from it, a local bound to it, an untyped one converted and scaled, an integral
// one as a shift count, a float32 one compared.
t := chain.Boil
println(chain.Boil.Int(), chain.Boil.Half().Int(), t.Half().Int(), float32(chain.G), chain.G*2, 1<<chain.Two, chain.K32 == 0.75)
// A method on an element of an imported package's array variable, which used
// to be "chain is not a value with fields or elements".
println(chain.Table[1].Int(), chain.Table[0].Half().Int())
// An imported package's ARRAY and SLICE variables in the rest of the positions:
// read, indexed, measured, written, incremented, ranged over and re-sliced.
println(chain.Ints[1], len(chain.Ints), chain.Sl[0], len(chain.Sl), len(chain.Tag[1:3]))
chain.Ints[3] = 44
chain.Ints[2]++
n := 0
for _, v := range chain.Ints {
	n += v
}
xs := chain.Ints[:2]
println(n, len(xs), xs[1], chain.InSum())
// A package using its OWN struct variable as a receiver and its own function as
// a value, both of which named an unmangled symbol outside main.
println(chain.Own(), chain.Kit.N)
// An imported package's array printed whole: its elements, not its address.
println(chain.Grid)
// A promoted method and field of an imported type, an imported interface as a
// parameter, and a pointer variable bound to a value of an imported type.
pan := chain.Panel{chain.Reg{5}, 6}
lmp := chain.Lamp{7}
qp := &lmp
println(pan.Twice(), pan.N, pan.Tag, chain.Draw(&lmp, qp), qp.Watts())
// The address of an imported package VARIABLE where that package's interface is
// wanted: as an argument, and bound to a variable of the interface type.
var lit chain.Lit = &chain.Bulb
println(chain.Draw(&chain.Bulb, qp), lit.Watts())
// %T of a type of another package: what the program calls it, not the C symbol
// the compiler mangled it to.
printf("%T %T\n", chain.Kit, lmp)
// An interface of main's EMBEDDING one of another package's, and a value of it
// passed where the embedded interface itself is wanted.
var bx Boxed = &crate
println(bx.Area(), bx.Tag(), area(bx))
// A method value of ANOTHER PACKAGE's variable: what is bound is the address of
// that package's global, and the second is PROMOTED from the type it embeds, so
// what is bound is the address of the embedded sub-object inside it.
watts := chain.Bulb.Watts
inc := chain.Kit.Inc
inc()
println(watts(), chain.Kit.N)
pi := chain.Deck.Inc
pi()
println(chain.Deck.Twice(), chain.Deck.N)
// A struct of main's embedding one of another package's: a promoted field, a
// promoted value-receiver method and a promoted pointer-receiver one.
println(framed.N, framed.Twice(), framed.F)
framed.Inc()
println(framed.N, framed.Reg.N, framed.Twice())
var pl Plusser = &wr
r := pl.Plus(greet.Vec{10, 20})
m := greet.Apply(dbl, greet.Vec{3, 4})
println(r.A, r.B, m.A, m.B, greet.Apply(dbl, m).Sum(), wr.Plus(m).Sum())
}

// A function of its own, not more of main: one function's locals live in COG RAM,
// of which there are 480 longs for all of them together, and main was already close
// to it. See chained for the day it crossed the line.
//
// The predeclared error, across a package boundary. It is the universe's type and
// not any package's, so the two sides have to agree on one C type and one table
// shape -- and the SENTINEL comparison is what a caller does with an exported error
// variable, which with no heap is the only way to have one.
func errors() {
	watt, err := chain.Power(true)
	println(watt, err == nil)
	watt, err = chain.Power(false)
	println(watt, err.Error(), err == &chain.ErrOff)
	// An interface of this package EMBEDDING error, satisfied by that package's type.
	var f Failing = &chain.ErrOff
	println(f.Error(), f.Pin())
}

// A qualified reference as a switch CASE. The case is folded like a constant
// expression, and an import resolves through the FILE scope which that walk does not
// reach -- so a variable's or a call's qualifier read as undefined, and only a
// constant's got through.
func qualifiedCases() {
	chain.Cur = 5
	switch chain.Cur {
	case chain.Cap:
		println("cap")
	case chain.Pair():
		println("pair")
	case chain.Cur:
		println("cur")
	default:
		println("none")
	}
}

// Failing embeds the predeclared error beside a method of its own, which is how a
// richer failure type is written in Go. error has no AST to read -- the universe
// holds it, not any file -- so its one method is contributed from the universe.
type Failing interface {
	error
	Pin() int
}

// Boxed embeds an imported interface, written AFTER a method of its own so that
// the two tables lay their slots out differently: greet.Shape's Area is slot 0
// while Boxed's is slot 1. A Boxed passed where a greet.Shape is wanted therefore
// has to be re-tabled rather than handed over as it stands.
type Boxed interface {
	Tag() int
	greet.Shape
}

// Framed embeds ANOTHER PACKAGE's struct: the field is named after the type
// unqualified, f.Reg, and what that type declares -- a field and both kinds of
// method -- promotes through it exactly as a same-package embedding does.
type Framed struct {
	chain.Reg
	F int
}

var framed = Framed{chain.Reg{8}, 9}

type Crate struct{ w int }

func (c *Crate) Area() int { return c.w * c.w }
func (c *Crate) Tag() int  { return c.w + 1 }

var crate = Crate{3}

func area(s greet.Shape) int { return s.Area() }

// dbl has Mapper's signature written with the qualifier main needs; Plusser asks
// for greet.Vec's Plus, which Wrapped has by promotion from the field it embeds --
// so the thunk hands over the embedded sub-object and the struct result comes back
// through the out parameter, across the package boundary both ways.
func dbl(v greet.Vec) greet.Vec { return greet.Vec{v.A * 2, v.B * 2} }

type Plusser interface{ Plus(w greet.Vec) greet.Vec }

type Wrapped struct {
	greet.Vec
	n int
}

var wr = Wrapped{greet.Vec{1, 2}, 0}

func mkShape() greet.Shape { return &quad }

const limit = greet.K + 5

const wide = greet.K * 2

// Package-scope values of an imported type, laid out statically.
var unit = greet.Vec{A: 1, B: 1}
var vecs = []greet.Vec{{9, 9}, {8, 8}}

// Storage for the qualified conversions above. The slice's backing is at package
// scope because a conversion of a local's slice reaches that local, and the
// lifetime rules follow it through the conversion exactly as they do without one.
// A constant and a variable built from another package's STRING constants, which
// is where the fold has to happen at compile time: C evaluates a file-scope
// initializer before there is anything to read.
const banner = greet.Tag + ": " + greet.Prompt

var stored = greet.Tag

var pool = [3]int{9, 0, 0}
var quad = greet.Quad{5, 6}
var sh greet.Shape

// Initialization order across the packages: tally reads greet AFTER the whole
// of greet has initialized, its init() included -- Go's barrier -- and main's
// own init() runs after main's variables and before main().
var tally = greet.Ordered + greet.Adjust

func init() { tally++ }

func initOrder() {
	println(greet.Ordered, greet.Adjust, tally)
	println(greet.Lo, greet.Hi)
	println(greet.Weights[0], greet.Weights[1], greet.Weights[2])
}
// The fixes of 2026-09-15/16 with the code in a SECOND package, in two files:
// the nil guards through qualified names, a package literal holding a slice
// literal and a pointer to one, and the evaluation order of literals, value
// lists and expressions across the boundary. Every probe of them had been one
// package, which is where mangling and the per-file token tables have broken
// before.
func libShapes() {
	println(lib.Order, lib.Cfg.Name, lib.Cfg.Pins[2], lib.Cfg.Dev.Id, lib.Cfg.Dev.Next.Id, lib.Cfg.Rate)
	println(lib.Ptrs[0].Id, lib.Sl[0].Next.Id, lib.Get().Id, lib.Get().Next.Val(), lib.Ptrs[1].Ptr(), lib.Cfg.Dev.Val())
	v := *lib.Get()
	w := *lib.Sl[1]
	println(v.Id, w.Id, lib.D2.Next.Regs[3], lib.Pa[1][2], len(lib.Pa[1]), cap(lib.Pick()), len(lib.Grid[lib.Idx()]), lib.Calls)
	lib.Calls = 0
	lib.Get().Id = 20
	lib.Ptrs[0].Id = 10
	lib.Cfg.Dev.Next.Id = 21
	lib.D2.Next.Regs[0] = 100
	lib.Pa[1][1] = 200
	lib.Cfg.Pins[0] = 7
	println(lib.D2.Id, lib.D1.Id, lib.D2.Next.Regs[0], lib.Pa[1][1], lib.Cfg.Pins[0], lib.Calls)
	c := lib.Make(1, 2)
	o1 := lib.Calls
	lib.Calls = 0
	a, b := lib.F(1), lib.MkA(2)[0]
	o2 := lib.Calls
	lib.Calls = 0
	x := lib.F(1) + lib.MkA(2)[lib.F(3)%2]
	o3 := lib.Calls
	lib.Calls = 0
	cfg := lib.Config{Rate: lib.F(1), Pins: []int{lib.F(2), lib.F(3)}, Dev: lib.Get()}
	o4 := lib.Calls
	lib.Calls = 0
	p, q := lib.Two(lib.F(1))
	o5 := lib.Calls
	println(c.Rate, c.Pins[0], a, b, x, cfg.Rate, cfg.Pins[1], cfg.Dev.Id, p, q, o1, o2, o3, o4, o5)
	if lib.P != nil && lib.P.Id == 1 || lib.None() != nil && lib.None().Id == 2 {
		println("no")
	}
	// Another package's methods as functions, the receiver first: a value method,
	// a pointer one, a value one through the pointer form, one of several results,
	// one bound to a variable of its function type, and one of a defined scalar.
	val := lib.Dev.Val
	gv, gok := (*greet.Gauge).Read(&greet.G1)
	var sumf func(greet.Vec) int = greet.Vec.Sum
	println(val(lib.D1), lib.Dev.Val(lib.D2), (*lib.Dev).Ptr(&lib.D1), (*lib.Dev).Val(lib.Get()), gv, gok, sumf(greet.Vec{A: 1, B: 2}), greet.Celsius.Double(21))
	// Another package's variables as the targets of a list assignment, a
	// destructuring, a swap and a for clause: each was refused as an unsupported
	// target, the whole-variable ones read as the package qualifier.
	lib.Calls = 0
	lib.P, lib.Calls = &lib.D1, lib.F(4)
	lib.Calls, lib.D2.Id = lib.Two(3)
	println(lib.P.Id, lib.Calls, lib.D2.Id)
	lib.D1.Id, lib.D2.Id = lib.D2.Id, lib.D1.Id
	for lib.Calls = 0; lib.Calls < 2; lib.Calls, lib.D1.Id = lib.Calls+1, lib.D1.Id+1 {
	}
	println(lib.D1.Id, lib.D2.Id, lib.Calls)
	// A function literal's signature naming another package's types: the literal is
	// checked in a scope hanging off the file's, where the import itself was taken
	// for a name shadowing it -- "lib (package name) is not a type".
	byID := func(d *lib.Dev) int { return d.Id }
	println(byID(&lib.D1), func(d lib.Dev) int { return d.Id + 1 }(lib.D2))
	// Aliases across the boundary, this package's and that one's, through a
	// declaration, a literal and a method call: "type LD = lib.Dev" was refused,
	// and "lib.DevAlias{...}" was no struct.
	var ld LD = lib.D1
	println(ld.Val(), lib.DevAlias{Id: 5}.Val(), LD{Id: 8}.Val(), byID(&ld))
	// A conversion to another package's pointer type, by its name and by aliases.
	println((*lib.Dev)(nil) == nil, (*lib.Dev)(&lib.D2).Ptr(), (*LD)(&ld).Val(), (*lib.DevAlias)(&ld).Ptr())
	// Types used above their declaration, in another file of their package.
	var sp lib.Span
	sp[1] = 5
	println(lib.Early.A+lib.Early.B, sp[1])
	lib.P = nil
	println(lib.P == nil, lib.None() == nil)
}
`,
	"initord/itrace/itrace.ogo": `var Trace int

func Mark(k int) int {
	Trace = Trace*10 + k
	return k
}

var A1 = Mark(1)
var A2 = A1 + Mark(2)

func init() {
	Mark(3)
}

func init() {
	Mark(4)
}
`,
	"initord/ideps/ideps.ogo": `import "initord/itrace"

var B2 = itrace.Mark(6) + B1

var B1 = itrace.Mark(5) + itrace.A2

func init() {
	itrace.Mark(7)
}

func Sum() int {
	return B1 + B2
}
`,
	"chain/chain.ogo": `import "greet"

// chain is imported BY main and imports greet itself, so the build walks two
// levels. greet is not under chain/ -- an import names a directory of the build
// root, whichever package writes it.
func Via(n int) int { return greet.Twice(n) + 1 }

// Big and Huge are what main calls methods on across the boundary: a 64-bit
// constant of an imported type, inlined at each use rather than declared.
type Big int64

const Huge Big = 5 << 40

func (b Big) Int() int { return int(b >> 40) }

func (b Big) Twice() Big { return b * 2 }

func Sum(a, b Big) Big { return a + b }

// Label and Unit are the string counterparts: a string constant of an imported
// defined type, called methods on across the boundary and bound to a local.
type Label string

const Unit Label = "mm"

func (l Label) Len() int { return len(l) }

func (l Label) Upper() Label {
	if l == "mm" {
		return "MM"
	}
	return l
}

// Temp, Boil, G, Two and K32 are the float counterparts: a float constant of an
// imported defined type, an untyped one, an integral one, a float32 one.
type Temp float64

const Boil Temp = 100.5
const G = 9.75
const Two = 2.0
const K32 float32 = 0.75

func (t Temp) Half() Temp { return t / 2 }

func (t Temp) Int() int { return int(t) }

// Table is an exported lookup table: an ARRAY variable a method is called on an
// element of, across the boundary.
var Table = [2]Temp{-2.5, Boil}

// Ints, Sl and Tag are what THIS package reaches into from inside itself -- an
// array, a slice over it, a string -- each of which named an unmangled symbol
// outside main, where the empty prefix made the source name the C name by
// accident. main reads and writes the same array across the boundary.
var Ints = [4]int{10, 20, 30, 40}
var Sl = Ints[:]
var Tag = "abcd"

// Reg, Kit, Shim and Own are this package's own symbols used from INSIDE it in
// the two positions that named an unmangled C symbol outside main: a struct
// variable as a method's receiver, and a function taken as a value.
type Reg struct{ N int }

func (r Reg) Twice() int { return r.N * 2 }

func (r *Reg) Inc() { r.N++ }

var Kit = Reg{4}

// Grid is printed WHOLE from main, which used to print its address.
var Grid = [2]int{8, 9}

// Panel, Lit and Lamp are the cross-package type-identity shapes: a method and a
// field PROMOTED from an embedded type, an interface parameter of an imported
// function, and a pointer variable bound to a value of an imported type.
type Panel struct {
	Reg
	Tag int
}

type Lit interface {
	Watts() int
}

type Lamp struct{ W int }

func (l *Lamp) Watts() int { return l.W }

func Draw(a Lit, b Lit) int { return a.Watts() + b.Watts() }

// Bulb is this package's own variable, handed to its own interface BY ADDRESS
// from main, where the root of that address is the package qualifier rather than
// a variable of the importing package.
var Bulb = Lamp{9}

// Deck is a package-level value of an embedding type, for a PROMOTED method taken
// as a value from outside this package.
var Deck = Panel{Reg{2}, 3}

func Shim(v int) int { return v + 1 }

func Apply(f func(int) int, v int) int { return f(v) }

func Own() int {
	f := Shim
	Kit.Inc()
	m := Kit.Inc
	m()
	return Apply(f, 3) + Kit.Twice()
}

// offErr and ErrOff are the error shapes across a boundary: a concrete type whose
// Error method makes it an error, and the exported SENTINEL a caller compares
// against. With no heap an error value is a pointer to a package variable, so an
// exported one is what a package has instead of errors.New.
type offErr struct{ N int }

func (e *offErr) Error() string { return "chain: off" }

func (e *offErr) Pin() int { return e.N }

var ErrOff = offErr{7}

func Power(on bool) (int, error) {
	if !on {
		return 0, &ErrOff
	}
	return 12, nil
}

// Cur, Cap and Pair are what a switch CASE names across a boundary: a variable, a
// constant and a call. Only the constant folded before, so the other two reported
// their own qualifier undefined.
var Cur int

const Cap = 7

func Pair() int { return 2 }

func InSum() int {
	n := 0
	for _, x := range Ints {
		n += x
	}
	Ints[0] = 11
	xs := Ints[:2]
	var dst [2]int
	copy(dst[:], Sl[1:3])
	return n + Ints[0] + len(xs) + Sl[3] + int(Tag[1]) + len(Tag[1:]) + dst[0] + Table[1].Int()
}

// Mask is a 64-bit type of this package that main shifts untyped constants into.
// Such a constant takes its type from where it stands, and a type of ANOTHER
// package is one the checker's Kind model does not follow, so every one of these
// positions shifted a 32-bit 1 and lost the bit. Top reads Width, declared below it.
type Mask uint64

var Flags Mask

var Top int64 = 1 << Width

var Width uint = 45

func Set(bit uint) { Flags |= 1 << bit }

func Bit(n uint) Mask { return 1 << n }
`,
	"greet/greet.ogo": `// Ordered reads Adjust through scaleUp, so it is initialized after both,
// wherever the three are written -- dependency order runs through a function's
// body. init() runs after this package's variables and before anything of an
// importing package.
var Lo, Hi = pairs()

func pairs() (int, int) { return baseVal * 3, baseVal + 40 }

var Ordered = baseVal * scaleUp()
var baseVal = 2

// A package slice whose elements are not constant, read across the package
// boundary: its backing is filled at initialization and ordered against
// baseVal and Adjust, the same as a scalar package variable.
var Weights = []int{baseVal, Adjust, baseVal * 3}

var Adjust = 7

func scaleUp() int { return Adjust + 3 }

func init() { Adjust = Adjust + 100 }

// Relay and Ack are this package's channels, used by whoever imports it. With no
// heap there is nothing for a constructor to return, so a package-level channel is
// how two packages come to share one -- the ordinary spelling here rather than the
// exotic one it would be in Go.
var Relay chan int
var Ack chan int

// Answer runs on a COG started by another package and rendezvouses on this
// package's channels, in both directions.
func Answer() {
v := <-Relay
Ack <- v * 3
}

type Point struct{ x, y int }

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

// Tag and Prompt are exported STRING constants, read from main as values, indexed,
// sliced, compared, concatenated and folded into a constant of main's own.
const Tag = "greet"

const Prompt = "hi"

// FromLoud reads a variable declared in loud.ogo, the package's other file.
var FromLoud = Quiet + 1

// Vec is an exported type with exported fields and an exported method, used from
// main through a var declaration (var v greet.Vec).
type Vec struct {
A int
B int
}

func (v Vec) Sum() int { return v.A + v.B }

// Plus names this package's own Vec, unqualified, in its signature; Mapper is a
// function type over it and Apply takes one. From main both are written with the
// qualifier, "func(greet.Vec) greet.Vec", and the checker used to compare the two
// spellings as text and refuse every use -- see sigIdentity.
func (v Vec) Plus(w Vec) Vec { return Vec{v.A + w.A, v.B + w.B} }

type Mapper func(Vec) Vec

func Apply(m Mapper, v Vec) Vec { return m(v) }

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

// Celsius, Row, L, Shape and Quad exist so main can spell a QUALIFIED conversion
// to each kind of target a conversion has: a defined scalar carrying a method, a
// defined array, a defined slice, and an interface with something implementing it.
type Celsius int

func (c Celsius) Double() int { return int(c) * 2 }

type Row [2]int

// Sum and Fill are for main to use Row as a TYPE, not only as a conversion: a
// variable, a parameter, a result, a field, a literal and a pointer of it.
func (r *Row) Sum() int { return r[0] + r[1] }

func Fill(v int) Row { return Row{v, v + 1} }

type L []int

type Shape interface {
Area() int
}

type Quad struct {
W int
H int
}

func (q *Quad) Area() int { return q.W * q.H }

// Sizer is a second interface with the same method set, for a type switch case
// naming an imported INTERFACE rather than an imported concrete type.
type Sizer interface {
Area() int
}

// Parameters named like main's constants (K, prefix): what a name means is the
// local, not the constant a fold would find under it.
func Tail(s, prefix string) string { return s[len(prefix):] }

func Wide(K int) int64 { return int64(K) * 2 }

// A method of SEVERAL results behind an interface, with a package-level array of
// that interface for another package to read it through: greet.Meters[0].Read()
// is a chain whose head is an import QUALIFIER, which no type walk here answers
// for -- only the renderer does.
type Reader interface {
	Read() (int, bool)
}

type Gauge struct {
	V  int
	Up bool
}

func (g *Gauge) Read() (int, bool) {
	if !g.Up {
		return 0, false
	}
	return g.V * 2, true
}

var Meters [2]Reader

var G1 = Gauge{V: 7, Up: true}

// Constants and bounds naming constants of loud.ogo, which the checker reaches after
// this file: each was evaluated through THIS file's tokens, and panicked.
const Slots = Frame*2 + 1

const Model = Family + "-7"

const Gain float32 = Step * 3

type Buf [Frame]byte

func BufLen() int {
	var b Buf
	b[7] = 2
	return len(b) + int(b[7])
}

func FrameCap(a [Frame * 2]uint8) int { return len(a) + int(a[0]) }
`,
	"greet/loud.ogo": `// Quiet is read from greet.ogo and Doubled reads Total from it, so whichever of
// the two files is emitted first, one of them names a variable it has not seen.
var Quiet = 3

var Doubled = Total * 2

// Named by greet.ogo's constants, from the file checked first.
const Frame = 1 << 3

const Family = "gx"

const Step = 0.25 / 2

func Loud(s string) string {
if len(s) > 0 {
	return "LOUD"
}
return s
}
`,
	"lib/lib.ogo": `type Dev struct {
	Id   int
	Next *Dev
	Regs *[4]int
}

// DevAlias is another name for Dev, for main to name through the qualifier.
type DevAlias = Dev

func (d Dev) Val() int { return d.Id }

func (d *Dev) Ptr() int { return d.Id + 100 }

type Config struct {
	Name string
	Pins []int
	Dev  *Dev
	Rate int
}

var Calls int

func F(n int) int {
	Calls = Calls*10 + n
	return n
}

var regs = [4]int{1, 2, 3, 4}

var D1 = Dev{Id: 1, Regs: &regs}

var D2 = Dev{Id: 2, Next: &D1}

var P *Dev

var Ptrs = [2]*Dev{&D1, &D2}

var Sl = []*Dev{&D2, &D1}

var Cfg = Config{Name: "p2", Pins: []int{F(1), F(2), 9}, Dev: &Dev{Id: 3, Next: &D2}, Rate: F(3)}

var Order = Calls

func Get() *Dev { return &D2 }

func None() *Dev { return nil }

// A variable of a type another file of this package declares, and a type over one.
var Early = Late{A: 3, B: 4}

type Span [2]Width
`,
	"lib/more.ogo": `var Grid [3][5]int

var Pa = [2]*[4]int{nil, &regs}

func Idx() int {
	Calls++
	return 1
}

func Pick() *[4]int {
	Calls += 10
	return &regs
}

func MkA(n int) [2]int {
	Calls = Calls*10 + n
	return [2]int{n, n + 1}
}

var back = [4]int{5, 6, 7, 8}

func Make(a, b int) Config {
	return Config{Rate: F(a), Pins: back[:F(b)]}
}

func Two(n int) (int, int) {
	Calls = Calls*10 + n
	return n, n + 1
}

type Late struct {
	A, B int
}

type Width int
`,
}

// TestMultiPkgRefusals pins the two things a refusal must get right when the
// program crosses a package boundary: that the rule fires there at all, and that
// it names the variable as the program spells it. A store's provenance check is
// the rule -- a package variable outlives every frame, whichever package declares
// it -- and the name reaches the emitter already mangled, so an unguarded message
// would report `geo_Sl` of a program that says `geo.Sl`.
func TestMultiPkgRefusals(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "a frame-backed slice stored in another package's variable",
			files: map[string]string{
				"geo/geo.ogo": "var Sl []int\n",
				"main.ogo": `import "geo"

func fill() {
	var back [4]int
	geo.Sl = back[:]
}

func main() {
	fill()
	println(len(geo.Sl))
}
`,
			},
			want: "cannot store a slice backed by local back in package variable geo.Sl",
		},
		{
			name: "a value where another package's interface is wanted",
			files: map[string]string{
				"geo/geo.ogo": `type Shape interface {
	Area() int
}

type Sq struct{ S int }

func (s *Sq) Area() int { return s.S * s.S }

func Total(a Shape) int { return a.Area() }
`,
				"main.ogo": `import "geo"

func main() {
	s := geo.Sq{2}
	println(geo.Total(s))
}
`,
			},
			want: "cannot use s (variable of type geo.Sq) as geo.Shape value in argument to Total: an interface holds a pointer here; write &s",
		},
		{
			name: "a type that does not implement another package's interface",
			files: map[string]string{
				"geo/geo.ogo": `type Shape interface {
	Area() int
}

func Total(a Shape) int { return a.Area() }
`,
				"main.ogo": `import "geo"

type Blob struct{ N int }

func main() {
	b := Blob{2}
	p := &b
	println(geo.Total(p))
}
`,
			},
			want: "Blob does not implement geo.Shape (missing method Area)",
		},
		{
			name: "another package's unexported field",
			files: map[string]string{
				"geo/geo.ogo": `type S struct {
	Open   int
	hidden int
}

var V = S{1, 2}
`,
				"main.ogo": `import "geo"

func main() {
	println(geo.V.hidden)
}
`,
			},
			want: "cannot refer to unexported field hidden of type geo.S",
		},
		{
			name: "another package's unexported method",
			files: map[string]string{
				"geo/geo.ogo": `type S struct{ N int }

func (s S) hidden() int { return s.N }

var V = S{1}
`,
				"main.ogo": `import "geo"

func main() {
	println(geo.V.hidden())
}
`,
			},
			want: "cannot refer to unexported method hidden of type geo.S",
		},
		{
			name: "assigning to another package's constant",
			files: map[string]string{
				"geo/geo.ogo": "const K = 1\n",
				"main.ogo": `import "geo"

func main() {
	geo.K = 2
	println(geo.K)
}
`,
			},
			want: "cannot assign to geo.K",
		},
		{
			name: "another package's defined type is not its underlying one",
			files: map[string]string{
				"geo/geo.ogo": `type T int

var V T = 1
`,
				"main.ogo": `import "geo"

func main() {
	var x int = geo.V
	println(x)
}
`,
			},
			want: "cannot use geo.V of type geo.T as type int in variable declaration",
		},
		{
			name: "a method on another package's type",
			files: map[string]string{
				"geo/geo.ogo": "type Pt struct{ X int }\n",
				"main.ogo": `import "geo"

func (p geo.Pt) twice() int { return p.X * 2 }

func main() { var q geo.Pt; println(q.X) }
`,
			},
			want: "cannot define new methods on non-local type geo.Pt",
		},
		{
			name: "embedding another package's unexported struct",
			files: map[string]string{
				"geo/geo.ogo": "type pt struct{ X int }\n",
				"main.ogo": `import "geo"

type M struct {
	geo.pt
	N int
}

func main() { var m M; println(m.N) }
`,
			},
			want: "undefined: geo.pt",
		},
		{
			name: "a method value of another package's variable with a value receiver",
			files: map[string]string{
				"geo/geo.ogo": `type Pt struct{ X int }

func (p Pt) Get() int { return p.X }

var V = Pt{3}
`,
				"main.ogo": `import "geo"

func main() {
	m := geo.V.Get
	println(m())
}
`,
			},
			want: "cannot take geo.V.Get as a value: a method value copies its receiver",
		},
		{
			name: "a method value of another package's unexported method",
			files: map[string]string{
				"geo/geo.ogo": `type Pt struct{ X int }

func (p *Pt) bump() int { p.X++; return p.X }

var V = Pt{3}
`,
				"main.ogo": `import "geo"

func main() {
	m := geo.V.bump
	println(m())
}
`,
			},
			want: "cannot refer to unexported method bump of type geo.Pt",
		},
		{
			name: "embedding another package's unexported interface",
			files: map[string]string{
				"geo/geo.ogo": `type reader interface {
	Read() int
}
`,
				"main.ogo": `import "geo"

type W interface {
	geo.reader
	Close() int
}

func main() { var w W; println(w == nil) }
`,
			},
			want: "undefined: geo.reader",
		},
		{
			name: "embedding another package's non-interface",
			files: map[string]string{
				"geo/geo.ogo": "type S struct{ n int }\n",
				"main.ogo": `import "geo"

type W interface {
	geo.S
	Close() int
}

func main() { var w W; println(w == nil) }
`,
			},
			want: "cannot embed geo.S in an interface: it is not an interface type",
		},
		{
			name: "the address of another package's constant",
			files: map[string]string{
				"geo/geo.ogo": "const K = 7\n",
				"main.ogo": `import "geo"

func main() {
	p := &geo.K
	println(*p)
}
`,
			},
			want: "invalid operation: cannot take address of geo.K",
		},
		{
			name: "a local shadowing an import",
			files: map[string]string{
				"geo/geo.ogo": "func Take(n int) int { return n }\n",
				"main.ogo": `import "geo"

func main() {
	geo := 1
	println(geo.Take(2))
}
`,
			},
			want: "type int has no method Take",
		},
		{
			name: "a field of another package's scalar",
			files: map[string]string{
				"geo/geo.ogo": "var N = 7\n",
				"main.ogo": `import "geo"

func main() {
	println(geo.N.X)
}
`,
			},
			want: "geo.N has no field X",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for name, src := range test.files {
				fsys[name] = &fstest.MapFile{Data: []byte(src)}
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			if err == nil {
				t.Fatalf("expected a refusal mentioning %q, but the program compiled", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error:\n got %v\nwant it to contain %q", err, test.want)
			}
		})
	}
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
	out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra",
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

// TestCrossFileConsts runs main packages of two files, a.ogo checked first, whose
// constants and array bounds name constants declared in b.ogo. The checker evaluated
// such a constant through the token table of the file that asked for it, which
// indexes a different expression: every one of these panicked in the checker or was
// refused -- "constant definition cycle" for a string, and "constant 700 overflows
// uint8" where a.ogo's 7 stood at the index of b.ogo's 2 -- and the other file order
// compiled, the constant being resolved by then. Each output is Go's.
func TestCrossFileConsts(t *testing.T) {
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
	for _, test := range []struct{ name, a, b, want string }{
		{
			name: "an integer constant",
			a:    "const A = B + 1\n\nfunc main() {\n\tprintln(A)\n}\n",
			b:    "const B = 2 * 3\n",
			want: "7\n",
		},
		{
			name: "a variable's array bound",
			a:    "var arr [N]int\n\nfunc main() {\n\tarr[N-1] = 9\n\tprintln(len(arr), arr[4])\n}\n",
			b:    "const N = 2 + 3\n",
			want: "5 9\n",
		},
		{
			name: "a defined array type's bound",
			a:    "type T [N]int\n\nfunc main() {\n\tvar t T\n\tt[1] = 4\n\tprintln(len(t), t[1])\n}\n",
			b:    "const N = 2 + 3\n",
			want: "5 4\n",
		},
		{
			name: "a struct field's bound",
			a:    "type S struct {\n\ta [N]int\n}\n\nfunc main() {\n\tvar s S\n\ts.a[2] = 6\n\tprintln(len(s.a), s.a[2])\n}\n",
			b:    "const N = 2 + 3\n",
			want: "5 6\n",
		},
		{
			name: "a signature's bound",
			a:    "func f(a [N]int) int { return len(a) + a[0] }\n\nfunc main() {\n\tvar a [5]int\n\ta[0] = 2\n\tprintln(f(a))\n}\n",
			b:    "const N = 2 + 3\n",
			want: "7\n",
		},
		{
			name: "a signature's bound beside a parameter named like its operand",
			a:    "func f(C int, a [B]int) int { return len(a) + C + a[1] }\n\nfunc main() {\n\tvar a [2]int\n\ta[1] = 5\n\tprintln(f(10, a), C)\n}\n",
			b:    "const B = C + 1\n\nconst C = 1\n",
			want: "17 1\n",
		},
		{
			name: "a method's receiver type",
			a:    "type T struct {\n\tv [N]int\n}\n\nfunc (t *T) Len() int { return len(t.v) + t.v[0] }\n\nfunc main() {\n\tvar t T\n\tt.v[0] = 1\n\tprintln(t.Len())\n}\n",
			b:    "const N = 1 << 3\n",
			want: "9\n",
		},
		{
			name: "an iota group",
			a:    "const Z = Y + 1\n\nfunc main() {\n\tprintln(Z)\n}\n",
			b:    "const (\n\tX = iota * 2\n\tY\n)\n",
			want: "3\n",
		},
		{
			name: "a group spec repeating an expression",
			a:    "const (\n\tA = B + iota\n\tA2\n)\n\nfunc main() {\n\tprintln(A, A2)\n}\n",
			b:    "const B = 10 - 3\n",
			want: "7 8\n",
		},
		{
			name: "a typed constant",
			a:    "const Q = T8 * 2\n\nfunc main() {\n\tprintln(Q)\n}\n",
			b:    "const T8 uint8 = 1 << 3\n",
			want: "16\n",
		},
		{
			name: "a string constant",
			a:    "const S = P + \"x\"\n\nfunc main() {\n\tprintln(S)\n}\n",
			b:    "const P = \"a\" + \"b\"\n",
			want: "abx\n",
		},
		{
			name: "a float32 constant",
			a:    "const F float32 = E * 2\n\nfunc main() {\n\tprintln(F == 1)\n}\n",
			b:    "const E = 1.5 / 3\n",
			want: "true\n",
		},
		{
			// b.ogo's 2 sits at the index of a.ogo's 7, which is what was folded.
			name: "a range check of the value the constant has",
			a:    "const A = B * 100\n\nconst X = 7 + 8 + 9 + 10\n\nvar u8 uint8 = A\n\nfunc main() {\n\tprintln(A, X, u8, arr[0])\n}\n",
			b:    "var arr [2]int\n\nconst B = 2\n",
			want: "200 34 200 0\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"a.ogo": &fstest.MapFile{Data: []byte(test.a)},
				"b.ogo": &fstest.MapFile{Data: []byte(test.b)},
			}
			pkg, err := Build(-1, []string{"a.ogo", "b.ogo"}, fsys)
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
			out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra",
				"-Wno-unused-function", "-Wno-format", "-I", shim, "-o", bin, csrc, "-lpthread", "-lm").CombinedOutput()
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
			if g := strings.ReplaceAll(string(got), "\r\n", "\n"); g != test.want {
				t.Errorf("output:\n got %q\nwant %q\n--- emitted ---\n%s", g, test.want, buf.String())
			}
		})
	}
}
