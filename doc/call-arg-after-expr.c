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
//	mix((0 - m), 0LL)               -217     -1078972716209405735   <- warned: a ZERO loses its width, LL or not
//	mix((0 - m), (int64_t)0)        -217     -217                   a cast keeps it
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
// ZERO IS DIFFERENT, found 2026-09-15 when the fuzzer's board sweep built seed 140
// with this warning under the workaround: every spelling of zero but a cast --
// `0LL`, `0ULL`, `(0)`, `'\0'`, `(1LL - 1)` -- is narrowed back to one word in that
// position, so the workaround's own `0LL` was the fault again: `mix((0 - m), 0LL)`
// is -1078972716209405735 and `umix(u * 2, 0ULL)` 18446744073709551399, for -217
// and 434. `(int64_t)0` is passed whole; so is a zero with another wide constant
// after it, `mix3((0 - m), 0LL, 4LL)`, and no other value measured -- 1, -1, 511,
// 512, 65535, INT_MAX, 2^31, 2^32 - 1, INT64_MIN -- does this. wideConstArg spells a
// zero to a 64-bit parameter as the cast.
//
// THE CAUSE, read 2026-10-16 in spin2cpp's frontends/types.c (FixupFunccallTypes)
// after a domain program over Spin2 drivers met a wider form of the fault: an
// argument the compiler computes as a PAIR of words -- every 64-bit operation, the
// helper calls int64_add and the rest returning two results, and a shift by 32,
// which HandleTwoNumerics rewrites into the pair itself -- is typed as a tuple,
// and the walk over the arguments matches a tuple word by word against the
// parameters: the pair fills the 64-bit parameter, and the walk then moves on to
// the parameter after the next one while the next ARGUMENT is still unread. Every
// argument after the pair is converted to the type of the parameter after its own,
// or, past the last parameter, to none. The constant above lost its width that
// way, and every other conversion is lost the same way:
//
//	                                    gcc      flexcc -2
//	f3((0 - m), 2.5f, k)                30493    29993       the float converted to an int64: 2.5 -> 2
//	f3((0 - m), fl, k)                  30493    29993       a float VARIABLE the same
//	f4((0 - m), 2.5f, i)                40493    39993
//	f5((0 - m), i, 2.5f)                50393    838910793   i converted to a float
//	f7((0 - m), k, fl)                  50293    419430393   <- warned: "expected 5 found 4"
//	kf((0 - m), 3)                      255      249         3 passed as its bits
//	f3(m, fl, k)                        30507    30507       a variable is no pair
//
// for f3(int64_t a, float b, int64_t c), f4 the same with an int32_t c, f5(int64_t
// a, int32_t b, float c), f7(int64_t a, int64_t b, float c) and kf(int64_t a,
// float b), each summing what it is handed in its own decimal digits. Over thirty
// shapes of the first argument, each followed by a float variable and an int64_t
// one: a pair is every binary operator, `-m`, and a NEGATIVE constant, `(-5LL)`,
// which is a negation, `(int64_t)(-5LL)` with it; no pair is a variable, `~m`, a
// cast of a 32-bit value or of an operation, `(int64_t)(m + 1)`, a dereference, a
// field, an element, a call, a constant that is not negative, `(int64_t)-5`, a
// conditional. The shift by 32, `(uint64_t)x << 32`, also warns where nothing
// follows it, "Unable to widen function result" -- the pair's first word is the
// literal 0, a long -- and is passed right there.
//
// It reached OctoGo as `scale(-m, 3)` and `f(-m, gain, k)` for a float32 gain,
// every one silent, and as `f(uint64(rpm)<<32, d)`, which the warning refused.
// WORKED AROUND 2026-10-16 in emitCallArgs and at the receivers (pairArg): an
// argument to a 64-bit parameter that is an operation, a negative constant
// included, is bound to a temporary first wherever an argument follows it, and a
// shift by 32 wherever it stands; so is a 64-bit value receiver, which is the
// first argument. wideConstArg's spellings stay: through a function pointer a
// constant is refused without them. Unreported upstream as of this writing.
//
// A FUNCTION POINTER A CALL RETURNS is worse, found the same day by the fuzzer's
// board sweep of the part written for the above (genPairs). A checked build calls
// a function value through its nil check, `ogo_nil_..(cb)(...)`, which is such a
// pointer, as `pick()(...)` is; there every argument of 64 bits but a variable is
// mishandled, in the LAST place too:
//
//	nilck(fv)(m, 30, m * 3)             wrong, and (0 - m), m + 1, m << 3, m >> 32, m & 0xff the same
//	nilck(fv)(m, 30, (-2119779639779294LL))   802112035 for 1463733303
//	nilck(fv)(m, 30, k)                 right: a variable
//	nilck(fv)(5ULL), pick()(5ULL)       "internal error: pointer does not reference memory"
//	nilck(fv)(m, 30, m << 32)           "Cannot handle expression yet"
//	fv(m, 30, (-2119779639779294LL))    right: the pointer a variable's
//	slot-of-a-table(s.data, m, 30, m * 3)  right: an interface's
//
// for an f(int64_t a, float b, int64_t c) reading every word of its parameters and
// m of 7, measured on a P2-EDGE, none warned about: the HIGH word of the operation
// is what arrives wrong, so a callee reading only the low one sees nothing. It reached v0.56.0 as `cb(m, 30, m*3)` through a package function
// value, 110104252 for 868, and `c1(m*3)` with one parameter. WORKED AROUND: a call
// through a function value binds every 64-bit operation, the last included, and
// every 64-bit constant (pairArg, valueCallArgC).
//
// Re-measured the same day at spin2cpp b5993a84 with the carried diffs: the
// second number is 220, the negation lost, where the table above has garbage --
// wrong still, and 3LL still right.
//
// To check, build for the P2 and read the second number: -214 is right. Each line
// after the third prints 1 where the call is right; the first, third, fourth,
// fifth, sixth and eighth print 0 on the board today, the second and the seventh
// 1.

