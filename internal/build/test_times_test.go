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
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"modernc.org/ogo/internal/octogo"
)

// TestTestTimes holds the runner's output to go test -v's where tools read it: a
// result line of a test and of a subtest ends with the time it took, " (0.12s)",
// "PASS" or the failures' line follows the last test, and the time the run took
// comes back for "ogo test" to print on the package's line (testTimeLine). The
// comparisons with go test drop the times, which vary from run to run, so this is
// where their form is held -- and that they are the test's: one that waits 120 ms
// takes about that.
func TestTestTimes(t *testing.T) {
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
	const src = `import (
	"p2"
	"testing"
)

func TestWaits(t *testing.T) {
	p2.WaitMs(120)
}

func sub(t *testing.T) {}

func TestSubs(t *testing.T) {
	t.Run("a", sub)
}

func TestFails(t *testing.T) {
	t.Error("no")
}

func TestSkips(t *testing.T) {
	t.Skip("later")
}
`
	names := []string{"TestWaits", "TestSubs", "TestFails", "TestSkips"}
	runner := testRunnerSrc(names, nil, benchRun{}, nil, nil, true)
	fsys := fstest.MapFS{
		"x_test.ogo":   &fstest.MapFile{Data: []byte(src)},
		testRunnerFile: &fstest.MapFile{Data: []byte(runner)},
		"main.ogo":     &fstest.MapFile{Data: []byte("func main() { ogoTestMain() }\n")},
	}
	pkg, err := octogo.Build(-1, []string{"main.ogo", "x_test.ogo", testRunnerFile}, fsys)
	if err != nil {
		t.Fatalf("%v\n%s", err, runner)
	}
	var buf bytes.Buffer
	if err := octogo.EmitC(pkg, &buf, octogo.Checked(), octogo.Inline()); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	csrc, bin := filepath.Join(tmp, "main.c"), filepath.Join(tmp, "prog")
	if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra", "-Wno-unused-function", "-Wno-format",
		"-I", shim, "-o", bin, csrc, "-lpthread", "-lm").CombinedOutput(); err != nil || len(bytes.TrimSpace(out)) != 0 {
		t.Fatalf("cc: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := string(out)
	for _, re := range []string{
		`(?m)^--- PASS: TestWaits \((\d+\.\d\d)s\)$`,
		`(?m)^--- PASS: TestSubs \(\d+\.\d\ds\)\n    --- PASS: TestSubs/a \(\d+\.\d\ds\)$`,
		`(?m)^--- FAIL: TestFails \(\d+\.\d\ds\)$`,
		`(?m)^--- SKIP: TestSkips \(\d+\.\d\ds\)$`,
		`(?m)^FAIL 1 of 4\n` + regexp.QuoteMeta(testTimeLine) + `\d+\n` + testDoneLine + `\n$`,
	} {
		m := regexp.MustCompile(re).FindStringSubmatch(got)
		if m == nil {
			t.Errorf("no %s in:\n%s", re, got)
			continue
		}
		if len(m) > 1 {
			if s, _ := strconv.ParseFloat(m[1], 64); s < 0.11 || s > 1 {
				t.Errorf("a test waiting 120 ms took %ss:\n%s", m[1], got)
			}
		}
	}
	// And "PASS" where nothing failed.
	if pass := testRunnerSrc([]string{"TestWaits"}, nil, benchRun{}, nil, nil, false); !strings.Contains(pass, `println("PASS")`) {
		t.Errorf("no PASS in:\n%s", pass)
	}
}
