// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

var (
	_ Typ = (*AliasType)(nil)
	_ Typ = (*PredefinedType)(nil)
)

// Kind describes a type category.
type Kind int

// Values of type Kind
const (
	PredefinedBool = iota
	PredefinedByte
	PredefinedInt
	PredefinedInt8
	PredefinedUint8
	PredefinedInt16
	PredefinedUint16
	PredefinedInt32
	PredefinedUint32
	PredefinedUintptr
	Alias
)

// Typ describes an OctoGo type.
type Typ interface {
	Kind() Kind
}

type kinder Kind

// Kind describes a type category.
func (k kinder) Kind() Kind {
	return Kind(k)
}

// AliasType represents T in 'type T = U'.
type AliasType struct { //TODO-
	declaration
	kinder
	U Typ
}

func newAlias(nmTok Token, valid int32, u Typ) *AliasType {
	return &AliasType{declaration: declaration{token: nmTok, valid: valid}, kinder: kinder(Alias), U: u}
}

// PredefinedType represents a built-in type.
type PredefinedType struct {
	declaration
	kinder
}
