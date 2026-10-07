// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"strings"
)

// A goroutine's stack is read off the backend's listing (internal/build), and a
// call through a register there is a call of every function whose address the
// program takes -- sound, and as wide as the program: p2-11's disk cog, whose FAT
// code calls the card through an interface, was charged the deepest path through
// every PDP-11 device method, 808 longs where 432 are reachable. The listing has no
// types and no C lines, so what an indirect call may reach is read off the C the
// emitter wrote, which says it exactly for an interface: every call through a table
// is spelled `((const X_vt*)...)->ogo_m_M(` or `x.vt->ogo_m_M(`, and the tables are
// static, `static const X_vt V = { "*T", thunk, ... };`, their slots in the order
// the table's struct declares them.
//
// ScanCalls reads, for each function the C defines, the functions it calls by name,
// its interface calls, and the number of its other calls that are not proved to be
// by name -- a call through a variable, a parameter, an element, a member, a
// parenthesised expression. A call is by name where the name is a function or a
// macro the C or its library declares; anything else counts as Other, and a
// function with an Other is given every taken function, as before. So a call the
// scanner misreads costs precision and nothing else -- except one it takes for a
// call by NAME, which is what TestScanCallsAgainstGCC holds against gcc's own count
// of indirect calls, function by function, over the run corpus. The listing is the
// second net (internal/build): a function whose calls through a register do not
// number exactly its interface calls is given every taken function too.

// CallScan is what ScanCalls read.
type CallScan struct {
	// Funcs is each function the C defines, by its C name.
	Funcs map[string]*FuncCalls
	// slots holds, by table type and member, the functions the tables put there;
	// members, by member alone, the union over every table type.
	slots   map[string]map[string][]string
	members map[string][]string
}

// FuncCalls is one function's calls.
type FuncCalls struct {
	Direct []string    // the functions the C defines that it calls by name
	Iface  []IfaceSite // its calls through an interface's table
	Other  int         // its other calls not proved to be by name
}

// IfaceSite is one call through a table: the table's type where the call names it,
// "" where it does not, and the member called.
type IfaceSite struct{ VT, Member string }

// Targets answers the functions an interface call may reach: those the tables of
// its type hold in its member, or, where the call names no type, those any table
// holds there. ok is false where no table holds anything there, which a caller
// takes as "unknown" and not as "nothing".
func (s *CallScan) Targets(site IfaceSite) (fns []string, ok bool) {
	if site.VT != "" {
		fns = s.slots[site.VT][site.Member]
	} else {
		fns = s.members[site.Member]
	}
	return fns, len(fns) != 0
}

// cKeywordCall is what may stand before "(" without being a call.
var cKeywordCall = map[string]bool{
	"if": true, "while": true, "for": true, "switch": true, "return": true, "sizeof": true,
	"_Alignof": true, "__alignof__": true, "alignof": true, "__attribute__": true, "defined": true,
	"__asm": true, "asm": true, "__asm__": true, "typeof": true, "__typeof__": true, "case": true,
	"do": true, "else": true, "_Static_assert": true, "_Generic": true, "__extension__": true,
	"volatile": true, "__volatile__": true, "goto": true,
}

// cTypeWord is what a cast's parentheses may hold besides typedef names.
var cTypeWord = map[string]bool{
	"void": true, "char": true, "short": true, "int": true, "long": true, "float": true,
	"double": true, "signed": true, "unsigned": true, "_Bool": true, "const": true,
	"volatile": true, "restrict": true, "__restrict": true,
}

type scanTok struct {
	s     string
	ident bool
}

