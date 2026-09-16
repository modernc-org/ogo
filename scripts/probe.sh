#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# probe.sh compares one OctoGo program with real Go. It is the harness behind the
# domain-program probes: write a program a user would write, run its Go twin, run
# it as OctoGo on the host through the test shim, and diff the two outputs. A
# refusal, a host C compiler error, a panic and a wrong answer each print as their
# own verdict, so a sweep of many probes reads as one line per program.
#
# Usage: scripts/probe.sh DIR [SED-EXPRESSION]
#   DIR holds main.ogo (no package clause -- OctoGo has none). The twin is
#   DIR/twin/main.go, the program with "package main" in front, built for
#   GOARCH=386 so int is 32 bits, as on the P2. SED-EXPRESSION rewrites the twin
#   only, for the two lines that cannot read the same in both languages: a
#   channel is DECLARED here, `var out chan T`, where Go must make it --
#     scripts/probe.sh d 's/^var out chan int$/var out = make(chan int)/'
#   Both sides run under a timeout, since a fixture mistake (a nil channel in Go,
#   a busy-wait that never ends) hangs rather than fails.
#
# Verdicts: MATCH, DIFFER (both outputs shown), REFUSED (the compiler's first
# line), GCC FAIL (the first error: an unused variable under -Werror is usually
# the fixture's; a real one is a bug). The host build uses exactly the flags of
# TestEmitCRun, so a program that passes here is a run case waiting to be pinned.
#
# What a good probe counts: the calls. Give every accessor a side effect on a
# package counter and print the counter beside the values, so an operand
# evaluated twice, or at the return instead of at the defer, shows as a number.
# Fixture traps met so far: a value receiver on a struct holding an array and a
# struct holding an array as a channel element are refused by the ABI wall, not
# by a gap; `append([]int{}, ...)` has no capacity here; an unused package
# variable fails the host build; at most seven goroutines are live at once; and
# main must not write what a worker reads, or the "miscompile" is a race.
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)
d=${1:?usage: probe.sh DIR [SED-EXPRESSION]}
d=$(cd "$d" && pwd)
expr=${2:-}
name=$(basename "$d")
mkdir -p "$d/twin"
{ echo 'package main'; echo; cat "$d/main.ogo"; } | sed -e "${expr:-s/^\$/&/}" > "$d/twin/main.go"
(cd "$d/twin" && timeout 30 env GOARCH=386 go run main.go > ../go.out 2>&1; echo "exit=$?" >> ../go.out)
(cd "$root" && go run ./build/dumpc "$d" > "$d/host.c" 2> "$d/dump.err")
if [ ! -s "$d/host.c" ] || [ -s "$d/dump.err" ]; then
	echo "== $name: REFUSED: $(head -1 "$d/dump.err")"
	exit 0
fi
if ! gcc -std=gnu11 -fwrapv -Wall -Werror -Wno-format -Wno-unused-function \
	-I "$root/internal/octogo/testdata/hostp2" -o "$d/host" "$d/host.c" -lpthread -lm > "$d/gcc.log" 2>&1; then
	echo "== $name: GCC FAIL: $(grep -m1 error "$d/gcc.log")"
	exit 0
fi
# In a subshell so that a panic's abort is noted in host.out, not on the terminal.
(timeout 30 "$d/host"; echo "exit=$?") > "$d/host.out" 2>&1
if diff "$d/go.out" "$d/host.out" > /dev/null; then
	echo "== $name: MATCH"
else
	echo "== $name: DIFFER"
	echo "-- go:"
	cat "$d/go.out"
	echo "-- host:"
	cat "$d/host.out"
fi
