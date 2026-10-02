// The target's C compiler lays out a STATIC INITIALIZER of a struct otherwise than
// it lays out the struct, when the struct has an array field of one- or two-byte
// elements that is four bytes or more: a field read then reads the bytes the
// initializer put somewhere else. Silent, and right on gcc:
//
//	                                       gcc               flexcc -2
//	static R8 r8 = {2, {1, -2, 3, -4}};    2 1 -2 3 -4       2 -2 3 -4 0
//	R8 l = {2, {1, -2, 3, -4}};  (local)   2 1 -2 3 -4       2 1 -2 3 -4
//	static B6T b6t = {{1..6}, 7, 8};       6 7 8             6 0 7
//	sizeof(R8), offset of r8.s             10, 2             12, 4
//
// The layout aligns a member by PaddedTypeAlign (frontends/common.c,
// fixupVarOffset), which rounds the alignment of anything of four bytes or more up
// to four, and advances by the member's size; the static initializer
// (backends/dat/outdat.c, outputInitializer) aligns the member by TypeAlign -- an
// array's ELEMENT alignment, one or two -- and pads a member of four bytes or more
// out to a multiple of four. So an int16_t[4] after a byte is read at offset 4 and
// written at offset 2, and a field after a uint8_t[6] is read at 6 and written at
// 8. Where the written total exceeds the type the compiler says "Bad initialization
// size" instead. Locals are initialized by stores and are right in every shape, as
// are an array of ONE short, a byte array of fewer than four, an int32_t array, a
// nested struct and an array of structs -- a struct's alignment is four in both.
//
// Measured 2026-10-02 against spin2cpp eb263961 (v7.7.3), flexcc -2 as ogo build
// invokes it, on a P2-EDGE. Found by a domain program whose message table held a
// `struct { uint8 ch; [4]int16 steps }`: its ramp summed to -3 for Go's -2.
//
// WORKED AROUND in internal/octogo/emit.go (flexccInitSkew): a package variable
// whose type the two models disagree about -- or an array or a slice of one -- is
// zeroed statically and filled where the package's initializer runs. Reported
// upstream as flexprop#116 (2026-10-02) with a fix tested natively: align a member
// in outputInitializer's struct loop by PaddedTypeAlign and take its own size, as
// the layout does -- test_offline 588/588 as without it, this file printing gcc's
// values on the board, and of 1248 other C programs no binary changed.
//
// To check, run it with scripts/cboard.sh: gcc prints the values as written.

#include <stdio.h>
#include <stdint.h>

typedef struct { uint8_t ch; int16_t s[4]; } R8;
typedef struct { uint8_t b[6]; int16_t t; uint8_t u; } B6T;

static R8 r8 = {2, {1, -2, 3, -4}};
static B6T b6t = {{1, 2, 3, 4, 5, 6}, 7, 8};

int main(void) {
	R8 l = {2, {1, -2, 3, -4}};
	printf("r8  %d %d %d %d %d\n", r8.ch, r8.s[0], r8.s[1], r8.s[2], r8.s[3]);
	printf("loc %d %d %d %d %d\n", l.ch, l.s[0], l.s[1], l.s[2], l.s[3]);
	printf("b6t %d %d %d\n", b6t.b[5], b6t.t, b6t.u);
	return 0;
}
