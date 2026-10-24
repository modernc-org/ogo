// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"strings"
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
	{"parenthesised target", `\n\s*\(fl_\d+\) = `},
	{"for loop", `\n\s*for \w+ < \d+ \{`}, // written `for (i < n)`, which ogo fmt prints as gofmt does
	{"if statement", `\n\s*if `},
	{"switch statement", `\n\s*switch `},
	// A statement standing alone in a header, a call whose results nothing reads:
	// the call counter is what says it ran once, and where.
	{"if with a call for its init", `\n\s*if fn_\d+\(.*\); `},
	{"switch with a call for its init", `\n\s*switch fn_\d+\(.*\); `},
	{"for with a call for its init and its post", `\n\s*for fn_\d+\(.*\); \w+ < \d+; fn_\d+\(.*\) \{`},
	// A function of several returns, in each of its shapes: the C the emitter
	// rewrites to leave through one return (singleExit), each shape a different
	// text to rewrite. Only such a function has a return below its top level.
	{"several returns: tests in a row", `\n\tif [^\n]+ \{\n\t\treturn [^\n]+\n\t\}\n\tif [^\n]+ \{\n\t\treturn `},
	{"several returns: an else chain", `\n\t\} else if [^\n]+ \{\n\t\treturn `},
	{"several returns: a switch", `\n\tswitch \{\n\tcase [^\n]+:\n\t\treturn `},
	{"several returns: nested tests", `\n\tif [^\n]+ \{\n\t\tif [^\n]+ \{\n\t\t\treturn `},
	// Constants, written every way one is read, and read beside each integer type
	// (see genConstDecls): the row a named constant beside an unsigned operand was
	// compared wrongly in, on the board, while no generated program had a constant.
	{"a constant group", `\nconst \(\n`},
	{"an iota group", `\n\t\w+ += iota\*\d+ \+ \d+\n\t\w+\n`},
	{"a rune constant", `\n\t\w+ += '[A-Za-z]'\n`},
	{"a constant written as a float", `\n\t\w+ += \d\.\de\d\n`},
	{"a hex constant", `\n\t\w+ += 0x[0-9a-f]+\n`},
	{"a negative constant", `\n\t\w+ += -\d+\n`},
	{"an expression of constants", `\n\t\w+ += \(\w+ [-+*] \w+\)\n`},
	{"a typed constant", `\nconst \w+ \w+ = -?\d+\n`},
	{"a typed constant as a conversion", `\nconst \w+ = \w+\(-?\d+\)\n`},
	{"a block constant", `\n\t+const \w+ = -?\d+\n`},
	{"a block constant read from the one it shadows", `\n\t+const \w+ = \w+ \+ \d+\n`},
	{"a constant compared with a sized value", `\n\t+if [^\n]* [<>=!]=? k_\d+ \{|\n\t+if k_\d+ [<>=!]=? `},
	{"a constant divided by a sized value", `= \(?k_\d+ [/%] \w+\)?\n`},
	{"a sized variable declared from a constant", `\n\t+var \w+ \w+ = k_\d+\n`},
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
	// A narrower value widened to 64 bits and its high word folded, a zero constant
	// on its left or not (genWideFold).
	{"a widened fold's high word", `int\(\(u?int64\(.+\) >> 32\)\)`},
	{"a widened fold with a zero on its left", `int\(\(u?int64\(\(0 [+|] `},
	// A fold converted to another sized kind, with an operation in it or not
	// (genCrossFold).
	{"a fold converted to another sized kind", `int\(u?int(8|16|32)\(\(`},
	{"an operation on a converted fold", `int\(\(u?int(8|16|32|64)\(.+\) [+*>]+ \d+\)\)`},
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
	// two-name group through a multi-value call; the check is what makes any
	// mis-ordering fail the oracle rather than pass unobserved.
	// A package table of a struct holding a small-element array (genStaticTables),
	// which the target's compiler initialized statically in another layout than it
	// reads (flexprop#116); an array of them and a slice of them.
	{"static table struct", `\ntype ST_\d+ struct \{\n\th u?int(8|16)\n\ta \[\d\]u?int(8|16)\n`},
	{"static table", `\nvar tb_\d+ = ST_\d+\{`},
	{"static table array", `\nvar tbs_\d+ = \[\d\]ST_\d+\{\{`},
	{"static table slice", `\nvar tsl_\d+ = \[\]ST_\d+\{\{`},
	{"static table checked", `\n\tif int\(tb_\d+\.h\)[^\n]* != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: tables"\)`},
	{"package var cluster", `\nvar gv_\d+ = `},
	{"cluster arithmetic initializer", `\nvar gv_\d+ = \S+ (&\^?|\||\^) `},
	{"cluster reader function", `\nfunc gr_\d+\(p_\d+ int\) int `},
	{"cluster reader call initializer", `\nvar gv_\d+ = gr_\d+\(`},
	{"cluster destructuring initializer", `\nvar gv_\d+, gv_\d+ = fn_\d+\(`},
	{"cluster checked", `\n\tif gv_\d+ != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: init order"\)`},
	{"method declaration, value receiver", `\nfunc \(r S_\d+\) `},
	{"method declaration, pointer receiver", `\nfunc \(r \*S_\d+\) `},
	{"method call, value-receiver getter", `\.get_\d+\(\)`},
	{"method call, pointer-receiver setter", `\.set_\d+\(`},
	// The one that pins the receiver rule: a value receiver writes to a COPY, so
	// the caller's field must be unchanged after this call.
	{"method call, value-receiver shadow", `\.shadow_\d+\(`},
	// A slice converted to types written out (genSliceConvStmt): the header under
	// another type, and an array COPY a later write through the slice must not
	// reach.
	{"slice conversion written out", `\n\s*cv_\d+ := \[\]int\(s_\d+\)`},
	{"slice to array conversion written out", `\n\s*ar_\d+ := \[\d+\]int\(s_\d+\)`},
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
	{"float32 assertion", `if fl_\d+ != `},
	{"float32 comparison", `if fl_\d+ < `},
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
	{"buffered channel", `\nvar ch_\d+ = make\(chan int, [1-3]\)`},
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
	{"equality comparison", `if \w+_\d+ == eq_\d+ \{`},
	{"inequality comparison", `if \w+_\d+ != eq_\d+ \{`},
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
	// The SPREAD append, one memmove in the emitter rather than a loop -- which is
	// what makes `append(s, s...)` right where the two overlap.
	{"append spread", `= append\(s_\d+, s_\d+\.\.\.\)`},
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
	// Any names: the type or the method may have drawn one of cNames. Only the
	// method expression writes "(*T).m".
	{"method expression, pointer receiver", `= \(\*\w+\)\.\w+`},
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
	// Aggregates (genAggregates): a struct of mixed widths -- a sub-word member, an
	// array of small elements, a 64-bit scalar -- handed by value, by pointer and in
	// an array, returned alone and beside an int, and exercised where a call stands,
	// through a slice of an array of them, a method of each receiver and equality.
	{"aggregate struct type", `\ntype AG_\d+ struct \{\n\t\w+ u?int(8|16) *\n`},
	{"aggregate with a 64-bit field", `\ntype AG_\d+ struct \{\n(\t[^\n]*\n)*\t\w+ +u?int64\n`},
	{"aggregate with an array field", `\ntype AG_\d+ struct \{\n(\t[^\n]*\n)*\t\w+ +\[\d\]u?int(8|16|32)\n`},
	{"aggregate by value parameter and result", `\nfunc agMod_\d+\(v AG_\d+, d int\) AG_\d+ \{`},
	{"aggregate array by value", `\nfunc agArr_\d+\(a \[3\]AG_\d+, d int\) int \{`},
	{"aggregate beside an int", `\nfunc agPair_\d+\(v AG_\d+, d int\) \(AG_\d+, int\) \{`},
	{"aggregate where a call stands", `agSum_\d+\(agMod_\d+\(agMk_\d+\(`},
	{"aggregate field of a call's result", `int\(agMk_\d+\(-?\d+\)\.\w+`},
	{"aggregate pointer method through a slice", `\n\ts\[2\]\.bump\(`},
	{"aggregate equality", `\n\tif e == v \{`},
	{"aggregate checked", `\n\tif agRun_\d+\(\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: aggregates"\)`},
	{"aggregate over a channel to a cog", `\n\tgo agCog_\d+\(2\)\n\tagIn_\d+ <- v\n`},
	{"aggregate nested in a struct", `\ntype AN_\d+ struct \{\n\tn u?int(8|16)\n\tg AG_\d+\n\ts \[2\]AG_\d+\n`},
	{"nested aggregate literal", `\treturn AN_\d+\{n: `},
	{"nested aggregate equality", `\n\tif q != o \{`},
	{"array of nested aggregates", `\n\tvar na \[2\]AN_\d+\n`},
	{"aggregate array returned", `\nfunc agTrio_\d+\(p int\) \[3\]AG_\d+ \{`},
	{"aggregate array result as an argument", `agArr_\d+\(agTrio_\d+\(`},
	{"method of a call's array result", `agTrio_\d+\(k ?\+ ?2\)\[1\]\.sum\(\)`},
	{"range over a call's array result", `range agTrio_\d+\(`},
	{"aggregate given to a deferred call", `\n\tdefer agNote_\d+\(v, \d\)\n`},
	{"aggregate given to a deferred literal", `\n\tdefer func\(w AG_\d+, m int\) \{`},
	{"deferred value-receiver method", `\n\tdefer v\.note\(\d\)\n`},
	{"aggregate through an interface method", `\n\tMix\(v AG_\d+, d int\) AG_\d+\n`},
	{"aggregate through a package function value", `agSum_\d+\(agFn_\d+\(agMk_\d+\(`},
	{"aggregate through a table of function values", `agSum_\d+\(agFns_\d+\[i\]\(v, d ?\+ ?i\)\)`},
	{"aggregate through a method value", `\n\tmv := agImp_\d+\.Mix\n`},
	{"interface result handed to an interface method", `i\.Sum\(i\.Mix\(agMk_\d+\(`},
	{"index of 64 bits", `\n\tr := int\(v\.\w+\[u%\d+\]\) \+ int\(v\.\w+\[w\]\)\*3\n`},
	{"slice bounds of 64 bits", `\n\ts := v\.\w+\[w:\]\n`},
	{"make length of 64 bits", `\n\tm := make\(\[\]int64, w, \d+\)\n`},
	{"table of 64 bits indexed by a call", `\n\t\t\tt\[agRow_\d+\(i\)\]\[j\] \+= t\[i\]\[1 ?- ?j\] >> 3\n`},
	// Values of two to four words (genMultiWord) handed by value where the
	// expression computing them calls a function, and read where a call stands.
	{"struct of several words", `\ntype MW_\d+ struct \{\n\tf0 u?int32\n\tf1 u?int32\n`},
	{"struct of several words indexed by a call", `mwSum_\d+\(mwT_\d+\[mwAt_\d+\(\d\)\]\)`},
	{"struct of several words a call points to", `mwSum_\d+\(\*mwPtr_\d+\(\)\)`},
	{"field of several words a call points to", `mwSum_\d+\(mwHold_\d+\(\)\.m\)`},
	{"value receiver indexed by a call", `mwT_\d+\[mwAt_\d+\(\d\)\]\.mix\(\)`},
	{"value receiver a call points to", `mwPtr_\d+\(\)\.mix\(\)`},
	{"function value given a value indexed by a call", `\n\tfv := mwSum_\d+\n\tr = r ?\* ?31 ?\+ ?fv\(mwT_\d+\[mwAt_`},
	{"later argument indexed by a call", `mwPair_\d+\(k, mwT_\d+\[mwAt_\d+\(\d\)\]\)`},
	{"string indexed by a call", `mwStr_\d+\(mwS_\d+\[mwAt_\d+\(\d\)\]\)`},
	{"string comparison indexed by a call", `\n\tif mwS_\d+\[mwAt_\d+\(\d\)\] == mwS_\d+\[\d\] \{`},
	{"int64 indexed by a call", `mwWide_\d+\(mwW_\d+\[mwAt_\d+\(\d\)\]\)`},
	{"slice indexed by a call", `mwSl_\d+\(mwL_\d+\[mwAt_\d+\(\d\)\]\)`},
	{"interface value indexed by a call", `mwIf_\d+\(is\[mwAt_\d+\(\d\)\]\)`},
	{"send of a value indexed by a call", `\n\tc <- mwT_\d+\[mwAt_\d+\(\d\)\]\n`},
	{"length of a string received", `len\(<-cs\)`},
	{"capacity of a slice received", `cap\(<-cl\)`},
	{"member past the first word of a call's result", `int\(mwMk_\d+\(k\)\.f[1-3]\)`},
	{"several words checked", `\n\tif mwRun_\d+\(-?\d+\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: several words"\)`},
	// A 64-bit operation handed on before arguments of other types (genPairs).
	{"pairs: 64 bits and then a float", `\nfunc paFn_\d+_\d+\(a u?int64, b float(32|64)`},
	{"pairs: 64 bits and then a narrow integer", `\nfunc paFn_\d+_\d+\(a u?int64, b u?int(8|16)\b`},
	{"pairs: 64 bits and then 64 bits", `\nfunc paFn_\d+_\d+\(a u?int64, b u?int64`},
	{"pairs: an integer constant to a float after an operation", `(s\.sc|paVa_\d+)\([^,\n]*pa[VU]_\d+[^,\n]*, -?\d+[,)]`},
	{"pairs: a shift left by 32 handed on", `\(pa[VU]_\d+ ?<< ?32, `},
	{"pairs: a shift right by 32 handed on", `\(pa[VU]_\d+ ?>> ?32, `},
	{"pairs: a negative constant handed on", `(paFn_\d+_\d+|s\.sc|paVa_\d+)\(-\d+, `},
	{"pairs: a negation handed on", `\(-pa[VU]_\d+, `},
	{"pairs: through a function value", `\n\t\tfv := paFn_\d+_\d+\n\t\tr = r ?\* ?31 ?\+ ?fv\(`},
	{"pairs: through an interface", `\n\t\tvar s PaS_\d+ = &paR_\d+\n\t\tr = r ?\* ?31 ?\+ ?s\.sc\(`},
	{"pairs: variadic", `paVa_\d+\([^\n]+, [^\n]+, [^\n]+\)\n`},
	{"pairs: an operation received", `\((q ?[*+] ?\d+|-q|q ?<< ?32)\)\.mix\(`},
	{"pairs: a negative constant received", `PaQ_\d+\(-\d+\)\.mix\(`},
	{"pairs: a widening conversion handed on", `, u?int64\(pa[IW]_\d+\)[,)]`},
	{"pairs: a call's result handed on", `, paIdU?_\d+\(pa[VU]_\d+\)[,)]`},
	{"pairs: a function value's 64-bit result in operations", `\+ ?int\(paFw_\d+\(paV_\d+\) ?>> ?7\)`},
	{"pairs: one 64-bit argument to a package function value", `\+ ?paOneV_\d+\([^\n]+\)\n`},
	{"pairs: through a package function value", `\+ ?paFnV_\d+\([^\n]+\)\n`},
	{"pairs checked", `\n\tif paRun_\d+\(\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: pairs"\)`},
	// Switches of constant cases on every integer type (genSwitches).
	{"switches: on an 8-bit tag", `\n\tswitch sw[IU]8_\d+[ {]`},
	{"switches: on a 16-bit tag", `\n\tswitch sw[IU]16_\d+[ {]`},
	{"switches: on an unsigned 32-bit tag", `\n\tswitch sw(U32|Un)_\d+[ {]`},
	{"switches: on a defined type, by its constants", `\n\tcase SwK[a-h]_\d+[,:]`},
	{"switches: on a 64-bit tag", `\n\tswitch swW_\d+[ {]`},
	{"switches: a shifted or masked tag", `\n\tswitch sw\w+_\d+ (>>|&|\^) \d+ \{`},
	{"switches: a tag that calls", `\n\tswitch swNext_\d+\(\) \{`},
	{"switches: an unsigned case past the int32s", `\n\tcase [^\n]*\b(42949672\d\d|0x[89a-f][0-9a-f]{7})\b`},
	{"switches: the most negative int32", `\n\tcase [^\n]*-2147483648\b`},
	{"switches: default first", `\n\tswitch [^\n]+ \{\n\tdefault:`},
	{"switches: default between cases", `\n\tdefault:\n(\t\t[^\n]*\n)*\tcase `},
	{"switches: fallthrough", `\n\t\tfallthrough\n\tcase `},
	{"switches: fallthrough out of a default", `\n\tdefault:\n(\t\t[^\n]*\n)*\t\tfallthrough\n`},
	{"switches: a break in a clause", `\n\t\tif r&1 == 0 \{\n\t\t\tr \+= \d+\n\t\t\tbreak\n\t\t\}`},
	{"switches: a switch in a clause", `\n\tcase [^\n]+:\n\t\tswitch `},
	{"switches: in a loop, continued and broken out of", `\t\t\tcontinue\n\t\tcase \d+:\n\t\t\tr = r\*31 \+ \d+\n\t\t\tbreak swL_\d+\n`},
	{"switches checked", `\n\tif swRun_\d+\(\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: switches"\)`},
	// What Go defines and C does not (genWraps).
	{"wraps: a shift count of 64 unsigned bits", `(<<|>>)=? ?wrK(U64|C)[ab]_\d+`},
	{"wraps: a shift count of 8 bits", `(<<|>>)=? ?wrK[IU]8[ab]_\d+`},
	{"wraps: a shift count summed in its type", `(<<|>>)=? ?\(wrK\w+_\d+ ?\+ ?wrK\w+_\d+\)`},
	{"wraps: a constant shift count past the width", `wr\w+_\d+ ?(<<|>>) ?(33|63|64|65|100)\)`},
	{"wraps: an untyped constant shifted beside a value", `[-+|^*&/%] ?\(\d+ ?(<<|>>) ?(wrK|wrF)\w+`},
	{"wraps: a typed constant shifted by a variable", `\(\w+\(\(?-?\w+\)?\) ?(<<|>>) ?(wrK|wrF)\w+`},
	{"wraps: the most negative value over -1", `\((wr\w+a_\d+|WrMin\w+_\d+|wrF\w+_\d+\(wr\w+a_\d+\)) ?[/%] ?(wr\w+b_\d+|\(-1\)|wrF\w+_\d+\(\(-1\)\))\)`},
	{"wraps: a compound shift through a pointer", `\n\t\*wp\d+ (<<|>>)= `},
	{"wraps: a compound division through a pointer", `\n\t\*wp\d+ (/|%)= `},
	{"wraps: a compound shift of an element at a calling index", `\n\twrA\w+_\d+\[wrIdx_\d+\(\d\)\] (<<|>>)= `},
	{"wraps: an increment or decrement", `\n\t(wt\d+|wr[A-Z]\w*[a-d]_\d+|\*wp\d+|wrA\w+_\d+\[[^\]\n]+\])(\+\+|--)\n`},
	{"wraps: a value tested against itself after an operation", `\n\tif \((wr\w+_\d+(\([^()]*\))?) ?[-+*] ?[^\n]+\) [<>=!]+ (wr\w+_\d+(\([^()]*\))?) \{`},
	{"wraps: a truncating conversion", `\bu?int(8|16)\(wrF?(I64|U64|D|I32|U32|N|Un)[a-d_]`},
	{"wraps: an extending conversion", `\b(u?int64|WrD_1)\(wrF?(I8|U8|I16|U16)[a-d_]`},
	{"wraps: a negation", `[ (]-\(wr`},
	{"wraps: a complement", `[ (]\^\(wr`},
	// A float64's arithmetic at its whole precision (genF64).
	{"float64: a package variable stored", `\n\tfd[A-D]_1 = `},
	{"float64: an element stored", `\n\tfdP_1\[\d\] = `},
	{"float64: through a pointer", `\n\t\*fdq [-+*/]?= `},
	{"float64: a compound assignment", `\n\t(fd\w+|fa|fb|\*fdq|fdP_1\[\d\]) [-+*/]= `},
	{"float64: a call's result", `fdId_1\(`},
	{"float64: a subnormal constant", `\de-3[01]\d\b`},
	{"float64: a hexadecimal constant", `0x1\.[0-9a-f]+p[-+]\d+`},
	{"float64: an integer constant past 2^53", `\b900719925474099[2-9]\b`},
	{"float64: from an int64", `float64\(fdI_1\)`},
	{"float64: from an int32", `float64\(fdN_1\)`},
	{"float64: from a uint32", `float64\(fdU_1\)`},
	{"float64: through a float32", `\n\tfdF_1 = float32\(`},
	{"float64: to an int", `\n\tr = r\*31 \+ int\([^\n]*\b(fd[A-DPIFNU]_1|fa|fb|fdq)\b[^\n]*\)\n`},
	{"float64: to an int64", `\n\tr = r\*31 \+ int\(int32\(int64\([^\n]*\)>>20\)\)\n`},
	{"float64: to a uint32", `\n\tr = r\*31 \+ int\(uint32\([^\n]*\)>>1\)\n`},
	{"float64: a comparison", `\n\tif [^\n]*(fd|fa|fb)[^\n]* (<|<=|>|>=|==|!=) `},
	{"float64: a math function", `math\.(Floor|Ceil|Trunc)\(`},
	{"float64: a negation", `[ (]-(fd|fa|fb|\*fdq|\()`},
	{"float64: a zero's sign checked", `!\(1/\(\S+\) [<>] 0\)`},
	{"float64 checked", `\n\tif fdRun_1\(\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: float64"\)`},
	{"wraps checked", `\n\tif wrRun_\d+\(\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: wraps"\)`},
	// Structs embedded by value and by pointer, variadic calls and string(b) where
	// it stands (genEmbed).
	{"struct embedded by value and by pointer", `\ntype EMO_\d+ struct \{\n\tEMB_\d+\n\t\*EMP_\d+\n`},
	{"promoted field read", `\+ ?int\(emO_\d+\.a\)\n`},
	{"promoted field written", `\n\temO_\d+\.a \+= -?\d+\n`},
	{"promoted pointer method", `int\(emO_\d+\.bump\(-?\d+\)\)`},
	{"promoted value method", `int\(emO_\d+\.sumb\(\)\)`},
	{"method promoted through an embedded pointer", `int\(emO_\d+\.inc\(-?\d+\)\)`},
	{"field shadowing a promoted one", `int\(emO_\d+\.b\) ?\+ ?int\(emO_\d+\.EMB_\d+\.b\) ?\* ?3`},
	{"promoted methods through an interface", `\n\tvar ei EMI_\d+ = &emO_\d+\n`},
	{"promoted method taken as a value", `\n\tmv := emO_\d+\.bump\n`},
	{"outer method reading its shadowing field", `int\(emO_\d+\.own\(\)\)`},
	{"variadic call with no variadic argument", `emSum_\d+\(int32\(k\)\)`},
	{"variadic call with several", `emSum_\d+\(int32\(k\), -?\d+`},
	{"slice spread into a variadic call", `emSum_\d+\(-?\d+, emArr_\d+\[\d:\d\]\.\.\.\)`},
	{"struct with an embedded pointer copied", `\n\tcp := emO_\d+\n\tcp\.a \+= -?\d+\n\tcp\.inc\(`},
	{"promoted through a pointer to the outer value", `\n\tpo := &emO_\d+\n`},
	{"string(b) compared", `\n\tif string\(emBuf_\d+\[\d+:\d+\]\) == "`},
	{"string(b) ordered", ` < string\(emBuf_\d+\[\d+:\d+\]\) \{`},
	{"string(b) switched on", `\n\tswitch string\(emBuf_\d+\[\d+:\d+\]\) \{`},
	{"string(b) measured", `len\(string\(emBuf_\d+\[\d+:\]\)\)`},
	{"embedding checked", `\n\tif emRun_\d+\(-?\d+\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: embedding"\)`},
	// The strings, bytes and errors packages and a Builder (genLib).
	{"imports of the library packages", `// OctoSmith generated program\. Seed: \d+\nimport \(\n\t"bytes"\n\t"errors"\n\t"strings"\n\)`},
	{"strings.Index and bytes.Index", `strings\.Index\(lbS_\d+\[\d\], lbS_\d+\[\d\]\)\n\tr = r ?\* ?31 ?\+ ?bytes\.Index\(`},
	{"LastIndex", `bytes\.LastIndex\(lbB_\d+\[\d\], lbB_\d+\[\d\]\)`},
	{"Contains", `bytes\.Contains\(lbB_\d+\[\d\], lbB_\d+\[\d\]\)`},
	{"ContainsAny", `bytes\.ContainsAny\(lbB_\d+\[\d\], lbS_\d+\[\d\]\)`},
	{"ContainsRune of no rune", `strings\.ContainsRune\(lbS_\d+\[\d\], (-1|55296|1114112)\)`},
	{"Count", `strings\.Count\(lbS_\d+\[\d\], lbS_\d+\[\d\]\)`},
	{"Cut", `\n\tbb, ab, foundb := bytes\.Cut\(`},
	{"CutPrefix and CutSuffix", `\n\tcsb, oksb := bytes\.CutSuffix\(`},
	{"HasPrefix and HasSuffix", `bytes\.HasSuffix\(lbB_\d+\[\d\], lbB_\d+\[\d\]\)`},
	{"IndexAny", `strings\.IndexAny\(lbS_\d+\[\d\], lbS_\d+\[\d\]\)`},
	{"IndexByte and LastIndexByte", `bytes\.LastIndexByte\(lbB_\d+\[\d\], \d+\)`},
	{"IndexRune of the replacement rune", `strings\.IndexRune\(lbS_\d+\[\d\], 65533\)`},
	{"TrimPrefix and TrimSuffix", `lbHB_\d+\(bytes\.TrimSuffix\(`},
	{"TrimSpace", `lbH_\d+\(strings\.TrimSpace\(lbS_\d+\[\d\]\)\)`},
	{"Compare and Equal", `lbI_\d+\(bytes\.Equal\(lbB_\d+\[\d\], lbB_\d+\[\d\]\)\)`},
	{"errors.Is along a chain", `lbI_\d+\(errors\.Is\(&lbW2_\d+, &lbW\d_\d+\)\)`},
	{"errors.As", `\n\tif errors\.As\(e, &p\) \{\n\t\tr = r ?\* ?31 ?\+ ?p\.c\n`},
	{"errors.Unwrap", `errors\.Unwrap\(e\) == lbW\d_\d+\.e`},
	{"Builder written past its end", `\n\tn3, e3 := sb\.Write\(lbB_\d+\[\d\]\)\n`},
	{"library checked", `\n\tif lbRun_\d+\(\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: library"\)`},
	// A package of the program's own (genPkg), in the programs "ogo smith -o"
	// writes, whose files the corpus holds after the standard output's.
	{"a package of the program's own imported", `\nimport \(\n(\t"\w+"\n)*\t"pkx"\n`},
	{"an init function in the program's package", `\nfunc init\(\) \{\n\tCount \+= 100\n\}`},
	{"another package's struct embedded", `\ntype PKO_\d+ struct \{\n\tpkx\.Base\n`},
	{"another package's variable's field written", `\n\tpkx\.V\.A \+= -?\d+\n`},
	{"another package's method reading an unexported field", `int\(pkx\.V\.Hidden\(\)\)`},
	{"another package's variable's method value", `\n\tmv := pkx\.V\.Bump\n`},
	{"another package's method expressions", `int\(\(\*pkx\.Base\)\.Bump\(&pkx\.V, -?\d+\)\)`},
	{"another package's array written", `\n\tpkx\.Arr\[\d\] \+= -?\d+\n`},
	{"another package's slice", `len\(pkx\.Sl\) ?\+ ?int\(pkx\.Sl\[\d\]\)`},
	{"a value receiver on another package's element", `int\(pkx\.Tab\[\d\]\.Sum\(\)\)`},
	{"more than four words another package returns", `pkx\.MkWide\(int32\(k\)\)\.Total\(\)`},
	{"another package's constants", `int\(pkx\.KB\) ?\+ ?int\(pkx\.Scale\) ?\+ ?pkx\.Big ?>> ?35`},
	{"three results of another package", `\n\tq, rem, ok := pkx\.Div\(`},
	{"another package's variadic function, a slice spread", `pkx\.Add\(-?\d+, pkx\.Arr\[\d:\d\]\.\.\.\)`},
	{"another package's table of unexported functions", `int\(pkx\.Ops\[\d\]\(-?\d+, -?\d+\)\)`},
	{"another package's initialization read", `pkx\.Count ?\+ ?int\(pkx\.Seed\)`},
	{"promoted across the package boundary", `int\(pkO_\d+\.Bump\(-?\d+\)\)`},
	{"main's type through another package's interface", `pkx\.Measure\(&pkO_\d+, -?\d+\)`},
	{"package checked", `\n\tif pkRun_\d+\(-?\d+\) != -?\d+ \{\n\t\tpanic\("OctoSmith Checksum Failure: package"\)`},
	// A name C has spoken for, which newVarName draws now and then (cNames): the
	// emitter renames each in every position, and a position it missed was a C
	// error no generated program could show while every name was a counter's.
	{"a name C has spoken for", `\b(` + strings.Join(cNames, "|") + `)\b`},
	// And one Go's universe has, drawn the same way (universeNames): the program's
	// own where it is declared, which the emitter read by its spelling alone.
	{"a name the universe has", `\b(` + strings.Join(universeNames, "|") + `)\b`},
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
	// And the programs "ogo smith -o" writes, packages of their own included.
	for seed := 1; seed <= seeds; seed++ {
		files, err := Files(int64(seed))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for _, name := range []string{"main.ogo", "pkx/pkx.ogo"} {
			b.Write(files[name])
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
