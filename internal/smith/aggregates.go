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

	anN, anZ agKind   // the nesting struct's scalars
	anC      [4]int64 // anSum's coefficients
	anEq     [2]int   // the value of q.s[1] agNest changes
	trioAt   [2]int   // the value agTrioRun reads off a call's array result
	lateM    [3]int64 // what agLate's deferred calls multiply by
	viaK     int64    // agImpl's k
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

	// Five more shapes, each half the time and each in a function of its own, so
	// the procedure's registers stay what they were: a struct nesting the aggregate
	// and an array of it, an array of it returned by value, deferred calls taking
	// it, whose copies are made at the defer, calls through function values and an
	// interface taking and returning it, and its array field read, sliced and copied
	// through indexes, bounds and a make length of 64 bits. Drawn after everything
	// above, for the reason the cog is.
	var later []func()
	if a.r.Intn(2) == 0 {
		kn, dn := k(), d()
		fmt.Fprintf(w, "\tr = r*31 + %s(%d, %d)\n", a.name("agNest"), kn, dn)
		step(a.nest(kn, dn))
		later = append(later, func() { a.writeNest(f) })
	}
	if a.r.Intn(2) == 0 {
		kt, dt := k(), d()
		fmt.Fprintf(w, "\tr = r*31 + %s(%d, %d)\n", a.name("agTrioRun"), kt, dt)
		step(a.trioRun(kt, dt))
		later = append(later, func() { a.writeTrio(f) })
	}
	if a.r.Intn(2) == 0 {
		dl := d()
		fmt.Fprintf(w, "\tr = r*31 + %s(w, %d)\n\tr = r*31 + %s\n", a.name("agLate"), dl, a.name("agLog"))
		ret, log := a.late(wv, dl)
		step(ret)
		step(log)
		later = append(later, func() { a.writeLate(f) })
	}
	if a.r.Intn(2) == 0 {
		kv, dv := k(), d()
		fmt.Fprintf(w, "\tr = r*31 + %s(%d, %d)\n", a.name("agVia"), kv, dv)
		step(a.via(kv, dv))
		later = append(later, func() { a.writeVia(f) })
	}
	if a.r.Intn(2) == 0 {
		ki, di := k(), d()
		fmt.Fprintf(w, "\tr = r*31 + %s(%d, %d)\n", a.name("agIdx"), ki, di)
		step(a.idx(ki, di))
		later = append(later, func() { a.writeIdx(f) })
	}
	fmt.Fprint(w, "\treturn r\n}\n\n")
	if cog {
		in, out := a.name("agIn"), a.name("agOut")
		fmt.Fprintf(w, "var %s chan %s\n\nvar %s chan %s\n\n", in, a.typ, out, a.typ)
		fmt.Fprintf(w, "func %s(n int) {\n\tfor i := 0; i < n; i++ {\n\t\tv := <-%s\n\t\t%s <- %s(v, %d)\n\t}\n}\n\n",
			a.name("agCog"), in, out, mod, dc)
	}
	for _, f := range later {
		f()
	}
	return proc + "()", Int32(r), true
}

// zero is the aggregate's zero value.
func (a *aggregate) zero() agValue {
	v := make(agValue, len(a.fields))
	for i, fl := range a.fields {
		v[i] = make([]int64, fl.elems())
	}
	return v
}

// agNested is a value of the struct agNest nests the aggregate in.
type agNested struct {
	n, z int64
	g    agValue
	s    [2]agValue
}

func (o agNested) clone() agNested {
	return agNested{o.n, o.z, o.g.clone(), [2]agValue{o.s[0].clone(), o.s[1].clone()}}
}

func (o agNested) equal(q agNested) bool {
	return o.n == q.n && o.z == q.z && o.g.equal(q.g) && o.s[0].equal(q.s[0]) && o.s[1].equal(q.s[1])
}

