// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

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
	"int": "int", "uint": "unsigned", "bool": cBool,
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

// cBool is the C type of an OctoGo bool: C99 _Bool. A distinct type, not int, so
// the emitter can tell a bool from an integer -- which is what lets it print
// true/false -- and so a bool packs to one byte in a struct or array. _Bool also
// normalizes any nonzero to 1 on store, matching Go's strict {false, true}. The
// checker forbids arithmetic on bool, so nothing relies on it being int.
const cBool = "_Bool"

const stringTypedef = "typedef struct { const char* str; int len; } ogo_string;\n"

// stringHelpers print a string header's exact bytes (a slice need not be
// null-terminated, so %.*s, not %s).
const stringHelpers = "static inline void ogo_print_str(ogo_string s) { printf(\"%.*s\", s.len, s.str); }\n" +
	"static inline void ogo_println_str(ogo_string s) { printf(\"%.*s\\n\", s.len, s.str); }\n"

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

// chanTypePrefix leads the C typedef name of an OctoGo channel `chan T`. A channel
// is a rendezvous cell in Hub RAM guarded by one P2 hardware lock: a sender
// deposits into the cell and waits for a receiver to take it, so the two meet in
// lock step and no buffer is needed. `taken` counts consumed values, which is how a
// sender identifies its own handoff when several senders share the channel --
// watching `full` alone would let another sender's deposit be mistaken for its own.
const chanTypePrefix = "ogo_chan_"

// chanCName is the C type name of `chan elem`.
func chanCName(elem string) string { return chanTypePrefix + sanitizeElem(elem) }

// chanCellCName is the cell a channel points at. A channel is a reference type in
// Go, and must be one here: passing a by-value cell to a goroutine would hand it a
// copy, and the two would rendezvous with themselves.
func chanCellCName(elem string) string { return chanTypePrefix + sanitizeElem(elem) + "_cell" }

// chanInitCName, chanSendCName and chanRecvCName name the per-element runtime
// helpers.
func chanInitCName(elem string) string { return "ogo_chan_init_" + sanitizeElem(elem) }

func chanSendCName(elem string) string { return "ogo_chan_send_" + sanitizeElem(elem) }

func chanRecvCName(elem string) string { return "ogo_chan_recv_" + sanitizeElem(elem) }

// chanTryRecvCName names the non-blocking receive a select polls with.
func chanTryRecvCName(elem string) string { return "ogo_chan_tryrecv_" + sanitizeElem(elem) }

// recvOperand recognises a receive expression `<-ch`, returning the channel's
// element C type and the C name of the channel. Shared by emission and inference so
// the two cannot disagree about what a receive yields.
func (e *emitter) recvOperand(n Node, kids []Node) (elem, base string, ok bool) {
	if n.sym != UnaryExpr || len(kids) != 2 || kids[0].sym != UnaryOp {
		return "", "", false
	}
	tok, ok := e.unaryOpTok(kids[0].ast)
	if !ok || e.f.ch(tok) != ARROW {
		return "", "", false
	}
	base, ok = e.exprIdent(kids[1].ast)
	if !ok {
		return "", "", false
	}
	ct, ok := e.varType(base)
	if !ok || !e.isChanCType(ct) {
		return "", "", false
	}
	return e.chanElemByName[ct], base, true
}

// goSite is one `go` statement: the callee's C name and the C types of its
// arguments. Each gets a struct to marshal the arguments through and a trampoline
// matching _cogstart's `void (*)(void *)` signature.
type goSite struct {
	callee string
	args   []string
	id     int
}

// goArgsCName and goTrampolineCName name a site's generated struct and trampoline.
func goArgsCName(id int) string { return fmt.Sprintf("ogo_go_args%d", id) }

func goTrampolineCName(id int) string { return fmt.Sprintf("ogo_go%d", id) }

// ogoCogPool is the goroutine slot pool. A goroutine needs a stack and somewhere
// to marshal its arguments, both of which must outlive the `go` statement -- the
// launched cog reads them asynchronously -- so neither can be a local of the
// launching function.
//
// The pool is sized to the hardware: 8 cogs less the one running main. That makes
// "out of slots" and "out of cogs" the same condition, and bounds the whole thing
// statically, with no allocator. It also makes `go` inside a loop safe, which is
// why it need not be rejected the way `defer` in a loop is: defer's problem was
// unbounded storage in the current frame, while this is bounded by the silicon.
// A slot is only recycled on two signals together: the goroutine reached the end
// of the trampoline (done), and _cogchk confirms its cog has stopped. Neither
// alone is enough. done cannot be trusted by itself because the goroutine sets it
// while still executing on the slot's stack, with the return through _cogstart's
// epilogue ahead of it -- handing that stack to a new cog wedges both. _cogchk
// alone would read a slot that has not started yet as idle, which is why cog
// stays -1 from claim until _cogstart returns an id. A never-used slot is always
// preferred, so recycling only happens once all 7 have been handed out.
const ogoCogPool = `#define OGO_COGS 8
#define OGO_STACK_LONGS 256
typedef struct { int used; int done; int cog; long args[OGO_ARG_LONGS]; long stack[OGO_STACK_LONGS]; } ogo_cog_slot;
static ogo_cog_slot ogo_cog_pool[OGO_COGS - 1];
static int ogo_cog_lock = -1;
static int ogo_cog_claim(void) {
	int got = -1;
	if (ogo_cog_lock < 0) {
		// The first claim is always main's: another cog can only be running
		// because a spawn already came through here, so this races nothing.
		ogo_cog_lock = _locknew();
		if (ogo_cog_lock < 0) {
			ogo_panic("out of hardware locks");
		}
	}
	while (!_locktry(ogo_cog_lock)) { // a goroutine may itself spawn one
		_waitx(1);
	}
	for (int i = 0; i < OGO_COGS - 1; i++) { // a slot no goroutine has ever used
		if (!ogo_cog_pool[i].used) {
			got = i;
			break;
		}
	}
	for (int i = 0; got < 0 && i < OGO_COGS - 1; i++) { // else recycle a finished one
		if (ogo_cog_pool[i].done && ogo_cog_pool[i].cog >= 0 && !_cogchk(ogo_cog_pool[i].cog)) {
			got = i;
		}
	}
	if (got >= 0) {
		ogo_cog_pool[got].used = 1;
		ogo_cog_pool[got].done = 0;
		ogo_cog_pool[got].cog = -1;
	}
	_lockrel(ogo_cog_lock);
	return got;
}
static void ogo_cog_release(int slot) { ogo_cog_pool[slot].used = 0; }
static void ogo_cog_done(int slot) { ogo_cog_pool[slot].done = 1; }
`

// emitGo emits a `go` statement: claim a pool slot, marshal the arguments into it,
// and hand the trampoline, the argument block and the slot's stack to _cogstart.
// Exceeding the cogs panics at runtime, which is what the spec prescribes.
func (e *emitter) emitGo(nodes []Node) {
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
	callee := e.soleIdent(head.ast)
	if callee == "" || len(suffix) != 1 || suffix[0].sym != CallSuffix {
		e.fail("only `go f(args)` on a plain function is supported yet")
		return
	}
	if _, ok := e.funcRet[callee]; !ok {
		e.fail("only `go f(args)` on a package function is supported yet")
		return
	}
	site := goSite{callee: callee, id: len(e.goSites)}
	args := e.callArgExprs(suffix[0].ast)
	for _, a := range args {
		ct, ok := e.inferCType(a.ast)
		if !ok {
			e.fail("cannot infer the type of a go argument")
			return
		}
		site.args = append(site.args, ct)
	}
	e.goSites = append(e.goSites, site)
	e.needPanic()
	e.includes["propeller2.h"] = true

	slot := e.newTmp()
	ap := e.newTmp()
	e.ind()
	e.emit("{\n")
	e.indent++
	e.ind()
	e.emit("int " + slot + " = ogo_cog_claim();\n")
	e.ind()
	e.emit("if (" + slot + " < 0) { ogo_panic(\"out of cogs\"); }\n")
	e.ind()
	e.emit(goArgsCName(site.id) + "* " + ap + " = (void*)ogo_cog_pool[" + slot + "].args;\n")
	e.ind()
	e.emit(ap + "->slot = " + slot + ";\n")
	for i, a := range args {
		e.ind()
		e.emit(fmt.Sprintf("%s->a%d = ", ap, i))
		e.emitExpr(a.ast)
		e.emit(";\n")
	}
	e.ind()
	e.emit("ogo_cog_pool[" + slot + "].cog = _cogstart_C(" + goTrampolineCName(site.id) + ", " + ap +
		", ogo_cog_pool[" + slot + "].stack, sizeof ogo_cog_pool[" + slot + "].stack);\n")
	e.ind()
	e.emit("if (ogo_cog_pool[" + slot + "].cog < 0) {\n")
	e.indent++
	e.ind()
	e.emit("ogo_cog_release(" + slot + ");\n")
	e.ind()
	e.emit("ogo_panic(\"out of cogs\");\n")
	e.indent--
	e.ind()
	e.emit("}\n")
	e.indent--
	e.ind()
	e.emit("}\n")
}

// goDefs renders the argument struct and trampoline for every launched goroutine,
// plus the pool sized to the widest argument block. The trampoline releases the
// slot when the goroutine returns, which is where the cog is freed too.
func (e *emitter) goDefs() string {
	if len(e.goSites) == 0 {
		return ""
	}
	var b strings.Builder
	widest := 1 // the slot field alone
	for _, s := range e.goSites {
		fmt.Fprintf(&b, "typedef struct { int slot;")
		for i, a := range s.args {
			fmt.Fprintf(&b, " %s a%d;", a, i)
		}
		fmt.Fprintf(&b, " } %s;\n", goArgsCName(s.id))
		if n := 1 + len(s.args); n > widest {
			widest = n
		}
	}
	for _, s := range e.goSites {
		fmt.Fprintf(&b, "static void %s(void* p) {\n\t%s* a = p;\n\t%s(",
			goTrampolineCName(s.id), goArgsCName(s.id), s.callee)
		for i := range s.args {
			if i != 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "a->a%d", i)
		}
		// Not ogo_cog_release: the goroutine is still on this slot's stack
		// here, with the return through _cogstart's epilogue ahead of it. done
		// only makes the slot a recycling candidate; ogo_cog_claim still waits
		// for _cogchk to confirm the cog stopped before reusing the stack.
		b.WriteString(");\n\togo_cog_done(a->slot);\n}\n")
	}
	// The argument block is sized in longs to keep it aligned for any member.
	return fmt.Sprintf("#define OGO_ARG_LONGS %d\n", widest) + ogoCogPool + b.String()
}

// selectCase is one CommClause of a select: the channel polled, where its value
// lands, and the statements to run. def marks the default clause, which has no
// channel.
type selectCase struct {
	def     bool
	ch      string // channel variable
	elem    string // its element C type
	target  string // variable receiving the value, empty for a bare `case <-ch:`
	declare bool   // ":=", so the target is introduced in the clause
	body    []Node
}

