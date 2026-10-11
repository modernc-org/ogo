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

// TestCheckIfaceLitAcrossPackages asks what an interface WRITTEN OUT in another
// package's declaration -- a parameter, a result, a field, a type switch's case --
// asks of a value from this one: its method set, resolved where it was written
// (ifaceLitName records the scope), as a declared interface's is.
func TestCheckIfaceLitAcrossPackages(t *testing.T) {
	const lib = `type T struct{ n int }

func (t *T) Code() int { return t.n }

func Take(v interface{ Code() int }) int { return v.Code() }

func Kind(x any) int {
	switch v := x.(type) {
	case interface{ Code() int }:
		return v.Code()
	}
	return -1
}

func Give() interface{ Code() int } { return &GT }

var GT = T{9}

type H struct{ C interface{ Code() int } }
`
	const head = `import "lib"

type E struct{ n int }

func (e *E) Code() int { return e.n }

type B struct{ n int }

func (b *B) Code() uint32 { return uint32(b.n) }

var ge = E{4}

var gb = B{5}

`
	for _, test := range []struct {
		name, body, refused string
	}{
		{"each position", `func main() {
	h := lib.H{C: &ge}
	println(lib.Take(&ge), lib.Kind(&ge), lib.Kind(&lib.GT), lib.Give().Code(), h.C.Code(), gb.n)
}`, ""},
		{"an argument of another signature", `func main() { println(lib.Take(&gb), ge.n) }`,
			"cannot use &gb (variable of type *B) as interface{Code() int} value in argument to Take: B does not implement interface{Code() int} (wrong type for method Code)"},
		{"a field of another signature", `func main() { h := lib.H{C: &gb}; println(h.C != nil, ge.n) }`,
			"as interface{Code() int} value in struct literal: B does not implement"},
		{"a result's method called with too many arguments", `func main() { println(lib.Give().Code(1), ge.n, gb.n) }`,
			"too many arguments in call to Code"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte(head + test.body + "\n")},
				"lib/lib.ogo": &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refused == "" && err != nil:
				t.Errorf("refused: %v", err)
			case test.refused != "" && err == nil:
				t.Errorf("taken, want %q", test.refused)
			case test.refused != "" && !strings.Contains(err.Error(), test.refused):
				t.Errorf("got %v, want %q", err, test.refused)
			}
		})
	}
}
