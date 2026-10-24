// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
)

// f64Part is what genF64 drew: the values of the package variables as the program
// starts (init) and as it runs (vals), the locals' values, the calls of the
// counting function and what the procedure comes to.
type f64Part struct {
	r     *rand.Rand
	init  map[string]float64
	vals  map[string]float64
	calls int32
	res   int32
	ints  map[string]int64 // fdI_1, an int64, fdN_1, an int32, fdU_1, a uint32
	f32   float32          // fdF_1
	f32_0 float32
}

// fdVars are the float64 package variables, fdP_1 the elements of its array.
var fdVars = []string{"fdA_1", "fdB_1", "fdC_1", "fdD_1", "fdP_1[0]", "fdP_1[1]", "fdP_1[2]"}

// fdLocals are the procedure's locals, and *fdq the pointer it holds to fdB_1.
var fdLocals = []string{"fa", "fb"}

// genF64 writes a procedure of float64 arithmetic at a float64's whole precision,
// which the target computes in software (the emitter's float64.go): sums,
// differences, products and quotients of package variables, locals, an element, a
// pointer's target, a call's result and constants, with the values drawn across the
// whole range -- subnormals, the neighbours of 2^53, long mantissas and short
// decimals no float holds exactly -- the conversions to and from the integer types
// and float32, the comparisons, compound assignments, and the math functions whose
// results are exact: Sqrt, Abs, Floor, Ceil and Trunc. Every value is what Go
// computes, in Go, each operation converted on its own so no two of them fuse, and
// the part draws from its own source (fdRand), so the rest of a seed's program is
// what it was.
func (f *Fuzzer) genF64() (call string, want Int32, ok bool) {
	if !f.fdOn {
		return "", 0, false
	}
	p := &f64Part{r: f.fdRand, init: map[string]float64{}, vals: map[string]float64{}, ints: map[string]int64{}}
	for _, n := range fdVars {
		p.init[n] = p.value()
	}
	for k, v := range p.init {
		p.vals[k] = v
	}
	p.ints["fdI_1"] = p.int64Val()
	p.ints["fdN_1"] = int64(int32(p.r.Uint32()))
	p.ints["fdU_1"] = int64(p.r.Uint32())
	p.f32_0 = float32(p.value())
	if math.IsInf(float64(p.f32_0), 0) || p.f32_0 == 0 {
		p.f32_0 = 1.5 // spelled as a constant: finite, and no negative zero
	}
	p.f32 = p.f32_0
	for _, l := range fdLocals {
		p.vals[l] = p.value()
	}

	var b strings.Builder
	for _, l := range fdLocals {
		fmt.Fprintf(&b, "\tvar %s float64 = %s\n", l, fdLit(p.vals[l]))
	}
	b.WriteString("\tfdq := &fdB_1\n")
	// math is imported for this part, so it is read whatever the draws.
	p.vals["fb"] = math.Sqrt(math.Abs(p.vals["fb"]))
	b.WriteString("\tfb = math.Sqrt(math.Abs(fb))\n")
	for i, n := 0, 6+p.r.Intn(9); i < n; i++ {
		switch p.r.Intn(12) {
		case 0, 1, 2:
			p.assign(&b)
		case 3, 4:
			p.compound(&b)
		case 5, 6:
			p.compare(&b)
		case 7:
			p.toInt(&b)
		case 8:
			p.viaFloat32(&b)
		case 9:
			p.check(&b, p.target())
		default:
			p.assign(&b)
		}
	}
	for _, t := range append(append([]string{}, fdVars...), append(fdLocals, "*fdq")...) {
		p.check(&b, t)
	}
	b.WriteString("\tr = r*31 + fdCalls_1\n")
	p.res = p.res*31 + p.calls
	p.write(f, b.String())
	return "fdRun_1()", Int32(p.res), true
}

// value draws a float64: an integer, a short decimal, a long mantissa at a
// moderate exponent, one near 2^53 or near 1, a subnormal, a power of two, or any
// finite one. A zero is positive: it is spelled as a constant, and a constant has
// no negative zero.
func (p *f64Part) value() float64 {
	if v := p.value1(); v != 0 {
		return v
	}
	return 0
}

