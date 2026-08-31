// The target's C compiler cannot parse a declarator whose IDENTIFIER is also a
// typedef name, in three positions this compiler emits. Valid C99 -- an ordinary
// identifier and a typedef name live in different namespaces there, and a struct
// member's name lives in the member namespace besides -- and gcc compiles all three:
//
//	                                        gcc     flexcc -2
//	struct VT { void (*Sample)(Sample*); };  ok     error: syntax error, unexpected type name `Sample'
//	void f(Sample Sample) { ... }            ok     error: syntax error, unexpected type name
//	Sample Sample = {0};                     ok     error: syntax error, unexpected '='
//
// for a `typedef struct { int v; } Sample;`. The parser evidently resolves the
// identifier as a type name before the declarator is read, so any position where a
// user's name meets its own type's name is refused.
//
// It reaches ordinary OctoGo three ways. A method named after a type -- `Sample()
// Sample`, `Frame() Frame`, which is idiomatic Go -- puts that name in an
// INTERFACE's vtable, which is a struct of function pointers; and a local or a
// parameter named after its type puts it in a declaration. A struct FIELD named
// after its type is fine, as is a method on a concrete type, whose C name is
// prefixed by the receiver's type already.
//
// Measured 2026-08-31 against spin2cpp 2bd01c4c, flexcc -2 as ogo build invokes it.
//
// WORKED AROUND for the vtable in internal/octogo/emit.go (vtMember): the table's
// members are the compiler's own names, so they are prefixed `ogo_m_` and can no
// longer collide with anything the program declares. The local and the parameter
// keep the user's name and are refused by the backend; that is a gap, not a
// miscompile. Unreported upstream as of this writing.
//
// To check, compile it: gcc takes it, the target's compiler does not.

typedef struct { int v; } Sample;

struct VT { const char* t; void (*Sample)(void*, Sample*); };

int take(Sample Sample) { return Sample.v; }

int main(void) {
	Sample Sample = {0};
	Sample.v = 3;
	return take(Sample);
}
