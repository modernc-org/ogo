// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"modernc.org/ccgo/v4/lib"
	"modernc.org/gc/v3"
)

const (
	cloneDir    = "flexprop"
	flexpropURL = "https://github.com/totalspectrum/flexprop.git"
	// flexpropRef pins the flexprop source so backend regeneration is
	// reproducible. v7.6.11 (commit 858f51c4a24e7ae0f6cbc78f625c731083ad304f,
	// released 2026-06-15) is the latest flexprop release as of 2026-07-10;
	// upstream cuts releases roughly every 1-2 months.
	//
	// The committed ccgo_<goos>_<goarch>.go was regenerated against this pin on
	// 2026-07-15 with ccgo v4.34.0 (flexprop repo and spin2cpp submodule both at
	// v7.6.11); mcpp_main.c.diff applied cleanly. To adopt a new flexpropRef: bump it,
	// `rm -rf flexprop flexprop_install`, rerun `go generate` (linux/amd64 only),
	// then update the flexcc --help golden in internal/flexcc/all_test.go.
	flexpropRef = "v7.6.11"
	installDir  = "flexprop_install"
)

var (
	gmake  = "make"
	goarch = env("TARGET_GOARCH", env("GOARCH", runtime.GOARCH))
	goos   = env("TARGET_GOOS", env("GOOS", runtime.GOOS))
	gsed   = "sed"
	target = fmt.Sprintf("%s/%s", goos, goarch)
)

func fail(rc int, msg string, args ...any) {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(fmt.Sprintf(msg, args...)))
	os.Exit(rc)
}

func shell(inDir, cmd string, args ...string) (err error) {
	fmt.Printf("%s: %s %v\n", inDir, cmd, args)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)

	defer cancel()

	p := exec.CommandContext(ctx, cmd, args...)
	p.Dir = inDir
	p.Stdout = os.Stdout
	p.Stderr = os.Stderr
	return p.Run()
}

func env(name, deflt string) (r string) {
	r = deflt
	if s := os.Getenv(name); s != "" {
		r = s
	}
	return r
}

