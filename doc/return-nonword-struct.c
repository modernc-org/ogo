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
// and says "unknown type" about it. What matters is that the two positions differ in
// consequence:
//
//	position                                          warns   code correct
//	----------------------------------------------------------------------
//	assigning a function to a function pointer          yes         YES
//	returning a call's struct result directly           yes          NO
//
// So a warning in this family is NOT reliably cosmetic. The function-pointer one was
// checked against real hardware and its values are right; this one was assumed to be
// the same thing and was not. Re-measure each position on the board before calling
// any of them harmless -- and prefer a workaround that makes the warning go away,
// since silence is then evidence rather than a suppressed signal. This is also why
// ogo does not cast to quiet the function-pointer case: a cast would hide a
// diagnostic that has now been shown to catch a real defect.
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
