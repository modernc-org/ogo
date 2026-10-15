// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"bytes"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"unicode/utf8"
)

// lbTokens are what genLib's strings are made of: letters, white space of one byte
// and of two -- U+0085 and U+00A0, which TrimSpace trims as Go's does -- runes of two
// and three bytes, and bytes that begin no rune or end one too soon.
var lbTokens = []string{"a", "b", "ab", "A", "0", " ", "\t", "\n", "\u0085", " ", "é", "€", "\xff", "\xc3"}

// lbRunes are the runes genLib asks for: ones the strings hold, the replacement
// rune -- which IndexRune finds at the first invalid byte -- and ones that are no
// rune at all, a surrogate, a negative and one past U+10FFFF, which nothing holds.
var lbRunes = []rune{'a', 'b', ' ', 'é', '€', 0x85, 0xa0, utf8.RuneError, 0xd800, -1, 0x110000}

const lbN = 6 // the strings and the byte slices genLib declares

// library is what genLib declared and drew.
type library struct {
	r    *rand.Rand
	strs [lbN]string
	bufs [lbN]string // the bytes of lbB, as a string
}

func (m *library) str() string {
	var b strings.Builder
	for range m.r.Intn(6) {
		b.WriteString(lbTokens[m.r.Intn(len(lbTokens))])
	}
	return b.String()
}

// lbHash is lbH's, in the target's 32-bit arithmetic.
func lbHash(s string) int32 {
	h := int32(len(s))
	for i := 0; i < len(s); i++ {
		h = h*31 + int32(s[i])
	}
	return h
}

