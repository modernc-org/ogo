// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"go/doc/comment"
	"strings"
)

// docCommentSep is the separator sep ahead of a token with its doc comment written
// as gofmt writes one: go/printer's intersperseComments takes an unindented comment
// group abutting the token that follows it on the next line for a doc comment --
// a top-level declaration's -- and formatDocComment rewrites it, its text through
// go/doc/comment and its directives, `//go:noescape` among them, moved to the end
// behind a `//` line. The token's own conditions, that it is no identifier and no
// `import` -- a cgo preamble is left as written -- are the caller's to ask. atStart says sep begins the file,
// where a comment on its first line begins a line; elsewhere sep begins right after
// a token, and the comments on that token's line are its own (go/parser's line
// comment group). A comment written so already is left as it is.
func docCommentSep(sep []byte, atStart bool) []byte {
	s := string(sep)
	if !strings.HasSuffix(s, "\n") {
		return sep // the token does not begin a line
	}
	type cmt struct {
		start, end     int // in s
		line, endLine  int // newlines before start and before end
		col1, trailing bool
	}
	var cs []cmt
	line := 0
	for i := 0; i < len(s); {
		switch {
		case s[i] == '\n':
			line++
			i++
		case strings.HasPrefix(s[i:], "//"), strings.HasPrefix(s[i:], "/*"):
			c := cmt{start: i, line: line, col1: i == 0 && atStart || i > 0 && s[i-1] == '\n'}
			if s[i+1] == '/' {
				end := strings.IndexByte(s[i:], '\n')
				if end < 0 {
					end = len(s) - i
				}
				i += end
			} else {
				end := strings.Index(s[i+2:], "*/")
				if end < 0 {
					return sep
				}
				line += strings.Count(s[i:i+2+end+2], "\n")
				i += 2 + end + 2
			}
			c.end, c.endLine = i, line
			cs = append(cs, c)
		default:
			i++
		}
	}
	// The comments of the line the separator begins on, and those adjacent to
	// them on that line, belong to the token before it.
	if !atStart {
		for k := range cs {
			if cs[k].line != 0 && (k == 0 || !cs[k-1].trailing || cs[k].line > cs[k-1].endLine) {
				break
			}
			cs[k].trailing = true
		}
	}
	if len(cs) == 0 || cs[len(cs)-1].trailing || cs[len(cs)-1].end != len(s)-1 {
		return sep // no comment abuts the token
	}
	// The group: adjacent comments, none after a blank line.
	first := len(cs) - 1
	for first > 0 && !cs[first-1].trailing && cs[first].line <= cs[first-1].endLine+1 {
		first--
	}
	if !cs[first].col1 {
		return sep
	}
	var list []string
	for _, c := range cs[first:] {
		list = append(list, s[c.start:c.end])
	}
	out := formatDocComment(list)
	if strings.Join(out, "\n") == strings.Join(list, "\n") {
		return sep
	}
	if len(out) == 0 {
		return []byte(s[:cs[first].start]) // the doc comment removed, what precedes kept
	}
	return []byte(s[:cs[first].start] + strings.Join(out, "\n") + "\n")
}

// formatDocComment is go/printer's of the same name, over the comments' texts: the
// canonical form of a doc comment.
func formatDocComment(list []string) []string {
	var kind, text string
	var directives []string
	switch {
	case len(list) == 1 && strings.HasPrefix(list[0], "/*"):
		kind = "/*"
		text = list[0]
		if !strings.Contains(text, "\n") || allStars(text) {
			return list
		}
		text = text[2 : len(text)-2]
	case strings.HasPrefix(list[0], "//"):
		kind = "//"
		var b strings.Builder
		for _, c := range list {
			after, found := strings.CutPrefix(c, "//")
			if !found {
				return list
			}
			if isDirective(after) {
				directives = append(directives, c)
				continue
			}
			b.WriteString(strings.TrimPrefix(after, " "))
			b.WriteString("\n")
		}
		text = b.String()
	default:
		return list
	}
	if text == "" {
		return list
	}
	var p comment.Parser
	var pr comment.Printer
	text = string(pr.Comment(p.Parse(text)))
	if kind == "/*" {
		return []string{"/*\n" + text + "*/"}
	}
	var out []string
	for text != "" {
		var line string
		line, text, _ = strings.Cut(text, "\n")
		switch {
		case line == "":
			line = "//"
		case strings.HasPrefix(line, "\t"):
			line = "//" + line
		default:
			line = "// " + line
		}
		out = append(out, line)
	}
	if len(directives) > 0 {
		out = append(out, "//")
		out = append(out, directives...)
	}
	return out
}

// isDirective is go/printer's: whether c, a line comment's text after its "//", is
// a directive -- //line, //extern, //export, or //[a-z0-9]+:[a-z0-9]...
func isDirective(c string) bool {
	if strings.HasPrefix(c, "line ") || strings.HasPrefix(c, "extern ") || strings.HasPrefix(c, "export ") {
		return true
	}
	colon := strings.Index(c, ":")
	if colon <= 0 || colon+1 >= len(c) {
		return false
	}
	for i := 0; i <= colon+1; i++ {
		if i == colon {
			continue
		}
		b := c[i]
		if !('a' <= b && b <= 'z' || '0' <= b && b <= '9') {
			return false
		}
	}
	return true
}

// allStars is go/printer's: whether text, a /* comment, has a star beginning each
// line after the first, the old style of block comment, which is left alone.
func allStars(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			j := i + 1
			for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
				j++
			}
			if j < len(text) && text[j] != '*' {
				return false
			}
		}
	}
	return true
}
