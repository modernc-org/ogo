// A counting loop whose bound the body lowers runs as many times as the bound said
// when the loop began. flexcc's simple loop conversion (CheckSimpleIncrementLoop in
// loops.c, at -O1, the default) rewrites
//
//	for (i = 0; i < n; i++) body      // i not used in the body
//
// as a count down from n, `for (i = n; i != 0; i--)`, which reads the bound ONCE.
// It asks that the loop variable is a local the body does not use, and asks
// nothing of the bound but that it is a constant or a name: a local the body
// writes, a global, a global a called function writes. So all three loops below
// run five times where C, and Go, run three, silently:
//
//	                                  output
//	gcc                               lbc 3 3 3
//	flexcc -2                         lbc 5 5 5
//	flexcc -2 -O1                     lbc 5 5 5
//	flexcc -2 -O2                     lbc 5 5 5
//	flexcc -2 -O0                     lbc 3 3 3
//	flexcc -2 -Ono-loop-basic         lbc 3 3 3
//
// Measured on a P2-EDGE 2026-10-03 with the in-process flexcc of spin2cpp v7.7.3
// (eb263961, upstream's master) and the carried optimize_ir.c.diff, which does not
// touch loops.c.
//
// The declaration form, `for (int i = 0; ...)`, is not converted -- its first
// clause is not an assignment -- and neither is a loop with no first clause. The
// fix in the backend is to leave a loop alone whose bound is not a constant or a
// local the body does not write and whose address is not taken: a global may be
// written by any call, and by another cog. Tested natively 2026-10-03, a check of
// that ahead of the rewrite (IsLoopInvariantLimit, beside it in loops.c): this
// file prints lbc 3 3 3 on the board; `make test_offline` reports what it reports
// without the check; of 1261 programs -- doc/ and a corpus dump -- this file is the
// only one built differently; and a loop over a local bound the body leaves alone
// is still counted down, as a REP. REPORTED 2026-10-03 as flexprop issue 119 with
// that check as the suggested fix.
//
// In OctoGo: `var i int; for i = 0; i < n; i++ { ...; n = 3 }`, with n a local or
// a package variable, ran five times on the board where Go runs three; found
// 2026-10-03 while checking which hub reads the backend keeps in a loop. The
// emitter writes an assigned first clause ahead of the loop since, `i = 0; for
// (; i < n; i++)`, which the conversion does not match (the run case "a for loop
// whose body lowers its bound").
//
// To check, build for the P2 with -2 and read the line: lbc 3 3 3 is right.

#include <stdio.h>

int gn = 5;
int gm = 5;

void lower(void) { gm = 3; }

int main(void) {
	int c = 0, d = 0, e = 0, i, n = 5;
	for (i = 0; i < n; i++) { // a local bound the body lowers
		c++;
		if (c == 2) n = 3;
	}
	for (i = 0; i < gn; i++) { // a global bound the body lowers
		d++;
		if (d == 2) gn = 3;
	}
	for (i = 0; i < gm; i++) { // a global bound a called function lowers
		e++;
		if (e == 2) lower();
	}
	printf("lbc %d %d %d\n", c, d, e);
	return 0;
}
