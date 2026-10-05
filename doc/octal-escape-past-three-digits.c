// An octal escape runs past three digits on the P2: "\0337", which C reads as ESC
// followed by a '7', is ONE character there, 0337, and the string a character
// short. Found by p2-11 (2026-09-30): a VT100's DECSC is ESC 7, and the string that
// held it printed wrong on the board while the host was right.
//
// Measured on a P2-EDGE with the flags ogo build passes, spin2cpp v7.7.3
// (eb263961), printing each array's length as sizeof says and its bytes in octal:
//
//	                  gcc                   flexcc
//	"\0337"           2: 033 067            1: 337
//	"\0010"           2: 001 060            1: 010
//	"a\0337b"         4: 141 033 067 142    3: 141 337 142
//	"\3770\2007"      4: 377 060 200 067    2: 370 007
//	"\033" "7"        2: 033 067            2: 033 067
//
// C takes three octal digits in an escape at most; the lexer (frontends/lexer.c,
// getEscapedChar) goes on while the next character is 0-7, keeping the low byte of
// what it read. The last line is the way round it: a literal closed after the
// escape and another opened, which C joins after the escapes are read. ogo writes
// that wherever a digit 0-7 follows an octal escape (cQuote); it writes octal at
// all because C's hex escape has no limit in C itself.
//
// Reported 2026-10-01 with a tested fix as flexprop#115: three digits at most in
// getEscapedChar, whose two callers, parseCString and getCChar, are both C. Built
// natively at v7.7.3, the reproducer prints gcc's values on every line on the
// board, and upstream's make test_offline passes as it does without it.
//
// FIXED upstream 2026-10-05 by spin2cpp 639a8c8e (7.7.4-beta, the commit after our
// pin eb263961), Eric Smith's own change with the same rule, three digits at most,
// and #115 closed. Built natively there, the reproducer prints gcc's values on every
// line on a P2-EDGE. Not adopted: cQuote's closed literal is right under either
// lexer and costs nothing, so it stays, and the fix comes in with the next
// regeneration's pin.

#include <stdio.h>

static void show(const char *name, const char *s, int n) {
	printf("%-10s %d:", name, n);
	for (int i = 0; i < n; i++) {
		printf(" %03o", s[i] & 0xff);
	}
	printf("\n");
}

int main(void) {
	static const char a[] = "\0337";
	static const char b[] = "\0010";
	static const char c[] = "a\0337b";
	static const char d[] = "\3770\2007";
	static const char e[] = "\033" "7";
	show("\\0337", a, sizeof a - 1);
	show("\\0010", b, sizeof b - 1);
	show("a\\0337b", c, sizeof c - 1);
	show("\\3770\\2007", d, sizeof d - 1);
	show("\\033\" \"7", e, sizeof e - 1);
	return 0;
}
