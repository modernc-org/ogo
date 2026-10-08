// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// differencesFile is the page of what builds both as OctoGo and as Go and runs
// differently. Its examples are tests: each is a run case (init below) -- run on the
// host, built for the target, run on the board -- and TestDifferencesGo runs each
// under Go, so the page cannot say what either compiler no longer does.
var differencesFile = filepath.Join("..", "..", "DIFFERENCES.md")

// difference is one example of the page: its heading, the program, what OctoGo and
// Go print, and the two marks an entry may carry -- an example the host cannot run as
// the target does, and one whose Go output varies from run to run.
type difference struct {
	title, src, ogo, goOut string
	boardOnly, goVaries    bool
}

// differences are the page's examples, and differencesErr what reading it failed
// with, reported by TestDifferencesParse rather than in init.
var differences, differencesErr = parseDifferences(differencesFile)

func init() {
	for _, d := range differences {
		emitRunCases = append(emitRunCases, emitRunCase{
			name:      "DIFFERENCES.md: " + d.title,
			src:       d.src,
			want:      d.ogo,
			panics:    strings.Contains(d.ogo, "panic:"),
			boardOnly: d.boardOnly,
		})
	}
}

// parseDifferences reads the page: an example is a "### " heading, a "go" code
// block, and the blocks after the lines "OctoGo prints:" and "Go prints:". The marks
// are HTML comments under the heading, `<!-- board-only: why -->` and `<!--
// go-varies: why -->`.
func parseDifferences(path string) ([]difference, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var (
		out   []difference
		cur   *difference
		block *string // the block being read into, nil outside one
		next  string  // what the next code block holds: "ogo", "go" or the program
		line  int
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line++
		text := sc.Text()
		if block != nil {
			if text == "```" {
				block = nil
				continue
			}
			*block += text + "\n"
			continue
		}
		switch {
		case strings.HasPrefix(text, "## "):
			cur = nil // a section of no examples, the list of refusals
		case strings.HasPrefix(text, "### "):
			out = append(out, difference{title: strings.ReplaceAll(strings.TrimPrefix(text, "### "), "`", "")})
			cur, next = &out[len(out)-1], "src"
		case cur == nil:
		case strings.HasPrefix(text, "<!-- board-only"):
			cur.boardOnly = true
		case strings.HasPrefix(text, "<!-- go-varies"):
			cur.goVaries = true
		case text == "OctoGo prints:":
			next = "ogo"
		case text == "Go prints:":
			next = "go"
		case text == "```go" || text == "```":
			switch next {
			case "src":
				block = &cur.src
			case "ogo":
				block = &cur.ogo
			case "go":
				block = &cur.goOut
			default:
				return nil, fmt.Errorf("%s:%d: a code block with no heading for it", path, line)
			}
			next = ""
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, d := range out {
		if d.src == "" || d.ogo == "" || d.goOut == "" {
			return nil, fmt.Errorf("%s: %q lacks its program, OctoGo's output or Go's", path, d.title)
		}
	}
	return out, nil
}

// TestDifferencesParse holds the page to its shape: every example with a program
// and both outputs, and as many as it has headings.
func TestDifferencesParse(t *testing.T) {
	if differencesErr != nil {
		t.Fatal(differencesErr)
	}
	if len(differences) < 10 {
		t.Fatalf("%s has %d examples, it had 11", differencesFile, len(differences))
	}
}

// panicTail is what follows the first line of a panic in Go's output -- the goroutine
// trace -- which the page leaves out.
var panicTail = regexp.MustCompile(`(?s)(panic: [^\n]*\n).*`)

// TestDifferencesGo runs each example of the page under Go and holds its output to
// the page's: what Go prints is a claim about Go, and Go can be asked. A package
// channel, which a declaration makes here, is made in Go; printf is fmt.Printf. An
// example whose Go output varies is built and not compared.
func TestDifferencesGo(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}
	goCmd, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	if differencesErr != nil {
		t.Fatal(differencesErr)
	}
	chanVar := regexp.MustCompile(`(?m)^var (\w+) chan (.+)$`)
	for _, d := range differences {
		t.Run(d.title, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			src := "package main\n\n" + chanVar.ReplaceAllString(d.src, "var $1 = make(chan $2)")
			files := []string{"main.go"}
			if strings.Contains(d.src, "printf(") {
				src += "\nfunc printf(f string, a ...any) { fmt.Printf(f, a...) }\n"
				src = strings.Replace(src, "package main\n", "package main\n\nimport \"fmt\"\n", 1)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(goCmd, append([]string{"run"}, files...)...)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			got := panicTail.ReplaceAllString(string(out), "$1")
			if strings.HasPrefix(got, "# ") || strings.Contains(got, "go: ") {
				t.Fatalf("go run: %v\n%s", err, out)
			}
			if d.goVaries {
				return
			}
			if got != d.goOut {
				t.Fatalf("Go prints\n%s\nand %s says\n%s", got, differencesFile, d.goOut)
			}
		})
	}
}
