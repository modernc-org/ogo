// A division by a CONSTANT was miscompiled on the P2 by the backend pinned to
// v7.7.0, and is FIXED in the backend as of the regeneration of 2026-08-29
// (spin2cpp 2bd01c4c, see internal/generator.go): built by the flexcc in
// internal/flexcc it prints 0 on a P2-EDGE. It was the check for whether
// regenerating the backend was worth doing, and it stays as the regression check.
//
//	gcc (the reference)                         0
//	flexcc v7.7.0, what internal/flexcc was     2035542505   <- wrong
//	flexcc master (v7.7.0-7-g54a19b1)           0
//	flexcc 2bd01c4c, what internal/flexcc is    0
//
// Measured on a P2-EDGE with the flags ogo build passed at the time, -2
// -Ono-inline-small -Ono-peephole. It is an optimizer fault: -O0 and -O1 are both
// correct, the default level is not.
//
// One thing the re-measurement of 2026-08-29 added: at a PLAIN -2 the v7.7.0
// transpile printed 0 as well. The fault needed -Ono-inline-small -- the flag
// passed to route around flexprop issue 103 -- to show, so one workaround exposed
// the next defect. Worth remembering before turning a pass off: the code the
// other passes then see is code nobody has measured.
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
// It was never worked around. There was no affordable flag: -Ono-regs corrected it
// but cost 68% more code, and the two flags then passed did not. Until the
// regeneration a program that divided by a constant near a never-taken call could
// be wrong on the board.
//
// To check that this is still fixed, run this on a board and read the number.

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
