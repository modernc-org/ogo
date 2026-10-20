// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing/fstest"
	"time"

	"modernc.org/ogo/internal/octogo"
	"modernc.org/ogo/loadp2"
)

// testRunnerFile is the name the generated runner is compiled under. It is not
// written to the user's directory -- the build reads it from an overlay -- but it
// is a real file name so a diagnostic in the generated code can point somewhere.
const testRunnerFile = "ogo_test_main.ogo"

// testDoneLine is what the runner prints last. The board returns no exit status,
// so the verdict travels back over the serial line as text and the host matches
// this to know the run finished rather than hung.
const testDoneLine = "ogo-test-done"

// testTimeLine leads the line the runner prints before testDoneLine: the
// microseconds its tests and benchmarks took on the board, the time go test puts on
// the package's last line. Measured there, it leaves out the load.
const testTimeLine = "ogo-test-time "

// importPath is how go test names the package in dir on its last line: the module's
// path and the package's directory below the root, where an ogo.mod says what the
// module is, and the directory as given where none does.
func importPath(dir, rel, modulePath string) string {
	switch {
	case modulePath == "":
		return dir
	case rel == ".":
		return modulePath
	}
	return modulePath + "/" + rel
}

// Test implements `ogo test [package]`: it compiles the package together with its
// _test.ogo files and a generated runner, then loads the result on a connected P2
// and reports what the tests printed.
//
// The board is the only place a result means anything. A host shim would be faster
// and would sometimes be WRONG: gcc and flexcc disagree about semantics and not
// only about warnings, and this compiler has already shipped a feature that passed
// on the host and failed on the board (doc/call-through-array-element.c). A test
// that reports "ok" from somewhere the program will never run is worse than a test
// that did not run at all, so there is no host mode.
//
// -c compiles the tests and does not run them, which is what CI without a board
// can honestly do.
func Test(args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	var compileOnly, allowWarnings, benchMem bool
	var port, runPat, benchPat, timeout string
	benchTime := "1s"
	var rest []string
	// A test binary takes the same clock as the program it tests. Running the tests
	// at a different speed than the thing ships at is how a timing bug hides.
	clock, xtal, goStack := 0, octogo.DefaultXtal, 0
	for i := 0; i < len(args); i++ {
		var err error
		switch a := args[i]; {
		case a == "-c":
			compileOnly = true
		case a == "--allow-backend-warnings" || a == "-allow-backend-warnings":
			allowWarnings = true
		case a == "-p":
			i++
			if i >= len(args) {
				return 2, fmt.Errorf("test: -p requires an argument")
			}
			port = args[i]
		case a == "-run" || strings.HasPrefix(a, "-run="):
			pat := strings.TrimPrefix(a, "-run=")
			if pat == "-run" {
				i++
				if i >= len(args) {
					return 2, fmt.Errorf("test: -run requires an argument")
				}
				pat = args[i]
			}
			runPat = pat
		case a == "-bench" || strings.HasPrefix(a, "-bench="):
			pat, err := flagValue("-bench", a, args, &i)
			if err != nil {
				return 2, err
			}
			benchPat = pat
		case a == "-benchtime" || strings.HasPrefix(a, "-benchtime="):
			v, err := flagValue("-benchtime", a, args, &i)
			if err != nil {
				return 2, err
			}
			benchTime = v
		case a == "-benchmem":
			benchMem = true
		case a == "-timeout" || strings.HasPrefix(a, "-timeout="):
			v, err := flagValue("-timeout", a, args, &i)
			if err != nil {
				return 2, err
			}
			timeout = v
		case a == "--clock" || a == "-clock":
			if clock, err = parseHz("test", a, args, &i); err != nil {
				return 2, err
			}
		case a == "--xtal" || a == "-xtal":
			if xtal, err = parseHz("test", a, args, &i); err != nil {
				return 2, err
			}
		case a == "--gostack" || a == "-gostack":
			// The goroutine stack the program ships with, as for the clock: the
			// usage named the flag and the parser refused it.
			i++
			if i >= len(args) {
				return 2, fmt.Errorf("test: %s requires a number of longs", a)
			}
			n, cerr := strconv.Atoi(strings.TrimSpace(args[i]))
			if cerr != nil {
				return 2, fmt.Errorf("test: %s wants a number of longs, got %q", a, args[i])
			}
			lo, hi, _ := octogo.GoStackRange()
			if n < lo || n > hi {
				return 2, fmt.Errorf("test: %s must be between %d and %d longs, got %d", a, lo, hi, n)
			}
			goStack = n
		case strings.HasPrefix(a, "-"):
			return 2, fmt.Errorf("test: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	clockOpts, err := clockOption(clock, xtal)
	if err != nil {
		return 2, err
	}
	if goStack != 0 {
		clockOpts = append(clockOpts, octogo.GoStack(goStack))
	}

	opts := testOptions{compileOnly: compileOnly, allowWarnings: allowWarnings, stackAsked: goStack != 0, port: port, runPat: runPat, clockOpts: clockOpts, benchMem: benchMem}
	if opts.clockHz = clock; clock == 0 {
		opts.clockHz = octogo.DefaultClock
	}
	if opts.benchTime, opts.benchCount, err = parseBenchTime(benchTime); err != nil {
		return 2, err
	}
	if timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err != nil || d < 0 {
			return 2, fmt.Errorf("test: invalid -timeout %q: want a duration, or 0 for no limit", timeout)
		}
		opts.timeout, opts.timeoutSet = d, true
	}
	if benchPat != "" {
		if opts.bench, err = octogo.CompileTestFilter("-bench", benchPat); err != nil {
			return 2, fmt.Errorf("test: %v", err)
		}
	}
	if runPat != "" {
		if opts.run, err = octogo.CompileTestFilter("-run", runPat); err != nil {
			return 2, fmt.Errorf("test: %v", err)
		}
	}
	dirs, many := []string{"."}, false
	switch {
	case len(rest) == 0:
	case len(rest) == 1 && strings.HasSuffix(rest[0], "..."):
		// `ogo test ./...` -- every package under a root, which is how a program of
		// several packages is tested in one command. Each is built and run in turn:
		// the board runs one binary at a time, so there is nothing to overlap.
		if dirs, err = packageDirs(rest[0]); err != nil {
			return 2, err
		}
		many = true
	case len(rest) == 1 && isDir(rest[0]):
		dirs = rest[:1]
	default:
		return 2, fmt.Errorf("test: expected at most one package directory")
	}
	if compileOnly && !many {
		// One package built and not run fails as a build does, with the error
		// alone, as `go test -c` does.
		return testPackage(dirs[0], opts, stdout, stderr)
	}
	worst := 0
	for _, d := range dirs {
		code, err := testPackage(d, opts, stdout, stderr)
		if err != nil {
			// Reported and passed over, as `go test ./...` does with a package
			// that does not build: the other packages are still worth running,
			// and the run fails as a whole at the end.
			fmt.Fprintln(stderr, err)
			fmt.Fprintf(stdout, "FAIL\t%s [build failed]\n", pkgName(d))
			code = max(code, 1)
		}
		worst = max(worst, code)
	}
	// go test's last line where a run named its packages and one failed, in case
	// the failure has scrolled away (printExitStatus in cmd/go): not after a build
	// alone, and not in its local directory mode, a `go test` naming none.
	if worst != 0 && len(rest) != 0 && !compileOnly {
		fmt.Fprintln(stdout, "FAIL")
	}
	return worst, nil
}

// pkgName is how go test names the package in dir on a line of its own: by its
// import path, or by the directory where the module it is in cannot be read.
func pkgName(dir string) string {
	if _, rel, modulePath, err := moduleContext(dir); err == nil {
		return importPath(dir, rel, modulePath)
	}
	return dir
}

// testOptions is what every package in a run is tested with -- the same flags,
// whether one package was named or a whole tree matched.
type testOptions struct {
	compileOnly bool
	// allowWarnings builds tests the backend warned about (backendFault).
	allowWarnings bool
	// stackAsked says --gostack chose the goroutine stack, which the build then
	// leaves as asked (compileSized).
	stackAsked bool
	port       string
	// run selects which tests the runner calls, as `go test -run` does: an
	// unanchored regular expression over the test's name, matched HERE rather
	// than on the board -- the selection is made where the runner is generated, so
	// what is not selected is not compiled either, which is what makes iterating
	// on one test worth the board's ten seconds. A level of the pattern after a
	// slash selects subtests, whose names exist only as the test runs: that part
	// is compiled here and matched on the board (octogo.TestFilter).
	run       *octogo.TestFilter
	runPat    string
	clockOpts []octogo.EmitOption
	clockHz   int
	// bench selects the benchmarks to run, as `go test -bench` does; none run
	// where it is nil. Each runs for benchTime, or benchCount times where that is
	// not 0 (-benchtime), its allocations reported where benchMem says so.
	bench      *octogo.TestFilter
	benchTime  time.Duration
	benchCount int
	benchMem   bool
	// timeout, where timeoutSet, is how long the board is given in place of what
	// captureTimeout computes (-timeout), 0 for no limit.
	timeout    time.Duration
	timeoutSet bool
}

// flagValue is the value of the flag name at args[*i], written `-name=value` or
// `-name value`.
func flagValue(name, a string, args []string, i *int) (string, error) {
	if v, ok := strings.CutPrefix(a, name+"="); ok {
		return v, nil
	}
	*i++
	if *i >= len(args) {
		return "", fmt.Errorf("test: %s requires an argument", name)
	}
	return args[*i], nil
}

// parseBenchTime reads -benchtime as go test does: a duration, or a count of runs
// written with an x after it, "100x".
func parseBenchTime(s string) (time.Duration, int, error) {
	if n, ok := strings.CutSuffix(s, "x"); ok {
		v, err := strconv.ParseInt(n, 10, 0)
		if err != nil || v <= 0 || v > 1e9 {
			return 0, 0, fmt.Errorf("test: invalid -benchtime %q: a count is from 1x to 1000000000x", s)
		}
		return 0, int(v), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, 0, fmt.Errorf("test: invalid -benchtime %q: want a duration, or a count such as 100x", s)
	}
	return d, 0, nil
}

// packageDirs expands a `.../...` pattern to the package directories under its
// root, in path order. A package is a directory holding at least one .ogo file;
// testdata and the dot- and underscore-prefixed directories are not packages, as
// they are not to the Go tool either.
func packageDirs(pattern string) ([]string, error) {
	root := strings.TrimSuffix(pattern, "...")
	root = strings.TrimSuffix(root, string(filepath.Separator))
	root = strings.TrimSuffix(root, "/")
	if root == "" {
		root = "."
	}
	if !isDir(root) {
		return nil, fmt.Errorf("test: %s: no such directory", root)
	}
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if base := filepath.Base(p); p != root && (base == "testdata" || strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")) {
			return filepath.SkipDir
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".ogo") {
				dirs = append(dirs, p)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("test: %s: %v", pattern, err)
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("test: %s matched no packages", pattern)
	}
	slices.Sort(dirs)
	return dirs, nil
}

// testPackage builds and runs one package's tests. It is the whole of what a named
// package does, and one step of what a pattern does.
func testPackage(dir string, opts testOptions, stdout, stderr io.Writer) (int, error) {
	compileOnly, port, clockOpts := opts.compileOnly, opts.port, opts.clockOpts
	files, testFiles, err := testPackageFiles(dir)
	if err != nil {
		return 2, err
	}
	if len(testFiles) == 0 {
		// No tests to run, but the package is still CHECKED. Reporting "ok" of
		// something never compiled is how `ogo test ./...` over a tree could report
		// every package green while the program did not build at all -- and a
		// package with no tests yet is exactly where that hides. The checker is
		// where a program's errors are found; the C stage is left to a build, which
		// is what compiles it for real.
		fsys, rel, modulePath, err := moduleContext(dir)
		if err != nil {
			return 2, err
		}
		if _, err := octogo.BuildModule(-1, modulePath, rel, files, fsys); err != nil {
			return 1, err
		}
		fmt.Fprintf(stdout, "?   \t%s\t[no test files]\n", importPath(dir, rel, modulePath))
		return 0, nil
	}

	// The tests are discovered by the parser rather than by reading the text: a
	// package that does not compile has no tests to find, and says so here.
	all := append(append([]string{}, files...), testFiles...)
	fsys, rel, modulePath, err := moduleContext(dir)
	if err != nil {
		return 2, err
	}

	pkg, err := octogo.BuildModule(-1, modulePath, rel, all, fsys)
	if err != nil {
		return 1, err
	}
	names, err := octogo.TestFuncs(pkg)
	if err != nil {
		return 1, err
	}
	// Benchmarks are found whether or not any is run, so a malformed one is
	// reported as vet reports it under go test.
	benches, err := octogo.BenchFuncs(pkg)
	if err != nil {
		return 1, err
	}
	if opts.bench == nil {
		benches = nil // go test runs a benchmark only where -bench asks for it
	} else {
		benches = selected(benches, opts.bench)
	}
	if opts.run != nil {
		kept := selected(names, opts.run)
		if len(kept) == 0 && len(names) != 0 && len(benches) == 0 {
			// Said out loud, as `go test` says it: a pattern that matches nothing
			// otherwise reports a green package that ran no test at all.
			fmt.Fprintf(stderr, "testing: warning: no tests to run\n")
		}
		names = kept
	}
	if len(names) == 0 && len(benches) == 0 {
		fmt.Fprintf(stdout, "ok  \t%s\t[no tests to run]\n", importPath(dir, rel, modulePath))
		return 0, nil
	}

	// The runner is an ordinary .ogo file compiled with the rest, layered over the
	// directory rather than written into it.
	// The runner joins the package, so it is layered where the package lives: at the
	// module root that is the top of fsys, in a module it is the package's directory.
	runner := path.Join(rel, testRunnerFile)
	all = append(all, testRunnerFile)
	// What the program calls its functions, for a refusal of compileSized's.
	var rep octogo.Report
	emitOpts := append([]octogo.EmitOption{octogo.Checked(), octogo.TestEntry("ogoTestMain"), octogo.FuncValueClasses(), octogo.WithReport(&rep)}, clockOpts...)
	var overlay overlayFS
	// emit builds the program with its runner, handing in what naming a subtest
	// takes or not, and emits its C.
	emit := func(subtests bool) (*bytes.Buffer, error) {
		src := testRunnerSrc(names, benches, benchRun{
			labels: benchLabels(benchPkg(dir, rel, modulePath), opts.clockHz),
			time:   opts.benchTime,
			count:  opts.benchCount,
			mem:    opts.benchMem,
		}, opts.run, opts.bench, subtests)
		overlay = overlayFS{
			FS:    fsys,
			extra: fstest.MapFS{runner: &fstest.MapFile{Data: []byte(src)}},
		}
		pkg, err := octogo.BuildModule(-1, modulePath, rel, all, overlay)
		if err != nil {
			return nil, err
		}
		var cbuf bytes.Buffer
		if err := octogo.EmitC(pkg, &cbuf, append(emitOpts[:len(emitOpts):len(emitOpts)], octogo.Inline())...); err != nil {
			return nil, err
		}
		return &cbuf, nil
	}
	// A program whose source names Run is handed the names, table and all, which
	// every program running a subtest is; one whose C then reaches neither T.Run nor
	// B.Run named another type's Run, and is emitted again without.
	subtests := octogo.MayRunSubtests(pkg)
	cbuf, err := emit(subtests)
	if err == nil && subtests && !octogo.RunsSubtests(cbuf.Bytes()) {
		cbuf, err = emit(false)
	}
	if err != nil {
		return 1, err
	}

	tmp, err := os.MkdirTemp("", "ogo-test-*")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(tmp)
	cFile := filepath.Join(tmp, "ogo_test.c")
	binary := filepath.Join(tmp, dirPkgName(dir)+".test.binary")
	// The same program plainer, should it outgrow the cog (compileMarked): its
	// switches chains of compares, and then no function marked either.
	emitAgain := func(opts ...octogo.EmitOption) func() ([]byte, error) {
		return func() ([]byte, error) {
			pkg, err := octogo.BuildModule(-1, modulePath, rel, all, overlay)
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
	chains := append(emitOpts[:len(emitOpts):len(emitOpts)], octogo.SwitchChains())
	plainer := []func() ([]byte, error){
		emitAgain(append(chains[:len(chains):len(chains)], octogo.Inline())...),
		emitAgain(chains...),
	}
	variants := func(extra ...octogo.EmitOption) ([]byte, []func() ([]byte, error), error) {
		with := func(opts []octogo.EmitOption, more ...octogo.EmitOption) []octogo.EmitOption {
			return append(append(slices.Clone(opts), more...), extra...)
		}
		c, err := emitAgain(with(emitOpts, octogo.Inline())...)()
		if err != nil {
			return nil, nil, err
		}
		return c, []func() ([]byte, error){
			emitAgain(with(chains, octogo.Inline())...),
			emitAgain(with(chains)...),
		}, nil
	}
	root, err := buildRoot(dir)
	if err != nil {
		return 2, err
	}
	keepC := filepath.Join(dir, dirPkgName(dir)+".test.c")
	if code, err := compileSized(cbuf.Bytes(), plainer, variants, rep.Funcs, rep.Goroutines, cFile, binary, root, keepC, opts.allowWarnings, opts.stackAsked, stdout, stderr); err != nil {
		return code, err
	}
	if compileOnly {
		out := filepath.Join(dir, dirPkgName(dir)+".test.binary")
		if err := copyFile(binary, out); err != nil {
			return 1, err
		}
		built := fmt.Sprintf("%d test%s", len(names), plural(len(names)))
		if len(benches) != 0 {
			built += fmt.Sprintf(" and %d benchmark%s", len(benches), plural(len(benches)))
		}
		fmt.Fprintf(stdout, "ok  \t%s\t[built %s, not run]\n", importPath(dir, rel, modulePath), built)
		return 0, nil
	}

	out, ok := loadp2.Capture(loadp2.Options{Binary: binary, Port: port}, testDoneLine, captureTimeout(len(benches), opts))
	took := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r ")
		switch {
		case line == "", line == testDoneLine:
		case strings.HasPrefix(line, testTimeLine):
			if us, err := strconv.ParseUint(strings.TrimPrefix(line, testTimeLine), 10, 32); err == nil {
				took = fmt.Sprintf("\t%.3fs", float64(us)/1e6)
			}
		case strings.HasPrefix(line, "( Entering terminal mode"):
			// The loader announcing itself, not the program speaking.
		default:
			fmt.Fprintln(stdout, line)
		}
	}
	ip := importPath(dir, rel, modulePath)
	switch {
	case !ok && strings.Contains(out, "panic: "):
		// A panic stops every cog of a debug build, the runner's among them.
		fmt.Fprintf(stdout, "FAIL\t%s\t[a panic stopped the board]\n", ip)
		return 1, nil
	case !ok:
		fmt.Fprintf(stdout, "FAIL\t%s\t[the board produced no result]\n", ip)
		return 1, nil
	case strings.Contains(out, "\n--- FAIL:") || strings.HasPrefix(out, "--- FAIL:"):
		fmt.Fprintf(stdout, "FAIL\t%s%s\n", ip, took)
		return 1, nil
	}
	fmt.Fprintf(stdout, "ok  \t%s%s\n", ip, took)
	return 0, nil
}

// selected is the names f selects, or all of them where f is nil.
func selected(names []string, f *octogo.TestFilter) []string {
	if f == nil {
		return names
	}
	var kept []string
	for _, nm := range names {
		if mask, _ := f.Top(nm); mask != 0 {
			kept = append(kept, nm)
		}
	}
	return kept
}

// captureTimeout is how long the board is given to say it is done: two minutes for
// the tests, and for each benchmark what go test's growing runs take at most to
// reach its time -- the last run up to 1.2 times longer than asked and the runs
// before it as long together -- three times over, and a minute more, which is what
// its sub-benchmarks, if any, have between them, there being no counting them
// before they run. A count of runs has no time to be read from, and is given ten
// minutes a benchmark, the time go test gives a whole package. -timeout replaces
// it all, and 0 there is no limit.
func captureTimeout(benches int, opts testOptions) time.Duration {
	if opts.timeoutSet {
		if opts.timeout == 0 {
			return math.MaxInt64
		}
		return opts.timeout
	}
	per := 3*opts.benchTime + time.Minute
	if opts.benchCount != 0 {
		per = 10 * time.Minute
	}
	return 2*time.Minute + time.Duration(benches)*per
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// testRunnerSrc generates the runner: one testing.T per test, so a failure in one
// does not mark the next, each run through testing.RunTest, which prints go test
// -v's === RUN line and, when the test ends, its --- PASS, --- FAIL or --- SKIP line
// with its subtests' under it -- what a test logs standing between them. RunTest is
// also what FailNow and SkipNow return to. The benchmarks after them, where -bench
// selected any, each through testing.RunBenchmark with a testing.B of its own,
// after testing.StartBenchmarks has printed go test's labels and set how they run.
//
// A failure is asked about BEFORE a skip, as Go's runner asks: the two marks are
// independent, and a test that fails and then skips has failed. Asked the other
// way round, such a test was reported skipped, counted in no failure, and the
// package passed.
//
// Where run, or bench, has a level below a test's own, the runner carries its
// program, and before each test, or benchmark, testing.SetFilter hands it to the
// testing package with the alternatives the name matched; a benchmark matched only
// in part runs only its sub-benchmarks (testing.OnlySubs). With no such level the
// runner is what it was.
func testRunnerSrc(names, benches []string, br benchRun, run, bench *octogo.TestFilter, subtests bool) string {
	var b strings.Builder
	b.WriteString("// Code generated by ogo test. DO NOT EDIT.\n\nimport (\n\t\"p2\"\n\t\"testing\"\n)\n\n")
	if subtests {
		writeTable(&b, "ogoNotPrint", "uint8", len(octogo.NotPrintTable()), func(i int) int { return int(octogo.NotPrintTable()[i]) })
	}
	runSubs := run != nil && run.Subs() && len(names) != 0
	benchSubs := bench != nil && bench.Subs() && len(benches) != 0
	if runSubs {
		writeFilterProg(&b, "ogoRunFilter", run.Prog())
	}
	if benchSubs {
		writeFilterProg(&b, "ogoBenchFilter", bench.Prog())
	}
	if runSubs || benchSubs {
		// Two words a subtest's slot, of which a test has 256.
		b.WriteString("var ogoFilterState [512]int32\n\n")
	}
	b.WriteString("func ogoTestMain() {\n\tfailed := false\n\togoStart := p2.GetUs()\n")
	if subtests {
		b.WriteString("\ttesting.SetSubtests(ogoNotPrint[:], testing.FullName)\n")
	}
	for i, nm := range names {
		fmt.Fprintf(&b, "\n\tvar t%d testing.T\n", i)
		if runSubs {
			mask, _ := run.Top(nm)
			fmt.Fprintf(&b, "\ttesting.SetFilter(ogoRunFilter[:], ogoFilterState[:], %d, testing.MatchSub)\n", mask)
		}
		fmt.Fprintf(&b, "\ttesting.RunTest(&t%d, %q, %s)\n", i, nm, nm)
		fmt.Fprintf(&b, "\tif t%d.Failed() {\n\t\tfailed = true\n\t}\n", i)
	}
	if len(benches) != 0 {
		// go test pads a result's name to one more than the longest name's length.
		width := 0
		for _, nm := range benches {
			width = max(width, len(nm)+1)
		}
		fmt.Fprintf(&b, "\n\ttesting.StartBenchmarks(%q, %d, %d, %d, %v)\n", br.labels, width, br.time.Nanoseconds(), br.count, br.mem)
		if runSubs && !benchSubs {
			b.WriteString("\ttesting.SetFilter(nil, nil, 0, nil)\n")
		}
		for i, nm := range benches {
			fmt.Fprintf(&b, "\n\tvar b%d testing.B\n", i)
			if benchSubs {
				mask, partial := bench.Top(nm)
				fmt.Fprintf(&b, "\ttesting.SetFilter(ogoBenchFilter[:], ogoFilterState[:], %d, testing.MatchSub)\n", mask)
				if partial {
					fmt.Fprintf(&b, "\ttesting.OnlySubs(&b%d)\n", i)
				}
			}
			fmt.Fprintf(&b, "\ttesting.RunBenchmark(&b%d, %q, %s)\n", i, nm, nm)
			fmt.Fprintf(&b, "\tif b%d.Failed() {\n\t\tfailed = true\n\t}\n", i)
		}
	}
	// go test -v's PASS where nothing failed and FAIL where something did; then the
	// time, which "ogo test" reads off and prints on the package's line
	// (testTimeLine).
	fmt.Fprintf(&b, "\n\tif failed {\n\t\tprintln(\"FAIL\")\n\t} else {\n\t\tprintln(\"PASS\")\n\t}\n\tprint(%q)\n\tprintln(p2.GetUs() - ogoStart)\n\tprintln(%q)\n}\n", testTimeLine, testDoneLine)
	return b.String()
}

// writeFilterProg writes a filter's program as a package array called name.
func writeFilterProg(b *strings.Builder, name string, prog []int32) {
	writeTable(b, name, "int32", len(prog), func(i int) int { return int(prog[i]) })
}

// writeTable writes a package array called name of n elements of type typ, at(i)
// each.
func writeTable(b *strings.Builder, name, typ string, n int, at func(int) int) {
	fmt.Fprintf(b, "var %s = [...]%s{", name, typ)
	for i := range n {
		switch {
		case i%16 == 0:
			b.WriteString("\n\t")
		default:
			b.WriteByte(' ')
		}
		fmt.Fprintf(b, "%d,", at(i))
	}
	b.WriteString("\n}\n\n")
}

// benchRun is how the benchmarks of a run are run (testing.StartBenchmarks).
type benchRun struct {
	labels string
	time   time.Duration
	count  int
	mem    bool
}

// benchLabels is what go test prints ahead of the first benchmark, as the lines
// benchstat reads it by: the package, and the cpu, which here says the clock it
// runs at -- the measure of every time a benchmark reports. goos and goarch, which
// would be names Go does not have, are left out.
func benchLabels(pkg string, hz int) string {
	clock := fmt.Sprintf("%d Hz", hz)
	if hz%1000000 == 0 {
		clock = fmt.Sprintf("%d MHz", hz/1000000)
	}
	return fmt.Sprintf("pkg: %s\ncpu: Propeller 2 at %s\n", pkg, clock)
}

// benchPkg names the package a benchmark label names: its import path in a module,
// and outside one, where the directory built is the root and has none, the
// directory's own name.
func benchPkg(dir, rel, modulePath string) string {
	if modulePath == "" {
		return dirPkgName(dir)
	}
	return path.Join(modulePath, rel)
}

// testPackageFiles splits a directory's sources into the package's own and its
// test files.
func testPackageFiles(dir string) (files, testFiles []string, err error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("test: %v", err)
	}
	for _, de := range des {
		switch nm := de.Name(); {
		case de.IsDir(), !strings.HasSuffix(nm, ".ogo"):
			// not a source file
		case strings.HasSuffix(nm, "_test.ogo"):
			testFiles = append(testFiles, nm)
		default:
			files = append(files, nm)
		}
	}
	if len(files)+len(testFiles) == 0 {
		return nil, nil, fmt.Errorf("test: no .ogo source files in %s", dir)
	}
	return files, testFiles, nil
}

// overlayFS reads from extra first and from FS otherwise, which is how the
// generated runner joins the package without being written to the user's
// directory.
type overlayFS struct {
	fs.FS
	extra fs.FS
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if f, err := o.extra.Open(name); err == nil {
		return f, nil
	}
	return o.FS.Open(name)
}

// ReadDir lists a directory of both file systems, extra's entry winning where both
// have one. The overlay adds a file beside the package's own, so a directory it
// has is the package's as well: listed through Open, it was extra's alone, the
// runner and nothing else, and the package's .spin2 files were not there for its
// functions declared without a body to be bound to.
func (o overlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	base, err := fs.ReadDir(o.FS, name)
	extra, err2 := fs.ReadDir(o.extra, name)
	if err != nil && err2 != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := append([]fs.DirEntry(nil), extra...)
	for _, e := range extra {
		seen[e.Name()] = true
	}
	for _, e := range base {
		if !seen[e.Name()] {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
