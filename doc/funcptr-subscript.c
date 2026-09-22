// A SUBSCRIPT through a POINTER to function pointers ignores its index: p[i] is
// element 0 whatever i is, read or written.
//
//	cc -o t funcptr-subscript.c && ./t
//	6 25 25
//	10 25 10      <- gcc, and what C says
//
//	flexcc -2 -o t.binary funcptr-subscript.c
//	10 10 25
//	10 6 25       <- on a P2-EDGE
//
// The first line reads the table through p with a constant index, a variable one and
// the spelling *(p + i); the second stores through p[1] and p[i] and reads the table
// back. On the board both subscripted reads read element 0 and both stores wrote it,
// the second over the first: only *(p + i) found the element it named.
//
// The cause is in the backend, backends/asm/outasm.c of spin2cpp at eb263961 (the
// v7.7.3 this repository's backend is transpiled from), CompileExpression's
// AST_ARRAYREF case:
//
//	if (IsFunctionType(ExprType(expr->left))) {
//	    return base;
//	}
//
// which returns the base and drops the offset. It is there for (*f)(), a function
// pointer dereferenced as ARRAYREF(MEMREF(functype, f), 0), where the index is always
// zero. But IsFunctionType answers yes for a POINTER to a function as well, and the C
// frontend types p[i] for a `fn *p` as ARRAYREF(MEMREF(fn, p), i), fn being a pointer
// to a function -- so every subscript through such a pointer loses its index. Asking
// for the function type itself,
//
//	AST *ltype = RemoveTypeModifiers(ExprType(expr->left));
//	if (ltype && ltype->kind == AST_FUNCTYPE) {
//	    return base;
//	}
//
// prints gcc's lines for this file when built natively, and upstream's `make
// test_offline` passes with it as without, 552 tests. A native build of eb263961
// without it prints the board's lines above, as this repository's in-process
// backend does.
//
// What is NOT affected: a subscript of an ARRAY of function pointers read into a
// variable, `fn f = table[i]` (the left side is the array, not a MEMREF), and
// *(p + i), whose index is zero. A CALL made directly on an element, table[i](x),
// has a fault of its own in the same family -- FindFuncSymbol walks through the
// subscript to the table's name -- which doc/call-through-array-element.c records,
// and a call through a pointer to a function pointer, (*pp)(x), calls through pp
// itself for the same reason.
//
// OctoGo reaches the shape through every slice of functions: a slice's elements are
// reached through its header's pointer, so `handlers[i]` of a []func(int) int was
// handlers[0] on the board, in a dispatch table, a range over callbacks, a variadic
// parameter of funcs, an append of one, a slice field -- while the host's C compiler
// got them all right, and an array of functions, the form the run cases used, was
// fine. Found by a board sweep of function values (2026-09-22): a range over a
// []func printed 60810 for Go's 60525. A slice element of function type is spelled
// *(s.ptr + (i)); see elemAtC in internal/octogo/emit.go.
//
// To check whether the workaround is still needed, run this on a board and compare
// the lines with gcc's.

#include <stdio.h>

typedef int (*fn)(int);

int dbl(int n) { return n * 2; }

int inc(int n) { return n + 1; }

int sq(int n) { return n * n; }

fn table[3] = {dbl, inc, sq};

int main(void) {
	fn *p = table;
	int i = 2;
	fn a = p[1], b = p[i], c = *(p + i);
	printf("%d %d %d\n", a(5), b(5), c(5));
	p[1] = sq;
	p[i] = dbl;
	fn t0 = table[0], t1 = table[1], t2 = table[2];
	printf("%d %d %d\n", t0(5), t1(5), t2(5));
	return 0;
}
