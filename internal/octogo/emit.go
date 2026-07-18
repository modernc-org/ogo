// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// p2Intrinsics maps exported functions of the builtin "p2" package to the
// flexcc / propeller2.h C intrinsics they wrap (the p2 mapping documented in
// CLAUDE.md's appendix). The call `p2.PinHigh(56)` emits `_pinh(56)`.
var p2Intrinsics = map[string]string{
	"PinHigh":      "_pinh",
	"PinLow":       "_pinl",
	"PinIn":        "_pinr",
	"WaitMs":       "_waitms",
	"AckPin":       "_akpin",
	"ReadPin":      "_rdpin",
	"WritePinMode": "_wrpin",
	"WritePinX":    "_wxpin",
	"WritePinY":    "_wypin",
}

// importIncludes maps an OctoGo import path to the C header it pulls in.
var importIncludes = map[string]string{
	"p2": "propeller2.h",
}

// cTypes maps predeclared OctoGo type names to C types. int is int32 in OctoGo
// and the P2's C int is 32-bit, so int maps to plain int. Fixed-width names use
// <stdint.h> (see stdintType).
var cTypes = map[string]string{
	"int": "int", "uint": "unsigned", "bool": "int",
	"int8": "int8_t", "int16": "int16_t", "int32": "int32_t",
	"uint8": "uint8_t", "uint16": "uint16_t", "uint32": "uint32_t",
	"byte": "uint8_t", "rune": "int32_t", "uintptr": "uintptr_t",
	// A Go string is an immutable { pointer, length } header -- a value type that
	// will later support slicing -- so it maps to the ogo_string struct.
	"string": cString,
}

// cString is the C type of an OctoGo string: a { const char* str; int len; }
// header emitted as stringTypedef, printed via the stringHelpers.
const cString = "ogo_string"

const stringTypedef = "typedef struct { const char* str; int len; } ogo_string;\n"

// stringHelpers print a string header's exact bytes (a slice need not be
// null-terminated, so %.*s, not %s).
const stringHelpers = "static void ogo_print_str(ogo_string s) { printf(\"%.*s\", s.len, s.str); }\n" +
	"static void ogo_println_str(ogo_string s) { printf(\"%.*s\\n\", s.len, s.str); }\n"

// sliceTypePrefix leads the C typedef name of an OctoGo slice `[]T`: a { pointer,
// length, capacity } header (`T* ptr; int len; int cap`) named per element type,
// e.g. []int -> ogo_slice_int, []*Point -> ogo_slice_Point_ptr. Like ogo_string it
// is a value type -- copied by value, a non-owning view over an array or another
// slice's backing storage. cap tracks that storage's remaining length (from ptr to
// its end), so a slice may be re-sliced or grown up to cap; it never acquires new
// backing memory (the P2 has no heap).
const sliceTypePrefix = "ogo_slice_"

// sliceCName is the C type name of a slice with C element type elem. A pointer
// element's "*" is spelled "_ptr" so the name stays a valid C identifier.
func sliceCName(elem string) string {
	return sliceTypePrefix + sanitizeElem(elem)
}

// sliceTypedefDef returns the C typedef declaring the slice header for element
// type elem. It has two emission sites -- the typedef section in EmitC, and
// inline in structFieldsOf for a struct-element slice field -- so the shape is
// defined once here.
func sliceTypedefDef(elem string) string {
	return fmt.Sprintf("typedef struct { %s* ptr; int len; int cap; } %s;\n", elem, sliceCName(elem))
}

// sanitizeElem turns a C element type into an identifier fragment: a pointer's "*"
// becomes "_ptr", so []*Point -> ogo_slice_Point_ptr stays a valid C identifier.
func sanitizeElem(elem string) string { return strings.ReplaceAll(elem, "*", "_ptr") }

// sliceElemFromCName recovers the element C type from a slice type name -- the
// inverse of sliceCName ("ogo_slice_int" -> "int", "ogo_slice_Point_ptr" ->
// "Point*").
func sliceElemFromCName(ct string) string {
	return strings.ReplaceAll(strings.TrimPrefix(ct, sliceTypePrefix), "_ptr", "*")
}

// appendCName, tryappendCName and appendokCName name the per-element append
// helpers: the trapping ogo_append_<T>, the ok-form ogo_tryappend_<T>, and the
// { slice, ok } result struct ogo_appendok_<T> the ok form returns.
func appendCName(elem string) string    { return "ogo_append_" + sanitizeElem(elem) }
func tryappendCName(elem string) string { return "ogo_tryappend_" + sanitizeElem(elem) }
func appendokCName(elem string) string  { return "ogo_appendok_" + sanitizeElem(elem) }

// printSliceCName and printlnSliceCName name the per-element slice print helpers
// that render a slice header as "[e0 e1 ...]" over the serial line -- the newline
// form appends a trailing '\n'. An array is printed through the same helpers by
// viewing it as a full-length slice header.
func printSliceCName(elem string) string   { return "ogo_print_slice_" + sanitizeElem(elem) }
func printlnSliceCName(elem string) string { return "ogo_println_slice_" + sanitizeElem(elem) }

// ogoPanicDef is the runtime panic: a best-effort diagnostic to the serial line, a
// short drain so it flushes, then a halt or -- in a release build -- a reboot. A
// debug halt (abort -> _Exit -> _cogstop) stops the offending cog for inspection; a
// release _reboot() restarts the board so an unattended device self-heals.
func ogoPanicDef(release bool) string {
	tail := "\tabort(); // -> _Exit -> _cogstop: halt the offending cog\n"
	if release {
		tail = "\t_reboot(); // restart the board (release: self-heal)\n"
	}
	return "static void ogo_panic(const char* msg) {\n" +
		"\tprintf(\"panic: %s\\n\", msg);\n" +
		"\t_waitms(10); // let the message flush over the serial line first\n" +
		tail +
		"}\n"
}

// ogoBound bounds-checks an index: it returns i when 0 <= i < n, else panics. The
// unsigned compare folds the low and high checks (a negative i wraps to >= n).
const ogoBound = "static int ogo_bound(int i, int n) {\n" +
	"\tif ((unsigned)i >= (unsigned)n) ogo_panic(\"index out of range\");\n" +
	"\treturn i;\n" +
	"}\n"

// ogoNonzero guards a divisor: it returns b when non-zero, else panics.
const ogoNonzero = "static int ogo_nonzero(int b) {\n" +
	"\tif (b == 0) ogo_panic(\"integer divide by zero\");\n" +
	"\treturn b;\n" +
	"}\n"

// sortedKeys returns the keys of a set in deterministic (sorted) order, for stable
// emission of per-element-type typedefs and helpers.
func sortedKeys(m map[string]bool) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	slices.Sort(r)
	return r
}

// EmitC writes C source for the built package pkg to w. It is the walking
// skeleton of the OctoGo C backend, grown to a first computational subset: a
// `func main()` with local int variables, `for {}` loops, assignments and
// arithmetic, calls to the builtin p2 package (mapped to P2 intrinsics), and
// print/println (mapped to printf over the P2 serial line). Anything it does not
// yet understand produces an "emit:" error rather than wrong C, so the surface
// grows honestly.
//
// The traversal mirrors the checker (see it()/sourceFile/funcDecl in check.go):
// dispatch non-terminals on Node.sym, read terminals via File.tok/File.ch.
// EmitOption configures a build. The zero configuration emits no automatic runtime
// checks and halts the offending cog on a panic (abort -> _cogstop). The `ogo build`
// CLI enables checks by default (see internal/build); its --unchecked omits them and
// its --release reboots instead of halting.
type EmitOption func(*emitter)

// Checked emits automatic runtime bounds and divide-by-zero checks: an out-of-range
// index or a divide-by-zero calls ogo_panic rather than silently corrupting memory
// or yielding a garbage quotient. append's own capacity trap is always present,
// independent of this option (choose the s, ok = append form to avoid it).
func Checked() EmitOption { return func(e *emitter) { e.checks = true } }

// Release makes a panic reboot the board (_reboot) instead of halting the cog, so
// an unattended device self-heals. Diagnostics and checks are unaffected.
func Release() EmitOption { return func(e *emitter) { e.release = true } }

func EmitC(pkg *Package, w io.Writer, opts ...EmitOption) error {
	e := &emitter{includes: map[string]bool{}, funcRet: map[string][]string{}, methodPtr: map[string]bool{}, globals: map[string]string{}, structs: map[string][]structField{}, namedTypes: map[string]bool{}, constInt: map[string]string{}, arrays: map[string]arrDim{}, globalArrays: map[string]arrDim{}, sliceVars: map[string]string{}, globalSliceVars: map[string]string{}, sliceElems: map[string]bool{}, sliceElemByName: map[string]string{}, inlineSliceDefs: map[string]bool{}, appendElems: map[string]bool{}, tryappendElems: map[string]bool{}, printSliceElems: map[string]bool{}}
	for _, opt := range opts {
		opt(e)
	}

	// Pass -1: struct type declarations -> C typedefs, recorded in the struct
	// environment (for typing `var p T` and field accesses). Emitted first so a
	// later signature, result struct, or variable of struct type resolves.
	var typedefs bytes.Buffer
	e.w = &typedefs
	for _, f := range pkg.Files {
		e.f = f
		e.collectStructs(f.AST)
	}

	// Pass 0: record each function's C result types in funcRet (for typing calls
	// in `x := f()` and destructuring `a, b := f()`), and emit a result-struct
	// typedef for each multi-result function (C has no multiple return, so a
	// function returning N>1 values returns a struct of N fields).
	for _, f := range pkg.Files {
		e.f = f
		e.collectResults(f.AST)
	}

	// Pass 0.5: package-level constant declarations, emitted (in source order)
	// before the functions that use them and recorded in the global type
	// environment so a `x := CONST` short declaration can be typed.
	var globals bytes.Buffer
	e.w = &globals
	for _, f := range pkg.Files {
		e.f = f
		e.emitPackageConsts(f.AST)
	}
	// Package-level variables follow the constants (so a variable's initializer may
	// fold a constant), each a file-scope `static` recorded in the global type
	// environment.
	for _, f := range pkg.Files {
		e.f = f
		e.emitPackageVars(f.AST)
	}

	// Pass 1: forward prototypes for user functions. C requires a declaration
	// before use, but OctoGo (like Go) does not order top-level declarations, so
	// a call may precede its definition; the prototypes make emission order
	// independent of source order.
	var protos bytes.Buffer
	e.w = &protos
	for _, f := range pkg.Files {
		e.f = f
		e.emitPrototypes(f.AST)
	}

	// Pass 2: the function definitions themselves.
	var body bytes.Buffer
	e.w = &body
	e.wroteDecl = false
	for _, f := range pkg.Files {
		e.f = f
		e.emitFileDecls(f.AST)
	}
	if e.err != nil {
		return e.err
	}

	// Assemble: header, sorted #includes, result-struct typedefs, prototypes,
	// then the definitions.
	incs := make([]string, 0, len(e.includes))
	for inc := range e.includes {
		incs = append(incs, inc)
	}
	slices.Sort(incs)

	var out bytes.Buffer
	out.WriteString("// Code generated by ogo. DO NOT EDIT.\n\n")
	for _, inc := range incs {
		fmt.Fprintf(&out, "#include <%s>\n", inc)
	}
	if len(incs) != 0 {
		out.WriteByte('\n')
	}
	// Slice header typedefs follow the struct typedefs (a slice's element may be a
	// one per distinct element type, split by element type. A scalar-element slice
	// (ogo_slice_int) has no struct dependency and is emitted before the struct
	// typedefs, since a struct field may hold one; a struct-element slice
	// (ogo_slice_Point) references its struct by pointer and so follows the structs.
	var scalarSliceDefs, structSliceDefs bytes.Buffer
	for _, el := range sortedKeys(e.sliceElems) {
		if e.inlineSliceDefs[el] {
			continue // already emitted inline, ahead of the struct field holding it
		}
		def := sliceTypedefDef(el)
		if e.isStruct(el) {
			structSliceDefs.WriteString(def)
		} else {
			scalarSliceDefs.WriteString(def)
		}
	}
	// append's ok-form result struct { slice, ok }, per element type; it references
	// the slice typedef emitted above.
	var appendokDefs bytes.Buffer
	for _, el := range sortedKeys(e.tryappendElems) {
		fmt.Fprintf(&appendokDefs, "typedef struct { %s slice; int ok; } %s;\n", sliceCName(el), appendokCName(el))
	}
	// The ogo_string typedef leads the typedef section (a struct, array, or result
	// field may be a string); scalar-element slice typedefs precede the struct
	// typedefs (a struct field may hold one), struct-element slices and the append
	// ok-form structs follow. The string print helpers follow the typedefs.
	if e.usesString || typedefs.Len() != 0 || scalarSliceDefs.Len() != 0 || structSliceDefs.Len() != 0 || appendokDefs.Len() != 0 {
		if e.usesString {
			out.WriteString(stringTypedef)
		}
		out.Write(scalarSliceDefs.Bytes())
		out.Write(typedefs.Bytes())
		out.Write(structSliceDefs.Bytes())
		out.Write(appendokDefs.Bytes())
		out.WriteByte('\n')
	}
	if e.usesStringPrint {
		out.WriteString(stringHelpers)
		out.WriteByte('\n')
	}
	// Runtime helpers: the panic routine, then the per-element append helpers (the
	// trapping form and the ok form), after the typedefs they reference.
	var helperDefs bytes.Buffer
	if e.usesPanic {
		helperDefs.WriteString(ogoPanicDef(e.release))
	}
	if e.usesBound {
		helperDefs.WriteString(ogoBound)
	}
	if e.usesNonzero {
		helperDefs.WriteString(ogoNonzero)
	}
	for _, el := range sortedKeys(e.appendElems) {
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s v) {\n"+
			"\tif (s.len >= s.cap) {\n\t\togo_panic(\"append: out of capacity\");\n\t} else {\n"+
			"\t\ts.ptr[s.len] = v;\n\t\ts.len++;\n\t}\n\treturn s;\n}\n",
			sliceCName(el), appendCName(el), sliceCName(el), el)
	}
	for _, el := range sortedKeys(e.tryappendElems) {
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s v) {\n\t%s r;\n"+
			"\tif (s.len >= s.cap) {\n\t\tr.slice = s;\n\t\tr.ok = 0;\n\t} else {\n"+
			"\t\ts.ptr[s.len] = v;\n\t\ts.len++;\n\t\tr.slice = s;\n\t\tr.ok = 1;\n\t}\n\treturn r;\n}\n",
			appendokCName(el), tryappendCName(el), sliceCName(el), el, appendokCName(el))
	}
	// The per-element slice printers render "[e0 e1 ...]"; the newline form defers
	// to the plain one and adds a trailing '\n'. They reference the slice typedef
	// emitted above and <stdio.h> (pulled in wherever a print is emitted).
	for _, el := range sortedKeys(e.printSliceElems) {
		fmt.Fprintf(&helperDefs, "static void %s(%s s) {\n"+
			"\tprintf(\"[\");\n"+
			"\tfor (int _i = 0; _i < s.len; _i++) {\n"+
			"\t\tif (_i) printf(\" \");\n"+
			"\t\tprintf(\"%%d\", s.ptr[_i]);\n"+
			"\t}\n"+
			"\tprintf(\"]\");\n}\n"+
			"static void %s(%s s) { %s(s); printf(\"\\n\"); }\n",
			printSliceCName(el), sliceCName(el),
			printlnSliceCName(el), sliceCName(el), printSliceCName(el))
	}
	if helperDefs.Len() != 0 {
		out.Write(helperDefs.Bytes())
		out.WriteByte('\n')
	}
	if globals.Len() != 0 {
		out.Write(globals.Bytes())
		out.WriteByte('\n')
	}
	if protos.Len() != 0 {
		out.Write(protos.Bytes())
		out.WriteByte('\n')
	}
	out.Write(body.Bytes())
	_, err := w.Write(out.Bytes())
	return err
}

type emitter struct {
	w               io.Writer // body buffer during the walk
	f               *File     // file currently being emitted, for token access
	indent          int
	includes        map[string]bool
	funcRet         map[string][]string      // user function / mangled method name -> C result types (empty=void), for typing calls
	methodPtr       map[string]bool          // mangled method name -> receiver is a pointer, for &/* adjustment at the call site
	globals         map[string]string        // package-level constant/variable name -> C type, for typing `x := g`
	structs         map[string][]structField // struct type name -> its fields, for typedefs, zero-init and field typing
	namedTypes      map[string]bool          // non-struct named type (e.g. `type Celsius int`) -> emitted as a typedef; may carry methods
	constInt        map[string]string        // integer-constant name -> its C literal value, for array bounds
	arrays          map[string]arrDim        // local array name -> element type and bound (reset per function)
	globalArrays    map[string]arrDim        // package-level array name -> element type and bound (persists across functions)
	sliceVars       map[string]string        // local slice name -> element C type, for `xs[i]` / len(xs) (reset per function)
	globalSliceVars map[string]string        // package-level slice name -> element C type (persists across functions)
	sliceElems      map[string]bool          // element C types that need an ogo_slice_<T> typedef
	sliceElemByName map[string]string        // ogo_slice_<T> C type name -> its element C type; the forward direction mangles pointers, so the reverse is recorded, not derived
	inlineSliceDefs map[string]bool          // struct element C types whose slice typedef was already emitted inline, between the element struct and the struct field that holds it
	appendElems     map[string]bool          // element C types needing the trapping ogo_append_<T> helper
	tryappendElems  map[string]bool          // element C types needing the ok-form ogo_tryappend_<T> helper + ogo_appendok_<T>
	printSliceElems map[string]bool          // element C types needing the ogo_print_slice_<T> / ogo_println_slice_<T> helpers
	defers          []deferredCall           // the current function's top-level defers, in source order, replayed LIFO before each return
	deferBlockDepth int                      // nesting inside if/for/switch bodies; a defer at depth > 0 is not modelled yet
	usesPanic       bool                     // ogo_panic is called: emit its definition and pull in its includes
	usesBound       bool                     // ogo_bound is called: emit the index bounds-check helper
	usesNonzero     bool                     // ogo_nonzero is called: emit the divide-by-zero-check helper
	release         bool                     // release build: a panic reboots (_reboot) instead of halting the cog
	checks          bool                     // emit runtime bounds / divide-by-zero checks (set by Checked; ogo build enables it by default)
	locals          map[string]string        // current function's parameter/local name -> C type, for typing `x := y`
	curFunc         string                   // name of the function whose body is being emitted (for its result-struct type)
	tmp             int                      // per-function counter for generated temporaries (destructuring)
	makeN           int                      // translation-unit counter for make() backing arrays
	wroteDecl       bool                     // a top-level definition has been emitted (drives blank-line separators)
	mainRet         bool                     // currently emitting main's body: a bare `return` yields `return 0;`
	declInit        bool                     // emitting a static initializer: a string literal must use a brace, not a compound literal
	usesString      bool                     // an ogo_string type/literal appears: emit stringTypedef
	usesStringPrint bool                     // a string is printed: emit stringHelpers
	err             error
}

