// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"go/constant"
)

var (
	_ = Value(constVal{})
)

// Value represents a value known at compile time.
type Value interface {
	Expr() ExpressionNode
	Type() Typ
}

type valuer struct {
	val Value
}

func (v valuer) Value() Value {
	return v.val
}

// constVal is a value known at compile time: the constant itself, and the type it
// has when it is a TYPED constant.
//
// Most constants are untyped -- a literal, and anything folded from literals --
// and take their type from the context they are used in. A constant becomes typed
// by being written with one, "const one int32 = 1", or by folding an expression
// that names one, "const one = int32(1) << 16": the conversion is what fixes the
// type, and the shift carries it out. The distinction did not exist here while
// every integer shared one kind; with int and int32 apart, it decides what "50 *
// one" is, and so what a variable declared from it becomes.
type constVal struct {
	cv constant.Value

	// typ is the constant's type and typed says it has one. They are apart rather
	// than using a zero Kind as "none", since Kind's zero is a real type (bool).
	typ   Kind
	typed bool
}

// typedAs returns l with the type k, for the conversion that gives a constant one.
func (l constVal) typedAs(k Kind) constVal {
	l.typ, l.typed = k, true
	return l
}

// sameTypeAs returns l carrying the type of whichever of the operands of a
// constant binary operation has one. Go requires two typed operands to be of the
// same type -- a mismatch is reported where the operation is checked, not here --
// so taking the first is taking the only one there can be.
func (l constVal) sameTypeAs(a, b constVal) constVal {
	switch {
	case a.typed:
		return l.typedAs(a.typ)
	case b.typed:
		return l.typedAs(b.typ)
	}
	return l
}

func (l constVal) Expr() ExpressionNode {
	return l
}

func (l constVal) Type() Typ {
	if l.typed {
		return l.typ
	}
	switch l.cv.Kind() {
	case constant.Bool:
		return UntypedBool
	case constant.String:
		return UntypedString
	case constant.Int:
		return UntypedInt
	case constant.Float:
		return UntypedFloat
	default:
		return nil
	}
}

func (l constVal) Value() Value {
	return l
}

func (f *File) evalConstExpr(e ExpressionNode) (r Value) {
	if e == nil {
		return nil
	}

	switch x := e.Value().(type) {
	case constVal:
		return x
	default:
		panic(todo("%T", x))
	}
}
