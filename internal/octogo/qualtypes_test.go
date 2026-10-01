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

// TestCheckQualifiedTypes asks what the checker makes of another package's defined
// type, `a.Count`, used in main -- most of all where main declares a type of the same
// name. The helpers following a chain of definitions looked the name up bare, in the
// package asking (typeIdentDecl), so `a.Count` was main's Count where main had one
// and of no Kind otherwise: valid programs were refused, a dereference of `a.P`
// always, and programs Go refuses were taken. A program that builds runs on the host
// and prints what Go prints; one Go refuses is refused, in the checker's words.
func TestCheckQualifiedTypes(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}
	const lib = `type Count int

type Name string

type Temp float32

type P *int

type A [3]int

type Item struct{ N int }

type S []Item

type T struct{ N int }

type Ch chan int

var G int

func Get() P { return &G }

func Add(c Count, n int) Count { return c + Count(n) }

func (c Count) Double() Count { return c * 2 }

func (q *T) Area() Count { return Count(q.N * q.N) }

type Shape interface{ Area() Count }

var Q = T{3}

func Qp() *T { return &Q }

func (t Temp) Half() Temp { return t / 2 }

func (t Temp) Int() int { return int(t) }
`
	for _, test := range []struct {
		name, main string
		want       string // the output, or with refuse the message
		refuse     bool
	}{
		{"a variable beside main's type of the name", `import "a"

type Count string

func main() {
	var c a.Count = 5
	var d Count = "x"
	println(c, d)
}
`, "5 x\n", false},
		{"a parameter and a result beside main's type", `import "a"

type Count string

func inc(x a.Count) a.Count { return x + 1 }

func main() { println(inc(4), a.Add(2, 3)) }
`, "5 5\n", false},
		{"a field and arithmetic beside main's type", `import "a"

type Count string

type H struct{ n a.Count }

func main() {
	h := H{n: 7}
	h.n += 2
	println(h.n, h.n > 10, h.n%5)
}
`, "9 false 4\n", false},
		{"a field of the type in main's type of the name", `import "a"

type T struct{ v a.T }

func main() {
	var t T
	t.v.N = 4
	println(t.v.N)
}
`, "4\n", false},
		{"a pointer type dereferenced", `import "a"

type P int

func main() {
	var p a.P = a.Get()
	println(p == nil, *p)
}
`, "false 0\n", false},
		{"an array type indexed", `import "a"

type A int

func main() {
	var x a.A
	x[1] = 5
	println(len(x), x[1])
}
`, "3 5\n", false},
		{"a slice type's element", `import "a"

type Item struct{ m int }

func first(s a.S) int { return s[0].N }

func main() {
	var back [2]a.Item
	back[0].N = 6
	println(first(back[:]))
}
`, "6\n", false},
		{"a send on the channel type beside main's", `import "a"

type Ch chan string

func send(c a.Ch) { c <- 5 }

func main() {
	_ = send
	println(1)
}
`, "1\n", false},
		{"a method's result beside main's type of the name", `import "a"

type Count string

func (c Count) Double() Count { return c }

func main() {
	var c a.Count = 4
	var x a.Count = c.Double()
	println(int(c.Double())+1, x, c.Double() == 8)
}
`, "9 8 true\n", false},
		{"an interface method's result beside main's type", `import "a"

type Count string

func main() {
	var s a.Shape = a.Qp()
	var n a.Count = s.Area()
	println(n*2, int(s.Area())+1)
}
`, "18 10\n", false},
		{"a method of a method's result", `import "a"

func main() {
	var t a.Temp = 9
	println(t.Half().Int(), t.Half().Half() > 2)
}
`, "4 true\n", false},
		{"a method a method's result lacks", `import "a"

func main() {
	var t a.Temp = 9
	println(t.Half().Nope())
}
`, "type a.Temp has no method Nope", true},
		{"a method's result into an int", `import "a"

func main() {
	var c a.Count = 4
	var w int = c.Double()
	println(w)
}
`, "cannot use c.Double() of type a.Count as type int in variable declaration", true},
		{"a method's result into main's type of the name", `import "a"

type Count int

func main() {
	var c a.Count = 4
	var w Count = c.Double()
	println(w)
}
`, "cannot use c.Double() of type a.Count as type Count in variable declaration", true},
		{"a string into an int type", `import "a"

func main() {
	var c a.Count = "x"
	println(c)
}
`, `cannot use "x" of type string as type Count in variable declaration`, true},
		{"a string into an int type beside main's string type", `import "a"

type Count string

func main() {
	var c a.Count = "x"
	println(c)
}
`, `cannot use "x" of type string as type Count in variable declaration`, true},
		{"an int into a string type", `import "a"

func main() {
	var n a.Name = 5
	println(n)
}
`, "cannot use 5 of type int as type Name in variable declaration", true},
		{"a string returned as an int type", `import "a"

func f() a.Count { return "x" }

func main() { println(f()) }
`, `cannot use "x" of type string as type Count in return statement`, true},
		{"a string argument for an int type", `import "a"

func f(c a.Count) {}

func main() { f("x") }
`, `cannot use "x" of type string as type Count in argument to f`, true},
		{"an int into a string type's field", `import "a"

type H struct{ n a.Name }

func main() {
	h := H{n: 3}
	println(h.n)
}
`, "cannot use 3 of type int as type Name in struct literal", true},
		{"an int type as a condition", `import "a"

func main() {
	var c a.Count = 3
	if c {
		println(1)
	}
}
`, "non-bool used as if condition", true},
		{"remainder of a float type", `import "a"

func main() {
	var t a.Temp = 3
	println(t % 2)
}
`, "operator % not defined on float32", true},
		{"a string sent on an int channel type", `import "a"

func send(c a.Ch) { c <- "x" }

func main() { _ = send }
`, `cannot use "x" of type string as type int in send`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo": &fstest.MapFile{Data: []byte(test.main)},
				"a/a.ogo":  &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if test.refuse {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("got %v, want an error containing %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			var buf bytes.Buffer
			if err := EmitC(pkg, &buf, Checked(), Inline()); err != nil {
				t.Fatalf("EmitC: %v", err)
			}
			if cc == "" {
				return
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
