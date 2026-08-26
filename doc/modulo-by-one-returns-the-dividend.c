// flexcc computes `x % 1` and `x % -1` as x, where C (and Go) say 0: a remainder
// is smaller than its divisor, so a division by one leaves nothing over. Every
// integer type up to 32 bits is affected, signed and unsigned; the 64-bit case
// goes through a runtime call and is right.
//
// Measured on a P2-EDGE against flexprop v7.7.0 with the flags ogo build uses
// (-2 -Ono-inline-small -Ono-peephole), for a dividend of -118 held in a volatile:
//
//	x % 1      -118   WRONG (want 0)
//	x % -1     -118   WRONG (want 0)
//	x % 2         0   correct
//	x % 3        -1   correct
//	x / 1      -118   correct
//	x / -1      118   correct
//	(unsigned)x % 1   118   WRONG (want 0)
//	(int64_t)x % 1      0   correct -- a helper call, not folded
//
// So it is the constant ±1 that is mis-folded, at 32 bits and narrower. A divisor
// that is not a literal is right, whether it is a variable, a call, or the same
// value passed through an identity function -- which is what makes it look like a
// strength reduction whose mask is wrong for 2^0.
//
// It reaches ordinary OctoGo through any `x % 1`, which is not the useless
// expression it looks: it is what a generated modulus, a period of one, or a
// constant-folded divisor comes to. The oracle fuzzer found it in a program that
// computed `z % 1` on an int8 and got its dividend back.
//
// Worked around by writing the operation as the multiplication by zero it is:
// `x * 0` is the same value, evaluates x exactly once as Go does, and leaves
// nothing for a compiler to fold wrongly. `x %= 1` becomes `x *= 0` for the same
// reason. See emitExprNode and emitAssignTail in internal/octogo/emit.go.

#include <stdio.h>
#include <stdint.h>

volatile int v = -118;
volatile int64_t w = -118;
volatile unsigned u = 118;

int main(void) {
	printf("int    %% 1  = %d (want 0)\n", (int)(v % 1));
	printf("int    %% -1 = %d (want 0)\n", (int)(v % -1));
	printf("int    %% 2  = %d (want 0)\n", (int)(v % 2));
	printf("int    %% 3  = %d (want -1)\n", (int)(v % 3));
	printf("uint   %% 1  = %u (want 0)\n", (unsigned)(u % 1));
	printf("int64  %% 1  = %d (want 0)\n", (int)(w % 1));
	int one = 1;
	printf("int    %% one (a variable) = %d (want 0)\n", (int)(v % one));
	return 0;
}
