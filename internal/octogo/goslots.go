// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A goroutine runs on a stack of its own, a slot of the pool the emitted runtime
// keeps in Hub RAM (ogoCogPoolC). The pool had seven slots, one for each cog but
// main's, every one the size the deepest goroutine needs: a program starting two
// goroutines kept seven stacks for the whole run, and a program whose one deep
// goroutine needed 800 longs gave each of the others 800 too. Seven slots of one
// size are what a program needs that may run any goroutine anywhere -- a go in a
// loop starts as many as there are cogs.
//
// Most programs start their goroutines once: main brings the drivers up and they
// run for the rest of the program. A go statement that runs at most once in the
// program's run needs a slot of its own and nobody else's, sized for that
// goroutine alone, and a program whose every go statement runs at most once needs
// a slot for each and no more. Each slot's stack is then what the backend's
// listing says ITS goroutine needs, read by the build (internal/build).
//
// A go statement runs at most once where it stands at most once in a function that
// runs at most once: in no loop and no function literal, and in a function with no
// goto back. A function runs at most once if it is the program's entry, main not
// named by the program, or an init, or if the program names it once, in a call
// that runs at most once. Names are counted by spelling across the whole program
// -- a field, a method or a local of the same name counts too -- which errs toward
// the shared pool, never toward a slot two goroutines want. Were one wanted by a
// second goroutine while the first runs, the second would wait for it as for any
// slot and then panic "out of cogs": nothing would share a stack.

// goSlotKey is a go statement: the file it is written in and its keyword's token.
type goSlotKey struct {
	f   *File
	tok int32
}

// onceFunc is a function declaration, for the question whether it runs at most
// once in the program's run.
type onceFunc struct {
	pkg   *Package
	name  string
	state int8 // 0 not asked yet, 1 being asked, 2 at most once, 3 more
}

// onceRef is where a name is written: in which function, nil at package level, and
// whether it is called where it stands at most once each time that function runs.
type onceRef struct {
	fn   *onceFunc
	once bool
}

// onceName is an identifier the program writes, and where.
type onceName struct {
	f   *File
	tok int32
	at  onceRef
}

// onceWalk is where a walk of a function's body is: in which function, under how
// many loops, inside a function literal, in a function with a goto back.
type onceWalk struct {
	fn    *onceFunc
	loops int
	lit   bool
	back  bool
}

// once reports that what stands here runs at most once each time the function does.
func (w onceWalk) once() bool { return w.loops == 0 && !w.lit && !w.back }

