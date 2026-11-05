// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package diag turns what the compiler reports into diagnostics a tool can place:
// a file, a start and an end, a message. It is shared by `ogo lsp` and `ogo build
// --json`, so an editor and a build panel point at the same bytes.
//
// The compiler reports a position and no end. The end is the end of the token at
// the position, found by the language's lexical rules over the line's text, or the
// line's end where no token begins there.
package diag

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"modernc.org/ogo/internal/octogo"
)

// Diagnostic is one error with its place. Lines and columns count from 1, and a
// column counts bytes, as the compiler's and Go's tools count them.
type Diagnostic struct {
	File    string // as the compiler names it: relative to the build's root; "" for none
	Line    int    // 0 where the error has no position
	Col     int
	EndLine int // set by SetEnd
	EndCol  int
	Message string
	Phase   string // "check" for the checker's and the emitter's, "backend" for the C backend's
}

// positioned matches a line that begins with its position, file:line:column:.
var positioned = regexp.MustCompile(`^(.+?):(\d+):(\d+): (.*)$`)

// FromError takes apart an error of the compiler's into its diagnostics: an
// octogo.ErrList item by item, any other error by its text, a line beginning
// file:line:column: starting each diagnostic and a line that does not continuing
// the one before. The emitter's "emit: " before a position is the part of the
// compiler that found it and is dropped. Duplicates, one place and one message, are
// reported once, as the command line reports them.
func FromError(err error, phase string) (r []Diagnostic) {
	if err == nil {
		return nil
	}
	seen := map[Diagnostic]bool{}
	add := func(d Diagnostic) {
		if !seen[d] {
			seen[d] = true
			r = append(r, d)
		}
	}
	var list octogo.ErrList
	if errors.As(err, &list) {
		for _, e := range list {
			d := Diagnostic{Message: e.Err.Error(), Phase: phase}
			if e.Pos.IsValid() {
				d.File, d.Line, d.Col = e.Pos.Filename, e.Pos.Line, e.Pos.Column
			}
			add(d)
		}
		return r
	}
	var cur *Diagnostic
	flush := func() {
		if cur != nil {
			add(*cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(strings.TrimRight(err.Error(), "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if m := positioned.FindStringSubmatch(strings.TrimPrefix(line, "emit: ")); m != nil {
			flush()
			ln, _ := strconv.Atoi(m[2])
			col, _ := strconv.Atoi(m[3])
			cur = &Diagnostic{File: m[1], Line: ln, Col: col, Message: m[4], Phase: phase}
			continue
		}
		if cur != nil {
			cur.Message += "\n" + line
			continue
		}
		cur = &Diagnostic{Message: line, Phase: phase}
	}
	flush()
	return r
}

// SetEnd sets the end of d from the source of its file: the end of the token at
// its position, on the same line, or the line's end where no token begins there or
// the position is past it. A diagnostic with no position ends where it starts.
func (d *Diagnostic) SetEnd(src []byte) {
	d.EndLine, d.EndCol = d.Line, d.Col
	if d.Line <= 0 || d.Col <= 0 {
		return
	}
	line, ok := Line(src, d.Line)
	if !ok {
		return
	}
	d.EndCol = TokenEnd(line, d.Col-1) + 1
	if d.EndCol < d.Col {
		d.EndCol = d.Col
	}
}

// Line is the text of the n'th line of src, counted from 1, without its line end.
func Line(src []byte, n int) (string, bool) {
	s := string(src)
	for i := 1; i < n; i++ {
		j := strings.IndexByte(s, '\n')
		if j < 0 {
			return "", false
		}
		s = s[j+1:]
	}
	if j := strings.IndexByte(s, '\n'); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSuffix(s, "\r"), true
}

// operators are the language's operators and punctuation of more than one byte,
// longest first, so the first that matches is the token.
var operators = []string{
	"<<=", ">>=", "&^=", "...",
	"&&", "||", "<-", "++", "--", "==", "!=", "<=", ">=", ":=",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<", ">>", "&^",
}

// TokenEnd is the byte offset in line just past the token beginning at offset i,
// or len(line) where none begins there: at white space, or past the line's end. A
// string, a raw string or a general comment that does not end on the line ends with
// it.
func TokenEnd(line string, i int) int {
	if i < 0 || i >= len(line) {
		return len(line)
	}
	c := line[i]
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		return len(line)
	case c == '"' || c == '\'':
		for j := i + 1; j < len(line); j++ {
			switch line[j] {
			case '\\':
				j++
			case c:
				return j + 1
			}
		}
		return len(line)
	case c == '`':
		if j := strings.IndexByte(line[i+1:], '`'); j >= 0 {
			return i + 1 + j + 1
		}
		return len(line)
	case strings.HasPrefix(line[i:], "//"):
		return len(line)
	case strings.HasPrefix(line[i:], "/*"):
		if j := strings.Index(line[i+2:], "*/"); j >= 0 {
			return i + 2 + j + 2
		}
		return len(line)
	case c >= '0' && c <= '9' || c == '.' && i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9':
		j := i + 1
		for j < len(line) {
			d := line[j]
			switch {
			case d >= '0' && d <= '9', d >= 'a' && d <= 'z', d >= 'A' && d <= 'Z', d == '_', d == '.':
				j++
			case (d == '+' || d == '-') && strings.ContainsRune("eEpP", rune(line[j-1])):
				j++
			default:
				return j
			}
		}
		return j
	}
	if r, n := utf8.DecodeRuneInString(line[i:]); isLetter(r) {
		j := i + n
		for j < len(line) {
			r, n := utf8.DecodeRuneInString(line[j:])
			if !isLetter(r) && !unicode.IsDigit(r) {
				break
			}
			j += n
		}
		return j
	}
	for _, op := range operators {
		if strings.HasPrefix(line[i:], op) {
			return i + len(op)
		}
	}
	_, n := utf8.DecodeRuneInString(line[i:])
	return i + n
}

func isLetter(r rune) bool { return r == '_' || unicode.IsLetter(r) }

// UTF16 is the number of UTF-16 code units in line up to the byte offset i, the
// unit a Language Server Protocol position counts by default.
func UTF16(line string, i int) int {
	if i > len(line) {
		i = len(line)
	}
	n := 0
	for _, r := range line[:i] {
		if r >= 0x10000 {
			n += 2
			continue
		}
		n++
	}
	return n
}
