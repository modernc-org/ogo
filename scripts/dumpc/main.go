// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command dumpc writes the C the tree's compiler emits for the OctoGo program in
// one directory, with the run-time checks on -- what TestEmitCRun hands the host
// C compiler. It is the compiler half of the probe scripts beside it, which build
// it from the tree being probed:
//
//	go run ./scripts/dumpc DIR > host.c
//
// A refusal, the checker's or the emitter's, goes to stderr and leaves stdout
// empty, which is how the scripts tell a refused program from a compiled one.
//
// It lives here, under a tracked directory, because its first home did not: the
// scripts were committed building ./build/dumpc, and /build/ is git-ignored, so
// the harness meant to survive a change of machine arrived on the next one
// without the one tool it builds.
package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"modernc.org/ogo/internal/octogo"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dumpc DIR")
		os.Exit(2)
	}
	if err := dump(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// dump emits the program in dir to stdout, or nothing at all: the C is buffered
// so that a refusal part of the way through leaves no half of a program behind.
func dump(dir string) error {
	des, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	// The files `ogo build` would take: the directory's .ogo sources, without its
	// tests.
	var files []string
	for _, de := range des {
		if nm := de.Name(); !de.IsDir() && strings.HasSuffix(nm, ".ogo") && !strings.HasSuffix(nm, "_test.ogo") {
			files = append(files, nm)
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("no .ogo source files in %s", dir)
	}
	pkg, err := octogo.Build(-1, files, os.DirFS(dir))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := octogo.EmitC(pkg, &buf, octogo.Checked()); err != nil {
		return err
	}
	_, err = os.Stdout.Write(buf.Bytes())
	return err
}
