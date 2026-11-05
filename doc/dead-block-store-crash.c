// The backend crashes, a nil dereference, where a call it inlines makes a
// branch dead and the dead code holds a block store with more code after it:
//
//	static int tracing(void) { return 0; }
//	...
//	if (!tracing())
//		return;
//	memset(buf, 0, sizeof(buf));   // SETQ #3, WRLONG
//	n = 1;
//
// at -O1, the level `ogo build` compiles at, and -O2; at -O0 it builds. Under
// the transpiled backend the crash is a Go panic, which `ogo build` reports as
// "flexcc crashed: runtime error: invalid memory address or nil pointer
// dereference"; a native build segfaults. It reaches OctoGo programs as Go
// writes them: a build switch, `func tracing() bool { return false }`, an early
// return on it and a zeroed array stored, `buf = [16]byte{}`, before another
// store -- dom102's mutant m0320, `for ; c < 0; c &= c - 1` in a uint32's bit
// count, a loop that never runs, built so. No binary is ever wrong: the build
// fails. Measured 2026-11-05 under flexprop v7.6.2's flexcc, the pin of
// v1.0.0-rc.3 (spin2cpp b5993a84 with the carried diffs) and upstream master
// a487858e, natively; and under v0.40.0's, v0.50.0's and rc.3's `ogo build`.
//
// The cause, read the same day in a487858e, which still has it: OptimizeReadWrite
// marks a SETQ before a memory operation FLAG_KEEP_INSTR and not the operation it
// modifies. When the inlined call makes the branch unconditional,
// EliminateDeadCode deletes the code after the jump up to the next label, every
// instruction but the volatile ones -- so the SETQ stays and its WRLONG goes. A
// SETQ the pass reaches itself it marks kept along with the instruction after it,
// `ir->next->flags |= FLAG_KEEP_INSTR`; once the rest of the function is gone the
// orphaned SETQ is its last instruction, and ir->next is NULL. Kept with the
// instruction it modifies where the dead code is deleted, and the mark asked of a
// next instruction that exists (carried as optimize_ir.c.diff's second hunk), the
// pair stays where the SETQ stayed, two never-run instructions the listing prints
// as `nop`; upstream's `make test_offline` passes 588 of 588 as before, and of
// 1641 programs -- the run cases, fuzzer seeds 1-400, doc/ and the domain programs
// to dom101 -- none builds differently, so none of them had an orphaned SETQ in
// its binary, where the fix would have kept the WRLONG beside it. One that does
// not end the list follows a jump nothing skips, so no path runs it.
//
// gcc prints, and the target should print:
//
//	7 0

#include <stdio.h>
#include <string.h>

unsigned char buf[16] = {7};
int n;

static int tracing(void) { return 0; }

void reset(void) {
	if (!tracing())
		return;
	memset(buf, 0, sizeof(buf));
	n = 1;
}

int main(void) {
	reset();
	printf("%d %d\n", buf[0], n);
	return 0;
}
