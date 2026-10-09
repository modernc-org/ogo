// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"modernc.org/ogo/internal/octogo"
)

// TestResolvePackage covers how the command line names a package: a package is a
// directory, so no argument means the current one, a directory argument means that
// one, and explicit files must agree on a directory. It also pins the output
// naming, which differs between the single-named-file form and the rest.
func TestResolvePackage(t *testing.T) {
	dir := t.TempDir()
	for _, nm := range []string{"main.ogo", "alt.ogo", "helper_test.ogo", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, nm), []byte("// x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "other.ogo"), []byte("// x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	base := filepath.Base(dir)
	for _, test := range []struct {
		name  string
		srcs  []string
		files []string // expected base names, nil when an error is expected
		out   string   // expected default output path
		err   string   // substring of the expected error
	}{
		{
			// A directory takes every .ogo in it -- but not _test.ogo (a test file)
			// nor a non-.ogo file nor a subdirectory -- and is named after itself.
			name:  "directory",
			srcs:  []string{dir},
			files: []string{"alt.ogo", "main.ogo"},
			out:   filepath.Join(dir, base+".binary"),
		},
		{
			// One named file compiles only itself and keeps its own name, matching
			// `go build main.go`.
			name:  "single file keeps its name",
			srcs:  []string{filepath.Join(dir, "main.ogo")},
			files: []string{"main.ogo"},
			out:   filepath.Join(dir, "main.binary"),
		},
		{
			name:  "explicit file list",
			srcs:  []string{filepath.Join(dir, "main.ogo"), filepath.Join(dir, "alt.ogo")},
			files: []string{"main.ogo", "alt.ogo"},
			out:   filepath.Join(dir, base+".binary"),
		},
		{
			// A package is one directory, so files may not straddle two.
			name: "files from two directories",
			srcs: []string{filepath.Join(dir, "main.ogo"), filepath.Join(sub, "other.ogo")},
			err:  "must be in one directory",
		},
		{
			name: "directory with no sources",
			srcs: []string{empty},
			err:  "no .ogo source files",
		},
		{
			// Anything that is not a directory is taken for a source file, so a
			// mistyped path arrives as one and must be reported as the path that
			// was typed rather than by whoever fails to open its base name.
			name: "a path that is not there",
			srcs: []string{filepath.Join(dir, "sensr")},
			err:  filepath.Join(dir, "sensr") + ": no such file or directory",
		},
		{
			name: "a file that is not .ogo",
			srcs: []string{filepath.Join(dir, "notes.txt")},
			err:  "named source files must be .ogo files",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotDir, gotFiles, gotOut, err := resolvePackage(test.srcs)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("want error containing %q, got %v", test.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePackage: %v", err)
			}
			if !slices.Equal(gotFiles, test.files) {
				t.Errorf("files: got %v, want %v", gotFiles, test.files)
			}
			if gotOut != test.out {
				t.Errorf("out: got %q, want %q", gotOut, test.out)
			}
			if gotDir != dir {
				t.Errorf("dir: got %q, want %q", gotDir, dir)
			}
		})
	}

	// No argument at all means the current directory.
	t.Run("no arguments", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		gotDir, gotFiles, _, err := resolvePackage(nil)
		if err != nil {
			t.Fatalf("resolvePackage: %v", err)
		}
		if gotDir != "." {
			t.Errorf("dir: got %q, want %q", gotDir, ".")
		}
		if want := []string{"alt.ogo", "main.ogo"}; !slices.Equal(gotFiles, want) {
			t.Errorf("files: got %v, want %v", gotFiles, want)
		}
	})
}

