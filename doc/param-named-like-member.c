// A struct parameter passed BY VALUE cannot be read through a member named like the
// parameter itself when that member is an aggregate:
//
//	typedef struct { int x, y; } T;
//	typedef struct { T a; int k; } S;
//	T f(S a) { return a.a; }   // error: Expecting identifier after '.'
//
// The same refusal for `a.a.x`, `&a.a`, a parameter after another one, and a struct
// of five words. Right: a member of a scalar type (`struct { int a; }`, a float, a
// pointer), a struct of one word all told, a POINTER parameter (`S *a; a->a`), a
// LOCAL of the name (`S a = b; a.a`) and the parameter renamed. Measured with the
// in-process flexcc (spin2cpp eb263961 with the carried optimize_ir.c.diff)
// 2026-10-06; every variant that compiles prints gcc's values on a P2-EDGE.
//
// It is loud, and Go writes it often: a method `func (n Named) String() string {
// return n.n }` over a string field, or `func (t T) Get() Inner { return t.t }`. The
// emitter receives such a parameter under another name and copies it into a local
// of its own on entry (memberShadowParam).
//
// gcc prints, and the target should print:
//
//	3 4

#include <stdio.h>

typedef struct {
	int x, y;
} T;

typedef struct {
	T a;
	int k;
} S;

T f(S a) { return a.a; }

int main(void) {
	S s = {{3, 4}, 5};
	T t = f(s);
	printf("%d %d\n", t.x, t.y);
	return 0;
}
