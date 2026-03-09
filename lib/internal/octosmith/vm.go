// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"strconv"
)

var (
	_ Value = (Int32)(0)
)

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
	case "-", "^", "+":
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
	case "+":
		return Int32(a + b), nil
	case "-":
		return Int32(a + b), nil
	case "^":
		return Int32(a ^ b), nil
	default:
		panic(todo("%q %v", op, b))
	}
}
