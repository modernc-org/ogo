// A call's STRUCT result passed where the call stands, as another call's by-value
// argument, reaches the callee corrupted when the struct is larger than four machine
// words. This is a miscompile: the target's compiler warns, and builds a binary that
// prints wrong numbers.
//
// Measured 2026-09-25 on a P2-EDGE with this repository's in-process backend
// (spin2cpp v7.7.3, the flags ogo build passes), by scripts/cboard.sh. Found by a
// calendar program whose `t.add(45).print()` -- a method on a METHOD's struct result
// -- panicked with an index out of range on the board and printed the right date on
// the host: its Stamp was six words, and the method's receiver came in shifted.
//
// This file, the reduction. gcc prints 123456 on every line; the board:
//
//	call:                 garbage      sum6(mk6(1))
//	call 2nd arg:         garbage      sum6x(0, mk6(1))
//	compound lit:         123456
//	var:                  123456
//	field:                123456
//	elem:                 123456
//	deref:                123456
//	deref call:           123456       sum6(*pk6())
//	call field:           0            sum6(mkh().in)
//	nested id:            112345       sum6(id6(g6)) -- shifted a word
//	return of call arg:   111234       r := id6(mk6(1)); sum6(r)
//
// and by size, the same `f(mk(1))` for a struct of n ints: 1, 2, 3 and 4 words are
// right, 5 is wrong in its last word, 6 and up are shifted. A struct of more than
// four words is returned through memory, and that is the shape that breaks. The
// target's compiler says, of each broken line,
//
//	warning: incompatible pointer types in parameter passing: expected unknown type
//	but got reference to unknown type
//
// the same words doc/return-nonword-struct.c records for a struct with a member
// narrower than a word -- another way to the same weakness.
//
// The workaround, which is what ogo emits: bind the call to a temporary and pass
// that, `S6 t = mk6(1); sum6(t);` -- correct on the board and silent. A function's
// struct result was bound already, as an argument (hoistStructCallArg) and as a
// receiver; a METHOD's result passed on as the next method's receiver,
// `s.bump(1).show()`, was not until then (chainReceiver).

#include <stdio.h>

typedef struct { int a, b, c, d, e, f; } S6;
typedef struct { S6 in; int k; } H;

S6 mk6(int k) { S6 s = {k, k + 1, k + 2, k + 3, k + 4, k + 5}; return s; }
S6 *pk6(void) { static S6 g = {1, 2, 3, 4, 5, 6}; return &g; }
H mkh(void) { H h = {{1, 2, 3, 4, 5, 6}, 7}; return h; }

int sum6(S6 s) { return s.a * 100000 + s.b * 10000 + s.c * 1000 + s.d * 100 + s.e * 10 + s.f; }
int sum6x(int x, S6 s) { return x + s.a * 100000 + s.b * 10000 + s.c * 1000 + s.d * 100 + s.e * 10 + s.f; }
S6 id6(S6 s) { return s; }

S6 g6 = {1, 2, 3, 4, 5, 6};

int main(void) {
	S6 arr[2] = {{1, 2, 3, 4, 5, 6}, {1, 2, 3, 4, 5, 6}};
	H h = {{1, 2, 3, 4, 5, 6}, 7};
	S6 *p = &g6;
	int k = 1;
	printf("call: %d\n", sum6(mk6(1)));
	printf("call 2nd arg: %d\n", sum6x(0, mk6(1)));
	printf("compound lit: %d\n", sum6((S6){1, 2, 3, 4, 5, 6}));
	printf("var: %d\n", sum6(g6));
	printf("field: %d\n", sum6(h.in));
	printf("elem: %d\n", sum6(arr[k]));
	printf("deref: %d\n", sum6(*p));
	printf("deref call: %d\n", sum6(*pk6()));
	printf("call field: %d\n", sum6(mkh().in));
	printf("nested id: %d\n", sum6(id6(g6)));
	S6 r = id6(mk6(1));
	printf("return of call arg: %d\n", sum6(r));
	S6 t = mk6(1);
	printf("bound: %d\n", sum6(t));
	return 0;
}
