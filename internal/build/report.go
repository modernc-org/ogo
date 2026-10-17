// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"modernc.org/ogo/internal/octogo"
)

// spin2Method is a method of a Spin2 object as the listing names it, without its
// underscore: <object>_spin2_<method>.
var spin2Method = regexp.MustCompile(`^(\w+?)_spin2_(\w+)$`)

// reportTop is how many functions and how much data the report lists by name, the
// largest first.
const reportTop = 20

// report prints what the program built from cFile into out takes of the P2 (ogo
// build --report): Hub RAM by its parts, the goroutines' stacks, the hardware
// locks, and the largest functions and data. The layout is read off a listing of
// addresses (layoutListing), of out itself where the backend takes its assembly
// back. The data is named by what the emitter knows of it (octogo.Report), each
// object at the size the target's compiler gives it (probeSizes).
func report(w io.Writer, rep *octogo.Report, cFile, out, inc string, stackAsked bool) error {
	c, err := os.ReadFile(cFile)
	if err != nil {
		return err
	}
	built, err := os.ReadFile(out)
	if err != nil {
		return err
	}
	listing, err := os.ReadFile(strings.TrimSuffix(out, filepath.Ext(out)) + ".p2asm")
	if err != nil {
		return fmt.Errorf("report: %v", err)
	}
	dir, err := os.MkdirTemp("", "ogo-report-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	lst, binary, exact, err := layoutListing(listing, built, cFile, inc, dir)
	if err != nil {
		return err
	}
	img, ok := readImage(lst, len(binary))
	if !ok {
		return fmt.Errorf("report: the backend's listing is not laid out as expected")
	}
	needs := readStackNeeds(listing, c)
	scan := octogo.ScanCalls(c)
	goName := func(listed string) string { return funcLabel(stackCName(listed), rep, scan) }

	// Hub RAM.
	mainNeed, mainLeast := needs.mainNeed, ""
	if mainNeed < 0 {
		mainNeed, mainLeast = needs.mainAtLeast, " at least"
	}
	fmt.Fprintf(w, "Hub RAM: %d of %d bytes in the binary; main's stack needs%s %d more, %d to spare\n",
		len(built), hubRAMBytes, mainLeast, mainNeed, hubRAMBytes-(img.stack+len(built)-img.size)-mainNeed)
	if !exact {
		d := len(binary) - len(built)
		how := fmt.Sprintf("%d bytes larger", d)
		if d < 0 {
			how = fmt.Sprintf("%d bytes smaller", -d)
		} else if d == 0 {
			how = "of the same size, its code a little different"
		}
		fmt.Fprintf(w, "  (the parts are those of the program built with a listing of addresses, which the\n  backend optimizes a little differently: %s)\n", how)
	}
	header := img.size - img.cog - img.code - img.strs - img.data - img.libData - img.tables
	for _, o := range img.objs {
		header -= o.size
	}
	row := func(n int, what, note string) {
		fmt.Fprintf(w, "  %-16s %7d  %s\n", what, n, note)
	}
	row(header, "header", "the loader's and the clock's")
	row(img.cog, "cog runtime", "the code each cog loads into its registers")
	row(img.code, "code", fmt.Sprintf("%d functions as the backend laid them out, the largest below", len(img.funcs)))
	row(img.strs, "strings", "the string literals")
	row(img.data, "data", "the program's: its variables, its goroutines' stacks, the runtime's state, below")
	for _, o := range img.objs {
		row(o.size, "spin2 DAT", "the code and data of the object "+o.name)
	}
	row(img.libData, "library data", "the C library's")
	row(img.tables, "tables", "the method table, and the objects' memory after it")

	// Goroutines.
	slots := octogo.GoSlots(c)
	started := func(tramp string) string {
		if g, ok := rep.Goroutines[tramp]; ok {
			return g
		}
		return tramp
	}
	switch {
	case len(slots) == 0:
		fmt.Fprintf(w, "Goroutines: none\n")
	case slots[0].Tramps == nil:
		s := slots[0]
		deep, who := 0, ""
		for _, t := range slices.Sorted(maps.Keys(needs.goTramps)) {
			if g := needs.goTramps[t]; g.longs > deep {
				deep, who = g.longs, t
			}
		}
		how := fmt.Sprintf("the deepest goroutine needs %d, %s", deep, started(who))
		if stackAsked {
			how = "as --gostack asked"
		}
		fmt.Fprintf(w, "Goroutines: 7 slots of %d longs, shared by %s; %s\n", s.Longs, count(len(rep.Goroutines), "go statement"), how)
	default:
		each := "each run once, on a slot of its own"
		if len(slots) == 1 {
			each = "run once, on a slot of its own"
		}
		fmt.Fprintf(w, "Goroutines: %s, %s\n", count(len(slots), "go statement"), each)
		fmt.Fprintf(w, "  %6s %6s  %s\n", "longs", "needs", "go statement")
		for _, s := range slots {
			deep := 0
			for _, t := range s.Tramps {
				if g, ok := needs.goTramps[t]; ok && g.longs > deep {
					deep = g.longs
				}
			}
			needed := fmt.Sprint(deep)
			if deep == 0 || stackAsked {
				needed = "-"
			}
			var what []string
			for _, t := range s.Tramps {
				what = append(what, started(t))
			}
			fmt.Fprintf(w, "  %6d %6s  %s\n", s.Longs, needed, strings.Join(what, ", "))
		}
	}

	// Hardware locks.
	locks := rep.ChanLocks
	if len(slots) != 0 {
		locks++
	}
	fmt.Fprintf(w, "Hardware locks: %d of 16 the runtime takes", locks)
	var parts []string
	if len(slots) != 0 {
		parts = append(parts, "1 the goroutine pool's")
	}
	if rep.ChanSites != 0 {
		parts = append(parts, fmt.Sprintf("%d of channels, for %s", rep.ChanLocks, count(rep.ChanSites, "make site")))
	}
	if len(parts) != 0 {
		fmt.Fprintf(w, ": %s", strings.Join(parts, ", "))
	}
	fmt.Fprintln(w)
	if rep.NewLocks != 0 {
		fmt.Fprintf(w, "  and the program calls p2.NewLock in %s\n", count(rep.NewLocks, "place"))
	}

	// Data, each object at the size the target's compiler gives it.
	var names []string
	for _, s := range rep.Storage {
		names = append(names, s.C)
	}
	sizes := probeSizes(c, names, dir, inc)
	fmt.Fprintf(w, "Data: the largest of the program's %d bytes\n", img.data)
	if sizes == nil {
		fmt.Fprintf(w, "  (not itemized: the program built to measure them did not build)\n")
	} else {
		type item struct {
			what string
			n    int
		}
		var items []*item
		byName := map[string]*item{}
		add := func(what string, n int) {
			it := byName[what]
			if it == nil {
				it = &item{what: what}
				byName[what] = it
				items = append(items, it)
			}
			it.n += n
		}
		types := map[string]string{}
		for _, s := range rep.Storage {
			if s.Var != "" && s.Type != "" {
				types[s.Var] = s.Type
			}
		}
		known := 0
		for i, s := range rep.Storage {
			n := sizes[i]
			known += n
			switch {
			case s.Var != "":
				add(strings.TrimSpace(s.Var+" "+types[s.Var]), n)
			case strings.HasPrefix(s.C, "ogo_cog_"):
				add("the goroutine slots: stacks, arguments, bookkeeping", n)
			case strings.HasPrefix(s.C, "ogo_chan_"):
				add("channel cells and buffers of package variables, and their locks", n)
			case strings.HasPrefix(s.C, "ogo_lit_"):
				add("string literal headers", n)
			case strings.Contains(s.C, "_vt_"):
				add("interfaces' method tables", n)
			case strings.HasPrefix(s.C, "ogo_spin2_"):
				add("the Spin2 objects' variables", n)
			case strings.HasPrefix(s.C, "ogo_plit_"):
				add("values package initialization makes", n)
			case strings.HasPrefix(s.C, "ogo_"):
				add("the runtime's state", n)
			default:
				add(s.C, n)
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].n > items[j].n })
		for i, it := range items {
			if i == reportTop || it.n == 0 {
				break
			}
			fmt.Fprintf(w, "  %7d  %s\n", it.n, it.what)
		}
		if rest := img.data - known; rest > 0 {
			fmt.Fprintf(w, "  %7d  the rest: the C library's state, the bytes of strings the values above point at, alignment\n", rest)
		}
	}

	// Code.
	funcs := slices.Clone(img.funcs)
	sort.SliceStable(funcs, func(i, j int) bool { return funcs[i].size > funcs[j].size })
	fmt.Fprintf(w, "Code: the largest of %d functions\n", len(funcs))
	for i, f := range funcs {
		if i == reportTop {
			break
		}
		fmt.Fprintf(w, "  %7d  %s\n", f.size, goName(f.name))
	}
	return nil
}

