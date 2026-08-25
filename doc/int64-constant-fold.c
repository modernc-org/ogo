// flexcc mis-folds a 64-BIT CONSTANT EXPRESSION inside a function body. Any two
// constant operands joined by + - * or /, where either is a long long -- a cast of a
// literal, an LL/ULL literal -- comes out as garbage, on either side of the operator,
// at -O0, -O1 and -2 alike. The same expression with a VARIABLE operand is right, a
// lone 64-bit literal is right, and the same fold at FILE scope (a global's
// initializer) is right. gcc computes all of it correctly.
//
// Measured on a P2-EDGE against flexprop v7.7.0, the backend in internal/flexcc,
// with the flags ogo build passes (-2 -Ono-inline-small -Ono-peephole). From a
// 288-expression matrix (constant form x side x other operand x operator):
//
//	                                  gcc            flexcc v7.7.0
//	(int64_t)(5) + 1                  6              -260437625599426554
//	1 + (int64_t)5                    6              3689348818177884166
//	5LL + 1                           6              3689348818177884166
//	5LL * 3LL                         15             3689348818177884175
//	4294967296LL - 1                  4294967295     3689348822472851455
//	(int64_t)(-5) * 1                 -5             8589934587
//	5ULL + 1                          6              18186306448110125062
//	(int64_t)(5) + (1 + 2)            8              25769803784
//	(int64_t)(5) + x, x int32 var     -2             -2             (correct: every variable shape)
//	(int64_t)(5) + 1 + y, y int64     ...            ...            (correct: a constant PREFIX before a variable)
//	int64_t gi = (int64_t)(5) + 1;    6              6              (correct: a file-scope initializer)
//	6LL alone                         6              6              (correct)
//	-5LL + 1                          -4             -4             (correct, oddly: a NEGATIVE LL literal folds)
//
// It reached OctoGo because a constant expression whose value fits an int was left
// as written, on the premise that C folds it the same way: `int64(5) + 1` printed
// 4294967296000006 on the board, `int64(1000) * 1000` printed 4294967302,
// `uint64(5) + 1` 4294967302, `Ticks(3) * 4` 4294967308, `int64(-3) * int64(7)`
// 51539607531. The compiler now folds every 64-bit constant expression itself and
// emits ONE literal (constSpelling in internal/octogo/emit.go); a 64-bit named
// constant is inlined at each use for the same reason.
//
// Two spellings measured while fixing it:
//
//	int64_t a[2] = {(-4294967295LL), 1};   flexcc: "Bad constant expression" -- a unary minus is refused in
//	                                       ANY aggregate initializer, local ones included; the bit pattern
//	                                       0xFFFFFFFF00000001ULL is what works there
//	v > 0xFFFFFFFF00000001ULL (v int64)    0 on BOTH compilers: an unsigned operand makes the comparison
//	                                       unsigned, so a negative constant spelled as its bit pattern
//	                                       compared wrongly wherever it stood in an expression
//	v > (-4294967295LL)                    1 on both (the spelling now used in expressions)
//
// The garbage is not stable: the same expression prints a different wrong value in
// a different program (the matrix above and this file disagree on `(int64_t)(5) + 1`),
// which is one reason a workaround was not attempted at the level of spellings.
//
// This file prints, on the board and under gcc:
//
//	A 25769803782 64424509446 -1042943914956112548 4611700105920118786    A 6 6 6 15
//	B -17179869185 34359738374 17403800158753439068 4611700294898679810    B 4294967295 -5 6 8
//	C -2 4294967307 6 6                                                    C -2 4294967307 6 6
//	D 0 1                                                                  D 0 1
//
// LIVE UPSTREAM: spin2cpp master (a430da8, 2026-08-21) prints every line identically
// to v7.7.0. Unreported as of 2026-08-25.

#include <stdio.h>
#include <stdint.h>

int32_t x = -7;
int64_t y = 4294967301LL;
int64_t gi = (int64_t)(5) + 1;

int main(void) {
    printf("A %lld %lld %lld %lld\n", (int64_t)(5) + 1, 1 + (int64_t)5, 5LL + 1, 5LL * 3LL);
    printf("B %lld %lld %llu %lld\n", 4294967296LL - 1, (int64_t)(-5) * 1, 5ULL + 1, (int64_t)(5) + (1 + 2));
    printf("C %lld %lld %lld %lld\n", (int64_t)(5) + x, (int64_t)(5) + 1 + y, gi, 6LL);
    int64_t v = 5;
    printf("D %d %d\n", v > 0xFFFFFFFF00000001ULL, v > (-4294967295LL));
    return 0;
}
