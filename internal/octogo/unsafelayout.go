// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"go/constant"
	"slices"
)

// unsafeLayoutFuncs are the functions of Go's unsafe this one provides beside
// Pointer: Sizeof, Alignof and Offsetof, each a constant of type uintptr that the
// checker computes from the target's layout (targetLayout).
var unsafeLayoutFuncs = map[string]bool{"Sizeof": true, "Alignof": true, "Offsetof": true}

// targetLayout is the size of a value of type t on the target, and the alignment
// its C compiler gives the type (TypeAlign, spin2cpp's frontends/expr.c), as it lays
// the emitted C out -- measured on a P2-EDGE and read off that compiler's source,
// 2026-10-08:
//
//   - a bool, an int8 and a uint8 take a byte, an int16 and a uint16 two, aligned
//     so; every other scalar four, float64 included, which is 32 bits here; an
//     int64 and a uint64 eight, aligned on four;
//   - a pointer, a channel, a function and an unsafe.Pointer four; a string eight,
//     a pointer and a length; a slice twelve, a pointer, a length and a capacity;
//     an interface eight, its data and its table -- each aligned on four;
//   - an array its elements, aligned as its element;
//   - a struct aligned on four, laid out twice (structSize): its fields are where
//     the second pass puts them, and its size is the larger of the two ends.
//
// It answers false for a type it cannot resolve.
func (f *File) targetLayout(t typeAt, depth int) (size, align int64, ok bool) {
	if depth > 16 || t.tn == nil {
		return 0, 0, false
	}
	if t.f == nil {
		t.f = f
	}
	if id, isIdent := t.tn.(*TypeNodeIdent); isIdent && !id.Qualifier.IsValid() {
		switch id.Name.Src() {
		case "error", "any":
			if _, isPre := t.s.find(id.Name.Src()).(*PredeclaredType); isPre || t.s.find(id.Name.Src()) == nil {
				return 8, 4, true
			}
		}
	}
	if k, ok := t.f.typeKind(t.s, t.tn); ok {
		switch k {
		case PredeclaredBool, PredeclaredInt8, PredeclaredUint8:
			return 1, 1, true
		case PredeclaredInt16, PredeclaredUint16:
			return 2, 2, true
		case PredeclaredInt64, PredeclaredUint64:
			return 8, 4, true
		case PredeclaredString:
			return 8, 4, true
		}
		return 4, 4, true
	}
	u := t.f.underlyingTypeAt(t)
	switch x := u.tn.(type) {
	case *TypeNodePointer, *TypeNodeChan, *FunctionType:
		return 4, 4, true
	case *TypeNodeSlice:
		return 12, 4, true
	case *TypeNodeInterface:
		return 8, 4, true
	case *TypeNodeArray:
		n, ok := arrayNodeLen(x)
		if !ok || n < 0 {
			return 0, 0, false
		}
		es, ea, ok := f.targetLayout(typeAt{x.TypeNode, u.s, u.f}, depth+1)
		if !ok {
			return 0, 0, false
		}
		return n * es, ea, true
	case *TypeNodeStruct:
		size, ok := u.f.structSize(u, x, depth)
		return size, 4, ok
	}
	return 0, 0, false
}

// memberAlign is PaddedTypeAlign: the alignment the target's compiler gives a
// member of a struct, of the given size and TypeAlign -- four where it takes four
// bytes or more.
func memberAlign(size, align int64) int64 {
	if size >= 4 {
		return 4
	}
	return align
}

// structSize is the size the target's compiler gives the struct st, written at u.
// It lays a struct out twice (frontends/common.c). The first pass, as it declares
// the members, aligns one of two bytes on two and one of four or more on four, and
// rounds the end up to four. The second, fixupVarOffset, aligns each on its
// PaddedTypeAlign and gives the members their offsets -- and the size only where it
// ends PAST the first, unrounded: `struct { b uint8; c [0]int32; d int16 }` is six
// bytes, c aligned on four by its element. Where the first pass aligns more, `{ a
// uint8; b [2]uint8; c uint8 }`, the members are at 0, 1 and 3 and the size eight.
// An empty struct is the byte the C gives it, four bytes.
func (f *File) structSize(u typeAt, st *TypeNodeStruct, depth int) (int64, bool) {
	var end1, end2 int64
	ok := true
	f.eachFieldLayout(u, st, depth, func(_ Token, off, size int64) {
		if off+size > end2 {
			end2 = off + size
		}
		switch {
		case size == 2:
			end1 = (end1 + 1) &^ 1
		case size >= 4:
			end1 = (end1 + 3) &^ 3
		}
		end1 += size
	}, func(int64) {}, &ok)
	if !ok {
		return 0, false
	}
	if end1 == 0 {
		end1 = 1 // char _ogo_empty, of a struct of no fields or of none but zero-sized ones
	}
	size := (end1 + 3) &^ 3
	if end2 > size {
		size = end2
	}
	return size, true
}

