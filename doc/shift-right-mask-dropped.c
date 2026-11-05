// An AND after a shift right by a constant is dropped where its mask covers
// every bit below the shift's top one and not the top one itself:
//
//	(x >> 30) & 1          // lost: bits 30 and 31
//	(x >> 28) & 7          // lost
//	(x >> 24) & 0x7f       // lost
//	(x >> 22) & 0x1ff      // lost
//	(x >> 31) & 2          // lost: after a shift by 31, every mask
//	(x >> 31) & 0          // lost
//	(x >> 29) & 5          // right: 5 lacks bit 1, below the top one
//	(x >> 30) & 3          // right: the mask covers every bit left
//	(x >> 31) & 1          // right
//	(x >> 28) & 15         // right: written as a GETNIB first
//	(x >> 20) & 0x7ff      // right: a mask over 511 is written as a ZEROX first
//
// for an unsigned 32-bit x, the shift in a register. The listing shows `shr
// local01, #30` and no AND after it. Measured over every shift of 1 to 31 and
// twenty masks of 0 to 511: 18 ANDs dropped that change the value. A mask of
// four or eight low bits, or a run of low bits over nine, is written as GETNIB,
// GETBYTE, ZEROX or BITL before the rule is reached and kept; a mask over 511
// that no other instruction encodes is dropped at any shift, `(x >> 20) &
// 0x17ff` keeping bit 11, x's top bit, which the mask clears. Measured on a P2-EDGE
// 2026-11-05 with the in-process flexcc (spin2cpp b5993a84 with the carried
// diffs), the pin of v1.0.0-rc.3; right at -O0 and with -O1,~peephole.
//
// The cause, read the same day in upstream master a487858e, which still has it:
// FixupShrAndImm (backends/asm/asm_peep.c, spin2cpp 5270a02e of 2026-06-09, in
// every flexprop release since v7.6.10) deletes `and x, #M` after `shr x, #N`
// when M covers every bit the shift can leave set, and counts them one short,
// `(1U<<(31 - N)) - 1` where they are `0xFFFFFFFFU >> N`. After a shift by 31
// that is no bit, and every mask goes. Counted as `0xFFFFFFFFU >> N` (carried as
// internal/asm_peep.c.diff), the 18 are kept, the 34 that change nothing still
// go, upstream's `make test_offline` passes as before, and of 1640 programs --
// the run cases, fuzzer seeds 1-400, doc/ and the domain programs to dom101 --
// none builds differently. Its sibling
// FixupShlShrAndImm counts `(1U<<N) - 1` for what `shl x, #N; sar x, #N` leaves,
// which is `0xFFFFFFFFU >> N` too, and deletes the shifts before an AND they
// change: `(uint16_t)(int8_t)x` builds to a lone `and #65535` for the P1. On the
// P2 the pair is always a SIGNX first (pat_signex comes before it), so no P2
// program reaches it.
//
// It reaches OctoGo programs as Go writes them: `x >> 31 & m`, `v >> 30 & 1`,
// `w >> 24 & 0x7f` of a uint32. dom101's mutant m0287, `(q/2 - r) >> 31 & 1`
// in ML-KEM's compress mutated to `& 0`, built to the original's binary byte for
// byte and passed a test Go fails 19333 times.
//
// gcc prints, and the target should print:
//
//	a=1 b=5 c=94 d=378 e=0 f=0 g=4
//	h=3 i=1 j=13 k=1514
//
// The target printed a=3 b=13 c=222 d=890 e=1 f=1 on the first line.

#include <stdint.h>
#include <stdio.h>

volatile uint32_t in = 0xdeadbeef;
volatile uint32_t top = 0x80000000;

int main(void) {
	uint32_t x = in, t = top;
	printf("a=%u ", (unsigned)((x >> 30) & 1u));
	printf("b=%u ", (unsigned)((x >> 28) & 7u));
	printf("c=%u ", (unsigned)((x >> 24) & 0x7fu));
	printf("d=%u ", (unsigned)((x >> 22) & 0x1ffu));
	printf("e=%u ", (unsigned)((t >> 31) & 2u));
	printf("f=%u ", (unsigned)((t >> 31) & 0u));
	printf("g=%u\n", (unsigned)((t >> 29) & 5u));
	printf("h=%u ", (unsigned)((x >> 30) & 3u));
	printf("i=%u ", (unsigned)((t >> 31) & 1u));
	printf("j=%u ", (unsigned)((x >> 28) & 15u));
	printf("k=%u\n", (unsigned)((x >> 20) & 0x7ffu));
	return 0;
}
