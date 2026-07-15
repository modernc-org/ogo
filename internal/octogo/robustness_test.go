package octogo

import (
	"testing"
	"testing/fstest"
)

// TestCheckerRobustness feeds parseable programs that once crashed the type
// checker -- degenerate constant folding (division by zero, an out-of-range or
// non-integer shift, and a binary or unary operator not defined for its operands,
// e.g. subtracting strings or complementing a float), a function type, and typed
// constant overflow over degenerate, arbitrary-precision and non-integer values --
// and requires that Build handles each without panicking.
func TestCheckerRobustness(t *testing.T) {
	progs := []string{
		"const x = 1 / 0\nfunc run() {}\n",
		"const x = 5 % 0\nfunc run() {}\n",
		"const x = 1 << -1\nfunc run() {}\n",
		"const x = \"a\" - \"b\"\nfunc run() {}\n",
		"const x = (1 + 2) / (3 - 3)\nfunc run() {}\n",
		"func run() { var a [1 / 0]int; _ = a }\n",
		// Unary operators not defined for the operand kind, which once reached an
		// unguarded constant.UnaryOp and panicked.
		"const x = -\"a\"\nfunc run() {}\n",
		"const x = ^1.5\nfunc run() {}\n",
		"const x = !1\nfunc run() {}\n",
		"func run() { var a [^1.5]int; _ = a }\n",
		// A shift whose shifted value is not an integer, which once reached an
		// unguarded constant.Shift and panicked.
		"const x = \"s\" << 1\nfunc run() {}\n",
		"func g(cb func()) {}\nfunc run() {}\n",
		"type H func(h int)\nfunc run() { var f H; _ = f }\n",
		"type Node struct{ next func() Node }\nfunc run() { var n Node; _ = n }\n",
		// A typed integer constant is range-checked against its declared type. The
		// folded value may overflow, even by an arbitrary-precision amount, or be
		// degenerate (division by zero, an unknown value) or non-integer (a string
		// or a float) -- the last three simply skip the range check -- at package
		// or function scope, none panicking.
		"const x int8 = 200\nfunc run() {}\n",
		"const x int8 = 1 << 100\nfunc run() {}\n",
		"const x uint8 = 1 / 0\nfunc run() {}\n",
		"const x int8 = \"a\"\nfunc run() {}\n",
		"const x uint16 = 1.5\nfunc run() {}\n",
		"func run() { const x uint8 = 300; _ = x }\n",
	}
	buildEach(t, progs)
}

