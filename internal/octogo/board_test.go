// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// boardBaud is the user baud rate of flexcc-emitted P2 programs (loadp2's default
// 115200 garbles them). It mirrors internal/loadp2.DefaultUserBaud, duplicated
// here to avoid importing that package (which pulls in the large transpiled
// loader) into the checker's test binary.
const boardBaud = 230400

// boardCaseTimeout bounds one program's load + run + capture. A load is ~0.6 s
// and the programs finish instantly, so a match normally lands in ~2 s; the slack
// covers serial latency and the concurrency rendezvous cases. A case that never
// matches waits the whole window.
const boardCaseTimeout = 12 * time.Second

// boardAttempts is how many times a case is loaded before it is failed, to ride
// out the occasional dropped serial handshake. A miscompile prints the same wrong
// output on every attempt, so retries only absorb transient flakes.
const boardAttempts = 3

// TestTargetBuild compiles every emitRunCases program with the real backend --
// `ogo build`, so checker -> C -> flexcc -> P2 binary, the path a user runs. It
// only compiles: running the programs is TestOnBoard's job and needs hardware.
//
// It exists because TestEmitCRun's host C compiler is not a stand-in for flexcc.
// The two disagree on what they accept, not just on what they warn about: flexcc
// cannot lower a compound literal of a struct that has an array field, so `b :=
// B{}` compiled cleanly on the host and failed for the target with "Unable to
// multiply assign this target", naming C the user never wrote. Nothing caught that
// until a board happened to be plugged in. This test needs no board, so the whole
// class of target-only compile break now fails in the default `go test ./...`.
func TestTargetBuild(t *testing.T) {
	ogo := buildOgoCLI(t)
	for _, test := range emitRunCases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel() // separate processes, so the builds are independent
			dir := t.TempDir()
			if err := boardBuild(ogo, dir, "prog", test.src, filepath.Join(dir, "prog.binary"), test.backendWarning); err != nil {
				t.Errorf("%v", err)
			}
		})
	}
}

// TestTargetBuildMultiPkg compiles a program that spans packages with the real
// backend. The single-file corpus says nothing about a package boundary, and that
// boundary is where the lowering is about nothing but names: every top-level symbol
// is mangled into its package's namespace and the whole program becomes one
// translation unit.
func TestTargetBuildMultiPkg(t *testing.T) {
	ogo := buildOgoCLI(t)
	dir := t.TempDir()
	if err := boardBuildTree(ogo, dir, multiPkgProgram, filepath.Join(dir, "prog.binary"), ""); err != nil {
		t.Errorf("%v", err)
	}
}

// TestOnBoardMultiPkg runs the multi-package program on real hardware, which
// TestTargetBuildMultiPkg only compiles. The two are the pair TestTargetBuild and
// TestOnBoard are: compiling proves flexcc accepts the emitted C, running proves it
// lowered it to what the program means.
func TestOnBoardMultiPkg(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "prog.binary")
	if err := boardBuildTree(ogo, dir, multiPkgProgram, bin, ""); err != nil {
		t.Fatalf("build: %v", err)
	}
	for attempt := 1; ; attempt++ {
		out, matched := boardLoad(ogo, port, bin, multiPkgWant)
		if matched {
			return
		}
		if attempt == boardAttempts {
			t.Errorf("board output did not contain %q after %d attempts\ngot:\n%s", multiPkgWant, boardAttempts, out)
			return
		}
		t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
	}
}

// smithSeeds is how many fuzzer-generated programs the target tests take. Each is
// a full flexcc build (and, on a board, a load), so this is a sample of the corpus
// TestOracle runs on the host, not the whole of it.
//
// Widening it to hunt for new bugs eventually runs into a target limit rather than
// a compiler one: a generated program is one very long main, and past some size it
// no longer fits the cog's code window ("fit 480 failed"). Such a seed is SKIPPED,
// not failed -- see outgrewCog. Generated programs sit close enough to that ceiling
// that adding to the generator pushes some seed over it, so this must not be a
// failure or the fuzzer's coverage becomes hostage to program size.
const smithSeeds = 12

// outgrewCog reports whether a build failed because the program does not fit the
// cog's code window -- "fit 480 failed: pc is 493" from the assembler.
//
// That is a property of the target, not a defect: a generated program is one very
// long main, and past some size it no longer fits. It bounds how much the
// generator may add rather than saying anything is wrong, so a seed that hits it
// is skipped and reported as skipped. A hand-written case must never hit it, which
// is why this is not in boardBuild.
func outgrewCog(err error) bool {
	return strings.Contains(err.Error(), "fit 480 failed")
}

