// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strings"
)

// wrTypes are the types genWraps computes in: every integer type, int and uint
// among them, and a defined type over int64. swType serves here too, its v the part
// of the names declared of the type.
var wrTypes = []swType{
	{"int8", 8, true, "I8"}, {"uint8", 8, false, "U8"},
	{"int16", 16, true, "I16"}, {"uint16", 16, false, "U16"},
	{"int32", 32, true, "I32"}, {"uint32", 32, false, "U32"},
	{"int64", 64, true, "I64"}, {"uint64", 64, false, "U64"},
	{"int", 32, true, "N"}, {"uint", 32, false, "Un"},
	{"WrD_1", 64, true, "D"},
}

// wrCounts are the types of the shift counts: the value types and a defined type
// over uint64. Not uintptr, which the program may declare as a name of its own
// (universeNames).
var wrCounts = append(wrTypes[:len(wrTypes):len(wrTypes)], swType{"WrC_1", 64, false, "C"})

// wrMax is the greatest value of t, a uint64's as the int64 of its bits.
func wrMax(t swType) int64 {
	if !t.signed && t.bits == 64 {
		return -1
	}
	return t.max()
}

// wrOp is a op b in the type t as Go computes it: wrapped at the type's width, an
// unsigned value read as the unsigned number it is, the most negative value over -1
// that value again and its remainder 0.
func wrOp(t swType, op string, a, b int64) int64 {
	switch op {
	case "+":
		return t.wrap(a + b)
	case "-":
		return t.wrap(a - b)
	case "*":
		return t.wrap(a * b)
	case "/":
		if !t.signed {
			return t.wrap(int64(uint64(a) / uint64(b)))
		}
		return t.wrap(a / b)
	case "%":
		if !t.signed {
			return t.wrap(int64(uint64(a) % uint64(b)))
		}
		return t.wrap(a % b)
	case "&":
		return a & b
	case "|":
		return a | b
	case "^":
		return a ^ b
	case "&^":
		return a &^ b
	}
	panic(todo("%q", op))
}

// wrShift is a shifted by n in the type t, as Go shifts: by any count, every bit out
// at the width and past it, a negative value right to -1.
func wrShift(t swType, op string, a int64, n uint64) int64 {
	switch {
	case op == "<<":
		return t.wrap(a << n)
	case t.signed:
		return a >> n
	}
	return int64(uint64(a) >> n)
}

// wrCmp is a op b in the type t.
func wrCmp(t swType, op string, a, b int64) bool {
	less, eq := a < b, a == b
	if !t.signed {
		less = uint64(a) < uint64(b)
	}
	switch op {
	case "==":
		return eq
	case "!=":
		return !eq
	case "<":
		return less
	case "<=":
		return less || eq
	case ">":
		return !less && !eq
	}
	return !less
}

// wrLit spells the value v of the type t as an untyped constant, a negative one in
// parentheses, an unsigned one in hex now and then.
func wrLit(t swType, v int64, hex bool) string {
	switch {
	case t.signed && v < 0:
		return fmt.Sprintf("(%d)", v)
	case t.signed:
		return fmt.Sprint(v)
	case hex:
		return fmt.Sprintf("%#x", uint64(v))
	}
	return fmt.Sprint(uint64(v))
}

// wrExpr is an expression genWraps wrote: its text, its value, whether it is a
// constant -- which Go folds, and refuses where the fold overflows -- and whether
// an untyped one, which takes its type from where it stands.
type wrExpr struct {
	text string
	v    int64
	k, u bool
}

// wraps is what genWraps drew: the values of the package variables as the program
// starts (init) and as it runs (vals), the calls of the counting functions, the
// locals named and what the procedure comes to.
type wraps struct {
	r     *rand.Rand
	init  map[string]int64
	vals  map[string]int64
	calls int32
	loc   int
	res   int32
}