// TestControlFlowRobustness exercises the statement-level analyses -- terminating
// statement / missing return, unreachable code, the unused-variable report, the
// multiple-defaults report, "go"-statement call checking, duplicate-case detection
// with case-expression folding, the assignment count mismatch, and constant-
// overflow folding at a var initializer, an assignment and a call argument -- over
// degenerate and deeply nested bodies, requiring each to be analysed without
// panicking. These walk the flat statement AST directly (locating blocks, clause
// bodies, clause heads and their case expressions, assignment targets and right-
// hand sides, the callee and CallSuffix, the closing brace, and every identifier),
// so an unexpected shape must yield a diagnostic or nothing, never a crash.
func TestControlFlowRobustness(t *testing.T) {
	progs := []string{
		// Terminating statement / missing return.
		"func f() int {}\n",
		"func f() int { {} }\n",
		"func f() int { { return 1 } }\n",
		"func f() int { if a {} }\n",
		"func f() int { if a { return 1 } else { return 2 } }\n",
		"func f() int { for {} }\n",
		"func f() int { for a {} }\n",
		"func f() int { switch {} }\n",
		"func f() int { switch a { case 1: return 1 } }\n",
		"func f() int { switch a { case 1: return 1; default: return 2 } }\n",
		"func f() int { select {} }\n",
		"func f() int { panic(0) }\n",
		"func f() int { panic() }\n",
		"func f() (a, b int) {}\n",
		"func f() int {\n\tif a {\n\t\tif b { return 1 } else { return 2 }\n\t} else {\n\t\treturn 3\n\t}\n}\n",
		"type T struct{ v int }\nfunc (t T) m() int {}\n",
		"func f() int { go g() }\nfunc g() {}\n",
		"func f() int { <-ch }\nvar ch chan int\n",
		// Unreachable code.
		"func f() { return\n\treturn }\n",
		"func f() { for {}\n\tx := 1\n\t_ = x }\n",
		"func f() { { return }\n\tx := 1\n\t_ = x }\n",
		"func f(a int) { switch a { case 1: return\n\t\tx := 1\n\t\t_ = x } }\n",
		// Unused variable.
		"func f() { var x int }\n",
		"func f() { x := 1 }\n",
		"func f() { var x, y int\n\t_ = x }\n",
		"func f() { var x int = x }\n",
		"func f() { { var x int } }\n",
		"func f() {\n\tswitch v := 1 {\n\tcase 1:\n\t\tvar x int\n\t}\n}\n",
		"func f() {\n\tx := 1\n\tx = 2\n}\n",
		// Multiple defaults in a switch or select, including a select comm clause
		// (a CommHead default, distinct from a switch's CaseHead) and nesting.
		"func f() { switch { default:\n\tdefault: } }\n",
		"func f(a int) { switch a { case 1:\n\tdefault:\n\tdefault: } }\n",
		"func f() { select { default:\n\tdefault: } }\n",
		"func f() { select { case <-ch:\n\tdefault:\n\tdefault: } }\nvar ch chan int\n",
		"func f() { switch { default: switch { default:\n\t\tdefault: } } }\n",
		// "go" statement calls: an undefined or non-function callee, an undefined
		// argument, a package/method-style call, and a parenthesized (non-plain)
		// callee, each resolved and checked without crashing.
		"func f() { go nope() }\n",
		"func f() { go nope(bad) }\n",
		"func f() { go p.q() }\n",
		"func f(x int) { go x() }\n",
		"func f() { go (g)() }\nfunc g() {}\n",
		// Duplicate switch cases and case-expression folding: value-based dedup over
		// case constants, a non-constant case folded silently (a variable is legal),
		// and an undefined name, a division by zero and an undefined operator each
		// folded and reported -- all without crashing.
		"func f() {\n\tvar x int = 0\n\tswitch x {\n\tcase 1:\n\tcase 1:\n\t}\n}\n",
		"func f() {\n\tvar x int = 0\n\tswitch x {\n\tcase 1, 1:\n\t}\n}\n",
		"func f(y int) {\n\tvar x int = 0\n\tswitch x {\n\tcase y:\n\tcase y:\n\t}\n}\n",
		"func f() {\n\tvar x int = 0\n\tswitch x {\n\tcase nope:\n\t}\n}\n",
		"func f() {\n\tvar x int = 0\n\tswitch x {\n\tcase 1 / 0:\n\t}\n}\n",
		"func f() {\n\tvar s string = \"\"\n\tswitch s {\n\tcase \"a\" - \"b\":\n\t}\n}\n",
		"func f() { switch { case true: case true: } }\n",
		// Assignment count mismatch: the single right-hand side's value count against
		// the left-hand targets, over a literal, an undefined and a non-function
		// call, a receive, and a nested operator expression.
		"func f() {\n\tvar a, b int\n\ta, b = 1\n\t_ = a\n\t_ = b\n}\n",
		"func f() {\n\ta, b := nope()\n\t_ = a\n\t_ = b\n}\n",
		"func f(g int) {\n\tvar a, b int\n\ta, b = g()\n\t_ = a\n\t_ = b\n}\n",
		"func f() {\n\tvar ch chan int\n\tvar a, b int\n\ta, b = <-ch\n\t_ = a\n\t_ = b\n\t_ = ch\n}\n",
		"func f() {\n\tvar a, b, c, d, e int\n\ta, b = (c + d) * e\n\t_ = a\n\t_ = b\n}\n",
		// Constant-overflow folding at a var initializer, an assignment and a call
		// argument: the value is folded only to range-check it, over an overflowing
		// literal, a non-constant operand (a parameter, a call, a receive), an
		// undefined name, a division by zero and a conversion -- none panicking.
		"func f() {\n\tvar x uint8 = 300\n\t_ = x\n}\n",
		"func f() {\n\tvar x uint8\n\tx = 300\n\t_ = x\n}\n",
		"func g(v uint8) {}\nfunc f() { g(300) }\n",
		"func f(y int) {\n\tvar x uint8 = y\n\t_ = x\n}\n",
		"func f() {\n\tvar ch chan int\n\tvar x uint8 = <-ch\n\t_ = x\n\t_ = ch\n}\n",
		"func f() {\n\tvar x uint8 = nope\n\t_ = x\n}\n",
		"func f() {\n\tvar x uint8 = 1 / 0\n\t_ = x\n}\n",
		"func f(y int) {\n\tvar x uint8 = uint8(y)\n\t_ = x\n}\n",
	}
	buildEach(t, progs)
}

