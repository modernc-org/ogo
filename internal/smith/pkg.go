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

// pkgPart is what genPkg declared in the package pkx and in main, and the state its
// procedure runs over: pkx's variables and main's pkO, which the procedure writes as
// it goes.
type pkgPart struct {
	r          *rand.Rand
	c1, c2, c3 int32 // Sum's coefficients, and MkWide's offset
	k0         int32 // KA's value
	scale      int32
	seed       int32
	va         int32 // pkx.V
	vb         uint32
	arr        [4]int32
	tabA       [3]int32
	tabB       [3]uint32
	sl         []int32
	count      int32 // pkx.Count, after pkx's initialization: 10 from Seed's, 100 from init
	oa         int32 // main's pkO: its embedded pkx.Base and its own field
	ob         uint32
	own        int32
}

func (m *pkgPart) sum(a int32, b uint32) int32 { return a*m.c1 + int32(b)*m.c2 }

func pkBump(a *int32, b *uint32, d int32) int32 {
	*a += d
	*b += uint32(d) * 3
	return *a
}

func (m *pkgPart) wideTotal(k int32) int64 {
	t := int64(k) << 33
	for i := range int32(5) {
		t += int64(k*(i+1) + m.c3)
	}
	return t
}

func pkAdd(base int32, xs ...int32) int32 {
	for i, x := range xs {
		base += x * int32(i+1)
	}
	return base
}

// genPkg writes, where a program has packages of its own (ogo smith -o, Files), a
// package pkx the program imports, and a procedure of main's reaching into it by
// qualified names: a struct of pkx's embedded in main's type, its unexported field
// behind exported methods, promoted across the boundary; pkx's variables -- a
// struct, arrays, a slice, an int its initialization wrote through a function and
// init -- read and written; method values and method expressions of pkx's type; a
// struct of more than four words a pkx function returns; a defined integer type's
// constants and untyped and typed ones; a function of three results, a variadic one
// and a table of unexported ones; and main's type implementing pkx's interface,
// handed to a pkx function. Every program generated before was one package, and the
// package boundary is where the emitter's names and the checker's resolution part
// ways. What the procedure comes to is computed here, in Go, and it draws from its
// own source (pkRand), so the rest of the seed's program is what it was.
func (f *Fuzzer) genPkg() (call string, want Int32, ok bool) {
	if !f.pkOn {
		return "", 0, false
	}
	r := f.pkRand
	draw := func(lo, hi int) int32 { return int32(lo + r.Intn(hi-lo+1)) }
	m := &pkgPart{r: r}
	m.c1, m.c2, m.c3 = draw(1, 19), draw(1, 19), draw(-500, 500)
	m.k0, m.scale, m.seed = draw(0, 250), draw(-1000, 1000), draw(-1000, 1000)
	m.va, m.vb = int32(r.Uint32()), r.Uint32()
	for i := range m.arr {
		m.arr[i] = draw(-1000, 1000)
	}
	for i := range m.tabA {
		m.tabA[i], m.tabB[i] = int32(r.Uint32()), r.Uint32()
	}
	m.sl = make([]int32, 1+r.Intn(4))
	for i := range m.sl {
		m.sl[i] = draw(-1000, 1000)
	}
	m.count = 110
	m.oa, m.ob, m.own = draw(-5000, 5000), r.Uint32(), draw(-5000, 5000)
	m.writePkg(&f.pkOut)
	m.writeMain(f)
	return m.run(f, draw(-200, 200))
}

