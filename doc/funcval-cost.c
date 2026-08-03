// What a Go-style "funcval" function value would cost on this target, measured.
//
// Go represents a function value as a single word pointing at a struct whose first
// word is the code pointer (the rest being captured variables). The design is from
// a 2013 golang-dev thread -- proposed as a pointer to a two-word {code, data}
// struct, adopted by rsc as a pointer to a variable-sized struct with the code
// pointer first, so that even reflection avoids allocating. It is what makes a
// METHOD VALUE possible: `f := q.Area` has to carry its receiver beside the code,
// and a bare C function pointer cannot.
//
// ogo represents a function value as the raw C function pointer instead, which is
// why it has no method values. Adopting the funcval design is therefore a real
// option, and this file is the measurement that priced it.
//
// Measured on a P2-EDGE at 200 MHz through this repository's backend (spin2cpp
// v7.7.0, with the -Ono-inline-small -Ono-peephole that ogo build passes), 20000
// calls through a 4-entry dispatch table, cycles from _cnt():
//
//	variant                                cycles   per call    vs A
//	-------------------------------------------------------------------
//	D  direct calls, a 4-way switch       3160064    158.0     +3.9%
//	A  raw code pointer (today)           3040056    152.0        --
//	E  raw pointer + a dead ctx argument  3120064    156.0     +2.6%
//	C  funcval, no thunk                  3760056    188.0    +23.7%
//	B  funcval + a thunk per function     4440056    222.0    +46.1%
//
// Sizes: A 8828 bytes, C 8832, B 8896, D 8744.
//
// Each variant was built as a program of its own for those, which is how a real one
// would be. Built as this file is -- all three in one binary, so their code shares a
// layout -- the same board reports A 3080056, C 3560056, B 4400056: +15.6% and
// +42.9%. Read the separate builds as the number and these as the confirmation.
//
// Read as an attribution, each line adding one thing to the one above it:
//
//   - the hidden context argument costs 4 cycles a call (E - A). Nothing.
//   - the second load costs 32 (C - E). This is the design itself: the value is a
//     pointer to a struct, so reaching the code is a SECOND dependent hub read,
//     which cannot overlap the first. On this part that is the whole cost.
//   - a thunk costs another 34 (B - C), because it makes every indirect call two
//     calls. Avoidable: let a function used as a value take the context as its own
//     first parameter, so the funcval points at the real function. Then a DIRECT
//     call to it passes a null context and pays the 4 cycles above.
//
// So the honest price of method values, done well, is ~24% on every call through a
// function value -- a dispatch table's inner loop -- and ~0 on everything else. It
// is not the extra word and not the extra argument; it is the dependent hub read,
// and no arrangement of the C removes it.
//
// Worth knowing beside it: an indirect call through a table (A) is FASTER than a
// four-way switch over direct calls (D) here. A dispatch table is already the right
// shape on this target, which is what makes slowing it down the wrong trade.
//
// What was built instead: a method value whose receiver is BOUND AT COMPILE TIME.
// `f := gp.bump` for a package-level gp lifts to a function of its own that names
// gp, so the value stays a one-word function pointer and nothing else pays. It
// handles a pointer-receiver method on a package-level variable and refuses the
// rest -- which is the subset where binding an address means exactly what Go means.
//
// The decision recorded with this: not adopted. If it is revisited, the way to
// avoid a non-local cliff -- one method value making an unrelated dispatch table
// 24% slower -- is to choose the representation PER SIGNATURE rather than per
// program: the whole program is one translation unit, so the compiler can see which
// function types a method value is ever made of. And revisit after devirtualization
// exists, since that is what removes the indirection where the target is provable.
//
// To re-measure, compile each variant below on its own (they each define main) and
// read the numbers.

#include <propeller2.h>
#include <stdio.h>

#define ROUNDS 20000

// ---------------------------------------------------------------- variant A
// Today: a function value IS the C function pointer. The element is bound to a
// temporary before the call, which is the workaround from
// doc/call-through-array-element.c.