// genWraps writes a procedure of what Go defines and C does not: arithmetic that
// overflows, at every integer type and int among them, the most negative value
// divided by -1 and its remainder, shifts by counts of every integer type at the
// width and past it, a uint64's past 2^63 too, a negative value shifted left,
// conversions that truncate and extend, compound assignments of each through a
// local, a package variable, an element at a constant index and at a calling one
// and a pointer, an increment past the end, and the comparisons a C compiler may
// fold where it takes an overflow to be impossible, `(x + 1) > x`. The fuzzer's
// own arithmetic keeps to what C defines (Int32.binOp, Sized.binOp), so none of
// this was generated. Its values are what Go computes, in Go, and it draws from
// its own source (wrRand), so the rest of a seed's program is what it was.
func (f *Fuzzer) genWraps() (call string, want Int32, ok bool) {
	r := f.wrRand
	if r.Intn(3) == 0 {
		return "", 0, false
	}
	w := &wraps{r: r, init: map[string]int64{}, vals: map[string]int64{}}
	for _, t := range wrTypes {
		for i := 0; i < 4; i++ {
			var v int64
			switch {
			case i == 0 && t.signed:
				v = t.min()
			case i == 0:
				v = wrMax(t)
			case i == 1 && t.signed:
				v = -1
			case i == 1:
				v = t.wrap(1 << (t.bits - 1))
			default:
				v = w.edge(t)
			}
			w.init[w.varName(t, i)] = v
		}
		for i := 0; i < 2; i++ {
			w.init[fmt.Sprintf("wrA%s_1[%d]", t.v, i)] = w.edge(t)
		}
	}
	for _, t := range wrCounts {
		for i := 0; i < 2; i++ {
			w.init[w.countName(t, i)] = w.countVal(t)
		}
	}
	for k, v := range w.init {
		w.vals[k] = v
	}

	var b strings.Builder
	for i, n := 0, 6+r.Intn(9); i < n; i++ {
		switch r.Intn(10) {
		case 0, 1, 2, 3:
			t := wrTypes[r.Intn(len(wrTypes))]
			w.fold(&b, t, w.nonConst(t, 2))
		case 4, 5, 6:
			w.compound(&b)
		case 7, 8:
			w.compare(&b)
		default:
			w.edgeDiv(&b)
		}
	}
	b.WriteString("\tr = r*31 + wrCalls_1\n")
	w.res = w.res*31 + w.calls
	w.write(f, b.String())
	return "wrRun_1()", Int32(w.res), true
}

func (w *wraps) varName(t swType, i int) string { return fmt.Sprintf("wr%s%c_1", t.v, 'a'+i) }

func (w *wraps) countName(t swType, i int) string { return fmt.Sprintf("wrK%s%c_1", t.v, 'a'+i) }

// edge draws a value of t, most often one at or next to an end of the type.
func (w *wraps) edge(t swType) int64 {
	r := w.r
	switch r.Intn(10) {
	case 0:
		return t.min()
	case 1:
		return wrMax(t)
	case 2:
		return t.wrap(t.min() + 1)
	case 3:
		return t.wrap(wrMax(t) - 1)
	case 4:
		return t.wrap(-1)
	case 5:
		return int64(r.Intn(5))
	case 6:
		// Near the square root of the type's range, where a product overflows.
		return t.wrap(1<<(t.bits/2) + int64(r.Intn(3)) - 1)
	}
	return t.wrap(int64(r.Uint64()))
}

// countVal draws a count of the type t: below the widths, at each, past them, and
// for an unsigned type of 64 bits past 2^63.
func (w *wraps) countVal(t swType) int64 {
	c := []int64{0, 1, 5, 7, 8, 15, 16, 31, 32, 33, 63, 64, 65, 100, 127}
	switch {
	case !t.signed:
		c = append(c, 128, 200, 255)
		if t.bits >= 16 {
			c = append(c, 256, 1000, 65535)
		}
		if t.bits >= 32 {
			c = append(c, 1<<31, 1<<31+8, 1<<32-1)
		}
		if t.bits == 64 {
			c = append(c, 1<<32, 1<<32+16, math.MinInt64, math.MinInt64+32, -64, -1)
		}
	default:
		if t.bits >= 16 {
			c = append(c, 1000, math.MaxInt16)
		}
		if t.bits >= 32 {
			c = append(c, math.MaxInt32)
		}
		if t.bits == 64 {
			c = append(c, 1<<32, 1<<32+8, math.MaxInt64)
		}
	}
	return c[w.r.Intn(len(c))]
}

// variable is one of the package variables of t, read.
func (w *wraps) variable(t swType) wrExpr {
	name := w.varName(t, w.r.Intn(4))
	return wrExpr{text: name, v: w.vals[name]}
}

// call is x handed through t's counting function, which returns it.
func (w *wraps) call(t swType, x wrExpr) wrExpr {
	w.calls++
	return wrExpr{text: "wrF" + t.v + "_1(" + x.text + ")", v: x.v}
}

