// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"modernc.org/ogo/internal/octogo"
)

// filterSrc is a package of tests and benchmarks, OctoGo and Go alike, whose
// subtests' names are the shapes a -run pattern meets: a name used twice, an empty
// one, spaces and control characters go test rewrites, a slash in a name -- a level
// of its own -- a capital, a letter beyond ASCII, and three levels.
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
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool")
	}
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found")
	}
	shim, err := filepath.Abs(filepath.Join("..", "octogo", "testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module x\n\ngo 1.24\n")
	write(t, filepath.Join(dir, "x_test.go"), "package x\n\n"+filterSrc)
	goTest := filepath.Join(dir, "x.test")
	cmd := exec.Command(goTool, "test", "-c", "-o", goTest, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test -c: %v\n%s", err, out)
	}

	pkg, err := octogo.Build(-1, []string{"x_test.ogo"}, fstest.MapFS{"x_test.ogo": &fstest.MapFile{Data: []byte(filterSrc)}})
	if err != nil {
		t.Fatal(err)
	}
	tests, err := octogo.TestFuncs(pkg)
	if err != nil {
		t.Fatal(err)
	}
	benches, err := octogo.BenchFuncs(pkg)
	if err != nil {
		t.Fatal(err)
	}

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
		{run: "^$", bench: "BenchmarkS/x"},
		{run: "^$", bench: "BenchmarkS/"},
		{run: "^$", bench: "BenchmarkS/x/deep"},
		{run: "^$", bench: "BenchmarkS/y"},
		{run: "^$", bench: "BenchmarkS/y#01"},
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

	goOut := func(c tc) string {
		args := []string{"-test.v", "-test.count=1", "-test.cpu=1", "-test.run=" + c.run}
		if c.bench != "" {
			args = append(args, "-test.bench="+c.bench, "-test.benchtime=1x")
		}
		out, _ := exec.Command(goTest, args...).CombinedOutput()
		return normalizeTestOut(string(out))
	}

	var mu sync.Mutex // serializes the builds, which share octogo's caches
	ogoOut := func(t *testing.T, c tc) string {
		runF, err := octogo.CompileTestFilter("-run", c.run)
		if err != nil {
			t.Fatal(err)
		}
		var benchF *octogo.TestFilter
		bs := []string(nil)
		if c.bench != "" {
			if benchF, err = octogo.CompileTestFilter("-bench", c.bench); err != nil {
				t.Fatal(err)
			}
			bs = selected(benches, benchF)
		}
		ts := selected(tests, runF)
		if len(ts) == 0 && len(bs) == 0 {
			return "" // ogo test says there is nothing to run and builds nothing
		}
		runner := testRunnerSrc(ts, bs, benchRun{count: 1}, runF, benchF)
		fsys := fstest.MapFS{
			"x_test.ogo":   &fstest.MapFile{Data: []byte(filterSrc)},
			testRunnerFile: &fstest.MapFile{Data: []byte(runner)},
			"main.ogo":     &fstest.MapFile{Data: []byte("func main() { ogoTestMain() }\n")},
		}
		mu.Lock()
		pkg, err := octogo.Build(-1, []string{"main.ogo", "x_test.ogo", testRunnerFile}, fsys)
		var buf bytes.Buffer
		if err == nil {
			err = octogo.EmitC(pkg, &buf, octogo.Checked(), octogo.Inline())
		}
		mu.Unlock()
		if err != nil {
			t.Fatalf("%v\n%s", err, runner)
		}
		tmp := t.TempDir()
		csrc := filepath.Join(tmp, "main.c")
		if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(tmp, "prog")
		if out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra", "-Wno-unused-function", "-Wno-format",
			"-I", shim, "-o", bin, csrc, "-lpthread", "-lm").CombinedOutput(); err != nil || len(bytes.TrimSpace(out)) != 0 {
			t.Fatalf("cc: %v\n%s", err, out)
		}
		out, err := exec.Command(bin).CombinedOutput()
		if err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		return normalizeTestOut(string(out))
	}

	for _, c := range cases {
		name := c.run
		if c.bench != "" {
			name += " -bench " + c.bench
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := goOut(c)
			if os.Getenv("OGO_FILTER_SHOW") != "" {
				t.Logf("go test:\n%s", want)
			}
			if got := ogoOut(t, c); got != want {
				t.Errorf("-run %q -bench %q\ngot:\n%s\nwant (go test):\n%s", c.run, c.bench, got, want)
			}
		})
	}
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
