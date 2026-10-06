// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"math/rand"
	"strings"
)

// agKind is an integer kind a field of an aggregate is drawn from: its width and
// whether it is signed, which is all its arithmetic needs.
type agKind struct {
	name   string
	bits   int
	signed bool
}

var agKinds = []agKind{
	{"int8", 8, true},
	{"uint8", 8, false},
	{"int16", 16, true},
	{"uint16", 16, false},
	{"int32", 32, true},
	{"uint32", 32, false},
	{"int64", 64, true},
	{"uint64", 64, false},
}

// wrap is v converted to k, as Go converts an integer: the low bits kept, and
// extended by the sign for a signed kind. A 64-bit kind's value is its bit pattern
// in an int64, which Go's int64 arithmetic wraps as uint64's does.
func (k agKind) wrap(v int64) int64 {
	if k.bits == 64 {
		return v
	}
	mask := int64(1)<<k.bits - 1
	v &= mask
	if k.signed && v&(int64(1)<<(k.bits-1)) != 0 {
		v -= int64(1) << k.bits
	}
	return v
}

// shr is v >> n in k: arithmetic for a signed kind, logical for an unsigned one.
func (k agKind) shr(v int64, n uint) int64 {
	if k.signed {
		return v >> n
	}
	if k.bits == 64 {
		return int64(uint64(v) >> n)
	}
	return v >> n // v is zero-extended already
}

// agField is a field of an aggregate: a scalar, n == 0, or an array of n.
type agField struct {
	name string
	k    agKind
	n    int
}

// elems is how many values the field holds.
func (fl agField) elems() int { return max(fl.n, 1) }

// ref is the field's i-th value read off base.
func (fl agField) ref(base string, i int) string {
	if fl.n == 0 {
		return base + "." + fl.name
	}
	return fmt.Sprintf("%s.%s[%d]", base, fl.name, i)
}

// agValue is a value of an aggregate, field by field and element by element.
type agValue [][]int64

func (v agValue) clone() agValue {
	r := make(agValue, len(v))
	for i := range v {
		r[i] = append([]int64(nil), v[i]...)
	}
	return r
}

func (v agValue) equal(w agValue) bool {
	for i := range v {
		for j := range v[i] {
			if v[i][j] != w[i][j] {
				return false
			}
		}
	}
	return true
}

// agOp is an update of one value of a field, `F op= T(...)`, that a by-value
// parameter, a pointer and a method apply: the text written for the receiver
// named and what it does to the value.
type agOp struct {
	field, elem int
	text        func(base, d string) string
	apply       func(cur, d int64) int64
}

// aggregate is a struct type genAggregates declared, with its functions.
type aggregate struct {
	id     int
	typ    string
	fields []agField
	mkC    [][]int64 // agMk's coefficient and offset per value: T(p*c + o)
	mkO    [][]int64
	mod    []agOp // agMod's updates of its by-value parameter, d its argument
	ptr    []agOp // agPtr's updates through its pointer
	bump   agOp   // the pointer method's
	sumC   [][]int64
	hiC    []int64 // the high half of each 64-bit field, its coefficient; 0 for none
	pairAt [2]int  // the value agPair sets to its argument
	r      *rand.Rand
}

func (a *aggregate) name(s string) string { return fmt.Sprintf("%s_%d", s, a.id) }

// mk is agMk(p).
func (a *aggregate) mk(p int64) agValue {
	v := make(agValue, len(a.fields))
	for i, fl := range a.fields {
		v[i] = make([]int64, fl.elems())
		for j := range v[i] {
			v[i][j] = fl.k.wrap(int64(int32(p*a.mkC[i][j] + a.mkO[i][j])))
		}
	}
	return v
}

func (a *aggregate) apply(v agValue, ops []agOp, d int64) agValue {
	v = v.clone()
	for _, op := range ops {
		k := a.fields[op.field].k
		v[op.field][op.elem] = k.wrap(op.apply(v[op.field][op.elem], d))
	}
	return v
}

// toInt is int(x) of a value of k on a 32-bit int: its low 32 bits.
func toInt(v int64) int32 { return int32(v) }

// sum is agSum(v), in int arithmetic.
func (a *aggregate) sum(v agValue) int32 {
	var r int32
	for i, fl := range a.fields {
		for j := range v[i] {
			r += toInt(v[i][j]) * int32(a.sumC[i][j])
		}
		if a.hiC[i] != 0 {
			r += toInt(fl.k.shr(v[i][0], 32)) * int32(a.hiC[i])
		}
	}
	return r
}

