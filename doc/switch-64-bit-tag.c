// A switch on a 64-bit value hangs the board where its cases are dense enough for
// a table of jumps. Valid C; gcc prints every line:
//
//	                       gcc     flexcc -2
//	narrow(5), narrow(9)   67 -1   67 -1     a 32-bit tag, through the same table
//	wide(5)                67      nothing more is printed: the board hangs
//	wide(0), wide(9)       11 -1   not reached
//
// The listing says why. A 32-bit tag is bounded before the jump, `fle _var01,
// #8` and then `jmprel _var01`; the 64-bit one jumps by a register nothing in the
// function wrote, unbounded:
//
//	_wide
//		mov	_var04, _var02
//		jmprel	_var04
//
// so every value jumps where that register happens to point. A sparse switch, which
// the compiler writes as compares, is not measured here.
//
// Measured on a P2-EDGE 2026-10-16 at spin2cpp b5993a84 with the carried diffs,
// flexcc -2. It does not reach OctoGo: the emitter writes a switch whose tag is an
// integer of 32 bits or fewer as a C switch and a 64-bit one as a chain of
// compares (switchCaseLabels). Unreported upstream as of this writing.
//
// To check, build for the P2 and read the second line: `w5 67` is right.

#include <stdio.h>
#include <stdint.h>

__attribute__((noinline)) int64_t id(int64_t v) { return v; }

__attribute__((noinline)) int wide(int64_t x) {
	switch (x) {
	case 0: return 11; case 1: return 22; case 2: return 37; case 3: return 41;
	case 4: return 53; case 5: return 67; case 6: return 71; case 7: return 89;
	default: return -1;
	}
}

__attribute__((noinline)) int narrow(int32_t x) {
	switch (x) {
	case 0: return 11; case 1: return 22; case 2: return 37; case 3: return 41;
	case 4: return 53; case 5: return 67; case 6: return 71; case 7: return 89;
	default: return -1;
	}
}

int main(void) {
	printf("start %d %d\n", narrow((int32_t)id(5)), narrow((int32_t)id(9)));
	printf("w5 %d\n", wide(id(5)));
	printf("w0 %d\n", wide(id(0)));
	printf("w9 %d\n", wide(id(9)));
	printf("done\n");
	return 0;
}
