// Past its local register limit, the backend crashes rather than stopping. Given a
// function with more live values than its register allocator can place, spin2cpp
// says
//
//	error: Internal error exceeded local register limit, possibly due to -O2
//	optimization needing additional registers or code being too complicated
//
// and then renames a register it never allocated (RenameSubregs): a segmentation
// fault in a native build, exit status 139, and a nil dereference in the
// transpile, which `ogo build` reports as "flexcc crashed: ..." since 2026-09-25
// (compileC in internal/build) rather than dying with a Go stack trace.
//
// Thirty struct locals of three words, each passed by value twice, are enough.
// With ten the program builds; with twenty the cog register pool runs out first,
// "fit 480 failed", which is a clean error. OctoGo met it in a probe program of
// ten functions of about thirty calls each, every call converting several
// byte-slice constants -- slice headers, three words apiece.
//
// Measured with a native flexcc of spin2cpp v7.7.3 (eb263961) and with the
// in-process one (scripts/flexcc -2), 2026-09-25. To check whether this is still
// so, compile it: exit status 139 (native) or a Go panic (scripts/flexcc) is the
// crash, and a clean "exceeded local register limit" with exit status 1 would be
// the fix.

#include <stdio.h>

typedef struct {
	unsigned char *ptr;
	int len, cap;
} S;

static unsigned char buf[4];

int use(S a, S b) { return a.len + b.cap; }

int f(int k) {
	S v0 = {buf, k + 0, 0};
	S v1 = {buf, k + 1, 1};
	S v2 = {buf, k + 2, 2};
	S v3 = {buf, k + 3, 3};
	S v4 = {buf, k + 4, 4};
	S v5 = {buf, k + 5, 5};
	S v6 = {buf, k + 6, 6};
	S v7 = {buf, k + 7, 7};
	S v8 = {buf, k + 8, 8};
	S v9 = {buf, k + 9, 9};
	S v10 = {buf, k + 10, 10};
	S v11 = {buf, k + 11, 11};
	S v12 = {buf, k + 12, 12};
	S v13 = {buf, k + 13, 13};
	S v14 = {buf, k + 14, 14};
	S v15 = {buf, k + 15, 15};
	S v16 = {buf, k + 16, 16};
	S v17 = {buf, k + 17, 17};
	S v18 = {buf, k + 18, 18};
	S v19 = {buf, k + 19, 19};
	S v20 = {buf, k + 20, 20};
	S v21 = {buf, k + 21, 21};
	S v22 = {buf, k + 22, 22};
	S v23 = {buf, k + 23, 23};
	S v24 = {buf, k + 24, 24};
	S v25 = {buf, k + 25, 25};
	S v26 = {buf, k + 26, 26};
	S v27 = {buf, k + 27, 27};
	S v28 = {buf, k + 28, 28};
	S v29 = {buf, k + 29, 29};
	int s = 0;
	s += use(v0, v3);
	s += use(v1, v10);
	s += use(v2, v17);
	s += use(v3, v24);
	s += use(v4, v1);
	s += use(v5, v8);
	s += use(v6, v15);
	s += use(v7, v22);
	s += use(v8, v29);
	s += use(v9, v6);
	s += use(v10, v13);
	s += use(v11, v20);
	s += use(v12, v27);
	s += use(v13, v4);
	s += use(v14, v11);
	s += use(v15, v18);
	s += use(v16, v25);
	s += use(v17, v2);
	s += use(v18, v9);
	s += use(v19, v16);
	s += use(v20, v23);
	s += use(v21, v0);
	s += use(v22, v7);
	s += use(v23, v14);
	s += use(v24, v21);
	s += use(v25, v28);
	s += use(v26, v5);
	s += use(v27, v12);
	s += use(v28, v19);
	s += use(v29, v26);
	return s;
}

int main(void) {
	printf("%d\n", f(3));
	return 0;
}
