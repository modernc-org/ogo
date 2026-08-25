// flexcc converts a float to a 64-bit integer by REINTERPRETING ITS BITS, and clamps
// a float converted to a 32-bit unsigned at 2147483647. Every narrower conversion is
// correct, and so is the reverse direction. gcc compiles all of it correctly; only
// flexcc differs, and it differs at -O0, -O1 and -2 alike, with and without the
// -Ono-inline-small -Ono-peephole that ogo build passes -- so it is the code
// generator, not the optimizer.
//
// Measured on a P2-EDGE against flexprop v7.7.0, the backend in internal/flexcc:
//
//	                                    gcc                flexcc v7.7.0
//	A  (long long)d, d = 3.0            3                  1077936128   <-- 0x40400000, the bits of 3.0f
//	   (long long)f, f = 3.0f           3                  1077936128
//	   (unsigned long long)d            3                  1077936128
//	B  (int)d, (unsigned)d              3 3                3 3          (correct)
//	   (unsigned)big, big = 3e9         3000000000         2147483647   <-- clamped at INT_MAX
//	C  (long long)neg, neg = -12345.75  -12345             -968825088   <-- the bits again
//	   (long long)(int)d                3                  3            (correct: a 32-bit conversion, then widened)
//	D  (double)ll == 36000000.0         1                  1            (correct)
//	F  (long long)3.0, a constant       3                  1077936128   <-- folded wrongly too
//
// The 32-bit SIGNED conversion is right, and the workaround in the compiler
// (ogoF2u32, ogoF2i64 and ogoF2u64 in internal/octogo/emit.go) is built on nothing
// else: a value within its range converts directly, and a wider one is taken apart
// at 2^32 -- the high word by a division by 2^32, the low word by subtracting the
// high word back out, both exact in binary floating point -- and converted in
// halves.
//
// Writing that workaround measured two more faults, both in what a RETURN statement
// returns, and both avoided by making the operation a statement of its own:
//
//	G  int32_t t = (int32_t)v; return (int64_t)t;      high word not sign-extended   <-- a widening cast in a return
//	                                                   (-12345 returns 4294954951 below; a 3 returned -1042943919190441981 in another program,
//	                                                   so the word is whatever the register held, zero or not)
//	   int64_t wh(int32_t t) { return (int64_t)t; }    the same                     <-- even of a plain parameter
//	   return (int64_t)(int32_t)v;                     the same
//	   int32_t t = (int32_t)v; int64_t r = t; return r;        correct    (widened by assignment)
//	   int32_t t = (int32_t)v; return t;                       correct    (widened implicitly)
//	   printf("%lld", (int64_t)k);                             correct    (the same cast as an argument)
//	H  int64_t r = ...; return v < 0 ? -r : r;         0, or garbage                <-- a negation in a conditional in a return
//	   if (v < 0) { r = -r; } return r;                        correct
//	   if (v < 0) { r = 0 - r; } return r;                     correct
//	   int64_t m = -r; return v < 0 ? m : r;                   correct    (the operands are variables)
//	   if (v < 0) { r = ~r + 1; } return r;             garbage                      <-- the known 64-bit complement fault
//
// This file prints, on the board and under gcc:
//
//	A 1077936128 1077936128 1077936128    A 3 3 3
//	B 3 3 2147483647                      B 3 3 3000000000
//	C -968825088 3                        C -12345 3
//	D 1                                   D 1
//	F 1077936128                          F 3
//	G 3 4294954951 -12345                 G 3 -12345 -12345
//	H 0 -343597383680                     H -343597383680 -343597383680
//
// G is the fault the backend battery records as "a cast to a 64-bit type applied to
// a 64-bit expression", seen from the other side: what it needs is the cast in a
// return, and the operand being an expression or a narrower variable.
//
// LIVE UPSTREAM: spin2cpp master (a430da8, 2026-08-21, the build that fixed #105)
// prints every line above identically to v7.7.0. Unreported as of 2026-08-25.
//
// Found by a lock-in detector -- a CORDIC sine table, int64 accumulators -- whose
// converted results matched Go on the host and not on the board.

#include <stdio.h>
#include <stdint.h>

double d = 3.0;
float f = 3.0f;
double big = 3000000000.0;
double neg = -12345.75;
long long ll = 36000000;

static int64_t g_cast(double v) { int32_t t = (int32_t)v; return (int64_t)t; }
static int64_t g_assign(double v) { int32_t t = (int32_t)v; int64_t r = t; return r; }

static int64_t h_ternary(int64_t r, double v) { return v < 0 ? -r : r; }
static int64_t h_if(int64_t r, double v) { if (v < 0) { r = -r; } return r; }

int main(void) {
    printf("A %lld %lld %llu\n", (long long)d, (long long)f, (unsigned long long)d);
    printf("B %d %u %u\n", (int)d, (unsigned)d, (unsigned)big);
    printf("C %lld %lld\n", (long long)neg, (long long)(int)d);
    printf("D %d\n", (double)ll == 36000000.0);
    printf("F %lld\n", (long long)3.0);
    printf("G %lld %lld %lld\n", g_cast(d), g_cast(neg), g_assign(neg));
    printf("H %lld %lld\n", h_ternary(343597383680LL, neg), h_if(343597383680LL, neg));
    return 0;
}
