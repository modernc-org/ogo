#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# stmtprobe.sh sweeps one STATEMENT at a time: for each line read from stdin it
# writes a program made of HEAD, that line, and TAIL, then compares it with real
# Go as probe.sh does. It is how a position sweep is run -- the same declarations
# and the same printout around thirty spellings of a store, a range, a switch
# tag, a defer -- and the one-line verdicts make the odd one out easy to see.
#
# Usage: scripts/stmtprobe.sh DIR HEAD.ogo TAIL.ogo < statements
#   HEAD ends inside main's body (it opens `func main() {`), TAIL closes it and
#   usually prints the package counter the head's accessors bump. The statement
#   line is indented by one tab. Program N lives in DIR/pN.
#
# A program gcc refuses is also built for the target: a shape the host's C
# compiler rejects and flexcc accepts is the dangerous kind, since flexcc is
# silent about it (see CLAUDE.md, "A backend diagnostic fails the target-build
# tests"). That needs an `ogo` on PATH built from the tree being probed.
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)
dir=${1:?usage: stmtprobe.sh DIR HEAD.ogo TAIL.ogo < statements}
head=${2:?}
tail=${3:?}
mkdir -p "$dir"
dumpc=$(mktemp -d)/dumpc
(cd "$root" && go build -o "$dumpc" ./build/dumpc) || exit 1
n=0
while IFS= read -r stmt; do
	n=$((n+1)); d=$dir/p$n; rm -rf "$d"; mkdir -p "$d/twin"
	{ cat "$head"; printf '\t%s\n' "$stmt"; cat "$tail"; } > "$d/main.ogo"
	{ echo package main; echo; cat "$d/main.ogo"; } > "$d/twin/main.go"
	want=$(cd "$d/twin" > /dev/null && timeout 30 env GOARCH=386 go run main.go 2>&1 | tr '\n' ' ')
	"$dumpc" "$d" > "$d/host.c" 2> "$d/dump.err"
	if [ -s "$d/dump.err" ]; then
		echo "p$n $stmt => go=[$want] REFUSED: $(head -c 150 "$d/dump.err" | tr '\n' ' ')"
		continue
	fi
	if ! gcc -std=gnu11 -fwrapv -w -I "$root/internal/octogo/testdata/hostp2" -o "$d/host" "$d/host.c" -lpthread -lm > "$d/gcc.log" 2>&1; then
		if (cd "$d" > /dev/null && ogo build -o x.bin . > build.log 2>&1); then
			echo "p$n $stmt => go=[$want] GCC FAILED, FLEXCC BUILDS: $(grep -m1 error "$d/gcc.log")"
		else
			echo "p$n $stmt => go=[$want] gcc+flexcc fail: $(grep -m1 error "$d/gcc.log")"
		fi
		continue
	fi
	got=$(timeout 10 "$d/host" 2>&1 | tr '\n' ' ')
	[ "$got" = "$want" ] && s=ok || s=DIFF
	echo "p$n $stmt => $s go=[$want] host=[$got]"
done
rm -rf "$(dirname "$dumpc")"