// emit writes verbatim C text, latching the first write error. All C is written
// through emit so no source text is ever interpreted as a printf verb.
func (e *emitter) emit(s string) {
	if e.err != nil {
		return
	}
	_, e.err = io.WriteString(e.w, s)
}

// fail latches the first "not yet implemented / unsupported" emit error.
func (e *emitter) fail(format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf("emit: "+format, args...)
	}
}

func (e *emitter) ind() {
	for i := 0; i < e.indent; i++ {
		e.emit("\t")
	}
}

// src returns a terminal token's source text.
func (e *emitter) src(tok int32) string { return e.f.tok(tok).Src() }

func (e *emitter) emitFileDecls(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			switch c.sym {
			case ImportDecl:
				e.addImportIncludes(c.ast)
			case TopLevelDecl:
				e.emitTopLevelDecl(c.ast)
			case 0:
				// SEMICOLON / EOF separators.
			default:
				e.fail("unsupported source-file element %v", c.sym)
			}
		}
	}
}

func (e *emitter) addImportIncludes(ast []int32) {
	for n := range it(ast) {
		if n.sym != ImportSpec {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != 0 || e.f.ch(c.tok) != STRING {
				continue
			}
			path, err := strconv.Unquote(e.src(c.tok))
			if err != nil {
				continue
			}
			if inc, ok := importIncludes[path]; ok {
				e.includes[inc] = true
			} else {
				e.fail("no C header mapping for import %q", path)
			}
		}
	}
}

func (e *emitter) emitTopLevelDecl(ast []int32) {
	for n := range it(ast) {
		switch n.sym {
		case FuncDecl:
			e.emitFuncDecl(n.ast)
		case ConstDecl:
			// Package-level constants are emitted in an earlier pass
			// (emitPackageConsts), before the functions that reference them.
		case VarDecl:
			// Package-level variables are emitted in an earlier pass
			// (emitPackageVars).
		case TypeDecl:
			// Struct typedefs are emitted in an earlier pass (collectStructs).
		default:
			e.fail("unsupported top-level declaration %v", n.sym)
		}
	}
}

// structField is one field of a struct type: its name and C type, in declaration
// order.
// structField is one field of a struct typedef. bound is empty for a plain field;
// when set the field is a fixed-size array, declared `ctype name[bound]` -- C puts
// the extent on the declarator, not the type, so the two cannot simply be
// concatenated the way every other field is.
type structField struct{ name, ctype, bound string }

// arrDim describes an array variable: its element C type and its C bound (for
// element typing and len).
type arrDim struct{ elem, bound string }

// deferredCall is a recorded `defer` statement: the call's head (AssignHead) and
// its suffix (Selector / Index / CallSuffix), replayed through emitCall before the
// function returns.
type deferredCall struct {
	head   Node
	suffix []Node
}

// collectStructs records each package-level struct type's fields in the struct
// environment and emits a C typedef -- `typedef struct { <t0> f0; ... } T;`.
// Only structs with explicitly-typed, non-embedded fields are modelled.
func (e *emitter) collectStructs(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym == TypeDecl {
					e.collectTypeDecl(d.ast)
				}
			}
		}
	}
}

// collectTypeDecl records a type declaration (single or grouped) and emits its
// typedef: a `type Name struct { ... }` as `typedef struct { ... } Name;`, and a
// non-struct named type `type Name <underlying>` (e.g. `type Celsius int`) as
// `typedef <underlying> Name;`. The named type may then back variables and carry
// methods. An underlying type outside the modelled subset fails honestly.
func (e *emitter) collectTypeDecl(ast []int32) {
	for n := range it(ast) {
		if n.sym != TypeSpec {
			continue
		}
		var name string
		var typeAST []int32
		for s := range it(n.ast) {
			switch s.sym {
			case 0:
				if e.f.ch(s.tok) == IDENT && name == "" {
					name = e.src(s.tok)
				}
			case Type:
				typeAST = s.ast
			}
		}
		if name == "" || typeAST == nil {
			e.fail("malformed type declaration")
			return
		}
		if structAST := e.structTypeAST(typeAST); structAST != nil {
			fields := e.structFieldsOf(structAST)
			e.structs[name] = fields
			e.emit("typedef struct {")
			for _, fld := range fields {
				if fld.bound != "" {
					e.emit(" " + fld.ctype + " " + fld.name + "[" + fld.bound + "];")
					continue
				}
				e.emit(" " + fld.ctype + " " + fld.name + ";")
			}
			e.emit(" } " + name + ";\n")
			continue
		}
		// A non-struct named type: `type Celsius int` -> `typedef int Celsius;`. The
		// underlying must be a modelled scalar (or other cType-resolvable) type.
		underlying := e.cType(typeAST)
		if underlying == "" {
			return // cType has latched the failure
		}
		e.namedTypes[name] = true
		e.emit("typedef " + underlying + " " + name + ";\n")
	}
}

// structTypeAST returns the StructType node's children within a Type subtree, or
// nil if the type is not a struct.
func (e *emitter) structTypeAST(typeAST []int32) []int32 {
	for n := range it(typeAST) {
		if n.sym == StructType {
			return n.ast
		}
	}
	return nil
}

// structFieldsOf reads a StructType's FieldDecls into ordered fields. A field
// group `x, y int` yields one field per name. An embedded or untyped field fails.
func (e *emitter) structFieldsOf(structAST []int32) []structField {
	var out []structField
	for n := range it(structAST) {
		if n.sym != FieldDecl {
			continue
		}
		var names []string
		ctype := ""
		bound := "" // non-empty for a fixed-size array field
		for c := range it(n.ast) {
			switch c.sym {
			case Type:
				if elem, ok := e.sliceType(c.ast); ok {
					// A scalar-element slice field (`data []int`) is a slice header
					// whose typedef is emitted before this struct's (see EmitC).
					//
					// A struct-element slice field (`pts []Point`) inverts that: its
					// header names Point, so it must follow Point's typedef, yet it is
					// held by value here and so must precede this struct's. Emit it
					// inline -- collectTypeDecl calls us before emitting this struct's
					// typedef, and both write the same buffer, so it lands exactly
					// between the two. Recorded so EmitC's typedef section skips it.
					//
					// A forward reference (`[]B` with B declared later) cannot reach
					// here: sliceType resolves the element through cType, which fails
					// on an unknown name. Declaration order is the rule for plain
					// struct fields too.
					if e.isStruct(elem) && !e.inlineSliceDefs[elem] {
						e.inlineSliceDefs[elem] = true
						e.emit(sliceTypedefDef(elem))
					}
					e.needSlice(elem)
					ctype = sliceCName(elem)
					break
				}
				// A fixed-size array field (`data [3]int`, `pts [3]Point`). Unlike a
				// slice it needs no header typedef -- the storage is inline -- but C
				// puts the extent on the declarator, so the bound travels beside the
				// element type and is applied when the typedef is written.
				if el, bnd, ok := e.arrayType(c.ast); ok {
					ctype, bound = el, bnd
					break
				}
				ctype = e.cType(c.ast)
			case 0:
				if e.f.ch(c.tok) == IDENT {
					names = append(names, e.src(c.tok))
				}
			}
		}
		if ctype == "" || len(names) == 0 {
			e.fail("embedded or untyped struct fields are not supported yet")
			return out
		}
		for _, nm := range names {
			out = append(out, structField{name: nm, ctype: ctype, bound: bound})
		}
	}
	return out
}

// emitPackageConsts emits the file's package-level constant declarations as C
// file-scope `static const` definitions and records each in the global type
// environment.
func (e *emitter) emitPackageConsts(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym == ConstDecl {
					e.emitConstDecl(d.ast, true)
				}
			}
		}
	}
}

// emitPackageVars emits the file's package-level variable declarations as C
// file-scope `static` definitions and records each in the global type environment.
func (e *emitter) emitPackageVars(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym == VarDecl {
					e.emitPackageVarDecl(d.ast)
				}
			}
		}
	}
}

// emitPackageVarDecl emits a package-level variable declaration (one VarSpec or a
// parenthesized group). Each variable is a file-scope `static`, so an uninitialized
// one is zeroed by C and needs no initializer; an array is a plain C array; an
// initializer must be a constant expression (emitGlobalInit). A blank name and an
// inferred (typeless) variable are not modelled.
func (e *emitter) emitPackageVarDecl(ast []int32) {
	for n := range it(ast) {
		if n.sym != VarSpec {
			continue
		}
		var names []string
		var typeAST, initExpr []int32
		for s := range it(n.ast) {
			switch s.sym {
			case IdentifierList:
				for id := range it(s.ast) {
					if id.sym == 0 && e.f.ch(id.tok) == IDENT {
						names = append(names, e.src(id.tok))
					}
				}
			case Type:
				typeAST = s.ast
			case Expression:
				initExpr = s.ast
			case 0:
				// The "=" separator.
			default:
				e.fail("unsupported var-spec element %v", s.sym)
			}
		}
		if typeAST == nil {
			// Type-inferred package variable `var x = expr`. C requires a constant
			// initializer at file scope (emitGlobalInit), so a single named variable
			// with an inferable type is modelled; a make/slice initializer still needs
			// an explicit type and fails honestly through inference.
			if len(names) != 1 {
				e.fail("a type-inferred package variable must be a single name (var x = expr)")
				return
			}
			if names[0] == "_" {
				continue // a blank package variable declares nothing
			}
			ct, ok := e.inferCType(initExpr)
			if !ok {
				e.fail("cannot infer a type for the package variable %q", names[0])
				return
			}
			e.globals[names[0]] = ct
			if e.isSliceCType(ct) {
				e.globalSliceVars[names[0]] = sliceElemFromCName(ct)
			}
			e.emit("static " + ct + " " + names[0] + " = ")
			e.emitGlobalInit(initExpr)
			e.emit(";\n")
			continue
		}
		if len(names) != 1 && initExpr != nil {
			e.fail("multi-name package variable with an initializer is not supported yet")
			return
		}
		// A package-level fixed array `var a [N]T` -> `static T a[N];`.
		if elem, bound, ok := e.arrayType(typeAST); ok {
			if initExpr != nil {
				e.fail("array variable initializers are not supported yet")
				return
			}
			for _, nm := range names {
				if nm != "_" {
					e.globalArrays[nm] = arrDim{elem, bound}
					e.emit("static " + elem + " " + nm + "[" + bound + "];\n")
				}
			}
			continue
		}
		// A package-level slice `var xs []T` -> `static ogo_slice_T xs;` (BSS-zeroed
		// to {NULL, 0}); its element type is recorded for `xs[i]` / len(xs) in bodies.
		if elem, ok := e.sliceType(typeAST); ok {
			e.needSlice(elem)
			cname := sliceCName(elem)
			if initExpr != nil {
				me, lenAST, capAST, ok := e.makeSliceInit(initExpr)
				if !ok {
					e.fail("a package slice initializer must be make([]T, ...)")
					return
				}
				if me != elem {
					e.fail("make element type %q does not match the declared slice element type %q", me, elem)
					return
				}
				if len(names) != 1 || names[0] == "_" {
					e.fail("a make slice initializer needs a single named variable")
					return
				}
				e.globalSliceVars[names[0]] = elem
				e.globals[names[0]] = cname
				e.emitMakeSliceVar(names[0], cname, elem, lenAST, capAST, true)
				continue
			}
			for _, nm := range names {
				if nm != "_" {
					e.globalSliceVars[nm] = elem
					e.globals[nm] = cname
					e.emit("static " + cname + " " + nm + ";\n")
				}
			}
			continue
		}
		ctype := e.cType(typeAST)
		if ctype == "" {
			return
		}
		for _, nm := range names {
			if nm == "_" {
				continue // a blank package variable declares nothing
			}
			e.globals[nm] = ctype
			e.emit("static " + ctype + " " + nm)
			if initExpr != nil {
				e.emit(" = ")
				e.emitGlobalInit(initExpr)
			}
			e.emit(";\n")
		}
	}
}

// emitGlobalInit emits a package variable's initializer, which C requires to be a
// constant expression. A bare integer-constant reference is folded to its value
// (flexcc rejects a `static const` in a global initializer); a string or struct
// literal uses the brace form (declInit).
func (e *emitter) emitGlobalInit(initExpr []int32) {
	if tok, ok := e.soleToken(initExpr); ok && e.f.ch(tok) == IDENT {
		if v, ok := e.constInt[e.src(tok)]; ok {
			e.emit(v)
			return
		}
	}
	e.declInit = true
	e.emitExpr(initExpr)
	e.declInit = false
}

// emitConstDecl emits a constant declaration -- one ConstSpec or a parenthesized
// group -- as C const definitions. A package-level constant becomes a file-scope
// `static const`; a local one a block-scope `const`. An untyped constant's C type
// is inferred from its initializer, defaulting to int. The name is recorded in the
// global or local type environment so its later uses can be typed.
func (e *emitter) emitConstDecl(ast []int32, pkg bool) {
	for n := range it(ast) {
		if n.sym != ConstSpec {
			continue
		}
		var name, ctype string
		var initExpr []int32
		for s := range it(n.ast) {
			switch s.sym {
			case Type:
				ctype = e.cType(s.ast)
			case Expression:
				initExpr = s.ast
			case 0:
				if e.f.ch(s.tok) == IDENT {
					name = e.src(s.tok)
				}
			}
		}
		if name == "" || initExpr == nil {
			e.fail("malformed const declaration")
			return
		}
		if ctype == "" {
			ct, ok := e.inferCType(initExpr)
			if !ok {
				ct = "int" // an untyped constant defaults to int
			}
			ctype = ct
		}
		if pkg {
			e.globals[name] = ctype
		} else {
			e.locals[name] = ctype
		}
		// A constant that is a single integer literal can serve as an array bound
		// (flexcc rejects a `static const` there); record its value.
		if tok, ok := e.soleToken(initExpr); ok && e.f.ch(tok) == INT {
			e.constInt[name] = normalizeIntLit(e.src(tok))
		}
		e.ind()
		if pkg {
			// A file-scope constant has static storage, so a string initializer
			// must be a brace, not a compound literal (see emitStringLit).
			e.emit("static const " + ctype + " " + name + " = ")
			e.declInit = true
			e.emitExpr(initExpr)
			e.declInit = false
		} else {
			e.emit("const " + ctype + " " + name + " = ")
			e.emitExpr(initExpr)
		}
		e.emit(";\n")
	}
}

// collectResults records every user function's C result types in funcRet and,
// for a function with more than one result, emits a result-struct typedef —
// `typedef struct { <t0> _0; <t1> _1; ... } ogo_ret_<name>;` — that its C
// signature returns in place of C's absent multiple-return.
// collectResults records every user function's and method's C result types in
// funcRet (keyed by the plain name for a function, the mangled `<T>_<method>` for a
// method) and, for a multi-result callee, emits its result-struct typedef. A
// method's receiver pointer-ness is recorded in methodPtr for the call site.
func (e *emitter) collectResults(ast []int32) {
	e.eachFuncDeclAST(ast, func(d []int32) {
		name, sig, _, recv, ok := e.funcParts(d)
		if !ok || name == "" {
			return
		}
		cname := name
		if recv != nil {
			_, rct := e.receiverInfo(recv)
			cname = methodCName(methodBaseType(rct), name)
			e.methodPtr[cname] = e.isPointer(rct)
		}
		_, resTypes := e.resultInfo(sig)
		e.funcRet[cname] = resTypes
		if len(resTypes) > 1 {
			e.emit("typedef struct { ")
			for i, ct := range resTypes {
				fmt.Fprintf(e.w, "%s _%d; ", ct, i)
			}
			e.emit("} " + e.retStructName(cname) + ";\n")
		}
	})
}

// eachFuncDeclAST calls fn with the AST of each FuncDecl (function or method) in a
// file's AST.
func (e *emitter) eachFuncDeclAST(ast []int32, fn func(d []int32)) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym == FuncDecl {
					fn(d.ast)
				}
			}
		}
	}
}

// retStructName is the C typedef name of a multi-result function's result struct.
func (e *emitter) retStructName(fn string) string { return "ogo_ret_" + fn }

