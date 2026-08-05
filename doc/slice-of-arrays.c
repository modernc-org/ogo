// A slice whose element is an ARRAY cannot be built on this target, and the reason
// is worth keeping because the construct LOOKS like it works.
//
// The C is straightforward. An array element has no inline spelling where a slice
// header's pointer goes -- `int (*ptr)[2]` puts the name in the middle of the
// declarator -- so a typedef moves it out of the way, exactly as one does for a
// function pointer:
//
//	typedef int ogo_arr_2_int[2];
//	typedef struct { ogo_arr_2_int* ptr; int len; int cap; } slice;
//
// gcc compiles that and every operation over it correctly. The target's compiler
// (spin2cpp v7.7.0, through this repository's backend) does not: it models
// `ogo_arr_2_int*` as a pointer to a POINTER, and says so when the two meet --
//
//	warning: incompatible pointer types in assignment:
//	expected pointer to pointer to int  but got pointer to array of int
//
// -- so the addresses it computes step by the wrong size.
//
// WHAT MAKES IT DANGEROUS is that it is not uniformly wrong. Measured on a P2-EDGE
// on 2026-08-05, this program answered correctly:
//
//	xs := make([][2]int, 3)
//	xs[0][1] = 7
//	xs[2][0] = 4
//	println(xs[0][1], xs[2][0], len(xs))     // 7 4 3, correct
//
// and this one did not:
//
//	sum := 0
//	for i, v := range xs { sum += i + v[0] + v[1] }
//	println(sum)                              // 36, where Go says 14
//
// A bare `append` was wrong too. So a small program can pass every check and a
// slightly larger one silently give different numbers -- which is worse than the
// construct not existing, and is why it is REFUSED rather than shipped with a
// warning attached. `chan [3]int` is refused for a related but distinct reason: the
// rendezvous copies its element by value, which C cannot do for an array at all.
//
// The support was written, measured, and reverted (85c5ea8, then its revert). What
// would make it viable is a representation that does not rely on the backend
// understanding a pointer to a typedef'd array -- a flat slice with a stride, or a
// struct wrapping the array as its only field, which is also the workaround to
// suggest to a user today.
//
// To re-measure: build this with the target backend and with gcc, and compare. The
// numbers below are the same shape as the OctoGo program above.

#include <stdio.h>
#include <string.h>

typedef int arr2[2];

typedef struct {
	arr2 *ptr;
	int len;
	int cap;
} slice;

static arr2 backing[3];

int main(void) {
	slice xs = {backing, 3, 3};
	memset(backing, 0, sizeof(backing));

	xs.ptr[0][1] = 7;
	xs.ptr[2][0] = 4;
	printf("%d %d %d\n", xs.ptr[0][1], xs.ptr[2][0], xs.len); // 7 4 3

	int sum = 0;
	for (int i = 0; i < xs.len; i++) {
		arr2 v;
		memcpy(v, xs.ptr[i], sizeof(v));
		sum += i + v[0] + v[1];
	}
	printf("%d\n", sum); // 14 on gcc
	return 0;
}
