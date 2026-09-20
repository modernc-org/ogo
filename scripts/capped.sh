#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# capped.sh runs a command under a memory cap and a hard timeout. Every script here
# that runs the COMPILER over a program written to find its faults runs it through
# this, because a compiler fault is not always a wrong answer: `type A struct{ A }`
# sent the emitter down an embedding without end at about 4 GB a second, the sweep
# that wrote the program ran the compiler bare, and the machine froze for four
# hours. A plain `timeout` does not help: by the time it fires the process is too
# deep in swap to die when told, and took another minute and a half to.
#
# Usage: scripts/capped.sh [-t SECONDS] [-m KILOBYTES] COMMAND [ARG...]
#   -t  wall-clock limit, enforced with SIGKILL (default 60)
#   -m  address-space limit, `ulimit -v` (default 4000000, about 3.8 GB). A Go
#       program cannot start its threads under 1.5 GB (`pthread_create failed`),
#       and under this one a runaway dies within a second with its stack on
#       stderr -- which is the diagnosis.
#
# Cap the compiler, not the build of it: `go build` and `go run` want more address
# space than this, so the scripts build dumpc first and run the binary through here.
#
# The exit status is the command's, 137 when the timeout killed it. For dumpc 0 is
# a program compiled, 1 a program REFUSED, and anything else -- 2 is a Go panic or
# a fatal error -- is a COMPILER FAULT. The scripts report it as one and never as a
# refusal: a crash that counts as "refused" agrees with Go about every program Go
# rejects, which is how the fault above went unnoticed in the sweep that found it.
set -u
secs=60
kb=4000000
while getopts t:m: opt; do
	case $opt in
	t) secs=$OPTARG ;;
	m) kb=$OPTARG ;;
	*)
		echo "usage: capped.sh [-t SECONDS] [-m KILOBYTES] COMMAND [ARG...]" >&2
		exit 2
		;;
	esac
done
shift $((OPTIND - 1))
[ $# -ne 0 ] || {
	echo "usage: capped.sh [-t SECONDS] [-m KILOBYTES] COMMAND [ARG...]" >&2
	exit 2
}
ulimit -v "$kb" || exit 2
exec timeout -s KILL "$secs" "$@"
