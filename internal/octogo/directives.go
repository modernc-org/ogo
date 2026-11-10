// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// gc's compiler directives (cmd/compile/internal/noder of Go 1.27: noder.go's
// pragma and checkEmbed, lex.go's pragmaFlag and pragcgo, writer.go's
// checkPragmas and collectDecls). A //go: comment is gc's, not the language's,
// and this compiler implements one of them, //go:noescape (spin2.go). But a
// program gc refuses for one is a program Go rejects, and a file here is a Go
// file without its package clause -- the twin a probe builds puts `package
// main` in front -- so the directives of a file are asked what gc asks of its
// twin's, and refused in gc's words where gc refuses them: on a line of its
// own or misplaced, a flag directive on a function's declaration or nowhere,
// //go:build nowhere (it stands above the package clause, which a file here
// does not have), //go:embed on a variable and only with the embed package
// imported, which this compiler does not have, and //go:linkname in a file
// importing unsafe, naming a function or a variable the package declares.
// What gc accepts is accepted and, but for //go:noescape, means nothing here:
// there is no linker a //go:linkname could speak to, and a function without a
// body is a Spin2 object's.

// goDirective is a //go: comment as gc's scanner hands one to its pragma
// handler: its text after the "//", its offset in its token's separator, and
// whether nothing but blanks stand before it on its line.
type goDirective struct {
	off   int
	text  string
	blank bool
}

// goDirectives are the //go: comments of a token's separator, sep, in order;
// begins says a line begins where the separator does. A block comment holds
// none, and `// go:x`, with a space, is a comment.
func goDirectives(sep string, begins bool) (r []goDirective) {
	atLine := begins
	for i := 0; i < len(sep); {
		switch c := sep[i]; {
		case c == '\n':
			atLine = true
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case strings.HasPrefix(sep[i:], "/*"):
			end := strings.Index(sep[i+2:], "*/")
			if end < 0 {
				return r
			}
			i += end + 4
			atLine = false
		case strings.HasPrefix(sep[i:], "//"):
			end := strings.IndexByte(sep[i:], '\n')
			if end < 0 {
				end = len(sep) - i
			}
			if text := strings.TrimSuffix(sep[i+2:i+end], "\r"); strings.HasPrefix(text, "go:") {
				r = append(r, goDirective{i, text, atLine})
			}
			i += end
			atLine = false
		default:
			i++
			atLine = false
		}
	}
	return r
}

// gcPragma is a bit of gc's ir.PragmaFlag, which a directive sets on the
// declaration it is attached to.
type gcPragma uint32

const (
	gcGoBuild gcPragma = 1 << iota
	gcNoescape
	gcNorace
	gcNosplit
	gcNoinline
	gcNoCheckPtr
	gcSystemstack
	gcNowritebarrier
	gcNowritebarrierrec
	gcYeswritebarrierrec
	gcCgoUnsafeArgs
	gcUintptrKeepAlive
	gcUintptrEscapes
	gcRegisterParams

	// gcFuncPragmas are what a function's declaration takes: every flag but
	// //go:build's, which only the package clause takes.
	gcFuncPragmas = ^gcGoBuild
	// gcRuntimePragmas are the runtime's own.
	gcRuntimePragmas = gcSystemstack | gcNowritebarrier | gcNowritebarrierrec | gcYeswritebarrierrec
)

// gcPragmaFlag is gc's pragmaFlag: the flag a directive's verb sets, or none.
// //go:nointerface sets one only under the fieldtrack experiment.
func gcPragmaFlag(verb string) gcPragma {
	switch verb {
	case "go:build":
		return gcGoBuild
	case "go:noescape":
		return gcNoescape
	case "go:norace":
		return gcNorace
	case "go:nosplit":
		return gcNosplit | gcNoCheckPtr
	case "go:noinline":
		return gcNoinline
	case "go:nocheckptr":
		return gcNoCheckPtr
	case "go:systemstack":
		return gcSystemstack
	case "go:nowritebarrier":
		return gcNowritebarrier
	case "go:nowritebarrierrec":
		return gcNowritebarrierrec | gcNowritebarrier
	case "go:yeswritebarrierrec":
		return gcYeswritebarrierrec
	case "go:cgo_unsafe_args":
		return gcCgoUnsafeArgs | gcNoCheckPtr
	case "go:uintptrkeepalive":
		return gcUintptrKeepAlive
	case "go:uintptrescapes":
		return gcUintptrEscapes | gcUintptrKeepAlive
	case "go:registerparams":
		return gcRegisterParams
	}
	return 0
}

