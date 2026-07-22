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
	undup "modernc.org/undup/lib"
)

const (
	cloneDir    = "flexprop"
	flexpropURL = "https://github.com/totalspectrum/flexprop.git"
	// flexpropRef pins the flexprop source so backend regeneration is
	// reproducible. v7.7.0 (released 2026-07-17) is the latest flexprop release as
	// of 2026-07-20; upstream cuts releases roughly every 1-2 months.
	//
	// The committed ccgo_<goos>_<goarch>.go was regenerated against this pin on
	// 2026-07-20 with ccgo v4.34.6 (flexprop repo and spin2cpp submodule both at
	// v7.7.0); mcpp_main.c.diff applied cleanly. To adopt a new flexpropRef: bump it,
	// `rm -rf flexprop flexprop_install`, rerun `go generate`, then update the flexcc
	// --help golden in internal/flexcc/all_test.go.
	//
	// Two backends are generated from this pin: linux/amd64 natively (the default)
	// and windows/amd64 cross-compiled on a linux/amd64 host with MinGW
	// (TARGET_GOOS=windows TARGET_GOARCH=amd64 go run generator.go; needs
	// x86_64-w64-mingw32-gcc and the ccgo CLI on PATH). See transpileWindows.
	flexpropRef = "v7.7.0"
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
	case "linux/amd64", "windows/amd64":
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
	if err := os.MkdirAll(flexccDir, 0755); err != nil {
		fail(1, "os.MkdirAll(%q): err=%v", flexccDir, err)
	}

	// Regeneration is wrapped in an undup expand/dedup so the committed backends stay
	// folded with none of the manual steps done in the wrong order: reconstruct the
	// full per-target files from the prior fold, (re)generate this target over its
	// file, gofmt so shared decls are byte-canonical across targets, then re-fold.
	undupExpand(flexccDir)

	// The linux/amd64 backend is transpiled natively (ccgo -exec make);
	// windows/amd64 is cross-compiled on this linux host with MinGW. Both hand off
	// the emitted package-main Go to main2lib, which threads a *CC state struct and
	// re-homes it into package flexcc.
	var flexccGoSrc string
	switch goos {
	case "windows":
		flexccGoSrc = transpileWindows(wd)
	default:
		flexccGoSrc = transpileLinux(wd, flexccDir)
	}
	flexccGoDest := filepath.Join(flexccDir, fmt.Sprintf("ccgo_%s_%s.go", goos, goarch))
	main2lib(flexccGoDest, flexccGoSrc)

	gofmtFlexcc(flexccDir)
	undupDedup(flexccDir)
}

// undupBase / undupPattern identify the flexcc backend group undup folds: the
// per-target ccgo_<goos>_<goarch>.go files and the shared ccgo.go they collapse
// into.
const (
	undupBase    = "ccgo"
	undupPattern = "ccgo_*.go"
)

// undupExpand reconstructs the full per-target ccgo_*.go files from a prior undup
// fold (the shared ccgo.go), removing that shared file, so the regen and the
// following undupDedup see full inputs -- undup.Dedup reads only the per-target
// files and would silently drop a fold left in place. It is a no-op when the
// backend is not folded (no ccgo.go), e.g. a first-ever generation.
func undupExpand(flexccDir string) {
	if _, err := os.Stat(filepath.Join(flexccDir, undupBase+".go")); err != nil {
		if os.IsNotExist(err) {
			return
		}
		fail(1, "stat %s.go: err=%v", undupBase, err)
	}
	r, err := undup.Expand(flexccDir, undupBase)
	if err != nil {
		fail(1, "undup expand: err=%v", err)
	}
	fmt.Printf("undup expand: %d targets reconstructed\n", r.Targets)
}

