// A 64-bit conditional expression whose chosen arm does ARITHMETIC comes out with
// the right low word and a garbage high word -- silently, at every optimization
// level. gcc computes it right:
//
//	                                  output
//	gcc                               7 93
//	flexcc -2                         6741888693713240071 6741888693713240157
//	flexcc -2 -O1                     6741888693713240071 6741888693713240157
//	flexcc -2 -Ono-const              6741888693713240071 6741888693713240157
//	flexcc -2 -Ono-peephole           -719820176459038713 -719820176459038627
//	flexcc -2 -Ono-inline-small       6741888693713240071 6741888693713240157
//	flexcc -2 -Ono-regs               -1042930725050908665 -1042930725050908579
//	flexcc -2 -O0                     -260424409985056761 -260424409985056675
//
// The low word is always the right one (6741888693713240071 is 0x5d8f95b900000007),
// and the high word changes with the flags, as a register nothing wrote would.
// Measured on a P2-EDGE 2026-09-18 with the in-process flexcc (spin2cpp 3840014f
// plus internal/optimize_ir.c.diff).
//
// What decides it, measured the same day on the board, `v` a long long holding -7:
//
//	v < 0 ? -v : v                     wrong, as an argument and as an initializer
//	v > 0 ? v : v + 100                wrong: the arm taken is the arithmetic one
//	v < 0 ? 0ULL - (u64)v : (u64)v     wrong, and ~(u64)v and (u64)v + 1 in an arm
//	1 ? 0ULL - (u64)v : 5ULL           wrong: a constant condition too
//	v < 0 ? neg : pos                  RIGHT: both arms variables, computed before
//	n < 0 ? (long long)-n : (long long)n  RIGHT: the arms widen a 32-bit value
//	if (v < 0) m = -v; else m = v;     RIGHT: the statement form
//	-v, v + 1, 0ULL - (u64)v alone     RIGHT, as an argument or an initializer
//
// CompileCondResult (backends/asm/outasm.c) gives the result ONE register and moves
// one word into it from each arm; why an arm that is a variable or a widening cast
// survives was not traced.
//
// In OctoGo it cannot be written -- Go has no conditional operator -- and the
// emitter writes no 64-bit one around a user's expression. It was met in the
// emitter's own output: printf's integer layout (intPrintHelper) was first handed
// its magnitude as `v < 0 ? 0ULL - (unsigned long long)v : (unsigned long long)v`,
// and every negative number printed on the board as a twenty-digit one. The
// negation moved into the helper, as a statement.
#include <stdio.h>

int g = -7;

static int get(void) { return g; }

int main(void) {
	long long v = get();
	long long w = v < 0 ? -v : v;
	long long z = v > 0 ? v : v + 100;
	printf("%lld %lld\n", w, z);
	return 0;
}
