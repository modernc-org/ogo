// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// examplesDir is the repo's _examples tree. It is named with a leading underscore
// so the Go tool ignores it -- the sources in it are .ogo, and a directory of them
// is not a Go package -- which is also why nothing but these tests compiles it.
const examplesDir = "../../_examples"

// printfCall matches a call of the printf built-in, and only a call: a mention of
// the name in prose must not be rewritten.
var printfCall = regexp.MustCompile(`\bprintf\(`)

// exampleGoTwin turns an example's OctoGo source into the Go program it is
// modelled on. There are exactly two differences and this is both of them: OctoGo
// has no package clause, and printf is a built-in here where it is fmt.Printf
// there.
//
// That the list is two long is the property being tested, not an implementation
// detail of the test. An example needing a third substitution has stopped being
// the same program, and the right answer is to change the example.
func exampleGoTwin(src string) string {
	return "package main\n\nimport \"fmt\"\n\n" + printfCall.ReplaceAllString(src, "fmt.Printf(")
}

// portableExamples returns the examples that are pure language -- no import of
// "p2", so no pin, no cog and no board. Those are the ones that can be run on a
// host and compared against Go; blink, counter and gopher drive hardware and mean
// nothing off the board.
func portableExamples(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("reading %s: %v", examplesDir, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(examplesDir, e.Name(), "main.ogo"))
		if err != nil {
			continue // a directory that is not an example
		}
		if src := string(b); !strings.Contains(src, `import "p2"`) {
			out[e.Name()] = src
		}
	}
	if len(out) == 0 {
		t.Fatal("no portable example found; _examples should hold at least one program that " +
			"imports nothing, so there is something to compare against Go")
	}
	return out
}

// TestExampleMatchesGo runs each portable example twice -- once compiled by this
// compiler, once by Go -- and requires the two to print the same bytes.
//
// This is the strongest test in the repo of what the project actually claims. The
// run corpus pins behaviour against what a human wrote down as expected, which is
// only as good as the human; this pins it against the language OctoGo is modelled
// on, running the same source. A divergence is either a bug here or a place where
// the two genuinely differ, and both are worth being told about.
//
// Skipped when no C compiler is available, so the suite still runs anywhere.
func TestExampleMatchesGo(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found; skipping the compare-with-Go test")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool found; skipping the compare-with-Go test")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}

	for name, src := range portableExamples(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()

			// The Go side: the same source, given a package clause and fmt.
			goSrc := filepath.Join(dir, "twin.go")
			if err := os.WriteFile(goSrc, []byte(exampleGoTwin(src)), 0o644); err != nil {
				t.Fatal(err)
			}
			want, err := exec.Command("go", "run", goSrc).CombinedOutput()
			if err != nil {
				t.Fatalf("go run: %v\n%s", err, want)
			}

			// The OctoGo side, through the emitter and a host C compiler.
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			var buf bytes.Buffer
			if err := EmitC(pkg, &buf, Checked()); err != nil {
				t.Fatalf("EmitC: %v", err)
			}
			csrc := filepath.Join(dir, "main.c")
			if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "prog")
			ccOut, err := exec.Command(cc, "-std=gnu11", "-Wall", "-Wextra",
				"-Wno-unused-function", "-Wno-format", "-I", shim, "-o", bin, csrc, "-lpthread").CombinedOutput()
			if err != nil {
				t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, ccOut, buf.String())
			}
			if len(bytes.TrimSpace(ccOut)) != 0 {
				t.Errorf("cc warned:\n%s", ccOut)
			}
			got, err := exec.Command(bin).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, got)
			}

			if g, w := strings.ReplaceAll(string(got), "\r\n", "\n"), string(want); g != w {
				t.Errorf("output differs from Go's:\n%s", firstDiff(g, w))
			}
		})
	}
}

// firstDiff reports where two outputs part company, by line, with a little context
// either side. A whole-output dump of two programs that print a hundred lines each
// says only that they differ.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		gl, wl := "<end of output>", "<end of output>"
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl == wl {
			continue
		}
		var b strings.Builder
		for j := max(0, i-2); j < i; j++ {
			b.WriteString("  line " + itoa(j+1) + ":  " + g[j] + "\n")
		}
		b.WriteString("  line " + itoa(i+1) + " ogo:  " + gl + "\n")
		b.WriteString("  line " + itoa(i+1) + " Go:   " + wl + "\n")
		return b.String()
	}
	return "<no line differs, but the outputs are not equal: check trailing bytes>"
}

