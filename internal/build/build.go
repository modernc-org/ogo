// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package build wires the OctoGo compile-and-load pipeline behind the `ogo
// build` and `ogo run` subcommands: check + emit C (internal/octogo), compile
// to a P2 binary (internal/flexcc), and — for run — load it onto a connected
// board (internal/loadp2). It is the walking skeleton of the toolchain's back
// half; the emitter it drives currently handles only a trivial program.
package build

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"modernc.org/ogo/internal/flexcc"
	"modernc.org/ogo/internal/loadp2"
	"modernc.org/ogo/internal/octogo"
)

// Build implements `ogo build <file.ogo>`: it produces a P2 <file>.binary (or
// the -o target). It returns a process exit code and, for control-flow errors,
// a Go error; tool diagnostics (checker, flexcc) go to stderr.
func Build(args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	_, code, err := compile(args, stdout, stderr)
	return code, err
}

// Run implements `ogo run <file.ogo>`: build, then load and run on a connected
// P2 board with an interactive terminal. It loads with a precise crystal clock
// (loadp2 -f, 200 MHz) and a matching 230400 read baud so the program's serial
// output is readable out of the box — see internal/loadp2.DefaultClockHz for why
// the clock, not just the baud, is the load-bearing setting. A board with a
// non-standard crystal, or a program that reconfigures its serial, needs the raw
// `ogo loadp2` passthrough with explicit -f/-b instead.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	bin, code, err := compile(args, stdout, stderr)
	if err != nil || code != 0 {
		return code, err
	}

	if bin == "" { // a library: checked, but there is nothing to run
		return 2, fmt.Errorf("run: no func main to run")
	}

	return loadp2.Load(loadp2.Options{Binary: bin, Terminal: true}), nil
}

// compile checks and emits C for the package named by args, then compiles it to a
// P2 binary with the embedded flexcc, returning the binary's absolute path. The
// package's files become one C translation unit, so a multi-file package is a
// single flexcc invocation.
func compile(args []string, stdout, stderr io.Writer) (binary string, code int, err error) {
	srcs, flags, err := parseArgs(args)
	if err != nil {
		return "", 2, err
	}
	out := flags.out
	dir, files, defaultOut, err := resolvePackage(srcs)
	if err != nil {
		return "", 2, err
	}

	// An ogo.mod above the package makes its directory part of a module: import
	// paths are then read against the module's root and carry its path, so a
	// package is named the same by every file that imports it. Without one the
	// package's own directory is the root, as it always was.
	fsys, rel, modulePath, err := moduleContext(dir)
	if err != nil {
		return "", 2, err
	}

	pkg, err := octogo.BuildModule(-1, modulePath, rel, files, fsys)
	if err != nil {
		return "", 1, err // checker diagnostics
	}

	// A package that declares no func main is a LIBRARY. OctoGo has no package
	// clause, so that is the whole of what tells the two apart, and there is no
	// intent to contradict: a directory either has a main or it has not.
	//
	// It is still emitted. The C is where the lifetime and escape refusals are
	// made, and a library checked without them would be checked far less than the
	// same code is when a program is built from it. What it does not get is
	// flexcc: a translation unit with no main does not link, and the diagnostic
	// for that came from the C compiler, about a C program the user never wrote.
	library := !octogo.HasMain(pkg)
	if library && out != "" {
		return "", 2, fmt.Errorf("build: -o %s: %s declares no func main, so there is no binary to write", out, dir)
	}

	// Runtime bounds / divide-by-zero checks are on by default (a debug build):
	// --unchecked omits them, --release reboots on a panic instead of halting.
	var emitOpts []octogo.EmitOption
	if !flags.unchecked {
		emitOpts = append(emitOpts, octogo.Checked())
	}
	if flags.release {
		emitOpts = append(emitOpts, octogo.Release())
	}
	clockOpts, err := clockOption(flags.clock, flags.xtal)
	if err != nil {
		return "", 2, err
	}
	emitOpts = append(emitOpts, clockOpts...)
	if flags.goStack != 0 {
		emitOpts = append(emitOpts, octogo.GoStack(flags.goStack))
	}
	var cbuf bytes.Buffer
	if err := octogo.EmitC(pkg, &cbuf, emitOpts...); err != nil {
		return "", 1, err
	}

	if library {
		fmt.Fprintf(stdout, "ok  \t%s\t[no func main, checked only]\n", dir)
		return "", 0, nil
	}

	// flexcc reads its input from disk, so stage the emitted C in a temp dir.
	tmp, err := os.MkdirTemp("", "ogo-build-*")
	if err != nil {
		return "", 1, err
	}
	defer os.RemoveAll(tmp)
	cFile := filepath.Join(tmp, strings.TrimSuffix(filepath.Base(defaultOut), ".binary")+".c")
	if err := os.WriteFile(cFile, cbuf.Bytes(), 0o644); err != nil {
		return "", 1, err
	}

	if out == "" {
		out = defaultOut
	}
	if out, err = filepath.Abs(out); err != nil {
		return "", 1, err
	}

	if code, err := compileC(cFile, out, stdout, stderr); err != nil {
		return "", code, err
	}
	return out, 0, nil
}

