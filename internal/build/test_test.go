// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modernc.org/ogo/internal/octogo"
)

const (
	testPkgSrc = `type Ring struct {
	buf  [4]int
	head int
	n    int
}

func (r *Ring) Push(v int) bool {
	if r.n == len(r.buf) {
		return false
	}
	r.buf[(r.head+r.n)%len(r.buf)] = v
	r.n++
	return true
}

func (r *Ring) Pop() (int, bool) {
	if r.n == 0 {
		return 0, false
	}
	v := r.buf[r.head]
	r.head = (r.head + 1) % len(r.buf)
	r.n--
	return v, true
}

func main() { println("the program, not the tests") }
`

	testPkgTestSrc = `import "testing"

func TestPushPop(t *testing.T) {
	var r Ring
	r.Push(1)
	if v, ok := r.Pop(); !ok || v != 1 {
		t.Errorf("pop: %d, %v; want 1 true", v, ok)
	}
	if _, ok := r.Pop(); ok {
		t.Fatal("pop of an empty ring")
	}
	t.Log("done", t.Name())
}

func TestSkipped(t *testing.T) { t.Skip() }

// notATest is not run: the name does not begin with Test.
func notATest(t *testing.T) { t.Fail() }

// Testfoo is not run either: "Test" followed by a lowercase letter is not a test's
// name, in Go's rule, and "go test" would not run it.
func Testfoo(t *testing.T) { t.Fail() }
`
)

// TestOgoTestCompiles drives `ogo test -c` over a package with tests: it discovers
// the tests, generates the runner, and takes the whole thing through the checker
// and the backend. It stops short of the board, which is where a RESULT would come
// from -- there is deliberately no host mode (see Test) -- so what this asserts is
// that the machinery builds, which is exactly what -c claims.
func TestOgoTestCompiles(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(dir, "ring_test.ogo"), testPkgTestSrc)

	var out, errb bytes.Buffer
	code, err := Test([]string{"-c", dir}, nil, &out, &errb)
	if err != nil || code != 0 {
		t.Fatalf("ogo test -c: code=%d err=%v\nstdout:\n%s\nstderr:\n%s", code, err, out.String(), errb.String())
	}
	// Two tests: TestPushPop and TestSkipped. notATest and Testfoo are not ones,
	// so neither is counted.
	if got := out.String(); !strings.Contains(got, "built 2 tests") {
		t.Fatalf("expected 2 tests, got:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(dir)+".test.binary")); err != nil {
		t.Fatalf("no test binary: %v", err)
	}
}

// TestOgoTestRefusesMalformedTest: a function named as a test over a signature that
// is not a test's is an error in vet's words, as it is under "go test", rather than
// a function quietly never run.
func TestOgoTestRefusesMalformedTest(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(dir, "ring_test.ogo"), `import "testing"

func TestPushPop(t *testing.T) {}

// TestX has a result, which a test does not.
func TestX(t *testing.T) int { return 0 }
`)
	var out, errb bytes.Buffer
	code, err := Test([]string{"-c", dir}, nil, &out, &errb)
	if code == 0 || err == nil {
		t.Fatalf("ogo test -c accepted a malformed test: code=%d err=%v", code, err)
	}
	if want := "ring_test.ogo:6:6: wrong signature for TestX, must be: func TestX(t *testing.T)"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q, want one containing %q", err, want)
	}
}