// leaf draws an operand of t: a variable, a call, a typed constant at an end of the
// type or an untyped one.
func (w *wraps) leaf(t swType) wrExpr {
	r := w.r
	switch n := r.Intn(20); {
	case n < 9:
		return w.variable(t)
	case n < 13:
		if r.Intn(3) == 0 {
			v := w.edge(t)
			return w.call(t, wrExpr{text: wrLit(t, v, false), v: v})
		}
		x := w.variable(t)
		return w.call(t, x)
	case n < 16:
		if t.signed && r.Intn(2) == 0 {
			return wrExpr{text: "WrMin" + t.v + "_1", v: t.min(), k: true}
		}
		return wrExpr{text: "WrMax" + t.v + "_1", v: wrMax(t), k: true}
	}
	v := w.edge(t)
	return wrExpr{text: wrLit(t, v, r.Intn(3) == 0), v: v, k: true, u: true}
}

// nonConst draws an expression of t that is not a constant.
func (w *wraps) nonConst(t swType, depth int) wrExpr {
	if e := w.expr(t, depth); !e.k {
		return e
	}
	return w.variable(t)
}

// count draws a shift count, of any integer type, and its value.
func (w *wraps) count() (wrExpr, uint64) {
	r := w.r
	t := wrCounts[r.Intn(len(wrCounts))]
	switch r.Intn(8) {
	case 0, 1:
		v := []int64{0, 1, 3, 7, 8, 15, 16, 31, 32, 33, 63, 64, 65, 100}[r.Intn(14)]
		return wrExpr{text: fmt.Sprint(v), v: v, k: true, u: true}, uint64(v)
	case 2:
		x := w.countVar(t)
		return w.call(t, x), uint64(x.v)
	case 3:
		if !t.signed {
			x, y := w.countVar(t), w.countVar(t)
			v := t.wrap(x.v + y.v)
			return wrExpr{text: "(" + x.text + " + " + y.text + ")", v: v}, uint64(v)
		}
	}
	x := w.countVar(t)
	return x, uint64(x.v)
}

// countVar is one of the package variables of the count type t, read.
func (w *wraps) countVar(t swType) wrExpr {
	name := w.countName(t, w.r.Intn(2))
	return wrExpr{text: name, v: w.vals[name]}
}

// nonConstCount draws a shift count that is not a constant.
func (w *wraps) nonConstCount() (wrExpr, uint64) {
	if c, n := w.count(); !c.k {
		return c, n
	}
	x := w.countVar(wrCounts[w.r.Intn(len(wrCounts))])
	return x, uint64(x.v)
}

var wrOps = []string{"+", "+", "-", "-", "*", "*", "/", "%", "&", "|", "^", "&^"}

// expr draws an expression of t, depth operators deep at most.
func (w *wraps) expr(t swType, depth int) wrExpr {
	r := w.r
	if depth <= 0 || r.Intn(4) == 0 {
		return w.leaf(t)
	}
	switch r.Intn(12) {
	case 0:
		x := w.nonConst(t, depth-1)
		if r.Intn(2) == 0 {
			return wrExpr{text: "-(" + x.text + ")", v: t.wrap(-x.v)}
		}
		return wrExpr{text: "^(" + x.text + ")", v: t.wrap(^x.v)}
	case 1:
		from := wrTypes[r.Intn(len(wrTypes))]
		x := w.nonConst(from, depth-1)
		return wrExpr{text: t.name + "(" + x.text + ")", v: t.wrap(x.v)}
	case 2, 3:
		op := []string{"<<", ">>"}[r.Intn(2)]
		x := w.expr(t, depth-1)
		c, n := w.count()
		if x.k && c.k {
			c, n = w.nonConstCount()
		}
		xt := x.text
		if x.u {
			// An untyped constant shifted by a count that is not one takes the
			// type of where the shift stands, not t's.
			xt = t.name + "(" + xt + ")"
		}
		return wrExpr{text: "(" + xt + " " + op + " " + c.text + ")", v: wrShift(t, op, x.v, n)}
	case 4:
		// An untyped constant shifted by a count that is not one, beside a value of
		// t: it is converted to t, which a count at the width shifts out.
		x := w.nonConst(t, depth-1)
		op := []string{"+", "-", "|", "^", "*", "&", "/", "%", "&^"}[r.Intn(9)]
		lit := 1 + r.Int63n(min(t.max(), 255))
		sop := []string{"<<", ">>"}[r.Intn(2)]
		c, n := w.nonConstCount()
		sv := wrShift(t, sop, lit, n)
		if (op == "/" || op == "%") && sv == 0 {
			op = "+"
		}
		return wrExpr{text: fmt.Sprintf("(%s %s (%d %s %s))", x.text, op, lit, sop, c.text), v: wrOp(t, op, x.v, sv)}
	}
	op := wrOps[r.Intn(len(wrOps))]
	x := w.expr(t, depth-1)
	y := w.expr(t, depth-1)
	if x.k && y.k {
		y = w.variable(t)
	}
	if (op == "/" || op == "%") && y.v == 0 {
		op = "^"
	}
	return wrExpr{text: "(" + x.text + " " + op + " " + y.text + ")", v: wrOp(t, op, x.v, y.v)}
}

