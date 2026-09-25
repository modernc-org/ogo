// A compound literal handed to a call by value costs the target a cog register
// pair AT EVERY CALL, never given back. flexcc gives each C local a register
// (`local_N res 1` in COG_BSS) out of a pool the assembler checks with `fit 480`,
// and it reuses a SCALAR temporary's -- a hundred blocks of `int t = f(k)` spend
// 21 -- but not the struct a compound literal argument is built in. So a hundred
// calls taking `(S){k, 1}` fail to build:
//
//	complit-arg-registers.p2asm: error: fit 480 failed: pc is 543
//
// having spent 210, where the same hundred calls taking a static S spend 21, and
// a variable or the two ints as scalars as little. Built with the flexcc in
// internal/flexcc (spin2cpp v7.7.3, scripts/flexcc -2), 2026-09-25; build with
// -DSTATIC to see the other side.
//
// The cost grows with the FUNCTION, not with anything live at once: every
// argument of every call is still charged at the end. Measured on OctoGo's own
// output, where every constant string was such a literal, `(ogo_string){"s",
// n}`: 25 calls of strings.HasPrefix(line, "...") and 25 prints spent 276 and
// failed, and a function of 200 such calls took FIVE MINUTES to be refused with
// "Internal error exceeded local register limit" -- the time grows about as the
// cube of the calls. OctoGo names a file-scope header for each constant string
// since (stringLitName in internal/octogo/emit.go). A slice header of an array, a
// struct literal and an interface value are compound literals too, and remain:
// sixty of `sum((ogo_slice_int){arr, 8, 8})` in one function fail to build.
//
// A related cost, not shown here: a small function the optimizer INLINES brings
// its struct parameter as a new local at every call site, so sixty calls of an
// inlined `int area(P p)` spend 133 even when the argument is a variable.
//
// To check whether this is still so, compile this with and without -DSTATIC and
// count the `res 1` lines of the .p2asm, or read the error.

#include <stdio.h>

typedef struct {
	int a, b;
} S;

__attribute__((noinline)) int g(S s) { return s.a + s.b; }

#ifdef STATIC
static S lit = {1, 1};
#define ONE n += g(lit);
#else
#define ONE n += g((S){n, 1});
#endif

#define TEN ONE ONE ONE ONE ONE ONE ONE ONE ONE ONE
#define HUNDRED TEN TEN TEN TEN TEN TEN TEN TEN TEN TEN

int main(void) {
	int n = 0;
	HUNDRED
	printf("%d\n", n);
	return 0;
}
