// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// TestGoSlots pins which programs give each go statement a slot of its own
// (goslots.go): one whose every go statement runs at most once, and no more of them
// than there are cogs beside main's. Every other program has the shared pool, seven
// slots any goroutine may run on, and so does one where the question has no answer
// it can prove: a name spelled twice in the package declaring the function counts
// as a second call.
func TestGoSlots(t *testing.T) {
	const worker = "var out = make(chan int)\n\nfunc work(n int) {\n\tout <- n\n}\n\n"
	for _, test := range []struct {
		name  string
		src   string
		lib   string // a package "lib" beside the program, if any
		slots int    // the slots of their own; 0 for the shared pool
	}{
		{"one in main", worker + "func main() {\n\tgo work(1)\n\tprintln(<-out)\n}\n", "", 1},
		{"two in main", worker + "func main() {\n\tgo work(1)\n\tgo work(2)\n\tprintln(<-out + <-out)\n}\n", "", 2},
		{"in a branch of main", worker + "var n = 1\n\nfunc main() {\n\tif n > 0 {\n\t\tgo work(1)\n\t} else {\n\t\tgo work(2)\n\t}\n\tprintln(<-out)\n}\n", "", 2},
		{"in a loop", worker + "func main() {\n\tfor i := 0; i < 2; i++ {\n\t\tgo work(i)\n\t}\n\tprintln(<-out + <-out)\n}\n", "", 0},
		{"in a function literal", worker + "func main() {\n\tf := func() {\n\t\tgo work(1)\n\t}\n\tf()\n\tprintln(<-out)\n}\n", "", 0},
		{"started as a literal", worker + "func main() {\n\tgo func(n int) {\n\t\tout <- n\n\t}(3)\n\tprintln(<-out)\n}\n", "", 1},
		{"in a function called once", worker + "func start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tstart()\n\tprintln(<-out)\n}\n", "", 1},
		{"in a function called twice", worker + "func start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tstart()\n\tstart()\n\tprintln(<-out + <-out)\n}\n", "", 0},
		{"in a function called in a loop", worker + "func start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tfor i := 0; i < 1; i++ {\n\t\tstart()\n\t}\n\tprintln(<-out)\n}\n", "", 0},
		{"in a function taken as a value", worker + "func start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tf := start\n\tf()\n\tprintln(<-out)\n}\n", "", 0},
		{"in a function started once", worker + "func start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tgo start()\n\tprintln(<-out)\n}\n", "", 2},
		{"in a function called by one called once", worker + "func inner() {\n\tgo work(1)\n}\n\nfunc outer() {\n\tinner()\n}\n\nfunc main() {\n\touter()\n\tprintln(<-out)\n}\n", "", 1},
		{"in a function calling itself", worker + "var n = 1\n\nfunc start() {\n\tgo work(1)\n\tif n > 1 {\n\t\tstart()\n\t}\n}\n\nfunc main() {\n\tstart()\n\tprintln(<-out)\n}\n", "", 0},
		{"in a function named like a local", worker + "func start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tstart()\n\tvar start = 2\n\tprintln(<-out + start)\n}\n", "", 0},
		// A field's declaration is a bare name of the package too; a selector of
		// a value is not.
		{"in a function named like a field", worker + "type T struct{ start int }\n\nfunc start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tvar t T\n\tt.start = 2\n\tstart()\n\tprintln(<-out + t.start)\n}\n", "", 0},
		{"in a function named like another type's field", worker + "import \"lib\"\n\nfunc start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tstart()\n\tprintln(<-out + lib.T{}.Start)\n}\n", "type T struct{ Start int }\n", 1},
		{"in a method", worker + "type T struct{}\n\nfunc (T) start() {\n\tgo work(1)\n}\n\nfunc main() {\n\tvar t T\n\tt.start()\n\tprintln(<-out)\n}\n", "", 0},
		{"in an init", worker + "func init() {\n\tgo work(1)\n}\n\nfunc main() {\n\tprintln(<-out)\n}\n", "", 1},
		{"in a package variable's initializer", worker + "func start() bool {\n\tgo work(1)\n\treturn true\n}\n\nvar started = start()\n\nfunc main() {\n\tprintln(<-out, started)\n}\n", "", 1},
		{"after a label a goto goes back to", worker + "var n = 1\n\nfunc main() {\nagain:\n\tgo work(n)\n\tprintln(<-out)\n\tif n < 2 {\n\t\tn++\n\t\tgoto again\n\t}\n}\n", "", 0},
		{"in a main the program calls", worker + "var n = 1\n\nfunc main() {\n\tgo work(n)\n\tprintln(<-out)\n\tif n < 2 {\n\t\tn++\n\t\tmain()\n\t}\n}\n", "", 0},
		{"eight in main", worker + "func main() {\n" + strings.Repeat("\tgo work(1)\n", 8) + "\tfor i := 0; i < 8; i++ {\n\t\tprintln(<-out)\n\t}\n}\n", "", 0},
		{"seven in main", worker + "func main() {\n" + strings.Repeat("\tgo work(1)\n", 7) + "\tfor i := 0; i < 7; i++ {\n\t\tprintln(<-out)\n\t}\n}\n", "", 7},
		// Another package's names are its own: a constant spelled like the
		// function is no call of it, nor is a function of the same name there.
		{"beside another package's name", worker + "import \"lib\"\n\nfunc drives() {\n\tgo work(lib.Drives)\n}\n\nfunc main() {\n\tdrives()\n\tprintln(<-out, lib.Drives, lib.Count())\n}\n",
			"const Drives = 8\n\nconst drives = 8\n\nfunc Count() int {\n\treturn drives\n}\n\nfunc drives2() {}\n", 1},
		{"in another package's function called once", "import \"lib\"\n\nfunc main() {\n\tlib.Start()\n\tprintln(<-lib.Out)\n}\n",
			"var Out = make(chan int)\n\nfunc work() {\n\tOut <- 4\n}\n\nfunc Start() {\n\tgo work()\n}\n", 1},
		{"in another package's function called twice", "import \"lib\"\n\nfunc main() {\n\tlib.Start()\n\tlib.Start()\n\tprintln(<-lib.Out + <-lib.Out)\n}\n",
			"var Out = make(chan int)\n\nfunc work() {\n\tOut <- 4\n}\n\nfunc Start() {\n\tgo work()\n}\n", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := test.src
			if strings.HasPrefix(src, worker+"import") {
				// An import goes first.
				i := strings.Index(src, "import")
				j := strings.Index(src[i:], "\n") + i + 1
				src = src[i:j] + "\n" + worker + src[j:]
			}
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			if test.lib != "" {
				fsys["lib/lib.ogo"] = &fstest.MapFile{Data: []byte(test.lib)}
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err != nil {
				t.Fatalf("Build: %v\n%s", err, src)
			}
			var b bytes.Buffer
			if err := EmitC(pkg, &b, Checked()); err != nil {
				t.Fatalf("EmitC: %v", err)
			}
			c := b.Bytes()
			got := 0
			if m := regexp.MustCompile(`(?m)^#define OGO_SLOTS (\d+)$`).FindSubmatch(c); m != nil {
				got, _ = strconv.Atoi(string(m[1]))
			} else if !bytes.Contains(c, []byte("#define OGO_SLOTS (OGO_COGS - 1)\n")) {
				t.Fatalf("no goroutine pool in\n%s", c)
			}
			if got != test.slots {
				t.Fatalf("%d slots of their own, want %d\n%s", got, test.slots, src)
			}
			slots := GoSlots(c)
			if test.slots == 0 {
				if len(slots) != 1 || slots[0].Tramps != nil {
					t.Fatalf("GoSlots: %v, want the shared pool", slots)
				}
				return
			}
			if len(slots) != test.slots {
				t.Fatalf("GoSlots: %v, want %d", slots, test.slots)
			}
			for i, s := range slots {
				if len(s.Tramps) != 1 || s.Longs != defaultGoStackLongs {
					t.Fatalf("GoSlots: slot %d %v, want one trampoline and %d longs", i, s, defaultGoStackLongs)
				}
			}
		})
	}
}

