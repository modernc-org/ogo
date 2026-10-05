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

// TestCheckFieldMethodAcrossPackages: a method called on a FIELD of another
// package's struct, `f.ID.Node()` for an `f *a.Frame`. The field's type name was
// answered only where it was this package's (fieldTypeName), so the method was
// asked of the Kind the type is defined over, "type uint16 has no method Node"; and
// the call walk gave up at a variable of another package's type (callChainWalk), so
// once found, the method's arguments and result were asked nothing.
func TestCheckFieldMethodAcrossPackages(t *testing.T) {
	const lib = `type ID uint16

func (id ID) Node() uint8 { return uint8(id & 0x7f) }

func (id ID) With(o ID) ID { return id | o }

func (id *ID) Set(v uint16) { *id = ID(v) }

type Inner struct{ ID ID }

type Frame struct {
	ID ID
	In Inner
	P  *Inner
}

var G = Frame{ID: 0x83, P: &Inner{ID: 5}}
`
	for _, test := range []struct {
		name, body string
		want       string // "" for a program Go takes
	}{
		{"pointer param", `func h(f *a.Frame) uint16 { return uint16(f.ID.Node()) }`, ""},
		{"value param", `func h(f a.Frame) uint16 { return uint16(f.ID.Node()) }`, ""},
		{"inner", `func h(f *a.Frame) uint16 { return uint16(f.In.ID.Node()) }`, ""},
		{"pointer field", `func h(f *a.Frame) uint16 { return uint16(f.P.ID.Node()) }`, ""},
		{"argument of its type", `func h(f *a.Frame) uint16 { return uint16(f.ID.With(a.ID(1))) }`, ""},
		{"pointer method", `func h(f *a.Frame) uint16 { f.ID.Set(9); return uint16(f.ID) }`, ""},
		{"missing", `func h(f *a.Frame) uint16 { return uint16(f.ID.Nosuch()) }`, "has no method Nosuch"},
		{"too many", `func h(f *a.Frame) uint16 { return uint16(f.In.ID.Node(3)) }`, "too many arguments in call to Node"},
		{"result", `func h(f *a.Frame) uint16 { var s string = f.P.ID.Node(); _ = s; return 0 }`, "cannot use f.P.ID.Node() of type uint8 as type string"},
		{"argument", `func h(f *a.Frame) uint16 { f.ID.Set("x"); return 0 }`, `cannot use "x" of type string as type uint16`},
		{"other type", `type MyID uint16

func h(f *a.Frame) uint16 { return uint16(f.ID.With(MyID(1))) }`, "cannot use MyID(1) of type MyID as type a.ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "import \"a\"\n\n" + test.body + "\n\nfunc main() {\n\tf := a.G\n\tprintln(h(&f))\n}\n"
			if strings.Contains(test.body, "f a.Frame)") {
				src = strings.Replace(src, "h(&f)", "h(f)", 1)
			}
			fsys := fstest.MapFS{
				"main.ogo": &fstest.MapFile{Data: []byte(src)},
				"a/a.ogo":  &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked(), Inline())
			}
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}