// emitPrototypes emits a forward prototype for every user function and method in a
// file (all but main, which C declares implicitly). Run before the definitions so a
// call need not follow its callee's definition.
func (e *emitter) emitPrototypes(ast []int32) {
	e.eachFuncDeclAST(ast, func(d []int32) {
		name, sig, _, recv, ok := e.funcParts(d)
		if !ok || name == "" || (recv == nil && name == "main") {
			return
		}
		var proto string
		if recv == nil {
			proto = e.funcSignatureC(name, sig)
		} else {
			rn, rct := e.receiverInfo(recv)
			proto = e.methodSignatureC(methodCName(methodBaseType(rct), name), rn, rct, sig)
		}
		if proto != "" {
			e.emit(proto + ";\n")
		}
	})
}

func (e *emitter) emitFuncDecl(ast []int32) {
	name, sig, body, recv, ok := e.funcParts(ast)
	if !ok {
		return
	}
	if body == nil {
		e.fail("func %q must have a body", name)
		return
	}

	if e.wroteDecl {
		e.emit("\n")
	}
	e.wroteDecl = true

	if recv == nil && name == "main" {
		e.emitMain(sig, body)
		return
	}
	e.locals = map[string]string{}
	e.arrays = map[string]arrDim{}
	e.sliceVars = map[string]string{}
	e.tmp = 0
	e.defers = nil
	// A method is a function with a mangled name and its receiver as the first
	// parameter, bound in the local environment so the body reads it like any local
	// (a pointer receiver's field access is then `->`, exactly as for a `*T` param).
	var proto string
	if recv == nil {
		proto = e.funcSignatureC(name, sig)
		e.curFunc = name
	} else {
		recvName, recvCType := e.receiverInfo(recv)
		cname := methodCName(methodBaseType(recvCType), name)
		proto = e.methodSignatureC(cname, recvName, recvCType, sig)
		e.curFunc = cname
		e.locals[recvName] = recvCType
	}
	if proto == "" {
		return
	}
	e.bindParams(sig)
	e.emit(proto + " {\n")
	e.indent++
	e.emitParamCopies(sig)
	e.declareNamedResults(sig)
	e.emitBlockStmts(body)
	// A body that falls off the end (no trailing return) runs its deferred calls
	// here; one ending in a return already replayed them at that return.
	if len(e.defers) != 0 && !e.bodyEndsInReturn(body) {
		e.emitDeferred()
	}
	e.indent--
	e.emit("}\n")
}

// bodyEndsInReturn reports whether a function body's last top-level statement is a
// return, so deferred calls need not be replayed again at the closing brace.
func (e *emitter) bodyEndsInReturn(body []int32) bool {
	var lastAST []int32
	for n := range it(body) {
		if n.sym == Statement {
			lastAST = n.ast
		}
	}
	nodes := slices.Collect(it(lastAST))
	return len(nodes) != 0 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == RETURN
}

// declareNamedResults declares a function's named result parameters as zero-
// initialized locals (P2 stack locals are not auto-zeroed) and binds them in the
// local type environment, so the body may assign and read them like Go's named
// results. Unnamed and blank results declare nothing.
func (e *emitter) declareNamedResults(sig []int32) {
	names, types := e.resultInfo(sig)
	for i, nm := range names {
		if nm == "" || nm == "_" {
			continue
		}
		e.locals[nm] = types[i]
		e.ind()
		e.emit(types[i] + " " + nm + " = 0;\n")
	}
}

// funcParts pulls the name, signature subtree, body subtree and receiver subtree
// from a FuncDecl AST. recv is non-nil for a method (a Receiver is present); body
// is nil for a bodyless declaration. ok is false only if the walk hit an
// unexpected element.
func (e *emitter) funcParts(ast []int32) (name string, sig, body, recv []int32, ok bool) {
	ok = true
	for n := range it(ast) {
		switch n.sym {
		case 0:
			if e.f.ch(n.tok) == IDENT {
				name = e.src(n.tok)
			}
		case Receiver:
			recv = n.ast
		case Signature:
			sig = n.ast
		case Block:
			body = n.ast
		default:
			e.fail("unsupported in function declaration: %v", n.sym)
			ok = false
		}
	}
	return name, sig, body, recv, ok
}

// receiverInfo parses a Receiver subtree `"(" identifier Type ")"`, returning the
// receiver's name and its C type (e.g. "Point" or "Point*").
func (e *emitter) receiverInfo(recv []int32) (name, ctype string) {
	for n := range it(recv) {
		switch {
		case n.sym == 0 && e.f.ch(n.tok) == IDENT:
			name = e.src(n.tok)
		case n.sym == Type:
			ctype = e.cType(n.ast)
		}
	}
	return name, ctype
}

// methodBaseType is a receiver C type without any pointer star: a type's value and
// pointer methods share this base, so `T` and `*T` methods live in one C namespace,
// as the checker requires (a value/pointer method-name collision is an error).
func methodBaseType(recvCType string) string { return strings.TrimSuffix(recvCType, "*") }

// methodCName mangles a method to its C function name `<BaseType>_<method>`.
func methodCName(baseType, method string) string { return baseType + "_" + method }

// emitMain emits `func main()` as `int main(void)`; main takes no parameters or
// results.
func (e *emitter) emitMain(sig, body []int32) {
	params, resTypes := e.cSig(sig)
	if params != "" || len(resTypes) != 0 {
		e.fail("func main must have no parameters or results")
		return
	}
	e.locals = map[string]string{}
	e.arrays = map[string]arrDim{}
	e.sliceVars = map[string]string{}
	e.tmp = 0
	e.defers = nil
	e.curFunc = "main"
	e.emit("int main(void) {\n")
	e.indent++
	e.mainRet = true
	e.emitBlockStmts(body)
	e.mainRet = false
	if len(e.defers) != 0 && !e.bodyEndsInReturn(body) {
		e.emitDeferred()
	}
	e.ind()
	e.emit("return 0;\n")
	e.indent--
	e.emit("}\n")
}

// funcSignatureC builds the C signature `<ret> name(params)` for a user function,
// e.g. `int add(int a, int b)`, `void run(void)`, or -- for more than one result
// -- `ogo_ret_divmod divmod(int a, int b)`.
func (e *emitter) funcSignatureC(name string, sig []int32) string {
	params, resTypes := e.cSig(sig)
	if params == "" {
		params = "void"
	}
	return e.cReturnType(name, resTypes) + " " + name + "(" + params + ")"
}

// methodSignatureC builds a method's C signature with the receiver as the leading
// parameter, e.g. `int Point_Sum(Point p)` or `void Point_Scale(Point* p, int f)`.
func (e *emitter) methodSignatureC(cname, recvName, recvCType string, sig []int32) string {
	params, resTypes := e.cSig(sig)
	recvParam := recvCType + " " + recvName
	if params == "" {
		params = recvParam
	} else {
		params = recvParam + ", " + params
	}
	return e.cReturnType(cname, resTypes) + " " + cname + "(" + params + ")"
}

// cReturnType is a function's C return type: void for no results, the type itself
// for one, and the result struct for more than one (C has no multiple return).
func (e *emitter) cReturnType(name string, resTypes []string) string {
	switch len(resTypes) {
	case 0:
		return "void"
	case 1:
		return resTypes[0]
	default:
		return e.retStructName(name)
	}
}

// cSig renders a Signature's parameters as a C parameter list ("int a, int b")
// and returns its result C types (empty for none). Parameters are always named
// (the grammar requires it); results are a single unnamed type or, for one or
// more, a named parameter list.
func (e *emitter) cSig(sig []int32) (params string, resTypes []string) {
	var parts []string
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if seenRPar {
				e.forEachParam(n.ast, func(_ string, ta []int32) { resTypes = append(resTypes, e.cType(ta)) })
			} else {
				parts = e.cParamList(n.ast)
			}
		case Type:
			// A single unnamed result: Signature = "(" [...] ")" Type .
			resTypes = append(resTypes, e.cType(n.ast))
		case 0:
			if e.f.ch(n.tok) == RPAREN {
				seenRPar = true
			}
		default:
			e.fail("unsupported signature element %v", n.sym)
		}
	}
	return strings.Join(parts, ", "), resTypes
}

// resultInfo returns a function's result names and C types (one entry per result
// value). An unnamed single result has an empty name; a multi-result signature is
// always named (the grammar requires it).
func (e *emitter) resultInfo(sig []int32) (names, types []string) {
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if seenRPar {
				e.forEachParam(n.ast, func(nm string, ta []int32) {
					names = append(names, nm)
					types = append(types, e.cType(ta))
				})
			}
		case Type:
			if seenRPar {
				names = append(names, "")
				types = append(types, e.cType(n.ast))
			}
		case 0:
			if e.f.ch(n.tok) == RPAREN {
				seenRPar = true
			}
		}
	}
	return names, types
}

// cParamList renders one ParameterList's `IdentifierList Type` groups to C
// parameters, expanding a shared type ("a, b int" -> "int a, int b"). A fixed-
// array parameter is received by pointer (Go passes arrays by value, but C cannot,
// and flexcc cannot wrap them in a struct either): the C parameter is
// `<elem>* _ogo_<name>`, and the function copies it into a same-named local on
// entry (see emitParamCopies) to restore the value semantics.
func (e *emitter) cParamList(ast []int32) []string {
	var out []string
	e.forEachParam(ast, func(name string, ta []int32) {
		if elem, _, ok := e.arrayType(ta); ok {
			out = append(out, elem+"* "+paramArgName(name))
			return
		}
		out = append(out, e.cType(ta)+" "+name)
	})
	return out
}

// forEachParam walks a ParameterList's `IdentifierList Type` groups, calling fn
// with each parameter's name and C type (a shared type "a, b int" yields two
// calls). It underlies both the C parameter rendering (cParamList) and the local
// type environment (bindParams).
func (e *emitter) forEachParam(ast []int32, fn func(name string, typeAST []int32)) {
	var names []string
	for n := range it(ast) {
		switch n.sym {
		case IdentifierList:
			names = names[:0]
			for id := range it(n.ast) {
				if id.sym == 0 && e.f.ch(id.tok) == IDENT {
					names = append(names, e.src(id.tok))
				}
			}
		case Type:
			for _, nm := range names {
				fn(nm, n.ast)
			}
			names = names[:0]
		}
	}
}

// paramArgName is the C name of a value-array parameter as it is received (a
// pointer), distinct from the local copy the body sees under the source name.
func paramArgName(name string) string { return "_ogo_" + name }

// bindParams records the current function's parameters in the local type
// environment, so a `x := p` short declaration can be typed from a parameter p. A
// fixed-array parameter is recorded as an array (its body sees a same-named local
// copy). It reads only the parameter list (before the signature's closing ")"),
// not the results.
func (e *emitter) bindParams(sig []int32) {
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if !seenRPar {
				e.forEachParam(n.ast, func(name string, ta []int32) {
					if elem, bound, ok := e.arrayType(ta); ok {
						e.arrays[name] = arrDim{elem, bound}
						return
					}
					if elem, ok := e.sliceType(ta); ok {
						e.sliceVars[name] = elem
					}
					e.locals[name] = e.cType(ta)
				})
			}
		case 0:
			if e.f.ch(n.tok) == RPAREN {
				seenRPar = true
			}
		}
	}
}

// emitParamCopies emits, at a function's entry, a local copy of each fixed-array
// parameter — `<elem> <name>[<N>]; memcpy(<name>, _ogo_<name>, sizeof(<name>));` —
// so the body mutates a copy and the caller's array is untouched (Go's array
// value semantics). The parameter itself arrives by pointer (see cParamList).
func (e *emitter) emitParamCopies(sig []int32) {
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if !seenRPar {
				e.forEachParam(n.ast, func(name string, ta []int32) {
					if elem, bound, ok := e.arrayType(ta); ok {
						e.includes["string.h"] = true
						e.ind()
						e.emit(elem + " " + name + "[" + bound + "];\n")
						e.ind()
						e.emit("memcpy(" + name + ", " + paramArgName(name) + ", sizeof(" + name + "));\n")
					}
				})
			}
		case 0:
			if e.f.ch(n.tok) == RPAREN {
				seenRPar = true
			}
		}
	}
}

// emitBlockStmts emits the statements of a Block (skipping its braces).
func (e *emitter) emitBlockStmts(ast []int32) {
	for n := range it(ast) {
		switch n.sym {
		case 0:
			// LBRACE / RBRACE / SEMICOLON.
		case Statement:
			e.emitStatement(n.ast)
		default:
			e.fail("unsupported block element %v", n.sym)
		}
	}
}

func (e *emitter) emitStatement(ast []int32) {
	nodes := slices.Collect(it(ast))
	if len(nodes) == 0 {
		return // EmptyStatement
	}
	switch first := nodes[0]; {
	case first.sym == VarDecl:
		e.emitVarDecl(first.ast)
	case first.sym == ConstDecl:
		e.emitConstDecl(first.ast, false)
	case first.sym == 0 && e.f.ch(first.tok) == FOR:
		e.emitFor(nodes)
	case first.sym == IfStmt:
		e.emitIf(first.ast)
	case first.sym == SwitchStmt:
		e.emitSwitch(first.ast)
	case first.sym == 0 && e.f.ch(first.tok) == RETURN:
		e.emitReturn(nodes)
	case first.sym == 0 && e.f.ch(first.tok) == DEFER:
		e.emitDefer(nodes)
	case first.sym == AssignHead:
		e.emitAssignHeadStmt(nodes)
	case first.sym == 0:
		e.fail("%v statement is not supported yet", e.f.ch(first.tok))
	default:
		e.fail("statement %v is not supported yet", first.sym)
	}
}

// emitVarDecl handles `var name Type` (zero-initialized; P2 stack locals are not
// auto-zeroed, so the initializer is required), `var name Type = expr`, and fixed
// arrays `var a [N]T`.
func (e *emitter) emitVarDecl(ast []int32) {
	for n := range it(ast) {
		if n.sym != VarSpec {
			continue
		}
		var names []string
		var typeAST, initExpr []int32
		for s := range it(n.ast) {
			switch s.sym {
			case IdentifierList:
				for id := range it(s.ast) {
					if id.sym == 0 && e.f.ch(id.tok) == IDENT {
						names = append(names, e.src(id.tok))
					}
				}
			case Type:
				typeAST = s.ast
			case Expression:
				initExpr = s.ast
			case 0:
				// The "=" separator between the type and the initializer.
			default:
				e.fail("unsupported var-spec element %v", s.sym)
			}
		}
		if typeAST == nil {
			// Type-inferred `var x = expr` (the var form of `x := expr`) or
			// `var a, b = f()` (destructuring). The grammar guarantees an initializer
			// when the type is omitted.
			if initExpr == nil {
				e.fail("var declaration needs a type or an initializer")
				return
			}
			if len(names) != 1 {
				e.emitDestructure(names, true, initExpr)
				continue
			}
			if names[0] == "_" {
				e.emitDiscard(initExpr)
				continue
			}
			e.emitInferredLocal(names[0], initExpr)
			continue
		}
		// A fixed array `var a [N]T` -> `T a[N] = {0};`. Its name maps to the
		// element type for `x := a[i]` typing. An initializer copies another array of
		// the same type by value (`var b [N]T = a`); C cannot assign arrays, so the
		// array is declared uninitialized and filled with memcpy.
		if elem, bound, ok := e.arrayType(typeAST); ok {
			if len(names) != 1 && initExpr != nil {
				e.fail("multi-name var with initializer is not supported yet")
				return
			}
			for _, nm := range names {
				if nm == "_" {
					if initExpr != nil {
						e.emitDiscard(initExpr)
					}
					continue
				}
				e.arrays[nm] = arrDim{elem, bound}
				if initExpr == nil {
					e.ind()
					e.emit(elem + " " + nm + "[" + bound + "] = {0};\n")
					continue
				}
				e.includes["string.h"] = true
				e.ind()
				e.emit(elem + " " + nm + "[" + bound + "];\n")
				e.ind()
				e.emit("memcpy(" + nm + ", ")
				e.emitExpr(initExpr)
				e.emit(", sizeof(" + nm + "));\n")
			}
			continue
		}
		// A slice `var xs []T` -> `ogo_slice_T xs = {0};` (a { pointer, length }
		// header, zero value {NULL, 0}); its name maps to the element type for `xs[i]`
		// and len(xs). An initializer is a plain slice-header value copy.
		if elem, ok := e.sliceType(typeAST); ok {
			if len(names) != 1 && initExpr != nil {
				e.fail("multi-name var with initializer is not supported yet")
				return
			}
			cname := sliceCName(elem)
			e.needSlice(elem)
			// A make([]T, ...) initializer synthesises a backing array + header.
			if initExpr != nil && len(names) == 1 && names[0] != "_" {
				if me, lenAST, capAST, ok := e.makeSliceInit(initExpr); ok {
					if me != elem {
						e.fail("make element type %q does not match the declared slice element type %q", me, elem)
						return
					}
					e.sliceVars[names[0]] = elem
					e.locals[names[0]] = cname
					e.emitMakeSliceVar(names[0], cname, elem, lenAST, capAST, false)
					continue
				}
			}
			for _, nm := range names {
				if nm == "_" {
					if initExpr != nil {
						e.emitDiscard(initExpr)
					}
					continue
				}
				e.sliceVars[nm] = elem
				e.locals[nm] = cname
				e.ind()
				e.emit(cname + " " + nm + " = ")
				if initExpr != nil {
					e.emitExpr(initExpr)
				} else {
					e.emit("{0}")
				}
				e.emit(";\n")
			}
			continue
		}
		ctype := e.cType(typeAST)
		if ctype == "" {
			return
		}
		if len(names) != 1 && initExpr != nil {
			// A multi-name initializer destructures a multi-result call, declaring
			// each name -- the var form of `a, b := f()`.
			e.emitDestructure(names, true, initExpr)
			continue
		}
		for _, nm := range names {
			if nm == "_" {
				// A blank var declares nothing. With an initializer, its side
				// effects still run and the value is discarded; without one, it
				// emits nothing at all.
				if initExpr != nil {
					e.emitDiscard(initExpr)
				}
				continue
			}
			e.locals[nm] = ctype
			e.ind()
			e.emit(ctype + " " + nm + " = ")
			switch {
			case initExpr != nil:
				e.emitExpr(initExpr)
			case e.isStruct(ctype) || ctype == cString:
				e.emit("{0}") // zero every field (a string's zero is {NULL, 0})
			default:
				e.emit("0")
			}
			e.emit(";\n")
		}
	}
}

