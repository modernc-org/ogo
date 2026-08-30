// The target's C compiler refuses a member of a compound literal, and then
// crashes. Valid C99; gcc compiles and runs every line:
//
//	typedef struct { const char* str; int len; } ogo_string;
//	int n = ((ogo_string){"abc", 3}).len;      gcc: 3     flexcc: error: request for member len in something not an object
//	ogo_string s = (ogo_string){"abc", 3};     fine on both
//	int m = s.len;                             fine on both
//
// After the error, flexcc (the transpiled spin2cpp 2bd01c4c in internal/flexcc)
// dereferences a nil pointer in its expression compiler -- IsCogMem called on the
// failed expression -- and the build process panics instead of exiting with the
// diagnostic. A native spin2cpp of the same commit reports the error and stops.
//
// Measured 2026-08-30 on the host, flexcc -2 as ogo build invokes it.
//
// It reached ordinary OctoGo as `msg[len(msg)-2]` with a `const msg`: a string
// constant is spelled as a compound literal at every use, and len read the
// length off it. WORKED AROUND in emitLen: the length of a constant string is
// folded to its byte count. Unreported upstream as of this writing.
//
// To check, build for the P2 and read whether the first line compiles.

#include <stdio.h>

typedef struct { const char* str; int len; } ogo_string;

int main(void) {
	int n = ((ogo_string){"abc", 3}).len;
	ogo_string s = (ogo_string){"abc", 3};
	printf("%d %d\n", n, s.len);
	return 0;
}
