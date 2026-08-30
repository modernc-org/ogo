// An integer constant passed to a 64-bit parameter is passed as ONE word when an
// argument before it is an arithmetic expression of 64-bit type. Valid C; gcc
// computes every line:
//
//	                                gcc      flexcc -2
//	mix(m, 3)                       220      220
//	mix(-m, 3)                      -214     -1078972716209405732   <- warned: "Bad number of parameters in call to mix: expected 4 found 3"
//	mix((0 - m), 3)                 -214     -1078972716209405732   <- warned
//	mix(m + 1, 3)                   251      -1078972716209405732   <- warned
//	mix((m * 2), 3)                 437      -359516817401577686    <- warned
//	mix((0 - m), 3 + 4)             -210     wrong                  <- warned
//	mix((0 - m), -3)                -220     wrong                  <- warned
//	mix((0 - m), 3u)                -214     wrong                  <- warned
//	mix3((0 - m), 3, 4)             -192     wrong                  <- warned, expected 6 found 5
//	umix(u * 2, 3)                  437      wrong                  <- warned; unsigned the same
//	take3(1, (0 - m), 3)            -213     -359516817401577685    <- warned; a 32-bit parameter first changes nothing
//	mix((0 - m), 3LL)               -214     -214                   the constant carrying its width
//	mix((0 - m), (int64_t)3)        -214     -214                   or cast
//	mix((0 - m), k)                 -214     -214                   an int32_t VARIABLE is converted
//	mix(3, (0 - m))                 86       86                     the constant first
//	mix((0 - m), m)                 -210     -210
//	t = (0 - m); mix(t, 3)          -214     -214                   the expression bound to a variable first
//	mix(id64(3), 3)                 96       96                     a CALL before the constant is fine
//	mix(mix(m, 1), 3)               6761     6761
//	g2((0 - m), 3)                  -214     -214                   an int32_t parameter after the expression is fine
//	fl((0 - m), 1.5f)               -214     -214                   so is a float, a double, an int, a string
//	fp((0 - m), 3), fp(m, 3)        -214/220 REFUSED               through a function pointer: "error: Bad number of parameters"
//
// for an int64_t m of 7 that the compiler cannot see through, mix(a, b) being
// a * 31 + b, mix3 a * 31 + b * 7 + c, take3(p, a, b) p + a * 31 + b. The
// converting of a constant argument to its parameter's type stops after an
// argument that is an arithmetic expression of 64-bit type -- a negation, a sum, a
// product -- and the constant after it fills one of the parameter's two words, so
// the callee reads garbage for it and for every parameter after it. A variable, a
// call result or a constant BEFORE the expression is converted as C says.
//
// Measured on a P2-EDGE 2026-08-30 against spin2cpp 2bd01c4c, flexcc -2 with no
// other flag, as ogo build invokes it. The warning is the only sign; the binary
// is built and runs.
//
// It reaches ordinary OctoGo as `mix(-m, 3)`, `f(m+1, 3)`, or any constant
// argument to an int64/uint64 parameter after an expression, found by a hashing
// probe diffed against Go: `mix(-m, 3)` printed -192 for -5260211717565488541.
// WORKED AROUND in emitCallArgs (wideConstArg): a constant argument to a 64-bit
// parameter is spelled with the parameter's width, `3LL`/`3ULL`, in every
// position; that is also what lets a call through a function value pass a constant
// at all. Unreported upstream as of this writing.
//
// To check, build for the P2 and read the second number: -214 is right.

#include <stdio.h>
#include <stdint.h>

__attribute__((noinline)) int64_t id64(int64_t v) { return v; }
__attribute__((noinline)) int64_t mix(int64_t a, int64_t b) { return a * 31 + b; }

int main(void) {
	int64_t m = id64(7);
	int64_t a = mix(m, 3);
	int64_t b = mix(-m, 3);
	int64_t c = mix(-m, 3LL);
	printf("%lld\n", a);
	printf("%lld\n", b);
	printf("%lld\n", c);
	return 0;
}