// backendRefused reports whether a build failed because of a backend defect this
// compiler cannot do anything about, as opposed to something wrong with the
// emitted C.
//
// There is one so far: the optimizer emits a branch to a label it then does not
// define, and the assembler says "Unknown symbol 'L__0579'". gcc compiles the same
// C without a warning and flexcc assembles it at -O0, so the C is not at fault --
// see doc/optimizer-dangling-label.c. A seed that hits it is skipped and says so,
// for the same reason a seed that outgrows the cog is: the fuzzer's job is to
// report what it found, not to fail on a defect that is already written down.
func backendRefused(err error) bool {
	return strings.Contains(err.Error(), "Unknown symbol 'L__")
}

// smithProgram generates one fuzzer program by running the `ogo smith` subcommand.
//
// Going through the CLI rather than importing the generator is what keeps this
// test here at all: internal/smith imports this package, so an import of it from
// package octogo's own tests would be a cycle.
func smithProgram(t *testing.T, ogo string, seed int) string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command(ogo, "smith", "-seed", strconv.Itoa(seed))
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("ogo smith -seed %d: %v\n%s", seed, err, errb.String())
	}
	return out.String()
}

// TestTargetBuildSmith compiles fuzzer-generated programs with the real backend.
//
// TestOracle proves the generated program computes what the generator predicted,
// but it compiles with the host C compiler, which is not flexcc: the two have been
// observed to disagree on what they accept, not just on what they warn about. A
// generated program is also unlike anything in the hand-written corpus -- deeply
// nested, wide expressions, many names -- so it is the one thing likely to find a
// backend limit nobody wrote a case for.
func TestTargetBuildSmith(t *testing.T) {
	ogo := buildOgoCLI(t)
	for seed := 1; seed <= smithSeeds; seed++ {
		t.Run(strconv.Itoa(seed), func(t *testing.T) {
			t.Parallel()
			src := smithProgram(t, ogo, seed)
			dir := t.TempDir()
			if err := boardBuild(ogo, dir, "prog", src, filepath.Join(dir, "prog.binary"), ""); err != nil {
				if outgrewCog(err) || backendRefused(err) {
					t.Skip(err.Error())
				}
				t.Errorf("%v\n--- program ---\n%s", err, src)
			}
		})
	}
}

// TestOnBoardSmith runs fuzzer-generated programs on real hardware.
//
// This is the oracle closing its last gap. A generated program self-checks a
// running checksum the generator computed as it emitted the code, so a wrong
// answer anywhere in the chain -- this compiler, flexcc, or the P2 itself --
// panics instead of printing OctoSmith OK. Until now that check only ever ran
// against the host shim, so it said nothing about the target, which is the only
// machine the language is for.
func TestOnBoardSmith(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	const want = "OctoSmith OK"
	for seed := 1; seed <= smithSeeds; seed++ {
		t.Run(strconv.Itoa(seed), func(t *testing.T) {
			src := smithProgram(t, ogo, seed)
			dir := t.TempDir()
			bin := filepath.Join(dir, "prog.binary")
			if err := boardBuild(ogo, dir, "prog", src, bin, ""); err != nil {
				if outgrewCog(err) || backendRefused(err) {
					t.Skip(err.Error())
				}
				t.Fatalf("build: %v", err)
			}
			for attempt := 1; ; attempt++ {
				out, matched := boardLoad(ogo, port, bin, want)
				if matched {
					return
				}
				// A checksum failure is a miscompile, not a flaky serial line, so
				// report it at once rather than retrying it boardAttempts times.
				if strings.Contains(out, "Checksum Failure") {
					t.Errorf("the generated program's checksum did not hold ON THE BOARD "+
						"(it holds on the host, so this is the backend or the target)\ngot:\n%s"+
						"\n--- program ---\n%s", out, src)
					return
				}
				if attempt == boardAttempts {
					t.Errorf("board output did not contain %q after %d attempts\ngot:\n%s"+
						"\n--- program ---\n%s", want, boardAttempts, out, src)
					return
				}
				t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
			}
		})
	}
}

// TestTargetBuildFlags compiles the same corpus in the other two configurations
// `ogo build` offers. --unchecked emits different C -- an index, a divisor and a
// shift count lose their guards, and a helper that carried one loses that branch --
// and --release changes what a panic does. Each can break on its own, and every
// other target test builds with neither.
func TestTargetBuildFlags(t *testing.T) {
	ogo := buildOgoCLI(t)
	for _, flag := range []string{"--unchecked", "--release"} {
		t.Run(flag, func(t *testing.T) {
			for _, test := range emitRunCases {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					dir := t.TempDir()
					if err := boardBuild(ogo, dir, "prog", test.src, filepath.Join(dir, "prog.binary"), test.backendWarning, flag); err != nil {
						t.Errorf("%v", err)
					}
				})
			}
		})
	}
}

