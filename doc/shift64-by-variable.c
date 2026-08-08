// A 64-bit shift by a VARIABLE count is miscompiled. Shifting a long long by a
// count that is not a compile-time constant produces garbage -- not a wrong count,
// not a truncation, but a value with no relation to either operand -- while the
// same shift by a constant count is correct, and multiplying by the equivalent
// power of two is correct.
//
// Measured on a P2-EDGE, built with the flexcc in internal/flexcc (spin2cpp
// v7.7.0). This prints:
//
//	counts        3 3 3
//	<< variable   -166599530848976896 -166599530848976896 -166599530848976896
//	<< constant   8796093022208
//	* 8           8796093022208
//	small value   -166600269583351752
//	unsigned      1099511627776
//
// where every one of those lines should read 8796093022208, except "small value"
// (56) and "counts" (3 3 3). gcc on the host gets all of them right, which is why
// this went unnoticed: it is one of the cases where the two C compilers disagree
// on semantics rather than on warnings.
//
// The count's own type does not matter -- int, long long and unsigned all
// misbehave alike -- and neither does the magnitude of the value: 7, widened to a
// long long and shifted by a variable 3, is garbage too. What distinguishes the
// working cases is only that the count is a constant. A function call taking and
// returning a long long is fine (by3 below), so it is the shift and not the ABI.
//
// It is not fully deterministic in when it bites: `long long w = 5; w << n` for a
// variable n has been observed to give the right answer, which is what a
// register-allocation or peephole fault looks like from outside.
//
// CONSEQUENCE FOR ogo: `v << n` and `v >> n` on an int64 or uint64 with a
// non-constant count go through ogo_shl_int64_t / ogo_shr_int64_t (emit.go's
// shiftHelperDef), whose body performs exactly this shift. Those are miscompiled
// on the target. Narrowing the helper's count parameter to int does NOT help --
// that was tried. The fix is to do the shift in 32-bit halves, every one of which
// has either a constant count or a 32-bit operand, both of which are correct
// here; that is unbuilt, so the run-case table deliberately keeps off the shape
// (see "an inferred value takes the type of the typed operand").
//
// This is pre-existing and was found by a new test, not caused by one: the
// compiler at 7113ce0 emits a byte-identical binary for the reproducer.
//
// To check whether this is still so, run this on a board and read the numbers.

#include <stdio.h>

static long long by3(long long v) { return v << 3; }

int main(void) {
	long long v = 1LL << 40;
	int m = 3;
	long long n = 3;
	unsigned c = 3;
	printf("counts        %d %lld %u\n", m, n, c);
	printf("<< variable   %lld %lld %lld\n", v << m, v << n, v << c);
	printf("<< constant   %lld\n", v << 3);
	printf("* 8           %lld\n", v * 8);
	printf("by3           %lld\n", by3(v));
	unsigned lo = 7;
	printf("small value   %lld\n", (long long)lo << m);
	unsigned long long u = 1ULL << 40;
	printf("unsigned      %llu\n", u << m);
	return 0;
}
