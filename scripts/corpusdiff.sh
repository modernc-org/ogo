#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# corpusdiff.sh compares two dumpcorpus.sh dumps by CONTENT: each program of the
# second dump is looked up among the first by the digest of its C with the
# temporaries renumbered (_ogo_t7 and _ogo_t9 are the same program), and the ones
# with no twin are listed. Matching by content rather than by file name means a
# run case inserted in the middle of the table shifts nothing; a listed case is
# either new since the first dump or emitted differently, and `diff` of the two
# files, through the same renumbering, says which.
#
# Usage: scripts/corpusdiff.sh BEFORE-DIR AFTER-DIR
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
before=${1:?usage: corpusdiff.sh BEFORE-DIR AFTER-DIR}
after=${2:?}
norm() { sed 's/_ogo_t[0-9]*/_ogo_tN/g' "$1" | md5sum | cut -d' ' -f1; }
declare -A seen
for f in "$before"/*.c; do
	seen[$(norm "$f")]=$(basename "$f")
done
n=0
for f in "$after"/*.c; do
	h=$(norm "$f")
	if [ -z "${seen[$h]:-}" ]; then
		echo "NO TWIN: $(basename "$f")"
		n=$((n+1))
	fi
done
echo "$n of $(ls "$after"/*.c | wc -l) programs have no twin in $before"
