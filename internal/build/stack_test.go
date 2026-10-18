// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"os"
	"path/filepath"
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
// indirect call kept to its own side, a cycle through one taken as its deepest
// simple path, and such a cycle holding a recursion left unbounded.
func TestStackNeeds(t *testing.T) {
	c := []byte("static void ogo_go0(void* p) {\nint work(int n) {\nint leaf(int n) { return n; }\nint other(int n) {\n")
	for _, tc := range []struct {
		name    string
		listing []byte
		longs   int
		known   bool
		goName  string // the deepest goroutine's function, or the one recursing
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
			0, false, "work",
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
		{
			// flush calls a helper that also calls itself, and the helper calls
			// through a pointer back to flush: flush reaches the recursion by a
			// call it makes, cycle or not.
			"a recursion in a cycle through an indirect call",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#___flush"),
				fnListing("___flush", 1, 40, "call\t#___helper"),
				fnListing("___helper", 1, 60, "call\tlocal02", "if_z\tcall\t#___helper"),
				"ptr_a\n\tlong\t@___helper\nptr_b\n\tlong\t@___flush"),
			0, false, "__helper",
		},
		{
			// The same recursion, reached only through the pointer: a call
			// through a pointer is taken to reach every function whose address
			// is taken, most of them never there, and its cycle is walked once.
			"a recursion only a cycle's indirect call reaches",
			listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#___flush"),
				fnListing("___flush", 1, 40, "call\tlocal02"),
				fnListing("___handler", 1, 60, "call\t#___flush", "if_z\tcall\t#___handler"),
				"ptr_a\n\tlong\t@___handler\nptr_b\n\tlong\t@___flush"),
			1 + (16+56+76+3)/4, true, "__flush",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := readStackNeeds(tc.listing, c)
			name := r.goName
			if !r.goKnown {
				name = r.goRecurse
			}
			if r.goLongs != tc.longs || r.goKnown != tc.known || name != tc.goName {
				t.Fatalf("got %d longs, known %v, name %q; want %d, %v, %q\n%s", r.goLongs, r.goKnown, name, tc.longs, tc.known, tc.goName, tc.listing)
			}
		})
	}
}

// TestStackNeedsInterfaceCalls reads a goroutine whose one call through a register
// is an interface call: it reaches the two functions its table's slot holds and not
// the deep taken function beside them, which a call through a function value, or a
// listing making more calls through a register than the C makes through tables,
// still reaches. A method its slot holds calling the interface again recurses, as a
// direct call of itself would: the depth is the values', which no listing has.
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
	t.Run("a method calling its interface recurses", func(t *testing.T) {
		c := strings.Replace(viaTable, "static int I_B_M(void* p) { return 2; }",
			"static int I_B_M(void* p) { I x = *(I*)p; return ((const I_vt*)ogo_iface_vt(x.vt))->ogo_m_M(x.data); }", 1)
		l := listingOf(fnListing("_ogo_go0_0001", 1, 0, "call\t#_use"),
			fnListing("_use", 2, 0, "call\tlocal01"),
			fnListing("_I_A_M_0002", 1, 40),
			fnListing("_I_B_M_0003", 1, 80, "call\tlocal02"),
			fnListing("_big_0004", 1, 4000),
			"ptr_a\n\tlong\t@_I_A_M_0002\nptr_b\n\tlong\t@_I_B_M_0003\nptr_c\n\tlong\t@_big_0004")
		if r := readStackNeeds(l, []byte(c)); r.goKnown || r.goRecurse != "I_B_M" {
			t.Fatalf("known %v, recursing %q; want unknown, through I_B_M", r.goKnown, r.goRecurse)
		}
	})
}

