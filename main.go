// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command ogo is a compiler for the OctoGo programming language. OctoGo
// brings Native Go-like Concurrency for the Parallax Propeller 2.
package main // import "modernc.org/octogo"

import (
	"fmt"
	"os"
	"strings"

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
	case
		"build",
		"help",
		"run",
		"test",
		"version":

		fail(1, "TODO: %v", subCommand)
	default:
		fail(2, `ogo is a tool for managing OctoGo source code.

Usage:

	ogo <command> [arguments]

The commands are:

	build       compile packages and dependencies
	fmt         reformat source files
	loadp2      load a program onto a Propeller 2 board (loadp2 passthrough)
	run         compile and run a program on a connected board
	smith       output a random program for compiler testing
	test        test packages
	version     print Go version

Use "%s help <command>" for more information about a command.`, os.Args[0])
	}
}
