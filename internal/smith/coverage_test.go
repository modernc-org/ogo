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
	{"sized int64/uint64", `\n\s*var z_\d+ u?int64 = `},
	{"64-bit high-half fold", `int\(\(z_\d+ ?>> ?32\)\)`},
	{"sized unary minus", `= -\(z_\d+\)`},
	{"sized complement", `= \^\(z_\d+\)`},
	{"sized shift", `z_\d+ (<<|>>) `},
	{"sized compound assignment", `z_\d+ (\+|-|\*|/|%|<<|>>|&|\||\^|&\^)= `},
	{"sized conversion to int", `int\(-?\^?\(?z_\d+`},
	// The one that matters: a value folded into the checksum WITHOUT being stored
	// back first. A store truncates to the type, so a stored result cannot tell a
	// compiler that computed in the wrong width from one that did not.
	// A DEFINED type over a sized kind, and a variable declared with it. The pair
	// matters: declaring the type and never using it would satisfy the first
	// pattern while testing nothing, which is the failure mode this whole test
	// exists to catch.
	{"defined type over a sized kind", `\ntype D_\d+ (u?int8|u?int16|uint32)\n`},
	{"sized variable of a defined type", `\n\s*var z_\d+ D_\d+ = `},
	// A defined SLICE type, and a variable made with one. The make is the point:
	// it is what gives the capacity headroom the append generator needs, and it is
	// the spelling that was refused outright until this week.
	{"defined type over a slice", `\ntype L_\d+ \[\]int\n`},
	{"slice variable of a defined type", `\n\s*var s_\d+ L_\d+ = make\(L_\d+, `},
	// The initialization-order cluster: package variables whose initializers
	// depend on one another, emitted SHUFFLED (genPkgVarCluster). The reader
	// function is what routes a dependency through a function's BODY, and the
	// two-name group through a multi-value call; the fold is what makes any
	// mis-ordering fail the oracle rather than pass unobserved.
	{"package var cluster", `\nvar gv_\d+ = `},
	{"cluster arithmetic initializer", `\nvar gv_\d+ = \S+ (&\^?|\||\^) `},
	{"cluster reader function", `\nfunc gr_\d+\(p_\d+ int\) int `},
	{"cluster reader call initializer", `\nvar gv_\d+ = gr_\d+\(`},
	{"cluster destructuring initializer", `\nvar gv_\d+, gv_\d+ = fn_\d+\(`},
	{"cluster checksum fold", `\n\s*octosmith_checksum = octosmith_checksum \^ gv_\d+`},
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
	// Two operations in a row with NO parenthesis between them, the shape every
	// other generated expression cannot take (BinaryExprNode parenthesises). The
	// first may overflow the type, and Go wraps it before the second is applied;
	// a compiler wrapping only the total agreed with the VM on every other fold.
	{"sized chain of two operations", `int\(\(z_\d+ [-+*] -?\d+ [-+*/%] -?\d+\)\)`},
	// A negation feeding an addition or subtraction in one expression, which the
	// target miscompiled for a 64-bit value with its inliner on.
	{"sized negation beside an addition", `int\(\(-z_\d+ [-+] -?\d+\)\)`},
	// The high half of a 64-bit fold EXPRESSION, not only of the variable: the
	// negation fault above corrupted only the high word, which int(...) drops.
	{"64-bit fold expression high half", `int\(\(\(-z_\d+ [-+] -?\d+\) ?>> ?32\)\)`},
	// A function whose whole return operand is a widening conversion, and a sized
	// variable drawn from a call of one: `return int64(p)` is what the target
	// returned with a garbage high word.
	{"widening function", `\nfunc fn_\d+\([^)]*\) int64 \{\n\treturn int64\(`},
	{"sized variable from a widening call", `\n\s*var z_\d+ int64 = fn_\d+\(`},
	{"defined int64 variable from a widening call", `\n\s*var z_\d+ D_\d+ = D_\d+\(fn_\d+\(`},
	// A function of 64-bit parameters, and a call of one with a constant after an
	// argument that is an arithmetic expression of 64-bit type -- a product or a
	// negation -- which the target passed as one word; see FuncDef.Params64.
	{"function of int64 parameters", `\nfunc fn_\d+\(p_\d+ int64[^)]*\) int64 \{\n\treturn `},
	{"constant after an expression argument", `fn_\d+\(\(z_\d+ ?[-+*] ?-?\d+\), -?\d+`},
	{"constant after a negated argument", `fn_\d+\(-\(z_\d+\), -?\d+`},
	// A float32 variable, its arithmetic in Go's float32 semantics, and the three
	// ways it reaches the checksum -- the assertion against the exact literal, a
	// comparison, the truncation to int -- plus an int converted into it.
	{"float32 variable", `\n\s*var fl_\d+ float32 = `},
	{"float32 arithmetic step", `\n\s*fl_\d+ = \(fl_\d+ ?[-+*/] ?`},
	{"float32 negation", `\n\s*fl_\d+ = -\(fl_\d+\)`},
	{"float32 assertion", `if \(fl_\d+ != `},
	{"float32 comparison", `if \(fl_\d+ < `},
	{"int from a float32", `int\(fl_\d+\)`},
	{"float32 from an int", `float32\([a-z]+_\d+\)`},
	{"fixed array", `\n\s*var a_\d+ \[\d+\]int`},
	{"element index", `[as]_\d+\[\d+\]`},
	{"element swap", `\w+\[\d+\], \w+\[\d+\] = `},
	// A pointer to an ARRAY, the one pointer an index applies to. The write and the
	// read-back are separate entries because they are separate properties: the
	// write must go through the dereference, and the array's own name must see it.
	{"pointer to an array", `\n\s*pa_\d+ := &a_\d+`},
	{"write through a pointer to an array", `\n\s*pa_\d+\[\d+\] = `},
	{"len of a pointer to an array", `len\(pa_\d+\)`},
	{"range over a pointer to an array", `range pa_\d+ \{`},
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
	{"function value", `\n\s*var fv_\d+ func\(`},
	{"call through a function value", `\n\s*octosmith_checksum = \(octosmith_checksum \^ fv_\d+\(`},
	{"two-result function value", `\n\s*var fv_\d+ func\([^)]*\) \(int, int\) = `},
	{"goroutine worker", `\nfunc cog_\d+\(c chan int, p_\d+ int\) \{`},
	{"channel send from a worker", `\n\s*c <- `},
	{"go statement", `\n\s*go cog_\d+\(ch_\d+, `},
	{"channel receive", `\n\s*r_\d+ := <-ch_\d+`},
	{"min builtin", `= min\(`},
	{"max builtin", `= max\(`},
	{"deferred call", `\n\s*defer sink_\d+\(v\)`},
	{"deferred method call, value receiver", `\n\s*defer dr_\d+\.emit_\d+\(\)`},
	{"call of a defer-carrying procedure", `\n\s*dp_\d+\(\)`},
	// Interfaces. Every struct implements Val the SAME way round, which is what lets
	// one interface type hold any of them -- and what makes the dispatch dynamic to
	// the compiler while staying static to the generator, so the oracle can still
	// predict it. The four call shapes are separate entries because they lower
	// differently: through the vtable, through an assertion, and through the case
	// comparisons of a type switch.
	{"interface method declaration", `\nfunc \(r \*S_\d+\) Val\(\) int`},
	{"interface type", `\ntype Valuer interface`},
	{"interface variable bound to a struct", `\n\s*var if_\d+ Valuer = &st_\d+`},
	{"call through an interface", `if_\d+\.Val\(\)`},
	{"type assertion, then a call on its result", `if_\d+\.\(\*S_\d+\)\.Val\(\)`},
	{"type switch on an interface", `switch x := if_\d+\.\(type\)`},
	// The multiplication's spacing follows the formatter: inside the parentheses
	// the depth rule spaces it ("(x.Val() * 3)"), as gofmt does.
	{"type switch case not taken", `case \*S_\d+:\n\s*\w+ = \w+ \^ \(x\.Val\(\) ?\* ?\d+\)\n\s*case \*S_\d+:`},
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
