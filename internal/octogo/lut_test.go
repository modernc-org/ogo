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

// TestEmitCLUT pins p2.ReadLUT and p2.WriteLUT: the helpers they call are defined
// where a program calls one and nowhere else, each the one RDLUT or WRLUT marked for
// the backend to inline, with the range check a checked build adds and an unchecked
// one leaves out; and a constant address past the half of the LUT the program may
// use is refused where it is written, checks or no checks. The run cases "the LUT
// RAM of each cog" and "a LUT address out of range" hold what they do on the host
// and on the board.
func TestEmitCLUT(t *testing.T) {
	emit := func(t *testing.T, src string, opts ...EmitOption) (string, error) {
		t.Helper()
		pkg, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}})
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		err = EmitC(pkg, &buf, opts...)
		return buf.String(), err
	}
	const uses = `import "p2"

func main() {
	var a uint32 = 7
	p2.WriteLUT(a, 42)
	println(p2.ReadLUT(a))
}
`
	c, err := emit(t, uses, Checked())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"static unsigned ogo_rdlut(unsigned a) __attribute__((inline)) {",
		"\t\trdlut v, a\n",
		"\t\twrlut v, a\n",
		`if (a > 255) ogo_panic("LUT address out of range");`,
		"ogo_wrlut(a, 42);",
		"ogo_rdlut(a)",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("checked: missing %q in\n%s", want, c)
		}
	}
	if c, err = emit(t, uses); err != nil {
		t.Fatal(err)
	} else if strings.Contains(c, "LUT address out of range") {
		t.Errorf("unchecked: a range check in\n%s", c)
	}

	c, err = emit(t, `import "p2"

func main() { println(p2.GetCt() > 0) }
`, Checked())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c, "ogo_rdlut") {
		t.Errorf("the LUT helpers in a program not calling them:\n%s", c)
	}

	for _, call := range []string{"p2.WriteLUT(256, 1)", "println(p2.ReadLUT(300))"} {
		_, err := emit(t, "import \"p2\"\n\nfunc main() {\n\t"+call+"\n}\n")
		if err == nil || !strings.Contains(err.Error(), "out of range [0:256]") {
			t.Errorf("%s: got %v, want the address refused", call, err)
		}
	}
}
