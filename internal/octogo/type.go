// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

var (
	_ Typ   = (*PredeclaredType)(nil)
	_ Typ   = Kind(0)
	_ gater = (*PredeclaredType)(nil)
	_ gater = Kind(0)
)

// Kind describes a type category.
type Kind int8

// Values of type Kind
const (
	PredeclaredBool Kind = iota
	PredeclaredInt8
	PredeclaredUint8
	PredeclaredInt16
	PredeclaredUint16
	PredeclaredInt32
	PredeclaredUint32
	PredeclaredInt64
	PredeclaredUint64
	// PredeclaredInt and PredeclaredUint are int and uint, which are 32 bits wide
	// on the target but are types of their own, distinct from int32 and uint32 as
	// they are in Go. Sharing int32's kind made every check that compares types
	// blind to the difference, so "var y int32 = x" for an int x compiled here and
	// was refused by Go. Contrast byte and rune, which are ALIASES: those do share
	// uint8's and int32's kinds, because in Go they are the same type.
	PredeclaredInt
	PredeclaredUint
	PredeclaredFloat32
	PredeclaredFloat64
	PredeclaredUintptr
	PredeclaredString
	// PredeclaredBuilder is the compiler-known string Builder (see emit.go's
	// registerBuilder). It has no scalar semantics; the kind exists only so the type
	// name resolves in a signature, e.g. a "*Builder" parameter.
	PredeclaredBuilder
	UntypedBool
	UntypedFloat
	UntypedInt
	// UntypedRune is the kind of a rune literal, 'a'. It is kept apart from
	// UntypedInt only for its DEFAULT type: a variable inferred from one becomes a
	// rune, not an int. In every other respect the two behave alike.
	UntypedRune
	UntypedNil
	UntypedString
	Alias
)

// Kind implements Typ.
func (k Kind) Kind() Kind {
	return k
}

// Type implements TypeNode.
func (k Kind) Type() Typ {
	return k
}

func (k Kind) state() (r gate) {
	return resolved
}

func (k Kind) open() {}

func (k Kind) close() {}

// Typ describes an OctoGo type.
type Typ interface {
	Kind() Kind
}

type typer struct {
	typ Typ
}

func (t typer) Type() Typ {
	return t.typ
}

type kinder Kind

// Kind describes a type category.
func (k kinder) Kind() Kind {
	return Kind(k)
}

// PredeclaredType represents a built-in type.
type PredeclaredType struct {
	declaration
	kinder
}

// Type implements TypeNode.
func (t *PredeclaredType) Type() Typ {
	return t
}

func (t *PredeclaredType) state() (r gate) {
	return resolved
}

func (t *PredeclaredType) open() {}

func (t *PredeclaredType) close() {}
