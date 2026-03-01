.PHONY:	all clean edit editor test generate

all:

clean:
	rm -f cpu.test mem.test *.out
	go clean

edit:
	@touch log
	@if [ -f "Session.vim" ]; then gvim -S & else gvim -p Makefile ../doc.go *.go & fi

editor: parser.go
	gofmt -l -s -w ..
	go test -o /dev/null -c
	go build -o /dev/null extract_grammar.go
	go build -o /dev/null generator.go
	go install -v ..
	golint
	staticcheck

generate:
	go generate -v -x

parser.go: ../doc.go
	go run extract_grammar.go -- ../doc.go > octogo.ebnf
	egg -o parser.go -start SourceFile -package octogo octogo.ebnf

test:
	go test -timeout 24h -count=1 -failfast
	go build -v -o /dev/null
