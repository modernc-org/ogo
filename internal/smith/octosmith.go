// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith // import "modernc.org/octogo/lib/internal/smith"

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"

	"modernc.org/ogo/internal/octogo"
	"modernc.org/opt"
)

// SubCommand implements "ogo smith".
func SubCommand(args []string, stdout, stderr io.Writer) (rc int, err error) {
	set := opt.NewSet()
	var args2 []string
	set.Arg("seed", false, func(opt, arg string) error { args2 = append(args2, "-seed", arg); return nil })
	if err := set.Parse(args, func(arg string) error {
		switch {
		case strings.HasPrefix(arg, "-"):
			rc = 2
			return fmt.Errorf("unexpected flag: %v", arg)
		default:
			rc = 2
			return fmt.Errorf("no non-flag arguments expected: %v", arg)
		}
		return nil
	}); err != nil {
		return 2, fmt.Errorf("%v", err)
	}

	b := bytes.NewBuffer(nil)
	if err := Main(args2, b, stderr); err != nil {
		return 1, fmt.Errorf("octosmith err=%v", err)
	}

	return rc, nil
}

//TODO given the same seed, the results are not reproducible.

// Main is the entry point for the octosmith fuzzer as a standalone executable.
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

	b := bytes.NewBuffer(nil)

	// Initialize the fuzzer state
	f := NewFuzzer(seed, b)

	// Output the package and import declarations (OctoGo omits 'package' clause)
	// SourceFile = { ImportDecl ";" } { TopLevelDecl ";" } .
	fmt.Fprintf(b, "// OctoSmith generated program. Seed: %d\n", seed)

	// This function will be defined in gemini.go
	// It drives the TopLevelDecl generation.
	err := f.GenerateProgram(NewMachine(), NewMemory())
	if err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}

	return octogo.FormatFile("octosmith.ogo", b.Bytes(), stdout)
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
