// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"time"
)

// Main is the entry point for the octosmith fuzzer.
// It parses arguments, initializes the deterministic RNG, and drives generation.
func Main(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("octosmith", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var seed int64
	fs.Int64Var(&seed, "seed", 0, "Seed for the random number generator (0 = use current time)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	// Initialize the fuzzer state
	f := NewFuzzer(seed, stdout)

	// Output the package and import declarations (OctoGo omits 'package' clause)
	// SourceFile = { ImportDecl ";" } { TopLevelDecl ";" } .
	fmt.Fprintf(stdout, "// OctoSmith generated program. Seed: %d\n", seed)

	// This function will be defined in gemini.go
	// It drives the TopLevelDecl generation.
	err := f.GenerateProgram()
	if err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}

	return nil
}

// Fuzzer holds the global state for the generation process.
type Fuzzer struct {
	Rand       *rand.Rand
	Out        io.Writer
	GlobalEnv  *Scope
	CurrentEnv *Scope

	// Hardware limits tracking
	CogCount int // Max 8

	// Checksum variable name to ensure deterministic execution validation
	ChecksumName string
}

func NewFuzzer(seed int64, out io.Writer) *Fuzzer {
	rng := rand.New(rand.NewSource(seed))
	global := NewScope(nil)

	return &Fuzzer{
		Rand:         rng,
		Out:          out,
		GlobalEnv:    global,
		CurrentEnv:   global,
		CogCount:     1, // Main starts on the first Cog
		ChecksumName: "octosmith_checksum",
	}
}
