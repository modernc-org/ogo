// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode"
)

// A TestFilter is a -run or -bench pattern as go test reads one (testing's
// match.go): split at each slash and each bar outside brackets and parentheses into
// alternatives, each a regular expression for each level of a test's name -- the
// name split at its slashes -- every one of which must match its level, unanchored,
// as regexp.MatchString matches. A test's own name is one level and is matched
// here, where the runner is generated; a subtest's levels exist only as the test
// runs, on the board, and are matched there, by the testing package's Pike machine
// running the program Prog encodes -- each level's regular expression compiled by
// Go's regexp/syntax, as regexp.Compile compiles it, so both sides match alike.
type TestFilter struct {
	alts [][]*regexp.Regexp
	prog []int32
}

// The program's limits: a level's regular expression of 256 instructions, which the
// machine's sets of threads are sized for, and 32 alternatives, the bits of the
// mask saying which a name still matches.
const (
	filterMaxInsts = 256
	filterMaxAlts  = 32
)

// The machine's operations (testing's opMatch and the rest).
const (
	filterOpMatch = iota
	filterOpAlt
	filterOpNop
	filterOpEmpty
	filterOpRune
	filterOpFail
)

// CompileTestFilter compiles pattern, the value of flag, -run or -bench. The
// program it encodes is laid out in int32s:
//
//	[0]                 the number of alternatives, A
//	[1+a]               where alternative a begins
//	alternative         the number of its levels, then where each level's program begins
//	level's program     the number of its instructions, L; its first one; then the
//	                    instructions, four words each: op, out, arg, n
//	ranges              pairs of runes, first and last
//
// out and an alternative's arg, its second branch, are instructions of the same
// program, numbered from 0; an empty-width instruction's arg is the syntax.EmptyOp
// it asks for; a rune's arg is where its n pairs begin. Every match of a rune is
// such a range: a literal folding case is its case orbit spelled out, which spares
// the board Unicode's tables.
func CompileTestFilter(flag, pattern string) (*TestFilter, error) {
	split := splitTestPattern(pattern)
	if len(split) > filterMaxAlts {
		return nil, fmt.Errorf("%s %q: more than %d alternatives", flag, pattern, filterMaxAlts)
	}
	f := &TestFilter{}
	var levels [][]int32 // the program of each level, its rune ranges indexed from 0
	for a, alt := range split {
		var res []*regexp.Regexp
		for k, el := range alt {
			el = rewriteTestName(el)
			where := fmt.Sprintf("element %d", k)
			if len(split) > 1 {
				where = fmt.Sprintf("alternative %d, element %d", a, k)
			}
			re, err := regexp.Compile(el)
			if err != nil {
				return nil, fmt.Errorf("bad %s pattern %q: %s (%q): %v", flag, pattern, where, el, err)
			}
			p, err := filterLevel(el)
			if err != nil {
				return nil, fmt.Errorf("%s pattern %q: %s (%q): %v", flag, pattern, where, el, err)
			}
			res = append(res, re)
			levels = append(levels, p)
		}
		f.alts = append(f.alts, res)
	}

	// The layout: the alternatives' table, the levels' programs, the ranges after
	// all of them, each level's arg of a rune moved to where its ranges land.
	prog := []int32{int32(len(f.alts))}
	prog = append(prog, make([]int32, len(f.alts))...)
	var at []int // where each alternative's list of levels is
	for a := range f.alts {
		prog[1+a] = int32(len(prog))
		at = append(at, len(prog))
		prog = append(prog, int32(len(f.alts[a])))
		prog = append(prog, make([]int32, len(f.alts[a]))...)
	}
	var ranges []int32
	var fix []int // the rune instructions' args, indexes into ranges until the end
	lv := 0
	for a := range f.alts {
		for k := range f.alts[a] {
			p := levels[lv]
			lv++
			prog[at[a]+1+k] = int32(len(prog))
			n := int(p[0])
			prog = append(prog, p[0], p[1])
			base := len(ranges)
			insts := p[2 : 2+4*n]
			for j := 0; j < n; j++ {
				if insts[4*j] == filterOpRune {
					fix = append(fix, len(prog)+4*j+2)
					insts[4*j+2] += int32(base)
				}
			}
			prog = append(prog, insts...)
			ranges = append(ranges, p[2+4*n:]...)
		}
	}
	for _, i := range fix {
		prog[i] += int32(len(prog))
	}
	f.prog = append(prog, ranges...)
	return f, nil
}

