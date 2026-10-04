// A compound literal anywhere under a cast to void -- the literal itself, its
// address, or an argument of a call the cast discards -- is refused by the backend
// while generating code:
//
//	v.c:8: error: Internal error, asm code cannot handle assignment
//	v.c:8: error: Unknown symbol '_initval__0002'
//
// with "Bad number of parameters in call to f: expected 2 found 1" before it where
// the literal is a struct argument. The same call as a statement, `f((S){1, 2});`,
// and the same value as a variable's initializer build. Measured 2026-10-04 with the
// in-process flexcc and with a native build of spin2cpp v7.7.3 (eb263961, upstream's
// master), which refuse it alike.
//
// It is loud, not silent: nothing is built. In OctoGo a blank assignment is the
// cast, `_ = x` being `(void)(x);`, so `_ = gs == S{1, 2}`, `_ = &S{1, 2}`, `_ =
// A([3]int{1, 2, 3})` and `_ = L(nil)` for a defined slice type did not build. The
// emitter binds such a value to a temporary first and discards the temporary
// (emitDiscard). Found by a sweep of the target build over the probe programs'
// warnings and refusals. Not reported.
//
// To check, build for the P2 with -2: the build is refused.

typedef struct S {
	int a, b;
} S;

S gs = {1, 2};

int f(S x) { return x.a; }

int eq(S x, S y) { return x.a == y.a && x.b == y.b; }

int main(void) {
	(void)((S){1, 2});
	(void)(&(S){1, 2});
	(void)(f((S){1, 2}));
	(void)(eq(gs, (S){1, 2}));
	return 0;
}
