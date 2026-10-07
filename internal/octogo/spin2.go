// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// spin2Object is a Spin2 object a package carries: a .spin2 file beside its .ogo
// files, whose PUB methods implement the package's functions declared without a
// body. It is what Go's .s files are to a Go package -- code the compiler does not
// read, called through declarations it does -- and the P2's own form of it: a
// Spin2 object, its PASM started on a cog of its own or run where it is called,
// is how the P2's drivers are written. The C the emitter writes imports it with the
// backend's `struct __using`, and a function bound to one of its methods is a C
// function calling that method (spin2Binding).
type spin2Object struct {
	path    string                  // the file's path in the build's file system, which is the path the C imports it by
	methods map[string]*spin2Method // its PUB methods, by the name lowercased: Spin2 does not tell case apart
	cname   string                  // the C name of the one instance the program has of it, given by the emitter
}

// spin2Method is a PUB method of a spin2Object, as its declaration reads.
type spin2Method struct {
	name    string // as written, which is how the C calls it
	params  int
	results int
	line    int
}

// spin2Binding is a function declared without a body, bound to the Spin2 method
// implementing it.
type spin2Binding struct {
	obj    *spin2Object
	method *spin2Method
}

// spin2Pub matches a PUB method's declaration on a line cleared of comments and
// strings: `PUB name(a, b = 3) : r1, r2 | locals`, the keyword in any case, the
// parameters in parentheses or none at all.
var spin2Pub = regexp.MustCompile(`(?i)^\s*pub\s+([A-Za-z_][A-Za-z_0-9]*)\s*(\(([^)]*)\))?\s*(:\s*([^|]*))?`)

// scanSpin2 reads the PUB methods a Spin2 source declares. It reads declarations
// and nothing else: the backend compiles the object, and says what is wrong with it
// where it is wrong. What is removed first is what could hide or fake a declaration:
// a comment, `'` to the end of the line, `{...}` and `{{...}}`, which nest; and a
// string, in which neither opens a comment.
func scanSpin2(src []byte) map[string]*spin2Method {
	methods := map[string]*spin2Method{}
	var line strings.Builder
	lineNo, depth, inString := 1, 0, false
	flush := func() {
		if m := spin2Pub.FindStringSubmatch(line.String()); m != nil {
			key := strings.ToLower(m[1])
			if _, dup := methods[key]; !dup {
				methods[key] = &spin2Method{name: m[1], params: spin2Count(m[3]), results: spin2Count(m[5]), line: lineNo}
			}
		}
		line.Reset()
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\n':
			flush()
			lineNo++
			inString = false
			continue
		case depth > 0:
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			}
			continue
		case inString:
			if c == '"' {
				inString = false
			}
			line.WriteByte(' ')
			continue
		}
		switch c {
		case '"':
			inString = true
			line.WriteByte(' ')
		case '{':
			depth++
		case '\'':
			for i+1 < len(src) && src[i+1] != '\n' {
				i++
			}
		default:
			line.WriteByte(c)
		}
	}
	flush()
	return methods
}

// spin2Count counts the names of a parameter or result list, `a, b = 3`.
func spin2Count(list string) int {
	if strings.TrimSpace(list) == "" {
		return 0
	}
	return strings.Count(list, ",") + 1
}