func (m *pkgPart) writePkg(w *strings.Builder) {
	w.WriteString("// Package pkx is the package the generated program imports.\n\n")
	w.WriteString("type Base struct {\n\tA int32\n\tb uint32\n}\n\n")
	w.WriteString("func (p *Base) Bump(d int32) int32 {\n\tp.A += d\n\tp.b += uint32(d) * 3\n\treturn p.A\n}\n\n")
	fmt.Fprintf(w, "func (v Base) Sum() int32 { return v.A*%d + int32(v.b)*%d }\n\n", m.c1, m.c2)
	w.WriteString("func (v Base) Hidden() uint32 { return v.b }\n\n")
	w.WriteString("func NewBase(a int32, b uint32) Base { return Base{A: a, b: b} }\n\n")
	w.WriteString("type Wide struct {\n\tW [5]int32\n\tN int64\n}\n\n")
	fmt.Fprintf(w, "func MkWide(k int32) Wide {\n\tvar w Wide\n\tfor i := range w.W {\n\t\tw.W[i] = k*int32(i+1) + %d\n\t}\n\tw.N = int64(k) << 33\n\treturn w\n}\n\n", m.c3)
	w.WriteString("func (w Wide) Total() int64 {\n\tt := w.N\n\tfor _, x := range w.W {\n\t\tt += int64(x)\n\t}\n\treturn t\n}\n\n")
	fmt.Fprintf(w, "type Kind uint8\n\nconst (\n\tKA Kind = iota + %d\n\tKB\n\tKC\n)\n\n", m.k0)
	fmt.Fprintf(w, "const Big = 1 << 40\n\nconst Scale int32 = %d\n\n", m.scale)
	fmt.Fprintf(w, "var V = Base{A: %d, b: %d}\n\n", m.va, m.vb)
	fmt.Fprintf(w, "var Arr = [4]int32{%d, %d, %d, %d}\n\n", m.arr[0], m.arr[1], m.arr[2], m.arr[3])
	var a []string
	for i := range m.tabA {
		a = append(a, fmt.Sprintf("{A: %d, b: %d}", m.tabA[i], m.tabB[i]))
	}
	fmt.Fprintf(w, "var Tab = [3]Base{%s}\n\n", strings.Join(a, ", "))
	a = a[:0]
	for _, v := range m.sl {
		a = append(a, strconv.Itoa(int(v)))
	}
	fmt.Fprintf(w, "var Sl = []int32{%s}\n\n", strings.Join(a, ", "))
	w.WriteString("var Count int\n\nvar Seed = mkSeed()\n\n")
	fmt.Fprintf(w, "func mkSeed() int32 {\n\tCount += 10\n\treturn %d\n}\n\n", m.seed)
	w.WriteString("func init() {\n\tCount += 100\n}\n\n")
	w.WriteString("type Shaper interface {\n\tArea() int32\n}\n\n")
	w.WriteString("func Measure(s Shaper, n int32) int32 { return s.Area()*n + int32(Count) }\n\n")
	w.WriteString("func Div(a, b int32) (int32, int32, bool) {\n\tif b == 0 {\n\t\treturn 0, 0, false\n\t}\n\treturn a / b, a % b, true\n}\n\n")
	w.WriteString("func Add(base int32, xs ...int32) int32 {\n\tfor i, x := range xs {\n\t\tbase += x * int32(i+1)\n\t}\n\treturn base\n}\n\n")
	w.WriteString("func add(a, b int32) int32 { return a + b }\n\nfunc sub(a, b int32) int32 { return a - b }\n\nfunc mul(a, b int32) int32 { return a * b }\n\n")
	w.WriteString("var Ops = [3]func(int32, int32) int32{add, sub, mul}\n")
}

func (m *pkgPart) writeMain(f *Fuzzer) {
	w := f.Out
	fmt.Fprint(w, "type PKO_1 struct {\n\tpkx.Base\n\town int32\n}\n\n")
	fmt.Fprint(w, "func (o *PKO_1) Area() int32 { return o.A*2 + o.own }\n\n")
	fmt.Fprintf(w, "var pkO_1 = PKO_1{Base: pkx.NewBase(%d, %d), own: %d}\n\n", m.oa, m.ob, m.own)
	fmt.Fprint(w, "func pkI_1(b bool) int {\n\tif b {\n\t\treturn 1\n\t}\n\treturn 0\n}\n\n")
}

