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
	"testing/fstest"
)

// TestEmitCRun compiles emitted C with a host compiler and runs it, checking what
// the program prints. The golden tests pin the shape of the output; this pins its
// behaviour, which is the only way to catch a lowering that reads correctly and
// computes the wrong thing.
//
// P2 intrinsics are supplied by testdata/hostp2, which backs cogs with pthreads and
// hardware locks with mutexes at the real 8-cog and 16-lock limits. Concurrency in
// particular cannot be checked any other way: a rendezvous needs a second cog, so
// inspecting the generated code proves nothing about whether two of them meet.
//
// Skipped when no C compiler is available, so the suite still runs anywhere.
func TestEmitCRun(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found; skipping the run-the-output tests")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		src  string
		want string
		// panics marks a program expected to abort through ogo_panic rather than
		// run to completion.
		panics bool
	}{
		{
			name: "arithmetic and control flow",
			src: `func main() {
	x := 17
	x %= 5
	x <<= 3
	x += 2
	println(x)
}
`,
			want: "18\n",
		},
		{
			name: "slices, arrays and access chains",
			src: `type P struct {
	v [2]int
}

type B struct {
	pts  []P
	grid [2][3]int
}

func main() {
	var b B
	b.pts = make([]P, 2, 2)
	b.pts[1].v[0] = 30
	b.grid[1][2] = 12
	t := b.pts[1:2]
	println(b.pts[1].v[0] + b.grid[1][2] + len(t))
}
`,
			want: "43\n",
		},
		{
			name: "defer captures at the defer, not the return",
			src: `func step(n int) {
	println(n)
}

func f(c int) {
	x := 1
	defer step(x)
	x = 99
	if c > 0 {
		y := 7
		defer step(y)
	}
	defer step(3)
}

func main() {
	f(1)
	println(0)
	f(0)
}
`,
			want: "3\n7\n1\n0\n3\n1\n",
		},
		{
			name: "goroutine hands a value to main",
			src: `func worker(ch chan int, n int) {
	ch <- n * 10
}

func main() {
	var ch chan int
	go worker(ch, 1)
	go worker(ch, 2)
	go worker(ch, 3)
	a := <-ch
	b := <-ch
	c := <-ch
	println(a + b + c)
}
`,
			want: "60\n",
		},
		{
			name: "select takes default, then blocks for a sender",
			src: `func worker(ch chan int) {
	ch <- 7
}

func main() {
	var ch chan int
	x := 0
	select {
	case x = <-ch:
		println(x)
	default:
		println(99)
	}
	go worker(ch)
	select {
	case x = <-ch:
		println(x)
	}
}
`,
			want: "99\n7\n",
		},
		{
			name: "package initialization runs before main",
			src: `func five() int {
	return 5
}

var a = 2
var b = a + 3
var c = five()
var ch chan int
var tally int

func init() {
	tally = a + b + c
}

func worker(k chan int) {
	k <- tally
}

func main() {
	go worker(ch)
	println(<-ch)
}
`,
			want: "12\n",
		},
		{
			name: "break and continue",
			src: `func main() {
	i := 0
	for {
		i++
		if i > 2 {
			break
		}
	}
	n := 0
	j := 0
	for j < 5 {
		j++
		if j == 2 {
			continue
		}
		n = n + j
	}
	println(i)
	println(n)
}
`,
			want: "3\n13\n",
		},
		{
			name: "index out of range traps",
			src: `func main() {
	s := make([]int, 2, 2)
	i := 5
	println(s[i])
}
`,
			panics: true,
		},
		{
			name: "more goroutines than cogs traps",
			src: `func spin(ch chan int) {
	ch <- 1
}

func main() {
	var ch chan int
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	go spin(ch)
	println(<-ch)
}
`,
			panics: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(test.src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			var buf bytes.Buffer
			if err := EmitC(pkg, &buf, Checked()); err != nil {
				t.Fatalf("EmitC: %v", err)
			}

			dir := t.TempDir()
			csrc := filepath.Join(dir, "main.c")
			if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "prog")
			// -Wall -Wextra so a lowering that provokes a diagnostic fails here
			// rather than being discovered on real hardware.
			out, err := exec.Command(cc, "-std=gnu11", "-Wall", "-Wextra", "-I", shim,
				"-o", bin, csrc, "-lpthread").CombinedOutput()
			if err != nil {
				t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, out, buf.String())
			}
			if len(bytes.TrimSpace(out)) != 0 {
				t.Errorf("cc warned:\n%s\n--- emitted ---\n%s", out, buf.String())
			}

			got, runErr := exec.Command(bin).CombinedOutput()
			if test.panics {
				if runErr == nil {
					t.Errorf("expected a panic, but the program exited cleanly with %q", got)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("run: %v\n%s", runErr, got)
			}
			if g := strings.ReplaceAll(string(got), "\r\n", "\n"); g != test.want {
				t.Errorf("output:\n got %q\nwant %q\n--- emitted ---\n%s", g, test.want, buf.String())
			}
		})
	}
}
