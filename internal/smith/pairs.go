// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// paVal is an argument genPairs writes, as the callee reads it: an integer of the
// parameter's type held in n (u for a uint64), a float in f, a bool in b.
type paVal struct {
	n int64
	u uint64
	f float64
	b bool
}

// paTypes are the types of the parameters after the first.
var paTypes = []string{"float32", "float64", "int32", "int", "int8", "uint8", "uint16", "int64", "uint64", "bool"}

// pairs is what genPairs declared: the package values and the functions' parameter
// types.
type pairs struct {
	r     *rand.Rand
	v     int64   // paV_1
	u     uint64  // paU_1
	fl    float64 // paF_1, a float32
	d     float64 // paD_1, a float64
	i     int32   // paI_1
	n     int32   // paN_1, an int
	b, c  int8    // paB_1, paC_1
	e     uint8   // paE_1
	w     uint16  // paW_1
	t     bool    // paT_1
	rn    int32   // paR_1.n
	funcs [][]string
}

// term is what a callee's hash adds for an argument of the type typ.
func paTerm(typ string, a paVal) int32 {
	switch typ {
	case "int64":
		return int32(a.n>>32)*3 + int32(a.n)
	case "uint64":
		return int32(uint32(a.u>>32))*3 + int32(uint32(a.u))
	case "float32", "float64":
		return int32(a.f * 4)
	case "bool":
		if a.b {
			return 7
		}
		return 3
	}
	return int32(a.n) // the value was made the parameter's type's when it was drawn
}

// hash is what a function of the parameter types params returns for args.
func paHash(params []string, args []paVal) (r int32) {
	for i, p := range params {
		r = r*31 + paTerm(p, args[i])
	}
	return r
}

// genPairs declares functions whose first parameter is of 64 bits and whose later
// ones are of other types -- float32, float64, the narrow integers, 64 bits again,
// bool -- and a procedure calling them with a 64-bit OPERATION for the first
// argument: a negation, a negative constant, each binary operator, a shift by a
// constant and by exactly 32 either way. The target's C compiler passes such an
// operation as a pair of words and then converted each argument after it to the
// type of the parameter after its own, or to none, in silence, until 2026-10-16
// (doc/call-arg-after-expr.c, pairArg): a float handed over as an int64, an int32
// as the bits of a float, an integer constant to a float parameter as its bits. No
// generated program had an operation followed by an argument of another type -- 59
// of the first 400 seeds had one followed by 64-bit arguments, which the compiler
// passed right. The procedure calls them directly, through a function value,
// through an interface's method, as a variadic call, and as a method of a 64-bit
// defined type whose receiver is the operation; after the first argument a 64-bit
// one may be a widening conversion or a call's result too, and a function value's
// 64-bit result is read in operations -- through a function value each of those
// was wrong or refused until 2026-10-16 as well (valueCallArgC, valueResultC). Like the aggregates
// (genAggregates), what it comes to is computed here, in Go, and it draws from its
// own source (paRand), so the rest of a seed's program is what it was.
func (f *Fuzzer) genPairs() (call string, want Int32, ok bool) {
	r := f.paRand
	if r.Intn(4) == 0 {
		return "", 0, false
	}
	p := &pairs{r: r}
	p.v, p.u = int64(r.Uint64()), r.Uint64()
	p.fl, p.d = float64(r.Intn(4001)-2000)/4, float64(r.Intn(4001)-2000)/4
	p.i, p.n = int32(r.Uint32()), int32(r.Uint32())
	p.b, p.c, p.e, p.w = int8(r.Uint32()), int8(r.Uint32()), uint8(r.Uint32()), uint16(r.Uint32())
	p.t = r.Intn(2) == 0
	p.rn = int32(r.Intn(1000))
	for i, n := 0, 2+r.Intn(3); i < n; i++ {
		ps := []string{[]string{"int64", "uint64"}[r.Intn(2)]}
		for j, m := 0, 1+r.Intn(3); j < m; j++ {
			ps = append(ps, paTypes[r.Intn(len(paTypes))])
		}
		p.funcs = append(p.funcs, ps)
	}
	p.write(f)
	return p.run(f)
}