// itoa spells a small non-negative int, so firstDiff needs no import for it.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// TestTargetBuildExamples builds every example with the real backend, which is
// what a reader of the repo runs. They are the only programs here written to be
// read rather than to exercise a feature, and nothing else compiles them: the run
// corpus is its own table and the Go tool skips a directory named with a leading
// underscore. An example that stopped compiling would have been found by a user.
//
// The standard is the one every target build is held to -- a successful build must
// also be SILENT, since flexcc warns where it should refuse.
func TestTargetBuildExamples(t *testing.T) {
	ogo := buildOgoCLI(t)
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("reading %s: %v", examplesDir, err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(examplesDir, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.ogo")); err != nil {
			continue
		}
		found++
		t.Run(e.Name(), func(t *testing.T) {
			t.Parallel() // separate processes, so the builds are independent
			out := filepath.Join(t.TempDir(), e.Name()+".binary")
			if err := boardBuildPaths(ogo, out, "", nil, dir); err != nil {
				t.Errorf("%v", err)
			}
		})
	}
	if found == 0 {
		t.Fatalf("no example found under %s", examplesDir)
	}
}

// TestOnBoardExample runs each portable example on real hardware and requires it
// to print what Go prints -- the same comparison TestExampleMatchesGo makes, with
// the board standing where the host C compiler stood.
//
// The two are not the same claim. flexcc and the host compiler have been observed
// to disagree about SEMANTICS and not merely about warnings, so a host-green
// example says nothing about the target; and the expectation here is computed by
// running Go rather than written down, so nothing can pin a wrong answer.
func TestOnBoardExample(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool found; skipping the compare-with-Go test")
	}
	ogo := buildOgoCLI(t)
	for name, src := range portableExamples(t) {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			goSrc := filepath.Join(dir, "twin.go")
			if err := os.WriteFile(goSrc, []byte(exampleGoTwin(src)), 0o644); err != nil {
				t.Fatal(err)
			}
			want, err := exec.Command("go", "run", goSrc).CombinedOutput()
			if err != nil {
				t.Fatalf("go run: %v\n%s", err, want)
			}
			bin := filepath.Join(dir, name+".binary")
			if err := boardBuildPaths(ogo, bin, "", nil, filepath.Join(examplesDir, name)); err != nil {
				t.Fatalf("build: %v", err)
			}
			var out string
			var matched bool
			for attempt := 0; attempt < boardAttempts && !matched; attempt++ {
				if attempt > 0 {
					t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
				}
				out, matched = boardLoad(ogo, port, bin, string(want))
			}
			if !matched {
				t.Errorf("board output does not match Go's after %d attempts:\n%s",
					boardAttempts, firstDiff(out, string(want)))
			}
		})
	}
}

// TestExampleTwinIsGofmtClean holds the claim the portable examples are FOR: that
// the source reads as Go, not merely that it computes what Go computes. An example
// gofmt would reformat is one a Go reader would flinch at.
//
// It doubles as a canary for `ogo fmt` drifting from gofmt, which is the likelier
// cause of a failure here than a badly written example: both tools format this
// file today, and they are supposed to agree. gofmt tightens the operands of a
// call that takes more than one argument -- "f(x+dx, y+dy)" -- where ogo fmt
// spaces them, so an example is written clear of that until the formatter catches
// up.
func TestExampleTwinIsGofmtClean(t *testing.T) {
	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		t.Skip("no gofmt found; skipping the formatting check")
	}
	for name, src := range portableExamples(t) {
		t.Run(name, func(t *testing.T) {
			twin := exampleGoTwin(src)
			cmd := exec.Command(gofmt)
			cmd.Stdin = strings.NewReader(twin)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("gofmt: %v", err)
			}
			if string(out) != twin {
				t.Errorf("the Go twin of _examples/%s is not gofmt-clean.\n%s\n"+
					"Either the example needs reformatting, or `ogo fmt` has drifted "+
					"from gofmt -- check which by running both over the .ogo source.",
					name, firstDiff(twin, string(out)))
			}
		})
	}
}