// TestOgoTestPattern: `ogo test ./...` tests every package under a root, in path
// order, and reports each one -- which is how a program of several packages is
// tested in one command. testdata and the dot- and underscore-prefixed directories
// are not packages.
func TestOgoTestPattern(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ring", "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(root, "ring", "ring_test.ogo"), testPkgTestSrc)
	write(t, filepath.Join(root, "plain", "plain.ogo"), "func Nothing() int { return 0 }\n")
	write(t, filepath.Join(root, "testdata", "skipme.ogo"), "not ogo at all\n")
	write(t, filepath.Join(root, "_scratch", "skipme.ogo"), "not ogo at all\n")

	var out, errb bytes.Buffer
	code, err := Test([]string{"-c", filepath.Join(root, "...")}, nil, &out, &errb)
	if err != nil || code != 0 {
		t.Fatalf("ogo test -c ./...: code=%d err=%v\nstdout:\n%s\nstderr:\n%s", code, err, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{"plain\t[no test files]", "ring\t[built 2 tests, not run]"} {
		if !strings.Contains(got, want) {
			t.Errorf("output:\n%s\nwant a line containing %q", got, want)
		}
	}
	if strings.Contains(got, "testdata") || strings.Contains(got, "_scratch") {
		t.Errorf("output:\n%s\nwant no testdata or _scratch package", got)
	}
	// Path order, so a run reads the same way twice.
	if i, j := strings.Index(got, "plain"), strings.Index(got, "ring"); i > j {
		t.Errorf("output:\n%s\nwant plain before ring", got)
	}
}

// TestOgoTestPatternMatchesNothing: a pattern that matches no package is an error,
// not a silent success -- a run that tested nothing must not read as one that
// passed.
func TestOgoTestPatternMatchesNothing(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	code, err := Test([]string{"-c", filepath.Join(root, "...")}, nil, &out, &out)
	if code == 0 || err == nil {
		t.Fatalf("an empty tree passed: code=%d err=%v out=%s", code, err, out.String())
	}
	if want := "matched no packages"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q, want one containing %q", err, want)
	}
}

// TestOgoTestRunSelects: -run compiles and runs only the tests whose name matches,
// as `go test -run` does. The selection is made where the runner is generated, so
// what is not selected is not built either -- which is what the count reports.
func TestOgoTestRunSelects(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(dir, "ring_test.ogo"), testPkgTestSrc)

	for _, tc := range []struct{ pattern, want string }{
		{"PushPop", "built 1 test,"},
		{"Push|Skip", "built 2 tests,"},
		{"^TestSkipped$", "built 1 test,"},
	} {
		var out, errb bytes.Buffer
		code, err := Test([]string{"-c", "-run", tc.pattern, dir}, nil, &out, &errb)
		if err != nil || code != 0 {
			t.Fatalf("-run %q: code=%d err=%v\nstdout:\n%s\nstderr:\n%s", tc.pattern, code, err, out.String(), errb.String())
		}
		if got := out.String(); !strings.Contains(got, tc.want) {
			t.Errorf("-run %q: output:\n%s\nwant a line containing %q", tc.pattern, got, tc.want)
		}
	}
	// The equals form, which is how the flag is usually written.
	var out, errb bytes.Buffer
	if code, err := Test([]string{"-c", "-run=PushPop", dir}, nil, &out, &errb); err != nil || code != 0 {
		t.Fatalf("-run=PushPop: code=%d err=%v out=%s", code, err, out.String())
	}
	if got := out.String(); !strings.Contains(got, "built 1 test,") {
		t.Errorf("-run=PushPop: output:\n%s", got)
	}
}

// TestOgoTestRunMatchesNothing: a pattern selecting no test says so, as `go test`
// says it. The package still passes -- Go's exit status for it is 0 -- so the
// warning is what keeps a mistyped pattern from reading as a green run.
func TestOgoTestRunMatchesNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(dir, "ring_test.ogo"), testPkgTestSrc)

	var out, errb bytes.Buffer
	code, err := Test([]string{"-c", "-run", "Nope", dir}, nil, &out, &errb)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v out=%s", code, err, out.String())
	}
	if want := "no tests to run"; !strings.Contains(errb.String(), want) && !strings.Contains(out.String(), want) {
		t.Fatalf("stdout:\n%s\nstderr:\n%s\nwant %q", out.String(), errb.String(), want)
	}
}

