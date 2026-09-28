// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"math"
	"unicode/utf8"
)

// gofmt aligns nothing itself. go/printer writes a TAB where a cell ends -- after
// the names of a field, after the key of a literal's element, ahead of a trailing
// comment -- and text/tabwriter lays the cells out: a column is a run of
// consecutive lines that have a cell there, as wide as the widest. Where
// go/printer wants two neighbours NOT to share a column it breaks the line with a
// form feed, which begins a new section.
//
// So the formatter does the same, in three steps. Ahead of the walk, markIndents
// and markCells decide where cells end (cellsBefore, commentCells) and where
// sections begin (sectionBefore). The walk writes every gap as one blank and
// records where a marked token landed (marks, sections). alignCells then lays the
// finished text out as the tabwriter would.
//
// Before 2026-09-28 each kind of column had a mechanism of its own -- a field's
// type and a spec's value by measuring the declaration, a one-line function's
// brace likewise, a trailing comment by where it landed -- and each its own idea
// of what a run is. They agreed with gofmt on the run corpus and not on p2-11's
// sources: a comment column ran on across a line without a comment, a const
// block's across a bare name, a list's across a line of several elements, and a
// literal's keys were not aligned at all.

// cellMark is where a cell ended: the next one begins at byte off of output line
// line. Several marks in one place are empty cells.
type cellMark struct {
	line, off int
}

// listElem is one element of a list, and where the walk wrote it.
type listElem struct {
	beginsLine  bool // in the source
	oneLine     bool // in the source
	blankBefore bool // a blank line stands ahead of it
	pair        bool // key: value

	line, off       int // where its first token landed
	keyOff          int // where a pair's colon did
	endLine, endOff int // where its last token ended
}

// elemList is a list go/printer writes with exprList, which decides between a
// line break and a form feed by the sizes of the elements -- where it knows the
// tokens either side of the list, which it does for a literal's elements, a
// call's arguments and a case's values. Anywhere else every break is a form feed.
type elemList struct {
	elems []*listElem
	sized bool
}

// markCells is markIndents' part in the alignment: what it decides for the
// production k.
func (f *formatter) markCells(k kid, top bool) {
	body := k.ast[2:]
	switch k.sym {
	case ConstDecl, VarDecl, TypeDecl:
		f.specCells(body, k.sym)
	case StructType:
		f.fieldCells(k)
	case InterfaceType:
		f.sectionAfterMultiline(body, MethodSpec)
	case Block, CaseClause, CommClause:
		f.sectionAfterMultiline(body, Statement)
	case CaseHead:
		f.sectionBefore[firstIndex(k.ast)] = true
		for _, c := range kidsOf(body) {
			if c.sym == ExpressionList {
				f.sizedList[&c.ast[0]] = true
			}
		}
	case CommHead:
		f.sectionBefore[firstIndex(k.ast)] = true
	case ParameterList, ResultList:
		for _, c := range kidsOf(body) {
			if first := firstIndex(c.ast); c.sym != 0 && first >= 0 && f.beginsLine(first) {
				f.sectionBefore[first] = true
			}
		}
	case ElementList, ArgumentList:
		f.listCells(body, true)
	case ExpressionList:
		f.listCells(body, f.sizedList[&k.ast[0]])
	case TopLevelDecl:
		f.funcCells(k)
	case Signature, MethodSpec:
		f.resultParens(body)
	}
}

// resultParens drops the parentheses around a result list of one type with no
// name, as gofmt does: "func() (int)" is "func() int".
func (f *formatter) resultParens(body []int32) {
	kids := kidsOf(body)
	for i, k := range kids {
		if k.sym != ResultList || i == 0 || i+1 >= len(kids) || kids[i-1].sym != 0 || kids[i+1].sym != 0 {
			continue
		}
		lp, rp := kids[i-1].ast[0], kids[i+1].ast[0]
		if f.multiline(lp, rp) {
			return
		}
		var drop []int32
		decls := 0
		for _, r := range kidsOf(k.ast[2:]) {
			switch {
			case r.sym == 0:
				drop = append(drop, r.ast[0]) // a comma after the last, and only, result
			case r.sym == ParamDecl:
				decls++
				if d := kidsOf(r.ast[2:]); len(d) != 1 || d[0].sym != Type {
					return // it has a name, or is variadic
				}
			}
		}
		if decls != 1 {
			return
		}
		for _, t := range append(drop, lp, rp) {
			f.skipTok[t] = true
		}
	}
}

