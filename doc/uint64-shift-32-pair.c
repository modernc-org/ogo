// An unsigned 64-bit value shifted left by 32 and then right by 32 -- its low half,
// zero-extended -- is refused by the backend while generating code:
//
//	n.c:4: error: Cannot handle expression yet
//
// (backends/asm/outasm.c). Only that pair of counts: 31/31, 33/33, 40/40, 48/48,
// 32 then 31 or 40, the signed type, a temporary between the two shifts, `<< 16 <<
// 16` and `& 0xFFFFFFFF` all build. Measured 2026-10-03 with the in-process flexcc
// and with a native build of spin2cpp v7.7.3 (eb263961, upstream's master) with
// the carried optimize_ir.c.diff, which refuse it alike.
//
// It is loud, not silent: nothing is built. In OctoGo, `x << 32 >> 32` for a uint64
// x is that pair, and the build stops with the message above about the C written
// for it. Found by OctoSmith, seed 359 of v0.48.0's generator, in the board sweep
// of seeds 201-1000. Not worked around; `x & 0xFFFFFFFF` is the spelling that
// builds. REPORTED 2026-10-03 as flexprop issue 122.
//
// FIXED upstream by spin2cpp b5993a84 (2026-10-13: fetching the low or high word of
// a 64-bit value built from two words takes that word), the pin the backend was
// regenerated at the same day. Measured natively before adoption: this file and
// nine unsigned neighbours -- through a function, an array element, a call's
// result, `>> 31`, `<< 31`, narrowed -- build and print gcc's values on a P2-EDGE.
// The fix is narrow: doc/uint64-widened-shift-32.c and
// doc/cast-of-64bit-expression.c build to the same binaries as before it.
//
// To check, build for the P2 with -2: it prints 89abcdef; before the fix the build
// was refused.

#include <stdio.h>

unsigned long long z = 0x123456789abcdefULL;

int main(void) {
	unsigned long long r = (z << 32) >> 32;
	printf("%llx\n", r); // 89abcdef
	return 0;
}
