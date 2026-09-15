// Two `if` statements in a row that update the same global: when the first
// condition is false, the second update starts from a stale register instead of
// from the global -- silently, at the default optimization level, in about the most
// ordinary code there is. gcc computes it right:
//
//	                                  second line
//	gcc                               588851508
//	flexcc -2                         811733765
//	flexcc -2 -O1                     811733765
//	flexcc -2 -Ono-peephole           811733765
//	flexcc -2 -Ono-regs               588851508
//	flexcc -2 -Ono-branch-convert     588851508
//	flexcc -2 -O0                     588851508
//
// Measured on a P2-EDGE 2026-09-15 with a native flexcc of spin2cpp master
// 3840014f. It is OLD: the same shape written in OctoGo -- `if b1 { g = g ^ K }`
// twice on a package variable -- prints 811733765 built by v0.33.0 (the v7.7.0
// backend, with the -Ono-inline-small -Ono-peephole it passed then), by the backend
// of 2026-08-29, and by the regeneration of 2026-09-15 before this fix was added to
// it.
//
// Both bodies become conditional instructions, and the second one loses its read:
//
//	    cmp     local01, #0 wz
//	 if_ne rdlong  local02, ptr__dat__
//	 if_ne xor     local02, ##1364946277
//	 if_ne wrlong  local02, ptr__dat__
//	    cmp     local03, #0 wz
//	                                   <- if_ne rdlong local02, ptr__dat__ is gone
//	 if_ne xor     local02, ##811733764
//	 if_ne wrlong  local02, ptr__dat__
//
// OptimizeReadWrite (backends/asm/optimize_ir.c) pairs the first block's `if_ne
// wrlong` with the second block's `if_ne rdlong` of the same address, finds the
// read's condition a subset of the write's -- NE and NE -- and turns the read into a
// copy of the register just written, which then goes as a self-move. But the two
// NEs test different compares; the `cmp local03` between them re-set Z. When b1 is
// false nothing was written, and the register holds a stale 1 (811733765 is
// 811733764 ^ 1).
//
// Found by the fuzzer's board sweep of 2026-09-15: seeds 391, 525 and 793 fail
// their checksum on a P2-EDGE and pass on the host. Of the 977 programs of seeds
// 1-1000 that fit a cog, 40 compile differently with the fix, and all 40 pass on the
// board.
//
// The fix is one condition: the pairing requires that nothing between the two
// instructions sets a flag either condition reads, !FlagsChangeInRange(ir->next,
// nextread->prev, FlagsUsedByCond(ir->cond) | FlagsUsedByCond(nextread->cond)).
// spin2cpp's `make test_offline` passes 588 of 588 with it, as without, and every
// other reproducer in doc/ compiles byte-identically. REPORTED 2026-09-15 as
// flexprop issue 110 with that change as the suggested fix, and FIXED HERE the same
// day ahead of upstream: it is the second part of internal/optimize_ir.c.diff, and
// the run case "two conditional updates of a package variable in a row" pins it.
//
// To check, build for the P2 with -2 and read the second line: 588851508 is right.

#include <stdio.h>

int g;

__attribute__((noinline)) int id(int v) { return v; }

int main(void) {
	_Bool b1 = id(0), b2 = id(1);
	g = id(326842928);
	printf("%d\n", g);
	if (b1) {
		g = g ^ 1364946277;
	}
	if (b2) {
		g = g ^ 811733764;
	}
	printf("%d\n", g);
	return 0;
}
