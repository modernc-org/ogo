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

// printVersion reports what a bug report needs to identify a build: the module
// version (or the VCS revision, for a build from source), the host platform and
// the Go toolchain that built it. The values come from the build info the Go
// toolchain stamps in, so a `go install modernc.org/ogo@latest` binary names its
// release while a local build names its commit.
func printVersion(w io.Writer) {
	version, revision, modified := "(devel)", "", false
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" {
			version = v
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
	// there is no module version at all.
	if version == "(devel)" && revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		version = revision
	}
	// A pseudo-version of a dirty tree already carries the marker.
	if modified && !strings.HasSuffix(version, "+dirty") {
		version += "+dirty"
	}
	fmt.Fprintf(w, "ogo version %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
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
		fail(1, "TODO: %v", subCommand)
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
	"build": `usage: ogo build [-o output] [--release] [--unchecked] [package | file.ogo ...]

Build compiles a package to a Propeller 2 binary.

A package is a directory. With no argument the current directory is built; with a
directory argument that directory is; either way every .ogo file in it is compiled
together, except _test.ogo files. Source files may also be named explicitly, in
which case they must all be in one directory.

The binary is written beside the package and named after its directory, except
when a single file is named, which keeps that file's name: ogo build x.ogo writes
x.binary. -o overrides the path.

Runtime checks for out-of-range indexing and division by zero are on by default.

	-o output     write the binary here
	--unchecked   omit the runtime checks
	--release     reboot the board on a panic instead of halting the cog
`,
	"run": `usage: ogo run [--release] [--unchecked] [package | file.ogo ...]

Run builds a package exactly as ogo build does, loads the binary onto a connected
Propeller 2 and opens a terminal on its serial output. It sets a precise 200 MHz
clock and reads at 230400 baud, so println output is readable out of the box (a
board with a non-standard crystal needs "ogo loadp2" with an explicit -f).

Press Ctrl-] to leave the terminal.

On Windows, run this from cmd.exe or PowerShell, not a Unix-emulation shell
(git-bash, MSYS2, Cygwin): the serial handshake is flaky there and the exit key
may not respond (use Ctrl-C).
`,
	"fmt": `usage: ogo fmt [-l] [-w] [-exclude regexp] [path ...]

Fmt formats .ogo source files in the canonical style. Each path may be a file or a
directory, which is searched recursively. With no flags the formatted result is
compared and nothing is written.

	-l            list the files whose formatting differs
	-w            rewrite the files in place
	-exclude re   skip paths matching the regular expression
`,
	"loadp2": `usage: ogo loadp2 [loadp2 arguments]

Loadp2 hands its arguments to the embedded Propeller 2 loader unchanged, so
loadp2's own flag grammar applies rather than ogo's. Run it without arguments for
loadp2's usage.

The loader is built in; no separate loadp2 installation is needed.

Unlike "ogo run", this passthrough uses loadp2's own defaults, which leave the P2
on its imprecise internal oscillator and read at 115200 -- so a program's println
output is garbled at every baud. To see it, set a real clock and the matching
baud, e.g. "ogo loadp2 -f 200000000 -b 230400 -t prog.binary" (200000000 assumes
the usual 20 MHz crystal). "ogo run" does this for you.

On Windows, run this from cmd.exe or PowerShell, not a Unix-emulation shell
(git-bash, MSYS2, Cygwin): the serial handshake is flaky there and the terminal's
exit key may not respond (use Ctrl-C).
`,
	"smith": `usage: ogo smith [-seed n]

Smith writes a random OctoGo program to standard output, for testing the compiler
against itself: it interprets the program as it generates it, so the program
carries an assertion of its own expected result and a compiled binary that fails
that assertion implicates the compiler.

	-seed n       seed the generator (0 uses the current time)

Note: generation is not yet reproducible from a seed.
`,
	"test": `usage: ogo test [package]

Test is not implemented yet. Files ending in _test.ogo are recognized as test
files and excluded from a build, but nothing runs them.
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
