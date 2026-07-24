#!/bin/sh
# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# release.sh builds the published preview binaries for every supported target and
# stages them for a GitHub Release. The whole module is CGO-free, so every target
# cross-compiles from a single host with no native toolchain -- one run produces all
# the zips plus a SHA256SUMS.
#
# Usage: scripts/release.sh [VERSION]
#   VERSION defaults to the exact tag at HEAD (git describe --tags), so the normal
#   flow is: commit, `git tag vX.Y.Z`, then `make release`. Each zip is
#   self-contained (the binary embeds flexcc and the P2 include tree) and carries
#   both licenses; the binary self-reports VERSION via `ogo version`.
#
# Output: build/release/<VERSION>/ containing ogo-<VERSION>-<goos>-<goarch>.zip for
# every target and a SHA256SUMS over them.

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
	VERSION=$(git describe --tags --exact-match 2>/dev/null || git describe --tags 2>/dev/null || echo dev)
fi

# The supported targets, matching the committed flexcc backends
# (internal/flexcc/ccgo_<goos>_<goarch>.go).
TARGETS='linux/amd64 linux/arm64 windows/amd64 darwin/arm64 darwin/amd64'

OUT="$ROOT/build/release/$VERSION"
rm -rf "$OUT"
mkdir -p "$OUT"

echo "building ogo $VERSION for: $TARGETS"
for t in $TARGETS; do
	goos=${t%/*}
	goarch=${t#*/}
	bin=ogo
	[ "$goos" = windows ] && bin=ogo.exe

	stage="$OUT/stage-$goos-$goarch"
	mkdir -p "$stage"

	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
		-o "$stage/$bin" .

	cp "$ROOT/LICENSE" "$stage/LICENSE"
	cp "$ROOT/internal/flexcc/LICENSE-flexprop" "$stage/LICENSE-flexprop"

	cat > "$stage/README.txt" <<EOF
ogo $VERSION -- $goos/$goarch (preview)

OctoGo is a Go-inspired language and compiler for the Parallax Propeller 2. This
binary is self-contained: it embeds the flexspin/flexcc C backend and the P2
include tree, and an in-process P2 loader, so no separate flexprop install is
needed. There is nothing to install -- run the binary from anywhere.

Quick start:
  ./$bin build hello.ogo      # compile hello.ogo to a P2 binary
  ./$bin run   hello.ogo      # compile and load it onto a connected P2
  ./$bin help                 # list subcommands
  ./$bin version              # should print: ogo version $VERSION $goos/$goarch

This is a preview: pre-v1, the language and CLI may still change. Report issues and
follow progress at https://gitlab.com/cznic/ogo (issues/PRs are also accepted at the
GitHub mirror https://github.com/modernc-org/ogo).

Licensing: ogo is BSD-licensed (see LICENSE). It embeds and redistributes
flexspin/flexcc; see LICENSE-flexprop for its license and attribution.
EOF

	name="ogo-$VERSION-$goos-$goarch.zip"
	( cd "$stage" && zip -q -X "$OUT/$name" "$bin" LICENSE LICENSE-flexprop README.txt )
	rm -rf "$stage"
	echo "  $name    ($(du -h "$OUT/$name" | cut -f1))"
done

( cd "$OUT" && sha256sum *.zip > SHA256SUMS )
echo
echo "artifacts in $OUT:"
ls -1 "$OUT"
