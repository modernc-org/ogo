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
// THE SAME FAULT APPLIES TO THE TARGET, where it is a silent NO-OP:
//
//	id(&t)->a[1] += 5;   // adds nothing at all
//	id(&t)->a[1] *= 2;   // likewise
//	id(&t)->a[1]++;      // CORRECT -- ++ and -- are unaffected
//	t.a[1] += 5;         // CORRECT through a value
//	T *h = id(&t); h->a[1] += 5;        // CORRECT -- the pointer is a variable
//	int *e = &id(&t)->a[1]; *e += 5;    // CORRECT -- the element is named
//
// which costs any per-bin or per-channel total kept in an array field and updated
// through a pointer:
//
//	func (h *Hist) Add(v int32) { h.bins[v%4] += v }
//
// A SLICE field is not affected: it is reached through its own backing pointer, so
// no call stands between the pointer and the index.
//
// Worked around in emitHoistedCompound (internal/octogo/emit.go), which binds the
// OPERAND to a temporary, and hoistCompoundTarget, which names the TARGET
// element's address. The address rather than the pointer, because it needs only
// the element type and evaluates the index exactly once.
//
// REPORTED as flexprop issue 106 and FIXED UPSTREAM by spin2cpp 2bd01c4, "fix for
// function array dereference bugs" (2026-08-28). The fault was in the transform
// that rewrites `M op= N` into `M := M op N`: an ARRAYREF whose base had side
// effects was given a temporary even when the base was a member reference, and
// taking a temporary of an array-typed member is what went wrong. Eric Smith's own
// suggested workaround was the long form, `x = x + expr`, which the table above
// already shows correct.
//
// VERIFIED on a P2-EDGE 2026-08-29, all EIGHTEEN spellings from the issue, pinned
// against master, with the flags ogo build passes:
//
//	                                     pinned v7.7.0    master 2bd01c4   gcc
//	x -= id(p)->a[1]                     -1571814688      970              970
//	x -= (*id(p)).a[1]                      83711123      970              970
//	x += id(p)->a[1]                       -60640451     1030             1030
//	id(p)->a[1] += 5                             100      105              105
//	id(p)->a[1] *= 2                             100      200              200
//	(the other thirteen)                     correct  correct          correct
//
// The workaround STAYS until the pin moves: internal/flexcc is a transpiled copy of
// v7.7.0, and the fix is not in any release yet. This file is the check -- compile
// it with the pinned backend and it is still wrong.

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

	t.a[1] = 100;
	id(&t)->a[1] += 5;
	printf("compound INTO the same element:            a=%d (want 105)\n", t.a[1]);

	t.a[1] = 100;
	id(&t)->a[1]++;
	printf("++ on the same element:                    a=%d (want 101)\n", t.a[1]);

	return 0;
}
