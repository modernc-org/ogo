// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestResolvePackage covers how the command line names a package: a package is a
// directory, so no argument means the current one, a directory argument means that
// one, and explicit files must agree on a directory. It also pins the output
// naming, which differs between the single-named-file form and the rest.
func TestResolvePackage(t *testing.T) {
	dir := t.TempDir()
	for _, nm := range []string{"main.ogo", "aux.ogo", "helper_test.ogo", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, nm), []byte("// x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "other.ogo"), []byte("// x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	base := filepath.Base(dir)
	for _, test := range []struct {
		name  string
		srcs  []string
		files []string // expected base names, nil when an error is expected
		out   string   // expected default output path
		err   string   // substring of the expected error
	}{
		{
			// A directory takes every .ogo in it -- but not _test.ogo (a test file)
			// nor a non-.ogo file nor a subdirectory -- and is named after itself.
			name:  "directory",
			srcs:  []string{dir},
			files: []string{"aux.ogo", "main.ogo"},
			out:   filepath.Join(dir, base+".binary"),
		},
		{
			// One named file compiles only itself and keeps its own name, matching
			// `go build main.go`.
			name:  "single file keeps its name",
			srcs:  []string{filepath.Join(dir, "main.ogo")},
			files: []string{"main.ogo"},
			out:   filepath.Join(dir, "main.binary"),
		},
		{
			name:  "explicit file list",
			srcs:  []string{filepath.Join(dir, "main.ogo"), filepath.Join(dir, "aux.ogo")},
			files: []string{"main.ogo", "aux.ogo"},
			out:   filepath.Join(dir, base+".binary"),
		},
		{
			// A package is one directory, so files may not straddle two.
			name: "files from two directories",
			srcs: []string{filepath.Join(dir, "main.ogo"), filepath.Join(sub, "other.ogo")},
			err:  "must be in one directory",
		},
		{
			name: "directory with no sources",
			srcs: []string{empty},
			err:  "no .ogo source files",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotDir, gotFiles, gotOut, err := resolvePackage(test.srcs)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("want error containing %q, got %v", test.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePackage: %v", err)
			}
			if !slices.Equal(gotFiles, test.files) {
				t.Errorf("files: got %v, want %v", gotFiles, test.files)
			}
			if gotOut != test.out {
				t.Errorf("out: got %q, want %q", gotOut, test.out)
			}
			if gotDir != dir {
				t.Errorf("dir: got %q, want %q", gotDir, dir)
			}
		})
	}

	// No argument at all means the current directory.
	t.Run("no arguments", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		gotDir, gotFiles, _, err := resolvePackage(nil)
		if err != nil {
			t.Fatalf("resolvePackage: %v", err)
		}
		if gotDir != "." {
			t.Errorf("dir: got %q, want %q", gotDir, ".")
		}
		if want := []string{"aux.ogo", "main.ogo"}; !slices.Equal(gotFiles, want) {
			t.Errorf("files: got %v, want %v", gotFiles, want)
		}
	})
}

// TestParseArgs pins the flag handling, in particular that several positional
// arguments are now collected rather than refused.
func TestParseArgs(t *testing.T) {
	srcs, out, release, unchecked, err := parseArgs([]string{"a.ogo", "b.ogo", "-o", "x.binary", "--release", "--unchecked"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if want := []string{"a.ogo", "b.ogo"}; !slices.Equal(srcs, want) {
		t.Errorf("srcs: got %v, want %v", srcs, want)
	}
	if out != "x.binary" || !release || !unchecked {
		t.Errorf("got out=%q release=%v unchecked=%v", out, release, unchecked)
	}
	if _, _, _, _, err := parseArgs([]string{"-o"}); err == nil {
		t.Error("-o without an argument: want an error")
	}
	if _, _, _, _, err := parseArgs([]string{"-nope"}); err == nil {
		t.Error("unknown flag: want an error")
	}
}
