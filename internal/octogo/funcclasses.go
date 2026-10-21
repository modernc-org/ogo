// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// A recursion through a call of a function value was invisible to the stack reader
// (internal/build, stack.go): the listing says only that a call goes through a
// register, and such a call was taken to reach every function whose address the
// program takes. That is sound for a stack's size and closes cycles no run makes --
// obeam's interpreter calls its instruction handlers and its BIFs through two
// tables of one signature, each taken to reach the other's functions -- so a cycle
// only such a call closes was walked once, and a goroutine recursing through a
// callback was given what one pass needs.
//
// What a call through a value may reach is what its TYPE may hold: the functions
// the program used as values of that type. The checker records the defined type a
// function used as a value becomes, where it stands in a position wanting one
// (noteFuncValueType). Where the build asks (FuncValueClasses), the emitter spells
// every call through a function value with the C type of the value, `OGO_FV(type,
// callee)(args)`, a macro that is its callee, and writes after the C what each such
// type may hold:
//
//   - a value of a function type written out, `func(int) int`, any function of its
//     signature the program uses as a value -- of whatever type, as Go assigns
//     either way between it and a defined type of that signature;
//   - and so may a value of a DEFINED type of a signature for which the program has
//     a value written out anywhere -- a variable, a field, a parameter, a result --
//     or converts a value of one defined type of it to another;
//   - a value of any other defined type holds the functions made values of that
//     type and those used as values where no type was recorded. Two defined types
//     are two types to the checker, so nothing else reaches one -- but through a
//     pointer read as a pointer to another type, `*(*Op)(unsafe.Pointer(&h))`, or
//     converted to one, `(*Op)(&h)`, which Go allows of two types of one
//     signature: such a conversion opens the signature of each function type
//     either pointer reaches (noteFuncValuePun).
//
// The macro rides on the call's own text, so a rendering the emitter throws away --
// one made to learn a type -- takes its mark with it, and the C compiles to the
// binary it compiled to without. A member is what a value of the type IS, which is
// not always the function made a value: a method value is a wrapper lifted with its
// receiver bound, one by receiver, and a function of several results is taken
// through one writing them out (fvHeld). The table named the function until
// 2026-10-21, which was enough for a recursion and is not for a stack's size; the
// build reads the classes for both.

// FuncValueClasses has the emitter spell each call through a function value with
// the type of the value, and write after the C what each such type may hold (see
// above, and ReadFuncValueClasses).
func FuncValueClasses() EmitOption {
	return func(e *emitter) { e.fvTable = true }
}

// fvCallMacro is the callee of a call through a function value with the type of the
// value beside it.
const fvCallMacro = "OGO_FV"

// fvState is what the emitter gathers for FuncValueClasses.
type fvState struct {
	fvTable bool
	// fvClassMembers is, by the C type of a defined function type, the functions
	// made values of it, as funcValueMembers names them.
	fvClassMembers map[string][]string
	// fvAnyMembers is, by signature (funcShapeID), the functions used as values
	// where no type was recorded.
	fvAnyMembers map[string][]string
	// fvOpen is the signatures whose defined types may hold each other's values: a
	// conversion between two made them so, or a pointer to one read as another
	// (noteFuncValuePun); fvAllOpen says that of every signature.
	fvOpen    map[string]bool
	fvAllOpen bool
	// fvTypes is the C types calls through function values were spelled with.
	fvTypes map[string]bool
	// fvLits is, by its place (litKey), the C functions a literal was lifted to.
	fvLits map[string][]string
	// fvBound is, by the type fvCallee spelled a call through a value known to
	// hold one function with, that function as funcValueMembers names it;
	// fvBoundKey the same the other way round.
	fvBound, fvBoundKey map[string]string
	// fvHeld is, by the C name of a function a value is made of, the C functions
	// the value is instead: the wrappers a method value is lifted to, one by its
	// receiver, and the one a function of several results is taken through.
	fvHeld map[string][]string
}

// noteFuncValueHeld records the C function wrapper as what a value of the function
// cname is.
func (e *emitter) noteFuncValueHeld(cname, wrapper string) {
	if e.fvHeld == nil {
		e.fvHeld = map[string][]string{}
	}
	if !slices.Contains(e.fvHeld[cname], wrapper) {
		e.fvHeld[cname] = append(e.fvHeld[cname], wrapper)
	}
}

