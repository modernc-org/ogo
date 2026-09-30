// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"testing/fstest"

	"modernc.org/ogo/internal/octogo"
)

// TestOracle is the fuzzer exercising the compiler: it generates a fixed corpus
// of seeds, compiles each generated program to C and runs it on the host P2 shim,
// and requires a clean exit. A generated program self-checks a running checksum
// and panics on a mismatch, so a non-zero exit implicates the compiler; a
// Build/EmitC/cc failure implicates the generator. The seeds are fixed, so this is
// a stable regression, not a live fuzz -- widen oracleSeeds to hunt for new bugs.
// Skipped when no C compiler is available.
func TestOracle(t *testing.T) {
	const oracleSeeds = 100

	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found; skipping the smith oracle run")
	}
	shim, err := filepath.Abs(filepath.Join("..", "octogo", "testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}

	// singleExits counts the seeds whose C has a function written to leave through
	// one return: the lowering a function of several returns exists to reach (see
	// FuncDef.Guards), and one the generator stops reaching in silence if the
	// emitter's inlining budget and its guardSites drift apart. It is asked only of
	// a run of every seed, not of one picked with -run.
	singleExits, ran := 0, 0
	defer func() {
		t.Logf("a function written to leave through one return in %d of %d seeds", singleExits, ran)
		if !t.Failed() && ran == oracleSeeds && singleExits < oracleSeeds/2 {
			t.Errorf("a function rewritten to leave through one return in %d of %d seeds: "+
				"the functions of several returns are no longer marked for inlining "+
				"(see guardSites)", singleExits, oracleSeeds)
		}
	}()
	for seed := 1; seed <= oracleSeeds; seed++ {
		t.Run(strconv.Itoa(seed), func(t *testing.T) {
			ran++
			var prog bytes.Buffer
			if err := Main([]string{"-seed", strconv.Itoa(seed)}, &prog, io.Discard); err != nil {
				t.Fatalf("generate: %v", err)
			}
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: prog.Bytes()}}
			pkg, err := octogo.Build(-1, []string{"main.ogo"}, fsys)
			if err != nil {
				t.Fatalf("Build (generator bug?): %v\n%s", err, prog.String())
			}
			var c bytes.Buffer
			if err := octogo.EmitC(pkg, &c, octogo.Checked(), octogo.Inline()); err != nil {
				t.Fatalf("EmitC (generator bug?): %v\n%s", err, prog.String())
			}
			if bytes.Contains(c.Bytes(), []byte("goto _ogo_exit")) {
				singleExits++
			}
			dir := t.TempDir()
			csrc := filepath.Join(dir, "main.c")
			if err := os.WriteFile(csrc, c.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "prog")
			if out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-I", shim, "-o", bin, csrc, "-lpthread").CombinedOutput(); err != nil {
				t.Fatalf("cc: %v\n%s\n--- program ---\n%s", err, out, prog.String())
			}
			if got, err := exec.Command(bin).CombinedOutput(); err != nil {
				t.Fatalf("oracle failure (compiler bug?): %v\n%s\n--- program ---\n%s", err, got, prog.String())
			}
		})
	}
}