// TestBuildSizesGoroutineStacks builds a program whose goroutine holds 2400 bytes
// of locals, which a slot of the default 256 longs cannot: the build compiles it
// with slots that fit, read off its listing, unless --gostack asked for a size. A
// slot is what its goroutines need, a go statement's own slot what its goroutine
// does and every slot of the shared pool what the deepest does. A goroutine
// needing more than the largest slot, and a main cog holding more than Hub RAM,
// are told they do not fit.
func TestBuildSizesGoroutineStacks(t *testing.T) {
	const deep = `var out = make(chan int)

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
	var built []byte // the C compiles built last
	compiles := func(t *testing.T, src string, stackAsked bool) (slots []octogo.GoSlot, err error) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "main.ogo"), src)
		emit := func(extra ...octogo.EmitOption) ([]byte, []func() ([]byte, error), error) {
			pkg, err := octogo.Build(-1, []string{"main.ogo"}, os.DirFS(dir))
			if err != nil {
				t.Fatal(err)
			}
			var c bytes.Buffer
			if err := octogo.EmitC(pkg, &c, append([]octogo.EmitOption{octogo.Checked(), octogo.Inline(), octogo.FuncValueClasses()}, extra...)...); err != nil {
				t.Fatal(err)
			}
			return c.Bytes(), nil, nil
		}
		c, _, _ := emit()
		var said bytes.Buffer
		cFile := filepath.Join(dir, "prog.c")
		if _, err := compileSized(c, nil, emit, cFile, filepath.Join(dir, "prog.binary"), "", filepath.Join(dir, "kept.c"), false, stackAsked, &said, &said); err != nil {
			return nil, err
		}
		var rerr error
		if built, rerr = os.ReadFile(cFile); rerr != nil {
			t.Fatal(rerr)
		}
		return octogo.GoSlots(built), nil // none for a program starting no goroutine
	}
	compile := func(t *testing.T, src string, stackAsked bool) (longs int, err error) {
		slots, err := compiles(t, src, stackAsked)
		if err != nil || len(slots) == 0 {
			return 0, err
		}
		return slots[0].Longs, nil
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
	t.Run("each its own", func(t *testing.T) {
		// Two go statements run once each: a slot of its own for each, the
		// shallow goroutine's what it needs and no more.
		two := strings.Replace(deep, "func main() {\n\tgo work(2)\n\tprintln(<-out)", "func shallow() {\n\tout <- 1\n}\n\nfunc main() {\n\tgo work(2)\n\tgo shallow()\n\tprintln(<-out + <-out)", 1)
		slots, err := compiles(t, two, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(slots) != 2 || slots[0].Longs < 600+16 || slots[1].Longs >= def || slots[1].Longs < 16 || slots[1].Longs%16 != 0 {
			t.Fatalf("slots %v, want work's of at least 616 longs and shallow's of less than %d, in sixteens", slots, def)
		}
		// Started in a loop, any goroutine may run on any slot: every slot the
		// deepest's size.
		loop := strings.Replace(two, "\tgo work(2)\n\tgo shallow()\n", "\tfor i := 0; i < 2; i++ {\n\t\tgo work(i)\n\t}\n\tgo shallow()\n", 1)
		loop = strings.Replace(loop, "<-out + <-out", "<-out + <-out + <-out", 1)
		if slots, err = compiles(t, loop, false); err != nil {
			t.Fatal(err)
		}
		if len(slots) != 1 || slots[0].Tramps != nil || slots[0].Longs < 600+16 {
			t.Fatalf("slots %v, want the shared pool's, of at least 616 longs", slots)
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
	// checked asks the C built last for a stack check in each of fns, its margin
	// and the reserve written, against each cog's end of stack or main's alone.
	checked := func(t *testing.T, perCog bool, fns ...string) {
		t.Helper()
		margins, reserve := octogo.StackMargins(built)
		for _, fn := range fns {
			if !strings.Contains(string(built), "OGO_STACK_CHECK(OGO_STACK_MARGIN_"+fn+");") || margins[fn] <= 0 {
				t.Errorf("no stack check of %d bytes in %s: margins %v", margins[fn], fn, margins)
			}
		}
		if len(margins) != len(fns) || reserve <= 0 {
			t.Errorf("margins %v, reserve %d; want one of %v each and a reserve", margins, reserve, fns)
		}
		if got := strings.Contains(string(built), "static unsigned ogo_stack_limit[8]"); got != perCog {
			t.Errorf("each cog's end of stack kept: %v, want %v", got, perCog)
		}
	}
	t.Run("recursion", func(t *testing.T) {
		// No listing bounds a recursion's depth, and the default slot was a guess
		// the fence checked only as the goroutine ended; --gostack is the size
		// the program chooses, and a check at the entry of each function on the
		// recursion stops it before it writes past the slot.
		rec := `var out = make(chan int)

func depth(n int) int {
	if n == 0 {
		return 0
	}
	return 1 + depth(n-1)
}

func work(n int) {
	out <- depth(n)
}

func main() {
	go work(5)
	println(<-out)
}
`
		_, err := compile(t, rec, false)
		if err == nil || !strings.Contains(err.Error(), "a goroutine recurses through depth") {
			t.Fatalf("err=%v, want the goroutine's recursion through depth refused", err)
		}
		if _, err := compile(t, rec, true); err != nil {
			t.Fatalf("with the stack asked for: %v", err)
		}
		checked(t, true, "depth")
		// Through an interface's table, as an expression tree is evaluated: 200
		// levels ran past the slot sized for one and hung the board in silence.
		tree := `var out = make(chan int)

type Expr interface{ Eval() int }

type Num struct{ v int }

func (n *Num) Eval() int { return n.v }

type Add struct{ l, r Expr }

func (a *Add) Eval() int { return a.l.Eval() + a.r.Eval() }

var nodes [200]Add
var leaf = Num{1}

func work(n int) {
	var e Expr = &leaf
	for i := 0; i < n; i++ {
		nodes[i].l = e
		nodes[i].r = &leaf
		e = &nodes[i]
	}
	out <- e.Eval()
}

func main() {
	go work(200)
	println(<-out)
}
`
		_, err = compile(t, tree, false)
		if err == nil || !strings.Contains(err.Error(), "a goroutine recurses through Add_Eval") {
			t.Fatalf("err=%v, want the goroutine's recursion through Add_Eval refused", err)
		}
		if _, err := compile(t, tree, true); err != nil {
			t.Fatalf("with the stack asked for: %v", err)
		}
		checked(t, true, "Add_Eval")
		// A recursion on main's cog is main's stack, the rest of Hub RAM, checked
		// against its end.
		if _, err := compile(t, strings.Replace(rec, "go work(5)\n\tprintln(<-out)", "println(depth(5))", 1), false); err != nil {
			t.Fatalf("recursion on main's cog: %v", err)
		}
		checked(t, false, "depth")
		// Through a call of a function value, what the value's type holds: such a
		// call was taken to reach every function the program takes the address of,
		// a cycle through it walked once, and the recursion went unseen on either
		// cog.
		value := `var out = make(chan int)

type visit func(n int) int

var v visit

func walk(n int) int {
	if n == 0 {
		return 0
	}
	return 1 + v(n-1)
}

func work(n int) {
	out <- walk(n)
}

func init() { v = walk }

func main() {
	go work(200)
	println(<-out)
}
`
		_, err = compile(t, value, false)
		if err == nil || !strings.Contains(err.Error(), "a goroutine recurses through walk") {
			t.Fatalf("err=%v, want the goroutine's recursion through walk refused", err)
		}
		if _, err := compile(t, value, true); err != nil {
			t.Fatalf("with the stack asked for: %v", err)
		}
		checked(t, true, "walk")
		if _, err := compile(t, strings.Replace(value, "go work(200)\n\tprintln(<-out)", "println(walk(5))", 1), false); err != nil {
			t.Fatalf("recursion through a value on main's cog: %v", err)
		}
		checked(t, false, "walk")
		// And two tables of one signature, obeam's handlers and BIFs, each of its
		// own defined type, reach only their own functions: a handler calling a
		// BIF closes no cycle, where every taken function would have.
		tables := `var out = make(chan int)

type handler func(n int) int

type bif func(n int) int

func b1(n int) int { return n * 2 }

var bifs = [1]bif{b1}

func hAdd(n int) int { return n + 1 }

func hCall(n int) int { return bifs[0](n) }

var handlers = [2]handler{hAdd, hCall}

func work(n int) {
	s := 0
	for op := 0; op < 2; op++ {
		s += handlers[op](n)
	}
	out <- s
}

func main() {
	go work(3)
	println(<-out)
}
`
		if _, err := compile(t, tables, false); err != nil {
			t.Fatalf("two tables of one signature: %v", err)
		}
		if strings.Contains(string(built), "OGO_STACK_CHECK(") {
			t.Errorf("a stack check where nothing recurses")
		}
		// A goroutine calling through a value of one type, beside a recursion
		// through a table of another: the goroutine's call reaches every taken
		// function for its stack's size, and that reached the recursion, so it was
		// refused for one it never makes. And an evaluator whose operator calls
		// Eval twice, inlined into it, was read as making one call through the
		// table where its listing makes two, and so as no recursion.
		both := `var out = make(chan int32)

type job func(int32) int32

func square(x int32) int32 { return x * x }

var jobs = [1]job{square}

func work() { out <- jobs[0](3) }

type Node struct {
	op   int
	v    int32
	l, r *Node
}

type evalFn func(n *Node) int32

var ops [3]evalFn

func init() { ops = [3]evalFn{evNum, evAdd, evNeg} }

func Eval(n *Node) int32 { return ops[n.op](n) }

func evNum(n *Node) int32 { return n.v }

func evAdd(n *Node) int32 { return Eval(n.l) + Eval(n.r) }

func evNeg(n *Node) int32 { return -Eval(n.l) }

var a, b = Node{v: 2}, Node{v: 3}

var d = Node{op: 2, l: &b}

var c = Node{op: 1, l: &a, r: &d}

func main() {
	go work()
	println(<-out, Eval(&c))
}
`
		if _, err := compile(t, both, false); err != nil {
			t.Fatalf("a goroutine beside a recursion through a table: %v", err)
		}
		checked(t, false, "evAdd", "evNeg")
	})
	t.Run("main", func(t *testing.T) {
		_, err := compile(t, "func main() {\n\tvar big [140000]int\n\tbig[1] = 2\n\tfor i := range big {\n\t\tbig[i] += i\n\t}\n\tprintln(big[1] + big[139999])\n}\n", false)
		if err == nil || !strings.Contains(err.Error(), "main's stack needs") {
			t.Fatalf("err=%v, want main's stack not fitting", err)
		}
	})
	// A recursion beside the frame left main's need unbounded, and the frame was
	// asked nothing: the program built and printed nothing on the board. Its
	// recursion checked, what main needs is the way to the checks, the frame in it.
	t.Run("main with a recursion", func(t *testing.T) {
		_, err := compile(t, `func depth(n int) int {
	if n == 0 {
		return 0
	}
	return 1 + depth(n-1)
}

func big() int {
	var b [140000]int
	for i := range b {
		b[i] = i
	}
	return b[139999]
}

func main() { println(depth(3), big()) }
`, false)
		if err == nil || !strings.Contains(err.Error(), "main's stack needs 5") {
			t.Fatalf("err=%v, want main's stack not fitting", err)
		}
	})
}

// TestStackNeedsChecks reads the stack checks off a listing: without them, the
// recursion main reaches is the function to check; with them, each check's margin
// is its function's frame and what its calls take until the next check -- the
// panic left to the reserve, what it needs -- and main needs the way to its first
// check. A table's entry the method was inlined into is checked as the method, and
// a recursion through it names the method.
func TestStackNeedsChecks(t *testing.T) {
	const c = "int main(void) {\nint work(int n) {\nint helper(int n) {\nstatic void ogo_panic(const char* msg) {\n"
	l := listingOf(fnListing("_main", 1, 0, "call\t#_work"),
		fnListing("_work", 2, 0, "call\t#_work", "call\t#_helper", "if_a\tcall\t#_ogo_panic_0001"),
		fnListing("_helper", 1, 80),
		fnListing("_ogo_panic_0001", 3, 200))
	r := readStackNeeds(l, []byte(c))
	if len(r.margins) != 0 || r.mainCut >= 0 || strings.Join(r.recMain, ",") != "work" {
		t.Fatalf("unchecked: margins %v, main %d, to check %v; want none, unbounded, work", r.margins, r.mainCut, r.recMain)
	}
	checked := c + "#define OGO_STACK_MARGIN_work 0\n#define OGO_STACK_RESERVE 0\n"
	r = readStackNeeds(l, []byte(checked))
	work, helper, main, panicNeed := 4*(2+3), 4*(1+3)+80, 4*(1+3), 4*(3+3)+200
	if r.margins["work"] != work+helper || r.reserve != panicNeed || r.mainCut != main+work || len(r.recMain) != 0 {
		t.Fatalf("checked: margins %v, reserve %d, main %d, to check %v; want work's %d, %d, %d, none",
			r.margins, r.reserve, r.mainCut, r.recMain, work+helper, panicNeed, main+work)
	}

	const decls = `#define ogo_iface_vt(v) (v)
struct I_vt { const char* _ogo_type; int (*ogo_m_M)(void*); };
typedef struct I { void* data; const I_vt* vt; } I;
int A_M(A* a) { I x = a->next; return ((const I_vt*)ogo_iface_vt(x.vt))->ogo_m_M(x.data); }
static int I_A_M(void* p) { return A_M((A*)p); }
static const I_vt I_vt_A = { "*main.A", I_A_M };
int main(void) {
`
	tl := listingOf(fnListing("_main", 1, 0, "call\t#_I_A_M_0002"),
		fnListing("_I_A_M_0002", 2, 0, "call\tlocal01"),
		"ptr_a\n\tlong\t@_I_A_M_0002")
	if r := readStackNeeds(tl, []byte(decls)); strings.Join(r.recMain, ",") != "A_M" {
		t.Fatalf("through a thunk: to check %v, want A_M", r.recMain)
	}
	if r := readStackNeeds(tl, []byte(decls+"#define OGO_STACK_MARGIN_A_M 0\n")); len(r.recMain) != 0 || r.margins["A_M"] != 2*4*(2+3) {
		t.Fatalf("through a checked thunk: margins %v, to check %v; want A_M's %d, none", r.margins, r.recMain, 2*4*(2+3))
	}
}

// TestStackNeedsMainAtLeast reads what main's stack needs at the least where a
// recursion leaves its need unbounded: the deepest chain of direct calls entering
// each component once, the recursive one included, and no call through a register.
func TestStackNeedsMainAtLeast(t *testing.T) {
	c := []byte("int main(void) {\nint first(void) {\nint rec(int n) {\nint other(int n) {\n")
	listing := listingOf(fnListing("_main", 1, 0, "call\t#_rec", "call\t#_first", "call\tlocal01"),
		fnListing("_first", 1, 600000),
		fnListing("_rec", 1, 40, "call\t#_rec", "call\t#_leaf"),
		fnListing("_leaf", 1, 8),
		fnListing("_other", 1, 900000),
		"ptr_a\n\tlong\t@_other")
	r := readStackNeeds(listing, c)
	// main 4*(1+3), first 4*(1+3)+600000; other is reached through a register only.
	if want := 16 + 16 + 600000; r.mainNeed != -1 || r.mainAtLeast != want {
		t.Fatalf("main needs %d, at least %d; want -1, %d\n%s", r.mainNeed, r.mainAtLeast, want, listing)
	}
}

// TestStackNeedsWrappedFrame reads a frame past 2 GB, which the backend's 32-bit
// arithmetic writes negative, `add ptra, ##-1073741808` for three locals of a
// gigabyte: it was skipped, the pattern taking digits only, and the frame counted as
// nothing.
func TestStackNeedsWrappedFrame(t *testing.T) {
	c := []byte("int main(void) {\n")
	listing := listingOf(fnListing("_main", 1, 0, "add\tptra, ##-1073741808"))
	if r := readStackNeeds(listing, c); r.mainNeed <= 512<<10 {
		t.Fatalf("main needs %d bytes; want more than Hub RAM\n%s", r.mainNeed, listing)
	}
}