// TestWithGoSlots: the sizes the build writes into the C are the ones GoSlots
// reads back, slot by slot or one for every slot, and nothing else changes.
func TestWithGoSlots(t *testing.T) {
	own := []byte("x\n#define OGO_SLOTS 2\n#define OGO_STACK_LONGS_0 256 // ogo_go0\nstatic long a;\n#define OGO_STACK_LONGS_1 256 // ogo_go1 ogo_go4\ny\n")
	got := WithGoSlots(own, []int{160, 512})
	if want := "x\n#define OGO_SLOTS 2\n#define OGO_STACK_LONGS_0 160 // ogo_go0\nstatic long a;\n#define OGO_STACK_LONGS_1 512 // ogo_go1 ogo_go4\ny\n"; string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	s := GoSlots(got)
	if len(s) != 2 || s[0].Longs != 160 || s[1].Longs != 512 || strings.Join(s[1].Tramps, " ") != "ogo_go1 ogo_go4" {
		t.Fatalf("GoSlots: %v", s)
	}
	if s := GoSlots(WithGoSlots(own, []int{96})); len(s) != 2 || s[0].Longs != 96 || s[1].Longs != 96 {
		t.Fatalf("one size for every slot: %v", s)
	}
	shared := []byte("#define OGO_SLOTS (OGO_COGS - 1)\n#define OGO_STACK_LONGS 256\n")
	if s := GoSlots(WithGoSlots(shared, []int{784})); len(s) != 1 || s[0].Longs != 784 || s[0].Tramps != nil {
		t.Fatalf("shared: %v", s)
	}
	if s := GoSlots([]byte("int main(void) { return 0; }\n")); s != nil {
		t.Fatalf("no goroutine: %v", s)
	}
}