// buildFlags is what a build command line says, beyond the sources themselves.
type buildFlags struct {
	out       string
	release   bool
	unchecked bool
	// clock is the system clock the program asks for, in Hz, and xtal the crystal
	// it is made from. Zero clock leaves it to the backend, which falls back to
	// 160 MHz -- a 20 MHz crystal times eight, and a round multiplier rather than
	// any limit of the part.
	clock int
	xtal  int
	// goStack is the longs of stack each goroutine slot carries, 0 for the default.
	// A goroutine that outruns its slot panics rather than corrupting the pool, so
	// this is the knob that diagnostic points at.
	goStack int
}

// parseHz reads a flag's frequency argument, advancing i past it. It takes plain Hz
// and the two suffixes a clock is usually spoken in, so "--clock 200MHz" works as
// well as the nine digits -- which are easy to write with the wrong number of zeros,
// and where a stray one is a board running ten times too slow rather than an error.
func parseHz(cmd, name string, args []string, i *int) (int, error) {
	*i++
	if *i >= len(args) {
		return 0, fmt.Errorf("%s: %s requires a frequency", cmd, name)
	}
	s := args[*i]
	scale := 1
	switch {
	case strings.HasSuffix(strings.ToLower(s), "mhz"):
		scale, s = 1000000, s[:len(s)-3]
	case strings.HasSuffix(strings.ToLower(s), "khz"):
		scale, s = 1000, s[:len(s)-3]
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%s: %s wants a frequency, got %q", cmd, name, args[*i])
	}
	return n * scale, nil
}

// clockOption turns a wanted clock and crystal into the emit option that asks for
// it, or nothing at all when no clock was asked for -- which leaves the choice to
// the backend, and its fallback is 160 MHz.
func clockOption(clock, xtal int) ([]octogo.EmitOption, error) {
	if clock == 0 {
		return nil, nil
	}
	// Refused rather than rounded to the nearest the crystal can make: every wait,
	// baud rate and sample period is scaled by this number, so a program running one
	// percent fast would report nothing at all.
	setting, err := octogo.ClockFor(xtal, clock)
	if err != nil {
		return nil, err
	}
	return []octogo.EmitOption{octogo.Clock(setting)}, nil
}

// parseArgs pulls the positional source arguments and the flags from args.
func parseArgs(args []string) (srcs []string, f buildFlags, err error) {
	hz := func(name string, i *int) (int, error) { return parseHz("build", name, args, i) }
	f.xtal = octogo.DefaultXtal
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-o":
			i++
			if i >= len(args) {
				return nil, buildFlags{}, fmt.Errorf("build: -o requires an argument")
			}
			f.out = args[i]
		case a == "--release" || a == "-release":
			f.release = true
		case a == "--unchecked" || a == "-unchecked":
			f.unchecked = true
		case a == "--clock" || a == "-clock":
			if f.clock, err = hz(a, &i); err != nil {
				return nil, buildFlags{}, err
			}
		case a == "--gostack" || a == "-gostack":
			i++
			if i >= len(args) {
				return nil, buildFlags{}, fmt.Errorf("build: %s requires a number of longs", a)
			}
			n, cerr := strconv.Atoi(strings.TrimSpace(args[i]))
			if cerr != nil {
				return nil, buildFlags{}, fmt.Errorf("build: %s wants a number of longs, got %q", a, args[i])
			}
			lo, hi, _ := octogo.GoStackRange()
			if n < lo || n > hi {
				return nil, buildFlags{}, fmt.Errorf("build: %s must be between %d and %d longs, got %d", a, lo, hi, n)
			}
			f.goStack = n
		case a == "--xtal" || a == "-xtal":
			if f.xtal, err = hz(a, &i); err != nil {
				return nil, buildFlags{}, err
			}
		case strings.HasPrefix(a, "-"):
			return nil, buildFlags{}, fmt.Errorf("build: unknown flag %q", a)
		default:
			srcs = append(srcs, a)
		}
	}
	return srcs, f, nil
}