// cType maps a Type subtree that names a single predeclared or struct type to its
// C type, recording any needed <stdint.h> include. A struct type's C name is the
// type name itself (its typedef); a pointer type "*T" maps to "<T>*". Other
// composite types (array, slice, channel, ...) are not modelled and fail honestly.
func (e *emitter) cType(ast []int32) string {
	nodes := slices.Collect(it(ast))
	// Pointer type: "*" Type -> "<elem>*".
	if len(nodes) == 2 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == MUL && nodes[1].sym == Type {
		if elem := e.cType(nodes[1].ast); elem != "" {
			return elem + "*"
		}
		return ""
	}
	// Slice type: "[" "]" Type -> the ogo_slice_<elem> header.
	if elem, ok := e.sliceType(ast); ok {
		e.needSlice(elem)
		return sliceCName(elem)
	}
	var toks []int32
	nonTerminal := false
	for _, n := range nodes {
		if n.sym != 0 {
			nonTerminal = true // a StructType body, etc.
			continue
		}
		toks = append(toks, n.tok)
	}
	// A simple named type is exactly one IDENT token; anything else -- a pointer
	// "*T", an array "[N]T", a slice, a channel, a qualified name -- carries extra
	// tokens or nodes and is not modelled yet.
	name := ""
	if len(toks) == 1 && e.f.ch(toks[0]) == IDENT {
		name = e.src(toks[0])
	}
	if nonTerminal || name == "" {
		for _, t := range toks {
			if e.f.ch(t) == IDENT {
				name = e.src(t)
			}
		}
		e.fail("unsupported type %q", name)
		return ""
	}
	if ct, ok := cTypes[name]; ok {
		if strings.HasSuffix(ct, "_t") {
			e.includes["stdint.h"] = true
		}
		if ct == cString {
			e.usesString = true
		}
		return ct
	}
	if _, ok := e.structs[name]; ok {
		return name
	}
	if e.namedTypes[name] {
		return name
	}
	e.fail("unsupported type %q", name)
	return ""
}

// isUserType reports whether a C type name denotes a user-defined type that may
// carry methods -- a struct or a non-struct named type -- as opposed to a
// predeclared type or an imported package qualifier.
func (e *emitter) isUserType(ctype string) bool { return e.isStruct(ctype) || e.namedTypes[ctype] }

// arrayType recognises a fixed-array type `[N]T`, returning the element C type and
// the C bound. A slice `[]T` (no bound) or a non-constant bound is not modelled.
func (e *emitter) arrayType(typeAST []int32) (elem, bound string, ok bool) {
	nodes := slices.Collect(it(typeAST))
	if len(nodes) == 0 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != LBRACK {
		return "", "", false
	}
	var sizeAST, elemAST []int32
	for _, n := range nodes {
		switch n.sym {
		case Expression:
			sizeAST = n.ast
		case Type:
			elemAST = n.ast
		}
	}
	if sizeAST == nil || elemAST == nil {
		return "", "", false // a slice, or a malformed array
	}
	bound, ok = e.arrayBoundC(sizeAST)
	if !ok {
		return "", "", false
	}
	if elem = e.cType(elemAST); elem == "" {
		return "", "", false
	}
	return elem, bound, true
}

// sliceType recognises a slice type `[]T`, returning its element C type. It is a
// bracketed type with no length -- as opposed to a fixed array `[N]T`, which
// carries a size expression (handled by arrayType).
func (e *emitter) sliceType(typeAST []int32) (elem string, ok bool) {
	nodes := slices.Collect(it(typeAST))
	if len(nodes) == 0 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != LBRACK {
		return "", false
	}
	var elemAST []int32
	for _, n := range nodes {
		switch n.sym {
		case Expression:
			return "", false // a sized array, not a slice
		case Type:
			elemAST = n.ast
		}
	}
	if elemAST == nil {
		return "", false
	}
	if elem = e.cType(elemAST); elem == "" {
		return "", false
	}
	return elem, true
}

// arrayBoundC renders a fixed-array bound as a C integer constant: a single
// integer literal directly, or a single integer-constant name folded to its value
// (flexcc rejects a `static const` as an array bound). Anything else is unmodelled.
func (e *emitter) arrayBoundC(sizeAST []int32) (string, bool) {
	tok, ok := e.soleToken(sizeAST)
	if !ok {
		return "", false
	}
	switch e.f.ch(tok) {
	case INT:
		return normalizeIntLit(e.src(tok)), true
	case IDENT:
		if v, ok := e.constInt[e.src(tok)]; ok {
			return v, true
		}
	}
	return "", false
}

// soleToken returns the single terminal token of an expression subtree, if it has
// exactly one (a bare literal or identifier), descending non-terminal wrappers.
func (e *emitter) soleToken(ast []int32) (int32, bool) {
	var tok int32
	count := 0
	var walk func([]int32)
	walk = func(a []int32) {
		for n := range it(a) {
			if n.sym == 0 {
				tok, count = n.tok, count+1
			} else {
				walk(n.ast)
			}
		}
	}
	walk(ast)
	return tok, count == 1
}

// factorIndex recognises a Factor that is a single index `base[i]` -- an
// identifier followed by a FactorSuffix of exactly one Index -- returning the base
// name and the index expression.
func (e *emitter) factorIndex(kids []Node) (base string, indexAST []int32, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	suffix := slices.Collect(it(kids[1].ast))
	if len(suffix) != 1 || suffix[0].sym != Index {
		return "", nil, false
	}
	return e.src(kids[0].tok), suffix[0].ast, true
}

// factorFieldIndex recognises `base.f...[i]` -- a field-access chain followed by a
// single trailing index -- returning the base name, the field chain and the index
// expression. It is the field-base counterpart of factorIndex, letting a slice
// struct field be indexed directly (`b.data[i]`).
func (e *emitter) factorFieldIndex(kids []Node) (base string, fields []string, indexAST []int32, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, nil, false
	}
	suffix := slices.Collect(it(kids[1].ast))
	if len(suffix) < 2 || suffix[len(suffix)-1].sym != Index {
		return "", nil, nil, false
	}
	for _, n := range suffix[:len(suffix)-1] {
		if n.sym != Selector {
			return "", nil, nil, false
		}
		fld := e.soleIdent(n.ast)
		if fld == "" {
			return "", nil, nil, false
		}
		fields = append(fields, fld)
	}
	return e.src(kids[0].tok), fields, suffix[len(suffix)-1].ast, true
}

// factorIndexSelect recognises a Factor that indexes a container and then selects
// from the element -- `s[i].x`, `a[i].x`, `p.pts[i].x`, `s[i].x.y`: an identifier,
// an optional leading field chain, exactly one Index, then at least one trailing
// Selector. None of the other three factor shapes can express it, because each
// stops at the first step of the other kind: factorFieldAccess admits no Index,
// factorIndex no Selector, and factorFieldIndex requires the Index to be last.
//
// Exactly one Index. A second (`m[i][j]`) would need the already-emitted prefix as
// the inner bound's length expression, and it is no longer a string by then, so it
// is rejected here and fails honestly upstream rather than emitting an unchecked
// access.
func (e *emitter) factorIndexSelect(kids []Node) (base string, pre []string, indexAST []int32, post []string, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, nil, nil, false
	}
	pre, indexAST, post, ok = e.splitIndexSelect(slices.Collect(it(kids[1].ast)))
	if !ok {
		return "", nil, nil, nil, false
	}
	return e.src(kids[0].tok), pre, indexAST, post, true
}

// splitIndexSelect splits an access chain `{Selector} Index {Selector}` into the
// leading field chain, the index, and the trailing field chain. Shared by the
// expression shape (a FactorSuffix's children) and the assignment target shape
// (a postfix minus its PostfixOp), which are the same node sequence.
func (e *emitter) splitIndexSelect(chain []Node) (pre []string, indexAST []int32, post []string, ok bool) {
	at := -1
	for i, n := range chain {
		if n.sym != Index {
			continue
		}
		if at >= 0 {
			return nil, nil, nil, false // more than one index
		}
		at = i
	}
	if at < 0 || at == len(chain)-1 {
		return nil, nil, nil, false // no index, or nothing selected after it
	}
	if pre, ok = e.selectorFieldsAll(chain[:at]); !ok {
		return nil, nil, nil, false
	}
	if post, ok = e.selectorFieldsAll(chain[at+1:]); !ok || len(post) == 0 {
		return nil, nil, nil, false
	}
	return pre, chain[at].ast, post, true
}

// sliceParts inspects an Index node. isSlice reports a colon -- a slice expression;
// low and high are the bound expressions, nil when omitted. For a plain index (no
// colon), isSlice is false and low is the index expression.
func (e *emitter) sliceParts(indexAST []int32) (low, high []int32, isSlice bool) {
	beforeColon := true
	for n := range it(indexAST) {
		switch n.sym {
		case Expression:
			if beforeColon {
				low = n.ast
			} else {
				high = n.ast
			}
		case 0:
			if e.f.ch(n.tok) == COLON {
				isSlice, beforeColon = true, false
			}
		}
	}
	return low, high, isSlice
}

// varType returns a variable's C type from the local then the package environment.
func (e *emitter) varType(name string) (string, bool) {
	if ct, ok := e.locals[name]; ok {
		return ct, true
	}
	ct, ok := e.globals[name]
	return ct, ok
}

// sliceSource describes what a slice expression slices: the C type of the result
// header, the base pointer, and the base's length and capacity. baseLen is the
// default when high is omitted; baseCap becomes the header's third field and is
// empty for a string, which has no capacity and slices to a 2-field ogo_string.
type sliceSource struct{ cname, ptr, baseLen, baseCap string }

// sliceableVar resolves a variable base to slice: a string, a fixed array, or a
// slice.
func (e *emitter) sliceableVar(base string) (sliceSource, bool) {
	switch {
	case e.isStringVarName(base):
		e.usesString = true
		return sliceSource{cString, base + ".str", base + ".len", ""}, true
	case e.hasArrayVar(base):
		a, _ := e.arrayVar(base)
		e.needSlice(a.elem)
		return sliceSource{sliceCName(a.elem), base, a.bound, a.bound}, true
	case e.hasSliceVar(base):
		elem, _ := e.sliceElem(base)
		e.needSlice(elem)
		return sliceSource{sliceCName(elem), base + ".ptr", base + ".len", base + ".cap"}, true
	}
	return sliceSource{}, false
}

// sliceableField resolves a struct field base to slice -- `b.data[1:3]` over a
// slice field, `b.arr[1:3]` over an array field. A slice field is re-sliced
// through its own header (its cap still bounds how far the result may grow); an
// array field decays to its inline storage, bounded both ways by the extent.
func (e *emitter) sliceableField(base string, fields []string) (sliceSource, bool) {
	if len(fields) == 0 {
		return sliceSource{}, false
	}
	lv := e.fieldAccessC(base, fields)
	if a, ok := e.fieldArray(base, fields); ok {
		e.needSlice(a.elem)
		return sliceSource{sliceCName(a.elem), lv, a.bound, a.bound}, true
	}
	ct, ok := e.fieldType(base, fields)
	if !ok || !e.isSliceCType(ct) {
		return sliceSource{}, false
	}
	if elem, ok := e.sliceElemByName[ct]; ok {
		e.needSlice(elem)
	}
	return sliceSource{ct, lv + ".ptr", lv + ".len", lv + ".cap"}, true
}

// emitSliceExpr emits a slice expression `base[low:high]` as a new { pointer,
// length } header aliasing base's storage -- a non-owning view, no copy. An
// omitted low is 0; an omitted high is base's length. Three bases are modelled: a
// string (-> ogo_string over .str/.len), a fixed array (-> ogo_slice_<elem> over
// the decayed array and its compile-time bound), and a slice (-> the same header
// over .ptr/.len). In a static initializer a brace is used, not a compound literal
// (not a constant expression there; see declInit).
func (e *emitter) emitSliceExpr(src sliceSource, low, high []int32) {
	cname, ptr, baseLen, baseCap := src.cname, src.ptr, src.baseLen, src.baseCap
	if e.declInit {
		e.emit("{")
	} else {
		e.emit("(" + cname + "){")
	}
	// ptr: base's data, offset by low.
	e.emit(ptr)
	if low != nil {
		e.emit(" + ")
		e.emitExpr(low)
	}
	// len: (high, or base's length when omitted) - low.
	e.emit(", ")
	if high != nil {
		e.emitExpr(high)
	} else {
		e.emit(baseLen)
	}
	if low != nil {
		e.emit(" - ")
		e.emitExpr(low)
	}
	// cap (slices only): cap(base) - low, so the result can still be re-sliced up
	// to the end of the backing storage (Go: the slice upper bound reaches cap).
	if baseCap != "" {
		e.emit(", " + baseCap)
		if low != nil {
			e.emit(" - ")
			e.emitExpr(low)
		}
	}
	e.emit("}")
}

// isStringVarName reports whether base names a string-typed variable.
func (e *emitter) isStringVarName(base string) bool {
	ct, ok := e.varType(base)
	return ok && ct == cString
}

// hasArrayVar reports whether base names a fixed-array variable.
func (e *emitter) hasArrayVar(base string) bool { _, ok := e.arrayVar(base); return ok }

// hasSliceVar reports whether base names a slice variable.
func (e *emitter) hasSliceVar(base string) bool { _, ok := e.sliceElem(base); return ok }

// needPanic records that ogo_panic is reachable and pulls in its includes (printf,
// abort, and _waitms / _reboot from propeller2.h).
func (e *emitter) needPanic() {
	e.usesPanic = true
	e.includes["stdio.h"] = true
	e.includes["stdlib.h"] = true
	e.includes["propeller2.h"] = true
}

// emitIndex emits an index expression, wrapping it in a bounds check ogo_bound(i,
// len) unless checks are disabled, the container's length is unknown (lenExpr ""),
// or the index is a constant provably in range. lenExpr is the container's length:
// a slice's ".len", or an array's compile-time bound.
func (e *emitter) emitIndex(idxAST []int32, lenExpr string) {
	if !e.checks || lenExpr == "" || e.constIndexInRange(idxAST, lenExpr) {
		e.emitExpr(idxAST)
		return
	}
	e.needPanic()
	e.usesBound = true
	e.emit("ogo_bound(")
	e.emitExpr(idxAST)
	e.emit(", " + lenExpr + ")")
}

// constIndexInRange reports whether idxAST is an integer literal provably within
// [0, lenExpr) -- both decimal constants and the index in range -- so its bounds
// check can be skipped. A runtime length (a slice's ".len") never parses as an int.
func (e *emitter) constIndexInRange(idxAST []int32, lenExpr string) bool {
	tok, ok := e.soleToken(idxAST)
	if !ok || e.f.ch(tok) != INT {
		return false
	}
	i, err1 := strconv.Atoi(normalizeIntLit(e.src(tok)))
	n, err2 := strconv.Atoi(lenExpr)
	return err1 == nil && err2 == nil && i >= 0 && i < n
}

// isIntLiteral reports whether an operand is a bare integer literal (a non-zero
// constant divisor needs no divide-by-zero check; a constant zero is a compile
// error the C backend rejects).
func (e *emitter) isIntLiteral(n Node) bool {
	tok, ok := e.soleToken(n.ast)
	return ok && e.f.ch(tok) == INT
}

// newBacking returns a fresh, translation-unit-unique name for a make() backing
// array.
func (e *emitter) newBacking() string {
	s := "ogo_backing_" + strconv.Itoa(e.makeN)
	e.makeN++
	return s
}

// peelToFactorAST descends single-child expression wrappers (Expression/SimpleExpr/
// Term/UnaryExpr) and returns the innermost node's child AST -- the Factor level.
func (e *emitter) peelToFactorAST(ast []int32) []int32 {
	cur := ast
	for {
		kids := slices.Collect(it(cur))
		if len(kids) == 1 && kids[0].sym != 0 {
			cur = kids[0].ast
			continue
		}
		return cur
	}
}

// makeSliceInit recognises a `make([]T, len [, cap])` initializer, returning the
// element C type and the length/capacity expression ASTs (capAST nil for the
// two-argument form, where cap == len). ok is false for any other expression.
func (e *emitter) makeSliceInit(initExpr []int32) (elem string, lenAST, capAST []int32, ok bool) {
	recv, suffix, isCall := e.directCall(initExpr)
	if !isCall || recv != "make" || len(suffix) != 1 || suffix[0].sym != CallSuffix {
		return "", nil, nil, false
	}
	args := e.callArgExprs(suffix[0].ast)
	if len(args) < 2 || len(args) > 3 {
		return "", nil, nil, false
	}
	// The first argument is the slice type "[]T" as a factor; read its element type.
	if elem, ok = e.sliceType(e.peelToFactorAST(args[0].ast)); !ok {
		return "", nil, nil, false
	}
	lenAST = args[1].ast
	if len(args) == 3 {
		capAST = args[2].ast
	}
	return elem, lenAST, capAST, true
}

