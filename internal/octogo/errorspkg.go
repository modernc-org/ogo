// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/octogo"

import (
	"fmt"
	"strings"
)

// The two functions of the errors package the compiler writes (errorsSrc in
// build.go): New, at each call, and asTarget, once for the program.

// errorsPrefix is the errors package's namespace in the C.
const errorsPrefix = "errors"

// errorsNewOrigin names the error errors.New makes in a function, for the lifetime
// rules: the errorString it points at is a temporary of the block the call stands
// in, as a composite literal's is.
const errorsNewOrigin = "the error errors.New makes in a function"

// errorsNewRef is the reference errors.New in a function is.
func errorsNewRef() frameRef {
	return frameRef{origin: errorsNewOrigin, what: "an error made by errors.New in this function"}
}

// errorsNewArg reports whether ast is a call of errors.New -- `errors.New(x)` under
// whatever name the package was imported as, and `New(x)` in the package's own
// source -- and answers its argument.
func (e *emitter) errorsNewArg(ast []int32) (Node, bool) {
	recv, sfx, ok := e.directCall(e.unparenExpr(ast))
	if !ok {
		return Node{}, false
	}
	var call Node
	switch {
	case len(sfx) == 1 && sfx[0].sym == CallSuffix && recv == "New" && e.curPkgPrefix == errorsPrefix:
		call = sfx[0]
	case len(sfx) == 2 && sfx[0].sym == Selector && sfx[1].sym == CallSuffix &&
		e.importQualifiers[recv] == errorsPrefix && e.soleIdent(sfx[0].ast) == "New":
		if _, isLocal := e.locals[recv]; isLocal {
			return Node{}, false
		}
		call = sfx[1]
	default:
		return Node{}, false
	}
	args := e.callArgExprs(call.ast)
	if len(args) != 1 {
		return Node{}, false
	}
	return args[0], true
}

// errorsNewC writes errors.New(text) and answers the C of its value: an error
// holding the address of an errorString made where the call stands. Go allocates
// one; here, in a package variable's initializer, it is a static object of the
// program -- the sentinel idiom, `var ErrTimeout = errors.New("timeout")` -- and in
// a function a temporary of the block, which the lifetime rules hold where a
// composite literal's address is held (frameRefOf answers errorsNewRef).
//
// The value is a NAME in both, never a compound literal: an interface's compound
// literal is a member read or a cast away from shapes the target's compiler refuses
// or crashes on (doc/complit-arg-in-cast.c).
//
// discard is a call standing as a statement: the text is evaluated, as Go evaluates
// an argument, and nothing is made.
func (e *emitter) errorsNewC(arg Node, discard bool) (string, bool) {
	text := e.captureC(func() { e.emitExpr(arg.ast) })
	if discard {
		return "(void)(" + text + ")", true
	}
	es := e.mangle(errorsPrefix, "errorString")
	errC := e.errorIfaceCType()
	if !e.needVTable(errC, es) {
		return "", false
	}
	vt := e.ifaceVTVar(errC, es)
	if e.pkgScope {
		obj := fmt.Sprintf("ogo_plit_%d", e.makeN)
		val := fmt.Sprintf("ogo_plit_%d", e.makeN+1)
		e.makeN += 2
		e.pkgLitObjects = append(e.pkgLitObjects,
			"static "+es+" "+obj+";",
			"static "+errC+" "+val+" = {&"+obj+", &"+vt+"};")
		e.prologue = append(e.prologue, obj+".s = "+text+";\n")
		return val, true
	}
	obj := e.hoist(es, func() { e.emit("{" + text + "}") })
	return e.hoist(errC, func() { e.emit("{&" + obj + ", &" + vt + "}") }), true
}

// seedErrorsAsTarget gives asTarget, which has no body to summarise, the summary
// its C has: it stores what its first parameter holds through its second, which is
// what As does with the error it finds -- `errors.As(err, &gp)` keeps err's referent
// in gp. Nothing else: it keeps neither parameter itself.
func (e *emitter) seedErrorsAsTarget(d []int32) {
	name, _, body, recv, ok := e.funcParts(d)
	if !ok || body != nil || recv != nil || name != "asTarget" || e.curPkgPrefix != errorsPrefix {
		return
	}
	cname := e.mangle(errorsPrefix, name)
	e.crossParams[cname] = make([]leak, 2)
	e.crossContents[cname] = make([]leak, 2)
	e.crossInto[cname] = []uint32{1 << 1, 0}
	e.crossNames[cname] = name
	e.errorsAsTarget = true
}

