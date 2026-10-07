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
		goName  string // the deepest goroutine's function
	}{
		{"no goroutine", listingOf(fnListing("_main", 1, 0, "call\t#_work"), fnListing("_work", 2, 8)), 0, true, ""},
		{
			// trampoline 4*(1+3), work 4*(2+3)+2400, leaf none: 2436 bytes, 609
			// longs, and the long the cog's entry takes.
			"frames add up",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_work"),
				fnListing("_work", 2, 2400, "call\t#_leaf"),
				fnListing("_leaf", -1, 0)),
			610, true, "work",
		},
		{
			"the deepest of two callees",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_leaf", " if_e\tcall\t#_work"),
				fnListing("_work", 2, 400),
				fnListing("_leaf", 1, 16)),
			1 + (16+420+3)/4, true, "work",
		},
		{
			"recursion through a direct call",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_work"),
				fnListing("_work", 2, 0, "call\t#_other"),
				fnListing("_other", 2, 0, "call\t#_work")),
			0, false, "",
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
			1 + (16+20+116+3)/4, true, "work",
		},
		{
			// A library cycle through a pointer: flush calls a handler, which
			// calls flush directly. The deepest simple path is taken.
			"a cycle through an indirect call",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#___flush"),
				fnListing("___flush", 1, 40, "call\tlocal02"),
				fnListing("___handler", 1, 60, "call\t#___flush"),
				"ptr_a\n\tlong\t@___handler\nptr_b\n\tlong\t@___flush"),
			1 + (16+56+76+3)/4, true, "__flush",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := readStackNeeds(tc.listing, c)
			if r.goLongs != tc.longs || r.goKnown != tc.known || r.goName != tc.goName {
				t.Fatalf("got %d longs, known %v, name %q; want %d, %v, %q\n%s", r.goLongs, r.goKnown, r.goName, tc.longs, tc.known, tc.goName, tc.listing)
			}
		})
	}
}

// TestStackNeedsInterfaceCalls reads a goroutine whose one call through a register
// is an interface call: it reaches the two functions its table's slot holds and not
// the deep taken function beside them, which a call through a function value, or a
// listing making more calls through a register than the C makes through tables,
// still reaches.
func TestStackNeedsInterfaceCalls(t *testing.T) {
	const decls = `#define ogo_iface_vt(v) (v)
struct I_vt { const char* _ogo_type; int (*ogo_m_M)(void*); };
typedef struct I { void* data; const I_vt* vt; } I;
typedef int (*fnT)(int ogo_p0);
static int I_A_M(void* p) { return 1; }
static int I_B_M(void* p) { return 2; }
static int big(int n) { return n; }
static const I_vt I_vt_A = { "*main.A", I_A_M };
static const I_vt I_vt_B = { "*main.B", I_B_M };
`
	viaTable := decls + `int use(I x) {
	return ((const I_vt*)ogo_iface_vt(x.vt))->ogo_m_M(x.data);
}
static void ogo_go0(void* p) {
	use(*(I*)p);
}
`
	viaValue := decls + `int use(I x) {
	fnT f = big;
	return ((const I_vt*)ogo_iface_vt(x.vt))->ogo_m_M(x.data) + f(1);
}
static void ogo_go0(void* p) {
	use(*(I*)p);
}
`
	listing := func(useIndirect int) []byte {
		body := []string{}
		for range useIndirect {
			body = append(body, "call\tlocal01")
		}
		return listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_use"),
			fnListing("_use", 2, 0, body...),
			fnListing("_I_A_M_0002", 1, 40),
			fnListing("_I_B_M_0003", 1, 80),
			fnListing("_big_0004", 1, 4000),
			"ptr_a\n\tlong\t@_I_A_M_0002\nptr_b\n\tlong\t@_I_B_M_0003\nptr_c\n\tlong\t@_big_0004")
	}
	narrow := 1 + (16+20+96+3)/4 // trampoline, use, I_B_M
	wide := 1 + (16+20+4016+3)/4 // trampoline, use, big
	for _, tc := range []struct {
		name  string
		c     string
		calls int
		longs int
	}{
		{"an interface call reaches its slot", viaTable, 1, narrow},
		{"a call through a function value reaches every taken function", viaValue, 2, wide},
		{"a listing the C does not account for", viaTable, 2, wide},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := readStackNeeds(listing(tc.calls), []byte(tc.c)); r.goLongs != tc.longs || !r.goKnown {
				t.Fatalf("got %d longs, known %v; want %d", r.goLongs, r.goKnown, tc.longs)
			}
		})
	}
}

// TestBuildSizesGoroutineStacks builds a program whose goroutine holds 2400 bytes
// of locals, which a slot of the default 256 longs cannot: the build compiles it
// with slots that fit, read off its listing, unless --gostack asked for a size. A
// goroutine needing more than the largest slot, and a main cog holding more than
// Hub RAM, are told they do not fit.
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
	t.Run("past the largest slot", func(t *testing.T) {
		// Slots of the largest size would leave this goroutine 2,000 longs short,
		// in silence; the build says so instead, and --gostack is the way past.
		big := strings.Replace(deep, "[600]int", "[10000]int", 1)
		_, err := compile(t, big, false)
		if err == nil || !strings.Contains(err.Error(), "goroutine work needs a stack of") {
			t.Fatalf("err=%v, want goroutine work's stack not fitting a slot", err)
		}
		if _, err := compile(t, big, true); err != nil {
			t.Fatalf("with the stack asked for: %v", err)
		}
	})
	t.Run("main", func(t *testing.T) {
		_, err := compile(t, "func main() {\n\tvar big [140000]int\n\tbig[1] = 2\n\tfor i := range big {\n\t\tbig[i] += i\n\t}\n\tprintln(big[1] + big[139999])\n}\n", false)
		if err == nil || !strings.Contains(err.Error(), "main's stack needs") {
			t.Fatalf("err=%v, want main's stack not fitting", err)
		}
	})
}