// genAggregates declares, at package scope, a struct of fields of mixed widths --
// sub-word integers, arrays of them, 64-bit ones -- with functions taking it BY
// VALUE and by pointer, returning it alone and beside an int, and taking an ARRAY
// of it by value, and a procedure that runs them where a value is wanted, where a
// call stands, through a slice of an array of them, a method of each receiver, and
// equality; main folds what the procedure returns.
//
// That is the family the target's C compiler has been wrong about in silence most
// often -- a struct of more than four words, or of a sub-word member, returned
// through a reference it sizes wrongly (flexprop#113), a struct holding an array
// copied by assignment at some sizes and not at others, a static initializer laid
// out otherwise than the type -- and no generated program passed or returned a
// struct or an array at all. Like the tables (genStaticTables), what the procedure
// comes to is computed here, in Go, with Go's integer semantics, and not by the VM.
func (f *Fuzzer) genAggregates() (call string, want Int32, ok bool) {
	r := f.aggRand
	if r.Intn(3) == 0 {
		return "", 0, false
	}
	// One a program, named by its own prefix: minted from VarSeq, it shifted every
	// name after it.
	a := &aggregate{id: 1, r: r}
	a.typ = a.name("AG")
	names := []string{"h", "w", "x", "q", "t", "u", "b", "c", "m", "k"}
	r.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	nf := 3 + r.Intn(4)
	for i := 0; i < nf; i++ {
		k := agKinds[r.Intn(len(agKinds))]
		if i == 0 {
			k = agKinds[r.Intn(4)] // a sub-word member always
		}
		fl := agField{name: names[i], k: k}
		if r.Intn(3) == 0 || i == 1 {
			fl.k = agKinds[r.Intn(6)] // an array of 8-, 16- or 32-bit elements
			fl.n = 1 + r.Intn(6)
		}
		a.fields = append(a.fields, fl)
	}
	// And at least one 64-bit scalar, which every program needs for the widths the
	// faults were met at.
	if !func() bool {
		for _, fl := range a.fields {
			if fl.n == 0 && fl.k.bits == 64 {
				return true
			}
		}
		return false
	}() {
		a.fields = append(a.fields, agField{name: names[nf], k: agKinds[6+r.Intn(2)]})
	}
	draw := func(lo, hi int) int64 { return int64(lo + r.Intn(hi-lo+1)) }
	for _, fl := range a.fields {
		var cs, os, ss []int64
		for j := 0; j < fl.elems(); j++ {
			c := draw(-60, 60)
			if c == 0 {
				c = 1
			}
			cs, os, ss = append(cs, c), append(os, draw(-2000, 2000)), append(ss, draw(1, 19))
		}
		a.mkC, a.mkO, a.sumC = append(a.mkC, cs), append(a.mkO, os), append(a.sumC, ss)
		hi := int64(0)
		if fl.k.bits == 64 {
			hi = draw(1, 19)
		}
		a.hiC = append(a.hiC, hi)
	}
	// An update of a value picked at random, in one of four shapes.
	newOp := func() agOp {
		i := r.Intn(len(a.fields))
		fl := a.fields[i]
		j := r.Intn(fl.elems())
		c := draw(2, 9)
		k := fl.k
		op := agOp{field: i, elem: j}
		switch r.Intn(4) {
		case 0:
			op.text = func(base, d string) string { return fmt.Sprintf("%s += %s(%s * %d)", fl.ref(base, j), k.name, d, c) }
			op.apply = func(cur, d int64) int64 { return cur + k.wrap(int64(int32(d*c))) }
		case 1:
			op.text = func(base, d string) string { return fmt.Sprintf("%s ^= %s(%s + %d)", fl.ref(base, j), k.name, d, c) }
			op.apply = func(cur, d int64) int64 { return cur ^ k.wrap(int64(int32(d+c))) }
		case 2:
			op.text = func(base, d string) string { return fmt.Sprintf("%s -= %s(%s)", fl.ref(base, j), k.name, d) }
			op.apply = func(cur, d int64) int64 { return cur - k.wrap(d) }
		default:
			op.text = func(base, d string) string { return fmt.Sprintf("%s *= %d", fl.ref(base, j), c) }
			op.apply = func(cur, d int64) int64 { return cur * c }
		}
		return op
	}
	for i, n := 0, 1+r.Intn(3); i < n; i++ {
		a.mod = append(a.mod, newOp())
	}
	for i, n := 0, 1+r.Intn(2); i < n; i++ {
		a.ptr = append(a.ptr, newOp())
	}
	a.bump = newOp()
	pi := r.Intn(len(a.fields))
	a.pairAt = [2]int{pi, r.Intn(a.fields[pi].elems())}
	a.write(f)

	k := func() int64 { return draw(-200, 200) }
	d := func() int64 { return draw(-50, 50) }
	return a.run(f, k, d)
}

