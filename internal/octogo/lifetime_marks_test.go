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

// lifetimeMarksHeader declares the package storage the marks tests store into.
const lifetimeMarksHeader = `import "unsafe"

var gs []uint32

var gq *uint32

var garr [4]uint32

var gps []*uint32

var _ = unsafe.Sizeof(garr)

`

// TestEmitCLoopCarriedMarks is the lifetime marks across a loop's back edge: a mark
// a statement of the body makes is read by the statements before it on the body's
// next run, which the marks, made in the order statements are emitted, did not see
// -- `for ... { gq = p; p = &x }` stored a local's address in a package variable in
// silence, in a for of every form and behind a backward goto. EmitC emits a
// program again with each loop's marks seeded at its head (loopSeeds). The controls
// are a mark read locally, package storage and a variable the body declares anew
// on each run.
func TestEmitCLoopCarriedMarks(t *testing.T) {
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"a pointer marked after its read in a for body", `func f() { var x uint32; var p *uint32; for i := 0; i < 2; i++ { gq = p; p = &x } }

func main() { f() }
`, true},
		{"the read behind a condition", `func f() { var x uint32; var p *uint32; for i := 0; i < 2; i++ { if i == 1 { gq = p }; p = &x } }

func main() { f() }
`, true},
		{"an array element marked after its read", `func f() { var a [4]*uint32; var x uint32; for i := 0; i < 2; i++ { if i == 1 { gq = a[0] }; a[0] = &x } }

func main() { f() }
`, true},
		{"a variable the for header declares, marked by the post", `func f() { var x uint32; for p := (*uint32)(nil); p == nil; p = &x { gq = p } }

func main() { f() }
`, true},
		{"a range loop", `func f() { var x uint32; var p *uint32; xs := []int{1, 2}; for range xs { gq = p; p = &x } }

func main() { f() }
`, true},
		{"an inner loop's read of an outer loop's mark", `func f() { var x uint32; var p *uint32; for { for j := 0; j < 2; j++ { gq = p }; p = &x; break } }

func main() { f() }
`, true},
		{"a slice marked as a view after its read", `func f() { var a [4]uint32; var s []uint32; for i := 0; i < 2; i++ { gs = s; s = a[:] } }

func main() { f() }
`, true},
		{"a copy of the marked variable read", `func f() { var x uint32; var p *uint32; for i := 0; i < 2; i++ { q := p; gq = q; p = &x } }

func main() { f() }
`, true},
		{"the mark behind a condition", `func f() { var x uint32; var p *uint32; for i := 0; i < 2; i++ { gq = p; if i == 0 { p = &x } } }

func main() { f() }
`, true},
		{"a mark two runs of the body away", `func f() { var x uint32; var p, q *uint32; for i := 0; i < 3; i++ { gq = q; q = p; p = &x } }

func main() { f() }
`, true},
		{"a backward goto", `func f() { var x uint32; var p *uint32; n := 0
L:
	gq = p
	p = &x
	n++
	if n < 2 {
		goto L
	}
}

func main() { f() }
`, true},
		{"a backward goto from a block", `func f() { var x uint32; var p *uint32; n := 0
L:
	{ gq = p }
	{ p = &x }
	n++
	if n < 2 {
		goto L
	}
}

func main() { f() }
`, true},
		{"a backward goto after a forward one", `func f() { var x uint32; var p *uint32; n := 0
	goto M
L:
	gq = p
	return
M:
	p = &x
	n++
	goto L
}

func main() { f() }
`, true},
		{"a loop in a function literal", `func f() { func() { var x uint32; var p *uint32; for i := 0; i < 2; i++ { gq = p; p = &x } }() }

func main() { f() }
`, true},
		{"control: the marked pointer read locally", `func f() { var x uint32; var p *uint32; for i := 0; i < 2; i++ { p = &x; println(*p) }; println(*p) }

func main() { f() }
`, false},
		{"control: package storage", `func f() { var p *uint32; for i := 0; i < 2; i++ { gq = p; p = &garr[0] } }

func main() { f() }
`, false},
		{"control: a variable of the body, fresh each run", `func f() { var x uint32; for i := 0; i < 2; i++ { var p *uint32; gq = p; p = &x; _ = p } }

func main() { f() }
`, false},
		{"control: a range variable, assigned each run", `func f() { var x uint32; ps := []*uint32{&garr[0]}; for _, p := range ps { gq = p; p = &x; _ = p } }

func main() { f() }
`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := lifetimeMarksHeader + test.src
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refuse && err == nil:
				t.Errorf("a reference to this frame left it:\n%s", src)
			case test.refuse && !strings.Contains(err.Error(), "outlive"):
				t.Errorf("refused, but not for its lifetime: %v", err)
			case !test.refuse && err != nil:
				t.Errorf("refused: %v\n%s", err, src)
			}
		})
	}
}