// onceGoStatements answers the go statements of the program pkgs make, root its
// main package, entered through entry, where every one of them runs at most once
// in the program's run and there are no more than slots; ok is false for any
// other program, and for one with no go statement.
func onceGoStatements(pkgs []*Package, root *Package, entry string, slots int) (gos []goSlotKey, ok bool) {
	names := map[string][]onceName{} // every identifier the program writes, by spelling
	var sites []onceRef
	var funcs []*onceFunc
	var walk func(f *File, ast []int32, w onceWalk)
	walk = func(f *File, ast []int32, w onceWalk) {
		for n := range it(ast) {
			switch n.sym {
			case 0:
				if f.ch(n.tok) == IDENT {
					name := f.tok(n.tok).Src()
					names[name] = append(names[name], onceName{f, n.tok, onceRef{w.fn, w.once() && f.ch(n.tok+1) == LPAREN}})
				}
			case FuncDecl:
				d := &onceFunc{pkg: f.Package}
				method := false
				var body []int32
				for c := range it(n.ast) {
					switch {
					case c.sym == Receiver:
						method = true
					case c.sym == Block:
						body = c.ast
					case c.sym == 0 && f.ch(c.tok) == IDENT && d.name == "":
						d.name = f.tok(c.tok).Src()
					}
				}
				switch {
				case method:
					d.state = 3 // called through a value, a table or a promotion as often as by name
				case d.name == "init":
					d.state = 2 // Go refuses naming an init
				}
				// The declaration's own tokens, its name among them, name nothing.
				inner := onceWalk{fn: d, back: body != nil && f.backwardGoto(body)}
				for c := range it(n.ast) {
					if c.sym != 0 {
						walk(f, c.ast, inner)
					}
				}
				funcs = append(funcs, d)
			case FuncLiteral:
				lit := w
				lit.lit = true
				walk(f, n.ast, lit)
			case Statement:
				inner := w
				for c := range it(n.ast) {
					if c.sym == 0 {
						switch f.ch(c.tok) {
						case FOR:
							inner.loops++
						case GO:
							gos = append(gos, goSlotKey{f, c.tok})
							sites = append(sites, onceRef{w.fn, w.once()})
						}
					}
					break
				}
				walk(f, n.ast, inner)
			default:
				walk(f, n.ast, w)
			}
		}
	}
	for _, p := range pkgs {
		for _, f := range p.Files {
			walk(f, f.AST, onceWalk{})
		}
	}
	if len(gos) == 0 || len(gos) > slots {
		return nil, false
	}
	// refs answers where the program names d's function: by its bare name in its
	// own package and in a file importing it with a dot, and as q.name in a file
	// importing it as q. A name after any other selector is a field or a method.
	refs := func(d *onceFunc) (r []onceRef) {
		for _, n := range names[d.name] {
			f := n.f
			if n.tok > 0 && f.ch(n.tok-1) == PERIOD {
				if n.tok < 2 || f.ch(n.tok-2) != IDENT {
					continue
				}
				q := f.tok(n.tok - 2).Src()
				for _, spec := range f.ImportSpecs {
					if spec.Pkg == d.pkg && !spec.IsDotImport && spec.ImportQualifier == q {
						r = append(r, n.at)
						break
					}
				}
				continue
			}
			if f.Package == d.pkg {
				r = append(r, n.at)
				continue
			}
			for _, spec := range f.ImportSpecs {
				if spec.Pkg == d.pkg && spec.IsDotImport {
					r = append(r, n.at)
					break
				}
			}
		}
		return r
	}
	for _, d := range funcs {
		if d.state == 0 && d.name == entry && d.pkg == root {
			d.state = 3 // `main()` runs main again
			if len(refs(d)) == 0 {
				d.state = 2
			}
		}
	}
	var onceFn func(d *onceFunc) bool
	onceFn = func(d *onceFunc) bool {
		if d == nil {
			return true // a package variable's initializer runs once
		}
		switch d.state {
		case 1:
			return false // a call of its own: no caller from outside
		case 2:
			return true
		case 3:
			return false
		}
		d.state = 1
		r := refs(d)
		ok := len(r) == 1 && r[0].once && onceFn(r[0].fn)
		d.state = 3
		if ok {
			d.state = 2
		}
		return ok
	}
	for _, s := range sites {
		if !s.once || !onceFn(s.fn) {
			return nil, false
		}
	}
	return gos, true
}

// GoSlot is a slot of the goroutine pool of C that EmitC wrote: the trampolines
// that may run on it, each a go statement's, nil for every one, and the longs of
// stack it holds.
type GoSlot struct {
	Tramps []string
	Longs  int
}

var (
	goSlotsShared = regexp.MustCompile(`(?m)^#define OGO_STACK_LONGS (\d+)$`)
	goSlotsOwn    = regexp.MustCompile(`(?m)^#define OGO_STACK_LONGS_(\d+) (\d+) //((?: \w+)*)$`)
)

// GoSlots answers the slots of the goroutine pool c declares, C that EmitC wrote,
// by their sizes: one GoSlot for the seven of the shared pool, which are of one
// size and any goroutine's, or each slot a go statement has to itself; nil for a
// program starting no goroutine. The build sizes them from the listing of a first
// compile (internal/build), and the sizes change nothing else the backend makes of
// the program.
func GoSlots(c []byte) (r []GoSlot) {
	if m := goSlotsShared.FindSubmatch(c); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		return []GoSlot{{Longs: n}}
	}
	for _, m := range goSlotsOwn.FindAllSubmatch(c, -1) {
		n, _ := strconv.Atoi(string(m[2]))
		r = append(r, GoSlot{Tramps: strings.Fields(string(m[3])), Longs: n})
	}
	return r
}

// WithGoSlots answers c, C that EmitC wrote, with the slots GoSlots reads off it
// holding longs[i] longs of stack each, or every slot longs[0] where only one is
// given: what GoStack would have asked for, applied to C already written.
func WithGoSlots(c []byte, longs []int) []byte {
	if len(longs) == 0 {
		return c
	}
	if goSlotsShared.Match(c) {
		return goSlotsShared.ReplaceAll(c, []byte("#define OGO_STACK_LONGS "+strconv.Itoa(longs[0])))
	}
	i := 0
	return goSlotsOwn.ReplaceAllFunc(c, func(line []byte) []byte {
		m := goSlotsOwn.FindSubmatch(line)
		n := longs[min(i, len(longs)-1)]
		i++
		return []byte(fmt.Sprintf("#define OGO_STACK_LONGS_%s %d //%s", m[1], n, m[3]))
	})
}