// multiline reports whether the tokens first to last are written across lines.
func (f *formatter) multiline(first, last int32) bool {
	return first >= 0 && last >= 0 && f.p.Token(first).Position().Line != f.endLine(last)
}

// sectionAfterMultiline begins a section at every sym of body that follows one
// written across lines: go/printer breaks the line after such a one with a form
// feed.
func (f *formatter) sectionAfterMultiline(body []int32, sym Symbol) {
	wasMultiline := false
	for _, k := range kidsOf(body) {
		if k.sym != sym {
			continue
		}
		first, last := firstIndex(k.ast), lastIndex(k.ast)
		if first < 0 {
			continue // an empty statement
		}
		if wasMultiline {
			f.sectionBefore[first] = true
		}
		wasMultiline = f.multiline(first, last)
	}
}

// specTokens finds the tokens of a spec its cells begin at: the first of its
// type and its "=", either -1 where the spec has none.
func specTokens(spec kid) (typ, eq int32) {
	typ, eq = -1, -1
	for _, c := range kidsOf(spec.ast[2:]) {
		switch {
		case c.sym == Type && typ < 0:
			typ = firstIndex(c.ast)
		case c.sym == 0 && eq < 0:
			eq = c.ast[0] // the only token of a spec's own is its "="
		}
	}
	if spec.sym != TypeSpec {
		return typ, eq
	}
	// A type spec's first token is its name, and whatever follows the name is one
	// cell: "= T" for an alias.
	eq = -1
	for i, c := range kidsOf(spec.ast[2:]) {
		if i == 1 {
			typ = firstIndex(c.ast)
		}
	}
	return typ, eq
}

// specCells marks the cells of a grouped declaration's specs, which are
// go/printer's valueSpec's: the names, the type, the "= value" and the comment,
// the comment always the fourth. A spec with no type has the type's cell all the
// same, empty, where a spec of its run has one (keepTypeColumn).
func (f *formatter) specCells(body []int32, decl Symbol) {
	var specs []kid
	for _, k := range kidsOf(body) {
		switch k.sym {
		case ConstSpec, VarSpec, TypeSpec:
			specs = append(specs, k)
		}
	}
	if len(specs) < 2 {
		return // a group of one is written with blanks
	}
	f.sectionAfterMultiline(body, specs[0].sym)
	if decl == TypeDecl {
		for _, s := range specs {
			if typ, _ := specTokens(s); typ >= 0 {
				f.cellsBefore[typ] = 1
			}
		}
		return
	}

	// keepTypeColumn: in a run of specs with values the type column is kept for
	// all of them where one of them has a type.
	keep := make([]bool, len(specs))
	start, keepType := -1, false
	populate := func(end int) {
		for i := start; keepType && i < end; i++ {
			keep[i] = true
		}
	}
	for i, s := range specs {
		typ, eq := specTokens(s)
		switch {
		case eq >= 0 && start < 0:
			start, keepType = i, false
		case eq < 0 && start >= 0:
			populate(i)
			start = -1
		}
		if typ >= 0 {
			keepType = true
		}
	}
	if start >= 0 {
		populate(len(specs))
	}

	for i, s := range specs {
		typ, eq := specTokens(s)
		cells := 0
		if typ >= 0 {
			f.cellsBefore[typ] = 1
			cells = 1
		}
		if eq >= 0 {
			switch {
			case typ < 0 && keep[i]:
				f.cellsBefore[eq] = 2
				cells = 2
			default:
				f.cellsBefore[eq] = 1
				cells++
			}
		}
		if last := lastIndex(s.ast); last >= 0 {
			f.commentCells[last] = 3 - cells
		}
	}
}