// TestParseArgs pins the flag handling, in particular that several positional
// arguments are now collected rather than refused.
func TestParseArgs(t *testing.T) {
	srcs, f, err := parseArgs([]string{"a.ogo", "b.ogo", "-o", "x.binary", "--release", "--unchecked", "--allow-backend-warnings"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if want := []string{"a.ogo", "b.ogo"}; !slices.Equal(srcs, want) {
		t.Errorf("srcs: got %v, want %v", srcs, want)
	}
	if f.out != "x.binary" || !f.release || !f.unchecked || !f.allowWarnings {
		t.Errorf("got out=%q release=%v unchecked=%v allowWarnings=%v", f.out, f.release, f.unchecked, f.allowWarnings)
	}
	if f.clock != 0 {
		t.Errorf("clock: got %d, want 0 -- an unasked-for clock is the backend's to pick", f.clock)
	}
	if f.xtal != octogo.DefaultXtal {
		t.Errorf("xtal: got %d, want the %d default", f.xtal, octogo.DefaultXtal)
	}
	if f.noInline {
		t.Error("noInline: set by nothing")
	}
	for _, flag := range []string{"--no-inline", "-no-inline"} {
		if _, f, err := parseArgs([]string{flag, "a.ogo"}); err != nil || !f.noInline {
			t.Errorf("parseArgs(%s): noInline=%v, %v", flag, f.noInline, err)
		}
	}
	if _, _, err := parseArgs([]string{"-o"}); err == nil {
		t.Error("-o without an argument: want an error")
	}
	if _, _, err := parseArgs([]string{"-nope"}); err == nil {
		t.Error("unknown flag: want an error")
	}

	// A clock may be written in Hz or with the suffix it is usually spoken in --
	// nine digits are easy to write with the wrong number of zeros.
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"--clock", "200000000"}, 200000000},
		{[]string{"--clock", "200MHz"}, 200000000},
		{[]string{"-clock", "200mhz"}, 200000000},
		{[]string{"--clock", "160MHz"}, 160000000},
	} {
		_, f, err := parseArgs(append(tc.args, "a.ogo"))
		if err != nil {
			t.Errorf("parseArgs(%v): %v", tc.args, err)
			continue
		}
		if f.clock != tc.want {
			t.Errorf("parseArgs(%v): clock = %d, want %d", tc.args, f.clock, tc.want)
		}
	}
	if _, _, err := parseArgs([]string{"--clock", "fast"}); err == nil {
		t.Error("a clock that is not a frequency: want an error")
	}
	if _, _, err := parseArgs([]string{"--clock"}); err == nil {
		t.Error("--clock without an argument: want an error")
	}
}

// TestBuildLibrary covers a package that declares no func main. OctoGo has no
// package clause, so that is the whole of what tells a library from a program, and
// building one used to reach flexcc and fail there with `could not find function
// main` -- a C compiler's complaint about a C program the user never wrote.
func TestBuildLibrary(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ogoModFile), "module example.com/proj\n")
	if err := os.MkdirAll(filepath.Join(root, "sensor"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "sensor", "sensor.ogo"), "func Read() int { return 42 }\n")
	if err := os.MkdirAll(filepath.Join(root, "util"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "util", "util.ogo"),
		"import \"example.com/proj/sensor\"\n\nfunc Twice() int { return 2 * sensor.Read() }\n")

	for _, tc := range []struct {
		name string
		args []string
		code int
		want string // substring of the output, or of the error when code != 0
	}{
		{
			name: "a library is checked and makes no binary",
			args: []string{filepath.Join(root, "sensor")},
			want: "[no func main, checked only]",
		},
		{
			// Its imports are resolved as they are for a program, so a library
			// can be checked on its own without building whatever uses it.
			name: "a library that imports another package",
			args: []string{filepath.Join(root, "util")},
			want: "[no func main, checked only]",
		},
		{
			name: "-o has nothing to write",
			args: []string{"-o", filepath.Join(t.TempDir(), "x.binary"), filepath.Join(root, "sensor")},
			code: 2,
			want: "declares no func main",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			code, err := Build(tc.args, nil, &buf, &buf)
			got := buf.String()
			if err != nil {
				got = err.Error()
			}
			if code != tc.code {
				t.Errorf("code=%d, want %d\n%s", code, tc.code, got)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("output %q does not contain %q", got, tc.want)
			}
		})
	}

	// No binary anywhere: a library that produced one would be a program.
	des, err := os.ReadDir(filepath.Join(root, "sensor"))
	if err != nil {
		t.Fatal(err)
	}
	for _, de := range des {
		if strings.HasSuffix(de.Name(), ".binary") {
			t.Errorf("a library wrote %s", de.Name())
		}
	}
}

