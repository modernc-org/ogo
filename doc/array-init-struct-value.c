// An ARRAY initializer whose elements are struct VALUES -- variables of a struct
// type, not brace lists -- is refused by the target's compiler, where C admits it
// and gcc takes it:
//
//	struct element                  refused, "expected pointer to int but got __anon_..."
//	tagged struct element           refused, "... but got _struct__Bt"
//	pointer member first or second  refused either way
//	the members braced out          RIGHT: {{s1.str, s1.len}, {s2.str, s2.len}}
//	assigned after the declaration  RIGHT: tt[0] = s1; tt[1] = s2;
//
// It reads the first value as the first MEMBER of the first element -- brace
// elision -- and so wants a pointer, then an int, where it was handed the whole
// struct. A STRUCT initializer holding the same values is fine (`Box b = {xs, 2}`
// with xs a slice), and so is a struct field initialized from a struct variable;
// it is arrays of aggregates alone. Measured on a P2-EDGE 2026-09-18 with the
// in-process flexcc (spin2cpp 3840014f plus internal/optimize_ir.c.diff): the
// program below does not compile; drop `as` and it prints "S 2 bc 2 bc".
//
// In OctoGo the elements of a slice or an array literal are such an initializer,
// the backing array's. A user struct had long been braced out member by member
// (structBraceC); a string and a slice header, which the emitter mints rather than
// registers, were not, so `[]string{s1, s2}` and `[][]int{a, b}` -- as ordinary as
// Go gets -- did not build for the target at all while the host ran them.
#include <stdio.h>

typedef struct { const char* str; int len; } S;

int main(void) {
	S s1 = {"a", 1}, s2 = {"bc", 2};
	S as[2] = {s1, s2};
	S ss[2] = {{s1.str, s1.len}, {s2.str, s2.len}};
	S tt[2];
	tt[0] = s1;
	tt[1] = s2;
	printf("S %d %s %d %s\n", ss[1].len, ss[1].str, tt[1].len, tt[1].str);
	(void)as;
	return 0;
}
