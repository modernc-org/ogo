// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package diag

import (
	"errors"
	"go/token"
	"testing"

	"modernc.org/ogo/internal/octogo"
)

func TestTokenEnd(t *testing.T) {
	for _, c := range []struct {
		line string
		at   int
		want string // the token found there
	}{
		{"\tprintln(undefinedName)", 9, "undefinedName"},
		{"\tx := é1 + 2", 6, "é1"},
		{"\tx := 0x1fp-3 + 2", 6, "0x1fp-3"},
		{"\tx := 1e+10", 6, "1e+10"},
		{"\tx := .5", 6, ".5"},
		{`	s := "a\"b" + t`, 6, `"a\"b"`},
		{"\ts := `raw` + t", 6, "`raw`"},
		{"\ts := `raw", 6, "`raw"},
		{"\tr := '\\''", 6, "'\\''"},
		{"\tx <<= 2", 3, "<<="},
		{"\tx := y", 3, ":="},
		{"\tf(a...)", 4, "..."},
		{"\tx = a &^ b", 7, "&^"},
		{"\tx = y // note", 7, "// note"},
		{"\tx = /* c */ y", 5, "/* c */"},
		{"\tx = y", 0, "\tx = y"}, // white space: the line's end
		{"\tx = y", 6, ""},        // past the line: no token
		{"}", 0, "}"},
	} {
		end := TokenEnd(c.line, c.at)
		got := ""
		if c.at < len(c.line) {
			got = c.line[c.at:end]
		}
		if got != c.want {
			t.Errorf("TokenEnd(%q, %d): got %q, want %q", c.line, c.at, got, c.want)
		}
	}
}

func TestFromError(t *testing.T) {
	list := octogo.ErrList{
		{Pos: token.Position{Filename: "lib/lib.ogo", Line: 2, Column: 9}, Err: errors.New("undefined: x")},
		{Pos: token.Position{Filename: "lib/lib.ogo", Line: 2, Column: 9}, Err: errors.New("undefined: x")},
		{Err: errors.New("no position")},
	}
	got := FromError(list.Err(), "check")
	want := []Diagnostic{
		{File: "lib/lib.ogo", Line: 2, Col: 9, Message: "undefined: x", Phase: "check"},
		{Message: "no position", Phase: "check"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}

	got = FromError(errors.New("emit: main.ogo:3:9: cannot return a slice\nbacked by b\nmain.ogo:4:1: second"), "check")
	if len(got) != 2 || got[0].File != "main.ogo" || got[0].Line != 3 || got[0].Col != 9 ||
		got[0].Message != "cannot return a slice\nbacked by b" || got[1].Line != 4 || got[1].Message != "second" {
		t.Errorf("text: got %+v", got)
	}
	if got := FromError(errors.New("emit: the marks did not settle"), "check"); len(got) != 1 || got[0].Line != 0 || got[0].Message != "emit: the marks did not settle" {
		t.Errorf("unpositioned: got %+v", got)
	}
}

func TestSetEnd(t *testing.T) {
	src := []byte("func main() {\n\tprintln(undefinedName)\n}")
	d := Diagnostic{Line: 2, Col: 10}
	d.SetEnd(src)
	if d.EndLine != 2 || d.EndCol != 23 {
		t.Errorf("got end %d:%d, want 2:23", d.EndLine, d.EndCol)
	}
	d = Diagnostic{Line: 3, Col: 1}
	d.SetEnd(src)
	if d.EndCol != 2 {
		t.Errorf("last line without a newline: got end col %d, want 2", d.EndCol)
	}
	d = Diagnostic{Line: 9, Col: 1}
	d.SetEnd(src)
	if d.EndLine != 9 || d.EndCol != 1 {
		t.Errorf("past the file: got %d:%d, want 9:1", d.EndLine, d.EndCol)
	}
}

func TestUTF16(t *testing.T) {
	line := "\tprintln(\"é𝄞\", x)"
	i := len("\tprintln(\"é𝄞\", ")
	if got, want := UTF16(line, i), len("\tprintln(\"")+1+2+len("\", "); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}
