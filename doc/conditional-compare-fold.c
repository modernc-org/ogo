// A CONDITIONAL instruction that sets flags, whose operands the optimizer comes to
// know as constants, is folded as though it always ran: the flags it would set are
// taken as known after it, though it sets them only where its condition holds. Here
// `f` holds `v < v`, which the optimizer decides is 0, and `(12 != g) || f` is
// compiled to
//
//	cmp	g, #12 wz
//	if_e	cmp	f, #0 wz        ' only where g == 12
//	if_e	jmp	#skip           ' over `r = 1`
//
// -- right so far. Then f is known to be 0, the `if_e cmp f, #0 wz` is folded as a
// compare that always sets Z, and the `if_e jmp` after it becomes a jmp: r is 0
// whatever g holds, silently, at the default optimization level. gcc computes it
// right:
//
//	                                  output
//	gcc                               1 1
//	flexcc -2                         0 1
//	flexcc -2 -O1                     0 1
//	flexcc -2 -O0                     1 1
//	flexcc -2 -Ono-const              0 1
//	flexcc -2 -Ono-peephole           0 1
//	flexcc -2 -Ono-cse                0 1
//	flexcc -2 -Ono-branch-convert     1 1
//	flexcc -2 -Ono-regs               1 1
//
// Measured on a P2-EDGE 2026-10-02 with a native flexcc of spin2cpp v7.7.3
// (eb263961), the commit upstream's master still is. The second value is the
// control: a stored `_Bool` the optimizer cannot decide, `seed > 100`, is right,
// and so is the same expression with `v < v` written in place, which no
// conditional compare is made for.
//
// TransformConstDst (backends/asm/optimize_ir.c), reached from OptimizeMoves
// through PropagateConstForward, folds an instruction whose operands are both
// known and hands the flags to ApplyConditionAfter without asking ir->cond. Every
// other place that applies known flags -- OptimizeCompares, the constant moves of
// OptimizeMoves -- asks for COND_TRUE first. The fix returns there when the
// instruction is conditional; spin2cpp's `make test_offline` passes 588 of 588
// with it, as without, and of 1550 programs -- the run corpus, these reproducers
// and fuzzer seeds 1-400 and 1001-1300 -- six build differently, all six right on
// the board with it and one, seed 1284, wrong without.
//
// In OctoGo: fuzzer seed 1284 wrote `var b_62 bool = ((b_50 || (12 != gv_37)) ||
// b_50)` for a b_50 of `(v_39 < v_39) && ...`, and failed its checksum on the
// board and passed on the host. REPORTED 2026-10-02 as flexprop issue 118 with
// that change as the suggested fix, and FIXED HERE the same day ahead of
// upstream, as internal/optimize_ir.c.diff, with the backend regenerated; the
// run case "a stored comparison the backend decides, or-ed after another" pins
// it.
//
// To check, build for the P2 with -2 and read the line: 1 1 is right.

#include <stdio.h>

int g;        // 0
int seed = 5;

int main(void) {
	int v = seed;
	_Bool f = (v < v);       // false, and the optimizer can tell
	int r = (12 != g) || f;  // 1, g being 0
	_Bool f2 = (seed > 100); // false, and the optimizer cannot tell
	int r2 = (12 != g) || f2;
	printf("%d %d\n", r, r2);
	return 0;
}
