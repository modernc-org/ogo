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

// embedding is what genEmbed declared, and the state its procedure runs over: the
// fields of the package variables emO and emP, which the procedure writes as it
// goes.
type embedding struct {
	r      *rand.Rand
	a      int32  // emO.EMB.a
	bIn    uint32 // emO.EMB.b, which emO's own b shadows
	bOut   int32  // emO.b
	c      int32  // emO.c
	n      int32  // emP.n, which emO reaches through its embedded *EMP
	c1, c2 int32  // sumb's coefficients
	buf    string // emBuf's bytes
	arr    [4]int32
}

// The model of what the methods and functions compute, in the target's 32-bit
// arithmetic.

func (m *embedding) bump(d int32) int32 { m.a += d; return m.a }

func (m *embedding) sumb() int32 { return m.a*m.c1 + int32(m.bIn)*m.c2 }

func (m *embedding) inc(d int32) int32 { m.n += d; return m.n }

func (m *embedding) own() int32 { return m.c*3 + m.bOut }

func emSum(base int32, xs ...int32) int32 {
	s := base
	for i, x := range xs {
		s += x * int32(i+1)
	}
	return s
}

// emBytes are the bytes emBuf is drawn from: letters, a digit, a space, and a rune
// of two bytes, so a slice of emBuf may end inside one -- string(b) compares bytes,
// not runes.
const emBytes = "ABCDEFGHabcdefgh0123 é"

// genEmbed declares, at package scope, a struct embedded by value and one embedded
// by pointer in an outer struct whose own field shadows the inner one's, an
// interface the outer type implements through methods it promotes, a variadic
// function and a byte buffer, and a procedure: promoted fields read, written and
// shadowed, promoted methods called through either embedding, through the
// interface, through a method value and through a pointer to the outer value, the
// outer value copied (its embedded pointer shared), variadic calls with no
// arguments, with several and with a slice spread, and string(b) compared, switched
// on and measured -- the shapes a program reaching for embedding and variadic calls
// writes, none of which a generated program had. Like genMultiWord, what the
// procedure comes to is computed here, in Go, and it draws from its own source
// (emRand), so the rest of a seed's program is what it was.
func (f *Fuzzer) genEmbed() (call string, want Int32, ok bool) {
	r := f.emRand
	if r.Intn(2) == 0 {
		return "", 0, false
	}
	draw := func(lo, hi int) int32 { return int32(lo + r.Intn(hi-lo+1)) }
	m := &embedding{r: r}
	m.a, m.bIn, m.bOut, m.c, m.n = int32(r.Uint32()), r.Uint32(), int32(r.Uint32()), draw(-5000, 5000), draw(-5000, 5000)
	m.c1, m.c2 = draw(1, 19), draw(1, 19)
	rs := []rune(emBytes)
	var b strings.Builder
	for range 6 + r.Intn(7) {
		b.WriteRune(rs[r.Intn(len(rs))])
	}
	m.buf = b.String()
	for i := range m.arr {
		m.arr[i] = draw(-1000, 1000)
	}
	m.write(f)
	return m.run(f, draw(-200, 200))
}

