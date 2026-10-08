// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestTestingRefusals: what the testing package's formatting methods refuse, each
// where it is written. Their arguments are a print's, not their declaration's `args
// ...any`, so a format is a constant string, as printf's is, and its verbs are
// counted against the arguments; a deferred or started call, which would have to
// be written where it runs, is refused with the literal that does it instead.
func TestTestingRefusals(t *testing.T) {
	for _, test := range []struct {
		body, want string // want "" for a body that builds
	}{
		{`t.Errorf("got %d, want %d", 1, 2)`, ""},
		{`t.Log("a", 1, true)`, ""},
		{`if t.Error("x"); true {
	}`, ""},
		{`for i := 0; i < 2; t.Log(i) {
		i++
	}`, ""},
		{`defer func(t *testing.T) { t.Log("x") }(t)`, ""},
		{`t.Errorf(ff, 1)`, "Errorf's format must be a constant string, as printf's is"},
		{`t.Errorf(5)`, "cannot use 5 (value of type int) as string value in argument to Errorf"},
		{`t.Logf()`, "not enough arguments in call to Logf: it takes a format"},
		{`t.Errorf("%d")`, "Errorf: the format has 1 verb but 0 arguments given"},
		{`t.Fatalf("%d", 1, 2)`, "Fatalf: the format has 1 verb but 2 arguments given"},
		{`var x int = t.Log("v")
	_ = x`, `t.Log("v") (no value) used as value`},
		{`defer t.Log("x")`, "`defer t.Log(...)` is not supported yet; write `defer func(t *testing.T) { t.Log(...) }(t)`"},
		{`go t.Errorf("x %d", 1)`, "`go t.Errorf(...)` is not supported yet; write `go func(t *testing.T) { t.Errorf(...) }(t)`"},
	} {
		t.Run(test.body, func(t *testing.T) {
			src := "import \"testing\"\n\nvar ff = \"%d\"\n\nfunc test(t *testing.T) {\n\t" + test.body + "\n}\n\nfunc main() {\n\tvar t testing.T\n\ttesting.RunTest(&t, \"T\", test)\n\t_ = ff\n}\n"
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked(), Inline())
			}
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}

// TestOnBoardOgoTest runs `ogo test` on the board over a package whose tests use the
// testing package as Go's are written: the output reads as go test -v's does, a
// message carries its file and line, Fatalf in a helper stops its test, Skip stops
// its, and a failure is the exit status.
func TestOnBoardOgoTest(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	dir := t.TempDir()
	for name, src := range map[string]string{
		"stack.ogo": `type Stack struct {
	buf [8]int
	n   int
}

func (s *Stack) Push(v int) bool {
	if s.n == len(s.buf) {
		return false
	}
	s.buf[s.n] = v
	s.n++
	return true
}

func (s *Stack) Pop() (int, bool) {
	if s.n == 0 {
		return 0, false
	}
	s.n--
	return s.buf[s.n], true
}
`,
		"stack_test.ogo": `import "testing"

func TestPushPop(t *testing.T) {
	var s Stack
	for i := 1; i <= 3; i++ {
		if !s.Push(i) {
			t.Fatalf("push %d failed", i)
		}
	}
	for want := 3; want >= 1; want-- {
		if got, ok := s.Pop(); !ok || got != want {
			t.Errorf("pop: got %d, %v; want %d, true", got, ok, want)
		}
	}
	t.Log("popped", 3, "values")
}

func TestFull(t *testing.T) {
	var s Stack
	for i := 0; i < 8; i++ {
		s.Push(i)
	}
	if s.Push(9) {
		t.Error("push into a full stack succeeded")
	}
	mustPop(t, &s, 8)
	t.Errorf("not reached: %s", t.Name())
}

func mustPop(t *testing.T, s *Stack, want int) {
	t.Helper()
	if got, _ := s.Pop(); got != want {
		t.Fatalf("pop: got %d, want %d", got, want)
	}
}

func TestSkipped(t *testing.T) {
	t.Skip("not on this board")
	t.Fail()
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const want = `=== RUN   TestPushPop
    stack_test.ogo:15: popped 3 values
--- PASS: TestPushPop
=== RUN   TestFull
    stack_test.ogo:33: pop: got 7, want 8
--- FAIL: TestFull
=== RUN   TestSkipped
    stack_test.ogo:38: not on this board
--- SKIP: TestSkipped
FAIL 1 of 3
`
	for attempt := 1; ; attempt++ {
		cmd := exec.Command(ogo, "test", "-p", port, ".")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		got := strings.ReplaceAll(string(out), "\r", "")
		exitErr, failed := err.(*exec.ExitError)
		if strings.Contains(got, want) && failed && exitErr.ExitCode() == 1 {
			return
		}
		if attempt == boardAttempts {
			t.Fatalf("ogo test: %v\ngot:\n%s\nwant it to contain:\n%s", err, got, want)
		}
		t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
	}
}