// TestOgoTestBenchSelection: a benchmark is built and run only where -bench asks
// for it, as go test runs one -- a package of benchmarks alone has nothing to run
// without it -- and -bench selects by an unanchored regular expression, as -run
// does. A benchmark of the wrong signature is refused whether or not -bench asks.
func TestOgoTestBenchSelection(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(dir, "ring_test.ogo"), testPkgTestSrc+`
func BenchmarkPush(b *testing.B) {
	var r Ring
	for i := 0; i < b.N; i++ {
		r.Push(i)
	}
}

func BenchmarkOther(b *testing.B) {}
`)
	only := filepath.Join(t.TempDir(), "only")
	write(t, filepath.Join(only, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(only, "ring_test.ogo"), "import \"testing\"\n\nfunc BenchmarkNop(b *testing.B) {}\n")
	bad := filepath.Join(t.TempDir(), "bad")
	write(t, filepath.Join(bad, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(bad, "ring_test.ogo"), "import \"testing\"\n\nfunc BenchmarkBad(t *testing.T) {}\n")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", dir}, "[built 2 tests, not run]"},
		{[]string{"-c", "-bench", ".", dir}, "[built 2 tests and 2 benchmarks, not run]"},
		{[]string{"-c", "-bench=Push", dir}, "[built 2 tests and 1 benchmark, not run]"},
		{[]string{"-c", "-bench", "Nope", dir}, "[built 2 tests, not run]"},
		{[]string{"-c", "-run", "^$", "-bench", "Push", dir}, "[built 0 tests and 1 benchmark, not run]"},
		{[]string{"-c", only}, "[no tests to run]"},
		{[]string{"-c", "-bench", ".", only}, "[built 0 tests and 1 benchmark, not run]"},
	} {
		var out, errb bytes.Buffer
		code, err := Test(tc.args, nil, &out, &errb)
		if err != nil || code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Errorf("%v: code=%d err=%v\n%s%s\nwant %q", tc.args, code, err, out.String(), errb.String(), tc.want)
		}
	}
	var out bytes.Buffer
	if code, err := Test([]string{"-c", bad}, nil, &out, &out); code != 1 || err == nil || !strings.Contains(err.Error(), "wrong signature for BenchmarkBad, must be: func BenchmarkBad(b *testing.B)") {
		t.Errorf("a malformed benchmark: code=%d err=%v", code, err)
	}
	if code, err := Test([]string{"-c", "-bench", "[", dir}, nil, &out, &out); code != 2 || err == nil || !strings.Contains(err.Error(), "bad -bench pattern") {
		t.Errorf("a bad -bench pattern: code=%d err=%v", code, err)
	}
}

// TestOgoTestRunBadPattern: the pattern is a regular expression, and one that does
// not compile is reported as the usage error it is rather than matching nothing.
func TestOgoTestRunBadPattern(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)

	var out bytes.Buffer
	code, err := Test([]string{"-c", "-run", "[", dir}, nil, &out, &out)
	if code != 2 || err == nil {
		t.Fatalf("a bad pattern was accepted: code=%d err=%v out=%s", code, err, out.String())
	}
	if want := "bad -run pattern"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q, want one containing %q", err, want)
	}
}

// TestOgoTestRunSubtestPattern: a -run pattern with a slash selects subtests, as in
// go test -- the test by the level before the slash, here, and its subtests by the
// rest, on the board -- and builds; a level that is no regular expression, or one
// past what the board's machine runs, is refused naming the level.
func TestOgoTestRunSubtestPattern(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(dir, "ring_test.ogo"), testPkgTestSrc)

	var out bytes.Buffer
	code, err := Test([]string{"-c", "-run", "TestPushPop/one", dir}, nil, &out, &out)
	if err != nil || code != 0 {
		t.Fatalf("ogo test -c -run TestPushPop/one: code=%d err=%v\n%s", code, err, out.String())
	}
	if got := out.String(); !strings.Contains(got, "built 1 test,") {
		t.Fatalf("expected 1 test, got:\n%s", got)
	}
	for _, c := range []struct{ pat, want string }{
		{"TestPushPop/(", `bad -run pattern "TestPushPop/(": element 1 ("("): error parsing regexp: missing closing )`},
		{"x|TestPushPop/(", `bad -run pattern "x|TestPushPop/(": alternative 1, element 1 ("(")`},
		{"TestPushPop/a{300}", `element 1 ("a{300}"): compiles to 302 instructions, more than the 256 the board's matcher runs`},
		{strings.Repeat("a|", 32) + "a", "more than 32 alternatives"},
	} {
		out.Reset()
		code, err := Test([]string{"-c", "-run", c.pat, dir}, nil, &out, &out)
		if code != 2 || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("-run %q: code=%d err=%v, want one containing %q", c.pat, code, err, c.want)
		}
	}
}

