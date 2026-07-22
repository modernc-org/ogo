// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package loadp2 integrates the Parallax Propeller 2 loader
// (modernc.org/loadp2/lib, the original loadp2 C program transpiled to Go) into
// ogo, so a developer needs no separate loadp2 binary. It backs two things:
//
//   - the `ogo loadp2 <args>` verbatim passthrough (SubCommand), and
//   - the loader behind `ogo run` / `ogo test` (Load), once the emitter and
//     `ogo build` produce .binary files.
//
// loadp2 drives the process's real stdio, serial ports and controlling terminal
// and keeps global state; lib.Main locks the OS thread for the call and is not
// safe for concurrent use. Like the flexcc backend it is built for linux/amd64,
// windows/amd64 and darwin (amd64 + arm64); on other targets lib.Main reports the
// unsupported target and returns non-zero. On windows the serial/terminal commands
// (ogo run, ogo loadp2) need a native console -- cmd.exe or PowerShell, not
// git-bash/MSYS2/Cygwin.
package loadp2

import (
	"strconv"

	loadp2lib "modernc.org/loadp2/lib"
)

// SubCommand implements `ogo loadp2 <args>`: a verbatim passthrough to the
// loader. args is the raw argument tail (without a program name); it is handed
// to loadp2 untouched so loadp2's own flag grammar (-a/-9/-e, @ADDR=file load
// specs, ...) is never reinterpreted by ogo. The returned int is loadp2's exit
// status, which ogo exits with.
func SubCommand(args []string) int {
	return loadp2lib.Main(append([]string{"loadp2"}, args...))
}

// Options configures a load driven by `ogo run` / `ogo test`. The zero value
// loads Binary with loadp2's defaults (115200 baud, auto-detected port).
// DefaultUserBaud is the -b user baud rate Load uses when Options.UserBaud is 0.
// flexcc-emitted P2 programs print at 230400 (verified on hardware 2026-07-15),
// so this matches the emitted serial out of the box — unlike loadp2's own
// default of 115200, which garbles that output. Faster rates near 1 Mbps (e.g.
// 921600) also work in practice but are host/USB-adapter dependent, so 230400 is
// the portable default; callers override via Options.UserBaud.
const DefaultUserBaud = 230400

type Options struct {
	Binary   string   // path to the .binary to load (required)
	Port     string   // -p serial port; empty lets loadp2 auto-detect
	UserBaud int      // -b user baud rate; 0 = DefaultUserBaud (230400)
	Verbose  bool     // -v verbose loader output
	Quiet    bool     // -q quiet mode, watch for the program's exit sequence (ogo test)
	Terminal bool     // -t enter interactive terminal after load (ogo run)
	Extra    []string // additional raw loadp2 flags, appended after the binary
}

// Load builds the loadp2 argument list from o and runs the loader, returning
// loadp2's exit status. It is the single internal entry point for flashing a
// compiled program onto the board.
func Load(o Options) int {
	return loadp2lib.Main(buildArgs(o))
}

// buildArgs turns o into a loadp2 argv (including argv[0] "loadp2"). A zero
// Options.UserBaud resolves to DefaultUserBaud (230400), matching the baud of
// flexcc-emitted programs (verified on P2 hardware 2026-07-15).
//
// TODO: exact flag ordering (notably -t/-e/-a relative to the filespec) should
// be validated against real hardware when `ogo run`/`ogo test` are wired; the
// current order follows loadp2's documented usage.
func buildArgs(o Options) []string {
	args := []string{"loadp2"}
	if o.Port != "" {
		args = append(args, "-p", o.Port)
	}
	baud := o.UserBaud
	if baud == 0 {
		baud = DefaultUserBaud
	}
	args = append(args, "-b", strconv.Itoa(baud))
	if o.Verbose {
		args = append(args, "-v")
	}
	if o.Quiet {
		args = append(args, "-q")
	}
	if o.Terminal {
		args = append(args, "-t")
	}
	if o.Binary != "" {
		args = append(args, o.Binary)
	}
	return append(args, o.Extra...)
}
