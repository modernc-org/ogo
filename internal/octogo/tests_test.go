// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// errorCommentRx matches: // ERROR "some regexp"
var errorCommentRx = regexp.MustCompile(`//\s*ERROR\s+"([^"]+)"`)

// expectedError represents an error we expect the compiler to throw on a specific line.
type expectedError struct {
	line int
	rx   *regexp.Regexp
}

// compilerError represents the shape of an error returned by your parser/semantic checker.
type compilerError struct {
	Line    int
	Message string
}

func TestOctoGoSpecs(t *testing.T) {
	testDir := "testdata"
	fsys := os.DirFS(testDir)

	m, err := fs.Glob(fsys, "*.ogo")
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range m {
		switch {
		case re != nil:
			if !re.MatchString(path) {
				continue
			}
		default:
			switch {
			case
				strings.Contains(path, "02_"), //TODO name resolving
				strings.Contains(path, "03_"), //TODO name resolving
				strings.Contains(path, "06_"), //TODO name resolving
				strings.Contains(path, "09_"): //TODO name resolving

				continue
			}
		}

		t.Log(path)
		t.Run(filepath.Base(path), func(t *testing.T) {
			runSingleTest(t, fsys, path)
		})
	}

	if err != nil {
		t.Fatalf("Failed walking test directory: %v", err)
	}
}

func runSingleTest(t *testing.T, fsys fs.FS, path string) {
	expectedCompile, expectedErrs, err := parseAnnotations(fsys, path)
	if err != nil {
		t.Fatalf("Failed to parse annotations in %s: %v", path, err)
	}

	actualErrs := runCompiler(t, filepath.Base(path), fsys)
	t.Logf("len(actualErrs)=%v", len(actualErrs))
	for _, v := range actualErrs {
		t.Log(v)
	}

	if expectedCompile {
		if len(actualErrs) > 0 {
			t.Errorf("Expected file to COMPILE, but got %d errors:", len(actualErrs))
			for _, e := range actualErrs {
				t.Errorf("  Line %d: %s", e.Line, e.Message)
			}
		}
		return
	}

	// Match actual errors against expected errors
	checkErrors(t, expectedErrs, actualErrs, path)
}

// parseAnnotations reads the test file and extracts // COMPILE and // ERROR directives.
func parseAnnotations(fsys fs.FS, path string) (bool, []expectedError, error) {
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return false, nil, err
	}

	var errs []expectedError
	expectCompile := false

	scanner := bufio.NewScanner(bytes.NewReader(b))
	lineNum := 1
	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "// COMPILE") {
			expectCompile = true
		}

		if match := errorCommentRx.FindStringSubmatch(line); match != nil {
			re := strings.ReplaceAll(match[1], "@", `"`)
			rx, err := regexp.Compile(re)
			if err != nil {
				return false, nil, fmt.Errorf("invalid regexp on line %d: %v", lineNum, err)
			}
			// We usually expect the error to be triggered on the line immediately following the comment,
			// or on the same line if placed at the end. For this implementation, we associate it with
			// the line following the comment.
			errs = append(errs, expectedError{line: lineNum + 1, rx: rx})
		}
		lineNum++
	}

	return expectCompile, errs, scanner.Err()
}

// checkErrors verifies that every expected error occurred, and no unexpected errors occurred.
func checkErrors(t *testing.T, expected []expectedError, actual []compilerError, path string) {
	matchedActual := make(map[int]bool)

	linesOK := map[int]bool{}
	// 1. Verify all expected errors were found
	for _, exp := range expected {
		found := false
		for i, act := range actual {
			if act.Line == exp.line && exp.rx.MatchString(act.Message) {
				found = true
				matchedActual[i] = true
				linesOK[act.Line] = true
				break
			}
		}
		if !found {
			t.Errorf("%v:%d: Missing expected error on line %[2]d matching: %s", path, exp.line, exp.rx.String())
		}
	}

	// 2. Report any actual errors that were NOT expected
	for i, act := range actual {
		if !matchedActual[i] && !linesOK[act.Line] {
			t.Errorf("Unexpected compiler error on line %d: %s", act.Line, act.Message)
		}
	}
}

func runCompiler(t *testing.T, path string, fsys fs.FS) (r []compilerError) {
	pkg, err := Build(-1, []string{path}, fsys)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}

	for _, v := range pkg.Files {
		switch x := v.errList.Err().(type) {
		case nil:
			// ok
		case ErrList:
			for _, v := range x {
				r = append(r, compilerError{v.Pos.Line, v.Err.Error()})
			}
		default:
			t.Errorf("%s: %v", path, x)
		}
	}
	return r
}