// eachFieldLayout lays out the fields of the struct st, written at u, calling field
// for each with its name and offset and end with where the last one ends. A field
// it cannot lay out clears ok.
func (f *File) eachFieldLayout(u typeAt, st *TypeNodeStruct, depth int, field func(name Token, off, size int64), end func(int64), ok *bool) {
	var off int64
	for _, fld := range st.Fields {
		names := fld.Names
		tn := fld.TypeNode
		if emb, isEmb := embeddedFieldName(fld); isEmb {
			names = []Token{emb}
			tn = &TypeNodeIdent{Qualifier: fld.EmbeddedPkg, Name: emb}
			if fld.EmbeddedPtr {
				tn = &TypeNodePointer{TypeNode: tn}
			}
		}
		if tn == nil {
			*ok = false
			return
		}
		size, align, good := f.targetLayout(typeAt{tn, u.s, u.f}, depth+1)
		if !good {
			*ok = false
			return
		}
		a := memberAlign(size, align)
		if size == 0 {
			// A member of no bytes moves nothing: the emitter declares one
			// whose element is aligned on more than a byte over uint8_t
			// (zeroSizedDecl), the target's compiler having sized the struct
			// without the move and placed its members with it.
			a = 1
		}
		for _, nm := range names {
			off = (off + a - 1) / a * a
			field(nm, off, size)
			off += size
		}
	}
	end(off)
}

// fieldOffset is the offset of the field name in the struct of type t, a field of
// its own or one promoted from an embedded struct at the shallowest depth that has
// it, as Go selects -- reachable without going through a pointer, which Offsetof
// asks. The string says why there is none.
func (f *File) fieldOffset(t typeAt, name string) (int64, string) {
	type level struct {
		t    typeAt
		base int64
	}
	cur := []level{{t: t}}
	for depth := 0; depth < 16 && len(cur) != 0; depth++ {
		var found []int64
		var viaPtr bool
		var next []level
		for _, l := range cur {
			u := l.t.f.underlyingTypeAt(l.t)
			st, ok := u.tn.(*TypeNodeStruct)
			if !ok {
				continue
			}
			good := true
			type entry struct {
				name Token
				off  int64
			}
			var entries []entry
			u.f.eachFieldLayout(u, st, 0, func(nm Token, off, _ int64) {
				entries = append(entries, entry{nm, off})
			}, func(int64) {}, &good)
			if !good {
				return 0, "cannot lay its type out"
			}
			for i, fld := range st.Fields {
				_ = i
				if emb, isEmb := embeddedFieldName(fld); isEmb {
					var off int64
					for _, e := range entries {
						if e.name == emb {
							off = e.off
						}
					}
					if emb.Src() == name {
						found = append(found, l.base+off)
						continue
					}
					if fld.EmbeddedPtr {
						eu := u.f.underlyingTypeAt(typeAt{&TypeNodeIdent{Qualifier: fld.EmbeddedPkg, Name: emb}, u.s, u.f})
						if est, isSt := eu.tn.(*TypeNodeStruct); isSt && slices.ContainsFunc(est.Fields, func(p ParameterDeclNode) bool {
							return slices.ContainsFunc(p.Names, func(n Token) bool { return n.Src() == name })
						}) {
							viaPtr = true
						}
						continue
					}
					next = append(next, level{typeAt{&TypeNodeIdent{Qualifier: fld.EmbeddedPkg, Name: emb}, u.s, u.f}, l.base + off})
					continue
				}
				for _, nm := range fld.Names {
					if nm.Src() != name {
						continue
					}
					for _, e := range entries {
						if e.name == nm {
							found = append(found, l.base+e.off)
						}
					}
				}
			}
		}
		switch {
		case len(found) == 1:
			return found[0], ""
		case len(found) > 1:
			return 0, "ambiguous selector"
		case viaPtr:
			return 0, "selector implies indirection of embedded field"
		}
		cur = next
	}
	return 0, "no such field"
}