// TestBuildLibraryEmits pins WHY a library is emitted rather than merely checked:
// the lifetime and escape refusals are made by the emitter, so a library that was
// only type-checked would be held to far less than the same code is when a program
// is built from it.
func TestBuildLibraryEmits(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "lib.ogo"), "func Bad(r int) string { return string(rune(r)) }\n")
	var buf bytes.Buffer
	code, err := Build([]string{dir}, nil, &buf, &buf)
	got := buf.String()
	if err != nil {
		got = err.Error()
	}
	if code == 0 {
		t.Fatalf("code=0, want a refusal\n%s", got)
	}
	if !strings.Contains(got, "does not outlive the function") {
		t.Errorf("output %q is not the lifetime refusal", got)
	}
}

// TestCompileCBackendCrash holds compileC to reporting a backend that CRASHES as an
// error, not a panic of the whole compiler. doc/register-limit-crash.c is a
// function the backend gives up on and then dereferences a register it never
// allocated -- in the transpile a nil dereference, which flexcc.Main passes on.
// Either answer is fine here, the crash reported or a clean refusal from a backend
// that no longer crashes: what must not happen is the panic reaching the caller.
func TestCompileCBackendCrash(t *testing.T) {
	src := filepath.Join("..", "..", "doc", "register-limit-crash.c")
	if _, err := os.Stat(src); err != nil {
		t.Skip(err)
	}
	var stdout, stderr bytes.Buffer
	rc, err := compileC(src, filepath.Join(t.TempDir(), "prog.binary"), "", &stdout, &stderr)
	if err == nil || rc == 0 {
		t.Fatalf("compileC = %d, %v: the program should not build\n%s%s", rc, err, stdout.Bytes(), stderr.Bytes())
	}
	t.Logf("%v", err)
}

// buildProgram builds the program src with the real backend and the flags given,
// and answers with the binary and the backend's listing of it. A build that fails
// or says anything fails the test.
func buildProgram(t *testing.T, src string, flags ...string) (binary, listing []byte) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "prog", "main.ogo"), src)
	out := filepath.Join(dir, "prog.binary")
	var buf bytes.Buffer
	code, err := Build(append(flags, "-o", out, filepath.Join(dir, "prog")), nil, &buf, &buf)
	if err != nil || code != 0 {
		t.Fatalf("build %v: code=%d err=%v\n%s", flags, code, err, buf.String())
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		t.Fatalf("build %v: the backend was not silent:\n%s", flags, s)
	}
	binary, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if listing, err = os.ReadFile(filepath.Join(dir, "prog.p2asm")); err != nil {
		t.Fatal(err)
	}
	return binary, listing
}

// TestBuildInlines holds what a runtime check costs to the check itself. A method
// of one line is what the backend inlines by itself until the line has a check in
// it, and then it is a call, which on this target costs what forty or fifty
// instructions do: measured on a P2-EDGE at 160 MHz, a method testing a field of
// its receiver took 211 clocks a call checked and 36 unchecked, a setter of an
// array's element 229 and 25, and two accessors of a word 395 and 52 (p2-11's
// OCTOGO.md). With the small functions marked for the backend they take 51, 67 and
// 102, and a helper of three returns 29 for 83, written to leave through one
// (singleExit). No board is asked: a function that is inlined everywhere is one
// the listing has no call of, and no body.
func TestBuildInlines(t *testing.T) {
	const src = `type machine struct {
	traps uint16
	r     [8]uint16
}

func (m *machine) aborted() bool { return m.traps&3 != 0 }

func (m *machine) set(r int, v uint16) { m.r[r] = v }

var ram [4096]uint16

func readWord(a uint32) uint16 { return ram[a>>1] }

func writeWord(a uint32, v uint16) { ram[a>>1] = v }

func nz(v, sign uint16) uint16 {
	if v == 0 {
		return 4
	}
	if v&sign != 0 {
		return 8
	}
	return 0
}

var m machine

func main() {
	n := 0
	for i := 0; i < 1000; i++ {
		a := uint32(i) & 4094
		writeWord(a, readWord(a)+1)
		m.set(i&7, uint16(i))
		if m.aborted() {
			n++
		}
		n += int(nz(uint16(i), 0x80))
	}
	println(n, m.r[3], ram[5])
}
`
	calls := func(listing []byte) (n int) {
		for _, fn := range []string{"machine_aborted", "machine_set", "readWord", "writeWord", "nz"} {
			n += len(regexp.MustCompile(`(?m)^\s+call\w*\s+#_`+fn+`\s*$`).FindAll(listing, -1))
		}
		return n
	}
	marked, listing := buildProgram(t, src)
	if n := calls(listing); n != 0 {
		t.Errorf("%d calls of a small function are left in the checked program, want none", n)
	}
	// The premise: what the backend does by itself is to call all four, or the
	// test above holds nothing.
	unmarked, listing := buildProgram(t, src, "--no-inline")
	if n := calls(listing); n != 5 {
		t.Errorf("--no-inline: %d calls, want 5: the backend inlines by itself what this test is about", n)
	}
	if bytes.Equal(marked, unmarked) {
		t.Errorf("--no-inline builds the same binary")
	}
	// And without the checks the accessors never needed the mark: the unchecked
	// program is the one the backend inlined all along, but for the function of
	// three returns, which it calls checked or not.
	if _, listing = buildProgram(t, src, "--unchecked", "--no-inline"); calls(listing) != 1 {
		t.Errorf("--unchecked --no-inline: %d calls, want that of nz alone", calls(listing))
	}
}

