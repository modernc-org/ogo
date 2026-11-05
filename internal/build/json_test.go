// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func buildJSONOf(t *testing.T, args ...string) (jsonBuild, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code, err := Build(append([]string{"--json"}, args...), nil, &out, &errb)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if errb.Len() != 0 {
		t.Errorf("stderr %q, want nothing: --json says everything on stdout", errb.String())
	}
	var r jsonBuild
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out.String())
	}
	if n := strings.Count(strings.TrimSpace(out.String()), "\n"); n != 0 {
		t.Errorf("stdout is %d lines, want one", n+1)
	}
	return r, code
}

// TestBuildJSON holds ogo build --json to what the IDE's handoff asks of it: one
// object, absolute paths, a diagnostic's end at its token's, the phase, and the
// exit status a build without the flag has.
func TestBuildJSON(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ok", "main.ogo"), "func main() {\n\tprintln(1)\n}\n")
	r, code := buildJSONOf(t, filepath.Join(dir, "ok"))
	if code != 0 || !r.OK || r.Library || len(r.Diagnostics) != 0 {
		t.Errorf("a program that builds: code %d, %+v", code, r)
	}
	if want := filepath.Join(dir, "ok", "ok.binary"); r.Binary != want || !fileExists(r.Binary) {
		t.Errorf("binary %q, want %q written", r.Binary, want)
	}
	if want := filepath.Join(dir, "ok", "ok.p2asm"); r.Listing != want {
		t.Errorf("listing %q, want %q", r.Listing, want)
	}

	// An error in an imported package names its file absolute, and ends where the
	// name does.
	write(t, filepath.Join(dir, "imp", "main.ogo"), "import \"lib\"\n\nfunc main() {\n\tprintln(lib.F())\n}\n")
	write(t, filepath.Join(dir, "imp", "lib", "lib.ogo"), "func F() int {\n\treturn undefinedName\n}\n")
	r, code = buildJSONOf(t, filepath.Join(dir, "imp"))
	want := jsonDiagnostic{File: filepath.Join(dir, "imp", "lib", "lib.ogo"), Line: 2, Col: 9, EndLine: 2, EndCol: 22, Severity: "error", Phase: "check", Message: "undefined: undefinedName"}
	if code != 1 || r.OK || len(r.Diagnostics) != 1 || r.Diagnostics[0] != want {
		t.Errorf("a checker error: code %d, %+v\nwant %+v", code, r.Diagnostics, want)
	}

	// The emitter's refusal is the checker's phase, without its "emit: ".
	write(t, filepath.Join(dir, "life", "main.ogo"), "func f() []byte {\n\tvar b [4]byte\n\treturn b[:]\n}\n\nfunc main() {\n\tprintln(len(f()))\n}\n")
	r, code = buildJSONOf(t, filepath.Join(dir, "life"))
	if code != 1 || len(r.Diagnostics) != 1 || r.Diagnostics[0].Phase != "check" || r.Diagnostics[0].Line != 3 || r.Diagnostics[0].Col != 9 ||
		!strings.HasPrefix(r.Diagnostics[0].Message, "cannot return a slice backed by local b") {
		t.Errorf("a lifetime refusal: code %d, %+v", code, r.Diagnostics)
	}

	// A library is checked and writes nothing.
	write(t, filepath.Join(dir, "lib", "lib.ogo"), "func F() int {\n\treturn 1\n}\n")
	r, code = buildJSONOf(t, filepath.Join(dir, "lib"))
	if code != 0 || !r.OK || !r.Library || r.Binary != "" {
		t.Errorf("a library: code %d, %+v", code, r)
	}

	// What the backend refuses is its phase, with what it said kept.
	write(t, filepath.Join(dir, "big", "main.ogo"), "var big [600000]byte\n\nfunc main() {\n\tbig[1] = 2\n\tprintln(big[1])\n}\n")
	r, code = buildJSONOf(t, filepath.Join(dir, "big"))
	if code != 1 || len(r.Diagnostics) != 1 || r.Diagnostics[0].Phase != "backend" || r.Diagnostics[0].File != "" ||
		!strings.Contains(r.Diagnostics[0].Message, "does not fit the P2's 512 KB of Hub RAM") || !strings.Contains(r.Output, "exceeds maximum") {
		t.Errorf("a program too big for the hub: code %d, %+v", code, r)
	}

	r, code = buildJSONOf(t, "--nosuch", filepath.Join(dir, "ok"))
	if code != 2 || len(r.Diagnostics) != 1 || r.Diagnostics[0].Phase != "usage" {
		t.Errorf("a bad flag: code %d, %+v", code, r.Diagnostics)
	}
}
