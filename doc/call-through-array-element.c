// A call made directly through an ARRAY ELEMENT of function-pointer type reaches
// the wrong function: every element calls whatever the first one holds.
//
//	cc -o t call-through-array-element.c && ./t
//	9 3 18        <- gcc, and what C says
//
//	flexcc -2 -o t.binary call-through-array-element.c
//	9 9 9         <- on a P2-EDGE
//
// Measured through this repository's in-process backend (spin2cpp v7.7.0) on a
// P2-EDGE, in every shape tried:
//
//   - a constant index, table[0](6, 3), and a variable one, table[i](6, 3)
//   - a table filled by assignment, and one filled at package initialization
//     (static Op table[2] = {add, sub})
//
// What is NOT affected, and is the workaround: binding the element to a variable
// first. `Op f = table[i]; f(6, 3)` calls the right function every time, as does a
// function value held in a plain variable that was never in an array.
//
// ogo therefore never emits the direct form. A call whose callee is a chain ending
// in an element of function-pointer type binds that element to a temporary; see the
// CallSuffix case of chainCText in internal/octogo/emit.go.
//
// This is the defect a dispatch table runs into, which is most of the reason to put
// functions in an array at all. It went unnoticed because the host C compiler gets
// it right: the emit-and-run tests passed, and only TestOnBoard disagreed.
//
// To check whether the workaround is still needed, run this on a board and read the
// numbers.

#include <stdio.h>

typedef int (*Op)(int, int);

int add(int a, int b) { return a + b; }

int sub(int a, int b) { return a - b; }

int mul(int a, int b) { return a * b; }

static Op table[3];

int main(void) {
	table[0] = add;
	table[1] = sub;
	table[2] = mul;
	for (int i = 0; i < 3; i++) {
		printf("%d ", table[i](6, 3));
	}
	printf("\n");
	return 0;
}
