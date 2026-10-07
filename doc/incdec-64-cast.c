// An increment or a decrement is refused when its target holds a cast to or from a
// 64-bit type anywhere -- in an index, in a call's argument, under a dereference:
//
//	h[g((int64_t)k)]++;          // error: bad cast of expression
//	h[(int)(ll & 3)]++;          // the same
//	(*gp((long long)k))--;       // the same
//	q.a[g((int64_t)k)]++;        // the same
//
// Right: the compound form of the same target, `h[g((int64_t)k)] += 1`, an
// assignment to it, a read of it, a cast of a plain 64-bit variable, `h[(int)ll]++`,
// and a cast to a narrower type, `h[g((int)k)]++`. Measured with the in-process
// flexcc (spin2cpp eb263961 with the carried optimize_ir.c.diff) 2026-10-07.
//
// Loud. OctoGo reaches it through a shift past a byte's width, which goes to a
// guarded helper taking an int64_t count -- `hist[v>>k]++` of a byte v, the count
// widened by a cast -- and through an index converted from an int64, `h[int(ll &
// 3)]++`. The emitter writes an increment of any target but a plain name or field
// path as `+= 1` (incDecC), which the backend also compiles no larger.
//
// gcc prints, and the target should print:
//
//	2 0 1 0

#include <stdio.h>
#include <stdint.h>

static int g(int64_t n) { return n & 3; }

int main(void) {
	int h[4] = {0};
	volatile int k = 12;
	volatile long long ll = 6;
	h[g((int64_t)k)]++;
	h[(int)(ll & 3)]++;
	h[g((int64_t)k)]++;
	printf("%d %d %d %d\n", h[0], h[1], h[2], h[3]);
	return 0;
}
