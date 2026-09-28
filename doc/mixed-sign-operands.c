// An unsigned operand beside a SIGNED one is compared and divided as C says by gcc
// -- the usual arithmetic conversions, both operands converted to the unsigned type
// -- and as signed numbers by the target's C compiler, silently but for the first
// line:
//
//	                                            gcc                      flexcc -2
//	u < c, c > u, u >= c, m < one               0 0 1 0                  1 1 0 1
//	u < 10000, 10000 > u                        0 0                      0 0
//	s < one, one > s                            0 0                      1 1
//	v < five, five > v                          1 1                      0 0
//	c / u, c % u, u / c, u % c                  0 10000 429496 296       4294967295 3000 429496 296
//	10000 / u, 10000 % u                        0 10000                  4294967295 3000
//	4 * u / 3, 4 * u >> 1, 4 * u > 100          1431646432 2147469648 1  4294957963 4294953296 0
//
// Measured on a P2-EDGE 2026-09-28 with a native flexcc of spin2cpp v7.7.3
// (eb26396, its master that day); the in-process one prints the same. For the four
// comparisons of the first line, and for nothing else, it says
//
//	warning: signed/unsigned comparison may not work properly
//
// All of it is in frontends/types.c. CompileComparison compares unsigned, at 32
// bits, only where both operands are unsigned or the signed one IsUnsignedConst, a
// constant EXPRESSION of no negative value -- which a literal is and an object is
// not, a `static const int` included. Its 64-bit branch compares unsigned where
// EITHER operand is, where C does only for an unsigned operand of 64 bits. And
// MatchIntegerTypes answers with the type of the LEFT operand when the two are of
// one size, which HandleTwoNumerics chooses the division and the remainder by, and
// which goes on to whatever reads the result: `4 * u` has the right bits and
// divides and shifts as a signed number afterwards.
//
// In OctoGo an untyped constant takes the type of the operand it meets, so none of
// these is a mixed operation in the source. They were in the C. A constant operand
// on the LEFT of an unsigned one has been spelled unsigned since 2026-08-19 (`4u *
// u`); a NAMED constant was a `static const int` object until 2026-09-28, and `b-a
// < patience` for a difference past 2^31 was true on the board -- found by p2-11.
// The emitter writes a constant as its VALUE since, spelled in the type it meets
// (untypedOperandC, intConstRef). The run cases "an untyped constant takes the type
// of the unsigned operand it meets" and "unsigned arithmetic with a constant on the
// left" pin both, and the workarounds stay whatever upstream does: the C they write
// is right under either rule.
//
// REPORTED 2026-09-28 as flexprop issue 114, with a change that gives C its own
// rules and leaves the other languages theirs: a comparison is unsigned where either
// operand is an unsigned int after the integer promotions (or an unsigned long long,
// at 64 bits), and an operation of two operands is unsigned where one of them is an
// unsigned type of the full size, whichever side it is on. The result of a
// comparison is left out, being an unsigned type to the compiler and an int in C:
// without that `(width + (leftright==PAD_ON_RIGHT)) / 2` in the library's _fmtpad
// became an unsigned division in every program that prints. With the change this
// program prints gcc's lines on the board; spin2cpp's `make test_offline` passes 588
// of 588, as without; and of 1222 programs -- the run corpus, fuzzer seeds 1-400 and
// doc/ -- 1081 binaries are identical and 121 differ, 109 of them in the library's
// float formatter (`base*DOUBLE_ONE/2`, an int times an unsigned) and 12 in a
// division of an operand narrower than an int by an unsigned constant, where the
// two rules agree. Every one of the 121 was run on the board built both ways: 120
// print the same, and this one prints what gcc prints.
//
// A neighbour that is NO fault of either compiler, and looks like one: an unsigned
// int divided by a long long is a signed division of 64 bits in C. A guard handing a
// uint64 divisor back as a long long made that of `10000 / w`, which was
// 18446744073709551615 for a w past 2^63 on the host and the board alike
// (ogoNonzero64u, and the run case "a uint64 divides a constant unsigned").
//
// To check, build for the P2 with -2 and read the lines: gcc's are C's.

#include <stdio.h>

unsigned u = 4294960296u; /* 2^32 - 7000 */
unsigned one = 1, five = 5;
int c = 10000;
int m = -1;
short s = -1;
long long v = -5;

int main(void) {
	printf("compare: %d %d %d %d\n", u < c, c > u, u >= c, m < one);
	printf("literal: %d %d\n", u < 10000, 10000 > u);
	printf("narrow:  %d %d\n", s < one, one > s);
	printf("wide:    %d %d\n", v < five, five > v);
	printf("divide:  %u %u %u %u\n", c / u, c % u, u / c, u % c);
	printf("literal: %u %u\n", 10000 / u, 10000 % u);
	printf("product: %u %u %d\n", 4 * u / 3, 4 * u >> 1, 4 * u > 100);
	return 0;
}
