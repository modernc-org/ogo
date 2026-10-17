// A switch whose cases are further apart than INT32_MAX builds for ever. Valid C;
// gcc builds it in no time and prints 3:
//
//	cases                                        flexcc -2
//	2147483647, -1585330326, 1, 2, 3, 4, 5       still building when stopped
//	2147483647, -2147483648, 1, 2, 3, 4, 5       builds
//	2147483647, 0, 1, 2, 3, 4, 5                 builds
//	1000000, -1000000, 1, 2, 3, 4, 5             builds
//
// THE CAUSE, read in spin2cpp's frontends/case.c at b5993a84: the cases are sorted
// by qsort with
//
//	static int caseCmp(const void *av, const void *bv)
//	{
//	    const CaseHolder *a = (const CaseHolder *)av;
//	    const CaseHolder *b = (const CaseHolder *)bv;
//	    return a->val - b->val;
//	}
//
// a difference of two int32_t values, which overflows for two that are further apart
// than INT32_MAX, so the comparison says a < b and b < c and c < a of some three. A
// sort handed such an order may do anything; the transpiled backend's runs on
// without end, and which sets reach that depends on where the overflow falls, not
// on how many cases there are or how far apart the farthest are. A native build's
// qsort may return instead, in an order that is not sorted, and the code that
// follows reads the first case as the least and the last as the greatest -- which
// is a table of jumps built from a wrong base, if it decides on one. An unsigned
// case past INT32_MAX is held as an int32_t too, so 4294967295 and 1 are two apart.
// The comparison of labels before it, labelCmp, is written the same way.
//
// Measured with the in-process flexcc of 2026-10-17, spin2cpp b5993a84 with the
// carried diffs. It reached OctoGo through the fuzzer's switches (genSwitches): a
// switch on an int32 of cases near 152696, 2147483647 and -1585330326 among them
// had built for fifteen minutes when the sweep's cap killed it. WORKED AROUND in
// switchCaseLabels: a switch whose cases, as int32_t, are further apart than
// INT32_MAX is a chain of compares. Unreported upstream as of this writing.
//
// Stopped after 30 seconds as this file and after 28 minutes as another of the
// fuzzer's programs; scripts/cccorpus.sh, which gives each file 300 seconds, lists it as
// status 124 for as long as the fault stands.
//
// To check, build it for the P2 with a timeout: a build that ends and prints 3 is
// right.

#include <stdio.h>

volatile int vin = 3;

__attribute__((noinline)) int f(int x) {
	switch (x) {
	case 2147483647: return 1;
	case -1585330326: return 2;
	case 1: return 3;
	case 2: return 4;
	case 3: return 5;
	case 4: return 6;
	case 5: return 7;
	}
	return 0;
}

int main(void) {
	printf("%d\n", f(vin - 2));
	return 0;
}
