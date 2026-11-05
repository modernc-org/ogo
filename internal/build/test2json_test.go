// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTest2JSONMatchesGo holds test2json to Go's: go test -v's output of a package
// converted is the events `go tool test2json -t` makes of the same text, but for
// their times. Two packages: passing, failing and skipped tests, subtests and logs,
// and one whose tests pass, so that its benchmarks run, with a sub-benchmark and a
// log. (go test -json itself runs the tests to report each result as it happens,
// where -v reports a test's subtests once it is done, which is what ogo test
// prints.)
func TestTest2JSONMatchesGo(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool")
	}
	for _, src := range []string{`package x

import "testing"

func TestPass(t *testing.T) { t.Log("a log line") }

func TestSub(t *testing.T) {
	t.Run("ok", func(t *testing.T) {})
	t.Run("bad", func(t *testing.T) { t.Error("wrong") })
	t.Run("skipped", func(t *testing.T) { t.Skip("not today") })
	t.Run("deep", func(t *testing.T) {
		t.Run("deeper", func(t *testing.T) { t.Log("down here") })
	})
}

func TestSkip(t *testing.T) { t.Skip("never") }

func TestFail(t *testing.T) { t.Fatal("stop") }
`, `package x

import "testing"

func TestPass(t *testing.T) { t.Run("sub", func(t *testing.T) { t.Log("in sub") }) }

func BenchmarkLoop(b *testing.B) {
	for i := 0; i < b.N; i++ {
	}
}

func BenchmarkSub(b *testing.B) {
	b.Run("one", func(b *testing.B) {})
	b.Log("logged")
}
`} {
		compareTest2JSON(t, goTool, src)
	}
}

func compareTest2JSON(t *testing.T, goTool, src string) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/x\n\ngo 1.24\n")
	write(t, filepath.Join(dir, "x_test.go"), src)
	run := func(args ...string) (string, int) {
		cmd := exec.Command(goTool, append([]string{"test", "-count=1"}, args...)...)
		cmd.Dir = dir
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}
	// The times are each converter's own, and so is the package's Elapsed, Go's
	// being how long its converter ran.
	type ev struct {
		Action, Package, Test, Output, OutputType string
		Elapsed                                   float64
	}
	normal := func(e testEvent) ev {
		r := ev{e.Action, e.Package, e.Test, e.Output, e.OutputType, -1}
		if e.Elapsed != nil && e.Test != "" {
			r.Elapsed = *e.Elapsed
		}
		return r
	}
	text, code := run("-v", "-bench", ".", "-benchtime", "1x", ".")
	if strings.Contains(src, "Benchmark") && !strings.Contains(text, "BenchmarkSub/one") {
		t.Fatalf("the benchmarks did not run:\n%s", text)
	}
	conv := exec.Command(goTool, "tool", "test2json", "-t", "-p", "example.com/x")
	conv.Stdin = strings.NewReader(text)
	ref, err := conv.Output()
	if err != nil {
		t.Fatalf("go tool test2json: %v", err)
	}
	var want []ev
	for _, line := range strings.Split(strings.TrimSpace(string(ref)), "\n") {
		var e testEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("go tool test2json: %v: %s", err, line)
		}
		want = append(want, normal(e))
	}
	var buf bytes.Buffer
	test2json(&buf, "example.com/x", text, code)
	var got []ev
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e testEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("test2json: %v: %s", err, line)
		}
		got = append(got, normal(e))
	}
	for i := 0; i < max(len(got), len(want)); i++ {
		var g, w ev
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("event %d:\ngot  %+v\nwant %+v", i, g, w)
		}
	}
}

// TestTestJSON runs `ogo test --json` where no board is needed: a package that
// does not build is its error as output and a fail, one with no test files a skip,
// and nothing goes to stderr.
func TestTestJSON(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "bad", "x.ogo"), "func F() int {\n\treturn missing\n}\n")
	write(t, filepath.Join(dir, "bad", "x_test.ogo"), "import \"testing\"\n\nfunc TestF(t *testing.T) {\n\tF()\n}\n")
	write(t, filepath.Join(dir, "none", "x.ogo"), "func F() int {\n\treturn 1\n}\n")
	for _, c := range []struct {
		dir    string
		code   int
		final  string
		output string
	}{
		{"bad", 1, "fail", "x.ogo:2:9: undefined: missing\n"},
		{"none", 0, "skip", "[no test files]\n"},
	} {
		var out, errb bytes.Buffer
		code, err := Test([]string{"--json", filepath.Join(dir, c.dir)}, nil, &out, &errb)
		if err != nil || code != c.code || errb.Len() != 0 {
			t.Errorf("%s: code %d, err %v, stderr %q; want %d", c.dir, code, err, errb.String(), c.code)
		}
		var events []testEvent
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			var e testEvent
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("%s: %v: %s", c.dir, err, line)
			}
			events = append(events, e)
		}
		if len(events) < 3 || events[0].Action != "start" || events[len(events)-1].Action != c.final {
			t.Errorf("%s: events %+v, want a start and a %s last", c.dir, events, c.final)
			continue
		}
		found := false
		for _, e := range events {
			found = found || e.Action == "output" && strings.HasSuffix(e.Output, c.output)
		}
		if !found {
			t.Errorf("%s: no output ending %q in %+v", c.dir, c.output, events)
		}
	}
}
