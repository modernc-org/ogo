// An argument passed through a function pointer whose type leaves its parameters
// UNNAMED is not converted to the parameter's type: an int handed to a float or a
// double parameter arrives as its bit pattern.
//
//	cc -o t unnamed-param-no-conversion.c && ./t
//	600 150 60
//	600 150 60
//	6 6           <- gcc, and what C says
//
//	flexcc -2 -o t.binary unnamed-param-no-conversion.c
//	0 0 0
//	600 150 60
//	6 66937836    <- on a P2-EDGE
//
// The first line calls through pointers whose types are written `float (*)(float,
// float)`, `double (*)(double)` and a vtable-style slot `double (*)(void*, double)`,
// each with an int among the arguments; the second makes the same calls through the
// same types with the parameters NAMED. C converts an argument to a prototype's
// parameter type whether or not the parameter has a name.
//
// The cause is in the frontend, frontends/types.c of spin2cpp at eb263961 (v7.7.3):
// FixupFunccallTypes takes the type an argument is converted to from the called
// type's parameter only when that parameter is an AST_DECLARE_VAR, and the C grammar
// makes an unnamed parameter -- raw_parameter_declaration's bare
// declaration_specifiers -- the type itself, so no type is expected and the argument
// is passed as it is. A direct call is not affected, even of a function declared
// with unnamed parameters ahead of its definition (measured): the definition names
// every parameter. A call through a pointer has only the pointer's type to go by.
//
// OctoGo reached it with an untyped constant, the one argument Go converts
// implicitly: `h(3)` for an `h := half` taking a float64 printed 3e-45 where Go
// prints 1.5, and so did a call through an interface's method, a package variable,
// a slice or struct field holding a function, a deferred call and a goroutine's
// callback -- every call through a function value or an interface slot with a
// float parameter. The host's C compiler got all of them right. Found by a board
// sweep of function values (2026-09-22). The emitter names the number and bool
// parameters of the function types it writes, `ogo_functypeN` and an interface's
// slots alike; see cFuncTypeParams in internal/octogo/emit.go.
//
// Only those, because naming does the opposite to a STRUCT parameter: through
// `int (*)(P p)` a function taking a P read garbage, where `int (*)(P)` and a
// direct call are right -- the third line, a three-field P passed through each. By
// the reading of FixupFunccallTypes, not measured further: an unnamed parameter
// whose argument goes on the stack is passed as a copied reference
// (AST_COPYREFTYPE), which is how the function takes it, and a named one is given
// its declared type instead.
//
// To check whether the workaround is still needed, run this on a board and compare
// the first line with the second, and the two numbers of the third.

#include <stdio.h>

typedef float (*fn)(float, float);
typedef double (*fd)(double);
typedef struct { double (*scale)(void*, double); } slot;

typedef float (*fnn)(float a, float b);
typedef double (*fdn)(double a);
typedef struct { double (*scale)(void* self, double f); } slotn;

typedef struct { int a, b; signed char c; } P;
typedef int (*fp)(P);
typedef int (*fpn)(P p);

float mulf(float a, float b) { return a * b; }

double half(double a) { return a / 2; }

double scale(void* self, double f) { return *(double*)self * f; }

int sum(P p) { return p.a + p.b + p.c; }

int main(void) {
	int n = 4;
	double k = 20;
	fn g = mulf;
	fd h = half;
	slot s = {scale};
	fnn gn = mulf;
	fdn hn = half;
	slotn sn = {scale};
	float r1 = g(1.5f, n);
	double r2 = h(3), r3 = s.scale(&k, 3);
	printf("%d %d %d\n", (int)(r1 * 100), (int)(r2 * 100), (int)r3);
	r1 = gn(1.5f, n);
	r2 = hn(3), r3 = sn.scale(&k, 3);
	printf("%d %d %d\n", (int)(r1 * 100), (int)(r2 * 100), (int)r3);
	P p = {1, 2, 3};
	fp u = sum;
	fpn un = sum;
	printf("%d %d\n", u(p), un(p));
	return 0;
}
