// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
)

// swType is an integer type a switch genSwitches writes is on, and the package
// variable of it the switch reads.
type swType struct {
	name   string
	bits   int
	signed bool
	v      string
}

// swTypes are the types of the tags: every integer type of 32 bits or fewer, whose
// switch of constant cases is a C switch, a defined type with constants of its own,
// and int64, whose switch is a chain of compares (switchCaseLabels).
var swTypes = []swType{
	{"int8", 8, true, "swI8_1"}, {"uint8", 8, false, "swU8_1"},
	{"int16", 16, true, "swI16_1"}, {"uint16", 16, false, "swU16_1"},
	{"int32", 32, true, "swI32_1"}, {"uint32", 32, false, "swU32_1"},
	{"int", 32, true, "swN_1"}, {"uint", 32, false, "swUn_1"},
	{"SwK_1", 8, false, "swK_1"}, {"int64", 64, true, "swW_1"},
}

// swKNames are the constants of SwK_1, of the values 0 to 7.
var swKNames = []string{"SwKa_1", "SwKb_1", "SwKc_1", "SwKd_1", "SwKe_1", "SwKf_1", "SwKg_1", "SwKh_1"}

func (t swType) wrap(v int64) int64 {
	if t.bits == 64 {
		return v
	}
	v &= 1<<t.bits - 1
	if t.signed && v >= 1<<(t.bits-1) {
		v -= 1 << t.bits
	}
	return v
}

func (t swType) min() int64 {
	switch {
	case !t.signed:
		return 0
	case t.bits == 64:
		return math.MinInt64
	}
	return -(1 << (t.bits - 1))
}

func (t swType) max() int64 {
	switch {
	case t.bits == 64:
		return math.MaxInt64
	case t.signed:
		return 1<<(t.bits-1) - 1
	}
	return 1<<t.bits - 1
}

// swClause is a clause of a switch genSwitches writes: its case values, or none
// for the default, and what its body does in order -- a break when r is even,
// after adding k2; a nested switch; adding k; falling through.
type swClause struct {
	vals      []int64
	isDefault bool
	brk       bool
	inner     *swStmt
	body      bool
	fall      bool
	k, k2     int32
}

// swStmt is a switch genSwitches writes: its tag, the value the tag has when the
// statement runs, and its clauses.
type swStmt struct {
	typ     swType
	tag     string
	tagV    int64
	clauses []swClause
}

// switches is what genSwitches drew: the package variables' values and the calls
// of swNext_1 made so far.
type switches struct {
	r     *rand.Rand
	vals  map[string]int64
	count int64
}

// genSwitches declares a variable of every integer type a switch is on, and a
// procedure of switches over them whose cases are constants -- a C switch for a tag
// of 32 bits or fewer since 2026-10-16, which the target's C compiler dispatches
// through a table of jumps where the cases are dense and through compares where they
// are not: cases near the tag, far from it and at the ends of its type, a default
// first, between and last, a fallthrough into the clause written next and out of a
// default, a break from inside a clause, a switch inside a clause, a tag that calls
// and is evaluated once, and a switch in a loop that continues it and breaks out of
// it by its label. genSwitchStmt's switches are on an int, every case beside the
// tag, one clause with a body and none falling through, which is what a C switch
// changed least. Like the aggregates (genAggregates), what it comes to is computed
// here, in Go, and it draws from its own source (swRand), so the rest of a seed's
// program is what it was.
func (f *Fuzzer) genSwitches() (call string, want Int32, ok bool) {
	r := f.swRand
	if r.Intn(3) == 0 {
		return "", 0, false
	}
	s := &switches{r: r, vals: map[string]int64{}}
	w := f.Out
	fmt.Fprint(w, "type SwK_1 uint8\n\nconst (\n\t"+swKNames[0]+" SwK_1 = iota\n")
	for _, n := range swKNames[1:] {
		fmt.Fprintf(w, "\t%s\n", n)
	}
	fmt.Fprint(w, ")\n\n")
	for _, t := range swTypes {
		var v int64
		switch {
		case t.bits == 64:
			v = r.Int63() - 1<<62
		case t.name == "SwK_1":
			v = int64(r.Intn(12))
		default:
			v = t.wrap(int64(r.Uint64()))
		}
		s.vals[t.name] = v
		fmt.Fprintf(w, "var %s %s = %d\n\n", t.v, t.name, v)
	}
	fmt.Fprint(w, "var swCount_1 int\n\n")
	fmt.Fprint(w, "func swNext_1() int {\n\tswCount_1++\n\treturn swCount_1*7 - 3\n}\n\n")

	var b strings.Builder
	var res int32
	for i, n := 0, 3+r.Intn(4); i < n; i++ {
		st := s.gen(0)
		s.write(&b, st, "\t")
		res = s.exec(st, res)
	}
	if r.Intn(2) == 0 {
		res = s.loop(&b, res)
	}
	// Every value read, whatever was drawn: one nothing reads is a C variable
	// nothing reads, which the host's -Werror refuses. And the calls of the tag that
	// calls, one each.
	b.WriteString("\tr = r*31 + swCount_1\n")
	res = res*31 + int32(s.count)
	b.WriteString("\tr = r*31 + int(swI8_1) + int(swU8_1) + int(swI16_1) + int(swU16_1) + int(swI32_1) + int(swU32_1) + swN_1 + int(swUn_1) + int(swK_1) + int(swW_1>>40)\n")
	sum := int32(0)
	for _, t := range swTypes {
		v := s.vals[t.name]
		if t.bits == 64 {
			v >>= 40
		}
		sum += int32(v)
	}
	res = res*31 + sum
	fmt.Fprintf(w, "func swRun_1() int {\n\tr := 0\n%s\treturn r\n}\n\n", b.String())
	return "swRun_1()", Int32(res), true
}