// TestPointerArrayRobustness exercises the pointer, array and element checks --
// dereference and index assignment targets ("*p = e", "a[i] = e", including "cannot
// indirect"/"cannot index", an undefined deref base, and multi-star, nested and
// mixed shapes), dereference and index reads flowing through conditions, arguments,
// operators and ":=" inference, the constant-overflow range check at a struct-field,
// dereference and element target, and the write-only local-variable report -- over
// degenerate bodies, requiring each to be analysed without panicking. These walk the
// flat AST for assignment heads and postfixes, factor suffixes and unary operators,
// so an unexpected shape must yield a diagnostic or nothing, never a crash.
func TestPointerArrayRobustness(t *testing.T) {
	bodies := []string{
		// Dereference and index assignment targets.
		"var a [3]int; a[0] = 1; _ = a",
		"var a [3]int; a[0] = true; _ = a",
		"var n int; n[0] = 1; _ = n",
		"var v int; var p *int = &v; *p = 1",
		"var v int; var p *int = &v; *p = true",
		"var x int; *x = 1; _ = x",
		"*q = 1",
		"var a [2][3]int; a[0][1] = 4",
		"var p **int; **p = 3",
		"var a [3]int; a[i] = 1; _ = a",
		"var s []int; s[0] = 1",
		"var m map[int]int; m[0] = 1",
		"a[0] := 1",
		"*p += 1",
		// Dereference and index reads.
		"var a [3]int; if a[0] {\n}\n_ = a",
		"var p *int; if *p {\n}",
		"var a [3]bool; use(a[0]); _ = a",
		"var p *bool; use(*p)",
		"var a [3]int; x := a[0]; _ = x; _ = a",
		"var p *int; x := *p; _ = x",
		"var a [3]int; _ = a[0] + a[1]",
		"var p *int; _ = *p + 1",
		"var a [3]int; _ = *a[0]",
		"var p **int; _ = **p",
		"var a [3]int; var p *int; _ = a[0] < *p",
		"var a [3]int; _ = a[a[0]]",
		// Constant overflow at a struct-field, dereference and element target.
		"var a [3]uint8; a[0] = 300; _ = a",
		"var v uint8; var p *uint8 = &v; *p = 300",
		"var a [2]int16; a[0] = 40000; _ = a",
		// Struct field targets and reads.
		"var s pt; s.x = 1; _ = s",
		"var s pt; s.x = true; _ = s",
		"var s pt; if s.x {\n}\n_ = s",
		"var p *pt; p.x = 300; _ = p",
		"var s pt; s.x = s.x + 1; _ = s",
		// Write-only local variables.
		"x := 1; x = 2",
		"var x int; x = 5",
		"var x, y int; x = 1; y = 2",
		"x := 1; x = x + 1",
	}
	progs := make([]string, len(bodies))
	for i, b := range bodies {
		progs[i] = "type pt struct{ x int }\nfunc use(b bool) {}\nfunc g() {\n\t" + b + "\n}\n"
	}
	buildEach(t, progs)
}