// mintErrorsAsTarget writes asTarget: whether an error's concrete value is
// assignable to what target points at and, if it is, the store. Go's asks
// reflection; here what target points at is told by target's TABLE, one per type
// put into the empty interface, so the function tests target's table against every
// one the program makes -- known only once every body is emitted, which is why it is
// written here, after them. Writing it makes tables of its own (the candidates of
// an interface target), so it is written again until no table of the empty
// interface is new.
//
// For a target pointing at
//   - error itself: the error is stored as it is;
//   - another interface: the error is, where its concrete type implements that
//     interface, under the table of the pair -- the test a type switch case makes;
//   - a pointer *Y: the error is, where it holds a *Y -- its table is Y's;
//   - anything else: nothing is, since an interface holds a pointer here and never
//     a Y itself.
//
// And it panics, as Go's As does, for a target that is no pointer to an interface
// or to a type implementing error, and for a nil pointer: the compiler refuses such
// a target where it sees its type (checkErrorsAsTarget), so only one handed through
// an `any` reaches these.
func (e *emitter) mintErrorsAsTarget() {
	if !e.errorsAsTarget {
		return
	}
	cname := e.mangle(errorsPrefix, "asTarget")
	params := e.funcParams[cname]
	if len(params) != 2 {
		e.fail("errors.asTarget: unexpected signature")
		return
	}
	errC, anyC := params[0], params[1]
	e.needPanic()
	savedLocals := e.locals
	defer func() { e.locals = savedLocals }()
	var text string
	for seen := map[string]bool{}; ; {
		targets := e.ifaceConcretes(anyC)
		fresh := false
		for _, t := range targets {
			if !seen[t] {
				seen[t], fresh = true, true
			}
		}
		if !fresh && text != "" {
			break
		}
		e.locals = map[string]string{"ogo_err": errC, "ogo_target": anyC}
		var b strings.Builder
		fmt.Fprintf(&b, "_Bool %s(%s ogo_err, %s ogo_target) {\n", cname, errC, anyC)
		// (void): with no target a program makes, the error is read by no test.
		b.WriteString("\t(void)ogo_err;\n\tif (!ogo_target.data) ogo_panic(\"errors: target must be a non-nil pointer\");\n")
		for _, t := range targets {
			test, ok := e.errorsAsTest(errC, anyC, t)
			if !ok {
				return
			}
			b.WriteString(test)
		}
		b.WriteString("\togo_panic(\"errors: *target must be interface or implement error\");\n\treturn 0;\n}\n")
		text = b.String()
		if !fresh {
			break
		}
	}
	e.vtables.WriteString(text)
}

// errorsAsTest is mintErrorsAsTarget's test for a target pointing at a t.
func (e *emitter) errorsAsTest(errC, anyC, t string) (string, bool) {
	var b strings.Builder
	fmt.Fprintf(&b, "\tif (ogo_target.vt == &%s) {\n", e.ifaceVTVar(anyC, t))
	switch {
	case t == errC:
		fmt.Fprintf(&b, "\t\t*(%s*)ogo_target.data = ogo_err;\n\t\treturn 1;\n", errC)
	case e.isIfaceCType(t):
		types := e.ifaceConcretes(errC)
		cond := "ogo_err.vt != 0"
		if len(e.ifaceMethods[t]) != 0 {
			var ok bool
			if cond, types, ok = e.ifaceCaseCond("ogo_err", errC, t); !ok {
				return "", false
			}
		}
		rebind, ok := e.ifaceRebindC("ogo_v", t, "ogo_err", errC, types, 3)
		if !ok {
			return "", false
		}
		fmt.Fprintf(&b, "\t\tif (%s) {\n\t\t\t%s ogo_v = {0};\n%s\t\t\t*(%s*)ogo_target.data = ogo_v;\n\t\t\treturn 1;\n\t\t}\n\t\treturn 0;\n",
			cond, t, rebind, t)
	case e.isPointer(t):
		y := e.elemType(t)
		if !e.implementsIface(y, errC) {
			return "", true // no test: falls to the panic, *Y implementing no error
		}
		if e.ifaceVTables[errC+"|"+y] {
			fmt.Fprintf(&b, "\t\tif (ogo_err.vt == &%s) {\n\t\t\t*(%s*)ogo_target.data = (%s)ogo_err.data;\n\t\t\treturn 1;\n\t\t}\n",
				e.ifaceVTVar(errC, y), t, t)
		}
		b.WriteString("\t\treturn 0;\n")
	default:
		if !e.valueImplementsError(t, errC) {
			return "", true
		}
		b.WriteString("\t\treturn 0;\n")
	}
	b.WriteString("\t}\n")
	return b.String(), true
}

// valueImplementsError reports whether a VALUE of the concrete type t, not a
// pointer to one, implements error: its Error method has a value receiver.
func (e *emitter) valueImplementsError(t, errC string) bool {
	if !e.implementsIface(t, errC) {
		return false
	}
	cname, _, _, has := e.promotedMethod(t, "Error")
	return has && !e.methodPtr[cname]
}

// checkErrorsAsTarget refuses a target of errors.As whose type says Go's As panics
// on it -- vet's errorsas check, which go test runs: a pointer to neither an
// interface nor a type implementing error, or no pointer at all. A target of an
// interface type is a pointer only at run time, and asTarget asks it there.
func (e *emitter) checkErrorsAsTarget(args []Node) bool {
	if len(args) != 2 {
		return true
	}
	a := args[1]
	if e.isNilExpr(a.ast) {
		e.failAt(a.ast, "second argument to errors.As must be a non-nil pointer to either a type that implements error, or to any interface type")
		return false
	}
	ct, ok := e.inferCType(a.ast)
	if !ok || e.isIfaceCType(ct) {
		return true
	}
	errC := e.errorIfaceCType()
	if e.isPointer(ct) {
		t := e.elemType(ct)
		switch {
		case e.isIfaceCType(t):
			return true
		case e.isPointer(t) && e.implementsIface(e.elemType(t), errC):
			return true
		case !e.isPointer(t) && e.valueImplementsError(t, errC):
			return true
		}
	}
	e.failAt(a.ast, "second argument to errors.As must be a non-nil pointer to either a type that implements error, or to any interface type")
	return false
}
