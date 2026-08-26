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