// nest is agNest(k, d), drawing what the nesting struct is made of first.
func (a *aggregate) nest(k, d int64) int32 {
	r := a.r
	a.anN, a.anZ = agKinds[r.Intn(4)], agKinds[r.Intn(len(agKinds))]
	for i := range a.anC {
		a.anC[i] = int64(2 + r.Intn(8))
	}
	ei := r.Intn(len(a.fields))
	a.anEq = [2]int{ei, r.Intn(a.fields[ei].elems())}

	mk := func(p int64) agNested {
		return agNested{
			n: a.anN.wrap(int64(int32(p * 3))),
			z: a.anZ.wrap(p),
			g: a.mk(p),
			s: [2]agValue{a.mk(p + 1), a.apply(a.mk(p), a.mod, p)},
		}
	}
	sum := func(o agNested) int32 {
		return toInt(o.n)*int32(a.anC[0]) + a.sum(o.g)*int32(a.anC[1]) + a.sum(o.s[0])*int32(a.anC[2]) +
			a.sum(o.s[1])*int32(a.anC[3]) + toInt(o.z)
	}
	var res int32
	o := mk(k)
	o.s[1] = a.apply(o.s[1], []agOp{a.bump}, d)
	o.g = a.apply(o.s[0], a.mod, d)
	o.s[0] = a.apply(o.s[0], a.ptr, d)
	res = res*31 + sum(o)
	q := o.clone()
	if q.equal(o) {
		res++
	}
	q.s[1][ei][a.anEq[1]] = a.fields[ei].k.wrap(q.s[1][ei][a.anEq[1]] + 1)
	if !q.equal(o) {
		res += 2
	}
	zero := agNested{g: a.zero(), s: [2]agValue{a.zero(), a.zero()}}
	return res*31 + sum(o) + sum(zero)
}

// writeNest declares the nesting struct and agNest.
func (a *aggregate) writeNest(f *Fuzzer) {
	w := f.Out
	an, mk, sum := a.name("AN"), a.name("anMk"), a.name("anSum")
	fmt.Fprintf(w, "type %s struct {\n\tn %s\n\tg %s\n\ts [2]%s\n\tz %s\n}\n\n", an, a.anN.name, a.typ, a.typ, a.anZ.name)
	fmt.Fprintf(w, "func %s(p int) %s {\n\treturn %s{n: %s(p * 3), g: %s(p), s: [2]%s{%s(p + 1), %s(%s(p), p)}, z: %s(p)}\n}\n\n",
		mk, an, an, a.anN.name, a.name("agMk"), a.typ, a.name("agMk"), a.name("agMod"), a.name("agMk"), a.anZ.name)
	fmt.Fprintf(w, "func %s(o %s) int {\n\treturn int(o.n)*%d + %s(o.g)*%d + %s(o.s[0])*%d + o.s[1].sum()*%d + int(o.z)\n}\n\n",
		sum, an, a.anC[0], a.name("agSum"), a.anC[1], a.name("agSum"), a.anC[2], a.anC[3])
	fl := a.fields[a.anEq[0]]
	fmt.Fprintf(w, "func %s(k, d int) int {\n\tr := 0\n\to := %s(k)\n\to.s[1].bump(d)\n\to.g = %s(o.s[0], d)\n\t%s(&o.s[0], d)\n"+
		"\tr = r*31 + %s(o)\n\tq := o\n\tif q == o {\n\t\tr++\n\t}\n\t%s++\n\tif q != o {\n\t\tr += 2\n\t}\n"+
		"\tvar na [2]%s\n\tna[1] = o\n\tr = r*31 + %s(na[1]) + %s(na[0])\n\treturn r\n}\n\n",
		a.name("agNest"), mk, a.name("agMod"), a.name("agPtr"), sum, fl.ref("q.s[1]", a.anEq[1]), an, sum, sum)
}

// trio is agTrio(p).
func (a *aggregate) trio(p int64) [3]agValue {
	return [3]agValue{a.mk(p), a.apply(a.mk(p+7), []agOp{a.bump}, p), a.mk(p + 14)}
}

// trioRun is agTrioRun(k, d), drawing the value it reads off a call first.
func (a *aggregate) trioRun(k, d int64) int32 {
	fi := a.r.Intn(len(a.fields))
	a.trioAt = [2]int{fi, a.r.Intn(a.fields[fi].elems())}
	var r int32
	step := func(v int32) { r = r*31 + v }
	t := a.trio(k)
	u := a.trio(k + 1)
	step(a.sum(u[0]) + a.sum(a.apply(u[1], a.mod, d))*7 + a.sum(u[2]) + a.sum(t[2]))
	step(a.sum(a.trio(k + 2)[1]))
	step(toInt(a.trio(k + 3)[0][fi][a.trioAt[1]]))
	for _, e := range a.trio(k + 4) {
		step(a.sum(e))
	}
	return r
}

