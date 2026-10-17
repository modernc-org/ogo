// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// image is the layout of a binary in Hub RAM, read off the backend's listing of
// addresses (--listing): where each part begins and how large each function is.
type image struct {
	size    int         // bytes, the whole binary
	stack   int         // where main's stack begins, its first long the binary's last
	cog     int         // the code the cogs load into their registers
	code    int         // the hub functions
	strs    int         // the string literals, a pool after the functions
	data    int         // the program's data, _dat_
	objs    []imageFunc // the DAT of each Spin2 object, which follows the program's data
	libData int         // the library's, __system__dat_
	tables  int         // the method table and what follows it up to the stack
	funcs   []imageFunc
}

// imageFunc is a hub function of the binary: its name as the listing writes it
// and its bytes.
type imageFunc struct {
	name string
	size int
}

var (
	lstLabel  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	lstAddr   = regexp.MustCompile(`^([0-9a-f]{5})\b`)
	lstObjDat = regexp.MustCompile(`^_(\w+)_spin2_dat_$`)
)

// readImage reads the layout of the binary of size bytes a listing of addresses
// describes; ok is false where the listing does not have the parts every binary
// does. What follows the method table -- the objects' memory, the first long of
// main's stack -- is counted with it.
func readImage(lst []byte, size int) (r image, ok bool) {
	type label struct {
		name string
		addr int
	}
	var labels []label
	var strAt []int // where a string literal's bytes are
	at := map[string]int{}
	for _, line := range strings.Split(string(lst), "\n") {
		bar := strings.IndexByte(line, '|')
		if bar < 0 {
			continue
		}
		m := lstAddr.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		addr, err := strconv.ParseInt(m[1], 16, 64)
		if err != nil {
			continue
		}
		src := strings.TrimRight(line[bar+1:], " \t\r")
		src = strings.TrimPrefix(src, " ")
		if lstLabel.MatchString(src) {
			labels = append(labels, label{src, int(addr)})
			if _, dup := at[src]; !dup {
				at[src] = int(addr)
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(src), `byte	"`) {
			strAt = append(strAt, int(addr))
		}
	}
	hub, okh := at["hubentry"]
	dat, okd := at["_dat_"]
	sys, oks := at["__system__dat_"]
	stack, okst := at["stackspace"]
	if !okh || !okd || !oks || !okst || !(hub < dat && dat <= sys && sys <= stack && stack <= size) {
		return r, false
	}
	r.size, r.stack = size, stack
	r.cog = hub - at["entry"]
	r.data = sys - dat
	// A Spin2 object's DAT is a block of its own after the program's data,
	// _<object>_spin2_dat_.
	var objs []label
	for _, l := range labels {
		if l.addr > dat && l.addr < sys && lstObjDat.MatchString(l.name) {
			objs = append(objs, l)
		}
	}
	sort.SliceStable(objs, func(i, j int) bool { return objs[i].addr < objs[j].addr })
	for i, o := range objs {
		if i == 0 {
			r.data = o.addr - dat
		}
		end := sys
		if i+1 < len(objs) {
			end = objs[i+1].addr
		}
		r.objs = append(r.objs, imageFunc{lstObjDat.FindStringSubmatch(o.name)[1] + ".spin2", end - o.addr})
	}
	r.libData = size - sys
	if mt, ok := at["__methodtable__"]; ok && sys <= mt && mt <= stack {
		r.libData, r.tables = mt-sys, size-mt
	}
	// A function is a label in the hub code with a label of its name and _ret.
	var starts []label
	for _, l := range labels {
		if l.addr >= hub && l.addr < dat {
			if _, isFunc := at[l.name+"_ret"]; isFunc && strings.HasPrefix(l.name, "_") {
				starts = append(starts, l)
			}
		}
	}
	sort.SliceStable(starts, func(i, j int) bool { return starts[i].addr < starts[j].addr })
	// The string pool begins at the first string after the last function's return.
	pool := dat
	if len(starts) != 0 {
		last := at[starts[len(starts)-1].name+"_ret"]
		for _, a := range strAt {
			if a > last && a < pool {
				pool = a
			}
		}
		// back to the label the pool's first string stands under
		for _, l := range labels {
			if l.addr > last && l.addr <= pool && strings.HasPrefix(l.name, "LR__") {
				pool = l.addr
				break
			}
		}
	}
	r.strs = dat - pool
	r.code = pool - hub
	for i, s := range starts {
		end := pool
		if i+1 < len(starts) {
			end = starts[i+1].addr
		}
		r.funcs = append(r.funcs, imageFunc{s.name, end - s.addr})
	}
	return r, true
}