// undupDedup folds the byte-identical top-level decls shared by the per-target
// ccgo_*.go backends into the build-tagged shared ccgo.go, verifying each target
// reconstructs byte-for-byte before writing anything. Its inputs must be
// gofmt-canonical (see gofmtFlexcc) or the freshly generated target's decls will
// not match the expand-reconstructed ones and would not fold.
func undupDedup(flexccDir string) {
	r, err := undup.Dedup(flexccDir, undupBase, undupPattern)
	if err != nil {
		fail(1, "undup dedup: err=%v", err)
	}
	fmt.Printf("undup dedup: %d targets, %d decls, %.2fx (%d -> %d bytes)\n",
		r.Targets, r.TotalDecls, r.Ratio(), r.InBytes, r.OutBytes)
}

// gofmtFlexcc runs gofmt -s -w over the flexcc package. main2lib emits via sed,
// not gofmt, and undup keys decls on their exact bytes, so the per-target files
// must be canonicalized before the fold. Doing it here (rather than leaving it to
// generate.go's go:generate gofmt step, which runs too late -- after undupDedup)
// also removes the need to gofmt by hand after a manual windows regen.
func gofmtFlexcc(flexccDir string) {
	if err := shell("", "gofmt", "-s", "-w", flexccDir); err != nil {
		fail(1, "gofmt %s: err=%v", flexccDir, err)
	}
}

// transpileLinux transpiles flexcc for linux/amd64 the native way: `ccgo -exec
// make` stands in for the C compiler across every flexprop translation unit and
// the final link, emitting spin2cpp/build/flexcc.go. It also refreshes the
// embedded P2 include tree and flexprop license; those artifacts are
// target-independent but are produced here because this is the path that runs
// the flexprop `install`. Returns the emitted flexcc.go path for main2lib.
func transpileLinux(wd, flexccDir string) string {
	flexccGoSrc := filepath.Join(wd, cloneDir, "spin2cpp", "build", "flexcc.go")
	installDir := filepath.Join(wd, installDir)
	installDir2 := filepath.Join(installDir, "flexprop")
	os.RemoveAll(installDir)
	os.Remove(flexccGoSrc)
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

	// Bundle the installed flexprop P2 include/lib tree next to the transpiled
	// compiler so the in-repo flexcc is self-contained (see flexcc/p2include.go),
	// and carry flexprop's license for attribution.
	if err := writeP2Include(filepath.Join(installDir, "include"), filepath.Join(flexccDir, "p2include.tar.gz")); err != nil {
		fail(1, "writeP2Include: err=%v", err)
	}
	if err := copyFile(filepath.Join(wd, cloneDir, "License.txt"), filepath.Join(flexccDir, "LICENSE-flexprop")); err != nil {
		fail(1, "copy flexprop license: err=%v", err)
	}
	return flexccGoSrc
}