// writeTrio declares agTrio, returning an array of the aggregate, and agTrioRun,
// which reads one where the call stands: an argument, an element's method, an
// element's field and a range.
func (a *aggregate) writeTrio(f *Fuzzer) {
	w := f.Out
	trio, sum := a.name("agTrio"), a.name("agSum")
	fmt.Fprintf(w, "func %s(p int) [3]%s {\n\tvar t [3]%s\n\tfor i := range t {\n\t\tt[i] = %s(p + i*7)\n\t}\n\tt[1].bump(p)\n\treturn t\n}\n\n",
		trio, a.typ, a.typ, a.name("agMk"))
	fl := a.fields[a.trioAt[0]]
	fmt.Fprintf(w, "func %s(k, d int) int {\n\tr := 0\n\tt := %s(k)\n\tr = r*31 + %s(%s(k+1), d) + %s(t[2])\n"+
		"\tr = r*31 + %s(k + 2)[1].sum()\n\tr = r*31 + int(%s)\n\tfor _, e := range %s(k + 4) {\n\t\tr = r*31 + e.sum()\n\t}\n\treturn r\n}\n\n",
		a.name("agTrioRun"), trio, a.name("agArr"), trio, sum, trio, fl.ref(trio+"(k + 3)[0]", a.trioAt[1]), trio)
}

// late is agLate(v, d) and what agLog holds after it, drawing the multipliers
// first.
func (a *aggregate) late(v agValue, d int64) (ret, log int32) {
	for i := range a.lateM {
		a.lateM[i] = int64(2 + a.r.Intn(8))
	}
	s := a.sum(v)
	// The deferred calls run last first, each with the value v had at its defer.
	log = s * int32(a.lateM[0])
	log = log*31 + s*int32(a.lateM[1])
	log = log*31 + s*int32(a.lateM[2])
	return a.sum(a.apply(a.apply(v, []agOp{a.bump}, d), a.ptr, d)), log
}

// writeLate declares agLate, deferring a function, a function literal and a
// value-receiver method each given the aggregate, which it changes afterwards.
func (a *aggregate) writeLate(f *Fuzzer) {
	w := f.Out
	log, sum := a.name("agLog"), a.name("agSum")
	fmt.Fprintf(w, "var %s int\n\n", log)
	fmt.Fprintf(w, "func (v %s) note(m int) { %s = %s*31 + %s(v)*m }\n\n", a.typ, log, log, sum)
	fmt.Fprintf(w, "func %s(v %s, m int) { %s = %s*31 + %s(v)*m }\n\n", a.name("agNote"), a.typ, log, log, sum)
	fmt.Fprintf(w, "func %s(v %s, d int) int {\n\t%s = 0\n\tdefer v.note(%d)\n"+
		"\tdefer func(w %s, m int) { %s = %s*31 + %s(w)*m }(v, %d)\n\tdefer %s(v, %d)\n"+
		"\tv.bump(d)\n\t%s(&v, d)\n\treturn %s(v)\n}\n\n",
		a.name("agLate"), a.typ, log, a.lateM[2], a.typ, log, log, sum, a.lateM[1], a.name("agNote"), a.lateM[0], a.name("agPtr"), sum)
}

// via is agVia(k, d), drawing the implementation's constant first.
func (a *aggregate) via(k, d int64) int32 {
	a.viaK = int64(2 + a.r.Intn(8))
	var r int32
	step := func(v int32) { r = r*31 + v }
	v := a.mk(k)
	step(a.sum(a.apply(v, a.mod, d)))
	step(a.sum(a.apply(a.mk(k+1), a.mod, d)))
	step(a.sum(a.apply(v, a.mod, d)))
	step(a.sum(a.apply(v, []agOp{a.bump}, d+1)))
	w := a.apply(v, a.mod, d+a.viaK)
	step(a.sum(w) + a.sum(w)*int32(a.viaK))
	step(a.sum(a.apply(a.mk(k+2), a.mod, d+a.viaK)) * int32(a.viaK))
	step(a.sum(a.apply(v, a.mod, d+a.viaK)))
	step(a.sum(v) + 1)
	step(a.sum(v))
	return r
}