// run writes pkRun, a statement or two for each of the shapes drawn, in an order
// drawn, and computes what it returns given k.
func (m *pkgPart) run(f *Fuzzer, k int32) (string, Int32, bool) {
	r := m.r
	var b strings.Builder
	var res int32
	step := func(v int32) { res = res*31 + v }
	d := func() int32 { return int32(r.Intn(201) - 100) }
	shapes := []func(){
		func() { // a field of another package's variable, read and written
			v := d()
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.V.A)\n\tpkx.V.A += %d\n\tr = r*31 + int(pkx.V.A)\n", v)
			step(m.va)
			m.va += v
			step(m.va)
		},
		func() { // its methods, one reading an unexported field
			v := d()
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.V.Bump(%d))\n\tr = r*31 + int(pkx.V.Sum())\n\tr = r*31 + int(pkx.V.Hidden())\n", v)
			step(pkBump(&m.va, &m.vb, v))
			step(m.sum(m.va, m.vb))
			step(int32(m.vb))
		},
		func() { // a method value of it
			v := d()
			fmt.Fprintf(&b, "\tmv := pkx.V.Bump\n\tr = r*31 + int(mv(%d))\n", v)
			step(pkBump(&m.va, &m.vb, v))
		},
		func() { // method expressions of its type
			v := d()
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.Base.Sum(pkx.V))\n\tr = r*31 + int((*pkx.Base).Bump(&pkx.V, %d))\n", v)
			step(m.sum(m.va, m.vb))
			step(pkBump(&m.va, &m.vb, v))
		},
		func() { // an array of another package, written and read
			i, j, v := r.Intn(4), r.Intn(4), d()
			fmt.Fprintf(&b, "\tpkx.Arr[%d] += %d\n\tr = r*31 + int(pkx.Arr[%d])\n", i, v, j)
			m.arr[i] += v
			step(m.arr[j])
		},
		func() { // a slice of another package
			i := r.Intn(len(m.sl))
			fmt.Fprintf(&b, "\tr = r*31 + len(pkx.Sl) + int(pkx.Sl[%d])*3\n", i)
			step(int32(len(m.sl)) + m.sl[i]*3)
		},
		func() { // a value receiver on an element of another package's array
			i := r.Intn(3)
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.Tab[%d].Sum())\n", i)
			step(m.sum(m.tabA[i], m.tabB[i]))
		},
		func() { // a struct of more than four words another package returns
			i := r.Intn(5)
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.MkWide(int32(k)).Total()>>3)\n\tw := pkx.MkWide(int32(k) + 1)\n\tr = r*31 + int(w.W[%d])\n", i)
			step(int32(m.wideTotal(k) >> 3))
			step((k+1)*int32(i+1) + m.c3)
		},
		func() { // another package's constants: a defined type's, untyped, typed
			dd := int32(r.Intn(256))
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.KB) + int(pkx.Scale) + pkx.Big>>35\n\tkk := pkx.KC\n\tkk += %d\n\tr = r*31 + int(kk)\n", dd)
			step(m.k0 + 1 + m.scale + 32)
			step(int32(uint8(m.k0 + 2 + dd)))
		},
		func() { // a function of three results
			a, bb := d(), []int32{0, -3, 7, 1, -1}[r.Intn(5)]
			fmt.Fprintf(&b, "\tq, rem, ok := pkx.Div(int32(k)+%d, %d)\n\tr = r*31 + int(q)*7 + int(rem) + pkI_1(ok)\n", a, bb)
			if bb == 0 {
				step(0)
			} else {
				x := k + a
				step((x/bb)*7 + x%bb + 1)
			}
		},
		func() { // a variadic function, with none, several and a slice spread
			x, y, i := d(), d(), r.Intn(4)
			j := i + r.Intn(5-i)
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.Add(int32(k)))\n\tr = r*31 + int(pkx.Add(1, %d, %d))\n\tr = r*31 + int(pkx.Add(%d, pkx.Arr[%d:%d]...))\n", x, y, x, i, j)
			step(k)
			step(pkAdd(1, x, y))
			step(pkAdd(x, m.arr[i:j]...))
		},
		func() { // a table of another package's unexported functions
			i, x, y := r.Intn(3), d(), d()
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.Ops[%d](%d, %d))\n", i, x, y)
			step([]int32{x + y, x - y, x * y}[i])
		},
		func() { // its initialization, and a variable of it written
			v := d()
			fmt.Fprintf(&b, "\tr = r*31 + pkx.Count + int(pkx.Seed)\n\tpkx.Count += %d\n\tr = r*31 + pkx.Count\n", v)
			step(m.count + m.seed)
			m.count += v
			step(m.count)
		},
		func() { // its type embedded in main's, promoted across the boundary
			v := d()
			fmt.Fprintf(&b, "\tr = r*31 + int(pkO_1.A)\n\tr = r*31 + int(pkO_1.Bump(%d))\n\tr = r*31 + int(pkO_1.Sum()) + int(pkO_1.Hidden())\n", v)
			step(m.oa)
			step(pkBump(&m.oa, &m.ob, v))
			step(m.sum(m.oa, m.ob) + int32(m.ob))
		},
		func() { // main's type implementing its interface, handed to it
			n := int32(r.Intn(9) - 4)
			fmt.Fprintf(&b, "\tr = r*31 + int(pkx.Measure(&pkO_1, %d))\n", n)
			step((m.oa*2+m.own)*n + m.count)
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
	fmt.Fprintf(f.Out, "func pkRun_1(k int) int {\n\tr := 0\n%s\treturn r\n}\n\n", b.String())
	return fmt.Sprintf("pkRun_1(%d)", k), Int32(res), true
}
