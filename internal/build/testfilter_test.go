// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"unicode"

	"modernc.org/ogo/internal/octogo"
)

// filterSrc is a package of tests and benchmarks, OctoGo and Go alike, whose
// subtests' names are the shapes a -run pattern meets: a name used twice, an empty
// one, spaces and control characters go test rewrites, a slash in a name -- a level
// of its own -- a capital, a letter beyond ASCII, and three levels. TestD's are
// the names go test's unique tells apart by its counts of full names: a name
// written as one a count made, and one a slash makes the same as another
// subtest's, its own count after it and another's, in the subtest it continues and
// in another.
const filterSrc = `import "testing"

func leaf(t *testing.T) {}

func TestA(t *testing.T) {
	t.Run("a", leaf)
	t.Run("b", leaf)
	t.Run("b", leaf)
	t.Run("a b", leaf)
	t.Run("", leaf)
	t.Run("", leaf)
	t.Run("a/b", nested)
	t.Run("B", leaf)
	t.Run("été", leaf)
	t.Run("x\ty\x01", leaf)
	t.Run("a/b", nested)
}

func nested(t *testing.T) {
	t.Run("deep", leaf)
	t.Run("c", leaf)
}

func TestB(t *testing.T) {
	t.Run("x", func(t *testing.T) {
		t.Run("y", leaf)
		t.Run("z", func(t *testing.T) {
			t.Run("deep", leaf)
		})
	})
	t.Run("y", leaf)
	t.Run("z", func(t *testing.T) {
		t.Errorf("z fails")
	})
}

func TestC(t *testing.T) {}

func TestD(t *testing.T) {
	t.Run("b", leaf)
	t.Run("b", leaf)
	t.Run("b#01", leaf)
	t.Run("b", leaf)
	t.Run("b#02", leaf)
	t.Run("b#01#01", leaf)
	t.Run("b#01", leaf)
	t.Run("", leaf)
	t.Run("#00", leaf)
	t.Run("", leaf)
	t.Run("#01", leaf)
	t.Run("a", func(t *testing.T) {
		t.Run("b", leaf)
		t.Run("", leaf)
	})
	t.Run("a/b", leaf)
	t.Run("a/", leaf)
	t.Run("a", func(t *testing.T) {
		t.Run("b", leaf)
	})
	t.Run("a#01/b", leaf)
	t.Run("a/b#01", leaf)
	t.Run("x/b#01", leaf)
	t.Run("x", func(t *testing.T) {
		t.Run("b", leaf)
		t.Run("b", leaf)
		t.Run("b#01", leaf)
	})
	t.Run("p", func(t *testing.T) {
		t.Run("a", func(t *testing.T) {
			t.Run("b", leaf)
			t.Run("b#01", leaf)
		})
		t.Run("a/b", leaf)
	})
	t.Run("x y", leaf)
	t.Run("x_y", leaf)
	t.Run("x\ty", leaf)
}

func bleaf(b *testing.B) {
	for i := 0; i < b.N; i++ {
	}
}

func BenchmarkS(b *testing.B) {
	b.Run("x", func(b *testing.B) {
		b.Run("deep", bleaf)
		b.Run("y", bleaf)
	})
	b.Run("y", bleaf)
	b.Run("y", bleaf)
	b.Run("y#01", bleaf)
	b.Run("a b", bleaf)
}

func BenchmarkP(b *testing.B) {
	for i := 0; i < b.N; i++ {
	}
}
`

