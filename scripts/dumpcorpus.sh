#!/bin/bash
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# dumpcorpus.sh writes the C the CURRENT tree emits for every run case of the
# emitter test table (caseNNN.c, numbered by position) and for fuzzer seeds 1..N
# (smithNNNN.c). Two dumps, one before an emitter change and one after, compared
# with corpusdiff.sh, say exactly which programs the change touched -- and a
# change meant for one shape that touches five hundred programs is a change to
# look at again before the suite is even run. A case the compiler refuses writes
# caseNNN.err instead.
#
# Usage: scripts/dumpcorpus.sh OUTDIR [SEEDS]   (SEEDS defaults to 400)
#   JOBS=N sets how many seeds are compiled at once (default half the cores).
#
# Every compiler run goes through capped.sh, as every sweep's must: the run cases
# in a test binary built first and run capped, each seed's generation and compile
# on its own. A seed whose compiler FAULTED -- neither compiled nor refused, a
# panic or a run the cap killed -- writes smithNNNN.fault with the status, and the
# count is printed at the end; it used to be deleted like a refusal, and so was
# indistinguishable from one.
#
# It drops a throwaway test file into internal/octogo while it builds and removes
# it when it exits, interrupted or not; do not run it concurrently with itself, and
# do not start a full `go test` of the package while it is running.
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:?usage: dumpcorpus.sh OUTDIR [SEEDS]}
seeds=${2:-400}
jobs=${JOBS:-$((($(nproc) + 1) / 2))}
out=$(mkdir -p "$out" && cd "$out" && pwd)
rm -rf "$out"/*
tmp=$(mktemp -d)
trap 'rm -f "$root/internal/octogo/zz_dumpcorpus_test.go"; rm -rf "$tmp"' EXIT
trap 'exit 130' INT # so that EXIT runs: a fatal signal nothing traps skips it
trap 'exit 143' TERM
(cd "$root" && go build -o "$tmp/dumpc" ./scripts/dumpc && go build -o "$tmp/ogo" .) || exit 1
cd "$root/internal/octogo" && cat > zz_dumpcorpus_test.go <<'EOT'
package octogo

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestZZDumpCorpus(t *testing.T) {
	dir := os.Getenv("ZZ_DIR")
	if dir == "" {
		t.Skip()
	}
	for i, test := range emitRunCases {
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(test.src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		if err := EmitC(pkg, &buf, Checked()); err != nil {
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("case%03d.err", i)), []byte(err.Error()), 0o644)
			continue
		}
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("case%03d.c", i)), buf.Bytes(), 0o644)
	}
}
EOT
go test -c -o "$tmp/octogo.test" . || exit 1
rm -f zz_dumpcorpus_test.go
ZZ_DIR=$out "$root/scripts/capped.sh" -t 1200 "$tmp/octogo.test" -test.run TestZZDumpCorpus -test.count=1 > "$tmp/cases.log" 2>&1
rc=$?
if [ $rc -ne 0 ]; then
	echo "RUN CASES: COMPILER FAULT ($rc):"
	tail -20 "$tmp/cases.log"
fi
gen() {
	n=$1
	d=$(mktemp -d)
	f=$out/smith$(printf %04d "$n")
	"$root/scripts/capped.sh" -t 30 "$tmp/ogo" smith -seed "$n" > "$d/main.ogo" 2> "$d/smith.err"
	rc=$?
	if [ $rc -ne 0 ]; then
		{ echo "smith: status $rc"; head -20 "$d/smith.err"; } > "$f.fault"
		rm -rf "$d"
		return
	fi
	"$root/scripts/capped.sh" -t 30 "$tmp/dumpc" "$d" > "$f.c" 2> "$d/dumpc.err"
	rc=$?
	case $rc in
	0) ;;
	1) rm -f "$f.c" ;; # refused
	*)
		rm -f "$f.c"
		{ echo "dumpc: status $rc"; head -20 "$d/dumpc.err"; } > "$f.fault"
		;;
	esac
	rm -rf "$d"
}
export -f gen
export tmp out root
seq 1 "$seeds" | xargs -P "$jobs" -I{} bash -c 'gen {}'
faults=$(find "$out" -name '*.fault' | wc -l)
echo "$(ls "$out" | wc -l) files in $out, $faults of them compiler faults (*.fault)"
