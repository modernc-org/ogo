// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"modernc.org/ogo/internal/diag"
)

// jsonFlag reports whether args ask for --json.
func jsonFlag(args []string) bool {
	return slices.ContainsFunc(args, isJSONFlag)
}

func isJSONFlag(a string) bool { return a == "--json" || a == "-json" }

func withoutJSON(args []string) []string {
	return slices.DeleteFunc(slices.Clone(args), isJSONFlag)
}

// jsonDiagnostic is one error or warning of `ogo build --json`. The file is
// absolute, lines and columns count from 1 and a column counts bytes; the end is
// that of the token at the position, or of the line where none begins there. An
// error with no position has no file and lines of 0.
type jsonDiagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	EndLine  int    `json:"endLine"`
	EndCol   int    `json:"endCol"`
	Severity string `json:"severity"` // "error" or "warning"
	Phase    string `json:"phase"`    // "check", "backend" or "usage"
	Message  string `json:"message"`
}

// jsonBuild is what `ogo build --json` writes, one object on stdout.
type jsonBuild struct {
	OK          bool             `json:"ok"`
	Library     bool             `json:"library"` // no func main: checked, and no binary
	Binary      string           `json:"binary"`  // written, or ""
	Listing     string           `json:"listing"` // the binary's .p2asm, or ""
	C           string           `json:"c"`       // the C kept beside a refused build, or ""
	Diagnostics []jsonDiagnostic `json:"diagnostics"`
	Output      string           `json:"output"`           // what the build would have printed but for the flag
	Report      string           `json:"report,omitempty"` // --report's text
	Stats       struct {
		MS int64 `json:"ms"`
	} `json:"stats"`
}

// buildJSON is `ogo build --json`: the build as Build makes it, its outcome written
// as one JSON object on stdout and nothing on stderr, and the exit status Build's.
func buildJSON(args []string, stdout io.Writer) (int, error) {
	t0 := time.Now()
	var o buildOutputs
	var said bytes.Buffer
	bin, code, err := compileOut(args, &said, &said, &o)
	r := jsonBuild{
		OK:          code == 0 && err == nil,
		Library:     o.library,
		Output:      said.String(),
		Report:      o.report.String(),
		Diagnostics: []jsonDiagnostic{},
	}
	if r.OK && !o.library {
		r.Binary = bin
		if listing := strings.TrimSuffix(bin, filepath.Ext(bin)) + ".p2asm"; fileExists(listing) {
			r.Listing = listing
		}
	}
	if err != nil && o.keepC != "" && strings.Contains(err.Error(), o.keepC) {
		r.C = o.keepC
	}
	phase := "backend"
	switch {
	case code == 2:
		phase = "usage"
	case o.stage != "":
		phase = o.stage
	}
	for _, d := range diag.FromError(err, phase) {
		jd := jsonDiagnostic{Line: d.Line, Col: d.Col, EndLine: d.Line, EndCol: d.Col, Severity: "error", Phase: d.Phase, Message: d.Message}
		if d.File != "" && o.root != "" {
			jd.File = filepath.Join(o.root, filepath.FromSlash(d.File))
			src, _ := os.ReadFile(jd.File)
			d.SetEnd(src)
			jd.EndLine, jd.EndCol = d.EndLine, d.EndCol
		}
		r.Diagnostics = append(r.Diagnostics, jd)
	}
	if r.OK && !o.library {
		// A clean build is silent: what a passing build said is the C backend's
		// warnings, which --allow-backend-warnings let through.
		for _, line := range strings.Split(said.String(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				r.Diagnostics = append(r.Diagnostics, jsonDiagnostic{Severity: "warning", Phase: "backend", Message: line})
			}
		}
	}
	r.Stats.MS = time.Since(t0).Milliseconds()
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return 1, err
	}
	return code, nil
}

func fileExists(fn string) bool {
	fi, err := os.Stat(fn)
	return err == nil && !fi.IsDir()
}
