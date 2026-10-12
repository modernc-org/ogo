// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
		// A subtest's name is kept to the end of the test: a constant, or what
		// lives as long; one viewing a local buffer is refused where it is passed.
		{`t.Run("x", func(t *testing.T) {})`, ""},
		{`var buf [8]byte
	b := NewBuilder(buf[:])
	b.WriteString("x")
	t.Run(b.String(), func(t *testing.T) {})`, "cannot pass b.String(), which holds a pointer into local buf to Run: its parameter 1 is stored where it outlives every frame"},
		{`t.Run("x", 5)`, "cannot use 5 (untyped int constant) as func(*testing.T) value in argument to Run"},
		{`ok := t.Run("x", func(t *testing.T) {})
	var n int = ok
	_ = n`, "cannot use ok"},
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

// TestBenchmarkRefusals: the testing package's rules as a benchmark meets them --
// a *testing.B's formatting methods are a print's as a T's are, deferred or started
// they are refused with the literal that does it, a metric's unit is kept to the end
// of the benchmark as a subtest's name is, and a method Go's B has and this one
// does not yet, Run, is missing as any method is.
func TestBenchmarkRefusals(t *testing.T) {
	for _, test := range []struct {
		body, want string // want "" for a body that builds
	}{
		{`b.Errorf("got %d, want %d", 1, 2)`, ""},
		{`b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		b.StartTimer()
	}
	b.SetBytes(64)
	b.ReportAllocs()
	b.ReportMetric(1.5, "clk/op")
	b.Log(b.Name(), b.Failed(), b.Skipped())`, ""},
		{`defer func(b *testing.B) { b.Log("x") }(b)`, ""},
		{`b.Logf(ff, 1)`, "Logf's format must be a constant string, as printf's is"},
		{`b.Fatalf("%d")`, "Fatalf: the format has 1 verb but 0 arguments given"},
		{`defer b.Log("x")`, "`defer b.Log(...)` is not supported yet; write `defer func(b *testing.B) { b.Log(...) }(b)`"},
		{`go b.Errorf("x %d", 1)`, "`go b.Errorf(...)` is not supported yet; write `go func(b *testing.B) { b.Errorf(...) }(b)`"},
		{`var buf [8]byte
	s := NewBuilder(buf[:])
	s.WriteString("u/op")
	b.ReportMetric(1, s.String())`, "cannot pass s.String(), which holds a pointer into local buf to ReportMetric"},
		{`b.Run("x", func(b *testing.B) {
		for b.Loop() {
		}
	})`, ""},
		{`b.Run(ff, func(b *testing.B) {})`, ""},
		{`var buf [8]byte
	s := NewBuilder(buf[:])
	s.WriteString("x")
	b.Run(s.String(), func(b *testing.B) {})`, "cannot pass s.String(), which holds a pointer into local buf to Run"},
		{`b.Run("x", nil)`, ""},
		{`b.Run("x", func(t *testing.T) {})`, "cannot use"},
		{`b.Run(1, func(b *testing.B) {})`, "cannot use"},
		{`for b.Loop() {
		b.Loop()
	}
	n := b.Loop()
	_ = n`, ""},
		{`b.Loop(1)`, "too many arguments"},
		{`var s string = b.Loop()
	_ = s`, "cannot use"},
		{`b.RunParallel(nil)`, "type testing.B has no method RunParallel"},
		{`b.Elapsed()`, "type testing.B has no method Elapsed"},
		{`var s string = b.N
	_ = s`, "cannot use b.N"},
		{`b.SetBytes(1.5)`, "1.5"},
	} {
		t.Run(test.body, func(t *testing.T) {
			src := "import \"testing\"\n\nvar ff = \"%d\"\n\nfunc bench(b *testing.B) {\n\t" + test.body + "\n}\n\nfunc main() {\n\tvar b testing.B\n\ttesting.StartBenchmarks(\"\", 2, 0, 1, false)\n\ttesting.RunBenchmark(&b, \"B\", bench)\n\t_ = ff\n}\n"
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

// TestOnBoardOgoTestBench runs `ogo test -bench` on the board over benchmarks whose
// operation takes a known time, a wait: each result line is go test's, its ns/op
// the wait's within what the loop around it costs, and its last run as long as
// -benchtime asked at least -- a b.N loop's, Loop's with its setup and cleanup
// untimed, and a sub-benchmark's either way. And with a count of runs at another
// clock, b.N is the count, the label says the clock built, and the time is still
// the wait's -- the clocks are converted at the rate the program runs at.
func TestOnBoardOgoTestBench(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	dir := filepath.Join(t.TempDir(), "waits")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"wait.ogo": "func Nop() {}\n",
		"wait_test.ogo": `import (
	"p2"
	"testing"
)

func TestNop(t *testing.T) { Nop() }

func BenchmarkWait1ms(b *testing.B) {
	for i := 0; i < b.N; i++ {
		p2.WaitMs(1)
	}
}

func BenchmarkWait10us(b *testing.B) {
	for i := 0; i < b.N; i++ {
		p2.WaitUs(10)
	}
}

func BenchmarkLoop1ms(b *testing.B) {
	p2.WaitMs(100)
	for b.Loop() {
		p2.WaitMs(1)
	}
	p2.WaitMs(100)
}

func BenchmarkSub(b *testing.B) {
	b.Run("10us", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			p2.WaitUs(10)
		}
	})
	b.Run("loop", func(b *testing.B) {
		for b.Loop() {
			p2.WaitUs(10)
		}
	})
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line := regexp.MustCompile(`(?m)^(Benchmark[\w/]+)( *)\t( *\d+)\t( *[\d.]+) ns/op$`)
	check := func(args []string, mhz, count int, wants ...float64) {
		for attempt := 1; ; attempt++ {
			cmd := exec.Command(ogo, append([]string{"test", "-p", port, "-run", "^$"}, args...)...)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			got := strings.ReplaceAll(string(out), "\r", "")
			bad := func(format string, v ...any) {
				if attempt == boardAttempts {
					t.Fatalf("ogo test %v: %v\n%s\n%s", args, err, fmt.Sprintf(format, v...), got)
				}
				t.Logf("retry %d/%d: %s", attempt, boardAttempts-1, fmt.Sprintf(format, v...))
			}
			if err != nil {
				bad("failed")
				continue
			}
			if want := fmt.Sprintf("pkg: waits\ncpu: Propeller 2 at %d MHz\nBenchmarkWait1ms\n", mhz); !strings.Contains(got, want) {
				bad("no labels %q", want)
				continue
			}
			ms := line.FindAllStringSubmatch(got, -1)
			if len(ms) != len(wants) {
				bad("%d result lines", len(ms))
				continue
			}
			ok := true
			for i, m := range ms {
				name, want := m[1], wants[i]
				n, _ := strconv.Atoi(strings.TrimSpace(m[3]))
				ns, _ := strconv.ParseFloat(strings.TrimSpace(m[4]), 64)
				// go test pads the name to one more than the longest, b.N to 8
				// places, and a time of 1000 ns or more to 10.
				if len(name)+len(m[2]) != len("BenchmarkWait10us")+1 || len(m[3]) != 8 || len(m[4]) != 10 {
					ok = false
				}
				switch {
				case ns < want || ns > want*1.01+1500: // the loop and the call cost ~100 clocks
					ok = false
				case count != 0 && n != count:
					ok = false
				case count == 0 && float64(n)*ns < 1e9:
					ok = false
				}
			}
			if !ok {
				bad("results out of bounds")
				continue
			}
			return
		}
	}
	// Loop's setup and cleanup, 100 ms each, are not timed, and a sub-benchmark is
	// timed as any benchmark is, whichever way it loops.
	check([]string{"-bench", "."}, 160, 0, 1e6, 1e4, 1e6, 1e4, 1e4)
	check([]string{"-bench", "Wait|Sub", "-benchtime", "7x", "--clock", "200MHz"}, 200, 7, 1e6, 1e4, 1e4, 1e4)
}
