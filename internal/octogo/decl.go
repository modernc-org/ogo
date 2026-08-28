// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"go/constant"
	"maps"
	"slices"
)

// Name definitions for predeclared identifiers.
const preclaredNames = `// any is an alias for interface{} and is equivalent to it in all ways.
any
// bool is the set of boolean values, true and false.
bool
// byte is an alias for uint8 and is equivalent to uint8 in all ways. It is
// used, by convention, to distinguish byte values from 8-bit unsigned integer
// values.
byte
// false is an untyped boolean false value
false
// int is the set of all signed integers of the target's word size, 32 bits.
// It is a type of its own, distinct from int32, as it is in Go.
int
// int16 is the set of all signed 16-bit integers. Range: -32768 through 32767.
int16
// int32 is the set of all signed 32-bit integers. Range: -2147483648 through
// 2147483647.
int32
// int8 is the set of all signed 8-bit integers. Range: -128 through 127.
int8
// error is the conventional interface for representing an error condition, with
// the nil value representing no error. It is interface{ Error() string } under a
// name the universe holds, so a value of it is a pointer beside a table like every
// other interface value here -- which means a sentinel error is a package-level
// variable whose address is returned, there being no heap to make one in.
error
// nil is an untyped nil constant
nil
// rune is an alias for int32 and is equivalent to int32 in all ways. It is
// used, by convention, to distinguish character values from integer values.
rune
// true is an untyped boolean true value
true
// uint is the set of all unsigned integers of the target's word size, 32 bits.
// It is a type of its own, distinct from uint32, as it is in Go.
uint
// uint16 is the set of all unsigned 16-bit integers. Range: 0 through 65535.
uint16
// uint32 is the set of all unsigned 32-bit integers. Range: 0 through
// 4294967295.
uint32
// uint8 is the set of all unsigned 8-bit integers. Range: 0 through 255.
uint8
// uintptr is an integer type that is large enough to hold the bit pattern of
// any pointer.
uintptr
// append adds one element to a slice and returns the result. On this fixed-memory
// target a slice cannot grow past its capacity: the one-result form traps on
// overflow, while "s, ok = append(s, x)" reports through ok whether it fit.
append
// cap returns the capacity of a slice, the length of its backing array.
cap
// clear sets every element of its slice argument to the zero value.
clear
// copy copies elements between two slices of the same element type and returns
// the number copied, min(len(dst), len(src)); the two may overlap.
copy
// len returns the length of a string, array or slice.
len
// max returns the largest of its ordered arguments.
max
// min returns the smallest of its ordered arguments.
min
// print writes its arguments to the serial console with no separator or newline.
print
// printf writes its arguments to the serial console under the control of a format
// string, which must be a constant. The verbs are checked against the arguments'
// types when the program is compiled, so a mismatch is an error rather than
// nonsense at run time.
printf
// println writes its arguments to the serial console, space-separated and
// newline-terminated.
println
`

//TODO what is the size of a flexcc func pointer?

var (
	_ Declaration = (*ConstDeclaration)(nil)
	_ Declaration = (*ImportDeclaration)(nil)
	_ Declaration = (*PredeclaredFunc)(nil)
	_ Declaration = (*PredeclaredType)(nil)
	_ Declaration = (*VarDeclaration)(nil)
)

// PredeclaredFunc is a predeclared (built-in) function such as len or append. It
// is registered in the Universe so a use resolves and carries its doc comment
// from preclaredNames; the checker and emitter special-case each builtin, so no
// signature is modelled here. make and new are deliberately not registered --
// their use is validated on resolving to nothing (see checkFactorNames) -- and
// the builtins not yet emitted are left to isBuiltinFuncName.
type PredeclaredFunc struct {
	declaration
}

// Universe binds predefined declarations.
var Universe = newScope(nil, UniverseScope)

// errorSpecSrc carries the two names the predeclared error interface is written
// with that are not declarations of their own: the method's, and its result type's.
// A Token comes from a scanner and not from a literal -- it is an index into a
// source -- so they are scanned rather than made up.
const errorSpecSrc = `Error
string
`

