// A 64-bit unary minus whose result meets an addition or subtraction in the same
// expression is miscompiled on the P2 with the small-function inliner on. Valid C;
// gcc computes every line:
//
//	                        gcc                 flexcc -2        flexcc -2 -Ono-inline-small
//	-x                      -5                  -5               -5
//	-x - 3                  -8                  4294967288       -8       <- high word never negated
//	-x + 3                  -2                  4294967294       -2
//	-(x + 3)                -8                  -8               -8
//	0 - x - 3               -8                  -8               -8
//	t = -x; t - 3           -8                  4294967288       -8       <- the copy is folded back
//
// for an int64_t x of 5 that the compiler cannot see through (it comes out of a
// call it cannot inline). The same with x = 2^40 gives -1095216660483 for
// -1099511627779: off by 2^32 either way, the sign of the high word. Unsigned
// values are not affected, nor is a negation alone, a negation of a sum, a
// negation feeding a multiply, a shift or a compare, or the complement spelling
// `-1 ^ x` beside an addition.
//
// Measured on a P2-EDGE 2026-08-30 against spin2cpp 2bd01c4c: right at -O0 and
// with -Ono-inline-small, wrong at the default level. The negation of a 64-bit
// value is an inlined sequence of the runtime's, and what follows it in the same
// expression is where it goes wrong -- which is also why binding the negation to
// a variable first does not help: the optimizer folds the copy away.
//
// It reaches ordinary OctoGo as `-x - 1` on an int64, found by a 64-bit
// arithmetic probe diffed against Go: the dividend `-big - 3` of a division was
// wrong for every divisor, and the first divisor tried, 1, made it look like a
// division fault. WORKED AROUND in emitExprNode: a 64-bit unary minus is emitted
// as `(0 - x)`, which is right in every context measured (r2/r4 of the session's
// scratch tables: an initializer, an operand, an argument, a return, a compare, a
// divisor, a shift, nested, on the most negative value). Unreported upstream as of
// this writing.
//
// A measuring note that cost two hours: never build an operand of a reproducer as
// `(uint64_t)f()` -- the cast of a 64-bit call result is the battery's oldest
// fault, and it made unsigned subtraction look broken in three tables running.
//
// To check, build for the P2 with no -O flag and read the second number.

#include <stdio.h>
#include <stdint.h>

__attribute__((noinline)) int64_t id64(int64_t v) { return v; }

int main(void) {
	int64_t x = id64(5);
	int64_t a = -x;
	int64_t b = -x - 3;
	int64_t c = -(x + 3);
	int64_t d = 0 - x - 3;
	printf("%lld\n", a);
	printf("%lld\n", b);
	printf("%lld\n", c);
	printf("%lld\n", d);
	return 0;
}