// emitMakeSliceVar emits a slice variable initialized by make: a fixed backing
// array sized by the capacity (a compile-time constant, since the P2 has no heap)
// plus a { ptr, len, cap } header over it. static drives file-scope emission -- a
// `static` backing that C zero-inits vs a stack backing zeroed explicitly (P2 stack
// locals are not auto-zeroed).
// emitMakeSliceAssign assigns a fresh make([]T, ...) slice to an existing lvalue --
// a slice variable or a struct field -- by hoisting a local backing array and
// assigning a { backing, len, cap } header to lhs (distinct from emitMakeSliceVar,
// which declares a new variable).
func (e *emitter) emitMakeSliceAssign(lhs, cname, elem string, lenAST, capAST []int32) {
	sizeAST := capAST
	if sizeAST == nil {
		sizeAST = lenAST // the two-argument form: cap == len
	}
	size, ok := e.arrayBoundC(sizeAST)
	if !ok {
		e.fail("make needs a constant capacity")
		return
	}
	backing := e.newBacking()
	e.ind()
	e.emit(elem + " " + backing + "[" + size + "] = {0};\n")
	e.ind()
	e.emit(lhs + " = (" + cname + "){" + backing + ", ")
	if capAST != nil {
		e.emitExpr(lenAST)
	} else {
		e.emit(size)
	}
	e.emit(", " + size + "};\n")
}

func (e *emitter) emitMakeSliceVar(name, cname, elem string, lenAST, capAST []int32, static bool) {
	sizeAST := capAST
	if sizeAST == nil {
		sizeAST = lenAST // the two-argument form: cap == len
	}
	size, ok := e.arrayBoundC(sizeAST)
	if !ok {
		e.fail("make needs a constant capacity")
		return
	}
	backing := e.newBacking()
	// Backing array.
	if static {
		e.emit("static " + elem + " " + backing + "[" + size + "];\n")
	} else {
		e.ind()
		e.emit(elem + " " + backing + "[" + size + "] = {0};\n")
	}
	// Header { backing, len, cap }. cap == the backing size; len is the initial
	// length (the size for the two-argument form).
	if static {
		e.emit("static ")
	} else {
		e.ind()
	}
	e.emit(cname + " " + name + " = {" + backing + ", ")
	if capAST != nil {
		e.emitExpr(lenAST)
	} else {
		e.emit(size)
	}
	e.emit(", " + size + "};\n")
}

// emitFor handles `for {}` (infinite -> for(;;)) and `for cond {}` (conditional
// -> while(cond)). Init/post clauses and range are not supported yet.
func (e *emitter) emitFor(nodes []Node) {
	var cond, body []int32
	for _, n := range nodes[1:] {
		switch n.sym {
		case Expression:
			cond = n.ast
		case Block:
			body = n.ast
		default:
			e.fail("for-loop clause %v is not supported yet", n.sym)
			return
		}
	}
	if body == nil {
		e.fail("for-loop without a body")
		return
	}
	e.ind()
	if cond == nil {
		e.emit("for (;;) {\n")
	} else {
		e.emit("while ")
		e.emitCondition(cond)
		e.emit(" {\n")
	}
	e.indent++
	e.deferBlockDepth++
	e.emitBlockStmts(body)
	e.deferBlockDepth--
	e.indent--
	e.ind()
	e.emit("}\n")
}

// emitSwitch emits a switch statement as an if / else-if chain. OctoGo has no
// fallthrough, so each clause is independent; and Go case expressions may be
// non-constant, which a C switch could not express. Three shapes are handled:
//
//	switch x { case a, b: ... }        // value switch:      _t = x;  (_t == a || _t == b)
//	switch { case cond: ... }          // expression switch: (cond)
//	switch v := expr { case a: ... }   // guard-var switch:  v = expr; (v == a)
//
// The guard is evaluated once (into the guard variable, or a temporary for a
// non-trivial value), so it is not re-run per case. The default clause, wherever
// it appears in source, becomes the trailing else.
func (e *emitter) emitSwitch(ast []int32) {
	var guardAST []int32
	var cases []Node
	for n := range it(ast) {
		switch n.sym {
		case SwitchGuard:
			guardAST = n.ast
		case CaseClause:
			cases = append(cases, n)
		}
	}

	// Resolve the guard variable to compare against ("" for an expression switch),
	// emitting an enclosing block + declaration when it needs a scoped name.
	guardVar, block := "", false
	if guardAST != nil {
		var ok bool
		guardVar, block, ok = e.emitSwitchGuard(guardAST)
		if !ok {
			return
		}
	}

	var defaultClause Node
	hasDefault, wrote := false, false
	for _, cc := range cases {
		exprs, isDefault := e.caseHead(cc.ast)
		if isDefault {
			defaultClause, hasDefault = cc, true
			continue
		}
		if !wrote {
			e.ind()
			e.emit("if ")
			wrote = true
		} else {
			e.emit(" else if ")
		}
		e.emitCaseCond(guardVar, exprs)
		e.emit(" {\n")
		e.indent++
		e.emitCaseBody(cc.ast)
		e.indent--
		e.ind()
		e.emit("}")
	}
	switch {
	case hasDefault && wrote:
		e.emit(" else {\n")
		e.indent++
		e.emitCaseBody(defaultClause.ast)
		e.indent--
		e.ind()
		e.emit("}\n")
	case hasDefault: // a switch of only a default clause
		e.ind()
		e.emit("{\n")
		e.indent++
		e.emitCaseBody(defaultClause.ast)
		e.indent--
		e.ind()
		e.emit("}\n")
	case wrote:
		e.emit("\n")
	}

	if block {
		e.indent--
		e.ind()
		e.emit("}\n")
	}
}

// emitSwitchGuard emits the guard of a value or guard-var switch, returning the C
// name to compare cases against, whether an enclosing block was opened (to close
// after the chain), and ok. A plain variable guard is compared directly; a
// non-trivial value or a `v := expr` guard is bound to a scoped name first.
func (e *emitter) emitSwitchGuard(guardAST []int32) (guardVar string, block, ok bool) {
	var exprs []Node
	hasDefine := false
	for n := range it(guardAST) {
		switch n.sym {
		case Expression:
			exprs = append(exprs, n)
		case 0:
			if e.f.ch(n.tok) == DEFINE {
				hasDefine = true
			}
		}
	}
	if hasDefine { // switch v := expr
		if len(exprs) < 2 {
			e.fail("malformed switch guard")
			return "", false, false
		}
		vtok, isID := e.soleToken(exprs[0].ast)
		if !isID || e.f.ch(vtok) != IDENT {
			e.fail("unsupported switch guard variable")
			return "", false, false
		}
		vtype, tok := e.inferCType(exprs[1].ast)
		if !tok {
			e.fail("cannot infer the type of the switch guard variable")
			return "", false, false
		}
		name := e.src(vtok)
		e.ind()
		e.emit("{\n")
		e.indent++
		e.locals[name] = vtype
		e.ind()
		e.emit(vtype + " " + name + " = ")
		e.emitExpr(exprs[1].ast)
		e.emit(";\n")
		return name, true, true
	}
	if len(exprs) < 1 {
		e.fail("malformed switch guard")
		return "", false, false
	}
	// A plain variable can be compared directly; a richer value is bound to a
	// temporary so it is evaluated once.
	if tok, single := e.soleToken(exprs[0].ast); single && e.f.ch(tok) == IDENT {
		return e.src(tok), false, true
	}
	gtype, tok := e.inferCType(exprs[0].ast)
	if !tok {
		e.fail("cannot infer the type of the switch value")
		return "", false, false
	}
	tmp := e.newTmp()
	e.ind()
	e.emit("{\n")
	e.indent++
	e.ind()
	e.emit(gtype + " " + tmp + " = ")
	e.emitExpr(exprs[0].ast)
	e.emit(";\n")
	return tmp, true, true
}

// caseHead returns a case clause's case expressions and whether it is the default.
func (e *emitter) caseHead(cc []int32) (exprs []Node, isDefault bool) {
	for n := range it(cc) {
		if n.sym != CaseHead {
			continue
		}
		for h := range it(n.ast) {
			switch h.sym {
			case ExpressionList:
				for ex := range it(h.ast) {
					if ex.sym == Expression {
						exprs = append(exprs, ex)
					}
				}
			case 0:
				if e.f.ch(h.tok) == DEFAULT {
					isDefault = true
				}
			}
		}
	}
	return exprs, isDefault
}

// emitCaseCond emits a case's condition: for a value switch, the guard equals any
// of the case expressions (`guard == a || guard == b`); for an expression switch
// (guardVar ""), the case expressions are themselves the conditions (`a || b`).
func (e *emitter) emitCaseCond(guardVar string, exprs []Node) {
	e.emit("(")
	for i, ex := range exprs {
		if i != 0 {
			e.emit(" || ")
		}
		if guardVar != "" {
			e.emit(guardVar + " == ")
		}
		e.emitExpr(ex.ast)
	}
	e.emit(")")
}

// emitCaseBody emits the statements of a case clause (those following its ":").
func (e *emitter) emitCaseBody(cc []int32) {
	e.deferBlockDepth++
	defer func() { e.deferBlockDepth-- }()
	for n := range it(cc) {
		if n.sym == Statement {
			e.emitStatement(n.ast)
		}
	}
}

// emitIf emits an if statement (the IfStmt node):
//
//	IfStmt = "if" Expression Block [ "else" ( IfStmt | Block ) ] .
//
// It indents the leading `if`, then defers to emitIfBody, which handles the
// condition, the branch, and any `else`/`else if` continuation.
func (e *emitter) emitIf(ast []int32) {
	e.ind()
	e.emitIfBody(ast)
}

// emitIfBody emits `if (cond) { ... }` and its optional else branch, assuming the
// cursor is already positioned — an initial indent for a top-level if, or the
// `} else ` written by an enclosing call for an `else if`. It recurses on an
// `else if` continuation so the C reads `} else if (c) {` on one line.
func (e *emitter) emitIfBody(ast []int32) {
	var cond, thenBody, elseBody, elseIf []int32
	for n := range it(ast) {
		switch n.sym {
		case Expression:
			cond = n.ast
		case Block:
			if thenBody == nil {
				thenBody = n.ast
			} else {
				elseBody = n.ast
			}
		case IfStmt:
			elseIf = n.ast
		case 0:
			// IF / ELSE terminals.
		default:
			e.fail("if clause %v is not supported yet", n.sym)
			return
		}
	}
	if cond == nil || thenBody == nil {
		e.fail("malformed if statement")
		return
	}
	e.emit("if ")
	e.emitCondition(cond)
	e.emit(" {\n")
	e.indent++
	e.deferBlockDepth++
	e.emitBlockStmts(thenBody)
	e.deferBlockDepth--
	e.indent--
	e.ind()
	e.emit("}")
	switch {
	case elseIf != nil:
		e.emit(" else ")
		e.emitIfBody(elseIf)
	case elseBody != nil:
		e.emit(" else {\n")
		e.indent++
		e.deferBlockDepth++
		e.emitBlockStmts(elseBody)
		e.deferBlockDepth--
		e.indent--
		e.ind()
		e.emit("}\n")
	default:
		e.emit("\n")
	}
}

// emitCondition emits an if/for condition wrapped in exactly one set of
// parentheses. It emits the Expression's children directly (rather than the
// Expression node, which would add its own binary-operator parens) so a simple
// `i < 20` becomes `(i < 20)`, not `((i < 20))`.
func (e *emitter) emitCondition(exprChildren []int32) {
	e.emit("(")
	for n := range it(exprChildren) {
		e.emitExprNode(n)
	}
	e.emit(")")
}

// emitReturn handles `return`, `return expr`, and `return e0, e1, ...`. A bare
// return in main yields `return 0;` to satisfy C's int main; in a void function
// it yields `return;`. A single value emits `return <expr>;`. Multiple values are
// returned as the function's result struct via a compound literal,
// `return (ogo_ret_<fn>){ e0, e1, ... };`.
// emitDefer records a top-level `defer` statement to be replayed before the
// function returns. The call is not emitted here; emitDeferred replays it in LIFO
// order at each return and at a fall-through function end. A defer inside a nested
// block (an if/switch body) is conditionally registered at runtime, which needs a
// per-defer flag not modelled yet, so it fails honestly.
func (e *emitter) emitDefer(nodes []Node) {
	if e.deferBlockDepth > 0 {
		e.fail("defer inside a nested block is not supported yet")
		return
	}
	var head Node
	var suffix []Node
	for _, n := range nodes {
		switch n.sym {
		case AssignHead:
			head = n
		case Selector, Index, CallSuffix:
			suffix = append(suffix, n)
		}
	}
	if head.sym != AssignHead || len(suffix) == 0 {
		e.fail("a defer statement must be a function call")
		return
	}
	e.defers = append(e.defers, deferredCall{head, suffix})
}

// emitDeferred replays the recorded defers in LIFO order, each as a statement call.
// Deferred arguments are evaluated where the call runs (at the return), not where
// the defer was written -- a simplification from Go that is identical for the
// constant / cleanup arguments that dominate here.
func (e *emitter) emitDeferred() {
	for i := len(e.defers) - 1; i >= 0; i-- {
		e.emitCall(e.defers[i].head, e.defers[i].suffix)
	}
}

func (e *emitter) emitReturn(nodes []Node) {
	var exprs []Node
	for _, n := range nodes[1:] {
		if n.sym == ExpressionList {
			for c := range it(n.ast) {
				if c.sym == Expression {
					exprs = append(exprs, c)
				}
			}
		}
	}
	e.emitDeferred()
	e.ind()
	switch len(exprs) {
	case 0:
		if e.mainRet {
			e.emit("return 0;\n")
		} else {
			e.emit("return;\n")
		}
	case 1:
		e.emit("return ")
		e.emitExpr(exprs[0].ast)
		e.emit(";\n")
	default:
		e.emit("return (" + e.retStructName(e.curFunc) + "){")
		for i, ex := range exprs {
			if i != 0 {
				e.emit(", ")
			}
			e.emitExpr(ex.ast)
		}
		e.emit("};\n")
	}
}

// emitAssignHeadStmt handles the `AssignHead Postfix` statement family: a call
// (Postfix ends in CallSuffix) or an assignment (Postfix carries a PostfixOp).
func (e *emitter) emitAssignHeadStmt(nodes []Node) {
	if len(nodes) != 2 || nodes[1].sym != Postfix {
		e.fail("unsupported statement form")
		return
	}
	head := nodes[0]
	postfix := slices.Collect(it(nodes[1].ast))
	switch {
	case containsSym(postfix, PostfixOp):
		e.emitAssignment(head, postfix)
	case containsSym(postfix, CallSuffix):
		e.emitCall(head, postfix)
	default:
		e.fail("unsupported statement form")
	}
}

// emitCall emits a call statement: builtin print/println (mapped to printf) or,
// via emitCallExpr, a user-function or p2-intrinsic call, indented and closed
// with `;`.
func (e *emitter) emitCall(head Node, postfix []Node) {
	recv := e.soleIdent(head.ast)
	if recv == "" {
		e.fail("unsupported call target")
		return
	}
	if len(postfix) == 1 && postfix[0].sym == CallSuffix && (recv == "println" || recv == "print") {
		e.emitPrint(recv == "println", postfix[0].ast)
		return
	}
	e.ind()
	if !e.emitCallExpr(recv, postfix) {
		e.fail("only <pkg>.<Func>(args) or builtin(args) call statements are supported yet")
		return
	}
	e.emit(";\n")
}

// emitCallExpr emits a call in value position (no indent, no trailing `;`): a
// direct user-function call `name(args)` or a p2-qualified call mapped to its
// intrinsic `_intr(args)`. It reports false when the head/suffix is not a
// supported call shape, latching a specific error for a bad p2 call; it is shared
// by the statement call path (emitCall) and the expression Factor path. The
// checker has already verified the callee resolves and the arguments match.
func (e *emitter) emitCallExpr(recv string, suffix []Node) bool {
	switch {
	case len(suffix) == 1 && suffix[0].sym == CallSuffix:
		if recv == "len" {
			e.emitLen(suffix[0].ast)
			return true
		}
		if recv == "cap" {
			e.emitCap(suffix[0].ast)
			return true
		}
		if recv == "append" {
			// Single-result append: s = append(s, x). The two-result form
			// s, ok = append(s, x) is handled in emitMultiAssign.
			e.emitAppend(suffix[0].ast)
			return true
		}
		if recv == "make" {
			// make needs a hoisted backing array, so it is only handled as a
			// `var s []T = make(...)` initializer (see emitMakeSliceVar), not as a
			// general expression.
			e.fail("make is only supported as a `var s []T = make(...)` initializer yet")
			return true
		}
		e.emit(recv + "(")
		e.emitCallArgs(suffix[0].ast)
		e.emit(")")
		return true
	case len(suffix) == 2 && suffix[0].sym == Selector && suffix[1].sym == CallSuffix:
		method := e.soleIdent(suffix[0].ast)
		// A method call `x.M(args)` on a variable of a user-defined type (struct or
		// named, value or pointer) lowers to `<T>_M(recv, args)`, with the receiver
		// adjusted to match the method's value or pointer receiver. Distinguished from
		// a package call (`p2.F(...)`) by recv naming a typed variable, not an import.
		if rct, ok := e.varType(recv); ok && e.isUserType(methodBaseType(rct)) {
			cname := methodCName(methodBaseType(rct), method)
			e.emit(cname + "(")
			e.emitMethodReceiver(recv, rct, e.methodPtr[cname])
			if len(e.callArgExprs(suffix[1].ast)) > 0 {
				e.emit(", ")
				e.emitCallArgs(suffix[1].ast)
			}
			e.emit(")")
			return true
		}
		if recv != "p2" {
			e.fail("unknown package %q (only p2 is supported yet)", recv)
			return false
		}
		intr, ok := p2Intrinsics[method]
		if !ok {
			e.fail("unsupported p2 function %q", method)
			return false
		}
		e.emit(intr + "(")
		e.emitCallArgs(suffix[1].ast)
		e.emit(")")
		return true
	}
	return false
}