// TestRunnerSrcFilters pins the runner a pattern naming subtests or sub-benchmarks
// makes: its program in a package array, SetFilter ahead of each test and
// benchmark with the alternatives its name matched, a benchmark matched only in
// part run for its sub-benchmarks alone, and the tests' filter taken back before
// benchmarks run under no such pattern. A pattern of one level makes the runner it
// made before.
func TestRunnerSrcFilters(t *testing.T) {
	mk := func(flag, pat string) *octogo.TestFilter {
		f, err := octogo.CompileTestFilter(flag, pat)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	plain := testRunnerSrc([]string{"TestA"}, []string{"BenchmarkA"}, benchRun{}, nil, nil, false)
	if got := testRunnerSrc([]string{"TestA"}, []string{"BenchmarkA"}, benchRun{}, mk("-run", "TestA|TestB"), mk("-bench", "A"), false); got != plain {
		t.Errorf("patterns of one level changed the runner:\n%s", got)
	}
	got := testRunnerSrc([]string{"TestA", "TestB"}, []string{"BenchmarkA"}, benchRun{}, mk("-run", "TestA/x|TestB"), nil, false)
	for _, want := range []string{
		"var ogoRunFilter = [...]int32{\n\t2, ",
		"var ogoFilterState [512]int32\n",
		"\ttesting.SetFilter(ogoRunFilter[:], ogoFilterState[:], 1, testing.MatchSub)\n\ttesting.RunTest(&t0, \"TestA\", TestA)\n",
		"\ttesting.SetFilter(ogoRunFilter[:], ogoFilterState[:], 2, testing.MatchSub)\n\ttesting.RunTest(&t1, \"TestB\", TestB)\n",
		"\ttesting.SetFilter(nil, nil, 0, nil)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the runner lacks %q:\n%s", want, got)
		}
	}
	got = testRunnerSrc(nil, []string{"BenchmarkA", "BenchmarkB"}, benchRun{}, nil, mk("-bench", "A$/x|B$"), false)
	for _, want := range []string{
		"\ttesting.SetFilter(ogoBenchFilter[:], ogoFilterState[:], 1, testing.MatchSub)\n\ttesting.OnlySubs(&b0)\n\ttesting.RunBenchmark(&b0, ",
		"\ttesting.SetFilter(ogoBenchFilter[:], ogoFilterState[:], 2, testing.MatchSub)\n\ttesting.RunBenchmark(&b1, ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the runner lacks %q:\n%s", want, got)
		}
	}
}

// TestOgoTestNoFiles: a package with no _test.ogo is not an error, as in Go.
func TestOgoTestNoFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)

	var out bytes.Buffer
	code, err := Test([]string{"-c", dir}, nil, &out, &out)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v out=%s", code, err, out.String())
	}
	if got := out.String(); !strings.Contains(got, "no test files") {
		t.Fatalf("got:\n%s", got)
	}
}

// TestOgoTestNoFilesStillChecks: a package with no tests is compiled anyway, so a
// tree where something does not build cannot report every package "ok".
func TestOgoTestNoFilesStillChecks(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ring", "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(root, "ring", "ring_test.ogo"), testPkgTestSrc)
	write(t, filepath.Join(root, "broken", "broken.ogo"), "func Bad() int { return \"x\" }\n")

	var out, errb bytes.Buffer
	code, err := Test([]string{"-c", filepath.Join(root, "...")}, nil, &out, &errb)
	if code == 0 {
		t.Fatalf("a tree with a package that does not build passed: err=%v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
	}
	if got := out.String(); !strings.Contains(got, "FAIL\tbroken") && !strings.Contains(got, "FAIL\t"+filepath.Join(root, "broken")) {
		t.Errorf("output:\n%s\nwant a FAIL line for the broken package", got)
	}
	// The package that does build is still tested: one bad package does not stop
	// the run.
	if got := out.String(); !strings.Contains(got, "built 2 tests") {
		t.Errorf("output:\n%s\nwant the other package still built", got)
	}
}

// TestOgoTestReportsCheckerErrors: a test file that does not compile is reported as
// itself, not as a missing test.
func TestOgoTestReportsCheckerErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(dir, "ring_test.ogo"), `import "testing"

func TestBroken(t *testing.T) { t.Nope() }
`)

	var out bytes.Buffer
	code, err := Test([]string{"-c", dir}, nil, &out, &out)
	if err == nil || code == 0 {
		t.Fatalf("expected a checker error, got code=%d err=%v out=%s", code, err, out.String())
	}
	if !strings.Contains(err.Error(), "Nope") {
		t.Fatalf("expected the error to name the method, got %v", err)
	}
}

