// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"modernc.org/ogo/internal/octogo"
)

// A goroutine runs on a stack of its own, a slot of the pool the emitted runtime
// keeps in hub RAM, and a slot has the size the C was emitted with: 256 longs unless
// --gostack asked for another. A goroutine holding a table of 512 bytes in a local
// ran past it, and the fence the runtime keeps at the slot's end said so only when
// the goroutine ENDED -- having written over whatever lay beyond, a running
// goroutine's slot among them. The backend's listing has every function's frame and
// every call, so the build reads the stack each goroutine needs off it and compiles
// the same C again with each slot what its goroutines need (compileSized): a slot a
// go statement has to itself what that goroutine needs, every slot of the shared
// pool what the deepest does (octogo.GoSlots). Nothing is asked of the program.
// Until 2026-10-17 a slot was only ever made larger, 256 longs being the least
// any had; a goroutine of the run cases needs about 160, the most of it the panic
// path's formatting.
//
// A frame is what pushregs_ saves, a long for each register it is told to (COUNT_)
// and three of its own, and what `add ptra` reserves past them; a function calling
// no other keeps its return address in the cog's hardware stack and has none. A
// call is `call #name`, and a call through a register is a call of every function
// whose address is taken on its side: the library's calls through a FILE's
// functions reach the library's handlers, the program's calls through a function
// value or a table reach the program's functions. A library function reading ptra
// into a register formats into the memory past it unreserved (the number printer
// does), which is given 256 bytes.
//
// Recursion has no depth a listing can bound, and a build starting such a goroutine
// is refused unless --gostack gives the slots a size: left to the 256 longs a slot
// held by default, it ran past them in silence, the fence saying so only when it
// ended. A recursion is a cycle of direct calls and interface calls, which reach
// what their tables' slots hold (below): an expression tree evaluated through an
// interface on a goroutine, 200 deep, was given what one pass through the cycle
// needs, and a P2-EDGE printed nothing at all. A call through a function value
// reaches what the value's type may hold, as the build's table says
// (octogo.FuncValueClasses): obeam's interpreter calls its BIFs and its instruction
// handlers through two tables of one signature and two defined types, and each
// reaches its own functions. That says where a recursion is and nothing else: for a
// stack's size such a call still reaches every taken function. A cycle through any
// other call through a register is walked once, the deepest simple path through
// it: such a call is WIDE, taken to reach every function whose address its side
// takes, so it closes cycles no run makes -- the library's flush calling a FILE's
// function that can reach flush again -- and a recursion only a wide call closes
// goes unseen. A recursion a function reaches by the calls it is known to make is
// one, cycle or not.
//
// A recursion where it may run -- on main's cog, and on a goroutine whose slots
// --gostack sized -- is checked as it runs (compileSized, octogo.StackChecks), and
// the listing says what the checks need: which functions the C checks, the margin
// each check covers -- its function's frame and what its calls take until the next
// check, read off the graph where a checked function calls nothing -- what the panic
// of one needs past them (the reserve), what main needs up to its checks, and the
// recursions no check covers yet.
//
// The program's side was as wide as the program: p2-11's disk cog, whose FAT code
// calls the card through an interface, was charged the deepest path through every
// PDP-11 device method a table holds, 809 longs where 432 are reachable. An
// interface call reaches only the functions its table's slot holds, which the C
// says (octogo.ScanCalls), so a program function whose calls through a register are
// all interface calls -- in its own C and in the C of what was inlined into it, the
// functions its C calls by name and its listing does not -- reaches those, where
// the listing agrees: its calls through a register number exactly the interface
// calls the C has there (narrowTargets). Any other function, and any that does not
// agree, reaches every taken function of its side.

// hubRAMBytes is the P2's Hub RAM, which a program's code, data and main stack
// share.
const hubRAMBytes = 512 << 10

// stackScratch is what a function formatting into the memory past ptra may use.
const stackScratch = 256

// cogStartLongs is what the cog's entry takes off the base of the slot before it
// calls the trampoline: _cogstart_C leaves the function and its argument there, and
// the entry reads them and starts the call one long in.
const cogStartLongs = 1