// emitMethodReceiver emits a method call's receiver argument, bridging the receiver
// the caller holds and the one the method declares: it takes the address of a value
// passed to a pointer method (&x), dereferences a pointer passed to a value method
// (*x), and otherwise passes the receiver unchanged.
func (e *emitter) emitMethodReceiver(recv, recvCType string, wantPtr bool) {
	switch havePtr := e.isPointer(recvCType); {
	case wantPtr && !havePtr:
		e.emit("&" + recv)
	case !wantPtr && havePtr:
		e.emit("*" + recv)
	default:
		e.emit(recv)
	}
}

// emitLen emits the builtin `len(x)`: an array's length is its compile-time bound;
// a string's and a slice's is its header's `len` field.
func (e *emitter) emitLen(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) != 1 {
		e.fail("len takes exactly one argument")
		return
	}
	arg := args[0].ast
	if tok, ok := e.soleToken(arg); ok && e.f.ch(tok) == IDENT {
		if a, ok := e.arrayVar(e.src(tok)); ok {
			e.emit(a.bound)
			return
		}
	}
	// A string and a slice both carry their length in a `.len` header field.
	if ct, ok := e.inferCType(arg); ok && (ct == cString || e.isSliceCType(ct)) {
		e.emit("(")
		e.emitExpr(arg)
		e.emit(").len")
		return
	}
	e.fail("len is only supported for strings, arrays and slices yet")
}

// emitCap emits the builtin `cap(x)`: an array's capacity is its compile-time
// bound; a slice's is its header's `cap` field. Strings have no capacity.
func (e *emitter) emitCap(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) != 1 {
		e.fail("cap takes exactly one argument")
		return
	}
	arg := args[0].ast
	if tok, ok := e.soleToken(arg); ok && e.f.ch(tok) == IDENT {
		if a, ok := e.arrayVar(e.src(tok)); ok {
			e.emit(a.bound)
			return
		}
	}
	if ct, ok := e.inferCType(arg); ok && e.isSliceCType(ct) {
		e.emit("(")
		e.emitExpr(arg)
		e.emit(").cap")
		return
	}
	e.fail("cap is only supported for arrays and slices yet")
}

// appendParts validates an append call suffix -- exactly two arguments whose first
// is a slice -- returning that slice's element C type and the argument nodes, and
// recording needSlice(elem). The first argument may be a slice variable or a
// slice-typed struct field (append(b.data, x)); its type is inferred, and emitExpr
// renders either form. ok is false (with a latched error) for any other shape.
func (e *emitter) appendParts(callSuffix []int32) (elem string, args []Node, ok bool) {
	args = e.callArgExprs(callSuffix)
	if len(args) != 2 {
		e.fail("append takes exactly two arguments yet -- append(s, x)")
		return "", nil, false
	}
	ct, ok := e.inferCType(args[0].ast)
	if !ok || !e.isSliceCType(ct) {
		e.fail("append's first argument must be a slice variable or slice field yet")
		return "", nil, false
	}
	elem = sliceElemFromCName(ct)
	e.needSlice(elem)
	return elem, args, true
}

// exprIdent returns the sole identifier an expression reduces to (peeling wrapper
// levels), e.g. the "s" in an argument that is just s. ok is false if the
// expression is not exactly one identifier.
func (e *emitter) exprIdent(ast []int32) (string, bool) {
	if tok, ok := e.soleToken(ast); ok && e.f.ch(tok) == IDENT {
		return e.src(tok), true
	}
	return "", false
}

// emitAppend emits the single-result append `append(s, x)` as a call to the
// trapping ogo_append_<T> helper (which panics if the slice is already at cap).
func (e *emitter) emitAppend(callSuffix []int32) {
	elem, args, ok := e.appendParts(callSuffix)
	if !ok {
		return
	}
	e.appendElems[elem] = true
	e.needPanic()
	e.emit(appendCName(elem) + "(")
	e.emitExpr(args[0].ast)
	e.emit(", ")
	e.emitExpr(args[1].ast)
	e.emit(")")
}

// emitTryAppend emits the two-result append `s, ok = append(s, x)` (or `:=`): it
// binds the ok-form helper's { slice, ok } result to a temporary, then assigns (or,
// for `:=`, declares) the slice and ok targets. A blank target is skipped. This
// form never traps -- an overflow leaves the slice unchanged and reports ok == 0.
func (e *emitter) emitTryAppend(targets []string, define bool, callSuffix []int32) {
	if len(targets) != 2 {
		e.fail("the two-result append form is `s, ok = append(s, x)`")
		return
	}
	elem, args, ok := e.appendParts(callSuffix)
	if !ok {
		return
	}
	e.tryappendElems[elem] = true
	tmp := e.newTmp()
	e.ind()
	e.emit(appendokCName(elem) + " " + tmp + " = " + tryappendCName(elem) + "(")
	e.emitExpr(args[0].ast)
	e.emit(", ")
	e.emitExpr(args[1].ast)
	e.emit(");\n")
	// The slice target, then the ok target (int).
	if targets[0] != "_" {
		e.ind()
		if define {
			e.sliceVars[targets[0]] = elem
			e.locals[targets[0]] = sliceCName(elem)
			e.emit(sliceCName(elem) + " " + targets[0] + " = " + tmp + ".slice;\n")
		} else {
			e.emit(targets[0] + " = " + tmp + ".slice;\n")
		}
	}
	if targets[1] != "_" {
		e.ind()
		if define {
			e.locals[targets[1]] = "int"
			e.emit("int " + targets[1] + " = " + tmp + ".ok;\n")
		} else {
			e.emit(targets[1] + " = " + tmp + ".ok;\n")
		}
	}
}

// arrayVar looks a name up in the local then the package array environment.
func (e *emitter) arrayVar(name string) (arrDim, bool) {
	if a, ok := e.arrays[name]; ok {
		return a, true
	}
	a, ok := e.globalArrays[name]
	return a, ok
}

// emitPrint maps print/println to serial output. Each argument prints by type: an
// integer via printf %d, a string via the ogo_string helper (exact byte length),
// and a slice or array as "[e0 e1 ...]". Multiple arguments are separated by a
// single space; println appends a trailing newline.
func (e *emitter) emitPrint(newline bool, callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	e.includes["stdio.h"] = true
	switch {
	case len(args) == 0:
		e.ind()
		if newline {
			e.emit("printf(\"\\n\");\n")
		} else {
			e.emit("(void)0;\n")
		}
	case len(args) == 1:
		e.emitPrintOne(newline, args[0])
	default:
		e.emitPrintMulti(newline, args)
	}
}

// emitPrintOne emits print/println of a single argument, appending a newline when
// newline is set. Integer and string output are folded into one call (preserving
// the compact printf("%d\n", x) / ogo_println_str(x) forms); slices and arrays go
// through their per-element print helper.
func (e *emitter) emitPrintOne(newline bool, arg Node) {
	if ct, ok := e.inferCType(arg.ast); ok {
		if ct == cString {
			e.usesStringPrint = true
			e.ind()
			if newline {
				e.emit("ogo_println_str(")
			} else {
				e.emit("ogo_print_str(")
			}
			e.emitExpr(arg.ast)
			e.emit(");\n")
			return
		}
		if e.isSliceCType(ct) {
			e.emitPrintSlice(newline, sliceElemFromCName(ct), func() { e.emitExpr(arg.ast) })
			return
		}
	}
	// A bare array variable decays to a pointer, so it is printed by viewing it as a
	// full-length slice header rather than as a (meaningless) %d of its address.
	if base, ok := e.exprIdent(arg.ast); ok {
		if a, ok := e.arrayVar(base); ok {
			e.emitPrintSlice(newline, a.elem, func() {
				e.emit("(" + sliceCName(a.elem) + "){" + base + ", " + a.bound + ", " + a.bound + "}")
			})
			return
		}
	}
	// Default: an integer, or an integer-typed expression, via printf %d.
	e.ind()
	if newline {
		e.emit("printf(\"%d\\n\", ")
	} else {
		e.emit("printf(\"%d\", ")
	}
	e.emitExpr(arg.ast)
	e.emit(");\n")
}

// emitPrintSlice emits a call to the ogo_print_slice_<T> / ogo_println_slice_<T>
// helper for element type elem, with the slice-header argument written by emitArg.
// Only integer elements are printable for now; anything else fails honestly rather
// than emitting a wrong %d.
func (e *emitter) emitPrintSlice(newline bool, elem string, emitArg func()) {
	if !e.canPrintElem(elem) {
		e.fail("printing a slice or array of %q is not supported yet", elem)
		return
	}
	e.needSlice(elem)
	e.printSliceElems[elem] = true
	e.ind()
	if newline {
		e.emit(printlnSliceCName(elem) + "(")
	} else {
		e.emit(printSliceCName(elem) + "(")
	}
	emitArg()
	e.emit(");\n")
}

// emitPrintMulti emits print/println of two or more arguments. When every argument
// prints as a plain integer they fold into a single space-separated printf; a mix
// of types instead prints each value in turn, a space between operands, then the
// trailing newline for println.
func (e *emitter) emitPrintMulti(newline bool, args []Node) {
	allScalar := true
	for _, arg := range args {
		if !e.isScalarPrint(arg) {
			allScalar = false
			break
		}
	}
	if allScalar {
		e.ind()
		e.emit("printf(\"")
		for i := range args {
			if i > 0 {
				e.emit(" ")
			}
			e.emit("%d")
		}
		if newline {
			e.emit("\\n")
		}
		e.emit("\"")
		for _, arg := range args {
			e.emit(", ")
			e.emitExpr(arg.ast)
		}
		e.emit(");\n")
		return
	}
	for i, arg := range args {
		if i > 0 {
			e.ind()
			e.emit("printf(\" \");\n")
		}
		e.emitPrintOne(false, arg)
	}
	if newline {
		e.ind()
		e.emit("printf(\"\\n\");\n")
	}
}

// isScalarPrint reports whether arg prints via printf %d (an integer or integer-
// typed expression) -- i.e. it is neither a string, a slice nor an array.
func (e *emitter) isScalarPrint(arg Node) bool {
	if ct, ok := e.inferCType(arg.ast); ok {
		if ct == cString || e.isSliceCType(ct) {
			return false
		}
	}
	if base, ok := e.exprIdent(arg.ast); ok {
		if _, ok := e.arrayVar(base); ok {
			return false
		}
	}
	return true
}

// canPrintElem reports whether a slice/array of the given C element type can be
// printed. Only int is printable for now (its %d helper); other element types fail
// honestly until their own print form is wired up.
func (e *emitter) canPrintElem(elem string) bool { return elem == "int" }

// derefStars returns the leading pointer-indirection prefix of an AssignHead
// (AssignHead = { "*" } identifier ...), so a dereferenced target `*p = v` writes
// through the pointer rather than to it.
func (e *emitter) derefStars(headAST []int32) string {
	stars := ""
	for n := range it(headAST) {
		if n.sym != 0 || e.f.ch(n.tok) != MUL {
			break
		}
		stars += "*"
	}
	return stars
}

// emitAssignment handles a single-target assignment `lhs = expr`, a field
// assignment `base.f = expr`, a short declaration `lhs := expr`, and increment /
// decrement. The PostfixOp is the postfix's last element; any Selectors before it
// form a field-access target. Indexed targets are not modelled.
func (e *emitter) emitAssignment(head Node, postfix []Node) {
	if len(postfix) == 0 || postfix[len(postfix)-1].sym != PostfixOp {
		e.fail("unsupported assignment target")
		return
	}
	base := e.soleIdent(head.ast)
	if base == "" {
		e.fail("only assignment to a simple variable is supported yet")
		return
	}
	// A dereferenced target `*p = v` (AssignHead = { "*" } identifier): keep the
	// leading star(s) so the assignment writes through the pointer, not to it. The
	// only reachable case is a pointer receiver mutating its pointee (`*c = v`).
	stars := e.derefStars(head.ast)
	// Index target `a[i] = v` (single index; mixing indexes and fields is not
	// modelled). The index is an expression, so it is emitted directly rather than
	// built into the string lhs the field path uses.
	if len(postfix) == 2 && postfix[0].sym == Index {
		e.emitIndexAssign(base, postfix[0], postfix[1])
		return
	}
	// An indexed element's field `s[i].x = v` / `p.pts[i].x = v`: an optional field
	// chain, one index, then at least one selector before the assignment. Tried
	// ahead of the index-last shape below, which cannot match a trailing selector.
	if pre, indexAST, post, ok := e.splitIndexSelect(postfix[:len(postfix)-1]); ok {
		e.emitIndexSelectAssign(base, stars, pre, indexAST, post, postfix[len(postfix)-1])
		return
	}
	// A slice-field indexed target `b.data[i] = v`: a field-access chain, a single
	// trailing index, then the assignment (postfix = { Selector } Index PostfixOp).
	if len(postfix) >= 3 && postfix[len(postfix)-2].sym == Index {
		if flds, ok := e.selectorFields(postfix[:len(postfix)-2]); ok {
			if _, _, _, ok := e.indexedContainer(base, flds); ok {
				e.emitFieldIndexAssign(base, flds, postfix[len(postfix)-2], postfix[len(postfix)-1])
				return
			}
		}
	}
	var fields []string
	for _, n := range postfix[:len(postfix)-1] {
		fld := ""
		if n.sym == Selector {
			fld = e.soleIdent(n.ast)
		}
		if fld == "" {
			e.fail("only simple and field assignment targets are supported yet")
			return
		}
		fields = append(fields, fld)
	}
	lhs := base
	if len(fields) != 0 {
		lhs = e.fieldAccessC(base, fields) // a field target, "->" through pointers
	}
	lhs = stars + lhs
	op := slices.Collect(it(postfix[len(postfix)-1].ast))

	// Multiple assignment `a, b = f()` / `a, b := f()`: the PostfixOp carries the
	// extra targets as LhsItems ahead of the operator.
	if containsSym(op, LhsItem) {
		if len(fields) != 0 {
			e.fail("a field target in a multiple assignment is not supported yet")
			return
		}
		e.emitMultiAssign(base, op)
		return
	}
	// Increment/decrement: PostfixOp = "++" | "--" (no operand of its own).
	if len(op) == 1 && op[0].sym == 0 {
		switch e.f.ch(op[0].tok) {
		case INC:
			e.ind()
			e.emit(lhs + "++;\n")
			return
		case DEC:
			e.ind()
			e.emit(lhs + "--;\n")
			return
		}
	}
	// PostfixOp = AssignOp Expression -- a compound assignment `x += e`. The target
	// is emitted once, which is the language semantics: it is evaluated once, not
	// twice as the `x = x + e` expansion would suggest.
	if len(op) == 2 && op[0].sym == AssignOp {
		t, ok := e.assignTailOf(postfix[len(postfix)-1])
		if !ok {
			e.fail("unsupported compound assignment operator")
			return
		}
		e.ind()
		e.emit(lhs)
		e.emitAssignTail(t)
		return
	}
	// PostfixOp = ( "=" | ":=" ) Expression, for the single-target forms.
	if len(op) != 2 || op[0].sym != 0 || op[1].sym != Expression {
		e.fail("only `name = expr`, `name := expr`, `name++` and `name--` are supported yet")
		return
	}
	switch e.f.ch(op[0].tok) {
	case ASSIGN:
		if lhs == "_" {
			// A blank-identifier assignment discards the value: evaluate the
			// right-hand side for its side effects and drop the result. The
			// `(void)` cast makes the discard explicit and valid C even when the
			// expression is a plain value. (`_ := expr` is rejected by the checker.)
			e.emitDiscard(op[1].ast)
			return
		}
		// A make initializer assigned to an existing lvalue -- a slice variable
		// (`s = make(...)`) or a slice struct field (`b.data = make(...)`) -- hoists a
		// backing array and assigns a fresh { backing, len, cap } header.
		if elem, lenAST, capAST, ok := e.makeSliceInit(op[1].ast); ok {
			e.needSlice(elem)
			e.emitMakeSliceAssign(lhs, sliceCName(elem), elem, lenAST, capAST)
			return
		}
		e.ind()
		e.emit(lhs + " = ")
		e.emitExpr(op[1].ast)
		e.emit(";\n")
	case DEFINE:
		if len(fields) != 0 {
			e.fail("a short declaration cannot have a field target")
			return
		}
		e.emitInferredLocal(base, op[1].ast)
	default:
		e.fail("only `name = expr` and `name := expr` are supported yet")
	}
}

// emitInferredLocal emits a type-inferred local declaration -- the short form
// `name := expr` or the var form `var name = expr` -- inferring name's C type from
// the initializer. A make initializer synthesises a slice backing array + header; a
// slice-typed result records its element type so later indexing / len / cap /
// append on name resolve.
func (e *emitter) emitInferredLocal(name string, initExpr []int32) {
	if elem, lenAST, capAST, ok := e.makeSliceInit(initExpr); ok {
		cname := sliceCName(elem)
		e.needSlice(elem)
		e.sliceVars[name] = elem
		e.locals[name] = cname
		e.emitMakeSliceVar(name, cname, elem, lenAST, capAST, false)
		return
	}
	ct, ok := e.inferCType(initExpr)
	if !ok {
		e.fail("cannot infer a type for the declaration of %q", name)
		return
	}
	e.locals[name] = ct
	if e.isSliceCType(ct) {
		e.sliceVars[name] = sliceElemFromCName(ct)
	}
	e.ind()
	e.emit(ct + " " + name + " = ")
	e.emitExpr(initExpr)
	e.emit(";\n")
}