// unsafeLayoutConst computes unsafe.Sizeof, Alignof or Offsetof of arg, the one
// argument of the call: a value whose type is asked, never evaluated, and for
// Offsetof a selector of a field. The string says why there is no value.
func (f *File) unsafeLayoutConst(s *Scope, fn string, arg Node) (int64, string) {
	if f.isNilOperand(arg) {
		return 0, "use of untyped nil"
	}
	if fn == "Offsetof" {
		for range 8 { // Go takes the selector parenthesised, `Offsetof((x.f))`
			in, isParen := f.parenthesized(arg)
			if !isParen {
				break
			}
			arg = in
		}
		inner, ok := f.soleFactorOf(arg)
		if !ok {
			return 0, "invalid argument: " + f.exprSource(arg) + " is not a selector expression"
		}
		kids := slices.Collect(it(inner.ast))
		if len(kids) != 2 || kids[1].sym != FactorSuffix {
			return 0, "invalid argument: " + f.exprSource(arg) + " is not a selector expression"
		}
		steps := slices.Collect(it(kids[1].ast))
		if len(steps) == 0 || steps[len(steps)-1].sym != Selector {
			return 0, "invalid argument: " + f.exprSource(arg) + " is not a selector expression"
		}
		member := selectorTok(f, steps[len(steps)-1])
		base := factorWithoutLastStep(kids, steps)
		t, ok := f.valueTypeAt(s, base)
		if !ok || t.tn == nil {
			return 0, "cannot tell the type of " + f.exprSource(arg)
		}
		if t.f == nil {
			t.f = f
		}
		u := t.f.underlyingTypeAt(t)
		if p, isPtr := u.tn.(*TypeNodePointer); isPtr {
			u = t.f.underlyingTypeAt(typeAt{p.TypeNode, u.s, u.f})
		}
		if _, isSt := u.tn.(*TypeNodeStruct); !isSt {
			return 0, "invalid argument: " + f.exprSource(arg) + " is not a selector of a struct field"
		}
		off, why := f.fieldOffset(u, member.Src())
		if why == "no such field" {
			return 0, "invalid argument: " + f.exprSource(arg) + " is not a selector of a struct field"
		}
		if why != "" {
			return 0, "invalid argument: " + why + " " + f.exprSource(arg)
		}
		return off, ""
	}
	var size, align int64
	k, kindKnown := f.exprType(s, arg)
	switch {
	case kindKnown && (k == UntypedInt || k == UntypedRune):
		size, align = 4, 4 // an untyped constant's default type: int, rune
	case kindKnown && k == UntypedFloat:
		size, align = 4, 4 // float64, 32 bits here
	case kindKnown && k == UntypedBool:
		size, align = 1, 1
	case kindKnown && k == UntypedString:
		size, align = 8, 4
	default:
		t, ok := f.valueTypeAt(s, arg)
		if !ok || t.tn == nil {
			if !kindKnown {
				return 0, "cannot tell the type of " + f.exprSource(arg)
			}
			t = typeAt{&TypeNodeIdent{Name: Token{}}, s, f}
			switch k {
			case PredeclaredBool, PredeclaredInt8, PredeclaredUint8:
				size, align = 1, 1
			case PredeclaredInt16, PredeclaredUint16:
				size, align = 2, 2
			case PredeclaredInt64, PredeclaredUint64, PredeclaredString:
				size, align = 8, 4
			default:
				size, align = 4, 4
			}
			break
		}
		var good bool
		if size, align, good = f.targetLayout(t, 0); !good {
			return 0, "cannot tell the size of " + f.exprSource(arg)
		}
	}
	if fn == "Alignof" {
		return memberAlign(size, align), ""
	}
	return size, ""
}

// constUnsafe folds `unsafe.Sizeof(x)`, `unsafe.Alignof(x)` and
// `unsafe.Offsetof(x.f)`, Go's constants of type uintptr, recording the value by
// the call's parentheses for the emitter (lenConsts, as a constant len is). A call
// with something wrong is an unknown value, checkQualifiedRef saying what.
func (f *File) constUnsafe(s *Scope, n Node) (ExpressionNode, bool) {
	fn, arg, call, ok := f.unsafeLayoutCall(s, n)
	if !ok {
		return nil, false
	}
	v, why := f.unsafeLayoutConst(s, fn, arg)
	if why != "" {
		return constVal{cv: constant.MakeUnknown()}, true
	}
	if f.lenConsts == nil {
		f.lenConsts = map[*int32]int64{}
	}
	f.lenConsts[&call.ast[0]] = v
	return constVal{cv: constant.MakeInt64(v)}.typedAs(PredeclaredUintptr), true
}

// unsafeLayoutCall matches a Factor that is a call of one of unsafeLayoutFuncs, the
// qualifier naming this file's import of unsafe, with exactly one argument.
func (f *File) unsafeLayoutCall(s *Scope, n Node) (fn string, arg, call Node, ok bool) {
	kids := slices.Collect(it(n.ast))
	if len(kids) != 2 || kids[0].sym != 0 || f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", Node{}, Node{}, false
	}
	qual := f.tok(kids[0].tok)
	if !f.isImportQualifier(s, qual.Src()) || !f.unsafeQualifier(qual) {
		return "", Node{}, Node{}, false
	}
	steps := slices.Collect(it(kids[1].ast))
	if len(steps) != 2 || steps[0].sym != Selector || steps[1].sym != CallSuffix || len(steps[1].ast) == 0 {
		return "", Node{}, Node{}, false
	}
	fn = selectorTok(f, steps[0]).Src()
	if !unsafeLayoutFuncs[fn] {
		return "", Node{}, Node{}, false
	}
	args := argNodes(f.callArgList(steps[1]))
	if len(args) != 1 {
		return "", Node{}, Node{}, false
	}
	return fn, args[0], steps[1], true
}
