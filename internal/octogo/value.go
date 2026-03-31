// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"go/constant"
)

var (
	_ = Value(literal{})
)

// Value represents a value known at compile time.
type Value interface {
	Type() Typ
}

type valuer struct {
	val Value
}

func (v valuer) Value() Value {
	return v.val
}

type literal struct {
	cv constant.Value
}

func (l literal) Type() Typ {
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

func (l literal) Value() Value {
	return l
}
