// A FUNCTION NAME passed as an argument to a call made through a function
// POINTER reaches the callee as garbage: calling it returns nonsense or never
// returns at all.
//
//	cc -o t funcptr-arg-to-indirect-call.c && ./t
//	before
//	after 6       <- gcc, and what C says
//
//	flexcc -2 -o t.binary funcptr-arg-to-indirect-call.c
//	before
//	after -2063597568    <- on a P2-EDGE
//
// Measured through this repository's in-process backend (spin2cpp v7.7.3) on a
// P2-EDGE, 2026-09-22, in the shapes around it:
//
//   - apply(dbl) through a local or a global function pointer: wrong, and without
//     the "before" line it printed nothing at all, as did apply2(dbl, 5) with a
//     second argument -- the program does not return from the call
//   - lit0(dbl), the same function called DIRECTLY with the name: right
//   - f(3) through a function pointer with a scalar argument: right
//   - ft0 d = dbl; apply(d), the name bound to a variable first: right
//
// The last is the workaround. OctoGo reaches the shape wherever a function value
// is called with a function as the argument -- `apply(sum)` for a literal `apply`
// taking a `func(...int) int`, a callback registered through a table, a method
// value handed to a handler held in a field -- and the host's C compiler gets it
// right, so only the board showed it: a run case printed the other literal's
// result, and the smallest OctoGo program printing apply(dbl) printed 3160 for 6.
// A call through a function VALUE or an interface slot binds an argument of
// function type that is not already a variable to a temporary first; see
// indirectFuncArg in internal/octogo/emit.go.
//
// To check whether the workaround is still needed, run this on a board and read the
// number after "after".

#include <stdio.h>

typedef int (*ft0)(int);
typedef int (*ft1)(ft0);

int lit0(ft0 g) { return g(3); }

int dbl(int n) { return n * 2; }

int main(void) {
	ft1 apply = lit0;
	printf("before\n");
	int r = apply(dbl);
	printf("after %d\n", r);
	return 0;
}
