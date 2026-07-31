// _locknew() does not report exhaustion. The P2 has sixteen hardware locks, and
// LOCKNEW sets the C flag when none is free; flexcc's _locknew is documented to
// return -1 then. It does not: after handing out 0..15 it returns 15 for every
// further call, forever.
//
// Measured on a P2-EDGE, built with the flexcc in internal/flexcc (spin2cpp
// v7.7.0). Twenty calls print:
//
//	0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 15 15 15 15
//
// Two consequences, both silent:
//
//   - A caller cannot detect exhaustion. `int l = _locknew(); if (l < 0) ...`
//     never fires, so a program that means to fall back has no way to know.
//   - Two logically distinct locks alias. Sharing a lock is harmless where it only
//     costs contention -- ogo's channel rendezvous is one such place, and channels
//     past the sixteenth work correctly because of it -- but code that nests two
//     supposedly independent locks acquires the same one twice. _locktry is not
//     reentrant, so the inner attempt can never succeed and the program spins.
//
// ogo's channel runtime checks `ch->lock < 0` and panics "out of hardware locks".
// That check is dead: it cannot fire, which is why 24 channels each completing a
// rendezvous run correctly rather than reporting anything.
//
// To check whether this is still so, run this on a board and read the numbers.

#include <propeller2.h>
#include <stdio.h>

int main(void) {
	for (int i = 0; i < 20; i++) {
		printf("%d ", _locknew());
	}
	printf("\n");
	return 0;
}
