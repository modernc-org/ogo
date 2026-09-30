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

// TestEmitCSliceElemAddr asks what the address of an element of a slice reaches:
// the array the slice views, which for `s := xs[:]` is this frame's. addrOfRoot
// stopped at the slice's index and the backing was asked of a slice's value alone,
// so `keep(&s[0])` left a pointer into a dead frame in a package variable where
// `keep(&xs[0])` was refused (addrOfSliceElem). Each sink and each backing the
// frame owns, and the controls viewing what it does not.
func TestEmitCSliceElemAddr(t *testing.T) {
	const keep = `var g *int

func keep(p *int) { g = p }

`
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"handed to a keeper", keep + `func run() {
	var xs [4]int
	s := xs[:]
	keep(&s[0])
}

func main() { run() }
`, true},
		{"bound to a pointer first", keep + `func run() {
	var xs [4]int
	s := xs[:]
	p := &s[1]
	keep(p)
}

func main() { run() }
`, true},
		{"stored in a package variable", keep + `func run() {
	var xs [4]int
	s := xs[:]
	g = &s[2]
}

func main() { run() }
`, true},
		{"returned", `func first() *int {
	var xs [4]int
	s := xs[:]
	return &s[0]
}

func main() { println(*first()) }
`, true},
		{"after a reslice", keep + `func run() {
	var xs [4]int
	s := xs[:]
	keep(&s[1:][0])
}

func main() { run() }
`, true},
		{"of what make allocates", keep + `func run() {
	s := make([]int, 4)
	keep(&s[0])
}

func main() { run() }
`, true},
		{"of a slice literal", keep + `func run() {
	s := []int{1, 2, 3}
	keep(&s[0])
}

func main() { run() }
`, true},
		{"read through a pointer into a slice of pointers", keep + `func run() {
	var x int
	s := []*int{&x}
	p := &s[0]
	g = *p
}

func main() { run() }
`, true},
		{"kept in a local of the outer block", `func run(c bool) int {
	var xs [4]int
	s := xs[:]
	var q *int
	if c {
		q = &s[0]
	} else {
		q = &s[1]
	}
	*q = 5
	return xs[0] + xs[1]
}

func main() { println(run(true)) }
`, false},
		{"of a parameter's slice", keep + `func run(v []int) { keep(&v[0]) }

var gs [4]int

func main() { run(gs[:]) }
`, false},
		{"of a slice of a package array", keep + `var gs [4]int

func run() {
	s := gs[:]
	keep(&s[0])
}

func main() { run() }
`, false},
		{"only read through", `func run() int {
	var xs [4]int
	s := xs[:]
	p := &s[0]
	*p = 7
	return *p
}

func main() { println(run()) }
`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(test.src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refuse && err == nil:
				t.Errorf("a reference to this frame left it:\n%s", test.src)
			case test.refuse && !strings.Contains(err.Error(), "outlive") && !strings.Contains(err.Error(), "local"):
				t.Errorf("refused, but not for its lifetime: %v", err)
			case !test.refuse && err != nil:
				t.Errorf("refused: %v\n%s", err, test.src)
			}
			if test.name == "handed to a keeper" && err != nil && !strings.Contains(err.Error(), "the address of an element of s") {
				t.Errorf("the refusal does not name what is passed: %v", err)
			}
		})
	}
}
