// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package build wires the OctoGo compile-and-load pipeline behind the `ogo
// build` and `ogo run` subcommands: check + emit C (internal/octogo), compile
// to a P2 binary (internal/flexcc), and — for run — load it onto a connected
// board (loadp2). It is the walking skeleton of the toolchain's back
// half; the emitter it drives currently handles only a trivial program.
package build

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"modernc.org/ogo/internal/flexcc"
	"modernc.org/ogo/internal/octogo"
	"modernc.org/ogo/loadp2"
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
// output is readable out of the box — see loadp2.DefaultClockHz for why
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
	// Each call through a function value spelled with its type, for the stack
	// reader to see a recursion through one (readStackNeeds); the binary is the one
	// built without.
	emitOpts = append(emitOpts, octogo.FuncValueClasses())
	// What the program calls its functions, for the report and for a refusal of
	// compileSized's to say in the program's words.
	var rep octogo.Report
	emitOpts = append(emitOpts, octogo.WithReport(&rep))
	// The small functions are marked for the backend to inline, unless
	// --no-inline asks for the program without.
	markedOpts := emitOpts
	if !flags.noInline {
		markedOpts = append(emitOpts[:len(emitOpts):len(emitOpts)], octogo.Inline())
	}
	var cbuf bytes.Buffer
	if err := octogo.EmitC(pkg, &cbuf, markedOpts...); err != nil {
		return "", 1, err
	}
	// The same program plainer, should this one outgrow the cog (compileMarked):
	// its switches chains of compares, and then no function marked either. The
	// package is made again for each: what emitting it left in it is the first
	// emission's.
	emitAgain := func(opts ...octogo.EmitOption) func() ([]byte, error) {
		return func() ([]byte, error) {
			pkg, err := octogo.BuildModule(-1, modulePath, rel, files, fsys)
			if err != nil {
				return nil, err
			}
			var c bytes.Buffer
			if err := octogo.EmitC(pkg, &c, opts...); err != nil {
				return nil, err
			}
			return c.Bytes(), nil
		}
	}
	plainer := []func() ([]byte, error){
		emitAgain(append(markedOpts[:len(markedOpts):len(markedOpts)], octogo.SwitchChains())...),
		emitAgain(append(emitOpts[:len(emitOpts):len(emitOpts)], octogo.SwitchChains())...),
	}
	variants := func(extra ...octogo.EmitOption) ([]byte, []func() ([]byte, error), error) {
		with := func(opts []octogo.EmitOption, more ...octogo.EmitOption) []octogo.EmitOption {
			return append(append(slices.Clone(opts), more...), extra...)
		}
		c, err := emitAgain(with(markedOpts)...)()
		if err != nil {
			return nil, nil, err
		}
		return c, []func() ([]byte, error){
			emitAgain(with(markedOpts, octogo.SwitchChains())...),
			emitAgain(with(emitOpts, octogo.SwitchChains())...),
		}, nil
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

	if out == "" {
		out = defaultOut
	}
	if out, err = filepath.Abs(out); err != nil {
		return "", 1, err
	}

	root, err := buildRoot(dir)
	if err != nil {
		return "", 2, err
	}
	keepC := strings.TrimSuffix(out, filepath.Ext(out)) + ".c"
	if code, err := compileSized(cbuf.Bytes(), plainer, variants, rep.Funcs, cFile, out, root, keepC, flags.allowWarnings, flags.goStack != 0, stdout, stderr); err != nil {
		return "", code, err
	}
	if flags.report {
		if err := report(stdout, &rep, cFile, out, root, flags.goStack != 0); err != nil {
			return "", 1, err
		}
	}
	return out, 0, nil
}

// emitVariants emits a program again with options added to each way it is emitted:
// its C, and the plainer ways compileMarked may compile it instead.
type emitVariants func(extra ...octogo.EmitOption) ([]byte, []func() ([]byte, error), error)

// stackCheckRounds is how many times a program is compiled again for its stack
// checks to cover what its listing says: one to put them in, one to write their
// margins, and the rest for a backend that lays a function out differently once.
const stackCheckRounds = 4

