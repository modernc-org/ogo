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

// TestCheckImportedQualifiedResult: a method of another package's type whose
// result that package spells with a qualifier of its own, `unsafe.Pointer`. Its
// name was read as the receiver package's, `hub.Pointer`, so `(*uint32)(blk.Addr(0))`
// was refused as a conversion of a hub.Pointer; and its Kind was read nowhere, the
// signature carried into this file failing on the qualified name, so `var n int =
// blk.Addr(0)` was taken where the function form, `hub.Addr(0)`, was refused.
func TestCheckImportedQualifiedResult(t *testing.T) {
	const lib = `import "unsafe"

type Block struct {
	Words [4]uintptr
}

func (b *Block) Addr(i int) unsafe.Pointer {
	return unsafe.Pointer(b.Words[i])
}

func Addr(i int) unsafe.Pointer { return nil }
`
	for _, test := range []struct {
		name, body string
		want       string // "" for a program Go takes
	}{
		{"conversion", `q := (*uint32)(blk.Addr(0))
	_ = q`, ""},
		{"store through", `*(*uint32)(blk.Addr(0)) = 7`, ""},
		{"into unsafe.Pointer", `var a unsafe.Pointer = blk.Addr(0)
	_ = a`, ""},
		{"method into int", `var n int = blk.Addr(0)
	_ = n`, "cannot use blk.Addr(0) of type unsafe.Pointer as type int"},
		{"function into int", `var n int = hub.Addr(0)
	_ = n`, "cannot use hub.Addr(0) of type unsafe.Pointer as type int"},
		{"arithmetic", `x := blk.Addr(0) + 1
	_ = x`, "operator + not defined on unsafe.Pointer and int"},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "import (\n\t\"unsafe\"\n\n\t\"hub\"\n)\n\nvar blk hub.Block\n\nfunc main() {\n\t" + test.body + "\n\t_ = unsafe.Pointer(nil)\n}\n"
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte(src)},
				"hub/hub.ogo": &fstest.MapFile{Data: []byte(lib)},
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

// TestCheckQualifiedConversion: a conversion to another package's defined type of a
// Kind asks its operand what one to this package's asks. Only the count was asked,
// so `lib.Op(-1)`, `lib.Op(true)`, `lib.Op(nil)` and `lib.Name(2.5)` were taken.
func TestCheckQualifiedConversion(t *testing.T) {
	const lib = "type Op uint8\n\ntype Name string\n\ntype F float32\n"
	for _, test := range []struct {
		conv string
		want string // "" for a conversion Go takes
	}{
		{"lib.Op(-1)", "constant -1 overflows lib.Op"},
		{"lib.Op(1 << 40)", "constant 1099511627776 overflows lib.Op"},
		{"lib.Op(true)", "cannot convert true (untyped bool constant) to type lib.Op"},
		{"lib.Op(nil)", "cannot convert nil to type lib.Op"},
		{"lib.Op([]int{1})", "cannot convert []int{1} to type lib.Op: it is a slice"},
		{"lib.Name(2.5)", "cannot convert 2.5 (untyped float constant) to type lib.Name"},
		{"lib.F(\"s\")", "cannot convert \"s\" (untyped string constant) to type lib.F"},
		{"lib.Op(3)", ""},
		{"lib.Name(5)", ""},
		{"lib.Name('a')", ""},
		{"lib.F(2.5)", ""},
		{"lib.Op(x)", ""},
	} {
		t.Run(test.conv, func(t *testing.T) {
			src := "import \"lib\"\n\nfunc main() {\n\tx := 3\n\t_ = x\n\t_ = " + test.conv + "\n}\n"
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte(src)},
				"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
			}
			_, err := Build(-1, []string{"main.ogo"}, fsys)
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}

// TestCheckQualifiedArrayLit: a literal of another package's defined array or slice
// type, its elements and the chain on it. The elements were asked nothing, the
// element type being that package's spelling, and a chain was walked by nothing --
// a pointer method on one taken once the emitter read such a chain.
func TestCheckQualifiedArrayLit(t *testing.T) {
	const lib = "type Q int16\n\ntype Taps [3]Q\n\nfunc (t *Taps) Sum() int32 { return int32(t[0]) }\n\nfunc (t Taps) First() Q { return t[0] }\n\ntype IS []Q\n\nfunc (s IS) Len() int { return len(s) }\n"
	for _, test := range []struct {
		expr string
		want string // "" for one Go takes
	}{
		{"lib.Taps{1, true, 2}", "cannot use true of type bool as type Q in array or slice literal"},
		{"lib.Taps{1, 0x80000000}", "constant 2147483648 overflows Q"},
		{"lib.Taps{1, \"s\"}", "cannot use \"s\" of type string as type Q in array or slice literal"},
		{"lib.Taps{1, 2, 3, 4}", "index 3 out of bounds [0:3]"},
		{"lib.IS{1, n}", "cannot use n of type int16 as type lib.Q in array or slice literal"},
		{"lib.Taps{1, 2, 3}.Sum()", "cannot call pointer method Sum on lib.Taps"},
		{"lib.Taps{1, 2, 3}.Nope()", "type lib.Taps has no method Nope"},
		{"lib.IS{1}.Nope()", "type lib.IS has no method Nope"},
		{"lib.Taps{1, 2, 3}.First(4)", "too many arguments in call to First"},
		{"n + lib.Taps{1, 2, 3}[1]", "mismatched types"},
		{"n + lib.Taps{1, 2, 3}.First()", "mismatched types"},
		{"lib.Taps{1, 2, 3}.First() + lib.IS{4}[0] + q", ""},
		{"lib.IS{1, 2}.Len() + int(lib.Taps{}[0])", ""},
	} {
		t.Run(test.expr, func(t *testing.T) {
			src := "import \"lib\"\n\nfunc main() {\n\tvar n int16\n\tvar q lib.Q\n\t_, _ = n, q\n\t_ = " + test.expr + "\n}\n"
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte(src)},
				"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
			}
			_, err := Build(-1, []string{"main.ogo"}, fsys)
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}

// TestCheckThirdPackage: a type a package writes qualified, `q15.Q` in filt, read and
// written from main, with main importing q15 and without. It could not be carried
// into main's spelling, so a value going into filt's field, element, variable or
// literal of a q15.Q was asked nothing, and one read out of it named nothing. And a
// value received from another package's channel had no Kind, however reached.
func TestCheckThirdPackage(t *testing.T) {
	const q15 = "type Q int16\n\ntype R struct{ N int }\n\nfunc (q Q) Twice() Q { return q * 2 }\n"
	const filt = "import \"q15\"\n\ntype S struct {\n\tX q15.Q\n\tR q15.R\n}\n\ntype Taps [3]q15.Q\n\nvar G q15.Q\n\nvar GR q15.R\n\nvar GS S\n\nvar Arr Taps\n\nvar Ch chan q15.Q\n\nvar Ci chan int\n\ntype H struct{ In chan int }\n\nvar V H\n\nfunc Get() chan int { return Ci }\n"
	for _, test := range []struct {
		imp  bool
		body string
		want string // "" for one Go takes
	}{
		{false, "filt.GS.X = true", "cannot use true of type bool as type Q in assignment"},
		{false, "filt.GS.X = 70000", "constant 70000 overflows Q"},
		{false, "filt.Arr[1] = true", "cannot use true of type bool as type Q in assignment"},
		{false, "filt.GR.N = true", "cannot use true of type bool as type int in assignment"},
		{false, "filt.GS.R.N = \"s\"", "cannot use \"s\" of type string as type int in assignment"},
		{false, "_ = filt.Taps{1, true}", "cannot use true of type bool as type Q in array or slice literal"},
		{false, "filt.G = 5\n\tfilt.Arr[1] = 9\n\tfilt.GR.N = 3", ""},
		{true, "_ = filt.S{X: true}", "cannot use true of type bool as type Q in struct literal"},
		{true, "filt.G = true", "cannot use true of type bool as type Q in assignment"},
		{true, "var n int16\n\tfilt.G = n", "cannot use n of type int16 as type q15.Q in assignment"},
		{true, "var x int16 = filt.G\n\t_ = x", "cannot use filt.G of type q15.Q as type int16"},
		{true, "var x int16 = filt.GS.X\n\t_ = x", "of type q15.Q as type int16"},
		{true, "var x int16 = filt.Arr[0]\n\t_ = x", "of type q15.Q as type int16"},
		{true, "var x int16 = filt.Taps{1}[0]\n\t_ = x", "of type q15.Q as type int16"},
		{true, "var x int16 = filt.G.Twice()\n\t_ = x", "of type q15.Q as type int16"},
		{true, "var x int16 = <-filt.Ch\n\t_ = x", "of type q15.Q as type int16"},
		{true, "var q q15.Q = filt.G + filt.GS.X + filt.Arr[0] + filt.G.Twice() + <-filt.Ch\n\tfilt.G = q", ""},
		{false, "var x bool = <-filt.Ci\n\t_ = x", "cannot use <-filt.Ci of type int as type bool"},
		{false, "var x bool = <-filt.V.In\n\t_ = x", "of type int as type bool"},
		{false, "var x string\n\tx = <-filt.Get()\n\t_ = x", "of type int as type string"},
		{false, "var x int = <-filt.Ci + <-filt.V.In + <-filt.Get()\n\t_ = x", ""},
	} {
		t.Run(test.body, func(t *testing.T) {
			imp := "import \"filt\"\n\n"
			if test.imp {
				imp = "import (\n\t\"filt\"\n\t\"q15\"\n)\n\nvar _ q15.Q\n\n"
			}
			src := imp + "func main() {\n\t" + test.body + "\n}\n"
			fsys := fstest.MapFS{
				"main.ogo":      &fstest.MapFile{Data: []byte(src)},
				"filt/filt.ogo": &fstest.MapFile{Data: []byte(filt)},
				"q15/q15.ogo":   &fstest.MapFile{Data: []byte(q15)},
			}
			_, err := Build(-1, []string{"main.ogo"}, fsys)
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}

// TestCheckIfaceMethodValue: a method value whose receiver is an interface value
// is refused by design, as one whose receiver is a pointer is -- Go saves the
// value when the method value is taken, and a binding made at compile time cannot
// -- and says so in every position. It was "type Shape has no field Area", or the
// emitter's "cannot infer a type".
func TestCheckIfaceMethodValue(t *testing.T) {
	const pre = "type Shape interface{ Area() int }\n\ntype Sq struct{ n int }\n\nfunc (s *Sq) Area() int { return s.n }\n\ntype H struct{ s Shape }\n\nvar gs = Sq{3}\n\nvar tab = [1]Shape{&gs}\n\nvar one Shape = &gs\n\nvar h = H{&gs}\n\nfunc use(f func() int) {}\n\n"
	for _, test := range []struct {
		body string
		want string // "" for a program this takes
	}{
		{"f := one.Area\n\t_ = f", "cannot take one.Area as a value: its receiver is an interface"},
		{"f := tab[0].Area\n\t_ = f", "cannot take tab[0].Area as a value: its receiver is an interface"},
		{"var f func() int = tab[0].Area\n\t_ = f", "cannot take tab[0].Area as a value"},
		{"use(h.s.Area)", "cannot take h.s.Area as a value"},
		{"use(one.Area)", "cannot take one.Area as a value"},
		{"println(one.Area(), tab[0].Area(), h.s.Area())", ""},
		{"f := func() int { return one.Area() }\n\t_ = f", ""},
		{"f := gs.Area\n\t_ = f", ""},
	} {
		t.Run(test.body, func(t *testing.T) {
			src := pre + "func main() {\n\t" + test.body + "\n}\n"
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			_, err := Build(-1, []string{"main.ogo"}, fsys)
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}
