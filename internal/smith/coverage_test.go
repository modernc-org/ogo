// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"testing"
)

// generatedConstructs is what the generator is supposed to produce, each with a
// pattern matching it in the generated source. Adding a construct to the generator
// means adding it here, which is the point: the entry is what makes its coverage a
// tested property rather than an assumption.
var generatedConstructs = []struct {
	name    string
	pattern string
}{
	{"for loop", `\n\s*for \(`},
	{"if statement", `\n\s*if \(`},
	{"switch statement", `\n\s*switch `},
	{"switch skipped case", `\n\s*case -?\d+:\n\s*case `},
	{"switch multi-value case", `\n\s*case -?\d+, -?\d+:`},
	{"switch default", `\n\s*default:`},
	{"int variable", `\n\s*var v_\d+ int`},
	{"bool variable", `\n\s*var b_\d+ bool = `},
	{"bool negation", `!\(`},
	{"short-circuit &&", ` && `},
	{"short-circuit ||", ` \|\| `},
	{"bool as a condition", `\n\s*if b_\d+ \{`},
	{"sized int8/uint8", `\n\s*var z_\d+ u?int8 = `},
	{"sized int16/uint16", `\n\s*var z_\d+ u?int16 = `},
	{"sized uint32", `\n\s*var z_\d+ uint32 = `},
	{"sized unary minus", `= -\(z_\d+\)`},
	{"sized complement", `= \^\(z_\d+\)`},
	{"sized shift", `z_\d+ (<<|>>) `},
	{"sized compound assignment", `z_\d+ (\+|-|\*|/|%|<<|>>|&|\||\^|&\^)= `},
	{"sized conversion to int", `int\(-?\^?\(?z_\d+`},
	// The one that matters: a value folded into the checksum WITHOUT being stored
	// back first. A store truncates to the type, so a stored result cannot tell a
	// compiler that computed in the wrong width from one that did not.
	{"method declaration, value receiver", `\nfunc \(r S_\d+\) `},
	{"method declaration, pointer receiver", `\nfunc \(r \*S_\d+\) `},
	{"method call, value-receiver getter", `\.get_\d+\(\)`},
	{"method call, pointer-receiver setter", `\.set_\d+\(`},
	// The one that pins the receiver rule: a value receiver writes to a COPY, so
	// the caller's field must be unchanged after this call.
	{"method call, value-receiver shadow", `\.shadow_\d+\(`},
	{"string variable", `\n\s*var t_\d+ string = `},
	{"string len", `len\(t_\d+\)`},
	{"string byte index", `int\(t_\d+\[\d+\]\)`},
	{"string slice", `len\(t_\d+\[\d+:\d+\]\)`},
	{"string comparison", `t_\d+ == "`},
	{"string range, index and rune", `range t_\d+ \{`},
	// strconv.Quote leaves a printable rune as itself, so a multibyte literal is
	// spotted by its bytes rather than by an escape.
	{"multibyte string literal", `var t_\d+ string = "[^"]*[^\x00-\x7f]`},
	{"sized fold of an unstored expression", `int\((\(z_\d+ |-\(z_\d+|\^\(z_\d+)`},
	{"fixed array", `\n\s*var a_\d+ \[\d+\]int`},
	{"element index", `[as]_\d+\[\d+\]`},
	{"element swap", `\w+\[\d+\], \w+\[\d+\] = `},
	{"slice make", `make\(\[\]int`},
	{"append", `append\(`},
	{"len", `len\(`},
	{"cap", `cap\(`},
	{"struct type", `\ntype S_\d+ struct`},
	{"struct variable", `\n\s*var st_\d+ S_\d+`},
	{"struct field", `st_\d+\.f_\d+`},
	{"struct copy", `\n\s*st_\d+ := st_\d+`},
	{"compound assignment", `\w+ (\+|-|\*|/|%|<<|>>|&|\||\^|&\^)= `},
	{"function declaration", `\nfunc fn_\d+\(`},
	{"function call", `fn_\d+\(`},
	{"two-result function", `\) \(int, int\) \{`},
	{"two-result destructuring", `\n\s*d_\d+, d_\d+ := fn_\d+\(`},
	{"deferred call", `\n\s*defer sink_\d+\(v\)`},
	{"deferred method call, value receiver", `\n\s*defer dr_\d+\.emit_\d+\(\)`},
	{"call of a defer-carrying procedure", `\n\s*dp_\d+\(\)`},
}

// TestGeneratorCoverage asserts that the generator still emits every construct it
// is meant to.
//
// A fuzzer has a blind spot no other test covers: one that generates *less* still
// passes everything. TestOracle only checks that whatever was generated computes
// what the VM predicted, so a construct that stops being produced takes its
// coverage with it silently, and the suite stays green.
//
// That is not hypothetical. Rebalancing genStatement's dispatch once dropped
// compound assignment entirely; staticcheck noticed only because the function
// became unreferenced. It would not have noticed a generator still called from a
// case whose probability range can no longer be reached -- reordering two cases is
// enough -- which this test would catch and staticcheck would not.
func TestGeneratorCoverage(t *testing.T) {
	const seeds = 100

	var b bytes.Buffer
	for seed := 1; seed <= seeds; seed++ {
		if err := Main([]string{"-seed", strconv.Itoa(seed)}, &b, io.Discard); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
	src := b.String()

	for _, c := range generatedConstructs {
		re, err := regexp.Compile(c.pattern)
		if err != nil {
			t.Fatalf("%s: bad pattern %q: %v", c.name, c.pattern, err)
		}
		switch n := len(re.FindAllString(src, -1)); {
		case n == 0:
			t.Errorf("%s never generated in %d seeds (pattern %q): a generator that "+
				"produces less still passes TestOracle, so this is the only test that "+
				"notices. Either the dispatch no longer reaches it, or the pattern needs "+
				"updating because the output changed.", c.name, seeds, c.pattern)
		default:
			t.Logf("%-22s %d", c.name, n)
		}
	}
}