// fold adds the value of e, of the type t, to r: through a local where it has 64
// bits, both of its words.
func (w *wraps) fold(b *strings.Builder, t swType, e wrExpr) {
	if t.bits != 64 {
		fmt.Fprintf(b, "\tr = r*31 + int(%s)\n", e.text)
		w.res = w.res*31 + int32(e.v)
		return
	}
	w.loc++
	n := fmt.Sprintf("wt%d", w.loc)
	fmt.Fprintf(b, "\t%s := %s\n\tr = r*31 + (int(%s) ^ int(%s>>32))\n", n, e.text, n, n)
	hi := e.v >> 32
	if !t.signed {
		hi = int64(uint64(e.v) >> 32)
	}
	w.res = w.res*31 + (int32(e.v) ^ int32(hi))
}

// compound writes a compound assignment, an increment or a decrement of a value of
// some type -- a local, a package variable, an element of a package array at a
// constant index or at one a call returns, or one through a pointer -- and adds
// the value written to r.
func (w *wraps) compound(b *strings.Builder) {
	r := w.r
	t := wrTypes[r.Intn(len(wrTypes))]
	arr := "wrA" + t.v + "_1"
	i := r.Intn(2)
	var target, read, key string
	switch r.Intn(5) {
	case 0:
		x := w.nonConst(t, 1)
		w.loc++
		target = fmt.Sprintf("wt%d", w.loc)
		fmt.Fprintf(b, "\t%s := %s\n", target, x.text)
		key, read = target, target
		w.vals[key] = x.v
	case 1:
		x := w.variable(t)
		target, key, read = x.text, x.text, x.text
	case 2:
		target = fmt.Sprintf("%s[%d]", arr, i)
		key, read = target, target
	case 3:
		w.calls++
		target = fmt.Sprintf("%s[wrIdx_1(%d)]", arr, i)
		key = fmt.Sprintf("%s[%d]", arr, i)
		read = key
	default:
		w.loc++
		p := fmt.Sprintf("wp%d", w.loc)
		fmt.Fprintf(b, "\t%s := &%s[%d]\n", p, arr, i)
		target, read = "*"+p, "*"+p
		key = fmt.Sprintf("%s[%d]", arr, i)
	}
	cur := w.vals[key]
	var nv int64
	switch op := append([]string{"<<", ">>", "<<", ">>", "++", "--"}, wrOps...)[r.Intn(6+len(wrOps))]; op {
	case "++":
		fmt.Fprintf(b, "\t%s++\n", target)
		nv = t.wrap(cur + 1)
	case "--":
		fmt.Fprintf(b, "\t%s--\n", target)
		nv = t.wrap(cur - 1)
	case "<<", ">>":
		c, n := w.count()
		fmt.Fprintf(b, "\t%s %s= %s\n", target, op, c.text)
		nv = wrShift(t, op, cur, n)
	default:
		e := w.expr(t, 1)
		if (op == "/" || op == "%") && e.v == 0 {
			op = "^"
		}
		fmt.Fprintf(b, "\t%s %s= %s\n", target, op, e.text)
		nv = wrOp(t, op, cur, e.v)
	}
	w.vals[key] = nv
	w.fold(b, t, wrExpr{text: read, v: nv})
}

