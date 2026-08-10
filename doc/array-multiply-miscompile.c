// An unwritten local array element, multiplied, comes out as garbage on the P2.
// This is valid C with no undefined behaviour in it, and it is wrong at every
// optimization level except -O0.
//
//	gcc (the reference)                     0
//	flexcc -2                               647359469   <- wrong, deterministically
//	flexcc -2 -O0                           0
//	flexcc -2 -O1                           647359469   <- wrong
//	flexcc -2 -Ono-inline-small -Ono-peephole  647359469 <- wrong; the flags ogo
//	                                                       build passes do NOT
//	                                                       cover this one
//
// Measured on a P2-EDGE, with the flexcc in internal/flexcc (v7.7.0) and with one
// built from spin2cpp master (v7.7.0-7-g54a19b1). BOTH are wrong, so unlike
// doc/const-divide-miscompile.c this is LIVE UPSTREAM and is the one to report.
//
// It is close kin to flexprop issue 103, whose reproducer is
// doc/optimizer-miscompile.c -- both are an unwritten local array element meeting a
// general multiply -- but the #103 fix does NOT cover it. Measured side by side,
// with no workaround flags:
//
//	                      flexcc v7.7.0   flexcc master
//	the #103 reproducer   -202817768      0            <- fixed there
//	this file             647359469       647359469    <- not
//
// This one is also STRICTLY SIMPLER than #103's: that one needed a call taking the
// element and a file-scope destination, and this needs neither.
//
// Found by the smith oracle on the board (seed 323 of a 400-seed sample) and
// reduced from a 300-line generated program.
//
// Three ingredients, each needed. Measured one change at a time on hardware:
//
//	the element must be UNWRITTEN                            needed
//	  "a[0] = 0;" before reading it                          right
//	a general multiply                                       needed
//	  "2 * a[0]" -- a power of two, so a shift               right
//	  "57 + a[0]"                                            right
//	index [0] of the array                                   needed
//	  "a[1]" instead                                         right
//
// What does NOT matter, so do not read anything into it: the array's size (2 and 4
// are both wrong), whether the destination is a local or a file-scope int, whether
// it is assigned or xor-assigned, and whether anything was stored in it first. A
// CALL taking the element is not needed either, which is the clearest difference
// from #103.
//
// REDUCTION NOTE, worth keeping: the first attempt at this used the predicate
// "gcc output != board output" and reduced to a program that was wrong at EVERY
// level -- it had wandered off the optimizer bug onto a different divergence, and
// was useless as a report. The predicate has to assert the whole property being
// reduced: gcc == flexcc -O0 != flexcc at the default level.
//
// NOT worked around. -Ono-regs corrects it but costs 68% more code; eleven other
// passes turned off individually do not. Until it is fixed upstream and the backend
// regenerated, a program that multiplies a local array element it has not written
// can be wrong on the board.
//
// To check whether this is still so, run this on a board and read the number.

#include <stdio.h>

int main(void) {
	int a[2] = {0};
	int r = 57 * a[0];
	printf("%d\n", r);
	return 0;
}
