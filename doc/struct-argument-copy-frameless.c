// A function that has no frame of its own and passes a struct by value leaves the
// stack higher, at every call, by the copy of the struct it made. Valid C; gcc
// leaves the stack where it was:
//
//	                                  gcc   flexcc -2           flexcc -2, fixed
//	minute(t), 1000 calls             0     16000 bytes lost    0
//	plain(t), 1000 calls              0     16000 bytes lost    0
//	addOne(t, i), 1000 calls          0     0                   0   a register kept across the call: a frame
//	absT(t), 1000 calls               0     0                   0   a local array of its own: a frame
//
// for a T of an int64_t, an int32_t and a pointer, 16 bytes, which flexcc passes
// by reference to a copy -- as it passes every struct over 16 bytes and every
// struct with a member that is not a long. minute and plain do nothing but hand t
// on, so they need neither a register saved nor a local of their own, and get no
// frame; the copy of t they make for the call is `mov arg01, ptra; add ptra, #16`,
// and nothing gives it back, a frame's `mov ptra, fp` being what does that. The
// stack climbs until it runs past the end of Hub RAM and the program stops, or
// writes over whatever is there. An inlined copy of such a function does the same
// in its caller's loop.
//
// THE CAUSE, read 2026-10-28 in spin2cpp's functions.c: AstFuncTempMemory, which
// frontends/types.c calls for each argument passed to a copied reference
// parameter (AST_COPYREFTYPE), puts an AST_ALLOCA at the top of the function's
// body and does not set the function's uses_alloca, which an alloca written in
// the source sets in the front end's pass, before the types are checked. The asm
// backend's NeedFramePointer gives a function using alloca a frame, and the
// inliner's NeverInline refuses to inline one, both by that flag; without it the
// function is frameless. Upstream's own Test/Expect/stest184.pasm, a struct of
// five ints passed on, shows the stack moved up and never moved back.
//
// Measured on a P2-EDGE 2026-10-28 against spin2cpp b5993a84 with the carried
// diffs, flexcc -2 as ogo build invokes it; the same before the regeneration of
// 2026-10-25, and in every release of ogo checked, v0.40.0 among them. FIXED in
// internal/functions.c.diff: AstFuncTempMemory sets uses_alloca. Nothing warns.
//
// It reaches ordinary OctoGo as a value method that hands its receiver on,
// `func (t Time) Minute() int { return int(t.abs()%3600) / 60 }` as Go's time
// package writes it, and as any function forwarding a struct parameter: dom78's
// cron search stopped printing after 10835 passes, its stack at 0x7d430 of 0x80000.
#include <stdio.h>
#include <stdint.h>

typedef struct {
	int64_t sec;
	int32_t nsec;
	void *loc;
} T;

// a frame of its own: a local array
uint64_t absT(T t) {
	volatile uint32_t w[3];
	w[0] = (uint32_t)t.sec;
	w[1] = (uint32_t)(t.sec >> 32);
	w[2] = (uint32_t)t.nsec;
	return ((uint64_t)w[1] << 32 | w[0]) + w[2];
}

// no frame: nothing of their own but passing t on
int minute(T t) { return (int)(absT(t) % 3600u) / 60; }
int64_t plain(T t) { return absT(t); }

// a frame: k is kept across the call
int addOne(T t, int k) { return (int)absT(t) + k; }

// the address of its local is the stack pointer where it is called, called
// through a pointer so that no compiler inlines it
int sp0(void) {
	volatile int x = 0;
	return (int)(intptr_t)&x;
}

int (*volatile sp)(void) = sp0;

int main(void) {
	T t = {63988228103LL, 7, 0};
	int m = 0;
	int s0 = sp();
	for (int i = 0; i < 1000; i++) {
		m += minute(t);
	}
	int s1 = sp();
	for (int i = 0; i < 1000; i++) {
		m += (int)plain(t);
	}
	int s2 = sp();
	for (int i = 0; i < 1000; i++) {
		m += addOne(t, i);
	}
	int s3 = sp();
	for (int i = 0; i < 1000; i++) {
		m += (int)absT(t);
	}
	int s4 = sp();
	printf("minute %d plain %d addOne %d absT %d bytes lost over 1000 calls each, m %d\n", s1 - s0, s2 - s1, s3 - s2, s4 - s3, m);
	return 0;
}