// filterLevel compiles one level's regular expression as regexp.Compile does --
// Perl syntax, simplified -- into the program CompileTestFilter lays out: L, the
// first instruction, the instructions, then its ranges, a rune's arg indexing them.
func filterLevel(expr string) ([]int32, error) {
	re, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		return nil, err
	}
	sp, err := syntax.Compile(re.Simplify())
	if err != nil {
		return nil, err
	}
	if len(sp.Inst) > filterMaxInsts {
		return nil, fmt.Errorf("compiles to %d instructions, more than the %d the board's matcher runs", len(sp.Inst), filterMaxInsts)
	}
	out := []int32{int32(len(sp.Inst)), int32(sp.Start)}
	var ranges []int32
	pair := func(lo, hi rune) { ranges = append(ranges, lo, hi) }
	for _, in := range sp.Inst {
		op, arg, n := int32(filterOpFail), int32(0), int32(0)
		switch in.Op {
		case syntax.InstAlt, syntax.InstAltMatch:
			op, arg = filterOpAlt, int32(in.Arg)
		case syntax.InstCapture, syntax.InstNop:
			op = filterOpNop
		case syntax.InstEmptyWidth:
			op, arg = filterOpEmpty, int32(in.Arg)
		case syntax.InstMatch:
			op = filterOpMatch
		case syntax.InstFail:
			op = filterOpFail
		case syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
			op, arg = filterOpRune, int32(len(ranges))
			switch {
			case in.Op == syntax.InstRuneAny:
				pair(0, unicode.MaxRune)
			case in.Op == syntax.InstRuneAnyNotNL:
				pair(0, '\n'-1)
				pair('\n'+1, unicode.MaxRune)
			case len(in.Rune) == 1:
				// One rune, and with FoldCase every rune of its orbit, as
				// syntax.Inst.MatchRunePos asks unicode.SimpleFold.
				r0 := in.Rune[0]
				pair(r0, r0)
				if in.Op == syntax.InstRune && syntax.Flags(in.Arg)&syntax.FoldCase != 0 {
					for r := unicode.SimpleFold(r0); r != r0; r = unicode.SimpleFold(r) {
						pair(r, r)
					}
				}
			default:
				for j := 0; j+1 < len(in.Rune); j += 2 {
					pair(in.Rune[j], in.Rune[j+1])
				}
			}
			n = int32(len(ranges))/2 - arg/2
		}
		out = append(out, op, int32(in.Out), arg, n)
	}
	return append(out, ranges...), nil
}

// Top reports which alternatives the test or benchmark name matches, a bit each,
// none where it does not run, and whether it matches only in part: the first
// alternative it matches names levels below it, so of a benchmark only its
// sub-benchmarks run (go test's simpleMatch and alternationMatch).
func (f *TestFilter) Top(name string) (mask uint32, partial bool) {
	first := true
	for a, alt := range f.alts {
		if !alt[0].MatchString(name) {
			continue
		}
		mask |= 1 << a
		if first {
			partial, first = len(alt) > 1, false
		}
	}
	return mask, partial
}

// Subs reports whether any alternative names a level below a test's own, which is
// what makes the board match a subtest's name.
func (f *TestFilter) Subs() bool {
	for _, alt := range f.alts {
		if len(alt) > 1 {
			return true
		}
	}
	return false
}

// Prog is the program the board runs (testing.SetFilter), laid out as
// CompileTestFilter says.
func (f *TestFilter) Prog() []int32 { return f.prog }

// splitTestPattern is go test's splitRegexp: the alternatives of s, each its levels.
func splitTestPattern(s string) [][]string {
	var b [][]string
	var a []string
	cs, cp := 0, 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '[':
			cs++
		case ']':
			if cs--; cs < 0 { // an unmatched ']' is legal
				cs = 0
			}
		case '(':
			if cs == 0 {
				cp++
			}
		case ')':
			if cs == 0 {
				cp--
			}
		case '\\':
			i++
		case '/':
			if cs == 0 && cp == 0 {
				a = append(a, s[:i])
				s = s[i+1:]
				i = 0
				continue
			}
		case '|':
			if cs == 0 && cp == 0 {
				a = append(a, s[:i])
				s = s[i+1:]
				i = 0
				b = append(b, a)
				a = nil
				continue
			}
		}
		i++
	}
	return append(b, append(a, s))
}

// rewriteTestName is go test's rewrite, of a pattern's level as of a subtest's name:
// each space an underscore, each rune that does not print escaped as Go quotes it.
func rewriteTestName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case testNameSpace(r):
			b.WriteByte('_')
		case !strconv.IsPrint(r):
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// testNameSpace is go test's isSpace: not the same as Unicode's Z class.
func testNameSpace(r rune) bool {
	if r < 0x2000 {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0, 0x1680:
			return true
		}
		return false
	}
	if r <= 0x200a {
		return true
	}
	switch r {
	case 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return false
}
