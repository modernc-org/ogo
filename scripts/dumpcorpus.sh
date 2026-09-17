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
#
# It drops a throwaway test file into internal/octogo while it runs and removes
# it after; do not run it concurrently with itself, and do not start a full
# `go test` of the package while it is running.
set -u
unset CDPATH # a set CDPATH makes cd echo the directory, doubling every $(cd ... && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:?usage: dumpcorpus.sh OUTDIR [SEEDS]}
seeds=${2:-400}
out=$(mkdir -p "$out" && cd "$out" && pwd)
rm -rf "$out"/*
tmp=$(mktemp -d)
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
ZZ_DIR=$out go test -run TestZZDumpCorpus -count=1 . 2>&1 | tail -1
rm -f zz_dumpcorpus_test.go
gen() {
	n=$1; d=$(mktemp -d)
	"$tmp/ogo" smith -seed "$n" > "$d/main.ogo" 2>/dev/null && "$tmp/dumpc" "$d" > "$out/smith$(printf %04d "$n").c" 2>/dev/null || rm -f "$out/smith$(printf %04d "$n").c"
	rm -rf "$d"
}
export -f gen; export tmp out
seq 1 "$seeds" | xargs -P 16 -I{} bash -c 'gen {}'
rm -rf "$tmp"
echo "$(ls "$out" | wc -l) files in $out"