func (p *f64Part) value1() float64 {
	r := p.r
	sign := 1.0
	if r.Intn(3) == 0 {
		sign = -1
	}
	switch r.Intn(9) {
	case 0:
		return sign * float64(r.Intn(2001))
	case 1:
		return sign * float64(r.Intn(200001)) / 1000
	case 2:
		return sign * math.Float64frombits(uint64(1023-60+r.Intn(121))<<52|r.Uint64()&(1<<52-1))
	case 3:
		return sign * float64(1<<53+r.Intn(9)-4)
	case 4:
		return sign * (1 + float64(r.Intn(9)-4)*0x1p-52)
	case 5:
		return sign * math.Float64frombits(r.Uint64()&(1<<52-1)) // a subnormal
	case 6:
		return sign * math.Ldexp(1, r.Intn(2098)-1074)
	case 7:
		return sign * math.Float64frombits(uint64(1+r.Intn(2046))<<52|r.Uint64()&(1<<52-1))
	}
	return sign * math.Float64frombits(uint64(1023-20+r.Intn(41))<<52|r.Uint64()&(1<<52-1))
}

// int64Val draws an int64 a float64 holds or rounds: past 2^53 most often.
func (p *f64Part) int64Val() int64 {
	r := p.r
	switch r.Intn(3) {
	case 0:
		return int64(r.Intn(2000001) - 1000000)
	case 1:
		return int64(1<<53+r.Intn(17)-8) * int64(1-2*r.Intn(2))
	}
	return int64(r.Uint64())
}

// fdLit spells v as a constant that is v exactly: the shortest decimal that reads
// back to it, or now and then its hexadecimal form; a negative one parenthesised.
func fdLit(v float64) string {
	s := strconv.FormatFloat(math.Abs(v), 'g', -1, 64)
	if int(math.Float64bits(v))%8 == 3 {
		s = strconv.FormatFloat(math.Abs(v), 'x', -1, 64)
	}
	if math.Signbit(v) {
		return "(-" + s + ")"
	}
	return s
}

// fdExpr is an expression genF64 wrote: its text, its value and whether it is a
// constant, which Go folds exactly and converts to a float64 once, so no operation
// joins two of them.
type fdExpr struct {
	text string
	v    float64
	k    bool
}

// fdOp is the float64 Go computes for a op b, rounded on its own: the conversion is
// what keeps an operation from fusing with the next, which Go's spec allows.
func fdOp(op byte, a, b float64) float64 {
	switch op {
	case '+':
		return float64(a + b)
	case '-':
		return float64(a - b)
	case '*':
		return float64(a * b)
	}
	return float64(a / b)
}

// leaf draws an operand: a variable, a local, a pointer's target, a constant, a
// conversion of an integer or of a float32 variable.
func (p *f64Part) leaf() fdExpr {
	r := p.r
	switch r.Intn(9) {
	case 0, 1:
		n := fdVars[r.Intn(len(fdVars))]
		return fdExpr{text: n, v: p.vals[n]}
	case 2:
		n := fdLocals[r.Intn(len(fdLocals))]
		return fdExpr{text: n, v: p.vals[n]}
	case 3:
		return fdExpr{text: "*fdq", v: p.vals["fdB_1"]}
	case 4, 5:
		v := p.value()
		if r.Intn(2) == 0 {
			// An integer constant, which a float64 operand converts, past 2^53 too.
			k := int64(r.Intn(4001) - 2000)
			if r.Intn(3) == 0 {
				k = 1<<53 + int64(r.Intn(9))
			}
			s := strconv.FormatInt(k, 10)
			if k < 0 {
				s = "(" + s + ")"
			}
			return fdExpr{text: s, v: float64(k), k: true}
		}
		return fdExpr{text: fdLit(v), v: v, k: true}
	case 6:
		n := []string{"fdI_1", "fdN_1", "fdU_1"}[r.Intn(3)]
		return fdExpr{text: "float64(" + n + ")", v: float64(p.ints[n])}
	case 7:
		return fdExpr{text: "float64(fdF_1)", v: float64(p.f32)}
	}
	n := fdVars[r.Intn(len(fdVars))]
	return fdExpr{text: n, v: p.vals[n]}
}

