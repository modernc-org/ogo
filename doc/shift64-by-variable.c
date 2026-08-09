// A 64-bit shift by a VARIABLE count used to come back wrong on the target. Two
// separate backend faults were behind it, neither of them about shifting; both are
// worked around in emit.go and this file is what re-checks them after a regen.
//
// Measured on a P2-EDGE, built with the flexcc in internal/flexcc (spin2cpp
// v7.7.0) and the flags ogo build passes (-2 -Ono-inline-small -Ono-peephole).
// gcc gets every line right, which is why neither was visible off-target.
//
//	shl expr-cast -166617551233716360   <- should be 655884233731895160
//	shl var-cast  655884233731895160
//	seen var,int   576460752303423488
//	seen expr,ll   576460752303423488
//	seen expr,int  -2                   <- should be 576460752303423488
//	seen expr,cast 576460752303423488
//
// The first line's wrong value VARIES BETWEEN RUNS -- it is stale frame content,
// not a wrong computation -- so match on it being neither the right answer nor the
// same twice, rather than on the digits. The rest are stable.
//
// FAULT 1 -- a cast to a 64-bit type applied to a 64-bit EXPRESSION is
// miscompiled. Already known (it is why divHelperDef and emitConversion bind a
// temporary), and it had a second home in the left-shift helper:
// `(int64_t)((uint64_t)v << n)` is exactly that shape. The right shift needs no
// cast, which is why `>>` was right all along and only `<<` was wrong. Worked
// around in shiftHelperDef by binding the shifted value first. Casting a VARIABLE
// is fine; it is the cast of an expression that fails.
//
// FAULT 2 -- an argument NARROWER than an int64_t parameter is not widened, when
// another argument at the same call is a 64-bit expression. flexcc says so:
//
//	warning: Bad number of parameters in call to seen: expected 4 found 3
//
// It counts each 64-bit parameter as two slots, and passes three. The callee's
// count keeps its low word and reads its high word out of whatever was next in the
// frame, so `seen` above takes the `n >= 64` branch and returns -2. An UNGUARDED
// shift survives this -- the shift instruction uses the low word -- so the fault
// only shows where the callee reads the whole value, which is what the range
// guards that make a Go shift a Go shift do. Writing the VALUE as a plain variable
// escapes it, which is why every shift written the ordinary way was right and only
// `(s << 62) >> m` was wrong. Worked around in shiftCountC by casting a narrower
// count to int64_t at the call site -- and only a narrower one, since casting a
// 64-bit count would walk into fault 1.
//
// A shift is the only place the emitter calls a helper whose parameters differ in
// width from each other. If another one is ever added, this is the fault to keep
// in mind.
//
// To check whether this is still so, run this on a board and read the numbers.

#include <stdio.h>

// Fault 1: the cast is applied to the shift's result, a 64-bit expression.
static long long shl_expr(long long v, long long n) {
	return (long long)((unsigned long long)v << n);
}

// The same shift with the value bound before the cast -- the workaround.
static long long shl_var(long long v, long long n) {
	unsigned long long t = (unsigned long long)v << n;
	return (long long)t;
}

// Fault 2: no cast anywhere. It returns -1 or -2 when the count it was handed is
// not the count that was passed.
static long long seen(long long v, long long n) {
	if (n < 0) return -1;
	if (n >= 64) return -2;
	return v >> n;
}

int main(void) {
	long long v = 81985529216486895LL;
	long long n = 3;
	int m = 3;

	printf("shl expr-cast %lld\n", shl_expr(v, n));
	printf("shl var-cast  %lld\n", shl_var(v, n));

	long long s = 1;
	long long t = s << 62;
	printf("seen var,int   %lld\n", seen(t, m));
	printf("seen expr,ll   %lld\n", seen(s << 62, n));
	printf("seen expr,int  %lld\n", seen(s << 62, m));
	printf("seen expr,cast %lld\n", seen(s << 62, (long long)m));
	return 0;
}