// compileSized is compileMarked, and then the program's stacks held to what its
// listing says they need (readStackNeeds). Unless --gostack asked for a size, the
// same C is compiled again with each slot of the goroutine pool holding what its
// goroutines need (octogo.GoSlots): every slot of the shared pool what the deepest
// goroutine needs, and a slot a go statement has to itself what that goroutine
// needs. An explicit --gostack is the program's to choose, and a goroutine that
// recurses, whose need the listing cannot bound, is refused without one. And a
// program whose main cog's deepest stack does not fit in Hub RAM beside its code
// and data is told so, the binary removed: the stack grows into whatever lies past
// the top, without a word. A recursion where it may run -- on main's cog, and on a
// goroutine whose slot --gostack sized -- is checked at the entry of every function
// on it: variants emits the program again with the checks (octogo.StackChecks), and
// it is compiled until each check covers what the listing says it must
// (octogo.WithStackMargins), so a recursion panics before it writes past the end of
// its stack. Without variants nothing is checked. A refusal names a function as
// names, the program's names of the C's functions (octogo.Report.Funcs), says.
func compileSized(c []byte, plainer []func() ([]byte, error), variants emitVariants, names map[string]string, cFile, out, inc, keepC string, allowWarnings, stackAsked bool, stdout, stderr io.Writer) (int, error) {
	if code, err := compileMarked(c, plainer, cFile, out, inc, keepC, allowWarnings, stdout, stderr); err != nil {
		return code, err
	}
	built, err := os.ReadFile(cFile)
	if err != nil {
		return 1, err
	}
	listing, err := os.ReadFile(strings.TrimSuffix(out, filepath.Ext(out)) + ".p2asm")
	if err != nil {
		return 0, nil // nothing to read the stacks off: the build stands as it is
	}
	needs := readStackNeeds(listing, built)
	if !stackAsked && !needs.goKnown {
		// A recursion has no depth the listing can bound, and the default slot
		// was a guess the fence checked only as the goroutine ended -- after it
		// had written past its slot. --gostack makes the size the program's.
		os.Remove(out)
		through := ""
		if needs.goRecurse != "" {
			through = " through " + goFuncName(needs.goRecurse, names)
		}
		return 1, fmt.Errorf("ogo: a goroutine recurses%s, so its stack has no bound the backend's listing can read; give the goroutine stacks a size with --gostack longs", through)
	}
	toCheck := func(n stackNeeds) []string {
		if stackAsked {
			return union(n.recMain, n.recGo)
		}
		return n.recMain
	}
	if fns := toCheck(needs); variants != nil && len(fns) != 0 {
		perCog := stackAsked && len(needs.recGo) != 0
		var margins map[string]int
		reserve := 0
		for round := 0; ; round++ {
			checked, checkedPlainer, err := variants(octogo.StackChecks(fns, perCog))
			if err != nil {
				return 1, err
			}
			if margins != nil {
				checked, checkedPlainer = octogo.WithStackMargins(checked, margins, reserve), withStackMargins(checkedPlainer, margins, reserve)
			}
			if code, err := compileMarked(checked, checkedPlainer, cFile, out, inc, keepC, allowWarnings, stdout, stderr); err != nil {
				return code, err
			}
			if built, err = os.ReadFile(cFile); err != nil {
				return 1, err
			}
			if listing, err = os.ReadFile(strings.TrimSuffix(out, filepath.Ext(out)) + ".p2asm"); err != nil {
				return 1, err
			}
			needs, plainer = readStackNeeds(listing, built), checkedPlainer
			written, wroteReserve := octogo.StackMargins(built)
			covered := needs.reserve <= wroteReserve
			for fn, m := range needs.margins {
				covered = covered && m <= written[fn]
			}
			grown := union(fns, toCheck(needs))
			if covered && len(grown) == len(fns) {
				break // a recursion no check can be put in stays as it was
			}
			if round == stackCheckRounds {
				os.Remove(out)
				return 1, fmt.Errorf("ogo: internal error: the stack checks of %s do not cover what the backend's listing says after %d compiles", strings.Join(fns, ", "), round+2)
			}
			fns, margins, reserve = grown, written, max(wroteReserve, needs.reserve)
			for fn, m := range needs.margins {
				margins[fn] = max(margins[fn], m)
			}
		}
	}
	if slots := octogo.GoSlots(built); !stackAsked && len(slots) != 0 {
		_, hi, _ := octogo.GoStackRange()
		longs := make([]int, len(slots))
		changed := false
		for i, s := range slots {
			longs[i] = s.Longs // a slot none of whose goroutines the listing has keeps its size
			tramps := s.Tramps
			if tramps == nil {
				tramps = slices.Sorted(maps.Keys(needs.goTramps)) // the shared pool's slots are every goroutine's
			}
			deep, who := 0, ""
			for _, t := range tramps {
				if g, ok := needs.goTramps[t]; ok && g.longs > deep {
					deep, who = g.longs, g.name
				}
			}
			if deep == 0 {
				continue
			}
			if deep > hi {
				// Capped at the largest slot, the goroutine ran on past it, over
				// the slots after it and whatever followed the pool, in silence
				// until it ended: a mutant's `var seen [65535]uint32` printed
				// nothing on the board. --gostack takes it as asked, unmeasured.
				os.Remove(out)
				switch n := goFuncName(who, names); {
				case who == "":
					who = "a goroutine"
				case strings.HasPrefix(n, "the function literal"):
					who = "the goroutine of " + n
				default:
					who = "goroutine " + n
				}
				return 1, fmt.Errorf("ogo: %s needs a stack of %d longs by the backend's listing, and a goroutine's slot holds %d at most", who, deep, hi)
			}
			longs[i] = (deep + 15) &^ 15
			changed = changed || longs[i] != s.Longs
		}
		if changed {
			restacked := make([]func() ([]byte, error), len(plainer))
			for i, f := range plainer {
				restacked[i] = func() ([]byte, error) {
					p, err := f()
					if err != nil {
						return nil, err
					}
					return octogo.WithGoSlots(p, longs), nil
				}
			}
			if code, err := compileMarked(octogo.WithGoSlots(built, longs), restacked, cFile, out, inc, keepC, allowWarnings, stdout, stderr); err != nil {
				return code, err
			}
		}
	}
	mainNeed, atLeast := needs.mainNeed, ""
	switch {
	case mainNeed >= 0:
	case needs.mainCut >= 0 && len(needs.margins) != 0:
		// Every recursion main reaches is checked: what it needs is the way to the
		// checks and what the panic of one takes.
		mainNeed = needs.mainCut + needs.reserve
	default:
		// Recursion leaves the need unbounded; what it needs at the least still
		// fits or does not.
		mainNeed, atLeast = needs.mainAtLeast, " at least"
	}
	if mainNeed > 0 {
		if fi, err := os.Stat(out); err == nil && fi.Size()+int64(mainNeed) > hubRAMBytes {
			os.Remove(out)
			return 1, fmt.Errorf("ogo: the program does not fit the P2's 512 KB of Hub RAM: main's stack needs%s %d bytes beside its %d of code and data", atLeast, mainNeed, fi.Size())
		}
	}
	return 0, nil
}

