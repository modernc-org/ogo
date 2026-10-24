// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"regexp"
)

// The data a program carries is what the code it runs names. The backend drops a
// function nothing calls, but not a table only such a function read: a program
// calling math.Sqrt carried math's tables for Sin, Tan and the reduction of a huge
// argument, 320 bytes, and a float64 constant only a function never run named
// stayed too -- 80 bytes in every test program, of the testing package's benchmark
// arithmetic. So the emitted C is read as the backend will link it: the functions
// main reaches, through the names their bodies and everything outside a body
// spell. A function of an embedded package that main does not reach is not
// written, nor a static datum only such functions named; the backend compiles
// every function it is given before it drops the ones nothing calls, so a datum
// goes only with every function naming it.
//
// It is text the emitter wrote, in one shape: a function's definition begins at
// the start of a line with a header holding no `=` and ends at a line that is `}`;
// a prototype, a header ending in `);`, names nothing; every other line outside a
// body is a root, a vtable's initializer and a function value's among them. A
// datum dropped is a definition of one line, `static T name[...] = {...};` or
// `static T name;`. What this cannot read it keeps.

var (
	cIdentTokRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	cFuncNameRE = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*\($`)
)

// dataDefName answers the name a line defines as a static datum of one line,
// `static T name[N] = {...};`, `static T name = ...;` or `static T name;`, or "".
func dataDefName(line []byte) string {
	if !bytes.HasPrefix(line, []byte("static ")) || !bytes.HasSuffix(line, []byte(";")) {
		return ""
	}
	head := line[:len(line)-1] // a tentative definition, `static T name[N];`
	if eq := bytes.Index(line, []byte(" = ")); eq >= 0 {
		head = line[:eq]
	}
	if i := bytes.IndexByte(head, '['); i >= 0 {
		head = head[:i]
	}
	if bytes.ContainsAny(head, "(*") {
		return ""
	}
	ids := cIdentTokRE.FindAll(head, -1)
	if len(ids) == 0 {
		return ""
	}
	return string(ids[len(ids)-1])
}

// funcHeaderName answers the name of the function a line at the start of a line
// begins defining -- its header, up to the `{` of its body -- or "".
func funcHeaderName(line []byte) string {
	if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '/' || line[0] == '}' || line[0] == '{' {
		return ""
	}
	open := bytes.IndexByte(line, '{')
	if open < 0 {
		return ""
	}
	head := bytes.TrimSpace(line[:open])
	if bytes.ContainsAny(head, "=") || !bytes.Contains(head, []byte(")")) {
		return ""
	}
	for _, kw := range []string{"typedef ", "struct ", "union ", "enum "} {
		if bytes.HasPrefix(head, []byte(kw)) {
			return ""
		}
	}
	p := bytes.IndexByte(head, '(')
	if p < 0 {
		return ""
	}
	m := cFuncNameRE.FindSubmatch(head[:p+1])
	if m == nil {
		return ""
	}
	return string(m[1])
}

// dropUnreachedData removes from c the functions in funcs that nothing main reaches
// calls, with their prototypes, and the one-line definitions of the static data
// only those functions named. A function and a datum go only when no line kept
// names them -- the backend compiles every function before it drops the ones
// nothing calls, so the C must stay whole -- and what this cannot read it keeps.
func dropUnreachedData(c []byte, funcs map[string]bool) []byte {
	lines := bytes.Split(c, []byte("\n"))
	type span struct{ from, to int } // lines [from, to]
	defs := map[string]span{}        // the functions the C defines
	data := map[string]int{}         // a static datum's definition line
	protos := map[int]string{}       // a prototype's line, by what it declares
	var roots [][]byte
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if name := funcHeaderName(line); name != "" {
			if _, dup := defs[name]; dup {
				return c
			}
			if bytes.HasSuffix(bytes.TrimSpace(line), []byte("}")) {
				defs[name] = span{i, i} // a body of one line
				continue
			}
			j := i + 1
			for j < len(lines) && string(lines[j]) != "}" {
				j++
			}
			if j == len(lines) {
				return c // not the shape this reads: keep everything
			}
			defs[name] = span{i, j}
			i = j
			continue
		}
		if name := dataDefName(line); name != "" {
			if _, dup := data[name]; !dup {
				data[name] = i
				continue
			}
		}
		// A prototype names no use of what it declares.
		if t := bytes.TrimSpace(line); len(line) != 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' && bytes.HasSuffix(t, []byte(");")) && !bytes.Contains(t, []byte("=")) && !bytes.HasPrefix(t, []byte("typedef")) {
			if p := bytes.IndexByte(t, '('); p > 0 {
				if m := cFuncNameRE.FindSubmatch(t[:p+1]); m != nil {
					protos[i] = string(m[1])
				}
			}
			continue
		}
		roots = append(roots, line)
	}
	text := func(name string) []byte {
		f := defs[name]
		return bytes.Join(lines[f.from:f.to+1], []byte("\n"))
	}
	// What main reaches: the closure over the names the roots and the reached
	// bodies spell.
	reached := map[string]bool{}
	var work [][]byte
	mark := func(t []byte) {
		for _, id := range cIdentTokRE.FindAll(t, -1) {
			if s := string(id); !reached[s] {
				if _, ok := defs[s]; ok {
					reached[s] = true
					work = append(work, text(s))
				}
			}
		}
	}
	for _, r := range roots {
		mark(r)
	}
	mark([]byte("main"))
	for len(work) != 0 {
		t := work[len(work)-1]
		work = work[:len(work)-1]
		mark(t)
	}
	// The functions to drop: those of funcs main does not reach, less any a kept
	// function or a root names, until none does.
	drop := map[string]bool{}
	for name := range defs {
		if funcs[name] && !reached[name] {
			drop[name] = true
		}
	}
	if len(drop) == 0 {
		return c
	}
	names := func(t []byte, into map[string]bool) {
		for _, id := range cIdentTokRE.FindAll(t, -1) {
			into[string(id)] = true
		}
	}
	var named map[string]bool
	for {
		named = map[string]bool{}
		for _, r := range roots {
			names(r, named)
		}
		for name := range defs {
			if !drop[name] {
				names(text(name), named)
			}
		}
		changed := false
		for name := range drop {
			if named[name] {
				delete(drop, name)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	// A datum goes with the functions naming it: one a dropped function names and
	// nothing kept does, a kept datum's initializer among what is kept. A datum
	// nothing named stays as it was.
	dropNamed := map[string]bool{}
	for name := range drop {
		names(text(name), dropNamed)
	}
	used := map[string]bool{}
	for name := range data {
		if named[name] || !dropNamed[name] {
			used[name] = true
		}
	}
	for grew := true; grew; {
		grew = false
		for name, i := range data {
			if !used[name] {
				continue
			}
			for _, id := range cIdentTokRE.FindAll(lines[i], -1) {
				if _, ok := data[string(id)]; ok && !used[string(id)] {
					used[string(id)] = true
					grew = true
				}
			}
		}
	}
	gone := map[int]bool{}
	for name, i := range data {
		if !used[name] {
			gone[i] = true
		}
	}
	for name := range drop {
		for i := defs[name].from; i <= defs[name].to; i++ {
			gone[i] = true
		}
	}
	for i, name := range protos {
		if drop[name] {
			gone[i] = true
		}
	}
	var b bytes.Buffer
	for i, line := range lines {
		if gone[i] {
			continue
		}
		b.Write(line)
		if i != len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// noteEmbeddedFunc records cname, a function the C defines, where the package being
// emitted is one the compiler carries as source.
func (e *emitter) noteEmbeddedFunc(cname string) {
	for path := range embeddedPkgs {
		if pkgPrefix(path) == e.curPkgPrefix && e.curPkgPrefix != "" {
			if e.embeddedFuncs == nil {
				e.embeddedFuncs = map[string]bool{}
			}
			e.embeddedFuncs[cname] = true
			return
		}
	}
}