// fieldCells marks the cells of a struct's fields: the names, the type and the
// comment, the comment the third whether the field has names or is embedded. A
// struct of one field is written with blanks.
func (f *formatter) fieldCells(k kid) {
	body := k.ast[2:]
	f.sectionAfterMultiline(body, FieldDecl)
	var fields []kid
	for _, c := range kidsOf(body) {
		if c.sym == FieldDecl {
			fields = append(fields, c)
		}
	}
	if len(fields) < 2 || !f.multiline(firstIndex(k.ast), lastIndex(k.ast)) {
		return
	}
	for _, fd := range fields {
		last := lastIndex(fd.ast)
		if last < 0 {
			continue
		}
		f.commentCells[last] = 2
		for _, c := range kidsOf(fd.ast[2:]) {
			if c.sym == Type {
				f.cellsBefore[firstIndex(c.ast)] = 1
				f.commentCells[last] = 1
			}
		}
	}
}

// funcCells marks the brace of a function declared on one line, which is a cell
// of its own: go/printer separates the body of such a one from its header by a
// tab.
func (f *formatter) funcCells(tld kid) {
	if len(tld.ast) > 2 && tld.ast[2] < 0 && Symbol(-tld.ast[2]) == FuncDecl {
		fd := kidsOf(tld.ast[2:])[0]
		first, last := firstIndex(fd.ast), lastIndex(fd.ast)
		if f.multiline(first, last) {
			// The next declaration being a function of several lines is the one
			// place go/printer begins a section between two declarations.
			f.sectionBefore[first] = true
			return
		}
		for _, c := range kidsOf(fd.ast[2:]) {
			if c.sym == Block {
				f.cellsBefore[firstIndex(c.ast)] = 1
			}
		}
	}
}

// listCells notes the elements of a list for listSections, and marks the value
// of a "key: value" that begins a line and ends on it: its key is a cell.
func (f *formatter) listCells(body []int32, sized bool) {
	l := &elemList{sized: sized}
	var elems []kid
	for _, k := range kidsOf(body) {
		if k.sym != 0 {
			elems = append(elems, k)
		}
	}
	prevLast := int32(-1)
	for _, k := range elems {
		first, last := firstIndex(k.ast), lastIndex(k.ast)
		if first < 0 || last < 0 {
			continue
		}
		e := &listElem{
			beginsLine: f.beginsLine(first),
			oneLine:    !f.multiline(first, last),
		}
		if prevLast >= 0 {
			e.blankBefore = f.p.Token(first).Position().Line-f.endLine(prevLast) > 1
		}
		prevLast = last
		if k.sym == Element {
			kids := kidsOf(k.ast[2:])
			for i, c := range kids {
				if c.sym == 0 && Symbol(f.p.Token(c.ast[0]).Ch) == COLON && i+1 < len(kids) {
					e.pair = true
					f.elemColon[c.ast[0]] = append(f.elemColon[c.ast[0]], e)
					if len(elems) > 1 && e.beginsLine && e.oneLine {
						f.cellsBefore[firstIndex(kids[i+1].ast)] = 1
					}
					break
				}
			}
		}
		f.elemFirst[first] = append(f.elemFirst[first], e)
		f.elemLast[last] = append(f.elemLast[last], e)
		l.elems = append(l.elems, e)
	}
	if len(l.elems) != 0 {
		f.lists = append(f.lists, l)
	}
}

