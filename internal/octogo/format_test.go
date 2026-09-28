// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The three programs below are in gofmt's layout, which TestFormatContinuation
// checks against gofmt itself where there is one.

// continuationOperands is every place a binary expression is written across
// lines: the operand after an operator that ends its line is one level in, and
// the levels follow Go's tree, not the grammar's three flat ones.
const continuationOperands = `type P struct {
	x, y int
}

func h(a, b int) int { return a + b }

func k(b bool) int {
	if b {
		return 1
	}
	return 0
}

func two() (int, int) {
	return 1 +
			2,
		3 *
			4
}

func g(a, b, c int, p, q, r bool) int {
	v1 := p ||
		q &&
			r
	v2 := p &&
		q ||
		r
	v3 := p ||
		q ||
		r &&
			p
	v4 := a +
		b*
			c
	v5 := a*
		b +
		c
	v6 := a +
		b -
		c*
			a*
			b
	v7 := (a +
		b) *
		c
	v8 := a == b ||
		b == c &&
			c ==
				a
	v9 := h(a+
		b,
		c)
	v10 := h(
		a+
			b,
		c*
			a,
	)
	v11 := -a +
		-b
	v12 := a<<2 |
		b<<4 |
		c
	v13 := []int{a +
		b, c}
	v14 := P{
		x: a +
			b,
		y: c,
	}
	v15 := a < b && (b < c ||
		c < a) &&
		p
	var v16 = a +
		b
	var v17, v18 = a +
		b, b +
		c
	v19, v20 := a+
		b,
		b+
			c
	arr := [4]int{}
	arr[a+
		b] = c +
		a
	v1 = v1 ||
		p
	a += b +
		c
	if v := a +
		b; v > c &&
		p {
		return v
	}
	for i := a +
		b; i < c &&
		p; i += a +
		b {
		a++
	}
	switch a +
		b {
	case a +
		b, c +
		a:
		a--
	}
	switch {
	case p &&
		q,
		r:
		a--
	}
	for p &&
		q {
		p = false
	}
	ch := make(chan int, 1)
	ch <- a +
		b
	defer h(a+
		b, c)
	println(v1, v2, v3, v4, v5, v6, v7, v8, v9, v10, v11, v12, len(v13), v14.x, v15, v16, v17, v18, v19, v20, arr[0], <-ch)
	println("a" +
		"b" +
		"c")
	if a < b &&
		b < c ||
		p {
		return h(a, b) +
			h(b,
				c) +
			k(p &&
				q)
	}
	return a +
		b
}

func main() {
	x, y := two()
	println(g(1, 2, 3, true, false, true), x, y)
}
`

// continuationLists is the lists, the calls and the selectors: a list indents
// from the first element that begins a line, a return's as a whole where more
// than one value breaks, and a selector on a line of its own takes the arguments
// of its call with it.
const continuationLists = `type P struct {
	x, y int
}

type T struct {
	p P
	n int
}

func (t T) get() T { return t }

func (t T) add(a, b int) T {
	t.n += a + b
	return t
}

func h(a, b int) int { return a + b }

func ap(f func(int) int, a int) int { return f(a) }

func two() (int, int) {
	return h(1,
		2), 3
}

func three() (int, int, int) {
	return 1,
		2,
		3
}

func four() (int, int) {
	return ap(func(v int) int {
			return v
		}, 1), ap(func(v int) int {
			return v + 1
		}, 2)
}

func g(a, b, c int) int {
	s1 := []P{{
		x: 1,
	}, {
		x: 2,
	}}
	s2 := []int{1, 2,
		3, 4}
	s3 := []int{1, 2,
		3, 4,
	}
	s4 := []int{
		1, 2,
		// trailing
	}
	s5 := []int{
		// nothing
	}
	s6 := [][]int{{1,
		2}, {3,
		4}}
	v1 := ap(func(v int) int {
		return v + a
	},
		b)
	v2 := ap(func(v int) int {
		return v + a
	}, b)
	v3 := h(h(a,
		b),
		c)
	v4 := h(a, h(b,
		c))
	var t T
	t2 := t.
		get().
		add(1,
			2).
		get()
	v5 := t.
		p.
		x
	v6 :=
		a +
			b
	v7, v8 :=
		a,
		b
	v9 := h(
		a,
		// comment
		b,
	// last
	)
	x, y :=
		two()
	var arr [8]int
	arr[a] = arr[b+
		c]
	println(len(s1), len(s2), len(s3), len(s4), len(s5), len(s6), v1, v2, v3, v4, t2.n, v5, v6, v7, v8, v9, x, y,
		arr[0])
	println(a,
		b,
		c)
	println(
		a, b)
	{
		a++
		// trailing in a block
	}
	switch a {
	case 1,
		2,
		3:
		a--
	// before a case
	case 4:
	}
	return a
}

func main() {
	x, y := two()
	a, b, c := three()
	d, e := four()
	println(g(1, 2, 3), x, y, a, b, c, d, e)
}
`

