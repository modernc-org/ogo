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
// 2026-08-29 against spin2cpp master 2bd01c4: the same one-argument _float_add is
// still emitted, but the diagnostic for it has been DEMOTED FROM AN ERROR TO A
// WARNING -- so the call goes out missing its second operand and reads whatever is
// in that register.
//
//	gcc (the reference)     -2.5
//	flexcc v7.7.0 (pinned)  refuses to compile: "error: Bad number of parameters"
//	flexcc master -2        warning, then -6.78636E+35   <- wrong
//	flexcc master -2 -O1    warning, then -6.78636E+35   <- wrong
//	flexcc master -2 -O0    warning, then -1.707864E+32  <- wrong, differently
//
// The value is NOT STABLE: in a longer program some occurrences of `+d` came out
// right and others did not, which is what a missing operand looks like. A unary
// minus is lowered correctly, a unary plus on an int needs no runtime call, and a
// constant folds before it gets there -- so it is exactly `+<float variable>`.
//
// REPORTED 2026-08-29 as flexprop issue 107. It costs this compiler nothing either
// way -- the operator is dropped before the C is written, Go defining it as the
// identity -- but it is the kind of change a backend regeneration would carry in
// unnoticed, and a warning that silently replaces an error is worth knowing about
// before the pin moves.
//
// CARRIED IN, knowingly, by the regeneration of 2026-08-29 (spin2cpp 2bd01c4c):
// the backend in internal/flexcc now warns over this file and miscompiles it, as
// the table says master did. Nothing in OctoGo reaches it.
//
// FIXED UPSTREAM 2026-09-05: Eric Smith (totalspectrum) closed issue 107 with
// "This should be fixed in github now (in the spin2cpp repository, it'll be merged
// into flexprop before the next release)" -- a commit past the 2bd01c4c pin. It is
// NOT in internal/flexcc yet, so this file still reproduces under the pinned
// backend; the next regeneration past the fix clears it. Re-run this battery then.
// Nothing in OctoGo changes either way: dropping `+x` on a float is the correct
// lowering regardless (Go defines it as the identity), so the emitter keeps doing
// it and this file stays only as the record.

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
