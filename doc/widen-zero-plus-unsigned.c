// A cast to a 64-bit integer type of a 32-bit UNSIGNED expression whose left
// operand is a zero constant -- `0u + u`, `0 - u`, `0u | u`, `0 * u`, `5u - 5u + u`,
// under any operator -- leaves the high word as its register happened to hold it:
// the zero extension is not written. The low word is right, so a value that is
// stored back into 32 bits never shows it.
//
//	(int64_t)(0u + u)     hi=1571815688 lo=7
//	(int64_t)(u + 0u)     hi=0 lo=7
//
// A zero on the right, a one on the left, an `int` or a narrower unsigned operand
// (promoted to int), a variable, a call, and the implicit conversions -- an
// argument, a return, an initializer -- are right; and so is a cast to uint32_t
// first, `(int64_t)(uint32_t)(0u + u)`, in every shape measured (180 of them: five
// operands, three zeros, six operators, both 64-bit types, two spellings of the
// inner cast). Measured on a P2-EDGE with the in-process flexcc (spin2cpp eb263961
// with the carried optimize_ir.c.diff) 2026-10-07.
//
// Silent. OctoGo writes such a cast for `int64(0 - v)` of a uint32 v -- printed
// -166570509754957831 on the board for Go's 4294967289 -- for `uint64(z + v)` of a
// constant z of 0, for `float64(0 + v)`, and for a shift COUNT, which the guarded
// shift helpers take as an int64_t: `(28 &^ 28) + (55 >> g)` folded to `0u + ...`
// and the count was negative, "panic: negative shift amount" (OctoSmith seed 5849,
// right on the host). The emitter casts a 32-bit unsigned expression to uint32_t
// before widening it (widenU32).
//
// gcc prints, and the target should print:
//
//	0u+u hi=0 lo=7
//	0u-u hi=0 lo=4294967289
//	0+u hi=0 lo=7
//	5u-5u+u hi=0 lo=7
//	0u*u hi=0 lo=0
//	u+0u hi=0 lo=7
//	(uint32_t)(0u+u) hi=0 lo=7

#include <stdio.h>
#include <stdint.h>

static void show(const char *tag, int64_t n) {
	printf("%s hi=%d lo=%u\n", tag, (int)(n >> 32), (unsigned)n);
}

static unsigned id(unsigned v) { return v; }

int main() {
	unsigned u = id(7);
	show("0u+u", (int64_t)(0u + u));
	show("0u-u", (int64_t)(0u - u));
	show("0+u", (int64_t)(0 + u));
	show("5u-5u+u", (int64_t)(5u - 5u + u));
	show("0u*u", (int64_t)(0u * u));
	show("u+0u", (int64_t)(u + 0u));
	show("(uint32_t)(0u+u)", (int64_t)(uint32_t)(0u + u));
	return 0;
}
