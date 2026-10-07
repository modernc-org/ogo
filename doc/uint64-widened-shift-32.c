// A value of 32 bits or fewer cast to uint64_t and shifted right by exactly 32,
// `(uint64_t)u >> 32`, reads its result from a register nothing writes:
//
//	_f1
//		mov	result1, _var02
//		mov	result2, #0
//
// for an unsigned u (0 wanted) and a signed one (its sign wanted) alike, and through
// an inner cast, `(uint64_t)(int64_t)u >> 32`. A shift by any other count goes
// through the system module's __system___int64_shr and is right, and so are an
// int64_t's shift by 32 (__system___int64_sar), the high word of a 64-bit value
// computed from widened ones, `((uint64_t)a * (uint64_t)b) >> 32`, and a uint64_t
// variable shifted. What the register held is what is printed, so a run can look
// right by luck; the listing is the measure. Measured on a P2-EDGE with the
// in-process flexcc (spin2cpp eb263961 with the carried optimize_ir.c.diff)
// 2026-10-07.
//
// Silent. OctoGo writes `uint64(x) >> 32` of a uint32, an int32 or a narrower x so,
// and printed 4052137992 for Go's 0 on the board; found by the fuzzer's widening
// fold, `int(uint64(e) >> 32)`, the day it was written. The emitter writes the
// value without the shift (wideShift32C): 0 of an unsigned x, evaluated, and the
// sign word of a signed one, `(uint64_t)(uint32_t)((int32_t)x >> 31)`.
//
// gcc prints, and the target should print:
//
//	f1 0:0 f2 0:0 f3 0:4294967295 f4 0:0
//
// The target printed, that day:
//
//	f1 0:4255121704 f2 0:8456 f3 0:8453 f4 0:8453

#include <stdio.h>
#include <stdint.h>

typedef uint64_t (*F)(unsigned);

static uint64_t f1(unsigned u) { return (uint64_t)u >> 32; }
static uint64_t f2(unsigned u) { return (uint64_t)(int64_t)u >> 32; }
static uint64_t f3(unsigned u) { return (uint64_t)(int)u >> 32; }
static uint64_t f4(unsigned u) { return (uint64_t)(uint16_t)u >> 32; }

// Called through a table so that no call is inlined into another's registers.
static F tab[] = {f1, f2, f3, f4};

int main() {
	for (int i = 0; i < 4; i++) {
		uint64_t r = tab[i](0xFFFFFFF9u);
		printf("f%d %u:%u ", i + 1, (unsigned)(r >> 32), (unsigned)r);
	}
	printf("\n");
	return 0;
}
