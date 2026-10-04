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

// TestEmitCSummaryThroughUnnamed: a callee storing its parameter through a pointer
// no name holds -- a call's result, `gethp().p = v` and `fa()[0] = v`, or a
// dereference of anything but a name, `(*f())[0] = v` -- alone, in a list and in a
// for clause. What such a pointer reaches cannot be named in the callee, and the
// summaries took the store for none: `keep(&x)` left a local's address in package
// storage in silence (storedThroughUnnamed). The dereferenced forms did not
// compile until they were made to, which is what found the rest. Each callee is
// called once on a local, which must be refused, and once on a package variable,
// which must not.
func TestEmitCSummaryThroughUnnamed(t *testing.T) {
	const head = `var gp = []*int{nil}

var ga [2]*int

type H struct{ p *int }

var gh H

var gx int

var n int

func f() *[]*int { return &gp }

func fa() *[2]*int { return &ga }

func gethp() *H { return &gh }
`
	for _, callee := range []string{
		"func keep(v *int) { gethp().p = v }",
		"func keep(v *int) { fa()[0] = v }",
		"func keep(v *int) { (*f())[0] = v }",
		"func keep(v *int) { (*gethp()).p = v }",
		"func keep(v *int) { (*fa())[1] = v }",
		"func keep(v *int) { n, gethp().p = 1, v }",
		"func keep(v *int) { n, fa()[1] = 2, v }",
		"func keep(v *int) {\n\tfor i := 0; i < 1; gethp().p = v {\n\t\ti++\n\t}\n}",
	} {
		for _, call := range []string{"keep(&x)", "keep(&gx)"} {
			src := head + callee + "\n\nfunc main() {\n\tx := 5\n\t_ = x\n\t" + call + "\n}\n"
			t.Run(callee+"/"+call, func(t *testing.T) {
				fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
				pkg, err := Build(-1, []string{"main.ogo"}, fsys)
				if err == nil {
					err = EmitC(pkg, io.Discard, Checked())
				}
				refuse := call == "keep(&x)"
				switch {
				case refuse && err == nil:
					t.Errorf("a reference to this frame left it:\n%s", src)
				case refuse && !strings.Contains(err.Error(), "outlives every frame"):
					t.Errorf("refused, but not for its lifetime: %v", err)
				case !refuse && err != nil:
					t.Errorf("refused: %v\n%s", err, src)
				}
			})
		}
	}
}

// TestEmitCDerefCallRefusals: what the dereference of a call, newly writable, is
// asked of the frame. A reference to the frame stored through it is refused --
// what it reaches is not known to be this function's -- and named as written; and
// a method keeping its receiver, called on `(*(&l))`, keeps the local l.
func TestEmitCDerefCallRefusals(t *testing.T) {
	for _, test := range []struct{ src, want string }{
		{`var gp = []*int{nil}

func f() *[]*int { return &gp }

func main() {
	x := 5
	(*f())[0] = &x
}
`, "through (*f())[0]"},
		{`type H struct{ n int }

var gk *H

func (h *H) keep() int {
	gk = h
	return h.n
}

func main() {
	var l H
	x := (*(&l)).keep()
	println(x)
}
`, "its receiver is stored where it outlives every frame"},
	} {
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(test.src)}}
		pkg, err := Build(-1, []string{"main.ogo"}, fsys)
		if err == nil {
			err = EmitC(pkg, io.Discard, Checked())
		}
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("got %v, want an error containing %q\n%s", err, test.want, test.src)
		}
	}
}
