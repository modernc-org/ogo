// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"go/constant"
	"math"
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
	// isRune is an untyped integer constant of the RUNE class: a rune literal, or
	// an operation one took part in that Go's rule gives its class -- an integer
	// beside a rune is a rune, and a shift is its left operand's. Its default type
	// is rune where an integer's is int: `const H = 'c'; x := H` makes x a rune,
	// and the named constant was an integer, so `x = n` of an int was taken.
	isRune bool
}

// typedAs returns l with the type k, for the conversion that gives a constant one.
// A float's value is the one its type holds, as Go rounds a typed float constant
// where it is declared, converted and computed: `const B float64 = 0.1`, B*3*10
// is 3.0000000000000004, which int() refuses, where the value computed exactly was
// 3 and taken.
func (l constVal) typedAs(k Kind) constVal {
	l.typ, l.typed = k, true
	if l.cv != nil && isFloatKind(k) {
		l.cv = roundedToFloat(l.cv, k)
	}
	return l
}

// roundedToFloat is v as the float kind k holds it, an integer staying an integer;
// past the type's range it is v, which the overflow checks report.
func roundedToFloat(v constant.Value, k Kind) constant.Value {
	if v.Kind() != constant.Int && v.Kind() != constant.Float {
		return v
	}
	var f float64
	if k == PredeclaredFloat32 {
		f32, _ := constant.Float32Val(v)
		f = float64(f32)
	} else {
		f, _ = constant.Float64Val(v)
	}
	if math.IsInf(f, 0) {
		return v
	}
	r := constant.MakeFloat64(f)
	if v.Kind() == constant.Int {
		return constant.ToInt(r)
	}
	return r
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
	l.isRune = a.isRune || b.isRune
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
		if l.isRune {
			return UntypedRune
		}
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
		// Not a constant: `const g = 2 * *3`, a dereference of one. Answered as
		// none, for the declaration's own check to say what it is, where a
		// mistyped `**` crashed the compiler.
		return nil
	}
}
