// A COMPOUND assignment whose OPERAND is an element of an array of two or more
// dimensions, read through a row index that holds a function call, loads nothing:
//
//	int32_t m[3][4];
//	x -= m[f(i)][2];       // lost: x minus a register no instruction wrote
//	x -= (m[f(i)][2]);     // lost
//	x -= m[f(i)][j];       // lost, the column a variable
//	x -= m[f(i)][0];       // right: column 0, whose address is the row's
//	x = x - m[f(i)][2];    // right: the plain form
//	x -= m[i][2];          // right: no call in the row index
//	x -= m[f(i)][2] + 1;   // right: a sum, a product, a negation, a conversion
//	int32_t t = m[f(i)][2]; x -= t;   // right: bound first
//
// The listing shows it: `sub local01, local02` with no instruction writing local02.
// Measured for 32-bit elements, the row of two dimensions and the middle index of
// three, every operator but `*=`, for a 64-bit target too; a 16-bit element of the
// same shape crashed the backend and an 8-bit one was refused, "Array dereferences
// on non-array"; 64-bit elements and float64 are lowered otherwise and are right.
// It is the transform that rewrites `M op= N` into `M := M op N`, which spin2cpp
// 2bd01c4 fixed for an array MEMBER reached through a call (flexprop#106,
// doc/compound-call-index.c): a ROW is an array-typed value reached through a call
// in the same way, and was not covered. Measured on a P2-EDGE 2026-10-29 with the
// in-process flexcc (spin2cpp b5993a84 with the carried diffs), the pin of
// v1.0.0-rc.2.
//
// The cause, read the same day in upstream master a7a41364, which still has it:
// ExtractSideEffects (frontends/hltransform.c) copies an ARRAYREF's base that has
// side effects into a temporary `_arr_` of the base's own type, and for a row that
// type is an array, which no temporary holds -- the call is made and nothing is
// copied, so the element read is a register nothing wrote. A temporary that is a
// pointer to the row's first element, holding its address (ArrayToPointerType, as
// the function's AST_CONDRESULT case writes one), prints gcc's lines for this file
// in a native build, and so for doc/row-call-compound.c (the compound store),
// doc/wide-row-call-index.c (an element wider than a long, whose plain assignment
// takes the same rewrite) and doc/ptr-to-array-through-call.c (an array a call's
// pointer reaches): one cause. Prepared as an upstream PR, held until the open ones
// are answered.
//
// It reaches OctoGo programs because the call is the compiler's own: a checked
// build -- the default -- writes every index but a constant one through ogo_bound,
// so `eg -= palette[bestIndex][1]` is `eg -= palette.ptr[ogo_bound(bestIndex,
// palette.len)][1]`. image/draw's Floyd-Steinberg error diffusion, ported for dom81,
// dithered to noise on the board where the host and Go agreed. The emitter binds
// such an operand to a temporary (rowElement, emitHoistedCompound).
//
// gcc prints, and the target should print, for each i of 0, 1, 2:
//
//	0: z1=996 z2=997 z3=959 z4=996 z6=997 z7=997 z8=999 z9=997
//	1: z1=993 z2=993 z3=958 z4=992 z6=993 z7=993 z8=995 z9=993
//	2: z1=990 z2=989 z3=957 z4=988 z6=989 z7=989 z8=991 z9=989
//
// The target prints z1, z2, z3, z4 and z9 wrong.

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static int f(int i, int n) {
	if ((unsigned)i >= (unsigned)n) {
		printf("panic: index out of range\n");
		abort();
	}
	return i;
}

static int32_t g2[3][4] = {{1, 2, 3, 4}, {5, 6, 7, 8}, {9, 10, 11, 12}};
static int32_t g3[2][3][4];

int z1(int i, int j) { int32_t a = 1000; a -= g2[f(i, 3)][f(j, 4)]; return a; }  /* a variable column */
int z2(int i) { int32_t a = 1000; a -= g2[f(i, 3)][2]; return a; }                /* column 2 */
int z3(int i) { int32_t a = 1000; a -= g3[f(1, 2)][f(i, 3)][2]; return a; }       /* three dimensions */
int z4(int i) { int32_t a = 1000; a -= (g2[f(i, 3)][3]); return a; }             /* parenthesised */
int z6(int i) { int32_t a = 1000; a -= g2[i][2]; return a; }                      /* no call */
int z7(int i) { int32_t a = 1000; a = a - g2[f(i, 3)][2]; return a; }             /* the plain form */
int z8(int i) { int32_t a = 1000; a -= g2[f(i, 3)][0]; return a; }                /* column 0 */
int64_t z9(int i) { int64_t a = 1000; a -= g2[f(i, 3)][2]; return a; }            /* a 64-bit target */

int main(void) {
	for (int i = 0; i < 3; i++) {
		g3[1][i][2] = 41 + i;
	}
	for (int i = 0; i < 3; i++) {
		printf("%d: z1=%d z2=%d z3=%d z4=%d z6=%d z7=%d z8=%d z9=%d\n", i, z1(i, 3 - i), z2(i), z3(i), z4(i), z6(i), z7(i), z8(i), (int)z9(i));
	}
	return 0;
}