// TestEmitCSliceViewMarks is the marks of a slice and of the local it views: a store
// into either is a store into both, and a slice's elements were marked when it was
// bound, as the array's stood then -- `s := a[:]; a[0] = &x; gq = s[0]` was taken.
// A read of a slice's elements consults what it may view (viewedHolder), and a store
// through a view known to view a local marks the local (noteStoredThrough).
func TestEmitCSliceViewMarks(t *testing.T) {
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"an array marked after a slice viewed it", `func f() { var a [4]*uint32; var x uint32; s := a[:]; a[0] = &x; gq = s[0] }

func main() { f() }
`, true},
		{"a copy of the view", `func f() { var a [4]*uint32; var x uint32; s := a[:]; t := s; s[0] = &x; gq = t[0] }

func main() { f() }
`, true},
		{"a reslice of the view", `func f() { var a [4]*uint32; var x uint32; s := a[:]; t := s[1:]; s[1] = &x; gq = t[0] }

func main() { f() }
`, true},
		{"an unsafe.Slice view", `func f() { var a [4]*uint32; var x uint32; s := unsafe.Slice(&a[0], 4); a[0] = &x; gq = s[0] }

func main() { f() }
`, true},
		{"a view of a row", `func f() { var a [2][2]*uint32; var x uint32; s := a[0][:]; a[0][0] = &x; gq = s[0] }

func main() { f() }
`, true},
		{"a view of a field", `func f() { type H struct{ a [2]*uint32 }; var h H; var x uint32; s := h.a[:]; h.a[0] = &x; gq = s[0] }

func main() { f() }
`, true},
		{"the array marked in a loop", `func f() { var a [4]*uint32; var x uint32; s := a[:]; for i := range a { a[i] = &x }; gq = s[0] }

func main() { f() }
`, true},
		{"a store through the view, read through the array", `func f() { var a [4]*uint32; var x uint32; s := a[:]; s[0] = &x; gq = a[0] }

func main() { f() }
`, true},
		{"a store through a reslice view, read through the array", `func f() { var a [4]*uint32; var x uint32; s := a[1:]; s[0] = &x; gq = a[1] }

func main() { f() }
`, true},
		{"control: package storage in the array", `func f() { var a [4]*uint32; s := a[:]; a[0] = &garr[0]; gq = s[0] }

func main() { f() }
`, false},
		{"control: a slice of package storage", `func f() { var a [4]*uint32; var x uint32; s := gps; a[0] = &x; gq = s[0]; println(*a[0]) }

func main() { f() }
`, false},
		{"control: a view of another array", `func f() { var a, b [4]*uint32; var x uint32; s := a[:]; t := b[:]; a[0] = &x; gq = t[0]; println(*s[0]) }

func main() { f() }
`, false},
		{"control: the view read locally", `func f() { var a [4]*uint32; var x uint32; s := a[:]; a[0] = &x; println(*s[0]) }

func main() { f() }
`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := lifetimeMarksHeader + test.src
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refuse && err == nil:
				t.Errorf("a reference to this frame left it:\n%s", src)
			case test.refuse && !strings.Contains(err.Error(), "outlive"):
				t.Errorf("refused, but not for its lifetime: %v", err)
			case !test.refuse && err != nil:
				t.Errorf("refused: %v\n%s", err, src)
			}
		})
	}
}