// write declares the values, the types and the functions.
func (p *pairs) write(f *Fuzzer) {
	w := f.Out
	fmt.Fprintf(w, "var paV_1 int64 = %d\n\n", p.v)
	fmt.Fprintf(w, "var paU_1 uint64 = %d\n\n", p.u)
	fmt.Fprintf(w, "var paF_1 float32 = %s\n\n", paFloat(p.fl))
	fmt.Fprintf(w, "var paD_1 float64 = %s\n\n", paFloat(p.d))
	fmt.Fprintf(w, "var paI_1 int32 = %d\n\n", p.i)
	fmt.Fprintf(w, "var paN_1 int = %d\n\n", p.n)
	fmt.Fprintf(w, "var paB_1, paC_1 int8 = %d, %d\n\n", p.b, p.c)
	fmt.Fprintf(w, "var paE_1 uint8 = %d\n\n", p.e)
	fmt.Fprintf(w, "var paW_1 uint16 = %d\n\n", p.w)
	fmt.Fprintf(w, "var paT_1 = %v\n\n", p.t)
	body := func(recv string, params []string, names []string) string {
		var b strings.Builder
		b.WriteString("\tr := " + recv + "\n")
		for i, t := range params {
			x := names[i]
			switch t {
			case "int64", "uint64":
				fmt.Fprintf(&b, "\tr = r*31 + int(%s>>32)*3 + int(%s)\n", x, x)
			case "float32", "float64":
				fmt.Fprintf(&b, "\tr = r*31 + int(%s*4)\n", x)
			case "bool":
				fmt.Fprintf(&b, "\tif %s {\n\t\tr = r*31 + 7\n\t} else {\n\t\tr = r*31 + 3\n\t}\n", x)
			default:
				fmt.Fprintf(&b, "\tr = r*31 + int(%s)\n", x)
			}
		}
		b.WriteString("\treturn r\n")
		return b.String()
	}
	names := func(n int) (a []string) {
		for i := 0; i < n; i++ {
			a = append(a, string(rune('a'+i)))
		}
		return a
	}
	sig := func(params []string) string {
		var a []string
		for i, t := range params {
			a = append(a, names(len(params))[i]+" "+t)
		}
		return strings.Join(a, ", ")
	}
	for i, ps := range p.funcs {
		fmt.Fprintf(w, "func paFn_1_%d(%s) int {\n%s}\n\n", i, sig(ps), body("0", ps, names(len(ps))))
	}
	// A method of a 64-bit defined type, its receiver the first word pair.
	fmt.Fprint(w, "type PaQ_1 int64\n\n")
	fmt.Fprintf(w, "func (q PaQ_1) mix(b float32, c PaQ_1) int {\n%s}\n\n",
		body("0", []string{"int64", "float32", "int64"}, []string{"int64(q)", "b", "int64(c)"}))
	// An interface's method.
	fmt.Fprint(w, "type PaS_1 interface {\n\tsc(a int64, b float32, c int64) int\n}\n\n")
	fmt.Fprint(w, "type PaR_1 struct{ n int }\n\n")
	fmt.Fprintf(w, "var paR_1 = PaR_1{%d}\n\n", p.rn)
	fmt.Fprintf(w, "func (s *PaR_1) sc(a int64, b float32, c int64) int {\n%s}\n\n",
		body("s.n", []string{"int64", "float32", "int64"}, names(3)))
	// Functions handing a 64-bit value on, and one returning one, for a function
	// value whose result is read in an operation.
	fmt.Fprint(w, "func paId_1(v int64) int64 { return v + 5 }\n\n")
	fmt.Fprint(w, "func paIdU_1(v uint64) uint64 { return v ^ 0x5555 }\n\n")
	fmt.Fprint(w, "func paR64_1(a int64) int64 { return a*3 + 1 }\n\n")
	// Package variables holding functions: a checked build calls one through its
	// nil check, the pointer a call returns, every time. A local bound to one
	// declared function is called directly, and asked nothing of it.
	fmt.Fprint(w, "var paFw_1 = paR64_1\n\n")
	fmt.Fprint(w, "func paOne_1(c int64) int { return int(c>>32)*11 + int(c) }\n\n")
	fmt.Fprint(w, "var paOneV_1 = paOne_1\n\n")
	fmt.Fprint(w, "var paFnV_1 = paFn_1_0\n\n")
	// A variadic function.
	fmt.Fprintf(w, "func paVa_1(a int64, b float32, rest ...float32) int {\n%s}\n\n",
		strings.Replace(body("0", []string{"int64", "float32"}, names(2)), "\treturn r\n",
			"\tfor _, x := range rest {\n\t\tr = r*31 + int(x*4)\n\t}\n\treturn r\n", 1))
}

