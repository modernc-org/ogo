// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

var (
	_ Typ = (*PredeclaredType)(nil)
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
	PredeclaredUintptr
	UntypedBool
	UntypedFloat
	UntypedInt
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

func (k Kind) setResolving() {}

func (k Kind) setResolved() {}

// Typ describes an OctoGo type.
type Typ interface {
	Kind() Kind
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

func (t *PredeclaredType) setResolving() {}

func (t *PredeclaredType) setResolved() {}