// bindSpin2 binds a function declared without a body, n, to the PUB method of the
// package's Spin2 object implementing it (see spin2Object): the method of its name,
// which Spin2 reads whatever the case -- `Start` is `PUB start` -- taking as many
// parameters, and returning a value where the function declares one. A Spin2
// method takes and returns 32-bit longs, so a variadic parameter and a second
// result are refused; which types a long may carry is the emitter's to say, where
// the C of the function's prototype is known. The embedded packages' bodyless
// functions are the emitter's intrinsics, and are left alone.
func (f *File) bindSpin2(n Node, isMethod bool, sig *SignatureNode) {
	p := f.Package
	if p == nil || sig == nil {
		return
	}
	if _, embedded := embeddedPkgs[p.ImportPath]; embedded || intrinsicImports[p.ImportPath] {
		return
	}
	var name Token
	for c := range it(n.ast) {
		if c.sym == 0 {
			if tok := f.tok(c.tok); Symbol(tok.Ch) == IDENT {
				name = tok
				break
			}
		}
	}
	pos, nm := name.Position(), name.Src()
	switch {
	case isMethod:
		f.err(pos, "missing method body: only a function may be implemented by a .spin2 file")
		return
	case len(p.spin2) == 0:
		f.err(pos, "missing function body: %s is implemented by no .spin2 file of its package", nm)
		return
	}
	var found []*spin2Binding
	for _, o := range p.spin2 {
		if m := o.methods[strings.ToLower(nm)]; m != nil {
			found = append(found, &spin2Binding{obj: o, method: m})
		}
	}
	switch len(found) {
	case 0:
		f.err(pos, "missing function body: no .spin2 file of the package has a PUB method %s", nm)
		return
	case 1:
	default:
		f.err(pos, "%s: PUB %s is in both %s and %s", nm, found[0].method.name, path.Base(found[0].obj.path), path.Base(found[1].obj.path))
		return
	}
	b := found[0]
	params, results, variadic := 0, 0, false
	if sig.Params != nil {
		for _, d := range sig.Params.List {
			params += max(len(d.Names), 1)
			variadic = variadic || d.Variadic
		}
	}
	if sig.Results != nil {
		for _, d := range sig.Results.List {
			results += max(len(d.Names), 1)
		}
	}
	m, file := b.method, path.Base(b.obj.path)
	switch {
	case variadic:
		f.err(pos, "%s: a function implemented by PUB %s of %s takes no variadic parameter", nm, m.name, file)
	case params != m.params:
		f.err(pos, "%s takes %d parameters, and PUB %s of %s takes %d", nm, params, m.name, file, m.params)
	case m.results > 1:
		f.err(pos, "%s: PUB %s of %s returns %d results, and a function implemented in Spin2 returns one at most", nm, m.name, file, m.results)
	case results > 1:
		f.err(pos, "%s returns %d results, and a function implemented in Spin2 returns one at most", nm, results)
	case results > m.results:
		f.err(pos, "%s returns a result, and PUB %s of %s returns none", nm, m.name, file)
	default:
		p.spin2Mu.Lock()
		if p.spin2Funcs == nil {
			p.spin2Funcs = map[string]*spin2Binding{}
		}
		p.spin2Funcs[nm] = b
		p.spin2Mu.Unlock()
	}
}

// spin2Crosses reports whether a value of the C type ct may be passed to a Spin2
// method, or returned from one: a method takes and returns 32-bit longs, which
// hold an integer of 32 bits or fewer, a bool or a pointer, and nothing else does.
func (e *emitter) spin2Crosses(ct string) bool {
	ut := strings.TrimSpace(e.underlyingCType(ct))
	if ut == cBool || strings.HasSuffix(ut, "*") {
		return true
	}
	w := cIntWidths[ut]
	return w > 0 && w <= 32
}

// emitSpin2Objects declares the program's one instance of each Spin2 object a
// function is bound to (spin2Binding): `struct __using`, the backend's import of a
// Spin2 object into C, by the object's path in the build's file system, which
// `ogo build` hands the backend as an include directory -- so what the object
// itself names, another object or a file of data, is found beside it. The host's C
// compiler has no such import, and a program carrying one is the target's only.
func (e *emitter) emitSpin2Objects(pkgs []*Package) {
	n := 0
	for _, p := range pkgs {
		used := map[*spin2Object]bool{}
		for _, b := range p.spin2Funcs {
			used[b.obj] = true
		}
		for _, o := range p.spin2 {
			if !used[o] {
				continue
			}
			o.cname = fmt.Sprintf("ogo_spin2_%d", n)
			n++
			e.emit(fmt.Sprintf("#ifdef __FLEXC__\nstruct __using(%s) %s;\n#else\n#error %s\n#endif\n",
				cQuote(o.path), o.cname, cQuote(o.path+": a Spin2 object is compiled by the P2's C compiler only")))
		}
	}
	if n != 0 {
		e.usesSpin2 = true
		e.emit("\n")
	}
}

