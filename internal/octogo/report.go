// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Report is what a program's C says of its resources in the program's own terms,
// for `ogo build --report`, which reads the rest off the backend's listing and its
// binary: the Go name of each function the C defines, every object it declares at
// file scope and what it is to the program, the go statements, and the hardware
// locks the program takes. WithReport has EmitC fill one.
type Report struct {
	// Funcs is the Go name of each function of the program by its C name.
	Funcs map[string]string
	// Storage is every object the C declares at file scope, once each, in the
	// order it declares them.
	Storage []Storage
	// Goroutines is each go statement by its trampoline's C name: where it
	// stands and what it starts.
	Goroutines map[string]string
	// ChanSites is how many make sites of a channel the program has, and
	// ChanLocks the hardware locks they take between them.
	ChanSites, ChanLocks int
	// NewLocks is how many calls of p2.NewLock the program writes.
	NewLocks int
}

// Storage is an object the C declares at file scope.
type Storage struct {
	C string // its C name
	// Var is the package variable it is or whose value it holds, as the program
	// names it; "" for none.
	Var string
	// Type is the variable's type as the program writes it, for a variable.
	Type string
}

// WithReport has EmitC fill r from the program it writes.
func WithReport(r *Report) EmitOption { return func(e *emitter) { e.report = r } }

var (
	// cStatic is the head of a declaration at file scope the emitter writes, one
	// object to a line: qualifiers, a type, stars, the name, its dimensions and
	// what follows them.
	cStatic  = regexp.MustCompile(`(?m)^static ((?:(?:const|volatile) )*)([A-Za-z_][A-Za-z0-9_]*)((?: ?\*)*) ?(?:const )?([A-Za-z_][A-Za-z0-9_]*)((?:\[[^\]\n]+\])*)( = [^\n]*|;)`)
	cIdentRE = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
	// cMadeObject is what the emitter makes a package variable's value of.
	cMadeObject = regexp.MustCompile(`^ogo_(?:backing|plit|lit|chan_cell|chan_buf|chan_lock)_?[0-9]+$`)
	// cInitStore is a store of package initialization, its target's root and the
	// value; cChanInit a channel cell made, the cell and the rest.
	cInitStore     = regexp.MustCompile(`(?m)^\t+([A-Za-z_][A-Za-z0-9_]*)(?:\.[A-Za-z_][A-Za-z0-9_]*)* = ([^\n]*);$`)
	cChanInit      = regexp.MustCompile(`ogo_chan_init[A-Za-z0-9_]*\(&(ogo_chan_cell_[0-9]+), ([^\n]*)\);`)
	cSpin2Instance = regexp.MustCompile(`(?m)^struct __using\("([^"\n]+)"\) (ogo_spin2_[0-9]+);`)
	cMainHead      = regexp.MustCompile(`(?m)^int main\(void\) \{$`)
	// cRuntimeLock is a hardware lock the runtime takes: the goroutine pool's, a
	// channel make site's.
	cRuntimeLock = regexp.MustCompile(`\b(?:ogo_cog_lock|ogo_chan_lock[0-9]+) = _locknew\(\)`)
)

// noteReportFunc records the Go name of a function the C defines as cname: name in
// the package being emitted, a method of the receiver's C type recv where that is
// not "".
func (e *emitter) noteReportFunc(cname, name, recv string) {
	if e.report == nil {
		return
	}
	if e.reportFuncs == nil {
		e.reportFuncs = map[string]string{}
	}
	pkg := e.pkgNames[e.curPkgPrefix]
	switch {
	case recv != "":
		t := e.goTypeName(recv)
		star := strings.HasPrefix(t, "*")
		t = strings.TrimPrefix(t, "*")
		if e.curPkgPrefix != "" && !strings.Contains(t, ".") {
			t = pkg + "." + t
		}
		if star {
			t = "*" + t
		}
		e.reportFuncs[cname] = "(" + t + ")." + name
	case e.curPkgPrefix == "":
		e.reportFuncs[cname] = name
	default:
		e.reportFuncs[cname] = pkg + "." + name
	}
}

// noteReportLit records the function literal lit, lifted to the C function cname.
func (e *emitter) noteReportLit(cname string, lit Node) {
	if e.report == nil {
		return
	}
	if e.reportFuncs == nil {
		e.reportFuncs = map[string]string{}
	}
	pos := e.f.tok(lit.Pos()).Position()
	e.reportFuncs[cname] = fmt.Sprintf("the function literal at %s:%d", filepath.Base(pos.Filename), pos.Line)
}

// noteReportGo records the go statement whose keyword is at and whose head and
// suffix are nodes, launched through the trampoline of site.
func (e *emitter) noteReportGo(site int, nodes []Node) {
	if e.report == nil || len(nodes) < 2 {
		return
	}
	if e.reportGos == nil {
		e.reportGos = map[string]string{}
	}
	what := "a function literal"
	if nodes[1].sym != FuncLiteral {
		end := nodes[len(nodes)-1].Pos() // the call's "("
		var b strings.Builder
		for t := nodes[1].Pos(); t >= 0 && t < end; t++ {
			b.WriteString(e.f.tok(t).Src())
		}
		what = b.String()
	}
	pos := e.f.tok(nodes[0].tok).Position()
	e.reportGos[goTrampolineCName(site)] = fmt.Sprintf("%s:%d %s", filepath.Base(pos.Filename), pos.Line, what)
}

