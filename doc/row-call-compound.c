// A COMPOUND assignment to an element of an array of two or more dimensions, an
// index of which BEFORE THE LAST holds a function call, stores nothing:
//
//	int32_t r2[3][4];
//	r2[f(i)][1] += 1;   // lost
//	r2[f(i)][1]++;      // right
//	r2[i][f(j)] += 1;   // right: the call in the last index
//	a1[f(i)] += 1;      // right: one dimension
//
// Measured for 16- and 32-bit elements, the row of two dimensions and the first and
// the middle index of three; an 8-bit and a 64-bit element of the same shape are
// refused instead, "Array dereferences on non-array" -- the 64-bit one through the
// row's address too, `(&w[f(i)][0])[1] += 5`, the spelling wideRowC writes for a
// read or a plain store. Through a pointer to the element bound first,
// `int32_t *p = &r2[f(i)][1]; *p += 1`, and with the row index bound to a variable
// first, it is right. Measured on a P2-EDGE 2026-10-15 with the in-process flexcc
// (spin2cpp b5993a84 with the carried diffs).
//
// It reached OctoGo programs through `++` and `--`, which the emitter writes `x += 1`
// where the target is no plain name (incDecC, doc/incdec-64-cast.c), of an element
// whose row index read a field through a pointer -- the nil check is a call -- or
// called a function: a data logger's histogram, `hist[r.Ch][k]++`, printed zeros on
// the board where the host and Go agreed. The emitter binds such a target's address
// first (hoistCompoundTarget, rowIndexCalls), as it did a compound assignment's.
//
// The cause is the one of doc/compound-row-element.c, the compound read: the row is
// copied into a temporary of array type, which holds nothing (2026-10-29).
//
// gcc prints, and the target should print, for each i of 0, 1, 2:
//
//	i: a1=1 c2=1 r2=1 t3=1 m3=1 h2=1
//
// The target prints r2, t3, m3 and h2 as 0.

#include <stdio.h>
#include <stdint.h>

static int f(int i, int n) {
	if ((unsigned)i >= (unsigned)n) {
		printf("panic\n");
	}
	return i;
}

int32_t a1[4];       /* one dimension, a call in the index */
int32_t c2[3][4];    /* two, a call in the column only */
int32_t r2[3][4];    /* two, a call in the row */
int32_t t3[2][3][4]; /* three, a call in the first */
int32_t m3[2][3][4]; /* three, a call in the middle */
uint16_t h2[3][4];   /* two of 16 bits, a call in the row */

int main(void) {
	for (int i = 0; i < 3; i++) {
		a1[f(i, 4)] += 1;
		c2[i][f(1, 4)] += 1;
		r2[f(i, 3)][1] += 1;
		t3[f(1, 2)][i][1] += 1;
		m3[1][f(i, 3)][1] += 1;
		h2[f(i, 3)][1] += 1;
	}
	for (int i = 0; i < 3; i++) {
		printf("%d: a1=%d c2=%d r2=%d t3=%d m3=%d h2=%d\n", i, (int)a1[i], (int)c2[i][1], (int)r2[i][1], (int)t3[1][i][1], (int)m3[1][i][1], (int)h2[i][1]);
	}
	return 0;
}