// noteFuncValueLit records the literal at key lifted to the C function cname.
func (e *emitter) noteFuncValueLit(key, cname string) {
	if e.fvLits == nil {
		e.fvLits = map[string][]string{}
	}
	e.fvLits[key] = append(e.fvLits[key], cname)
}

// fvCallee is the callee text of a call through a function value of C type ct,
// raw as written and callee as it is to be called, spelled with its type where the
// build asks (FuncValueClasses). A local the bindings know holds one function on
// every path (boundFunc) is spelled with a type of its own, holding that function:
// the knowledge the call's nil check is left out on.
func (e *emitter) fvCallee(ct, raw, callee string) string {
	if !e.fvTable {
		return callee
	}
	ct = e.unaliased(ct)
	if isCIdent(raw) {
		if fn := e.boundFunc(raw); fn != "" {
			if e.fvBound == nil {
				e.fvBound, e.fvBoundKey = map[string]string{}, map[string]string{}
			}
			key, ok := e.fvBoundKey[fn]
			if !ok {
				key = fmt.Sprintf("%s%d", fvBoundType, len(e.fvBoundKey))
				e.fvBoundKey[fn], e.fvBound[key] = key, fn
			}
			ct = key
		}
	}
	if e.fvTypes == nil {
		e.fvTypes = map[string]bool{}
	}
	e.fvTypes[ct] = true
	return fvCallMacro + "(" + ct + ", " + callee + ")"
}

// fvBoundType begins the name of the type of a call through a value known to hold
// one function (fvCallee).
const fvBoundType = "ogo_fvbound"

// fvBareCallee is a callee fvCallee spelled, as it was before.
func fvBareCallee(fn string) string {
	inner, ok := strings.CutPrefix(fn, fvCallMacro+"(")
	if !ok || !strings.HasSuffix(inner, ")") {
		return fn
	}
	_, callee, ok := strings.Cut(inner[:len(inner)-1], ", ")
	if !ok {
		return fn
	}
	return callee
}

// fvContextOf is the C type of the defined type the checker recorded for the value
// starting at the Factor kids, and "" where it recorded none or the type is no
// function type of the emitter's.
func (e *emitter) fvContextOf(kids []Node) string {
	if len(kids) == 0 || e.f == nil {
		return ""
	}
	dt, ok := e.f.funcValueTypes[kids[0].Pos()]
	if !ok {
		return ""
	}
	ct := e.unaliased(e.typeMangle(pkgPrefix(dt.importPath), dt.name))
	if !e.isFuncCType(ct) || e.underlyingCType(ct) == ct {
		return ""
	}
	return ct
}

// noteFuncValueClass records the function name, as funcValueMembers names it, used
// as a value of signature fv: of the defined type ctx, or of every type of its
// signature where ctx is "".
func (e *emitter) noteFuncValueClass(fv funcValueType, name, ctx string) {
	if ctx != "" {
		if e.fvClassMembers == nil {
			e.fvClassMembers = map[string][]string{}
		}
		if !slices.Contains(e.fvClassMembers[ctx], name) {
			e.fvClassMembers[ctx] = append(e.fvClassMembers[ctx], name)
		}
		return
	}
	id := funcShapeID(fv)
	if e.fvAnyMembers == nil {
		e.fvAnyMembers = map[string][]string{}
	}
	if !slices.Contains(e.fvAnyMembers[id], name) {
		e.fvAnyMembers[id] = append(e.fvAnyMembers[id], name)
	}
}

// noteFuncValueConv records a conversion to the function type ct of arg: one of
// another type opens ct's signature (fvOpen). A function converted, `Op(h1)`, is a
// value collectFuncValues has taken to be of every type of its signature.
func (e *emitter) noteFuncValueConv(ct string, arg Node) {
	if !e.isFuncCType(ct) {
		return
	}
	if src, ok := e.inferCType(arg.ast); ok && e.unaliased(src) == e.unaliased(ct) {
		return
	}
	if kids, ok := e.soleFactor(arg.ast); ok && e.isFuncValueFactor(kids) {
		return
	}
	if e.fvOpen == nil {
		e.fvOpen = map[string]bool{}
	}
	e.fvOpen[e.funcTypeID(e.underlyingCType(ct))] = true
}

