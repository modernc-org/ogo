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

// TestEmitCSummaryOtherPackageStores asks what a function keeps of a parameter it
// stores into ANOTHER package's variable: all of it, as a store into one of its own
// package's does. The summaries asked isPackageVar of a target's root, which for
// `lib.G` is the qualifier and no variable of this package, so `lib.G = p` recorded
// nothing and `keep(&x)` left x's address in lib, where `lib.G = &x` was refused
// (outlivesByName). Each way a store reaches such a variable, and a control.
func TestEmitCSummaryOtherPackageStores(t *testing.T) {
	const lib = `type H struct{ P *int }

var G *int

var GH H

var GA [2]*int
`
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"a statement", `import "lib"

func keep(p *int) { lib.G = p }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a list", `import "lib"

func keep(p *int) {
	n := 0
	lib.G, n = p, 1
	_ = n
}

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a field", `import "lib"

func keep(p *int) { lib.GH.P = p }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"an element", `import "lib"

func keep(p *int) { lib.GA[1] = p }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"through a callee", `import "lib"

func put(dst **int, p *int) { *dst = p }

func keep(p *int) { put(&lib.G, p) }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a for clause's post", `import "lib"

func keep(p *int) {
	for i := 0; i < 1; lib.G = p {
		i++
	}
}

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"through a local pointer", `import "lib"

func keep(p *int) {
	q := &lib.G
	*q = p
}

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a package variable's address", `import "lib"

var gx int

func keep(p *int) { lib.G = p }

func main() { keep(&gx) }
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
