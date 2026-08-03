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

// TestX is not run either: a test takes exactly one parameter and returns nothing.
func TestX(t *testing.T) int { return 0 }
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
	// Two tests: TestPushPop and TestSkipped. notATest is not one, and TestX has a
	// result, so neither is counted.
	if got := out.String(); !strings.Contains(got, "built 2 tests") {
		t.Fatalf("expected 2 tests, got:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(dir)+".test.binary")); err != nil {
		t.Fatalf("no test binary: %v", err)
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
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}
