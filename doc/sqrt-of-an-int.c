// sqrt() of an INTEGER argument computes the integer square root on the P2, where C
// says the argument converts to double first: sqrt(2) is 1 there, and 1.41421 on
// the host. The neighbouring calls with an integer argument -- pow, fmodf, fabs,
// floor -- are right, and sqrt of a float variable holding 2 is right too.
//
// It is the header: the installed P2 tree's math.h defines sqrt as a macro over a
// compiler builtin, and the builtin, handed an int, works on the int. gcc's libm
// prototype converts the argument, which is why the host sees nothing.
//
// Measured on a P2-EDGE with the flags ogo build passes (a plain -2), on the
// backend of 2026-09-15 (spin2cpp 3840014f, internal/optimize_ir.c.diff):
//
//	                        gcc       flexcc
//	1 sqrt(2)               1.41421   0E-38     <- wrong: the integer 1's bits
//	2 sqrt(2.0)             1.41421   1.41421
//	3 sqrt((double)i)       1.41421   1.41421
//	4 sqrt(i)               1.41421   0E-38     <- wrong: the variable is an int
//	5 pow(2, 10)            1024      1024
//	6 fmodf(-7, 3)          -1        -1
//	7 fabs(-3)              3         3
//	8 floor(-7)             -7        -7
//	9 sqrt(f)               1.41421   1.41421
//
// Lines 1 and 4 print the bit pattern of the integer 1 read as a float, which is
// what %g makes of an int on the stack: the builtin computed the integer square
// root of 2. Everything that CONVERTS the argument is right, which is why it
// survives: the argument of every other call here is converted by its macro.
//
// It reached OctoGo through `math.Sqrt(2)`, whose untyped constant argument was
// emitted as the integer it defaults to, so `float32(math.Sqrt(2))` printed 1 on a
// P2-EDGE where Go prints 1.4142135 -- found by a float battery against Go
// (2026-09-18). The emitter converts every argument of a math intrinsic to its
// declared parameter's type now, `sqrt((double)(2))`, which is what Go does at the
// call (emitMathArgs in internal/octogo/emit.go), so the program is right on both.
// This file is what re-checks the builtin after a regeneration:
//
//	scripts/cboard.sh doc/sqrt-of-an-int.c

#include <stdio.h>
#include <math.h>

int main(void) {
	int i = 2;
	float f = 2;
	printf("1 sqrt(2)          = %g want 1.41421\n", sqrt(2));
	printf("2 sqrt(2.0)        = %g want 1.41421\n", sqrt(2.0));
	printf("3 sqrt((double)i)  = %g want 1.41421\n", sqrt((double)i));
	printf("4 sqrt(i)          = %g want 1.41421\n", sqrt(i));
	printf("5 pow(2, 10)       = %g want 1024\n", pow(2, 10));
	printf("6 fmodf(-7, 3)     = %g want -1\n", fmodf(-7, 3));
	printf("7 fabs(-3)         = %g want 3\n", fabs(-3));
	printf("8 floor(-7)        = %g want -7\n", floor(-7));
	printf("9 sqrt(f)          = %g want 1.41421\n", sqrt(f));
	return 0;
}
