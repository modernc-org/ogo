// flexcc miscompiles a STRUCT WITH PADDING returned through a FUNCTION POINTER in
// code running on a SPAWNED COG. All three are needed: the same call on the main
// cog is right, the same struct returned by a DIRECT call on a cog is right, and a
// struct with no padding through a pointer on a cog is right. What goes wrong is
// not a wrong value: the write lands somewhere it should not and takes the program
// with it -- the cog stops, and the main cog dies at its next statement.
//
// Measured on a P2-EDGE against flexprop v7.7.0 with the flags ogo build uses
// (-2 -Ono-inline-small -Ono-peephole), one program per shape, each returning the
// struct through `static const vt table = {impl}; static const vt *tp = &table;`
// and calling `tp->f(...)` from a cog started with _cogstart_C:
//
//	struct              size  padding  result
//	{int, int}            8   none     correct
//	{int, int, int}      12   none     correct
//	{char, char}          4   tail     correct   (fits in one word)
//	{double, int}         8   none     correct   (double is 4 bytes here)
//	{int, char}           8   yes      DIES
//	{int, short}          8   yes      DIES
//	{long long, int}     12   yes      DIES
//	{int, {int, char}}   12   yes      DIES
//
// So: larger than one word AND holding padding. A one-word struct is returned in a
// register and never reaches the faulty path.
//
// It reaches ordinary OctoGo through an interface method with several results --
// `Next() (int32, bool)`, whose result struct is {int32_t, _Bool} and so is
// exactly the failing shape -- called on a goroutine, which is a cog:
//
//	var src Source          // an interface
//	func feed(c chan int32) { v, _ := src.Next(); c <- v }   // on a cog: dies
//
// The whole program produced NO output: main printed nothing after `go feed(ch)`.
//
// Worked around by giving the vtable slot an OUT PARAMETER: a method of several
// results writes them through a trailing pointer and its slot returns void, so
// nothing is returned through the function pointer at all. The thunk's own call to
// the concrete method is direct, which is correct. See ifaceMethod.out in
// internal/octogo/emit.go.
//
// STILL UNFIXED for a multi-result FUNCTION VALUE (`fn := two; a, b := fn(3)` on a
// cog), which is the same call through a pointer. That path also draws a flexcc
// diagnostic about the typedef's return type on the main cog, so it is not a
// shipped shape; both are one piece of work, to be done together.

#include <stdio.h>
#include <propeller2.h>

typedef struct { int a; char b; } padded; // 8 bytes, three of them padding

typedef struct { padded (*f)(void *); } vt;

static padded impl(void *p) {
	padded r = {*(int *)p, 1};
	return r;
}

static const vt table = {impl};
static const vt *tp = &table;

static int value = 7;
static volatile int got, ran;
static long stack[512];

static void cog(void *arg) {
	(void)arg;
	ran = 1;
	padded s = tp->f(&value); // <-- the fault: through a pointer, on a cog
	got = s.a * 100 + s.b;
}

int main(void) {
	printf("size=%d\n", (int)sizeof(padded));
	_cogstart_C(cog, 0, stack, sizeof stack);
	for (int i = 0; i < 16 && got == 0; i++) {
		_waitms(50);
	}
	// Never reached on a P2: the cog's write took the program down.
	printf("ran=%d got=%d (want 1 701)\n", ran, got);
	// The same call on THIS cog is correct.
	padded s = tp->f(&value);
	printf("main: %d %d (want 7 1)\n", s.a, (int)s.b);
	return 0;
}