// errorNames holds the tokens scanned from errorSpecSrc, by name.
var errorNames = map[string]Token{}

// scanNames returns every identifier in a source, by name. It is how the universe's
// declarations get tokens: the doc comments beside them are the point, and a token
// is what carries a position back to one.
func scanNames(fn, src string) map[string]Token {
	var p Parser
	sc := NewRecScanner(fn, []byte(src), p.scan, int(white_space))
	names := map[string]Token{}
	for {
		tok := sc.Scan()
		switch tok.Ch {
		case rune(TOK_EOF):
			return names
		case rune(TOK_003b): // ';'
			// ok
		case rune(identifier):
			names[tok.Src()] = tok
		default:
			panic(todo("%v: internal error: %v", tok.Position(), tok))
		}
	}
}

func init() {
	names := scanNames("builtin.ogo", preclaredNames)
	errorNames = scanNames("builtin.ogo", errorSpecSrc)

	//TODO len(), cap()

	Universe.Declarations = map[string]Declaration{}

	// Predefines types
	f := func(nm string, k Kind) {
		Universe.Declarations[nm] = &PredeclaredType{declaration: declaration{token: names[nm]}, kinder: kinder(k)}
	}
	f("bool", PredeclaredBool)
	f("int16", PredeclaredInt16)
	f("int32", PredeclaredInt32)
	f("int64", PredeclaredInt64)
	f("int8", PredeclaredInt8)
	f("uint16", PredeclaredUint16)
	f("uint32", PredeclaredUint32)
	f("uint64", PredeclaredUint64)
	f("uint8", PredeclaredUint8)
	f("uintptr", PredeclaredUintptr)
	f("float32", PredeclaredFloat32)
	f("float64", PredeclaredFloat64)
	f("string", PredeclaredString)
	// Builder is the compiler-known string builder (see emit.go). Registered as a
	// predeclared type so its name resolves in a signature, e.g. "*Builder". Its
	// construction (NewBuilder) and methods are handled by the emitter. Intended to
	// become strings.Builder once packages land -- see specs.go.
	f("Builder", PredeclaredBuilder)
	// any is Go's alias for the empty interface, and is registered AS one -- a type
	// declaration over an interface with no methods -- rather than as a Kind of its
	// own. Everything that keys on a variable's type being an interface then works
	// for it with no case of its own: assigning a pointer in, asserting one back
	// out, a type switch. It is what `type any interface{}` in a source file gives,
	// spelled once in the universe.
	//
	// Go added it in 1.18, with generics, so it is outside "pre-generics Go" read
	// by date -- but it is only a spelling of interface{}, which is not, and the
	// policy is about what the language can express (see specs.go).
	anyTok := names["any"]
	Universe.Declarations["any"] = &TypeDeclaration{
		declaration: declaration{token: anyTok},
		TypeSpec:    &TypeSpecNode{Name: anyTok, TypeNode: &TypeNodeInterface{}},
	}

	// error is registered the same way, as the one-method interface it is in Go.
	// Everything the interface machinery does then works for it with no case of its
	// own: a concrete pointer assigned in, `err != nil`, the call through the table,
	// a type assertion or switch back out. What it is NOT is a Kind -- an error value
	// is two words, and a Kind describes a scalar.
	errTok := names["error"]
	Universe.Declarations["error"] = &TypeDeclaration{
		declaration: declaration{token: errTok},
		TypeSpec: &TypeSpecNode{Name: errTok, TypeNode: &TypeNodeInterface{
			Methods: []MethodSpecNode{{
				Name:    errorNames["Error"],
				Results: &ParameterListNode{List: []ParameterDeclNode{{TypeNode: &TypeNodeIdent{Name: errorNames["string"]}}}},
			}},
		}},
	}

	// int and uint are types of their own, 32 bits wide on the target but distinct
	// from int32 and uint32, as they are in Go.
	f("int", PredeclaredInt)
	f("uint", PredeclaredUint)

	// Type aliases. byte and rune are Go's two predeclared ALIASES -- byte IS
	// uint8 and rune IS int32, the same type under a second name -- so they share
	// the kind rather than getting one, and a value of either is assignable to the
	// other without a conversion.
	f("byte", PredeclaredUint8)
	f("rune", PredeclaredInt32)

	// Untyped bool constants
	f2 := func(nm string, v bool) {
		tok := names[nm]
		Universe.Declarations[nm] = &ConstDeclaration{
			declaration: declaration{token: tok},
			ConstSpec: &ConstSpecNode{
				Name:  tok,
				Value: constVal{cv: constant.MakeBool(v)},
			},
		}
	}
	f2("false", false)
	f2("true", true)

	// Untyped nil
	nm := "nil"
	tok := names[nm]
	Universe.Declarations[nm] = &ConstDeclaration{
		declaration: declaration{token: tok},
		ConstSpec: &ConstSpecNode{
			Name: tok,
		},
	}

	// Predeclared functions. Only the emitted builtins are registered: make keeps
	// the resolve-to-nothing validation in checkFactorNames (its slice form is
	// allowed, other forms and new are rejected as dynamic allocation), and the
	// builtins not yet emitted stay exempt via isBuiltinFuncName.
	for _, bn := range []string{"append", "cap", "clear", "copy", "len", "max", "min", "print", "printf", "println"} {
		Universe.Declarations[bn] = &PredeclaredFunc{declaration: declaration{token: names[bn]}}
	}
	// NewBuilder(back []byte) Builder -- the compiler-known Builder constructor. It
	// is registered so a use resolves; its result type and methods are handled by
	// the emitter (see registerBuilder). The token is synthesized: NewBuilder is not
	// in preclaredNames.
	Universe.Declarations["NewBuilder"] = &PredeclaredFunc{declaration: declaration{name: "NewBuilder"}}
}

