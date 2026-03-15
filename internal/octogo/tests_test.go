// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"bufio"
	"fmt"
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
	// Adjust this path to wherever you store the .ogo test files.
	testDir := "testdata"

	err := filepath.WalkDir(testDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Only test .ogo files
		if d.IsDir() || filepath.Ext(path) != ".ogo" {
			return nil
		}
		switch {
		case re != nil:
			if re.MatchString(path) {
				return nil
			}
		default:
			switch {
			case
				strings.Contains(path, "02_"), //TODO name resolving
				strings.Contains(path, "03_"), //TODO name resolving
				strings.Contains(path, "06_"), //TODO name resolving
				strings.Contains(path, "09_"): //TODO name resolving

				return nil
			}
		}

		t.Log(path)
		t.Run(filepath.Base(path), func(t *testing.T) {
			runSingleTest(t, path)
		})
		return nil
	})

	if err != nil {
		t.Fatalf("Failed walking test directory: %v", err)
	}
}

func runSingleTest(t *testing.T, path string) {
	expectedCompile, expectedErrs, err := parseAnnotations(path)
	if err != nil {
		t.Fatalf("Failed to parse annotations in %s: %v", path, err)
	}

	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", path, err)
	}

	actualErrs := runCompiler(path, src)
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
func parseAnnotations(path string) (bool, []expectedError, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, nil, err
	}
	defer f.Close()

	var errs []expectedError
	expectCompile := false

	scanner := bufio.NewScanner(f)
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

func runCompiler(path string, src []byte) (r []compilerError) {
	pkg := NewBuildContext(-1).NewPackage([]string{path}, map[string][]byte{path: []byte(src)})
	for _, v := range pkg.Files {
		switch x := v.Err.(type) {
		case nil:
			// ok
		case ErrList:
			for _, v := range x {
				r = append(r, compilerError{v.Pos.Line, v.Err.Error()})
			}
		default:
			panic(todo("%v: %T", path, x))
		}
	}
	return r
}
