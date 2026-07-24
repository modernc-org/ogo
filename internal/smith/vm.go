// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"math"
	"strconv"
)

var (
	_ Value = (Int32)(0)
	_ Value = (Bool)(false)
	_ Value = (*ArrayVal)(nil)
)

// ArrayVal is a fixed integer array in the generation-time VM, zero-initialized to
// match the emitter's `var a [N]int`. It holds concrete element values so an index
// read or write resolves to a known Int32, keeping the oracle in step with the
// compiled array. It is a pointer type so a write mutates the stored array in place.
type ArrayVal struct {
	Elems []Int32
}

func (a *ArrayVal) Literal() string { return "" } // declared zero, never literal-initialized here
func (a *ArrayVal) Type() Type      { return ArrayType{Len: len(a.Elems), Elem: BasicType{Kind: KindInt}} }
func (a *ArrayVal) Value() any      { return a.Elems }
func (a *ArrayVal) binOp(op string, rhs Value) (Value, error) {
	panic(todo("array is not a binary operand: %q", op))
}

type storage map[string]Value

type memory struct {
	scopes []storage
	m      storage
}

func (m *memory) PushScope() {
	m.scopes = append(m.scopes, m.m)
	m.m = storage{}
}

func (m *memory) PopScope() {
	n := len(m.scopes)
	m.m = m.scopes[n-1]
	m.scopes = m.scopes[:n-1]
}

func (m *memory) Store(name string, val Value) {
	// Look through scopes from top to bottom
	for i := len(m.scopes) - 1; i >= 0; i-- {
		if _, ok := m.scopes[i][name]; ok {
			m.scopes[i][name] = val
			return
		}
	}
	// Also check current local scope
	if _, ok := m.m[name]; ok {
		m.m[name] = val
		return
	}
	// If it doesn't exist anywhere, declare it locally
	m.m[name] = val
}

func (m *memory) Load(name string) (r Value) {
	i := len(m.scopes)
	for s := m.m; s != nil; {
		if r, ok := s[name]; ok {
			return r
		}

		if i == 0 {
			panic(todo("[%q] no value", name))
		}

		i--
		s = m.scopes[i]
	}
	panic(todo(""))
}

func NewMemory() Memory {
	return &memory{m: storage{}}
}

type machine struct{}

func (m *machine) Eval(op string, operands ...any /* Value but also "0" etc. */) (Value, error) {
	switch op {
	case "int_lit":
		n, err := strconv.ParseInt(operands[0].(string), 0, 32)
		if err != nil {
			panic(todo("", err))
		}

		return Int32(n), nil
	case "!=", "+", "-", "*", "/", "%", "<<", ">>", "&", "|", "&^", "<", "<=", "==", ">", ">=", "^":
		switch len(operands) {
		case 2:
			return operands[0].(Value).binOp(op, operands[1].(Value))
		default:
			panic(todo("", len(operands)))
		}
	default:
		panic(todo("op=%v operands=%v", op, operands))
	}
}

func NewMachine() Machine {
	return &machine{}
}

type Int32 int32

func (n Int32) Literal() string {
	return fmt.Sprint(int32(n))
}

func (n Int32) Type() Type {
	return BasicType{Kind: KindInt}
}

func (n Int32) Value() any {
	return int32(n)
}

func (n Int32) binOp(op string, rhs Value) (Value, error) {
	a := int32(n)
	b := int32(rhs.(Int32))
	switch op {
	// Signed +, -, * that overflow int32 are undefined in C, and a constant
	// expression that overflows is rejected outright by the checker (Go's
	// constant-overflow rule). Detect overflow in int64 and reject it so the
	// generator falls back to a safe operator; the emitter never emits an
	// overflowing arithmetic form.
	case "+":
		if r := int64(a) + int64(b); r >= math.MinInt32 && r <= math.MaxInt32 {
			return Int32(r), nil
		}
		return nil, fmt.Errorf("add overflow: %d + %d", a, b)
	case "-":
		if r := int64(a) - int64(b); r >= math.MinInt32 && r <= math.MaxInt32 {
			return Int32(r), nil
		}
		return nil, fmt.Errorf("sub overflow: %d - %d", a, b)
	case "*":
		if r := int64(a) * int64(b); r >= math.MinInt32 && r <= math.MaxInt32 {
			return Int32(r), nil
		}
		return nil, fmt.Errorf("mul overflow: %d * %d", a, b)
	case "/", "%":
		// Division and modulo by zero are undefined in C (and would panic through the
		// emitter's nonzero guard); INT32_MIN / -1 overflows. Reject both so the
		// generator falls back to a safe operator, since it never emits them.
		if b == 0 || (a == math.MinInt32 && b == -1) {
			return nil, fmt.Errorf("undefined %s: %d %s %d", op, a, op, b)
		}
		if op == "/" {
			return Int32(a / b), nil
		}
		return Int32(a % b), nil
	case "<<", ">>":
		// A shift amount outside [0, 32) is undefined in C. Reject it (the generator
		// then picks a safe operator instead, since the emitter never emits it).
		if b < 0 || b >= 32 {
			return nil, fmt.Errorf("shift amount out of range: %d", b)
		}
		if op == ">>" {
			// int32 width matches C's int, and gcc's arithmetic right shift of a
			// negative value agrees with Go's, so >> needs no further restriction.
			return Int32(a >> uint(b)), nil
		}
		// A signed left shift is undefined in C when the operand is negative or the
		// result does not fit in int (result overflow). Go defines both (wrapping),
		// so restrict << to the C-defined cases to keep the oracle's Go-side and its
		// emitted C in agreement.
		if a < 0 || int64(a)<<uint(b) > math.MaxInt32 {
			return nil, fmt.Errorf("left shift overflow: %d << %d", a, b)
		}
		return Int32(a << uint(b)), nil
	case "&":
		return Int32(a & b), nil
	case "|":
		return Int32(a | b), nil
	case "&^":
		return Int32(a &^ b), nil
	case "^":
		return Int32(a ^ b), nil
	case "==":
		return Bool(a == b), nil
	case "!=":
		return Bool(a != b), nil
	case "<":
		return Bool(a < b), nil
	case "<=":
		return Bool(a <= b), nil
	case ">":
		return Bool(a > b), nil
	case ">=":
		return Bool(a >= b), nil
	default:
		panic(todo("%q %v", op, b))
	}
}

type Bool bool

func (b Bool) Literal() string {
	if b {
		return "true"
	}
	return "false"
}

func (b Bool) Type() Type { return BasicType{Kind: KindBool} }

func (b Bool) Value() any { return bool(b) }

func (b Bool) binOp(op string, rhs Value) (Value, error) {
	panic(todo("bool binOp %q", op))
}
