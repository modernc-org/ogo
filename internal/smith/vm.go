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
	_ Value = (Sized)(Sized{})
	_ Value = (*ArrayVal)(nil)
	_ Value = (*SliceVal)(nil)
	_ Value = (*StructVal)(nil)
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

// SliceVal is an integer slice in the generation-time VM, created zero-length-or-more
// by `make([]int, len, cap)`. Elems models the live elements, so an index read or
// write and len() resolve to known values; Cap is the backing array's fixed extent.
// The target has no heap, so the emitted ogo_append panics rather than reallocating
// once the backing array is full -- the generator therefore only appends while
// len(Elems) < Cap, and Cap is what tells it when to stop. Like ArrayVal it is a
// pointer type, so an element write or an append mutates the stored slice in place.
type SliceVal struct {
	Elems []Int32
	Cap   int
}

func (s *SliceVal) Literal() string { return "" } // built by make, never literal-initialized
func (s *SliceVal) Type() Type      { return SliceType{Elem: BasicType{Kind: KindInt}} }
func (s *SliceVal) Value() any      { return s.Elems }
func (s *SliceVal) binOp(op string, rhs Value) (Value, error) {
	panic(todo("slice is not a binary operand: %q", op))
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

// Sized is a value of a sized integer type in the generation-time VM. Its
// arithmetic wraps at the type's width, which is what Go does and what C does only
// once the emitter truncates -- the divergence that made every operator on int8,
// uint8, int16 and uint16 wrong until v0.13.0, and the reason these are generated.
type Sized struct {
	v int64
	k BasicKind
}

// NewSized builds a value of kind k, wrapped into range.
func NewSized(v int64, k BasicKind) Sized { return Sized{wrapSized(v, k), k} }

func (n Sized) Literal() string { return sizedLitText(n.v, n.k) }

func (n Sized) Type() Type { return BasicType{Kind: n.k} }

func (n Sized) Value() any { return n.v }

// Int32 is the value as the checksum sees it: the type's own value converted to
// int, which is what `int(z)` does in the generated program.
func (n Sized) Int32() Int32 { return Int32(int32(n.v)) }

func (n Sized) binOp(op string, rhs Value) (Value, error) {
	r, ok := rhs.(Sized)
	if !ok || r.k != n.k {
		return nil, fmt.Errorf("mixed types in %q", op)
	}
	a, b := n.v, r.v
	bits, signed, _ := sizedInfo(n.k)
	wrap := func(v int64) (Value, error) { return NewSized(v, n.k), nil }
	switch op {
	case "+":
		return wrap(a + b)
	case "-":
		return wrap(a - b)
	case "*":
		return wrap(a * b)
	case "/", "%":
		// Division by zero is undefined in C and panics through the emitter's guard.
		// The most negative value over -1 is fine at the NARROW widths: C computes
		// them in int, where the quotient fits, and the truncation back is what Go's
		// wrap already says. At 64 bits it does not fit -- the quotient is one past
		// the type -- so that one pair is skipped.
		if b == 0 {
			return nil, fmt.Errorf("undefined %s: %d %s %d", op, a, op, b)
		}
		if !signed {
			// An unsigned value whose top bit is set is a negative int64 here, so the
			// division must read both operands as the unsigned numbers they are.
			// Below 64 bits no pattern reaches that bit and the two agree.
			ua, ub := uint64(a), uint64(b)
			if op == "/" {
				return wrap(int64(ua / ub))
			}
			return wrap(int64(ua % ub))
		}
		if bits == 64 && a == math.MinInt64 && b == -1 {
			return nil, fmt.Errorf("undefined %s: %d %s %d", op, a, op, b)
		}
		if op == "/" {
			return wrap(a / b)
		}
		return wrap(a % b)
	case "<<", ">>":
		// Go defines a shift by any count; C leaves one at or past the width
		// undefined, and the emitter guards only what it must. Keep to counts the
		// emitted C computes directly.
		if b < 0 || b >= int64(bits) {
			return nil, fmt.Errorf("shift amount out of range: %d", b)
		}
		if op == ">>" {
			if signed {
				return wrap(a >> uint(b))
			}
			return wrap(int64(uint64(a) >> uint(b)))
		}
		return wrap(a << uint(b))
	case "&":
		return wrap(a & b)
	case "|":
		return wrap(a | b)
	case "&^":
		return wrap(a &^ b)
	case "^":
		return wrap(a ^ b)
	case "==":
		return Bool(a == b), nil
	case "!=":
		return Bool(a != b), nil
	case "<", "<=", ">", ">=":
		// Ordered by the type's own reading: an unsigned pattern with its top bit
		// set is the LARGEST value there and a negative int64 here. Below 64 bits no
		// pattern reaches that bit, so the two orders agree.
		x, y := a, b
		if !signed {
			switch op {
			case "<":
				return Bool(uint64(x) < uint64(y)), nil
			case "<=":
				return Bool(uint64(x) <= uint64(y)), nil
			case ">":
				return Bool(uint64(x) > uint64(y)), nil
			}
			return Bool(uint64(x) >= uint64(y)), nil
		}
		switch op {
		case "<":
			return Bool(x < y), nil
		case "<=":
			return Bool(x <= y), nil
		case ">":
			return Bool(x > y), nil
		}
		return Bool(x >= y), nil
	default:
		panic(todo("%q %v", op, b))
	}
}

// neg and not are the unary operators, which wrap like the binary ones: -x on an
// unsigned type is its two's complement, and ^x is the complement at the type's
// width, not at C's int.
func (n Sized) neg() Sized { return NewSized(-n.v, n.k) }

func (n Sized) not() Sized { return NewSized(^n.v, n.k) }

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

// StructVal is a generated struct value in the generation-time VM: one Int32 per
// field, zero when the variable is declared, exactly as the emitter zeroes it.
//
// Like ArrayVal and SliceVal it is a pointer type so a field write mutates the
// stored value in place. Copy is therefore explicit -- which is the point of
// having it, since `w := v` copies a struct by value in Go and getting that wrong
// is a miscompile the oracle can see.
type StructVal struct {
	Def    *StructDef
	Fields map[string]Int32
}

func (s *StructVal) Literal() string { return "" } // declared zero, never literal-initialized
func (s *StructVal) Type() Type      { return StructType{Def: s.Def} }
func (s *StructVal) Value() any      { return s.Fields }
func (s *StructVal) binOp(op string, rhs Value) (Value, error) {
	panic(todo("struct is not a binary operand: %q", op))
}

// Copy is Go's by-value struct assignment.
func (s *StructVal) Copy() *StructVal {
	r := &StructVal{Def: s.Def, Fields: make(map[string]Int32, len(s.Fields))}
	for k, v := range s.Fields {
		r.Fields[k] = v
	}
	return r
}

// Equal reports whether every field matches, which is what the emitted per-type
// equality helper compares.
func (s *StructVal) Equal(o *StructVal) bool {
	for _, f := range s.Def.Fields {
		if s.Fields[f] != o.Fields[f] {
			return false
		}
	}
	return true
}
