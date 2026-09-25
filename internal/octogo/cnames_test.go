// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	cnamesUpdate   = flag.Bool("cnames-update", false, "rewrite cnames.go from the target's include tree and the host's headers (see TestCNames)")
	cnamesSpin2cpp = flag.String("cnames-spin2cpp", "", "with -cnames-update: a spin2cpp checkout at the backend's pin, whose system modules every program is linked with")
)

// TestCNames holds the emitter's renaming to what the target's C library speaks for.
//
// A top-level symbol of the main package keeps its source name in the emitted C, so
// a name the library has already taken collides with it: a function its headers
// declare, one its sources define, a type, a macro. flexcc refuses some of these in
// the library's own source ("posixio.c: redefining function or subroutine read"),
// only warns about others ("Redefining function or subroutine clock with an
// incompatible type") and takes the rest in silence, and a macro breaks the
// declaration of a local, a parameter or a field of its name wherever it stands
// (`_Bool EOF;` is `_Bool (-1);`). The emitter renames every such name
// (cReserved, cUnusable), and cnames.go is where the lists of them come from.
//
// This test derives the target's part again, from the include tree the backend
// embeds, and fails when the tree names one the lists lack -- which is what a
// backend regeneration that adds a function looks like. With -cnames-update it
// rewrites cnames.go instead, adding the host's names (TestEmitCRun compiles the same
// C against the host's headers) and, given -cnames-spin2cpp, the functions of the
// backend's system modules, which live in the compiler rather than in the tree:
//
//	go test -run TestCNames -cnames-update -cnames-spin2cpp ~/src/.../spin2cpp
//
// Names are only ever added, so an update on another host, or without the
// checkout, keeps what an earlier one found.
func TestCNames(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc to preprocess the target's headers with")
	}

	tree := t.TempDir()
	if err := untarGz(filepath.Join("..", "flexcc", "p2include.tar.gz"), tree); err != nil {
		t.Fatal(err)
	}
	headers, err := emittedHeaders()
	if err != nil {
		t.Fatal(err)
	}
	macros, names, types, err := targetCNames(gcc, tree, headers)
	if err != nil {
		t.Fatal(err)
	}
	if len(macros) < 100 || len(names) < 500 || len(types) < 30 {
		// The scan found too little to be the library: a broken derivation must not
		// pass as a library with nothing in it.
		t.Fatalf("the target's library names %d macros, %d identifiers and %d types, too few", len(macros), len(names), len(types))
	}

	if *cnamesUpdate {
		hostMacros, hostNames, err := hostCNames(gcc, headers)
		if err != nil {
			t.Fatal(err)
		}
		var sys []string
		if *cnamesSpin2cpp != "" {
			if sys, err = spinSysNames(*cnamesSpin2cpp); err != nil {
				t.Fatal(err)
			}
		}
		allMacros := union(cLibMacros, macros, hostMacros)
		allTypes := slices.DeleteFunc(union(cLibTypes, types), func(s string) bool {
			_, ok := slices.BinarySearch(allMacros, s)
			return ok
		})
		// A macro or a type is renamed in every position already, so the last list
		// repeats neither.
		allNames := slices.DeleteFunc(union(cLibNames, names, hostNames, sys), func(s string) bool {
			_, isMacro := slices.BinarySearch(allMacros, s)
			_, isType := slices.BinarySearch(allTypes, s)
			return isMacro || isType
		})
		if err := os.WriteFile("cnames.go", cnamesSource(allMacros, allTypes, allNames), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("cnames.go: %d macros, %d types, %d file-scope names", len(allMacros), len(allTypes), len(allNames))
		return
	}

	var missing []string
	for _, m := range macros {
		if !cUnusable[m] {
			missing = append(missing, m+" (a macro)")
		}
	}
	for _, m := range types {
		if !cUnusable[m] {
			missing = append(missing, m+" (a type)")
		}
	}
	for _, n := range names {
		if !cUnusable[n] && !cReserved[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) != 0 {
		t.Errorf("the target's C library names %d identifiers the emitter does not rename: %s\n"+
			"regenerate cnames.go: go test -run TestCNames -cnames-update -cnames-spin2cpp <spin2cpp checkout>",
			len(missing), strings.Join(missing, " "))
	}
}

// TestFileScopeNames pins the scanner TestCNames derives the lists with, one C shape
// at a time: what each declares at file scope, with the type names and tags written
// there, and nothing from inside a parameter list or a body.
func TestFileScopeNames(t *testing.T) {
	for _, test := range []struct{ src, want string }{
		{"int read(int fd, void *buf, int count);", "read"},
		{"typedef struct { int quot, rem; } div_t;", "div_t"},
		{"typedef int (*putcfunc_t)(int c, void *f);", "putcfunc_t"},
		{"struct tm *gmtime(const long *t);", "gmtime tm"},
		{"extern int _mb_cur_max;", "_mb_cur_max"},
		{"int a, *b, c[3];", "a b c"},
		{"enum color { RED, GREEN = 2, BLUE };", "BLUE GREEN RED color"},
		// A prototype whose parameters name a type and a variable each is no
		// identifier list, and what follows it no old-style parameter declaration.
		{`double difftime(time_t t2, time_t t1) __fromfile("x.c"); static int dst = -1; int after(void);`, "after difftime"},
		// What a static declaration declares is the source's own.
		{"static int dst = -1; static struct group groups[] = {{1}}; static int helper(int c) { return c; }", ""},
		{"static enum { A, B } e;", "A B"},
		// A definition names itself and nothing inside its body.
		{"int close(int fd) { int local = fd; return local; } int next;", "close next"},
		// An old-style definition declares its parameters between the list and
		// the body; they name nothing outside it.
		{"wchar_t *wcsncat(dst, src, n) wchar_t *dst; const wchar_t *src; size_t n; { return dst; } int after;", "after wchar_t wcsncat"},
		{"char *strdup(s) const char *s; { return 0; }", "strdup"},
		{`void f(void) __attribute__((noreturn)); int g;`, "f g"},
	} {
		if got, want := fileScopeNames(test.src), strings.Fields(test.want); !slices.Equal(got, want) {
			t.Errorf("%s\ngot  %v\nwant %v", test.src, got, want)
		}
	}
}

// TestTypedefNames pins the typedef scan TestCNames derives cLibTypes with.
func TestTypedefNames(t *testing.T) {
	for _, test := range []struct{ src, want string }{
		{"typedef unsigned long size_t;", "size_t"},
		{"typedef struct _dir { int a; } DIR; typedef DIR vfs_dir_t;", "DIR vfs_dir_t"},
		{"typedef struct s_vfs_file_t vfs_file_t; typedef vfs_file_t FILE;", "FILE vfs_file_t"},
		{"typedef int (*putcfunc_t)(int c, void *f);", "putcfunc_t"},
		{"typedef int a, *b, c[4];", "a b c"},
		{"typedef struct { int quot, rem; } div_t; int div_t_user(void);", "div_t"},
		{"int notatype; struct tm { int x; };", ""},
	} {
		if got, want := typedefNames(test.src), strings.Fields(test.want); !slices.Equal(got, want) {
			t.Errorf("%s\ngot  %v\nwant %v", test.src, got, want)
		}
	}
}

// emittedHeaders is every header the emitted C may include: the ones the emitter
// names (e.includes["stdio.h"] = true) and the ones an import maps to. Read from
// the emitter's source, so a header it starts to include is scanned without anyone
// having to remember to list it here.
func emittedHeaders() ([]string, error) {
	src, err := os.ReadFile("emit.go")
	if err != nil {
		return nil, err
	}
	var r []string
	for _, m := range regexp.MustCompile(`includes\["([A-Za-z0-9_/]+\.h)"\]`).FindAllSubmatch(src, -1) {
		r = append(r, string(m[1]))
	}
	for _, h := range importIncludes {
		r = append(r, h)
	}
	slices.Sort(r)
	r = slices.Compact(r)
	if len(r) < 5 {
		return nil, fmt.Errorf("emit.go names only %d headers: %v", len(r), r)
	}
	return r, nil
}

// flexccDefines are the macros flexcc defines before it reads a P2 program
// (spin2cpp's cmdline.c, for `flexcc -2` compiling to assembly), so the headers are
// read with the conditionals the backend takes.
var flexccDefines = []string{
	"-D__propeller__=2", "-D__P2__=1", "-D__propeller2__=1",
	"-D__FASTSPIN__=7", "-D__FLEXSPIN__=7", "-D__FLEXBASIC__=7", "-D__FLEXC__=7", "-D__SPINCVT__=7",
	"-D__FLEX_MAJOR__=7", "-D__FLEX_MINOR__=7", "-D__FLEX_REV__=3",
	"-D__SPIN2PASM__=1", "-D__OUTPUT_ASM__=1",
}

// targetCNames is what the target's library names: the macros of the headers the
// emitted C includes, and the file-scope identifiers of those headers and of every C
// source of the library. A source is compiled into a program when the program uses
// a function the source defines -- `__fromfile` in the header's declaration -- and
// its functions then share the program's name space, which is how a user function
// named close collides with posixio.c's. Every source is scanned, not only the ones
// a program is seen to pull in: a name renamed that did not need it costs nothing.
func targetCNames(gcc, tree string, headers []string) (macros, names, types []string, err error) {
	incs := filepath.Join(tree, "ogo_cnames.c")
	var b strings.Builder
	for _, h := range headers {
		fmt.Fprintf(&b, "#include <%s>\n", h)
	}
	if err := os.WriteFile(incs, []byte(b.String()), 0o644); err != nil {
		return nil, nil, nil, err
	}
	base := append([]string{"-undef", "-nostdinc", "-I", tree}, flexccDefines...)
	dm, err := runGcc(gcc, append(append([]string{"-E", "-dM"}, base...), incs)...)
	if err != nil {
		return nil, nil, nil, err
	}
	macros = macroNames(dm)

	files := []string{incs}
	for _, dir := range []string{"libc", "libsys"} {
		if err := filepath.WalkDir(filepath.Join(tree, dir), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".c") {
				files = append(files, path)
			}
			return err
		}); err != nil {
			return nil, nil, nil, err
		}
	}
	var (
		mu       sync.Mutex
		all      []string
		typedefs []string
		errs     []error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, runtime.GOMAXPROCS(0))
	)
	for _, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			out, err := runGcc(gcc, append(append([]string{"-E", "-P"}, base...), "-I", filepath.Dir(f), f)...)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			all = append(all, fileScopeNames(out)...)
			typedefs = append(typedefs, typedefNames(out)...)
		}()
	}
	wg.Wait()
	if len(errs) != 0 {
		return nil, nil, nil, errs[0]
	}
	return macros, union(all), union(typedefs), nil
}

