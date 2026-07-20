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

// compile checks and emits C for the package named by args, then compiles it to a
// P2 binary with the embedded flexcc, returning the binary's absolute path. The
// package's files become one C translation unit, so a multi-file package is a
// single flexcc invocation.
func compile(args []string, stdout, stderr io.Writer) (binary string, code int, err error) {
	srcs, out, release, unchecked, err := parseArgs(args)
	if err != nil {
		return "", 2, err
	}
	dir, files, defaultOut, err := resolvePackage(srcs)
	if err != nil {
		return "", 2, err
	}

	pkg, err := octogo.Build(-1, files, os.DirFS(dir))
	if err != nil {
		return "", 1, err // checker diagnostics
	}

	// Runtime bounds / divide-by-zero checks are on by default (a debug build):
	// --unchecked omits them, --release reboots on a panic instead of halting.
	var emitOpts []octogo.EmitOption
	if !unchecked {
		emitOpts = append(emitOpts, octogo.Checked())
	}
	if release {
		emitOpts = append(emitOpts, octogo.Release())
	}
	var cbuf bytes.Buffer
	if err := octogo.EmitC(pkg, &cbuf, emitOpts...); err != nil {
		return "", 1, err
	}

	// flexcc reads its input from disk, so stage the emitted C in a temp dir.
	tmp, err := os.MkdirTemp("", "ogo-build-*")
	if err != nil {
		return "", 1, err
	}
	defer os.RemoveAll(tmp)
	cFile := filepath.Join(tmp, strings.TrimSuffix(filepath.Base(defaultOut), ".binary")+".c")
	if err := os.WriteFile(cFile, cbuf.Bytes(), 0o644); err != nil {
		return "", 1, err
	}

	if out == "" {
		out = defaultOut
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

// parseArgs pulls the positional source arguments, the optional -o output, and
// the --release / --unchecked build-mode flags from args.
func parseArgs(args []string) (srcs []string, out string, release, unchecked bool, err error) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-o":
			i++
			if i >= len(args) {
				return nil, "", false, false, fmt.Errorf("build: -o requires an argument")
			}
			out = args[i]
		case a == "--release" || a == "-release":
			release = true
		case a == "--unchecked" || a == "-unchecked":
			unchecked = true
		case strings.HasPrefix(a, "-"):
			return nil, "", false, false, fmt.Errorf("build: unknown flag %q", a)
		default:
			srcs = append(srcs, a)
		}
	}
	return srcs, out, release, unchecked, nil
}

// resolvePackage turns the positional arguments into the directory holding the
// package, the base names of its sources, and the default output path.
//
// A package is a directory, so no argument means the current directory and a
// single directory argument means that directory -- in both cases every .ogo file
// in it is compiled together. Anything else is an explicit list of source files,
// which must all live in one directory for the same reason.
//
// The binary is named after the package directory and written beside it, except
// for the single-named-file form, which keeps the file's own name (x.ogo ->
// x.binary).
func resolvePackage(srcs []string) (dir string, files []string, out string, err error) {
	switch {
	case len(srcs) == 0:
		dir = "."
	case len(srcs) == 1 && isDir(srcs[0]):
		dir = srcs[0]
	default:
		for _, src := range srcs {
			switch d := filepath.Dir(src); {
			case dir == "":
				dir = d
			case d != dir:
				return "", nil, "", fmt.Errorf("build: all source files must be in one directory, got %s and %s", dir, d)
			}
			files = append(files, filepath.Base(src))
		}
		if len(srcs) == 1 {
			return dir, files, strings.TrimSuffix(srcs[0], ".ogo") + ".binary", nil
		}
		return dir, files, filepath.Join(dir, dirPkgName(dir)+".binary"), nil
	}
	if files, err = packageFiles(dir); err != nil {
		return "", nil, "", err
	}
	return dir, files, filepath.Join(dir, dirPkgName(dir)+".binary"), nil
}

// packageFiles lists a directory's .ogo sources, in the stable order os.ReadDir
// gives. Files ending in _test.ogo are test files and are not part of a build.
func packageFiles(dir string) (r []string, err error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("build: %v", err)
	}
	for _, de := range des {
		switch nm := de.Name(); {
		case de.IsDir(), !strings.HasSuffix(nm, ".ogo"), strings.HasSuffix(nm, "_test.ogo"):
			// not a source file of this package
		default:
			r = append(r, nm)
		}
	}
	if len(r) == 0 {
		return nil, fmt.Errorf("build: no .ogo source files in %s", dir)
	}
	return r, nil
}

// dirPkgName is the name a directory's binary takes: the directory's own name,
// resolved so that "." and ".." become the real name rather than a dot.
func dirPkgName(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Base(abs)
	}
	return filepath.Base(dir)
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
