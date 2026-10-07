// An array whose elements are aggregates, initialized with FEWER elements than its
// length, is refused by the target's compiler where C zero-fills the rest. Two
// shapes, two messages:
//
//	static int grid[3][2] = { {1, 2} };   // error: Internal compiler error,
//	                                      //        expected initializer list
//	void f(void) { P ps[3] = { {1, 2} }; } // error: Expected multiple values
//
// The first is a STATIC array of rows -- at file scope or a function's static --
// or of structs holding an array, `static R sr[3] = { { {1, 2} } }`, and it is said
// only where the array is READ: an unused one is dropped before the initializer is
// looked at. `{ 1, 2 }` with the braces elided is refused the same way. Right: all
// the rows written out, a static array of plain structs given fewer, `static P
// sp[3] = { {1, 2} }`, and `{ 0 }`. The second is a LOCAL array of structs, or of
// structs holding an array; a local array of rows, `int lg[3][2] = { {1, 2} }`, a
// local array of pointers and of scalars given fewer are right. Measured with the
// in-process flexcc (spin2cpp eb263961 with the carried optimize_ir.c.diff)
// 2026-10-07. The second is said first: with both in one file, as here, the static
// is reported once the local is taken out.
//
// Loud. The emitter writes the missing elements out as zeros in both shapes
// (padLocalAggregates), and a package variable of a type holding an array is
// declared bare, C zeroing it, and filled at package initialization (staticZeroC).
//
// gcc prints, and the target should print:
//
//	2 0 2 0

#include <stdio.h>

typedef struct {
	int a, b;
} P;

static int grid[3][2] = {{1, 2}};

int main(void) {
	P ps[3] = {{1, 2}};
	printf("%d %d %d %d\n", grid[0][1], grid[2][1], ps[0].b, ps[2].a);
	return 0;
}
