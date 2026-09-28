#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# fmtcmp.sh formats OctoGo sources with gofmt and with the CURRENT tree's `ogo
# fmt`, and names the files the two lay out differently. A source is Go once it
# is given a package clause, so gofmt is the formatter's oracle the way `go run`
# is the emitter's.
#
# `ogo fmt -l` is not this check: a program formatted with `ogo fmt` is in its
# layout already, so it lists nothing however far that layout is from gofmt's.
# p2-11's sources were, and eleven of thirty-six differed (2026-09-28).
#
# Usage: scripts/fmtcmp.sh [-v N] FILE.ogo...
#   -v N prints the first N lines of each difference, gofmt's lines marked "<"
#   and ogo fmt's ">", tabs shown as "→". A file gofmt refuses is counted as not
#   Go and skipped: a test of what the checker refuses, say.
#
# The exit status is 1 when any file differs or `ogo fmt` fails on one.
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)
show=0
if [ "${1:-}" = "-v" ]; then
	show=${2:?usage: fmtcmp.sh [-v N] FILE.ogo...}
	shift 2
fi
[ $# -gt 0 ] || { echo "usage: fmtcmp.sh [-v N] FILE.ogo..." >&2; exit 2; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
(cd "$root" && go build -o "$tmp/ogo" .) || exit 1
n=0 skipped=0 bad=0
for f in "$@"; do
	n=$((n + 1))
	{ echo 'package main'; echo; cat "$f"; } > "$tmp/x.go"
	if ! gofmt "$tmp/x.go" > "$tmp/want.go" 2> /dev/null; then
		skipped=$((skipped + 1))
		continue
	fi
	tail -n +3 "$tmp/want.go" > "$tmp/want.ogo"
	cp "$f" "$tmp/got.ogo"
	if ! "$tmp/ogo" fmt -w "$tmp/got.ogo" 2> "$tmp/err"; then
		echo "FMT FAIL $f"
		head -3 "$tmp/err"
		bad=$((bad + 1))
		continue
	fi
	if ! cmp -s "$tmp/want.ogo" "$tmp/got.ogo"; then
		bad=$((bad + 1))
		echo "DIFFER $f"
		if [ "$show" -gt 0 ]; then
			diff "$tmp/want.ogo" "$tmp/got.ogo" | head -"$show" | sed 's/\t/→/g'
		fi
	fi
done
echo "$n files, $skipped not Go, $bad differing"
[ "$bad" -eq 0 ]
