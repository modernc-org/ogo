// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestTODO(t *testing.T) {
	t.Log("TODO")
}

// commandListRE matches a command line of the usage overview: a tab, the name,
// then the one-line summary.
var commandListRE = regexp.MustCompile(`(?m)^\t([a-z0-9]+) {2,}\S`)

// TestHelpCoversEveryCommand keeps the two lists from drifting: every command the
// usage overview advertises must have detail behind "ogo help <command>", and no
// detail may exist for a command the overview does not list.
func TestHelpCoversEveryCommand(t *testing.T) {
	var buf bytes.Buffer
	usage(&buf)
	listed := map[string]bool{}
	for _, m := range commandListRE.FindAllStringSubmatch(buf.String(), -1) {
		listed[m[1]] = true
	}
	if len(listed) == 0 {
		t.Fatalf("no commands parsed from the usage overview:\n%s", buf.String())
	}
	for name := range listed {
		if _, ok := commandHelp[name]; !ok {
			t.Errorf("%q is listed in the usage overview but has no help text", name)
		}
	}
	for name := range commandHelp {
		if !listed[name] {
			t.Errorf("%q has help text but is not listed in the usage overview", name)
		}
	}
}

// TestHelp checks the three shapes of the help command.
func TestHelp(t *testing.T) {
	var buf bytes.Buffer
	if !help(&buf, nil) {
		t.Error("help with no argument: want ok")
	}
	if !strings.Contains(buf.String(), "The commands are:") {
		t.Errorf("help with no argument should print the overview, got:\n%s", buf.String())
	}

	buf.Reset()
	if !help(&buf, []string{"build"}) {
		t.Error(`help "build": want ok`)
	}
	if got := buf.String(); !strings.HasPrefix(got, "usage: ogo build") {
		t.Errorf(`help "build" should start with its usage line, got:\n%s`, got)
	}

	buf.Reset()
	if help(&buf, []string{"nosuchcommand"}) {
		t.Error("help for an unknown command: want not ok")
	}
	if buf.Len() != 0 {
		t.Errorf("help for an unknown command should write nothing, got:\n%s", buf.String())
	}
}

// TestCommandHelpShape keeps each help text self-describing: it must open with its
// own usage line, so "ogo help x" always begins by saying how to invoke x.
func TestCommandHelpShape(t *testing.T) {
	for name, text := range commandHelp {
		if want := "usage: ogo " + name; !strings.HasPrefix(text, want) {
			t.Errorf("help for %q should start with %q, got %q", name, want, firstLine(text))
		}
		if !strings.HasSuffix(text, "\n") {
			t.Errorf("help for %q should end with a newline", name)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestDocsListEveryCommand keeps the prose in step with the tool. The command
// overview exists in three places -- usage(), the README's Usage table and the
// package documentation in specs.go -- because each is read in a different place
// and none can link to the others usefully. Three hand-written copies of one list
// is exactly the shape that goes stale, so adding a command fails here until both
// documents mention it, and removing one fails until they stop.
//
// Only the names are checked. The wording around them is free to differ, and does:
// the README uses a table, specs.go the same tab-indented list the terminal prints.
func TestDocsListEveryCommand(t *testing.T) {
	var buf bytes.Buffer
	usage(&buf)
	var names []string
	for _, m := range commandListRE.FindAllStringSubmatch(buf.String(), -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatalf("no commands parsed from the usage overview:\n%s", buf.String())
	}
	for _, doc := range []string{"README.md", "specs.go"} {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		s := string(b)
		for _, name := range names {
			// Matched with the surrounding punctuation the documents use, so that
			// a stray occurrence of a word like "run" or "test" in prose does not
			// count as documenting the command.
			if !strings.Contains(s, "`"+name+"`") && !strings.Contains(s, "\t"+name+" ") {
				t.Errorf("%s does not document the %q command", doc, name)
			}
		}
	}
}