// funcLabel is what the report calls the function the C names cn: its Go name, or
// for an interface's table entry its method's, inlined into it or called, or the
// Spin2 object's, the runtime's or the C library's it is.
func funcLabel(cn string, rep *octogo.Report, scan *octogo.CallScan) string {
	if n, ok := rep.Funcs[cn]; ok {
		return n
	}
	if fc := scan.Funcs[cn]; fc != nil && scan.InTable(cn) && len(fc.Direct) == 1 {
		if n, ok := rep.Funcs[fc.Direct[0]]; ok {
			return n + " (its interface table's entry)"
		}
	}
	if m := spin2Method.FindStringSubmatch(cn); m != nil {
		return fmt.Sprintf("%s.spin2's %s", m[1], m[2])
	}
	if strings.HasPrefix(cn, "ogo_") {
		return cn + " (the runtime)"
	}
	return cn + " (the C library)"
}

// probeSizes answers the size the target's compiler gives each of the objects names
// declares at file scope in c, the C compiled from inc, built in dir with a table of
// them (octogo.SizeProbe); nil where that does not build.
func probeSizes(c []byte, names []string, dir, inc string) []int {
	probe := octogo.SizeProbe(c, names)
	if probe == nil {
		return nil
	}
	cFile := filepath.Join(dir, "probe.c")
	if err := os.WriteFile(cFile, probe, 0o644); err != nil {
		return nil
	}
	out := filepath.Join(dir, "probe.binary")
	if _, err := compileC(cFile, out, inc, io.Discard, io.Discard); err != nil {
		return nil
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil
	}
	sizes := octogo.ProbedSizes(b)
	if len(sizes) != len(names) {
		return nil
	}
	return sizes
}