// commentsBeforeClosers is a comment on a line of its own ahead of a token that
// stands one level out: what is ahead of a closing brace is the last thing of
// what the braces hold, and what is ahead of a "case" or a closing parenthesis is
// the token's where it was written in the token's column.
const commentsBeforeClosers = `const (
	a = 1
	// b = 2, with the specs
)

var (
// only a comment
)

type T struct {
	x int
	// y int
}

type I interface {
	M()
	// N()
}

func h(a, b int) int { return a + b }

func e() {
	// nothing
}

func g(n int) int {
	v := h(
		1,
		2,
		// with the arguments
	)
	w := h(
		1,
		2,
	// with the parenthesis
	)
	switch n {
	// before the first
	case 1:
		n++
		// with the body
	case 2:
		n--
	// with the case
	case 3:
		// with an empty body
	case 4:
	// with the case, the body empty
	case 5:
		n++
		// one of two, with the body
	// two of two, with the case
	case 6:
		n++
	// one of two, with the case
	// two of two, further in
	default:
		n++
		// last, with the body
	}
	switch n {
	case 7:
		// last, with the case's column
	}
	ch := make(chan int, 1)
	select {
	case ch <- 1:
		n++
		// with the body
	case x := <-ch:
		n += x
	// with the case
	default:
		// last
	}
	for n < 10 {
		n++
		// the loop's last
	}
	if n > 3 {
		n++
		// the if's last
	} else {
		n--
		// the else's last
	}
	f := func() {
		n++
		// the literal's last
	}
	f()
	s := []int{
		1,
		// the elements' last
	}
outer:
	for {
		// before a break
		break outer
	}
	return v + w + len(s)
	// after the return
}

func main() {
	e()
	println(g(1), a)
}
`

// TestFormatContinuation pins the indentation of what continues a line, which
// before 2026-09-28 came back at the level of the statement it continued in every
// position but a call's arguments and a literal's body (p2-11's OCTOGO.md: "ogo
// fmt takes the indentation from the second line of an expression"). Each program
// is formatted as it stands, which must change nothing, and with every line's
// indentation taken away, which must give it all back -- but for the comments,
// whose place gofmt reads off the column they were written in.
func TestFormatContinuation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		src     string
		rebuild bool // the layout is a function of the line breaks alone
	}{
		{"operands", continuationOperands, true},
		{"lists", continuationLists, true},
		{"comments", commentsBeforeClosers, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			formatCheck(t, tc.src, tc.src)
			if tc.rebuild {
				var bare []string
				for _, line := range strings.Split(tc.src, "\n") {
					bare = append(bare, strings.TrimLeft(line, "\t"))
				}
				formatCheck(t, strings.Join(bare, "\n"), tc.src)
			}
			gofmt, err := exec.LookPath("gofmt")
			if err != nil {
				return
			}
			goSrc := filepath.Join(t.TempDir(), "x.go")
			if err := os.WriteFile(goSrc, []byte("package main\n\n"+tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(gofmt, goSrc).Output()
			if err != nil {
				t.Fatalf("gofmt: %v", err)
			}
			if g := string(bytes.TrimPrefix(out, []byte("package main\n\n"))); g != tc.src {
				t.Errorf("the expectation is not gofmt's layout:\n%s", firstDiff(tc.src, g))
			}
		})
	}
}