// TestBlankIdentifierRobustness exercises the blank-identifier-as-value check --
// reading "_" as an operand, a call argument, an initializer, a condition, a return
// value, a call or "go" callee, or the base of a "_.f"/"_[i]"/"*_" target, all of
// which are illegal ("cannot use _ as value or type"), against its legal positions:
// a whole "="/":=" target, a variable, parameter or field name -- over degenerate,
// deeply nested and multiply-suffixed shapes. These walk the flat AST for factors,
// call callees, unary operators and assignment target bases, so an unexpected shape
// (a blank with several suffixes, a multi-star deref, a blank on both sides, a blank
// nested in an operator or argument tree) must yield a diagnostic or nothing, never
// a crash.
func TestBlankIdentifierRobustness(t *testing.T) {
	progs := []string{
		// Legal positions: a whole target, a discard, a declaration name.
		"func f() { _ = 1 }\n",
		"func f() { var _ int }\n",
		"func f(_ int) {}\n",
		"type T struct{ _ int }\nfunc f() { var t T; _ = t }\n",
		"var _ = 1\n",
		// Blank read as an operand, nested arbitrarily deep in an expression tree.
		"func f() { x := _; _ = x }\n",
		"func f() { x := _ + 1; _ = x }\n",
		"func f() { x := (_); _ = x }\n",
		"func f() { x := _ + _ * _; _ = x }\n",
		"func f() { x := -_; _ = x }\n",
		"func f() { x := ^_; _ = x }\n",
		"func f() { x := !_; _ = x }\n",
		"func f() { x := *_; _ = x }\n",
		"func f() { x := &_; _ = x }\n",
		"func f() { x := <-_; _ = x }\n",
		"func f() { x := ((_ + 1) * (_ - 1)); _ = x }\n",
		// Blank read through a condition and a return value.
		"func f() { if _ {\n} }\n",
		"func f() { for _ {\n} }\n",
		"func f() int { return _ }\n",
		"func f() int { return _ + 1 }\n",
		// Blank read as a call argument, including nested and multiple.
		"func g(v int) {}\nfunc f() { g(_) }\n",
		"func g(v int) {}\nfunc f() { g(_ + 1) }\n",
		"func g(a, b int) {}\nfunc f() { g(_, _) }\n",
		"func g(v int) int { return 0 }\nfunc f() { x := g(g(_)); _ = x }\n",
		// Blank as a call or "go" callee, bare and suffixed.
		"func f() { _() }\n",
		"func f() { go _() }\n",
		"func f() { _(1, 2) }\n",
		"func f() { _.m() }\n",
		"func f() { _[0]() }\n",
		// Blank as the base of a target, bare, multi-suffixed and multi-star.
		"func f() { _.a = 1 }\n",
		"func f() { _.a.b = 1 }\n",
		"func f() { _[0] = 1 }\n",
		"func f() { _[0][1] = 1 }\n",
		"func f() { *_ = 1 }\n",
		"func f() { **_ = 1 }\n",
		"func f() { _.a[0] = 1 }\n",
		// Blank on both sides and in a switch guard (guards are not name-checked, so
		// this is silent -- it must still not crash).
		"func f() { _ = _ }\n",
		"func f() { switch _ {\ncase 1:\n} }\n",
		"func f() { _ = *_ }\n",
	}
	buildEach(t, progs)
}

