// An assignment through a pointer-to-array that came out of a CALL is dropped.
//
//	                            gcc (the reference)   flexcc on a P2-EDGE
//	(*po)[0] = "direct"         direct                direct
//	(*guard(po))[0] = "viafn"   viafn                 direct   <- the write vanished
//	read (*po)[0] afterwards    viafn                 direct   <- and never happened
//
// guard is the identity: it takes the pointer and returns it. The value is correct
// going in and the assignment simply does not land. Nothing is reported: no warning,
// no error, and the program runs on with the old contents.
//
// The comma form fails the same way, so it is not the call that matters so much as
// the pointer-to-array being an operand of anything other than the dereference:
//
//	(nilv(po), (*po))[0] = "comma"    ->  nothing written, both reads empty
//
// Measured with the flexcc in internal/flexcc (v7.7.0), with the flags ogo build
// passes. A plain `(*po)[0] = ...` is correct, which is why nothing in the compiler
// has met this: emitting an assignment through a call's result is refused ("only
// simple and field assignment targets are supported yet"), so the shape cannot come
// out of ogo today.
//
// IT IS WHY A POINTER TO AN ARRAY CARRIES NO NIL CHECK. Every other pointer's
// dereference is wrapped in a guard that returns it (see nilCheckedC), and wrapping
// this one costs the store it was meant to protect -- a far worse bargain than the
// one it buys. When this is fixed upstream and the backend regenerated, the
// exclusion in nilCheckedC can go and a `*[N]T` gains the check with the rest.
//
// To check whether this is still so, compile it and read the three lines.

#include <stdio.h>

typedef struct {
	const char* str;
	int len;
} ogo_string;

typedef ogo_string arr1[1];

static arr1* guard(arr1* p) { return p; }

int main(void) {
	ogo_string one[1] = {0};
	arr1* po = &one;

	(*po)[0] = (ogo_string){"direct", 6};
	printf("direct: [%s]  want direct\n", (*po)[0].str);

	(*guard(po))[0] = (ogo_string){"viafn", 5};
	printf("viafn:  [%s]  want viafn\n", (*guard(po))[0].str);

	printf("after:  [%s]  want viafn\n", (*po)[0].str);
	return 0;
}
