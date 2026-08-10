// A division by a CONSTANT is miscompiled on the P2 by the pinned backend, and is
// ALREADY FIXED UPSTREAM. This file is the check for whether regenerating the
// backend is worth doing: build it with the flexcc in internal/flexcc and it prints
// the wrong number; build it with one from spin2cpp master and it prints 0.
//
//	gcc (the reference)                    0
//	flexcc v7.7.0, what internal/flexcc is 2035542505   <- wrong
//	flexcc master (v7.7.0-7-g54a19b1)      0
//
// Measured on a P2-EDGE with the flags ogo build passes, -2 -Ono-inline-small
// -Ono-peephole. It is an optimizer fault: -O0 and -O1 are both correct, the
// default level is not.
//
// Found by the smith oracle on the board (seed 74 of a 400-seed sample) and reduced
// from a 152-line generated program by delta debugging against gcc's output. It is
// a heisenbug -- printing the operands of the comparison that goes wrong makes it
// compute correctly -- so it cannot be reduced by instrumenting it.
//
// Three ingredients, each needed. Measured one change at a time against this file:
//
//	a division by a CONSTANT that is not a power of two      needed
//	  "/ 2" instead of "/ 23"                                right
//	  "/ d" for an int d holding 23                          right
//	  "* 23" instead of "/ 23"                               right
//	  "% 23" instead of "/ 23"                               WRONG, same fault
//	the never-taken branch, whole                            needed
//	  its body without the call                              right
//	  comparing v.data rather than v.vt against the table    right
//	  "if (0)" in place of the comparison                    right
//	  the branch removed                                     right
//	TWO arrays, with the divided one read at a nonzero index needed
//	  "b[0]" instead of "b[1]"                               right
//	  "a[1]" instead of "b[1]", so one array serves both     right
//
// A constant divisor is what makes this more than a broken divide: a non-power-of-
// two constant is lowered to a multiply by a reciprocal, which on this target is
// the CORDIC qmul and its getqx -- the same pair as flexprop issue 103, whose fix
// was "an optimization moved an instruction between a qmul and its getqx that the
// qmul indirectly depended on". So this is very probably that same fault reached by
// a different route, which is consistent with master having it fixed.
//
// What does NOT matter, so do not read anything into it: the multiply in the
// branch, _Bool versus int for the flag, writing the comparison as "!(x <= y)"
// rather than "x > y", the checksum being file-scope or local, the arrays' size,
// which element of the first array is read, and writing a[2] = 0 before reading it.
//
// NOT worked around. There is no affordable flag: -Ono-regs corrects it but costs
// 68% more code, and the two flags already passed do not. The fix is to regenerate
// internal/flexcc once upstream tags a release containing it -- see
// internal/generator.go. Until then a program that divides by a constant near a
// never-taken call can be wrong on the board.
//
// To check whether this is still so, run this on a board and read the number.

#include <stdio.h>

typedef struct S S;
struct S { int f; };
typedef struct { int (*Val)(void*); } S_vt;
typedef struct { void* data; const S_vt* vt; } iface;

int S_Val(S* r) { return r->f; }
static int thunk(void* r) { return S_Val((S*)r); }
static const S_vt vtable = { thunk };

int main(void) {
	int checksum = 0;
	iface v = {0};
	if (v.vt == &vtable) {
		checksum ^= S_Val((S*)v.data);
	}
	int a[3] = {0};
	int b[3] = {0};
	if (a[2] > (b[1] / 23)) {
		checksum ^= 2035542505;
	}
	printf("%d\n", checksum);
	return 0;
}
