// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"testing"
	"testing/fstest"
)

// TestMayRunSubtests holds what decides whether the runner hands a program what
// naming a subtest takes: Run named in a package of the program, the main one or
// one it imports, and not in the testing package, which declares it. A package
// naming Run where the program does not call it counts too: the price is the
// table's bytes, where missing a call would leave a subtest no name.
func TestMayRunSubtests(t *testing.T) {
	const lib = `import "testing"

func Sub(t *testing.T, f func(*testing.T)) { t.Run("x", f) }

func Log(t *testing.T) { t.Log("x") }
`
	const plain = `import "testing"

func Log(t *testing.T) { t.Log("x") }
`
	for _, test := range []struct {
		name, main string
		want       bool
	}{
		{"none", `import "testing"

func test(t *testing.T) { t.Log("x") }

func main() {
	var t testing.T
	testing.RunTest(&t, "T", test)
}
`, false},
		{"lib without", `import (
	"plain"
	"testing"
)

func main() {
	var t testing.T
	testing.RunTest(&t, "T", plain.Log)
}
`, false},
		{"lib's unused", `import (
	"lib"
	"testing"
)

func main() {
	var t testing.T
	testing.RunTest(&t, "T", lib.Log)
}
`, true},
		{"call", `import "testing"

func test(t *testing.T) { t.Run("x", test) }

func main() {
	var t testing.T
	testing.RunTest(&t, "T", test)
}
`, true},
		{"method expression", `import "testing"

func leaf(t *testing.T) {}

func test(t *testing.T) {
	r := (*testing.T).Run
	r(t, "x", leaf)
}

func main() {
	var t testing.T
	testing.RunTest(&t, "T", test)
}
`, true},
		{"in lib", `import (
	"lib"
	"testing"
)

func leaf(t *testing.T) {}

func test(t *testing.T) { lib.Sub(t, leaf) }

func main() {
	var t testing.T
	testing.RunTest(&t, "T", test)
}
`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo":        &fstest.MapFile{Data: []byte(test.main)},
				"lib/lib.ogo":     &fstest.MapFile{Data: []byte(lib)},
				"plain/plain.ogo": &fstest.MapFile{Data: []byte(plain)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err != nil {
				t.Fatal(err)
			}
			if got := MayRunSubtests(pkg); got != test.want {
				t.Errorf("got %v, want %v", got, test.want)
			}
		})
	}
}

// TestRunsSubtests holds the second question, asked of the C a test program was
// emitted as: whether it reaches T.Run or B.Run, by a call, a method expression in
// a package variable or an interface's table -- and not where Run is the program's
// own method.
func TestRunsSubtests(t *testing.T) {
	const head = `import "testing"

func leaf(t *testing.T) {}

func bleaf(b *testing.B) {}
`
	const main = `
func main() {
	var t testing.T
	testing.RunTest(&t, "T", test)
}
`
	for _, test := range []struct {
		name, src string
		want      bool
	}{
		{"none", `func test(t *testing.T) { t.Log("x") }`, false},
		{"call", `func test(t *testing.T) { t.Run("x", leaf) }`, true},
		{"benchmark", `func test(t *testing.T) {}

func bench(b *testing.B) { b.Run("x", bleaf) }

var sink = bench`, true},
		{"package method expression", `var run = (*testing.T).Run

func test(t *testing.T) { run(t, "x", leaf) }`, true},
		{"interface", `type runner interface {
	Run(name string, f func(*testing.T)) bool
}

func test(t *testing.T) {
	var r runner = t
	r.Run("x", leaf)
}`, true},
		{"own Run", `type M struct{ n int }

func (m *M) Run(n int) int {
	m.n += n
	return m.n
}

var m M

func test(t *testing.T) { t.Log(m.Run(3)) }`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := head + "\n" + test.src + "\n" + main
			pkg, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}})
			if err != nil {
				t.Fatal(err)
			}
			var c bytes.Buffer
			if err := EmitC(pkg, &c, Checked(), Inline()); err != nil {
				t.Fatal(err)
			}
			if !MayRunSubtests(pkg) && test.want {
				t.Fatal("the premise: the source names Run")
			}
			if got := RunsSubtests(c.Bytes()); got != test.want {
				t.Errorf("got %v, want %v", got, test.want)
			}
		})
	}
}