// fillReport fills e.report from c, the C this pass writes of the program pkgs
// make.
func (e *emitter) fillReport(c []byte, pkgs []*Package) {
	r := e.report
	// p2.NewLock is _locknew wherever the program calls it; the runtime's own are
	// the goroutine pool's and the make sites'.
	newLocks := bytes.Count(c, []byte("_locknew()")) - len(cRuntimeLock.FindAll(c, -1))
	*r = Report{Funcs: e.reportFuncs, Goroutines: e.reportGos, ChanSites: e.chanLockN, ChanLocks: min(e.chanLockN, chanSiteLocks), NewLocks: newLocks}
	if r.Funcs == nil {
		r.Funcs = map[string]string{}
	}
	if r.Goroutines == nil {
		r.Goroutines = map[string]string{}
	}
	vars := map[string]reportVar{} // the package variables by their C names
	for _, p := range pkgs {
		if p.Scope == nil {
			continue
		}
		prefix := pkgPrefix(p.ImportPath)
		for name, d := range p.Scope.Declarations {
			if _, ok := d.(*VarDeclaration); !ok {
				continue
			}
			cname := e.mangle(prefix, name)
			typ := ""
			if a, ok := e.globalArrays[cname]; ok {
				typ = e.goArrayTypeName(a)
			} else if ct, ok := e.globals[cname]; ok {
				typ = e.goTypeName(ct)
			} else {
				continue
			}
			goName := name
			if prefix != "" {
				goName = e.pkgNames[prefix] + "." + name
			}
			vars[cname] = reportVar{goName, typ}
		}
	}
	// What a variable's value is made of: a backing its initializer names, a
	// value package initialization makes and points it at (`&T{}`, errors.New, a
	// channel's cell, its buffer and its lock), and what those name in turn.
	edges := map[string][]string{}
	link := func(from, text string) {
		for _, id := range cIdentRE.FindAllString(text, -1) {
			if cMadeObject.MatchString(id) && id != from {
				edges[from] = append(edges[from], id)
			}
		}
	}
	seen := map[string]bool{}
	var objs []Storage
	for _, m := range cStatic.FindAllSubmatch(c, -1) {
		name := string(m[4])
		link(name, string(m[6]))
		if seen[name] {
			continue // a tentative definition again
		}
		seen[name] = true
		s := Storage{C: name}
		if v, ok := vars[name]; ok {
			s.Var, s.Type = v.name, v.typ
		}
		objs = append(objs, s)
	}
	for _, m := range cInitStore.FindAllSubmatch(c, -1) {
		link(string(m[1]), string(m[2]))
	}
	for _, m := range cChanInit.FindAllSubmatch(c, -1) {
		link(string(m[1]), string(m[2]))
	}
	// A Spin2 object's instance, `struct __using("<path>") ogo_spin2_<n>;`.
	for _, m := range cSpin2Instance.FindAllSubmatch(c, -1) {
		objs = append(objs, Storage{C: string(m[2])})
	}
	owner := map[string]string{}
	for _, s := range objs {
		if s.Var == "" {
			continue
		}
		todo := []string{s.C}
		for len(todo) != 0 {
			n := todo[len(todo)-1]
			todo = todo[:len(todo)-1]
			for _, to := range edges[n] {
				if _, owned := owner[to]; !owned {
					owner[to] = s.Var
					todo = append(todo, to)
				}
			}
		}
	}
	for i, s := range objs {
		if v, ok := owner[s.C]; ok && s.Var == "" {
			objs[i].Var = v
		}
	}
	r.Storage = objs
}

// reportVar is a package variable by its C name, for the report.
type reportVar struct {
	name, typ string
}

// sizeProbeMark leads the table SizeProbe writes: "rogo" "size" in the binary.
var sizeProbeMark = [2]uint32{0x6f676f72, 0x657a6973}

// SizeProbe answers c, C that EmitC wrote, with a table of the size of each of the
// objects names behind sizeProbeMark and their count, kept by main: built, the
// binary holds what the target's compiler made of each, which ProbedSizes reads.
// It is nil where c has no main to keep it.
func SizeProbe(c []byte, names []string) []byte {
	loc := cMainHead.FindIndex(c)
	if loc == nil {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "static const unsigned ogo_report_sizes[] = {%#xu, %#xu, %d", sizeProbeMark[0], sizeProbeMark[1], len(names))
	for _, n := range names {
		fmt.Fprintf(&b, ", sizeof(%s)", n)
	}
	b.WriteString("};\nstatic volatile unsigned ogo_report_keep;\n")
	return slices.Concat(c[:loc[0]], []byte(b.String()), c[loc[0]:loc[1]], []byte("\n\togo_report_keep = ogo_report_sizes[2];"), c[loc[1]:])
}

// ProbedSizes reads the sizes SizeProbe's table holds out of binary, a program
// built from its C; nil where the table is not there.
func ProbedSizes(binary []byte) []int {
	var mark [8]byte
	binary32 := func(b []byte, v uint32) { b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24) }
	binary32(mark[:4], sizeProbeMark[0])
	binary32(mark[4:], sizeProbeMark[1])
	i := bytes.Index(binary, mark[:])
	if i < 0 || i+12 > len(binary) {
		return nil
	}
	le := func(at int) int {
		return int(binary[at]) | int(binary[at+1])<<8 | int(binary[at+2])<<16 | int(binary[at+3])<<24
	}
	n := le(i + 8)
	if n < 0 || i+12+4*n > len(binary) {
		return nil
	}
	r := make([]int, n)
	for k := range r {
		r[k] = le(i + 12 + 4*k)
	}
	return r
}