// write declares the types, the values and the functions.
func (m *embedding) write(f *Fuzzer) {
	w := f.Out
	fmt.Fprint(w, "type EMB_1 struct {\n\ta int32\n\tb uint32\n}\n\n")
	fmt.Fprint(w, "func (p *EMB_1) bump(d int32) int32 {\n\tp.a += d\n\treturn p.a\n}\n\n")
	fmt.Fprintf(w, "func (v EMB_1) sumb() int32 {\n\treturn v.a*%d + int32(v.b)*%d\n}\n\n", m.c1, m.c2)
	fmt.Fprint(w, "type EMP_1 struct {\n\tn int32\n}\n\n")
	fmt.Fprint(w, "func (p *EMP_1) inc(d int32) int32 {\n\tp.n += d\n\treturn p.n\n}\n\n")
	fmt.Fprint(w, "func (p *EMP_1) get() int32 {\n\treturn p.n\n}\n\n")
	fmt.Fprint(w, "type EMO_1 struct {\n\tEMB_1\n\t*EMP_1\n\tb int32\n\tc int32\n}\n\n")
	fmt.Fprint(w, "func (o *EMO_1) own() int32 {\n\treturn o.c*3 + o.b\n}\n\n")
	fmt.Fprint(w, "type EMI_1 interface {\n\tsumb() int32\n\tget() int32\n}\n\n")
	fmt.Fprintf(w, "var emP_1 = EMP_1{n: %d}\n\n", m.n)
	fmt.Fprintf(w, "var emO_1 = EMO_1{EMB_1: EMB_1{a: %d, b: %d}, EMP_1: &emP_1, b: %d, c: %d}\n\n", m.a, m.bIn, m.bOut, m.c)
	fmt.Fprintf(w, "var emBuf_1 = []byte(%s)\n\n", strconv.Quote(m.buf))
	fmt.Fprintf(w, "var emArr_1 = [4]int32{%d, %d, %d, %d}\n\n", m.arr[0], m.arr[1], m.arr[2], m.arr[3])
	fmt.Fprint(w, "func emSum_1(base int32, xs ...int32) int32 {\n\ts := base\n\tfor i, x := range xs {\n\t\ts += x * int32(i+1)\n\t}\n\treturn s\n}\n\n")
}

