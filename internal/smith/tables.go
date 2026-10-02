// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"strconv"
	"strings"
)

// smallKinds are the one- and two-byte integer kinds a table's fields are drawn
// from, with the range their values are drawn in.
var smallKinds = []struct {
	name   string
	lo, hi int
}{
	{"uint8", 0, 255},
	{"int8", -128, 127},
	{"uint16", 0, 65535},
	{"int16", -32768, 32767},
}

// staticTable is a package variable genStaticTables declared, as the expression
// main folds into the checksum and the value that expression has.
type staticTable struct {
	sum  string // the fold: every field and element read, added up as ints
	want Int32
}

// genStaticTables declares, at package scope, a struct type with a small head
// field, an ARRAY of one- or two-byte elements and sometimes a tail field, and
// package variables of it initialized from constant literals: one of the struct,
// sometimes an array of them and a slice of them. Main folds every field and
// element into the checksum first thing (GenerateProgram).
//
// The target's C compiler lays out a static initializer of such a struct otherwise
// than it lays out the type -- an array field of small elements of four bytes or
// more, at an offset or of a size the two models disagree about -- and a field read
// read the bytes of another, in silence (doc/static-init-array-field.c,
// flexprop#116). Nothing generated a package variable of a struct holding an array
// until then, so the sweeps on the board could not see it. The values are the
// generator's own constants, so what the fold comes to is known without the VM
// modelling the struct.
func (f *Fuzzer) genStaticTables() []staticTable {
	if f.Rand.Intn(3) == 0 {
		return nil
	}
	f.VarSeq++
	id := f.VarSeq
	typ := fmt.Sprintf("ST_%d", id)
	head := smallKinds[f.Rand.Intn(len(smallKinds))]
	elem := smallKinds[f.Rand.Intn(len(smallKinds))]
	n := 1 + f.Rand.Intn(7)
	tail := ""
	switch f.Rand.Intn(4) {
	case 0:
		tail = "uint8"
	case 1:
		tail = "int16"
	case 2:
		tail = "int32"
	}
	fmt.Fprintf(f.Out, "type %s struct {\n\th %s\n\ta [%d]%s\n", typ, head.name, n, elem.name)
	if tail != "" {
		fmt.Fprintf(f.Out, "\tt %s\n", tail)
	}
	fmt.Fprint(f.Out, "}\n\n")

	draw := func(lo, hi int) int { return lo + f.Rand.Intn(hi-lo+1) }
	// value draws one struct's values and answers its literal's body, without the
	// type, and what its fields add up to.
	value := func() (string, int64) {
		h := draw(head.lo, head.hi)
		sum := int64(h)
		var elems []string
		for i := 0; i < n; i++ {
			e := draw(elem.lo, elem.hi)
			sum += int64(e)
			elems = append(elems, strconv.Itoa(e))
		}
		body := fmt.Sprintf("%d, [%d]%s{%s}", h, n, elem.name, strings.Join(elems, ", "))
		switch tail {
		case "uint8":
			t := draw(0, 255)
			sum += int64(t)
			body += ", " + strconv.Itoa(t)
		case "int16":
			t := draw(-32768, 32767)
			sum += int64(t)
			body += ", " + strconv.Itoa(t)
		case "int32":
			t := draw(-100000, 100000)
			sum += int64(t)
			body += ", " + strconv.Itoa(t)
		}
		return body, sum
	}
	// reads is every field and element of the struct at base, as int terms.
	reads := func(base string) []string {
		terms := []string{"int(" + base + ".h)"}
		for i := 0; i < n; i++ {
			terms = append(terms, fmt.Sprintf("int(%s.a[%d])", base, i))
		}
		if tail != "" {
			terms = append(terms, "int("+base+".t)")
		}
		return terms
	}
	var out []staticTable
	add := func(terms []string, sum int64) {
		out = append(out, staticTable{sum: strings.Join(terms, " + "), want: Int32(sum)})
	}

	name := fmt.Sprintf("tb_%d", id)
	body, sum := value()
	fmt.Fprintf(f.Out, "var %s = %s{%s}\n\n", name, typ, body)
	add(reads(name), sum)

	// An array of them, the elements' literals elided as Go allows.
	if f.Rand.Intn(2) == 0 {
		name := fmt.Sprintf("tbs_%d", id)
		k := 2 + f.Rand.Intn(2)
		var bodies, terms []string
		var total int64
		for i := 0; i < k; i++ {
			b, s := value()
			bodies = append(bodies, "{"+b+"}")
			terms = append(terms, reads(fmt.Sprintf("%s[%d]", name, i))...)
			total += s
		}
		fmt.Fprintf(f.Out, "var %s = [%d]%s{%s}\n\n", name, k, typ, strings.Join(bodies, ", "))
		add(terms, total)
	}
	// And a slice of them, whose backing array is the static the initializer writes.
	if f.Rand.Intn(3) == 0 {
		name := fmt.Sprintf("tsl_%d", id)
		b, s := value()
		fmt.Fprintf(f.Out, "var %s = []%s{{%s}}\n\n", name, typ, b)
		add(reads(name+"[0]"), s)
	}
	return out
}
