package octogo

import (
	"testing"
	"testing/fstest"
)

// TestCheckerRobustness feeds parseable programs that once crashed the type
// checker -- degenerate constant folding (division by zero, an out-of-range or
// non-integer shift, and a binary or unary operator not defined for its operands,
// e.g. subtracting strings or complementing a float) and a function type -- and
// requires that Build handles each without panicking.
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
	}
	buildEach(t, progs)
}

// TestControlFlowRobustness exercises the statement-level analyses -- terminating
// statement / missing return, unreachable code, the unused-variable report, the
// multiple-defaults report, and "go"-statement call checking -- over degenerate
// and deeply nested bodies, requiring each to be analysed without panicking. These
// walk the flat statement AST directly (locating blocks, clause bodies, clause
// heads, the callee and CallSuffix, the closing brace, and every identifier), so
// an unexpected shape must yield a diagnostic or nothing, never a crash.
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
