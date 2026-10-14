// A member past the first long of a struct a call RETURNS IN REGISTERS -- two to
// four longs, every member a long -- is read from a register nothing wrote:
// `mkP().b` is garbage, `mkP().a` right. Silent: nothing is said.
//
// Measured 2026-10-14 on a P2-EDGE with this repository's in-process backend
// (spin2cpp b5993a84 with the carried CORDIC hunk, the flags ogo build passes) by
// scripts/cboard.sh, and with a native build of the same tree; the binary was the
// same with the backend before (spin2cpp 1d709b25), so the fault is old. gcc prints
// the number in each line's comment; the board printed garbage for each `.len` and
// `.b` -- one value per build, -251220984 in one and 1571815688 in another -- and 1
// for `.a`.
//
// The listing says where the value goes: `mkP(3).b` is
//
//	call	#_mkP
//	mov	local03, local02	' local02 is never written; b is in result2
//
// The cause, read in spin2cpp's source: outasm.c compiles a member of an object
// (AST_METHODREF) as the object's operand at the member's offset. For a call, the
// operand is CompileFunccallFirstResult's -- the first of the results, copied to a
// temporary -- and an offset of four from a register is the register after it,
// which after allocation is no result at all. A struct the call returns through
// memory (more than four longs, or a member that is no long) is right.
//
// In OctoGo a string, a slice header and an interface value are such structs, and
// the emitter binds a call before it reads a field of the result (emitHeaderField:
// `len(b.String())` printed -251214335 on the board before it did). The receive was
// the shape it missed, a receive being a call of the helper in the C: `len(<-ch)`
// of a string channel printed 1569718284 for Go's 5, `cap(<-ch)` of a slice one
// garbage, buffered or not, until 2026-10-14.
//
// A fix, tested natively: in outasm.c's AST_METHODREF, a member of a call whose
// struct comes back in registers is the call's result of that index. Every line
// below then prints gcc's value on the board, and spin2cpp's `make test_offline`
// passes 588 of 588 with it as without.
//
// To check, build for the P2 with -2: each line should print gcc's value.

#include <stdio.h>

typedef struct { const char *ptr; int len; } S;
typedef struct { int a, b; } P2;

S gs = {"hello", 5};

S mkS(void) { return gs; }
static S sloopS(int k) { while (1) { S v; if (k-- <= 0) { v = gs; return v; } } }
P2 mkP(void) { P2 r = {1, 2}; return r; }

int main(void) {
	printf("mkS().len %d\n", mkS().len);       // 5
	printf("(mkS()).len %d\n", (mkS()).len);   // 5
	printf("sloopS().len %d\n", sloopS(3).len); // 5
	printf("mkP().b %d\n", mkP().b);           // 2
	printf("mkP().a %d\n", mkP().a);           // 1
	return 0;
}