// scanTokens splits C text into the tokens ScanCalls reads: identifiers, punctuation
// and, as one opaque token each, numbers and string and character literals.
// Comments and preprocessor lines are dropped, the names of the macros the latter
// define collected.
func scanTokens(c []byte) (toks []scanTok, macros map[string]bool) {
	macros = map[string]bool{}
	s := string(c)
	lineStart := true
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == '\n':
			lineStart = true
			i++
			continue
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\f' || ch == '\v':
			i++
			continue
		case lineStart && ch == '#':
			// A directive, continued across lines ending in a backslash.
			j := i
			for j < len(s) {
				k := strings.IndexByte(s[j:], '\n')
				if k < 0 {
					j = len(s)
					break
				}
				j += k
				if j > 0 && s[j-1] == '\\' {
					j++
					continue
				}
				break
			}
			dir := strings.TrimSpace(s[i+1 : j])
			if rest, ok := strings.CutPrefix(dir, "define"); ok {
				rest = strings.TrimLeft(rest, " \t")
				n := 0
				for n < len(rest) && scanIdentByte(rest[n], n == 0) {
					n++
				}
				if n != 0 {
					macros[rest[:n]] = true
				}
			}
			i = j
			continue
		}
		lineStart = false
		switch {
		case ch == '/' && i+1 < len(s) && s[i+1] == '/':
			k := strings.IndexByte(s[i:], '\n')
			if k < 0 {
				i = len(s)
			} else {
				i += k
			}
		case ch == '/' && i+1 < len(s) && s[i+1] == '*':
			k := strings.Index(s[i+2:], "*/")
			if k < 0 {
				i = len(s)
			} else {
				i += k + 4
			}
		case ch == '"' || ch == '\'':
			j := i + 1
			for j < len(s) && s[j] != ch {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			toks = append(toks, scanTok{s: s[i:min(j+1, len(s))]})
			i = j + 1
		case scanIdentByte(ch, true):
			j := i
			for j < len(s) && scanIdentByte(s[j], false) {
				j++
			}
			toks = append(toks, scanTok{s: s[i:j], ident: true})
			i = j
		case ch >= '0' && ch <= '9':
			j := i
			for j < len(s) && (scanIdentByte(s[j], false) || s[j] == '.' ||
				(s[j] == '+' || s[j] == '-') && (s[j-1] == 'e' || s[j-1] == 'E' || s[j-1] == 'p' || s[j-1] == 'P')) {
				j++
			}
			toks = append(toks, scanTok{s: s[i:j]})
			i = j
		case ch == '-' && i+1 < len(s) && s[i+1] == '>':
			toks = append(toks, scanTok{s: "->"})
			i += 2
		default:
			toks = append(toks, scanTok{s: s[i : i+1]})
			i++
		}
	}
	return toks, macros
}

func scanIdentByte(b byte, first bool) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || !first && b >= '0' && b <= '9'
}