// maxInlineVisits bounds the copies of the functions inlined into one that are read
// for its calls (narrowTargets); past it the function is every taken function's.
const maxInlineVisits = 4096

// maxWalkComponent is the largest component of functions joined by indirect calls
// that is searched path by path, its visited set a bit mask; a larger one is taken
// to need every frame it holds, which no path through it exceeds.
const maxWalkComponent = 20

var (
	stackLabel  = regexp.MustCompile(`^(_\w+)$`)
	stackCount  = regexp.MustCompile(`^\s*mov\s+COUNT_, ##?(\d+)`)
	stackAdd    = regexp.MustCompile(`^\s*add\s+ptra, ##?(-?\d+)`)
	stackPush   = regexp.MustCompile(`^\s*call\s+#pushregs_\s*$`)
	stackCall   = regexp.MustCompile(`^\s*(?:if_\w+\s+)?(?:call|jmp)\s+#(_\w+)`)
	stackICall  = regexp.MustCompile(`^\s*(?:if_\w+\s+)?(?:call|calla|callb)\s+[^#\s]`)
	stackAddr   = regexp.MustCompile(`@(_\w+)`)
	stackRead   = regexp.MustCompile(`^\s*mov\s+\w+, ptra\s*$`)
	stackStatic = regexp.MustCompile(`_\d{4}$`)
	stackTramp  = regexp.MustCompile(`^_ogo_go\d+(?:_\d{4})?$`)
	// cFuncName is a function's definition in the emitted C, a one-line body
	// included: the name before the parameters, no initializer's '=' ahead of it.
	cFuncName = regexp.MustCompile(`(?m)^[A-Za-z_][^=;(\n]*?(\w+)\([^;\n]*\)[A-Za-z_ ]*\{`)
)

type stackFunc struct {
	count    int  // registers pushregs_ saves
	pushes   bool // the function has a frame
	reserve  int  // bytes add ptra reserves, and ptra++ pushes
	scratch  bool // formats into the memory past ptra
	calls    []string
	indirect int // calls through a register
}

// goNeed is what one go statement's goroutine needs: the longs of its stack, -1
// where it recurses, and the function it runs, or where it recurses, as the C
// names it.
type goNeed struct {
	longs int
	name  string
}

type stackEdge struct {
	to       string
	indirect bool
	// wide is an indirect call taken to reach every function whose address its
	// side takes, which closes cycles no run makes; an interface call reaches
	// what its table's slot holds, and is not wide.
	wide bool
	// recursion is a call through a function value to what the value's type may
	// hold, read for a recursion alone: the stack it takes is read off the wide
	// edges beside it, as before such calls were narrowed.
	recursion bool
}

// stackNeeds is what a program's stacks need, read from the backend's listing of
// it: the deepest goroutine's in longs, 0 for a program that starts none, each
// goroutine's, and the main cog's in bytes, -1 where recursion through direct calls
// leaves it unbounded.
type stackNeeds struct {
	goLongs   int
	goKnown   bool   // every goroutine's need is bounded
	goName    string // the deepest goroutine's function, as the C names it
	goRecurse string // where the first goroutine of unbounded need recurses, as the C names it
	// goTramps is each goroutine's need by its trampoline, as the C names that.
	goTramps map[string]goNeed
	mainNeed int
	// mainAtLeast is what main's stack needs at the least, in bytes, recursion or
	// not: the deepest chain of direct calls entering each component of them once.
	mainAtLeast int
	// margins is what each stack check the C carries covers, in bytes, by the C
	// name of the function it is in, where no recursion it reaches is unchecked;
	// reserve is what the panic of a check needs.
	margins map[string]int
	reserve int
	// mainCut is what main's stack needs until its checks take over, -1 where a
	// recursion it reaches is not checked.
	mainCut int
	// recMain and recGo are the recursions main's cog and the goroutines reach that
	// no check covers, by the C names of the functions to check.
	recMain, recGo []string
}