// paFloat writes a float of quarters as a constant with a point.
func paFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// op draws a 64-bit operation of the type typ ("int64" or "uint64") for a first
// argument, and returns its text and its value.
func (p *pairs) op(typ string) (string, paVal) {
	r := p.r
	k := int64(1 + r.Intn(1000))
	s := uint(1 + r.Intn(62))
	if typ == "uint64" {
		u := p.u
		ku := uint64(k)
		switch r.Intn(14) {
		case 0:
			return "-paU_1", paVal{u: -u}
		case 1:
			return fmt.Sprintf("paU_1 + %d", k), paVal{u: u + ku}
		case 2:
			return fmt.Sprintf("paU_1 - %d", k), paVal{u: u - ku}
		case 3:
			return fmt.Sprintf("paU_1 * %d", k), paVal{u: u * ku}
		case 4:
			return fmt.Sprintf("paU_1 / %d", k), paVal{u: u / ku}
		case 5:
			return fmt.Sprintf("paU_1 %% %d", k), paVal{u: u % ku}
		case 6:
			return fmt.Sprintf("paU_1 << %d", s), paVal{u: u << s}
		case 7:
			return fmt.Sprintf("paU_1 >> %d", s), paVal{u: u >> s}
		case 8:
			return "paU_1 << 32", paVal{u: u << 32}
		case 9:
			return "paU_1 >> 32", paVal{u: u >> 32}
		case 10:
			return fmt.Sprintf("paU_1 & %d", k), paVal{u: u & ku}
		case 11:
			return fmt.Sprintf("paU_1 | %d", k), paVal{u: u | ku}
		case 12:
			return fmt.Sprintf("paU_1 ^ %d", k), paVal{u: u ^ ku}
		default:
			return "paU_1 << 32 >> 32", paVal{u: u << 32 >> 32}
		}
	}
	v := p.v
	switch r.Intn(16) {
	case 0:
		return "-paV_1", paVal{n: -v}
	case 1:
		c := int64(r.Uint64() >> uint(1+r.Intn(62)))
		if c == 0 {
			c = 1
		}
		return fmt.Sprintf("-%d", c), paVal{n: -c} // a negative constant, a negation to the C compiler
	case 2:
		return fmt.Sprintf("paV_1 + %d", k), paVal{n: v + k}
	case 3:
		return fmt.Sprintf("paV_1 - %d", k), paVal{n: v - k}
	case 4:
		return fmt.Sprintf("paV_1 * %d", k), paVal{n: v * k}
	case 5:
		return fmt.Sprintf("paV_1 / %d", k), paVal{n: v / k}
	case 6:
		return fmt.Sprintf("paV_1 %% %d", k), paVal{n: v % k}
	case 7:
		return fmt.Sprintf("paV_1 << %d", s), paVal{n: v << s}
	case 8:
		return fmt.Sprintf("paV_1 >> %d", s), paVal{n: v >> s}
	case 9:
		return "paV_1 << 32", paVal{n: v << 32}
	case 10:
		return "paV_1 >> 32", paVal{n: v >> 32}
	case 11:
		return fmt.Sprintf("paV_1 & %d", k), paVal{n: v & k}
	case 12:
		return fmt.Sprintf("paV_1 | %d", k), paVal{n: v | k}
	case 13:
		return fmt.Sprintf("paV_1 ^ %d", k), paVal{n: v ^ k}
	case 14:
		return fmt.Sprintf("paV_1 &^ %d", k), paVal{n: v &^ k}
	default:
		return "paV_1 << 32 >> 32", paVal{n: v << 32 >> 32}
	}
}

