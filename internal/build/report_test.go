// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"modernc.org/ogo/internal/octogo"
)

// TestReadImage reads a listing of addresses as the backend writes one: the parts
// of the binary between the labels every binary has, and each hub function from its
// label to the next one's, the last to the string literals after it.
func TestReadImage(t *testing.T) {
	const lst = `00000                 | dat
00404 000             | entry
00940                 | hubentry
00940                 | _f
00948                 | _f_ret
0094c                 | _g_0012
00950                 | LR__0001
00958                 | _g_0012_ret
00960                 | LR__0002
00960     61 62 00    | 	byte	"ab"
00963     00          | 	byte	0
00964                 | _dat_
00a00                 | _drv_spin2_dat_
00b00                 | __system__dat_
00b20                 | __methodtable__
00b28                 | stackspace
`
	r, ok := readImage([]byte(lst), 0xb2c)
	if !ok {
		t.Fatal("not read")
	}
	want := image{size: 0xb2c, stack: 0xb28, cog: 0x940 - 0x404, code: 0x960 - 0x940, strs: 4, data: 0xa00 - 0x964,
		objs: []imageFunc{{"drv.spin2", 0x100}}, libData: 0x20, tables: 0xb2c - 0xb20,
		funcs: []imageFunc{{"_f", 0xc}, {"_g_0012", 0x14}}}
	if got := r; !imagesEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
	if _, ok := readImage([]byte("00940                 | hubentry\n"), 100); ok {
		t.Fatal("a listing without its parts read")
	}
}

func imagesEqual(a, b image) bool {
	if a.size != b.size || a.stack != b.stack || a.cog != b.cog || a.code != b.code || a.strs != b.strs || a.data != b.data ||
		a.libData != b.libData || a.tables != b.tables || len(a.objs) != len(b.objs) || len(a.funcs) != len(b.funcs) {
		return false
	}
	for i := range a.objs {
		if a.objs[i] != b.objs[i] {
			return false
		}
	}
	for i := range a.funcs {
		if a.funcs[i] != b.funcs[i] {
			return false
		}
	}
	return true
}

// TestBuildReport builds a program with --report and reads what it says: the
// binary's size as it is on disk, a go statement's slot and what it needs, the
// locks, a variable at the size the target gives it with what its value holds, and
// a function by its Go name.
func TestBuildReport(t *testing.T) {
	const src = `var out = make(chan int)
var table [100]uint32

type T struct{ n int }

func (t *T) bump() { t.n++ }

func work(n int) { out <- n }

func main() {
	var t T
	t.bump()
	go work(1)
	println(<-out, table[3], t.n)
}
`
	dir := t.TempDir()
	write(t, filepath.Join(dir, "prog", "main.ogo"), src)
	out := filepath.Join(dir, "prog.binary")
	var stdout, stderr bytes.Buffer
	if code, err := Build([]string{"--report", "-o", out, filepath.Join(dir, "prog")}, nil, &stdout, &stderr); err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s%s", code, err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	got := stdout.String()
	bin, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Hub RAM: " + strconv.Itoa(len(bin)) + " of 524288 bytes in the binary; main's stack needs ",
		"Goroutines: 1 go statement, run once, on a slot of its own\n",
		"Hardware locks: 2 of 16 the runtime takes: 1 the goroutine pool's, 1 of channels, for 1 make site\n",
		"      400  table [100]uint32\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// A go statement in a loop shares the pool, which the report says.
	loop := strings.Replace(src, "\tgo work(1)\n\tprintln(<-out,", "\tfor i := 0; i < 2; i++ {\n\t\tgo work(i)\n\t}\n\tprintln(<-out+<-out,", 1)
	write(t, filepath.Join(dir, "loop", "main.ogo"), loop)
	var shared bytes.Buffer
	if code, err := Build([]string{"--report", "-o", filepath.Join(dir, "loop.binary"), filepath.Join(dir, "loop")}, nil, &shared, &shared); err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, shared.String())
	}
	if re := `(?m)^Goroutines: 7 slots of \d+ longs, shared by 1 go statement; the deepest goroutine needs \d+, main\.ogo:14 work$`; !regexp.MustCompile(re).MatchString(shared.String()) {
		t.Errorf("no line matching %s in\n%s", re, shared.String())
	}
	for _, re := range []string{
		`(?m)^ +\d+ +\d+  main\.ogo:13 work$`,                  // the slot, what it needs, the go statement
		`(?m)^ +\d+  out chan int$`,                            // the channel with its cell and lock
		`(?m)^ +\d+  main$`,                                    // a function by its Go name
		`(?m)^ +\d+  _?\w+ \(the C library\)$`,                 // and the library's
		`(?m)^ +\d+  the goroutine slots: stacks, arguments, `, // what the runtime keeps for them
	} {
		if !regexp.MustCompile(re).MatchString(got) {
			t.Errorf("no line matching %s in\n%s", re, got)
		}
	}
}

// TestFuncLabel names functions as the report lists them: by their Go names, an
// interface's table entry by its method's, and the rest by whose they are.
func TestFuncLabel(t *testing.T) {
	c := []byte(`struct I_vt { const char* _ogo_type; int (*ogo_m_M)(void*); };
int T_M(T* t) { return t->n; }
static int I_T_M(void* p) { return T_M((T*)p); }
static const I_vt I_vt_T = { "*main.T", I_T_M };
int work(int n) { return n; }
`)
	rep := &octogo.Report{Funcs: map[string]string{"T_M": "(*T).M", "work": "work"}}
	scan := octogo.ScanCalls(c)
	for _, tc := range []struct{ cn, want string }{
		{"work", "work"},
		{"I_T_M", "(*T).M (its interface table's entry)"},
		{"led_spin2_blink", "led.spin2's blink"},
		{"ogo_panic", "ogo_panic (the runtime)"},
		{"_system___fmtnum", "_system___fmtnum (the C library)"},
	} {
		if got := funcLabel(tc.cn, rep, scan); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.cn, got, tc.want)
		}
	}
}

// TestLayoutListing: a binary's layout is read off its own assembly assembled again
// with a listing where that makes the same bytes, and off its C compiled with one
// where it does not, which the report then says is not the binary's.
func TestLayoutListing(t *testing.T) {
	dir := t.TempDir()
	cFile := filepath.Join(dir, "prog.c")
	write(t, cFile, "#include <stdio.h>\nint main(void) {\n\tprintf(\"%d\\n\", 42);\n\treturn 0;\n}\n")
	out := filepath.Join(dir, "prog.binary")
	var said bytes.Buffer
	if _, err := compileC(cFile, out, "", &said, &said); err != nil {
		t.Fatalf("%v\n%s", err, said.String())
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(filepath.Join(dir, "prog.p2asm"))
	if err != nil {
		t.Fatal(err)
	}
	lst, binary, exact, err := layoutListing(asm, built, cFile, "", t.TempDir())
	if err != nil || !exact || !bytes.Equal(binary, built) || !bytes.Contains(lst, []byte("| _main\n")) {
		t.Fatalf("its own assembly: exact %v, err %v, same binary %v", exact, err, bytes.Equal(binary, built))
	}
	lst, binary, exact, err = layoutListing([]byte("not an assembly\n"), built, cFile, "", t.TempDir())
	if err != nil || exact || len(binary) == 0 || !bytes.Contains(lst, []byte("| _main\n")) {
		t.Fatalf("an assembly that does not assemble: exact %v, err %v", exact, err)
	}
}