// expr draws an expression of float64s, depth levels deep at most.
func (p *f64Part) expr(depth int) fdExpr {
	r := p.r
	if depth == 0 || r.Intn(3) == 0 {
		return p.leaf()
	}
	switch r.Intn(10) {
	case 0:
		x := p.expr(depth - 1)
		v := -x.v
		if x.k && v == 0 {
			v = 0 // a constant's negation, folded: no negative zero
		}
		return fdExpr{text: "-" + p.paren(x), v: v, k: x.k}
	case 1:
		x := p.expr(depth - 1)
		p.calls++
		return fdExpr{text: "fdId_1(" + x.text + ")", v: x.v}
	case 2:
		x := p.nonConst(depth - 1)
		fn := []string{"Sqrt", "Abs", "Floor", "Ceil", "Trunc"}[r.Intn(5)]
		var v float64
		switch fn {
		case "Sqrt":
			v = math.Sqrt(x.v)
		case "Abs":
			v = math.Abs(x.v)
		case "Floor":
			v = math.Floor(x.v)
		case "Ceil":
			v = math.Ceil(x.v)
		default:
			v = math.Trunc(x.v)
		}
		return fdExpr{text: "math." + fn + "(" + x.text + ")", v: v}
	}
	l := p.expr(depth - 1)
	rt := p.expr(depth - 1)
	if l.k && rt.k {
		rt = p.nonConst(depth - 1)
	}
	op := "+-*/"[r.Intn(4)]
	if op == '/' && rt.k && rt.v == 0 {
		op = '+' // a constant zero divisor is refused, as Go refuses it
	}
	return fdExpr{text: "(" + l.text + " " + string(op) + " " + rt.text + ")", v: fdOp(op, l.v, rt.v)}
}

// nonConst is an expression that is not a constant.
func (p *f64Part) nonConst(depth int) fdExpr {
	for {
		if x := p.expr(depth); !x.k {
			return x
		}
	}
}

func (p *f64Part) paren(x fdExpr) string {
	if strings.HasPrefix(x.text, "(") || !strings.ContainsAny(x.text, " -") {
		return x.text
	}
	return "(" + x.text + ")"
}

// target draws what a statement stores into: a variable, an element, a local or
// the pointer's target.
func (p *f64Part) target() string {
	switch p.r.Intn(4) {
	case 0:
		return fdLocals[p.r.Intn(len(fdLocals))]
	case 1:
		return "*fdq"
	}
	return fdVars[p.r.Intn(len(fdVars))]
}

func fdKey(t string) string {
	if t == "*fdq" {
		return "fdB_1"
	}
	return t
}

func (p *f64Part) assign(b *strings.Builder) {
	t := p.target()
	x := p.nonConst(3)
	fmt.Fprintf(b, "\t%s = %s\n", t, x.text)
	p.vals[fdKey(t)] = x.v
}

func (p *f64Part) compound(b *strings.Builder) {
	t := p.target()
	x := p.expr(2)
	op := "+-*/"[p.r.Intn(4)]
	if op == '/' && x.k && x.v == 0 {
		op = '-'
	}
	fmt.Fprintf(b, "\t%s %c= %s\n", t, op, x.text)
	p.vals[fdKey(t)] = fdOp(op, p.vals[fdKey(t)], x.v)
}

// compare writes a comparison whose outcome the generator knows, NaN's included.
func (p *f64Part) compare(b *strings.Builder) {
	l, r := p.nonConst(2), p.expr(2)
	if p.r.Intn(2) == 0 {
		l, r = r, l
	}
	op := []string{"<", "<=", ">", ">=", "==", "!="}[p.r.Intn(6)]
	var t bool
	switch op {
	case "<":
		t = l.v < r.v
	case "<=":
		t = l.v <= r.v
	case ">":
		t = l.v > r.v
	case ">=":
		t = l.v >= r.v
	case "==":
		t = l.v == r.v
	default:
		t = l.v != r.v
	}
	k := int32(p.r.Int31())
	fmt.Fprintf(b, "\tif %s %s %s {\n\t\tr ^= %d\n\t}\n", l.text, op, r.text, k)
	if t {
		p.res ^= k
	}
}

