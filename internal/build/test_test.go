// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		println("pop:", v, ok, "want 1 true")
		t.Fail()
	}
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
// a failure in one does not mark the next, and Go's own result lines.
func TestRunnerSrc(t *testing.T) {
	got := testRunnerSrc([]string{"TestA", "TestB"})
	for _, want := range []string{
		`import "testing"`,
		"func ogoTestMain() {",
		"var t0 testing.T",
		"TestA(&t0)",
		"var t1 testing.T",
		"TestB(&t1)",
		`println("--- PASS: TestA")`,
		`println("--- FAIL: TestB")`,
		`println("--- SKIP: TestB")`,
		testDoneLine,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
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
