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

// TestEmitCInitSkew pins which package variables keep a static initializer and
// which are filled at package initialization, for the types the target's C compiler
// lays out one way and initializes statically another (flexccInitSkew,
// doc/static-init-array-field.c). Only the board can see the fault itself -- gcc
// lays both out alike -- so what the host can hold is the routing: every variable
// of a skewed type, an array of one and a slice's backing are zeroed statically, and
// the two whose layouts agree keep their braces. And the goroutine runtime's
// argument blocks, written after the program's globals, name their members with the
// compiler's prefix (doc/member-named-like-global.c).
func TestEmitCInitSkew(t *testing.T) {
	const src = `type Ramp struct {
	Ch    uint8
	Steps [4]int16
}

type B4 struct {
	ch uint8
	s  [4]uint8
}

type B5 struct {
	ch uint8
	s  [5]uint8
}

type H3 struct {
	ch uint8
	s  [3]int16
}

type B6T struct {
	b [6]uint8
	t int16
	u uint8
}

type R16 struct {
	ch uint16
	s  [2]int16
}

type Outer struct {
	n int32
	r Ramp
	k uint8
}

type OK struct {
	w int32
	s [4]uint8
}

type OK2 struct {
	ch uint8
	s  [3]uint8
}

var r = Ramp{2, [4]int16{1, -2, 3, -4}}

var b4 = B4{9, [4]uint8{1, 2, 3, 4}}

var b5 = B5{9, [5]uint8{1, 2, 3, 4, 5}}

var h3 = H3{9, [3]int16{1, -2, 3}}

var b6t = B6T{[6]uint8{1, 2, 3, 4, 5, 6}, 7, 8}

var r16 = R16{2, [2]int16{1, -2}}

var o = Outer{5, Ramp{3, [4]int16{6, -7, 8, -9}}, 4}

var rs = [2]Ramp{{1, [4]int16{2, 3, 4, 5}}, {6, [4]int16{7, 8, 9, 10}}}

var sl = []B4{{1, [4]uint8{2, 3, 4, 5}}}

var ok = OK{7, [4]uint8{1, 2, 3, 4}}

var ok2 = OK2{7, [3]uint8{1, 2, 3}}

func main() {
	println("r", r.Ch, r.Steps[0], r.Steps[1], r.Steps[2], r.Steps[3])
	println("b4", b4.ch, b4.s[0], b4.s[3], "b5", b5.s[0], b5.s[4])
	println("h3", h3.s[0], h3.s[1], h3.s[2], "b6t", b6t.b[5], b6t.t, b6t.u)
	println("r16", r16.ch, r16.s[0], r16.s[1])
	println("o", o.n, o.r.Ch, o.r.Steps[0], o.r.Steps[3], o.k)
	println("rs", rs[0].Steps[0], rs[1].Ch, rs[1].Steps[3], "sl", sl[0].ch, sl[0].s[0], sl[0].s[3])
	println("ok", ok.w, ok.s[0], ok.s[3], "ok2", ok2.ch, ok2.s[2])
}
`
	fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
	pkg, err := Build(-1, []string{"main.ogo"}, fsys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var buf bytes.Buffer
	if err := EmitC(pkg, &buf, Checked(), Inline()); err != nil {
		t.Fatalf("EmitC: %v", err)
	}
	c := buf.String()
	for _, want := range []string{
		"static Ramp r;\n",
		"static B4 b4;\n",
		"static B5 b5;\n",
		"static H3 h3;\n",
		"static B6T b6t;\n",
		"static R16 r16;\n",
		"static Outer o;\n",
		"static Ramp rs[2];\n",
		"static OK ok = {7, {1, 2, 3, 4}};\n",
		"static OK2 ok2 = {7, {1, 2, 3}};\n",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q in\n%s", want, c)
		}
	}
	if strings.Contains(c, "static B4 ogo_backing_0[1] = ") {
		t.Errorf("a slice's backing of a skewed type has a static initializer:\n%s", c)
	}

	const goSrc = `var s0 = 1

var a0 = 2

var fn = 3

var out = make(chan int)

func f(n int) { out <- n }

func main() {
	h := f
	go f(s0)
	go h(a0)
	println(<-out+<-out, fn)
}
`
	fsys = fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(goSrc)}}
	if pkg, err = Build(-1, []string{"main.ogo"}, fsys); err != nil {
		t.Fatalf("Build: %v", err)
	}
	buf.Reset()
	if err := EmitC(pkg, &buf, Checked(), Inline()); err != nil {
		t.Fatalf("EmitC: %v", err)
	}
	c = buf.String()
	for _, want := range []string{
		"typedef struct { int ogo_slot; int ogo_a0; } ogo_go_args0;\n",
		"ogo_go_args0 ogo_s0; ogo_go_args1 ogo_s1; } ogo_go_args;",
		" ogo_fn; int ogo_a0; } ogo_go_args1;\n",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q in\n%s", want, c)
		}
	}
}
