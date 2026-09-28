// An unsigned operand of 32 bits beside a SIGNED one is compared and divided as C
// says -- both converted to unsigned -- by gcc, and as signed numbers by the target's
// C compiler wherever the signed one is an object rather than a constant:
//
//	                  u < c  c > u  u < 10000  u < (unsigned)c
//	gcc                 0      0       0            0
//	flexcc -2           1      1       0            0
//
//	                  c / u        c % u   10000u / u  (unsigned)c / u
//	gcc                 0          10000       0            0
//	flexcc -2         4294967295    3000       0            0
//
//	                  10000u / id64(w)        10000u / w
//	gcc               18446744073709551615        0
//	flexcc -2         18446744073709551615        0
//
// for `static const int c = 10000`, a uint32_t u of 4294960296 and a uint64_t w of
// 18446744073709544616. Measured on a P2-EDGE 2026-09-28 with the in-process flexcc,
// spin2cpp v7.7.3 (scripts/cboard.sh).
//
// The comparison is CompileComparison's (frontends/types.c): with one operand
// unsigned and the other signed it compares unsigned only where the signed one
// IsUnsignedConst, a constant EXPRESSION of no negative value, which a literal is
// and a `static const` object is not. Otherwise it compares signed and, both being
// of 32 bits, says so while it builds:
//
//	warning: signed/unsigned comparison may not work properly
//
// The division is MatchIntegerTypes's: two operands of one size take the type of
// the LEFT one, so `c / u` is a signed division and `u / c` an unsigned one. It says
// nothing. The third line is no fault of either compiler, and is here because it
// looks like one: an unsigned int divided by a long long is a signed division of 64
// bits in C, which is what a guard handing a uint64 divisor back as a long long
// made of `10000 / w`.
//
// In OctoGo an untyped constant takes the type of the operand it meets, so none of
// these is a mixed operation in the source. They were in the C: a named constant
// was emitted as the object above, and `b-a < patience` for a difference past 2^31
// was true on the board -- found by p2-11, 2026-09-28. The emitter writes a constant
// as its VALUE since, spelled in the type it meets (untypedOperandC, intConstRef),
// and a uint64 divisor's guard hands back a uint64 (ogoNonzero64u). The run cases
// "an untyped constant takes the type of the unsigned operand it meets" and "a
// uint64 divides a constant unsigned" pin both.
//
// Not reported upstream: the warning says the comparison is known, and the division
// follows from the same rule.
//
// To check, build for the P2 with -2 and read the lines: gcc's are C's.

#include <stdint.h>
#include <stdio.h>

static const int c = 10000;
static uint32_t u = 4294960296u;
static uint64_t w = 18446744073709544616ull;

static long long id64(long long b) { return b; }

int main(void) {
	printf("%d %d %d %d\n", u < c, c > u, u < 10000, u < (unsigned)c);
	printf("%u %u %u %u\n", c / u, c % u, 10000u / u, (unsigned)c / u);
	printf("%llu %llu\n", 10000u / id64(w), 10000u / w);
	return 0;
}
