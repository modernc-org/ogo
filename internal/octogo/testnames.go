// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// changesPerBlock is how many changes of NotPrintTable a block holds. It is even, so
// the testing package's widePrint tells whether a rune prints by the changes it
// passes within a block alone.
const changesPerBlock = 32

var notPrintTable = sync.OnceValue(func() []byte {
	var changes []rune
	prints := true
	for r := rune(0x100); r <= unicode.MaxRune; r++ {
		if p := strconv.IsPrint(r); p != prints {
			changes = append(changes, r)
			prints = p
		}
	}
	nb := (len(changes) + changesPerBlock - 1) / changesPerBlock
	tab := make([]byte, 2+5*nb)
	tab[0], tab[1] = byte(nb), byte(nb>>8)
	for b := range nb {
		k := 2 + 5*b
		first, off := changes[b*changesPerBlock], len(tab)
		tab[k], tab[k+1], tab[k+2] = byte(first), byte(first>>8), byte(first>>16)
		tab[k+3], tab[k+4] = byte(off), byte(off>>8)
		for m := b*changesPerBlock + 1; m < min((b+1)*changesPerBlock, len(changes)); m++ {
			tab = binary.AppendUvarint(tab, uint64(changes[m]-changes[m-1]))
		}
	}
	return tab
})

// NotPrintTable is where the runes past U+00FF change from printing to not and back,
// as strconv.IsPrint has them, laid out as the testing package reads it
// (widePrint): the number of blocks, two bytes, low first; for each block its first
// change, three bytes, and the offset of the rest of it, two; and the rest, each
// change its distance from the one before as a uvarint (encoding/binary). The
// runner hands it to testing.SetSubtests, so a subtest's name is rewritten by the
// Unicode tables of the Go that built ogo, which are go test's where that Go runs
// it.
func NotPrintTable() []byte { return notPrintTable() }

// MayRunSubtests reports whether the program whose main package is p may run a
// subtest or a sub-benchmark: whether a package of it but those the compiler
// carries names Run, as every way of reaching T.Run or B.Run -- a call, a method
// value or expression, an interface, an embedding -- spells it.
func MayRunSubtests(p *Package) bool {
	for _, pkg := range reachablePackages(p) {
		if _, embedded := embeddedPkgs[pkg.ImportPath]; embedded {
			continue
		}
		for _, f := range pkg.Files {
			if f == nil {
				continue
			}
			for i := 0; i < f.parser.sc.Len(); i++ {
				if t := f.tok(int32(i)); Symbol(t.Ch) == IDENT && t.Src() == "Run" {
					return true
				}
			}
		}
	}
	return false
}

// RunsSubtests reports whether c, the C a test program was emitted as, reaches T.Run
// or B.Run: names either anywhere but where it is declared or defined, at file scope
// before its parameters. MayRunSubtests asks the source, where Run is any type's
// method; this asks what the program became, so a test binary whose package names
// another type's Run is not handed the table it would never read.
func RunsSubtests(c []byte) bool {
	depth := 0
	for i := 0; i < len(c); {
		ch := c[i]
		switch {
		case ch == '/' && i+1 < len(c) && c[i+1] == '/':
			for i < len(c) && c[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(c) && c[i+1] == '*':
			end := bytes.Index(c[i+2:], []byte("*/"))
			if end < 0 {
				return true
			}
			i += end + 4
		case ch == '"' || ch == '\'':
			for i++; i < len(c) && c[i] != ch; i++ {
				if c[i] == '\\' {
					i++
				}
			}
			i++
		case ch == '{':
			depth++
			i++
		case ch == '}':
			depth--
			i++
		case ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9':
			j := i
			for j < len(c) && (c[j] == '_' || c[j] >= 'a' && c[j] <= 'z' || c[j] >= 'A' && c[j] <= 'Z' || c[j] >= '0' && c[j] <= '9') {
				j++
			}
			switch strings.TrimPrefix(string(c[i:j]), "ogo_j_") {
			case "testing_T_Run", "testing_B_Run":
				k := j
				for k < len(c) && (c[k] == ' ' || c[k] == '\t') {
					k++
				}
				if depth != 0 || k == len(c) || c[k] != '(' {
					return true
				}
			}
			i = j
		default:
			i++
		}
	}
	return false
}
