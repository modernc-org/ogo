// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command ogo is a compiler for the OctoGo programming language. OctoGo
// brings Native Go-like Concurrency for the Parallax Propeller 2.
//
// # Windows
//
// On Windows, run ogo from cmd.exe or PowerShell, not a Unix-emulation shell
// (git-bash, MSYS2, Cygwin). The board-facing commands (run, loadp2) drive the
// serial port through the native Windows console and are unreliable under those
// shells: the P2 handshake times out intermittently and the terminal's exit key
// can stop responding (use Ctrl-C to escape). Building (build, fmt) is
// unaffected and works in any shell.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"modernc.org/ogo/internal/build"
	"modernc.org/ogo/internal/format"
	"modernc.org/ogo/internal/loadp2"
	"modernc.org/ogo/internal/smith"
	"modernc.org/opt"
)

func fail(rc int, s string, args ...any) {
	s = fmt.Sprintf(s, args...)
	fmt.Fprintln(os.Stderr, s)
	os.Exit(rc)
}

// version, when non-empty, is the release tag stamped into a prebuilt binary by the
// build with -ldflags "-X main.version=vX.Y.Z". A `go build` of a tagged checkout
// only records the commit, not the tag, so the published preview binaries set this
// to self-report their release; an ordinary build leaves it empty and falls back to
// the Go build info below.
var version string