// goFuncName is what the program calls the function the C names cn, by names
// (octogo.Report.Funcs); cn where names does not say.
func goFuncName(cn string, names map[string]string) string {
	if n, ok := names[cn]; ok {
		return n
	}
	return cn
}

// withStackMargins is plainer with the margins and the reserve of its stack checks
// set (octogo.WithStackMargins).
func withStackMargins(plainer []func() ([]byte, error), margins map[string]int, reserve int) []func() ([]byte, error) {
	set := make([]func() ([]byte, error), len(plainer))
	for i, f := range plainer {
		set[i] = func() ([]byte, error) {
			p, err := f()
			if err != nil {
				return nil, err
			}
			return octogo.WithStackMargins(p, margins, reserve), nil
		}
	}
	return set
}

// union is the names in a or b, sorted.
func union(a, b []string) []string {
	u := slices.Clone(a)
	for _, s := range b {
		if !slices.Contains(u, s) {
			u = append(u, s)
		}
	}
	slices.Sort(u)
	return u
}

// compileMarked writes the C of a program to cFile and compiles it into out. Where
// the program outgrows the cog's registers, it compiles the programs plainer
// answers instead, in turn, each the same program with less of it made faster: its
// switches chains of compares, and then none of its functions marked for the
// backend to inline either. Every copy of an inlined function brings the registers
// of its locals to where it lands, the backend gives registers back between a C
// switch's arms in some functions and fewer in others, a cog has 480 for
// everything, and a program that fits a plainer way is not to fail for having been
// made faster. A plainer program whose C is the C tried already is not compiled
// again. Nothing is said of an attempt that outgrew the cog; a program that fits no
// way is told what the last attempt said, and which of its functions hold the
// registers (cogHint).
func compileMarked(c []byte, plainer []func() ([]byte, error), cFile, out, inc, keepC string, allowWarnings bool, stdout, stderr io.Writer) (int, error) {
	listing := strings.TrimSuffix(out, filepath.Ext(out)) + ".p2asm"
	for {
		if err := os.WriteFile(cFile, c, 0o644); err != nil {
			return 1, err
		}
		start := time.Now()
		var said, saidErr bytes.Buffer
		code, err := compileC(cFile, out, inc, &said, &saidErr)
		all := append(said.Bytes(), saidErr.Bytes()...)
		if err != nil && outgrewCog(all) {
			next, nerr := plainerC(c, &plainer)
			if nerr != nil {
				return 1, nerr
			}
			if next != nil {
				c = next
				continue
			}
		}
		stdout.Write(said.Bytes())
		stderr.Write(saidErr.Bytes())
		switch {
		case err != nil && outgrewCog(all):
			fmt.Fprintln(stderr, cogHint(listing, start, programFuncs(c)))
			return code, err
		case err != nil:
			return backendRefusal(c, keepC, all, code, err)
		case !allowWarnings:
			return backendFault(c, out, keepC, all)
		}
		return code, err
	}
}

