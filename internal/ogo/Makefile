# Copyright 2026 The OctoGo Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

.PHONY:	all clean edit editor test generate

all:
	gofmt -l -s -w ..

clean:
	rm -f cpu.test mem.test *.out
	go clean

edit:
	@touch log
	@if [ -f "Session.vim" ]; then gvim -S & else gvim -p Makefile *.go & fi

editor: parser.go
	# gofmt -l -s -w ..
	go test -o /dev/null -c
	go build -o /dev/null extract_grammar.go
	golint
	staticcheck

generate:
	go generate -v -x

# Prerequisites:
#
# $ go install modernc.org/egg@latest
parser.go: ../../octogo.go
	go run extract_grammar.go -- ../../octogo.go > octogo.ebnf
	# egg -dbg -o parser.go -start SourceFile -package octogo octogo.ebnf
	egg -o parser.go -start SourceFile -package octogo octogo.ebnf
	sed -i \
		-e 's/"modernc.org\/scanner"//' \
		-e 's/scanner\.//g' \
		parser.go
	gofmt -l -s -w ..

test:
	go test -timeout 24h -count=1 -failfast
	go build -v -o /dev/null
