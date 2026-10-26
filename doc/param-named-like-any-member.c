// A struct parameter passed BY VALUE that the backend keeps whole -- one with a
// member narrower than a long, a byte, a bool, a uint16, or an aggregate member --
// cannot be named like ANY member the function selects, of whatever struct:
//
//	typedef struct dir dir;  struct dir { FS *fs; unsigned short first; };
//	typedef struct it it;    struct it { int d; int i; };
//	void f(dir d, it *p) { p->d = 1; }   // error: Expecting identifier after '.'
//
// The same refusal for a read, `return p->d;`, a member of an aggregate type, a
// member reached through a call's result, `id(p)->d`, a struct of one uint16, of
// a byte or a bool beside an int, of five words with a uint16 last, and of an
// aggregate member (doc/param-named-like-member.c is that row for the parameter's
// own member). Right: the parameter renamed, a parameter of two ints or of a
// pointer and an int, a member selected that the parameter is not named like, and
// a pointer parameter. Measured with the in-process flexcc (spin2cpp b5993a84 with
// the carried diffs) 2026-10-26, the renamed form printing gcc's values on a
// P2-EDGE; found by a FAT file system's `func (d dir) create(..., it *dirIter)`
// storing `it.d = d` (dom72).
//
// It is loud. The emitter receives such a parameter under another name wherever
// its name is one a member may have, and copies it into a local of its own on
// entry (memberShadowParam).
//
// gcc prints, and the target should print once it builds:
//
//	1 2 3

#include <stdio.h>

typedef struct FS FS;
typedef struct dir dir;
typedef struct it it;

struct FS {
	int n;
};

struct dir {
	FS *fs;
	unsigned short first;
};

struct it {
	int d;
	int i;
};

int f(dir d, it *p) {
	p->d = d.fs->n;
	return d.first;
}

int main(void) {
	FS fs = {1};
	dir d = {&fs, 2};
	it x = {0, 3};
	int first = f(d, &x);
	printf("%d %d %d\n", x.d, first, x.i);
	return 0;
}