// plainerC answers the first program of plainer whose C is not c, taking the ones
// it tried off the list, or nil where none is left.
func plainerC(c []byte, plainer *[]func() ([]byte, error)) ([]byte, error) {
	for len(*plainer) != 0 {
		next := (*plainer)[0]
		*plainer = (*plainer)[1:]
		p, err := next()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(p, c) {
			return p, nil
		}
	}
	return nil, nil
}

// backendRefusal is backendFault for a build the backend REFUSED: the C ogo wrote
// is the compiler's own, so a C compiler refusing it -- "Cannot handle expression
// yet" about a pair of shifts, a crash -- is a fault of ogo's or of the backend it
// builds on, never of the program. The C was written to a directory removed when
// the build ends, and the lines the backend named could not be read: it is kept at
// keepC, to be reported. Where the backend spoke only of the program's own .spin2
// objects, the program is what it refused, and nothing is kept or said.
func backendRefusal(c []byte, keepC string, said []byte, code int, err error) (int, error) {
	ours := strings.HasPrefix(err.Error(), "flexcc crashed")
	for _, line := range strings.Split(string(said), "\n") {
		if line = strings.TrimSpace(line); line == "" || harmlessWarning(line) || spin2Diag.MatchString(line) || hubOverflow(line) {
			continue
		}
		ours = true
	}
	if !ours {
		for _, line := range strings.Split(string(said), "\n") {
			if callOutOfRange.MatchString(line) {
				return code, errors.New("ogo: the program does not fit the P2's 512 KB of Hub RAM: its code lies past what a call can reach")
			}
		}
		return code, err
	}
	if werr := os.WriteFile(keepC, c, 0o644); werr != nil {
		return code, err
	}
	return code, fmt.Errorf("%v\nogo: the C compiler refused the C ogo wrote for this program. It is a fault of ogo's, not of the program: please report it, with the program and %s, the C whose lines it names", err, keepC)
}

// hubOverflow is the backend's word that the program does not fit Hub RAM, "final
// output size of N bytes exceeds maximum of 524288", which is the PROGRAM's size:
// it only warns, and writes a binary no P2 can load.
func hubOverflow(line string) bool {
	return strings.Contains(line, "exceeds maximum of") || callOutOfRange.MatchString(line)
}

// callOutOfRange is the assembler's word that a call or a jump cannot reach its
// target, "Operand for call is out of range", which in an image of Hub RAM's 512 KB
// none is: the image is larger, and that is the program's size too. A local array
// of 512 KB, its initializer a store an element, met it before anything said the
// program did not fit.
var callOutOfRange = regexp.MustCompile(`error: Operand for \w+ is out of range`)

