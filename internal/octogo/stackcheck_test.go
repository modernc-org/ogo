// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStackChecks holds the C of the stack checks the build asks for: a check at
// the entry of each function named that the program has, each with its margin
// macro, against main's cog's end of stack or each cog's, which a goroutine's
// trampoline then records; the margins and the reserve the build writes in; and C
// the host's compiler takes, where the checks are nothing, running as it did.
func TestStackChecks(t *testing.T) {
	const src = `var out = make(chan int)

func depth(n int) int {
	if n == 0 {
		return 0
	}
	return 1 + depth(n-1)
}

func work(n int) {
	out <- depth(n)
}

func main() {
	go work(3)
	v := <-out
	println(v, depth(2))
}
`
	for _, perCog := range []bool{false, true} {
		_, c := markedFuncs(t, src, StackChecks([]string{"depth", "nosuch"}, perCog))
		for _, want := range []string{
			"int depth(int n) {\n\tOGO_STACK_CHECK(OGO_STACK_MARGIN_depth);\n",
			"\n#define OGO_STACK_MARGIN_depth 0\n",
			"\n#define OGO_STACK_RESERVE 0\n",
		} {
			if !strings.Contains(c, want) {
				t.Errorf("perCog %v: no %q in\n%s", perCog, want, c)
			}
		}
		if strings.Contains(c, "OGO_STACK_MARGIN_nosuch") {
			t.Errorf("perCog %v: a margin for a function the program does not have", perCog)
		}
		if got := strings.Contains(c, "\tOGO_STACK_SET(&OGO_GUARD_HI(a->ogo_slot));\n"); got != perCog {
			t.Errorf("perCog %v: the trampoline records its slot's end: %v", perCog, got)
		}
		if got := strings.Contains(c, "static unsigned ogo_stack_limit[8]"); got != perCog {
			t.Errorf("perCog %v: each cog's end of stack kept: %v", perCog, got)
		}

		margins, reserve := StackMargins(WithStackMargins([]byte(c), map[string]int{"depth": 48}, 568))
		if !maps.Equal(margins, map[string]int{"depth": 48}) || reserve != 568 {
			t.Errorf("perCog %v: margins %v, reserve %d; want depth's 48 and 568", perCog, margins, reserve)
		}

		cc, err := exec.LookPath("gcc")
		if err != nil {
			continue
		}
		shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		csrc := filepath.Join(dir, "main.c")
		if err := os.WriteFile(csrc, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "prog")
		out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra", "-Werror",
			"-Wno-unused-function", "-Wno-format", "-I", shim, "-o", bin, csrc, "-lpthread").CombinedOutput()
		if err != nil {
			t.Fatalf("perCog %v: cc: %v\n%s", perCog, err, out)
		}
		if got, err := exec.Command(bin).CombinedOutput(); err != nil || string(got) != "3 2\n" {
			t.Errorf("perCog %v: ran %q, %v; want \"3 2\\n\"", perCog, got, err)
		}
	}
}
