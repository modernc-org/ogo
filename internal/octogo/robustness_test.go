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
