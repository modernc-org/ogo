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

// TestEmitCArrayPtrConvLifetime asks the lifetime rules about a conversion to a
// pointer to an array type written out, `(*[4]int)(x)`. The conversion is the
// pointer it converts, or -- from a slice -- the slice's backing, so whatever x
// reaches of a frame the conversion reaches too: stored, returned, sent, kept by a
// callee directly or through a local, a field, a receiver, or turned into a number.
// The summaries read a body by shape, and read `(*T)(x)` only for a NAMED T until
// the bracketed one was taught them (ptrConvShape): every callee row was accepted.
// The controls convert package storage, and a local only read through.
func TestEmitCArrayPtrConvLifetime(t *testing.T) {
	const hdr = `import "unsafe"

var gp *[4]int

var g2 *[2]int

var ch = make(chan *[4]int)

var gu uintptr

var g [4]int

var _ = unsafe.Pointer(&g)

`
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"a local's address stored", `func run() {
	var a [4]int
	gp = (*[4]int)(&a)
}

func main() {
	run()
}
`, true},
		{"a local's address through an unsafe.Pointer stored", `func run() {
	var a [4]int
	gp = (*[4]int)(unsafe.Pointer(&a))
}

func main() {
	run()
}
`, true},
		{"a slice of a local stored", `func run() {
	var a [4]int
	g2 = (*[2]int)(a[2:])
}

func main() {
	run()
}
`, true},
		{"a slice of a local held in a local stored", `func run() {
	var a [4]int
	s := a[1:]
	g2 = (*[2]int)(s)
}

func main() {
	run()
}
`, true},
		{"a conversion held in a local stored", `func run() {
	var a [4]int
	p := (*[2]int)(a[:])
	g2 = p
}

func main() {
	run()
}
`, true},
		{"a local's address returned", `func f() *[4]int {
	var a [4]int
	return (*[4]int)(&a)
}

func main() { _ = f() }
`, true},
		{"a slice of a local returned", `func f() *[2]int {
	var a [4]int
	return (*[2]int)(a[2:])
}

func main() { _ = f() }
`, true},
		{"a callee storing its slice", `func keep(s []int) { g2 = (*[2]int)(s) }

func run() {
	var a [4]int
	keep(a[:])
}

func main() {
	run()
}
`, true},
		{"a callee storing its pointer", `func keep(p *[4]int) { gp = (*[4]int)(p) }

func run() {
	var a [4]int
	keep(&a)
}

func main() {
	run()
}
`, true},
		{"a callee storing its pointer through an unsafe.Pointer", `func keep(p *int) { gp = (*[4]int)(unsafe.Pointer(p)) }

func run() {
	var a [4]int
	keep(&a[0])
}

func main() {
	run()
}
`, true},
		{"a callee returning its slice", `func view(s []int) *[2]int { return (*[2]int)(s) }

func run() {
	var a [4]int
	g2 = view(a[:])
}

func main() {
	run()
}
`, true},
		{"a callee storing through a local", `func keep(s []int) {
	p := (*[2]int)(s[1:])
	g2 = p
}

func run() {
	var a [4]int
	keep(a[:])
}

func main() {
	run()
}
`, true},
		{"a callee storing into a parameter's field", `type W struct{ p *[2]int }

var w W

func keep(dst *W, s []int) { dst.p = (*[2]int)(s) }

func run() {
	var a [4]int
	keep(&w, a[:])
}

func main() {
	run()
}
`, true},
		{"a method keeping its receiver's array", `type R struct{ b [4]int }

func (r *R) save() { gp = (*[4]int)(&r.b) }

func main() {
	var r R
	r.save()
}
`, true},
		{"a callee sending its pointer to another cog", `func send(p *int) { ch <- (*[4]int)(unsafe.Pointer(p)) }

func run() {
	var a [4]int
	send(&a[0])
}

func main() {
	run()
}
`, true},
		{"a uintptr of a conversion of a slice of a local", `func run() {
	var a [4]int
	gu = uintptr(unsafe.Pointer((*[2]int)(a[:])))
}

func main() {
	run()
}
`, true},
		{"package storage stored, directly and by a callee", `func keep(s []int) { g2 = (*[2]int)(s) }

func main() {
	keep(g[:])
	g2 = (*[2]int)(g[2:])
	gp = (*[4]int)(unsafe.Pointer(&g))
}
`, false},
		{"a local read through", `func sum(s []int) int {
	p := (*[2]int)(s)
	return p[0] + p[1]
}

func main() {
	var a [4]int
	p := (*[4]int)(&a)
	p[2] = sum(a[1:])
}
`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := hdr + test.src
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refuse && err == nil:
				t.Errorf("a reference to this frame left it:\n%s", src)
			case test.refuse && !strings.Contains(err.Error(), "outlive") && !strings.Contains(err.Error(), "lifetime") && !strings.Contains(err.Error(), "another cog"):
				t.Errorf("refused, but not for its lifetime: %v", err)
			case !test.refuse && err != nil:
				t.Errorf("refused: %v\n%s", err, src)
			}
		})
	}
}