// printVersion reports what a bug report needs to identify a build: the release tag
// or module version (or the VCS revision, for a build from source), the host
// platform and the Go toolchain that built it. The values come from the stamped-in
// version above or the build info the Go toolchain records, so a published preview
// binary and a `go install modernc.org/ogo@latest` binary each name their release
// while a plain local build names its commit.
func printVersion(w io.Writer) {
	ver, revision, modified := "(devel)", "", false
	if version != "" {
		ver = version // a published binary stamped with its release tag by the build
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if version == "" {
			if v := bi.Main.Version; v != "" {
				ver = v
			}
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	// A module version already names the commit (a release tag, or a pseudo-version
	// with the revision in it), so the bare revision is only worth printing when
	// there is no stamped or module version at all.
	if ver == "(devel)" && revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		ver = revision
	}
	// A pseudo-version of a dirty tree already carries the marker.
	if modified && !strings.HasSuffix(ver, "+dirty") {
		ver += "+dirty"
	}
	fmt.Fprintf(w, "ogo version %s %s/%s\n", ver, runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(w, "built with %s\n", runtime.Version())
}

func main() {
	// loadp2 is a verbatim passthrough to the transpiled P2 loader. Its flag
	// grammar (-a/-9/-e, @ADDR=file load specs) is not ogo's, so dispatch it
	// before ogo's option parser can touch it, handing over the raw arg tail and
	// exiting with loadp2's own status.
	if len(os.Args) >= 2 && os.Args[1] == "loadp2" {
		os.Exit(loadp2.SubCommand(os.Args[2:]))
	}

	set := opt.NewSet()
	var subCommand string
	var args []string
	if err := set.Parse(os.Args[1:], func(arg string) error {
		switch {
		case strings.HasPrefix(arg, "-"):
			args = append(args, arg)
		default:
			switch {
			case subCommand == "":
				subCommand = arg
			default:
				args = append(args, arg)
			}
		}
		return nil
	}); err != nil {
		fail(2, "%v", err)
	}

	switch subCommand {
	case "fmt":
		if rc, err := format.SubCommand(args, os.Stdin, os.Stdout, os.Stderr); rc != 0 || err != nil {
			fail(rc, "err=%v", err)
		}
	case "smith":
		if rc, err := octosmith.SubCommand(args, os.Stdin, os.Stdout, os.Stderr); rc != 0 || err != nil {
			fail(rc, "err=%v", err)
		}
	case "build":
		rc, err := build.Build(args, os.Stdin, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(rc)
	case "run":
		rc, err := build.Run(args, os.Stdin, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(rc)
	case "version":
		printVersion(os.Stdout)
	case "help":
		if !help(os.Stdout, args) {
			fail(2, "unknown command %q. Run %q.", args[0], os.Args[0]+" help")
		}
	case "test":
		rc, err := build.Test(args, os.Stdin, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(rc)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
}

// usage writes the command overview.
func usage(w io.Writer) {
	fmt.Fprintf(w, `ogo is a tool for managing OctoGo source code.

Usage:

	ogo <command> [arguments]

The commands are:

	build       compile packages and dependencies
	fmt         reformat source files
	help        show help for a command
	loadp2      load a program onto a Propeller 2 board (loadp2 passthrough)
	run         compile and run a program on a connected board
	smith       output a random program for compiler testing
	test        test packages
	version     print the ogo version

Use "%s help <command>" for more information about a command.
`, os.Args[0])
}

// commandHelp is the per-command detail behind "ogo help <command>".
var commandHelp = map[string]string{
	"build": `usage: ogo build [-o output] [--release] [--unchecked] [--clock hz] [package | file.ogo ...]

Build compiles a package to a Propeller 2 binary.

A package is a directory. With no argument the current directory is built; with a
directory argument that directory is; either way every .ogo file in it is compiled
together, except _test.ogo files. Source files may also be named explicitly, in
which case they must all be in one directory.

The binary is written beside the package and named after its directory, except
when a single file is named, which keeps that file's name: ogo build x.ogo writes
x.binary. -o overrides the path.

Runtime checks are on by default: out-of-range indexing and slicing, division and
remainder by zero, a shift by a negative count, appending past a slice's capacity,
a nil pointer dereference and cog exhaustion. Each prints "panic: <what>" and halts
the offending cog. A pointer to an ARRAY is the one that carries no nil check, which
is a limit of the C backend rather than a rule.

The system clock is 160 MHz unless --clock asks for another, that being what the C
backend falls back to: a 20 MHz crystal times eight, which is a round multiplier
rather than any limit of the part. --clock takes plain Hz or a suffix, so both
"--clock 200000000" and "--clock 200MHz" ask for the same thing, and the frequency
must be one the crystal can make EXACTLY -- one it cannot is refused rather than
rounded to the nearest, since every wait, baud rate and sample period is scaled by
it and a board running one percent fast reports nothing at all. --xtal states the
crystal when it is not the usual 20 MHz; nothing can ask the board, so an unstated
crystal is believed.

	-o output     write the binary here
	--unchecked   omit the runtime checks
	--release     reboot the board on a panic instead of halting the cog
	--clock hz    the system clock to ask for, e.g. 200MHz (default 160 MHz)
	--xtal hz     the board's crystal (default 20MHz)
`,
	"run": `usage: ogo run [--release] [--unchecked] [--clock hz] [package | file.ogo ...]

Run builds a package exactly as ogo build does, loads the binary onto a connected
Propeller 2 and opens a terminal on its serial output, reading at 230400 baud so
println output is readable out of the box.

The clock is the one the program was BUILT with -- 160 MHz unless --clock says
otherwise, exactly as for ogo build. The loader is passed a -f frequency, but that
does not decide what the program runs at: a flexcc-compiled program sets its own
clock as it starts, which is why the frequency has to be chosen at build time.

Press Ctrl-] to leave the terminal.

On Windows, run this from cmd.exe or PowerShell, not a Unix-emulation shell
(git-bash, MSYS2, Cygwin): the serial handshake is flaky there and the exit key
may not respond (use Ctrl-C).
`,
	"fmt": `usage: ogo fmt [-l] [-w] [-exclude regexp] [path ...]

Fmt formats .ogo source files in the canonical style. Each path may be a file or a
directory, which is searched recursively. With no flags the formatted result goes
to standard output and no file is touched, as gofmt does.

	-l            list the files whose formatting differs
	-w            rewrite the files in place
	-exclude re   skip paths matching the regular expression
`,
	"loadp2": `usage: ogo loadp2 [loadp2 arguments]

Loadp2 hands its arguments to the embedded Propeller 2 loader unchanged, so
loadp2's own flag grammar applies rather than ogo's. Run it without arguments for
loadp2's usage.

The loader is built in; no separate loadp2 installation is needed.

Unlike "ogo run", this passthrough uses loadp2's own defaults, and the one that
matters is the read baud: loadp2 reads at 115200 where an ogo program writes at
230400, so its output arrives as rubbish bytes rather than text. Pass the matching
baud to see it:

	ogo loadp2 -b 230400 -t prog.binary

That is the whole fix, measured. The -f frequency is NOT part of it for a program
built here: such a program sets its own clock as it starts, so -f neither changes
what it runs at nor what its output looks like -- the same binary prints cleanly
with -b alone and rubbish without it, whatever -f says. -f still matters for a
binary that sets no clock of its own, which is why the flag is passed on. Choose an
ogo program's clock at build time with "ogo build --clock". "ogo run" supplies the
baud for you.

On Windows, run this from cmd.exe or PowerShell, not a Unix-emulation shell
(git-bash, MSYS2, Cygwin): the serial handshake is flaky there and the terminal's
exit key may not respond (use Ctrl-C).
`,
	"smith": `usage: ogo smith [-seed n]

Smith writes a random OctoGo program to standard output, for testing the compiler
against itself: it interprets the program as it generates it, so the program
carries an assertion of its own expected result and a compiled binary that fails
that assertion implicates the compiler.

A seed is reproducible: the same -seed writes the same program every time, which
is what makes a failing one worth reporting. Omitting it, or passing 0, seeds from
the clock instead.

	-seed n       seed the generator (0 uses the current time)
`,
	"test": `usage: ogo test [-c] [-p port] [--clock hz] [package]

Test builds the package together with its _test.ogo files and a generated runner,
loads the result on a connected Propeller 2, and reports what the tests printed.

A test is a function named Test<Something> taking a *testing.T, in a file whose
name ends _test.ogo. The testing package is imported by name and needs nothing on
disk. There is no Errorf -- formatting needs allocation this target does not have
-- so a test prints with the builtin println and calls t.Fail():

	import "testing"

	func TestPop(t *testing.T) {
		if got, ok := pop(); !ok || got != 3 {
			println("pop:", got, ok, "want 3 true")
			t.Fail()
		}
	}

Tests run ON THE BOARD and nowhere else. A host emulation would be faster and
would sometimes be wrong -- the two C compilers disagree about semantics, not only
about warnings -- and a test reporting "ok" from somewhere the program will never
run is worse than a test that did not run.

	-c          build the tests and do not run them, leaving <pkg>.test.binary
	            beside the package. It is what CI without a board can honestly do.
	-p port     serial port to load through; omitted lets the loader find one.
	--clock hz  the system clock to run the tests at, as for ogo build. A test
	            binary should take the clock the program it tests ships with:
	            running them at a different speed is how a timing bug hides.
	--xtal hz   the board's crystal (default 20MHz)

Exit status is 0 when every test passed and 1 when any failed.
`,
	"version": `usage: ogo version

Version prints the ogo version, the host platform and the Go toolchain that built
it -- what a bug report needs to identify a build.
`,
	"help": `usage: ogo help [command]

Help shows the command overview, or the detail for one command.
`,
}

// help writes the overview when given no command, or that command's detail. It
// reports false for an unknown command, leaving the diagnostic to the caller.
func help(w io.Writer, args []string) bool {
	if len(args) == 0 {
		usage(w)
		return true
	}
	text, ok := commandHelp[args[0]]
	if !ok {
		return false
	}
	fmt.Fprint(w, text)
	return true
}
