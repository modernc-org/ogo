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

// TestCheckGoQualifiedConversion: `go` and `defer` of a conversion to ANOTHER
// package's type, `go lib.T(1, 2)`, are Go's "go requires function call, not
// conversion", as the same statement of this package's type was. The selector after
// the qualifier let it through as a call, to a trampoline calling the type, which
// the C compiler refused about generated C. Found mutating a domain program, `go
// sensor.Source(...)` turned into `go sensor.Reading(...)`.
func TestCheckGoQualifiedConversion(t *testing.T) {
	const lib = `type T struct{ A, B int }

type N int

func F(n int) {}

func (t T) M() {}
`
	for _, test := range []struct {
		name, main string
		want       string // "" for a program Go takes
	}{
		{"go struct", `func main() { go lib.T(1, 2) }`, "go requires function call, not conversion lib.T(1, 2)"},
		{"defer defined", `func main() { defer lib.N(3) }`, "defer requires function call, not conversion lib.N(3)"},
		{"go function", `func main() { go lib.F(1) }`, ""},
		{"defer method on a conversion", `func main() { defer lib.T(lib.T{1, 2}).M() }`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte("import \"lib\"\n\n" + test.main + "\n")},
				"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
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
