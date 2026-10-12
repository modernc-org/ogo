// A cast to a 64-bit integer type of a 64-bit EXPRESSION -- a same-width cast, a
// no-op in C -- leaves the high word of the result as a register happened to hold
// it, on the P2. The low word is right, so a value stored back into 32 bits never
// shows it. gcc computes every line right.
//
//	(int64_t)(a - b)           hi=-846200804 lo=7     <- wrong (a, b uint64_t)
//	(uint64_t)(a - b)          hi=-846200804 lo=7     <- wrong
//	(uint64_t)a                hi=0 lo=9              a variable: right
//	a - b                      hi=0 lo=7              no cast: right
//	(int64_t)(u + 1u)          hi=0 lo=8              a 32-bit expression widened: right
//
// and the same wrong high word wherever the cast stands: an argument of a
// prototyped function (above), an initializer, `int64_t v = (int64_t)(a - b);`, an
// operand of a comparison, `(uint64_t)(a - b) == 7` false, and a variadic argument,
// `printf("%llx", (uint64_t)(a - b))` printing cd90001c00000007. The garbage word
// was the same 0xcd90001c in every program measured.
//
// Known since 2026-08 as FAULT 1 of doc/shift64-by-variable.c, where it is worked
// around in the left-shift helper. The emitter binds the operand to a variable
// first in a conversion (emitConversion), passes a 64-bit value to a helper
// uncast (emitWideArg: the integer verbs, and since 2026-10-12 %q and %U, which
// printed '�' and U+F546C60800000042 for Go's 'B' and U+0042), and binds a
// 64-bit result before returning it (doc/return-widening-cast.c, a neighbour).
//
// Measured on a P2-EDGE 2026-10-12 with the in-process flexcc (spin2cpp 1d709b25
// with the carried optimize_ir.c.diff). Not reported upstream.
//
// To check, build for the P2 and read the hi= of each line.

#include <stdio.h>
#include <stdint.h>

static void show(const char *tag, int64_t n) {
	printf("%s hi=%d lo=%u\n", tag, (int)(n >> 32), (unsigned)n);
}

static unsigned id(unsigned v) { return v; }
static uint64_t id64(uint64_t v) { return v; }

int main() {
	unsigned u = id(7);
	uint64_t a = id64(9), b = id64(2);
	show("(int64_t)(a - b)", (int64_t)(a - b));
	show("(uint64_t)(a - b)", (uint64_t)(a - b));
	show("(uint64_t)a", (uint64_t)a);
	show("a - b", a - b);
	show("(int64_t)(u + 1u)", (int64_t)(u + 1u));
	int64_t v = (int64_t)(a - b);
	show("initializer", v);
	printf("comparison %d\n", (uint64_t)(a - b) == 7);
	printf("variadic %llx\n", (unsigned long long)(uint64_t)(a - b));
	return 0;
}
