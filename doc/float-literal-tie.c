// A decimal float literal lying HALFWAY between two floats is rounded either way on
// the P2, where C rounds it to the even one. Found by the on-board fuzzer (seed 479,
// 2026-09-25): a float32 compared with the constant 2.000872e+09 -- which Go, gcc and
// the fuzzer's own VM read as 2000871936 -- was unequal on the board, and the program
// took the other branch in silence.
//
// Measured on a P2-EDGE with the flags ogo build passes, spin2cpp v7.7.3 (eb263961),
// printing each literal's bits:
//
//	                                   gcc        flexcc
//	1  2.000872e+09f                   4eee85c4   4eee85c5   <- wrong, one float up
//	2  2.000008e+09f                   4eee6b66   4eee6b66
//	3  2.000024e+09f                   4eee6be4   4eee6be3   <- wrong, one float down
//	4  2.00004e+09f                    4eee6c60   4eee6c60
//	5  7.000064e+12f                   54cbba8a   54cbba8a
//	6  7.008256e+12f                   54cbf794   54cbf794
//	7  (float)2.000872e+09             4eee85c4   4eee85c5   <- wrong, the double too
//	8  2000872000.0f                   4eee85c4   4eee85c4
//	9  0x1.dd0b88p+30f                 4eee85c4   4eee85c4
//	10 2.5e+09f                        4f1502f9   4f1502f9
//
// Lines 1-6 are each EXACTLY a point halfway between two floats: 2000872000 lies
// between 2000871936 and 2000872064, and C rounds the tie to the even significand,
// ...c4. The lexer (frontends/lexer.c, parseNumber) reads a decimal in DOUBLE
// arithmetic -- the integer part exactly, each fraction digit times an inexact
// 1e-k from a table, the exponent by pow(10.0, e) -- and then casts the double to
// float. The value it builds is a few double roundings off the decimal, so a tie is
// broken by whichever way that error falls: up for 1, down for 3, and right for the
// others by luck. Line 7 shows the double is no better, the target's double being a
// 32-bit float. Line 8 is the same value as line 1 spelled without an exponent or a
// fraction, which the lexer reads exactly, and line 9 the same float in hex, whose
// digits and exponent are powers of two and which it therefore reads exactly too;
// line 10 is a literal nowhere near a tie, read right.
//
// Only a decimal ON or within a few double roundings of such a point is at risk, and
// it is a real one for a compiler that writes floats in their SHORTEST spelling: the
// shortest decimal naming a float with an even significand may be exactly the tie,
// round-half-even being what makes it name that float at all. Of 2.4 million
// float32 values sampled across magnitudes from 1e-30 to 1e30, 1613 had such a
// spelling, every one a large integer: 1600 near 2e9 and 13 near 7e12.
//
// The emitter's workaround, in internal/octogo/emit.go: a decimal lying within 2^-40
// of its magnitude of a float32 tie (nearFloat32Tie) is written in hex instead --
// a folded constant's shortest spelling (floatSpelling) and a literal as the
// program wrote it (cFloatLit) alike -- and every other one as before.

#include <stdio.h>
#include <stdint.h>
#include <string.h>

static uint32_t bits(float f) {
	uint32_t b;
	memcpy(&b, &f, sizeof b);
	return b;
}

int main(void) {
	printf("1 %08lx\n", (unsigned long)bits(2.000872e+09f));
	printf("2 %08lx\n", (unsigned long)bits(2.000008e+09f));
	printf("3 %08lx\n", (unsigned long)bits(2.000024e+09f));
	printf("4 %08lx\n", (unsigned long)bits(2.00004e+09f));
	printf("5 %08lx\n", (unsigned long)bits(7.000064e+12f));
	printf("6 %08lx\n", (unsigned long)bits(7.008256e+12f));
	printf("7 %08lx\n", (unsigned long)bits((float)2.000872e+09));
	printf("8 %08lx\n", (unsigned long)bits(2000872000.0f));
	printf("9 %08lx\n", (unsigned long)bits(0x1.dd0b88p+30f));
	printf("10 %08lx\n", (unsigned long)bits(2.5e+09f));
	return 0;
}
