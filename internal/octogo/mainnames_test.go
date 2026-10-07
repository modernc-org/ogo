// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmitCMainNamesInPackages runs programs whose main package declares a variable
// named like a name of a library it imports -- a constant, a function, a function
// value -- each used inside the library. Main's prefix is empty, so its globals are
// keyed by their source names, and the registries' bare-name fallback, meant for
// another package's global arriving already mangled, answered a name of the library
// with main's variable (bareGlobal): a's function f, called in a, went through main's
// function value f, and `range n` over a's constant 4 ranged over main's array n.
// Found by p2-11, whose vga declared `var params [16]uint32` beside vt100's `const
// params = 16`. Every expected output is Go's.
func TestEmitCMainNamesInPackages(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}
	const mainArr = `import "a"

var n [16]uint32

func main() {
	n[0] = 1
	println(a.X(), n[0])
}
`
	for _, test := range []struct {
		name, lib, main, want string
	}{
		{"a constant compared beside main's array", `const n = 16

func X() bool { return 3 < n }
`, mainArr, "true 1\n"},
		{"a function called beside main's function value", `func n() int { return 7 }

func X() int { return n() }
`, `import "a"

func nine() int { return 9 }

var n func() int = nine

func main() { println(a.X(), n()) }
`, "7 9\n"},
		{"a function as a value beside main's function value", `func n() int { return 7 }

func X() int {
	g := n
	return g()
}
`, `import "a"

func nine() int { return 9 }

var n func() int = nine

func main() { println(a.X(), n()) }
`, "7 9\n"},
		{"a function deferred beside main's function value", `var k int

func n() { k++ }

func X() int {
	defer n()
	n()
	return k
}

func K() int { return k }
`, `import "a"

var cnt int

func nine() { cnt += 9 }

var n func() = nine

func main() {
	x := a.X()
	n()
	println(x, a.K(), cnt)
}
`, "1 2 9\n"},
		{"a constant ranged over beside main's array", `const n = 4

func X() int {
	s := 0
	for i := range n {
		s += i
	}
	return s
}
`, mainArr, "6 1\n"},
		{"a constant printed beside main's array", `const n = 16

func X() int {
	println(n)
	return 0
}
`, mainArr, "16\n0 1\n"},
		{"the library's own array", `var n [3]uint32

func X() int {
	n[1] = 4
	return int(n[1]) + len(n)
}
`, mainArr, "7 1\n"},
		{"the library's own function value", `func seven() int { return 7 }

var n func() int = seven

func X() int { return n() }
`, `import "a"

func nine() int { return 9 }

var n func() int = nine

func main() { println(a.X(), n()) }
`, "7 9\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo": &fstest.MapFile{Data: []byte(test.main)},
				"a/a.ogo":  &fstest.MapFile{Data: []byte(test.lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			var buf bytes.Buffer
			if err := EmitC(pkg, &buf, Checked(), Inline()); err != nil {
				t.Fatalf("EmitC: %v", err)
			}
			dir := t.TempDir()
			csrc, bin := filepath.Join(dir, "main.c"), filepath.Join(dir, "prog")
			if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			// The flags of runCorpus, which says why each is there.
			if out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra", "-Wno-unused-function", "-Wno-format",
				"-I", shim, "-o", bin, csrc, "-lpthread", "-lm").CombinedOutput(); err != nil || len(bytes.TrimSpace(out)) != 0 {
				t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, out, buf.String())
			}
			got, err := exec.Command(bin).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, got)
			}
			if g := strings.ReplaceAll(string(got), "\r\n", "\n"); g != test.want {
				t.Errorf("output:\n got %q\nwant %q", g, test.want)
			}
		})
	}
}
