// `{}` for a zero-length array of SCALARS draws a warning about an initializer that
// has no elements:
//
//	struct { int n; int z[0]; int k; } s = { 1, {}, 2 };  // warning: Extra
//	                                                      // initializers for array
//	int z[0] = {};                                        // the same
//
// The same with a designated initializer, `.z = {}`, and as "too many elements found
// in initializer" for a static. Silent: the member left out, `{ 1 }`, and a
// zero-length array of STRUCTS given `{}`. Measured with the in-process flexcc
// (spin2cpp eb263961 with the carried optimize_ir.c.diff) 2026-10-07; harmless on a
// P2-EDGE in every position measured (2026-10-06), the members after it in place.
//
// Go writes a zero-length array as a field, `_ [0]func()` being the idiom that makes
// a struct incomparable, and the emitter writes such a member's zero value `{}`. The
// warning fails `ogo build`, a backend warning being a fault until shown harmless, so
// a program with one is refused loudly rather than built.
//
// gcc (zero-length arrays and `{}` being GNU C) prints, and the target prints:
//
//	1 2 0

#include <stdio.h>

int main(void) {
	struct {
		int n;
		int z[0];
		int k;
	} s = {1, {}, 2};
	int z[0] = {};
	printf("%d %d %d\n", s.n, s.k, (int)sizeof z);
	return 0;
}
