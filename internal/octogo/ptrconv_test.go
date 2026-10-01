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

// TestEmitCPtrConvSummaries asks what a function keeps of a parameter it converts
// to a pointer type, `(*T)(p)`: what it keeps of p. The summaries read a body by
// shape, and the conversion was a shape they did not read, so `gp = (*int)(p)` and
// `return (*int)(p)` recorded nothing and `keep(&x)` left x's address in a package
// variable, where `gp = p` was refused (ptrConvShape). Each way the converted value
// reaches a sink, and the controls that keep nothing.
func TestEmitCPtrConvSummaries(t *testing.T) {
	const lib = `type N int

var G *N
`
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"stored", `var gp *int

func keep(p *int) { gp = (*int)(p) }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"returned", `var gp *int

func pass(p *int) *int { return (*int)(p) }

func run() {
	var x int
	gp = pass(&x)
}

func main() { run() }
`, true},
		{"a call converted", `var gp *int

func pass(p *int) *int { return p }

func keep(p *int) { gp = (*int)(pass(p)) }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a field read through it", `type B struct{ xs []int }

var gs []int

func keep(p *B) { gs = (*B)(p).xs }

func run() {
	var a [4]int
	b := B{a[:]}
	keep(&b)
}

func main() { run() }
`, true},
		{"held by a local first", `var gp *int

func keep(p *int) {
	q := (*int)(p)
	gp = q
}

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"into a field of a package variable", `type H struct{ p *int }

var gh H

func keep(p *int) { gh.p = (*int)(p) }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"to another package's type", `import "lib"

func keep(p *lib.N) { lib.G = (*lib.N)(p) }

func run() {
	var x lib.N
	keep(&x)
}

func main() { run() }
`, true},
		{"a package variable's address", `var gp *int

var gx int

func keep(p *int) { gp = (*int)(p) }

func main() { keep(&gx) }
`, false},
		{"only read through", `func read(p *int) int { return *(*int)(p) }

func run() int {
	var x int = 4
	return read(&x)
}

func main() { println(run()) }
`, false},
		{"converted into a local and read", `func read(p *int) int {
	q := (*int)(p)
	return *q + 1
}

func run() int {
	var x int = 4
	return read(&x)
}

func main() { println(run()) }
`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte(test.src)},
				"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refuse && err == nil:
				t.Errorf("a reference to this frame left it:\n%s", test.src)
			case test.refuse && !strings.Contains(err.Error(), "outlive"):
				t.Errorf("refused, but not for its lifetime: %v", err)
			case !test.refuse && err != nil:
				t.Errorf("refused: %v\n%s", err, test.src)
			}
		})
	}
}
