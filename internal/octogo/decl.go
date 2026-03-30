// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"fmt"
	"go/constant"
	"maps"
	"slices"
)

// Name definitions for predeclared identifiers.
const preclaredNames = `// bool is the set of boolean values, true and false.
bool
// byte is an alias for uint8 and is equivalent to uint8 in all ways. It is
// used, by convention, to distinguish byte values from 8-bit unsigned integer
// values.
byte
// false is an untyped boolean false value
false
// int is an alias for int32.
int
// int16 is the set of all signed 16-bit integers. Range: -32768 through 32767.
int16
// int32 is the set of all signed 32-bit integers. Range: -2147483648 through
// 2147483647.
int32
// int8 is the set of all signed 8-bit integers. Range: -128 through 127.
int8
// nil is an untyped nil constant
nil
// rune is an alias for int32 and is equivalent to int32 in all ways. It is
// used, by convention, to distinguish character values from integer values.
rune
// true is an untyped boolean true value
true
// uint is an alias for uint32.
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
`

//TODO what is the size of a flexcc func pointer?

var (
	_ Declaration = (*ConstDeclaration)(nil)
	_ Declaration = (*ImportDeclaration)(nil)
	_ Declaration = (*PredeclaredType)(nil)
	_ Declaration = (*VarDeclaration)(nil)
)

// Universe binds predefined declarations.
var Universe = newScope(nil, UniverseScope)

func init() {
	var p Parser
	sc := NewRecScanner("builtin.ogo", []byte(preclaredNames), p.scan, int(white_space))
	names := map[string]Token{}
out:
	for {
		tok := sc.Scan()
		switch tok.Ch {
		case rune(TOK_EOF):
			break out
		case rune(TOK_003b): // ';'
			// ok
		case rune(identifier):
			names[tok.Src()] = tok
		default:
			panic(todo("%v: internal error: %v", tok.Position(), tok))
		}
	}

	//TODO any = interface
	//TODO len(), cap()

	Universe.Declarations = map[string]Declaration{}

	// Predefines types
	f := func(nm string, k Kind) {
		Universe.Declarations[nm] = &PredeclaredType{declaration: declaration{token: names[nm]}, kinder: kinder(k)}
	}
	f("bool", PredeclaredBool)
	f("int16", PredeclaredInt16)
	f("int32", PredeclaredInt32)
	f("int8", PredeclaredInt8)
	f("uint16", PredeclaredUint16)
	f("uint32", PredeclaredUint32)
	f("uint8", PredeclaredUint8)
	f("uintptr", PredeclaredUintptr)
	// Type aliases
	f("byte", PredeclaredUint8)
	f("int", PredeclaredInt32)
	f("rune", PredeclaredInt32)
	f("uint", PredeclaredUint32)

	// Untyped bool constants
	f2 := func(nm string, v bool) {
		tok := names[nm]
		Universe.Declarations[nm] = &ConstDeclaration{
			declaration: declaration{token: tok},
			ConstSpec: &ConstSpecNode{
				Name:     tok,
				Value:    constant.MakeBool(v),
				TypeNode: UntypedBool, //TODO- When Value implements TypeNode
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
			Name:     tok,
			TypeNode: UntypedNil,
		},
	}
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
		return fmt.Errorf("%s declared in the same scope before at %v", nm, ex.Token().Position())
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
}

// VarDeclaration represents a named run time value.
type VarDeclaration struct {
	declaration
	VarSpec *VarSpecNode
}

// FuncDeclaration represents a named function.
type FuncDeclaration struct {
	declaration
	FuncDecl *FuncDeclNode
}
