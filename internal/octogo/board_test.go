// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"context"
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
// here to avoid importing that package (which pulls in the linux/amd64-only
// transpiled loader) into the checker's test binary.
const boardBaud = 230400

// boardCaseTimeout bounds one program's load + run + capture. A load is ~0.6 s
// and the programs finish instantly, so a match normally lands in ~2 s; the slack
// covers serial latency and the concurrency rendezvous cases. A case that never
// matches waits the whole window.
const boardCaseTimeout = 12 * time.Second

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
	ogo := filepath.Join(t.TempDir(), "ogo")
	if out, err := exec.Command("go", "build", "-o", ogo, "modernc.org/ogo").CombinedOutput(); err != nil {
		t.Fatalf("go build ogo: %v\n%s", err, out)
	}

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
			out, matched := boardLoad(ogo, port, bin, stop)
			if !matched {
				what := strconv.Quote(test.want)
				if test.panics {
					what = "a panic"
				}
				t.Errorf("board output did not contain %s\ngot:\n%s", what, out)
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
// cleaned output and whether stop was seen. loadp2 -t does not exit on its own, so
// the process is killed as soon as the match lands, keeping a passing case near
// the ~2 s load-and-print time rather than the full timeout.
func boardLoad(ogo, port, binary, stop string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), boardCaseTimeout)
	defer cancel()

	// -t echoes the program's serial output; -NOEOF keeps terminal mode alive
	// despite the /dev/null stdin's immediate EOF.
	cmd := exec.CommandContext(ctx, ogo, "loadp2", "-t", "-NOEOF", "-p", port, "-b", strconv.Itoa(boardBaud), binary)
	rd, wr, err := os.Pipe()
	if err != nil {
		return "pipe: " + err.Error(), false
	}
	cmd.Stdout, cmd.Stderr = wr, wr
	if err := cmd.Start(); err != nil {
		wr.Close()
		rd.Close()
		return "start: " + err.Error(), false
	}
	wr.Close() // the child holds its own copy; drop ours so a dead child EOFs rd

	// One reader goroutine owns the accumulator, so there is no shared-state race.
	// On a match it cancels the context (killing loadp2) and drains to EOF.
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
			n, rerr := rd.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
				if !matched && strings.Contains(cleanBoardOutput(buf.String()), stop) {
					matched = true
					cancel()
				}
			}
			if rerr != nil {
				resc <- result{cleanBoardOutput(buf.String()), matched}
				return
			}
		}
	}()

	r := <-resc
	rd.Close()
	_ = cmd.Wait()
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