// TestFilterMatchesGoTest holds -run and -bench patterns naming subtests and
// sub-benchmarks to go test's own selection: each pattern run by go test over the
// package as Go, and by the runner ogo test generates over it as OctoGo, built
// for the host shim -- the levels below a test's own matched by the testing
// package's machine, as on the board -- and the RUN and result lines of the two,
// and of benchmarks the names and which of them print a result, compared.
func TestFilterMatchesGoTest(t *testing.T) {
	o := newGoTestOracle(t, filterSrc)
	type tc struct {
		run, bench string
	}
	cases := []tc{
		{run: "TestA/b"},
		{run: "TestA/^b$"},
		{run: "TestA/b#01"},
		{run: "/b"},
		{run: "//"},
		{run: "TestA/a_b"},
		{run: "TestA/a b"},
		{run: "TestA/^a$"},
		{run: "TestA/^a$/c"},
		{run: "TestA/a/b/deep"},
		{run: "TestA/a/b#01/"},
		{run: "TestA/#00"},
		{run: "TestA/^#01$"},
		{run: "TestA/^$"},
		{run: "TestA/(?i)^b$"},
		{run: "TestA/[aB]$"},
		{run: "TestA/é"},
		{run: "TestA/^.t.$"},
		{run: "TestA/\\x01"},
		{run: "TestA/x_y"},
		{run: "TestA/\\bb\\b"},
		{run: "TestA/\\Bt\\B"},
		{run: "TestA/a\\z"},
		{run: "TestA/^\\Aa\\z"},
		{run: "TestA/(?m)^b$"},
		{run: "TestA/b+|TestB/x/y"},
		{run: "Test/x|TestA/a"},
		{run: "TestA|TestB/y"},
		{run: "Test(A|B)/y"},
		{run: "TestB/x/y"},
		{run: "TestB/x/"},
		{run: "TestB/./deep"},
		{run: "TestB/z"},
		{run: "TestB/[^z]"},
		{run: "TestB/x/z/deep"},
		{run: "TestB/x/z/deep/more"},
		{run: "(?i)testb/X"},
		{run: "TestC/x"},
		{run: "Test[/]"},
		{run: "Test[/A]"},
		{run: "TestA/a.b"},
		{run: "TestA/a{1,2}"},
		{run: "TestA/[[:upper:]]"},
		{run: "TestA/\\pL\\pL"},
		{run: "TestA/\\Q#0\\E"},
		{run: "TestD"},
		{run: "TestD/b#01"},
		{run: "TestD/^b#01$"},
		{run: "TestD/b#01#01"},
		{run: "TestD/#0"},
		{run: "TestD/a/b"},
		{run: "TestD/a#01/b#01"},
		{run: "TestD/x/b#01#01"},
		{run: "TestD/x/b#01$"},
		{run: "TestD/x/"},
		{run: "TestD/p/a/b#01#01"},
		{run: "^$", bench: "BenchmarkS/x"},
		{run: "^$", bench: "BenchmarkS/"},
		{run: "^$", bench: "BenchmarkS/x/deep"},
		{run: "^$", bench: "BenchmarkS/y"},
		{run: "^$", bench: "BenchmarkS/y#01"},
		{run: "^$", bench: "BenchmarkS/y#01#01"},
		{run: "^$", bench: "BenchmarkS/a_b"},
		{run: "^$", bench: "BenchmarkS/y/z"},
		{run: "^$", bench: "BenchmarkS/./x"},
		{run: "^$", bench: "BenchmarkS/a b/"},
		{run: "^$", bench: "BenchmarkS/y/|BenchmarkS/y#01"},
		{run: "^$", bench: "BenchmarkP/z"},
		{run: "^$", bench: "BenchmarkS|BenchmarkP/z"},
		{run: "^$", bench: "BenchmarkP/z|BenchmarkS"},
		{run: "^$", bench: "/y"},
		{run: "TestB/y", bench: "."},
		{run: "TestB/y", bench: "BenchmarkS/x"},
	}
	for _, c := range cases {
		name := c.run
		if c.bench != "" {
			name += " -bench " + c.bench
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o.compare(t, c.run, c.bench)
		})
	}
}

