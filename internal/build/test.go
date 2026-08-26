// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"
	"time"

	"modernc.org/ogo/internal/loadp2"
	"modernc.org/ogo/internal/octogo"
)

// testRunnerFile is the name the generated runner is compiled under. It is not
// written to the user's directory -- the build reads it from an overlay -- but it
// is a real file name so a diagnostic in the generated code can point somewhere.
const testRunnerFile = "ogo_test_main.ogo"

// testDoneLine is what the runner prints last. The board returns no exit status,
// so the verdict travels back over the serial line as text and the host matches
// this to know the run finished rather than hung.
const testDoneLine = "ogo-test-done"

// Test implements `ogo test [package]`: it compiles the package together with its
// _test.ogo files and a generated runner, then loads the result on a connected P2
// and reports what the tests printed.
//
// The board is the only place a result means anything. A host shim would be faster
// and would sometimes be WRONG: gcc and flexcc disagree about semantics and not
// only about warnings, and this compiler has already shipped a feature that passed
// on the host and failed on the board (doc/call-through-array-element.c). A test
// that reports "ok" from somewhere the program will never run is worse than a test
// that did not run at all, so there is no host mode.
//
// -c compiles the tests and does not run them, which is what CI without a board
// can honestly do.
func Test(args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	var compileOnly bool
	var port string
	var rest []string
	// A test binary takes the same clock as the program it tests. Running the tests
	// at a different speed than the thing ships at is how a timing bug hides.
	clock, xtal := 0, octogo.DefaultXtal
	for i := 0; i < len(args); i++ {
		var err error
		switch a := args[i]; {
		case a == "-c":
			compileOnly = true
		case a == "-p":
			i++
			if i >= len(args) {
				return 2, fmt.Errorf("test: -p requires an argument")
			}
			port = args[i]
		case a == "--clock" || a == "-clock":
			if clock, err = parseHz("test", a, args, &i); err != nil {
				return 2, err
			}
		case a == "--xtal" || a == "-xtal":
			if xtal, err = parseHz("test", a, args, &i); err != nil {
				return 2, err
			}
		case strings.HasPrefix(a, "-"):
			return 2, fmt.Errorf("test: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	clockOpts, err := clockOption(clock, xtal)
	if err != nil {
		return 2, err
	}

	opts := testOptions{compileOnly: compileOnly, port: port, clockOpts: clockOpts}
	dir := "."
	switch {
	case len(rest) == 0:
	case len(rest) == 1 && strings.HasSuffix(rest[0], "..."):
		// `ogo test ./...` -- every package under a root, which is how a program of
		// several packages is tested in one command. Each is built and run in turn:
		// the board runs one binary at a time, so there is nothing to overlap.
		dirs, err := packageDirs(rest[0])
		if err != nil {
			return 2, err
		}
		worst := 0
		for _, d := range dirs {
			code, err := testPackage(d, opts, stdout, stderr)
			if err != nil {
				// Reported and passed over, as `go test ./...` does with a package
				// that does not build: the other packages are still worth running,
				// and the run fails as a whole at the end.
				fmt.Fprintln(stderr, err)
				fmt.Fprintf(stdout, "FAIL\t%s\t[build failed]\n", d)
				if code == 0 {
					code = 1
				}
			}
			if code > worst {
				worst = code
			}
		}
		return worst, nil
	case len(rest) == 1 && isDir(rest[0]):
		dir = rest[0]
	default:
		return 2, fmt.Errorf("test: expected at most one package directory")
	}
	return testPackage(dir, opts, stdout, stderr)
}

// testOptions is what every package in a run is tested with -- the same flags,
// whether one package was named or a whole tree matched.
type testOptions struct {
	compileOnly bool
	port        string
	clockOpts   []octogo.EmitOption
}

// packageDirs expands a `.../...` pattern to the package directories under its
// root, in path order. A package is a directory holding at least one .ogo file;
// testdata and the dot- and underscore-prefixed directories are not packages, as
// they are not to the Go tool either.
func packageDirs(pattern string) ([]string, error) {
	root := strings.TrimSuffix(pattern, "...")
	root = strings.TrimSuffix(root, string(filepath.Separator))
	root = strings.TrimSuffix(root, "/")
	if root == "" {
		root = "."
	}
	if !isDir(root) {
		return nil, fmt.Errorf("test: %s: no such directory", root)
	}
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if base := filepath.Base(p); p != root && (base == "testdata" || strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")) {
			return filepath.SkipDir
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".ogo") {
				dirs = append(dirs, p)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("test: %s: %v", pattern, err)
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("test: %s matched no packages", pattern)
	}
	slices.Sort(dirs)
	return dirs, nil
}

// testPackage builds and runs one package's tests. It is the whole of what a named
// package does, and one step of what a pattern does.
func testPackage(dir string, opts testOptions, stdout, stderr io.Writer) (int, error) {
	compileOnly, port, clockOpts := opts.compileOnly, opts.port, opts.clockOpts
	files, testFiles, err := testPackageFiles(dir)
	if err != nil {
		return 2, err
	}
	if len(testFiles) == 0 {
		fmt.Fprintf(stdout, "ok  \t%s\t[no test files]\n", dir)
		return 0, nil
	}

	// The tests are discovered by the parser rather than by reading the text: a
	// package that does not compile has no tests to find, and says so here.
	all := append(append([]string{}, files...), testFiles...)
	fsys, rel, modulePath, err := moduleContext(dir)
	if err != nil {
		return 2, err
	}

	pkg, err := octogo.BuildModule(-1, modulePath, rel, all, fsys)
	if err != nil {
		return 1, err
	}
	names, err := octogo.TestFuncs(pkg)
	if err != nil {
		return 1, err
	}
	if len(names) == 0 {
		fmt.Fprintf(stdout, "ok  \t%s\t[no tests to run]\n", dir)
		return 0, nil
	}

	// The runner is an ordinary .ogo file compiled with the rest, layered over the
	// directory rather than written into it.
	// The runner joins the package, so it is layered where the package lives: at the
	// module root that is the top of fsys, in a module it is the package's directory.
	runner := path.Join(rel, testRunnerFile)
	overlay := overlayFS{
		FS:    fsys,
		extra: fstest.MapFS{runner: &fstest.MapFile{Data: []byte(testRunnerSrc(names))}},
	}
	all = append(all, testRunnerFile)
	pkg, err = octogo.BuildModule(-1, modulePath, rel, all, overlay)
	if err != nil {
		return 1, err
	}

	var cbuf bytes.Buffer
	emitOpts := append([]octogo.EmitOption{octogo.Checked(), octogo.TestEntry("ogoTestMain")}, clockOpts...)
	if err := octogo.EmitC(pkg, &cbuf, emitOpts...); err != nil {
		return 1, err
	}

	tmp, err := os.MkdirTemp("", "ogo-test-*")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(tmp)
	cFile := filepath.Join(tmp, "ogo_test.c")
	if err := os.WriteFile(cFile, cbuf.Bytes(), 0o644); err != nil {
		return 1, err
	}
	binary := filepath.Join(tmp, dirPkgName(dir)+".test.binary")
	if code, err := compileC(cFile, binary, stdout, stderr); err != nil {
		return code, err
	}
	if compileOnly {
		out := filepath.Join(dir, dirPkgName(dir)+".test.binary")
		if err := copyFile(binary, out); err != nil {
			return 1, err
		}
		fmt.Fprintf(stdout, "ok  \t%s\t[built %d test%s, not run]\n", dir, len(names), plural(len(names)))
		return 0, nil
	}

	out, ok := loadp2.Capture(loadp2.Options{Binary: binary, Port: port}, testDoneLine, 2*time.Minute)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r ")
		switch {
		case line == "", line == testDoneLine:
		case strings.HasPrefix(line, "( Entering terminal mode"):
			// The loader announcing itself, not the program speaking.
		default:
			fmt.Fprintln(stdout, line)
		}
	}
	switch {
	case !ok:
		fmt.Fprintf(stdout, "FAIL\t%s\t[the board produced no result]\n", dir)
		return 1, nil
	case strings.Contains(out, "\n--- FAIL:") || strings.HasPrefix(out, "--- FAIL:"):
		fmt.Fprintf(stdout, "FAIL\t%s\n", dir)
		return 1, nil
	}
	fmt.Fprintf(stdout, "ok  \t%s\n", dir)
	return 0, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// testRunnerSrc generates the runner: one testing.T per test, so a failure in one
// does not mark the next, and Go's own --- PASS / --- FAIL lines so the output
// reads the way a Go programmer expects.
func testRunnerSrc(names []string) string {
	var b strings.Builder
	b.WriteString("// Code generated by ogo test. DO NOT EDIT.\n\nimport \"testing\"\n\nfunc ogoTestMain() {\n\tfailed := 0\n")
	for i, nm := range names {
		fmt.Fprintf(&b, "\n\tvar t%d testing.T\n", i)
		fmt.Fprintf(&b, "\t%s(&t%d)\n", nm, i)
		fmt.Fprintf(&b, "\tif t%d.Skipped() {\n\t\tprintln(\"--- SKIP: %s\")\n\t} else if t%d.Failed() {\n\t\tprintln(\"--- FAIL: %s\")\n\t\tfailed++\n\t} else {\n\t\tprintln(\"--- PASS: %s\")\n\t}\n", i, nm, i, nm, nm)
	}
	fmt.Fprintf(&b, "\n\tif failed != 0 {\n\t\tprintln(\"FAIL\", failed, \"of\", %d)\n\t}\n\tprintln(%q)\n}\n", len(names), testDoneLine)
	return b.String()
}

// testPackageFiles splits a directory's sources into the package's own and its
// test files.
func testPackageFiles(dir string) (files, testFiles []string, err error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("test: %v", err)
	}
	for _, de := range des {
		switch nm := de.Name(); {
		case de.IsDir(), !strings.HasSuffix(nm, ".ogo"):
			// not a source file
		case strings.HasSuffix(nm, "_test.ogo"):
			testFiles = append(testFiles, nm)
		default:
			files = append(files, nm)
		}
	}
	if len(files)+len(testFiles) == 0 {
		return nil, nil, fmt.Errorf("test: no .ogo source files in %s", dir)
	}
	return files, testFiles, nil
}

// overlayFS reads from extra first and from FS otherwise, which is how the
// generated runner joins the package without being written to the user's
// directory.
type overlayFS struct {
	fs.FS
	extra fs.FS
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if f, err := o.extra.Open(name); err == nil {
		return f, nil
	}
	return o.FS.Open(name)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