// TestBuildInlineFallback holds a build to not failing for having been made
// faster. Every copy of an inlined function brings the registers of its locals to
// where it lands, the backend gives none back between the arms of a switch, and a
// cog has 480 for everything: a program of many small helpers, all called from one
// function, outgrows them with the helpers marked and fits without. It is built
// without then, and nothing is said of the first attempt.
func TestBuildInlineFallback(t *testing.T) {
	const helpers = 32
	var b strings.Builder
	b.WriteString("import \"p2\"\n\ntype V struct {\n\ta, b int\n}\n\nvar v [8]V\n\n")
	for i := range helpers {
		fmt.Fprintf(&b, `func mix%d(p *V, b, c int) int {
	x := p.a*b + c + %[1]d
	y := x ^ (p.b >> 3)
	if y&1 != 0 {
		y += c
	}
	p.a = x*y + b
	return p.a ^ x
}

`, i)
	}
	b.WriteString("func step(k, n int) int {\n\tswitch k {\n")
	for i := range helpers {
		fmt.Fprintf(&b, "\tcase %d:\n\t\tn += mix%[1]d(&v[%d], n, %d)\n\t\tn ^= mix%[1]d(&v[%d], k, n)\n", i, i%8, i+1, (i+3)%8)
	}
	fmt.Fprintf(&b, "\t}\n\treturn n\n}\n\nfunc main() {\n\tn := int(p2.GetCt() & 1)\n\tfor k := 0; k < %d; k++ {\n"+
		"\t\tn = step(k, n)\n\t}\n\tprintln(n != 0)\n}\n", helpers)
	src := b.String()

	// The premise: the program as it is first emitted, its helpers marked, is one
	// the backend refuses for the registers.
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
	if n := bytes.Count(c.Bytes(), []byte(") "+octogo.InlineMark+" {")); n != helpers {
		t.Fatalf("%d functions are marked, want the %d helpers", n, helpers)
	}
	cFile := filepath.Join(dir, "marked.c")
	write(t, cFile, c.String())
	if _, err := compileC(cFile, filepath.Join(dir, "marked.binary"), "", &said, &said); err == nil {
		t.Fatalf("the program builds with its helpers marked: the fallback is not what built it, and the test needs a larger one")
	}
	if !outgrewCog(said.Bytes()) {
		t.Fatalf("the program with its helpers marked fails for something else:\n%s", said.Bytes())
	}

	binary, _ := buildProgram(t, src)
	unmarked, _ := buildProgram(t, src, "--no-inline")
	if !bytes.Equal(binary, unmarked) {
		t.Errorf("the program built is not the one --no-inline builds")
	}
}

// TestOutgrewCog pins the two things the backend says of a program that needs
// more registers than a cog has, and that nothing else is taken for them: a
// program refused for another reason is refused, not built again.
func TestOutgrewCog(t *testing.T) {
	for _, test := range []struct {
		said string
		want bool
	}{
		{"prog.p2asm:3941: error: fit 480 failed: pc is 546\n", true},
		{"prog.c:12: error: Internal error exceeded local register limit, possibly due to -O2\n", true},
		{"prog.c:12: error: unknown identifier x\n", false},
		{"", false},
	} {
		if got := outgrewCog([]byte(test.said)); got != test.want {
			t.Errorf("outgrewCog(%q) = %v, want %v", test.said, got, test.want)
		}
	}
}