// write declares the type and its functions.
func (a *aggregate) write(f *Fuzzer) {
	w := f.Out
	fmt.Fprintf(w, "type %s struct {\n", a.typ)
	for _, fl := range a.fields {
		if fl.n == 0 {
			fmt.Fprintf(w, "\t%s %s\n", fl.name, fl.k.name)
		} else {
			fmt.Fprintf(w, "\t%s [%d]%s\n", fl.name, fl.n, fl.k.name)
		}
	}
	fmt.Fprint(w, "}\n\n")

	fmt.Fprintf(w, "func %s(p int) %s {\n\tvar v %s\n", a.name("agMk"), a.typ, a.typ)
	for i, fl := range a.fields {
		for j := 0; j < fl.elems(); j++ {
			fmt.Fprintf(w, "\t%s = %s(p*%d + %d)\n", fl.ref("v", j), fl.k.name, a.mkC[i][j], a.mkO[i][j])
		}
	}
	fmt.Fprint(w, "\treturn v\n}\n\n")

	fmt.Fprintf(w, "func %s(v %s, d int) %s {\n", a.name("agMod"), a.typ, a.typ)
	for _, op := range a.mod {
		fmt.Fprintf(w, "\t%s\n", op.text("v", "d"))
	}
	fmt.Fprint(w, "\treturn v\n}\n\n")

	var terms []string
	for i, fl := range a.fields {
		for j := 0; j < fl.elems(); j++ {
			terms = append(terms, fmt.Sprintf("int(%s)*%d", fl.ref("v", j), a.sumC[i][j]))
		}
		if a.hiC[i] != 0 {
			terms = append(terms, fmt.Sprintf("int(%s>>32)*%d", fl.ref("v", 0), a.hiC[i]))
		}
	}
	fmt.Fprintf(w, "func %s(v %s) int {\n\treturn %s\n}\n\n", a.name("agSum"), a.typ, strings.Join(terms, " + "))

	fmt.Fprintf(w, "func %s(p *%s, d int) {\n", a.name("agPtr"), a.typ)
	for _, op := range a.ptr {
		fmt.Fprintf(w, "\t%s\n", op.text("p", "d"))
	}
	fmt.Fprint(w, "}\n\n")

	fmt.Fprintf(w, "func %s(a [3]%s, d int) int {\n\ta[1] = %s(a[1], d)\n\treturn %s(a[0]) + %s(a[1])*7 + %s(a[2])\n}\n\n",
		a.name("agArr"), a.typ, a.name("agMod"), a.name("agSum"), a.name("agSum"), a.name("agSum"))

	pf := a.fields[a.pairAt[0]]
	fmt.Fprintf(w, "func %s(v %s, d int) (%s, int) {\n\t%s = %s(d)\n\treturn v, %s(v)\n}\n\n",
		a.name("agPair"), a.typ, a.typ, pf.ref("v", a.pairAt[1]), pf.k.name, a.name("agSum"))

	fmt.Fprintf(w, "func (v %s) sum() int { return %s(v) }\n\n", a.typ, a.name("agSum"))
	fmt.Fprintf(w, "func (v *%s) bump(d int) {\n\t%s\n}\n\n", a.typ, a.bump.text("v", "d"))
}

