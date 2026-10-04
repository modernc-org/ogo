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

// TestCheckUnexportedImplements: another package's exported variable of an
// UNEXPORTED type, `var ErrBusy = &errBusy{}` -- the sentinel idiom -- stored in or
// compared with an interface. Its type was looked up as though this file had written
// the name, which a file may not for an unexported one, so it was found to have no
// methods at all: "a.errBusy does not implement error (missing method Error)" in a
// declaration, an assignment, an argument, a return, a literal, a field, a send and
// a comparison (typeDeclNamedIn). And its IDENTITY: a value of such a type went
// into an int, into a type of this package's of the same shape, and as an argument
// of another struct type, unchecked. Go's refusals stand: a type that lacks the
// method, a value whose method has a pointer receiver, and the name written out, a
// type switch's case among them.
func TestCheckUnexportedImplements(t *testing.T) {
	const lib = `type sErr struct{ n int }

func (*sErr) Error() string { return "s" }

type vErr struct{ n int }

func (vErr) Error() string { return "v" }

type none struct{ n int }

type shape struct{ w int }

func (s *shape) Area() int { return s.w * s.w }

var PS = &sErr{1}

var VS sErr

var PV = &vErr{2}

var PN = &none{3}

var Sh = &shape{2}

var sPool sErr

func NewS() *sErr { return &sPool }

type point struct{ X, Y int }

var Origin = point{1, 2}

var Corner = point{3, 4}

func Dist(a, b point) int { return b.X - a.X + b.Y - a.Y }

type celsius int

var Room celsius = 21
`
	for _, test := range []struct {
		name, main string
		want       string // "" for a program Go takes
	}{
		{"decl", `func main() { var e error = a.PS; println(e != nil) }`, ""},
		{"addr", `func main() { var e error = &a.VS; println(e != nil) }`, ""},
		{"valueRecv", `func main() { var e error = a.PV; println(e != nil) }`, ""},
		{"assign", `func main() { var e error; e = a.NewS(); println(e != nil) }`, ""},
		{"compare", `func main() { var e error = a.NewS(); println(e == a.PS, a.PS == e) }`, ""},
		{"arg", `func show(e error) string { return e.Error() }

func main() { println(show(a.PS)) }`, ""},
		{"return", `func get() error { return a.PS }

func main() { println(get() == a.PS) }`, ""},
		{"lit", `func main() { errs := []error{a.PS, a.PV}; println(len(errs)) }`, ""},
		{"field", `type W struct{ e error }

func main() { w := W{e: a.PS}; println(w.e != nil) }`, ""},
		{"ownIface", `type Shaper interface{ Area() int }

func main() { var s Shaper = a.Sh; println(s.Area()) }`, ""},
		{"missing", `func main() { var e error = a.PN; println(e != nil) }`, "does not implement error (missing method Error)"},
		{"missingCompare", `func main() { var e error; println(e == a.PN) }`, "does not implement error (missing method Error)"},
		{"missingOwn", `type Sizer interface{ Size() int }

func main() { var z Sizer = a.Sh; println(z != nil) }`, "missing method Size"},
		{"value", `func main() { var e error = a.VS; println(e != nil) }`, "an interface holds a pointer here"},
		{"named", `func main() { var e error = a.PS; _, ok := e.(*a.sErr); println(ok) }`, "cannot refer to unexported name a.sErr"},
		// Its identity is the value's as well (definedName, kindlessCategory,
		// checkDefinedType): all four were taken.
		{"inferred", `func main() { c := a.Room; c = a.Room + 1; println(c, a.Dist(a.Origin, a.Corner)) }`, ""},
		{"intoInt", `func main() { var n int = a.Room; println(n) }`, "cannot use a.Room of type a.celsius as type int"},
		{"intoOwn", `type celsius int

func main() { var c celsius = a.Room; println(c) }`, "cannot use a.Room of type a.celsius as type celsius"},
		{"structInto", `type point struct{ X, Y int }

func main() { var p point = a.Origin; println(p.X) }`, "cannot use a.Origin of type a.point as type point"},
		{"structArg", `type point struct{ X, Y int }

func main() { println(a.Dist(point{1, 2}, a.Corner)) }`, "cannot use point{1, 2} of type point as type a.point"},
		{"case", `func main() {
	var x any = a.PS
	switch x.(type) {
	case *a.sErr:
		println(1)
	}
}`, "cannot refer to unexported name a.sErr"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo": &fstest.MapFile{Data: []byte("import \"a\"\n\n" + test.main + "\n")},
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