// TestRunnerSrc pins the shape of the generated runner: one testing.T per test, so
// a failure in one does not mark the next, each run through testing.RunTest, which
// FailNow returns to and which prints Go's RUN and result lines -- asking about the
// failure before the skip, as Go asks (the run case "subtests run by t.Run" holds
// that) -- and every failure counted.
func TestRunnerSrc(t *testing.T) {
	got := testRunnerSrc([]string{"TestA", "TestB"}, nil, benchRun{}, nil, nil, false)
	if strings.Contains(got, "Benchmark") {
		t.Errorf("benchmarks in a runner without any:\n%s", got)
	}
	if strings.Contains(got, "ogoNotPrint") || strings.Contains(got, "SetSubtests") {
		t.Errorf("a runner of a program running no subtest hands in their names:\n%s", got)
	}
	// A program that may run a subtest is handed what naming one takes: the table
	// of the runes that do not print, the runner's, and FullName, before any test.
	subs := testRunnerSrc([]string{"TestA", "TestB"}, nil, benchRun{}, nil, nil, true)
	if !strings.HasPrefix(subs, "// Code generated by ogo test. DO NOT EDIT.\n\nimport \"testing\"\n\nvar ogoNotPrint = [...]uint8{\n\t") ||
		!strings.Contains(subs, "func ogoTestMain() {\n\tfailed := 0\n\ttesting.SetSubtests(ogoNotPrint[:], testing.FullName)\n\n\tvar t0 testing.T\n") {
		t.Errorf("a runner of a program running subtests:\n%s", subs)
	}
	for _, want := range []string{
		`import "testing"`,
		"func ogoTestMain() {",
		"var t0 testing.T",
		`testing.RunTest(&t0, "TestA", TestA)`,
		"if t0.Failed() {\n\t\tfailed++",
		"var t1 testing.T",
		`testing.RunTest(&t1, "TestB", TestB)`,
		"if t1.Failed() {\n\t\tfailed++",
		`println("FAIL", failed, "of", 2)`,
		testDoneLine,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestRunnerSrcBenchmarks pins the benchmarks' part of the runner: the labels and
// how they run set once, ahead of them and after the tests, the names padded to one
// more than the longest, one testing.B each, and each failure counted with the
// tests'.
func TestRunnerSrcBenchmarks(t *testing.T) {
	got := testRunnerSrc([]string{"TestA"}, []string{"BenchmarkA", "BenchmarkLonger"}, benchRun{
		labels: benchLabels("pkg/x", 180000000),
		time:   2 * time.Second,
		mem:    true,
	}, nil, nil, false)
	for _, want := range []string{
		`testing.RunTest(&t0, "TestA", TestA)`,
		`testing.StartBenchmarks("pkg: pkg/x\ncpu: Propeller 2 at 180 MHz\n", 16, 2000000000, 0, true)`,
		"var b0 testing.B",
		`testing.RunBenchmark(&b0, "BenchmarkA", BenchmarkA)`,
		"if b0.Failed() {\n\t\tfailed++",
		`testing.RunBenchmark(&b1, "BenchmarkLonger", BenchmarkLonger)`,
		`println("FAIL", failed, "of", 3)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "RunTest") > strings.Index(got, "StartBenchmarks") {
		t.Errorf("the benchmarks run before the tests:\n%s", got)
	}
	if got := benchLabels("d", 160000500); got != "pkg: d\ncpu: Propeller 2 at 160000500 Hz\n" {
		t.Errorf("labels %q", got)
	}
	if got := benchPkg("x/sensor", "sensor", "example.com/proj"); got != "example.com/proj/sensor" {
		t.Errorf("module package %q", got)
	}
	if got := benchPkg("../a/b", ".", ""); got != "b" {
		t.Errorf("package outside a module %q", got)
	}
}

// TestParseBenchTime pins -benchtime as go test reads it: a duration, or a count
// of runs with an x after it, and neither zero nor negative.
func TestParseBenchTime(t *testing.T) {
	for _, c := range []struct {
		in    string
		d     time.Duration
		n     int
		valid bool
	}{
		{"1s", time.Second, 0, true},
		{"250ms", 250 * time.Millisecond, 0, true},
		{"2m", 2 * time.Minute, 0, true},
		{"100x", 0, 100, true},
		{"1x", 0, 1, true},
		{"0x", 0, 0, false},
		{"-1x", 0, 0, false},
		{"0s", 0, 0, false},
		{"-1s", 0, 0, false},
		{"x", 0, 0, false},
		{"fast", 0, 0, false},
		{"2000000000x", 0, 0, false},
	} {
		d, n, err := parseBenchTime(c.in)
		if (err == nil) != c.valid || d != c.d || n != c.n {
			t.Errorf("%q: %v %v %v", c.in, d, n, err)
		}
	}
}

// TestCaptureTimeout pins how long the board is given: two minutes and, for each
// benchmark, three times its time and a minute -- ten minutes for a count of runs --
// unless -timeout says, 0 there being no limit; and -timeout is go test's duration.
func TestCaptureTimeout(t *testing.T) {
	for _, c := range []struct {
		benches int
		opts    testOptions
		want    time.Duration
	}{
		{0, testOptions{benchTime: time.Second}, 2 * time.Minute},
		{2, testOptions{benchTime: time.Second}, 2*time.Minute + 2*(3*time.Second+time.Minute)},
		{3, testOptions{benchCount: 5}, 32 * time.Minute},
		{3, testOptions{benchTime: time.Second, timeout: 90 * time.Second, timeoutSet: true}, 90 * time.Second},
		{3, testOptions{benchTime: time.Second, timeoutSet: true}, math.MaxInt64},
	} {
		if got := captureTimeout(c.benches, c.opts); got != c.want {
			t.Errorf("%d %+v: %v, want %v", c.benches, c.opts, got, c.want)
		}
	}
	dir := t.TempDir()
	for _, arg := range []string{"-timeout=-1s", "-timeout=soon"} {
		var out bytes.Buffer
		if code, err := Test([]string{"-c", arg, dir}, nil, &out, &out); code != 2 || err == nil || !strings.Contains(err.Error(), "invalid -timeout") {
			t.Errorf("%s: %d %v", arg, code, err)
		}
	}
	var out bytes.Buffer
	if code, err := Test([]string{"-c", "-timeout"}, nil, &out, &out); code != 2 || err == nil || !strings.Contains(err.Error(), "-timeout requires an argument") {
		t.Errorf("-timeout alone: %d %v", code, err)
	}
}

func write(t *testing.T, path, src string) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExamplesBuild builds every program under _examples with the real backend.
// They are the code a reader meets first, so a language change that breaks one has
// to fail here rather than in a photograph.
func TestExamplesBuild(t *testing.T) {
	des, err := os.ReadDir("../../_examples")
	if err != nil {
		t.Skip("no _examples")
	}
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		t.Run(de.Name(), func(t *testing.T) {
			dir := filepath.Join("../../_examples", de.Name())
			out := filepath.Join(t.TempDir(), de.Name()+".binary")
			var buf bytes.Buffer
			code, err := Build([]string{"-o", out, dir}, nil, &buf, &buf)
			if err != nil || code != 0 {
				t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
			}
			// The backend warns where it should refuse, so any output from a
			// SUCCESSFUL build is treated as a failure -- the same rule the
			// on-board suite applies.
			if s := strings.TrimSpace(buf.String()); s != "" {
				t.Fatalf("backend was not silent:\n%s", s)
			}
		})
	}
}

// TestOgoTestFlags: what `ogo help test` names is taken. --gostack was in the usage
// line and refused by the parser, "unknown flag"; --allow-backend-warnings is
// ogo build's, for a test binary the C backend warns about.
func TestOgoTestFlags(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ring.ogo"), testPkgSrc)
	write(t, filepath.Join(dir, "ring_test.ogo"), testPkgTestSrc)
	for _, test := range []struct {
		args []string
		want string // "" for a build that succeeds
	}{
		{[]string{"--gostack", "512"}, ""},
		{[]string{"--allow-backend-warnings"}, ""},
		{[]string{"--gostack", "5"}, "must be between"},
		{[]string{"--gostack"}, "wants a number of longs"}, // the directory is taken for its value
	} {
		var out bytes.Buffer
		code, err := Test(append(append([]string{"-c"}, test.args...), dir), nil, &out, &out)
		switch {
		case test.want == "" && (err != nil || code != 0):
			t.Errorf("%v: code=%d err=%v\n%s", test.args, code, err, out.String())
		case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
			t.Errorf("%v: err=%v, want one containing %q", test.args, err, test.want)
		}
	}
}
