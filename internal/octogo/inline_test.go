// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// markedRE reads the name of a function the C marks for the backend to inline.
var markedRE = regexp.MustCompile(`(?m)^[^\n(]*?(\w+)\([^\n]*\) ` + inlineMark + ` \{$`)

// markedFuncs is the checked C of a program, emitted with the options given, and
// the functions marked in it, sorted.
func markedFuncs(t *testing.T, src string, opts ...EmitOption) (names []string, c string) {
	t.Helper()
	fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
	pkg, err := Build(-1, []string{"main.ogo"}, fsys)
	if err != nil {
		t.Fatalf("Build: %v\n%s", err, src)
	}
	var buf bytes.Buffer
	if err := EmitC(pkg, &buf, append([]EmitOption{Checked()}, opts...)...); err != nil {
		t.Fatalf("EmitC: %v\n%s", err, src)
	}
	c = buf.String()
	for _, m := range markedRE.FindAllStringSubmatch(c, -1) {
		names = append(names, m[1])
	}
	slices.Sort(names)
	return names, c
}

// TestEmitCInline holds which functions are marked for the backend to inline (see
// inlineCandidate): the small ones, of scalar parameters, that something calls,
// and whose copies come to inlineCopies statements at most. A runtime check is
// what made a one-line method too large for the backend to inline by itself, and
// a call costs what forty or fifty instructions do, so which functions are marked
// is what a checked program runs at; and every copy costs size and registers, so
// which are NOT is whether a large one builds.
func TestEmitCInline(t *testing.T) {
	asked := []EmitOption{Inline()}
	calls := func(n int, call string) string {
		return strings.Repeat("\t"+call+"\n", n)
	}
	stmts := func(n int) string {
		return strings.Repeat("\tg++\n", n-1) + "\treturn g + k\n"
	}
	for _, test := range []struct {
		name string
		src  string
		opts []EmitOption
		want []string
	}{
		{"accessors", `type M struct {
	traps uint16
	r     [8]uint16
}

var m M

var ram [64]uint16

func (m *M) aborted() bool { return m.traps&3 != 0 }

func (m *M) set(r int, v uint16) { m.r[r] = v }

func readWord(a uint32) uint16 { return ram[a>>1] }

func main() {
	m.set(1, readWord(2))
	println(m.aborted())
}
`, asked, []string{"M_aborted", "M_set", "readWord"}},
		{"not asked for", `var ram [64]uint16

func readWord(a uint32) uint16 { return ram[a>>1] }

func main() { println(readWord(2)) }
`, nil, nil},
		{"six statements and seven", "var g int\n\nfunc six(k int) int {\n" + stmts(6) + "}\n\nfunc seven(k int) int {\n" + stmts(7) +
			"}\n\nfunc main() { println(six(1), seven(2)) }\n", asked, []string{"six"}},
		{"statements at any depth", `var g int

func deep(k int) int {
	if k > 0 {
		if k > 1 {
			for g < k {
				g++
				g += k
			}
		}
	}
	return g
}

func deeper(k int) int {
	if k > 0 {
		if k > 1 {
			for g < k {
				g++
				g += k
				g ^= 1
			}
		}
	}
	return g
}

func main() { println(deep(1), deeper(2)) }
`, asked, []string{"deep"}},
		{"parameters", `type P struct{ x, y int }

type A [4]int

var g int

func num(a int, b uint8, c float32, d bool) int { return a + int(b) + int(c) }

func ptr(p *P, q *int) int { return p.x + *q }

func str(s string) int { return len(s) }

func slc(s []int) int { return len(s) }

func stc(p P) int { return p.x }

func arr(a A) int { return a[0] }

func fun(f func() int) int { return f() }

func ifc(e error) bool { return e != nil }

func chn(c chan int) int { return <-c }

func (p P) value() int { return p.x }

func (p *P) pointer() int { return p.x }

func (a A) array() int { return a[0] }

func one() int { return 1 }

func main() {
	var p P
	var a A
	var c chan int
	println(num(1, 2, 3, true), ptr(&p, &g), str("s"), slc(a[:]), stc(p), arr(a), fun(one), ifc(nil), p.value(), p.pointer(), a.array())
	println(chn(c))
}
`, asked, []string{"P_pointer", "num", "ptr"}},
		{"results", `type P struct{ x, y int }

type A [4]int

type B struct {
	n int
	a A
}

var g int

var gp P

func none(k int) { g = k }

func num(k int) uint16 { return uint16(k) }

func flt(k int) float32 { return float32(k) }

func ptr(k int) *P { return &gp }

func two(k int) (int, bool) { return k, k > 0 }

func str(k int) string { return "s" }

func stc(k int) P { return P{k, k} }

func arr(k int) A { return A{k} }

func big(k int) B { return B{n: k} }

func slc(k int) []int { return nil }

func ifc(k int) error { return nil }

func named(k int) (r int) {
	r = k
	return
}

func main() {
	none(1)
	n, ok := two(2)
	println(num(1), flt(2), ptr(3).x, n, ok, str(4), stc(5).x, arr(6)[0], big(7).n, len(slc(8)), ifc(9) == nil, named(10))
}
`, asked, []string{"flt", "named", "none", "num", "ptr"}},
		{"more than it looks", `var g int

var c chan int

func deferred(k int) int {
	defer note(k)
	return k
}

func started(k int) { go note(k) }

func selected(k int) int {
	select {
	case v := <-c:
		return v
	default:
	}
	return k
}

func labeled(k int) int {
loop:
	for {
		break loop
	}
	return k
}

func jumped(k int) int {
	if k > 0 {
		goto out
	}
	g++
out:
	return k
}

func literal(k int) int {
	f := func(n int) int { return n + 1 }
	return f(k)
}

func note(k int) { g += k }

func main() {
	started(1)
	println(deferred(1), selected(2), labeled(3), jumped(4), literal(5))
}
`, asked, []string{"note"}},
		{"six statements in sixteen places", "var g int\n\nfunc six(k int) int {\n" + stmts(6) + "}\n\nfunc main() {\n" +
			calls(16, "g += six(1)") + "\tprintln(g)\n}\n", asked, []string{"six"}},
		{"six statements in seventeen places", "var g int\n\nfunc six(k int) int {\n" + stmts(6) + "}\n\nfunc main() {\n" +
			calls(17, "g += six(1)") + "\tprintln(g)\n}\n", asked, nil},
		{"one statement in ninety-six places", "var g int\n\nfunc one(k int) int {\n" + stmts(1) + "}\n\nfunc main() {\n" +
			calls(96, "g += one(1)") + "\tprintln(g)\n}\n", asked, []string{"one"}},
		{"one statement in ninety-seven places", "var g int\n\nfunc one(k int) int {\n" + stmts(1) + "}\n\nfunc main() {\n" +
			calls(97, "g += one(1)") + "\tprintln(g)\n}\n", asked, nil},
		{"methods of one name are counted together", "type A struct{ n int }\n\ntype B struct{ n int }\n\nvar a A\n\nvar b B\n\n" +
			"func (a *A) get() int { return a.n }\n\nfunc (b *B) get() int { return b.n }\n\nfunc main() {\n\tg := 0\n" +
			calls(48, "g += a.get()") + calls(47, "g += b.get()") + "\tprintln(g)\n}\n", asked, []string{"A_get", "B_get"}},
		{"methods of one name, one place too many", "type A struct{ n int }\n\ntype B struct{ n int }\n\nvar a A\n\nvar b B\n\n" +
			"func (a *A) get() int { return a.n }\n\nfunc (b *B) get() int { return b.n }\n\nfunc main() {\n\tg := 0\n" +
			calls(48, "g += a.get()") + calls(48, "g += b.get()") + "\tprintln(g)\n}\n", asked, nil},
		{"nothing calls it by name", `var g int

func bump(k int) { g += k }

func called(k int) { g -= k }

func main() {
	f := bump
	f(1)
	called(2)
	println(g)
}
`, asked, []string{"called"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, c := markedFuncs(t, test.src, test.opts...)
			if !slices.Equal(got, test.want) {
				t.Errorf("marked %v, want %v\n%s", got, test.want, test.src)
			}
			// The mark is defined where it is used and nowhere else: a program
			// with no function marked is the C it was before there were marks.
			if defined := strings.Contains(c, "#define "+inlineMark); defined != (len(test.want) != 0) {
				t.Errorf("the mark is defined: %v, with %d functions marked", defined, len(test.want))
			}
			if len(test.want) == 0 && strings.Contains(c, inlineMark) {
				t.Errorf("the C names the mark and marks nothing")
			}
		})
	}
}

