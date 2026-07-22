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
			if err := boardBuild(ogo, dir, "prog", test.src, filepath.Join(dir, "prog.binary")); err != nil {
				t.Errorf("%v", err)
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
	if err := boardBuild(ogo, dir, "preflight", "func main() { println(\"OGO-PREFLIGHT-OK\") }\n", preflight); err != nil {
		t.Fatalf("preflight build: %v", err)
	}
	if out, matched := boardLoad(ogo, port, preflight, "OGO-PREFLIGHT-OK"); !matched {
		t.Fatalf("board not responding on %s (is the P2-EDGE connected, powered, and the port right?)\ncaptured:\n%s", port, out)
	}

	for _, test := range emitRunCases {
		t.Run(test.name, func(t *testing.T) {
			bin := filepath.Join(dir, "prog.binary")
			if err := boardBuild(ogo, dir, "prog", test.src, bin); err != nil {
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
func boardBuild(ogo, dir, name, src, out string) error {
	srcFile := filepath.Join(dir, name+".ogo")
	if err := os.WriteFile(srcFile, []byte(src), 0o644); err != nil {
		return err
	}
	if b, err := exec.Command(ogo, "build", "-o", out, srcFile).CombinedOutput(); err != nil {
		return fmt.Errorf("ogo build: %v\n%s", err, b)
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