// arg draws an argument of the type typ for a parameter after the first: a
// constant, the package's value of the type, or an expression of it -- for a float
// an INTEGER constant as often as one with a point, the constant whose conversion
// was the first lost.
func (p *pairs) arg(typ string) (string, paVal) {
	r := p.r
	pick := r.Intn(3)
	switch typ {
	case "float32", "float64":
		name, x := "paF_1", p.fl
		if typ == "float64" {
			name, x = "paD_1", p.d
		}
		switch pick {
		case 0:
			k := r.Intn(1001) - 500
			if r.Intn(2) == 0 {
				return strconv.Itoa(k), paVal{f: float64(k)}
			}
			q := float64(r.Intn(4001)-2000) / 4
			return paFloat(q), paVal{f: q}
		case 1:
			return name, paVal{f: x}
		default:
			return name + " + 1", paVal{f: x + 1}
		}
	case "int32":
		switch pick {
		case 0:
			k := int32(r.Uint32())
			return strconv.Itoa(int(k)), paVal{n: int64(k)}
		case 1:
			return "paI_1", paVal{n: int64(p.i)}
		default:
			return "paI_1 * 3", paVal{n: int64(p.i * 3)}
		}
	case "int":
		switch pick {
		case 0:
			k := int32(r.Uint32())
			return strconv.Itoa(int(k)), paVal{n: int64(k)}
		case 1:
			return "paN_1", paVal{n: int64(p.n)}
		default:
			return "paN_1 - 7", paVal{n: int64(p.n - 7)}
		}
	case "int8":
		switch pick {
		case 0:
			k := int8(r.Uint32())
			return strconv.Itoa(int(k)), paVal{n: int64(k)}
		case 1:
			return "paB_1", paVal{n: int64(p.b)}
		default:
			return "paB_1 + paC_1", paVal{n: int64(p.b + p.c)}
		}
	case "uint8":
		switch pick {
		case 0:
			k := uint8(r.Uint32())
			return strconv.Itoa(int(k)), paVal{n: int64(k)}
		case 1:
			return "paE_1", paVal{n: int64(p.e)}
		default:
			return "paE_1 * 3", paVal{n: int64(p.e * 3)}
		}
	case "uint16":
		switch pick {
		case 0:
			k := uint16(r.Uint32())
			return strconv.Itoa(int(k)), paVal{n: int64(k)}
		case 1:
			return "paW_1", paVal{n: int64(p.w)}
		default:
			return "paW_1 * 3", paVal{n: int64(p.w * 3)}
		}
	case "int64":
		// A widening conversion and a call's result besides: through a function
		// value both arrived with a garbage high word until 2026-10-16
		// (valueCallArgC), which no shape here had asked.
		switch pick = r.Intn(5); pick {
		case 0:
			k := int64(r.Uint64() >> uint(r.Intn(64)))
			if r.Intn(2) == 0 {
				k = -k
			}
			return strconv.FormatInt(k, 10), paVal{n: k}
		case 1:
			return "paV_1", paVal{n: p.v}
		case 2:
			return "int64(paI_1)", paVal{n: int64(p.i)}
		case 3:
			return "paId_1(paV_1)", paVal{n: p.v + 5}
		default:
			return p.op("int64")
		}
	case "uint64":
		switch pick = r.Intn(5); pick {
		case 0:
			k := r.Uint64() >> uint(r.Intn(64))
			return strconv.FormatUint(k, 10), paVal{u: k}
		case 1:
			return "paU_1", paVal{u: p.u}
		case 2:
			return "uint64(paW_1)", paVal{u: uint64(p.w)}
		case 3:
			return "paIdU_1(paU_1)", paVal{u: p.u ^ 0x5555}
		default:
			return p.op("uint64")
		}
	default: // bool
		switch pick {
		case 0:
			b := r.Intn(2) == 0
			return strconv.FormatBool(b), paVal{b: b}
		case 1:
			return "paT_1", paVal{b: p.t}
		default:
			return "paI_1 > 3", paVal{b: p.i > 3}
		}
	}
}

