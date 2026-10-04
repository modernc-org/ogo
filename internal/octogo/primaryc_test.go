// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import "testing"

// TestPrimaryC pins what a discard's cast may be written before without
// parentheses (emitDiscard): `(void)gc == gc` cast gc alone.
func TestPrimaryC(t *testing.T) {
	for _, test := range []struct {
		text string
		want bool
	}{
		{"x", true},
		{"42", true},
		{"f()", true},
		{"f(a, (b + c))", true},
		{"(x + 1)", true},
		{`f(")")`, true},
		{`f('(')`, true},
		{"gc == gc", false},
		{"_ogo_t0 < g() && g() > 0", false},
		{"-f()", false},
		{"(x) + (y)", false},
		{"f() == g()", false},
		{`f(")") + 1`, false},
		{"", false},
	} {
		if g := primaryC(test.text); g != test.want {
			t.Errorf("primaryC(%q) = %v, want %v", test.text, g, test.want)
		}
	}
}
