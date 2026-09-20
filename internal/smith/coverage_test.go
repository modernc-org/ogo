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
	{"widening function", `\nfunc fn_\d+\([^)]*\) int64 \{\n(\toctosmith_calls = [^\n]*\n)?\treturn int64\(`},
	{"sized variable from a widening call", `\n\s*var z_\d+ int64 = fn_\d+\(`},
	{"defined int64 variable from a widening call", `\n\s*var z_\d+ D_\d+ = D_\d+\(fn_\d+\(`},
	// A function of 64-bit parameters, and a call of one with a constant after an
	// argument that is an arithmetic expression of 64-bit type -- a product or a
	// negation -- which the target passed as one word; see FuncDef.Params64.
	{"function of int64 parameters", `\nfunc fn_\d+\(p_\d+ int64[^)]*\) int64 \{\n(\toctosmith_calls = [^\n]*\n)?\treturn `},
	// Every generated function counts its calls, and main asserts the count: a call
	// evaluated twice or not at all changes nothing else a pure function's caller
	// can see (see Fuzzer.CallsName).
	{"call counted", `\n\toctosmith_calls = octosmith_calls \+ \d+\n\treturn `},
	{"call count asserted", `\n\tif octosmith_calls != -?\d+ \{`},
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
	// An untyped constant shifted by a count that is not constant, `(3 << c)`, in a
	// sized block: the count declared with a type of its own, which must not decide
	// the shift's, and the shift used as the variable's initializer, stored by a
	// step, and beside the variable in an unstored fold. Go types the constant by
	// that context; the emitter computed it in int or in the count's type until
	// 2026-09-14, and disabling the context typing fails the oracle -- checked.
	{"untyped shift count", `\n\s*var c_\d+ (uint|int|uint8|int64) = \d+\n`},
	{"sized variable from an untyped shift", `\n\s*var z_\d+ \S+ = \(\d ?<< ?c_\d+\)\n`},
	{"untyped shift stored", `\n\s*z_\d+ (=|\^=) .*\(\d ?<< ?c_\d+\)`},
	{"untyped shift beside a sized variable, unstored", `int\(\((z_\d+ ?[&|^+] ?\(\d ?<< ?c_\d+\)|\(\d ?<< ?c_\d+\) ?[&|^+] ?z_\d+)\)\)`},
	// A forward goto skipping a checksum fold, and the label it jumps to. The
	// label sits at a reduced indent, as gofmt places one. Both the goto codegen
	// (the emitter's label pass) and the checker's jump rules had no fuzz coverage
	// until this construct; the two entries keep the guard and the target each a
	// tested property.
	// A struct EMBEDDING another, whose fields and methods it promotes. Which of
	// the two a field write or a method call names cannot be told from the source
	// by a pattern -- the names are unique either way -- so what is pinned here is
	// that the shape is generated at all.
	{"embedded struct", `\ntype S_\d+ struct \{\n\tS_\d+\n`},
	// A labeled CONTINUE and BREAK out of nested loops, the jump that leaves more
	// than one at once. Both loops carry their step in a post clause, which is what
	// a labeled continue still runs.
	{"labeled loop", `\n\s*L_\d+:\n\s*for \w+ := 0;`},
	{"labeled continue", `\n\s*continue L_\d+`},
	{"labeled break", `\n\s*break L_\d+`},
	// A POINTER to a struct: a write through it, read back through both names, and
	// a method whose receiver is what the pointer holds.
	{"pointer to a struct", `\n\s*sp_\d+ := &st_\d+`},
	{"write through a pointer to a struct", `\n\s*sp_\d+\.f_\d+ = `},
	{"method on a pointer to a struct", `sp_\d+\.set_\d+\(`},
	// A SELECT taking a worker's value: two arms, one on the channel the worker
	// sends to and one on a channel nothing ever sends to, so which arm runs is
	// decided by the senders and not by timing.
	{"select receive", `\n\s*select \{`},
	// Struct and array EQUALITY, `v == w`, which C has no operator for: the emitter
	// mints a helper per type and this is what reaches it. The copy is what makes
	// the first pair equal for certain, and the write is what makes the second pair
	// differ -- each compared both ways, one of the two folding.
	{"struct equality copy", `\n\s*eq_\d+ := st_\d+\n`},
	{"array equality copy", `\n\s*eq_\d+ := a_\d+\n`},
	{"equality comparison", `if \(\w+_\d+ == eq_\d+\)`},
	{"inequality comparison", `if \(\w+_\d+ != eq_\d+\)`},
	{"equality after a field write", `\n\s*eq_\d+\.f_\d+ = `},
	{"equality after an element write", `\n\s*eq_\d+\[\d+\] = `},
	// The slice builtins, each with a helper of its own in the emitter: copy with
	// its count, clear, and the THREE-index reslice whose capacity the third bound
	// sets. The reslice is only read from -- it shares a backing array the VM does
	// not model -- so len, cap and an element of it are what the folds check.
	{"copy of a slice", `\n\s*cn_\d+ := copy\(cp_\d+, `},
	{"copy count folded", `\n\s*octosmith_checksum = \(octosmith_checksum \^ cn_\d+\)`},
	{"three-index reslice", `\n\s*vw_\d+ := cp_\d+\[\d+:\d+:\d+\]`},
	{"cap of a three-index reslice", `\^ cap\(vw_\d+\)`},
	{"clear of a slice", `\n\s*clear\(cp_\d+\)`},
	// A TWO-dimensional array: the emitter has a shape of its own per dimension --
	// an index that consumes one, a row that is still an array, a range whose
	// variable is a row -- and every array generated before this was flat.
	{"two-dimensional array", `\n\s*var g_\d+ \[\d+\]\[\d+\]int`},
	{"two-index write", `\n\s*g_\d+\[\d+\]\[\d+\] = `},
	{"len of a row", `\^ len\(g_\d+\[0\]\)`},
	{"range over rows", `\n\s*for i_\d+ := range g_\d+ \{`},
	{"range within a row", `\n\s*for j_\d+, v_\d+ := range g_\d+\[i_\d+\] \{`},
	{"select arm on a worker's channel", `\n\s*case r_\d+ := <-ch_\d+:`},
	{"select arm never ready", `\n\s*case r_\d+ := <-idle_\d+:`},
	// A METHOD EXPRESSION, the method as a function whose first parameter is the
	// receiver, bound to a value and called through it. The two receivers part
	// company here as they do at a method call: the pointer form is handed this
	// struct, the value form a copy of it, which the field read after the call
	// tells apart.
	{"method expression, value receiver", `= S_\d+\.shadow_\d+`},
	{"method expression, pointer receiver", `= \(\*S_\d+\)\.set_\d+`},
	{"call through a method expression", `\^ me_\d+\(`},
	// A function LITERAL, which is lifted to a function of its own: called where it
	// stands, and bound to a variable and called through it -- a function value
	// holding a literal, which the emitter binds by the literal's place rather than
	// by a name.
	{"function literal", `func\(p_\d+ int[^)]*\) int \{`},
	{"function literal bound to a value", `\n\s*var lv_\d+ func\([^)]*\) int = func\(`},
	{"call through a function literal value", `\^ lv_\d+\(`},
	{"function literal called where it stands", `\^ func\(p_\d+ int`},
	{"forward goto", `\n\s*goto L_\d+\n`},
	{"goto target label", `\n\s*L_\d+:\n`},
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
