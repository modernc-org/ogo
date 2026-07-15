// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package build wires the OctoGo compile-and-load pipeline behind the `ogo
// build` and `ogo run` subcommands: check + emit C (internal/octogo), compile
// to a P2 binary (internal/flexcc), and — for run — load it onto a connected
// board (internal/loadp2). It is the walking skeleton of the toolchain's back
// half; the emitter it drives currently handles only a trivial program.
package build // import "modernc.org/ogo/internal/build"

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/ogo/internal/flexcc"
	"modernc.org/ogo/internal/loadp2"
	"modernc.org/ogo/internal/octogo"
)

// Build implements `ogo build <file.ogo>`: it produces a P2 <file>.binary (or
// the -o target). It returns a process exit code and, for control-flow errors,
// a Go error; tool diagnostics (checker, flexcc) go to stderr.
func Build(args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	_, code, err := compile(args, stdout, stderr)
	return code, err
}

// Run implements `ogo run <file.ogo>`: build, then load and run on a connected
// P2 board with an interactive terminal (loadp2 -t at the default baud).
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	bin, code, err := compile(args, stdout, stderr)
	if err != nil || code != 0 {
		return code, err
	}
	return loadp2.Load(loadp2.Options{Binary: bin, Terminal: true}), nil
}

// compile checks and emits C for the source file in args, then compiles it to a
// P2 binary with the embedded flexcc, returning the binary's absolute path.
func compile(args []string, stdout, stderr io.Writer) (binary string, code int, err error) {
	src, out, err := parseArgs(args)
	if err != nil {
		return "", 2, err
	}

	dir, base := filepath.Split(src)
	if dir == "" {
		dir = "."
	}
	pkg, err := octogo.Build(-1, []string{base}, os.DirFS(dir))
	if err != nil {
		return "", 1, err // checker diagnostics
	}

	var cbuf bytes.Buffer
	if err := octogo.EmitC(pkg, &cbuf); err != nil {
		return "", 1, err
	}

	// flexcc reads its input from disk, so stage the emitted C in a temp dir.
	tmp, err := os.MkdirTemp("", "ogo-build-*")
	if err != nil {
		return "", 1, err
	}
	defer os.RemoveAll(tmp)
	cFile := filepath.Join(tmp, strings.TrimSuffix(base, ".ogo")+".c")
	if err := os.WriteFile(cFile, cbuf.Bytes(), 0o644); err != nil {
		return "", 1, err
	}

	if out == "" {
		out = strings.TrimSuffix(src, ".ogo") + ".binary"
	}
	if out, err = filepath.Abs(out); err != nil {
		return "", 1, err
	}

	// flexcc.Main auto-injects the embedded flexprop P2 include tree.
	if err := flexcc.Main(nil, stdout, stderr, []string{"-2", "-o", out, cFile}); err != nil {
		return "", 1, fmt.Errorf("flexcc: %v", err)
	}
	return out, 0, nil
}

// parseArgs pulls the single source file and optional -o output from args.
func parseArgs(args []string) (src, out string, err error) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-o":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("build: -o requires an argument")
			}
			out = args[i]
		case strings.HasPrefix(a, "-"):
			return "", "", fmt.Errorf("build: unknown flag %q", a)
		default:
			if src != "" {
				return "", "", fmt.Errorf("build: multiple source files are not supported yet")
			}
			src = a
		}
	}
	if src == "" {
		return "", "", fmt.Errorf("build: no source file specified")
	}
	return src, out, nil
}
