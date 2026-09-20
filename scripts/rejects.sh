#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# rejects.sh asks what an INCORRECT program earns. The other probes ask what a
# correct program does; this is the direction that fails silently, since a mistake
# the compiler accepts reaches the C compiler, which reports it about generated
# code or does not report it at all. Every program under the given directories goes
# through Go and through the tree's compiler, and a row is printed wherever the two
# disagree -- the two-column shape, what Go says and what this says.
#
# Usage: scripts/rejects.sh [-v] PARENT...
#   Every PARENT/NAME/main.ogo is a row (no package clause -- OctoGo has none; the
#   twin is NAME/twin/main.go, as in probe.sh). Write a batch as about thirty small
#   programs, each wrong in ONE way, and a few right ones beside them as controls.
#   -v prints every row, with both first lines: two refusals can agree for different
#   reasons, and one of them may be hiding the check that is missing.
#   DUMPC=/path/to/dumpc uses a compiler built elsewhere -- an older commit's, to
#   count what a sweep found before its fixes -- instead of building the tree's.
#
# Go's verdict is `go build` for GOARCH=386, not `go vet`: a program vet warns about
# is a program Go compiles. This compiler's is dumpc's exit status, run through
# capped.sh: 0 ACCepted, 1 REJected, and anything else a compiler FAULT -- a panic,
# a fatal error, a runaway killed by the cap. The first version of this sweep took
# any output on stderr for a refusal, so a compiler that crashed AGREED with Go; the
# program that froze the machine it ran on was counted as one more agreement.
#
# A row is not yet a bug: `new`, maps, len/cap of a channel and a VALUE stored in an
# interface differ BY DESIGN (CLAUDE.md has the list). A FAULT always is one, and
# the exit status is 1 when there is any.
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)
verbose=
if [ "${1:-}" = -v ]; then
	verbose=1
	shift
fi
[ $# -ne 0 ] || {
	echo "usage: rejects.sh [-v] PARENT..." >&2
	exit 2
}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
dumpc=${DUMPC:-}
if [ -z "$dumpc" ]; then
	dumpc=$tmp/dumpc
	(cd "$root" && go build -o "$dumpc" ./scripts/dumpc) || exit 1
fi
n=0
agree=0
differ=0
fault=0
for parent in "$@"; do
	for src in "$parent"/*/main.ogo; do
		[ -f "$src" ] || continue
		d=$(cd "$(dirname "$src")" && pwd)
		name=$(basename "$(dirname "$d")")/$(basename "$d")
		n=$((n + 1))
		mkdir -p "$d/twin"
		{
			echo 'package main'
			echo
			cat "$d/main.ogo"
		} > "$d/twin/main.go"
		gomsg=$(cd "$d/twin" && GOARCH=386 go build -o /dev/null main.go 2>&1)
		if [ $? -eq 0 ]; then g=ACC; else g=REJ; fi
		gomsg=$(echo "$gomsg" | grep -av '^#' | head -1)
		"$root/scripts/capped.sh" "$dumpc" "$d" > "$d/host.c" 2> "$d/dump.err"
		case $? in
		0) o=ACC ;;
		1) o=REJ ;;
		*) o=FAULT ;;
		esac
		omsg=$(head -1 "$d/dump.err" | cut -c1-100)
		if [ $o = FAULT ]; then
			fault=$((fault + 1))
		elif [ $g = $o ]; then
			agree=$((agree + 1))
			[ -n "$verbose" ] || continue
		else
			differ=$((differ + 1))
		fi
		printf '%-34s go:%-3s ogo:%-5s %s\n' "$name" $g $o "$omsg"
		[ -z "$verbose" ] || printf '%-34s %s\n' '' "go: ${gomsg#./main.go:}"
	done
done
echo "-- $n programs: $agree agree, $differ differ, $fault compiler faults"
[ $fault -eq 0 ]