// ScopeKind describes the type of a Scope.
type ScopeKind int

// ScopeKind values.
const (
	UniverseScope ScopeKind = iota
	FileScope
	PackageScope
	BlockScope
)

// Scope registers declarations.
type Scope struct {
	Kind         ScopeKind
	Declarations map[string]Declaration
	Parent       *Scope
}

func newScope(parent *Scope, kind ScopeKind) (r *Scope) {
	r = &Scope{Parent: parent, Kind: kind}
	return r
}

func (s *Scope) find(nm string) (d Declaration) {
	_, d = s.find2(nm)
	return d
}

func (s *Scope) find2(nm string) (resolvedIn *Scope, d Declaration) {
	for s != nil {
		if d = s.Declarations[nm]; d != nil {
			return s, d
		}

		s = s.Parent
	}
	return nil, nil
}

func (s *Scope) String() string {
	return fmt.Sprintf("%p.%v=%v", s, s.Kind, slices.Collect(maps.Keys(s.Declarations)))
}

func (s *Scope) child() (r *Scope) {
	return newScope(s, BlockScope)
}

func (s *Scope) add(d Declaration) (err error) {
	nm := d.Name()
	// non-blank identifiers do not bind
	if nm == "_" {
		return nil
	}

	if ex := s.Declarations[nm]; ex != nil {
		return fmt.Errorf("%s redeclared in this block, previous declaration at %v", nm, ex.Token().Position())
	}

	if s.Declarations == nil {
		s.Declarations = map[string]Declaration{}
	}
	s.Declarations[nm] = d
	return nil
}

// Declaration represents the object a name binds to. For example a const, var,
// type or function declaration, but also an import qualifier.
type Declaration interface {
	Name() string
	Token() Token
	Valid() int32
}

type declaration struct {
	name  string
	token Token
	valid int32
}

//TODO- func (d *declaration) declaration() *declaration {
//TODO- 	return d
//TODO- }

// Name returns the identifir of this declaration.
func (d *declaration) Name() (r string) {
	if d.name != "" {
		return d.name
	}

	return d.token.Src()
}

// Token returns the name token of this declaration. The token can be IDENT or
// STRING. To get the name the token represents, use Name().
func (d *declaration) Token() Token {
	return d.token
}

