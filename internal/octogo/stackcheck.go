// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A recursion has no depth the backend's listing bounds (internal/build, stack.go).
// A goroutine reaching one is refused unless --gostack sizes the slots, and the one
// that is not ran past its slot in silence, the fence saying so only as it ended;
// main's cog, whose stack is the rest of Hub RAM, ran on past the top of it. The
// build finds the recursions in the listing of a first compile and has the program
// emitted again with a check at the entry of each function on one (StackChecks):
// the stack pointer against the end of the cog's stack, less what the function may
// push before the next check -- its margin -- and what the panic saying so needs,
// the reserve. Both are read off the listing of the compile with the checks in, and
// written into the C (WithStackMargins), so a recursion panics "goroutine stack
// overflow" before it writes past the end. Main's cog's end is Hub RAM's, which a
// check folds into one compare, `cmp ptra, ##K` and a conditional call of the
// panic: a goroutine's is its slot's, which its trampoline records for its cog
// where --gostack sized the slots and a goroutine reaches a recursion (perCog), and
// each check then reads its own cog's.

// StackChecks has the emitter check the stack at the entry of each function the C
// names fns, against the end of main's cog's stack, or with perCog against each
// cog's (see above).
func StackChecks(fns []string, perCog bool) EmitOption {
	return func(e *emitter) {
		e.stackChecks = map[string]bool{}
		for _, f := range fns {
			e.stackChecks[f] = true
		}
		e.stackPerCog = perCog
	}
}

// stackMarginName is the macro holding the margin of the check in the function the
// C names fn.
func stackMarginName(fn string) string { return "OGO_STACK_MARGIN_" + fn }

// emitStackCheck writes the stack check at the entry of the function being emitted,
// where the build asked for one.
func (e *emitter) emitStackCheck() {
	if !e.stackChecks[e.curFunc] {
		return
	}
	e.stackChecked[e.curFunc] = true
	e.needPanic()
	e.ind()
	e.emit("OGO_STACK_CHECK(" + stackMarginName(e.curFunc) + ");\n")
}

// stackTrampolineC is what a goroutine's trampoline does first where each cog's end
// of stack is checked: record its slot's, which the slot's high guard marks.
func (e *emitter) stackTrampolineC() string {
	if e.stackChecks == nil || !e.stackPerCog {
		return ""
	}
	return "\tOGO_STACK_SET(&OGO_GUARD_HI(a->ogo_slot));\n"
}

// stackCheckDefs defines the stack checks: the end of each cog's stack, the check,
// and each function's margin, which with the reserve the build sets.
func (e *emitter) stackCheckDefs() string {
	var b strings.Builder
	b.WriteString("// The stack checked at the entry of every function on a recursion: the build\n" +
		"// sets each margin, what the function may push before the next check, and the\n" +
		"// reserve, what the panic saying so needs (StackChecks).\n" +
		"#ifdef __FLEXC__\n" +
		"#define OGO_STACK_TOP 0x80000u // the end of Hub RAM, of main's cog's stack\n" +
		"#define OGO_STACK_RESERVE 0\n")
	if e.stackPerCog {
		b.WriteString("#define OGO_STACK_END (OGO_STACK_TOP - OGO_STACK_RESERVE)\n" +
			"// each cog's end of stack less the reserve: main's cog's Hub RAM's, a goroutine's its slot's\n" +
			"static unsigned ogo_stack_limit[8] = {OGO_STACK_END, OGO_STACK_END, OGO_STACK_END, OGO_STACK_END, OGO_STACK_END, OGO_STACK_END, OGO_STACK_END, OGO_STACK_END};\n" +
			"#define OGO_STACK_SET(end) ogo_stack_limit[_cogid()] = (unsigned)(end) - OGO_STACK_RESERVE\n" +
			"#define OGO_STACK_CHECK(m) if ((int)_PTRA > (int)(ogo_stack_limit[_cogid()] - (m))) ogo_panic(\"goroutine stack overflow\")\n")
	} else {
		b.WriteString("#define OGO_STACK_CHECK(m) if ((unsigned)_PTRA > OGO_STACK_TOP - OGO_STACK_RESERVE - (m)) ogo_panic(\"goroutine stack overflow\")\n")
	}
	b.WriteString("#else\n" +
		"#define OGO_STACK_SET(end)\n" +
		"#define OGO_STACK_CHECK(m)\n" +
		"#endif\n")
	for _, fn := range slices.Sorted(maps.Keys(e.stackChecked)) {
		fmt.Fprintf(&b, "#define %s 0\n", stackMarginName(fn))
	}
	return b.String()
}

var (
	stackMarginDef  = regexp.MustCompile(`(?m)^#define OGO_STACK_MARGIN_(\w+) (\d+)$`)
	stackReserveDef = regexp.MustCompile(`(?m)^(#define OGO_STACK_RESERVE )(\d+)$`)
)

// StackMargins answers the margins the stack checks of C were written with, in
// bytes, by the C name of the function each is in, and the reserve; no margins
// where C checks nothing.
func StackMargins(c []byte) (margins map[string]int, reserve int) {
	margins = map[string]int{}
	for _, m := range stackMarginDef.FindAllSubmatch(c, -1) {
		margins[string(m[1])], _ = strconv.Atoi(string(m[2]))
	}
	if m := stackReserveDef.FindSubmatch(c); m != nil {
		reserve, _ = strconv.Atoi(string(m[2]))
	}
	return margins, reserve
}

// WithStackMargins is C with the margins of its stack checks and their reserve set,
// in bytes; a check margins does not name keeps its own.
func WithStackMargins(c []byte, margins map[string]int, reserve int) []byte {
	c = stackMarginDef.ReplaceAllFunc(c, func(def []byte) []byte {
		m := stackMarginDef.FindSubmatch(def)
		if n, ok := margins[string(m[1])]; ok {
			return fmt.Appendf(nil, "#define %s %d", stackMarginName(string(m[1])), n)
		}
		return def
	})
	return stackReserveDef.ReplaceAll(c, fmt.Appendf(nil, "${1}%d", reserve))
}
