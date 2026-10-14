// A by-value argument of two to four longs -- a struct the target passes in
// registers, a long long -- whose expression has a side effect is evaluated once per
// long. `take2(*get2())` calls get2 twice for a struct of two ints, three times for
// three, four for four. This is a silent miscompile: nothing is said, and a counter
// the callee bumps, or a value it pops, comes out wrong.
//
// Measured 2026-10-14 on a P2-EDGE with this repository's in-process backend
// (spin2cpp b5993a84 with the carried CORDIC hunk, the flags ogo build passes) by
// scripts/cboard.sh, and with a native build of the same tree. gcc calls each
// accessor once on every line below; the board:
//
//	take2(*get2())            2 calls      a struct of two ints
//	take3(*get3())            3            of three
//	take4(*get4())            4            of four
//	take1(*get1())            1            one int: right
//	take2(garr[idx()])        2            an element indexed by a call
//	take2(getq()->q)          2            a member through a call
//	take2(geta()->arr[1])     2            an element of a member through a call
//	ntake2(1, *get2())        2            not the first argument
//	itake2(*get2())           2            a callee marked for inlining
//	fp(*get2())               2            through a function pointer
//	take2(mk2())              1            a call returning the struct: right
//
// An assignment, `t = *get2()`, a return and an initializer are right.
//
// The cause, read in spin2cpp's source: functions.c passes an argument of n > 1
// longs through n `_parm_` temporaries, by the multiple assignment `(_parm_0,
// _parm_1) = expr`, and the backend's BuildExprlistFromObject (expr.c, asked from
// outasm.c's AST_ASSIGN) rewrites an expression that is neither a call nor a list as
// one member reference per long -- `expr.a, expr.b` -- each of which evaluates expr
// again. A 64-bit scalar is split the same way, into GETLOW(expr) and GETHIGH(expr).
// An assignment does not meet it because hltransform.c pulls the side effects of a
// right side wider than a long into temporaries first (ExtractSideEffects); the
// argument path, which runs later, never asks.
//
// In OctoGo every string, slice header and interface value is such a struct, and an
// int64 such a scalar: `takeS(strs[idx()])` called idx twice on the board,
// `sum(rows[next()])` three times, `take64(a64[i()])` and a value receiver
// `ps[idx()].sum()` twice -- the host right in each.
//
// A fix, tested natively: in functions.c, before the multiple assignment, the side
// effects of what such an argument reads through -- an index, a pointer an element
// or a member is read through, the base before the index -- are evaluated into
// temporaries of one long, so the split reads storage already there; the 64-bit
// branch splits the identifier it was handed rather than the original expression.
// Every line above then calls once on the board, and spin2cpp's `make test_offline`
// passes 588 of 588 with it as without. A first version handed the argument to
// ExtractSideEffects, the assignment's helper, which copies an ARRAY whose base has
// a side effect into a temporary of the array's type: an element of
// `(*f()->words)[g()]` of a uint64 array read garbage from the copy, which the run
// case "probe round 20: bit-packed telemetry" showed on the board and the
// reproducer did not.
//
// CARRIED since 2026-10-14 as internal/functions.c.diff, the backend regenerated
// with it the same day; the run case "a value of several words passed where its
// expression calls" holds it on the board. Not reported upstream yet.
//
// To check, build for the P2 with -2 and count: every line should end in the count
// gcc prints.

#include <stdio.h>

typedef struct { int a, b; } P2;
typedef struct { int a, b, c; } P3;
typedef struct { int a, b, c, d; } P4;
typedef struct { int a; } P1;
typedef struct { int n; P2 q; } Q;
typedef struct { P2 arr[3]; } A;

int calls;
P2 g2 = {1, 2};
P3 g3 = {1, 2, 3};
P4 g4 = {1, 2, 3, 4};
P1 g1 = {7};
Q gq = {1, {3, 4}};
A ga = {{{1, 2}, {3, 4}, {5, 6}}};
P2 garr[3] = {{1, 2}, {3, 4}, {5, 6}};

P2 *get2(void) { calls++; return &g2; }
P3 *get3(void) { calls++; return &g3; }
P4 *get4(void) { calls++; return &g4; }
P1 *get1(void) { calls++; return &g1; }
Q *getq(void) { calls++; return &gq; }
A *geta(void) { calls++; return &ga; }
int idx(void) { calls++; return 1; }
P2 mk2(void) { calls++; P2 r = {8, 9}; return r; }

int take2(P2 p) { return p.b; }
int take3(P3 p) { return p.c; }
int take4(P4 p) { return p.d; }
int take1(P1 p) { return p.a; }
int ntake2(int n, P2 p) { return n + p.b; }
#ifdef __FLEXC__
#define OGO_INLINE __attribute__((inline))
#else
#define OGO_INLINE
#endif
int itake2(P2 p) OGO_INLINE { return p.b; }
int (*fp)(P2) = take2;

#define R(name, expr) do { calls = 0; int v = (expr); printf("%-10s %d %d\n", name, v, calls); } while (0)

int main(void) {
	R("deref2", take2(*get2()));    // 2 1
	R("deref3", take3(*get3()));    // 3 1
	R("deref4", take4(*get4()));    // 4 1
	R("deref1", take1(*get1()));    // 7 1
	R("elemidx", take2(garr[idx()])); // 4 1
	R("member", take2(getq()->q));   // 4 1
	R("callelem", take2(geta()->arr[1])); // 4 1
	R("second", ntake2(1, *get2())); // 3 1
	R("inline", itake2(*get2()));    // 2 1
	R("fptr", fp(*get2()));          // 2 1
	R("mk2", take2(mk2()));          // 9 1
	return 0;
}
