// Comparing a function pointer in C compiles to a call of the system module's
// funcptr_cmp, which takes the ADDRESSES of two method pointers -- two longs each,
// an object and a function, as Spin and BASIC have them -- and is handed the two
// pointer VALUES. So `p == 0` compares the memory p points at with the memory at
// address 0, and the next long of each:
//
//	__system___funcptr_cmp
//		rdlong	result1, arg01
//		rdlong	_var01, arg02
//		sub	result1, _var01 wz
//	 if_e	add	arg01, #4
//	 ...
//
// A function named in a STATIC initializer is stored as its method-table index
// shifted up by 20, `long (1 {_inc})<<20`, which as an address is 0 again (a hub
// address has 20 bits), so the compare reads address 0 twice and says nil. A
// pointer taken at run time is the function's address with the index above it and
// reads code, which differs from address 0 by luck. Calling either works; `!p` and
// `p ? 1 : 0` test the word and are right. Measured on a P2-EDGE with the
// in-process flexcc (spin2cpp eb263961 with the carried optimize_ir.c.diff)
// 2026-10-07. Two function pointers to the same function compare by the same
// helper, and a static one and a run-time one are different words besides.
//
// Silent. OctoGo has only `f == nil` and `f != nil` of a function value, and a
// table of functions in a package variable is written as a static initializer: a
// dispatch table compared with nil read nil and its call was skipped, and the nil
// check of a call through a function value (2026-10-06) panicked. The emitter
// writes every such test as `!f` / `!!f` (emitFuncNilTriple, nilHelperDef).
//
// gcc prints, and the target should print:
//
//	h==0 0 h!=0 1 one==0 0 l==0 0
//	!h 0 (h?1:0) 1

#include <stdio.h>

typedef int (*F)(int);

static int inc(int n) { return n + 1; }
static int dbl(int n) { return 2 * n; }

static F tab[2] = {inc, dbl};
static F one = inc;

int main(void) {
	F h = tab[0];
	F l = dbl;
	printf("h==0 %d h!=0 %d one==0 %d l==0 %d\n", h == 0, h != 0, one == 0, l == 0);
	printf("!h %d (h?1:0) %d\n", !h, h ? 1 : 0);
	return 0;
}
