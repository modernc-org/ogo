// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"io"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmitCSummaryFieldNames: a field read out of a local that holds a parameter as
// a part is the parameter only where the field can hold a reference. The summaries
// read shapes, so the field's NAME decides: one no struct of the program gives a
// reference type holds nothing (fieldNameMayCarry). The controls keep their
// refusals: the field that holds the parameter, and a name another struct gives a
// pointer.
func TestEmitCSummaryFieldNames(t *testing.T) {
	const head = `type Doc struct{ N int }

type parser struct {
	pos int
	doc *Doc
}

var last int

var lastp *Doc

func keep(n int) { last = n }

func keepp(d *Doc) { lastp = d }

`
	const tail = `
func run() {
	var d Doc
	Parse(&d)
	println(d.N, last)
}

func main() {
	run()
}
`
	for _, test := range []struct {
		name, src string
		refused   bool
	}{
		{"int field", `func Parse(doc *Doc) { p := parser{doc: doc}; keep(p.pos) }`, false},
		{"int field held", `func Parse(doc *Doc) { p := parser{pos: 3, doc: doc}; n := p.pos; keep(n) }`, false},
		{"int field deeper", `func Parse(doc *Doc) { p := parser{doc: doc}; keep(p.doc.N) }`, false},
		{"pointer field", `func Parse(doc *Doc) { p := parser{doc: doc}; keepp(p.doc) }`, true},
		{"name another struct points with", `type other struct{ pos *int }

func Parse(doc *Doc) { p := parser{doc: doc}; keep(p.pos) }`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(head + test.src + "\n" + tail)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked(), Inline())
			}
			switch {
			case test.refused && (err == nil || !strings.Contains(err.Error(), "local variable d to Parse")):
				t.Fatalf("got %v, want a lifetime refusal", err)
			case !test.refused && err != nil:
				t.Fatalf("refused: %v", err)
			}
		})
	}
}
