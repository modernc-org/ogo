// flexcc refuses a unary PLUS applied to a double: "Bad number of parameters in
// call to _float_add: expected 2 found 1", from flexprop v7.7.0 building for the
// P2 with the flags ogo build uses. It reads the prefix + as a binary addition
// with one operand, at the point where it lowers double arithmetic to its
// _float_add runtime call. A unary minus is lowered correctly, and so is a unary
// plus on an int, which needs no runtime call.
//
// It reaches ordinary OctoGo through `+x` written on a float, which Go allows and
// which is the identity: `(+Neg).Abs()`, `+v`. The emitter drops the operator for
// a float operand (see emitExprNode), which is exactly what Go's compiler does
// with it.
//
// UPSTREAM IT IS NO LONGER A REFUSAL, AND THAT IS WORSE. Measured on a P2-EDGE
// 2026-08-29 against spin2cpp master 2bd01c4: the program COMPILES and prints
//
//	gcc (the reference)     -2.5 2.5 3
//	flexcc master -2        -6.78636E+35 2.5 3   <- wrong
//	flexcc master -2 -O1    -6.78636E+35 2.5 3   <- wrong
//	flexcc master -2 -O0    -1.707864E+32 2.5 3  <- wrong, differently
//	flexcc v7.7.0 (pinned)  refuses to compile
//
// So a loud compile-time error became a silent wrong answer, at every optimization
// level including -O0. Unreported. It costs this compiler nothing either way --
// the operator is dropped before the C is written -- but it is the kind of change
// a backend regeneration would carry in unnoticed, which is why it is recorded
// here rather than only in the issue tracker.

#include <stdio.h>

int main(void) {
	double d = -2.5;
	double p = +d;    /* refused: "expected 2 found 1" */
	double m = -d;    /* fine */
	int i = 3;
	int q = +i;       /* fine */
	printf("%g %g %d\n", p, m, q);
	return 0;
}
