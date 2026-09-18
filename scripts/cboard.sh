#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# cboard.sh compiles one C file with the in-process flexcc -- the backend `ogo build`
# uses, with the flags it passes -- loads it onto the board and prints what it
# writes to the serial line. It is how a reproducer in doc/ is re-measured after a
# backend regeneration, and how a new one is measured before it is written down.
#
# Usage: scripts/cboard.sh FILE.c [SECONDS] [PORT]
#
# The compile goes through a throwaway test dropped into internal/build while it
# runs (compileC is internal to the module); do not run it concurrently with a
# `go test` of that package.
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)
c=${1:?usage: cboard.sh FILE.c [SECONDS] [PORT]}
secs=${2:-5}
port=${3:-/dev/ttyUSB0}
c=$(cd "$(dirname "$c")" && pwd)/$(basename "$c")
if fuser -s "$port" 2>/dev/null; then
	echo "PORT BUSY:"
	fuser -v "$port"
	exit 1
fi
tmp=$(mktemp -d)
(cd "$root" && go build -o "$tmp/ogo" .) || exit 1
cat > "$root/internal/build/zz_cboard_test.go" <<'EOT'
package build

import (
	"os"
	"testing"
)

func TestZZCBoard(t *testing.T) {
	c, out := os.Getenv("ZZ_C"), os.Getenv("ZZ_OUT")
	if c == "" {
		t.Skip()
	}
	if _, err := compileC(c, out, os.Stdout, os.Stderr); err != nil {
		t.Fatal(err)
	}
}
EOT
(cd "$root" && ZZ_C="$c" ZZ_OUT="$tmp/prog.binary" go test ./internal/build -run TestZZCBoard -count=1 > "$tmp/build.log" 2>&1)
status=$?
rm -f "$root/internal/build/zz_cboard_test.go"
if [ $status -ne 0 ] || [ ! -s "$tmp/prog.binary" ]; then
	echo "BUILD FAILED:"
	cat "$tmp/build.log"
	rm -rf "$tmp"
	exit 1
fi
grep -v '^ok\|^PASS' "$tmp/build.log"
(sleep "$secs"; printf '\x1d') | timeout 90 "$tmp/ogo" loadp2 -t -NOEOF -p "$port" -b 230400 "$tmp/prog.binary" 2>&1 |
	tr -d '\r' | grep -v 'Entering terminal mode' | grep -v '^( '
rm -rf "$tmp"
