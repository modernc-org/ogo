// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command flexcc runs the tree's in-process flexcc -- internal/flexcc, the
// transpiled backend `ogo build` compiles with, and its embedded P2 include tree --
// on its own command line. What ogo build does with the C it emits is
//
//	flexcc -2 -o prog.binary prog.c
//
// It exists to compare a backend with another one (scripts/cccorpus.sh): the
// regenerated transpile with a native build of the spin2cpp commit it was made
// from, which is what makes it FAITHFUL, and the tree built for one platform with
// the same tree built for another, which is what makes the five backends one.
// Build it from the tree being measured, `go build -o X ./scripts/flexcc`, and
// for another platform with GOOS and GOARCH, as any Go program.
package main

import (
	"fmt"
	"os"

	"modernc.org/ogo/internal/flexcc"
)

func main() {
	if err := flexcc.Main(nil, os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