// buildOgoCLI builds the ogo command once for a test to shell out to.
func buildOgoCLI(t *testing.T) string {
	t.Helper()
	ogo := filepath.Join(t.TempDir(), "ogo")
	if out, err := exec.Command("go", "build", "-o", ogo, "modernc.org/ogo").CombinedOutput(); err != nil {
		t.Fatalf("go build ogo: %v\n%s", err, out)
	}
	return ogo
}

// TestOnBoard runs the emitRunCases table on a real Propeller 2 board: for each
// program it drives `ogo build` (checker -> C -> flexcc -> .binary) and then `ogo
// loadp2 -t` to load and run it, and checks the serial output. It is the hardware
// counterpart of TestEmitCRun, which exercises the same table on the host through
// a C compiler and the pthread shim.
//
// It is skipped unless OGO_BOARD_PORT names the board's serial port, so the
// default `go test ./...` (including on the board machine and in CI) never touches
// hardware:
//
//	OGO_BOARD_PORT=/dev/ttyUSB0 go test ./internal/octogo/ -run TestOnBoard -v
//
// The loader talks to one board at a time, so the cases run sequentially. Loads
// are RAM loads (non-destructive); each resets the P2, so cases do not interfere.
func TestOnBoard(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}

	// Build the ogo CLI once; the cases shell out to it for build and load. A
	// subprocess isolates loadp2, which drives the real serial port and terminal
	// and keeps global state, and lets a hung load be killed by timeout.
	ogo := buildOgoCLI(t)

	// Preflight: confirm the board answers before running the whole table, so a
	// disconnected or unpowered board fails fast with a clear message instead of
	// timing out on every case.
	dir := t.TempDir()
	preflight := filepath.Join(dir, "preflight.binary")
	if err := boardBuild(ogo, dir, "preflight", "func main() { println(\"OGO-PREFLIGHT-OK\") }\n", preflight, ""); err != nil {
		t.Fatalf("preflight build: %v", err)
	}
	if out, matched := boardLoad(ogo, port, preflight, "OGO-PREFLIGHT-OK"); !matched {
		t.Fatalf("board not responding on %s (is the P2-EDGE connected, powered, and the port right?)\ncaptured:\n%s", port, out)
	}

	for _, test := range emitRunCases {
		t.Run(test.name, func(t *testing.T) {
			bin := filepath.Join(dir, "prog.binary")
			if err := boardBuild(ogo, dir, "prog", test.src, bin, test.backendWarning); err != nil {
				t.Fatalf("build: %v", err)
			}
			// A panic case aborts through ogo_panic, which prints "panic: <msg>"
			// on the serial line and halts the cog; any other case must print its
			// expected output.
			stop := test.want
			if test.panics {
				stop = "panic:"
			}
			// The serial load is occasionally flaky (a dropped handshake makes
			// loadp2 exit early), so retry a no-match a couple of times. A real
			// miscompile is deterministic -- it prints the same wrong output every
			// time -- so retries never mask one, they only absorb transient hiccups.
			var out string
			var matched bool
			for attempt := 0; attempt < boardAttempts && !matched; attempt++ {
				if attempt > 0 {
					t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
				}
				out, matched = boardLoad(ogo, port, bin, stop)
			}
			if !matched {
				what := strconv.Quote(test.want)
				if test.panics {
					what = "a panic"
				}
				t.Errorf("board output did not contain %s after %d attempts\ngot:\n%s", what, boardAttempts, out)
			}
		})
	}
}

// boardBuild writes src to <dir>/<name>.ogo and compiles it to a P2 binary at out
// with `ogo build`. Checks are left on (the default), so the panic cases trap.
//
// A successful build that printed anything is also a failure, unless what it
// printed contains allowWarning. The C backend warns where it ought to refuse --
// it takes a duplicate declaration in one block, says "Redefining x", ignores the
// second and builds -- so a diagnostic it emits is the only sign that the emitted
// C is wrong in a way the exit status will not show. A clean build is silent, so
// anything at all is worth reading.
func boardBuild(ogo, dir, name, src, out, allowWarning string, flags ...string) error {
	srcFile := filepath.Join(dir, name+".ogo")
	if err := os.WriteFile(srcFile, []byte(src), 0o644); err != nil {
		return err
	}
	return boardBuildPaths(ogo, out, allowWarning, flags, srcFile)
}

