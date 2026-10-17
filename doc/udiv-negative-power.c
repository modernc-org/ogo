// An unsigned division or remainder by a constant of 2^32 - 2^k, for k below 31, is
// computed as if the divisor were the negative power of two its bits read as. Valid
// C; gcc prints the first column of each pair:
//
//	                  gcc   flexcc -2
//	5 / 0xffffffff    0     4294967291   negated: x / -1
//	5 / 0xfffffffe    0     2            shifted: x >> 1
//	a / 0xfffffff0    1     268435455    a >> 4, a = 0xfffffffd
//	a / 0xc0000000    1     3            a >> 30
//	a % 0xffffffff    0     4294967295   a itself, a = 0xffffffff
//	a % 0xfffffffe    4294967293  1      a & 1
//	a % 0xfffffff0    13    13           a & 15, right by chance
//	a / 0x80000000    1     1            right: -2^31 is its own magnitude
//
// THE CAUSE, read in spin2cpp's backends/asm/outasm.c at b5993a84: CompileDiv's
// FAST_IMMEDIATE_DIVIDES path takes an immediate divisor that is a power of two or
// the negative of one,
//
//	if (rhs->kind == IMM_INT && !isfrac64 && ((rhs->val > 0 && isPowerOf2(rhs->val))
//	    || (rhs->val < 0 && isPowerOf2(0-rhs->val)))) {
//
// before asking whether the division is signed. rhs->val is an int32_t, so an
// unsigned divisor past INT32_MAX passes as negative: -1 is written as a move (the
// remainder) or a NEG (the quotient), and -2^k as a shift right by k or an AND with
// 2^k - 1, the unsigned branch dropping only the sign handling. A divisor in a
// variable goes to QDIV and is right, and so is any other constant.
//
// Measured with the in-process flexcc of 2026-10-17, spin2cpp b5993a84 with the
// carried diffs. It reached OctoGo through the fuzzer's genWraps: five seeds of 201
// failed on a P2-EDGE and passed on the host, each dividing a uint32 or a uint by
// 4294967295 or 4294967294, written out or as a named constant, in an expression and
// assigned through an element, a pointer and a calling index. WORKED AROUND in the
// emitter (udivHighConst): such a division of a 32-bit unsigned level is written as
// the compare it comes to, the quotient of a divisor of 2^31 or more being 0 or 1 --
// `x >= d`, and `x >= d ? x - d : x` for the remainder -- through ogo_udivhi and
// ogo_umodhi, which divide nothing. A narrower unsigned value is divided in int, by
// a positive divisor, and is right. Unreported upstream as of this writing.
//
// To check, build it for the P2: a run that prints gcc's column is right.

#include <stdio.h>

volatile unsigned vin[2] = {5u, 0xfffffffdu};
volatile unsigned vmax = 0xffffffffu;

int main(void) {
	unsigned five = vin[0], a = vin[1], m = vmax;
	printf("%u %u %u %u\n", five / 0xffffffffu, five / 0xfffffffeu, a / 0xfffffff0u, a / 0xc0000000u);
	printf("%u %u %u %u\n", m % 0xffffffffu, a % 0xfffffffeu, a % 0xfffffff0u, a / 0x80000000u);
	return 0;
}
