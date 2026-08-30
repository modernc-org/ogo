// The target's printf ignores two of C's conversion flags, silently.
//
//	                 gcc (the reference)   flexcc on a P2-EDGE
//	printf("%#x", 255)      0xff                 ff        <- '#' ignored
//	printf("%#o", 8)        010                  10        <- '#' ignored
//	printf("%#X", 255)      0XFF                 FF        <- '#' ignored
//	printf("%08.3f", 1.5)   0001.500             " " 1.500 <- '0' ignored for a float
//	printf("%08.2f", 1.5)   00001.50             "  " 1.50 <- same
//	printf("%09.3f", -1.5)  -0001.500            "  "-1.500
//
// Everything else this compiler emits a spec for is right: '0' on the INTEGER verbs
// ("%05d" -> 00042), '-', '+', ' ', a width, and a precision all behave as gcc's do.
// So the defect is narrow, and it is only these two.
//
// Measured with the flexcc in internal/flexcc (v7.7.0), one conversion per printf
// call, which is the shape internal/octogo/emit.go emits: it prints each verb in a
// statement of its own. That shape matters -- with SEVERAL conversions in one call
// the same build also drops the ' ' flag from the fourth one ("% d" of 42 printing
// "42"), which does not reproduce when the conversion stands alone. Whatever that
// is, ogo programs cannot reach it.
//
// '#' is REFUSED, not worked around. `printf("%#x", v)` is a compile-time error
// naming the backend, because it is accepted by the HOST C compiler the emit tests
// run against: a silently narrower number on the board, in a program whose host run
// was green, is the failure this project is least willing to ship, and the prefix
// can be written into the format string instead. '0' on a float was refused the
// same way until 2026-08-30, when the emitter stopped using the target's printf for
// floats at all -- its DIGITS are wrong past the seventh (doc/printf-float-digits.c)
// -- and lays a float out from the exact decimal expansion itself (floatFmtHelper
// in internal/octogo/emit.go), padding included; so `%08.3f` works, as a side effect.
//
// To check whether this is still so, compile it and read the columns.

#include <stdio.h>

int main(void) {
	printf("A[");
	printf("%#x", 255);
	printf("] want 0xff\n");

	printf("B[");
	printf("%08.3f", 1.5);
	printf("] want 0001.500\n");

	printf("C[");
	printf("%05d", 42);
	printf("] want 00042, and this one is RIGHT\n");
	return 0;
}
