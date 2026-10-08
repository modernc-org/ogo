// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"io"
	"strings"
	"testing"
	"testing/fstest"
)

// TestCheckForeignMadeValues: what another package's types make, asked what this
// package's are. A value of another package's defined type that a conversion, a call,
// an operation or a literal made went into another package's interface unasked --
// `a.Count(a.N(5))`, `a.Count(a.MkN())`, `a.L = a.N(2)`, `a.Count(a.Strs{"x"})` --
// and reached C, the made-value rule asking a type of this package over a Kind only;
// and a method called on what another package's method returns, `f.Eval(2).Put(b)`,
// was asked its arguments as an operand only, not as a statement, deferred or a
// typed declaration's value.
func TestCheckForeignMadeValues(t *testing.T) {
	const lib = `type Lenner interface{ Len() int }

type Strs []string

func (s Strs) Len() int { return len(s) }

type N int

func (n N) Len() int { return int(n) }

func (n N) Put(b []byte) int { return len(b) + int(n) }

type F interface{ Eval(x int) N }

type sq struct{}

func (*sq) Eval(x int) N { return N(x * x) }

var g sq

var V F = &g

func Count(l Lenner) int { return l.Len() }

func MkN() N { return 3 }

var L Lenner
`
	for _, test := range []struct {
		name, body string
		want       string // "" for a program Go takes and this compiler builds
	}{
		{"pointer", `ys := a.Strs(xs[:3])
	println(a.Count(&ys))`, ""},
		{"interface result", `println(f.Eval(2).Put(bb[:1]))
	f.Eval(2).Put(bb[:0])
	var k int = f.Eval(2).Put(nil)
	_ = k`, ""},
		{"conversion of a slice", `println(a.Count(a.Strs(xs[:2])))`, "cannot use a.Strs(xs[:2]) (value of type a.Strs) as a.Lenner value in argument to Count: an interface holds a pointer here"},
		{"conversion of nil", `println(a.Count(a.Strs(nil)))`, "cannot use a.Strs(nil) (value of type a.Strs)"},
		{"conversion of a constant", `println(a.Count(a.N(5)))`, "cannot use a.N(5) (value of type a.N)"},
		{"call", `println(a.Count(a.MkN()))`, "cannot use a.MkN() (value of type a.N)"},
		{"operation", `println(a.Count(n + 1))`, "cannot use n + 1 (value of type a.N)"},
		{"literal", `println(a.Count(a.Strs{"x"}))`, "cannot use a.Strs{\"x\"} (value of struct type a.Strs)"},
		{"assignment", `a.L = a.N(2)`, "cannot use a.N(2) (value of type a.N) as a.Lenner value in assignment"},
		{"declaration", `var l a.Lenner = a.Strs(xs[:1])
	_ = l`, "cannot use a.Strs(xs[:1]) (value of type a.Strs) as a.Lenner value in variable declaration"},
		{"statement", `f.Eval(2).Put(nb[:0])`, "cannot use nb[:0] (variable of type []bool) as []byte value in argument to Put"},
		{"deferred", `defer f.Eval(2).Put(nb[:0])`, "cannot use nb[:0] (variable of type []bool) as []byte value in argument to Put"},
		{"typed declaration", `var s string = f.Eval(2).Put(nil)
	_ = s`, "cannot use f.Eval(2).Put(nil) of type int as type string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "import \"a\"\n\nfunc main() {\n\tvar xs [4]string\n\tvar bb [4]byte\n\tvar nb [4]bool\n\tvar n a.N = 2\n\tvar f a.F = a.V\n\t_, _, _, _, _ = xs, bb, nb, n, f\n\t" + test.body + "\n}\n"
			fsys := fstest.MapFS{
				"main.ogo": &fstest.MapFile{Data: []byte(src)},
				"a/a.ogo":  &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked(), Inline())
			}
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}
