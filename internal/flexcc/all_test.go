// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flexcc

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// versionLineRE matches the second line of the flexcc banner, e.g.
//
//	Version 7.7.0-HEAD-v7.7.0 Compiled on: Jul 20 2026
//
// It embeds the pinned build's version string (from the spin2cpp submodule) and
// the C __DATE__ of the transpile, both of which change every time the backend
// is regenerated. TestMainHelp normalizes this one line so regenerating against
// the same flexprop pin no longer forces a golden edit; the rest of the --help
// output is still matched verbatim.
var versionLineRE = regexp.MustCompile(`(?m)^Version .* Compiled on: .*$`)

// wantHelp is the expected `flexcc -h` output with the volatile version line
// (see versionLineRE) replaced by a fixed placeholder.
const wantHelp = `FlexC compiler (c) 2011-2026 Total Spectrum Software Inc. and contributors
Version <normalized>
usage: flexcc [options] file1.c file2.c ...
  [ --help ]         display this help
  [ -1bc ]           compile for Prop1 ROM bytecode
  [ -2 ]             compile for Prop2
  [ -2nu ]           compile for Prop2 bytecode
  [ -c ]             output only .o file
  [ -D <define> ]    add a define
  [ -g ]             include debug info in output
  [ -L or -I <path> ] add a directory to the include path
  [ -MMD ]           generate Make dependency file
  [ -o <name> ]      set output filename to <name>
  [ -O# ]            set optimization level:
          -O0 = no optimization
          -O1 = basic optimization
          -O2 = all optimization
  [ -Wall ]          enable warnings for language extensions and other features
  [ -Werror ]        make warnings into errors
  [ -Wabs-paths ]    print absolute paths for file names in errors/warnings
  [ -Wmax-errors=N ] allow at most N errors in a pass before stopping
  [ -x ]             capture program exit code (for testing)
  [ --code=cog ]     compile for COG mode instead of LMM
  [ --fcache=N ]     set FCACHE size to N (0 to disable)
  [ --fixedreal ]    use 16.16 fixed point in place of floats
  [ --lmm=xxx ]      use alternate LMM implementation for P1
           xxx = orig uses original flexspin LMM
           xxx = slow uses traditional (slow) LMM
  [ --nostdlib]      skip searching in the standard library location for include files
  [ --sizes]         print info about program sizes
  [ --verbose ]      print additional diagnostic messages (for debugging the compiler)
  [ --version ]      just show compiler version
  [ --zip ]          create zip archive of source files`

// TestMainHelp verifies `flexcc -h` exits with status 2 and prints the expected
// help text. The version/date banner line is normalized (see versionLineRE), so
// regenerating the backend against the same flexprop pin does not require
// updating this golden; a genuine change in the --help surface still fails.
func TestMainHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Main(nil, &stdout, &stderr, []string{"-h"})
	ec, ok := err.(exitCode)
	if !ok {
		t.Fatalf("Main(-h) = %T(%v), want exitCode", err, err)
	}
	if g, e := ec, exitCode(2); g != e {
		t.Fatalf("Main(-h) exit code = %v, want %v", g, e)
	}

	out := stdout.String()
	if !versionLineRE.MatchString(out) {
		t.Fatalf("help output missing 'Version ... Compiled on: ...' banner line:\n%s", out)
	}
	got := versionLineRE.ReplaceAllString(out, "Version <normalized>")
	if strings.TrimSpace(got) != strings.TrimSpace(wantHelp) {
		t.Errorf("flexcc -h mismatch (version line normalized):\n--- got ---\n%s\n--- want ---\n%s", got, wantHelp)
	}
}

// TestMainCompile drives the in-repo Go flexcc through a full compile+link of a
// tiny P2 program that pulls in <stdio.h> and <propeller2.h>, proving the
// backend compiles end to end with NO external flexprop install: the include
// path comes entirely from the embedded p2include.tar.gz (see p2include.go).
//
// The test forces the embedded path by clearing FLEXPROP_INCLUDE and points the
// extraction cache at a temp dir so it stays hermetic and self-cleaning.
func TestMainCompile(t *testing.T) {
	t.Setenv("FLEXPROP_INCLUDE", "") // ignore any ambient external tree
	t.Setenv("OGO_FLEXCC_CACHE", t.TempDir())

	dir := t.TempDir()
	src := filepath.Join(dir, "hello.c")
	out := filepath.Join(dir, "hello.binary")
	const prog = `#include <stdio.h>
#include <propeller2.h>
int main(void){ printf("hi %u\n", _clkfreq); _pinh(56); return 0; }
`
	if err := os.WriteFile(src, []byte(prog), 0666); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Main(nil, &stdout, &stderr, []string{"-2", "-o", out, src})
	if s := stdout.String(); s != "" {
		t.Logf("flexcc stdout:\n%s", s)
	}
	if s := stderr.String(); s != "" {
		t.Logf("flexcc stderr:\n%s", s)
	}
	if err != nil {
		t.Fatalf("Main compile = %v, want nil", err)
	}

	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output binary %q missing: %v", out, err)
	}
	if fi.Size() == 0 {
		t.Fatalf("output binary %q is empty", out)
	}
	t.Logf("Go flexcc produced %s (%d bytes)", out, fi.Size())
}
