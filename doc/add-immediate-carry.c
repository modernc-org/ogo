// The P2 optimizer rewrites a 64-bit addition or subtraction of a constant in two
// ways that change the CARRY the high word is computed from, and the high word comes
// out wrong by one -- silently, in ordinary code, with gcc computing every line right.
// A 64-bit add of a constant is lowered as `add lo, #k wc` feeding `addx hi, #h`, and
// both rewrites below are in spin2cpp's backends/asm/optimize_ir.c with no check that
// anything reads the C flag they disturb:
//
//   OptimizeImmediates (OPT_CONST_PROPAGATE, "-Ono-const" turns it off) turns
//   `add r, ##-k wc` into `sub r, #k wc` for 1 <= k <= 511. The value is the same
//   and C is the opposite sense -- a borrow, not a carry -- so `z + -1` on an int64
//   adds -1 to the high word where it should add nothing: every constant whose LOW
//   word is in 0xFFFFFE01..0xFFFFFFFF, signed or unsigned, added or subtracted.
//
//   OptimizeAddSub (OPT_BASIC_REGS, only "-Ono-regs" turns it off) merges two
//   immediate adds of one register into one and deletes the first -- but the first
//   one's `addx hi, #0` stays, and now adds whatever C was before. `a + b + 1000 +
//   2000` for a = -1, b = 1 is 4294970296, the carry out of `a + b` added twice; and
//   the merged immediate is truncated to 32 bits, so `z + 2000000000 + 2000000000`
//   loses its carry too. FindPrevSetterForReplace crosses statements, so `d := a +
//   b; d += 10; d += 20` is merged the same way. doc/negate64-then-add.c is this
//   merge as well, deleting an add whose Z a conditional reads.
//
// Measured on a P2-EDGE against the backend in internal/flexcc (spin2cpp 2bd01c4c,
// flexprop v7.7.0) with ogo build's flags, `-2`:
//
//	                                 gcc                  -2                   -2 -Ono-const
//	A  z + -1                        999999               -4293967297          999999
//	B  z - 0xFFFFFFFF                -4293967295          1000001              -4293967295
//	C  u + 0xFFFFFFFF (uint64)       4294979640           12344                4294979640
//	D  a + b + 1000 + 2000           3000                 4294970296           4294970296
//	E  y + 2000000000 + 2000000000   3999999969           -294967327           -294967327
//	F  d = a + b; d += 10; d += 20   30                   4294967326           4294967326
//
// for z = 1000000 (A-C), y = -31 (E), u = 12345 and a = -1, b = 1, each out of a call
// the compiler cannot see through. spin2cpp master (3840014f, 2026-09-05) prints the
// same six lines. -Ono-const cures A-C and nothing else; -Ono-regs and
// -Ono-inline-small cure all six (under the second the add is a call to the runtime's
// _int64_add, which neither rewrite sees), and both cost far too much to leave on --
// cycles per iteration of four loops, measured the same day:
//
//	                          -2      -2 -Ono-inline-small     -2 -Ono-regs
//	32-bit DDS                122     404                      500
//	a call per pass           72      120                      168
//	32-bit register mixing    96      96                       120
//	64-bit LCG                392     552                      992
//
// A REGRESSION IN OCTOGO, not only an upstream fault: ogo builds passed
// `-Ono-inline-small -Ono-peephole` until the backend regeneration of 2026-08-29,
// and the first of those hid both rewrites. The same OctoGo program built by v0.33.0
// prints 3000 for D; v0.34.0 through v0.39.0 print 4294970296. Found by widening the
// smith fuzzer's board sample to 120 seeds (seed 111: `int((z + 4492952664366759589
// - 6812432519738288466) >> 32)` one short in its high word).
//
// The fix is two conditions: OptimizeImmediates leaves an ADD/SUB alone when
// InstrSetsFlags(ir, FLAG_WC) && FlagsUsedAt(ir->next, FLAG_WC), and OptimizeAddSub
// merges only when !HasUsedFlags(prev) && !HasUsedFlags(ir). Checked with spin2cpp
// built natively at 2bd01c4c (which compiles this file byte-identically to the
// in-process flexcc before the change) and at master 3840014f, where the change
// applies as it is. With it every line above and every line of
// doc/negate64-then-add.c matches gcc; spin2cpp's own `make test_offline` passes 588
// of 588, as it does without; the six _examples compile byte-identically; of the
// fuzzer's seeds 1-300, the 292 that fit a cog compile identically but for three,
// and two of those three, 111 and 278, fail their checksum on the board before the
// change and pass after it; and of the four loops above it changes one instruction,
// the LCG's `acc -= 4294967295`, which -2 had made an `add #1`.
//
// REPORTED 2026-09-15 as flexprop issue 109, with the fix as the suggested change,
// and FIXED HERE the same day, ahead of upstream: the change is carried as
// internal/optimize_ir.c.diff, which internal/generator.go applies over spin2cpp
// 3840014f, and under the regenerated backend every line prints gcc's value. The
// same regeneration clears doc/negate64-then-add.c and doc/int64-min-spelling.c,
// which turned out to be this merge. Upstream's own fix is what lets the diff go:
// build this with a native flexcc of the later spin2cpp, without the diff, and it
// has to print gcc's values first.
//
// To check, build for the P2 with -2 and compare each line with gcc's.

#include <stdio.h>
#include <stdint.h>

__attribute__((noinline)) int64_t id64(int64_t v) { return v; }
__attribute__((noinline)) uint64_t idu64(uint64_t v) { return v; }

// F in a function of its own: the merge across statements depends on where the
// register pair lives, and in main's frame here it happens not to.
__attribute__((noinline)) int64_t accumulate(int64_t a, int64_t b)
{
	int64_t d = a + b;
	d += 10;
	d += 20;
	return d;
}

int main(void) {
	int64_t z = id64(1000000);
	uint64_t u = idu64(12345);
	int64_t a = id64(-1), b = id64(1);
	int64_t y = id64(-31);
	printf("A %lld\n", z + -1);
	printf("B %lld\n", z - 0xFFFFFFFFLL);
	printf("C %llu\n", u + 0xFFFFFFFFULL);
	printf("D %lld\n", a + b + 1000 + 2000);
	printf("E %lld\n", y + 2000000000LL + 2000000000LL);
	printf("F %lld\n", accumulate(a, b));
	return 0;
}