// run writes the procedure main calls and computes what it returns.
func (a *aggregate) run(f *Fuzzer, k, d func() int64) (string, Int32, bool) {
	w := f.Out
	var r int32
	step := func(v int32) { r = r*31 + v }
	mk, mod, sum := a.name("agMk"), a.name("agMod"), a.name("agSum")
	proc := a.name("agRun")
	fmt.Fprintf(w, "func %s() int {\n\tr := 0\n", proc)

	k1, d1 := k(), d()
	fmt.Fprintf(w, "\tv := %s(%d)\n\tw := %s(v, %d)\n", mk, k1, mod, d1)
	v := a.mk(k1)
	wv := a.apply(v, a.mod, d1)
	// v is unchanged by the callee: it was handed a copy.
	fmt.Fprintf(w, "\tr = r*31 + %s(v)\n\tr = r*31 + %s(w)\n", sum, sum)
	step(a.sum(v))
	step(a.sum(wv))

	// A struct where a call stands: a call's result handed to another.
	k3, d3 := k(), d()
	fmt.Fprintf(w, "\tr = r*31 + %s(%s(%s(%d), %d))\n", sum, mod, mk, k3, d3)
	step(a.sum(a.apply(a.mk(k3), a.mod, d3)))

	// Through a pointer, then a value-receiver method.
	d5 := d()
	fmt.Fprintf(w, "\t%s(&v, %d)\n\tr = r*31 + v.sum()\n", a.name("agPtr"), d5)
	v = a.apply(v, a.ptr, d5)
	step(a.sum(v))

	// An array of them by value: the callee's update of its copy reaches nothing
	// here.
	k6, d7 := k(), d()
	fmt.Fprintf(w, "\tvar a [3]%s\n\ta[0], a[1], a[2] = v, w, %s(%d)\n", a.typ, mk, k6)
	arr := [3]agValue{v.clone(), wv.clone(), a.mk(k6)}
	fmt.Fprintf(w, "\tr = r*31 + %s(a, %d)\n\tr = r*31 + %s(a[1])\n", a.name("agArr"), d7, sum)
	step(a.sum(arr[0]) + a.sum(a.apply(arr[1], a.mod, d7))*7 + a.sum(arr[2]))
	step(a.sum(arr[1]))

	// A struct beside an int.
	d8 := d()
	fmt.Fprintf(w, "\tu, n := %s(w, %d)\n\tr = r*31 + %s(u) + n\n", a.name("agPair"), d8, sum)
	u := wv.clone()
	pf := a.fields[a.pairAt[0]]
	u[a.pairAt[0]][a.pairAt[1]] = pf.k.wrap(d8)
	step(a.sum(u) + a.sum(u))

	// A value read off a call's result, and a shift of one.
	k9 := k()
	fi := a.r.Intn(len(a.fields))
	fj := a.r.Intn(a.fields[fi].elems())
	gi := a.r.Intn(len(a.fields))
	d10 := d()
	fmt.Fprintf(w, "\tr = r*31 + int(%s) + int(%s>>3)\n",
		a.fields[fi].ref(fmt.Sprintf("%s(%d)", mk, k9), fj), a.fields[gi].ref(fmt.Sprintf("%s(v, %d)", mod, d10), 0))
	step(toInt(a.mk(k9)[fi][fj]) + toInt(a.fields[gi].k.shr(a.apply(v, a.mod, d10)[gi][0], 3)))

	// The pointer method on an element of a slice of the array: the slice shares
	// the array's backing, so the array sees it.
	d11 := d()
	fmt.Fprintf(w, "\ts := a[:]\n\ts[2].bump(%d)\n\tr = r*31 + %s(a[2])\n", d11, sum)
	arr[2] = a.apply(arr[2], []agOp{a.bump}, d11)
	step(a.sum(arr[2]))

	// Equality of two of them, a copy and the copy changed.
	ei := a.r.Intn(len(a.fields))
	ej := a.r.Intn(a.fields[ei].elems())
	fmt.Fprintf(w, "\te := v\n\tif e == v {\n\t\tr++\n\t}\n\t%s++\n\tif e != v {\n\t\tr += 2\n\t}\n", a.fields[ei].ref("e", ej))
	e := v.clone()
	if e.equal(v) {
		r++
	}
	e[ei][ej] = a.fields[ei].k.wrap(e[ei][ej] + 1)
	if !e.equal(v) {
		r += 2
	}

	// Over a channel to a cog of its own and back, half the time: a struct holding
	// an array crosses a channel by pointer (the emitter's chanStructByPtr), and the
	// rendezvous copies it. Drawn last, and its cog written after the procedure, so
	// an aggregate drawing none is the program it was before this was added.
	cog := a.r.Intn(2) == 0
	var dc int64
	if cog {
		dc = d()
		k12 := k()
		in, out := a.name("agIn"), a.name("agOut")
		fmt.Fprintf(w, "\tgo %s(2)\n\t%s <- v\n\tx := <-%s\n\t%s <- %s(%d)\n\ty := <-%s\n\tr = r*31 + %s(x) + %s(y)*3\n",
			a.name("agCog"), in, out, in, mk, k12, out, sum, sum)
		step(a.sum(a.apply(v, a.mod, dc)) + a.sum(a.apply(a.mk(k12), a.mod, dc))*3)
	}
	fmt.Fprint(w, "\treturn r\n}\n\n")
	if cog {
		in, out := a.name("agIn"), a.name("agOut")
		fmt.Fprintf(w, "var %s chan %s\n\nvar %s chan %s\n\n", in, a.typ, out, a.typ)
		fmt.Fprintf(w, "func %s(n int) {\n\tfor i := 0; i < n; i++ {\n\t\tv := <-%s\n\t\t%s <- %s(v, %d)\n\t}\n}\n\n",
			a.name("agCog"), in, out, mod, dc)
	}
	return proc + "()", Int32(r), true
}