int a_add(int a, int b) { return a + b; }
int a_sub(int a, int b) { return a - b; }
int a_mul(int a, int b) { return a * b; }
int a_quo(int a, int b) { return a / b; }

typedef int (*A_Op)(int, int);

static A_Op a_table[4];

int variant_A(void) {
	a_table[0] = a_add;
	a_table[1] = a_sub;
	a_table[2] = a_mul;
	a_table[3] = a_quo;
	int sum = 0;
	unsigned t0 = _cnt();
	for (int i = 0; i < ROUNDS; i++) {
		A_Op f = a_table[i & 3];
		sum += f(i, 7);
	}
	unsigned t1 = _cnt();
	printf("A sum=%d cycles=%u\n", sum, t1 - t0);
	return sum;
}

// ---------------------------------------------------------------- variant C
// The funcval, without a thunk: the functions used as values take the context as
// their own first parameter, so a call through a value is one call and not two.

struct ogo_fv;

typedef int (*C_code)(const struct ogo_fv *, int, int);

struct ogo_fv {
	C_code code;
};

typedef const struct ogo_fv *C_Op;

int c_add(const struct ogo_fv *ctx, int a, int b) { (void)ctx; return a + b; }
int c_sub(const struct ogo_fv *ctx, int a, int b) { (void)ctx; return a - b; }
int c_mul(const struct ogo_fv *ctx, int a, int b) { (void)ctx; return a * b; }
int c_quo(const struct ogo_fv *ctx, int a, int b) { (void)ctx; return a / b; }

static const struct ogo_fv c_add_fv = {c_add};
static const struct ogo_fv c_sub_fv = {c_sub};
static const struct ogo_fv c_mul_fv = {c_mul};
static const struct ogo_fv c_quo_fv = {c_quo};

static C_Op c_table[4];

int variant_C(void) {
	c_table[0] = &c_add_fv;
	c_table[1] = &c_sub_fv;
	c_table[2] = &c_mul_fv;
	c_table[3] = &c_quo_fv;
	int sum = 0;
	unsigned t0 = _cnt();
	for (int i = 0; i < ROUNDS; i++) {
		C_Op f = c_table[i & 3];
		sum += f->code(f, i, 7);
	}
	unsigned t1 = _cnt();
	printf("C sum=%d cycles=%u\n", sum, t1 - t0);
	return sum;
}

// ---------------------------------------------------------------- variant B
// The funcval with a thunk per function, which is what a first implementation
// reaches for: it leaves the real functions' signatures alone, and costs an extra
// call frame on every call through a value.

static int b_add_thunk(const struct ogo_fv *ctx, int a, int b) { (void)ctx; return a_add(a, b); }
static int b_sub_thunk(const struct ogo_fv *ctx, int a, int b) { (void)ctx; return a_sub(a, b); }
static int b_mul_thunk(const struct ogo_fv *ctx, int a, int b) { (void)ctx; return a_mul(a, b); }
static int b_quo_thunk(const struct ogo_fv *ctx, int a, int b) { (void)ctx; return a_quo(a, b); }

static const struct ogo_fv b_add_fv = {b_add_thunk};
static const struct ogo_fv b_sub_fv = {b_sub_thunk};
static const struct ogo_fv b_mul_fv = {b_mul_thunk};
static const struct ogo_fv b_quo_fv = {b_quo_thunk};

static C_Op b_table[4];

int variant_B(void) {
	b_table[0] = &b_add_fv;
	b_table[1] = &b_sub_fv;
	b_table[2] = &b_mul_fv;
	b_table[3] = &b_quo_fv;
	int sum = 0;
	unsigned t0 = _cnt();
	for (int i = 0; i < ROUNDS; i++) {
		C_Op f = b_table[i & 3];
		sum += f->code(f, i, 7);
	}
	unsigned t1 = _cnt();
	printf("B sum=%d cycles=%u\n", sum, t1 - t0);
	return sum;
}

int main(void) {
	int sum = variant_A() + variant_C() + variant_B();
	printf("checksum %d\n", sum);
	return 0;
}