// listSections begins a section at every element go/printer's exprList breaks
// the line ahead of with a form feed: the first of a list; one that follows a
// line of several; and one whose size -- a pair's key's -- is too far from its
// neighbours' for a shared column to look like one, which is exprList's
// arithmetic, kept as it is.
func (f *formatter) listSections() {
	for _, l := range f.lists {
		size := 0
		lnsum, count := 0.0, 0
		prevBreak := -1
		for i, e := range l.elems {
			prevSize := size
			size = 0
			if l.sized && e.oneLine && e.line == e.endLine {
				size = e.endOff - e.off
				if e.pair {
					size = e.keyOff - e.off
				}
			}
			useFF := true
			if prevSize > 0 && size > 0 {
				const smallSize = 40
				if count == 0 || prevSize <= smallSize && size <= smallSize {
					useFF = false
				} else {
					const r = 2.5
					geomean := math.Exp(lnsum / float64(count))
					ratio := float64(size) / geomean
					useFF = r*ratio <= 1 || r <= ratio
				}
			}
			if e.beginsLine {
				if i == 0 || useFF || prevBreak+1 < i {
					f.sections[e.line] = true
				}
				prevBreak = i
				if i > 0 && e.blankBefore {
					lnsum, count = 0, 0
				}
			}
			if size > 0 {
				lnsum += math.Log(float64(size))
				count++
			}
		}
	}
}

// tableLine is one line of a table: the widths of its cells, the last of which
// ends the line and is part of no column, and -- once laid out -- how wide the
// column of each of the others is.
type tableLine struct {
	cells  []int
	widths []int
}

// layoutTable gives every cell that does not end its line the width of its
// column, as text/tabwriter does with gofmt's settings: a column is a run of
// consecutive lines with a cell in it, as wide as the widest of them and one
// more; a column of empty cells takes no room; and the columns after it are laid
// out within the run.
func layoutTable(lines []tableLine) {
	for i := range lines {
		lines[i].widths = make([]int, len(lines[i].cells))
	}
	var format func(line0, line1, column int)
	format = func(line0, line1, column int) {
		for this := line0; this < line1; this++ {
			if column >= len(lines[this].cells)-1 {
				continue
			}
			begin, width := this, 0
			for ; this < line1 && column < len(lines[this].cells)-1; this++ {
				if w := lines[this].cells[column]; w > 0 && w+1 > width {
					width = w + 1
				}
			}
			for i := begin; i < this; i++ {
				lines[i].widths[column] = width
			}
			format(begin, this, column+1)
			this--
		}
	}
	format(0, len(lines), 0)
}

// alignCells lays the written text out by the marks the walk recorded. A table
// is a run of consecutive lines that have a mark, stand at one indentation and
// begin no section but for the first.
func (f *formatter) alignCells(out []byte) []byte {
	if len(f.marks) == 0 {
		return out
	}
	f.listSections()
	lines := bytes.Split(out, nl)
	rows := map[int][]int{}
	for _, m := range f.marks {
		if m.line < len(lines) && m.off <= len(lines[m.line]) {
			rows[m.line] = append(rows[m.line], m.off)
		}
	}
	indent := func(i int) int {
		return len(lines[i]) - len(bytes.TrimLeft(lines[i], "\t"))
	}
	for i := 0; i < len(lines); i++ {
		if rows[i] == nil {
			continue
		}
		j := i
		for j+1 < len(lines) && rows[j+1] != nil && !f.sections[j+1] && indent(j+1) == indent(i) {
			j++
		}
		table := make([]tableLine, 0, j-i+1)
		var text [][][]byte
		for l := i; l <= j; l++ {
			prev := indent(l)
			var segs [][]byte
			var t tableLine
			for _, off := range rows[l] {
				if off < prev {
					off = prev
				}
				seg := bytes.TrimRight(lines[l][prev:off], " ")
				segs = append(segs, seg)
				t.cells = append(t.cells, utf8.RuneCount(seg))
				prev = off
			}
			segs = append(segs, lines[l][prev:])
			t.cells = append(t.cells, 0)
			text = append(text, segs)
			table = append(table, t)
		}
		layoutTable(table)
		for l := i; l <= j; l++ {
			t, segs := table[l-i], text[l-i]
			b := append([]byte{}, lines[l][:indent(l)]...)
			for k, seg := range segs {
				b = append(b, seg...)
				if k < len(segs)-1 {
					b = append(b, bytes.Repeat(sp, t.widths[k]-t.cells[k])...)
				}
			}
			lines[l] = b
		}
		i = j
	}
	return bytes.Join(lines, nl)
}
