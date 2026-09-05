// round() and roundf() yield an INTEGER on the P2, where C says they return double
// and float. Everything that CONVERTS the result is right, which is why it survives:
// `float r = roundf(2.5f)` is 3.0, and arithmetic on the result is right too. What
// is wrong is the value used as a float directly -- passed to a variadic function,
// or holding anything outside int range.
//
// Measured on a P2-EDGE with the flags ogo build passes, IDENTICALLY on the pinned
// v7.7.0 and on spin2cpp master 2bd01c4, and at -O0, -O1 and the default level, so
// it is not the optimizer:
//
//	                              gcc          flexcc
//	1 %g of roundf(2.5f)          3            0E-38          <- wrong
//	2 %g of roundf(-3.5f)        -4           -nan            <- wrong
//	3 %d of roundf(2.5f)          2116031936   3              <- the tell
//	4 (double)round(1e30)         1e+30        2.147484E+09   <- wrong
//	5 float r = roundf(2.5f)      3            3
//	6 float s = roundf(-3.5f)    -4           -4
//	7 %g of floorf(2.5f)          2            2
//	8 %g of ceilf(2.5f)           3            3
//	9 %g of fabsf(-2.5f)          2.5          2.5
//
// Line 3 is the diagnosis: read as %d the call prints exactly 3, so what is on the
// stack is the integer and not a float. Line 1's 0E-38 is that integer's bit pattern
// read as a float and line 2's -nan is -4's; line 4 is the same fault at the other
// end, clamped to INT_MAX. Lines 7-9 are the neighbouring rounding functions in the
// same position, all correct.
//
// It is probably just the header. In the installed P2 tree's math.h:
//
//	#define round(x)  __builtin_round(x)
//	#define roundf(x) __builtin_round(x)
//	#define floor(x)  __builtin_floorf(x)
//	#define ceil(x)   __builtin_ceilf(x)
//
// The neighbours use the f-suffixed builtins; round and roundf share
// __builtin_round, which behaves like C's lround.
//
// REPORTED 2026-08-29 as flexprop issue 108. It costs this compiler nothing:
// math.Round is written in OctoGo over Floor rather than mapped to roundf, which
// has no such limit -- see mathSrc in internal/octogo/build.go and mathIntrinsics in
// internal/octogo/emit.go, where round's absence from the table is deliberate.
//
// FIXED UPSTREAM 2026-09-05: Eric Smith (totalspectrum) closed issue 108 with
// "This should be fixed in 7.7.2", a release past the 2bd01c4c pin. It is NOT in
// internal/flexcc yet, so this file still reproduces under the pinned backend; the
// next regeneration past the fix clears it. Re-run this battery then, and once
// roundf returns a real float, reconsider whether math.Round can map to it -- the
// Floor-built form stays correct regardless, so this is a simplification to weigh,
// not a change to make.
//
// The failure shape is what makes it worth a file of its own: correct wherever a
// test is likely to look, wrong where it is not.

#include <stdio.h>
#include <math.h>

int main(void) {
	/* 1. The value, where C says a float is wanted. */
	printf("1  printf(\"%%g\", roundf(2.5f))   = %g          want 3\n", roundf(2.5f));
	printf("2  printf(\"%%g\", roundf(-3.5f))  = %g       want -4\n", roundf(-3.5f));

	/* 2. The same call read as an INTEGER, which is what it is. */
	printf("3  printf(\"%%d\", roundf(2.5f))   = %d          want garbage, gets 3\n", roundf(2.5f));

	/* 3. So it cannot hold a value outside int range. */
	printf("4  (double)round(1e30)          = %g want 1e+30\n", (double)round(1e30));

	/* 4. Through a variable it converts, and is right -- the workaround. */
	float r = roundf(2.5f), s = roundf(-3.5f);
	printf("5  float r = roundf(2.5f)       = %g          want 3\n", r);
	printf("6  float s = roundf(-3.5f)      = %g         want -4\n", s);

	/* 5. The neighbouring rounding functions in the same position are correct. */
	printf("7  printf(\"%%g\", floorf(2.5f))   = %g          want 2\n", floorf(2.5f));
	printf("8  printf(\"%%g\", ceilf(2.5f))    = %g          want 3\n", ceilf(2.5f));
	printf("9  printf(\"%%g\", fabsf(-2.5f))   = %g        want 2.5\n", fabsf(-2.5f));
	return 0;
}
