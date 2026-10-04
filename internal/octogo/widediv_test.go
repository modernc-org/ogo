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

// TestEmitCWideConstUnsigned holds, off the board, the spelling the board needs: an
// untyped constant past 32 bits on the left of / or % with a uint64 is written
// unsigned. The target's compiler types such an operation by its left operand
// (flexprop#114), so `408166956050LL / u` divided signed there and right on the
// host, which takes C's rules: only the C text can show it here.
func TestEmitCWideConstUnsigned(t *testing.T) {
	const src = `type D uint64

const big = 408166956050

const huge = 1 << 63

func main() {
	var u uint64 = 18446744073709551615
	var d D = 7
	println(big/u, big%d, huge/u, (big+1)/d, huge%d)
}
`
	pkg, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := EmitC(pkg, &buf, Checked()); err != nil {
		t.Fatal(err)
	}
	c := buf.String()
	for _, want := range []string{"408166956050ULL / ", "408166956050ULL % ", "9223372036854775808ULL / ", "408166956051ULL / ", "9223372036854775808ULL % "} {
		if !strings.Contains(c, want) {
			t.Errorf("no %q in the C", want)
		}
	}
	if strings.Contains(c, "LL / ") && strings.Contains(c, "408166956050LL") {
		t.Errorf("a constant is spelled signed:\n%s", c)
	}
}
