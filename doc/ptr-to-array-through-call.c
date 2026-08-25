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
// IT IS WHY A POINTER TO AN ARRAY IS NIL-CHECKED BY A STATEMENT OF ITS OWN rather
// than by the guard call every other pointer's dereference is wrapped in (see
// nilCheckedC): the guard is called for its panic ahead of the statement and its
// result dropped, and the dereference stays the plain `(*p)`, which this compiler
// stores through correctly. Measured 2026-08-25 on a P2-EDGE: the drop is specific to
// a STRUCT-valued element -- `(*guard(pa))[1] = 7` on an int array lands, and so
// does `*guard(p) = s` for a struct through a plain pointer -- and the same read,
// `(*guard(pa))[1]` of a struct element, fails to ASSEMBLE ("Unknown symbol
// _main__arr__0064_00"). Until 2026-08-25 the pointer to an array carried no check at
// all on that account.
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
