// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

// Kind describes a Type
type Kind int

// Values of type Kind
const (
	PredefinedBool = iota
	PredefinedByte
	PredefinedInt
	PredefinedInt8
	PredefinedUint8
	PredefinedInt16
	PredefinedUint16
	PredefinedInt32
	PredefinedUint32
)