#include <stdio.h>
#include <stdint.h>

__attribute__((noinline)) int64_t id64(int64_t v) { return v; }
__attribute__((noinline)) int64_t mix(int64_t a, int64_t b) { return a * 31 + b; }
__attribute__((noinline)) float idf(float v) { return v; }
__attribute__((noinline)) int32_t f3(int64_t a, float b, int64_t c) { return (int32_t)a + (int32_t)(b * 2.0f) * 100 + (int32_t)c * 10000; }
__attribute__((noinline)) int32_t f5(int64_t a, int32_t b, float c) { return (int32_t)a + b * 100 + (int32_t)(c * 2.0f) * 10000; }
__attribute__((noinline)) int32_t kf(int64_t a, float b) { return (int32_t)(a & 0xff) + (int32_t)(b * 2.0f); }
__attribute__((noinline)) int32_t f6(int64_t a, float b, int64_t c) { return (int32_t)a + (int32_t)(b * 2.0f) * 100 + (int32_t)(c >> 32) * 7 + (int32_t)c * 10000; }
typedef int32_t (*ft6)(int64_t a, float b, int64_t c);
__attribute__((noinline)) ft6 pick(void) { return f6; }

int main(void) {
	int64_t m = id64(7);
	int64_t a = mix(m, 3);
	int64_t b = mix(-m, 3);
	int64_t c = mix(-m, 3LL);
	printf("%lld\n", a);
	printf("%lld\n", b);
	printf("%lld\n", c);
	int64_t k = id64(3);
	int32_t i = (int32_t)id64(4);
	float fl = idf(2.5f);
	int64_t t = 0 - m;
	printf("%d f3((0 - m), fl, k)\n", f3((0 - m), fl, k) == 30493);
	printf("%d f3(t, fl, k)\n", f3(t, fl, k) == 30493);
	printf("%d f5((0 - m), i, 2.5f)\n", f5((0 - m), i, 2.5f) == 50393);
	printf("%d f3((-5LL), fl, k)\n", f3((-5LL), fl, k) == 30495);
	printf("%d kf((0 - m), 3)\n", kf((0 - m), 3) == 255);
	int64_t t3 = m * 3;
	printf("%d pick()(m, fl, m * 3)\n", pick()(m, fl, m * 3) == 7 + 500 + 21 * 10000);
	printf("%d pick()(m, fl, t3)\n", pick()(m, fl, t3) == 7 + 500 + 21 * 10000);
	t3 = 0 - m;
	printf("%d pick()(m, fl, 0 - m)\n", pick()(m, fl, 0 - m) == 7 + 500 - 7 - 7 * 10000);
	return 0;
}