// hostCNames is what the host's C library names for the same headers, the shim in
// testdata/hostp2 standing in for propeller2.h, compiled as TestEmitCRun compiles:
// -std=gnu11, which exposes much more than ISO C. A name that begins with an
// underscore is left out, being the C implementation's to use rather than a
// program's; they are most of glibc's, and differ from one glibc to the next.
func hostCNames(gcc string, headers []string) (macros, names []string, err error) {
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "ogo-cnames-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	incs := filepath.Join(dir, "ogo_cnames.c")
	var b strings.Builder
	for _, h := range headers {
		fmt.Fprintf(&b, "#include <%s>\n", h)
	}
	if err := os.WriteFile(incs, []byte(b.String()), 0o644); err != nil {
		return nil, nil, err
	}
	dm, err := runGcc(gcc, "-std=gnu11", "-E", "-dM", "-I", shim, incs)
	if err != nil {
		return nil, nil, err
	}
	src, err := runGcc(gcc, "-std=gnu11", "-E", "-P", "-I", shim, incs)
	if err != nil {
		return nil, nil, err
	}
	keep := func(s []string) []string {
		return slices.DeleteFunc(s, func(n string) bool { return strings.HasPrefix(n, "_") })
	}
	return keep(macroNames(dm)), keep(union(fileScopeNames(src))), nil
}