func lbBool(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// genLib declares strings, byte slices and an error wrapping two others, and a
// procedure handing them to the strings, bytes and errors packages and a Builder:
// every function of strings and of bytes, a Builder written past its end, and Is,
// As and Unwrap along a chain of three. The packages are OctoGo source, compiled as
// a program is, and each function is to mean what Go's means -- for an empty
// argument, a rune that is none, invalid UTF-8 -- which a program calling each one
// once had asked of a few arguments; the generator asks Go's own strings and bytes
// what each call comes to. Whether a seed has it is drawn first, from its own
// source (lbRand), so the imports can lead the program; the rest of the seed's
// program is what it was.
func (f *Fuzzer) genLib() (call string, want Int32, ok bool) {
	if !f.lbOn {
		return "", 0, false
	}
	m := &library{r: f.lbRand}
	for i := range m.strs {
		m.strs[i] = m.str()
		m.bufs[i] = m.str()
	}
	m.write(f)
	return m.run(f)
}

// drawLib draws whether the program has genLib's part, and writes the imports it
// needs if it does: it is asked before anything else is written.
func (f *Fuzzer) drawLib() {
	f.lbOn = f.lbRand.Intn(2) == 0
	if f.lbOn {
		fmt.Fprint(f.Out, "import (\n\t\"bytes\"\n\t\"errors\"\n\t\"strings\"\n)\n\n")
	}
}

func (m *library) write(f *Fuzzer) {
	w := f.Out
	q := func(s string) string { return strconv.Quote(s) }
	var a []string
	for _, s := range m.strs {
		a = append(a, q(s))
	}
	fmt.Fprintf(w, "var lbS_1 = [%d]string{%s}\n\n", lbN, strings.Join(a, ", "))
	a = a[:0]
	for _, s := range m.bufs {
		a = append(a, "[]byte("+q(s)+")")
	}
	fmt.Fprintf(w, "var lbB_1 = [%d][]byte{%s}\n\n", lbN, strings.Join(a, ", "))
	fmt.Fprint(w, "func lbH_1(s string) int {\n\th := len(s)\n\tfor i := 0; i < len(s); i++ {\n\t\th = h*31 + int(s[i])\n\t}\n\treturn h\n}\n\n")
	fmt.Fprint(w, "func lbHB_1(b []byte) int {\n\th := len(b)\n\tfor i := 0; i < len(b); i++ {\n\t\th = h*31 + int(b[i])\n\t}\n\treturn h\n}\n\n")
	fmt.Fprint(w, "func lbI_1(b bool) int {\n\tif b {\n\t\treturn 1\n\t}\n\treturn 0\n}\n\n")
	fmt.Fprint(w, "func lbE_1(e error) int {\n\tif e != nil {\n\t\treturn 1\n\t}\n\treturn 0\n}\n\n")
	fmt.Fprint(w, "var lbE0_1 = errors.New(\"e0\")\n\nvar lbE1_1 = errors.New(\"e1\")\n\n")
	fmt.Fprint(w, "type LBE_1 struct {\n\tc int\n\te error\n}\n\n")
	fmt.Fprint(w, "func (x *LBE_1) Error() string { return \"lbe\" }\n\n")
	fmt.Fprint(w, "func (x *LBE_1) Unwrap() error { return x.e }\n\n")
	fmt.Fprint(w, "var lbW0_1 = LBE_1{1, lbE0_1}\n\nvar lbW1_1 = LBE_1{2, &lbW0_1}\n\nvar lbW2_1 = LBE_1{3, &lbW1_1}\n\n")
}

// run writes lbRun, a statement for each of the shapes drawn, in an order drawn,
// and computes what it returns.
func (m *library) run(f *Fuzzer) (string, Int32, bool) {
	r := m.r
	var b strings.Builder
	var res int32
	step := func(v int32) { res = res*31 + v }
	ix := func() int { return r.Intn(lbN) }
	ru := func() rune { return lbRunes[r.Intn(len(lbRunes))] }
	by := func() byte {
		if r.Intn(2) == 0 {
			return lbTokens[r.Intn(len(lbTokens))][0]
		}
		return byte(r.Intn(256))
	}
	// one writes a call answering an int-valued expression, and its value.
	one := func(expr string, v int32) {
		fmt.Fprintf(&b, "\tr = r*31 + %s\n", expr)
		step(v)
	}
	two := func(fn string, i, j int) (string, string) {
		return fmt.Sprintf("%s(lbS_1[%d], lbS_1[%d])", fn, i, j), fmt.Sprintf("%s(lbB_1[%d], lbB_1[%d])", "bytes."+strings.TrimPrefix(fn, "strings."), i, j)
	}
	ss, bs := m.strs, m.bufs
	shapes := []func(){
		func() {
			i, j := ix(), ix()
			s, x := two("strings.Index", i, j)
			one(s, int32(strings.Index(ss[i], ss[j])))
			one(x, int32(bytes.Index([]byte(bs[i]), []byte(bs[j]))))
		},
		func() {
			i, j := ix(), ix()
			s, x := two("strings.LastIndex", i, j)
			one(s, int32(strings.LastIndex(ss[i], ss[j])))
			one(x, int32(bytes.LastIndex([]byte(bs[i]), []byte(bs[j]))))
		},
		func() {
			i, j := ix(), ix()
			s, x := two("strings.Contains", i, j)
			one("lbI_1("+s+")", lbBool(strings.Contains(ss[i], ss[j])))
			one("lbI_1("+x+")", lbBool(bytes.Contains([]byte(bs[i]), []byte(bs[j]))))
		},
		func() {
			i, j := ix(), ix()
			one(fmt.Sprintf("lbI_1(strings.ContainsAny(lbS_1[%d], lbS_1[%d]))", i, j), lbBool(strings.ContainsAny(ss[i], ss[j])))
			one(fmt.Sprintf("lbI_1(bytes.ContainsAny(lbB_1[%d], lbS_1[%d]))", i, j), lbBool(bytes.ContainsAny([]byte(bs[i]), ss[j])))
		},
		func() {
			i, c := ix(), ru()
			one(fmt.Sprintf("lbI_1(strings.ContainsRune(lbS_1[%d], %d))", i, c), lbBool(strings.ContainsRune(ss[i], c)))
			one(fmt.Sprintf("lbI_1(bytes.ContainsRune(lbB_1[%d], %d))", i, c), lbBool(bytes.ContainsRune([]byte(bs[i]), c)))
		},
		func() {
			i, j := ix(), ix()
			s, x := two("strings.Count", i, j)
			one(s, int32(strings.Count(ss[i], ss[j])))
			one(x, int32(bytes.Count([]byte(bs[i]), []byte(bs[j]))))
		},
		func() {
			i, j := ix(), ix()
			fmt.Fprintf(&b, "\tbf, af, found := strings.Cut(lbS_1[%d], lbS_1[%d])\n\tr = r*31 + lbH_1(bf)*7 + lbH_1(af) + lbI_1(found)\n", i, j)
			bf, af, found := strings.Cut(ss[i], ss[j])
			step(lbHash(bf)*7 + lbHash(af) + lbBool(found))
			fmt.Fprintf(&b, "\tbb, ab, foundb := bytes.Cut(lbB_1[%d], lbB_1[%d])\n\tr = r*31 + lbHB_1(bb)*7 + lbHB_1(ab) + lbI_1(foundb)\n", i, j)
			bb, ab, foundb := bytes.Cut([]byte(bs[i]), []byte(bs[j]))
			step(lbHash(string(bb))*7 + lbHash(string(ab)) + lbBool(foundb))
		},
		func() {
			i, j := ix(), ix()
			fmt.Fprintf(&b, "\tcp, okp := strings.CutPrefix(lbS_1[%d], lbS_1[%d])\n\tcs, oks := strings.CutSuffix(lbS_1[%d], lbS_1[%d])\n\tr = r*31 + lbH_1(cp) + lbI_1(okp)*3 + lbH_1(cs)*5 + lbI_1(oks)*9\n", i, j, j, i)
			cp, okp := strings.CutPrefix(ss[i], ss[j])
			cs, oks := strings.CutSuffix(ss[j], ss[i])
			step(lbHash(cp) + lbBool(okp)*3 + lbHash(cs)*5 + lbBool(oks)*9)
			fmt.Fprintf(&b, "\tcpb, okpb := bytes.CutPrefix(lbB_1[%d], lbB_1[%d])\n\tcsb, oksb := bytes.CutSuffix(lbB_1[%d], lbB_1[%d])\n\tr = r*31 + lbHB_1(cpb) + lbI_1(okpb)*3 + lbHB_1(csb)*5 + lbI_1(oksb)*9\n", i, j, j, i)
			cpb, okpb := bytes.CutPrefix([]byte(bs[i]), []byte(bs[j]))
			csb, oksb := bytes.CutSuffix([]byte(bs[j]), []byte(bs[i]))
			step(lbHash(string(cpb)) + lbBool(okpb)*3 + lbHash(string(csb))*5 + lbBool(oksb)*9)
		},
		func() {
			i, j := ix(), ix()
			s, x := two("strings.HasPrefix", i, j)
			one("lbI_1("+s+")", lbBool(strings.HasPrefix(ss[i], ss[j])))
			one("lbI_1("+x+")", lbBool(bytes.HasPrefix([]byte(bs[i]), []byte(bs[j]))))
			s, x = two("strings.HasSuffix", j, i)
			one("lbI_1("+s+")", lbBool(strings.HasSuffix(ss[j], ss[i])))
			one("lbI_1("+x+")", lbBool(bytes.HasSuffix([]byte(bs[j]), []byte(bs[i]))))
		},
		func() {
			i, j := ix(), ix()
			one(fmt.Sprintf("strings.IndexAny(lbS_1[%d], lbS_1[%d])", i, j), int32(strings.IndexAny(ss[i], ss[j])))
			one(fmt.Sprintf("bytes.IndexAny(lbB_1[%d], lbS_1[%d])", i, j), int32(bytes.IndexAny([]byte(bs[i]), ss[j])))
		},
		func() {
			i, c := ix(), by()
			one(fmt.Sprintf("strings.IndexByte(lbS_1[%d], %d)", i, c), int32(strings.IndexByte(ss[i], c)))
			one(fmt.Sprintf("bytes.IndexByte(lbB_1[%d], %d)", i, c), int32(bytes.IndexByte([]byte(bs[i]), c)))
			one(fmt.Sprintf("strings.LastIndexByte(lbS_1[%d], %d)", i, c), int32(strings.LastIndexByte(ss[i], c)))
			one(fmt.Sprintf("bytes.LastIndexByte(lbB_1[%d], %d)", i, c), int32(bytes.LastIndexByte([]byte(bs[i]), c)))
		},
		func() {
			i, c := ix(), ru()
			one(fmt.Sprintf("strings.IndexRune(lbS_1[%d], %d)", i, c), int32(strings.IndexRune(ss[i], c)))
			one(fmt.Sprintf("bytes.IndexRune(lbB_1[%d], %d)", i, c), int32(bytes.IndexRune([]byte(bs[i]), c)))
		},
		func() {
			i, j := ix(), ix()
			one(fmt.Sprintf("lbH_1(strings.TrimPrefix(lbS_1[%d], lbS_1[%d]))", i, j), lbHash(strings.TrimPrefix(ss[i], ss[j])))
			one(fmt.Sprintf("lbH_1(strings.TrimSuffix(lbS_1[%d], lbS_1[%d]))", j, i), lbHash(strings.TrimSuffix(ss[j], ss[i])))
			one(fmt.Sprintf("lbHB_1(bytes.TrimPrefix(lbB_1[%d], lbB_1[%d]))", i, j), lbHash(string(bytes.TrimPrefix([]byte(bs[i]), []byte(bs[j])))))
			one(fmt.Sprintf("lbHB_1(bytes.TrimSuffix(lbB_1[%d], lbB_1[%d]))", j, i), lbHash(string(bytes.TrimSuffix([]byte(bs[j]), []byte(bs[i])))))
		},
		func() {
			i := ix()
			one(fmt.Sprintf("lbH_1(strings.TrimSpace(lbS_1[%d]))", i), lbHash(strings.TrimSpace(ss[i])))
			one(fmt.Sprintf("lbHB_1(bytes.TrimSpace(lbB_1[%d]))", i), lbHash(string(bytes.TrimSpace([]byte(bs[i])))))
		},
		func() {
			i, j := ix(), ix()
			s, x := two("strings.Compare", i, j)
			one(s, int32(strings.Compare(ss[i], ss[j])))
			one(x, int32(bytes.Compare([]byte(bs[i]), []byte(bs[j]))))
			one(fmt.Sprintf("lbI_1(bytes.Equal(lbB_1[%d], lbB_1[%d]))", i, j), lbBool(bytes.Equal([]byte(bs[i]), []byte(bs[j]))))
		},
		func() { // errors along a chain of three
			k := r.Intn(3)
			one(fmt.Sprintf("lbI_1(errors.Is(&lbW%d_1, lbE0_1))", k), 1)
			one(fmt.Sprintf("lbI_1(errors.Is(&lbW%d_1, lbE1_1))", k), 0)
			one(fmt.Sprintf("lbI_1(errors.Is(&lbW2_1, &lbW%d_1))", k), 1)
			one(fmt.Sprintf("lbI_1(errors.Is(&lbW%d_1, &lbW2_1))", k), lbBool(k == 2))
			fmt.Fprintf(&b, "\tvar e error = &lbW%d_1\n\tvar p *LBE_1\n\tif errors.As(e, &p) {\n\t\tr = r*31 + p.c\n\t}\n", k)
			step(int32(k + 1))
			one(fmt.Sprintf("lbI_1(errors.Unwrap(e) == lbW%d_1.e)", k), 1)
			one("len(lbE0_1.Error()) + len(e.Error())*3", 2+3*3)
		},
	}
	if !f.usedCNames["NewBuilder"] && !f.usedCNames["Builder"] {
		// A Builder over a backing of 0 to 12 bytes, written past its end: what each
		// write answers, and what is written.
		shapes = append(shapes, func() {
			size, i, j, c, ch := r.Intn(13), ix(), ix(), ru(), by()
			fmt.Fprintf(&b, "\tvar back [%d]byte\n\tsb := NewBuilder(back[:])\n", size)
			fmt.Fprintf(&b, "\tn0, e0 := sb.WriteString(lbS_1[%d])\n\te1 := sb.WriteByte(%d)\n\tn2, e2 := sb.WriteRune(%d)\n\tn3, e3 := sb.Write(lbB_1[%d])\n", i, ch, c, j)
			fmt.Fprint(&b, "\tr = r*31 + n0 + lbE_1(e0)*2 + lbE_1(e1)*4 + n2*8 + lbE_1(e2)*16 + n3*32 + lbE_1(e3)*64\n\tr = r*31 + sb.Len()*3 + lbH_1(sb.String())\n")
			var w []byte
			put := func(p string) (int32, bool) {
				n := min(len(p), size-len(w))
				w = append(w, p[:n]...)
				return int32(n), n < len(p)
			}
			n0, e0 := put(ss[i])
			e1 := len(w) >= size
			if !e1 {
				w = append(w, ch)
			}
			var n2 int32
			e2 := true
			if rb := string(c); len(w)+len(rb) <= size { // string of an invalid rune is U+FFFD, as WriteRune writes it
				w = append(w, rb...)
				n2, e2 = int32(len(rb)), false
			}
			n3, e3 := put(bs[j])
			step(n0 + lbBool(e0)*2 + lbBool(e1)*4 + n2*8 + lbBool(e2)*16 + n3*32 + lbBool(e3)*64)
			step(int32(len(w))*3 + lbHash(string(w)))
		})
	}
	var drawn []func()
	for _, s := range shapes {
		if r.Intn(3) != 0 {
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
	fmt.Fprintf(f.Out, "func lbRun_1() int {\n\tr := 0\n%s\treturn r\n}\n\n", b.String())
	return "lbRun_1()", Int32(res), true
}
