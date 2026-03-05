// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"go/constant"
)

var (
	universe = &Scope{Nodes: map[string]Declaration{
		"bool":    newPredefinedType(PredefinedBool),
		"int16":   newPredefinedType(PredefinedInt16),
		"int32":   newPredefinedType(PredefinedInt32),
		"int8":    newPredefinedType(PredefinedInt8),
		"uint16":  newPredefinedType(PredefinedUint16),
		"uint32":  newPredefinedType(PredefinedUint32),
		"uint8":   newPredefinedType(PredefinedUint8),
		"uintptr": newPredefinedType(PredefinedUintptr),
	}}
)

func init() {
	universe.Nodes["byte"] = newAlias(universe.Nodes["uint8"].(Typ))
	universe.Nodes["int"] = newAlias(universe.Nodes["int32"].(Typ))
	universe.Nodes["rune"] = newAlias(universe.Nodes["int32"].(Typ))
	universe.Nodes["uint"] = newAlias(universe.Nodes["uint32"].(Typ))
	universe.Nodes["false"] = newConstSpecification(0, constant.MakeBool(false), universe.Nodes["bool"].(Typ))
	universe.Nodes["true"] = newConstSpecification(0, constant.MakeBool(true), universe.Nodes["bool"].(Typ))
}
