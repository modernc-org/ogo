// A function PARAMETER of array type corrupts unrelated code elsewhere in the same
// program. This is the defect that made a slice of arrays look unimplementable, and
// it is much narrower than that: nothing about pointers to arrays is wrong.
//
//	cc -o t array-param-corrupts.c && ./t
//	sum 10 (want 10)   push 1 5        <- gcc, and what C says
//
//	flexcc -2 -Ono-inline-small -Ono-peephole -o t.binary array-param-corrupts.c
//	sum 24 (want 10)   push 1 5        <- on a P2-EDGE
//
// The wrong value is computed by the LOOP, which runs before `push` is ever called.
// Merely having a function whose parameter is an array in the translation unit is
// enough. Measured 2026-08-06 with this repository's in-process backend (spin2cpp
// v7.7.0) and the flags ogo build passes.
//
// Change the one parameter and the program is correct on the same board:
//
//	static slice push(slice s, arr2 v)         sum 24   WRONG
//	static slice push(slice s, const int *v)   sum 10   right
//
// The wrong value is not even stable across unrelated edits: with a typedef'd `arr2
// v` local in the loop instead of `int v[2]` the same board printed -1991913358.
//
// What is NOT the cause, each ruled out on hardware:
//
//   - a pointer to a typedef'd array. `arr2 *ptr` in a struct, indexed `ptr[i][j]`,
//     is correct on the board.
//   - a typedef'd array LOCAL. Replacing `arr2 v` with `int v[2]` inside the loop
//     changes nothing; it is still wrong.
//   - the memcpy. The same copies are correct once the parameter is a pointer.
//
// ogo already avoids array parameters everywhere else, and always did: a user's
// `func take(a [3]int)` is emitted as `int take(int* _ogo_a)` with a memcpy on entry,
// which is Go's copy semantics and this defect's workaround at the same time. The
// only array-typed parameter the compiler ever emitted was in the generated `append`
// helper for a slice whose element is an array -- see doc/slice-of-arrays.c, whose
// diagnosis this file corrects.
//
// So a slice of arrays is implementable. Either keep the element typedef and give
// the helpers pointer parameters, or use a flat representation -- an `int*` to the
// innermost element plus a stride, indexing `ptr[i*stride + j]` -- which has no
// array-typed anything and is how C models this normally.
//
// Worth reporting upstream: the corruption is silent, reaches code that never
// touches the offending function, and the only hint is an unrelated-looking warning,
// "variable v may be used before it is set".

#include <stdio.h>
#include <string.h>

typedef int arr2[2];

typedef struct {
	arr2 *ptr;
	int len;
	int cap;
} slice;

// The parameter that does it. As `const int *v` the program is correct.
static slice push(slice s, arr2 v) {
	if (s.len < s.cap) {
		memcpy(s.ptr[s.len], v, sizeof(s.ptr[s.len]));
		s.len++;
	}
	return s;
}

static arr2 back[3];

int main(void) {
	memset(back, 0, sizeof back);
	slice xs = {back, 3, 3};
	xs.ptr[0][1] = 7;

	// This loop is what comes out wrong, and it runs before push is called.
	int sum = 0;
	for (int i = 0; i < xs.len; i++) {
		int v[2];
		memcpy(v, xs.ptr[i], sizeof(v));
		sum += i + v[0] + v[1];
	}
	printf("sum %d (want 10)\n", sum);

	arr2 r;
	r[0] = 5;
	r[1] = 6;
	slice ys = {back, 0, 3};
	ys = push(ys, r);
	printf("push %d %d\n", ys.len, ys.ptr[0][0]);
	return 0;
}
