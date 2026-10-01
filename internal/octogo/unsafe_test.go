// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmitCUnsafeLifetime asks what the lifetime rules make of unsafe.Pointer. A
// conversion to one is the pointer it converts, and a conversion back is too, so
// every rule about an address asks through them (unsafeConvOperand); a uintptr is a
// number, which carries an address where no rule can follow it, so the address of
// storage of a frame is refused as one, and a function turning a parameter into one
// is taken to keep the parameter (uintptrConvOperands). Each way a frame's address
// reaches somewhere it outlives the frame, and the controls over package storage and
// over an address only read through.
func TestEmitCUnsafeLifetime(t *testing.T) {
	const hdr = `import "unsafe"

var gp unsafe.Pointer

var gu uintptr

var gq *int

var gx int

`
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"a uintptr of a local's address", `func run() {
	var x int
	n := uintptr(unsafe.Pointer(&x))
	_ = n
}

func main() { run() }
`, true},
		{"a uintptr of a local's address stored", `func run() {
	var x int
	gu = uintptr(unsafe.Pointer(&x))
}

func main() { run() }
`, true},
		{"a uintptr of a pointer held in a local", `func run() {
	var x int
	up := unsafe.Pointer(&x)
	gu = uintptr(up)
}

func main() { run() }
`, true},
		{"a uintptr of an element of a slice of a local array", `func run() {
	var a [4]int
	s := a[:]
	gu = uintptr(unsafe.Pointer(&s[0]))
}

func main() { run() }
`, true},
		{"an unsafe.Pointer stored", `func run() {
	var x int
	gp = unsafe.Pointer(&x)
}

func main() { run() }
`, true},
		{"an unsafe.Pointer returned", `func run() unsafe.Pointer {
	var x int
	return unsafe.Pointer(&x)
}

func main() { _ = run() }
`, true},
		{"converted back and stored", `func run() {
	var x int
	gq = (*int)(unsafe.Pointer(&x))
}

func main() { run() }
`, true},
		{"a callee making a uintptr of its parameter", `func keep(p *int) { gu = uintptr(unsafe.Pointer(p)) }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a callee making a uintptr of its parameter into a local", `func keep(p *int) int {
	n := uintptr(unsafe.Pointer(p))
	return int(n & 3)
}

func run() {
	var x int
	_ = keep(&x)
}

func main() { run() }
`, true},
		{"a callee keeping an unsafe.Pointer", `func keep(p unsafe.Pointer) { gp = p }

func run() {
	var x int
	keep(unsafe.Pointer(&x))
}

func main() { run() }
`, true},
		{"a callee keeping its parameter converted", `func keep(p *int) { gp = unsafe.Pointer(p) }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"a callee handing its parameter back converted", `func pass(p *int) unsafe.Pointer { return unsafe.Pointer(p) }

func run() {
	var x int
	gp = pass(&x)
}

func main() { run() }
`, true},
		{"a goroutine handed one", `func worker(p unsafe.Pointer, done chan bool) {
	*(*int)(p) = 1
	done <- true
}

var done chan bool

func run() {
	var x int
	go worker(unsafe.Pointer(&x), done)
	<-done
}

func main() { run() }
`, true},
		{"through an alias, stored", `type P = unsafe.Pointer

func run() {
	var x int
	gp = P(&x)
}

func main() { run() }
`, true},
		{"through an alias, made a uintptr", `type P = unsafe.Pointer

func run() {
	var x int
	gu = uintptr(P(&x))
}

func main() { run() }
`, true},
		{"through an alias, kept by a callee", `type P = unsafe.Pointer

func keep(p *int) { gp = P(p) }

func run() {
	var x int
	keep(&x)
}

func main() { run() }
`, true},
		{"package storage", `func keep(p *int) { gu = uintptr(unsafe.Pointer(p)) }

func keep2(p unsafe.Pointer) { gp = p }

func main() {
	gp = unsafe.Pointer(&gx)
	gu = uintptr(unsafe.Pointer(&gx))
	gq = (*int)(unsafe.Pointer(&gx))
	keep(&gx)
	keep2(unsafe.Pointer(&gx))
}
`, false},
		{"a local read and written through", `func bits(f float32) uint32 { return *(*uint32)(unsafe.Pointer(&f)) }

func run() int {
	var x int = 5
	p := (*int)(unsafe.Pointer(&x))
	*p = 6
	*(*int)(unsafe.Pointer(&x)) += 1
	return x
}

func main() { println(run(), bits(1)) }
`, false},
		{"a number made of package storage and back", `func main() {
	u := uintptr(unsafe.Pointer(&gx))
	*(*int)(unsafe.Pointer(u)) = 3
	println(gx)
}
`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := hdr + test.src
			fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refuse && err == nil:
				t.Errorf("a reference to this frame left it:\n%s", src)
			case test.refuse && !strings.Contains(err.Error(), "outlive") && !strings.Contains(err.Error(), "lifetime"):
				t.Errorf("refused, but not for its lifetime: %v", err)
			case !test.refuse && err != nil:
				t.Errorf("refused: %v\n%s", err, src)
			}
		})
	}
}

// spin2Unsafe is a driver reading a parameter block the way p2-11's VGA driver does:
// an address written into the block as a long, read back and read through.
const spin2Unsafe = `PUB third(params) : r
  r := long[long[params][1]][2]
`

// TestOnBoardUnsafe runs what only the board can: an address kept in a uint32 --
// the host's are 64 bits -- read back by a Spin2 method and by the program, and a
// read of hub RAM at a fixed address, 0x14, where the backend keeps the clock
// frequency that p2.ClockFreq reads.
func TestOnBoardUnsafe(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "prog.binary")
	src := `import (
	"p2"
	"unsafe"
)

type Screen [8]uint32

var params [4]uint32

var screen Screen

func third(params unsafe.Pointer) uint32

func main() {
	screen[2] = 1234
	params[1] = uint32(uintptr(unsafe.Pointer(&screen)))
	println(third(unsafe.Pointer(&params)))
	s := (*Screen)(unsafe.Pointer(uintptr(params[1])))
	s[3] = 99
	println(screen[3], *(*uint32)(unsafe.Pointer(uintptr(0x14))) == p2.ClockFreq())
}
`
	if err := boardBuildTree(ogo, dir, map[string]string{"main.ogo": src, "drv.spin2": spin2Unsafe}, bin, ""); err != nil {
		t.Fatal(err)
	}
	const want = "1234\n99 true"
	for attempt := 1; ; attempt++ {
		out, matched := boardLoad(ogo, port, bin, "99 true")
		if matched && strings.Contains(strings.ReplaceAll(out, "\r", ""), want) {
			return
		}
		if attempt == boardAttempts {
			t.Fatalf("board output did not contain %q after %d attempts\ngot:\n%s", want, boardAttempts, out)
		}
		t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
	}
}