// TestSwitchGuardRobustness exercises name- and type-checking of a switch guard's
// value expression -- the operand of a plain "switch expr" guard or the right-hand
// side of a "switch v := expr" guard -- reporting an undefined name, a blank read or
// an ill-typed operator there, while the ":=" left-hand side declares the guard
// variable rather than reading it. These run over degenerate and nested guard shapes
// (a bare name, a parenthesized, unary, operator, call, index, selector or receive
// guard, an undefined or blank guard, a nested switch, a guard var bound from an
// undefined or blank value), requiring each to be analysed without panicking.
func TestSwitchGuardRobustness(t *testing.T) {
	progs := []string{
		// Plain guard: undefined, blank, ill-typed, and legal.
		"func f() { switch nope {\ncase 1:\n} }\n",
		"func f() { switch _ {\ncase 1:\n} }\n",
		"func f(x int) { switch x {\ncase 1:\n} }\n",
		"func f() { switch {\ncase true:\n} }\n",
		"func f() { var p *int; switch p + 1 {\ncase 1:\n}\n_ = p }\n",
		"func f() { switch \"a\" - \"b\" {\ncase 1:\n} }\n",
		// Nested and shaped guards.
		"func f() { switch (nope) {\ncase 1:\n} }\n",
		"func f(x int) { switch -x {\ncase 1:\n} }\n",
		"func f() { switch nope + _ {\ncase 1:\n} }\n",
		"func f() { switch a + b + c {\ncase 1:\n} }\n",
		"func g() int { return 0 }\nfunc f() { switch g() {\ncase 1:\n} }\n",
		"func f() { switch nope() {\ncase 1:\n} }\n",
		"func f() { var a [3]int; switch a[0] {\ncase 1:\n}\n_ = a }\n",
		"type pt struct{ x int }\nfunc f() { var s pt; switch s.x {\ncase 1:\n}\n_ = s }\n",
		"func f() { var ch chan int; switch <-ch {\ncase 1:\n}\n_ = ch }\n",
		"func f(x int) { switch x {\ncase 1:\n\tswitch nope {\ncase 2:\n\t}\n} }\n",
		// "v := expr" guard: the right-hand side is checked, the left-hand side declares.
		"func f() { switch v := nope {\ncase 1:\n\t_ = v\n} }\n",
		"func f() { switch v := _ {\ncase 1:\n\t_ = v\n} }\n",
		"func f() { switch v := 5 {\ncase 1:\n\t_ = v\n} }\n",
		"func f() { switch _ := 5 {\ncase 1:\n} }\n",
	}
	buildEach(t, progs)
}

// TestSelectRecvRobustness exercises the select comm-clause analysis -- declaring
// the variable a "case v := <-ch" short receive introduces, scoped to its clause,
// alongside the bare-receive, assign-receive, send and default forms -- over
// degenerate and nested comm shapes. The recv-variable extractor walks the flat AST
// for a CommHead, its CommOp, an AssignHead and a PostfixComm carrying a ":=", so an
// unexpected shape (a multi-star, selector, index or parenthesized ":=" target, a
// blank target, an undefined channel, an empty or nested select) must declare
// nothing or a single name, never crash.
func TestSelectRecvRobustness(t *testing.T) {
	progs := []string{
		// The four pre-existing forms and default.
		"func f() { var ch chan int; select {\ncase <-ch:\n}\n_ = ch }\n",
		"func f() { var ch chan int; var v int; select {\ncase v = <-ch:\n}\n_ = ch; _ = v }\n",
		"func f() { var ch chan int; select {\ncase ch <- 1:\n}\n_ = ch }\n",
		"func f() { select {\ndefault:\n} }\n",
		"func f() { select {\n} }\n",
		// The short-receive declaration, used and unused, blank, and scoped.
		"func f() { var ch chan int; select {\ncase v := <-ch:\n\t_ = v\n}\n_ = ch }\n",
		"func f() { var ch chan int; select {\ncase v := <-ch:\n}\n_ = ch }\n",
		"func f() { var ch chan int; select {\ncase _ := <-ch:\n}\n_ = ch }\n",
		"func f() { var a chan int; var b chan int; select {\ncase x := <-a:\n\t_ = x\ncase y := <-b:\n\t_ = y\n}\n_ = a; _ = b }\n",
		// Degenerate ":=" targets: multi-star, selector, index, parenthesized -- none
		// is a plain short declaration, so none introduces a (shadowing) variable.
		"func f() { var ch chan int; select {\ncase *p := <-ch:\n}\n_ = ch }\n",
		"func f() { var ch chan int; select {\ncase **p := <-ch:\n}\n_ = ch }\n",
		"func f() { var ch chan int; select {\ncase v.f := <-ch:\n}\n_ = ch }\n",
		"func f() { var ch chan int; select {\ncase v[0] := <-ch:\n}\n_ = ch }\n",
		"func f() { var ch chan int; select {\ncase (v) := <-ch:\n}\n_ = ch }\n",
		// An undefined channel in the receive is not modelled, so it is silent, but
		// must not crash. A nested select and a receive of a complex expression too.
		"func f() { select {\ncase v := <-nope:\n\t_ = v\n} }\n",
		"func f() { var ch chan int; var d chan int; select {\ncase v := <-ch:\n\tselect {\ncase w := <-d:\n\t\t_ = w\n\t}\n\t_ = v\n}\n_ = ch; _ = d }\n",
		"func f() { var chs [3]chan int; select {\ncase v := <-chs[0]:\n\t_ = v\n}\n_ = chs }\n",
	}
	buildEach(t, progs)
}

