// A NEGATIVE 64-bit constant passed to a 32-bit parameter is passed as two words,
// so it and every argument after it are read wrong, and the backend says only
//
//	warning: Bad number of parameters in call to f: expected 3 found 4
//
// Measured on a P2-EDGE 2026-10-03 with the in-process flexcc and with a native
// build of spin2cpp v7.7.3 (eb263961, upstream's master) with the carried
// optimize_ir.c.diff, which build this file to the same binary:
//
//	                                gcc              flexcc -2
//	f(-38LL, 2, 3) f(1,-38LL,3)     -3777 -277 82    50531186 -1017018438 -60641331
//	  f(1, 2, -38LL)
//	f(1, -1LL, 3)                   93               -1712933700
//	f(1, 21 + (-38LL), 3)           -67              -1017018438
//
// (A wrong value is whatever the shifted words hold, and differs from program to
// program.)
//
// A positive one is right, `38LL`, `0LL` and `2147483647LL`, and so is one past 32
// bits, `5000000002LL`, truncated as gcc truncates it; so are a cast, `(int)-38LL`,
// and a 64-bit variable. The count is NumExprItemsOnStack's (functions.c), by the
// argument's own type, ahead of any conversion to the parameter's. REPORTED
// 2026-10-03 as flexprop issue 121, with no suggested fix.
//
// In OctoGo: an untyped constant computed from 64-bit constants was typed 64-bit
// whatever its value, so `zero = big - big` was spelled `0LL` and `zero - 38` was
// `(-38LL)`, and `f(1, zero-38, 3)` gave -999419038 on the board for Go's -277.
// Found by OctoSmith seed 722 in a board sweep of v0.48.0; fixed in the emitter
// (e9dabd0), such a constant being an int now, which is Go's default type for it.
// The run case "a constant computed from 64-bit constants, where an int is wanted"
// holds it.
//
// To check, build for the P2 with -2 and compare the lines with gcc's.

#include <stdio.h>

int f(int a, int b, int c) { return a * 100 + b * 10 + c; }

int main(void) {
	printf("%d %d %d\n", f(-38LL, 2, 3), f(1, -38LL, 3), f(1, 2, -38LL));
	printf("%d %d\n", f(1, -1LL, 3), f(1, 21 + (-38LL), 3));
	printf("%d %d %d\n", f(1, 38LL, 3), f(1, 5000000002LL, 3), f(1, (int)-38LL, 3));
	return 0;
}
