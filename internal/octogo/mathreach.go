// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

// The math package is Go's whole, some 130 functions of float64 arithmetic in
// software, and a program calls a few. Emitted whole and left out afterwards
// (deaddata.go), it took a program calling math.Sqrt 0.83 seconds in the front end;
// 0.19 now, most of that the check of the package's bodies. So the functions a program reaches
// are found first, by name: math declares no type and no method, so a function is
// reached when another package names it, `math.Sqrt`, as a call or as a value, or
// when the body of one reached names it; the rest are not emitted, nor prototyped,
// nor summarized, and nor is a table no reached function names. A dot import of
// math names no qualifier, and emits all of it.

// noteMathReach records in mathReach the functions of math the program pkgs
// reaches, or leaves it nil, emitting all, where it cannot tell.
func (e *emitter) noteMathReach(pkgs []*Package) {
	e.mathReach = nil
	var mp *Package
	for _, p := range pkgs {
		if p.ImportPath == "math" {
			mp = p
		}
	}
	if mp == nil {
		return
	}
	type decl struct {
		f        *File
		from, to int32 // its tokens
	}
	decls := map[string]decl{}
	for _, f := range mp.Files {
		for n := range it(f.AST) {
			if n.sym != SourceFile {
				continue
			}
			for c := range it(n.ast) {
				if c.sym != TopLevelDecl {
					continue
				}
				for d := range it(c.ast) {
					if d.sym != FuncDecl {
						continue
					}
					if name := funcDeclName(f, d); name != "" {
						decls[name] = decl{f, d.Pos(), d.End()}
					}
				}
			}
		}
	}
	vars := map[string]bool{}
	if mp.Scope != nil {
		for name, d := range mp.Scope.Declarations {
			if _, ok := d.(*VarDeclaration); ok {
				vars[name] = true
			}
		}
	}
	reach := map[string]bool{} // functions and variables
	var work []string
	add := func(name string) {
		if vars[name] {
			reach[name] = true
			return
		}
		if _, ok := decls[name]; ok && !reach[name] {
			reach[name] = true
			work = append(work, name)
		}
	}
	for _, p := range pkgs {
		if p == mp {
			continue
		}
		for _, f := range p.Files {
			q := ""
			for _, is := range f.ImportSpecs {
				if is.ImportPath != "math" {
					continue
				}
				if is.IsDotImport {
					return
				}
				q = is.ImportQualifier
			}
			if q == "" {
				continue
			}
			n := int32(f.parser.sc.Len())
			for t := int32(0); t+2 < n; t++ {
				if f.ch(t) == IDENT && f.tok(t).Src() == q && f.ch(t+1) == PERIOD && f.ch(t+2) == IDENT {
					add(f.tok(t + 2).Src())
				}
			}
		}
	}
	for len(work) != 0 {
		d := decls[work[len(work)-1]]
		work = work[:len(work)-1]
		for t := d.from; t <= d.to; t++ {
			if d.f.ch(t) == IDENT {
				add(d.f.tok(t).Src())
			}
		}
	}
	e.mathReach, e.mathVars = reach, vars
}

// funcDeclName is the name a function declaration of file f declares, or "" for a
// method.
func funcDeclName(f *File, d Node) string {
	for c := range it(d.ast) {
		switch {
		case c.sym == Receiver:
			return ""
		case c.sym == 0 && f.ch(c.tok) == IDENT:
			return f.tok(c.tok).Src()
		}
	}
	return ""
}

// unreachedMath reports whether the function declaration d of the package being
// emitted is one of math's the program does not reach.
func (e *emitter) unreachedMath(d []int32) bool {
	if e.mathReach == nil || e.curPkgPrefix != pkgPrefix("math") {
		return false
	}
	name := funcDeclName(e.f, Node{sym: FuncDecl, ast: d})
	return name != "" && !e.mathReach[name]
}

// unreachedMathVar reports whether the variable declaration d of the package being
// emitted declares none of math's the program reaches: of the variables its tokens
// name, none is reached.
func (e *emitter) unreachedMathVar(d []int32) bool {
	if e.mathReach == nil || e.curPkgPrefix != pkgPrefix("math") {
		return false
	}
	n := Node{sym: VarDecl, ast: d}
	named := false
	for t := n.Pos(); t <= n.End(); t++ {
		if e.f.ch(t) != IDENT {
			continue
		}
		if name := e.f.tok(t).Src(); e.mathVars[name] {
			if e.mathReach[name] {
				return false
			}
			named = true
		}
	}
	return named
}
