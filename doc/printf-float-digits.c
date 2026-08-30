// The target's printf produces wrong digits for a float past the seventh
// significant one. Valid C; gcc prints the exact decimal expansion, rounded:
//
//	                          gcc (the reference)   flexcc -2 on a P2-EDGE
//	printf("%.7e", 1.0f/3)    3.3333334e-01         3.3333335e-01   <- wrong 8th digit
//	printf("%.3e", 100.0f)    1.000e+02             1.000e+02
//	printf("%.0e", 1e6f)      1e+06                 1e+06
//	strtof("0.33333334")      == 1.0f/3             == 1.0f/3       strtof is fine
//	strtof("0.3333333")       != 1.0f/3             != 1.0f/3
//
// The exact value of the float32 nearest a third is 0.333333343267..., whose
// eight-digit rounding is 3.3333334e-01; the target's library carries the
// conversion in float arithmetic and loses the last place. So neither a shortest
// round-trip form (Go's %v, %g, print and println), which needs correctly
// rounded candidates to search among, nor %e and %f at a precision past seven
// digits can be had from this printf. Also measured: the target's atoi does not
// read a leading '+', so the exponent of "1e+02" parses as 0.
//
// Measured 2026-08-30 against spin2cpp 2bd01c4c, flexcc -2 as ogo build invokes it.
//
// WORKED AROUND in internal/octogo/emit.go (floatFmtHelper): a float is laid out
// from its exact decimal expansion -- Go's strconv decimal ported to C for one
// width, with Go's roundShortest for the shortest form and half-to-even rounding
// under a precision -- and the target's printf sees only the finished text. The
// run case "a float prints in the shortest form" holds every verb on the board.
// Unreported upstream as of this writing.
//
// To check, build for the P2 and compare the first line's digits with gcc's.

#include <stdio.h>
#include <stdlib.h>

__attribute__((noinline)) float idf(float v) { return v; }

int main(void) {
	float t = idf(1.0f) / idf(3.0f);
	printf("%.7e\n", (double)t);
	printf("%.3e\n", (double)idf(100.0f));
	printf("%d %d\n", strtof("0.33333334", 0) == t, strtof("0.3333333", 0) == t);
	return 0;
}
