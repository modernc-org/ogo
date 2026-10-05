// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestScanUnterminated: a literal left open is a diagnostic in Go's words. The
// scanner, finding no lexeme, reported "invalid token" and returned a token at an
// index no token had, and a file with one between its declarations -- a stray `"`
// on a line of its own, a `'` before `var` -- crashed the compiler, "index out of
// range", where the next reader asked that token what it was. Found by mutating the
// run cases' sources one character at a time.
func TestScanUnterminated(t *testing.T) {
	for _, test := range []struct{ src, want string }{
		{"func f() {}\n\"\nfunc main() {}\n", "string literal not terminated"},
		{"'var x = 1\n\nfunc main() { _ = x }\n", "rune literal not terminated"},
		{"func main() {\n\ts := \"abc\n\t_ = s\n}\n", "string literal not terminated"},
		{"func main() {\n\tr := 'a\n\t_ = r\n}\n", "rune literal not terminated"},
		{"var s = `raw\n", "raw string literal not terminated"},
		{"func main() {}\n\x01\n", "invalid token"},
		// A file whose parse failed was walked by the later phases, its tree not
		// what was written: a unary operator left without its operand crashed the
		// compiler there, a nil operand dereferenced.
		{"func main() {\n\tprintln(1 + <-)\n}\n", "expected"},
		{"var b [4]int\n\nfunc main() {\n\ts := b[:!]\n\t_ = s\n}\n", "expected"},
		{"func main() {\n\tprintln(int64(-, 3))\n}\n", "expected"},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%q: the compiler panicked: %v", test.src, r)
				}
			}()
			_, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(test.src)}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("%q: got %v, want an error containing %q", test.src, err, test.want)
			}
		}()
	}
}