// spinSysNames is every method of the backend's system modules -- the Spin code
// flexcc compiles into every program beside it (spin2cpp's spinc.c): the P2
// platform's, the common, float and allocator ones. Their methods share the name
// space too, and some of them are C library functions in disguise, a `pri file
// "libc/..."` line pulling one in by name.
func spinSysNames(dir string) ([]string, error) {
	decl := regexp.MustCompile(`(?i)^\s*(?:pub|pri)\s+(.*)$`)
	annot := regexp.MustCompile(`\{[^}]*\}`)
	file := regexp.MustCompile(`(?i)^file\s+"[^"]*"\s*`)
	ident := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	var r []string
	for _, m := range []string{"p2_code.spin", "common_pasm.spin", "common.spin", "float.spin", "gcalloc.spin", "gc_pasm.spin"} {
		b, err := os.ReadFile(filepath.Join(dir, "sys", m))
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			sm := decl.FindStringSubmatch(line)
			if sm == nil {
				continue
			}
			rest := strings.TrimSpace(annot.ReplaceAllString(sm[1], ""))
			rest = file.ReplaceAllString(rest, "")
			if id := ident.FindString(rest); id != "" {
				// A method name the grammar of C cannot spell -- input`$ -- is no
				// collision.
				if len(rest) == len(id) || strings.ContainsAny(rest[len(id):len(id)+1], " (:|\t") {
					r = append(r, id)
				}
			}
		}
	}
	if len(r) < 100 {
		return nil, fmt.Errorf("%s: only %d system module methods; is it a spin2cpp checkout?", dir, len(r))
	}
	return union(r), nil
}

