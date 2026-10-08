// The CORDIC reordering pass moves a conditional move past code that overwrites the
// register the move keeps when it does not execute, and a value computed before a
// multiplication or a division reads as something computed after it.
//
// The target's compiler fills the cycles between a CORDIC command (QMUL, QDIV) and
// the GETQ that collects its result with instructions moved from around it
// (OptimizeCORDIC in spin2cpp's backends/asm/optimize_ir.c, on at -O1). Moving a
// block DOWN past the command, FindBlockForReorderingDownward takes every
// instruction setting its dst for a definition of it, which "meets" the dependency
// of a read of that register below it in the block. A CONDITIONAL one is no
// definition: where it does not execute, the register keeps the value it had
// before. A maximum is such a pair here:
//
//	cmps    t, #69 wcz
//	if_a    mov  result1, t
//	if_be   mov  result1, #69     ; moved, with the next, past the QMUL below
//	mov     t, result1
//	...     result1 = f(g)        ; the multiplication's operand, computed in result1
//	qmul    result1, ##56744
//
// The block `if_be mov result1, #69 ; mov t, result1` went below the QMUL, so t read
// f(g) wherever the maximum was not 69, and the passes after it folded the pair into
// `if_be mov t, #69` on a register t shared with f(g)'s. Silent: d below is f(g), 119,
// for 142.
//
// Found by OctoSmith seed 6978 on the board (a call counted twice, the condition
// that guarded it computed from the wrong d), the host passing it; `ogo build
// --no-inline` passed too, the function then being called rather than inlined and
// its code not movable. Needs an inlined function on each side: f below is marked as
// the emitter marks the functions it asks the backend to inline. `-Ono-cordic-reorder`
// cures it, and so does treating a conditional write as a read of its dst there:
//
//	if (top->cond == COND_TRUE) DeleteDependencies(&depends,top->dst);
//	...
//	if ((InstrReadsDst(top) || (InstrSetsDst(top) && top->cond != COND_TRUE)) && ModifiedInRange(bottom->next,after,top->dst))
//
// Measured on a P2-EDGE with the in-process flexcc (spin2cpp eb263961 with the
// carried optimize_ir.c.diff) 2026-10-08, and with a native build of the same commit,
// with and without the fix.
//
// gcc prints, and the target should print:
//
//	d 142 t1 6752614
//
// The target printed:
//
//	d 119 t1 6752614

#include <stdio.h>
#ifdef __FLEXC__
#define OGO_INLINE __attribute__((inline))
#else
#define OGO_INLINE
#endif

int cs = 7;
static int gv = 71;
static int e = 119;

static int mx(int a, int b) { return a > b ? a : b; }

int f(int p) OGO_INLINE {
	int rv = 0;
	if (p <= p) {
		rv = ((56 ^ ((((p | p)) | ((78 ^ p))))));
	} else {
		rv = p;
	}
	return rv;
}

int main(void) {
	int d = mx(gv << (81 / f(e)), 69);
	int t1 = f(56744 * f(cs));
	printf("d %d t1 %d\n", d, t1);
	return 0;
}
