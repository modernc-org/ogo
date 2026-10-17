// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// TestReport pins what EmitC tells `ogo build --report` of a program: the Go name of
// each function the C defines, every object the C declares at file scope and the
// package variable it is or whose value it holds, each go statement, and the locks.
func TestReport(t *testing.T) {
	const src = `import "lib"

type T struct{ n int }

func (t *T) bump() { t.n++ }

var out = make(chan int, 2)
var table [100]uint32
var tbl = []int{1, 2, 3}
var pp = &T{4}

func work(n int) { out <- n }

func main() {
	var t T
	t.bump()
	go work(1)
	go func(n int) { out <- n }(2)
	println(<-out, <-out, table[3], tbl[1], pp.n, t.n, lib.F(), lib.V)
}
`
	const lib = "var V = 7\n\nfunc F() int {\n\treturn V + 1\n}\n"
	fsys := fstest.MapFS{
		"main.ogo":    &fstest.MapFile{Data: []byte(src)},
		"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
	}
	pkg, err := Build(-1, []string{"main.ogo"}, fsys)
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	var c bytes.Buffer
	if err := EmitC(pkg, &c, Checked(), WithReport(&r)); err != nil {
		t.Fatal(err)
	}
	funcs := map[string]bool{}
	for _, n := range r.Funcs {
		funcs[n] = true
	}
	for _, want := range []string{"main", "work", "(*T).bump", "lib.F", "the function literal at main.ogo:18"} {
		if !funcs[want] {
			t.Errorf("no function %q in %v", want, r.Funcs)
		}
	}
	vars := map[string][]string{} // a variable's objects
	types := map[string]string{}
	for _, s := range r.Storage {
		if s.Var != "" {
			vars[s.Var] = append(vars[s.Var], s.C)
		}
		if s.Type != "" {
			types[s.Var] = s.Type
		}
	}
	for v, want := range map[string]string{"out": "chan int", "table": "[100]uint32", "tbl": "[]int", "pp": "*T", "lib.V": "int"} {
		if types[v] != want {
			t.Errorf("variable %s of type %q, want %q", v, types[v], want)
		}
	}
	// Each variable holds what its value is made of: the channel its cell, buffer
	// and lock, the slice its backing, the pointer what &T{4} made.
	has := func(v, prefix string) bool {
		for _, c := range vars[v] {
			if strings.HasPrefix(c, prefix) {
				return true
			}
		}
		return false
	}
	for _, x := range [][2]string{{"out", "ogo_chan_cell_"}, {"out", "ogo_chan_buf_"}, {"out", "ogo_chan_lock"}, {"tbl", "ogo_backing_"}, {"pp", "ogo_plit_"}} {
		if !has(x[0], x[1]) {
			t.Errorf("%s holds no %s*: %v", x[0], x[1], vars[x[0]])
		}
	}
	var gos []string
	for _, g := range r.Goroutines {
		gos = append(gos, g)
	}
	slices.Sort(gos)
	if got := strings.Join(gos, "; "); got != "main.ogo:17 work; main.ogo:18 a function literal" {
		t.Errorf("go statements %q", got)
	}
	if r.ChanSites != 1 || r.ChanLocks != 1 || r.NewLocks != 0 {
		t.Errorf("channel sites %d, their locks %d, p2.NewLock calls %d; want 1, 1, 0", r.ChanSites, r.ChanLocks, r.NewLocks)
	}
}

// TestSizeProbe: the table SizeProbe writes before main is the one ProbedSizes
// reads back out of a binary, behind its mark.
func TestSizeProbe(t *testing.T) {
	c := []byte("static int a;\nint main(void) {\n\treturn 0;\n}\n")
	p := string(SizeProbe(c, []string{"a", "b"}))
	for _, want := range []string{
		"static const unsigned ogo_report_sizes[] = {0x6f676f72u, 0x657a6973u, 2, sizeof(a), sizeof(b)};\n",
		"int main(void) {\n\togo_report_keep = ogo_report_sizes[2];\n\treturn 0;\n",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q in\n%s", want, p)
		}
	}
	if SizeProbe([]byte("static int a;\n"), []string{"a"}) != nil {
		t.Errorf("a probe without a main to keep it")
	}
	bin := []byte{9, 9, 0x72, 0x6f, 0x67, 0x6f, 0x73, 0x69, 0x7a, 0x65, 2, 0, 0, 0, 48, 0, 0, 0, 13, 0, 0, 0, 7}
	if got := ProbedSizes(bin); len(got) != 2 || got[0] != 48 || got[1] != 13 {
		t.Errorf("ProbedSizes: %v, want [48 13]", got)
	}
	if got := ProbedSizes(bin[:16]); got != nil {
		t.Errorf("ProbedSizes of a cut table: %v", got)
	}
}