// noteFuncValuePun records a conversion between pointers, or between a pointer and
// unsafe.Pointer, of the C types from and to, "" for one not known: what either
// points at may now be read as the other, a value of one function type as one of
// another of its signature -- `*(*Op)(unsafe.Pointer(&h))`, and `(*Op)(&h)`, which
// needs no unsafe -- so each function type either holds opens its signature
// (fvOpen). A type not known opens every signature.
func (e *emitter) noteFuncValuePun(from, to string) {
	for _, ct := range []string{from, to} {
		if ct == "" {
			e.fvAllOpen = true
			continue
		}
		e.funcTypesHeld(ct, map[string]bool{}, func(ft string) {
			if e.fvOpen == nil {
				e.fvOpen = map[string]bool{}
			}
			e.fvOpen[e.funcTypeID(e.underlyingCType(ft))] = true
		})
	}
}

// funcTypesHeld calls each with every function type a value of the C type ct holds:
// ct itself, a field's, an element's, and what a pointer, a slice or a channel
// reaches.
func (e *emitter) funcTypesHeld(ct string, seen map[string]bool, each func(string)) {
	ct = e.unaliased(ct)
	if seen[ct] {
		return
	}
	seen[ct] = true
	switch {
	case e.isFuncCType(ct):
		each(ct)
	case strings.HasSuffix(ct, "*"):
		e.funcTypesHeld(strings.TrimSuffix(ct, "*"), seen, each)
	case e.isSliceCType(ct):
		e.funcTypesHeld(sliceElemFromCName(ct), seen, each)
	case e.isChanCType(ct):
		e.funcTypesHeld(e.chanElemOfCType(ct), seen, each)
	}
	if a, ok := e.namedArrays[ct]; ok {
		e.funcTypesHeld(a.elem, seen, each)
	}
	for _, f := range e.structs[e.underlyingCType(ct)] {
		e.funcTypesHeld(f.ctype, seen, each)
	}
}

// isFuncValueFactor reports whether a Factor names a function as a value: a
// function, another package's, a literal, a method value or a method expression --
// what noteFuncValue records.
func (e *emitter) isFuncValueFactor(kids []Node) bool {
	if len(kids) == 1 && kids[0].sym == FuncLiteral {
		return true
	}
	if me, isME := e.methodExprAt(kids); isME && len(me.rest) == 0 {
		return true
	}
	if _, _, isMV := e.factorMethodValue(kids); isMV {
		return true
	}
	if len(kids) == 0 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT {
		return false
	}
	name := e.src(kids[0].tok)
	switch {
	case len(kids) == 1:
		_, isFunc := e.userFunc(name)
		return isFunc
	case len(kids) == 2 && kids[1].sym == FactorSuffix:
		steps := slices.Collect(it(kids[1].ast))
		if len(steps) == 1 && steps[0].sym == Selector {
			if prefix, isImport := e.importQualifiers[name]; isImport {
				_, ok := e.funcValueTypes[e.mangle(prefix, e.soleIdent(steps[0].ast))]
				return ok
			}
		}
	}
	return false
}

// fvMemberCNames are the C functions a member of funcValueMembers is: a function's
// own name, a literal's lifted functions, the function a method expression is
// lifted to; nil for one never emitted.
func (e *emitter) fvMemberCNames(m string) []string {
	switch {
	case strings.HasPrefix(m, "lit@"):
		return e.fvLits[m]
	case strings.HasPrefix(m, "mexpr:"):
		me, ok := e.methodExprMembers[m]
		if !ok {
			return nil
		}
		if name, ok := e.methodExprNames[fmt.Sprintf("%s.%s.%v", me.typeC, me.member, me.ptr)]; ok {
			return []string{name}
		}
		return nil
	}
	return []string{m}
}

// funcValueClassesC is the table FuncValueClasses asks for, read against text, the
// program's C: a comment ReadFuncValueClasses reads.
func (e *emitter) funcValueClassesC(text []byte) string {
	var b strings.Builder
	b.WriteString("\n/* ogo:funcvalues\n")
	cts := make([]string, 0, len(e.fvTypes))
	for ct := range e.fvTypes {
		cts = append(cts, ct)
	}
	sort.Strings(cts)
	used := map[string]bool{}
	for _, ct := range cts {
		var members []string
		under := e.underlyingCType(ct)
		shape := e.funcTypeID(under)
		switch fn, bound := e.fvBound[ct]; {
		case bound:
			members = []string{fn}
		case shape == "":
			continue // no type of the emitter's: nothing is said of it
		case under == ct || e.fvAllOpen || e.fvOpen[shape] || writtenOutUsed(text, under):
			members = e.funcValueMembers[shape]
		default:
			members = append(slices.Clone(e.fvClassMembers[ct]), e.fvAnyMembers[shape]...)
		}
		var names []string
		for _, m := range members {
			for _, cn := range e.fvMemberCNames(m) {
				for _, held := range e.fvHeldBy(text, cn, used) {
					if !slices.Contains(names, held) {
						names = append(names, held)
					}
				}
			}
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "type %s\n", ct)
		for _, cn := range names {
			fmt.Fprintf(&b, "member %s %s\n", ct, cn)
		}
	}
	b.WriteString("*/\n")
	return b.String()
}

