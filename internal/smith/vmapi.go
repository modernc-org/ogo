// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith // import "modernc.org/octogo/lib/internal/smith"

// Value represents a typed value known at generation time.
type Value interface {
	Type() Type      // Returns the octosmith.Type
	Literal() string // Returns the OctoGo literal string (e.g., “42”, “true”)
	Value() any      // Returns the underlying Go value (e.g., int64, bool)
	binOp(op string, rhs Value) (Value, error)
}

// Memory abstraction for the fuzzer to track state and lexical scope.
type Memory interface {
	PushScope()
	PopScope()
	Store(name string, val Value)
	Load(name string) Value
}

// Machine evaluates operations to compute the checksum during generation.
type Machine interface {
	// Eval performs a language operation.
	// op is the OctoGo operator string (e.g., “int_lit”, “+”, “-”, “==”, “^”)
	// If the operation is invalid (e.g., overflow, division by zero), it returns an error,
	// allowing the fuzzer to discard the attempt and fallback to a safe literal.
	Eval(op string, operands ...any /* Value but also "0" etc. */) (Value, error)
}
