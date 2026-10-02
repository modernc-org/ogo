// The target's C compiler DROPS a struct or union member whose name is the name of
// a global variable declared before the type. Valid C -- a member's name lives in
// the member namespace -- and gcc keeps every member:
//
//	                                      gcc        flexcc -2
//	static int32_t s0; union { In s0; }   sizeof 8   sizeof 0
//	static int32_t m1; struct { In m1; int z; }   12          4
//	the same union, the global after it   8          8
//
// with `typedef struct { int a; int b; } In;`. Naming the member is refused
// ("unknown identifier s0 in class ...", "s0 is not a member"), so code that names
// it does not build; code that reaches the storage through a pointer to another
// type, as a union of argument blocks is reached, builds -- with the member gone.
//
// Measured 2026-10-02 against spin2cpp eb263961 (v7.7.3), flexcc -2 as ogo build
// invokes it, on a P2-EDGE. Found by a domain program declaring `var s0 =
// proto.Set{...}`: the emitted C kept the name, the goroutine runtime's union of
// argument blocks -- written after the program's globals -- had a member s0, and
// went to size 0, so a cog's argument block sat on its stack. `go feeder(3)` read
// n as 32148 and never ended.
//
// WORKED AROUND in internal/octogo/emit.go (goDefs): every member of a type the
// emitter writes after the program's globals is named with the compiler's prefix,
// `ogo_a0`, `ogo_s0`, `ogo_fn`, which no program name meets. The program's own
// struct types are written before its globals, so their fields are not reached.
// Only a STATIC global does it, the lexer handing its name back as an
// AST_LOCAL_IDENTIFIER that DeclareCMemberVariables does not unwrap. Reported
// upstream as flexprop#117 (2026-10-02) with a fix tested natively: test_offline
// 588/588 as without it, this file printing gcc's values on the board, and of 1248
// other C programs no binary changed.
//
// To check, run it with scripts/cboard.sh: gcc prints 8 12 8.

#include <stdio.h>
#include <stdint.h>

typedef struct { int a; int b; } In;

static int32_t s0 = 5;
static int32_t m1 = 6;

typedef union { In s0; } U;
typedef struct { In m1; int z; } SS;
typedef union { In q0; } U2;

static int32_t q0 = 7;

int main(void) {
	printf("%d %d %d\n", (int)sizeof(U), (int)sizeof(SS), (int)sizeof(U2));
	return (int)(s0 + m1 + q0) - 18;
}
