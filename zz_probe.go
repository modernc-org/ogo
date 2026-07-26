//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing/fstest"

	"modernc.org/ogo/internal/octogo"
)

func main() {
	src, err0 := os.ReadFile(os.Args[1])
	if err0 != nil {
		panic(err0)
	}
	fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: src}}
	pkg, err := octogo.Build(-1, []string{"main.ogo"}, fsys)
	if err != nil {
		fmt.Printf("BUILD ERROR: %v\n", err)
		os.Exit(1)
	}
	var c bytes.Buffer
	if err := octogo.EmitC(pkg, &c, octogo.Checked()); err != nil {
		fmt.Printf("EMIT ERROR: %v\n", err)
		os.Exit(1)
	}
	if len(os.Args) > 2 && os.Args[2] == "-c" {
		os.Stdout.Write(c.Bytes())
		return
	}
	dir, _ := os.MkdirTemp("", "probe")
	defer os.RemoveAll(dir)
	csrc := filepath.Join(dir, "main.c")
	os.WriteFile(csrc, c.Bytes(), 0o644)
	shim, _ := filepath.Abs(filepath.Join("internal", "octogo", "testdata", "hostp2"))
	bin := filepath.Join(dir, "prog")
	cc, err := exec.Command("cc", "-std=gnu11", "-Wall", "-Wextra", "-Wno-unused-function", "-Wno-format", "-I", shim, "-o", bin, csrc, "-lpthread").CombinedOutput()
	if err != nil {
		fmt.Printf("CC ERROR: %v\n%s\n", err, cc)
		os.Exit(1)
	}
	// A warning is not an error to the compiler, but it is to TestEmitCRun, which
	// fails on any output at all. Swallowing them here would let a probe call a
	// program clean that the suite rejects -- which is how an unused receiver went
	// unnoticed. The flags are the ones that test uses, so the two agree on what
	// counts.
	if len(bytes.TrimSpace(cc)) != 0 {
		fmt.Printf("CC WARNED:\n%s\n", cc)
	}
	out, _ := exec.Command(bin).CombinedOutput()
	os.Stdout.Write(out)
}
