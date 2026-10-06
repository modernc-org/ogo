// An element of a two-dimensional (or deeper) array of 64-bit integers is read and
// written wrong when its ROW index holds a function call:
//
//	int64_t r[2][2];
//	r[f(i)][j] = v;     // the store is lost
//	x = r[f(i)][j];     // the read gives garbage
//
// A one-dimensional array, a call in the column index only, an int32_t element, a
// struct row holding an int64_t, and a row index of arithmetic (`i * 1`, `i & 1`,
// `1 - i`) are all right. A cast around the call, `r[(int)f(i)][j]`, `r[f(i) + 0][j]`,
// a conditional expression calling only on one branch, and a function the backend
// is told to inline are wrong alike. Through the row's address,
// `(&r[f(i)][0])[j]`, through a row pointer bound to a variable, and with the row
// index bound to a variable first, it is right. Measured on a P2-EDGE 2026-10-06
// with the in-process flexcc (spin2cpp eb263961 with the carried optimize_ir.c.diff).
//
// It is silent, and it is what a checked OctoGo build writes for every element of
// an int64 or uint64 table indexed by a variable: the bound check, ogo_bound, is a
// call. A Kalman filter over 2x2 matrices of Q16.16 in int64 printed zeros on the
// board where the host and Go agreed. The emitter writes such a row as
// `(&r[I][0])` before it is indexed (wideRowC).
//
// gcc prints, and the target should print:
//
//	r1 4294967303 4294967304
//	r2 4294967303 4294967306
//	r3 4294967303 4294967306
//	r4 4294967303 4294967306
//	r5 7 10
//	read 10
//
// The target prints r2 and r3 as 0 0, and the read as garbage.

#include <stdio.h>
#include <stdint.h>

static int f(int i, int n) {
	if ((unsigned)i >= (unsigned)n) {
		printf("panic\n");
	}
	return i;
}

int main(void) {
	int64_t r1[4] = {0};
	int64_t r2[2][2] = {0};
	int64_t r3[2][2] = {0};
	int64_t r4[2][2] = {0};
	int32_t r5[2][2] = {0};
	int64_t q[2][2] = {{1, 2}, {3, 4}};
	int64_t v = 0x100000007LL;
	int64_t sum = 0;
	for (int i = 0; i < 2; i++) {
		r1[f(i, 4)] = v + i;
		for (int j = 0; j < 2; j++) {
			r2[f(i, 2)][f(j, 2)] = v + i * 2 + j;
			r3[f(i, 2)][j] = v + i * 2 + j;
			r4[i][f(j, 2)] = v + i * 2 + j;
			r5[f(i, 2)][f(j, 2)] = 7 + i * 2 + j;
			sum += q[f(i, 2)][j];
		}
	}
	printf("r1 %lld %lld\n", r1[0], r1[1]);
	printf("r2 %lld %lld\n", r2[0][0], r2[1][1]);
	printf("r3 %lld %lld\n", r3[0][0], r3[1][1]);
	printf("r4 %lld %lld\n", r4[0][0], r4[1][1]);
	printf("r5 %d %d\n", r5[0][0], r5[1][1]);
	printf("read %lld\n", sum);
	return 0;
}