// TestBuildSharedVariables holds specs.go's "Memory shared between cogs" to the
// backend, which is where it could break: a read of hub RAM that a loop makes is
// made on every pass. Each loop of the program -- the spins and the loops holding
// them -- is a backward jump in the listing, and each must read hub RAM, or call
// what does, between its label and its jump: a value the backend kept in a
// register would leave a spin with neither, and it would spin for ever. Checked,
// unchecked and without the inlining marks, which lay the loops out each its own
// way. The run case "package variables two cogs share" holds what the program
// does, on the host and on the board.
func TestBuildSharedVariables(t *testing.T) {
	const src = `type ring struct {
	head, tail uint32
	buf        [8]uint32
}

func (q *ring) empty() bool { return q.head == q.tail }

var (
	r     ring
	flag  uint32
	done  bool
	total uint32
)

func produce() {
	for v := uint32(1); v <= 100; v++ {
		for r.head-r.tail == 8 {
		}
		r.buf[r.head%8] = v
		r.head++
	}
	flag = 1
	for !done {
	}
	total = 7
}

func main() {
	go produce()
	sum := uint32(0)
	for n := 0; n < 100; n++ {
		for r.empty() {
		}
		sum += r.buf[r.tail%8]
		r.tail++
	}
	for flag == 0 {
	}
	done = true
	for total == 0 {
	}
	println(sum, total)
}
`
	label := regexp.MustCompile(`^(LR__\d+)$`)
	jump := regexp.MustCompile(`#(LR__\d+)\s*$`)
	reads := regexp.MustCompile(`^\s+(?:if_\w+\s+)?(?:rd(?:long|word|byte)|call)\s`)
	for _, flags := range [][]string{nil, {"--unchecked"}, {"--no-inline"}} {
		_, listing := buildProgram(t, src, flags...)
		lines := strings.Split(string(listing), "\n")
		loops := 0
		for _, fn := range []string{"_main", "_produce"} {
			from := slices.Index(lines, fn)
			to := slices.Index(lines, fn+"_ret")
			if from < 0 || to < from {
				t.Fatalf("%v: no %s in the listing", flags, fn)
			}
			at := map[string]int{}
			for i := from; i < to; i++ {
				if m := label.FindStringSubmatch(lines[i]); m != nil {
					at[m[1]] = i
					continue
				}
				m := jump.FindStringSubmatch(lines[i])
				if m == nil {
					continue
				}
				start, back := at[m[1]]
				if !back {
					continue // forward
				}
				loops++
				if !slices.ContainsFunc(lines[start:i], reads.MatchString) {
					t.Errorf("%v: %s loops from %s to line %d with no hub read and no call:\n%s",
						flags, fn, m[1], i+1, strings.Join(lines[start:i+1], "\n"))
				}
			}
		}
		if loops < 7 {
			t.Errorf("%v: %d loops found in _main and _produce, want 7: the test reads the listing wrong", flags, loops)
		}
	}
}