// TestSubtestNamesMatchGoTest holds the names of subtests to go test's, rewritten
// as its rewrite rewrites them: a package whose subtests are named by the runes on
// both sides of every change from printing to not, as strconv.IsPrint has them --
// which the runner's table says to the board -- by the spaces, controls and
// Latin-1's corners Go spells by hand, and by bytes of no UTF-8, run by go test and
// by the runner over the host shim.
func TestSubtestNamesMatchGoTest(t *testing.T) {
	runes := []rune{1, 7, 8, 9, 0x1b, ' ', '_', 0x7e, 0x7f, 0x80, 0x84, 0x85, 0x9f, 0xa0, 0xa1, 0xac, 0xad, 0xae, 0xff,
		0x1680, 0x2000, 0x200a, 0x200b, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff, 0xfffd, 0xffff, unicode.MaxRune}
	prints := true
	for r := rune(0x100); r <= unicode.MaxRune; r++ {
		if p := strconv.IsPrint(r); p != prints {
			runes = append(runes, r-1, r)
			prints = p
		}
	}
	var names []string
	for len(runes) != 0 {
		n := min(8, len(runes))
		names = append(names, string(runes[:n]))
		runes = runes[n:]
	}
	names = append(names, "\xff", "a\xc3", "\xc3a", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xc0\x80", "\xe2\x82", "\xef\xbf\xbd", "\xf0\x9f\x98",
		"\xe0\x80\x80", "\xe0\xa0\x80", "\xf0\x80\x80\x80", "\xf0\x90\x80\x80", "\xf4\x8f\xbf\xbf", "\xed\x9f\xbf", "\xdf\xbf", "\xc2")
	var b strings.Builder
	b.WriteString("import \"testing\"\n\nfunc leaf(t *testing.T) {}\n")
	for i, nm := range names {
		if i%200 == 0 {
			if i != 0 {
				b.WriteString("}\n")
			}
			fmt.Fprintf(&b, "\nfunc TestN%d(t *testing.T) {\n", i/200)
		}
		fmt.Fprintf(&b, "\tt.Run(%s, leaf)\n", strconv.QuoteToASCII(nm))
	}
	b.WriteString("}\n")
	// Names longer than the board compares at a time, alike, unlike only past
	// that, written with a count, and one a slash makes another's at its level.
	long := strings.Repeat("n", 150)
	fmt.Fprintf(&b, `
func longer(t *testing.T) { t.Run(%[1]q, leaf) }

func TestLong(t *testing.T) {
	t.Run(%[1]q, leaf)
	t.Run(%[1]q, leaf)
	t.Run(%[2]q, leaf)
	t.Run(%[3]q, leaf)
	t.Run(%[1]q, longer)
	t.Run(%[4]q, leaf)
	t.Run(%[4]q, leaf)
}
`, long, long[:149]+"m", long+"#01", long+"#02/"+long)
	o := newGoTestOracle(t, b.String())
	o.compare(t, "Test", "")
}

// A goTestOracle runs a package of tests and benchmarks by go test, as Go, and by
// the runner ogo test generates over it, as OctoGo, built for the host shim.
type goTestOracle struct {
	src            string
	goTest, cc     string
	shim           string
	tests, benches []string
	mu             sync.Mutex // serializes the builds, which share octogo's caches
}

func newGoTestOracle(t *testing.T, src string) *goTestOracle {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool")
	}
	o := &goTestOracle{src: src}
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			o.cc = p
			break
		}
	}
	if o.cc == "" {
		t.Skip("no C compiler found")
	}
	if o.shim, err = filepath.Abs(filepath.Join("..", "octogo", "testdata", "hostp2")); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module x\n\ngo 1.24\n")
	write(t, filepath.Join(dir, "x_test.go"), "package x\n\n"+src)
	o.goTest = filepath.Join(dir, "x.test")
	cmd := exec.Command(goTool, "test", "-c", "-o", o.goTest, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test -c: %v\n%s", err, out)
	}

	pkg, err := octogo.Build(-1, []string{"x_test.ogo"}, fstest.MapFS{"x_test.ogo": &fstest.MapFile{Data: []byte(src)}})
	if err != nil {
		t.Fatal(err)
	}
	if o.tests, err = octogo.TestFuncs(pkg); err != nil {
		t.Fatal(err)
	}
	if o.benches, err = octogo.BenchFuncs(pkg); err != nil {
		t.Fatal(err)
	}
	if !octogo.MayRunSubtests(pkg) {
		t.Fatal("the premise: the package may run subtests")
	}
	return o
}

