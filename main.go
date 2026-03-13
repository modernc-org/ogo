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

	"modernc.org/opt"
)

func fail(rc int, s string, args ...any) {
	s = fmt.Sprintf(s, args...)
	fmt.Fprintln(os.Stderr, s)
	os.Exit(rc)
}

func main() {
	set := opt.NewSet()
	var subCommand string
	var args []string
	if err := set.Parse(os.Args[1:], func(arg string) error {
		switch {
		case strings.HasPrefix(arg, "-"):
			fail(2, "unexpected flag: %s", arg)
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
	case
		"build",
		"fmt",
		"help",
		"smith",
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
	smith       output a random program for compiler testing
	test        test packages
	version     print Go version

Use "%s help <command>" for more information about a command.`, os.Args[0])
	}
}
