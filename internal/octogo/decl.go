// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"fmt"

	"go/constant"
)

// Name definitions for predefined identifiers.
const predefinedNames = `// bool is the set of boolean values, true and false.
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
	_ Declaration = (*PredefinedType)(nil)
	_ Declaration = (*VarDeclaration)(nil)
)

// Universe binds predefined declarations.
var Universe = &Scope{
	Kind:  UniverseScope,
	Nodes: map[string]Declaration{},
}

func init() {
	var p Parser
	sc := NewRecScanner("builtin.ogo", []byte(predefinedNames), p.scan, int(white_space))
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
	//TODO min max
	//TODO len(), cap()

	// Predefines types
	f := func(nm string, k Kind) {
		Universe.Nodes[nm] = &PredefinedType{declaration: declaration{name: names[nm]}, kinder: kinder(k)}
	}
	f("bool", PredefinedBool)
	f("int16", PredefinedInt16)
	f("int32", PredefinedInt32)
	f("int8", PredefinedInt8)
	f("uint16", PredefinedUint16)
	f("uint32", PredefinedUint32)
	f("uint8", PredefinedUint8)
	f("uintptr", PredefinedUintptr)

	// Type aliases
	f2 := func(nm, aliasNm string) {
		Universe.Nodes[nm] = newAlias(names[nm], 0, Universe.Nodes[aliasNm].(Typ))
	}
	f2("byte", "uint8")
	f2("int", "int32")
	f2("rune", "int32")
	f2("uint", "uint32")

	// Bool constants
	boolType := Universe.Nodes["bool"].(Typ)
	f3 := func(nm string, v bool) {
		tok := names[nm]
		Universe.Nodes[nm] = &ConstDeclaration{
			declaration: declaration{name: tok},
			ConstSpec: &ConstSpecNode{
				Name:  tok,
				Value: constant.MakeBool(v),
				Type:  boolType,
			},
		}
	}
	f3("false", false)
	f3("true", true)
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
	Kind   ScopeKind
	Nodes  map[string]Declaration
	Parent *Scope
}

func newScope(parent *Scope, kind ScopeKind) (r *Scope) {
	return &Scope{Parent: parent, Kind: kind}
}

//TODO func (s *Scope) child() (r *Scope) {
//TODO 	return newScope(s, BlockScope)
//TODO }

func (s *Scope) add(d Declaration) (err error) {
	new := d.Name()
	nm := new.Src()
	// non-blank identifiers do not bind
	if nm == "_" {
		return nil
	}

	if ex := s.Nodes[nm]; ex != nil {
		return fmt.Errorf("%s declared in the same scope before at %v", nm, ex.Name().Position())
	}

	if s.Nodes == nil {
		s.Nodes = map[string]Declaration{}
	}
	s.Nodes[nm] = d
	return nil
}

// Declaration represents the object a name binds to. For example a const, var,
// type or function declaration, but also an import qualifier.
type Declaration interface {
	Name() Token
	Valid() int32
}

type declaration struct {
	name  Token
	valid int32
}

// Name reports the name token of this declaration.
func (d *declaration) Name() Token {
	return d.name
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
