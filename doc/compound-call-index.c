// flexcc miscompiles a COMPOUND assignment whose right operand indexes an array
// member through a pointer that a CALL returned. The same expression is correct
// with "=", correct through a pointer VARIABLE, and correct bound to a temporary
// first. gcc compiles all of it correctly; only flexcc differs.
//
// Measured on a P2-EDGE against flexprop v7.7.0, with the flags ogo build uses
// (-2 -Ono-inline-small -Ono-peephole) and without them:
//
//	1 plain ptr, arrow, array member    970  (correct)
//	2 plain ptr, deref, array member    970  (correct)
//	3 CALL ptr, arrow, array member     -1571814688   <-- wrong
//	4 CALL ptr, arrow, scalar member    970  (correct)
//	5 CALL ptr, deref, array member     83711123      <-- wrong
//	6 CALL ptr to int, deref            970  (correct)
//	7 CALL ptr, +=                      -60640451     <-- wrong
//	8 CALL ptr, explicit x = x - ...    970  (correct)
//	9 CALL ptr, via a temporary         970  (correct)
//
// So it takes all three of: a compound operator, an ARRAY member, and a pointer
// from a CALL. Any one of them removed and the answer is right, which is why it
// survived: nothing about the source looks unusual.
//
// It reaches ordinary OctoGo because the call is the compiler's own. A checked
// build -- the default -- wraps every pointer dereference in a nil guard, so
//
//	func (m *Mean) Push(v Q) Q { m.sum -= m.ring[m.at]; ... }
//
// a ring buffer's accumulator, emits `ogo_nil_Mean_ptr(m)->sum -=
// ogo_nil_Mean_ptr(m)->ring[...]` and quietly returns a wrong average. That is how
// it was found: a moving average that disagreed with the same program in Go.
//
// Worked around in emitHoistedCompound (internal/octogo/emit.go), which binds such
// an operand to a temporary.

#include <stdio.h>

typedef struct { int a[4]; int s; } T;

static T *id(T *p) { return p; }

int main(void) {
	T t = {0};
	t.a[1] = 30;

	int x = 1000;
	x -= id(&t)->a[1];
	printf("compound, call-returned ptr, array member: x=%d (want 970)\n", x);

	int y = 1000;
	y = y - id(&t)->a[1];
	printf("explicit, the same expression:             y=%d (want 970)\n", y);

	return 0;
}
