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
	go install -v .
	golint
	staticcheck

generate:
	go generate -v -x ./...

parser: lib/parser.go
	make -C lib parser.go

test:
	go test -timeout 24h -count=1 -failfast
	go build -v -o /dev/null