// TestChannelRobustness exercises send and receive checking on channels declared at
// package, local and parameter scope -- the channel shape and element type are now
// recorded on every variable declaration, so a send to or receive from a non-channel,
// a value/target type mismatch, an unmodelled element (a struct or nested channel),
// an undefined element type, and degenerate operand shapes must each be analysed
// without panicking.
func TestChannelRobustness(t *testing.T) {
	progs := []string{
		// Valid send/receive at each scope.
		"var ch chan int\nfunc f() { ch <- 1 }\n",
		"func f() { var ch chan int; ch <- 1; _ = ch }\n",
		"func f(ch chan int) { ch <- 1 }\n",
		"func f() { var ch chan int; var i int; i = <-ch; _ = ch; _ = i }\n",
		"func f(ch chan bool) { var b bool; b = <-ch; _ = b }\n",
		// Element-type mismatches on local and parameter channels.
		"func f() { var ch chan int; ch <- true; _ = ch }\n",
		"func f(ch chan bool) { ch <- 1 }\n",
		"func f() { var ch chan int; var b bool; b = <-ch; _ = ch; _ = b }\n",
		// Non-channel operands still rejected.
		"func f() { var n int; n <- 1; _ = n }\n",
		"func f() { var n int; x := <-n; _ = x; _ = n }\n",
		// Unmodelled element types: a struct, a nested channel -- recognized as a
		// channel (no false non-channel error) but the value type is not checked.
		"type pt struct{ x int }\nfunc f() { var ch chan pt; var p pt; ch <- p; _ = ch; _ = p }\n",
		"func f() { var ch chan chan int; var d chan int; ch <- d; _ = ch; _ = d }\n",
		"func f(ch chan chan bool) { x := <-ch; _ = x }\n",
		// An undefined channel element type is reported, not crashed on.
		"func f() { var ch chan Nope; _ = ch }\n",
		"func f(ch chan Nope) { _ = ch }\n",
		// Degenerate operands: undefined channel/value, a complex sent expression, a
		// send of a receive, a receive into a field/index/deref target.
		"func f() { nope <- 1 }\n",
		"func f() { var ch chan int; ch <- undef; _ = ch }\n",
		"func f() { var ch chan int; var a, b int; ch <- a + b; _ = ch; _ = a; _ = b }\n",
		"func f() { var a chan int; var b chan int; a <- <-b; _ = a; _ = b }\n",
		"type pt struct{ x int }\nfunc f() { var ch chan int; var s pt; s.x = <-ch; _ = ch; _ = s }\n",
		"func f() { var ch chan int; var a [3]int; a[0] = <-ch; _ = ch; _ = a }\n",
		"func f() { var ch chan int; var v int; var p *int = &v; *p = <-ch; _ = ch }\n",
	}
	buildEach(t, progs)
}