// readStackNeeds reads a program's stack needs off the listing the backend wrote
// for it and the C it was compiled from.
func readStackNeeds(listing, c []byte) (r stackNeeds) {
	lines := strings.Split(string(listing), "\n")
	labels := map[string]bool{}
	for _, l := range lines {
		if m := stackLabel.FindStringSubmatch(l); m != nil {
			labels[m[1]] = true
		}
	}
	fns := map[string]*stackFunc{}
	taken := map[string]bool{}
	var cur *stackFunc
	var curName string
	for _, l := range lines {
		if m := stackLabel.FindStringSubmatch(l); m != nil {
			switch {
			case labels[m[1]+"_ret"]:
				cur, curName = &stackFunc{}, m[1]
				fns[m[1]] = cur
				continue
			case cur != nil && m[1] == curName+"_ret":
				cur = nil
				continue
			}
		}
		for _, m := range stackAddr.FindAllStringSubmatch(l, -1) {
			taken[m[1]] = true
		}
		if cur == nil {
			continue
		}
		if m := stackCount.FindStringSubmatch(l); m != nil {
			cur.count, _ = strconv.Atoi(m[1])
		}
		if m := stackAdd.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n < 0 {
				// A frame past 2 GB, wrapped negative by the backend's 32-bit
				// arithmetic: no frame shrinks the stack, and the bound reads it
				// as larger than any Hub RAM.
				n = 1 << 31
			}
			cur.reserve += n
		}
		if strings.Contains(l, "ptra++") {
			cur.reserve += 4
		}
		if stackRead.MatchString(l) {
			cur.scratch = true
		}
		switch m := stackCall.FindStringSubmatch(l); {
		case stackPush.MatchString(l):
			cur.pushes = true
		case m == nil:
			if stackICall.MatchString(l) {
				cur.indirect++
			}
		case labels[m[1]+"_ret"]:
			cur.calls = append(cur.calls, m[1])
		}
	}

	var tramps []string
	for n := range fns {
		if stackTramp.MatchString(n) {
			tramps = append(tramps, n)
		}
	}

	// The program's functions, by the names the C gives them; the listing writes
	// a static one with a number after it.
	own := map[string]bool{}
	for _, m := range cFuncName.FindAllSubmatch(c, -1) {
		own[string(m[1])] = true
	}
	isOwn := func(n string) bool {
		n = strings.TrimPrefix(n, "_")
		return own[n] || own[stackStatic.ReplaceAllString(n, "")]
	}
	var ownTaken, libTaken []string
	for n := range taken {
		switch {
		case fns[n] == nil || stackTramp.MatchString(n):
			// a trampoline's address goes to the cog it starts, never to a call
		case isOwn(n):
			ownTaken = append(ownTaken, n)
		default:
			libTaken = append(libTaken, n)
		}
	}

	names := make([]string, 0, len(fns))
	for n := range fns {
		names = append(names, n)
	}
	sort.Strings(names)
	scan := octogo.ScanCalls(c)
	byCName := map[string]string{}
	for _, n := range names {
		byCName[stackCName(n)] = n
	}
	frame := map[string]int{}
	succ := map[string][]stackEdge{}
	for _, n := range names {
		f := fns[n]
		fr := f.reserve
		if f.pushes {
			fr += 4 * (f.count + 3)
		}
		if f.scratch {
			fr += stackScratch
		}
		frame[n] = fr
		for _, c := range f.calls {
			succ[n] = append(succ[n], stackEdge{to: c})
		}
		if f.indirect != 0 {
			targets, wide := libTaken, true
			var recursion []string
			if isOwn(n) {
				targets = ownTaken
				switch t, ok, viaValue := narrowTargets(n, fns, scan, byCName); {
				case ok && viaValue:
					// What a function value's type may hold says where a
					// recursion is; a stack is sized as before, the call
					// reaching every taken function.
					recursion = t
				case ok:
					targets, wide = t, false
				}
			}
			for _, c := range targets {
				succ[n] = append(succ[n], stackEdge{to: c, indirect: true, wide: wide})
			}
			for _, c := range recursion {
				succ[n] = append(succ[n], stackEdge{to: c, indirect: true, recursion: true})
			}
		}
	}

	g := stackGraphOf(names, fns, succ, frame)
	all, direct, members, recursive, need := g.all, g.direct, g.members, g.recursive, g.need

	r.goKnown = true
	r.goTramps = map[string]goNeed{}
	sort.Strings(tramps)
	for _, t := range tramps {
		v := need(t)
		// A recursion the goroutine reaches by the calls it is known to make, a
		// function value's to what its type may hold among them.
		through := ""
		if v >= 0 {
			if through = g.recurses(t); through != "" {
				v = -1
			}
		}
		if v < 0 {
			name := through
			if name == "" {
				name = recursingName(t, succ, recursive, need)
			}
			if fc := scan.Funcs[name]; fc != nil && scan.InTable(name) && len(fc.Direct) == 1 {
				name = fc.Direct[0] // the method an interface's thunk calls
			}
			if r.goKnown {
				r.goRecurse = name
			}
			r.goKnown = false
			r.goTramps[stackCName(t)] = goNeed{-1, name}
			continue
		}
		longs, name := cogStartLongs+(v+3)/4, goroutineName(fns[t].calls, need)
		r.goTramps[stackCName(t)] = goNeed{longs, name}
		if longs > r.goLongs {
			r.goLongs, r.goName = longs, name
		}
	}
	r.mainNeed = need("_main")
	if g.recurses("_main") != "" {
		r.mainNeed = -1
	}
	// Where main reaches a recursion, need has no answer, and a frame past Hub RAM
	// on the way to it went unasked: a mutant's 512 KB array result, beside a
	// recursive function, built and printed nothing at all. Any chain of direct
	// calls is one the program may make, so the deepest one entering each
	// component once is a bound from below; an indirect call's targets are every
	// function its side takes the address of, which is no chain.
	least := map[int]int{}
	var atLeast func(string) int
	atLeast = func(n string) int {
		if v, ok := least[direct[n]]; ok {
			return v
		}
		least[direct[n]] = 0 // a cycle back into the component adds nothing
		deep := 0
		for _, m := range members[all[n]] {
			if direct[m] != direct[n] {
				continue
			}
			for _, e := range succ[m] {
				if !e.indirect && direct[e.to] != direct[n] && fns[e.to] != nil {
					deep = max(deep, atLeast(e.to))
				}
			}
		}
		v := frame[n] + deep
		least[direct[n]] = v
		return v
	}
	if fns["_main"] != nil {
		r.mainAtLeast = atLeast("_main")
	}

	// The stack checks the C carries (octogo.StackChecks): a function checked as it
	// enters, and one whose only call is of a checked function the backend inlined
	// into it -- an interface's thunk, a method value's wrapper -- which so checks as
	// it enters too. A check covers what its function pushes until the next check:
	// its frame and what its calls need in the graph where a checked function calls
	// nothing, which is the check's margin.
	written, _ := octogo.StackMargins(c)
	inlinedInto := func(cn string) string { // the function inlined into cn, its one call
		if fc := scan.Funcs[cn]; fc != nil && len(fc.Direct) == 1 && len(fc.Iface) == 0 && len(fc.FV) == 0 && fc.Other == 0 && byCName[fc.Direct[0]] == "" {
			return fc.Direct[0]
		}
		return ""
	}
	checked := map[string]string{} // by the listing's name, the function whose margin its check reads
	for _, n := range names {
		cn := stackCName(n)
		if _, ok := written[cn]; ok {
			checked[n] = cn
		} else if d := inlinedInto(cn); d != "" {
			if _, ok := written[d]; ok {
				checked[n] = d
			}
		}
	}
	// The panic is the reserve's, past the margins and left out of them: whatever
	// calls it calls it from no further than a margin past the check before.
	panicFn := byCName["ogo_panic"]
	cg := g
	if len(checked) != 0 {
		cut := map[string][]stackEdge{}
		for n, es := range succ {
			if _, ok := checked[n]; !ok {
				for _, e := range es {
					if e.to != panicFn {
						cut[n] = append(cut[n], e)
					}
				}
			}
		}
		cg = stackGraphOf(names, fns, cut, frame)
	}
	r.margins = map[string]int{}
	for n, fn := range checked {
		deep := 0
		for _, e := range succ[n] {
			if e.to == panicFn {
				continue
			}
			if !e.wide && cg.recurses(e.to) != "" {
				deep = -1 // a recursion no check covers: a function to check, below
				break
			}
			if e.recursion {
				continue
			}
			v := cg.need(e.to)
			if v < 0 {
				deep = -1
				break
			}
			deep = max(deep, v)
		}
		if deep >= 0 {
			r.margins[fn] = max(r.margins[fn], frame[n]+deep)
		}
	}
	if panicFn != "" {
		r.reserve = max(need(panicFn), 0)
	}
	r.mainCut = -1
	if fns["_main"] != nil && cg.recurses("_main") == "" {
		r.mainCut = cg.need("_main")
	}
	// The recursions no check covers, reached from main's cog and from the
	// goroutines', by the functions to check: the program's own, an interface's
	// thunk by the method inlined into it.
	// A recursion of the calls known to make, a function value's included, is
	// asked of the calls known to be made; one of direct and interface calls is
	// asked of every call, as before.
	uncovered := func(roots []string) (to []string) {
		add := func(n string) {
			cn := stackCName(n)
			if d := inlinedInto(cn); d != "" {
				cn = d
			}
			if !slices.Contains(to, cn) {
				to = append(to, cn)
			}
		}
		for _, known := range []bool{false, true} {
			seen := map[string]bool{}
			var walk func(string)
			walk = func(n string) {
				if seen[n] || fns[n] == nil {
					return
				}
				seen[n] = true
				if (known && cg.recursion[n] || !known && cg.recursive[n]) && isOwn(n) {
					add(n)
				}
				for _, e := range succ[n] {
					if known && !e.wide || !known && !e.recursion {
						walk(e.to)
					}
				}
			}
			for _, n := range roots {
				walk(n)
			}
		}
		sort.Strings(to)
		return to
	}
	r.recMain = uncovered([]string{"_main"})
	r.recGo = uncovered(tramps)
	return r
}

