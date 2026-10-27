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

// fieldMarksHeader declares the structs and the keepers the field marks tests use:
// W holds a view of the frame in one field and package storage in the others.
const fieldMarksHeader = `type In struct {
	ex []byte
	n  int
}

type E struct{ ex2 []byte }

type W struct {
	E
	w   *int
	ex  []byte
	in  In
	arr [2]In
}

type W2 struct {
	E
	ex2 []byte
	w   *int
}

var (
	pkg int
	g   *int
	gex []byte
	gin In
	gw  W
)

func store(p *int)      { g = p }
func storeEx(b []byte)  { gex = b }
func (z *W) keepW()     { g = z.w }
func (z *W) keepEx()    { gex = z.ex }
func (z *W) keepAll()   { gw = *z }
func (z *W) viaW()      { store(z.w) }
func (z *W) viaEx()     { storeEx(z.ex) }
func (z *W) viaInF()    { keepInf(&z.in) }
func (z *W) viaInEx()   { z.in.keepEx() }
func (z *W) keepAlias() { t := z.ex; gex = t }
func (in *In) keepEx()  { gex = in.ex }
func (e *E) hold()      { gex = e.ex2 }
func (z *W2) viaProm()  { z.hold() }
func (z *W2) keepOwn()  { gex = z.ex2 }
func keepWf(z *W)       { g = z.w }
func keepExf(z *W)      { gex = z.ex }
func keepInf(in *In)    { gex = in.ex }
func wrapIn(z *W)       { keepInf(&z.in) }
func keepInAll(in *In)  { gin = *in }

`

