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
// THE CAUSE, found 2026-09-14, is the add/sub merge of doc/add-immediate-carry.c,
// reading Z where that file's lines read C. The inlined negation is
//
//	not lo; add lo, #1 wz; not hi; mov t, #0; if_e neg t, #1; sub hi, t
//
// -- the carry into the high word decided by the Z out of `add lo, #1` -- and
// OptimizeAddSub folds that `add #1` into the `- 3` after it, one `sub lo, #2 wc`,
// deleting the instruction whose Z the `if_e` reads. -Ono-regs cures every line
// too, and so does the two-condition fix given in that file.
//
// FIXED 2026-09-15 by that fix, carried as internal/optimize_ir.c.diff in the
// regeneration of that day (spin2cpp 3840014f): every line prints gcc's value.
// Upstream's own fix, in spin2cpp v7.7.3, prints them too (a native build without
// the diff, on a P2-EDGE 2026-09-21), and the backend is regenerated at it.
// The `(0 - x)` spelling below stays -- it is what a negation is, costs the same,
// and is right under either backend.
//
// It reaches ordinary OctoGo as `-x - 1` on an int64, found by a 64-bit
// arithmetic probe diffed against Go: the dividend `-big - 3` of a division was
// wrong for every divisor, and the first divisor tried, 1, made it look like a
// division fault. WORKED AROUND in emitExprNode: a 64-bit unary minus is emitted
// as `(0 - x)`, which is right in every context measured (r2/r4 of the session's
// scratch tables: an initializer, an operand, an argument, a return, a compare, a
// divisor, a shift, nested, on the most negative value). Reported upstream on
// 2026-09-15 as line G of flexprop issue 109.
//
// A measuring note that cost two hours: never build an operand of a reproducer as
// `(uint64_t)f()` -- the cast of a 64-bit call result is the battery's oldest
// fault, and it made unsigned subtraction look broken in three tables running.
// And a second: until 2026-09-14 this file computed the shapes in main, where at
// 2bd01c4c the merge does not fire, so it printed every line right while the fault
// was live. Whether it fires depends on where the register pair lives; each shape
// now has a function of its own.
//
// To check, build for the P2 with -2 and compare each line with gcc's.

#include <stdio.h>
#include <stdint.h>

__attribute__((noinline)) int64_t id64(int64_t v) { return v; }

__attribute__((noinline)) int64_t neg(int64_t x) { return -x; }
__attribute__((noinline)) int64_t negsub(int64_t x) { return -x - 3; }
__attribute__((noinline)) int64_t negadd(int64_t x) { return -x + 3; }
__attribute__((noinline)) int64_t negsum(int64_t x) { return -(x + 3); }
__attribute__((noinline)) int64_t zerosub(int64_t x) { return 0 - x - 3; }
__attribute__((noinline)) int64_t negcopy(int64_t x) { int64_t t = -x; return t - 3; }

int main(void) {
	int64_t x = id64(5);
	int64_t big = id64(1099511627776LL);
	printf("-x              %lld\n", neg(x));
	printf("-x - 3          %lld\n", negsub(x));
	printf("-x + 3          %lld\n", negadd(x));
	printf("-(x + 3)        %lld\n", negsum(x));
	printf("0 - x - 3       %lld\n", zerosub(x));
	printf("t = -x; t - 3   %lld\n", negcopy(x));
	printf("-big - 3        %lld\n", negsub(big));
	return 0;
}