// gcDirectiveUse is what one directive asks of where it stands: a flag the
// declaration it is attached to must take, or an embed only a variable takes.
type gcDirectiveUse struct {
	flag  gcPragma
	embed bool
}

// gcLinkname is a //go:linkname (or //go:linknamestd) a file carries: the
// directive's word and the local name it says.
type gcLinkname struct {
	tok       Token
	off       int
	directive string
	local     string
	remote    string
}

// gcDirective is gc's pragma handler of one directive, d, of tok's separator:
// what it refuses of the directive alone, through errf, what the directive asks
// of the declaration it is attached to, and its linkname.
func gcDirective(d goDirective, errf func(format string, args ...any)) (use gcDirectiveUse, ln *gcLinkname) {
	text := d.text
	if !d.blank {
		// A directive stands on a line of its own.
		errf("misplaced compiler directive")
		return use, nil
	}
	switch {
	case strings.HasPrefix(text, "go:wasmimport "):
		if len(strings.Fields(text)) != 3 {
			errf("usage: //go:wasmimport importmodule importname")
		}
	case strings.HasPrefix(text, "go:wasmexport "):
		if len(strings.Fields(text)) != 2 {
			errf("usage: //go:wasmexport exportname")
		}
	case strings.HasPrefix(text, "go:linkname "), strings.HasPrefix(text, "go:linknamestd "):
		f := strings.Fields(text)
		if !(2 <= len(f) && len(f) <= 3) {
			errf("usage: //%s localname [linkname]", f[0])
			break
		}
		ln = &gcLinkname{directive: f[0], local: f[1]}
		if len(f) == 3 {
			ln.remote = f[2]
		}
	case text == "go:embed", strings.HasPrefix(text, "go:embed "):
		args, err := gcParseGoEmbed(text[len("go:embed"):])
		if err != nil {
			errf("%s", err)
		}
		if len(args) == 0 {
			errf("usage: //go:embed pattern...")
			break
		}
		use.embed = true
	case strings.HasPrefix(text, "go:cgo_import_dynamic ") && len(gcPragmaFields(text)) >= 4:
		// Permitted for general use, golang.org/x/sys/unix on Solaris.
		if lib := strings.Trim(gcPragmaFields(text)[3], `"`); lib != "" && !gcSafeArg(lib) {
			errf("invalid library name %q in cgo_import_dynamic directive", lib)
		}
		gcPragcgo(text, errf)
	case strings.HasPrefix(text, "go:cgo_"):
		// No file here is cgo's.
		errf("//%s only allowed in cgo-generated code", text)
		gcPragcgo(text, errf)
		fallthrough
	default:
		verb := text
		if i := strings.Index(text, " "); i >= 0 {
			verb = verb[:i]
		}
		flag := gcPragmaFlag(verb)
		if flag&gcRuntimePragmas != 0 {
			errf("//%s only allowed in runtime", verb)
		}
		if flag == gcUintptrKeepAlive {
			errf("//%s is only allowed in the standard library", verb)
		}
		use.flag = flag
	}
	return use, ln
}

// gcPragcgo is what gc's pragcgo refuses of a //go:cgo_ directive's arguments.
func gcPragcgo(text string, errf func(format string, args ...any)) {
	f := gcPragmaFields(text)
	verb := strings.TrimPrefix(f[0], "go:")
	switch verb {
	case "cgo_export_static", "cgo_export_dynamic":
		switch {
		case len(f) == 2 && !gcIsQuoted(f[1]):
		case len(f) == 3 && !gcIsQuoted(f[1]) && !gcIsQuoted(f[2]):
		default:
			errf(`usage: //go:%s local [remote]`, verb)
		}
	case "cgo_import_dynamic":
		switch {
		case len(f) == 2 && !gcIsQuoted(f[1]):
		case len(f) == 3 && !gcIsQuoted(f[1]) && !gcIsQuoted(f[2]):
		case len(f) == 4 && !gcIsQuoted(f[1]) && !gcIsQuoted(f[2]) && gcIsQuoted(f[3]):
		default:
			errf(`usage: //go:cgo_import_dynamic local [remote ["library"]]`)
		}
	case "cgo_import_static":
		if !(len(f) == 2 && !gcIsQuoted(f[1])) {
			errf(`usage: //go:cgo_import_static local`)
		}
	case "cgo_dynamic_linker":
		if !(len(f) == 2 && gcIsQuoted(f[1])) {
			errf(`usage: //go:cgo_dynamic_linker "path"`)
		}
	case "cgo_ldflag":
		if !(len(f) == 2 && gcIsQuoted(f[1])) {
			errf(`usage: //go:cgo_ldflag "arg"`)
		}
	}
}

