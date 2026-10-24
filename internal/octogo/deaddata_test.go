// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"strings"
	"testing"
)

// TestDropUnreachedData pins what the pass drops: a function of an embedded
// package main does not reach, with its prototype, the table only it reads and a
// float64 constant only it names -- and nothing else: not a function a reached
// one calls or a root names, not one a kept function names though nothing reaches
// it, not a table such a function reads or a root names, not a program's own, and
// not anything in C it cannot read.
func TestDropUnreachedData(t *testing.T) {
	c := `typedef struct { uint32_t lo, hi; } ogo_f64;
static const ogo_f64 ogo_f64k_3ff0000000000000 = {0x00000000u, 0x3ff00000u};
static const ogo_f64 ogo_f64k_4000000000000000 = {0x00000000u, 0x40000000u};
ogo_f64 math_Sin(ogo_f64 x);
ogo_f64 math_Used(ogo_f64 x);
ogo_f64 math_ByValue(ogo_f64 x);
ogo_f64 math_Keep(ogo_f64 x);
void user_unused(void);
static ogo_f64 math__sin[2] = {{0x0u, 0x1u}, {0x2u, 0x3u}};
static ogo_f64 math__used[1] = {{0x4u, 0x5u}};
static ogo_f64 math__byValue[1] = {{0x6u, 0x7u}};
static ogo_f64 math__root[1] = {{0x8u, 0x9u}};
static ogo_f64 math__keep[1] = {{0xau, 0xbu}};
static int user_table[2] = {1, 2};
static ogo_f64* gp = math__root;
static ogo_f64 (*gf)(ogo_f64) = math_ByValue;

ogo_f64 math_Sin(ogo_f64 x) {
	return ogo_f64_add(math__sin[0], ogo_f64k_4000000000000000);
}

static ogo_f64 math_helper(ogo_f64 x) { return math__used[0]; }

ogo_f64 math_Used(ogo_f64 x) {
	return math_helper(x);
}

ogo_f64 math_ByValue(ogo_f64 x) {
	return math__byValue[0];
}

ogo_f64 math_Keep(ogo_f64 x) {
	return math__keep[0];
}

void user_unused(void) {
	(void)math_Keep(ogo_f64k_3ff0000000000000);
}

int main(void) {
	ogo_f64 y = math_Used(ogo_f64k_3ff0000000000000);
	(void)y;
	return 0;
}
`
	funcs := map[string]bool{"math_Sin": true, "math_helper": true, "math_Used": true, "math_ByValue": true, "math_Keep": true}
	got := string(dropUnreachedData([]byte(c), funcs))
	for _, gone := range []string{"static ogo_f64 math__sin[2]", "ogo_f64k_4000000000000000 =", "ogo_f64 math_Sin(ogo_f64 x)"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q kept:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"math__used[1] =", "math__byValue[1] =", "math__root[1] =", "math__keep[1] =", "user_table[2] =",
		"ogo_f64k_3ff0000000000000 =", "ogo_f64 math_Used(ogo_f64 x) {", "static ogo_f64 math_helper(", "ogo_f64 math_Keep(ogo_f64 x) {",
		"ogo_f64 math_Keep(ogo_f64 x);", "void user_unused(void) {"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q dropped:\n%s", kept, got)
		}
	}
	// What the pass cannot read it leaves alone: a body that never ends.
	broken := c + "void tail(void) {\n\treturn;\n"
	if got := string(dropUnreachedData([]byte(broken), funcs)); got != broken {
		t.Errorf("changed C it could not read")
	}
}