func main() {
	if ccgo.IsExecEnv() {
		if err := ccgo.NewTask(goos, goarch, os.Args, os.Stdout, os.Stderr, nil).Main(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return
	}

	wd, err := os.Getwd()
	if err != nil {
		fail(1, "os.Getwd: err=%v", err)
	}

	if wd, err = filepath.Abs(wd); err != nil {
		fail(1, "filepath.Abs(%q): err=%v", wd, err)
	}

	switch target {
	case "linux/amd64":
		// ok
	default:
		fail(1, "unsupported target: %s", target)
	}

	fi, err := os.Stat(cloneDir)
	if err != nil && !os.IsNotExist(err) {
		fail(1, "stat(%s): err=%v", cloneDir, err)
	}

	if err == nil && !fi.IsDir() {
		fail(1, "%s expected to not exist or to be a directory", cloneDir)
	}

	if err != nil { // cloneDir does not exist
		if err := shell("", "git", "clone", "--recursive", flexpropURL); err != nil {
			fail(1, "git clone: err=%v", err)
		}

		if err := shell(cloneDir, "git", "checkout", flexpropRef); err != nil {
			fail(1, "git checkout %s: err=%v", flexpropRef, err)
		}

		if err := shell(cloneDir, "git", "submodule", "update", "--init", "--recursive"); err != nil {
			fail(1, "git submodule update: err=%v", err)
		}

		if err := shell(filepath.Join(cloneDir, "spin2cpp"), "git", "apply", filepath.Join(wd, "mcpp_main.c.diff")); err != nil {
			fail(1, "git apply: err=%v", err)
		}
	}

	flexccDir := filepath.Join(wd, "flexcc")
	flexccGoSrc := filepath.Join(wd, cloneDir, "spin2cpp", "build", "flexcc.go")
	installDir := filepath.Join(wd, filepath.Join(installDir))
	installDir2 := filepath.Join(installDir, "flexprop")
	os.RemoveAll(installDir)
	os.Remove(flexccGoSrc)
	if err := os.MkdirAll(flexccDir, 0755); err != nil {
		fail(1, "os.MkdirAll(%q): err=%v", flexccDir, err)
	}

	if err := os.MkdirAll(installDir2, 0755); err != nil {
		fail(1, "os.MkdirAll(%q): err=%v", installDir2, err)
	}

	args := []string{
		"ccgo",

		"--prefix-enumerator=_",
		"--prefix-external=x__",
		"--prefix-macro=m_",
		"--prefix-static-internal=s__",
		"--prefix-static-none=s__",
		"--prefix-tagged-enum=_",
		"--prefix-tagged-struct=_",
		"--prefix-tagged-union=_",
		"--prefix-typename=_",
		"--prefix-undefined=_",
		"-DNDEBUG",
		"-extended-errors",
		"-ignore-link-errors",

		"-exec",
		"make",
		"-C", cloneDir,
		"clean",
		"install",
		fmt.Sprintf("INSTALL=%s", installDir),
	}

	fmt.Printf("%v\n", args)
	if err := ccgo.NewTask(goos, goarch, args, os.Stdout, os.Stderr, nil).Main(); err != nil {
		fail(1, "ccgo -exec: err=%v", err)
	}

	flexccGoDest := filepath.Join(flexccDir, fmt.Sprintf("ccgo_%s_%s.go", goos, goarch))
	main2lib(flexccGoDest, flexccGoSrc)

	// Bundle the installed flexprop P2 include/lib tree next to the transpiled
	// compiler so the in-repo flexcc is self-contained (see flexcc/p2include.go),
	// and carry flexprop's license for attribution.
	if err := writeP2Include(filepath.Join(installDir, "include"), filepath.Join(flexccDir, "p2include.tar.gz")); err != nil {
		fail(1, "writeP2Include: err=%v", err)
	}
	if err := copyFile(filepath.Join(wd, cloneDir, "License.txt"), filepath.Join(flexccDir, "LICENSE-flexprop")); err != nil {
		fail(1, "copy flexprop license: err=%v", err)
	}
}

// writeP2Include packs every regular file under srcDir into a deterministic
// gzip-compressed tar at destFile: entries sorted by slash-path, fixed mode and
// mtime, no uid/gid, so identical input yields identical bytes (no spurious
// regen diffs). The result is embedded by internal/flexcc/p2include.go and
// extracted at runtime to give flexcc its include path. Symlinks/specials are
// rejected because p2include.go's untar only materializes dirs and regular files.
func writeP2Include(srcDir, destFile string) error {
	var files []string
	err := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink not supported in P2 include tree: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("non-regular file in P2 include tree: %s", p)
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	var buf bytes.Buffer
	gw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gw)
	epoch := time.Unix(0, 0).UTC()
	for _, p := range files {
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     filepath.ToSlash(rel),
			Mode:     0o644,
			Size:     int64(len(body)),
			ModTime:  epoch,
			Format:   tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(body); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	return os.WriteFile(destFile, buf.Bytes(), 0o644)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func main2lib(destFn, srcFn string) {
	b, err := os.ReadFile(srcFn)
	if err != nil {
		fail(1, "os.ReadFile(%q): err=%v", srcFn, err)
	}

	ba := bytes.Split(b, []byte{'\n'})
	pref := []byte("//")
	pat := []byte{'{'}
	repl := []byte("<left-brace>")
	for i, v := range ba {
		if tv := bytes.TrimSpace(v); !bytes.HasPrefix(tv, pref) {
			continue
		}

		ba[i] = bytes.ReplaceAll(v, pat, repl)
	}
	b = bytes.Join(ba, []byte{'\n'})

	ast, err := gc.ParseFile(srcFn, b)
	if err != nil {
		fail(1, "gc.ParseFile(%s): err=%v", srcFn, err)
	}

	buf := bytes.NewBuffer(nil)
	w := func(s string, args ...interface{}) {
		fmt.Fprintf(buf, s, args...)
	}

	src := ast.SourceFile
	w("%s\n", src.PackageClause.Source(true))
	if src.ImportDeclList != nil {
		w("%s\n", src.ImportDeclList.Source(true))
	}

	vars := map[*gc.VarDeclNode]string{}
	xvars := map[string]*gc.VarDeclNode{}
	funcs := map[string]struct{}{}
	initFuncs := map[string]*gc.FunctionDeclNode{}
	initNum := 0
	for l := src.TopLevelDeclList; l != nil; l = l.List {
		switch x := l.TopLevelDecl.(type) {
		case *gc.FunctionDeclNode:
			switch nm := x.FunctionName.IDENT.Src(); {
			case nm == "__ccgo_fp":
				// nop
			case nm == "main":
				// nop
			case nm == "init":
				// nop
			default:
				funcs[nm] = struct{}{}
			}
		}
	}
	var a []string
	for l := src.TopLevelDeclList; l != nil; l = l.List {
		switch x := l.TopLevelDecl.(type) {
		case *gc.VarDeclNode:
			if x.LPAREN.IsValid() {
				w("%s\n\n", x.Source(true))
				break
			}

			switch y := x.VarSpec.(type) {
			case *gc.VarSpecNode:
				nm := y.IDENT.Src()
				if !strings.HasPrefix(nm, "x__") && !strings.HasPrefix(nm, "s__") {
					w("%s\n\n", x.Source(true))
					break
				}

				vars[x] = nm
				xvars[nm] = x
			default:
				w("%s\n\n", x.Source(true))
			}
		case *gc.ConstDeclNode:
			w("%s\n\n", x.Source(true))
		case *gc.TypeDeclNode:
			w("%s\n\n", x.Source(true))
		case *gc.FunctionDeclNode:
			switch nm := x.FunctionName.IDENT.Src(); {
			case nm == "__ccgo_fp":
				w("%s\n\n", x.Source(true))
				continue
			case nm == "main":
				// nop
			case nm == "init":
				s := x.Source(true)
				initNum++
				nm := fmt.Sprintf("%s%v", nm, initNum)
				initFuncs[nm] = x
				ix := strings.Index(s, "func init")
				// func init
				head := s[:ix]
				tail := s[ix+9:]
				tail = rename(tail, funcs)
				w("%sfunc (cc *CC) init%v%s\n\n", head, initNum, tail)
			default:
				s := x.Source(true)
				ix := strings.Index(s, "{")
				// func foo(tls *libc.TLS... {
				head := s[:ix]
				head = strings.Replace(head, "(tls *libc.TLS", "(tls *libc.TLS, cc *CC", 1)
				tail := s[ix:]
				tail = rename(tail, funcs)
				w("%s%s\n\n", head, tail)

			}
		default:
			fail(1, "%T", x)
		}
	}

	w("\n\ntype CC struct{\n")
	w("fopen map[uintptr]struct{}\n")
	w("mallocs map[uintptr]struct{}\n")
	w("stderr io.Writer\n")
	w("stdin io.Reader\n")
	w("stdout io.Writer\n")
	a = a[:0]
	for k := range xvars {
		a = append(a, k)
	}
	slices.Sort(a)
	initializers := map[string]gc.Node{}
	for _, nm := range a {
		vd := xvars[nm]
		vs := vd.VarSpec.(*gc.VarSpecNode)
		switch {
		case vs.TypeNode != nil:
			w("%s %s\n", nm, vs.TypeNode.Source(true))
			if vs.ExpressionList != nil {
				fail(1, "TODO")
			}
		default:
			switch x := vs.ExpressionList.Expression.(type) {
			case *gc.CompositeLitNode:
				initializers[nm] = x
				w("%s %s\n", nm, x.LiteralType.Source(true))
			case *gc.PrimaryExprNode:
				initializers[nm] = x
				w("%s %s\n", nm, primaryExprType(x))
			case *gc.OperandNode:
				if x.OperandName != nil {
					initializers[nm] = x
					w("%s %s\n", nm, x.OperandName.Source(true))
					break
				}

				if x.TypeArgs != nil {
					fail(1, "%T\n%s", x, x.Source(false))
				}

				if x.LiteralValue != nil {
					fail(1, "%T\n%s", x, x.Source(false))
				}

				fail(1, "TODO")
			case *gc.BinaryExpressionNode:
				initializers[nm] = x
				w("%s %s\n", nm, exprType(x))
			case *gc.UnaryExprNode:
				initializers[nm] = x
				w("%s %s\n", nm, exprType(x))
			default:
				fail(1, "%T(A) %v: %s", x, x.Position(), x.Source(false))
			}

		}
	}
	w("}\n\n")
	w("func newCC(pinner *runtime.Pinner) (cc *CC) {\n")
	w(`cc = &CC{
		fopen: map[uintptr]struct{}{},
		mallocs: map[uintptr]struct{}{},
	}
`)
	w("pinner.Pin(cc)\n")
	a = a[:0]
	for k := range initializers {
		a = append(a, k)
	}
	slices.Sort(a)
	for _, nm := range a {
		w("cc.%s = %s\n", nm, rename(initializers[nm].Source(true), funcs))
	}
	a = a[:0]
	for k := range initFuncs {
		a = append(a, k)
	}
	slices.Sort(a)
	for _, nm := range a {
		w("cc.%s()\n", nm)
	}
	w("return cc")
	w("}\n")

	// Fix qsort comparators. The per-function rewrite above gives every function
	// an extra cc *CC parameter, but libc.Xqsort invokes its comparator through
	// the C ABI func(*libc.TLS, uintptr, uintptr) int32 and cannot pass cc. For
	// each comparator F registered via __ccgo_fp inside a libc.Xqsort call, emit a
	// top-level trampoline F__ccb with that exact signature which recovers the
	// active *CC from ccCurrent (a package global set at Main entry, see
	// flexcc.go) and forwards to F, and point the call site at F__ccb. Assumes the
	// comparator is written inline as __ccgo_fp(F) on the Xqsort call line, which
	// holds for every qsort in the pinned flexprop source.
	gen := buf.String()
	var cbs []string
	seen := map[string]struct{}{}
	gen = qsortCbRE.ReplaceAllStringFunc(gen, func(m string) string {
		fn := qsortCbRE.FindStringSubmatch(m)[1]
		if _, ok := seen[fn]; !ok {
			seen[fn] = struct{}{}
			cbs = append(cbs, fn)
		}
		return strings.Replace(m, "__ccgo_fp("+fn+")", "__ccgo_fp("+fn+"__ccb)", 1)
	})
	buf.Reset()
	buf.WriteString(gen)
	slices.Sort(cbs)
	for _, fn := range cbs {
		w("\nfunc %s__ccb(tls *libc.TLS, a uintptr, b uintptr) int32 {\n\treturn %s(tls, ccCurrent, a, b)\n}\n", fn, fn)
	}

	if err := os.WriteFile(destFn, buf.Bytes(), 0660); err != nil {
		fail(1, "%v", err)
	}

	// libc.X__builtin_snprintf(
	// libc.X__builtin_vsnprintf(
	// libc.Xexit(
	// libc.Xfclose(
	// libc.Xfopen(
	// libc.Xfprintf(
	// libc.Xfreopen(
	// libc.Xprintf(
	// libc.Xsprintf(
	// libc.Xvfprintf(
	// libc.calloc(
	// libc.free(
	// libc.malloc(
	// libc.realloc(

	if err := shell("", gsed, "-i",
		"-e", `s/package main/package flexcc/g`,
		"-e", `s/"reflect"/"io";"reflect";"runtime"/g`,
		"-e", `s/\<libc\.Int32FromInt32$/int32/g`,
		"-e", `s/\<libc\.UintptrFromInt32$/uintptr/g`,
		"-e", `s/\<libc\.BoolUint8$/uint8/g`,
		"-e", `s/(\*(\*func(\*libc\.TLS/(*(*func(*libc.TLS, *CC/g`,

		"-e", `s/libc.Xcalloc(tls,/calloc(tls, cc,/g`,
		"-e", `s/libc.Xexit(tls,/exit(tls, cc,/g`,
		"-e", `s/libc.Xfclose(tls,/fclose(tls, cc,/g`,
		"-e", `s/libc.Xfopen(tls,/fopen(tls, cc,/g`,
		"-e", `s/libc.Xfprintf(tls,/fprintf(tls, cc,/g`,
		"-e", `s/libc.Xfree(tls,/free(tls, cc,/g`,
		"-e", `s/libc.Xfreopen(tls,/freopen(tls, cc,/g`,
		"-e", `s/libc.Xmalloc(tls,/malloc(tls, cc,/g`,
		"-e", `s/libc.Xprintf(tls,/printf(tls, cc,/g`,
		"-e", `s/libc.Xrealloc(tls,/realloc(tls, cc,/g`,
		"-e", `s/libc.Xvfprintf(tls,/vfprintf(tls, cc,/g`,
		destFn); err != nil {
		fail(1, "%v: err=%v", gsed, err)
	}
}

var (
	re  = regexp.MustCompile(`\b[sx]__[a-zA-Z0-9_]+\b`)
	re2 = regexp.MustCompile(`\b[sx]__[a-zA-Z0-9_]+\b\(tls`)
	re3 = regexp.MustCompile(`}\)\)\)\(tls\b`)
	// qsortCbRE matches a libc.Xqsort call whose comparator is registered inline
	// as __ccgo_fp(F), capturing F. Used to trampoline C-ABI qsort comparators
	// (see main2lib). [^\n]* keeps the match on one line; the anchoring on
	// __ccgo_fp( makes it capture the comparator, not any s__ identifier that
	// appears earlier in the Xqsort arguments (e.g. cc.s__globalBytecodes).
	qsortCbRE = regexp.MustCompile(`libc\.Xqsort\([^\n]*__ccgo_fp\(([sx]__[A-Za-z0-9_]+)\)\)`)
)

func rename(s string, funcs map[string]struct{}) string {
	s = re.ReplaceAllStringFunc(s, func(s string) string {
		if _, ok := funcs[s]; !ok {
			return "cc." + s
		}

		return s
	})
	s = re2.ReplaceAllStringFunc(s, func(s string) string {
		return s + ", cc"
	})
	return re3.ReplaceAllStringFunc(s, func(s string) string {
		return s + ", cc"
	})
}

func primaryExprType(n *gc.PrimaryExprNode) string {
	if n.Postfix != nil {
		return n.PrimaryExpr.Source(true)
	}

	switch x := n.PrimaryExpr.(type) {
	default:
		fail(1, "%T(B) %v: %s", x, x.Position(), x.Source(false))
	}

	panic("unrechable")
}

func exprType(n gc.Expression) string {
	for {
		switch x := n.(type) {
		case *gc.BinaryExpressionNode:
			n = x.LHS
		case *gc.PrimaryExprNode:
			if x.Postfix != nil {
				return x.PrimaryExpr.Source(true)
			}

			fail(1, "%T(D) %v: %s", x, x.Position(), x.Source(false))
		case *gc.OperandNameNode:
			switch s := strings.TrimSpace(x.Source(false)); s {
			case "__ccgo_ts":
				return "uintptr"
			default:
				fail(1, "%T(E) %v: %s %q", x, x.Position(), x.Source(false), s)
			}
		case *gc.UnaryExprNode:
			n = x.UnaryExpr
		default:
			fail(1, "%T(C) %v: %s", x, x.Position(), x.Source(false))
		}
	}
}