// Valid reports the token index at which the declaration is in scope.
// Meaningful only in block scopes.
func (d *declaration) Valid() int32 {
	return int32(d.valid)
}

// ImportDeclaration represents 'foo' in 'foo.Bar' when 'Bar' is exported from
// package imported as 'foo'.
type ImportDeclaration struct {
	declaration
	Import *ImportSpecNode
}

// ConstDeclaration represents a named constant compile time value.
type ConstDeclaration struct {
	declaration
	ConstSpec *ConstSpecNode
}

// TypeDeclaration represents a named type.
type TypeDeclaration struct {
	declaration
	TypeSpec *TypeSpecNode
	methods  map[string]*FuncDeclNode // methods declared with this type as receiver, by name
	// ptrRecv marks the methods declared with a POINTER receiver. It is what Go's
	// method-set rule turns on: a value of T carries the value-receiver methods,
	// and *T carries all of them, so an interface a pointer method belongs to is
	// satisfied by &x and not by x.
	ptrRecv map[string]bool
}

// VarDeclaration represents a named run time value.
// varRole says how a variable came to be declared, which a diagnostic naming what
// an identifier is bound to reports: Go distinguishes a parameter from a result
// variable from an ordinary local, and so does the message here.
type varRole int

// varRole values.
const (
	roleVar varRole = iota
	roleParam
	roleResult
)

type VarDeclaration struct {
	declaration
	VarSpec     *VarSpecNode
	role        varRole
	kind        Kind  // the variable's type, when it resolves to a predeclared type
	hasKind     bool  // kind is meaningful
	isPtr       bool  // the variable's type is a pointer "*T"
	typeName    Token // the variable's named (possibly pointed-to) type, for field access
	typeQual    Token // the package qualifier of a cross-package named type ("geo" in "geo.Point"), for cross-package member checks
	elemKind    Kind  // the predeclared element/pointed-to type of a pointer, array or slice variable, for deref/index assignment
	hasElemKind bool  // elemKind is meaningful

	// funcSig is the variable's function type "func Signature", when it has one. A
	// call through the variable is checked against it, exactly as a call of a named
	// function is checked against its declaration's.
	//
	// isFunc says the variable holds a function even where funcSig is nil, which is
	// the case for the function-valued forms the language refuses: the refusal is
	// reported once, where written, and calls through the variable are then admitted
	// unchecked rather than each reporting a second, misleading error of its own.
	funcSig *SignatureNode
	isFunc  bool

	// builderVar says the variable holds the predeclared Builder, inferred from a
	// NewBuilder call rather than from a written type. The Builder is the one type
	// whose methods the compiler knows instead of reading them from a declaration,
	// so a receiver of it resolves to no TypeDeclaration and has to be recognised.
	builderVar bool

	isChan          bool  // the variable's type is a channel "chan T"
	chanElemKind    Kind  // the predeclared element type of a channel variable, for send/receive type checks
	hasChanElemKind bool  // chanElemKind is meaningful
	chanElemName    Token // the element type's NAME, for a check a Kind cannot answer
	elemTypeName    Token // an array's or slice's element type NAME, for a field reached through an index

	// elemTypeNode is an array's or slice's ELEMENT type as resolved, kept for the
	// questions no flat field can carry: a channel has no Kind and no name, so `var
	// qs [7]chan req` records nothing about its elements without this, and `qs[i]`
	// could not be told from an ordinary element.
	elemTypeNode TypeNode
}

// FuncDeclaration represents a named function.
type FuncDeclaration struct {
	declaration
	FuncDecl *FuncDeclNode
}

// declaredTypeName renders a variable's named type as it was WRITTEN, qualifier
// included: "geo.Quad", not the bare "Quad" the token holds. The two are one type
// only inside geo, and every method-set question is asked by name -- so the bare one
// resolves in this package's scope, finds nothing, and reports a type as carrying no
// methods when its methods are in the package it came from.
func (d *VarDeclaration) declaredTypeName() string {
	if d.typeQual.IsValid() {
		return d.typeQual.Src() + "." + d.typeName.Src()
	}
	return d.typeName.Src()
}