// boardBuildTree writes a whole program -- a main package plus the packages it
// imports, keyed by path relative to dir -- and builds the directory, which is how
// `ogo build` is given a multi-package program.
func boardBuildTree(ogo, dir string, files map[string]string, out, allowWarning string, flags ...string) error {
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return err
		}
	}
	return boardBuildPaths(ogo, out, allowWarning, flags, dir)
}

// boardBuildPaths runs `ogo build` over the given source argument and holds it to
// the same standard either way: a successful build must also be silent.
func boardBuildPaths(ogo, out, allowWarning string, flags []string, src string) error {
	args := append([]string{"build", "-o", out}, flags...)
	b, err := exec.Command(ogo, append(args, src)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ogo build: %v\n%s", err, b)
	}
	switch chatter := strings.TrimSpace(string(b)); {
	case chatter == "":
	case allowWarning != "" && strings.Contains(chatter, allowWarning):
		// A diagnostic this case records as examined and harmless.
	default:
		return fmt.Errorf("ogo build succeeded but the backend reported:\n%s\n"+
			"A backend diagnostic means the emitted C is suspect even though the build passed. "+
			"Fix the emitter, or record it in the case's backendWarning field with the reason it is harmless.", chatter)
	}
	return nil
}

// boardLoad loads binary with `ogo loadp2 -t` and reads the board's serial output
// until it contains stop (success) or boardCaseTimeout elapses. It returns the
// cleaned output and whether stop was seen.
//
// loadp2 -t does not exit on its own, so it must be told to stop once the match
// lands. It is NOT SIGKILLed: an abruptly killed loadp2 leaves the serial port in
// a state (baud, modem lines) that wedges the board for subsequent loads -- the
// board then stops responding until it is physically reset. Instead we send
// Ctrl-] (0x1d), loadp2's documented "leave terminal mode" key, on its stdin, so
// it closes the port cleanly and exits 0. SIGKILL remains only as a last resort
// if a genuinely hung load ignores Ctrl-].
func boardLoad(ogo, port, binary, stop string) (string, bool) {
	// -t echoes the program's serial output; -NOEOF keeps terminal mode alive
	// despite the stdin pipe carrying no keystrokes until we send Ctrl-].
	cmd := exec.Command(ogo, "loadp2", "-t", "-NOEOF", "-p", port, "-b", strconv.Itoa(boardBaud), binary)
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return "pipe: " + err.Error(), false
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		return "pipe: " + err.Error(), false
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, outW, outW
	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		outR.Close()
		outW.Close()
		return "start: " + err.Error(), false
	}
	stdinR.Close() // the child holds its own copies; drop ours so a dead child EOFs outR
	outW.Close()

	// quit asks loadp2 to leave terminal mode and close the port cleanly. Writing
	// to a stdin whose reader has exited just errors, which is fine to ignore.
	quit := func() { stdinW.Write([]byte{0x1d}) }

	// One reader goroutine owns the accumulator, so there is no shared-state race.
	// On a match it asks loadp2 to quit, then drains to EOF (which arrives once
	// loadp2 has closed the port and exited).
	type result struct {
		out     string
		matched bool
	}
	resc := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		tmp := make([]byte, 4096)
		matched := false
		for {
			n, rerr := outR.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
				if !matched && strings.Contains(cleanBoardOutput(buf.String()), stop) {
					matched = true
					quit()
				}
			}
			if rerr != nil {
				resc <- result{cleanBoardOutput(buf.String()), matched}
				return
			}
		}
	}()

	// If the output never matches, ask loadp2 to quit at the deadline; only if that
	// is ignored -- a genuinely hung load -- fall back to SIGKILL, the one path that
	// can wedge the board, reached only on a real failure.
	nudge := time.AfterFunc(boardCaseTimeout, quit)
	kill := time.AfterFunc(boardCaseTimeout+3*time.Second, func() { _ = cmd.Process.Kill() })

	r := <-resc
	nudge.Stop()
	kill.Stop()
	_ = cmd.Wait()
	stdinW.Close()
	outR.Close()
	return r.out, r.matched
}

// cleanBoardOutput normalizes captured serial output for comparison: the P2 ends
// lines with CRLF, so strip the carriage returns to match the tables' "\n", and
// drop loadp2's terminal-mode banner line.
func cleanBoardOutput(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "Entering terminal mode") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