// stackGraph is what the stacks of a call graph need: need answers the bytes a call
// of a function may take, its frame and what it calls, -1 where a recursion leaves
// that unbounded.
type stackGraph struct {
	all, direct map[string]int // each function's component, over every call and over the direct ones
	members     map[int][]string
	recursive   map[string]bool // on a cycle of direct and interface calls
	need        func(string) int
	// recursion is a function on a cycle of the calls known to be made: direct,
	// interface and function value calls (stackEdge.recursion). recurses names, as
	// the C does, the first such function a function reaches by those calls, ""
	// for none.
	recursion map[string]bool
	recurses  func(string) string
}

// stackGraphOf is the stacks the functions names, of the listing fns, need, by the
// calls succ and the frames frame.
func stackGraphOf(names []string, fns map[string]*stackFunc, succ map[string][]stackEdge, frame map[string]int) stackGraph {
	// A stack is sized by the calls a recursion edge is beside (stackEdge.recursion),
	// so everything but recursion and recurses reads without them.
	all := stackComponents(names, succ, func(e stackEdge) bool { return !e.recursion })
	direct := stackComponents(names, succ, func(e stackEdge) bool { return !e.indirect })
	precise := stackComponents(names, succ, func(e stackEdge) bool { return !e.wide && !e.recursion })
	members := map[int][]string{}
	preciseSize := map[int]int{}
	for _, n := range names {
		members[all[n]] = append(members[all[n]], n)
		preciseSize[precise[n]]++
	}
	recursive := map[string]bool{}
	for _, n := range names {
		if preciseSize[precise[n]] > 1 {
			recursive[n] = true
		}
		for _, e := range succ[n] {
			if !e.wide && !e.recursion && e.to == n {
				recursive[n] = true
			}
		}
	}
	known := stackComponents(names, succ, func(e stackEdge) bool { return !e.wide })
	knownSize := map[int]int{}
	for _, n := range names {
		knownSize[known[n]]++
	}
	recursion := map[string]bool{}
	for _, n := range names {
		if knownSize[known[n]] > 1 {
			recursion[n] = true
		}
		for _, e := range succ[n] {
			if !e.wide && e.to == n {
				recursion[n] = true
			}
		}
	}
	reaches := map[string]string{}
	var recurses func(string) string
	recurses = func(n string) string {
		if r, ok := reaches[n]; ok {
			return r
		}
		reaches[n] = "" // a cycle met again is answered where it began
		r := ""
		if recursion[n] {
			r = stackStatic.ReplaceAllString(strings.TrimPrefix(n, "_"), "")
		}
		for _, e := range succ[n] {
			if r != "" {
				break
			}
			if !e.wide {
				r = recurses(e.to)
			}
		}
		reaches[n] = r
		return r
	}

	memo := map[string]int{}
	var need func(string) int
	// external is the deepest stack below u through a call leaving u's component.
	external := func(u string) int {
		deep := 0
		for _, e := range succ[u] {
			if e.recursion || all[e.to] == all[u] {
				continue
			}
			v := need(e.to)
			if v < 0 {
				return -1
			}
			deep = max(deep, v)
		}
		return deep
	}
	within := func(n string) int {
		// A recursion n reaches by the calls it is known to make is a recursion,
		// cycle or not; one it reaches only through a wide call is walked once,
		// as the cycle is.
		reach, seen := []string{n}, map[string]bool{n: true}
		for len(reach) != 0 {
			u := reach[len(reach)-1]
			reach = reach[:len(reach)-1]
			if recursive[u] {
				return -1
			}
			for _, e := range succ[u] {
				if !e.wide && !e.recursion && all[e.to] == all[n] && !seen[e.to] {
					seen[e.to] = true
					reach = append(reach, e.to)
				}
			}
		}
		m := members[all[n]]
		ext := map[string]int{}
		for _, u := range m {
			v := external(u)
			if v < 0 {
				return -1
			}
			ext[u] = v
		}
		if len(m) > maxWalkComponent {
			sum, deep := 0, 0
			for _, u := range m {
				sum += frame[u]
				deep = max(deep, ext[u])
			}
			return sum + deep
		}
		idx := map[string]int{}
		for i, u := range m {
			idx[u] = i
		}
		type state struct {
			u   int
			vis uint32
		}
		dp := map[state]int{}
		var walk func(u int, vis uint32) int
		walk = func(u int, vis uint32) int {
			k := state{u, vis}
			if v, ok := dp[k]; ok {
				return v
			}
			deep := ext[m[u]]
			for _, e := range succ[m[u]] {
				if j, in := idx[e.to]; in && !e.recursion && vis&(1<<j) == 0 {
					deep = max(deep, walk(j, vis|1<<j))
				}
			}
			dp[k] = frame[m[u]] + deep
			return dp[k]
		}
		return walk(idx[n], 1<<idx[n])
	}
	need = func(n string) int {
		if v, ok := memo[n]; ok {
			return v
		}
		if fns[n] == nil {
			return 0
		}
		v := -1
		switch {
		case recursive[n]:
		case len(members[all[n]]) == 1:
			if v = external(n); v >= 0 {
				v += frame[n]
			}
		default:
			v = within(n)
		}
		memo[n] = v
		return v
	}

	return stackGraph{all: all, direct: direct, members: members, recursive: recursive, need: need, recursion: recursion, recurses: recurses}
}

