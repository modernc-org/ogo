// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

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
	srcs, f, err := parseArgs([]string{"a.ogo", "b.ogo", "-o", "x.binary", "--release", "--unchecked"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if want := []string{"a.ogo", "b.ogo"}; !slices.Equal(srcs, want) {
		t.Errorf("srcs: got %v, want %v", srcs, want)
	}
	if f.out != "x.binary" || !f.release || !f.unchecked {
		t.Errorf("got out=%q release=%v unchecked=%v", f.out, f.release, f.unchecked)
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
	rc, err := compileC(src, filepath.Join(t.TempDir(), "prog.binary"), &stdout, &stderr)
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
// 102. No board is asked: a function that is inlined everywhere is one the listing
// has no call of, and no body.
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
	}
	println(n, m.r[3], ram[5])
}
`
	calls := func(listing []byte) (n int) {
		for _, fn := range []string{"machine_aborted", "machine_set", "readWord", "writeWord"} {
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
	if n := calls(listing); n != 4 {
		t.Errorf("--no-inline: %d calls, want 4: the backend inlines by itself what this test is about", n)
	}
	if bytes.Equal(marked, unmarked) {
		t.Errorf("--no-inline builds the same binary")
	}
	// And without the checks it never needed the mark: the unchecked program is
	// the one the backend inlined all along.
	if _, listing = buildProgram(t, src, "--unchecked", "--no-inline"); calls(listing) != 0 {
		t.Errorf("--unchecked --no-inline: %d calls, want none", calls(listing))
	}
}

// TestBuildInlineFallback holds a build to not failing for having been made
// faster. Every copy of an inlined function brings the registers of its locals to
// where it lands, the backend gives none back between the arms of a switch, and a
// cog has 480 for everything: a program of many small helpers, all called from one
// function, outgrows them with the helpers marked and fits without. It is built
// without then, and nothing is said of the first attempt.
func TestBuildInlineFallback(t *testing.T) {
	const helpers = 24
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
	if _, err := compileC(cFile, filepath.Join(dir, "marked.binary"), &said, &said); err == nil {
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
