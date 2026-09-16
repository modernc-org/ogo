#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# board.sh builds one program for the P2 with the CURRENT tree's compiler, loads
# it, and captures what it prints over the serial line, for the probes that must
# be seen on hardware: flexcc and gcc have disagreed on semantics before, so a
# host MATCH from probe.sh is a run case candidate and a board match is the
# verdict. The installed `ogo` is not used, since it may predate the change under
# test; the compiler is built from the tree into a temporary directory.
#
# Usage: scripts/board.sh DIR OUTFILE [SECONDS] [PORT]
#   DIR holds the program, OUTFILE receives the captured lines, SECONDS is how
#   long to listen (default 8), PORT defaults to /dev/ttyUSB0. A stale loader
#   holding the port is reported rather than fought: `fuser -v PORT`, then kill
#   it -- a killed board test orphans its `ogo loadp2`, which then holds the port
#   so every later run reads nothing.
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)
d=${1:?usage: board.sh DIR OUTFILE [SECONDS] [PORT]}
out=${2:?}
secs=${3:-8}
port=${4:-/dev/ttyUSB0}
d=$(cd "$d" && pwd)
if fuser -s "$port" 2>/dev/null; then
	echo "PORT BUSY:"
	fuser -v "$port"
	exit 1
fi
tmp=$(mktemp -d)
(cd "$root" && go build -o "$tmp/ogo" .) || exit 1
if ! (cd "$d" && "$tmp/ogo" build -o "$d/prog.binary" . > "$d/build.log" 2>&1); then
	echo "BUILD FAILED:"
	cat "$d/build.log"
	rm -rf "$tmp"
	exit 1
fi
[ -s "$d/build.log" ] && { echo "BACKEND SAID:"; cat "$d/build.log"; }
(sleep "$secs"; printf '\x1d') | timeout 90 "$tmp/ogo" loadp2 -t -NOEOF -p "$port" -b 230400 "$d/prog.binary" 2>&1 |
	tr -d '\r' | grep -v 'Entering terminal mode' | grep -v '^( ' > "$out"
rm -rf "$tmp"
wc -l "$out"
