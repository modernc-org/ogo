// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"go/constant"
)

// ConstSpecification represents compile-time named value.
type ConstSpecification struct {
	declaration
	constant.Value
	Typ
}

func newConstSpecification(name Token, valid int32, value constant.Value, typ Typ) *ConstSpecification {
	return &ConstSpecification{declaration{valid: valid}, value, typ}
}