// transpileWindows cross-compiles flexcc for windows/amd64 on this linux/amd64
// host, mirroring modernc.org/loadp2's windows path. It runs in two phases: a
// native `make` (real gcc) to produce the generated C sources ccgo needs, then a
// single direct ccgo pass over the whole flexcc link unit with the MinGW
// toolchain. Returns the emitted flexcc.go path for main2lib.
//
// `-exec make` is deliberately not reused here: in exec mode ccgo wraps the
// compiler make invokes, but it can't retarget those wrapped compiles at MinGW,
// so it would preprocess the windows code with linux headers. The direct pass
// with `--cpp x86_64-w64-mingw32-gcc` is the only way to get the windows
// headers/predefines. The P2 include tree and license are target-independent, so
// this path leaves the committed copies (produced by transpileLinux) untouched.
func transpileWindows(wd string) string {
	// spin2cppDir is relative to the process working directory (internal/) so that
	// ccgo, run with that as its cwd (see shell), records clean relative source
	// paths in the generated file's header rather than absolute host paths.
	spin2cppDir := filepath.Join(cloneDir, "spin2cpp")

	// Phase 1: native build so the generated C sources exist before ccgo reads
	// them — the three bison grammars (build/*.tab.c) and the xxd'd sys/*.spin.h
	// that spinc.c and outasm.c #include. The native host binaries it also produces
	// are discarded; the point is those generated prerequisites. Build the default
	// `all` target (not `build/flexcc` directly), because only `all` carries the
	// $(BUILD) mkdir prerequisite that creates build/ before the -MMD compiles write
	// their .d files there. `clean` first so a prior linux `-exec make` run's build/
	// (ccgo .o.go files) can't confuse make's native compile.
	if err := shell(spin2cppDir, gmake, "clean"); err != nil {
		fail(1, "windows: make clean: err=%v", err)
	}
	if err := shell(spin2cppDir, gmake, "OPT=-O1"); err != nil {
		fail(1, "windows: native make: err=%v", err)
	}

	// Phase 2: cross-transpile just the flexcc link unit. -D_X86INTRIN_H_INCLUDED=1
	// keeps MinGW's <winnt.h> from pulling in <x86intrin.h> (the avx512bf16 __bf16
	// intrinsic modernc.org/cc can't parse); -ignore-link-errors leaves the windows
	// CRT/Win32 symbols modernc.org/libc lacks as bare package-level names for
	// flexcc/supplement_windows_amd64.go to define. The flag set otherwise matches
	// transpileLinux's so the two backends stay structurally comparable.
	const xgcc = "x86_64-w64-mingw32-gcc"
	args := []string{
		"--goos", "windows", "--goarch", "amd64",
		"--cpp", xgcc, "-map", "gcc=" + xgcc,

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
		"-DFLEXSPIN_BUILD",
		"-D_X86INTRIN_H_INCLUDED=1",
		"-O1",
		"-I.", "-I./backends", "-I./frontends", "-Ibuild",
		"-extended-errors",
		"-ignore-link-errors",
		"-ignore-unsupported-alignment",
		"-eval-all-macros",
		"-o", "build/flexcc_windows.go",
	}
	args = append(args, resolveFlexccSources(spin2cppDir)...)
	if err := shell(spin2cppDir, "ccgo", args...); err != nil {
		fail(1, "windows: ccgo cross-transpile: err=%v", err)
	}
	return filepath.Join(wd, cloneDir, "spin2cpp", "build", "flexcc_windows.go")
}

// flexccVPATH mirrors the spin2cpp Makefile's VPATH: the directories, in search
// order, where the flexcc translation units live. resolveFlexccSources searches
// it to turn the Makefile's bare source basenames into paths, exactly as make
// does. Order matters: dofmt.c exists in both util/ and include/libsys/, and
// util/ must win — include/libsys/ is P2 target code, not part of the compiler,
// and is not on the Makefile VPATH.
var flexccVPATH = []string{
	".", "util", "frontends", "frontends/basic", "frontends/spin", "frontends/c",
	"backends", "frontends/bf", "backends/asm", "backends/cpp", "backends/bytecode",
	"backends/dat", "backends/nucode", "backends/objfile", "backends/zip",
	"backends/compress", "backends/compress/lz4", "mcpp",
}