// stackCName is the name the C gives the function the listing names n: without the
// listing's underscore, and without the number it gives a static function.
func stackCName(n string) string {
	return stackStatic.ReplaceAllString(strings.TrimPrefix(n, "_"), "")
}

// narrowTargets answers the functions the calls through a register of the program
// function n may reach where they are all interface calls or calls through function
// values, and false where that is not known. The C read is n's own and that of
// every function it calls by name that n's listing does not call -- inlined into
// it, or gone -- and every call there must be by name, through a table or through a
// value spelled with its type (no Other), the tables must hold something in each
// slot called, the build's table must say what each type of value may hold
// (octogo.FuncValueClasses), and n's listing must make as many calls through a
// register as the C makes through tables and values. viaValue says some of them are
// through values: the caller reads those targets for a recursion alone. Anything
// else is the caller's every-taken-function.
func narrowTargets(n string, fns map[string]*stackFunc, scan *octogo.CallScan, byCName map[string]string) (targets []string, ok, viaValue bool) {
	f, cn := fns[n], stackCName(n)
	if scan.Funcs[cn] == nil {
		return nil, false, false
	}
	listed := map[string]bool{}
	for _, c := range f.calls {
		listed[stackCName(c)] = true
	}
	var sites []octogo.IfaceSite
	var values []string
	other := 0
	// A function inlined twice brings its calls twice: visited once, a function
	// calling `Eval` in two places, which calls through a table, was read as
	// making one such call where its listing makes two, and was every taken
	// function's -- an expression evaluator's recursion through `evAdd` went
	// unseen. A function met again on the way to itself is no inlined copy.
	on, visits := map[string]bool{}, 0
	var visit func(string)
	visit = func(name string) {
		if on[name] || visits > maxInlineVisits {
			visits = maxInlineVisits + 1
			return
		}
		on[name] = true
		visits++
		fc := scan.Funcs[name]
		sites, values, other = append(sites, fc.Iface...), append(values, fc.FV...), other+fc.Other
		for _, d := range fc.Direct {
			if !listed[d] && scan.Funcs[d] != nil {
				visit(d)
			}
		}
		delete(on, name)
	}
	visit(cn)
	if visits > maxInlineVisits || other != 0 || len(sites)+len(values) != f.indirect {
		return nil, false, false
	}
	have := map[string]bool{}
	add := func(fs []string) bool {
		for _, fn := range fs {
			ln, ok := byCName[fn]
			if !ok {
				return false
			}
			if !have[ln] {
				have[ln] = true
				targets = append(targets, ln)
			}
		}
		return true
	}
	for _, site := range sites {
		fs, ok := scan.Targets(site)
		if !ok || !add(fs) {
			return nil, false, false
		}
	}
	for _, ct := range values {
		fs, ok := scan.FuncValueTargets(ct)
		if !ok || !add(fs) {
			return nil, false, false
		}
	}
	sort.Strings(targets)
	return targets, true, len(values) != 0
}

