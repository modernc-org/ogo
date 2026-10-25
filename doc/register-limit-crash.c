// Past its local register limit, the backend crashes rather than stopping. Given a
// function with more live values than its register allocator can place, spin2cpp
// says
//
//	error: Internal error exceeded local register limit, possibly due to -O2
//	optimization needing additional registers or code being too complicated
//
// and then renames a register it never allocated (RenameSubregs): a segmentation
// fault in a native build, exit status 139, and a nil dereference in the
// transpile, which `ogo build` reports as "flexcc crashed: ..." since 2026-09-25
// (compileC in internal/build) rather than dying with a Go stack trace.
//
// A hundred local arrays of three ints, all live at once and each read at an index
// the program computes, are enough: the backend keeps such an array in cog
// registers and indexes it in place (ALTS), so its three longs stay together. With
// fifty the cog register pool runs out first, "fit 480 failed", a clean error; at
// ninety the backend crashes.
//
// Until 2026-10-25 this was thirty STRUCT locals of three words, each passed by value
// twice, the shape OctoGo met it in (ten functions of about thirty calls each, every
// call converting several byte-slice constants -- slice headers, three words
// apiece). internal/outasm.c.diff's second hunk (SplitLocalStructs, see
// doc/complit-arg-registers.c) gives a struct local that nothing reads as a block one
// local a long, whose registers the optimizer reuses, and that program builds since
// and prints gcc's 960 on a P2-EDGE. An array indexed in place is read as a block,
// so it is not split, and reaches the crash both ways: native b5993a84 with and
// without the hunk, exit status 139.
//
// Measured first with a native flexcc of spin2cpp v7.7.3 (eb263961) and with the
// in-process one (scripts/flexcc -2), 2026-09-25; in this shape with native
// b5993a84 and the in-process backend of 2026-10-25. To check whether this is
// still so, compile it: exit status 139 (native) or a Go panic (scripts/flexcc) is
// the crash, and a clean "exceeded local register limit" with exit status 1 would
// be the fix. gcc prints 15350.

#include <stdio.h>

#define DECL(i) int a##i[3] = {k + 1##i, 1##i, k};
#define USE(i) s += a##i[k % 3] + a##i[(k + 1) % 3];
#define TEN(m, d) m(d##0) m(d##1) m(d##2) m(d##3) m(d##4) m(d##5) m(d##6) m(d##7) m(d##8) m(d##9)
#define HUNDRED(m) TEN(m, 0) TEN(m, 1) TEN(m, 2) TEN(m, 3) TEN(m, 4) TEN(m, 5) TEN(m, 6) TEN(m, 7) TEN(m, 8) TEN(m, 9)

int f(int k) {
	HUNDRED(DECL)
	int s = 0;
	HUNDRED(USE)
	return s;
}

int main(void) {
	printf("%d\n", f(2));
	return 0;
}
