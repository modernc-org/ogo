// sizeof(_Bool) is 4 to the target's C compiler, where a _Bool takes one byte.
// An array of _Bool lays its elements out a byte apart, a struct gives a _Bool
// member one byte, and sizeof of a _Bool variable, of an element and of
// _Bool[4] answer 1, 1 and 4; only the operator applied to the type's name,
// and to a typedef of it, answers 4. Every other type's sizeof is its stride.
//
// Measured on a P2-EDGE, built with the flexcc in internal/flexcc (spin2cpp
// b5993a84, flexprop v7.7.0). This prints, where gcc prints 1 for the first
// two lines' sizeof:
//
//	_Bool sizeof 4 stride 1
//	flag sizeof 4 stride 1
//	unsigned char sizeof 1 stride 1
//	variable 1, element 1, _Bool[4] 4, struct 8 (b at 1)
//	copied: 1 0 1, guard 11 22 33 44
//
// and the last line only because the copy below is sized by the variable: a
// copy sized `sizeof(_Bool) * n` is four times the array. OctoGo wrote that
// spelling where an array of bools is handed back through the caller's
// storage, `memcpy(_ogo_ret->_0, as, sizeof(_Bool)*256)` of Go 1.27's
// strings.makeASCIISet, which overwrote the caller's frame and the program
// ran no further (dom110). The emitter spells a bool's size sizeof(uint8_t)
// now (cSizeof); a copy sized by an object, sizeof(dst), was always right.
//
// To check whether this is still so, run this on a board: the first line's
// sizeof says it.

#include <stdio.h>
#include <string.h>

typedef _Bool flag;

typedef struct {
	_Bool a;
	_Bool b;
	int c;
} pair;

int main(void) {
	_Bool a3[3];
	flag f3[3];
	unsigned char u3[3];
	_Bool one;
	_Bool loc[4];
	printf("_Bool sizeof %d stride %d\n", (int)sizeof(_Bool), (int)((char *)&a3[1] - (char *)&a3[0]));
	printf("flag sizeof %d stride %d\n", (int)sizeof(flag), (int)((char *)&f3[1] - (char *)&f3[0]));
	printf("unsigned char sizeof %d stride %d\n", (int)sizeof(unsigned char), (int)((char *)&u3[1] - (char *)&u3[0]));
	printf("variable %d, element %d, _Bool[4] %d, struct %d (b at %d)\n", (int)sizeof one, (int)sizeof loc[0],
	       (int)sizeof(_Bool[4]), (int)sizeof(pair), (int)((char *)&((pair *)0)->b - (char *)0));
	struct {
		_Bool flags[3];
		_Bool pad;
		int guard[4];
	} h = {{0}, 0, {11, 22, 33, 44}};
	_Bool src[3] = {1, 0, 1};
	memcpy(h.flags, src, sizeof src);
	printf("copied: %d %d %d, guard %d %d %d %d\n", h.flags[0], h.flags[1], h.flags[2], h.guard[0], h.guard[1], h.guard[2],
	       h.guard[3]);
	return 0;
}