// compare holds what the runner runs and prints of -run run and -bench bench to
// what go test does.
func (o *goTestOracle) compare(t *testing.T, run, bench string) {
	want := o.goOut(run, bench)
	if os.Getenv("OGO_FILTER_SHOW") != "" {
		t.Logf("go test:\n%s", want)
	}
	if got := o.ogoOut(t, run, bench); got != want {
		t.Errorf("-run %q -bench %q\ngot:\n%s\nwant (go test):\n%s", run, bench, got, want)
	}
}

func (o *goTestOracle) goOut(run, bench string) string {
	args := []string{"-test.v", "-test.count=1", "-test.cpu=1", "-test.run=" + run}
	if bench != "" {
		args = append(args, "-test.bench="+bench, "-test.benchtime=1x")
	}
	out, _ := exec.Command(o.goTest, args...).CombinedOutput()
	return normalizeTestOut(string(out))
}

func (o *goTestOracle) ogoOut(t *testing.T, run, bench string) string {
	runF, err := octogo.CompileTestFilter("-run", run)
	if err != nil {
		t.Fatal(err)
	}
	var benchF *octogo.TestFilter
	bs := []string(nil)
	if bench != "" {
		if benchF, err = octogo.CompileTestFilter("-bench", bench); err != nil {
			t.Fatal(err)
		}
		bs = selected(o.benches, benchF)
	}
	ts := selected(o.tests, runF)
	if len(ts) == 0 && len(bs) == 0 {
		return "" // ogo test says there is nothing to run and builds nothing
	}
	runner := testRunnerSrc(ts, bs, benchRun{count: 1}, runF, benchF, true)
	fsys := fstest.MapFS{
		"x_test.ogo":   &fstest.MapFile{Data: []byte(o.src)},
		testRunnerFile: &fstest.MapFile{Data: []byte(runner)},
		"main.ogo":     &fstest.MapFile{Data: []byte("func main() { ogoTestMain() }\n")},
	}
	o.mu.Lock()
	pkg, err := octogo.Build(-1, []string{"main.ogo", "x_test.ogo", testRunnerFile}, fsys)
	var buf bytes.Buffer
	if err == nil {
		err = octogo.EmitC(pkg, &buf, octogo.Checked(), octogo.Inline())
	}
	o.mu.Unlock()
	if err != nil {
		t.Fatalf("%v\n%s", err, runner)
	}
	tmp := t.TempDir()
	csrc := filepath.Join(tmp, "main.c")
	if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(tmp, "prog")
	if out, err := exec.Command(o.cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra", "-Wno-unused-function", "-Wno-format",
		"-I", o.shim, "-o", bin, csrc, "-lpthread", "-lm").CombinedOutput(); err != nil || len(bytes.TrimSpace(out)) != 0 {
		t.Fatalf("cc: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	return normalizeTestOut(string(out))
}

var (
	goTestTime = regexp.MustCompile(` \(\d+\.\d+s\)$`)
	benchLine  = regexp.MustCompile(`^(Benchmark\S*?)(\s|$)`)
)

// normalizeTestOut keeps what -run and -bench decide of go test -v's output and the
// runner's: each RUN line and each result line, without go test's times, and of a
// benchmark its name and whether its line is a result.
func normalizeTestOut(out string) string {
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimLeft(line, " ")
		switch {
		case strings.HasPrefix(line, "=== RUN"):
			b.WriteString(line + "\n")
		case strings.HasPrefix(trimmed, "--- PASS:"), strings.HasPrefix(trimmed, "--- FAIL:"), strings.HasPrefix(trimmed, "--- SKIP:"):
			b.WriteString(goTestTime.ReplaceAllString(line, "") + "\n")
		case benchLine.MatchString(line):
			m := benchLine.FindStringSubmatch(line)
			if strings.Contains(line, "\t") {
				b.WriteString(m[1] + " result\n")
			} else {
				b.WriteString(m[1] + "\n")
			}
		}
	}
	return b.String()
}
