// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spin2Drv is a Spin2 object of a driver's shape: a method starting its PASM on a
// cog of its own, one stopping it, and one whose inline PASM runs in the cog that
// calls it.
const spin2Drv = `VAR
  long cog

PUB start(parm) : ok
  ok := cog := coginit(COGEXEC_NEW, @entry, parm) + 1

PUB stop()
  if cog
    cogstop(cog - 1)
    cog := 0

PUB popcount(x) : n
  org
        ones    n, x
  end

DAT
        org     0
entry   rdlong  val, ptra
        add     val, #1
        wrlong  val, ptra
        jmp     #$

val     long    0
`

// spin2Lib is a library package whose functions declared without a body are bound
// to spin2Drv's methods, and which exports functions calling them.
const spin2Lib = `func start(p *int32) int
func stop()
func popcount(x uint32) int

// Start starts the driver's cog on the cell p points at.
func Start(p *int32) int {
	return start(p)
}

// Stop stops it.
func Stop() {
	stop()
}

// Ones counts the bits of x set.
func Ones(x uint32) int {
	return popcount(x)
}
`

// spin2Module writes a module whose package drv carries spin2Drv, and returns its
// root.
func spin2Module(t *testing.T) string {
	root := t.TempDir()
	write(t, filepath.Join(root, "ogo.mod"), "module example.com/proj\n")
	write(t, filepath.Join(root, "drv", "drv.spin2"), spin2Drv)
	write(t, filepath.Join(root, "drv", "drv.ogo"), spin2Lib)
	return root
}

// TestBuildSpin2 builds a program importing a package that carries a Spin2 object:
// the C imports the object by its path from the module's root, which ogo build
// hands the backend as an include directory, and the backend says nothing. And a
// program handing the package a local's address is refused, the method being out of
// the compiler's sight and so taken to keep what it is given.
func TestBuildSpin2(t *testing.T) {
	root := spin2Module(t)
	write(t, filepath.Join(root, "cmd", "demo", "main.ogo"), `import (
	"example.com/proj/drv"
	"p2"
)

var cell int32 = 99

func main() {
	ok := drv.Start(&cell)
	p2.WaitMs(10)
	println("started", ok, "cell", cell, "ones", drv.Ones(0xF0F0))
	drv.Stop()
}
`)
	write(t, filepath.Join(root, "cmd", "bad", "main.ogo"), `import "example.com/proj/drv"

func main() {
	var local int32
	println(drv.Start(&local))
}
`)
	out := filepath.Join(root, "demo.binary")
	var buf bytes.Buffer
	if code, err := Build([]string{"-o", out, filepath.Join(root, "cmd", "demo")}, nil, &buf, &buf); err != nil || code != 0 {
		t.Fatalf("build: code=%d err=%v\n%s", code, err, buf.String())
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		t.Fatalf("the backend was not silent:\n%s", s)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Fatalf("no binary: %v", err)
	}

	buf.Reset()
	_, err := Build([]string{"-o", filepath.Join(root, "bad.binary"), filepath.Join(root, "cmd", "bad")}, nil, &buf, &buf)
	if want := "cannot pass the address of local variable local to Start"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want an error saying %q", err, want)
	}
}

// TestTestSpin2 compiles the tests of a package that carries a Spin2 object, which
// ogo test builds as ogo build does.
func TestTestSpin2(t *testing.T) {
	root := spin2Module(t)
	write(t, filepath.Join(root, "drv", "drv_test.ogo"), `import "testing"

func TestOnes(t *testing.T) {
	if n := Ones(0xF0); n != 4 {
		println("Ones(0xF0) =", n, "want 4")
		t.Fail()
	}
}
`)
	var buf bytes.Buffer
	if code, err := Test([]string{"-c", filepath.Join(root, "drv")}, nil, &buf, &buf); err != nil || code != 0 {
		t.Fatalf("test -c: code=%d err=%v\n%s", code, err, buf.String())
	}
}
