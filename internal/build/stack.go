// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A goroutine runs on a stack of its own, a slot of the pool the emitted runtime
// keeps in hub RAM, and a slot has the size the C was emitted with: 256 longs unless
// --gostack asked for another. A goroutine holding a table of 512 bytes in a local
// ran past it, and the fence the runtime keeps at the slot's end said so only when
// the goroutine ENDED -- having written over whatever lay beyond, a running
// goroutine's slot among them. The backend's listing has every function's frame and
// every call, so the build reads the stack each goroutine needs off it and, where
// that is more than its slots hold, compiles the same C again with slots that fit
// (compileSized). Nothing is asked of the program.
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
// Recursion through direct calls has no depth a listing can bound, and such a
// goroutine is left to the slot it gets and the fence. A cycle through an indirect
// call is the library's -- a flush calling a FILE's function that can reach flush
// again -- and is taken as the deepest simple path through it.

// hubRAMBytes is the P2's Hub RAM, which a program's code, data and main stack
// share.
const hubRAMBytes = 512 << 10

// stackScratch is what a function formatting into the memory past ptra may use.
const stackScratch = 256

// cogStartLongs is what the cog's entry takes off the base of the slot before it
// calls the trampoline: _cogstart_C leaves the function and its argument there, and
// the entry reads them and starts the call one long in.
const cogStartLongs = 1

// maxWalkComponent is the largest component of functions joined by indirect calls
// that is searched path by path, its visited set a bit mask; a larger one is taken
// to need every frame it holds, which no path through it exceeds.
const maxWalkComponent = 20

var (
	stackLabel  = regexp.MustCompile(`^(_\w+)$`)
	stackCount  = regexp.MustCompile(`^\s*mov\s+COUNT_, ##?(\d+)`)
	stackAdd    = regexp.MustCompile(`^\s*add\s+ptra, ##?(\d+)`)
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
	indirect bool
}

type stackEdge struct {
	to       string
	indirect bool
}

// stackNeeds is what a program's stacks need, read from the backend's listing of
// it: the deepest goroutine's in longs, 0 for a program that starts none, and the
// main cog's in bytes, -1 where recursion through direct calls leaves it unbounded.
type stackNeeds struct {
	goLongs  int
	goKnown  bool   // every goroutine's need is bounded
	goName   string // the deepest goroutine's function, as the C names it
	mainNeed int
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
				cur.indirect = true
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
			succ[n] = append(succ[n], stackEdge{c, false})
		}
		if f.indirect {
			targets := libTaken
			if isOwn(n) {
				targets = ownTaken
			}
			for _, c := range targets {
				succ[n] = append(succ[n], stackEdge{c, true})
			}
		}
	}

	all, direct := stackComponents(names, succ, false), stackComponents(names, succ, true)
	members := map[int][]string{}
	directSize := map[int]int{}
	for _, n := range names {
		members[all[n]] = append(members[all[n]], n)
		directSize[direct[n]]++
	}
	recursive := map[string]bool{}
	for _, n := range names {
		if directSize[direct[n]] > 1 {
			recursive[n] = true
		}
		for _, e := range succ[n] {
			if !e.indirect && e.to == n {
				recursive[n] = true
			}
		}
	}

	memo := map[string]int{}
	var need func(string) int
	// external is the deepest stack below u through a call leaving u's component.
	external := func(u string) int {
		deep := 0
		for _, e := range succ[u] {
			if all[e.to] == all[u] {
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
				if j, in := idx[e.to]; in && vis&(1<<j) == 0 {
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

	r.goKnown = true
	sort.Strings(tramps)
	for _, t := range tramps {
		v := need(t)
		if v < 0 {
			r.goKnown = false
			continue
		}
		if longs := cogStartLongs + (v+3)/4; longs > r.goLongs {
			r.goLongs, r.goName = longs, goroutineName(fns[t].calls, need)
		}
	}
	r.mainNeed = need("_main")
	return r
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

// stackComponents numbers the strongly connected components of the call graph,
// over every edge or over the direct calls alone (Tarjan's algorithm).
func stackComponents(names []string, succ map[string][]stackEdge, directOnly bool) map[string]int {
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
			if directOnly && e.indirect {
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