// TestBuildLockedRecord holds the lock rule of specs.go's "Memory shared between
// cogs" to the backend: a cog's writes before p2.Unlock are made before its
// lockrel, and its reads after a p2.TryLock that answered true after its locktry.
// The writer's every hub write lies between a locktry and the lockrel after it, and
// the reader's three reads lie between its own. The run case "a record written and
// read under a hardware lock" holds what the program does, on the host and on the
// board, where without the lock 53 snapshots of 2000 were torn.
func TestBuildLockedRecord(t *testing.T) {
	const src = `import "p2"

type rec struct {
	a, b, c int
}

var (
	shared rec
	lk     int
	done   = make(chan bool)
)

func writer() {
	for i := 1; i <= 1000; i++ {
		for !p2.TryLock(lk) {
		}
		shared.a = i
		shared.b = i * 2
		shared.c = i * 3
		p2.Unlock(lk)
	}
	done <- true
}

func snapshot() (int, int, int) {
	for !p2.TryLock(lk) {
	}
	a, b, c := shared.a, shared.b, shared.c
	p2.Unlock(lk)
	return a, b, c
}

func main() {
	lk = p2.NewLock()
	go writer()
	torn := 0
	for i := 0; i < 2000; i++ {
		a, b, c := snapshot()
		if b != a*2 || c != a*3 {
			torn++
		}
	}
	<-done
	println("torn:", torn)
}
`
	op := func(l, name string) bool {
		f := strings.Fields(l)
		if len(f) != 0 && strings.HasPrefix(f[0], "if_") {
			f = f[1:]
		}
		return len(f) != 0 && f[0] == name
	}
	for _, flags := range [][]string{nil, {"--unchecked"}, {"--no-inline"}} {
		_, listing := buildProgram(t, src, flags...)
		lines := strings.Split(string(listing), "\n")
		from, to := slices.Index(lines, "_writer"), slices.Index(lines, "_writer_ret")
		if from < 0 || to < from {
			t.Fatalf("%v: no _writer in the listing", flags)
		}
		// Each span runs from a locktry to the lockrel after it; held counts the
		// hub accesses of the kind asked in the span, and outside those not in one.
		spans := func(lines []string, access string) (held []int, outside int) {
			in := false
			for _, l := range lines {
				switch {
				case op(l, "locktry"):
					in = true
					held = append(held, 0)
				case op(l, "lockrel"):
					in = false
				case op(l, access) && in:
					held[len(held)-1]++
				case op(l, access):
					outside++
				}
			}
			return held, outside
		}
		held, outside := spans(lines[from:to], "wrlong")
		if len(held) != 1 || held[0] < 3 || outside != 0 {
			t.Errorf("%v: _writer's writes under the lock %v, outside it %d; want the three fields' between locktry and lockrel and none outside:\n%s",
				flags, held, outside, strings.Join(lines[from:to+1], "\n"))
		}
		// The reader is snapshot or wherever it was inlined: some span outside the
		// writer reads the three fields and the lock's number for its lockrel.
		other := append(slices.Clone(lines[:from]), lines[to:]...)
		held, _ = spans(other, "rdlong")
		if !slices.ContainsFunc(held, func(n int) bool { return n >= 4 }) {
			t.Errorf("%v: no reader span holds the three fields' reads, rdlongs under each lock %v", flags, held)
		}
	}
}

// TestCogHint pins how a program that outgrew a cog is told which functions hold
// the registers: from the listing, the program's own functions only -- those its C
// defines without static -- by the number of distinct local registers each uses,
// the most first. A listing older than the build, or none, names nothing.
func TestCogHint(t *testing.T) {
	const c = `static int32_t ogo_helper(int32_t x) {
int32_t mix(int32_t a, int32_t b) OGO_INLINE {
uint8_t crc8(ogo_slice_uint8_t p);
uint8_t crc8(ogo_slice_uint8_t p) {
void decoder_feed(decoder* d, uint8_t b) {
int main(void) {
`
	own := programFuncs([]byte(c))
	for _, name := range []string{"mix", "crc8", "decoder_feed", "main"} {
		if !own[name] {
			t.Errorf("programFuncs: %s is missing from %v", name, own)
		}
	}
	if own["ogo_helper"] {
		t.Errorf("programFuncs: a static helper is the program's")
	}

	const listing = `_main
	mov	local01, #1
	mov	local02, local01
	add	local_03, local02
	add	local_03, local01
_main_ret
	ret
_crc8
	mov	local01, #2
_crc8_ret
	ret
_decoder_feed
	mov	local01, #3
	mov	local02, #4
_decoder_feed_ret
	ret
_ogo_helper_0003
	mov	local01, local02
	mov	local03, local04
	mov	local05, local06
_ogo_helper_0003_ret
	ret
`
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.p2asm")
	write(t, path, listing)
	const want = "the functions holding the most are main (3), decoder_feed (2), crc8 (1); split main into smaller ones"
	if got := cogHint(path, time.Now(), own); !strings.Contains(got, want) {
		t.Errorf("cogHint:\ngot  %s\nwant ...%s", got, want)
	}
	for _, got := range []string{
		cogHint(path, time.Now().Add(time.Hour), own),              // the listing is older than the build
		cogHint(filepath.Join(dir, "none.p2asm"), time.Now(), own), // there is none
	} {
		if strings.Contains(got, "holding the most") || !strings.Contains(got, "split the largest function") {
			t.Errorf("cogHint named a function it cannot know: %s", got)
		}
	}
}

