// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// spin2Drv is a Spin2 object of the shape a driver has: a PUB method starting its
// PASM on a cog, one stopping it, a PRI method no Go function may be bound to, and
// the comments and strings a scanner could be fooled by.
const spin2Drv = `' A driver: PUB fake() in a comment is no method
{ PUB hidden() in a block comment is none either
  { and block comments nest: PUB nested() }
}
{{ a doc comment: PUB doc() }}
VAR
  long cog

PUB start(parm) : ok
  ok := cog := coginit(COGEXEC_NEW, @entry, parm) + 1

PUB Stop()
  if cog
    cogstop(cog - 1)
    cog := 0

pub add(a, b = 3) : r | t
  r := a + b

PRI helper() : r
  r := 1

DAT
msg     byte    "a { in a string opens no comment", 0
        org     0
entry   jmp     #$

PUB after() : r
  r := 7
`

func TestScanSpin2(t *testing.T) {
	methods := scanSpin2([]byte(spin2Drv))
	want := map[string][3]int{"start": {1, 1, 9}, "stop": {0, 0, 12}, "add": {2, 1, 17}, "after": {0, 1, 28}}
	if len(methods) != len(want) {
		t.Errorf("got %d methods, want %d: %v", len(methods), len(want), methods)
	}
	for key, w := range want {
		m := methods[key]
		if m == nil {
			t.Errorf("method %s not found", key)
			continue
		}
		if m.params != w[0] || m.results != w[1] || m.line != w[2] {
			t.Errorf("%s: params %d results %d line %d, want %v", key, m.params, m.results, m.line, w)
		}
	}
	if m := methods["stop"]; m != nil && m.name != "Stop" {
		t.Errorf("stop is named %q, want it as written, %q", m.name, "Stop")
	}
}

// TestSpin2Binding builds programs whose functions declared without a body are
// bound to a Spin2 object's methods, and the mistakes a binding refuses.
func TestSpin2Binding(t *testing.T) {
	for _, c := range []struct {
		name  string
		src   string
		spin2 string // "" is no .spin2 file at all
		err   string // a regexp the error matches; "" is no error
	}{
		{"bound", `func Start(p *[16]uint32) int
func STOP()
func add(a, b int) int
func main() { println(Start(nil), add(1, 2)); STOP() }
`, spin2Drv, ""},
		{"a result the method does not return discarded", `func start(p *int)
func main() { start(nil) }
`, spin2Drv, ""},
		{"no .spin2 file", `func start(p *int)
func main() { start(nil) }
`, "", `missing function body: start is implemented by no \.spin2 file`},
		{"no such method", `func launch(p *int)
func main() { launch(nil) }
`, spin2Drv, `no \.spin2 file of the package has a PUB method launch`},
		{"a private method", `func helper() int
func main() { println(helper()) }
`, spin2Drv, `has a PUB method helper`},
		{"parameters", `func start(p *int, q int) int
func main() { println(start(nil, 1)) }
`, spin2Drv, `start takes 2 parameters, and PUB start of drv\.spin2 takes 1`},
		{"a result the method lacks", `func stop() int
func main() { println(stop()) }
`, spin2Drv, `stop returns a result, and PUB Stop of drv\.spin2 returns none`},
		{"two results", `func add(a, b int) (int, int)
func main() { x, y := add(1, 2); println(x, y) }
`, spin2Drv, `add returns 2 results`},
		{"variadic", `func add(a int, b ...int) int
func main() { println(add(1, 2)) }
`, spin2Drv, `takes no variadic parameter`},
		{"a method", `type T struct{ x int }
func (t *T) start(p *int) int
func main() { var t T; println(t.start(nil)) }
`, spin2Drv, `missing method body`},
	} {
		t.Run(c.name, func(t *testing.T) {
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(c.src)}}
			if c.spin2 != "" {
				fsys["drv.spin2"] = &fstest.MapFile{Data: []byte(c.spin2)}
			}
			_, err := Build(-1, []string{"main.ogo"}, fsys)
			switch {
			case c.err == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.err == "":
			case err == nil:
				t.Fatalf("no error, want one matching %q", c.err)
			case !regexp.MustCompile(c.err).MatchString(err.Error()):
				t.Fatalf("error %q does not match %q", err, c.err)
			}
		})
	}
}

// spin2Emit builds and emits a program of main.ogo beside drv.spin2 (spin2Drv),
// returning the C or the first error.
func spin2Emit(src string) (string, error) {
	fsys := fstest.MapFS{
		"main.ogo":  &fstest.MapFile{Data: []byte(src)},
		"drv.spin2": &fstest.MapFile{Data: []byte(spin2Drv)},
	}
	pkg, err := Build(-1, []string{"main.ogo"}, fsys)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := EmitC(pkg, &b, Checked(), Inline()); err != nil {
		return "", err
	}
	return b.String(), nil
}

