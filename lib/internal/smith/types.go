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
)

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
	default:
		return "unknown"
	}
}

func (b BasicType) IsNumeric() bool {
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

// ChanType represents a bidirectional channel.
type ChanType struct {
	Elem Type
}

func (c ChanType) String() string {
	return "chan " + c.Elem.String()
}

func (c ChanType) IsNumeric() bool { return false }