// TestEmitCInlineMacro holds the mark to what the backend listens to. flexcc reads
// the keyword inline and ignores it; __attribute__((inline)) it takes after the
// declarator of a DEFINITION and nowhere else -- before the type it breaks the
// type, on a prototype it does not parse -- and the host's compiler is to see
// nothing, an attribute it does not know being a warning and so an error there.
func TestEmitCInlineMacro(t *testing.T) {
	_, c := markedFuncs(t, `var ram [64]uint16

func readWord(a uint32) uint16 { return ram[a>>1] }

func main() { println(readWord(2)) }
`, Inline())
	want := fmt.Sprintf("#ifdef __FLEXC__\n#define %[1]s __attribute__((inline))\n#else\n#define %[1]s\n#endif\n", inlineMark)
	if !strings.Contains(c, want) {
		t.Errorf("the C does not define the mark as\n%s", want)
	}
	for _, line := range strings.Split(c, "\n") {
		if !strings.Contains(line, inlineMark) || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasSuffix(line, ") "+inlineMark+" {") {
			t.Errorf("the mark stands where the backend does not take it: %q", line)
		}
	}
	if strings.Index(c, "#define "+inlineMark) > strings.Index(c, ") "+inlineMark+" {") {
		t.Errorf("the mark is used ahead of its definition")
	}
}