// resolvePackage turns the positional arguments into the directory holding the
// package, the base names of its sources, and the default output path.
//
// A package is a directory, so no argument means the current directory and a
// single directory argument means that directory -- in both cases every .ogo file
// in it is compiled together. Anything else is an explicit list of source files,
// which must all live in one directory for the same reason.
//
// The binary is named after the package directory and written beside it, except
// for the single-named-file form, which keeps the file's own name (x.ogo ->
// x.binary).
func resolvePackage(srcs []string) (dir string, files []string, out string, err error) {
	switch {
	case len(srcs) == 0:
		dir = "."
	case len(srcs) == 1 && isDir(srcs[0]):
		dir = srcs[0]
	default:
		for _, src := range srcs {
			// Whatever is not a directory is taken for a source file, so a
			// mistyped path arrives here as one and used to be reported by
			// whoever failed to open it: `open sensr: no such file or
			// directory', naming the base and not the path that was typed.
			switch _, err := os.Stat(src); {
			case err != nil:
				return "", nil, "", fmt.Errorf("build: %s: no such file or directory", src)
			case filepath.Ext(src) != ".ogo":
				return "", nil, "", fmt.Errorf("build: %s: named source files must be .ogo files", src)
			}

			switch d := filepath.Dir(src); {
			case dir == "":
				dir = d
			case d != dir:
				return "", nil, "", fmt.Errorf("build: all source files must be in one directory, got %s and %s", dir, d)
			}
			files = append(files, filepath.Base(src))
		}
		if len(srcs) == 1 {
			return dir, files, strings.TrimSuffix(srcs[0], ".ogo") + ".binary", nil
		}
		return dir, files, filepath.Join(dir, dirPkgName(dir)+".binary"), nil
	}
	if files, err = packageFiles(dir); err != nil {
		return "", nil, "", err
	}
	return dir, files, filepath.Join(dir, dirPkgName(dir)+".binary"), nil
}

// packageFiles lists a directory's .ogo sources, in the stable order os.ReadDir
// gives. Files ending in _test.ogo are test files and are not part of a build.
func packageFiles(dir string) (r []string, err error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("build: %v", err)
	}
	for _, de := range des {
		switch nm := de.Name(); {
		case de.IsDir(), !strings.HasSuffix(nm, ".ogo"), strings.HasSuffix(nm, "_test.ogo"):
			// not a source file of this package
		default:
			r = append(r, nm)
		}
	}
	if len(r) == 0 {
		return nil, fmt.Errorf("build: no .ogo source files in %s", dir)
	}
	return r, nil
}

