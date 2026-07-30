// A struct member whose name is also a type name. C keeps member names in a
// namespace of their own, so this is valid, and gcc compiles it silently:
//
//	cc -c -o /dev/null field-named-like-a-type.c
//
// flexcc refuses it. Built from spin2cpp v7.7.0:
//
//	flexcc -2 -o t.binary field-named-like-a-type.c
//	field-named-like-a-type.c:14: error: Internal error, confusing type declaration
//
// The line it names is the one BEFORE the member, which is what made this hard to
// place from the emitted C alone. A member named after a struct type gives
// "Unable to combine types" instead, at the same off-by-one line.
//
// ogo works around it: a member whose name collides with a type name is renamed in
// the emitted C (see fieldIdent in internal/octogo/emit.go). To check whether the
// workaround is still needed, compile this file with the backend in
// internal/flexcc; if it is accepted, fieldIdent can go.

typedef int e;

struct t {
	int e;
};

int main(void) {
	struct t v;
	v.e = 1;
	return v.e - 1;
}
