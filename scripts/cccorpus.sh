#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# cccorpus.sh compiles every C file of the given directories with ONE flexcc, with
# the flags `ogo build` passes (-2), and writes a line per file, sorted:
#
#	NAME RC SHA
#
# NAME is the file's name without .c, RC flexcc's exit status, SHA the first 16 hex
# digits of the binary's sha256, or - when nothing was built. Two such lists, from
# two backends, joined:
#
#	LC_ALL=C join A B | awk '$2 != $4 || $3 != $5'
#
# name every program the two compile differently. That is how a backend
# regeneration is measured (see internal/generator.go): against a native build of
# the spin2cpp commit it was transpiled from, where no line may differ -- the
# transpile is FAITHFUL; against the backend it replaces, which says what the new
# pin changes and so which programs to run on the board; and against the same tree
# built for each other platform, where again no line may differ. The inputs are
# doc/ (the reproducers) and a scripts/dumpcorpus.sh dump (the run cases and the
# fuzzer's seeds): about 1600 programs, a few minutes.
#
# Usage: scripts/cccorpus.sh [-I INCDIR] [-k KEEPDIR] FLEXCC OUTFILE DIR...
#   FLEXCC  a flexcc binary: the tree's in-process one, `go build -o X
#           ./scripts/flexcc` (with GOOS/GOARCH for another platform's), or a
#           native build of spin2cpp, its build/flexcc
#   -I      the include tree, for a native flexcc only: its checkout's include/,
#           which is what the embedded tree is packed from. Where the headers live
#           does not reach the binary -- 188 programs compiled against the
#           checkout's and against the extracted embedded tree came out identical
#           (2026-09-21) -- so a native build needs no copy of the embedded one.
#   -k      keep each program's directory (prog.c, prog.p2asm, prog.binary, log)
#           as KEEPDIR/NAME, to diff the assembly of a program that differs
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
export LC_ALL=C # one collation on every machine, or join refuses a list sorted on another
usage="usage: cccorpus.sh [-I INCDIR] [-k KEEPDIR] FLEXCC OUTFILE DIR..."
inc=
keep=
while getopts I:k: opt; do
	case $opt in
	I) inc=$OPTARG ;;
	k) keep=$OPTARG ;;
	*)
		echo "$usage" >&2
		exit 2
		;;
	esac
done
shift $((OPTIND - 1))
[ $# -ge 3 ] || {
	echo "$usage" >&2
	exit 2
}
cc=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
out=$2
shift 2
[ -x "$cc" ] || {
	echo "cccorpus.sh: $cc is not an executable" >&2
	exit 2
}
if [ -n "$inc" ]; then
	inc=$(cd "$inc" && pwd) || exit 2
fi
if [ -n "$keep" ]; then
	work=$(mkdir -p "$keep" && cd "$keep" && pwd) || exit 2
else
	work=$(mktemp -d)
fi
# macOS has neither sha256sum nor timeout unless coreutils is installed.
if command -v sha256sum > /dev/null; then sha="sha256sum"; else sha="shasum -a 256"; fi
to=$(command -v timeout || command -v gtimeout || true)
[ -z "$to" ] || to="$to 300"
one() {
	f=$1
	n=$(basename "$f" .c)
	d=$work/$n
	rm -rf "$d"
	mkdir -p "$d" && cp "$f" "$d/prog.c" || return
	if [ -n "$inc" ]; then
		(cd "$d" && $to "$cc" -I "$inc" -2 -o prog.binary prog.c > log 2>&1)
	else
		(cd "$d" && $to "$cc" -2 -o prog.binary prog.c > log 2>&1)
	fi
	rc=$?
	h=-
	if [ $rc -eq 0 ] && [ -s "$d/prog.binary" ]; then
		h=$($sha < "$d/prog.binary" | cut -c1-16)
	fi
	echo "$n $rc $h"
	[ -n "$keep" ] || rm -rf "$d"
}
export -f one
export cc inc keep work sha to
for d in "$@"; do
	ls "$d"/*.c
done | xargs -P "$(getconf _NPROCESSORS_ONLN)" -I{} bash -c 'one {}' | sort > "$out"
[ -n "$keep" ] || rm -rf "$work"
echo "$(wc -l < "$out" | tr -d ' ') programs, $(awk '$2 != 0' "$out" | wc -l | tr -d ' ') not built: $out"
