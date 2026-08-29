// FIXED in the backend as of the regeneration of 2026-08-29 (spin2cpp 2bd01c4c,
// see internal/generator.go), which carries the upstream fix for flexprop issue
// 103: built at a plain -2 by the flexcc in internal/flexcc this prints 0 on a
// P2-EDGE, where the v7.7.0 transpile printed -202817768, and `ogo build` no longer
// passes -Ono-inline-small. That flag was the workaround from v0.14.0 to the
// regeneration -- the note below on which flag answered for which defect, and
// internal/build on what the pair cost, are the record of it. This file stays as
// the regression check: build it without any -Ono flag and it must print 0.
//
// WORKED AROUND from when this was written until the regeneration: `ogo build`
// passed -Ono-inline-small, and with that pass off the program was right. Both
// passes behind the bug -- the small-function inliner and the register allocator
// -- had to cooperate for it, so turning either off was enough; the inliner was
// the cheaper one to lose, which was measured in time as well as in code size.
//
// -Ono-inline-small IS THE ONLY ONE OF THE TWO FLAGS `ogo build` PASSES THAT
// SAVES THIS PROGRAM. -Ono-peephole, the other one, leaves it printing the same
// wrong -202817768 as a plain build; that flag answers for
// doc/optimizer-dangling-label.c and for nothing else. Neither flag is redundant
// and neither covers the other's defect -- internal/build has the full matrix.
// This is worth stating plainly because the note that used to stand there said
// the opposite, and the flag it invited dropping was this one.
//
// FIXED UPSTREAM 2026-08-03 (flexprop issue 103): the root cause was an
// optimization moving an instruction between a `qmul` and its `getqx` that the
// `qmul` indirectly depended on. The backend here is a transpiled copy, and until
// 2026-08-29 it was pinned to v7.7.0, which predates the fix; the flag stayed until
// the pin moved.
//
// This is valid C that the target's compiler gets wrong. It is kept here as the
// reproducer for a backend bug the compiler has no workaround for, found by the
// smith oracle running on a P2-EDGE (seed 28 of a widened board sample) and
// reduced from a 103-line generated program.
//
// Expected: 0. On the board: -202817768, deterministically, every run.
//
// IT IS AN OPTIMIZER BUG. flexcc gets it right at -O0 and wrong at every other
// setting measured -- the default, -O1, -O2, -Os, and --fcache=0 -- each with a
// DIFFERENT wrong value, which is what a stale or uninitialized word looks like
// rather than a wrong computation. `ogo build` passes no -O, so it takes the
// default and is wrong.
//
// The host compiler is right, so nothing off-target sees it; the build reports
// nothing; and the wrong value is a plain integer, so nothing downstream looks
// suspicious either. It is a silent wrong answer.
//
// Four ingredients, all needed. Measured one change at a time against this file:
//
//	a[2] of a local int[4], read without ever being written    needed
//	  a[0] or a[3] of the same array                           right
//	  a[2] of a local int[8]                                   right
//	  a[2] after `a[2] = 0;` first                             right
//	  the array at file scope rather than local                right
//	  a plain local int rather than an array element           right
//	a call taking it                                           needed
//	  `a[2] * 35` with no call                                 right
//	a general multiply of the result                           needed
//	  `f2(a[2], 0)` alone, or `+ 35`, or `| 35`                right
//	  `* 1` or `* 2` -- a shift, not a multiply                right
//	  `* m` where m is a variable holding 35                   wrong
//	  `35 * f2(a[2], 0)`, operands swapped                     wrong
//	the result reaching a FILE-SCOPE int                       needed
//	  the same value left in a local and printed               right
//	  static or extern linkage on the global                   both wrong
//	  computed into a local first, then assigned to the global wrong
//
// That last one is what makes it more than a broken multiply: the local holds the
// right value and the global does not.
//
// It is also a heisenbug, which is worth knowing before trying to reduce it
// further. Any additional read of the intermediates makes it correct -- printing
// a[2] beside the result, or printing the local beside the global, or the
// checksum after each statement of the original program. Reduction has to be
// driven by comparing host output against board output, never by watching the
// program work.
//
// The OctoGo program this came from:
//
//	var checksum int = 0
//
//	func f2(a int, b int) int { return a }
//
//	func main() {
//		var a [4]int
//		checksum = f2(a[2], 0) * 35
//		println(checksum)
//	}

#include <stdio.h>

static int checksum = 0;

int f2(int a, int b) {
	(void)b;
	return a;
}

int main(void) {
	int a[4] = {0};
	checksum = (f2(a[2], 0) * 35);
	printf("%d\n", checksum); // 0 expected; the P2 prints -202817768
	return 0;
}