// gen draws a switch statement; one at depth 0 stands in swRun_1's body, where it
// runs once, in order, so its tag may call.
func (s *switches) gen(depth int) *swStmt {
	r := s.r
	t := swTypes[r.Intn(len(swTypes))]
	st := &swStmt{typ: t}
	v := s.vals[t.name]
	switch r.Intn(6) {
	case 2:
		k := 1 + r.Intn(t.bits-1)
		st.tag, st.tagV = fmt.Sprintf("%s >> %d", t.v, k), t.wrap(v>>k)
	case 3:
		m := 1 + r.Int63n(min(t.max(), 255))
		st.tag, st.tagV = fmt.Sprintf("%s & %d", t.v, m), t.wrap(v&m)
	case 4:
		c := r.Int63n(min(t.max(), 1000) + 1)
		st.tag, st.tagV = fmt.Sprintf("%s ^ %d", t.v, c), t.wrap(v^c)
	case 5:
		if depth == 0 && t.name == "int" {
			s.count++
			st.tag, st.tagV = "swNext_1()", int64(int32(s.count*7-3))
			break
		}
		fallthrough
	default:
		st.tag, st.tagV = t.v, v
	}

	used := map[int64]bool{}
	hit := r.Intn(4) != 0
	if !hit {
		used[st.tagV] = true // never written
	}
	pick := func() (int64, bool) {
		for try := 0; try < 20; try++ {
			var c int64
			switch r.Intn(7) {
			case 0, 1, 2:
				c = st.tagV + int64(r.Intn(9)) - 3
			case 3:
				if t.bits == 64 {
					c = r.Int63() - 1<<62
				} else {
					c = t.min() + r.Int63n(t.max()-t.min()+1)
				}
			case 4:
				c = []int64{t.min(), t.max(), 0, 1}[r.Intn(4)]
			default:
				c = st.tagV + int64(r.Intn(2001)) - 1000
			}
			if c < t.min() || c > t.max() || used[c] {
				continue
			}
			used[c] = true
			return c, true
		}
		return 0, false
	}
	n := 2 + r.Intn(6)
	for i := 0; i < n; i++ {
		var c swClause
		for j, m := 0, 1+r.Intn(3)/2; j < m; j++ {
			if v, ok := pick(); ok {
				c.vals = append(c.vals, v)
			}
		}
		if len(c.vals) != 0 {
			st.clauses = append(st.clauses, c)
		}
	}
	if len(st.clauses) == 0 {
		st.clauses = append(st.clauses, swClause{vals: []int64{st.tagV}})
		used[st.tagV] = true
	}
	if hit && !s.has(st, st.tagV) {
		c := &st.clauses[r.Intn(len(st.clauses))]
		c.vals = append(c.vals, st.tagV)
	}
	if r.Intn(5) < 3 {
		at := r.Intn(len(st.clauses) + 1)
		st.clauses = append(st.clauses[:at], append([]swClause{{isDefault: true}}, st.clauses[at:]...)...)
	}
	for i := range st.clauses {
		c := &st.clauses[i]
		c.body = r.Intn(7) != 0
		c.k, c.k2 = int32(1+r.Intn(999)), int32(1+r.Intn(99))
		c.brk = r.Intn(4) == 0
		c.fall = i+1 < len(st.clauses) && r.Intn(10) < 3
		if depth == 0 && r.Intn(10) == 0 {
			c.inner = s.gen(depth + 1)
		}
	}
	return st
}