// TestBuildCogHint builds a program whose main keeps 160 values at once, which
// outgrows a cog, and asks that the build says so in the program's terms: main is
// named as what to split, after what the backend said.
func TestBuildCogHint(t *testing.T) {
	const n = 160
	var b strings.Builder
	fmt.Fprintf(&b, "var g [%d]int32\n\nfunc main() {\n", n)
	for i := range n {
		fmt.Fprintf(&b, "\ta%d := g[%[1]d]\n", i)
	}
	b.WriteString("\tfor i := 0; i < 3; i++ {\n")
	for i := range n {
		fmt.Fprintf(&b, "\t\ta%d += a%d\n", i, (i+1)%n)
	}
	b.WriteString("\t}\n")
	for i := range n {
		fmt.Fprintf(&b, "\tg[%d] = a%[1]d\n", i)
	}
	b.WriteString("\tprintln(g[0])\n}\n")

	dir := t.TempDir()
	write(t, filepath.Join(dir, "prog", "main.ogo"), b.String())
	var said bytes.Buffer
	code, err := Build([]string{"-o", filepath.Join(dir, "prog.binary"), filepath.Join(dir, "prog")}, nil, &said, &said)
	if err == nil || code == 0 {
		t.Fatalf("the program builds: the test needs a larger one\n%s", said.String())
	}
	if !outgrewCog(said.Bytes()) {
		t.Fatalf("the program fails for something else:\n%s", said.String())
	}
	if !strings.Contains(said.String(), "the functions holding the most are main (") ||
		!strings.Contains(said.String(), "split main into smaller ones") {
		t.Errorf("the build does not name main:\n%s", said.String())
	}
}

// TestBackendFault holds a build to the standard the target-build tests hold the
// emitter to: the backend says nothing about the C ogo wrote, or the build fails.
// Every kind of warning that reached a build was wrong code, built in silence, so
// one is a fault of the compiler's, reported as such: no binary is left to be
// loaded, the C is kept to be reported, and --allow-backend-warnings builds anyway.
// A diagnostic examined and found harmless, harmlessWarnings, is passed over.
func TestBackendFault(t *testing.T) {
	const c = "int main(void) { return 0; }\n"
	dir := t.TempDir()
	out, keep := filepath.Join(dir, "p.binary"), filepath.Join(dir, "p.c")
	for _, test := range []struct {
		name, said string
		fault      bool
	}{
		{"silent", "", false},
		{"blank lines", "\n\n", false},
		{"harmless", "_platform_:23: warning: Deleting apparently unused cordic instruction qsqrt\n", false},
		{"the program's own Spin2", "/home/u/proj/drv/obj.spin2:2: warning: Applying @ to RES memory `buf' is not supported in standard Spin\n", false},
		{"warning", "/tmp/ogo-build-1/p.c:76: warning: incompatible types in comparison\n", true},
		{"warning beside a harmless one", "_platform_:23: warning: Deleting apparently unused cordic instruction qsqrt\n" +
			"/tmp/ogo-build-1/p.c:9: warning: Bad number of parameters in call to f: expected 2 found 1\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			write(t, out, "binary")
			os.Remove(keep)
			code, err := backendFault([]byte(c), out, keep, []byte(test.said))
			_, binErr := os.Stat(out)
			kept, keepErr := os.ReadFile(keep)
			switch {
			case !test.fault && (code != 0 || err != nil):
				t.Errorf("code=%d err=%v, want none", code, err)
			case !test.fault && binErr != nil:
				t.Errorf("the binary is gone: %v", binErr)
			case test.fault && (code == 0 || err == nil):
				t.Errorf("code=%d err=%v, want a fault", code, err)
			case test.fault && binErr == nil:
				t.Errorf("the binary is left to be loaded")
			case test.fault && (keepErr != nil || string(kept) != c):
				t.Errorf("the C is not kept: %v", keepErr)
			case test.fault && !strings.Contains(err.Error(), keep) || test.fault && !strings.Contains(err.Error(), "--allow-backend-warnings"):
				t.Errorf("the message names neither the C nor the way round it: %v", err)
			}
		})
	}
}