func gcIsQuoted(s string) bool {
	return len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"'
}

func gcIsSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// gcPragmaFields is gc's pragmaFields: s split at blanks but inside double
// quotes, and before and after a quoted region; an unclosed quote's rest is no
// field.
func gcPragmaFields(s string) []string {
	var a []string
	inQuote := false
	fieldStart := -1
	for i, c := range s {
		switch {
		case c == '"':
			if inQuote {
				inQuote = false
				a = append(a, s[fieldStart:i+1])
				fieldStart = -1
			} else {
				inQuote = true
				if fieldStart >= 0 {
					a = append(a, s[fieldStart:i])
				}
				fieldStart = i
			}
		case !inQuote && gcIsSpace(c):
			if fieldStart >= 0 {
				a = append(a, s[fieldStart:i])
				fieldStart = -1
			}
		default:
			if fieldStart == -1 {
				fieldStart = i
			}
		}
	}
	if !inQuote && fieldStart >= 0 {
		a = append(a, s[fieldStart:])
	}
	return a
}

func gcSafeArg(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return '0' <= c && c <= '9' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || c == '.' || c == '_' || c == '/' || c >= utf8.RuneSelf
}

// gcParseGoEmbed is gc's parseGoEmbed: the patterns of a //go:embed, bare,
// double-quoted or back-quoted.
func gcParseGoEmbed(args string) ([]string, error) {
	var list []string
	for args = strings.TrimSpace(args); args != ""; args = strings.TrimSpace(args) {
		var path string
	Switch:
		switch args[0] {
		default:
			i := len(args)
			for j, c := range args {
				if unicode.IsSpace(c) {
					i = j
					break
				}
			}
			path = args[:i]
			args = args[i:]
		case '`':
			i := strings.Index(args[1:], "`")
			if i < 0 {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
			path = args[1 : 1+i]
			args = args[1+i+1:]
		case '"':
			i := 1
			for ; i < len(args); i++ {
				if args[i] == '\\' {
					i++
					continue
				}
				if args[i] == '"' {
					q, err := strconv.Unquote(args[:i+1])
					if err != nil {
						return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args[:i+1])
					}
					path = q
					args = args[i+1:]
					break Switch
				}
			}
			if i >= len(args) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		}
		if args != "" {
			r, _ := utf8.DecodeRuneInString(args)
			if !unicode.IsSpace(r) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		}
		list = append(list, path)
	}
	return list, nil
}

// directiveTokens are the tokens of a file a directive in whose separator is
// attached to a declaration gc's parser takes it for (syntax.parser's
// takePragma): the `func` of a function's or a method's declaration and the
// token after it, and the token beginning each variable's spec -- the `var` of
// one written alone too. A directive before any other token is attached to
// nothing (clearPragma): before a group's parenthesis, a statement, another
// declaration's spec or the end of the file.
func (f *File) directiveTokens(root Node) (funcToks, varToks map[int]bool) {
	funcToks, varToks = map[int]bool{}, map[int]bool{}
	for n := range it(root.ast) {
		if n.sym != TopLevelDecl {
			continue
		}
		for d := range it(n.ast) {
			if d.sym != FuncDecl {
				continue
			}
			first := true
			for c := range it(d.ast) {
				if first {
					if c.sym == 0 {
						funcToks[int(c.tok)] = true
					}
					first = false
					continue
				}
				// The token after `func`: a receiver's parenthesis or the name.
				funcToks[int(c.Pos())] = true
				break
			}
		}
	}
	var walk func(n Node)
	walk = func(n Node) {
		if n.sym == VarDecl {
			kids := slices.Collect(it(n.ast))
			if len(kids) > 1 && kids[0].sym == 0 && f.ch(kids[0].tok) == VAR && !(kids[1].sym == 0 && f.ch(kids[1].tok) == LPAREN) {
				varToks[int(kids[0].tok)] = true
			}
		}
		if n.sym == VarSpec {
			varToks[int(n.Pos())] = true
		}
		for c := range it(n.ast) {
			if c.sym != 0 {
				walk(c)
			}
		}
	}
	walk(root)
	return funcToks, varToks
}
