// This is valid C that the target's compiler gets wrong. It is kept here as the
// reproducer for a backend bug the compiler has no workaround for yet, found by
// the smith oracle running on a P2-EDGE (seed 28 of a widened board sample) and
// reduced from a 103-line generated program to this.
//
// Expected: 0. On the board: -202817768, deterministically, every run.
//
// The host compiler is right, so nothing off-target sees it; `ogo build` reports
// nothing; and the wrong value is a plain integer, so nothing downstream looks
// suspicious either. It is a silent wrong answer.
//
// The trigger is narrow and looks like a stack-offset accident rather than
// anything semantic. Measured, each against the same program with one thing
// changed:
//
//	a[2] of a local int[4], read without ever being written    WRONG
//	a[0] or a[3] of the same array                             right
//	a[2] of a local int[8]                                     right
//	a[2] after `a[2] = 0;` first                                right
//	the same array at file scope rather than local             right
//	the call's argument a plain local int rather than a[2]     right
//	no call -- `a[2] * 35`                                     right
//	no multiply -- `f2(a[2], 0)` or `+ 35` or `| 35`           right
//	`* 1` or `* 2` (a shift) rather than `* 35`                right
//	`* m` where m is a variable holding 35                     WRONG
//	`35 * f2(a[2], 0)`, operands swapped                       WRONG
//	the call bound to a temporary first, then multiplied       WRONG
//
// So it needs all three of: an element of an untouched local array of exactly
// four ints, read at index 2; a call taking it; and a general multiply of the
// result. Binding the call to a variable first does not help, which is why there
// is no workaround here to apply.
//
// The OctoGo program this came from:
//
//	func f2(a int, b int) int { return a }
//
//	func main() {
//		var a [4]int
//		println(f2(a[2], 0) * 35)
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