// flexccSources is the flexcc link unit — flexcc.c cmdline.c $(OBJS) — taken
// verbatim and in order from the spin2cpp Makefile (v7.7.0). The linux backend
// gets the identical set implicitly via `ccgo -exec make`; the windows cross pass
// invokes ccgo directly and so must name each source. If this drifts from a
// future flexprop the cross-transpile fails loudly with undefined symbols, which
// is the signal to re-sync it. The three bison outputs (build/*.tab.c) are
// appended by resolveFlexccSources since they live in build/, not on VPATH.
var flexccSources = []string{
	"flexcc.c", "cmdline.c",
	// SPINSRCS = common.c case.c spinc.c $(LEXSRCS) functions.c ... version.c becommon.c brkdebug.c printdebug.c
	"common.c", "case.c", "spinc.c",
	// LEXSRCS = lexer.c uni2sjis.c symbol.c ast.c expr.c $(UTIL) preprocess.c
	"lexer.c", "uni2sjis.c", "symbol.c", "ast.c", "expr.c",
	// UTIL
	"dofmt.c", "flexbuf.c", "lltoa_prec.c", "strupr.c", "strrev.c", "strdupcat.c",
	"to_utf8.c", "from_utf8.c", "sha256.c", "softcordic.c",
	"preprocess.c",
	"functions.c", "cse.c", "loops.c", "hloptimize.c", "hltransform.c", "types.c",
	"pasm.c", "outdat.c", "outlst.c", "outobj.c", "spinlang.c", "basiclang.c",
	"clang.c", "bflang.c",
	// PASMBACK
	"outasm.c", "assemble_ir.c", "optimize_ir.c", "asm_peep.c", "inlineasm.c", "compress_ir.c",
	// BCBACK
	"outbc.c", "bcbuffers.c", "bcir.c", "bc_spin1.c",
	// NUBACK
	"outnu.c", "nuir.c", "nupeep.c",
	// CPPBACK
	"outcpp.c", "cppfunc.c", "outgas.c", "cppexpr.c", "cppbuiltin.c",
	// COMPBACK
	"compress.c", "lz4.c", "lz4hc.c",
	// ZIPBACK
	"outzip.c", "zip.c",
	// MCPP
	"directive.c", "expand.c", "mbchar.c", "mcpp_eval.c", "mcpp_main.c", "mcpp_system.c", "mcpp_support.c",
	"version.c", "becommon.c", "brkdebug.c", "printdebug.c",
}

// resolveFlexccSources resolves every flexccSources basename against flexccVPATH
// (relative to spin2cppDir) and appends the three generated bison grammars from
// build/. The returned paths are relative to spin2cppDir, which is ccgo's working
// directory for the cross pass.
func resolveFlexccSources(spin2cppDir string) []string {
	r := make([]string, 0, len(flexccSources)+3)
	for _, base := range flexccSources {
		found := ""
		for _, d := range flexccVPATH {
			if _, err := os.Stat(filepath.Join(spin2cppDir, d, base)); err == nil {
				found = filepath.Join(d, base)
				break
			}
		}
		if found == "" {
			fail(1, "windows: flexcc source not found via VPATH: %s", base)
		}
		r = append(r, found)
	}
	return append(r, "build/spin.tab.c", "build/basic.tab.c", "build/cgram.tab.c")
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

	sedArgs := []string{"-i",
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
	}

	// The windows backend needs fixups the linux one does not, so they are gated on
	// the target rather than relying on the patterns being absent from linux.
	if goos == "windows" {
		sedArgs = append(sedArgs,
			// Codegen the Go compiler rejects (each a single occurrence): getcwd is
			// mapped to libc.Xgetcwd but with an int32 length that will not convert to
			// its Tsize_t parameter; and miniz's ~mask folds to the constant -4096,
			// which cannot convert to uint32 (complementing the uint32 is equivalent).
			"-e", `s/libc.Xgetcwd(tls, \([^,]*\), int32(260))/libc.Xgetcwd(tls, \1, libc.Tsize_t(260))/g`,
			"-e", `s/uint32(^int32(_TDEFL_MAX_PROBES_MASK))/(^uint32(_TDEFL_MAX_PROBES_MASK))/g`,

			// Redirect the libc functions that are panic(todo()) stubs for windows to
			// the hand-written implementations in supplement_windows_amd64.go. On linux
			// these are real libc entries and must not be rewritten. Xstat is itself
			// real but forwards to the Xstat64 stub, so it is redirected too.
			"-e", `s/libc.XGetModuleFileNameA(tls,/xGetModuleFileNameA(tls,/g`,
			"-e", `s/libc.Xtime(tls,/xTime(tls,/g`,
			"-e", `s/libc.Xungetc(tls,/xUngetc(tls,/g`,
			"-e", `s/libc.Xabort(tls)/xAbort(tls)/g`,
			"-e", `s/libc.Xstat(tls,/xStat(tls,/g`,
		)
	}

	sedArgs = append(sedArgs, destFn)
	if err := shell("", gsed, sedArgs...); err != nil {
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