// count is n things, thing being one of them.
func count(n int, thing string) string {
	return fmt.Sprintf("%d %s%s", n, thing, plural(n))
}

// layoutListing answers a listing of addresses of the program whose assembly is
// listing and whose binary is built, and the binary that listing is of: built's own
// where its assembly, assembled again with a listing, makes the same bytes (exact),
// and otherwise that of the program's C, cFile, compiled again with one. The
// backend's assembler does not take every assembly it writes back
// ("Changing hub value for symbol"), and with a listing of the C it writes the C's
// lines into the assembly and optimizes around them.
func layoutListing(listing, built []byte, cFile, inc, dir string) (lst, binary []byte, exact bool, err error) {
	asm := filepath.Join(dir, "prog.spin2")
	if err := os.WriteFile(asm, listing, 0o644); err != nil {
		return nil, nil, false, err
	}
	again := filepath.Join(dir, "prog.binary")
	if _, err := compileC(asm, again, "", io.Discard, io.Discard, "--listing"); err == nil {
		if b, err := os.ReadFile(again); err == nil && bytes.Equal(b, built) {
			if l, err := os.ReadFile(filepath.Join(dir, "prog.lst")); err == nil {
				return l, b, true, nil
			}
		}
	}
	out := filepath.Join(dir, "listed.binary")
	if _, err := compileC(cFile, out, inc, io.Discard, io.Discard, "--listing"); err != nil {
		return nil, nil, false, fmt.Errorf("report: %v", err)
	}
	if binary, err = os.ReadFile(out); err != nil {
		return nil, nil, false, fmt.Errorf("report: %v", err)
	}
	if lst, err = os.ReadFile(filepath.Join(dir, "listed.lst")); err != nil {
		return nil, nil, false, fmt.Errorf("report: %v", err)
	}
	return lst, binary, false, nil
}
