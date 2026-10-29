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
// It is not the int64's: any element wider than a long is read and written as
// wrong -- a struct of two or three int32 (s2, s3 below), and so a string header,
// an interface value and a slice header -- while one of a long or less, a struct
// of two int16 or of one int32 (h2, s1), is right, and so is the ADDRESS of a wide
// element, and a whole element read through it (addr). Measured 2026-10-24, when
// `c := grid[y][x]` of a [3][4] array of a two-field struct printed garbage on a
// P2-EDGE in every release; wideRowC takes the row's address for every element of
// more than four bytes since.
//
// gcc prints, and the target should print:
//
//	r1 4294967303 4294967304
//	r2 4294967303 4294967306
//	r3 4294967303 4294967306
//	r4 4294967303 4294967306
//	r5 7 10
//	read 10
//	s2 15 16 99 41 42
//	s3 22 23 24
//	h2 15 16
//	s1 8
//	addr 9 10 11 12
//
// The target prints r2 and r3 as 0 0, the read as garbage, s2's and s3's reads
// as garbage, and s2's element as 13 14: the store of a whole element is lost, and
// the store of a field of one is right.
//
// The cause is the one of doc/compound-row-element.c (2026-10-29): an assignment of
// a value wider than a long takes the rewrite of `M op= N`, which copies the row
// into a temporary of array type, holding nothing.

#include <stdio.h>
#include <stdint.h>

typedef struct { int32_t a, b; } S2;
typedef struct { int32_t a, b, c; } S3;
typedef struct { int16_t a, b; } H2;
typedef struct { int32_t a; } S1;

S2 q2[3][3] = {{{1, 2}, {3, 4}, {5, 6}}, {{7, 8}, {9, 10}, {11, 12}}, {{13, 14}, {15, 16}, {17, 18}}};
S3 q3[3][3] = {{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}, {{10, 11, 12}, {13, 14, 15}, {16, 17, 18}}, {{19, 20, 21}, {22, 23, 24}, {25, 26, 27}}};
H2 qh[3][3] = {{{1, 2}, {3, 4}, {5, 6}}, {{7, 8}, {9, 10}, {11, 12}}, {{13, 14}, {15, 16}, {17, 18}}};
S1 q1[3][3] = {{{1}, {2}, {3}}, {{4}, {5}, {6}}, {{7}, {8}, {9}}};

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
	int j = 1;
	S2 a = q2[f(2, 3)][j];
	q2[f(2, 3)][j + 1].b = 99;
	S2 sv = {41, 42};
	q2[f(2, 3)][0] = sv;
	printf("s2 %d %d %d %d %d\n", a.a, a.b, q2[2][2].b, q2[2][0].a, q2[2][0].b);
	S3 b = q3[f(2, 3)][j];
	printf("s3 %d %d %d\n", b.a, b.b, b.c);
	H2 c = qh[f(2, 3)][j];
	printf("h2 %d %d\n", c.a, c.b);
	S1 d = q1[f(2, 3)][j];
	printf("s1 %d\n", d.a);
	S2* pa = &q2[f(1, 3)][j];
	S2 pv = *(&q2[f(1, 3)][j + 1]);
	printf("addr %d %d %d %d\n", pa->a, pa->b, pv.a, pv.b);
	return 0;
}
