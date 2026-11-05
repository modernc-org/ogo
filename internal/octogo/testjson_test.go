// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnBoardOgoTestJSON runs `ogo test --json` on the board: the events go test
// -json writes, a run, the output and a result for each test and subtest, a
// test's result after its subtests', and the package's result last, by the exit
// status.
func TestOnBoardOgoTestJSON(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	dir := t.TempDir()
	for name, src := range map[string]string{
		"add.ogo": "func Add(a, b int) int {\n\treturn a + b\n}\n",
		"add_test.ogo": `import "testing"

func TestAdd(t *testing.T) {
	t.Log("adding")
	if Add(1, 2) != 3 {
		t.Error("wrong")
	}
}

func TestSubs(t *testing.T) {
	t.Run("ok", func(t *testing.T) {})
	t.Run("bad", func(t *testing.T) { t.Error("off by one") })
}

func TestSkipped(t *testing.T) {
	t.Skip("not here")
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	type ev struct {
		Action, Test, Output string
	}
	want := []ev{
		{"start", "", ""},
		{"run", "TestAdd", ""},
		{"output", "TestAdd", "=== RUN   TestAdd\n"},
		{"output", "TestAdd", "    add_test.ogo:4: adding\n"},
		{"output", "TestAdd", "--- PASS: TestAdd"},
		{"pass", "TestAdd", ""},
		{"run", "TestSubs", ""},
		{"output", "TestSubs", "=== RUN   TestSubs\n"},
		{"run", "TestSubs/ok", ""},
		{"output", "TestSubs/ok", "=== RUN   TestSubs/ok\n"},
		{"run", "TestSubs/bad", ""},
		{"output", "TestSubs/bad", "=== RUN   TestSubs/bad\n"},
		{"output", "TestSubs/bad", "    add_test.ogo:12: off by one\n"},
		{"output", "TestSubs", "--- FAIL: TestSubs"},
		{"output", "TestSubs/ok", "    --- PASS: TestSubs/ok"},
		{"pass", "TestSubs/ok", ""},
		{"output", "TestSubs/bad", "    --- FAIL: TestSubs/bad"},
		{"fail", "TestSubs/bad", ""},
		{"fail", "TestSubs", ""},
		{"run", "TestSkipped", ""},
		{"output", "TestSkipped", "=== RUN   TestSkipped\n"},
		{"output", "TestSkipped", "    add_test.ogo:16: not here\n"},
		{"output", "TestSkipped", "--- SKIP: TestSkipped"},
		{"skip", "TestSkipped", ""},
		{"output", "", "FAIL\n"},
		{"output", "", "FAIL\t"},
		{"fail", "", ""},
	}
	for attempt := 1; ; attempt++ {
		cmd := exec.Command(ogo, "test", "--json", "-p", port, ".")
		cmd.Dir = dir
		out, err := cmd.Output()
		var got []ev
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			var e ev
			if json.Unmarshal([]byte(line), &e) != nil {
				got = nil
				break
			}
			got = append(got, e)
		}
		exitErr, failed := err.(*exec.ExitError)
		ok := failed && exitErr.ExitCode() == 1 && len(got) == len(want)
		for i := 0; ok && i < len(want); i++ {
			// A result's time and the package's line end differ from run to run.
			g, w := got[i], want[i]
			ok = g.Action == w.Action && g.Test == w.Test && strings.HasPrefix(g.Output, w.Output)
		}
		if ok {
			return
		}
		if attempt == boardAttempts {
			t.Fatalf("ogo test --json: %v\ngot:\n%s", err, out)
		}
		t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
	}
}
