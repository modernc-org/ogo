// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
)

// testEvent is one line of `ogo test --json`, go test -json's TestEvent: the
// fields, their names and what is left out when empty are Go 1.27's
// cmd/internal/test2json's.
type testEvent struct {
	Time       *time.Time `json:",omitempty"`
	Action     string
	Package    string   `json:",omitempty"`
	Test       string   `json:",omitempty"`
	Elapsed    *float64 `json:",omitempty"`
	Output     string   `json:",omitempty"`
	OutputType string   `json:",omitempty"`
}

// test2json writes what one package's run printed, its output as go test -v
// prints it (ogo test prints nothing else), as the events Go's test2json makes of
// the same text, and then the package's result, by the run's exit status. Read off
// cmd/internal/test2json (Go 1.27), its handleInputLine:
//
//   - "=== RUN   X", PAUSE, CONT and NAME name the test the output is of, and flush
//     the results held; RUN, PAUSE and CONT are events, a NAME is not, and is no
//     output either.
//   - "--- PASS: X (0.01s)", FAIL, SKIP and BENCH, indented four spaces a level, are
//     held until a line flushes them: a report of the same level or above, a
//     marker, or the package's PASS or FAIL, so a test's result follows its
//     subtests'.
//   - Any other line is output of the test last named, or where it is indented, of
//     the held report at its depth.
//   - The marker lines, the reports and PASS and FAIL are output of the type
//     "frame".
func test2json(w io.Writer, pkg, out string, code int) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	emit := func(e testEvent) {
		now := time.Now()
		e.Time = &now
		e.Package = pkg
		enc.Encode(e)
	}
	emit(testEvent{Action: "start"})
	var report []testEvent
	name := "" // the test the output is of
	flush := func(depth int) {
		name = ""
		for len(report) > depth {
			emit(report[len(report)-1])
			report = report[:len(report)-1]
		}
	}
	output := func(line string, frame bool) {
		e := testEvent{Action: "output", Test: name, Output: line}
		if frame {
			e.OutputType = "frame"
		}
		emit(e)
	}
	skip := false
	var took *float64
	for _, line := range strings.SplitAfter(out, "\n") {
		if line == "" {
			continue
		}
		trim := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if trim == "=== NAME" {
			line, trim = "=== NAME  \n", "=== NAME  "
		}
		if trim == "PASS" || trim == "FAIL" || strings.HasPrefix(trim, "FAIL\t") {
			flush(0)
			output(line, true)
			continue
		}
		if strings.HasPrefix(line, "?   \t") && strings.HasSuffix(trim, "\t[no test files]") && len(report) == 0 {
			skip = true
		}
		if action, test, ok := testMarker(trim); ok {
			flush(0)
			name = test
			switch action {
			case "name":
			case "pause":
				output(line, true)
				emit(testEvent{Action: action, Test: name})
			default:
				emit(testEvent{Action: action, Test: name})
				output(line, true)
			}
			continue
		}
		indent, rest := 0, trim
		for strings.HasPrefix(rest, "    ") {
			rest = rest[4:]
			indent++
		}
		if action, test, elapsed, ok := testResult(rest); ok && len(report) >= indent {
			flush(indent)
			name = test
			report = append(report, testEvent{Action: action, Test: test, Elapsed: elapsed})
			output(line, true)
			continue
		}
		if indent > 0 && indent <= len(report) {
			name = report[indent-1].Test
		}
		if t, ok := packageTime(trim, pkg); ok {
			took = &t
		}
		output(line, false)
	}
	flush(0)
	result := testEvent{Action: "pass", Elapsed: took}
	switch {
	case code != 0:
		result.Action = "fail"
	case skip:
		result.Action = "skip"
	}
	emit(result)
}

// testMarker takes apart a line naming the test that follows, "=== RUN   TestX".
func testMarker(s string) (action, name string, ok bool) {
	for _, m := range []string{"=== RUN   ", "=== PAUSE ", "=== CONT  ", "=== NAME  "} {
		if rest, found := strings.CutPrefix(s, m); found {
			return strings.ToLower(strings.TrimSpace(m[4:])), strings.TrimSpace(rest), true
		}
	}
	return "", "", false
}

// testResult takes apart a result line, "--- PASS: TestX (0.01s)".
func testResult(s string) (action, name string, elapsed *float64, ok bool) {
	for _, r := range []string{"--- PASS: ", "--- FAIL: ", "--- SKIP: ", "--- BENCH: "} {
		rest, found := strings.CutPrefix(s, r)
		if !found {
			continue
		}
		name = strings.TrimSpace(rest)
		if i := strings.Index(name, " ("); i >= 0 {
			if strings.HasSuffix(name, "s)") {
				if t, err := strconv.ParseFloat(name[i+2:len(name)-2], 64); err == nil {
					elapsed = &t
				}
			}
			name = name[:i]
		}
		return strings.ToLower(strings.TrimSuffix(r[4:], ": ")), name, elapsed, true
	}
	return "", "", nil, false
}

// packageTime is the run's time on the package's line, "ok  \tpkg\t0.123s", which
// the package's result carries as its Elapsed: Go's is the converter's own time,
// and the board's run is what this one reports.
func packageTime(s, pkg string) (float64, bool) {
	for _, p := range []string{"ok  \t" + pkg + "\t", "FAIL\t" + pkg + "\t"} {
		if rest, found := strings.CutPrefix(s, p); found {
			if f := strings.Fields(rest); len(f) != 0 && strings.HasSuffix(f[0], "s") {
				if t, err := strconv.ParseFloat(strings.TrimSuffix(f[0], "s"), 64); err == nil {
					return t, true
				}
			}
		}
	}
	return 0, false
}
