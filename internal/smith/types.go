// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
)

// Type represents an OctoGo data type used during fuzzing.
type Type interface {
	String() string // Returns the OctoGo syntax representation
	IsNumeric() bool
}

// BasicKind represents the fundamental predeclared types.
type BasicKind int

const (
	KindInt BasicKind = iota
	KindBool
	KindString
	KindRune
	// The sized integer types. Go computes in the operands' own type while C
	// promotes anything narrower than int, so these are where the two languages
	// part company -- which is why the generator has them at all.
	KindInt8
	KindUint8
	KindInt16
	KindUint16
	KindUint32
)

// sizedKinds are the integer types genSizedStmt exercises, narrowest first.
var sizedKinds = []BasicKind{KindInt8, KindUint8, KindInt16, KindUint16, KindUint32}

// sizedInfo is a sized integer type's width and signedness.
func sizedInfo(k BasicKind) (bits int, signed, ok bool) {
	switch k {
	case KindInt8:
		return 8, true, true
	case KindUint8:
		return 8, false, true
	case KindInt16:
		return 16, true, true
	case KindUint16:
		return 16, false, true
	case KindUint32:
		return 32, false, true
	}
	return 0, false, false
}

// sizedRange is a sized integer type's inclusive bounds.
func sizedRange(k BasicKind) (lo, hi int64) {
	bits, signed, _ := sizedInfo(k)
	if signed {
		return -1 << (bits - 1), 1<<(bits-1) - 1
	}
	return 0, 1<<bits - 1
}

// wrapSized reduces v to what the type k holds, which is what Go's arithmetic does
// and what C does only after the emitter truncates.
func wrapSized(v int64, k BasicKind) int64 {
	bits, signed, ok := sizedInfo(k)
	if !ok {
		return v
	}
	u := uint64(v) & (1<<uint(bits) - 1)
	if signed && u&(1<<uint(bits-1)) != 0 {
		return int64(u) - 1<<uint(bits)
	}
	return int64(u)
}

type BasicType struct {
	Kind BasicKind
}

func (b BasicType) String() string {
	switch b.Kind {
	case KindInt:
		return "int" // OctoGo numeric types currently omit float/complex
	case KindBool:
		return "bool" // Predeclared boolean type
	case KindString:
		return "string" // String types
	case KindRune:
		return "rune"
	case KindInt8:
		return "int8"
	case KindUint8:
		return "uint8"
	case KindInt16:
		return "int16"
	case KindUint16:
		return "uint16"
	case KindUint32:
		return "uint32"
	default:
		return "unknown"
	}
}

func (b BasicType) IsNumeric() bool {
	if _, _, ok := sizedInfo(b.Kind); ok {
		return true
	}
	return b.Kind == KindInt || b.Kind == KindRune
}

// ArrayType represents a fixed-size array.
type ArrayType struct {
	Len  int
	Elem Type
}

func (a ArrayType) String() string {
	return fmt.Sprintf("[%d]%s", a.Len, a.Elem.String())
}
func (a ArrayType) IsNumeric() bool { return false }

// SliceType represents a slice. Its length and capacity are not part of the type;
// the generation-time VM tracks both per variable (see SliceVal), because the
// target has no heap and an append past the backing array's capacity panics.
type SliceType struct {
	Elem Type
}

func (s SliceType) String() string { return "[]" + s.Elem.String() }

func (s SliceType) IsNumeric() bool { return false }

// ChanType represents a bidirectional channel.
type ChanType struct {
	Elem Type
}

func (c ChanType) String() string {
	return "chan " + c.Elem.String()
}

func (c ChanType) IsNumeric() bool { return false }

// StructDef is a generated struct type: its name and its fields, all int. Fields
// are kept in declaration order rather than a map so that generation stays
// reproducible from a seed.
type StructDef struct {
	Name   string
	Fields []string
	// Methods are generated alongside the type, three per struct, one of each
	// shape below. They are named rather than modelled: what each does to the
	// receiver is fixed, so the VM applies it directly (see genMethodCall).
	Get    string // value receiver, returns Field0
	Set    string // POINTER receiver, writes its argument into Field0 and returns it
	Shadow string // VALUE receiver, writes its argument into Field0 and returns it --
	// the caller's struct must be untouched, a copy being what a value receiver gets
}

// StructType names a generated struct. Only the name distinguishes it, so
// GetSymbolsOfType's String() comparison identifies a variable's struct exactly.
type StructType struct {
	Def *StructDef
}

func (s StructType) String() string { return s.Def.Name }

func (s StructType) IsNumeric() bool { return false }