// dirPkgName is the name a directory's binary takes: the directory's own name,
// resolved so that "." and ".." become the real name rather than a dot.
func dirPkgName(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Base(abs)
	}
	return filepath.Base(dir)
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// compileC compiles one emitted translation unit to a P2 binary with the embedded
// flexcc. It is the single place the backend's flags live, shared by `ogo build`
// and `ogo test`.
// flexcc.Main auto-injects the embedded flexprop P2 include tree.
//
// Builds used to pass --fcache=0, on the belief that FCACHE miscompiled the
// channel rendezvous. It did not: the rendezvous polled by calling _locktry
// every turn, which re-takes the lock too quickly for the cog on the other
// side to ever win it. FCACHE only made the loop fast enough to cross that
// threshold, so disabling it hid a livelock that was ours. The poll now reads
// the flag before asking for the lock (see chanRuntimeDefs), and the whole
// on-board suite passes with FCACHE on, so the flag is gone and loop caching
// is back for every program, not just the ones with channels.
//
// No optimizer pass is turned off. Two were, from v0.14.0 to the backend
// regeneration of 2026-08-29 -- `-Ono-inline-small -Ono-peephole` -- and the
// record of why, what each cost and how they were let go is kept below, because
// it is the shape a future workaround would take and the measurements are what
// decided it.
//
// The two passes carried two defects, both reduced to a dozen lines of C in doc/
// and both reported upstream (flexprop issues 103 and 104, fixed there on
// 2026-08-03): the first was an optimization moving an instruction between a
// qmul and its getqx that the qmul indirectly depended on, the second was
// dead-code elimination removing labels that were still branched to.
//
//	inline-small  the optimizer stored a value the program never computed into
//	              a file-scope int (doc/optimizer-miscompile.c). SILENT: gcc was
//	              right, the build said nothing, and a plain integer came out
//	              wrong.
//	peephole      the optimizer emitted a branch to a label it then did not
//	              define, and the assembler refused the program
//	              (doc/optimizer-dangling-label.c). Loud, at least.
//
// EACH FLAG FIXED EXACTLY ONE OF THE TWO, and neither one covered both, so both
// were required. Measured on a P2-EDGE, every cell of the matrix:
//
//	                     -2 plain      -Ono-peephole   -Ono-inline-small
//	dangling label       refused       BUILT           refused
//	silent miscompile    -202817768    -202817768      0
//
// This corrected what stood here before, which read "each defect needs both
// passes' cooperation, so turning either one off is enough". That was false, and
// false in the expensive direction: -Ono-inline-small was where nearly all the
// cost was (below), so the note invited dropping precisely the flag that was the
// only thing standing between a build and a silently wrong integer. It was never
// measured, only inferred from the two upstream reports.
//
// The cost, also measured rather than assumed. Size, over the whole pair: 0 to
// 15%, depending on the program -- 13360 -> 13232 bytes on the framing-receiver
// test case, 10292 -> 11792 on a fuzzer-generated one.
//
// SPEED was the number that was missing, and it was much larger. On a tight
// real-time loop -- a DDS phase accumulator plus two smart-pin DAC writes, the
// shape a motor controller runs -- cycles per iteration on hardware:
//
//	-2 plain                              205
//	-2 -Ono-peephole                      221   +8%
//	-2 -Ono-inline-small                  393   +92%
//	-2 with both, i.e. what shipped       384   +87%
//
// So the pair could cost 87% of a hot loop's time, and -Ono-inline-small was
// essentially all of it. That was the real price of the workaround, and it was
// the argument for regenerating the backend rather than a preference.
//
// The tax was SHAPE-DEPENDENT, not a uniform 87%. Two loops of opposite shape,
// cycles per iteration, same board:
//
//	                          call-heavy   register-heavy
//	-2 plain                      41            64
//	-2 with both, i.e. shipped   100            64
//	-2 -Ono-regs                 133           120
//
// Losing the inliner cost 2.4x where a small function was called every
// iteration and NOTHING at all where none was. Code that does its work in
// straight-line arithmetic paid nothing for this workaround.
//
// -Ono-regs covered both defects BY ITSELF -- verified against both reproducers
// -- and was still rejected. It costs 68% more code (and 32% on the DDS program),
// and on speed it was worse than the pair on both shapes above, badly so on the
// register-heavy one, which is what turning the register allocator off would
// predict. It won only on the DDS loop (286 vs 384), and one loop is not a
// default.
//
// HOW THEY WERE LET GO. The check was one reproducer per flag, since each flag
// answered for its own defect and for no other: doc/optimizer-miscompile.c had to
// print 0 without -Ono-inline-small, and doc/optimizer-dangling-label.c had to
// assemble without -Ono-peephole. Both were measured on a P2-EDGE against the
// regenerated backend (spin2cpp 2bd01c4c, see internal/generator.go) at a plain
// `-2`, and both held: 0, and an assembled program that runs identically to the
// flagged build. The same regeneration cleared what no flag had covered -- the
// unwritten-element multiply of flexprop#105, the constant divide of
// doc/const-divide-miscompile.c, and the compound assignment of flexprop#106 --
// so the whole battery in doc/ was re-run on hardware, pinned against
// regenerated, before the flags went: those five changed and nothing else did.
//
// If a pass has to go again, this is the shape: a reproducer in doc/ that names
// the flag, the matrix measured on hardware rather than inferred, and the cost in
// cycles on a loop of each shape, not just in bytes.
func compileC(cFile, out string, stdout, stderr io.Writer) (int, error) {
	if err := flexcc.Main(nil, stdout, stderr, []string{"-2", "-o", out, cFile}); err != nil {
		return 1, fmt.Errorf("flexcc: %v", err)
	}
	return 0, nil
}