// selectorFields collects the field names from a run of Selector nodes, or ok=false
// if any node is not a plain field selector (or the run is empty).
func (e *emitter) selectorFields(nodes []Node) (fields []string, ok bool) {
	fields, ok = e.selectorFieldsAll(nodes)
	return fields, ok && len(fields) != 0
}

// selectorFieldsAll is selectorFields without the non-empty requirement: an empty
// node list yields no fields and ok, so a chain may start directly with an index
// (`s[i].x`, where nothing is selected before the index).
func (e *emitter) selectorFieldsAll(nodes []Node) (fields []string, ok bool) {
	for _, n := range nodes {
		if n.sym != Selector {
			return nil, false
		}
		f := e.soleIdent(n.ast)
		if f == "" {
			return nil, false
		}
		fields = append(fields, f)
	}
	return fields, true
}

// chainFieldType walks a field chain from a starting C type, returning the type
// selected at the end. Used to validate a chain before any of it is emitted.
func (e *emitter) chainFieldType(ctype string, fields []string) (string, bool) {
	for _, f := range fields {
		var ok bool
		if ctype, ok = e.structFieldType(ctype, f); !ok {
			return "", false
		}
	}
	return ctype, true
}

// indexedContainer resolves what an index applies to: the C expression naming the
// element storage, the element C type, and the length to bounds-check against.
// With no leading field chain the base is a slice or array variable; with one it
// is a slice-typed struct field (`p.pts[i]`).
func (e *emitter) indexedContainer(base string, pre []string) (expr, elem, lenExpr string, ok bool) {
	if len(pre) == 0 {
		if el, ok := e.sliceElem(base); ok {
			return base + ".ptr", el, base + ".len", true
		}
		if a, ok := e.arrayVar(base); ok {
			return base, a.elem, a.bound, true
		}
		return "", "", "", false
	}
	// An array-typed field indexes its inline storage directly, bounded by the
	// declared extent; a slice-typed one goes through its header's backing pointer,
	// bounded by the runtime length.
	if a, ok := e.fieldArray(base, pre); ok {
		return e.fieldAccessC(base, pre), a.elem, a.bound, true
	}
	ct, ok := e.fieldType(base, pre)
	if !ok || !e.isSliceCType(ct) {
		return "", "", "", false
	}
	el, ok := e.sliceElemByName[ct]
	if !ok {
		return "", "", "", false
	}
	lv := e.fieldAccessC(base, pre)
	return lv + ".ptr", el, lv + ".len", true
}

// emitIndexSelect emits `<container>[i].f...` -- an indexed element followed by a
// field chain. The trailing selectors are emitted after the index rather than
// concatenated into the prefix, because once the index expression has been written
// the accumulated C text is no longer available as a string.
func (e *emitter) emitIndexSelect(expr, lenExpr string, low []int32, elem string, post []string) {
	e.emit(expr + "[")
	e.emitIndex(low, lenExpr)
	e.emit("]")
	ct := elem
	for _, f := range post {
		if e.isPointer(ct) {
			e.emit("->" + f)
		} else {
			e.emit("." + f)
		}
		ct, _ = e.structFieldType(ct, f) // validated by the caller via chainFieldType
	}
}

// assignTail classifies the PostfixOp closing an assignment statement: `++`, `--`,
// or `= expr`. For the first two suffix is the C operator and rhs is nil; for an
// assignment suffix is empty and rhs is the right-hand Expression. ok is false for
// any other PostfixOp -- a channel send, or a multi-target assignment -- which has
// its own path.
//
// It classifies without emitting, so a caller can reject an unsupported form
// before writing a partial statement.
func (e *emitter) assignTailOf(opNode Node) (assignTail, bool) {
	if opNode.sym != PostfixOp {
		return assignTail{}, false
	}
	op := slices.Collect(it(opNode.ast))
	if len(op) == 1 && op[0].sym == 0 {
		switch e.f.ch(op[0].tok) {
		case INC:
			return assignTail{op: "++"}, true
		case DEC:
			return assignTail{op: "--"}, true
		}
	}
	if len(op) != 2 || op[1].sym != Expression {
		return assignTail{}, false
	}
	if op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		return assignTail{op: "=", rhs: op[1].ast}, true
	}
	if op[0].sym == AssignOp {
		if tok, ok := e.soleToken(op[0].ast); ok {
			sym := e.f.ch(tok)
			if c, ok := cAssignOps[sym]; ok {
				return assignTail{op: c, rhs: op[1].ast, complement: sym == ANDNOT_ASSIGN}, true
			}
		}
	}
	return assignTail{}, false
}

// assignTail describes what follows an assignment target: the C operator, the
// right operand (nil for ++/--), and whether that operand must be complemented.
type assignTail struct {
	op         string
	rhs        []int32
	complement bool
}

// cAssignOps maps each compound assignment token to the C operator that applies
// it. C has no "&^=": Go's `x &^= y` clears in x every bit set in y, which is
// `x &= ~(y)`, so ANDNOT_ASSIGN maps to "&=" and complements its operand.
var cAssignOps = map[Symbol]string{
	ADD_ASSIGN:    "+=",
	SUB_ASSIGN:    "-=",
	MUL_ASSIGN:    "*=",
	QUO_ASSIGN:    "/=",
	REM_ASSIGN:    "%=",
	AND_ASSIGN:    "&=",
	OR_ASSIGN:     "|=",
	XOR_ASSIGN:    "^=",
	SHL_ASSIGN:    "<<=",
	SHR_ASSIGN:    ">>=",
	ANDNOT_ASSIGN: "&=",
}

// emitAssignTail writes the classified tail after a target has been emitted. The
// complemented operand is parenthesised, so `x &^= a | b` clears both bits rather
// than complementing only a.
func (e *emitter) emitAssignTail(t assignTail) {
	if t.rhs == nil {
		e.emit(t.op + ";\n") // "++" or "--"
		return
	}
	e.emit(" " + t.op + " ")
	if t.complement {
		e.emit("~(")
		e.emitExpr(t.rhs)
		e.emit(")")
	} else {
		e.emitExpr(t.rhs)
	}
	e.emit(";\n")
}

// emitIndexSelectAssign emits a write to a field of an indexed element,
// `s[i].x = v` or `p.pts[i].x = v`: the bounds-checked element access followed by
// the field chain. Only plain assignment is modelled -- no slice colon in the
// index, and no ++/-- on the selected field.
func (e *emitter) emitIndexSelectAssign(base, stars string, pre []string, indexAST []int32, post []string, opNode Node) {
	if stars != "" {
		// `*s[i].x = v` would deref the selected field, not the base; the parse puts
		// the star on the base, so the two readings differ. Not modelled.
		e.fail("a dereferenced indexed assignment target is not supported yet")
		return
	}
	if opNode.sym != PostfixOp {
		e.fail("unsupported assignment target")
		return
	}
	if _, _, isSlice := e.sliceParts(indexAST); isSlice {
		e.fail("slicing an assignment target is not supported yet")
		return
	}
	t, ok := e.assignTailOf(opNode)
	if !ok {
		e.fail("unsupported assignment form for an indexed element's field")
		return
	}
	expr, elem, lenExpr, ok := e.indexedContainer(base, pre)
	if !ok {
		e.fail("unsupported indexed assignment target")
		return
	}
	if _, ok := e.chainFieldType(elem, post); !ok {
		e.fail("unsupported field in an indexed assignment target")
		return
	}
	low, _, _ := e.sliceParts(indexAST)
	e.ind()
	e.emitIndexSelect(expr, lenExpr, low, elem, post)
	e.emitAssignTail(t)
}

// emitFieldIndexAssign emits a write to a slice-field element `b.data[i] = v`,
// through the field header's backing pointer and bounds-checked against its length.
// Only plain assignment is modelled (no slice colon, no ++/-- on the element).
func (e *emitter) emitFieldIndexAssign(base string, fields []string, index, opNode Node) {
	low, _, isSlice := e.sliceParts(index.ast)
	if isSlice {
		e.fail("slicing a slice-field target is not supported yet")
		return
	}
	t, ok := e.assignTailOf(opNode)
	if !ok {
		e.fail("unsupported assignment form for an indexed field element")
		return
	}
	expr, elem, lenExpr, ok := e.indexedContainer(base, fields)
	if !ok {
		e.fail("unsupported indexed field assignment target")
		return
	}
	e.ind()
	e.emitIndexSelect(expr, lenExpr, low, elem, nil)
	e.emitAssignTail(t)
}

// emitIndexAssign emits an indexed assignment `a[i] = v` or an indexed increment/
// decrement `a[i]++` / `a[i]--`. index is the Index node, opNode the PostfixOp.
func (e *emitter) emitIndexAssign(base string, index, opNode Node) {
	if opNode.sym != PostfixOp {
		e.fail("unsupported assignment target")
		return
	}
	var idx []int32
	for n := range it(index.ast) {
		if n.sym == Expression {
			idx = n.ast
		}
	}
	if idx == nil {
		e.fail("unsupported index target")
		return
	}
	// A slice element is addressed through its backing pointer; an array directly.
	// The index is bounds-checked against the container length.
	lhs := base
	lenExpr := ""
	if e.hasSliceVar(base) {
		lhs = base + ".ptr"
		lenExpr = base + ".len"
	} else if a, ok := e.arrayVar(base); ok {
		lenExpr = a.bound
	}
	t, ok := e.assignTailOf(opNode)
	if !ok {
		e.fail("unsupported assignment form for an indexed target")
		return
	}
	e.ind()
	e.emit(lhs + "[")
	e.emitIndex(idx, lenExpr)
	e.emit("]")
	e.emitAssignTail(t)
}

// emitMultiAssign emits a destructuring assignment `a, b = f()` or `a, b := f()`
// (any target may be the blank identifier). C has no multiple assignment, so the
// multi-result call's struct is bound to a temporary and each target reads its
// field: a `:=` target is declared with its result type, a `=` target is assigned,
// and a blank target is skipped. first is the head identifier; op holds the
// PostfixOp children (the remaining LhsItem targets, the operator, and the call).
func (e *emitter) emitMultiAssign(first string, op []Node) {
	targets := []string{first}
	define := false
	var rhs []int32
	for _, n := range op {
		switch n.sym {
		case LhsItem:
			id := e.lhsItemIdent(n.ast)
			if id == "" {
				e.fail("only simple variable targets are supported in multiple assignment")
				return
			}
			targets = append(targets, id)
		case Expression:
			rhs = n.ast
		case 0:
			if ch := e.f.ch(n.tok); ch == ASSIGN || ch == DEFINE {
				define = ch == DEFINE
			}
		}
	}
	e.emitDestructure(targets, define, rhs)
}

// emitDestructure lowers a multi-result call bound to several targets, shared by
// `a, b = f()` / `a, b := f()` and the var form `var a, b T = f()`. C has no
// multiple assignment, so the call's result struct is bound to a temporary and each
// target reads its field: a defined target is declared with its result type, an
// assigned target is assigned, and a blank target is skipped. rhs is the call
// expression; define selects declaration (`:=` / `var`) over plain assignment.
func (e *emitter) emitDestructure(targets []string, define bool, rhs []int32) {
	callee, suffix, ok := e.directCall(rhs)
	if !ok {
		e.fail("multiple assignment requires a single function call on the right-hand side")
		return
	}
	if callee == "append" && len(suffix) == 1 && suffix[0].sym == CallSuffix {
		// Two-result append: s, ok = append(s, x) -- the ok form, no trap.
		e.emitTryAppend(targets, define, suffix[0].ast)
		return
	}
	resTypes, ok := e.funcRet[callee]
	if !ok || len(resTypes) != len(targets) {
		e.fail("multiple-assignment target/result count mismatch")
		return
	}
	tmp := e.newTmp()
	e.ind()
	e.emit(e.retStructName(callee) + " " + tmp + " = ")
	if !e.emitCallExpr(callee, suffix) {
		e.fail("unsupported call on the right-hand side of a multiple assignment")
		return
	}
	e.emit(";\n")
	for i, tgt := range targets {
		if tgt == "_" {
			continue
		}
		e.ind()
		field := fmt.Sprintf("%s._%d", tmp, i)
		if define {
			e.locals[tgt] = resTypes[i]
			e.emit(resTypes[i] + " " + tgt + " = " + field + ";\n")
		} else {
			e.emit(tgt + " = " + field + ";\n")
		}
	}
}

// lhsItemIdent returns the single bare identifier of an LhsItem
// (LhsItem = AssignHead { Selector | Index }), or "" when the item carries a
// selector or index — an unsupported multiple-assignment target.
func (e *emitter) lhsItemIdent(ast []int32) string {
	nodes := slices.Collect(it(ast))
	if len(nodes) != 1 || nodes[0].sym != AssignHead {
		return ""
	}
	return e.soleIdent(nodes[0].ast)
}

// directCall reports the callee name and call suffix of an expression that is
// exactly a direct call `f(args)`, descending the single-child Expression/
// SimpleExpr/Term/UnaryExpr wrappers to a Factor whose only suffix is a CallSuffix.
// The suffix lets the caller re-emit the call through emitCallExpr, which — unlike
// emitExpr — does not reject a multi-result callee.
func (e *emitter) directCall(ast []int32) (recv string, suffix []Node, ok bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 {
		switch nodes[0].sym {
		case Expression, SimpleExpr, Term, UnaryExpr:
			nodes = slices.Collect(it(nodes[0].ast))
		case Factor:
			kids := slices.Collect(it(nodes[0].ast))
			if r, s, ok := e.factorCall(kids); ok && len(s) == 1 && s[0].sym == CallSuffix {
				return r, s, true
			}
			return "", nil, false
		default:
			return "", nil, false
		}
	}
	return "", nil, false
}

// newTmp returns a fresh generated temporary name, unique within the function.
func (e *emitter) newTmp() string {
	s := "_ogo_t" + strconv.Itoa(e.tmp)
	e.tmp++
	return s
}

// emitDiscard emits `(void)<expr>;` — an expression evaluated for its side effects
// with its value dropped, the C rendering of a blank-identifier discard. emitExpr
// already parenthesizes binary operands, so no extra parentheses are needed for
// the cast to bind correctly.
func (e *emitter) emitDiscard(expr []int32) {
	e.ind()
	e.emit("(void)")
	e.emitExpr(expr)
	e.emit(";\n")
}

func (e *emitter) emitCallArgs(callSuffix []int32) {
	first := true
	for _, arg := range e.callArgExprs(callSuffix) {
		if !first {
			e.emit(", ")
		}
		first = false
		e.emitExpr(arg.ast)
	}
}

// callArgExprs returns the argument Expression nodes of a CallSuffix.
func (e *emitter) callArgExprs(callSuffix []int32) []Node {
	var args []Node
	for n := range it(callSuffix) {
		if n.sym != ArgumentList {
			continue
		}
		for a := range it(n.ast) {
			if a.sym == Expression {
				args = append(args, a)
			}
		}
	}
	return args
}

// soleIdent returns the single identifier of a subtree (an AssignHead bare name
// or a Selector's field), or "" if the shape is richer.
func (e *emitter) soleIdent(ast []int32) string {
	name := ""
	for n := range it(ast) {
		if n.sym != 0 || e.f.ch(n.tok) != IDENT {
			continue
		}
		if name != "" {
			return ""
		}
		name = e.src(n.tok)
	}
	return name
}

// factorCall recognises a Factor of the form `identifier FactorSuffix` whose
// suffix is a call (a direct call `f(args)` or a qualified call `pkg.F(args)`),
// returning the head identifier and the suffix's child nodes. ok is false for a
// bare identifier, a literal, or a non-call suffix (field selection or index),
// which are handled elsewhere or not yet supported.
func (e *emitter) factorCall(kids []Node) (recv string, suffix []Node, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	suffix = slices.Collect(it(kids[1].ast))
	if !containsSym(suffix, CallSuffix) {
		return "", nil, false
	}
	return e.src(kids[0].tok), suffix, true
}

// isStruct reports whether a C type name denotes a modelled struct type.
func (e *emitter) isStruct(ctype string) bool { _, ok := e.structs[ctype]; return ok }

// isSliceCType reports whether a C type name is a slice header type (ogo_slice_<T>).
func (e *emitter) isSliceCType(ctype string) bool { return strings.HasPrefix(ctype, sliceTypePrefix) }

// needSlice records that a slice `[]elem` is used, so its header typedef is emitted.
func (e *emitter) needSlice(elem string) {
	e.sliceElems[elem] = true
	e.sliceElemByName[sliceCName(elem)] = elem
}

// sliceElem returns a slice variable's element C type, from the local then the
// package slice environment.
func (e *emitter) sliceElem(name string) (string, bool) {
	if el, ok := e.sliceVars[name]; ok {
		return el, true
	}
	el, ok := e.globalSliceVars[name]
	return el, ok
}

// factorFieldAccess recognises a Factor that is a field access `base.f` (or a
// chain `base.f.g`) -- an identifier followed by a FactorSuffix of selectors only,
// no index or call -- returning the base name and the selected field names.
func (e *emitter) factorFieldAccess(kids []Node) (base string, fields []string, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	for _, n := range slices.Collect(it(kids[1].ast)) {
		if n.sym != Selector {
			return "", nil, false
		}
		fld := e.soleIdent(n.ast)
		if fld == "" {
			return "", nil, false
		}
		fields = append(fields, fld)
	}
	if len(fields) == 0 {
		return "", nil, false
	}
	return e.src(kids[0].tok), fields, true
}

// isPointer reports whether a C type is a pointer (spelled "T*").
func (e *emitter) isPointer(ctype string) bool { return strings.HasSuffix(ctype, "*") }

// elemType strips one pointer level from a C type ("T*" -> "T").
func (e *emitter) elemType(ctype string) string { return strings.TrimSuffix(ctype, "*") }

