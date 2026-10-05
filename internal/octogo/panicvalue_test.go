// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmitCPanicValue: panic of a value that is no plain string (emitPanicValue).
// The run cases hold what each prints; this holds what is refused -- a struct, an
// array and a slice, of which Go writes only the address of a copy -- and that a
// plain string keeps ogo_panic.
func TestEmitCPanicValue(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       string // a substring of the C, or of the error when refused
		refused    bool
	}{
		{"string", `panic("x")`, `ogo_panic((ogo_lit_0).str)`, false},
		{"int", `panic(5)`, `ogo_panic_end();`, false},
		{"error", `var e error; panic(e)`, `printf("panic: "); ogo_panicv_`, false},
		{"nil", `panic(nil)`, `panic called with nil argument`, false},
		{"pointer", `var p *P; panic(p)`, `printf("panic: (*main.P) ");`, false},
		{"struct", `panic(P{1})`, `panic of a value of type main.P is not supported`, true},
		{"array", `var a [2]int; panic(a)`, `panic of a value of type [2]int is not supported`, true},
		{"slice", `var s []int; panic(s)`, `panic of a value of type []int is not supported`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "type P struct{ n int }\n\nfunc main() { " + test.body + " }\n"
			pkg, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}})
			if err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			err = EmitC(pkg, &b, Checked(), Inline())
			switch {
			case test.refused && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			case !test.refused && err != nil:
				t.Fatalf("refused: %v", err)
			case !test.refused && !strings.Contains(b.String(), test.want):
				t.Fatalf("the C lacks %q:\n%s", test.want, b.String())
			}
		})
	}
}