// emitSpin2Func writes a function declared without a body that a Spin2 object's
// method implements (spin2Binding): a C function of the function's own prototype,
// passing its arguments to the method of the program's instance of the object
// (emitSpin2Objects). It is an ordinary function to everything else -- a value, a
// deferred call, a goroutine -- and one of one statement, which the backend
// inlines. What its prototype holds must cross into a Spin2 method (spin2Crosses).
func (e *emitter) emitSpin2Func(ast []int32, name string, sig []int32, b *spin2Binding) {
	var args []string
	ok := true
	for n := range it(sig) {
		if n.sym != ParameterList {
			continue
		}
		e.forEachParamV(n.ast, func(pname string, ta []int32, _, _ bool) {
			if ct := e.cType(ta); !e.spin2Crosses(ct) && ok {
				e.failAt(ta, "%s: a parameter of type %s does not cross into Spin2, whose methods take 32-bit longs: an integer of 32 bits or fewer, a bool or a pointer does", name, e.goTypeName(ct))
				ok = false
			}
			args = append(args, e.localIdent(pname))
		})
	}
	_, resTypes := e.cSig(sig)
	if len(resTypes) == 1 && !e.spin2Crosses(resTypes[0]) && ok {
		e.failAt(sig, "%s: a result of type %s does not cross from Spin2, whose methods return 32-bit longs: an integer of 32 bits or fewer, a bool or a pointer does", name, e.goTypeName(resTypes[0]))
		ok = false
	}
	if !ok || b.obj.cname == "" {
		return
	}
	if e.wroteDecl {
		e.emit("\n")
	}
	e.wroteDecl = true
	e.emit(e.funcSignatureC(e.funcDefCName(name, ast), sig) + " {\n")
	call := fmt.Sprintf("%s.%s(%s)", b.obj.cname, b.method.name, strings.Join(args, ", "))
	switch {
	case len(resTypes) == 0:
		e.emit("\t" + call + ";\n")
	case e.underlyingCType(resTypes[0]) == cBool:
		e.emit("\treturn " + call + " != 0;\n")
	default:
		e.emit("\treturn (" + resTypes[0] + ")" + call + ";\n")
	}
	e.emit("}\n")
}

// seedSpin2Cross summarises a function a Spin2 object implements (spin2Binding) as
// keeping every parameter, its value and what it reaches, on another cog and where
// it outlives every frame: the method is out of the compiler's sight, and what a
// driver does with a pointer is keep it -- the screen a VGA cog reads, line after
// line, for as long as the program runs. So a reference to a frame handed to one is
// refused, directly or through any function relaying it, as it is when a function
// stores its parameter in a package variable; a package variable's address is not:
// it is a local that dangles. The summary is seeded before the fixed point
// (closeCrossParams) and has no edges of its own, there being no body to read.
func (e *emitter) seedSpin2Cross(d []int32) {
	name, sig, body, recv, ok := e.funcParts(d)
	if !ok || body != nil || recv != nil || e.f.Package.spin2Funcs[name] == nil {
		return
	}
	cname := e.mangle(e.curPkgPrefix, name)
	var kept []leak
	for n := range it(sig) {
		if n.sym != ParameterList {
			continue
		}
		e.forEachParam(n.ast, func(string, []int32, bool) {
			kept = append(kept, leakCog|leakGlobal)
		})
	}
	e.crossParams[cname] = kept
	e.crossContents[cname] = slices.Clone(kept)
	e.crossInto[cname] = make([]uint32, len(kept))
	e.crossNames[cname] = name
}

// spin2Objects reads the Spin2 objects of the package in the directory dir of
// fsys, in the order of their names. An unreadable directory has none: it is the
// .ogo files' directory, whose reading reports itself.
func spin2Objects(fsys fs.FS, dir string) (objs []*spin2Object) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}
	for _, v := range entries {
		if v.IsDir() || path.Ext(v.Name()) != ".spin2" {
			continue
		}
		p := path.Join(dir, v.Name())
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			continue
		}
		objs = append(objs, &spin2Object{path: p, methods: scanSpin2(src)})
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].path < objs[j].path })
	return objs
}