// matchOpen is the index of the "(" (or "[", "{") the closer at i closes, -1 where
// there is none.
func matchOpen(toks []scanTok, i int) int {
	closer := toks[i].s
	opener := map[string]string{")": "(", "]": "[", "}": "{"}[closer]
	depth := 0
	for j := i; j >= 0; j-- {
		switch toks[j].s {
		case closer:
			depth++
		case opener:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// matchClose is matchOpen forwards.
func matchClose(toks []scanTok, i int) int {
	opener := toks[i].s
	closer := map[string]string{"(": ")", "[": "]", "{": "}"}[opener]
	depth := 0
	for j := i; j < len(toks); j++ {
		switch toks[j].s {
		case opener:
			depth++
		case closer:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// ScanCalls reads the calls of the functions C defines; see CallScan.
func ScanCalls(c []byte) *CallScan {
	toks, macros := scanTokens(c)
	scan := &CallScan{
		Funcs:   map[string]*FuncCalls{},
		slots:   map[string]map[string][]string{},
		members: map[string][]string{},
	}
	known := map[string]bool{} // a function or a macro: a call of it is by name
	for _, n := range cLibNames {
		known[n] = true
	}
	for _, n := range cLibMacros {
		known[n] = true
	}
	for n := range macros {
		known[n] = true
	}
	types := map[string]bool{}
	for _, n := range cLibTypes {
		types[n] = true
	}
	vtMembers := map[string][]string{} // a table type's members, in order
	type body struct {
		name             string
		paramsFrom, from int // the tokens of the parameters, and those between the braces
		to               int
	}
	var bodies []body
	typedefBraces := false // the statement after `typedef struct {...}` names the type
	// File scope: a statement at a time, or a function's body.
	for i := 0; i < len(toks); {
		start := i
		depthParen := 0
		for i < len(toks) {
			t := toks[i].s
			if t == "(" {
				depthParen++
			} else if t == ")" {
				depthParen--
			}
			if depthParen == 0 && (t == ";" || t == "{") {
				break
			}
			i++
		}
		if i >= len(toks) {
			break
		}
		stmt := toks[start:i]
		if toks[i].s == "{" {
			end := matchClose(toks, i)
			if end < 0 {
				break
			}
			// A function's body is what follows its parameters' ")", attributes and
			// the inline mark between; a struct's, a union's and an initializer's
			// braces follow anything else.
			k := len(stmt) - 1
			for k >= 0 && stmt[k].ident && !cKeywordCall[stmt[k].s] {
				k--
			}
			if k >= 0 && stmt[k].s == ")" {
				// `__attribute__((inline))` stands after the parameters too.
				if o := matchOpen(stmt, k); o > 0 && stmt[o-1].s == "(" && o > 1 && stmt[o-2].s == "__attribute__" {
					k = o - 3
					for k >= 0 && stmt[k].ident {
						k--
					}
				}
			}
			if k >= 0 && stmt[k].s == ")" {
				if o := matchOpen(stmt, k); o > 0 && stmt[o-1].ident {
					name := stmt[o-1].s
					known[name] = true
					bodies = append(bodies, body{name, start + o, i + 1, end})
					i = end + 1
					continue
				}
			}
			typedefBraces = len(stmt) != 0 && stmt[0].s == "typedef"
			// A struct: a table's type records its members' order.
			if len(stmt) >= 2 && stmt[len(stmt)-2].s == "struct" && stmt[len(stmt)-1].ident {
				var members []string
				for j := i + 1; j+3 < end; j++ {
					if toks[j].s == "(" && toks[j+1].s == "*" && toks[j+2].ident && toks[j+3].s == ")" && strings.HasPrefix(toks[j+2].s, "ogo_m_") {
						members = append(members, toks[j+2].s)
					}
				}
				if len(members) != 0 {
					vtMembers[stmt[len(stmt)-1].s] = members
				}
			}
			// A table: `static const X_vt V = { "*T", f, g }`, its functions in the
			// order of X_vt's members.
			if len(stmt) >= 4 && stmt[len(stmt)-1].s == "=" {
				for j := 0; j+1 < len(stmt); j++ {
					if members, isVT := vtMembers[stmt[j].s]; isVT && stmt[j+1].ident {
						var fns []string
						for k := i + 1; k < end; k++ {
							if toks[k].ident && (toks[k-1].s == "," || toks[k-1].s == "{") {
								fns = append(fns, toks[k].s)
							}
						}
						vt := stmt[j].s
						if scan.slots[vt] == nil {
							scan.slots[vt] = map[string][]string{}
						}
						for m, fn := range fns {
							if m < len(members) {
								scan.slots[vt][members[m]] = append(scan.slots[vt][members[m]], fn)
								scan.members[members[m]] = append(scan.members[members[m]], fn)
							}
						}
						break
					}
				}
			}
			i = end + 1
			continue
		}
		// A statement: a typedef names a type, and a declaration with parameters
		// at its top level and no initializer declares a function.
		if typedefBraces {
			typedefBraces = false
			if len(stmt) != 0 && stmt[len(stmt)-1].ident {
				types[stmt[len(stmt)-1].s] = true
			}
		} else if len(stmt) != 0 && stmt[0].s == "typedef" {
			for j := 1; j < len(stmt); j++ {
				if stmt[j].ident && (j == len(stmt)-1 || j >= 2 && stmt[j-1].s == "*" && stmt[j-2].s == "(") {
					types[stmt[j].s] = true
				}
			}
		} else if !slicesContainsTok(stmt, "=") {
			for j := 1; j < len(stmt); j++ {
				if stmt[j].s == "(" && stmt[j-1].ident && !cTypeWord[stmt[j-1].s] {
					known[stmt[j-1].s] = true
					break
				}
			}
		}
		i++
	}
	defined := map[string]bool{}
	for _, b := range bodies {
		defined[b.name] = true
	}
	for _, b := range bodies {
		fc := &FuncCalls{}
		scan.Funcs[b.name] = fc
		// A name the function declares -- a parameter, a local -- is no function
		// of its name, whatever the C defines: `f(x)` through a local f of a
		// function type is a call through it.
		local := map[string]bool{}
		for j := b.paramsFrom + 1; j+1 < b.to; j++ {
			t := toks[j]
			if !t.ident || cTypeWord[t.s] || types[t.s] {
				continue
			}
			before, after := toks[j-1], toks[j+1].s
			typed := before.s == "*" || cTypeWord[before.s] || before.ident && types[before.s]
			if typed && (after == "=" || after == ";" || after == "," || after == "[" || after == ")") {
				local[t.s] = true
			}
		}
		for i := b.from; i < b.to; i++ {
			if toks[i].s != "(" || i == 0 {
				continue
			}
			prev := toks[i-1]
			switch {
			case prev.ident:
				switch {
				case cKeywordCall[prev.s]:
				case i >= 2 && (toks[i-2].s == "->" || toks[i-2].s == "."):
					if strings.HasPrefix(prev.s, "ogo_m_") {
						fc.Iface = append(fc.Iface, IfaceSite{VT: tableCast(toks, i-3, types), Member: prev.s})
					} else {
						fc.Other++
					}
				case local[prev.s]:
					fc.Other++
				case defined[prev.s]:
					fc.Direct = append(fc.Direct, prev.s)
				case known[prev.s]:
				default:
					fc.Other++
				}
			case prev.s == ")":
				if o := matchOpen(toks, i-1); o < 0 || !isCastGroup(toks[o+1:i-1], types) {
					fc.Other++
				}
			case prev.s == "]":
				fc.Other++
			}
		}
	}
	return scan
}

func slicesContainsTok(toks []scanTok, s string) bool {
	for _, t := range toks {
		if t.s == s {
			return true
		}
	}
	return false
}

// tableCast is the table type an interface call names: at, before its "->", closes
// `((const X_vt*)...)`; "" for any other spelling.
func tableCast(toks []scanTok, at int, types map[string]bool) string {
	if at < 0 || toks[at].s != ")" {
		return ""
	}
	o := matchOpen(toks, at)
	if o < 0 || o+5 >= len(toks) {
		return ""
	}
	if toks[o+1].s == "(" && toks[o+2].s == "const" && toks[o+3].ident && toks[o+4].s == "*" && toks[o+5].s == ")" {
		return toks[o+3].s
	}
	return ""
}

// isCastGroup reports whether a parenthesised group is a type, `(uint32_t)`,
// `(const Shape_vt*)`, `(struct S*)`: its parentheses are a cast and not a callee.
func isCastGroup(group []scanTok, types map[string]bool) bool {
	named := false
	for i := 0; i < len(group); i++ {
		t := group[i]
		switch {
		case t.s == "*":
		case t.s == "struct" || t.s == "union" || t.s == "enum":
			if i+1 >= len(group) || !group[i+1].ident {
				return false
			}
			i++
			named = true
		case cTypeWord[t.s]:
			named = named || t.s != "const" && t.s != "volatile" && t.s != "restrict" && t.s != "__restrict"
		case t.ident && types[t.s]:
			named = true
		default:
			return false
		}
	}
	return named
}
