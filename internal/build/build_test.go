// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"modernc.org/ogo/internal/octogo"
)

// TestResolvePackage covers how the command line names a package: a package is a
// directory, so no argument means the current one, a directory argument means that
// one, and explicit files must agree on a directory. It also pins the output
// naming, which differs between the single-named-file form and the rest.
func TestResolvePackage(t *testing.T) {
	dir := t.TempDir()
	for _, nm := range []string{"main.ogo", "alt.ogo", "helper_test.ogo", "notes.txt"} {
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
			files: []string{"alt.ogo", "main.ogo"},
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
			srcs:  []string{filepath.Join(dir, "main.ogo"), filepath.Join(dir, "alt.ogo")},
			files: []string{"main.ogo", "alt.ogo"},
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
		{
			// Anything that is not a directory is taken for a source file, so a
			// mistyped path arrives as one and must be reported as the path that
			// was typed rather than by whoever fails to open its base name.
			name: "a path that is not there",
			srcs: []string{filepath.Join(dir, "sensr")},
			err:  filepath.Join(dir, "sensr") + ": no such file or directory",
		},
		{
			name: "a file that is not .ogo",
			srcs: []string{filepath.Join(dir, "notes.txt")},
			err:  "named source files must be .ogo files",
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
		if want := []string{"alt.ogo", "main.ogo"}; !slices.Equal(gotFiles, want) {
			t.Errorf("files: got %v, want %v", gotFiles, want)
		}
	})
}

// TestParseArgs pins the flag handling, in particular that several positional
// arguments are now collected rather than refused.
func TestParseArgs(t *testing.T) {
	srcs, f, err := parseArgs([]string{"a.ogo", "b.ogo", "-o", "x.binary", "--release", "--unchecked"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if want := []string{"a.ogo", "b.ogo"}; !slices.Equal(srcs, want) {
		t.Errorf("srcs: got %v, want %v", srcs, want)
	}
	if f.out != "x.binary" || !f.release || !f.unchecked {
		t.Errorf("got out=%q release=%v unchecked=%v", f.out, f.release, f.unchecked)
	}
	if f.clock != 0 {
		t.Errorf("clock: got %d, want 0 -- an unasked-for clock is the backend's to pick", f.clock)
	}
	if f.xtal != octogo.DefaultXtal {
		t.Errorf("xtal: got %d, want the %d default", f.xtal, octogo.DefaultXtal)
	}
	if _, _, err := parseArgs([]string{"-o"}); err == nil {
		t.Error("-o without an argument: want an error")
	}
	if _, _, err := parseArgs([]string{"-nope"}); err == nil {
		t.Error("unknown flag: want an error")
	}

	// A clock may be written in Hz or with the suffix it is usually spoken in --
	// nine digits are easy to write with the wrong number of zeros.
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"--clock", "200000000"}, 200000000},
		{[]string{"--clock", "200MHz"}, 200000000},
		{[]string{"-clock", "200mhz"}, 200000000},
		{[]string{"--clock", "160MHz"}, 160000000},
	} {
		_, f, err := parseArgs(append(tc.args, "a.ogo"))
		if err != nil {
			t.Errorf("parseArgs(%v): %v", tc.args, err)
			continue
		}
		if f.clock != tc.want {
			t.Errorf("parseArgs(%v): clock = %d, want %d", tc.args, f.clock, tc.want)
		}
	}
	if _, _, err := parseArgs([]string{"--clock", "fast"}); err == nil {
		t.Error("a clock that is not a frequency: want an error")
	}
	if _, _, err := parseArgs([]string{"--clock"}); err == nil {
		t.Error("--clock without an argument: want an error")
	}
}

// TestBuildLibrary covers a package that declares no func main. OctoGo has no
// package clause, so that is the whole of what tells a library from a program, and
// building one used to reach flexcc and fail there with `could not find function
// main` -- a C compiler's complaint about a C program the user never wrote.
func TestBuildLibrary(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ogoModFile), "module example.com/proj\n")
	if err := os.MkdirAll(filepath.Join(root, "sensor"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "sensor", "sensor.ogo"), "func Read() int { return 42 }\n")
	if err := os.MkdirAll(filepath.Join(root, "util"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "util", "util.ogo"),
		"import \"example.com/proj/sensor\"\n\nfunc Twice() int { return 2 * sensor.Read() }\n")

	for _, tc := range []struct {
		name string
		args []string
		code int
		want string // substring of the output, or of the error when code != 0
	}{
		{
			name: "a library is checked and makes no binary",
			args: []string{filepath.Join(root, "sensor")},
			want: "[no func main, checked only]",
		},
		{
			// Its imports are resolved as they are for a program, so a library
			// can be checked on its own without building whatever uses it.
			name: "a library that imports another package",
			args: []string{filepath.Join(root, "util")},
			want: "[no func main, checked only]",
		},
		{
			name: "-o has nothing to write",
			args: []string{"-o", filepath.Join(t.TempDir(), "x.binary"), filepath.Join(root, "sensor")},
			code: 2,
			want: "declares no func main",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			code, err := Build(tc.args, nil, &buf, &buf)
			got := buf.String()
			if err != nil {
				got = err.Error()
			}
			if code != tc.code {
				t.Errorf("code=%d, want %d\n%s", code, tc.code, got)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("output %q does not contain %q", got, tc.want)
			}
		})
	}

	// No binary anywhere: a library that produced one would be a program.
	des, err := os.ReadDir(filepath.Join(root, "sensor"))
	if err != nil {
		t.Fatal(err)
	}
	for _, de := range des {
		if strings.HasSuffix(de.Name(), ".binary") {
			t.Errorf("a library wrote %s", de.Name())
		}
	}
}

// TestBuildLibraryEmits pins WHY a library is emitted rather than merely checked:
// the lifetime and escape refusals are made by the emitter, so a library that was
// only type-checked would be held to far less than the same code is when a program
// is built from it.
func TestBuildLibraryEmits(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "lib.ogo"), "func Bad(r int) string { return string(rune(r)) }\n")
	var buf bytes.Buffer
	code, err := Build([]string{dir}, nil, &buf, &buf)
	got := buf.String()
	if err != nil {
		got = err.Error()
	}
	if code == 0 {
		t.Fatalf("code=0, want a refusal\n%s", got)
	}
	if !strings.Contains(got, "does not outlive the function") {
		t.Errorf("output %q is not the lifetime refusal", got)
	}
}
