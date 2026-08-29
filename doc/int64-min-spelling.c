// The most negative 64-bit value has two spellings as a C expression, and the
// target's compiler gets exactly one of them right with its small-function inliner
// on. Valid C, and gcc computes both:
//
//	                                    gcc     flexcc -2      flexcc -2 -Ono-inline-small
//	(-9223372036854775807LL - 1)        MIN     MIN + 2^32     MIN
//	(-1 - 9223372036854775807LL)        MIN     MIN            MIN
//
// where MIN is -9223372036854775808 and the wrong value is -9223372032559808512:
// the high word has one more in it, as if the borrow of the "- 1" ran the wrong
// way. Measured on a P2-EDGE 2026-08-29 against the flexcc in internal/flexcc
// (spin2cpp 2bd01c4c) and against the previous v7.7.0 transpile, which agree on
// every cell -- so it is not the regeneration that changed anything but the flag
// that went with it. `ogo build` passed -Ono-inline-small from v0.14.0 to that
// day for an unrelated defect, and this one was hidden underneath it: the run case
// "a constant too wide for a C int" printed 9223372041149743104 for a uint64 that
// should have been 9223372036854775808 the first time the suite ran without the
// flag. The compiler now spells the value the second way (signed64Lit in
// internal/octogo/emit.go).
//
// Every position was measured for the spelling that is kept, and all are right
// under both backends with the inliner on: an initializer, an operand beside a
// variable, an argument, a return value, a comparison, a divisor, a shift, an
// equality test, and the same value held in a `static const int64_t` and read
// from there. The inliner does NOT otherwise feed the 64-bit constant-fold fault
// of doc/int64-constant-fold.c: a value returned by a call it inlines, `x =
// id(5)`, adds and multiplies with wide literals exactly as one returned by a
// call it cannot inline, which was the first thing to rule out.
//
// A measuring note, since it cost an hour: print a 64-bit value through a
// VARIABLE, never as `(long long)(expr)` -- the cast of a 64-bit expression to a
// 64-bit type is the battery's oldest fault, and it turns every line of a
// reproducer into garbage that looks like the fault being measured.
//
// To check, build for the P2 with no -O flag and read the two numbers.

#include <stdio.h>
#include <stdint.h>

int main(void) {
	int64_t a = (-9223372036854775807LL - 1);
	int64_t b = (-1 - 9223372036854775807LL);
	printf("%lld\n", a);
	printf("%lld\n", b);
	return 0;
}
