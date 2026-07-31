// A compound literal inside a cast. Valid C, and gcc compiles all three of these
// silently:
//
//	cc -c -o /dev/null complit-arg-in-cast.c
//
// flexcc does not. Built from spin2cpp v7.7.0:
//
//	flexcc -2 -o t.binary complit-arg-in-cast.c
//	complit-arg-in-cast.c:NN: warning: Bad number of parameters in call to total: expected 3 found 1
//
// and the call it then generates does not pass the value. The same shape with the
// call made through a FUNCTION POINTER, or with a slice header for the literal, is
// refused outright, which is where compilation of this file stops.
//
// The third function has to be compiled on its own to be seen: a cast of a field
// read straight off a literal makes flexcc dereference a nil pointer and die.
// Measured through this repository's in-process backend, where it surfaces as a Go
// panic out of the transpiled compiler; a stock flexcc binary was not tried.
//
// The cast alone is fine: (int)(total(s)) for a variable s compiles and runs. The
// literal alone is fine: total((S){1, 2, 3}) as a whole statement, in arithmetic,
// or as an argument of another call all compile and run. It is the two together.
//
// ogo works around it by binding the cast's operand to a temporary, which puts the
// literal outside the cast (see emitConversion in internal/octogo/emit.go). The
// shape a reader writes to reach it is `int(total(xs[:]))` -- a slice expression
// handed to a call, inside a conversion.
//
// To check whether the workaround is still needed, compile this file with the
// backend in internal/flexcc, and viaField again with the other two deleted; if all
// three are accepted, the hoist can go.

typedef struct {
	int a, b, c;
} S;

typedef int (*Fn)(S);

int total(S s) { return s.a; }

// Warns, and passes nothing.
int viaCast(void) { return (int)(total((S){1, 2, 3})); }

// Refused: "Internal error, asm code cannot handle assignment".
int viaPointer(void) {
	Fn f = total;
	return (int)(f((S){1, 2, 3}));
}

// Crashes the compiler.
int viaField(void) { return (int)((S){1, 2, 3}.a); }

int main(void) { return viaCast() + viaPointer() + viaField() - 3; }