func runGcc(gcc string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(gcc, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gcc %s: %v\n%s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return stdout.String(), nil
}

// macroNames is the names a `gcc -E -dM` listing defines.
func macroNames(dm string) (r []string) {
	for _, line := range strings.Split(dm, "\n") {
		rest, ok := strings.CutPrefix(line, "#define ")
		if !ok {
			continue
		}
		if i := strings.IndexAny(rest, " ("); i >= 0 {
			rest = rest[:i]
		}
		r = append(r, rest)
	}
	return union(r)
}

// cKeywordish is what fileScopeNames sees at file scope that names nothing: C's
// keywords and the GNU and flexcc extensions spelled like identifiers.
var cKeywordish = map[string]bool{}

func init() {
	for _, s := range strings.Fields(`auto break case char const continue default do double
		else enum extern float for goto if inline int long register restrict return short
		signed sizeof static struct switch typedef union unsigned void volatile while
		_Alignas _Alignof _Atomic _Bool _Complex _Generic _Imaginary _Noreturn
		_Static_assert _Thread_local
		__attribute__ __attribute __asm__ __asm asm __extension__ __inline__ __inline
		__restrict__ __restrict __const __const__ __volatile__ __volatile __signed__
		__signed __typeof__ __typeof typeof __alignof__ __alignof __int128 __label__
		__auto_type _Float16 _Float32 _Float32x _Float64 _Float64x _Float128 __float128
		__fromfile __using`) {
		cKeywordish[s] = true
	}
}

// cToken is a token of preprocessed C with the depth it stands at: the braces and
// parentheses around it, not counting itself.
type cToken struct {
	s              string
	braces, parens int
}

// cTokens splits preprocessed C into tokens. It knows as much of C's lexical
// grammar as finding names needs: identifiers, literals whole, and everything else
// a character at a time.
func cTokens(src string) (r []cToken) {
	isIdentStart := func(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	isIdent := func(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }
	braces, parens := 0, 0
	for i := 0; i < len(src); {
		c := src[i]
		var tok string
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
			continue
		case c == '#' && (i == 0 || src[i-1] == '\n'):
			// a #pragma or a line marker
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			tok, i = src[i:min(j+1, len(src))], j+1
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdent(src[j]) {
				j++
			}
			tok, i = src[i:j], j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (isIdent(src[j]) || src[j] == '.' ||
				(src[j] == '+' || src[j] == '-') && strings.ContainsRune("eEpP", rune(src[j-1]))) {
				j++
			}
			tok, i = src[i:j], j
		default:
			tok, i = src[i:i+1], i+1
		}
		switch tok {
		case "}":
			braces--
		case ")":
			parens--
		}
		r = append(r, cToken{tok, braces, parens})
		switch tok {
		case "{":
			braces++
		case "(":
			parens++
		}
	}
	return r
}

// cExternalDecls splits a translation unit's tokens into its external
// declarations: a declaration ends at its semicolon, a function definition at the
// end of its body. An old-style definition, `char *f(s) char *s; { ... }`, declares
// its parameters between the parameter list and the body, each with a semicolon of
// its own, and is one definition all the same.
func cExternalDecls(toks []cToken) (r [][]cToken) {
	var (
		cur      []cToken
		knr      bool // between an old-style definition's identifier list and its body
		body     bool // inside a function body
		listOnly bool // the parameter group just closed held only identifiers
	)
	for _, t := range toks {
		cur = append(cur, t)
		if t.braces != 0 || t.parens != 0 {
			continue
		}
		switch t.s {
		case ")":
			// Was the group this closes an identifier list, `(s, n)`? One name
			// between commas: `(time_t t1, time_t t0)` is a prototype's.
			m := len(cur) - 2
			for m >= 0 && cur[m].parens > 0 {
				m--
			}
			group := cur[m+1 : len(cur)-1]
			listOnly = len(group)%2 == 1
			for g, u := range group {
				if g%2 == 0 && (!isCIdentTok(u.s) || cKeywordish[u.s]) || g%2 == 1 && u.s != "," {
					listOnly = false
				}
			}
		case ";":
			if !knr {
				r = append(r, cur)
				cur = nil
			}
		case "{":
			if len(cur) >= 2 {
				switch cur[len(cur)-2].s {
				case ")", ";":
					body, knr = true, false
				}
			}
		case "}":
			if body {
				r = append(r, cur)
				cur, body = nil, false
			}
		default:
			// An identifier after an identifier list at file scope is the type of
			// an old-style definition's first parameter declaration.
			if listOnly && len(cur) >= 2 && cur[len(cur)-2].s == ")" && isCIdentTok(t.s) && !cDeclSuffix[t.s] {
				knr = true
			}
		}
		if t.s != ")" {
			listOnly = false
		}
	}
	if len(cur) != 0 {
		r = append(r, cur)
	}
	return r
}

// cDeclSuffix is what may follow a declarator's parameter list without starting an
// old-style definition's parameter declarations.
var cDeclSuffix = map[string]bool{
	"__attribute__": true, "__attribute": true, "__asm__": true, "__asm": true, "asm": true,
	"__fromfile": true,
}

func isCIdentTok(s string) bool {
	c := s[0]
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// fileScopeNames is the identifiers a preprocessed C translation unit gives
// external names at file scope: the ones its declarations and definitions declare,
// with the type names and tags written around them -- more than it declares, which
// costs the rename of a user name that did not need one and nothing else. What a
// static declaration declares is the source's own and is left out; flexcc keeps it
// there (a user function named like posixio.c's static _txputc builds and runs).
// Two kinds of declared name stand inside a bracket and are taken from there: a
// function pointer declarator's, `(*name)`, and an enumeration's constants, which
// are taken even from a static declaration.
func fileScopeNames(src string) []string {
	var r []string
	for _, d := range cExternalDecls(cTokens(src)) {
		static := false
		for _, t := range d {
			if t.braces == 0 && t.parens == 0 && t.s == "static" {
				static = true
			}
			if t.s == "(" || t.s == "{" || t.s == "=" {
				break
			}
		}
		// A definition's body, and what an old-style one declares before it, name
		// nothing outside: only the tokens up to the parameter list count.
		head := d
		for k, t := range d {
			if t.braces == 0 && t.parens == 0 && t.s == "{" && k > 0 && (d[k-1].s == ")" || d[k-1].s == ";") {
				head = d[:k]
				for m := k - 1; m >= 0; m-- {
					if d[m].s == ")" && d[m].braces == 0 && d[m].parens == 0 {
						head = d[:m+1]
						break
					}
				}
				break
			}
		}
		enum := false
		for k, t := range head {
			s := t.s
			if s == "enum" && t.braces == 0 {
				enum = true
			}
			if !isCIdentTok(s) || cKeywordish[s] {
				continue
			}
			var prev, prv2 string
			if k > 0 {
				prev = head[k-1].s
			}
			if k > 1 {
				prv2 = head[k-2].s
			}
			switch {
			case t.braces == 1 && enum && t.parens == 0 && (prev == "{" || prev == ","):
				r = append(r, s)
			case static:
			case t.braces == 0 && t.parens == 0:
				r = append(r, s)
			case t.braces == 0 && t.parens == 1 && prev == "*" && prv2 == "(":
				r = append(r, s)
			}
		}
	}
	return union(r)
}

// typedefNames is the names a preprocessed C translation unit declares with a
// typedef: each declarator's, the name in `(*name)` or else the last identifier
// written outside every bracket -- `typedef struct _dir {...} DIR;` is DIR, and
// `typedef vfs_file_t FILE;` FILE.
func typedefNames(src string) []string {
	var r []string
	for _, d := range cExternalDecls(cTokens(src)) {
		if len(d) == 0 || d[0].s != "typedef" {
			continue
		}
		var part []cToken
		flush := func() {
			name, ptrName := "", ""
			for k, t := range part {
				if t.braces != 0 || !isCIdentTok(t.s) || cKeywordish[t.s] {
					continue
				}
				switch {
				case t.parens == 1 && k >= 2 && part[k-1].s == "*" && part[k-2].s == "(" && ptrName == "":
					ptrName = t.s
				case t.parens == 0:
					name = t.s
				}
			}
			if ptrName != "" {
				name = ptrName
			}
			if name != "" {
				r = append(r, name)
			}
			part = nil
		}
		for _, t := range d[1:] {
			if t.braces == 0 && t.parens == 0 && t.s == "," {
				flush()
				continue
			}
			part = append(part, t)
		}
		flush()
	}
	return union(r)
}

// union is the sorted, duplicate-free union of some lists of names.
func union(lists ...[]string) []string {
	var r []string
	for _, l := range lists {
		r = append(r, l...)
	}
	slices.Sort(r)
	return slices.Compact(r)
}

func cnamesSource(macros, types, names []string) []byte {
	var b bytes.Buffer
	b.WriteString(`// Code generated by TestCNames -cnames-update. DO NOT EDIT.

package octogo

// cLibMacros is every macro the headers the emitted C includes define, on the target
// and on the host. The emitter renames an identifier of one of these names in every
// position (cUnusable).
var cLibMacros = []string{
`)
	writeNames(&b, macros)
	b.WriteString(`}

// cLibTypes is every type the target's headers and library sources name with a
// typedef. The target's C compiler cannot parse a declarator named like a typedef
// in scope -- a local FILE is "syntax error, unexpected type name" -- where C lets
// a local shadow one, so the emitter renames an identifier of one of these names in
// every position (cUnusable).
var cLibTypes = []string{
`)
	writeNames(&b, types)
	b.WriteString(`}

// cLibNames is every other identifier the target's C library speaks for at file scope
// -- declared by those headers, defined by a source of the library or by the
// backend's system modules -- and what the host's headers declare. The emitter
// renames a top-level symbol of one of these names (cReserved).
var cLibNames = []string{
`)
	writeNames(&b, names)
	b.WriteString("}\n")
	return b.Bytes()
}

// writeNames writes a sorted list of names, quoted, several to a line.
func writeNames(b *bytes.Buffer, names []string) {
	line := "\t"
	for _, n := range names {
		q := fmt.Sprintf("%q,", n)
		if len(line) > 1 && len(line)+1+len(q) > 88 {
			b.WriteString(strings.TrimRight(line, " ") + "\n")
			line = "\t"
		}
		line += q + " "
	}
	if len(line) > 1 {
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
}

// untarGz extracts the regular files of a .tar.gz into dir.
func untarGz(path, dir string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Clean(h.Name)
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("%s: unsafe path %q", path, h.Name)
		}
		dst := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
	}
}
