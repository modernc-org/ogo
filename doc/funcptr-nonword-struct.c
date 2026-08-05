// Assigning a function to a function POINTER whose result is a struct with any
// member narrower or wider than a machine word warns, wrongly:
//
//	warning: incompatible pointer types in assignment: expected function of 1 args
//	returning __anon_x_c_00000003 but got function of 1 args returning unknown type
//
// The types are identical -- both are spelled by the same typedef -- and the
// generated code is CORRECT: the values a call through the pointer returns are right,
// verified on a P2-EDGE by the run case "a multi-result function as a value", which
// returns (int, bool), (int, string) and (int8, int16) through function values and
// checks every one against what `go run` prints. Only the diagnostic is wrong.
//
// Measured on 2026-08-04 with this repository's in-process backend (spin2cpp v7.7.0)
// and the flags ogo build passes (-2 -Ono-inline-small -Ono-peephole). Every host C
// compiler accepts the same file silently, gcc even under -Wall.
//
// What decides it is the MEMBER TYPES, not the tag and not the arity:
//
//	{int, int}            silent
//	{int, unsigned}       silent
//	{unsigned, unsigned}  silent
//	{int, _Bool}          warns
//	{int, char}           warns
//	{int, short}          warns
//	{char, char}          warns
//	{int, long long}      warns
//
// So a struct of machine words unifies and anything else becomes "unknown type".
// Giving the struct a TAG does not help -- the message then names _struct__ret_b
// instead of __anon_... and is otherwise identical -- so it is not about anonymity.
//
// This matters here because a multi-result OctoGo function used as a VALUE is
// exactly this shape: the function type's typedef returns the result struct, and
// `var g func(int) (int, bool) = flags` is the assignment above. A result list of
// plain ints stays silent, which is why the warning only appears for a mixed one.
//
// The workaround, NOT taken: an explicit cast, `fp_b cast = (fp_b)fb;`, silences it
// (and stays silent on gcc -Wall). It was declined because it would have to be
// emitted wherever a function name becomes a value -- a declaration, an assignment,
// an argument, a struct field -- and a cast on a function pointer is precisely what
// hides a genuine mismatch if the emitter ever produces one. The warning is recorded
// in the run case's backendWarning field instead, where it is visible and explained.
//
// The cost of that choice is real: a user compiling valid Go-shaped code sees a
// warning from `ogo build`. Reporting it upstream is the way to remove it; if it is
// fixed, drop the backendWarning field from that case and this file with it.
//
// READ THIS BEFORE CALLING ANOTHER ONE HARMLESS. The same weakness in a different
// position IS a miscompile: returning a call's struct result directly loses the
// narrow member, and warns while doing it (doc/return-nonword-struct.c, measured
// 2026-08-05). So a warning in this family is not reliably cosmetic -- this one was
// checked against real hardware and its values are right, and the other was assumed
// to be the same thing and was not. Measure each position on the board, and prefer a
// workaround that makes the warning GO AWAY: silence is then evidence. That is a
// second reason not to cast here, beyond the one above -- a cast would suppress a
// diagnostic now known to catch a real defect.
//
// To re-measure, compile with the target backend and read the line numbers.

#include <stdio.h>

#define CASE(n, t0, t1)                                                          \
	typedef struct {                                                             \
		t0 _0;                                                                   \
		t1 _1;                                                                   \
	} ret_##n;                                                                   \
	typedef ret_##n (*fp_##n)(int);                                              \
	ret_##n f##n(int x) {                                                        \
		ret_##n r = {(t0)x, (t1)x};                                              \
		return r;                                                                \
	}

CASE(a, int, int)
CASE(b, int, _Bool)
CASE(c, int, char)
CASE(d, int, short)
CASE(e, char, char)
CASE(f, int, unsigned)
CASE(g, int, long long)
CASE(h, unsigned, unsigned)

// The same shape as b, with a tag: it warns too.
typedef struct ret_tagged {
	int _0;
	_Bool _1;
} ret_tagged;
typedef ret_tagged (*fp_tagged)(int);
ret_tagged ftagged(int x) {
	ret_tagged r = {x, x > 0};
	return r;
}

int main(void) {
	fp_a a = fa; // silent
	fp_b b = fb; // warns
	fp_c c = fc; // warns
	fp_d d = fd; // warns
	fp_e e = fe; // warns
	fp_f f = ff; // silent
	fp_g g = fg; // warns
	fp_h h = fh; // silent
	fp_tagged t = ftagged; // warns
	fp_b cast = (fp_b)fb;  // silent -- the workaround that was declined

	printf("%d\n", a(1)._0 + b(1)._0 + c(1)._0 + d(1)._0 + e(1)._0 + f(1)._0 +
	                   (int)g(1)._0 + (int)h(1)._0 + t(1)._0 + cast(1)._0);
	return 0;
}