// has reports whether a case of st is the value v.
func (s *switches) has(st *swStmt, v int64) bool {
	for _, c := range st.clauses {
		for _, x := range c.vals {
			if x == v {
				return true
			}
		}
	}
	return false
}

// exec is what st does to r, as Go runs it.
func (s *switches) exec(st *swStmt, r int32) int32 {
	taken := -1
	for i, c := range st.clauses {
		for _, v := range c.vals {
			if v == st.tagV {
				taken = i
			}
		}
	}
	if taken < 0 {
		for i, c := range st.clauses {
			if c.isDefault {
				taken = i
			}
		}
	}
	for i := taken; i >= 0 && i < len(st.clauses); i++ {
		c := st.clauses[i]
		if c.brk && r&1 == 0 {
			r += c.k2
			break
		}
		if c.inner != nil {
			r = s.exec(c.inner, r)
		}
		if c.body {
			r = r*31 + c.k
		}
		if !c.fall {
			break
		}
	}
	return r
}

// write writes st at the indent ind.
func (s *switches) write(b *strings.Builder, st *swStmt, ind string) {
	fmt.Fprintf(b, "%sswitch %s {\n", ind, st.tag)
	for _, c := range st.clauses {
		if c.isDefault {
			fmt.Fprintf(b, "%sdefault:\n", ind)
		} else {
			var vs []string
			for _, v := range c.vals {
				vs = append(vs, s.spell(st.typ, v))
			}
			fmt.Fprintf(b, "%scase %s:\n", ind, strings.Join(vs, ", "))
		}
		in := ind + "\t"
		if c.brk {
			fmt.Fprintf(b, "%sif r&1 == 0 {\n%s\tr += %d\n%s\tbreak\n%s}\n", in, in, c.k2, in, in)
		}
		if c.inner != nil {
			s.write(b, c.inner, in)
		}
		if c.body {
			fmt.Fprintf(b, "%sr = r*31 + %d\n", in, c.k)
		}
		if c.fall {
			fmt.Fprintf(b, "%sfallthrough\n", in)
		}
	}
	fmt.Fprintf(b, "%s}\n", ind)
}

// spell writes the case value v of the type t: a constant of SwK_1 by its name
// where it has one, now and then, and an unsigned value in hex now and then.
func (s *switches) spell(t swType, v int64) string {
	switch {
	case t.name == "SwK_1" && v < int64(len(swKNames)) && s.r.Intn(2) == 0:
		return swKNames[v]
	case !t.signed && s.r.Intn(3) == 0:
		return fmt.Sprintf("%#x", v)
	}
	return fmt.Sprint(v)
}

// loop writes a switch in a loop of n passes, on a value of the pass: a clause
// continuing the loop, one breaking out of it by its label, and the others adding
// to r, and after the switch the pass is added too.
func (s *switches) loop(b *strings.Builder, r int32) int32 {
	rnd := s.r
	n, a, c := 5+rnd.Intn(8), 1+rnd.Intn(7), rnd.Intn(8)
	perm := rnd.Perm(8)
	cont, brk := perm[0], perm[1]
	others := perm[2 : 2+rnd.Intn(4)]
	ks := make([]int32, len(others))
	for i := range ks {
		ks[i] = int32(1 + rnd.Intn(999))
	}
	kb, kd := int32(1+rnd.Intn(999)), int32(1+rnd.Intn(999))
	def := rnd.Intn(2) == 0
	fmt.Fprintf(b, "swL_1:\n\tfor i := 0; i < %d; i++ {\n\t\tswitch (i*%d + %d) & 7 {\n", n, a, c)
	fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tcontinue\n", cont)
	fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tr = r*31 + %d\n\t\t\tbreak swL_1\n", brk, kb)
	for i, v := range others {
		fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tr = r*31 + %d\n", v, ks[i])
	}
	if def {
		fmt.Fprintf(b, "\t\tdefault:\n\t\t\tr = r*31 + %d\n", kd)
	}
	b.WriteString("\t\t}\n\t\tr = r*31 + i\n\t}\n")
	for i := 0; i < n; i++ {
		t := (i*a + c) & 7
		if t == cont {
			continue
		}
		if t == brk {
			r = r*31 + kb
			break
		}
		found := false
		for j, v := range others {
			if t == v {
				r, found = r*31+ks[j], true
			}
		}
		if !found && def {
			r = r*31 + kd
		}
		r = r*31 + int32(i)
	}
	return r
}
