// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOgoMod(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string // the module path, or the error substring when it starts with "!"
	}{
		{"plain", "module example.com/proj\n", "example.com/proj"},
		{"no trailing newline", "module example.com/proj", "example.com/proj"},
		{"blank lines and comments", "// what this is\n\nmodule example.com/proj // and where\n\n", "example.com/proj"},
		{"tabs as separators", "\tmodule\texample.com/proj\t\n", "example.com/proj"},
		{"empty file", "", "!no module directive"},
		{"only a comment", "// nothing here\n", "!no module directive"},
		{"missing path", "module\n", "!has no path"},
		{"an extra argument", "module a/b c\n", "!takes one path"},
		{"blank path", "module \n", "!has no path"},
		{"duplicate", "module a/b\nmodule c/d\n", "!duplicate module directive"},
		{"a directive we do not implement", "module a/b\nrequire c/d v1.0.0\n", "!unsupported directive \"require\""},
		{"leading slash", "module /abs/path\n", "!invalid module path"},
		{"trailing slash", "module a/b/\n", "!invalid module path"},
		{"dot element", "module a/./b\n", "!invalid module path"},
		{"dotdot element", "module a/../b\n", "!invalid module path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOgoMod([]byte(tc.src))
			if want, isErr := strings.CutPrefix(tc.want, "!"); isErr {
				switch {
				case err == nil:
					t.Errorf("got %q and no error, want an error matching %q", got, want)
				case !strings.Contains(err.Error(), want):
					t.Errorf("error %q does not contain %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestModuleContext pins where a build reads its packages from. The rootless answer
// matters as much as the module one: a program that declares no module is built
// against its own directory, which is every program written before ogo.mod existed.
func TestModuleContext(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "cmd", "app")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ogoModFile), []byte("module example.com/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A directory below the marker is part of the module, however deep.
	for _, tc := range []struct{ dir, rel string }{
		{root, "."},
		{filepath.Join(root, "cmd"), "cmd"},
		{deep, "cmd/app"},
	} {
		_, rel, modulePath, err := moduleContext(tc.dir)
		if err != nil {
			t.Fatalf("%s: %v", tc.dir, err)
		}
		if modulePath != "example.com/proj" {
			t.Errorf("%s: module path %q, want example.com/proj", tc.dir, modulePath)
		}
		if rel != tc.rel {
			t.Errorf("%s: rel %q, want %q", tc.dir, rel, tc.rel)
		}
	}

	// No marker anywhere above: the package's own directory is the root.
	bare := t.TempDir()
	_, rel, modulePath, err := moduleContext(bare)
	if err != nil {
		t.Fatal(err)
	}
	if modulePath != "" || rel != "." {
		t.Errorf("without ogo.mod got module %q rel %q, want \"\" and \".\"", modulePath, rel)
	}
}

// TestModuleLayoutBuild builds, with the real backend, the layout a module exists
// for: two programs under cmd/ over ONE copy of a library beside them. Without an
// ogo.mod neither program can name that library at all -- an import path is read
// against the directory being built, and the library is not below it.
func TestModuleLayoutBuild(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"sensor", "cmd/firmware", "cmd/calib"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, ogoModFile), "module example.com/proj\n")
	write(t, filepath.Join(root, "sensor", "sensor.ogo"), "func Read() int { return 42 }\n")
	for _, p := range []string{"firmware", "calib"} {
		write(t, filepath.Join(root, "cmd", p, "main.ogo"),
			"import \"example.com/proj/sensor\"\n\nfunc main() { println(\""+p+"\", sensor.Read()) }\n")
	}

	for _, p := range []string{"firmware", "calib"} {
		t.Run(p, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), p+".binary")
			var buf bytes.Buffer
			code, err := Build([]string{"-o", out, filepath.Join(root, "cmd", p)}, nil, &buf, &buf)
			if err != nil || code != 0 {
				t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
			}
			// The backend warns where it should refuse, so any output from a
			// SUCCESSFUL build is a failure, as everywhere else here.
			if s := strings.TrimSpace(buf.String()); s != "" {
				t.Fatalf("backend was not silent:\n%s", s)
			}
		})
	}

	// The working directory must not decide what an import means: the same package
	// built from inside itself is the same program.
	out := filepath.Join(t.TempDir(), "wd.binary")
	var buf bytes.Buffer
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, "cmd", "firmware")); err != nil {
		t.Fatal(err)
	}
	code, err := Build([]string{"-o", out, "."}, nil, &buf, &buf)
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
	}
}
