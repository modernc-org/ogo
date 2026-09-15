// A signed comparison of two values the optimizer knows is decided by the sign of
// their 32-bit difference, which overflows when the two are more than 2^31 apart:
// the comparison comes out the wrong way round -- silently, at the default
// optimization level. gcc computes it right:
//
//	                                  output
//	gcc                               1 1 1
//	flexcc -2                         0 0 0
//	flexcc -2 -O1                     0 0 0
//	flexcc -2 -Ono-const              0 0 0
//	flexcc -2 -Ono-peephole           0 0 0
//	flexcc -2 -Ono-inline-small       0 0 0
//	flexcc -2 -Ono-regs               1 1 1
//	flexcc -2 -O0                     1 1 1
//
// Measured on a P2-EDGE 2026-09-15 with a native flexcc of spin2cpp master 3840014f
// carrying the two fixes internal/optimize_ir.c.diff held then. It is OLD: `m < 1`
// for an int32 m holding math.MinInt32, written in OctoGo, is false built by v0.33.0
// (the v7.7.0 backend). Operands closer together -- `i < 2000000000` -- are right,
// and so is a comparison of two literals, which the C front end folds.
//
// In OctoGo it shows through the small-function inliner too: a saturation check,
// `func notSaturated(v int32) bool { return v < 2147483647 }`, called as
// notSaturated(-7), returns false on the board.
//
// TransformConstDst (backends/asm/optimize_ir.c) folds the compare:
//
//	case OPC_CMPS:
//	    val1 -= val2;
//	    cval = val1;
//
// and ApplyConditionAfter takes C from the sign of cval. -7 - 2147483647 wraps
// positive, so C says "not less", and every condition on it after the compare is
// resolved the wrong way: `int i = -7; _Bool lt = i < 2147483647; printf("%d\n",
// lt);` prints a constant, `getbyte arg02, #0, #0`.
//
// Found 2026-09-15 by comparing wide constants against Go on the board. The fix
// decides C from the comparison itself, as the unsigned OPC_CMP case below it does,
// and keeps the difference for Z. spin2cpp's `make test_offline` passes 588 of 588
// with it, as without, and of 1438 programs -- the run corpus and fuzzer seeds 1-1000
// -- compiled with and without it, no binary differs. REPORTED 2026-09-15 as flexprop
// issue 111 with that change as the suggested fix, and FIXED HERE the same day ahead
// of upstream: it is the third part of internal/optimize_ir.c.diff, and the run case
// "a signed comparison of two values far apart" pins it.
//
// To check, build for the P2 with -2 and read the line: 1 1 1 is right.

#include <stdio.h>

int main(void) {
	int i = -7;
	int k = -2147483647 - 1;
	printf("%d %d %d\n", i < 2147483647, i <= 2147483646, k < 1);
	return 0;
}