// emitSelect emits a select as a poll over each channel's non-blocking receive, in
// clause order, which is the lowering the spec prescribes.
//
// With a default clause the poll runs once and falls through to the default, so it
// never blocks. Without one it repeats, yielding with _waitx(1) between rounds: a
// cog cannot sleep, and spinning on the Hub bus without yielding would starve the
// cogs doing real work.
//
// Send clauses are not modelled. An unbuffered send completes only once a receiver
// has taken the value, so a non-blocking send would have to report a rendezvous
// that has not happened yet; that needs a two-phase handshake the cell does not
// carry. Receive and default are what the spec's own example uses.
func (e *emitter) emitSelect(ast []int32) {
	var cases []selectCase
	for n := range it(ast) {
		if n.sym != CommClause {
			continue
		}
		c, ok := e.selectClause(n)
		if !ok {
			return // selectClause has latched the failure
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		e.fail("an empty select blocks forever and is not supported yet")
		return
	}
	hasDefault := false
	for _, c := range cases {
		if c.def {
			hasDefault = true
		}
	}
	done := e.newTmp()
	e.ind()
	e.emit("{\n")
	e.indent++
	if hasDefault {
		// One pass, so no loop and no flag to test: a default clause makes the
		// select non-blocking, and the clauses are a plain if/else chain.
		e.ind()
		e.emit("do {\n")
	} else {
		e.ind()
		e.emit("int " + done + " = 0;\n")
		e.ind()
		e.emit("while (!" + done + ") {\n")
	}
	e.indent++
	first := true
	for _, c := range cases {
		if c.def {
			continue // emitted last, as the else
		}
		tmp := e.newTmp()
		e.ind()
		e.emit(c.elem + " " + tmp + ";\n")
		e.ind()
		if !first {
			e.emit("else ")
		}
		first = false
		e.chanTryRecvElems[c.elem] = true
		e.emit("if (" + chanTryRecvCName(c.elem) + "(" + c.ch + ", &" + tmp + ")) {\n")
		e.indent++
		if !hasDefault {
			e.ind()
			e.emit(done + " = 1;\n") // set before the body, so a break in it is the user's
		}
		switch {
		case c.declare:
			e.locals[c.target] = c.elem
			e.ind()
			e.emit(c.elem + " " + c.target + " = " + tmp + ";\n")
		case c.target != "":
			e.ind()
			e.emit(c.target + " = " + tmp + ";\n")
		}
		for _, st := range c.body {
			e.emitStatement(st.ast)
		}
		e.indent--
		e.ind()
		e.emit("}\n")
	}
	for _, c := range cases {
		if !c.def {
			continue
		}
		e.ind()
		if !first {
			e.emit("else ")
		}
		e.emit("{\n")
		e.indent++
		for _, st := range c.body {
			e.emitStatement(st.ast)
		}
		e.indent--
		e.ind()
		e.emit("}\n")
	}
	if !hasDefault {
		e.ind()
		e.emit("if (!" + done + ") { _waitx(1); }\n")
	}
	e.indent--
	e.ind()
	if hasDefault {
		e.emit("} while (0);\n")
	} else {
		e.emit("}\n")
	}
	e.indent--
	e.ind()
	e.emit("}\n")
}

// selectClause reads one CommClause into a selectCase.
func (e *emitter) selectClause(n Node) (selectCase, bool) {
	var c selectCase
	for k := range it(n.ast) {
		switch k.sym {
		case CommHead:
			for h := range it(k.ast) {
				switch {
				case h.sym == 0 && e.f.ch(h.tok) == DEFAULT:
					c.def = true
				case h.sym == CommOp:
					if !e.selectCommOp(h, &c) {
						return c, false
					}
				}
			}
		case Statement:
			c.body = append(c.body, k)
		}
	}
	return c, true
}

// selectCommOp reads a CommOp: a bare `<-ch`, or `x = <-ch` / `x := <-ch`.
func (e *emitter) selectCommOp(n Node, c *selectCase) bool {
	for k := range it(n.ast) {
		switch k.sym {
		case AssignHead:
			c.target = e.soleIdent(k.ast)
		case PostfixComm:
			for q := range it(k.ast) {
				switch {
				case q.sym == 0 && e.f.ch(q.tok) == DEFINE:
					c.declare = true
				case q.sym == Expression:
					if !e.selectChan(q, c) {
						return false
					}
				}
			}
		case Expression:
			if !e.selectChan(k, c) {
				return false
			}
		}
	}
	if c.ch == "" {
		e.fail("only a receive or default clause is supported in select yet")
		return false
	}
	return true
}

// selectChan resolves the channel a clause polls.
func (e *emitter) selectChan(n Node, c *selectCase) bool {
	base, ok := e.exprIdent(n.ast)
	if !ok {
		e.fail("a select clause needs a plain channel operand")
		return false
	}
	ct, ok := e.varType(base)
	if !ok || !e.isChanCType(ct) {
		e.fail("a select clause needs a channel operand")
		return false
	}
	c.ch, c.elem = base, e.chanElemByName[ct]
	return true
}

// pkgInitCName is the synthesized function that performs package initialization:
// the variable initializers C cannot express at file scope, the channels whose
// locks must be acquired before anything uses them, and the user's own init().
const pkgInitCName = "ogo_pkg_init"

// zeroInitC is the C initializer for a zero value of ctype: a brace for anything
// aggregate, 0 otherwise.
func (e *emitter) zeroInitC(ctype string) string {
	if e.isStruct(ctype) || ctype == cString || e.isSliceCType(ctype) {
		return "{0}"
	}
	return "0"
}

// zeroBraceC is zeroInitC written out in full: every field of a struct, with an
// array field's extents brace-nested. "{0}" is C's universal zero only at the top
// level of an initializer; nested inside one, an aggregate sub-object initialized
// by a bare 0 draws -Wmissing-braces from the host compiler and, past one array
// dimension, defeats flexcc outright. A struct cannot contain itself -- the
// checker refuses a recursive type -- so this terminates.
func (e *emitter) zeroBraceC(ctype string) string {
	fields, ok := e.structs[ctype]
	if !ok {
		return e.zeroInitC(ctype) // a string or slice header leads with a scalar
	}
	var parts []string
	for _, f := range fields {
		parts = append(parts, e.zeroFieldC(f))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// zeroFieldC is the written-out zero of one struct field. An array's extents live
// on the declarator, so its zero is the element's wrapped in one brace per
// dimension.
func (e *emitter) zeroFieldC(f structField) string {
	z := e.zeroBraceC(f.ctype)
	if f.dim.bound != "" {
		for range f.dim.dims() {
			z = "{" + z + "}"
		}
	}
	return z
}

// hasArrayField reports whether a struct type holds a fixed-size array anywhere
// within it. flexcc cannot copy such a struct by value: `y = x` fails with
// "Unable to multiply assign this target", naming C the user never wrote.
//
// The predicate is deliberately coarser than the bug. What actually trips flexcc
// depends on its own layout decisions -- `struct { int a[3]; int n; }` fails while
// `struct { int a[2]; int n; }`, `struct { int m[2][3]; }` and a struct merely
// *containing* the failing one all copy fine -- so mirroring it would mean
// encoding a backend heuristic nobody can state. Every array-free struct copies
// correctly, at every size and shape tried, so "holds an array" is a safe
// over-approximation that leaves the common case untouched.
func (e *emitter) hasArrayField(ctype string) bool {
	for _, f := range e.structs[ctype] {
		if f.dim.bound != "" || e.hasArrayField(f.ctype) {
			return true
		}
	}
	return false
}

// emitStructCopy emits a copy of an array-holding struct as a memcpy, the one form
// flexcc lowers correctly (see hasArrayField). dst is a C lvalue; src is the
// right-hand side, which must be addressable -- taking its address is how the copy
// is expressed. A composite literal is not addressable, so it initializes a
// temporary first, initialization being the case that does work.
func (e *emitter) emitStructCopy(dst, ctype string, src []int32) {
	e.includes["string.h"] = true
	from := ""
	switch name, lit, ok := e.soleCompositeLit(src); {
	case ok:
		tmp := e.newTmp()
		e.ind()
		e.emit(ctype + " " + tmp + " = ")
		e.emitCompositeLit(name, lit, true)
		e.emit(";\n")
		from = tmp
	default:
		from = e.exprC(src)
	}
	// sizeof takes the type, not the destination: an indexed destination carries a
	// bounds check, and naming it twice would repeat that in the source even though
	// C leaves a sizeof operand unevaluated.
	e.ind()
	e.emit("memcpy(&" + dst + ", &" + from + ", sizeof(" + ctype + "));\n")
}

// checkStructCopySrc reports whether a struct-copy source can have its address
// taken, which is what memcpy needs. Only a call cannot be, and a function that
// returns such a struct is already refused where it is declared
// (refuseArrayStructABI), so this is a backstop that keeps the lowering from
// emitting "&f(...)" if that report is ever bypassed.
func (e *emitter) checkStructCopySrc(ctype string, src []int32) bool {
	kids, ok := e.soleFactor(src)
	if !ok {
		return true // not a bare factor: an operator chain, which is not a struct
	}
	if _, _, isCall := e.factorCall(kids); !isCall {
		return true
	}
	e.fail("cannot copy %s from a call: it holds an array, which the target's C compiler cannot return by value", ctype)
	return false
}

// captureC renders whatever emit writes to C text instead of the output stream.
// It is what lets a lowering that rewrites a statement -- memcpy needs its
// destination as a string -- reuse the emitters that otherwise stream their output.
func (e *emitter) captureC(emit func()) string {
	saved, savedIndent := e.w, e.indent
	var b bytes.Buffer
	e.w, e.indent = &b, 0
	emit()
	e.w, e.indent = saved, savedIndent
	return b.String()
}

// exprC renders an expression to C text instead of the output stream, for the
// statements collected into the package initializer.
func (e *emitter) exprC(ast []int32) string {
	return e.captureC(func() { e.emitExpr(ast) })
}

// deferPkgInit records a statement to run at package initialization.
func (e *emitter) deferPkgInit(stmt string) { e.pkgInit = append(e.pkgInit, stmt) }

// staticInitOK reports whether a package variable's initializer is a constant
// expression, which C requires of a file-scope initializer. An integer literal is,
// and so is a name the checker folded to one. Anything else -- a call, a reference
// to another variable, arithmetic over one -- is not, and C rejects it outright
// ("initializer element is not constant"), so it is assigned at package
// initialization instead.
//
// A composite literal qualifies when every element does, because at file scope it
// is emitted as a brace initializer (see emitCompositeLit), and a brace of
// constants is constant. Deferring one instead would be both wasteful -- the
// variable is zeroed and then overwritten with the same values at startup -- and,
// for a struct with an array field, broken: the deferred form is an assignment
// from a compound literal, which flexcc cannot lower.
func (e *emitter) staticInitOK(initExpr []int32) bool {
	if _, lit, ok := e.soleCompositeLit(initExpr); ok {
		for _, el := range compositeLitElements(lit) {
			if !e.staticInitOK(el.value.ast) {
				return false
			}
		}
		return true
	}
	tok, ok := e.soleToken(initExpr)
	if !ok {
		return false
	}
	switch e.f.ch(tok) {
	case INT, STRING:
		return true
	case IDENT:
		if _, isConst := e.constInt[e.src(tok)]; isConst {
			return true
		}
		s := e.src(tok)
		return s == "true" || s == "false"
	}
	return false
}

// pkgInitDefs renders the synthesized initializer, or "" when there is nothing to
// do. It is emitted after the prototypes, since it calls user functions.
func (e *emitter) pkgInitDefs() string {
	if !e.needsPkgInit() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "static void %s(void) {\n", pkgInitCName)
	for _, st := range e.pkgInit {
		fmt.Fprintf(&b, "\t%s\n", st)
	}
	// The variable initializers run first, then init(), which is Go's order.
	for _, fn := range e.initFuncs {
		fmt.Fprintf(&b, "\t%s();\n", fn)
	}
	b.WriteString("}\n")
	return b.String()
}

// needsPkgInit reports whether the package has anything to initialize.
func (e *emitter) needsPkgInit() bool { return len(e.pkgInit) != 0 || len(e.initFuncs) != 0 }

// chanType recognises a channel type `chan T`, returning its element C type.
func (e *emitter) chanType(typeAST []int32) (elem string, ok bool) {
	nodes := slices.Collect(it(typeAST))
	if len(nodes) == 0 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != CHAN {
		return "", false
	}
	for _, n := range nodes {
		if n.sym == Type {
			if elem = e.cType(n.ast); elem == "" {
				return "", false
			}
			return elem, true
		}
	}
	return "", false
}

// needChan records that `chan elem` is used, so its typedef and helpers are
// emitted.
func (e *emitter) needChan(elem string) {
	e.chanElems[elem] = true
	e.chanElemByName[chanCName(elem)] = elem
}

// isChanCType reports whether a C type is a channel cell.
func (e *emitter) isChanCType(ctype string) bool { return strings.HasPrefix(ctype, chanTypePrefix) }

// chanRuntimeDefs returns the typedef for `chan elem` plus the helpers the
// program actually reaches for: a send-only program never sees the receive, and
// only a select polls with tryrecv. Emitting the unused ones would be harmless
// except that they are `static` (see below), which makes an unused one a
// -Wunused-function warning the host test suite treats as a failure.
//
// Blocking is a poll with a _waitx(1) yield between attempts: a blocked cog
// cannot sleep, since there is no scheduler, and spinning on the Hub bus without
// yielding would starve the cogs doing real work.
//
// Each poll reads the volatile flag it is waiting on *before* asking for the
// lock, and only asks when the read says there is plausibly something to do. The
// authoritative check is still the one inside the lock, so the outer read is a
// hint and may be wrong either way: a false positive costs one acquire and
// release, a false negative costs one more turn round the loop.
//
// Testing first is what makes the rendezvous work, not an optimization. A loop
// that calls _locktry every turn re-takes the lock so quickly that the cog on the
// other side never wins it -- both sides live, neither progressing. It is a
// livelock in the polling loop, and it is timing-dependent, so it appears only
// once the loop is fast enough: with FCACHE lifting the loop into Cog RAM, a
// program with a few channels and a few goroutines would hang at a rendezvous.
// That was misread as an FCACHE miscompilation for a while, and builds carried
// --fcache=0 to avoid it; the flag is gone now (see internal/build) and the
// backoff is still one cycle. Raising the backoff instead also works -- 256
// cycles was enough for every case here -- but it paces the symptom, costs
// latency on every rendezvous, and leaves the threshold to be rediscovered by the
// next program that beats it.
//
// The helpers are deliberately `static` and NOT `static inline`. Inlined into a
// call argument -- `println(<-ch)` rather than `v := <-ch` -- flexcc miscompiles
// the rendezvous loop: sender and receiver both spin forever, each holding the
// other off, on hardware only. gcc compiles both shapes correctly, so the host
// tests cannot see it, and the board case above is what guards it. Do not re-add
// `inline` here; the call costs nothing next to the lock-and-yield loop it
// guards.
func (e *emitter) chanRuntimeDefs(elem string) string {
	c, snd, rcv, ini := chanCName(elem), chanSendCName(elem), chanRecvCName(elem), chanInitCName(elem)
	var b strings.Builder
	fmt.Fprintf(&b, `typedef struct { int lock; volatile int full; volatile int taken; volatile %[2]s val; } %[6]s;
typedef %[6]s* %[1]s;
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	if e.chanInitElems[elem] {
		fmt.Fprintf(&b, `static void %[5]s(%[1]s ch) {
	ch->lock = _locknew();
	if (ch->lock < 0) {
		ogo_panic("out of hardware locks");
	}
	ch->full = 0;
	ch->taken = 0;
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	}
	if e.chanSendElems[elem] {
		fmt.Fprintf(&b, `static void %[3]s(%[1]s ch, %[2]s v) {
	int mine = 0; // always set below before the rendezvous loop reads it; the
	// initializer only quiets flexcc, whose flow analysis cannot prove the first
	// loop exits solely through the break that follows the assignment.
	while (1) { // wait for the cell to be free, then deposit
		if (!ch->full && _locktry(ch->lock)) {
			if (!ch->full) {
				mine = ch->taken;
				ch->val = v;
				ch->full = 1;
				_lockrel(ch->lock);
				break;
			}
			_lockrel(ch->lock);
		}
		_waitx(1);
	}
	while (1) { // rendezvous: wait until a receiver has taken *this* value
		int done = 0;
		if (ch->taken != mine && _locktry(ch->lock)) {
			done = ch->taken != mine;
			_lockrel(ch->lock);
		}
		if (done) {
			return;
		}
		_waitx(1);
	}
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	}
	if e.chanTryRecvElems[elem] {
		fmt.Fprintf(&b, `static int ogo_chan_tryrecv_%[7]s(%[1]s ch, %[2]s* out) {
	if (ch->full && _locktry(ch->lock)) {
		if (ch->full) {
			*out = ch->val;
			ch->full = 0;
			ch->taken++;
			_lockrel(ch->lock);
			return 1;
		}
		_lockrel(ch->lock);
	}
	return 0;
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	}
	if e.chanRecvElems[elem] {
		fmt.Fprintf(&b, `static %[2]s %[4]s(%[1]s ch) {
	while (1) {
		if (ch->full && _locktry(ch->lock)) {
			if (ch->full) {
				%[2]s v = ch->val;
				ch->full = 0;
				ch->taken++;
				_lockrel(ch->lock);
				return v;
			}
			_lockrel(ch->lock);
		}
		_waitx(1);
	}
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	}
	return b.String()
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
	e := &emitter{includes: map[string]bool{}, funcRet: map[string][]string{}, methodPtr: map[string]bool{}, globals: map[string]string{}, structs: map[string][]structField{}, namedTypes: map[string]bool{}, constInt: map[string]string{}, constStr: map[string]string{}, arrays: map[string]arrDim{}, globalArrays: map[string]arrDim{}, sliceVars: map[string]string{}, globalSliceVars: map[string]string{}, chanElems: map[string]bool{}, chanInitElems: map[string]bool{}, chanSendElems: map[string]bool{}, chanRecvElems: map[string]bool{}, chanTryRecvElems: map[string]bool{}, chanElemByName: map[string]string{}, sliceElems: map[string]bool{}, sliceElemByName: map[string]string{}, inlineSliceDefs: map[string]bool{}, appendElems: map[string]bool{}, tryappendElems: map[string]bool{}, printSliceElems: map[string]bool{}, deferReplay: -1, iota: -1}
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

	// A channel's helpers call ogo_panic and the P2 lock/wait intrinsics, so both
	// must be requested before the include list is taken.
	if len(e.chanElems) != 0 {
		e.needPanic()
		e.includes["propeller2.h"] = true
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
	// A channel's helpers call ogo_panic (out of locks) and the P2 lock and wait
	// intrinsics, so they follow the panic definition and pull in propeller2.h.
	for _, el := range sortedKeys(e.chanElems) {
		helperDefs.WriteString(e.chanRuntimeDefs(el))
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
			"\t\t%s\n"+
			"\t}\n"+
			"\tprintf(\"]\");\n}\n"+
			"static void %s(%s s) { %s(s); printf(\"\\n\"); }\n",
			printSliceCName(el), sliceCName(el),
			sliceElemPrintf(el),
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
	// The goroutine pool and trampolines go after the prototypes -- a trampoline
	// calls a user function -- and before the bodies, whose `go` statements name
	// the argument structs declared here.
	if gd := e.goDefs(); gd != "" {
		out.WriteString(gd)
		out.WriteByte('\n')
	}
	// The package initializer likewise calls user functions, so it follows the
	// prototypes too.
	if pd := e.pkgInitDefs(); pd != "" {
		out.WriteString(pd)
		out.WriteByte('\n')
	}
	out.Write(body.Bytes())
	_, err := w.Write(out.Bytes())
	return err
}

type emitter struct {
	w                io.Writer // body buffer during the walk
	f                *File     // file currently being emitted, for token access
	indent           int
	includes         map[string]bool
	funcRet          map[string][]string      // user function / mangled method name -> C result types (empty=void), for typing calls
	methodPtr        map[string]bool          // mangled method name -> receiver is a pointer, for &/* adjustment at the call site
	globals          map[string]string        // package-level constant/variable name -> C type, for typing `x := g`
	structs          map[string][]structField // struct type name -> its fields, for typedefs, zero-init and field typing
	namedTypes       map[string]bool          // non-struct named type (e.g. `type Celsius int`) -> emitted as a typedef; may carry methods
	constInt         map[string]string        // integer-constant name -> its C literal value, for array bounds
	constStr         map[string]string        // string-constant name -> its decoded value, for folding string concatenation
	arrays           map[string]arrDim        // local array name -> element type and bound (reset per function)
	globalArrays     map[string]arrDim        // package-level array name -> element type and bound (persists across functions)
	sliceVars        map[string]string        // local slice name -> element C type, for `xs[i]` / len(xs) (reset per function)
	globalSliceVars  map[string]string        // package-level slice name -> element C type (persists across functions)
	pkgInit          []string                 // C statements for the synthesized package initializer, in source order
	initFuncs        []string                 // user init() functions, called after the variable initializers
	goSites          []goSite                 // launched goroutines, one per `go` statement: each needs an argument struct and a trampoline
	chanElems        map[string]bool          // element C types that need an ogo_chan_<T> cell and helpers
	chanInitElems    map[string]bool          // element types whose channel init helper is reached
	chanSendElems    map[string]bool          // element types whose channel send helper is reached
	chanRecvElems    map[string]bool          // element types whose blocking receive helper is reached
	chanTryRecvElems map[string]bool          // element types whose select tryrecv helper is reached
	chanElemByName   map[string]string        // ogo_chan_<T> C type name -> its element C type
	sliceElems       map[string]bool          // element C types that need an ogo_slice_<T> typedef
	sliceElemByName  map[string]string        // ogo_slice_<T> C type name -> its element C type; the forward direction mangles pointers, so the reverse is recorded, not derived
	inlineSliceDefs  map[string]bool          // struct element C types whose slice typedef was already emitted inline, between the element struct and the struct field that holds it
	appendElems      map[string]bool          // element C types needing the trapping ogo_append_<T> helper
	tryappendElems   map[string]bool          // element C types needing the ok-form ogo_tryappend_<T> helper + ogo_appendok_<T>
	printSliceElems  map[string]bool          // element C types needing the ogo_print_slice_<T> / ogo_println_slice_<T> helpers
	defers           []deferredCall           // the current function's top-level defers, in source order, replayed LIFO before each return
	inSwitchCase     bool                     // emitting a switch case body, where the if/else lowering gives break a different meaning
	deferBlockDepth  int                      // nesting inside if/for/switch bodies; a defer at depth > 0 needs a runtime flag
	deferReplay      int                      // slot being replayed, or -1: makes emitCallArgs read the captured temporaries
	iota             int                      // the current iota value while emitting a const spec's expression, or -1 outside one
	deferReplayArgs  []deferArg               // that slot's arguments, so emitCallArgs knows which were captured
	usesPanic        bool                     // ogo_panic is called: emit its definition and pull in its includes
	usesBound        bool                     // ogo_bound is called: emit the index bounds-check helper
	usesNonzero      bool                     // ogo_nonzero is called: emit the divide-by-zero-check helper
	release          bool                     // release build: a panic reboots (_reboot) instead of halting the cog
	checks           bool                     // emit runtime bounds / divide-by-zero checks (set by Checked; ogo build enables it by default)
	locals           map[string]string        // current function's parameter/local name -> C type, for typing `x := y`
	curFunc          string                   // name of the function whose body is being emitted (for its result-struct type)
	curResultNames   []string                 // current function's result C-variable names, for a bare "return" (naked return)
	tmp              int                      // per-function counter for generated temporaries (destructuring)
	makeN            int                      // translation-unit counter for make() backing arrays
	wroteDecl        bool                     // a top-level definition has been emitted (drives blank-line separators)
	mainRet          bool                     // currently emitting main's body: a bare `return` yields `return 0;`
	declInit         bool                     // emitting a static initializer: a string literal must use a brace, not a compound literal
	usesString       bool                     // an ogo_string type/literal appears: emit stringTypedef
	usesStringPrint  bool                     // a string is printed: emit stringHelpers
	err              error
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
type structField struct {
	name, ctype string
	dim         arrDim // dim.bound != "" for a fixed-array field; ctype is then its element type
}

// arrDim describes an array variable: its element C type and its C bound (for
// element typing and len).
// arrDim describes a fixed array: the innermost element C type, the outermost
// extent, and any further extents for a multi-dimensional one. `[2][3]int` is
// {elem: "int", bound: "2", inner: ["3"]} and declares as `int m[2][3]`.
//
// bound stays the outermost extent, which is what len/cap and slicing want (Go's
// len of a [2][3]int is 2), so the one-dimensional callers need no change. elem,
// by contrast, is only the type of an *indexed element* when inner is empty: one
// index into a [2][3]int yields a [3]int, not an int. Callers that type an element
// must check dims() == 1.
type arrDim struct {
	elem  string
	bound string
	inner []string
}

// dims reports the number of dimensions.
func (a arrDim) dims() int { return 1 + len(a.inner) }

// bounds returns every extent, outermost first.
func (a arrDim) bounds() []string { return append([]string{a.bound}, a.inner...) }

// declSuffix renders the C declarator brackets, `[2][3]`.
func (a arrDim) declSuffix() string {
	s := ""
	for _, b := range a.bounds() {
		s += "[" + b + "]"
	}
	return s
}

// deferredCall is a recorded `defer` statement: the call's head (AssignHead) and
// its suffix (Selector / Index / CallSuffix), replayed before the function returns.
//
// Arguments are captured into function-scope temporaries where the defer is
// written, which is where Go evaluates them. That is not merely closer to Go: a
// defer in a nested block may name a variable of that block, which no longer
// exists at the return the call is replayed from.
//
// cond marks a defer written inside a nested block, where whether it ran is a
// runtime question and needs a flag. A defer cannot appear in a loop -- the
// checker rejects that -- so the number of sites in a function is fixed at compile
// time and the flags are plain stack locals. This is Go's open-coded defer without
// the heap fallback Go keeps for the loop case OctoGo does not admit.
type deferredCall struct {
	head   Node
	suffix []Node
	args   []deferArg
	cond   bool
	slot   int
}

// deferArg is one argument of a deferred call. A literal needs no temporary --
// re-evaluating it at the return yields the same value -- so it is left inline,
// which matters on a target with 512 longs of cog RAM per cog.
type deferArg struct {
	ctype  string
	expr   []int32
	inline bool
}

// deferFlagName and deferArgName name the temporaries backing a defer slot.
func deferFlagName(slot int) string { return fmt.Sprintf("_ogo_defer%d", slot) }

func deferArgName(slot, arg int) string { return fmt.Sprintf("_ogo_defer%d_a%d", slot, arg) }

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
				if fld.dim.bound != "" {
					e.emit(" " + fld.ctype + " " + fld.name + fld.dim.declSuffix() + ";")
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
		var dim arrDim // dim.bound non-empty for a fixed-size array field
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
				if a, ok := e.arrayDim(c.ast); ok {
					ctype, dim = a.elem, a
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
			out = append(out, structField{name: nm, ctype: ctype, dim: dim})
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
		var typeAST []int32
		var initExprs [][]int32
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
			case ExpressionList:
				for _, x := range expressionListItems(s) {
					initExprs = append(initExprs, x.ast)
				}
			case 0:
				// The "=" separator.
			default:
				e.fail("unsupported var-spec element %v", s.sym)
			}
		}
		// As for locals: a value list is that many independent single-name
		// declarations; one value (or none) keeps the single-spec paths below.
		if len(names) > 1 && len(initExprs) == len(names) {
			e.emitPackageVarList(names, typeAST, initExprs)
			continue
		}
		var initExpr []int32
		if len(initExprs) != 0 {
			initExpr = initExprs[0]
		}
		if typeAST == nil {
			// Type-inferred package variable `var x = expr`. C requires a constant
			// initializer at file scope (emitGlobalInit), so a single named variable
			// with an inferable type is modelled; a make/slice initializer still needs
			// an explicit type and fails honestly through inference.
			if len(names) != 1 {
				if len(initExprs) == 1 {
					// `var a, b = f()` at package scope: a multi-result call bound to
					// several variables. C cannot call in a file-scope initializer, so
					// the call and its distribution are deferred to the synthesized
					// package init.
					e.emitPackageDestructure(names, initExprs[0])
					continue
				}
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
			if e.staticInitOK(initExpr) {
				e.emit("static " + ct + " " + names[0] + " = ")
				e.emitGlobalInit(initExpr)
				e.emit(";\n")
				continue
			}
			e.emit("static " + ct + " " + names[0] + " = " + e.zeroInitC(ct) + ";\n")
			e.deferPkgInit(names[0] + " = " + e.exprC(initExpr) + ";")
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
					e.globalArrays[nm] = arrDim{elem: elem, bound: bound}
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
			if e.isChanCType(ctype) {
				// The cell is a file-scope object like the variable pointing at it;
				// acquiring its lock is a call, so it waits for package init.
				elem := e.chanElemByName[ctype]
				cell := nm + "_cell"
				e.emit("static " + chanCellCName(elem) + " " + cell + ";\n")
				e.deferPkgInit(nm + " = &" + cell + ";")
				e.chanInitElems[elem] = true
				e.deferPkgInit(chanInitCName(elem) + "(" + nm + ");")
			}
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
	// iota counts specs across the group; lastExpr and lastType carry the previous
	// spec's expression and type forward for a spec that omits its own, mirroring
	// the checker's declareConst.
	iotaVal := 0
	var lastExpr []int32
	var lastType string
	haveLastType := false
	for n := range it(ast) {
		if n.sym != ConstSpec {
			continue
		}
		var name, ownType string
		var initExpr []int32
		hasType := false
		for s := range it(n.ast) {
			switch s.sym {
			case Type:
				ownType, hasType = e.cType(s.ast), true
			case Expression:
				initExpr = s.ast
			case 0:
				if e.f.ch(s.tok) == IDENT {
					name = e.src(s.tok)
				}
			}
		}
		if name == "" {
			e.fail("malformed const declaration")
			return
		}
		// A spec omitting its expression repeats the previous spec's expression and
		// type together; one with its own carries that forward.
		if initExpr != nil {
			lastExpr = initExpr
			lastType, haveLastType = ownType, hasType
		} else {
			initExpr = lastExpr
			ownType, hasType = lastType, haveLastType
		}
		if initExpr == nil {
			e.fail("malformed const declaration")
			return
		}
		curIota := iotaVal
		iotaVal++
		if name == "_" {
			continue // a blank const declares nothing; skip it, but it still advances iota
		}
		ctype := ownType
		if !hasType {
			e.iota = curIota // so inference sees iota as an int
			ct, ok := e.inferCType(initExpr)
			e.iota = -1
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
		// A constant that folds to a single integer -- a literal, or iota itself --
		// can serve as an array bound (flexcc rejects a `static const` there); record
		// its value.
		if tok, ok := e.soleToken(initExpr); ok && e.f.ch(tok) == INT {
			e.constInt[name] = normalizeIntLit(e.src(tok))
		} else if tok, ok := e.soleToken(initExpr); ok && e.f.ch(tok) == IDENT && e.src(tok) == "iota" {
			e.constInt[name] = strconv.Itoa(curIota)
		}
		// A constant string -- a literal or a concatenation of constants -- is
		// recorded decoded and emitted at each use as the folded literal, rather
		// than as a C variable. A Go constant has no address, so inlining it is
		// correct, and it avoids an unused-variable warning when the constant is
		// only ever folded into a concatenation (which does not name it).
		if v, ok := e.foldConstString(initExpr); ok {
			e.constStr[name] = v
			continue
		}
		e.iota = curIota // substitute iota with its value while emitting the expression
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
		e.iota = -1
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
		// Go runs init() before main. Recorded here, in the prototype pass, rather
		// than where the body is emitted: main is emitted in that same later pass,
		// so an init declared after it would otherwise not be known in time.
		if recv == nil && name == "init" {
			e.initFuncs = append(e.initFuncs, name)
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
	e.deferReplay = -1
	e.curResultNames = nil
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
	e.emitParamVoids(sig)
	e.declareNamedResults(sig, body)
	// A bare "return" (legal only when every result is named) returns these. A
	// blank result "_" has no C variable, so it contributes its zero value.
	e.curResultNames, _ = e.resultInfo(sig)
	for i, nm := range e.curResultNames {
		if nm == "" || nm == "_" {
			e.curResultNames[i] = "0"
		}
	}
	// The body goes to a buffer so the defer temporaries can be declared ahead of
	// it. They must be at function scope -- a defer in a nested block captures its
	// arguments there, but the call is replayed at a return that block has exited
	// -- and the full set is only known once the body has been walked.
	saved := e.w
	var bodyBuf bytes.Buffer
	e.w = &bodyBuf
	e.emitBlockStmts(body)
	// A body that falls off the end (no trailing return) runs its deferred calls
	// here; one ending in a return already replayed them at that return.
	if len(e.defers) != 0 && !e.bodyEndsInReturn(body) {
		e.emitDeferred()
	}
	e.w = saved
	e.emitDeferDecls()
	e.w.Write(bodyBuf.Bytes())
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
//
// A named result that the body never reads or writes and that no naked return
// hands back is not emitted as a C local at all: it would only draw an
// unused-variable warning ("(q, r int) { return a, b }" is idiomatic Go). Its
// values are supplied directly by each explicit return.
func (e *emitter) declareNamedResults(sig, body []int32) {
	names, types := e.resultInfo(sig)
	naked := e.bodyHasNakedReturn(body)
	for i, nm := range names {
		if nm == "" || nm == "_" {
			continue
		}
		e.locals[nm] = types[i]
		if !naked && !e.bodyMentions(body, nm) {
			continue
		}
		e.ind()
		e.emit(types[i] + " " + nm + " = 0;\n")
	}
}

// bodyHasNakedReturn reports whether ast contains a bare "return" -- a return
// statement with no ExpressionList -- anywhere, including in nested blocks. A
// naked return reads the named result variables, so their declaration cannot be
// elided when one is present.
func (e *emitter) bodyHasNakedReturn(ast []int32) bool {
	for n := range it(ast) {
		if n.sym == 0 {
			continue
		}
		hasRet, hasExpr := false, false
		for c := range it(n.ast) {
			switch {
			case c.sym == 0 && e.f.ch(c.tok) == RETURN:
				hasRet = true
			case c.sym == ExpressionList:
				hasExpr = true
			}
		}
		if hasRet && !hasExpr {
			return true
		}
		if e.bodyHasNakedReturn(n.ast) {
			return true
		}
	}
	return false
}

// bodyMentions reports whether name appears as an identifier anywhere in ast,
// used to decide whether a named result is actually read or written by the body.
func (e *emitter) bodyMentions(ast []int32, name string) bool {
	for n := range it(ast) {
		if n.sym == 0 {
			if e.f.ch(n.tok) == IDENT && e.src(n.tok) == name {
				return true
			}
			continue
		}
		if e.bodyMentions(n.ast, name) {
			return true
		}
	}
	return false
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
	e.deferReplay = -1
	e.curFunc = "main"
	e.emit("int main(void) {\n")
	e.indent++
	if e.needsPkgInit() {
		// Package initialization runs before anything in main, as in Go.
		e.ind()
		e.emit(pkgInitCName + "();\n")
	}
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
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			// Parameters are the only ParameterList; results are ResultList/Type.
			parts = e.cParamList(n.ast)
		case ResultList:
			for _, d := range e.f.paramDecls(n.ast) {
				ct := e.cType(d.TypeAST.ast)
				e.refuseArrayStructABI(ct, "result")
				k := len(d.Names)
				if k == 0 {
					k = 1 // an unnamed result is one value
				}
				for range k {
					resTypes = append(resTypes, ct)
				}
			}
		case Type:
			// A single unnamed result: Signature = "(" [...] ")" Type .
			ct := e.cType(n.ast)
			e.refuseArrayStructABI(ct, "result")
			resTypes = append(resTypes, ct)
		case 0:
			// structural "(" / ")"
		default:
			e.fail("unsupported signature element %v", n.sym)
		}
	}
	return strings.Join(parts, ", "), resTypes
}

// resultInfo returns a function's result names and C types (one entry per result
// value). An unnamed result has an empty name; a named result contributes its
// name (a shared "(a, b int)" yields one entry per name).
func (e *emitter) resultInfo(sig []int32) (names, types []string) {
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ResultList:
			for _, d := range e.f.paramDecls(n.ast) {
				ct := e.cType(d.TypeAST.ast)
				if len(d.Names) == 0 {
					names = append(names, "")
					types = append(types, ct)
					continue
				}
				for _, nm := range d.Names {
					names = append(names, nm.Src())
					types = append(types, ct)
				}
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
	e.forEachParam(ast, func(name string, ta []int32, _ bool) {
		if elem, _, ok := e.arrayType(ta); ok {
			out = append(out, elem+"* "+paramArgName(name))
			return
		}
		ct := e.cType(ta)
		e.refuseArrayStructABI(ct, "parameter "+name)
		out = append(out, ct+" "+name)
	})
	return out
}

// refuseArrayStructABI rejects passing or returning a struct that holds an array.
// A copy elsewhere is lowered to memcpy (see emitStructCopy), but a parameter or a
// result is the C calling convention itself, and flexcc gets that wrong in a way
// no lowering here can reach: it drops the argument slot ("Internal error,
// couldn't find object variable with offset 4") or fails to assign the result.
// Reported where the signature is written, so the message names the declaration
// rather than every call of it. Passing a pointer is the way to write this.
func (e *emitter) refuseArrayStructABI(ctype, what string) {
	if e.hasArrayField(ctype) {
		e.fail("%s: %s holds an array, which the target's C compiler cannot pass or return by value; use a pointer", what, ctype)
	}
}

// forEachParam walks a ParameterList's `IdentifierList Type` groups, calling fn
// with each parameter's name and C type (a shared type "a, b int" yields two
// calls). It underlies both the C parameter rendering (cParamList) and the local
// type environment (bindParams).
func (e *emitter) forEachParam(ast []int32, fn func(name string, typeAST []int32, synthetic bool)) {
	i := 0
	for _, d := range e.f.paramDecls(ast) {
		if len(d.Names) == 0 {
			fn(unnamedParamName(i), d.TypeAST.ast, true)
			i++
			continue
		}
		for _, nm := range d.Names {
			name := nm.Src()
			if name == "_" {
				fn(unnamedParamName(i), d.TypeAST.ast, true)
			} else {
				fn(name, d.TypeAST.ast, false)
			}
			i++
		}
	}
}

// unnamedParamName is the synthetic C name of the i-th parameter when it is
// unnamed or blank ("_"). flexcc miscompiles a definition that leaves a parameter
// unnamed -- it drops that parameter's argument slot and shifts every following
// argument -- so each such parameter is given a name (and a "(void)" reference in
// the body, since the source never uses it, to stay -Wunused-parameter clean).
func unnamedParamName(i int) string { return "_ogo_unused" + strconv.Itoa(i) }

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
				e.forEachParam(n.ast, func(name string, ta []int32, synthetic bool) {
					if synthetic {
						return // an unnamed parameter binds nothing; the body cannot name it
					}
					if elem, bound, ok := e.arrayType(ta); ok {
						e.arrays[name] = arrDim{elem: elem, bound: bound}
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
				e.forEachParam(n.ast, func(name string, ta []int32, synthetic bool) {
					if synthetic {
						return // an unnamed array parameter has no in-body copy
					}
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

// emitParamVoids emits a "(void)name;" for every synthetic (unnamed or blank)
// parameter, so the names forced on them for flexcc (see unnamedParamName) do not
// trip -Wunused-parameter. It reads only the parameter list, not the results.
func (e *emitter) emitParamVoids(sig []int32) {
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if !seenRPar {
				e.forEachParam(n.ast, func(name string, ta []int32, synthetic bool) {
					if !synthetic {
						return
					}
					cname := name
					if _, _, ok := e.arrayType(ta); ok {
						cname = paramArgName(name) // an array parameter is received by pointer
					}
					e.ind()
					e.emit("(void)" + cname + ";\n")
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
	case first.sym == SelectStmt:
		e.emitSelect(first.ast)
	case first.sym == 0 && e.f.ch(first.tok) == RETURN:
		e.emitReturn(nodes)
	case first.sym == 0 && e.f.ch(first.tok) == DEFER:
		e.emitDefer(nodes)
	case first.sym == 0 && e.f.ch(first.tok) == GO:
		e.emitGo(nodes)
	case first.sym == 0 && e.f.ch(first.tok) == BREAK:
		// A switch is lowered to a chain of conditionals, not a C switch, so a C
		// break inside one would leave an enclosing loop instead of the switch --
		// a silent difference. The checker allows break there, as Go does, so it is
		// refused here where the lowering is known.
		if e.inSwitchCase {
			e.fail("break inside a switch is not supported yet")
			return
		}
		e.ind()
		e.emit("break;\n")
	case first.sym == 0 && e.f.ch(first.tok) == CONTINUE:
		// Unaffected by the switch lowering: a C continue names the enclosing loop
		// either way, exactly as Go's does.
		e.ind()
		e.emit("continue;\n")
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
		var typeAST []int32
		var initExprs [][]int32
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
			case ExpressionList:
				for _, x := range expressionListItems(s) {
					initExprs = append(initExprs, x.ast)
				}
			case 0:
				// The "=" separator between the type and the initializer.
			default:
				e.fail("unsupported var-spec element %v", s.sym)
			}
		}
		// A value list gives every name its own initializer, so the spec is that
		// many independent single-name declarations. One value (or none) keeps the
		// single-spec paths below, which is where destructuring a call lives.
		if len(names) > 1 && len(initExprs) == len(names) {
			e.emitVarList(names, typeAST, initExprs)
			continue
		}
		var initExpr []int32
		if len(initExprs) != 0 {
			initExpr = initExprs[0]
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
		if a, ok := e.arrayDim(typeAST); ok {
			elem := a.elem
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
				e.arrays[nm] = a
				if initExpr == nil {
					e.ind()
					e.emit(elem + " " + nm + a.declSuffix() + " = {0};\n")
					continue
				}
				// A literal initializer is aggregate initialization, not a copy.
				if litType, lit, ok := e.soleArrayLit(initExpr); ok {
					if !e.sameArrayType(a, litType) {
						return
					}
					e.emitArrayLitVar(nm, litType, lit)
					continue
				}
				e.includes["string.h"] = true
				e.ind()
				e.emit(elem + " " + nm + a.declSuffix() + ";\n")
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
			// A make([]T, ...) or a literal initializer synthesises a backing array
			// + header, rather than copying an existing header.
			if initExpr != nil && len(names) == 1 && names[0] != "_" {
				if litType, lit, ok := e.soleArrayLit(initExpr); ok {
					if me, isSlice := e.sliceType(litType); !isSlice || me != elem {
						e.fail("a %s literal cannot initialize a variable declared []%s", e.litTypeName(litType), elem)
						return
					}
					e.emitArrayLitVar(names[0], litType, lit)
					continue
				}
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
			if initExpr != nil {
				e.emitVarDeclInit(ctype, nm, initExpr)
			} else {
				e.ind()
				e.emit(ctype + " " + nm + " = " + e.zeroInitC(ctype) + ";\n")
			}
			// A channel is storage, not a handle: the checker rejects make() for one
			// ("dynamic allocation not supported"), so the declaration is what
			// creates it. Acquiring the hardware lock here is what makes the cell
			// usable, and ties the lock's lifetime to the variable's.
			if e.isChanCType(ctype) {
				// The declaration owns the cell; the variable is a reference to it.
				elem := e.chanElemByName[ctype]
				cell := nm + "_cell"
				e.ind()
				e.emit(chanCellCName(elem) + " " + cell + " = {0};\n")
				e.ind()
				e.emit(nm + " = &" + cell + ";\n")
				e.ind()
				e.chanInitElems[elem] = true
				e.emit(chanInitCName(elem) + "(" + nm + ");\n")
			}
		}
	}
}

// factorCompositeLit matches a Factor of the shape "T{...}": an identifier naming
// the type, followed by the literal. Nothing else may follow, so a suffixed factor
// (a call, selector or index) is not one.
func (e *emitter) factorCompositeLit(kids []Node) (name string, lit Node, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != CompositeLit {
		return "", Node{}, false
	}
	return e.src(kids[0].tok), kids[1], true
}

// soleCompositeLit reports whether an expression is nothing but a composite
// literal -- no operator, no unary prefix, no call or index around it -- and
// returns the type name and the literal. This is the shape that may be spelled as
// a brace initializer rather than a compound literal; "f(P{1})" may not, because
// the literal there is an argument, not the initializer.
func (e *emitter) soleCompositeLit(ast []int32) (name string, lit Node, ok bool) {
	kids, ok := e.soleFactor(ast)
	if !ok {
		return "", Node{}, false
	}
	return e.factorCompositeLit(kids)
}

// soleFactor returns the children of the Factor an expression consists of, if that
// is all it is: no operator, no unary prefix, just the one operand.
func (e *emitter) soleFactor(ast []int32) (kids []Node, ok bool) {
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return nil, false
	}
	return slices.Collect(it(fac.ast)), true
}

// soleFactorNode is soleFactor returning the Factor node itself, for a caller that
// needs its AST slice rather than its children.
func (e *emitter) soleFactorNode(ast []int32) (Node, bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && nodes[0].sym != 0 {
		if n := nodes[0]; n.sym == Factor {
			return n, true
		}
		nodes = slices.Collect(it(nodes[0].ast))
	}
	return Node{}, false
}

// emitCompositeLit emits "T{a, b}" as the C compound literal "(T){a, b}", or, with
// brace set, as the plain initializer "{a, b}". "T{}" zeroes every field.
//
// Braces are what C spells in a declarator ("P p = {1, 2}") and what a file-scope
// initializer requires, a compound literal not being a constant expression. They
// are also the only form flexcc can lower for a struct that has an array field:
// given a compound literal of one it fails with "Unable to multiply assign this
// target", naming C the user never wrote. So the brace form propagates into an
// element that is itself a literal, which is C's own spelling for a nested
// aggregate initializer anyway.
func (e *emitter) emitCompositeLit(name string, lit Node, brace bool) {
	values, fields, ok := e.litFieldValues(name, lit)
	if !ok {
		return
	}
	if !brace {
		e.emit("(" + name + ")")
	}
	if len(values) == 0 {
		e.emit("{0}") // no values: zero every field
		return
	}
	e.emit("{")
	for i, v := range values {
		if i != 0 {
			e.emit(", ")
		}
		if v == nil {
			e.emit(e.zeroFieldC(fields[i])) // a field this keyed literal omits
			continue
		}
		e.emitLitElement(*v, brace)
	}
	e.emit("}")
}

// emitLitElement emits one element of a composite literal. Inside a brace
// initializer an element that is itself a literal is written with braces too,
// which is C's spelling for a nested aggregate and the only one flexcc lowers for
// a struct that holds an array (see emitCompositeLit).
func (e *emitter) emitLitElement(v Node, brace bool) {
	if nm, sub, ok := e.soleCompositeLit(v.ast); brace && ok {
		if len(compositeLitElements(sub)) == 0 {
			e.emit(e.zeroBraceC(nm)) // "{0}" does not nest; see zeroBraceC
			return
		}
		e.emitCompositeLit(nm, sub, true)
		return
	}
	// A string is a { pointer, length } struct, so an element that is one is a
	// nested aggregate and takes braces here for the same reason a literal does.
	// Only a bare string literal qualifies: a call that returns one is an
	// expression, and bracing what it contains would not be C.
	if tok, ok := e.soleToken(v.ast); brace && ok && e.f.ch(tok) == STRING {
		saved := e.declInit
		e.declInit = true
		e.emitExpr(v.ast)
		e.declInit = saved
		return
	}
	e.emitExpr(v.ast)
}

// emitLitValues emits an array or slice literal's elements as a braced C
// initializer list. Unlike a struct's, these have no field names to reorder, so
// they are positional by construction; Go's indexed form ("[3]int{2: 5}") is
// refused rather than silently dropped.
func (e *emitter) emitLitValues(lit Node) bool {
	elements := compositeLitElements(lit)
	for _, el := range elements {
		if el.keyed {
			e.fail("an index in an array or slice literal is not supported yet")
			return false
		}
	}
	if len(elements) == 0 {
		e.emit("{0}") // no values: zero every element
		return true
	}
	e.emit("{")
	for i, el := range elements {
		if i != 0 {
			e.emit(", ")
		}
		e.emitLitElement(el.value, true)
	}
	e.emit("}")
	return true
}

// emitArrayLitVar declares a local initialized from an array or slice literal.
//
// An array is C's own aggregate initialization, `int a[3] = {1, 2, 3};` -- which is
// also why an array literal is only ever a declaration's initializer: C cannot
// assign an array, so there is nowhere else to put one.
//
// A slice literal has no such spelling. It lowers the way make does, to a backing
// array plus a { pointer, len, cap } header, the difference being that the backing
// array carries the values and its length is the number of them.
func (e *emitter) emitArrayLitVar(name string, typeAST []int32, lit Node) {
	if a, ok := e.arrayDim(typeAST); ok {
		// Fewer values than the length is legal and zeroes the rest, as in Go; more
		// is not. C only warns about the excess, and the extra values are dropped,
		// so saying so here is the difference between a diagnostic and a surprise.
		if n, err := strconv.Atoi(a.bound); err == nil && len(compositeLitElements(lit)) > n {
			e.fail("too many values in %s literal: %s but the length is %s", arrayTypeName(a), countUnits(len(compositeLitElements(lit)), "value"), a.bound)
			return
		}
		e.arrays[name] = a
		e.ind()
		e.emit(a.elem + " " + name + a.declSuffix() + " = ")
		if !e.emitLitValues(lit) {
			return
		}
		e.emit(";\n")
		return
	}
	elem, ok := e.sliceType(typeAST)
	if !ok {
		e.fail("unsupported array or slice literal type")
		return
	}
	e.needSlice(elem)
	cname := sliceCName(elem)
	e.sliceVars[name] = elem
	e.locals[name] = cname
	count := len(compositeLitElements(lit))
	if count == 0 {
		// "[]T{}" is an empty slice, not a slice of one zero element. C has no
		// zero-length array to point it at, and it needs none: the header is the
		// zero value, whose pointer is never dereferenced because the length is 0.
		e.ind()
		e.emit(cname + " " + name + " = {0};\n")
		return
	}
	backing := e.newBacking()
	n := strconv.Itoa(count)
	e.ind()
	e.emit(elem + " " + backing + "[" + n + "] = ")
	if !e.emitLitValues(lit) {
		return
	}
	e.emit(";\n")
	e.ind()
	e.emit(cname + " " + name + " = {" + backing + ", " + n + ", " + n + "};\n")
}

// sameArrayType reports whether a literal's bracketed type is the array type the
// variable was declared with, and reports the mismatch by name if not. The checker
// does not compare composite types yet, so this is where "var a [3]int = [2]int{}"
// is caught -- without it the literal's own extent would silently win.
func (e *emitter) sameArrayType(declared arrDim, litType []int32) bool {
	lit, ok := e.arrayDim(litType)
	if ok && lit.elem == declared.elem && slices.Equal(lit.bounds(), declared.bounds()) {
		return true
	}
	e.fail("a %s literal cannot initialize a variable declared %s", e.litTypeName(litType), arrayTypeName(declared))
	return false
}

// litTypeName renders a literal's bracketed type for a diagnostic, as the source
// spells it rather than as C would.
func (e *emitter) litTypeName(litType []int32) string {
	if a, ok := e.arrayDim(litType); ok {
		return arrayTypeName(a)
	}
	if elem, ok := e.sliceType(litType); ok {
		return "[]" + elem
	}
	return "array or slice"
}

// arrayTypeName spells an array type the way the source does, "[2][3]int", rather
// than the way C declares it, which puts the extents on the declarator.
func arrayTypeName(a arrDim) string {
	s := ""
	for _, b := range a.bounds() {
		s += "[" + b + "]"
	}
	return s + a.elem
}

// soleArrayLit matches an initializer that is exactly an array or slice literal,
// "[N]T{...}" or "[]T{...}". The bracketed type the grammar already allows as a
// value (so "make([]int, n)" parses) carries the composite literal as its tail, so
// the Factor's own nodes are the type -- which is why arrayDim and sliceType, both
// of which read that shape, can be handed them unchanged.
func (e *emitter) soleArrayLit(initExpr []int32) (typeAST []int32, lit Node, ok bool) {
	fac, ok := e.soleFactorNode(initExpr)
	if !ok {
		return nil, Node{}, false
	}
	return e.factorArrayLit(fac)
}

// factorArrayLit matches a Factor that is an array or slice literal: the bracketed
// type followed by a composite literal.
func (e *emitter) factorArrayLit(fac Node) (typeAST []int32, lit Node, ok bool) {
	kids := slices.Collect(it(fac.ast))
	if len(kids) == 0 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LBRACK {
		return nil, Node{}, false
	}
	last := kids[len(kids)-1]
	if last.sym != CompositeLit {
		return nil, Node{}, false
	}
	// The Factor's own nodes are the bracketed type, so they are what arrayDim and
	// sliceType read; the trailing CompositeLit is not part of the type and both
	// ignore it, looking only for the length Expression and the element Type.
	return fac.ast, last, true
}

// litFieldValues returns a composite literal's values in field order, and the
// fields they belong to. A positional literal is in that order already, one value
// per field, and needs no field list. A keyed one is rewritten into it, with a nil
// for every field the literal omits, so that both forms emit through the one path
// above.
//
// The rewrite is why keyed literals do not become C designated initializers, which
// look like the obvious lowering: flexcc mishandles those. "(P){.n = 5}" skipping a
// struct-typed field fails with "Expected multiple values", and so does naming the
// fields out of declaration order. Written out positionally, a keyed literal is
// exactly as compilable as the positional one it is equivalent to.
func (e *emitter) litFieldValues(name string, lit Node) (values []*Node, fields []structField, ok bool) {
	elements := compositeLitElements(lit)
	if len(elements) == 0 || !elements[0].keyed {
		for i := range elements {
			values = append(values, &elements[i].value)
		}
		return values, nil, true
	}
	fields = e.structs[name]
	values = make([]*Node, len(fields))
	for i := range elements {
		el := &elements[i]
		key, ok := e.soleToken(el.key.ast)
		if !ok || e.f.ch(key) != IDENT {
			// The checker refuses these, so reaching here means one got past it.
			e.fail("a composite literal key must be a field name")
			return nil, nil, false
		}
		nm := e.src(key)
		at := slices.IndexFunc(fields, func(f structField) bool { return f.name == nm })
		if at < 0 {
			e.fail("unknown field %s in a %s literal", nm, name)
			return nil, nil, false
		}
		values[at] = &el.value
	}
	return values, fields, true
}

// emitVarInit emits a variable declaration's initializer. A composite literal that
// is the whole of one is emitted as a brace initializer; see emitCompositeLit.
func (e *emitter) emitVarInit(initExpr []int32) {
	if name, lit, ok := e.soleCompositeLit(initExpr); ok {
		e.emitCompositeLit(name, lit, true)
		return
	}
	e.emitExpr(initExpr)
}

// emitVarList emits a local `var a, b = e0, e1` (typed or inferred): each name is
// an independent declaration taking its own value, so this is the single-name path
// repeated. A declared array type is refused -- C cannot initialize an array from
// an expression, and copying one needs the single-name path's memcpy.
func (e *emitter) emitVarList(names []string, typeAST []int32, inits [][]int32) {
	if typeAST != nil {
		if _, ok := e.arrayDim(typeAST); ok {
			e.fail("a multi-name array var with an initializer is not supported yet")
			return
		}
	}
	for i, nm := range names {
		if nm == "_" {
			e.emitDiscard(inits[i]) // declares nothing; the value's effects still run
			continue
		}
		if typeAST == nil {
			e.emitInferredLocal(nm, inits[i])
			continue
		}
		ctype := e.cType(typeAST)
		if ctype == "" {
			return
		}
		if elem, ok := e.sliceType(typeAST); ok {
			e.sliceVars[nm] = elem
		}
		e.locals[nm] = ctype
		e.emitVarDeclInit(ctype, nm, inits[i])
	}
}

// emitPackageVarList is emitVarList for package scope: each name becomes its own
// file-scope static. A constant initializer is emitted in place; anything else is
// zero-initialized and assigned in the synthesized package init, exactly as the
// single-name path does.
func (e *emitter) emitPackageVarList(names []string, typeAST []int32, inits [][]int32) {
	for i, nm := range names {
		if nm == "_" {
			continue // a blank package variable declares nothing
		}
		ctype := ""
		switch {
		case typeAST != nil:
			ctype = e.cType(typeAST)
		default:
			var ok bool
			if ctype, ok = e.inferCType(inits[i]); !ok {
				e.fail("cannot infer a type for the package variable %q", nm)
				return
			}
		}
		if ctype == "" {
			return
		}
		e.globals[nm] = ctype
		if e.isSliceCType(ctype) {
			e.globalSliceVars[nm] = sliceElemFromCName(ctype)
		}
		if e.staticInitOK(inits[i]) {
			e.emit("static " + ctype + " " + nm + " = ")
			e.emitGlobalInit(inits[i])
			e.emit(";\n")
			continue
		}
		e.emit("static " + ctype + " " + nm + " = " + e.zeroInitC(ctype) + ";\n")
		e.deferPkgInit(nm + " = " + e.exprC(inits[i]) + ";")
	}
}

// emitPackageDestructure lowers a package-scope `var a, b = f()` that distributes a
// multi-result call across several package variables. It is emitDestructure split
// across two locations: C forbids a call in a file-scope initializer, so each
// variable is declared static and zero-initialized here, while the call binds to a
// temporary and each variable reads its field in the synthesized package init.
func (e *emitter) emitPackageDestructure(names []string, rhs []int32) {
	callee, suffix, ok := e.directCall(rhs)
	if !ok {
		e.fail("destructuring into package variables requires a single function call on the right-hand side")
		return
	}
	resTypes, ok := e.funcRet[callee]
	if !ok {
		e.fail("destructuring into package variables requires a call to a function, not %q", callee)
		return
	}
	if len(resTypes) != len(names) {
		e.fail("assignment mismatch: %d variables but %s returns %d values", len(names), callee, len(resTypes))
		return
	}
	call := e.captureC(func() { e.emitCallExpr(callee, suffix) })
	// An all-blank `var _, _ = f()` keeps the call for its side effects but binds
	// nothing, so no result temporary is emitted -- an unused one would warn.
	if !slices.ContainsFunc(names, func(nm string) bool { return nm != "_" }) {
		e.deferPkgInit(call + ";")
		return
	}
	for i, nm := range names {
		if nm == "_" {
			continue // a blank package variable declares nothing
		}
		e.globals[nm] = resTypes[i]
		if e.isSliceCType(resTypes[i]) {
			e.globalSliceVars[nm] = sliceElemFromCName(resTypes[i])
		}
		e.emit("static " + resTypes[i] + " " + nm + " = " + e.zeroInitC(resTypes[i]) + ";\n")
	}
	tmp := e.newTmp()
	e.deferPkgInit(e.retStructName(callee) + " " + tmp + " = " + call + ";")
	for i, nm := range names {
		if nm == "_" {
			continue // its value is produced but bound to nothing
		}
		e.deferPkgInit(fmt.Sprintf("%s = %s._%d;", nm, tmp, i))
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
	// Channel type: "chan" Type -> the ogo_chan_<elem> rendezvous cell.
	if elem, ok := e.chanType(ast); ok {
		e.needChan(elem)
		return chanCName(elem)
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

// convType reports the C type of a conversion `T(x)` when recv names a type usable
// in one: a predeclared numeric type, or a named type over such a type. A cast
// `(T)(x)` expresses it. bool and string are not numeric conversions -- bool has no
// arithmetic source and a string conversion would need a copy -- so they are left
// to the generic call path, which fails honestly.
func (e *emitter) convType(recv string) (string, bool) {
	if recv == "bool" || recv == "string" {
		return "", false
	}
	if ct, ok := cTypes[recv]; ok {
		if strings.HasSuffix(ct, "_t") {
			e.includes["stdint.h"] = true // a fixed-width target needs its header
		}
		return ct, true // int, uint, byte, rune, the fixed-width names
	}
	if e.namedTypes[recv] {
		return recv, true // `type Celsius int` used as Celsius(x)
	}
	return "", false
}

// arrayType recognises a fixed-array type `[N]T`, returning the element C type and
// the C bound. A slice `[]T` (no bound) or a non-constant bound is not modelled.
func (e *emitter) arrayType(typeAST []int32) (elem, bound string, ok bool) {
	a, ok := e.arrayDim(typeAST)
	if !ok || a.dims() != 1 {
		return "", "", false // a multi-dimensional array has no single bound
	}
	return a.elem, a.bound, true
}

// arrayDim recognises a fixed array type `[N]T`, including a multi-dimensional
// `[N][M]T`, returning its element type and every extent. cType models no array
// type, so a nested element is resolved by recursing here rather than through it
// -- which is why `[2][3]int` used to fail as `unsupported type ""`.
func (e *emitter) arrayDim(typeAST []int32) (arrDim, bool) {
	nodes := slices.Collect(it(typeAST))
	if len(nodes) == 0 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != LBRACK {
		return arrDim{}, false
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
		return arrDim{}, false // a slice, or a malformed array
	}
	bound, ok := e.arrayBoundC(sizeAST)
	if !ok {
		return arrDim{}, false
	}
	if inner, ok := e.arrayDim(elemAST); ok {
		return arrDim{elem: inner.elem, bound: bound, inner: inner.bounds()}, true
	}
	elem := e.cType(elemAST)
	if elem == "" {
		return arrDim{}, false
	}
	return arrDim{elem: elem, bound: bound}, true
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

// accessCur is the value reached partway along an access chain: a plain C value, a
// slice header, or a fixed array with the extents it has left. Exactly one of the
// three holds at a time -- dims non-empty means an array, slice means a header,
// otherwise ctype is a plain value.
type accessCur struct {
	ctype string   // the plain value's C type
	elem  string   // a slice's or array's element type
	dims  []string // an array's remaining extents, outermost first
	slice bool
}

// accessBase resolves the start of a chain: a slice variable, an array variable,
// or a plain local/global.
func (e *emitter) accessBase(base string) (accessCur, bool) {
	if el, ok := e.sliceElem(base); ok {
		return accessCur{elem: el, slice: true}, true
	}
	if a, ok := e.arrayVar(base); ok {
		return accessCur{elem: a.elem, dims: a.bounds()}, true
	}
	if ct, ok := e.varType(base); ok {
		return accessCur{ctype: ct}, true
	}
	return accessCur{}, false
}

// accessSelect advances the chain by a field selector.
func (e *emitter) accessSelect(cur accessCur, field string) (accessCur, bool) {
	if cur.slice || len(cur.dims) != 0 || cur.ctype == "" {
		return accessCur{}, false // only a plain struct value has fields
	}
	if a, ok := e.structFieldArray(cur.ctype, field); ok {
		return accessCur{elem: a.elem, dims: a.bounds()}, true
	}
	ct, ok := e.structFieldType(cur.ctype, field)
	if !ok {
		return accessCur{}, false
	}
	if el, ok := e.sliceElemByName[ct]; ok {
		return accessCur{elem: el, slice: true}, true
	}
	return accessCur{ctype: ct}, true
}

// accessIndex advances the chain by one index, reporting the type reached and the
// bound to check against. A slice's bound is a length expression built from the
// prefix, so it is returned separately and is only usable while the prefix is
// still available as C text.
func (e *emitter) accessIndex(cur accessCur, prefix string) (next accessCur, lenExpr string, ok bool) {
	switch {
	case cur.slice:
		if prefix == "" {
			// The prefix has already been emitted, so ".len" cannot be formed from
			// it. Indexing a slice this deep in a chain is therefore not modelled.
			return accessCur{}, "", false
		}
		return e.plainOrSlice(cur.elem), prefix + ".len", true
	case len(cur.dims) != 0:
		rest := cur.dims[1:]
		if len(rest) != 0 {
			return accessCur{elem: cur.elem, dims: rest}, cur.dims[0], true
		}
		return e.plainOrSlice(cur.elem), cur.dims[0], true
	}
	return accessCur{}, "", false
}

// plainOrSlice classifies an element C type as a nested slice header or a plain
// value.
func (e *emitter) plainOrSlice(elem string) accessCur {
	if el, ok := e.sliceElemByName[elem]; ok {
		return accessCur{elem: el, slice: true}
	}
	return accessCur{ctype: elem}
}

// emitAccessChain emits `base` followed by an arbitrary run of selectors and
// indexes, returning the type reached. It is the general form of the four fixed
// shapes above (field access, index, field-then-index, index-then-select), and
// reaches chains none of them can express -- `s[i].v[j]`, where an index, a
// selector and another index alternate.
//
// The prefix is accumulated as C text until an index is emitted; after that,
// selectors are emitted directly, since the text is no longer a string that can be
// concatenated or used to build a ".len".
func (e *emitter) emitAccessChain(base string, steps []Node) (accessCur, bool) {
	cur, ok := e.accessBase(base)
	if !ok {
		return accessCur{}, false
	}
	// Type the whole chain before emitting any of it, so an unsupported step fails
	// without leaving a half-written expression behind.
	if _, ok := e.accessChainType(base, steps); !ok {
		return accessCur{}, false
	}
	prefix := base
	for _, n := range steps {
		switch n.sym {
		case Selector:
			f := e.soleIdent(n.ast)
			next, ok := e.accessSelect(cur, f)
			if !ok {
				return accessCur{}, false
			}
			sep := "."
			if e.isPointer(cur.ctype) {
				sep = "->"
			}
			if prefix != "" {
				prefix += sep + f
			} else {
				e.emit(sep + f)
			}
			cur = next
		case Index:
			low, _, isSlice := e.sliceParts(n.ast)
			if isSlice || low == nil {
				return accessCur{}, false
			}
			next, lenExpr, ok := e.accessIndex(cur, prefix)
			if !ok {
				return accessCur{}, false
			}
			open := "["
			if cur.slice {
				open = ".ptr["
			}
			e.emit(prefix + open)
			e.emitIndex(low, lenExpr)
			e.emit("]")
			prefix = ""
			cur = next
		default:
			return accessCur{}, false
		}
	}
	if prefix != "" {
		e.emit(prefix)
	}
	return cur, true
}

// accessChainType walks a chain without emitting, for inference and for validating
// ahead of emission.
func (e *emitter) accessChainType(base string, steps []Node) (accessCur, bool) {
	cur, ok := e.accessBase(base)
	if !ok {
		return accessCur{}, false
	}
	prefix := base // only its emptiness matters here, mirroring emitAccessChain
	for _, n := range steps {
		switch n.sym {
		case Selector:
			f := e.soleIdent(n.ast)
			if cur, ok = e.accessSelect(cur, f); !ok {
				return accessCur{}, false
			}
		case Index:
			if _, _, isSlice := e.sliceParts(n.ast); isSlice {
				return accessCur{}, false
			}
			if cur, _, ok = e.accessIndex(cur, prefix); !ok {
				return accessCur{}, false
			}
			prefix = ""
		default:
			return accessCur{}, false
		}
	}
	return cur, true
}

// factorAccessChain recognises an identifier followed by a run of selectors and
// indexes that mixes both kinds more than once -- the shapes the fixed helpers
// cannot match. Narrower chains are left to them, so their pinned output is
// unchanged.
func (e *emitter) factorAccessChain(kids []Node) (string, []Node, bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	steps := slices.Collect(it(kids[1].ast))
	if len(steps) == 0 {
		return "", nil, false
	}
	for _, n := range steps {
		if n.sym != Index && n.sym != Selector {
			return "", nil, false
		}
	}
	return e.src(kids[0].tok), steps, true
}

// isAccessChain reports whether every step is a selector or an index.
func isAccessChain(steps []Node) bool {
	if len(steps) == 0 {
		return false
	}
	for _, n := range steps {
		if n.sym != Index && n.sym != Selector {
			return false
		}
	}
	return true
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
		if a.dims() > 1 {
			return sliceSource{}, false // slicing a multi-dimensional array is not modelled
		}
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

// forHeader decomposes a "for" header. The grammar parses a leading Expression
// before it knows what the header is, so that Expression is the condition when
// nothing follows it, an init statement's left-hand side when a three-clause tail
// follows, or a range key when a range tail does -- the same left-factoring
// SwitchGuard uses.
type forHeader struct {
	// Three-clause / condition form.
	initLHS   []int32 // nil when there is no init statement
	initOp    Symbol  // ASSIGN or DEFINE
	initRHS   []int32
	cond      []int32 // nil for a conditionless loop
	postLHS   []int32 // nil when there is no post statement
	postOp    Symbol  // ASSIGN, DEFINE, INC or DEC
	postRHS   []int32
	hasClause bool

	// Range form.
	isRange   bool
	rangeExpr []int32 // the ranged operand
	keyVar    []int32 // the index variable, nil for `for range x`
	valVar    []int32 // the value variable, for `for i, v := range x`
	rangeDef  bool    // ":=" rather than "="
}

// parseForHeader reads a ForHeader node.
func (e *emitter) parseForHeader(n Node) (h forHeader, ok bool) {
	kids := slices.Collect(it(n.ast))
	// A header that opens with "range" is the no-variable form `for range x`.
	if len(kids) >= 1 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == RANGE {
		h.isRange = true
		for _, c := range kids {
			if c.sym == Expression {
				h.rangeExpr = c.ast
			}
		}
		return h, true
	}
	for _, c := range kids {
		switch c.sym {
		case Expression:
			// The leading expression: the condition, unless a tail reassigns it.
			h.cond = c.ast
		case ForRest:
			if !e.parseForRest(c, &h) {
				return h, false
			}
		}
	}
	return h, true
}

// parseForRest reads the ForRest following the leading Expression, distinguishing
// the three-clause tail from the range tail.
func (e *emitter) parseForRest(n Node, h *forHeader) bool {
	kids := slices.Collect(it(n.ast))
	// `, val := range x`: a comma makes this the two-variable range form, with the
	// leading expression as the key.
	if len(kids) >= 1 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == COMMA {
		h.isRange = true
		h.keyVar, h.cond = h.cond, nil
		seenRange := false
		for _, c := range kids {
			switch {
			case c.sym == 0 && e.f.ch(c.tok) == DEFINE:
				h.rangeDef = true
			case c.sym == 0 && e.f.ch(c.tok) == RANGE:
				seenRange = true
			case c.sym == Expression && !seenRange:
				h.valVar = c.ast
			case c.sym == Expression && seenRange:
				h.rangeExpr = c.ast
			}
		}
		return true
	}
	// Otherwise a leading semicolon or an assignment operator, then ForAssignRest.
	for _, c := range kids {
		switch {
		case c.sym == 0 && e.f.ch(c.tok) == SEMICOLON:
			// `for expr ; cond ; post`: a bare expression as init.
			h.hasClause = true
			h.initLHS, h.cond = h.cond, nil
		case c.sym == 0:
			h.initOp = e.f.ch(c.tok)
		case c.sym == ForAssignRest:
			if !e.parseForAssignRest(c, h) {
				return false
			}
		case c.sym == Expression:
			// A bare-expression init's condition/post follow it in this node.
			h.cond = c.ast
		case c.sym == ForPost:
			if !e.parseForPost(c, h) {
				return false
			}
		}
	}
	return true
}

// parseForAssignRest reads what follows `:=`/`=`: either `range x` (the
// single-variable range form) or the RHS, condition and post of a three-clause.
func (e *emitter) parseForAssignRest(n Node, h *forHeader) bool {
	kids := slices.Collect(it(n.ast))
	if len(kids) >= 1 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == RANGE {
		h.isRange = true
		h.keyVar, h.cond = h.cond, nil
		h.rangeDef = h.initOp == DEFINE
		for _, c := range kids {
			if c.sym == Expression {
				h.rangeExpr = c.ast
			}
		}
		return true
	}
	// The three-clause form: the leading expression was the init LHS.
	h.hasClause = true
	h.initLHS, h.cond = h.cond, nil
	semis := 0
	for _, c := range kids {
		switch {
		case c.sym == 0 && e.f.ch(c.tok) == SEMICOLON:
			semis++
		case c.sym == Expression && semis == 0:
			h.initRHS = c.ast
		case c.sym == Expression && semis == 1:
			h.cond = c.ast
		case c.sym == ForPost:
			if !e.parseForPost(c, h) {
				return false
			}
		}
	}
	return true
}

// parseForPost reads a ForPost node: `i++`, `i--`, or an assignment.
func (e *emitter) parseForPost(n Node, h *forHeader) bool {
	for c := range it(n.ast) {
		switch {
		case c.sym == Expression && h.postLHS == nil:
			h.postLHS = c.ast
		case c.sym == Expression:
			h.postRHS = c.ast
		case c.sym == 0:
			h.postOp = e.f.ch(c.tok)
		}
	}
	return h.postLHS != nil
}

func (e *emitter) emitFor(nodes []Node) {
	var body []int32
	var h forHeader
	for _, n := range nodes[1:] {
		switch n.sym {
		case ForHeader:
			var ok bool
			if h, ok = e.parseForHeader(n); !ok {
				e.fail("unsupported for-loop header")
				return
			}
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
	if h.isRange {
		e.emitRange(&h, body)
		return
	}
	e.ind()
	if !h.hasClause {
		// The one- and two-part forms keep their existing lowering: a conditionless
		// loop is `for (;;)`, a conditional one a `while`.
		if h.cond == nil {
			e.emit("for (;;) {\n")
		} else {
			e.emit("while ")
			e.emitCondition(h.cond)
			e.emit(" {\n")
		}
	} else {
		// The three-clause form maps onto C's own, including the init declaration:
		// C scopes a variable declared there to the loop, exactly as Go does.
		e.emit("for (")
		if h.initLHS != nil {
			lhs := e.exprC(h.initLHS)
			switch h.initOp {
			case DEFINE:
				ct, ok := e.inferCType(h.initRHS)
				if !ok {
					e.fail("cannot infer the type of a for-loop init variable")
					return
				}
				e.locals[lhs] = ct
				e.emit(ct + " " + lhs + " = " + e.exprC(h.initRHS))
			case ASSIGN:
				e.emit(lhs + " = " + e.exprC(h.initRHS))
			default:
				e.emit(lhs)
			}
		}
		e.emit("; ")
		if h.cond != nil {
			e.emit(e.exprC(h.cond))
		}
		e.emit("; ")
		if h.postLHS != nil {
			lhs := e.exprC(h.postLHS)
			switch h.postOp {
			case INC:
				e.emit(lhs + "++")
			case DEC:
				e.emit(lhs + "--")
			case ASSIGN, DEFINE:
				e.emit(lhs + " = " + e.exprC(h.postRHS))
			default:
				e.emit(lhs)
			}
		}
		e.emit(") {\n")
	}
	e.emitLoopBody(body, nil)
}

// emitLoopBody emits a loop body between the opening `{` and the closing `}`,
// running inject (if any) as the body's first statement -- the range value copy --
// and restoring the switch context, since a break inside the loop names the loop.
func (e *emitter) emitLoopBody(body []int32, inject func()) {
	e.indent++
	e.deferBlockDepth++
	savedSwitch := e.inSwitchCase
	e.inSwitchCase = false
	if inject != nil {
		inject()
	}
	e.emitBlockStmts(body)
	e.inSwitchCase = savedSwitch
	e.deferBlockDepth--
	e.indent--
	e.ind()
	e.emit("}\n")
}

// emitRange emits the range forms of "for". Each becomes a counting loop; the
// operand is evaluated once (hoisted to a temporary), matching Go, and the
// two-variable form copies the element into the value variable at the top of each
// iteration.
func (e *emitter) emitRange(h *forHeader, body []int32) {
	ct, _ := e.inferCType(h.rangeExpr)
	key := "_"
	if h.keyVar != nil {
		key = e.exprC(h.keyVar)
	}
	if key == "_" {
		key = e.newTmp() // `for range x`, or `for _ := range x`: a hidden counter
	}

	switch {
	case e.isSliceCType(ct):
		// Hoist the slice header so .len and .ptr come from one evaluation.
		hdr := e.newTmp()
		e.ind()
		e.emit(ct + " " + hdr + " = " + e.exprC(h.rangeExpr) + ";\n")
		e.locals[key] = "int"
		e.ind()
		e.emit("for (int " + key + " = 0; " + key + " < " + hdr + ".len; " + key + "++) {\n")
		e.emitLoopBody(body, e.rangeValueInject(h, e.sliceElemByName[ct], hdr+".ptr["+key+"]"))
	case e.rangeArray(h.rangeExpr) != nil:
		a := e.rangeArray(h.rangeExpr)
		base, _ := e.exprIdent(h.rangeExpr)
		e.locals[key] = "int"
		e.ind()
		e.emit("for (int " + key + " = 0; " + key + " < " + a.bound + "; " + key + "++) {\n")
		e.emitLoopBody(body, e.rangeValueInject(h, a.elem, base+"["+key+"]"))
	case ct == cString:
		// Ranging a string iterates its byte indices. Go's two-variable form yields
		// a rune, which would need UTF-8 decoding, so only the index form is offered.
		if h.valVar != nil {
			e.fail("ranging a string yields only the byte index (rune decoding is not supported yet)")
			return
		}
		hdr := e.newTmp()
		e.ind()
		e.emit("ogo_string " + hdr + " = " + e.exprC(h.rangeExpr) + ";\n")
		e.locals[key] = "int"
		e.ind()
		e.emit("for (int " + key + " = 0; " + key + " < " + hdr + ".len; " + key + "++) {\n")
		e.emitLoopBody(body, nil)
	default:
		// An integer range. Hoist the bound so a side-effecting or costly operand is
		// evaluated once, as Go does.
		if h.valVar != nil {
			e.fail("ranging an integer yields only the index")
			return
		}
		n := e.newTmp()
		e.ind()
		e.emit("int " + n + " = " + e.exprC(h.rangeExpr) + ";\n")
		e.locals[key] = "int"
		e.ind()
		e.emit("for (int " + key + " = 0; " + key + " < " + n + "; " + key + "++) {\n")
		e.emitLoopBody(body, nil)
	}
}

// rangeValueInject returns a closure declaring the value variable of a
// two-variable range, or nil for the index-only form. elem is the element C type
// and access the C expression reading the current element.
func (e *emitter) rangeValueInject(h *forHeader, elem, access string) func() {
	if h.valVar == nil {
		return nil
	}
	val := e.exprC(h.valVar)
	if val == "_" {
		return nil // the value is discarded
	}
	e.locals[val] = elem
	return func() {
		e.ind()
		e.emit(elem + " " + val + " = " + access + ";\n")
	}
}

// rangeArray returns the array dimension of a range operand that is a bare array
// variable, or nil.
func (e *emitter) rangeArray(expr []int32) *arrDim {
	base, ok := e.exprIdent(expr)
	if !ok {
		return nil
	}
	if a, ok := e.arrayVar(base); ok {
		return &a
	}
	return nil
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
	// A break written here names the switch, which the if/else lowering cannot
	// express; emitStatement refuses it while this is set.
	saved := e.inSwitchCase
	e.inSwitchCase = true
	defer func() { e.inSwitchCase = saved }()
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
// emitDefer records a `defer` statement and emits its argument capture here, where
// Go evaluates the arguments. emitDeferred replays the call in LIFO order at each
// return and at a fall-through function end. A defer in a nested block also arms a
// flag, so the replay can tell whether the block ran.
func (e *emitter) emitDefer(nodes []Node) {
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
	if recv := e.soleIdent(head.ast); recv == "print" || recv == "println" {
		// emitPrint renders per-type printf calls and does not go through
		// emitCallArgs, so a captured temporary would be ignored and the argument
		// re-evaluated at the return. Reject rather than deviate silently.
		if len(e.callArgExprs(suffix[len(suffix)-1].ast)) != 0 {
			e.fail("deferring print with arguments is not supported yet")
			return
		}
	}
	d := deferredCall{head: head, suffix: suffix, cond: e.deferBlockDepth > 0, slot: len(e.defers)}
	// The call suffix is last; its arguments are what get captured.
	call := suffix[len(suffix)-1]
	if call.sym != CallSuffix {
		e.fail("a defer statement must be a function call")
		return
	}
	for _, a := range e.callArgExprs(call.ast) {
		if e.isIntLiteral(a) {
			d.args = append(d.args, deferArg{expr: a.ast, inline: true})
			continue
		}
		ct, ok := e.inferCType(a.ast)
		if !ok {
			e.fail("cannot infer the type of a deferred call argument")
			return
		}
		d.args = append(d.args, deferArg{ctype: ct, expr: a.ast})
	}
	for i, a := range d.args {
		if a.inline {
			continue
		}
		e.ind()
		e.emit(deferArgName(d.slot, i) + " = ")
		e.emitExpr(a.expr)
		e.emit(";\n")
	}
	if d.cond {
		e.ind()
		e.emit(deferFlagName(d.slot) + " = 1;\n")
	}
	e.defers = append(e.defers, d)
}

// emitDeferDecls declares the temporaries backing every defer slot in the function,
// at function scope. They must outlive the block a defer was written in, so they
// cannot be declared at the defer itself. The body is emitted into a buffer first,
// so the full set is known by the time these are written ahead of it.
func (e *emitter) emitDeferDecls() {
	for _, d := range e.defers {
		if d.cond {
			e.ind()
			e.emit("int " + deferFlagName(d.slot) + " = 0;\n")
		}
		for i, a := range d.args {
			if a.inline {
				continue
			}
			e.ind()
			e.emit(a.ctype + " " + deferArgName(d.slot, i) + " = 0;\n")
		}
	}
}

// emitDeferred replays the recorded defers in LIFO order, each reading the
// arguments captured at its defer statement. One written in a nested block is
// replayed under its flag, so it runs only if that block did. Defers recorded after
// this point are not replayed here, which is right: a return textually before a
// defer cannot have reached it.
func (e *emitter) emitDeferred() {
	for i := len(e.defers) - 1; i >= 0; i-- {
		d := e.defers[i]
		e.deferReplay, e.deferReplayArgs = d.slot, d.args
		if d.cond {
			// The flag is a statement prefix, so the call emitCall writes lands as
			// the guarded statement: `if (_ogo_deferN) f(...);`.
			e.ind()
			e.emit("if (" + deferFlagName(d.slot) + ") ")
			saved := e.indent
			e.indent = 0
			e.emitCall(d.head, d.suffix)
			e.indent = saved
		} else {
			e.emitCall(d.head, d.suffix)
		}
		e.deferReplay, e.deferReplayArgs = -1, nil
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
		// A bare "return": main returns 0, a void function returns nothing, and a
		// named-result function returns its result variables (naked return).
		switch {
		case e.mainRet:
			e.emit("return 0;\n")
		case len(e.curResultNames) == 0:
			e.emit("return;\n")
		case len(e.curResultNames) == 1:
			e.emit("return " + e.curResultNames[0] + ";\n")
		default:
			e.emit("return (" + e.retStructName(e.curFunc) + "){")
			for i, nm := range e.curResultNames {
				if i != 0 {
					e.emit(", ")
				}
				e.emit(nm)
			}
			e.emit("};\n")
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
		if ct, ok := e.convType(recv); ok {
			// A conversion `T(x)` -> a C cast `(T)(x)`.
			args := e.callArgExprs(suffix[0].ast)
			if len(args) == 1 {
				e.emit("(" + ct + ")(")
				e.emitExpr(args[0].ast)
				e.emit(")")
				return true
			}
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
	// A bool prints as the word true or false, as in Go.
	if ct, ok := e.inferCType(arg.ast); ok && ct == cBool {
		e.ind()
		nl := ""
		if newline {
			nl = "\\n"
		}
		e.emit("printf(\"%s" + nl + "\", ")
		e.emitBoolWord(arg)
		e.emit(");\n")
		return
	}
	// Default: an integer, or an integer-typed expression. The conversion is %u for
	// an unsigned type so a large value prints unsigned, as in Go, rather than
	// wrapping negative.
	verb := e.scalarPrintVerbOf(arg)
	e.ind()
	if newline {
		e.emit("printf(\"" + verb + "\\n\", ")
	} else {
		e.emit("printf(\"" + verb + "\", ")
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
		for i, arg := range args {
			if i > 0 {
				e.emit(" ")
			}
			if e.isBoolPrint(arg) {
				e.emit("%s")
			} else {
				e.emit(e.scalarPrintVerbOf(arg))
			}
		}
		if newline {
			e.emit("\\n")
		}
		e.emit("\"")
		for _, arg := range args {
			e.emit(", ")
			if e.isBoolPrint(arg) {
				e.emitBoolWord(arg)
			} else {
				e.emitExpr(arg.ast)
			}
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

// isBoolPrint reports whether an argument prints as a bool word.
func (e *emitter) isBoolPrint(arg Node) bool {
	ct, ok := e.inferCType(arg.ast)
	return ok && ct == cBool
}

// emitBoolWord renders a bool argument as the string "true" or "false" via a
// ternary, so println(b) prints the word rather than 1 or 0.
func (e *emitter) emitBoolWord(arg Node) {
	e.emit("(")
	e.emitExpr(arg.ast)
	e.emit(") ? \"true\" : \"false\"")
}

// scalarPrintVerb is the printf conversion for a scalar C type: %u for an unsigned
// one, %d otherwise. Go prints an unsigned value as unsigned, so `%d` on a uint
// wrapped negative once the high bit was set. A narrow unsigned (uint8_t, uint16_t)
// promotes to int in varargs, but its value is non-negative, so %u reads it back
// unchanged; a uint / uint32_t stays unsigned int, which is exactly what %u wants.
func scalarPrintVerb(ct string) string {
	switch ct {
	case "unsigned", "uint8_t", "uint16_t", "uint32_t", "uintptr_t":
		return "%u"
	}
	return "%d"
}

// isIntCType reports whether ct is one of the integer C types an OctoGo numeric
// maps to. It is the printable-integer set: a named type over int (its own typedef
// name) is not in it, so a slice of one still fails honestly.
func isIntCType(ct string) bool {
	switch ct {
	case "int", "unsigned", "int8_t", "int16_t", "int32_t",
		"uint8_t", "uint16_t", "uint32_t", "uintptr_t":
		return true
	}
	return false
}

// sliceElemPrintf is the printf statement the slice/array printer runs for one
// element `s.ptr[_i]`: a bool as the word true/false and a string as its exact
// bytes, matching the scalar forms (emitBoolWord, ogo_print_str), and an integer
// with the width-appropriate verb from scalarPrintVerb.
func sliceElemPrintf(el string) string {
	switch el {
	case cBool:
		return `printf("%s", s.ptr[_i] ? "true" : "false");`
	case cString:
		return `printf("%.*s", s.ptr[_i].len, s.ptr[_i].str);`
	}
	return fmt.Sprintf(`printf("%s", s.ptr[_i]);`, scalarPrintVerb(el))
}

// scalarPrintVerbOf returns the print conversion for an argument, defaulting to %d
// when its type cannot be inferred (an integer expression).
func (e *emitter) scalarPrintVerbOf(arg Node) string {
	if ct, ok := e.inferCType(arg.ast); ok {
		return scalarPrintVerb(ct)
	}
	return "%d"
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
// printed: any integer width, a bool, or a string -- the scalar-printable types,
// each rendered by sliceElemPrintf. A slice of structs, pointers, or a named type
// still fails honestly until its own print form is wired up.
func (e *emitter) canPrintElem(elem string) bool {
	return elem == cBool || elem == cString || isIntCType(elem)
}

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
	// A target whose chain alternates indexes and selectors more than once --
	// `s[i].v[j] = e`. Tried first: the fixed shapes below cannot match it, and the
	// chain is typed before anything is emitted, so a rejected one leaves no
	// half-written statement.
	if chain := postfix[:len(postfix)-1]; stars == "" && isAccessChain(chain) {
		if cur, ok := e.accessChainType(base, chain); ok && !cur.slice && len(cur.dims) == 0 {
			t, ok := e.assignTailOf(postfix[len(postfix)-1])
			if !ok {
				e.fail("unsupported assignment form for an access chain")
				return
			}
			e.emitAssignTailOrCopy(func() { e.emitAccessChain(base, chain) }, t)
			return
		}
	}
	// A full index into a multi-dimensional array, `m[i][j] = v`: an optional field
	// chain then one Index per dimension.
	// An indexed element's field `s[i].x = v` / `p.pts[i].x = v`: an optional field
	// chain, one index, then at least one selector before the assignment. Tried
	// ahead of the index-last shape below, which cannot match a trailing selector.
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
	// PostfixOp = "<-" Expression (send), or ( "=" | ":=" ) ExpressionList, for the
	// single-target forms. The RHS of "="/":=" is a list of one here (a longer list
	// is a multiple assignment, handled above); a send keeps its bare Expression.
	if len(op) != 2 || op[0].sym != 0 {
		e.fail("only `name = expr`, `name := expr`, `name++` and `name--` are supported yet")
		return
	}
	opTok := e.f.ch(op[0].tok)
	var rhsAst []int32
	if opTok == ARROW {
		if op[1].sym != Expression {
			e.fail("unsupported send statement")
			return
		}
		rhsAst = op[1].ast
	} else {
		rhs := e.rhsExprs(op[1])
		if len(rhs) != 1 {
			e.fail("only `name = expr`, `name := expr`, `name++` and `name--` are supported yet")
			return
		}
		rhsAst = rhs[0].ast
	}
	switch opTok {
	case ARROW:
		// A send `ch <- v`. The receive form `x = <-ch` is an ordinary assignment
		// whose right-hand side is a receive expression, so it does not come here.
		ct, ok := e.varType(lhs)
		if !ok || !e.isChanCType(ct) {
			e.fail("a send statement needs a channel on the left")
			return
		}
		e.ind()
		e.chanSendElems[e.chanElemByName[ct]] = true
		e.emit(chanSendCName(e.chanElemByName[ct]) + "(" + lhs + ", ")
		e.emitExpr(rhsAst)
		e.emit(");\n")
		return
	case ASSIGN:
		if lhs == "_" {
			// A blank-identifier assignment discards the value: evaluate the
			// right-hand side for its side effects and drop the result. The
			// `(void)` cast makes the discard explicit and valid C even when the
			// expression is a plain value. (`_ := expr` is rejected by the checker.)
			e.emitDiscard(rhsAst)
			return
		}
		// A make initializer assigned to an existing lvalue -- a slice variable
		// (`s = make(...)`) or a slice struct field (`b.data = make(...)`) -- hoists a
		// backing array and assigns a fresh { backing, len, cap } header.
		if elem, lenAST, capAST, ok := e.makeSliceInit(rhsAst); ok {
			e.needSlice(elem)
			e.emitMakeSliceAssign(lhs, sliceCName(elem), elem, lenAST, capAST)
			return
		}
		// Shared with the indexed and access-chain targets, so a struct holding an
		// array becomes a memcpy here too (see emitAssignTailOrCopy).
		e.emitAssignTailOrCopy(func() { e.emit(lhs) }, assignTail{op: "=", rhs: rhsAst})
	case DEFINE:
		if len(fields) != 0 {
			e.fail("a short declaration cannot have a field target")
			return
		}
		e.emitInferredLocal(base, rhsAst)
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
	if typeAST, lit, ok := e.soleArrayLit(initExpr); ok {
		e.emitArrayLitVar(name, typeAST, lit)
		return
	}
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
	e.emitVarDeclInit(ct, name, initExpr)
}

// emitVarDeclInit emits a local declaration of ctype with an initializer. A struct
// holding an array is declared and then filled with memcpy: initializing one from
// another value is a copy, which flexcc gets wrong (see hasArrayField). A composite
// literal is not a copy -- it is the aggregate initialization that does work -- so
// it stays on the ordinary path.
func (e *emitter) emitVarDeclInit(ctype, name string, initExpr []int32) {
	if _, _, isLit := e.soleCompositeLit(initExpr); !isLit && e.hasArrayField(ctype) {
		if !e.checkStructCopySrc(ctype, initExpr) {
			return
		}
		e.ind()
		e.emit(ctype + " " + name + ";\n")
		e.emitStructCopy(name, ctype, initExpr)
		return
	}
	e.ind()
	e.emit(ctype + " " + name + " = ")
	e.emitVarInit(initExpr)
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
			if a.dims() > 1 {
				// One index into a [N][M]T yields a [M]T row, not an element. C
				// cannot assign an array by value, and typing the result as elem
				// would emit `int r = m[1];`, so leave it to the full-index path.
				return "", "", "", false
			}
			return base, a.elem, a.bound, true
		}
		return "", "", "", false
	}
	// An array-typed field indexes its inline storage directly, bounded by the
	// declared extent; a slice-typed one goes through its header's backing pointer,
	// bounded by the runtime length.
	if a, ok := e.fieldArray(base, pre); ok {
		if a.dims() > 1 {
			return "", "", "", false // see the variable case above
		}
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
// rhsExprs returns the right-hand-side expressions of an assignment: the children
// of an ExpressionList, or a single bare Expression. The assignment RHS is an
// ExpressionList, while a compound assignment's operand is a bare Expression.
func (e *emitter) rhsExprs(n Node) []Node {
	if n.sym == Expression {
		return []Node{n}
	}
	if n.sym != ExpressionList {
		return nil
	}
	var out []Node
	for c := range it(n.ast) {
		if c.sym == Expression {
			out = append(out, c)
		}
	}
	return out
}

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
	if len(op) != 2 {
		return assignTail{}, false
	}
	rhs := e.rhsExprs(op[1])
	if len(rhs) != 1 {
		return assignTail{}, false // a multi-value list is not a single-target tail
	}
	if op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		return assignTail{op: "=", rhs: rhs[0].ast}, true
	}
	if op[0].sym == AssignOp {
		if tok, ok := e.soleToken(op[0].ast); ok {
			sym := e.f.ch(tok)
			if c, ok := cAssignOps[sym]; ok {
				return assignTail{op: c, rhs: rhs[0].ast, complement: sym == ANDNOT_ASSIGN}, true
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

// emitAssignTailOrCopy emits an indented assignment statement whose target is
// written by target and whose tail is t, lowering the one case C's own assignment
// cannot express here: a struct that holds an array is copied with memcpy (see
// hasArrayField). target is rendered rather than streamed for that case, since
// memcpy needs the destination as an argument; it is called exactly once either
// way, and not at all if the copy is refused, so a refusal leaves no half-written
// statement.
func (e *emitter) emitAssignTailOrCopy(target func(), t assignTail) {
	if t.op == "=" {
		if ct, ok := e.inferCType(t.rhs); ok && e.hasArrayField(ct) {
			if !e.checkStructCopySrc(ct, t.rhs) {
				return
			}
			e.emitStructCopy(e.captureC(target), ct, t.rhs)
			return
		}
	}
	e.ind()
	target()
	e.emitAssignTail(t)
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
	e.emitAssignTailOrCopy(func() { e.emitIndexSelect(expr, lenExpr, low, elem, nil) }, t)
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
		if a.dims() > 1 {
			e.fail("a multi-dimensional array must be indexed in every dimension")
			return
		}
		lenExpr = a.bound
	}
	t, ok := e.assignTailOf(opNode)
	if !ok {
		e.fail("unsupported assignment form for an indexed target")
		return
	}
	e.emitAssignTailOrCopy(func() {
		e.emit(lhs + "[")
		e.emitIndex(idx, lenExpr)
		e.emit("]")
	}, t)
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
	var rhs []Node
	for _, n := range op {
		switch n.sym {
		case LhsItem:
			id := e.lhsItemIdent(n.ast)
			if id == "" {
				e.fail("only simple variable targets are supported in multiple assignment")
				return
			}
			targets = append(targets, id)
		case ExpressionList:
			rhs = e.rhsExprs(n)
		case 0:
			if ch := e.f.ch(n.tok); ch == ASSIGN || ch == DEFINE {
				define = ch == DEFINE
			}
		}
	}
	// One expression for several targets distributes a multi-result call; a matching
	// count is a value list assigned pairwise.
	if len(rhs) == 1 {
		e.emitDestructure(targets, define, rhs[0].ast)
		return
	}
	if len(rhs) != len(targets) {
		e.fail("assignment mismatch: %d targets, %d values", len(targets), len(rhs))
		return
	}
	e.emitValueList(targets, define, rhs)
}

// emitValueList lowers `a, b = c, d` (or `:=`). Every value is evaluated into a
// temporary first, then each target takes its temporary, so all right-hand sides
// see the pre-assignment values -- which is what makes `a, b = b, a` a swap.
func (e *emitter) emitValueList(targets []string, define bool, rhs []Node) {
	tmps := make([]string, len(rhs))
	types := make([]string, len(rhs))
	for i, r := range rhs {
		ct, ok := e.inferCType(r.ast)
		if !ok {
			e.fail("cannot infer the type of a value in a multiple assignment")
			return
		}
		types[i] = ct
		tmps[i] = e.newTmp()
		e.ind()
		e.emit(ct + " " + tmps[i] + " = ")
		e.emitExpr(r.ast)
		e.emit(";\n")
	}
	for i, tgt := range targets {
		if tgt == "_" {
			continue
		}
		e.ind()
		if define {
			e.locals[tgt] = types[i]
			e.emit(types[i] + " " + tgt + " = " + tmps[i] + ";\n")
		} else {
			e.emit(tgt + " = " + tmps[i] + ";\n")
		}
	}
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
	for i, arg := range e.callArgExprs(callSuffix) {
		if !first {
			e.emit(", ")
		}
		first = false
		// Replaying a deferred call reads the temporaries captured at the defer
		// statement rather than re-evaluating the expressions, which may name a
		// variable that has since changed or gone out of scope.
		if e.deferReplay >= 0 {
			if a := e.deferReplayArgs[i]; a.inline {
				e.emitExpr(a.expr)
			} else {
				e.emit(deferArgName(e.deferReplay, i))
			}
			continue
		}
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
			if fld.dim.bound != "" {
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
		if fld.name == field && fld.dim.bound != "" {
			return fld.dim, true
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
	ctype, ok := e.varType(base)
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
// type environment: base's (possibly pointer) struct type, then each field's type
// in turn.
func (e *emitter) fieldType(base string, fields []string) (string, bool) {
	ctype, ok := e.varType(base)
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
	ctype, _ := e.varType(base)
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
			return cBool, true // a comparison yields bool
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
		if elem, _, ok := e.recvOperand(n, kids); ok {
			return elem, true
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
			// "T{...}" is a value of T, and a struct's C type is the typedef named
			// after it, so the literal types itself.
			if name, _, ok := e.factorCompositeLit(kids); ok {
				return name, true
			}
			if recv, suffix, ok := e.factorCall(kids); ok {
				return e.callResultCType(recv, suffix)
			}
			if base, fields, ok := e.factorFieldAccess(kids); ok {
				return e.fieldType(base, fields)
			}
			// `s[i].v[j]` -- the general chain's result type.
			if base, steps, ok := e.factorAccessChain(kids); ok {
				// Fall through to the fixed shapes when the walker cannot type the
				// chain, rather than short-circuiting: a slice expression is theirs.
				if cur, ok := e.accessChainType(base, steps); ok && !cur.slice && len(cur.dims) == 0 {
					return cur.ctype, true
				}
			}
			// `m[i][j]` -- a fully indexed multi-dimensional array yields its element.
			// `s[i].x` / `p.pts[i].x` -- the element's selected field type.
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
				// A plain index yields the element type of an array or a slice. One
				// index into a multi-dimensional array yields a row instead, which
				// has no C value type, so it is refused rather than typed as elem.
				if a, ok := e.arrayVar(base); ok {
					if a.dims() > 1 {
						return "", false
					}
					return a.elem, true
				}
				if elem, ok := e.sliceElem(base); ok {
					return elem, true
				}
				if e.isStringVarName(base) {
					return "uint8_t", true // s[i] is a byte, as in Go
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
				return cBool, true // the predeclared bool constants
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
		if ct, ok := e.convType(recv); ok {
			return ct, true // a conversion T(x) has type T
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
		// A string-typed additive expression is concatenation. C cannot add two
		// ogo_string structs, and the target has no heap to build a new one at
		// runtime, so a concatenation of constants is folded to a single literal and
		// anything with a runtime operand is rejected.
		if ct, ok := e.inferCType(n.ast); ok && ct == cString {
			if v, ok := e.foldConstString(n.ast); ok {
				e.emitFoldedString(v)
				return
			}
			e.fail("string concatenation with a non-constant operand needs allocation, which the target does not have")
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
		// A receive `<-ch` wraps its operand in the channel's recv helper, so it
		// cannot be emitted as the operator token followed by the operand.
		if elem, base, ok := e.recvOperand(n, kids); ok {
			e.chanRecvElems[elem] = true
			e.emit(chanRecvCName(elem) + "(" + base + ")")
			return
		}
		if n.sym == Factor {
			if name, lit, ok := e.factorCompositeLit(kids); ok {
				e.emitCompositeLit(name, lit, e.declInit)
				return
			}
			// An array or slice literal is a declaration's initializer and nothing
			// else: C cannot assign an array, and a slice literal needs a backing
			// array hoisted beside the declaration it belongs to, which an
			// expression position has nowhere to put.
			if litType, _, ok := e.factorArrayLit(n); ok {
				e.fail("a %s literal is only supported as a variable's initializer", e.litTypeName(litType))
				return
			}
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
			// A chain that alternates indexes and selectors more than once --
			// `s[i].v[j]` -- which no fixed shape below can match.
			if base, steps, ok := e.factorAccessChain(kids); ok {
				if _, ok := e.emitAccessChain(base, steps); ok {
					return
				}
			}
			// `m[i][j]` -- a full index into a multi-dimensional array.
			// `s[i].x` / `p.pts[i].x` -- index a container, then select from the
			// element. Checked ahead of the index-only shapes, which cannot match a
			// trailing selector anyway.
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
				// A slice is indexed through its backing pointer, a string through its
				// byte pointer, an array directly. The index is bounds-checked against
				// the container length.
				lenExpr := ""
				switch {
				case e.hasSliceVar(base):
					e.emit(base + ".ptr[")
					lenExpr = base + ".len"
				case e.isStringVarName(base):
					e.emit(base + ".str[")
					lenExpr = base + ".len"
				default:
					if a, ok := e.arrayVar(base); ok && a.dims() > 1 {
						e.fail("a multi-dimensional array must be indexed in every dimension")
						return
					}
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
		case "iota":
			// Inside a const spec's expression, iota is its value; elsewhere it is an
			// ordinary name (the checker rejects a bare iota outside a const).
			if e.iota >= 0 {
				e.emit(strconv.Itoa(e.iota))
				return
			}
			e.emit(s)
		default:
			// A string constant is inlined as its folded literal -- it has no C
			// variable (see emitConstDecl).
			if v, ok := e.constStr[s]; ok {
				e.emitFoldedString(v)
				return
			}
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
// foldConstString folds a compile-time-constant string expression to its decoded
// value: a string literal, a string constant, or a concatenation (with "+") of
// those. It reports false for anything with a non-constant operand -- a variable --
// or a non-additive operator, which is what distinguishes a foldable concatenation
// from a runtime one that a zero-allocation target cannot perform.
func (e *emitter) foldConstString(ast []int32) (string, bool) {
	var b strings.Builder
	ok := true
	var walk func(ast []int32)
	walk = func(ast []int32) {
		for n := range it(ast) {
			if !ok {
				return
			}
			switch n.sym {
			case AddOp:
				if e.opText(n.ast) != "+" {
					ok = false // only "+" concatenates; a string has no other operator
				}
			case 0:
				switch e.f.ch(n.tok) {
				case STRING:
					v, err := strconv.Unquote(e.src(n.tok))
					if err != nil {
						ok = false
						return
					}
					b.WriteString(v)
				case IDENT:
					if cv, is := e.constStr[e.src(n.tok)]; is {
						b.WriteString(cv)
					} else {
						ok = false // a non-constant operand: a runtime concatenation
					}
				case LPAREN, RPAREN:
					// grouping, no value
				default:
					ok = false
				}
			default:
				walk(n.ast)
			}
		}
	}
	walk(ast)
	if !ok {
		return "", false
	}
	return b.String(), true
}

// emitFoldedString emits a decoded string value as an ogo_string, re-quoting it as
// a C string literal. Under declInit it is a brace, not a compound literal, since a
// file-scope initializer is not a constant expression otherwise.
func (e *emitter) emitFoldedString(v string) {
	e.usesString = true
	body := strconv.Quote(v) + ", " + strconv.Itoa(len(v))
	if e.declInit {
		e.emit("{" + body + "}")
	} else {
		e.emit("(" + cString + "){" + body + "}")
	}
}

func (e *emitter) emitStringLit(tok int32) {
	src := e.src(tok)
	if len(src) != 0 && src[0] == '`' {
		// A raw string is verbatim between the back quotes, with carriage returns
		// discarded (Go spec) and no escape processing. Its text is not a valid C
		// literal as-is (it may hold real newlines and unescaped backslashes), so
		// decode it and re-quote through the same path a folded string uses.
		e.emitFoldedString(strings.ReplaceAll(src[1:len(src)-1], "\r", ""))
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