// TestBackendRefusal: a build the backend refuses keeps the C, whose lines the
// backend named in a directory removed with the build, and says the fault is ogo's;
// where it spoke only of the program's own .spin2 objects, nothing is kept or said.
func TestBackendRefusal(t *testing.T) {
	const c = "int main(void) { return x; }\n"
	dir := t.TempDir()
	keep := filepath.Join(dir, "p.c")
	failed := errors.New("flexcc: flexcc returned with status 1")
	for _, test := range []struct {
		name, said string
		err        error
		ours       bool
	}{
		{"the C", "/tmp/ogo-build-1/p.c:580: error: Cannot handle expression yet\n", failed, true},
		{"a crash", "", errors.New("flexcc crashed: runtime error"), true},
		{"the program's own Spin2", "/home/u/proj/drv/obj.spin2:2: error: syntax error\n", failed, false},
		// Code past what a call reaches is an image past Hub RAM: the program's.
		{"the program's size", "/tmp/p.p2asm:159: error: Operand for call is out of range\n", failed, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			os.Remove(keep)
			code, err := backendRefusal([]byte(c), keep, []byte(test.said), 1, test.err)
			kept, keepErr := os.ReadFile(keep)
			switch {
			case code == 0 || err == nil:
				t.Errorf("code=%d err=%v, want a failure", code, err)
			case test.ours && (keepErr != nil || string(kept) != c):
				t.Errorf("the C is not kept: %v", keepErr)
			case test.ours && !strings.Contains(err.Error(), keep):
				t.Errorf("the message does not name the C: %v", err)
			case !test.ours && keepErr == nil:
				t.Errorf("the C is kept for a refusal of the program's own")
			case strings.Contains(test.said, "out of range"):
				if !strings.Contains(err.Error(), "does not fit the P2's 512 KB of Hub RAM") {
					t.Errorf("err=%v, want the program's size", err)
				}
			case !test.ours && err != test.err:
				t.Errorf("err=%v, want %v as it was", err, test.err)
			}
		})
	}
}

// TestBuildBackendRefusal builds a program the backend refuses, `x << 32 >> 32` of
// a uint64 (doc/uint64-shift-32-pair.c, flexprop#122), and asks for the C to be kept
// and named. The day the backend builds it, the test says so and stops.
func TestBuildBackendRefusal(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "prog", "main.ogo"), "var x uint64 = 0x123456789\n\nfunc main() { println(x << 32 >> 32) }\n")
	out := filepath.Join(dir, "prog.binary")
	var buf bytes.Buffer
	code, err := Build([]string{"-o", out, filepath.Join(dir, "prog")}, nil, &buf, &buf)
	if err == nil && code == 0 {
		t.Skip("the backend builds it now: flexprop#122 is fixed, and doc/uint64-shift-32-pair.c says so")
	}
	keep := filepath.Join(dir, "prog.c")
	if _, kerr := os.Stat(keep); kerr != nil || err == nil || !strings.Contains(err.Error(), keep) || !strings.Contains(err.Error(), "fault of ogo's") {
		t.Fatalf("code=%d err=%v, want the C kept at %s and named\n%s", code, err, keep, buf.String())
	}
}

// TestBuildHubOverflow: a program too big for Hub RAM is the program's, told so --
// the backend only warns, and was taken for a fault of ogo's to be reported.
func TestBuildHubOverflow(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "prog", "main.ogo"), "var big [520000]byte\n\nfunc main() {\n\tbig[3] = 7\n\tprintln(big[3], len(big))\n}\n")
	out := filepath.Join(dir, "prog.binary")
	var buf bytes.Buffer
	_, err := Build([]string{"-o", out, filepath.Join(dir, "prog")}, nil, &buf, &buf)
	if err == nil || !strings.Contains(err.Error(), "does not fit") || strings.Contains(err.Error(), "fault of ogo's") {
		t.Fatalf("err=%v, want the program's size named\n%s", err, buf.String())
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Errorf("a binary no P2 can load was left")
	}
}

// TestBuildHarmlessWarning builds the one program known to make the backend warn
// about nothing that matters, a discarded square root started on a cog, and holds
// it to building: the allowlist is what keeps the rule above from refusing it.
func TestBuildHarmlessWarning(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "prog", "main.ogo"), "import \"math\"\n\nfunc main() { go math.Sqrt(2) }\n")
	out := filepath.Join(dir, "prog.binary")
	var buf bytes.Buffer
	code, err := Build([]string{"-o", out, filepath.Join(dir, "prog")}, nil, &buf, &buf)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
	}
	if !strings.Contains(buf.String(), "unused cordic instruction") {
		t.Logf("the backend no longer warns about it:\n%s", buf.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}
