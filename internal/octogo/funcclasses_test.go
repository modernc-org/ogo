// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"slices"
	"strings"
	"testing"
)

// TestFuncValueClasses holds what FuncValueClasses has the C say: each function's
// calls through a function value by the C type of the value, as ScanCalls reads
// them, and what a value of each such type may hold -- of a defined type, the functions made values
// of it and those used where no type was recorded; of a type written out, or of a
// defined type of a signature something written out has or a conversion opens,
// every function of the signature used as a value.
func TestFuncValueClasses(t *testing.T) {
	const tables = `type handler func(n int) int

type bif func(n int) int

type entry struct {
	name string
	f    bif
}

func b1(n int) int { return n * 2 }

func b2(n int) int { return n * 3 }

var bifs = [2]entry{{"b1", b1}, {"b2", b2}}

func hAdd(n int) int { return n + 1 }

func hCall(n int) int { return bifs[n&1].f(n) }

var handlers = [2]handler{hAdd, hCall}

func run(op, n int) int { return handlers[op](n) }
`
	for _, test := range []struct {
		name, src string
		calls     map[string][]string
		members   map[string][]string
	}{
		{"two tables", tables + `
func main() { println(run(1, 2)) }
`, map[string][]string{"run": {"handler"}, "hCall": {"bif"}},
			map[string][]string{"handler": {"hAdd", "hCall"}, "bif": {"b1", "b2"}}},
		{"an argument and a return", tables + `
func pick(h handler) handler { return h }

func give() handler { return hAdd }

func main() { println(run(1, 2), pick(hCall)(3), give()(4)) }
`, map[string][]string{"run": {"handler"}, "hCall": {"bif"}, "main": {"handler", "handler"}},
			map[string][]string{"handler": {"hAdd", "hCall"}, "bif": {"b1", "b2"}}},
		{"a function type written out", tables + `
var u func(int) int = b1

func main() { println(run(1, 2), u(3)) }
`, map[string][]string{"run": {"handler"}, "hCall": {"bif"}, "main": {"ogo_functype0"}},
			map[string][]string{"handler": {"b1", "b2", "hAdd", "hCall"}, "bif": {"b1", "b2", "hAdd", "hCall"}, "ogo_functype0": {"b1", "b2", "hAdd", "hCall"}}},
		{"a conversion", tables + `
func main() {
	var h handler = handler(bifs[0].f)
	println(run(1, 2), h(3))
}
`, map[string][]string{"run": {"handler"}, "hCall": {"bif"}, "main": {"handler"}},
			map[string][]string{"handler": {"b1", "b2", "hAdd", "hCall"}, "bif": {"b1", "b2", "hAdd", "hCall"}}},
		{"a function converted", tables + `
func hNeg(n int) int { return -n }

func main() {
	var h handler = handler(hNeg)
	println(run(1, 2), h(3))
}
`, map[string][]string{"run": {"handler"}, "hCall": {"bif"}, "main": {"handler"}},
			map[string][]string{"handler": {"hAdd", "hCall", "hNeg"}, "bif": {"b1", "b2", "hNeg"}}},
		{"a literal and a method value", `type handler func(n int) int

type T struct{ k int }

func (t *T) M(n int) int { return t.k + n }

var gt T

var hs = [2]handler{func(n int) int { return n }, gt.M}

func main() { println(hs[1](2)) }
`, map[string][]string{"main": {"handler"}},
			map[string][]string{"handler": {"T_M", "ogo_lit0"}}},
		{"a key named like a function", `type box struct{ run func() }

func hi() { println("hi") }

func run() {
	bx := box{run: hi}
	bx.run()
}

func main() { run() }
`, map[string][]string{"run": {"ogo_functype0"}},
			map[string][]string{"ogo_functype0": {"hi"}}},
		{"a local holding one function", `func twice(m int) int { return 2 * m }

func other(m int) int { return m }

var g func(int) int = other

func main() {
	f := twice
	println(f(3), g(4))
}
`, map[string][]string{"main": {"ogo_fvbound0", "ogo_functype0"}},
			map[string][]string{"ogo_fvbound0": {"twice"}, "ogo_functype0": {"other", "twice"}}},
		{"a type naming itself", `type State func(n int) State

func a(n int) State {
	if n == 0 {
		return nil
	}
	return b
}

func b(n int) State { return a }

func main() {
	s := a
	for n := 3; s != nil; n-- {
		s = s(n)
	}
}
`, map[string][]string{"main": {"ogo_functype0"}},
			map[string][]string{"ogo_functype0": {"a", "b"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, c := markedFuncs(t, test.src, FuncValueClasses())
			members, ok := ReadFuncValueClasses([]byte(c))
			if !ok {
				t.Fatalf("no table in\n%s", c)
			}
			scan := ScanCalls([]byte(c))
			for fn, want := range test.calls {
				var got []string
				if fc := scan.Funcs[fn]; fc != nil {
					got = slices.Clone(fc.FV)
					if fc.Other != 0 {
						t.Errorf("%s makes %d calls not known to be by name", fn, fc.Other)
					}
				}
				slices.Sort(got)
				slices.Sort(want)
				if !slices.Equal(got, want) {
					t.Errorf("calls of %s: got %v, want %v", fn, got, want)
				}
			}
			for ct, want := range test.members {
				got := members[ct]
				slices.Sort(want)
				if !slices.Equal(got, want) {
					t.Errorf("members of %s: got %v, want %v\n%s", ct, got, want, c[strings.Index(c, "/* ogo:funcvalues"):])
				}
			}
		})
	}
	// The option adds the macro, its uses and the table, and nothing else.
	for _, src := range []string{tables + "\nfunc main() { println(run(1, 2)) }\n", tables + "\nvar u func(int) int = b1\n\nfunc main() { println(run(1, 2), u(3)) }\n"} {
		_, plain := markedFuncs(t, src)
		_, with := markedFuncs(t, src, FuncValueClasses())
		if !strings.Contains(with, "OGO_FV(") {
			t.Errorf("no call is spelled with its type in\n%s", with)
		}
		if got := string(WithoutFuncValueClasses([]byte(with))); got != plain {
			t.Errorf("the option changed the C:\n%s\nwithout it:\n%s", got, plain)
		}
	}
}
