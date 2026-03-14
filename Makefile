# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

.PHONY:	all clean edit editor test generate

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

parser: internal/ogo/parser.go
	make -C internal/ogo parser.go

test:
	go test -timeout 24h -count=1 -failfast ./...
	go build -v -o /dev/null
