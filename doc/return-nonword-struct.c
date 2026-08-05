// Returning a call's STRUCT result directly loses members narrower than a machine
// word. This is a miscompile, not a diagnostic.
//
// Measured 2026-08-05 on a P2-EDGE through the OctoGo program below, built by this
// repository's in-process backend (spin2cpp v7.7.0) with the flags ogo build passes:
//
//	func flags(n int) (int, bool) { return n * 2, n > 0 }
//	func fwd(n int) (int, bool)   { return flags(n) }
//	...
//	v, ok := fwd(3)   // Go and gcc: 6 true.  P2, direct form: 6 FALSE.
//
// The `_Bool` member comes back false. The int beside it survives, which is what
// makes it easy to miss: half the value is right. The run case "a multi-result call
// forwarded as a return" is that program, and it fails on the board against the
// direct form and passes against the bound one.
//
// This file is the C reduced from it. gcc prints `direct 6 1   bound 6 1` -- both
// correct -- and the target's compiler warns on the direct form only:
//
//	warning: incompatible pointer types in return: expected unknown type
//	but got reference to unknown type
//
// The workaround, which is what ogo emits: bind the call to a temporary and return
// that. `ogo_ret_X t = f(); return t;` is correct on the board AND compiles
// silently, so the warning disappearing is a second confirmation that the direct
// form was the problem. See emitReturn in internal/octogo/emit.go.
//
// It is the same underlying weakness as doc/funcptr-nonword-struct.c -- the target's
// compiler cannot resolve a struct typedef whose members are not all machine words,
// and says "unknown type" about it. Every position a struct of that shape can reach
// was then swept, on the board, by the run case "a struct with a sub-word field, in
// every position":
//
//	position                                       warned   was correct
//	-------------------------------------------------------------------
//	assigned from a call to a local/global/field/element   no       yes
//	returned directly from a call                        yes        NO
//	passed by value from a call                          yes        NO
//	returned through an interface method (the thunk)      yes        NO
//	sent on a channel from a call                        yes       yes
//	returned by a function VALUE                         yes       yes
//
// So the warning is a SIGNAL, not a verdict: it fired on five positions, three of
// which were broken. It is also not the only signal -- the silent positions were all
// correct, but that is luck rather than a rule, which is why the sweep ran them too.
//
// The three broken ones are fixed by the same move: bind the call to a temporary.
// That is correct everywhere and, in all but the function-value case, silent -- so
// the warning going away is a second confirmation. The channel send was bound for
// the same reason even though it was already right, since silence is worth more than
// a warning nobody can act on.
//
// What is left warning is the function value, which doc/funcptr-nonword-struct.c
// measured and found cosmetic. ogo does NOT cast it quiet: a cast would suppress a
// diagnostic that has now caught three real defects.
//
// To re-measure, compile with the target backend and read the two numbers.

#include <stdio.h>

typedef struct {
	int _0;
	_Bool _1;
} ret;

ret flags(int n) {
	ret r = {n * 2, n > 0};
	return r;
}

// The direct form: what `return flags(n)` reads as.
ret fwd_direct(int n) { return flags(n); }

// The bound form: what ogo emits instead.
ret fwd_bound(int n) {
	ret t = flags(n);
	return t;
}

int main(void) {
	ret d = fwd_direct(3);
	ret b = fwd_bound(3);
	printf("direct %d %d   bound %d %d\n", d._0, (int)d._1, b._0, (int)b._1);
	return 0;
}