// compare writes a comparison of two values of some type, and adds which way it
// went to r: half the time a value against itself after an operation, the test a
// program writes for an overflow and a C compiler may fold to a constant.
func (w *wraps) compare(b *strings.Builder) {
	r := w.r
	t := wrTypes[r.Intn(len(wrTypes))]
	var x, y wrExpr
	if r.Intn(2) == 0 {
		v := w.leaf(t)
		if v.k {
			v = w.variable(t)
		}
		k := w.leaf(t)
		op := []string{"+", "-", "*"}[r.Intn(3)]
		x = wrExpr{text: "(" + v.text + " " + op + " " + k.text + ")", v: wrOp(t, op, v.v, k.v)}
		y = v
		if strings.HasPrefix(v.text, "wrF") {
			w.calls++ // written twice, called twice
		}
	} else {
		x, y = w.expr(t, 2), w.expr(t, 2)
		if x.k && y.k {
			y = w.variable(t)
		}
	}
	op := []string{"==", "!=", "<", "<=", ">", ">="}[r.Intn(6)]
	k1, k2 := int32(1+r.Intn(999)), int32(1+r.Intn(999))
	fmt.Fprintf(b, "\tif %s %s %s {\n\t\tr = r*31 + %d\n\t} else {\n\t\tr = r*31 + %d\n\t}\n", x.text, op, y.text, k1, k2)
	if wrCmp(t, op, x.v, y.v) {
		w.res = w.res*31 + k1
	} else {
		w.res = w.res*31 + k2
	}
}

// edgeDiv writes the most negative value of a signed type divided by -1, or its
// remainder, each operand a variable, a constant or a call, and adds the result to
// r. A variable may have been written since the program began, and then it is
// whatever division its values make.
func (w *wraps) edgeDiv(b *strings.Builder) {
	r := w.r
	var signed []swType
	for _, t := range wrTypes {
		if t.signed {
			signed = append(signed, t)
		}
	}
	t := signed[r.Intn(len(signed))]
	var x, y wrExpr
	switch r.Intn(3) {
	case 0:
		x = wrExpr{text: w.varName(t, 0), v: w.vals[w.varName(t, 0)]}
	case 1:
		x = wrExpr{text: "WrMin" + t.v + "_1", v: t.min(), k: true}
	default:
		x = w.call(t, wrExpr{text: w.varName(t, 0), v: w.vals[w.varName(t, 0)]})
	}
	switch r.Intn(3) {
	case 0:
		y = wrExpr{text: w.varName(t, 1), v: w.vals[w.varName(t, 1)]}
	case 1:
		if !x.k {
			y = wrExpr{text: "(-1)", v: -1, k: true}
			break
		}
		fallthrough
	default:
		y = w.call(t, wrExpr{text: "(-1)", v: -1})
	}
	if y.v == 0 {
		y = w.call(t, wrExpr{text: "(-1)", v: -1})
	}
	op := []string{"/", "%"}[r.Intn(2)]
	w.fold(b, t, wrExpr{text: "(" + x.text + " " + op + " " + y.text + ")", v: wrOp(t, op, x.v, y.v)})
}

// write declares what the procedure names, and the procedure.
func (w *wraps) write(f *Fuzzer, body string) {
	out := f.Out
	uses := func(name string) bool {
		return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(body)
	}
	fmt.Fprint(out, "type WrD_1 int64\n\ntype WrC_1 uint64\n\nvar wrCalls_1 int\n\n")
	for _, t := range wrTypes {
		for i := 0; i < 4; i++ {
			if n := w.varName(t, i); uses(n) {
				fmt.Fprintf(out, "var %s %s = %s\n\n", n, t.name, wrLit(t, w.init[n], false))
			}
		}
		if n := "wrA" + t.v + "_1"; uses(n) {
			fmt.Fprintf(out, "var %s = [2]%s{%s, %s}\n\n", n, t.name, wrLit(t, w.init[n+"[0]"], false), wrLit(t, w.init[n+"[1]"], false))
		}
		if n := "WrMin" + t.v + "_1"; uses(n) {
			fmt.Fprintf(out, "const %s %s = %s\n\n", n, t.name, wrLit(t, t.min(), false))
		}
		if n := "WrMax" + t.v + "_1"; uses(n) {
			fmt.Fprintf(out, "const %s %s = %s\n\n", n, t.name, wrLit(t, wrMax(t), false))
		}
	}
	for _, t := range wrCounts {
		for i := 0; i < 2; i++ {
			if n := w.countName(t, i); uses(n) {
				fmt.Fprintf(out, "var %s %s = %s\n\n", n, t.name, wrLit(t, w.init[n], false))
			}
		}
		if n := "wrF" + t.v + "_1"; uses(n) {
			fmt.Fprintf(out, "func %s(x %s) %s {\n\twrCalls_1++\n\treturn x\n}\n\n", n, t.name, t.name)
		}
	}
	if uses("wrIdx_1") {
		fmt.Fprint(out, "func wrIdx_1(i int) int {\n\twrCalls_1++\n\treturn i\n}\n\n")
	}
	fmt.Fprintf(out, "func wrRun_1() int {\n\tr := 0\n%s\treturn r\n}\n\n", body)
}
