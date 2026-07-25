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
	{"int variable", `\n\s*var v_\d+ int`},
	{"fixed array", `\n\s*var a_\d+ \[\d+\]int`},
	{"element index", `[as]_\d+\[\d+\]`},
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