// toInt folds a conversion to an integer type into r, where Go defines it. An
// expression it does not write takes back the calls it counted.
func (p *f64Part) toInt(b *strings.Builder) {
	calls := p.calls
	x := p.nonConst(2)
	a := math.Abs(x.v)
	switch {
	case math.IsNaN(x.v) || math.IsInf(x.v, 0):
		p.calls = calls
	case a < 1<<31 && p.r.Intn(2) == 0:
		fmt.Fprintf(b, "\tr = r*31 + int(%s)\n", x.text)
		p.res = p.res*31 + int32(x.v)
	case a < 1<<63 && p.r.Intn(2) == 0:
		fmt.Fprintf(b, "\tr = r*31 + int(int32(int64(%s)>>20))\n", x.text)
		p.res = p.res*31 + int32(int64(x.v)>>20)
	case x.v >= 0 && x.v < 1<<32:
		fmt.Fprintf(b, "\tr = r*31 + int(uint32(%s)>>1)\n", x.text)
		p.res = p.res*31 + int32(uint32(x.v)>>1)
	default:
		p.calls = calls
	}
}

// viaFloat32 rounds a float64 to the float32 variable and reads it back: one a
// float32 holds, Go leaving the result of one past its range to the implementation.
func (p *f64Part) viaFloat32(b *strings.Builder) {
	calls := p.calls
	x := p.nonConst(2)
	if math.Abs(x.v) > math.MaxFloat32 {
		p.calls = calls
		return
	}
	f := float32(x.v)
	fmt.Fprintf(b, "\tfdF_1 = float32(%s)\n", x.text)
	p.f32 = f
	t := p.target()
	fmt.Fprintf(b, "\t%s = float64(fdF_1)\n", t)
	p.vals[fdKey(t)] = float64(f)
}

// check asserts what t holds: its value against the constant that is it, and a
// zero's sign; a NaN unequal to itself, an infinity past the largest float64. The
// generator expects the value, so the xor is not in the result it predicts.
func (p *f64Part) check(b *strings.Builder, t string) {
	v := p.vals[fdKey(t)]
	k := int32(p.r.Int31())
	switch {
	case math.IsNaN(v):
		fmt.Fprintf(b, "\tif %s == %s {\n\t\tr ^= %d\n\t}\n", t, t, k)
	case math.IsInf(v, 1):
		fmt.Fprintf(b, "\tif %s <= 1.7976931348623157e308 {\n\t\tr ^= %d\n\t}\n", t, k)
	case math.IsInf(v, -1):
		fmt.Fprintf(b, "\tif %s >= -1.7976931348623157e308 {\n\t\tr ^= %d\n\t}\n", t, k)
	case v == 0:
		op := ">"
		if math.Signbit(v) {
			op = "<"
		}
		fmt.Fprintf(b, "\tif %s != 0 || !(1/(%s) %s 0) {\n\t\tr ^= %d\n\t}\n", t, t, op, k)
	default:
		fmt.Fprintf(b, "\tif %s != %s {\n\t\tr ^= %d\n\t}\n", t, fdLit(v), k)
	}
}

func (p *f64Part) write(f *Fuzzer, body string) {
	out := f.Out
	fmt.Fprint(out, "var fdCalls_1 int\n\n")
	for _, n := range fdVars[:4] {
		fmt.Fprintf(out, "var %s float64 = %s\n\n", n, fdLit(p.init[n]))
	}
	fmt.Fprintf(out, "var fdP_1 = [3]float64{%s, %s, %s}\n\n", fdLit(p.init["fdP_1[0]"]), fdLit(p.init["fdP_1[1]"]), fdLit(p.init["fdP_1[2]"]))
	fmt.Fprintf(out, "var fdI_1 int64 = %d\n\nvar fdN_1 int32 = %d\n\nvar fdU_1 uint32 = %d\n\n", p.ints["fdI_1"], p.ints["fdN_1"], p.ints["fdU_1"])
	fmt.Fprintf(out, "var fdF_1 float32 = %s\n\n", fdLit(float64(p.f32_0)))
	fmt.Fprint(out, "func fdId_1(x float64) float64 {\n\tfdCalls_1++\n\treturn x\n}\n\n")
	fmt.Fprintf(out, "func fdRun_1() int {\n\tr := 0\n%s\treturn r\n}\n\n", body)
}