// TestEmitCFieldMarks is the holder marks by field (markHolderField) and the
// summaries' fields (noteContents): a local struct given a view of the frame in one
// field keeps it in that field alone, so a read, a callee or a method keeping
// another field keeps nothing of the frame (dom75, sweeps/field-holder: 23 of 24
// such rows were refused). Each refused row keeps the view by another way the
// fields could lose it: a prefix of its path, a loop's back edge, a goto back, a
// block's merge over a shadowing name, a copy, a part of the struct handed on, a
// promotion whose fields are the embedded struct's, a local bound to the field.
func TestEmitCFieldMarks(t *testing.T) {
	const fill = "\tvar a [4]byte\n\tvar loc int\n\t_ = loc\n\tvar z W\n\tz.w = &pkg\n\tz.ex = a[:]\n"
	const fillIn = "\tvar a [4]byte\n\tvar z W\n\tz.w = &pkg\n\tz.in.ex = a[:]\n"
	for _, test := range []struct {
		name, body string
		refuse     bool
	}{
		{"another field read", fill + "\tg = z.w\n", false},
		{"the field read", fill + "\tgex = z.ex\n", true},
		{"the whole struct", fill + "\tgw = z\n", true},
		{"another field passed", fill + "\tstore(z.w)\n", false},
		{"the field passed", fill + "\tstoreEx(z.ex)\n", true},
		{"a method keeping another field", fill + "\tz.keepW()\n", false},
		{"a method keeping the field", fill + "\tz.keepEx()\n", true},
		{"a method keeping all of it", fill + "\tz.keepAll()\n", true},
		{"a method handing on another field", fill + "\tz.viaW()\n", false},
		{"a method handing on the field", fill + "\tz.viaEx()\n", true},
		{"a method keeping the field through a local", fill + "\tz.keepAlias()\n", true},
		{"a function keeping another field", fill + "\tkeepWf(&z)\n", false},
		{"a function keeping the field", fill + "\tkeepExf(&z)\n", true},
		{"the literal's other field", "\tvar a [4]byte\n\tz := W{w: &pkg, ex: a[:]}\n\tg = z.w\n", false},
		{"the literal's field", "\tvar a [4]byte\n\tz := W{w: &pkg, ex: a[:]}\n\tgex = z.ex\n", true},
		{"a nested field's struct", fillIn + "\tgin = z.in\n", true},
		{"a nested field's sibling", fillIn + "\tgex = z.ex\n", false},
		{"a part handed to a function keeping its field", fillIn + "\tkeepInf(&z.in)\n", true},
		{"a part's method keeping its field", fillIn + "\tz.in.keepEx()\n", true},
		{"a method handing on a part", fillIn + "\tz.viaInF()\n", true},
		{"a method calling a part's method", fillIn + "\tz.viaInEx()\n", true},
		{"a function handing on a part", fillIn + "\twrapIn(&z)\n", true},
		{"a part handed on, the view elsewhere", fill + "\tkeepInf(&z.in)\n", false},
		{"a part handed to a function keeping all of it", fillIn + "\tkeepInAll(&z.in)\n", true},
		{"a part handed to a function keeping all of it, the view elsewhere", fill + "\tkeepInAll(&z.in)\n", false},
		{"an element of an array field", "\tvar a [4]byte\n\tvar z W\n\tz.arr[0].ex = a[:]\n\tgex = z.arr[1].ex\n", true},
		{"a promoted field written, the embedded struct read", "\tvar a [4]byte\n\tvar z W\n\tz.ex2 = a[:]\n\tgex = z.E.ex2\n", true},
		{"a promoted field written, another field read", "\tvar a [4]byte\n\tvar z W\n\tz.w = &pkg\n\tz.ex2 = a[:]\n\tg = z.w\n", false},
		{"a promoted method keeping the embedded field", "\tvar a [4]byte\n\tvar z W2\n\tz.E.ex2 = a[:]\n\tz.viaProm()\n", true},
		{"the outer field a promoted one is shadowed by", "\tvar a [4]byte\n\tvar z W2\n\tz.E.ex2 = a[:]\n\tz.keepOwn()\n", false},
		{"a copy's field", fill + "\ty := z\n\tgex = y.ex\n", true},
		{"a copy's other field", fill + "\ty := z\n\tg = y.w\n", false},
		{"a copy assigned", fill + "\tvar y W\n\ty = z\n\tgex = y.ex\n", true},
		{"the field marked after its read in a loop", "\tvar a [4]byte\n\tvar z W\n\tfor i := 0; i < 2; i++ {\n\t\tgex = z.ex\n\t\tz.ex = a[:]\n\t}\n", true},
		{"a field marked after its read in a loop, another marked before", "\tvar a [4]byte\n\tvar b [2]int\n\tvar z W\n\tz.w = &b[0]\n\tfor i := 0; i < 2; i++ {\n\t\tgex = z.ex\n\t\tz.ex = a[:]\n\t}\n", true},
		{"another field read in that loop", "\tvar a [4]byte\n\tvar z W\n\tz.w = &pkg\n\tfor i := 0; i < 2; i++ {\n\t\tg = z.w\n\t\tz.ex = a[:]\n\t}\n", false},
		{"the field marked after a label a goto goes back to", "\tvar a [4]byte\n\tvar z W\n\tk := 0\nagain:\n\tgex = z.ex\n\tz.ex = a[:]\n\tk++\n\tif k < 2 {\n\t\tgoto again\n\t}\n", true},
		{"the field marked in a block", "\tvar a [4]byte\n\tvar z W\n\tif pkg == 0 {\n\t\tz.ex = a[:]\n\t}\n\tgex = z.ex\n", true},
		{"a whole mark under a shadowing name's field mark", "\tvar loc int\n\tvar z W\n\tz = W{w: &loc}\n\tif pkg == 0 {\n\t\tvar b [2]byte\n\t\tz := W{}\n\t\tz.ex = b[:]\n\t\t_ = z\n\t}\n\tg = z.w\n", true},
		{"a deferred method keeping the field", fill + "\tdefer z.keepEx()\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := fieldMarksHeader + "func run() {\n" + test.body + "}\n\nfunc main() { run() }\n"
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refuse && err == nil:
				t.Errorf("a reference to this frame left it:\n%s", src)
			case test.refuse && !strings.Contains(err.Error(), "outlive") && !strings.Contains(err.Error(), "package scope"):
				t.Errorf("refused, but not for its lifetime: %v", err)
			case !test.refuse && err != nil:
				t.Errorf("refused: %v\n%s", err, src)
			}
		})
	}
}
