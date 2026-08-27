// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package format

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExcludeSpelling guards the flag spellings, which is where this went wrong:
// only "--exclude" was accepted while "ogo help fmt" documented "-exclude", so a
// reader who followed the help got "unexpected flag: -exclude". The Makefile uses
// the long form and every other multi-letter flag in this tool takes either, so
// both are accepted and both are pinned here.
func TestExcludeSpelling(t *testing.T) {
	dir := t.TempDir()
	// Two files needing formatting, one of which every case below excludes.
	for _, name := range []string{"keep.ogo", "skip.ogo"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("func  main( ) {\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"-l", "-exclude", "skip", dir},
		{"-l", "-exclude=skip", dir},
		{"-l", "--exclude", "skip", dir},
		{"-l", "--exclude=skip", dir},
	} {
		var out, errb bytes.Buffer
		rc, err := SubCommand(args, nil, &out, &errb)
		if err != nil || rc != 0 {
			t.Errorf("%v: rc=%d err=%v stderr=%q", args, rc, err, errb.String())
			continue
		}
		got := out.String()
		if !strings.Contains(got, "keep.ogo") {
			t.Errorf("%v: want keep.ogo listed, got %q", args, got)
		}
		if strings.Contains(got, "skip.ogo") {
			t.Errorf("%v: want skip.ogo excluded, got %q", args, got)
		}
	}
}

// TestNoFlagsPrintsAnUnchangedFile: with no flags the formatted source goes to
// stdout WHETHER OR NOT it differs, as gofmt does. It used to be printed only when
// formatting changed something, so `ogo fmt x.ogo > y.ogo` on an already-formatted
// file wrote an empty y.ogo -- the source asked for, replaced by nothing.
func TestNoFlagsPrintsAnUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ogo")
	const formatted = "func main() {\n\tprintln(1)\n}\n"
	if err := os.WriteFile(path, []byte(formatted), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if rc, err := SubCommand([]string{path}, nil, &out, &errb); err != nil || rc != 0 {
		t.Fatalf("rc=%d err=%v stderr=%q", rc, err, errb.String())
	}
	if got := out.String(); got != formatted {
		t.Errorf("stdout = %q, want the file itself %q", got, formatted)
	}
	// -l has nothing to list, and -w nothing to write: neither prints the source.
	for _, flag := range []string{"-l", "-w"} {
		var lout, lerr bytes.Buffer
		if rc, err := SubCommand([]string{flag, path}, nil, &lout, &lerr); err != nil || rc != 0 {
			t.Fatalf("%s: rc=%d err=%v stderr=%q", flag, rc, err, lerr.String())
		}
		if got := lout.String(); got != "" {
			t.Errorf("%s on an unchanged file printed %q", flag, got)
		}
	}
}

// TestNoFlagsWritesNothing pins what "ogo fmt" with no flags does: it prints the
// formatted source and leaves the file alone. The help said "compared and nothing
// is written", which read as though it printed nothing either.
func TestNoFlagsWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ogo")
	const unformatted = "func  main( ) {\n}\n"
	if err := os.WriteFile(path, []byte(unformatted), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if rc, err := SubCommand([]string{path}, nil, &out, &errb); err != nil || rc != 0 {
		t.Fatalf("rc=%d err=%v stderr=%q", rc, err, errb.String())
	}
	if got := out.String(); !strings.Contains(got, "func main()") {
		t.Errorf("want the formatted source on stdout, got %q", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != unformatted {
		t.Errorf("the file was rewritten without -w: %q", b)
	}
}