// structFieldType returns the C type of a struct's field. ctype may be a struct
// value or a pointer to one (a field access auto-dereferences, like Go's).
func (e *emitter) structFieldType(ctype, field string) (string, bool) {
	for _, fld := range e.structs[e.elemType(ctype)] {
		if fld.name == field {
			if fld.bound != "" {
				// An array field has no single C value type -- C cannot assign or
				// pass one by value -- so it is not reportable here. Indexing reaches
				// it through structFieldArray instead, and any path that wanted a
				// plain value fails honestly rather than emitting an array where a
				// scalar was expected.
				return "", false
			}
			return fld.ctype, true
		}
	}
	return "", false
}

// structFieldArray returns a struct field's array dimension, for a field declared
// with a fixed extent (`data [3]int`). It is the array counterpart of
// structFieldType, which deliberately refuses such a field.
func (e *emitter) structFieldArray(ctype, field string) (arrDim, bool) {
	for _, fld := range e.structs[e.elemType(ctype)] {
		if fld.name == field && fld.bound != "" {
			return arrDim{elem: fld.ctype, bound: fld.bound}, true
		}
	}
	return arrDim{}, false
}

// fieldArray resolves an array-typed field at the end of an access chain
// `base.f.g...`, returning its element type and bound.
func (e *emitter) fieldArray(base string, fields []string) (arrDim, bool) {
	if len(fields) == 0 {
		return arrDim{}, false
	}
	ctype, ok := e.locals[base]
	if !ok {
		return arrDim{}, false
	}
	for _, f := range fields[:len(fields)-1] {
		if ctype, ok = e.structFieldType(ctype, f); !ok {
			return arrDim{}, false
		}
	}
	return e.structFieldArray(ctype, fields[len(fields)-1])
}

// fieldType resolves the C type of a field access chain `base.f.g...` from the
// local type environment: base's (possibly pointer) struct type, then each field's
// type in turn.
func (e *emitter) fieldType(base string, fields []string) (string, bool) {
	ctype, ok := e.locals[base]
	if !ok {
		return "", false
	}
	for _, f := range fields {
		if ctype, ok = e.structFieldType(ctype, f); !ok {
			return "", false
		}
	}
	return ctype, true
}

// fieldAccessC renders a field access chain `base.f.g...` in C, choosing "->" for
// each pointer step (an auto-dereferenced Go field access) and "." otherwise.
func (e *emitter) fieldAccessC(base string, fields []string) string {
	ctype := e.locals[base]
	s := base
	for _, f := range fields {
		if e.isPointer(ctype) {
			s += "->"
		} else {
			s += "."
		}
		s += f
		ctype, _ = e.structFieldType(ctype, f)
	}
	return s
}

// inferCType determines the C type of an expression for a `x := expr` short
// declaration, from the current type environment (locals and funcRet). ok is
// false when the type is outside the modelled subset, so the caller fails
// honestly rather than emitting a wrongly-typed variable.
func (e *emitter) inferCType(ast []int32) (string, bool) {
	return e.inferNodes(slices.Collect(it(ast)))
}

// inferNodes types one expression level (the children of an Expression/SimpleExpr/
// Term/UnaryExpr). A relational operator makes the value a bool (C int); an
// arithmetic operator makes the type that of the first operand; a unary operator
// is transparent. Otherwise the level is a single operand, typed by inferNode.
func (e *emitter) inferNodes(nodes []Node) (string, bool) {
	for _, n := range nodes {
		if n.sym == RelOp {
			return "int", true // a comparison yields bool, which is C int
		}
	}
	for _, n := range nodes {
		switch n.sym {
		case AddOp, MulOp, UnaryOp:
			continue // an operator; the type comes from the operand(s)
		case 0:
			switch e.f.ch(n.tok) {
			case SUB, ADD, NOT, XOR, MUL, AND:
				continue // a prefix operator token; skip to its operand
			}
		}
		return e.inferNode(n)
	}
	return "", false
}

// inferNode types a single expression node: a wrapper level recurses, a
// parenthesised expression unwraps, a call takes its result type, an identifier
// its declared type, and an integer literal is int.
func (e *emitter) inferNode(n Node) (string, bool) {
	switch n.sym {
	case Expression, SimpleExpr, Term:
		return e.inferNodes(slices.Collect(it(n.ast)))
	case UnaryExpr, Factor:
		kids := slices.Collect(it(n.ast))
		if len(kids) == 3 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == LPAREN {
			return e.inferNode(kids[1])
		}
		// Address-of `&x` adds a pointer level; deref `*p` removes one.
		if n.sym == UnaryExpr && len(kids) >= 2 && kids[0].sym == UnaryOp {
			if tok, ok := e.unaryOpTok(kids[0].ast); ok {
				switch e.f.ch(tok) {
				case AND:
					if t, ok := e.inferNode(kids[len(kids)-1]); ok {
						return t + "*", true
					}
					return "", false
				case MUL:
					if t, ok := e.inferNode(kids[len(kids)-1]); ok && e.isPointer(t) {
						return e.elemType(t), true
					}
					return "", false
				}
			}
		}
		if n.sym == Factor {
			if recv, suffix, ok := e.factorCall(kids); ok {
				return e.callResultCType(recv, suffix)
			}
			if base, fields, ok := e.factorFieldAccess(kids); ok {
				return e.fieldType(base, fields)
			}
			// `s[i].x` / `p.pts[i].x` -- the element's selected field type.
			if base, pre, indexAST, post, ok := e.factorIndexSelect(kids); ok {
				if _, _, isSlice := e.sliceParts(indexAST); !isSlice {
					if _, elem, _, ok := e.indexedContainer(base, pre); ok {
						return e.chainFieldType(elem, post)
					}
				}
				return "", false
			}
			// `base.f[i]` -- indexing a slice struct field. Checked before the
			// plain-index and fallback paths: factorFieldAccess rejects the trailing
			// Index and factorIndex rejects the leading Selector, so without this the
			// level fell through to inferNodes(kids), which types a Factor by its
			// first identifier and so yielded the *base struct's* type (`Buf` for
			// `b.data[0]`, not `int`) -- invalid C at the declaration it feeds.
			if base, fields, indexAST, ok := e.factorFieldIndex(kids); ok {
				if _, _, isSlice := e.sliceParts(indexAST); isSlice {
					// Re-slicing a field yields a slice header: the field's own type
					// for a slice field, one over the element type for an array field.
					if src, ok := e.sliceableField(base, fields); ok {
						return src.cname, true
					}
					return "", false
				}
				if _, elem, _, ok := e.indexedContainer(base, fields); ok {
					return elem, true
				}
				return "", false
			}
			if base, indexAST, ok := e.factorIndex(kids); ok {
				if _, _, isSlice := e.sliceParts(indexAST); isSlice {
					// Slicing a string yields a string; slicing an array or a slice
					// yields the corresponding slice header type.
					if e.isStringVarName(base) {
						return cString, true
					}
					if a, ok := e.arrayVar(base); ok {
						return sliceCName(a.elem), true
					}
					if elem, ok := e.sliceElem(base); ok {
						return sliceCName(elem), true
					}
					return "", false
				}
				// A plain index yields the element type of an array or a slice.
				if a, ok := e.arrayVar(base); ok {
					return a.elem, true
				}
				if elem, ok := e.sliceElem(base); ok {
					return elem, true
				}
				return "", false
			}
		}
		return e.inferNodes(kids)
	case 0:
		switch e.f.ch(n.tok) {
		case INT:
			return "int", true
		case STRING:
			return cString, true
		case IDENT:
			nm := e.src(n.tok)
			if nm == "true" || nm == "false" {
				return "int", true // the predeclared bool constants; bool is C int
			}
			if ct, ok := e.locals[nm]; ok {
				return ct, true
			}
			ct, ok := e.globals[nm]
			return ct, ok
		}
	}
	return "", false
}

// callResultCType returns the C result type of a call in expression position: a
// user function's recorded result type, or int for a p2 intrinsic (propeller2.h
// intrinsics all return int).
func (e *emitter) callResultCType(recv string, suffix []Node) (string, bool) {
	switch {
	case len(suffix) == 1 && suffix[0].sym == CallSuffix:
		if recv == "len" || recv == "cap" {
			return "int", true // the builtins len and cap return int
		}
		if recv == "append" {
			// append returns a slice of its first argument's element type.
			args := e.callArgExprs(suffix[0].ast)
			if len(args) >= 1 {
				if base, ok := e.exprIdent(args[0].ast); ok {
					if elem, ok := e.sliceElem(base); ok {
						return sliceCName(elem), true
					}
				}
			}
			return "", false
		}
		// Only a single-result call is a usable single value; a multi-result call
		// belongs in a destructuring assignment (emitMultiAssign), not here.
		if rts, ok := e.funcRet[recv]; ok && len(rts) == 1 {
			return rts[0], true
		}
		return "", false
	case len(suffix) == 2 && suffix[0].sym == Selector && suffix[1].sym == CallSuffix:
		// A single-result method call `x.M()` carries its recorded result type,
		// keyed by the receiver type's mangled method name.
		if rct, ok := e.varType(recv); ok && e.isUserType(methodBaseType(rct)) {
			method := e.soleIdent(suffix[0].ast)
			if rts, ok := e.funcRet[methodCName(methodBaseType(rct), method)]; ok && len(rts) == 1 {
				return rts[0], true
			}
			return "", false
		}
		if recv == "p2" {
			return "int", true
		}
	}
	return "", false
}

// emitExpr emits a value expression. Binary operators (Expression/SimpleExpr/
// Term) are parenthesized so the OctoGo parse grouping is preserved even where C
// operator precedence differs (notably Go binds << tighter than C does).
// Integer-literal text is normalized for C by normalizeIntLit.
func (e *emitter) emitExpr(ast []int32) {
	for n := range it(ast) {
		e.emitExprNode(n)
	}
}

func (e *emitter) emitExprNode(n Node) {
	switch n.sym {
	case Expression, SimpleExpr:
		kids := slices.Collect(it(n.ast))
		if len(kids) == 1 {
			e.emitExprNode(kids[0])
			return
		}
		e.emit("(")
		for _, c := range kids {
			e.emitExprNode(c)
		}
		e.emit(")")
	case Term:
		// Like SimpleExpr, but a "/" or "%" divisor is guarded against zero:
		// `a / b` -> `(a / ogo_nonzero(b))`. A constant divisor needs no guard.
		kids := slices.Collect(it(n.ast))
		if len(kids) == 1 {
			e.emitExprNode(kids[0])
			return
		}
		e.emit("(")
		guardNext := false
		for _, c := range kids {
			switch {
			case c.sym == MulOp:
				op := e.opText(c.ast)
				e.emit(" " + op + " ")
				guardNext = e.checks && (op == "/" || op == "%")
			case guardNext && !e.isIntLiteral(c):
				e.needPanic()
				e.usesNonzero = true
				e.emit("ogo_nonzero(")
				e.emitExprNode(c)
				e.emit(")")
				guardNext = false
			default:
				e.emitExprNode(c)
				guardNext = false
			}
		}
		e.emit(")")
	case UnaryExpr, Factor:
		kids := slices.Collect(it(n.ast))
		if len(kids) == 3 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == LPAREN {
			e.emit("(")
			e.emitExprNode(kids[1])
			e.emit(")")
			return
		}
		if n.sym == Factor {
			if recv, suffix, ok := e.factorCall(kids); ok {
				// A multi-result call yields no single value; it is only valid in a
				// destructuring assignment (emitMultiAssign), not as an operand.
				if len(suffix) == 1 && suffix[0].sym == CallSuffix && len(e.funcRet[recv]) > 1 {
					e.fail("a multi-value call cannot be used as a single value")
					return
				}
				if !e.emitCallExpr(recv, suffix) {
					e.fail("unsupported call in expression")
				}
				return
			}
			// `s[i].x` / `p.pts[i].x` -- index a container, then select from the
			// element. Checked ahead of the index-only shapes, which cannot match a
			// trailing selector anyway.
			if base, pre, indexAST, post, ok := e.factorIndexSelect(kids); ok {
				if low, _, isSlice := e.sliceParts(indexAST); !isSlice {
					if expr, elem, lenExpr, ok := e.indexedContainer(base, pre); ok {
						if _, ok := e.chainFieldType(elem, post); ok {
							e.emitIndexSelect(expr, lenExpr, low, elem, post)
							return
						}
					}
				}
			}
			if base, fields, indexAST, ok := e.factorFieldIndex(kids); ok {
				// A struct field indexed directly, `b.data[i]`: a slice field reads
				// through its header's backing pointer bounded by len, an array field
				// its inline storage bounded by the declared extent. indexedContainer
				// resolves which, so both read the same way here.
				low, high, isSlice := e.sliceParts(indexAST)
				if isSlice {
					// Re-slicing a struct field, `b.data[1:3]`.
					if src, ok := e.sliceableField(base, fields); ok {
						e.emitSliceExpr(src, low, high)
						return
					}
				} else {
					if expr, elem, lenExpr, ok := e.indexedContainer(base, fields); ok {
						e.emitIndexSelect(expr, lenExpr, low, elem, nil)
						return
					}
				}
			}
			if base, fields, ok := e.factorFieldAccess(kids); ok {
				e.emit(e.fieldAccessC(base, fields))
				return
			}
			if base, indexAST, ok := e.factorIndex(kids); ok {
				low, high, isSlice := e.sliceParts(indexAST)
				if isSlice {
					src, ok := e.sliceableVar(base)
					if !ok {
						e.fail("only string, array and slice slicing is supported yet")
						return
					}
					e.emitSliceExpr(src, low, high)
					return
				}
				// A slice is indexed through its backing pointer; an array (or string)
				// directly. The index is bounds-checked against the container length.
				lenExpr := ""
				if e.hasSliceVar(base) {
					e.emit(base + ".ptr[")
					lenExpr = base + ".len"
				} else {
					e.emit(base + "[")
					if a, ok := e.arrayVar(base); ok {
						lenExpr = a.bound
					}
				}
				e.emitIndex(low, lenExpr)
				e.emit("]")
				return
			}
		}
		for _, c := range kids {
			e.emitExprNode(c)
		}
	case AddOp, MulOp, RelOp:
		e.emit(" " + e.opText(n.ast) + " ")
	case UnaryOp:
		// A prefix operator: `-`, `!`, `&` (address-of), `*` (deref), `^` -> `~`.
		if tok, ok := e.unaryOpTok(n.ast); ok {
			e.emitOperandToken(tok)
		}
	case 0:
		e.emitOperandToken(n.tok)
	default:
		e.fail("unsupported expression node %v", n.sym)
	}
}

// unaryOpTok returns the operator token of a UnaryOp node.
func (e *emitter) unaryOpTok(ast []int32) (int32, bool) {
	for n := range it(ast) {
		if n.sym == 0 {
			return n.tok, true
		}
	}
	return 0, false
}

// opText returns the operator terminal's text from an AddOp/MulOp/RelOp node.
// The OctoGo operators here all coincide with their C spellings.
func (e *emitter) opText(ast []int32) string {
	for n := range it(ast) {
		if n.sym == 0 {
			return e.src(n.tok)
		}
	}
	return ""
}

func (e *emitter) emitOperandToken(tok int32) {
	switch ch := e.f.ch(tok); ch {
	case INT:
		e.emit(normalizeIntLit(e.src(tok)))
	case IDENT:
		// The predeclared bool constants have no C keyword here (bool is int); emit
		// their integer values. Any other identifier is a name reference.
		switch s := e.src(tok); s {
		case "true":
			e.emit("1")
		case "false":
			e.emit("0")
		default:
			e.emit(s)
		}
	case STRING:
		e.emitStringLit(tok)
	case SUB, ADD, NOT, AND, MUL:
		e.emit(e.src(tok)) // unary -, +, !, & (address-of), * (deref) prefix
	case XOR:
		e.emit("~") // Go unary ^ is bitwise complement
	default:
		e.fail("unsupported operand %v", ch)
	}
}

// emitStringLit emits a string literal as an ogo_string { pointer, length }
// header. In a static initializer a brace `{"s", n}` is required (a compound
// literal is not a constant expression there); elsewhere the compound literal
// `(ogo_string){"s", n}` is used. n is the decoded byte length (escapes counted as
// one byte). The literal text emits verbatim -- Go and C share the common escapes.
func (e *emitter) emitStringLit(tok int32) {
	src := e.src(tok)
	if len(src) != 0 && src[0] == '`' {
		e.fail("raw string literals are not supported yet")
		return
	}
	decoded, err := strconv.Unquote(src)
	if err != nil {
		e.fail("invalid string literal %s", src)
		return
	}
	e.usesString = true
	body := src + ", " + strconv.Itoa(len(decoded))
	if e.declInit {
		e.emit("{" + body + "}")
	} else {
		e.emit("(" + cString + "){" + body + "}")
	}
}

// normalizeIntLit rewrites an OctoGo integer literal to a form the C backend
// accepts. Go's explicit-octal prefix "0o"/"0O" is not valid C — C spells octal
// with a bare leading "0" (0o17 -> 017) — so it is converted. Underscores (digit
// separators) are stripped as well: the flexcc backend happens to accept them,
// but removing them keeps the emitted C independent of that leniency. Decimal,
// hex ("0x"/"0X"), binary ("0b"/"0B", accepted by flexcc) and Go's legacy
// leading-zero octal are already valid C and pass through, so a hex or binary
// literal keeps its base (readability matters for pin masks).
func normalizeIntLit(src string) string {
	s := strings.ReplaceAll(src, "_", "")
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'o' || s[1] == 'O') {
		s = "0" + s[2:]
	}
	return s
}

func containsSym(nodes []Node, sym Symbol) bool {
	for _, n := range nodes {
		if n.sym == sym {
			return true
		}
	}
	return false
}
