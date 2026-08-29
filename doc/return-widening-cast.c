// A function whose whole return operand is a WIDENING CAST to a 64-bit type returns
// a garbage high word on the P2. Valid C, and gcc computes every line:
//
//	int64_t wide(int n) { return (int64_t)n; }         wide(5)   -1042943919190441979  <- wrong
//	int64_t wvar(int n) { int64_t r = n; return r; }   wvar(5)   5
//	int64_t big(int v)  { return (int64_t)v * 1000000000; }        3000000000, right
//	int64_t sum(int64_t a, int64_t b) { return a + b; }            3, right
//
// So it is not "an expression in a return" and not "a cast in a return": the cast
// has to be the ENTIRE operand. The same cast inside a larger expression is fine,
// and the same value bound to a variable of the result type first is fine. It is
// the same for an unsigned result, `return (uint64_t)n;`, and it does not depend
// on the caller: the value is wrong stored into a variable, passed straight to
// printf, and consumed by arithmetic, and it is wrong whether or not the function
// is inlined (`__attribute__((noinline))` changes the garbage, not the fact).
//
// Measured on a P2-EDGE 2026-08-29 against spin2cpp 2bd01c4c at -O0, -O1, the
// default and with -Ono-inline-small -Ono-peephole: wrong at every setting, so it
// is the code generator rather than an optimization, and the v7.7.0 transpile is
// the same.
//
// It reaches ordinary OctoGo as `return int64(n)` -- the base case of every
// recursive function that widens its argument, which is how it was found: a
// fib(n) int64 printed 282076272138861 for 6765 in a driver probe diffed against
// Go on the board. The helpers in emit.go had routed around it since 2026-08-25
// ("never widen, negate or cast inside what a return returns"), and user code had
// not, because no run case returned a bare conversion from a 64-bit function.
//
// WORKED AROUND in emitReturnValue: a 64-bit result that is not a plain variable
// is bound to a temporary of the result type and that is returned. Unreported
// upstream as of this writing.
//
// To check, build for the P2 and read the first number.

#include <stdio.h>
#include <stdint.h>

int64_t wide(int n) { return (int64_t)n; }
int64_t wvar(int n) { int64_t r = n; return r; }
int64_t big(int v) { return (int64_t)v * 1000000000; }
int64_t sum(int64_t a, int64_t b) { return a + b; }

int main(void) {
	printf("%lld\n", wide(5));
	printf("%lld\n", wvar(5));
	printf("%lld\n", big(3));
	printf("%lld\n", sum(1, 2));
	return 0;
}
