// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

// TestScanCalls pins what ScanCalls reads of a small program's C: the interface
// calls with the table type they name, the tables' slots, a call through a function
// value counted as Other, and a call by name as Direct.
func TestScanCalls(t *testing.T) {
	src := `type Shape interface {
	Area() int
	Name() string
}

type Sq struct{ s int }

func (q *Sq) Area() int     { return q.s * q.s }
func (q *Sq) Name() string { return "sq" }

type Tri struct{ b, h int }

func (r *Tri) Area() int     { return r.b * r.h / 2 }
func (r *Tri) Name() string { return "tri" }

func total(ss []Shape) int {
	n := 0
	for _, s := range ss {
		n += s.Area()
	}
	return n
}

func apply(f func(int) int, v int) int { return f(v) }

func double(v int) int { return 2 * v }

func main() {
	ss := []Shape{&Sq{2}, &Tri{3, 4}}
	println(total(ss), apply(double, 3))
}
`
	c := emitForScan(t, src)
	scan := ScanCalls(c)
	tot := scan.Funcs["total"]
	if tot == nil || len(tot.Iface) != 1 || tot.Other != 0 || tot.Iface[0].Member != "ogo_m_Area" {
		t.Fatalf("total: %+v\n%s", tot, c)
	}
	fns, ok := scan.Targets(tot.Iface[0])
	slices.Sort(fns)
	if !ok || len(fns) != 2 {
		t.Fatalf("targets of %+v: %v, %v", tot.Iface[0], fns, ok)
	}
	if ap := scan.Funcs["apply"]; ap == nil || ap.Other != 1 || len(ap.Iface) != 0 {
		t.Fatalf("apply: %+v", ap)
	}
	if m := scan.Funcs["main"]; m == nil || !slices.Contains(m.Direct, "total") || !slices.Contains(m.Direct, "apply") {
		t.Fatalf("main: %+v", m)
	}
	if _, ok := scan.Targets(IfaceSite{Member: "ogo_m_Missing"}); ok {
		t.Fatal("a member no table holds answered targets")
	}
}

func emitForScan(t *testing.T, src string) []byte {
	t.Helper()
	fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
	pkg, err := Build(-1, []string{"main.ogo"}, fsys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var buf bytes.Buffer
	if err := EmitC(pkg, &buf, Checked(), Inline()); err != nil {
		t.Fatalf("EmitC: %v", err)
	}
	return buf.Bytes()
}

// TestScanCallsAgainstGCC holds ScanCalls to gcc's count of the indirect calls of
// each function of every run case's C: compiled at -O0, where nothing is inlined, a
// call through a pointer is one indirect call instruction and a call by name is
// not. The scanner may count more -- an Other it cannot prove by name costs only
// precision -- and never fewer: a call through a pointer it took for a call by name
// would leave a goroutine's slot short of the stack it needs, in silence.
func TestScanCallsAgainstGCC(t *testing.T) {
	var icall *regexp.Regexp
	switch runtime.GOARCH {
	case "amd64":
		icall = regexp.MustCompile(`^\s+call\s+\*`)
	case "arm64":
		icall = regexp.MustCompile(`^\s+blr\s+`)
	default:
		t.Skipf("no reading of gcc's %s assembly", runtime.GOARCH)
	}
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}
	label := regexp.MustCompile(`^([A-Za-z_]\w*):`)
	var funcs, exact atomic.Int64
	t.Run("cases", func(t *testing.T) {
		for _, test := range emitRunCases {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				c := emitForScan(t, test.src)
				dir := t.TempDir()
				csrc, asm := filepath.Join(dir, "main.c"), filepath.Join(dir, "main.s")
				if err := os.WriteFile(csrc, c, 0o644); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command(gcc, "-std=gnu11", "-fwrapv", "-O0", "-w", "-S", "-I", shim, "-o", asm, csrc).CombinedOutput(); err != nil {
					t.Fatalf("gcc: %v\n%s", err, out)
				}
				b, err := os.ReadFile(asm)
				if err != nil {
					t.Fatal(err)
				}
				count := map[string]int{}
				cur := ""
				for _, line := range bytes.Split(b, []byte("\n")) {
					if m := label.FindSubmatch(line); m != nil {
						cur = string(m[1])
						continue
					}
					if cur != "" && icall.Match(line) {
						count[cur]++
					}
				}
				scan := ScanCalls(c)
				for fn, n := range count {
					if !bytes.Contains(c, []byte(fn+"(")) {
						continue // the host shim's, from its header
					}
					fc := scan.Funcs[fn]
					if fc == nil {
						// Not read as a function: at build time it is given every
						// taken function, which is safe, but every function the
						// emitter writes is meant to be read.
						t.Errorf("%s: %d indirect calls by gcc, and no function of the scan", fn, n)
						continue
					}
					funcs.Add(1)
					switch have := len(fc.Iface) + fc.Other; {
					case have < n:
						t.Errorf("%s: %d indirect calls by gcc, %d by the scan (%+v)", fn, n, have, fc)
					case have == n:
						exact.Add(1)
					}
				}
			})
		}
	})
	t.Logf("%d functions with indirect calls, %d counted exactly", funcs.Load(), exact.Load())
}