// TestSpin2Emit asks what the C of a program calling Spin2 methods is: the one
// instance of the object, imported with the backend's `struct __using` under
// __FLEXC__, and each bound function a C function calling its method.
func TestSpin2Emit(t *testing.T) {
	c, err := spin2Emit(`var params [16]uint32

func start(p *[16]uint32) int
func Stop()
func add(a, b int) int

func main() {
	println(start(&params), add(1, 2))
	Stop()
}
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#ifdef __FLEXC__\nstruct __using(\"drv.spin2\") ogo_spin2_0;\n#else\n#error",
		"ogo_spin2_0.start(p);",
		"\togo_spin2_0.Stop();\n",
		"ogo_spin2_0.add(a, b);",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("the C lacks %q:\n%s", want, c)
		}
	}
	if strings.Count(c, "struct __using(") != 1 {
		t.Errorf("the object is imported %d times, want once", strings.Count(c, "struct __using("))
	}

	// A parameter left unnamed, where naming one would be pointless, is forwarded by
	// the name its prototype gives it.
	c, err = spin2Emit(`func add(int, int) int

func main() { println(add(1, 2)) }
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ogo_spin2_0.add(_ogo_unused0, _ogo_unused1);"; !strings.Contains(c, want) {
		t.Errorf("the C lacks %q:\n%s", want, c)
	}
}

// TestSpin2Refusals asks what a function a Spin2 object implements refuses: a
// reference to a frame, which the method is taken to keep, however it arrives --
// directly, through a function relaying it, deferred, on a cog, through a function
// value -- and what does not cross into a Spin2 method, whose values are longs.
func TestSpin2Refusals(t *testing.T) {
	for _, c := range []struct {
		name, src, err string
	}{
		{"a package variable's address", `var ps [16]uint32
func start(p *[16]uint32) int
func main() { println(start(&ps)) }
`, ""},
		{"a local's address", `func start(p *[16]uint32) int
func run() {
	var ps [16]uint32
	println(start(&ps))
}
func main() { run() }
`, `cannot pass the address of local variable ps to start: its parameter 1 reaches another cog`},
		{"through a relay", `func start(p *[16]uint32) int
func relay(p *[16]uint32) int { return start(p) }
func run() {
	var ps [16]uint32
	println(relay(&ps))
}
func main() { run() }
`, `cannot pass the address of local variable ps to relay`},
		{"deferred", `func start(p *[16]uint32) int
func run() {
	var ps [16]uint32
	defer start(&ps)
}
func main() { run() }
`, `cannot pass the address of local variable ps`},
		{"on a cog", `func start(p *[16]uint32) int
func run() {
	var ps [16]uint32
	go start(&ps)
}
func main() { run() }
`, `cannot pass the address of local variable ps to a goroutine`},
		{"a float", `func add(a float32, b int) int
func main() { println(add(1, 2)) }
`, `add: a parameter of type float32 does not cross into Spin2`},
		{"a string", `func add(a string, b int) int
func main() { println(add("x", 2)) }
`, `add: a parameter of type string does not cross into Spin2`},
		{"64 bits", `func add(a int64, b int) int
func main() { println(add(1, 2)) }
`, `add: a parameter of type int64 does not cross into Spin2`},
		{"a slice", `func add(a []int, b int) int
func main() { println(add(nil, 2)) }
`, `does not cross into Spin2`},
		{"a result of 64 bits", `func add(a, b int) uint64
func main() { println(add(1, 2)) }
`, `add: a result of type uint64 does not cross from Spin2`},
		{"a bool and a uint8 cross", `func add(a bool, b uint8) bool
func main() { println(add(true, 2)) }
`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := spin2Emit(c.src)
			switch {
			case c.err == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.err == "":
			case err == nil:
				t.Fatalf("no error, want one matching %q", c.err)
			case !regexp.MustCompile(c.err).MatchString(err.Error()):
				t.Fatalf("error %q does not match %q", err, c.err)
			}
		})
	}
}

// spin2Board is a Spin2 object for the board: a method starting PASM on a cog of
// its own, which increments the long it is handed, and one whose inline PASM runs
// in the cog calling it.
const spin2Board = `VAR
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

// TestOnBoardSpin2 runs a program whose functions a Spin2 object implements, on the
// board: a cog of PASM incrementing a package variable the program hands it, and
// inline PASM counting bits in the calling cog. The host's C compiler has no import
// of a Spin2 object, so this is the one place such a program runs.
func TestOnBoardSpin2(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "prog.binary")
	src := `import "p2"

var cell int32 = 41

func start(p *int32) int
func stop()
func popcount(x uint32) int

func main() {
	ok := start(&cell)
	p2.WaitMs(10)
	println("started", ok, "cell", cell)
	stop()
	println(popcount(0), popcount(1), popcount(0xFF), popcount(0xFFFFFFFF), popcount(0x80000001))
}
`
	if err := boardBuildTree(ogo, dir, map[string]string{"main.ogo": src, "drv.spin2": spin2Board}, bin, ""); err != nil {
		t.Fatal(err)
	}
	const want = "started 2 cell 42\n0 1 8 32 2"
	for attempt := 1; ; attempt++ {
		out, matched := boardLoad(ogo, port, bin, "0 1 8 32 2")
		if matched && strings.Contains(strings.ReplaceAll(out, "\r", ""), want) {
			return
		}
		if attempt == boardAttempts {
			t.Fatalf("board output did not contain %q after %d attempts\ngot:\n%s", want, boardAttempts, out)
		}
		t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
	}
}