// goroutineName is the function a trampoline starts, the one of its direct calls
// whose stack is deepest, with the listing's underscore and number taken off; ""
// where it calls the function through a register, a function value started.
func goroutineName(calls []string, need func(string) int) string {
	name, deep := "", -1
	for _, c := range calls {
		if strings.HasPrefix(c, "_ogo_cog_done") {
			continue
		}
		if v := need(c); v > deep {
			name, deep = c, v
		}
	}
	return stackStatic.ReplaceAllString(strings.TrimPrefix(name, "_"), "")
}

// recursingName names where a goroutine whose need is unbounded recurses: the
// first recursive function on a way from its trampoline, tramp, to one.
func recursingName(tramp string, succ map[string][]stackEdge, recursive map[string]bool, need func(string) int) string {
	seen := map[string]bool{}
	var walk func(string) string
	walk = func(n string) string {
		if recursive[n] {
			return stackStatic.ReplaceAllString(strings.TrimPrefix(n, "_"), "")
		}
		seen[n] = true
		for _, e := range succ[n] {
			if !seen[e.to] && need(e.to) < 0 {
				if r := walk(e.to); r != "" {
					return r
				}
			}
		}
		return ""
	}
	return walk(tramp)
}

// stackComponents numbers the strongly connected components of the call graph,
// over the edges it is asked to follow (Tarjan's algorithm).
func stackComponents(names []string, succ map[string][]stackEdge, follow func(stackEdge) bool) map[string]int {
	index, low := map[string]int{}, map[string]int{}
	on := map[string]bool{}
	var stack []string
	comp := map[string]int{}
	next, ncomp := 0, 0
	var visit func(string)
	visit = func(v string) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		on[v] = true
		for _, e := range succ[v] {
			if !follow(e) {
				continue
			}
			if _, seen := index[e.to]; !seen {
				visit(e.to)
				low[v] = min(low[v], low[e.to])
			} else if on[e.to] {
				low[v] = min(low[v], index[e.to])
			}
		}
		if low[v] == index[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				comp[w] = ncomp
				if w == v {
					break
				}
			}
			ncomp++
		}
	}
	for _, n := range names {
		if _, seen := index[n]; !seen {
			visit(n)
		}
	}
	return comp
}
