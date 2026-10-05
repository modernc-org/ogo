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

// TestCheckImportedQualifiedResult: a method of another package's type whose
// result that package spells with a qualifier of its own, `unsafe.Pointer`. Its
// name was read as the receiver package's, `hub.Pointer`, so `(*uint32)(blk.Addr(0))`
// was refused as a conversion of a hub.Pointer; and its Kind was read nowhere, the
// signature carried into this file failing on the qualified name, so `var n int =
// blk.Addr(0)` was taken where the function form, `hub.Addr(0)`, was refused.
func TestCheckImportedQualifiedResult(t *testing.T) {
	const lib = `import "unsafe"

type Block struct {
	Words [4]uintptr
}

func (b *Block) Addr(i int) unsafe.Pointer {
	return unsafe.Pointer(b.Words[i])
}

func Addr(i int) unsafe.Pointer { return nil }
`
	for _, test := range []struct {
		name, body string
		want       string // "" for a program Go takes
	}{
		{"conversion", `q := (*uint32)(blk.Addr(0))
	_ = q`, ""},
		{"store through", `*(*uint32)(blk.Addr(0)) = 7`, ""},
		{"into unsafe.Pointer", `var a unsafe.Pointer = blk.Addr(0)
	_ = a`, ""},
		{"method into int", `var n int = blk.Addr(0)
	_ = n`, "cannot use blk.Addr(0) of type unsafe.Pointer as type int"},
		{"function into int", `var n int = hub.Addr(0)
	_ = n`, "cannot use hub.Addr(0) of type unsafe.Pointer as type int"},
		{"arithmetic", `x := blk.Addr(0) + 1
	_ = x`, "operator + not defined on unsafe.Pointer and int"},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "import (\n\t\"unsafe\"\n\n\t\"hub\"\n)\n\nvar blk hub.Block\n\nfunc main() {\n\t" + test.body + "\n\t_ = unsafe.Pointer(nil)\n}\n"
			fsys := fstest.MapFS{
				"main.ogo":    &fstest.MapFile{Data: []byte(src)},
				"hub/hub.ogo": &fstest.MapFile{Data: []byte(lib)},
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
