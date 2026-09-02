// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gofmtDisagreements is how many of the run corpus `ogo fmt` still formats
// differently from gofmt. It is a RATCHET: the number may go down and never up.
//
// It is not zero and is not meant to be yet. What is left is two structural gaps,
// each its own piece of work: gofmt's expression-depth rule in the positions other
// than a call's arguments (a binary operand raises depth too, so "n*10 + int(c-d)"
// tightens inside the conversion), and its alignment of consecutive one-line
// function declarations into a column, which ogo fmt does not do at all.
//
// Lowering it is the point. Raising it means a formatting change made ogo fmt agree
// with gofmt LESS often, which is worth a second look even when the new output
// looks reasonable -- write the same source as .go and run gofmt on it.
//
// The count also moves when the CORPUS grows, which is not the same thing and is
// the only reason it has ever gone up: 37 -> 38 when "a variadic of interfaces"
// arrived writing a method pair on adjacent lines, and 38 -> 39 when "a conversion
// to an interface type" did the same. Both are the alignment gap already named
// above and not a change to the formatter. Raising it for that reason is allowed;
// raising it because output changed is not. Check which one it is by writing the
// program as .go and running gofmt on it, as the test does.
//
// 39 -> 34 closed a category that was plainly wrong rather than merely unaligned: a
// call spaced off what it is called ON ("pick(0) (3, 4)", "(dbl) (21)", "} ()").
// 34 -> 31 closed the other one, a numeric literal's base prefix and exponent left in
// upper case ("0B1010", "2.5E2").
// 31 -> 18 closed the PRECEDENCE spacing gap whole: computeTightOps carries
// go/printer's depth/cutoff rule ("i*10+j == 12", "fib(n-1)", "f(a + b)" and
// "f(a+b, c)", "(c >> 1) ^ poly" all as gofmt writes them).
// 18 -> 0 closed the rest: consecutive one-line function declarations align their
// braces into a column (alignFuncBraces -- a run breaks at a blank line, a comment
// LINE, a multi-line function or any other declaration, and aligns straight
// through a trailing comment); struct-field alignment breaks at an EMBEDDED field
// and at a field whose type spans lines, as gofmt's tabwriter column does; and a
// doubled parenthesis pair collapses to one ("((a))[1]" prints "(a)[1]").
//
// ZERO is a ratchet all the same: the corpus grows, and a new program may use
// what gofmt does and ogo fmt does not yet. When that happens, prefer closing the
// gap; document it here if it must stand.
const gofmtDisagreements = 0

// TestFormatMatchesGofmt formats every run-corpus program with `ogo fmt` and with
// real gofmt, and counts how many come out identical.
//
// The corpus is the widest body of OctoGo source there is, and every program in it
// is valid Go once given a package clause -- which is what makes gofmt an oracle
// for the formatter the way `go run` is one for the emitter. Reading the output and
// noticing a divergence finds one at a time; this finds all of them.
//
// Skipped when gofmt is not on PATH.
func TestFormatMatchesGofmt(t *testing.T) {
	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		t.Skip("no gofmt found; skipping the compare-with-gofmt run")
	}
	dir := t.TempDir()
	goSrc := filepath.Join(dir, "x.go")

	agree, differ := 0, 0
	var firstFew []string
	for _, tc := range emitRunCases {
		if err := os.WriteFile(goSrc, []byte("package main\n\n"+tc.src), 0o644); err != nil {
			t.Fatal(err)
		}
		want, err := exec.Command(gofmt, goSrc).Output()
		if err != nil {
			continue // not valid Go: nothing to compare against
		}
		var got bytes.Buffer
		if err := FormatFile("x.ogo", []byte(tc.src), &got); err != nil {
			t.Errorf("%s: FormatFile: %v", tc.name, err)
			continue
		}
		if strings.TrimPrefix(string(want), "package main\n\n") == got.String() {
			agree++
			continue
		}
		differ++
		if len(firstFew) < 8 {
			firstFew = append(firstFew,
				tc.name+"\n"+firstDiff(got.String(), strings.TrimPrefix(string(want), "package main\n\n")))
		}
	}
	t.Logf("%d of %d programs format exactly as gofmt does", agree, agree+differ)
	if differ > gofmtDisagreements {
		t.Errorf("ogo fmt now disagrees with gofmt on %d programs, was %d.\n%s\n"+
			"A formatting change made the two agree LESS often. Check the new output "+
			"against real gofmt on the same source written as .go before accepting it.",
			differ, gofmtDisagreements, strings.Join(firstFew, "\n"))
	}
	if differ < gofmtDisagreements {
		t.Errorf("ogo fmt now disagrees with gofmt on only %d programs, better than the "+
			"recorded %d. Lower gofmtDisagreements to %d so the improvement is held.",
			differ, gofmtDisagreements, differ)
	}
}
