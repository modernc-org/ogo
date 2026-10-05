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

// TestEmitCFloatConstIndex: a constant index or slice bound past an array's
// extent is refused whatever its spelling. An integral float, `a[1e3]`, was written
// as the integer it is and bounded by nothing -- the bound check read integer
// constants alone -- so the program panicked at run time, and read out of bounds in
// an unchecked build, where Go refuses it.
func TestEmitCFloatConstIndex(t *testing.T) {
	for _, test := range []struct {
		body string
		want string // "" for a program Go takes
	}{
		{"_ = a[1e3]", "index 1000 out of bounds [0:5]"},
		{"_ = h.v[1e3]", "index 1000 out of bounds [0:3]"},
		{"_ = m[0][1e3]", "index 1000 out of bounds [0:2]"},
		{"_ = a[2:9e0]", "index 9 out of bounds [0:6]"},
		{"_ = a[4.0]", ""},
		{"_ = a[1:4e0]", ""},
	} {
		t.Run(test.body, func(t *testing.T) {
			src := "type H struct{ v [3]int }\n\nvar a [5]int\n\nvar h H\n\nvar m [2][2]int\n\nfunc main() {\n\t" + test.body + "\n}\n"
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
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
