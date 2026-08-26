// flexcc computes `~x` WRONG in the high word when x is 64 bits, for a constant
// operand and for a variable alike. The low word is right, which is what makes it
// hard to see: a value that looks nearly correct is off by 2^32 times something.
//
// Measured on a P2-EDGE against flexprop v7.7.0 with the flags ogo build uses,
// for x = -3869336025161645586:
//
//	~x, constant operand           hi=-83710123   lo=1080147473   WRONG
//	~x, variable operand           hi=-83710123   lo=1080147473   WRONG
//	(int64_t)-1 ^ x, variable      hi=900899997   lo=1080147473   correct
//	(int64_t)-1 ^ x, constant      hi=900899997   lo=1080147473   correct
//	the folded value written out   hi=900899997   lo=1080147473   correct
//
// gcc gives hi=900899997 for all five.
//
// The emitter already wrote the complement as `(T)-1 ^ x` rather than `~x` -- for
// a different reason, and only for a VARIABLE operand; a constant kept the short
// spelling. `x &^= K` for a wide constant K is the shape that reached it, and on a
// P2 it left x unchanged where Go clears the bits. The oracle fuzzer found it the
// day int64 entered the generator.
//
// Worked around by FOLDING the complement of a constant: the value is known, so
// no operator is emitted at all. See emitComplement in internal/octogo/emit.go.

#include <stdio.h>
#include <stdint.h>

volatile int64_t seed = 3869336025161645586LL;

int main(void) {
	int64_t v = -seed; // -3869336025161645586, computed at run time
	int64_t a = ~(-3869336025161645586LL);
	printf("~constant        hi=%d lo=%d\n", (int)(a >> 32), (int)a);
	int64_t b = ~v;
	printf("~variable        hi=%d lo=%d\n", (int)(b >> 32), (int)b);
	int64_t c = ((int64_t)-1 ^ (v));
	printf("(int64_t)-1 ^ v  hi=%d lo=%d\n", (int)(c >> 32), (int)c);
	int64_t d = 3869336025161645585LL;
	printf("folded           hi=%d lo=%d\n", (int)(d >> 32), (int)d);
	return 0;
}
