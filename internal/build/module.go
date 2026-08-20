// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ogoModFile marks the root of a module: the directory every import path in it is
// read against. It is deliberately not go.mod. An OctoGo project may sit inside a
// Go repository -- this compiler's own _examples/ do -- and searching for go.mod
// would silently adopt the SURROUNDING module, making the examples part of
// modernc.org/ogo and requiring their imports to say so. A marker only an OctoGo
// project has cannot be found by accident.
const ogoModFile = "ogo.mod"

// findModule locates the module 'dir' belongs to: the nearest ancestor holding an
// ogo.mod, and the module path that file declares. It returns an empty root when
// there is no such ancestor, which is not an error -- a program that declares no
// module is built against its own directory, as every program was before modules
// existed here.
func findModule(dir string) (root, modulePath string, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", fmt.Errorf("build: %v", err)
	}

	for d := abs; ; {
		fn := filepath.Join(d, ogoModFile)
		switch _, err := os.Stat(fn); {
		case err == nil:
			b, err := os.ReadFile(fn)
			if err != nil {
				return "", "", fmt.Errorf("build: %v", err)
			}

			if modulePath, err = parseOgoMod(b); err != nil {
				return "", "", fmt.Errorf("%s: %v", fn, err)
			}

			return d, modulePath, nil
		}

		parent := filepath.Dir(d)
		if parent == d { // the filesystem root, and no marker anywhere above
			return "", "", nil
		}

		d = parent
	}
}

// parseOgoMod reads an ogo.mod. The file holds one directive, `module <path>`, and
// says nothing else: a directive this compiler does not implement is refused rather
// than ignored, because a silently ignored `require` reads as a promise that
// versions are resolved, which they are not. There are no external dependencies to
// resolve -- every package of a program is a directory inside the module.
func parseOgoMod(b []byte) (modulePath string, err error) {
	for i, line := range strings.Split(string(b), "\n") {
		if j := strings.Index(line, "//"); j >= 0 {
			line = line[:j]
		}
		if line = strings.TrimSpace(line); line == "" {
			continue
		}

		fields := strings.Fields(line)
		if fields[0] != "module" {
			return "", fmt.Errorf("%d: unsupported directive %q, %s holds a module directive and nothing else", i+1, fields[0], ogoModFile)
		}

		if modulePath != "" {
			return "", fmt.Errorf("%d: duplicate module directive", i+1)
		}

		switch len(fields) {
		case 1:
			return "", fmt.Errorf("%d: module directive has no path", i+1)
		case 2:
			// ok
		default:
			return "", fmt.Errorf("%d: module directive takes one path, got %d arguments", i+1, len(fields)-1)
		}

		if err := validModulePath(fields[1]); err != nil {
			return "", fmt.Errorf("%d: %v", i+1, err)
		}

		modulePath = fields[1]
	}
	if modulePath == "" {
		return "", fmt.Errorf("no module directive")
	}

	return modulePath, nil
}

// validModulePath rejects what cannot prefix an import path.
func validModulePath(s string) error {
	switch {
	case strings.HasPrefix(s, "/"), strings.HasSuffix(s, "/"):
		return fmt.Errorf("invalid module path %q: leading or trailing slash", s)
	case strings.Contains(s, " "):
		return fmt.Errorf("invalid module path %q: contains a space", s)
	}
	for _, v := range strings.Split(s, "/") {
		switch v {
		case "", ".", "..":
			return fmt.Errorf("invalid module path %q", s)
		}
	}
	return nil
}

// moduleContext is what an octogo build reads a package against: the filesystem it
// resolves import paths in, the package's own directory within that filesystem, and
// the module path import paths carry. Without an ogo.mod the filesystem is the
// package's own directory and 'rel' is ".", which is what every build did before
// modules and what the tests, the fuzzer and a single-directory program still do.
func moduleContext(dir string) (fsys fs.FS, rel, modulePath string, err error) {
	root, modulePath, err := findModule(dir)
	if err != nil {
		return nil, "", "", err
	}

	if root == "" {
		return os.DirFS(dir), ".", "", nil
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", "", fmt.Errorf("build: %v", err)
	}

	r, err := filepath.Rel(root, abs)
	if err != nil {
		return nil, "", "", fmt.Errorf("build: %v", err)
	}

	return os.DirFS(root), path.Clean(filepath.ToSlash(r)), modulePath, nil
}
