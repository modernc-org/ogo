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

// TestEmitCRelayAcrossPackages asks what a function relaying its parameter to
// ANOTHER package's function keeps: what that function keeps of it. The summaries
// named a callee by the name it is called by, and `lib.Keep` is no name of the
// package being read, so `func relay(p *int) { lib.Keep(p) }` recorded nothing and
// `relay(&x)` left x's address in lib.G, where the same relay inside lib was
// refused (qualifiedFuncCall). Each way a relay may be written, and the controls
// that keep nothing.
func TestEmitCRelayAcrossPackages(t *testing.T) {
	const lib = `var G *int

var Gs []int

func Keep(p *int) { G = p }

func KeepS(v []int) { Gs = v }

func Pass(p *int) *int { return p }

func Read(p *int) int { return *p }
`
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"a relay as a statement", `import "lib"

func relay(p *int) { lib.Keep(p) }

func run() {
	var x int
	relay(&x)
}

func main() { run() }
`, true},
		{"a relay deferred", `import "lib"

func relay(p *int) { defer lib.Keep(p) }

func run() {
	var x int
	relay(&x)
}

func main() { run() }
`, true},
		{"a relay's result stored", `import "lib"

var g *int

func relay(p *int) { g = lib.Pass(p) }

func run() {
	var x int
	relay(&x)
}

func main() { run() }
`, true},
		{"a relay's result handed on", `import "lib"

var g *int

func keep(p *int) { g = p }

func relay(p *int) { keep(lib.Pass(p)) }

func run() {
	var x int
	relay(&x)
}

func main() { run() }
`, true},
		{"a slice relayed", `import "lib"

func relay(v []int) { lib.KeepS(v) }

func run() {
	var b [4]int
	relay(b[:])
}

func main() { run() }
`, true},
		{"a relay two deep", `import "lib"

func relay(p *int) { lib.Keep(p) }

func outer(p *int) { relay(p) }

func run() {
	var x int
	outer(&x)
}

func main() { run() }
`, true},
		{"a relay to a reader", `import "lib"

func relay(p *int) int { return lib.Read(p) }

func run() {
	var x int
	println(relay(&x))
}

func main() { run() }
`, false},
		{"a package variable relayed", `import "lib"

var gx int

func relay(p *int) { lib.Keep(p) }

func main() { relay(&gx) }
`, false},
		{"a relay's result read", `import "lib"

func relay(p *int) int { return *lib.Pass(p) }

func run() {
	var x int
	println(relay(&x))
}

func main() { run() }
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
