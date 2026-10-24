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

// TestEmitCMathReach pins what of math is emitted: the functions a program
// reaches -- by a call, as a value, from another package, through the body of one
// reached -- and the tables those read, and nothing else.
func TestEmitCMathReach(t *testing.T) {
	emit := func(files map[string]string) string {
		fs := fstest.MapFS{}
		var names []string
		for n, src := range files {
			fs[n] = &fstest.MapFile{Data: []byte(src)}
			if !strings.Contains(n, "/") {
				names = append(names, n)
			}
		}
		pkg, err := Build(-1, names, fs)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := EmitC(pkg, &b, Checked()); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	c := emit(map[string]string{"main.ogo": "import \"math\"\n\nfunc main() {\n\tx := 2.0\n\tprintln(math.Sqrt(x))\n}\n"})
	// The result struct of Frexp and Lgamma, which only emitting them declares: the
	// pass that leaves unreached C out afterwards (deaddata.go) keeps a typedef.
	for _, gone := range []string{"math_Sin(ogo_f64 x) {", "math__sin[", "math_Lgamma(", "math_pow10tab[", "ogo_ret_ogo_f64_int;"} {
		if strings.Contains(c, gone) {
			t.Errorf("Sqrt alone: %q emitted", gone)
		}
	}
	c = emit(map[string]string{
		"main.ogo":    "import (\n\t\"lib\"\n\t\"math\"\n)\n\nfunc main() {\n\tf := math.Cbrt\n\tx := 27.0\n\tprintln(f(x), lib.H(3, 4))\n}\n",
		"lib/lib.ogo": "import \"math\"\n\nfunc H(a, b float64) float64 { return math.Hypot(a, b) }\n",
	})
	for _, kept := range []string{"math_Cbrt(ogo_f64 x) {", "math_ogo_kw_cbrt(ogo_f64 x) {", "math_Hypot(", "math_ogo_kw_hypot("} {
		if !strings.Contains(c, kept) {
			t.Errorf("a value, another package's call and what they call: %q not emitted", kept)
		}
	}
	if strings.Contains(c, "math_Sin(ogo_f64 x) {") {
		t.Errorf("math_Sin emitted for a program that never names it")
	}
}
