// A struct with a member of no bytes whose element is aligned on more than a byte,
// `int32_t c[0]`, placed where that alignment moves it, has two sizes: the one it
// has alone and a smaller one as a member of another struct, so the member after it
// in the containing struct overlaps its own last member.
//
// The target's compiler lays a struct out twice (spin2cpp frontends/common.c). The
// first pass, DeclareMemberVariablesOfSizeFlag, aligns a member of two bytes on two
// and one of four or more on four -- a member of no bytes not at all -- and rounds
// the end up to four, which is the struct's size, varsize. The second, the
// fixupVarOffset of FixupOffsets, aligns each member on its PaddedTypeAlign, the
// element's alignment for an array, and gives the members their offsets; where its
// end is past varsize, varsize is raised to it, unrounded. A struct holding K is laid
// out with the varsize K had then:
//
//	struct K { uint8_t b; int32_t c[0]; int16_t d; };  // b 0, c 4, d 4: sizeof 6
//	struct L { struct K k; uint8_t z; };               // z 4: inside k.d
//
// Silent: storing l.z overwrites the low byte of l.k.d. An array of K is spaced six
// bytes apart and is right. OctoGo writes such a member for a zero-length array
// field, `[0]int32`, and for the `_ [0]func()` idiom, where it moves: the emitter
// declares a member of no elements whose element is aligned on more than a byte over
// uint8_t (zeroSizedDecl), which no pass moves; its uses read no element. Measured on
// a P2-EDGE with the in-process flexcc (spin2cpp eb263961 with the carried
// optimize_ir.c.diff) 2026-10-08.
//
// gcc prints, and the target should print:
//
//	K 8 L 12 z at 8
//	1 2 3
//
// The target printed:
//
//	K 6 L 8 z at 4
//	1 3 3

#include <stdio.h>
#include <stddef.h>
#include <stdint.h>

struct K { uint8_t b; int32_t c[0]; int16_t d; };
struct L { struct K k; uint8_t z; };

int main() {
	struct L l;
	printf("K %d L %d z at %d\n", (int)sizeof(struct K), (int)sizeof(struct L), (int)offsetof(struct L, z));
	l.k.b = 1;
	l.k.d = 2;
	l.z = 3;
	printf("%d %d %d\n", l.k.b, l.k.d, l.z);
	return 0;
}
