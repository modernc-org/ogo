// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// terminalLine is the line ogo run's terminal opens with, which `ogo help run`
// documents as what a program driving run waits for before it writes.
const terminalLine = "( Entering terminal mode.  Press Ctrl-] or Ctrl-Z to exit. )\n"

// TestOnBoardRunPipes holds what `ogo help run` says of a run driven over pipes,
// as an editor's terminal drives it: the line that opens the terminal first on
// stdout, the board's output as it arrives, each line ending in CR LF, stdin
// reaching the program, Ctrl-] and Ctrl-Z leaving with status 0 and neither
// reaching the program, the end of stdin leaving too, the loader's exit sequence
// ending the run with the status after it, and nothing on stderr. A program that
// returns from main leaves the terminal open: the run ends by one of those.
func TestOnBoardRunPipes(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	echo := filepath.Join(t.TempDir(), "echo")
	if err := os.MkdirAll(echo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(echo, "main.ogo"), []byte(`import "p2"

func main() {
	println("ready")
	for {
		c := p2.ReadByte(0)
		if c < 0 {
			continue
		}
		if c == 'q' {
			break
		}
		if c == 'e' {
			// The loader takes the last byte it read after the sequence for the
			// status, so nothing follows it.
			p2.WriteByte(0xff)
			p2.WriteByte(0)
			p2.WriteByte(3)
			for {
			}
		}
		printf("got %d\n", c)
	}
	println("bye")
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		input []string // written one at a time, each once the output has what the last asked for; "" closes stdin
		wait  []string // what the output has before the next input is written
		want  string   // the whole of stdout
		code  int
	}{
		{"input and Ctrl-]", []string{"ab", "\x1d"}, []string{"ready\r\n", "got 98\r\n"}, terminalLine + "ready\r\ngot 97\r\ngot 98\r\n", 0},
		{"Ctrl-Z", []string{"x", "\x1aq"}, []string{"ready\r\n", "got 120\r\n"}, terminalLine + "ready\r\ngot 120\r\n", 0},
		{"the end of stdin", []string{"q", ""}, []string{"ready\r\n", "bye\r\n"}, terminalLine + "ready\r\nbye\r\n", 0},
		{"the exit sequence", []string{"e"}, []string{"ready\r\n"}, terminalLine + "ready\r\n", 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			for attempt := 0; attempt < boardAttempts; attempt++ {
				if attempt > 0 {
					t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
				}
				out, errOut, code, ok := runOverPipes(t, ogo, port, echo, test.input, test.wait)
				if !ok {
					continue
				}
				if out != test.want || errOut != "" || code != test.code {
					t.Errorf("stdout %q, stderr %q, status %d; want %q, nothing and %d", out, errOut, code, test.want, test.code)
				}
				return
			}
			t.Errorf("the board never answered in %d attempts", boardAttempts)
		})
	}
}

// runOverPipes runs `ogo run --port port dir` with pipes on all three ends, writing
// each input once stdout has the terminal's line and the wait before it. It reports
// false where the board never got that far, a load lost.
func runOverPipes(t *testing.T, ogo, port, dir string, input, wait []string) (out, errOut string, code int, ok bool) {
	cmd := exec.Command(ogo, "run", "--port", port, dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var buf bytes.Buffer
	read := make(chan struct{})
	go func() {
		defer close(read)
		b := make([]byte, 256)
		for {
			n, err := stdout.Read(b)
			mu.Lock()
			buf.Write(b[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	has := func(s string, d time.Duration) bool {
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			mu.Lock()
			got := strings.Contains(buf.String(), s)
			mu.Unlock()
			if got {
				return true
			}
		}
		return false
	}
	ok = has(terminalLine, 30*time.Second)
	for i := 0; ok && i < len(input); i++ {
		if ok = has(wait[i], 10*time.Second); ok {
			if input[i] == "" {
				stdin.Close()
				continue
			}
			io.WriteString(stdin, input[i])
		}
	}
	done := make(chan error, 1)
	go func() { <-read; done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Errorf("the run did not end 10 s after its last input")
		cmd.Process.Kill()
		err = <-done
	}
	stdin.Close()
	mu.Lock()
	out = buf.String()
	mu.Unlock()
	code = cmd.ProcessState.ExitCode()
	return out, stderr.String(), code, ok
}
