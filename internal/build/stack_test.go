// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"modernc.org/ogo/internal/octogo"
)

// listingOf writes a listing of functions in the backend's shape: each a label, a
// body and a _ret label, a frame where count is not negative.
func listingOf(fns ...string) []byte { return []byte(strings.Join(fns, "\n") + "\n") }

func fnListing(name string, count, reserve int, body ...string) string {
	var b strings.Builder
	b.WriteString(name + "\n")
	if count >= 0 {
		b.WriteString("\tmov\tCOUNT_, #" + strconv.Itoa(count) + "\n\tcall\t#pushregs_\n")
	}
	if reserve > 0 {
		b.WriteString("\tadd\tptra, ##" + strconv.Itoa(reserve) + "\n")
	}
	for _, l := range body {
		b.WriteString("\t" + l + "\n")
	}
	if count >= 0 {
		b.WriteString("\tmov\tptra, fp\n\tcall\t#popregs_\n")
	}
	b.WriteString(name + "_ret\n\tret\n")
	return b.String()
}

// TestStackNeeds reads stacks off listings in the backend's shape: frames summed
// along the deepest path, recursion through direct calls left unbounded, an
// indirect call kept to its own side, and a cycle through one taken as its deepest
// simple path.
func TestStackNeeds(t *testing.T) {
	c := []byte("static void ogo_go0(void* p) {\nint work(int n) {\nint leaf(int n) { return n; }\nint other(int n) {\n")
	for _, tc := range []struct {
		name    string
		listing []byte
		longs   int
		known   bool
	}{
		{"no goroutine", listingOf(fnListing("_main", 1, 0, "call\t#_work"), fnListing("_work", 2, 8)), 0, true},
		{
			// trampoline 4*(1+3), work 4*(2+3)+2400, leaf none: 2436 bytes, 609
			// longs, and the long the cog's entry takes.
			"frames add up",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_work"),
				fnListing("_work", 2, 2400, "call\t#_leaf"),
				fnListing("_leaf", -1, 0)),
			610, true,
		},
		{
			"the deepest of two callees",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_leaf", " if_e\tcall\t#_work"),
				fnListing("_work", 2, 400),
				fnListing("_leaf", 1, 16)),
			1 + (16+420+3)/4, true,
		},
		{
			"recursion through a direct call",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_work"),
				fnListing("_work", 2, 0, "call\t#_other"),
				fnListing("_other", 2, 0, "call\t#_work")),
			0, false,
		},
		{
			// The program's call through a register reaches the program's taken
			// functions, not the library's 4000-byte handler.
			"an indirect call stays on its side",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_work"),
				fnListing("_work", 2, 0, "call\tlocal01"),
				fnListing("_other", 1, 100),
				fnListing("__system___handler", 1, 4000),
				"ptr_a\n\tlong\t@_other\nptr_b\n\tlong\t@__system___handler"),
			1 + (16+20+116+3)/4, true,
		},
		{
			// A library cycle through a pointer: flush calls a handler, which
			// calls flush directly. The deepest simple path is taken.
			"a cycle through an indirect call",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#___flush"),
				fnListing("___flush", 1, 40, "call\tlocal02"),
				fnListing("___handler", 1, 60, "call\t#___flush"),
				"ptr_a\n\tlong\t@___handler\nptr_b\n\tlong\t@___flush"),
			1 + (16+56+76+3)/4, true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := readStackNeeds(tc.listing, c)
			if r.goLongs != tc.longs || r.goKnown != tc.known {
				t.Fatalf("got %d longs, known %v; want %d, %v\n%s", r.goLongs, r.goKnown, tc.longs, tc.known, tc.listing)
			}
		})
	}
}

// TestBuildSizesGoroutineStacks builds a program whose goroutine holds 2400 bytes
// of locals, which a slot of the default 256 longs cannot: the build compiles it
// with slots that fit, read off its listing, unless --gostack asked for a size. And
// a main cog holding more than Hub RAM is told it does not fit.
func TestBuildSizesGoroutineStacks(t *testing.T) {
	const deep = `var out chan int

func work(n int) {
	var buf [600]int
	for i := range buf {
		buf[i] = i * n
	}
	s := 0
	for _, v := range buf {
		s += v
	}
	out <- s
}

func main() {
	go work(2)
	println(<-out)
}
`
	slot := regexp.MustCompile(`(?m)^#define OGO_STACK_LONGS (\d+)$`)
	compile := func(t *testing.T, src string, stackAsked bool) (longs int, err error) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "main.ogo"), src)
		pkg, err := octogo.Build(-1, []string{"main.ogo"}, os.DirFS(dir))
		if err != nil {
			t.Fatal(err)
		}
		var c, said bytes.Buffer
		if err := octogo.EmitC(pkg, &c, octogo.Checked(), octogo.Inline()); err != nil {
			t.Fatal(err)
		}
		cFile := filepath.Join(dir, "prog.c")
		unmarked := func() ([]byte, error) { return nil, errors.New("not expected to outgrow the cog") }
		if _, err := compileSized(c.Bytes(), unmarked, cFile, filepath.Join(dir, "prog.binary"), "", filepath.Join(dir, "kept.c"), false, stackAsked, &said, &said); err != nil {
			return 0, err
		}
		built, rerr := os.ReadFile(cFile)
		if rerr != nil {
			t.Fatal(rerr)
		}
		m := slot.FindSubmatch(built)
		if m == nil {
			t.Fatalf("no slot size in the C compiled")
		}
		longs, _ = strconv.Atoi(string(m[1]))
		return longs, nil
	}
	_, _, def := octogo.GoStackRange()
	t.Run("sized", func(t *testing.T) {
		longs, err := compile(t, deep, false)
		if err != nil {
			t.Fatal(err)
		}
		if longs < 600+16 || longs%16 != 0 {
			t.Fatalf("slots of %d longs, want at least the 616 the goroutine's locals and frames take, in sixteens", longs)
		}
	})
	t.Run("asked", func(t *testing.T) {
		longs, err := compile(t, deep, true)
		if err != nil {
			t.Fatal(err)
		}
		if longs != def {
			t.Fatalf("slots of %d longs where the stack was asked for, want the %d the C was emitted with", longs, def)
		}
	})
	t.Run("main", func(t *testing.T) {
		_, err := compile(t, "func main() {\n\tvar big [140000]int\n\tbig[1] = 2\n\tfor i := range big {\n\t\tbig[i] += i\n\t}\n\tprintln(big[1] + big[139999])\n}\n", false)
		if err == nil || !strings.Contains(err.Error(), "main's stack needs") {
			t.Fatalf("err=%v, want main's stack not fitting", err)
		}
	})
}
