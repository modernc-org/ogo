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

// mwField is a field of the struct genMultiWord declares: 32 bits, signed or not.
type mwField struct {
	name   string
	signed bool
}

func (fl mwField) typ() string {
	if fl.signed {
		return "int32"
	}
	return "uint32"
}

// lit is v, the field's bits, written as a constant of the field's type.
func (fl mwField) lit(v int32) string {
	if fl.signed {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatUint(uint64(uint32(v)), 10)
}

// multiWord is what genMultiWord declared: the struct's fields, the package
// values, and the coefficients of the functions reading them.
type multiWord struct {
	r      *rand.Rand
	fields []mwField
	table  [3][]int32 // mwT
	hold   []int32    // mwH.m
	holdN  int32      // mwH.n
	strs   [3]string  // mwS
	wides  [3]int64   // mwW
	slices [3][]int32 // mwL
	ptrAt  int        // the element of mwT mwPtr returns
	sumC   []int32    // mwSum's coefficients
	mixC   []int32    // mix's
	valAt  int        // the field val reads
	valC   int32      // and multiplies by
	mkC    []int32    // mwMk's coefficient and offset per field: T(p*c + o)
	mkO    []int32
}

// The model of what the functions compute, in int arithmetic on a 32-bit int.

func (m *multiWord) sum(v []int32) (s int32) {
	for i := range v {
		s += v[i] * m.sumC[i]
	}
	return s
}

func (m *multiWord) mix(v []int32) (s int32) {
	for i := range v {
		s += v[i] * m.mixC[i]
	}
	return s
}

func (m *multiWord) val(v []int32) int32 { return v[m.valAt]*m.valC + 1 }

func (m *multiWord) mk(p int32) []int32 {
	v := make([]int32, len(m.fields))
	for i := range v {
		v[i] = p*m.mkC[i] + m.mkO[i]
	}
	return v
}

func (m *multiWord) str(s string) int32 { return int32(len(s))*7 + int32(s[len(s)-1]) }

func (m *multiWord) wide(x int64) int32 { return int32(x>>32)*3 + int32(x) }

func (m *multiWord) slice(s []int32) int32 { return int32(len(s))*5 + int32(len(s))*3 + s[len(s)-1] }

// genMultiWord declares, at package scope, values of two to four words -- a struct
// of 32-bit fields, an int64, a string, a slice and an interface value -- and a
// procedure handing each by value where the expression computing it calls a
// function: an element indexed by a call, a dereference and a field read through a
// pointer a call returns, a value receiver, a call through a function value, a
// later argument, a string comparison and a send. The function called counts its
// calls, and the procedure folds the count. The procedure also reads a member past
// the first word of a struct a call returns, and the length and capacity of a value
// received.
//
// The target passes and returns such a value in registers, a word each, and its C
// compiler evaluated an argument of one once per word, in silence, until 2026-10-14
// (doc/multi-long-arg-evaluated-per-long.c) -- `takeS(strs[idx()])` called idx
// twice -- and read a member past the first word of one a call returns from a
// register nothing wrote (doc/member-of-struct-result.c), which the emitter's binding
// of the call kept from every program but `len(<-ch)`'s. No generated program passed
// such a value where a call stands. Like the aggregates (genAggregates), what the
// procedure comes to is computed here, in Go, and it draws from its own source
// (mwRand), so the rest of a seed's program is what it was.
func (f *Fuzzer) genMultiWord() (call string, want Int32, ok bool) {
	r := f.mwRand
	if r.Intn(4) == 0 {
		return "", 0, false
	}
	draw := func(lo, hi int) int32 { return int32(lo + r.Intn(hi-lo+1)) }
	coefs := func(n int) []int32 {
		c := make([]int32, n)
		for i := range c {
			c[i] = draw(1, 19)
		}
		return c
	}
	m := &multiWord{r: r}
	// Two to four fields of 32 bits: a struct the target passes and returns in
	// registers, a word each.
	m.fields = make([]mwField, 2+r.Intn(3))
	for i := range m.fields {
		m.fields[i] = mwField{fmt.Sprintf("f%d", i), r.Intn(2) == 0}
	}
	value := func() []int32 {
		v := make([]int32, len(m.fields))
		for i := range v {
			v[i] = int32(r.Uint32())
		}
		return v
	}
	for i := range m.table {
		m.table[i] = value()
	}
	m.hold, m.holdN = value(), int32(r.Uint32())
	for i := range m.strs {
		m.strs[i] = stringCorpus[1+r.Intn(len(stringCorpus)-1)] // not "": mwStr reads the last byte
	}
	for i := range m.wides {
		if m.wides[i] = int64(r.Uint64()); m.wides[i] == math.MinInt64 {
			m.wides[i]++
		}
	}
	for i := range m.slices {
		m.slices[i] = make([]int32, 1+r.Intn(4))
		for j := range m.slices[i] {
			m.slices[i][j] = draw(-1000, 1000)
		}
	}
	m.ptrAt = r.Intn(3)
	m.sumC, m.mixC = coefs(len(m.fields)), coefs(len(m.fields))
	m.valAt, m.valC = r.Intn(len(m.fields)), draw(2, 9)
	for range m.fields {
		m.mkC, m.mkO = append(m.mkC, draw(-60, 60)), append(m.mkO, draw(-2000, 2000))
	}
	m.write(f)
	return m.run(f, draw(-200, 200))
}

// write declares the types, the values and the functions.
func (m *multiWord) write(f *Fuzzer) {
	w := f.Out
	structLit := func(v []int32) string {
		var a []string
		for i, fl := range m.fields {
			a = append(a, fl.lit(v[i]))
		}
		return "{" + strings.Join(a, ", ") + "}"
	}
	fmt.Fprint(w, "type MW_1 struct {\n")
	for _, fl := range m.fields {
		fmt.Fprintf(w, "\t%s %s\n", fl.name, fl.typ())
	}
	fmt.Fprint(w, "}\n\n")
	fmt.Fprint(w, "type MWH_1 struct {\n\tn int32\n\tm MW_1\n}\n\n")
	fmt.Fprint(w, "type MWer_1 interface {\n\tval() int\n}\n\n")
	fmt.Fprint(w, "var mwN_1 int\n\n")

	var a []string
	for _, v := range m.table {
		a = append(a, structLit(v))
	}
	fmt.Fprintf(w, "var mwT_1 = [3]MW_1{%s}\n\n", strings.Join(a, ", "))
	fmt.Fprintf(w, "var mwH_1 = MWH_1{n: %d, m: MW_1%s}\n\n", m.holdN, structLit(m.hold))
	fmt.Fprintf(w, "var mwS_1 = [3]string{%s, %s, %s}\n\n", strconv.Quote(m.strs[0]), strconv.Quote(m.strs[1]), strconv.Quote(m.strs[2]))
	fmt.Fprintf(w, "var mwW_1 = [3]int64{%d, %d, %d}\n\n", m.wides[0], m.wides[1], m.wides[2])
	a = a[:0]
	for _, s := range m.slices {
		var e []string
		for _, v := range s {
			e = append(e, strconv.Itoa(int(v)))
		}
		a = append(a, "{"+strings.Join(e, ", ")+"}")
	}
	fmt.Fprintf(w, "var mwL_1 = [3][]int{%s}\n\n", strings.Join(a, ", "))

	fmt.Fprint(w, "func mwAt_1(i int) int {\n\tmwN_1++\n\treturn i\n}\n\n")
	fmt.Fprintf(w, "func mwPtr_1() *MW_1 {\n\tmwN_1++\n\treturn &mwT_1[%d]\n}\n\n", m.ptrAt)
	fmt.Fprint(w, "func mwHold_1() *MWH_1 {\n\tmwN_1++\n\treturn &mwH_1\n}\n\n")
	a = a[:0]
	for i, fl := range m.fields {
		a = append(a, fmt.Sprintf("%s(p*%d + %d)", fl.typ(), m.mkC[i], m.mkO[i]))
	}
	fmt.Fprintf(w, "func mwMk_1(p int) MW_1 {\n\treturn MW_1{%s}\n}\n\n", strings.Join(a, ", "))
	terms := func(c []int32) string {
		var a []string
		for i, fl := range m.fields {
			a = append(a, fmt.Sprintf("int(v.%s)*%d", fl.name, c[i]))
		}
		return strings.Join(a, " + ")
	}
	fmt.Fprintf(w, "func mwSum_1(v MW_1) int {\n\treturn %s\n}\n\n", terms(m.sumC))
	fmt.Fprintf(w, "func (v MW_1) mix() int {\n\treturn %s\n}\n\n", terms(m.mixC))
	fmt.Fprintf(w, "func (p *MW_1) val() int {\n\treturn int(p.%s)*%d + 1\n}\n\n", m.fields[m.valAt].name, m.valC)
	fmt.Fprint(w, "func mwIf_1(x MWer_1) int {\n\treturn x.val() * 3\n}\n\n")
	fmt.Fprint(w, "func mwStr_1(s string) int {\n\treturn len(s)*7 + int(s[len(s)-1])\n}\n\n")
	fmt.Fprint(w, "func mwWide_1(x int64) int {\n\treturn int(x>>32)*3 + int(x)\n}\n\n")
	fmt.Fprint(w, "func mwSl_1(s []int) int {\n\treturn len(s)*5 + cap(s)*3 + s[len(s)-1]\n}\n\n")
	fmt.Fprint(w, "func mwPair_1(n int, v MW_1) int {\n\treturn n*7 + mwSum_1(v)\n}\n\n")
}

// run writes mwRun, a statement for each of the shapes drawn, in an order drawn,
// and computes what it returns given k.
func (m *multiWord) run(f *Fuzzer, k int32) (string, Int32, bool) {
	r := m.r
	var b strings.Builder
	var res, calls int32
	step := func(v int32) { res = res*31 + v }
	idx := func() int { return r.Intn(3) }
	shapes := []func(){
		func() { // an element indexed by a call
			i := idx()
			fmt.Fprintf(&b, "\tr = r*31 + mwSum_1(mwT_1[mwAt_1(%d)])\n", i)
			step(m.sum(m.table[i]))
			calls++
		},
		func() { // a dereference of a pointer a call returns
			fmt.Fprint(&b, "\tr = r*31 + mwSum_1(*mwPtr_1())\n")
			step(m.sum(m.table[m.ptrAt]))
			calls++
		},
		func() { // a field read through a pointer a call returns
			fmt.Fprint(&b, "\tr = r*31 + mwSum_1(mwHold_1().m)\n")
			step(m.sum(m.hold))
			calls++
		},
		func() { // a value receiver indexed by a call
			i := idx()
			fmt.Fprintf(&b, "\tr = r*31 + mwT_1[mwAt_1(%d)].mix()\n", i)
			step(m.mix(m.table[i]))
			calls++
		},
		func() { // a value receiver through a pointer a call returns
			fmt.Fprint(&b, "\tr = r*31 + mwPtr_1().mix()\n")
			step(m.mix(m.table[m.ptrAt]))
			calls++
		},
		func() { // a call through a function value
			i := idx()
			fmt.Fprintf(&b, "\tfv := mwSum_1\n\tr = r*31 + fv(mwT_1[mwAt_1(%d)])\n", i)
			step(m.sum(m.table[i]))
			calls++
		},
		func() { // not the first argument
			i := idx()
			fmt.Fprintf(&b, "\tr = r*31 + mwPair_1(k, mwT_1[mwAt_1(%d)])\n", i)
			step(k*7 + m.sum(m.table[i]))
			calls++
		},
		func() { // a string
			i := idx()
			fmt.Fprintf(&b, "\tr = r*31 + mwStr_1(mwS_1[mwAt_1(%d)])\n", i)
			step(m.str(m.strs[i]))
			calls++
		},
		func() { // a string comparison, against itself as often as against another
			i, j := idx(), idx()
			if r.Intn(2) == 0 {
				j = i
			}
			fmt.Fprintf(&b, "\tif mwS_1[mwAt_1(%d)] == mwS_1[%d] {\n\t\tr += 3\n\t}\n", i, j)
			if m.strs[i] == m.strs[j] {
				res += 3
			}
			calls++
		},
		func() { // an int64
			i := idx()
			fmt.Fprintf(&b, "\tr = r*31 + mwWide_1(mwW_1[mwAt_1(%d)])\n", i)
			step(m.wide(m.wides[i]))
			calls++
		},
		func() { // a slice
			i := idx()
			fmt.Fprintf(&b, "\tr = r*31 + mwSl_1(mwL_1[mwAt_1(%d)])\n", i)
			step(m.slice(m.slices[i]))
			calls++
		},
		func() { // an interface value
			e := [2]int{idx(), idx()}
			j := r.Intn(2)
			fmt.Fprintf(&b, "\tis := [2]MWer_1{&mwT_1[%d], &mwT_1[%d]}\n\tr = r*31 + mwIf_1(is[mwAt_1(%d)])\n", e[0], e[1], j)
			step(m.val(m.table[e[j]]) * 3)
			calls++
		},
		func() { // a send
			i := idx()
			fmt.Fprintf(&b, "\tc := make(chan MW_1, 1)\n\tc <- mwT_1[mwAt_1(%d)]\n\tr = r*31 + mwSum_1(<-c)\n", i)
			step(m.sum(m.table[i]))
			calls++
		},
		func() { // the length of a string received
			i := idx()
			fmt.Fprintf(&b, "\tcs := make(chan string, 1)\n\tcs <- mwS_1[%d]\n\tr = r*31 + len(<-cs)\n", i)
			step(int32(len(m.strs[i])))
		},
		func() { // the capacity of a slice received
			i := idx()
			fmt.Fprintf(&b, "\tcl := make(chan []int, 1)\n\tcl <- mwL_1[%d]\n\tr = r*31 + cap(<-cl)\n", i)
			step(int32(len(m.slices[i])))
		},
		func() { // a member past the first word of a struct a call returns
			fi := 1 + r.Intn(len(m.fields)-1)
			fmt.Fprintf(&b, "\tr = r*31 + int(mwMk_1(k).%s)\n", m.fields[fi].name)
			step(m.mk(k)[fi])
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
	step(calls)
	fmt.Fprintf(f.Out, "func mwRun_1(k int) int {\n\tr := 0\n\tmwN_1 = 0\n%s\treturn r*31 + mwN_1\n}\n\n", b.String())
	return fmt.Sprintf("mwRun_1(%d)", k), Int32(res), true
}