// backendFault fails a build the backend completed and said something about. The C
// is the compiler's own, written to build without a word, so a warning about it is
// a fault of the compiler's -- and every kind that reached a build so far was wrong
// code, not noise: "Redefining x" kept the first of two declarations, "Bad number of
// parameters" passed a 64-bit constant as two words to an int, "incompatible
// pointer types in parameter passing" shifted a struct argument a word, and
// "incompatible types in comparison" compared two interfaces by their tables, each
// built and run in silence. The binary is removed, so `ogo run` and a later load
// cannot pick it up, and the C is kept at keepC to be reported with the program.
// --allow-backend-warnings builds as before. A line holding one of
// harmlessWarnings is passed over.
func backendFault(c []byte, out, keepC string, said []byte) (int, error) {
	// A program too big for Hub RAM is told so, and is no fault of ogo's: it is
	// what the program declares. The backend warns and writes the binary all the
	// same, which no P2 can load.
	for _, line := range strings.Split(string(said), "\n") {
		if line = strings.TrimSpace(line); hubOverflow(line) {
			os.Remove(out)
			return 1, fmt.Errorf("ogo: the program does not fit the P2's 512 KB of Hub RAM: %s", strings.TrimPrefix(line, "warning: "))
		}
	}
	var left []string
	for _, line := range strings.Split(string(said), "\n") {
		// A line about a .spin2 file is about the program's own code, an object a
		// package carries, which the backend compiles beside the C: shown, as
		// it was, and the program's to answer.
		if line = strings.TrimSpace(line); line == "" || harmlessWarning(line) || spin2Diag.MatchString(line) {
			continue
		}
		left = append(left, line)
	}
	if len(left) == 0 {
		return 0, nil
	}
	os.Remove(out)
	kept := keepC
	if err := os.WriteFile(kept, c, 0o644); err != nil {
		kept = ""
	}
	msg := "ogo: the C compiler warned about the C ogo wrote for this program, and a warning there has meant wrong code before, so no binary was written. " +
		"It is a fault of ogo's, not of the program: please report it, with the program"
	if kept != "" {
		msg += " and " + kept + ", the C the warning is about"
	}
	msg += ". --allow-backend-warnings builds it anyway."
	return 1, errors.New(msg)
}

// harmlessWarnings are backend diagnostics examined and found to say nothing about
// a program's meaning.
var harmlessWarnings = []string{
	// A CORDIC operation whose result nothing reads -- `go math.Sqrt(2)`, the
	// result of a function started on a cog being discarded -- is deleted. Nothing
	// read it, so nothing changes.
	"Deleting apparently unused cordic instruction",
	// A product of a value the backend has propagated to a constant, computed
	// twice -- `n := 1000; a := n * 1000; b := n * 1000` -- is merged into one
	// CORDIC multiply read twice, and the fold of a constant CORDIC command
	// writes the product into both reads, which is what each asks for, saying
	// it found two. Measured on a P2-EDGE (the run case "a product of a constant
	// value computed twice"). The GETQY twin of the line is the same code and
	// has not been seen, so it is a fault until it is examined.
	"found two GETQX during constant propagate",
}

// spin2Diag is a backend diagnostic about a Spin2 source, "path/obj.spin2:12: ...".
var spin2Diag = regexp.MustCompile(`\.spin2:[0-9]+:`)

func harmlessWarning(line string) bool {
	for _, h := range harmlessWarnings {
		if strings.Contains(line, h) {
			return true
		}
	}
	return false
}

// cogHint says, of a program that outgrew a cog's registers, which of its
// functions hold the most of them, read from the listing the backend wrote. The
// backend says "fit 480 failed: pc is 494" or "exceeded local register limit" and
// names no function, and a function's locals are what fill the cog: each value it
// keeps is a register of its own. So the answer is to split the function that
// holds the most, and this names it, by the name it has in the C, which is the
// OctoGo name for a function of the main package, Type_method for a method and
// pkg_Function for another package's. Only the program's own functions are named,
// the ones its C defines without static (programFuncs): the library's and the
// emitter's helpers are no program's to split. A listing older than the build, or
// none, names nothing.
func cogHint(listing string, since time.Time, own map[string]bool) string {
	const advice = "ogo: the program needs more registers than a cog has: a function's locals take one each"
	if fi, err := os.Stat(listing); err != nil || fi.ModTime().Before(since.Add(-time.Second)) {
		return advice + "; split the largest function into smaller ones"
	}
	b, err := os.ReadFile(listing)
	if err != nil {
		return advice + "; split the largest function into smaller ones"
	}
	lines := strings.Split(string(b), "\n")
	at := map[string]int{}
	for i, line := range lines {
		if listingLabel.MatchString(line) {
			at[line] = i
		}
	}
	type held struct {
		name string
		regs int
	}
	var fns []held
	for label, from := range at {
		to, ok := at[label+"_ret"]
		if !ok || to < from || !strings.HasPrefix(label, "_") {
			continue
		}
		name := label[1:]
		if !own[name] {
			continue // the library's or the emitter's
		}
		regs := map[string]bool{}
		for _, line := range lines[from:to] {
			for _, r := range listingLocal.FindAllString(line, -1) {
				regs[r] = true
			}
		}
		if len(regs) != 0 {
			fns = append(fns, held{name, len(regs)})
		}
	}
	if len(fns) == 0 {
		return advice + "; split the largest function into smaller ones"
	}
	slices.SortFunc(fns, func(a, b held) int {
		if a.regs != b.regs {
			return b.regs - a.regs
		}
		return strings.Compare(a.name, b.name)
	})
	var names []string
	for _, f := range fns[:min(3, len(fns))] {
		names = append(names, fmt.Sprintf("%s (%d)", f.name, f.regs))
	}
	return fmt.Sprintf("%s, and the functions holding the most are %s; split %s into smaller ones",
		advice, strings.Join(names, ", "), fns[0].name)
}