// run writes paRun, the calls drawn in an order drawn, and computes what it
// returns.
func (p *pairs) run(f *Fuzzer) (string, Int32, bool) {
	r := p.r
	var b strings.Builder
	var res int32
	usesQ := false
	step := func(v int32) { res = res*31 + v }
	args := func(ps []string) (string, []paVal) {
		text, v := p.op(ps[0])
		texts, vals := []string{text}, []paVal{v}
		for _, t := range ps[1:] {
			text, v := p.arg(t)
			texts, vals = append(texts, text), append(vals, v)
		}
		return strings.Join(texts, ", "), vals
	}
	fixed := []string{"int64", "float32", "int64"}
	shapes := []func(){
		func() { // a call
			i := r.Intn(len(p.funcs))
			a, vals := args(p.funcs[i])
			fmt.Fprintf(&b, "\tr = r*31 + paFn_1_%d(%s)\n", i, a)
			step(paHash(p.funcs[i], vals))
		},
		func() { // a call through a function value
			i := r.Intn(len(p.funcs))
			a, vals := args(p.funcs[i])
			fmt.Fprintf(&b, "\t{\n\t\tfv := paFn_1_%d\n\t\tr = r*31 + fv(%s)\n\t}\n", i, a)
			step(paHash(p.funcs[i], vals))
		},
		func() { // an interface's method
			a, vals := args(fixed)
			fmt.Fprintf(&b, "\t{\n\t\tvar s PaS_1 = &paR_1\n\t\tr = r*31 + s.sc(%s)\n\t}\n", a)
			v := p.rn
			for i, t := range fixed {
				v = v*31 + paTerm(t, vals[i])
			}
			step(v)
		},
		func() { // a variadic call, the slice after the operation
			a, vals := args(fixed[:2])
			v := paHash(fixed[:2], vals)
			for i, n := 0, r.Intn(3); i < n; i++ {
				text, x := p.arg("float32")
				a += ", " + text
				v = v*31 + paTerm("float32", x)
			}
			fmt.Fprintf(&b, "\tr = r*31 + paVa_1(%s)\n", a)
			step(v)
		},
		func() { // a function value's 64-bit result read in operations
			a, v := p.arg("int64")
			fmt.Fprintf(&b, "\tr = r*31 + int(paFw_1(paV_1)>>7) + int(paFw_1(%s)%%1000)\n", a)
			step(int32((p.v*3+1)>>7) + int32((v.n*3+1)%1000))
		},
		func() { // a single 64-bit argument to a package function value
			a, v := p.arg("int64")
			fmt.Fprintf(&b, "\tr = r*31 + paOneV_1(%s)\n", a)
			step(int32(v.n>>32)*11 + int32(v.n))
		},
		func() { // a call through a package function value
			a, vals := args(p.funcs[0])
			fmt.Fprintf(&b, "\tr = r*31 + paFnV_1(%s)\n", a)
			step(paHash(p.funcs[0], vals))
		},
		func() { // a method whose 64-bit receiver is the operation
			k := int64(1 + r.Intn(100))
			var recv string
			var q int64
			switch r.Intn(5) {
			case 0:
				recv, q = fmt.Sprintf("(q * %d)", k), p.v*k
			case 1:
				recv, q = fmt.Sprintf("(q + %d)", k), p.v+k
			case 2:
				recv, q = "(-q)", -p.v
			case 3:
				recv, q = fmt.Sprintf("PaQ_1(-%d)", k), -k
			default:
				recv, q = "(q << 32)", p.v<<32
			}
			usesQ = usesQ || strings.Contains(recv, "q")
			fa, fv := p.arg("float32")
			c := int64(r.Intn(2001) - 1000)
			fmt.Fprintf(&b, "\tr = r*31 + %s.mix(%s, PaQ_1(%d))\n", recv, fa, c)
			step(paHash(fixed, []paVal{{n: q}, fv, {n: c}}))
		},
	}
	n := 6 + r.Intn(7)
	for i := 0; i < n; i++ {
		shapes[r.Intn(len(shapes))]()
	}
	// Every value read, whatever was drawn: one nothing reads is a C variable
	// nothing reads, which the host's -Werror refuses.
	b.WriteString("\tr = r*31 + int(paV_1>>40) + int(paU_1>>40) + int(paF_1*4) + int(paD_1*4) + int(paI_1) + paN_1\n")
	b.WriteString("\tr = r*31 + int(paB_1) + int(paC_1) + int(paE_1) + int(paW_1) + paR_1.n\n")
	b.WriteString("\tif paT_1 {\n\t\tr++\n\t}\n")
	step(int32(p.v>>40) + int32(p.u>>40) + int32(p.fl*4) + int32(p.d*4) + p.i + p.n)
	step(int32(p.b) + int32(p.c) + int32(p.e) + int32(p.w) + p.rn)
	if p.t {
		res++
	}
	head := "\tr := 0\n"
	if usesQ {
		head = "\tq := PaQ_1(paV_1)\n" + head
	}
	fmt.Fprintf(f.Out, "func paRun_1() int {\n%s%s\treturn r\n}\n\n", head, b.String())
	return "paRun_1()", Int32(res), true
}
