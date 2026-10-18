// A printf of a constant format evaluates its arguments under the I/O lock. The
// target's compiler writes such a printf as its own print calls between __lockio,
// which takes the FILE's lock (_lockmem), and the store that frees it, and the
// arguments are evaluated in between. C evaluates the arguments before the call, and
// printf's lock is printf's own; here an argument that waits for another cog holds
// the lock while it waits, and that cog's printf waits for the lock.
//
// Measured on a P2-EDGE, built with the flexcc in internal/flexcc (spin2cpp
// b5993a84). The cog prints at once and main's argument takes 500 ms, so C prints
// the cog's line first and the cog's printf returns at once; this prints
//
//	main 7
//	cog 1
//	the cog's printf took 500 ms
//
// main's line first, the cog's printf blocked for as long as main's argument ran.
// Where the argument waits for the cog to do something after its printf -- a
// receive of what the cog sends next -- neither ever prints: `go work(); println(<-out)`
// in OctoGo, with work printing a number before it sends, printed nothing at all.
// putchar takes no lock, so a cog writing a string through it was not held.
//
// ogo's emitter binds a print's argument that has an effect -- a receive, a call --
// to a temporary before the print (hoistPrintArgs). To check whether this is still
// so, run this on a board and read the lines.

#include <propeller2.h>
#include <stdio.h>

static long stack[256];
static volatile int started, took = -1;

static void cog(void* p) {
	(void)p;
	started = 1;
	int t0 = _getms();
	printf("cog %d\n", 1);
	took = _getms() - t0;
	for (;;) {
	}
}

static int slow(void) {
	while (!started) {
	}
	_waitms(500);
	return 7;
}

int main(void) {
	_cogstart_C(cog, 0, stack, sizeof(stack));
	printf("main %d\n", slow());
	while (took < 0) {
	}
	printf("the cog's printf took %d ms\n", took);
	for (;;) {
	}
}