// programFuncs answers the names of the functions the C of a program defines at
// file scope without static: the program's own, which the backend's listing
// labels by the same name. Every helper the emitter writes is static.
func programFuncs(c []byte) map[string]bool {
	own := map[string]bool{}
	for _, line := range strings.Split(string(c), "\n") {
		if m := cFuncDef.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, "static ") {
			own[m[1]] = true
		}
	}
	return own
}

var (
	listingLabel = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	listingLocal = regexp.MustCompile(`\blocal_?[0-9]+\b`)
	// cFuncDef is a function's definition as the emitter writes it: a type, the
	// name and its parameters on one line, ending in the brace that opens the body.
	cFuncDef = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_ *]*[ *]([A-Za-z_][A-Za-z0-9_]*)\(.*\)[A-Za-z_ ]*\{$`)
)

// outgrewCog reports whether what the backend said is that the program needs more
// registers than a cog has: the assembler's check of the register pool, or the
// limit of one function's locals.
func outgrewCog(said []byte) bool {
	return bytes.Contains(said, []byte("fit 480 failed")) || bytes.Contains(said, []byte("exceeded local register limit"))
}

// buildFlags is what a build command line says, beyond the sources themselves.
type buildFlags struct {
	out       string
	release   bool
	unchecked bool
	// noInline leaves inlining to the backend's own idea of what is small, which
	// trades the time of a call for the size of a copy at every call.
	noInline bool
	// allowWarnings builds a program the backend warned about (backendFault).
	allowWarnings bool
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
	// report prints what the program takes of the P2 after it is built
	// (report).
	report bool
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
		case a == "--no-inline" || a == "-no-inline":
			f.noInline = true
		case a == "--allow-backend-warnings" || a == "-allow-backend-warnings":
			f.allowWarnings = true
		case a == "--report" || a == "-report":
			f.report = true
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
// WHAT THAT MISSED. -Ono-inline-small had been hiding a third fault, one no
// reproducer named, so no battery re-run could see it go live: two optimizer
// rewrites that disturb the carry of a 64-bit add or subtract of a constant
// (doc/add-immediate-carry.c, flexprop#109), wrong on the board from v0.34.0 until
// the backend was regenerated on 2026-09-15 carrying a fix,
// internal/optimize_ir.c.diff, and on 2026-09-21 at spin2cpp v7.7.3, which has
// upstream's own. The widened on-board fuzzer sample is what found it. The flag was
// not brought back for it: it cost 1.4x to 3.3x on the loops measured then, and the
// fault was the backend's to fix.
//
// If a pass has to go again, this is the shape: a reproducer in doc/ that names
// the flag, the matrix measured on hardware rather than inferred, and the cost in
// cycles on a loop of each shape, not just in bytes -- and a wide fuzzer sweep on
// the board before and after, for the faults no reproducer names yet.
//
// inc is the directory the program's Spin2 objects are imported from (buildRoot),
// handed the backend as an include directory, or "" for none: the emitted C names
// each by its path from there, `struct __using("vga/drv.spin2")`, so what the object
// itself names is found beside it.
func compileC(cFile, out, inc string, stdout, stderr io.Writer, extra ...string) (rc int, err error) {
	// The backend is a C program transpiled to Go, and it can crash as one: having
	// said "exceeded local register limit" about a function too big for it,
	// spin2cpp goes on to rename a register it never allocated, which in the
	// transpile is a nil dereference -- and flexcc.Main passes on every panic that is
	// not an exit. That is the backend's fault, and a stack dump of the whole
	// compiler is the wrong way to report it: it is an error naming the crash, after
	// whatever the backend said first.
	defer func() {
		if r := recover(); r != nil {
			rc, err = 1, fmt.Errorf("flexcc crashed: %v", r)
		}
	}()
	args := append([]string{"-2"}, extra...)
	if inc != "" {
		args = append(args, "-I", inc)
	}
	if err := flexcc.Main(nil, stdout, stderr, append(args, "-o", out, cFile)); err != nil {
		return 1, fmt.Errorf("flexcc: %v", err)
	}
	return 0, nil
}