// fvHeldBy is the C functions a value of the function cname may be, read against
// text, the program's C: cname itself, or where the value is a wrapper, the wrappers
// the C takes as values. A wrapper is lifted once and kept, a rendering thrown away
// among those that lift one, so one the C names only where it declares and defines
// it is no value's. used remembers the answer for each wrapper.
func (e *emitter) fvHeldBy(text []byte, cname string, used map[string]bool) []string {
	held, ok := e.fvHeld[cname]
	if !ok {
		return []string{cname}
	}
	var r []string
	for _, w := range held {
		u, asked := used[w]
		if !asked {
			u = len(regexp.MustCompile(`\b`+regexp.QuoteMeta(w)+`\b`).FindAllIndex(text, 3)) > 2
			used[w] = u
		}
		if u {
			r = append(r, w)
		}
	}
	return r
}

// writtenOutUsed reports whether the C declares anything of the function typedef
// under itself -- a variable, a field, a parameter, a result -- beyond its typedef,
// a defined type's typedef over it and its nil check: a value of the type written
// out, which Go assigns to and from every defined type of its signature.
func writtenOutUsed(text []byte, under string) bool {
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(under) + `\b`)
	guard := "static " + under + " " + nilHelperName(under) + "("
	for _, line := range strings.Split(string(text), "\n") {
		if !word.MatchString(line) {
			continue
		}
		if t := strings.TrimSpace(line); !strings.HasPrefix(t, "typedef ") && !strings.HasPrefix(t, guard) {
			return true
		}
	}
	return false
}

var fvTableRE = regexp.MustCompile(`(?s)/\* ogo:funcvalues\n(.*?)\*/`)

// WithoutFuncValueClasses is C emitted with FuncValueClasses as it is emitted
// without: the macro's definition, its uses and the table taken out. The C of a
// program is the same either way, but for those, which is what this is for: a
// difference it leaves is one the option made.
func WithoutFuncValueClasses(c []byte) []byte {
	s := strings.Replace(string(c), "#define "+fvCallMacro+"(t, f) (f)\n", "", 1)
	if loc := fvTableRE.FindStringIndex(s); loc != nil {
		start, end := loc[0], loc[1] // the table, and the line breaks it was written between
		if start > 0 && s[start-1] == '\n' {
			start--
		}
		if end < len(s) && s[end] == '\n' {
			end++
		}
		s = s[:start] + s[end:]
	}
	var b strings.Builder
	for {
		i := strings.Index(s, fvCallMacro+"(")
		if i < 0 {
			b.WriteString(s)
			return []byte(b.String())
		}
		b.WriteString(s[:i])
		rest := s[i+len(fvCallMacro)+1:]
		comma := strings.Index(rest, ", ")
		depth, end := 1, -1
		for j := comma + 2; comma >= 0 && j < len(rest) && end < 0; j++ {
			switch rest[j] {
			case '(':
				depth++
			case ')':
				if depth--; depth == 0 {
					end = j
				}
			}
		}
		if end < 0 {
			b.WriteString(s[i:])
			return []byte(b.String())
		}
		// The callee alone, unwrapped again if it holds another such call.
		s = rest[comma+2:end] + rest[end+1:]
	}
}

// ReadFuncValueClasses reads the table FuncValueClasses had written into c: by the
// C type of a function value a call is made through, the C functions a value of it
// may hold. A type it has no entry for is one nothing is known of; ok is false
// where there is no table.
func ReadFuncValueClasses(c []byte) (members map[string][]string, ok bool) {
	m := fvTableRE.FindSubmatch(c)
	if m == nil {
		return nil, false
	}
	members = map[string][]string{}
	for _, line := range strings.Split(string(m[1]), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "type":
			if members[f[1]] == nil {
				members[f[1]] = []string{}
			}
		case len(f) == 3 && f[0] == "member":
			members[f[1]] = append(members[f[1]], f[2])
		}
	}
	return members, true
}