// writeVia declares agVia, which hands the aggregate by value to a function value
// -- a local, a package variable, an element of a table, a literal -- to an
// interface's methods, and to a method value, each a call through a pointer.
func (a *aggregate) writeVia(f *Fuzzer) {
	w := f.Out
	iface, impl, mod, sum := a.name("AGer"), a.name("agImpl"), a.name("agMod"), a.name("agSum")
	fn, fns, imp := a.name("agFn"), a.name("agFns"), a.name("agImp")
	fmt.Fprintf(w, "type %s interface {\n\tMix(v %s, d int) %s\n\tSum(v %s) int\n}\n\n", iface, a.typ, a.typ, a.typ)
	fmt.Fprintf(w, "type %s struct {\n\tk int\n}\n\n", impl)
	fmt.Fprintf(w, "func (m *%s) Mix(v %s, d int) %s {\n\tv = %s(v, d+m.k)\n\treturn v\n}\n\n", impl, a.typ, a.typ, mod)
	fmt.Fprintf(w, "func (m *%s) Sum(v %s) int { return %s(v) * m.k }\n\n", impl, a.typ, sum)
	fmt.Fprintf(w, "var %s = %s\n\nvar %s [2]func(%s, int) %s\n\nvar %s = %s{k: %d}\n\n", fn, mod, fns, a.typ, a.typ, imp, impl, a.viaK)
	fmt.Fprintf(w, "func %s(k, d int) int {\n\tr := 0\n\tf := %s\n\tv := %s(k)\n\tr = r*31 + %s(f(v, d))\n"+
		"\tr = r*31 + %s(%s(%s(k+1), d))\n"+
		"\t%s[0], %s[1] = %s, func(w %s, e int) %s {\n\t\tw.bump(e)\n\t\treturn w\n\t}\n"+
		"\tfor i := range %s {\n\t\tr = r*31 + %s(%s[i](v, d+i))\n\t}\n"+
		"\tvar i %s = &%s\n\tw := i.Mix(v, d)\n\tr = r*31 + %s(w) + i.Sum(w)\n"+
		"\tr = r*31 + i.Sum(i.Mix(%s(k+2), d))\n"+
		"\tmv := %s.Mix\n\tr = r*31 + %s(mv(v, d))\n"+
		"\tg := func(x %s) int { return %s(x) + 1 }\n\tr = r*31 + g(v)\n\tr = r*31 + %s(v)\n\treturn r\n}\n\n",
		a.name("agVia"), mod, a.name("agMk"), sum,
		sum, fn, a.name("agMk"),
		fns, fns, mod, a.typ, a.typ,
		fns, sum, fns,
		iface, imp, sum,
		a.name("agMk"),
		imp, sum,
		a.typ, sum, sum)
}

// idx is agIdx(k, d): the aggregate's array field read, sliced and copied through
// indexes, bounds and a make length of 64 bits.
func (a *aggregate) idx(k, d int64) int32 {
	fl := a.fields[1]
	n := int64(fl.n)
	v := a.mk(k)
	u := uint64(int32(k*k + d*d + 3))
	i := int64(u % uint64(n))
	var r int32
	r = toInt(v[1][i]) + toInt(v[1][i])*3
	r = r*31 + int32(n-i)
	r = r*31 + int32(i+1) + toInt(v[1][i])
	r = r*31 + int32(i)
	if i > 0 {
		r += toInt(v[1][i-1])
	}
	v[1][i] = fl.k.wrap(d)
	return r*31 + a.sum(v)
}

// writeIdx declares agIdx.
func (a *aggregate) writeIdx(f *Fuzzer) {
	w := f.Out
	fl := a.fields[1]
	fmt.Fprintf(w, "func %s(k, d int) int {\n\tv := %s(k)\n\tu := uint64(k*k + d*d + 3)\n\tw := int64(u %% %d)\n"+
		"\tr := int(v.%s[u%%%d]) + int(v.%s[w])*3\n\ts := v.%s[w:]\n\tr = r*31 + len(s)\n"+
		"\tt := v.%s[:u%%%d+1]\n\tr = r*31 + len(t) + int(t[len(t)-1])\n"+
		"\tm := make([]int64, w, %d)\n\tfor i := range m {\n\t\tm[i] = int64(v.%s[i])\n\t}\n\tr = r*31 + len(m)\n"+
		"\tif len(m) > 0 {\n\t\tr += int(m[len(m)-1])\n\t}\n\tv.%s[u%%%d] = %s(d)\n\treturn r*31 + %s(v)\n}\n\n",
		a.name("agIdx"), a.name("agMk"), fl.n,
		fl.name, fl.n, fl.name, fl.name,
		fl.name, fl.n,
		fl.n, fl.name,
		fl.name, fl.n, fl.k.name, a.name("agSum"))
}
