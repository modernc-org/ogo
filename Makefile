# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

.PHONY:	all board clean edit editor test generate release

all:

clean:
	rm -f cpu.test mem.test *.out
	go clean

edit:
	@touch log
	@if [ -f "Session.vim" ]; then gvim -S & else gvim -p Makefile *.go & fi

editor: parser
	gofmt -l -s -w .
	go test -o /dev/null -c
	go install -v
	golint
	staticcheck

generate:
	go generate -v -x ./...

parser: internal/octogo/parser.go
	make -C internal/octogo parser.go

test:
	gofmt -l -s -w .
	go install
	ogo fmt -l -w --exclude='\/testdata\/' .
	go test -timeout 24h -count=1 -failfast ./...

# board runs the emitRunCases table on a real Propeller 2 board (ogo build ->
# flexcc -> loadp2), checking each program's serial output. Needs a connected P2
# on OGO_BOARD_PORT (default /dev/ttyUSB0) and the user in the dialout group. It
# is separate from `test` because it needs hardware; `go test ./...` skips it.
board:
	OGO_BOARD_PORT=$${OGO_BOARD_PORT:-/dev/ttyUSB0} go test -v -count=1 -timeout 10m -run TestOnBoard ./internal/octogo/

# release builds the self-contained preview binaries for every supported target
# (CGO-free, so all cross-compile from one host) and stages them under
# build/release/<version>/ as one zip per target plus SHA256SUMS, ready to upload to
# a GitHub Release. VERSION defaults to the tag at HEAD; override with `make release
# VERSION=vX.Y.Z`. Tag first, so the binaries self-report the release.
release:
	scripts/release.sh $(VERSION)