// run writes emRun, a statement or two for each of the shapes drawn, in an order
// drawn, and computes what it returns given k.
func (m *embedding) run(f *Fuzzer, k int32) (string, Int32, bool) {
	r := m.r
	var b strings.Builder
	var res int32
	step := func(v int32) { res = res*31 + v }
	d := func() int32 { return int32(r.Intn(201) - 100) }
	// span draws a slice of emBuf, and lit a constant to compare it with: the slice
	// itself as often as not, else another string of the bytes.
	span := func() (int, int) {
		i := r.Intn(len(m.buf))
		return i, i + r.Intn(len(m.buf)-i+1)
	}
	lit := func(s string) string {
		if r.Intn(2) == 0 {
			return s
		}
		rs := []rune(emBytes)
		var o strings.Builder
		for range r.Intn(4) {
			o.WriteRune(rs[r.Intn(len(rs))])
		}
		return o.String()
	}
	shapes := []func(){
		func() { // a promoted field read
			fmt.Fprint(&b, "\tr = r*31 + int(emO_1.a)\n")
			step(m.a)
		},
		func() { // a promoted field written, read by its full path
			v := d()
			fmt.Fprintf(&b, "\temO_1.a += %d\n\tr = r*31 + int(emO_1.EMB_1.a)\n", v)
			m.a += v
			step(m.a)
		},
		func() { // a promoted pointer method
			v := d()
			fmt.Fprintf(&b, "\tr = r*31 + int(emO_1.bump(%d))\n", v)
			step(m.bump(v))
		},
		func() { // a promoted value method
			fmt.Fprint(&b, "\tr = r*31 + int(emO_1.sumb())\n")
			step(m.sumb())
		},
		func() { // a method promoted through an embedded pointer, and what it points at
			v := d()
			fmt.Fprintf(&b, "\tr = r*31 + int(emO_1.inc(%d))\n\tr = r*31 + int(emP_1.n)\n", v)
			step(m.inc(v))
			step(m.n)
		},
		func() { // a field shadowing the promoted one, and the promoted one by its path
			fmt.Fprint(&b, "\tr = r*31 + int(emO_1.b) + int(emO_1.EMB_1.b)*3\n")
			step(m.bOut + int32(m.bIn)*3)
		},
		func() { // promoted methods through an interface
			fmt.Fprint(&b, "\tvar ei EMI_1 = &emO_1\n\tr = r*31 + int(ei.sumb())\n\tr = r*31 + int(ei.get())\n")
			step(m.sumb())
			step(m.n)
		},
		func() { // a promoted method taken as a value
			v := d()
			fmt.Fprintf(&b, "\tmv := emO_1.bump\n\tr = r*31 + int(mv(%d))\n", v)
			step(m.bump(v))
		},
		func() { // the outer type's own method, reading its shadowing field
			fmt.Fprint(&b, "\tr = r*31 + int(emO_1.own())\n")
			step(m.own())
		},
		func() { // a variadic call with no variadic argument
			fmt.Fprint(&b, "\tr = r*31 + int(emSum_1(int32(k)))\n")
			step(emSum(k))
		},
		func() { // a variadic call with several
			xs := []int32{d(), d(), d()}[:1+r.Intn(3)]
			var a []string
			for _, x := range xs {
				a = append(a, strconv.Itoa(int(x)))
			}
			fmt.Fprintf(&b, "\tr = r*31 + int(emSum_1(int32(k), %s))\n", strings.Join(a, ", "))
			step(emSum(k, xs...))
		},
		func() { // a slice of an array spread
			v := d()
			i := r.Intn(4)
			j := i + r.Intn(5-i)
			fmt.Fprintf(&b, "\tr = r*31 + int(emSum_1(%d, emArr_1[%d:%d]...))\n", v, i, j)
			step(emSum(v, m.arr[i:j]...))
		},
		func() { // the outer value copied: its own fields apart, its embedded pointer shared
			v, e := d(), d()
			fmt.Fprintf(&b, "\tcp := emO_1\n\tcp.a += %d\n\tcp.inc(%d)\n\tr = r*31 + int(cp.a) - int(emO_1.a)\n\tr = r*31 + int(emP_1.n)\n", v, e)
			m.inc(e)
			step(v)
			step(m.n)
		},
		func() { // promoted methods and fields through a pointer to the outer value
			v := d()
			fmt.Fprintf(&b, "\tpo := &emO_1\n\tr = r*31 + int(po.bump(%d))\n\tr = r*31 + int(po.a) + int(po.get())\n", v)
			step(m.bump(v))
			step(m.a + m.n)
		},
		func() { // string(b) compared
			i, j := span()
			s := lit(m.buf[i:j])
			fmt.Fprintf(&b, "\tif string(emBuf_1[%d:%d]) == %s {\n\t\tr += 3\n\t}\n", i, j, strconv.Quote(s))
			if m.buf[i:j] == s {
				res += 3
			}
		},
		func() { // string(b) ordered
			i, j := span()
			s := lit(m.buf[i:j])
			fmt.Fprintf(&b, "\tif %s < string(emBuf_1[%d:%d]) {\n\t\tr += 13\n\t}\n", strconv.Quote(s), i, j)
			if s < m.buf[i:j] {
				res += 13
			}
		},
		func() { // string(b) switched on, with constant cases
			i, j := span()
			s := m.buf[i:j]
			c1, c2, c3 := lit(s), lit(s), lit(s)
			for c2 == c1 {
				c2 += "x"
			}
			for c3 == c1 || c3 == c2 {
				c3 += "y"
			}
			fmt.Fprintf(&b, "\tswitch string(emBuf_1[%d:%d]) {\n\tcase %s:\n\t\tr += 5\n\tcase %s, %s:\n\t\tr += 7\n\tdefault:\n\t\tr += 11\n\t}\n",
				i, j, strconv.Quote(c1), strconv.Quote(c2), strconv.Quote(c3))
			switch s {
			case c1:
				res += 5
			case c2, c3:
				res += 7
			default:
				res += 11
			}
		},
		func() { // string(b) measured
			i, _ := span()
			fmt.Fprintf(&b, "\tr = r*31 + len(string(emBuf_1[%d:]))\n", i)
			step(int32(len(m.buf) - i))
		},
	}
	var drawn []func()
	for _, s := range shapes {
		if r.Intn(4) != 0 {
			drawn = append(drawn, s)
		}
	}
	if len(drawn) == 0 {
		drawn = shapes[:1]
	}
	r.Shuffle(len(drawn), func(i, j int) { drawn[i], drawn[j] = drawn[j], drawn[i] })
	for _, s := range drawn {
		s()
	}
	fmt.Fprintf(f.Out, "func emRun_1(k int) int {\n\tr := 0\n%s\treturn r\n}\n\n", b.String())
	return fmt.Sprintf("emRun_1(%d)", k), Int32(res), true
}