// TestIndexNameRobustness exercises name-checking of index expressions -- the "i" in
// "a[i]" -- wherever an index appears: a read, a call argument, a condition, a nested
// or chained index, an assignment target, an LhsItem, and a "go" callee. Each index
// is resolved through checkNames, so an undefined name, a blank read, a defined
// variable, a literal, and a compound or itself-indexed index expression must each be
// analysed without panicking.
func TestIndexNameRobustness(t *testing.T) {
	progs := []string{
		// Read positions.
		"func f() { var a [3]int; _ = a[nope]; _ = a }\n",
		"func f() { var a [3]int; var i int; _ = a[i]; _ = a; _ = i }\n",
		"func f() { var a [3]int; _ = a[0]; _ = a }\n",
		"func g(v int) {}\nfunc f() { var a [3]int; g(a[nope]); _ = a }\n",
		"func f() { var a [3]int; if a[nope] == 0 {\n}\n_ = a }\n",
		"func f() { var a [3]int; _ = a[_]; _ = a }\n",
		// Nested, chained, and compound indexes.
		"func f() { var a [3]int; var b [3]int; _ = a[b[nope]]; _ = a; _ = b }\n",
		"func f() { var a [3][3]int; _ = a[nope][bad]; _ = a }\n",
		"func f() { var a [3]int; var b, c int; _ = a[b+c]; _ = a; _ = b; _ = c }\n",
		"func f() { var a [3]int; _ = a[nope]() ; _ = a }\n",
		"func f() { var a [3]int; _ = a[nope].m(); _ = a }\n",
		// Assignment targets and LhsItems.
		"func f() { var a [3]int; a[nope] = 1; _ = a }\n",
		"func f() { var a [3]int; var i int; a[i] = 1; _ = a; _ = i }\n",
		"func f() { var a [2][2]int; a[nope][bad] = 1; _ = a }\n",
		"func two() (a, b int) { return 1, 2 }\nfunc f() { var x int; var a [3]int; x, a[nope] = two(); _ = x; _ = a }\n",
		// An indexed send/receive target and an undefined indexed base.
		"func f() { var a [3]int; a[nope] = <-ch; _ = a }\nvar ch chan int\n",
		"func f() { nope[bad] = 1 }\n",
		"func f() { _ = nope[bad] }\n",
		// "go" callee index.
		"func f() { var a [3]int; go a[nope](); _ = a }\n",
		"func f() { var a [3]int; var i int; go a[i].m(); _ = a; _ = i }\n",
	}
	buildEach(t, progs)
}

// TestChannelOperandRobustness exercises name- and channel-checking of the channel
// operand of a send statement "ch <- v" and a bare receive statement "<-ch" -- an
// undefined, blank, non-channel, indexed, selector, pointer or compound operand, at
// package, local and parameter scope -- requiring each to be analysed without
// panicking.
func TestChannelOperandRobustness(t *testing.T) {
	progs := []string{
		// Send operand: valid, undefined, blank, non-channel.
		"func f() { var ch chan int; ch <- 1; _ = ch }\n",
		"var ch chan int\nfunc f() { ch <- 1 }\n",
		"func f(ch chan int) { ch <- 1 }\n",
		"func f() { nope <- 1 }\n",
		"func f() { _ <- 1 }\n",
		"func f() { var n int; n <- 1; _ = n }\n",
		// Send with a suffixed or pointer channel operand.
		"func f() { var chs [3]chan int; var i int; chs[i] <- 1; _ = chs; _ = i }\n",
		"func f() { var chs [3]chan int; chs[nope] <- 1; _ = chs }\n",
		"func f() { var p pt; p.ch <- 1; _ = p }\ntype pt struct{ ch int }\n",
		"func f() { var p *int; *p <- 1; _ = p }\n",
		// Bare receive operand: valid, undefined, blank, non-channel, compound.
		"func f() { var ch chan int; <-ch; _ = ch }\n",
		"var ch chan int\nfunc f() { <-ch }\n",
		"func f(ch chan int) { <-ch }\n",
		"func f() { <-nope }\n",
		"func f() { <-_ }\n",
		"func f() { var n int; <-n; _ = n }\n",
		"func f() { var ch chan int; <-ch + 1; _ = ch }\n",
		"func f() { var a, b chan int; <-a; <-b; _ = a; _ = b }\n",
		"func nope() int { return 0 }\nfunc f() { <-nope() }\n",
		"func f() { var ch chan int; <-<-ch; _ = ch }\n",
	}
	buildEach(t, progs)
}

// buildEach runs Build on each program under a recover, failing on any panic.
func buildEach(t *testing.T, progs []string) {
	t.Helper()
	for _, src := range progs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on %q: %v", src, r)
				}
			}()
			fsys := fstest.MapFS{"x.ogo": &fstest.MapFile{Data: []byte(src)}}
			Build(-1, []string{"x.ogo"}, fsys)
		}()
	}
}
