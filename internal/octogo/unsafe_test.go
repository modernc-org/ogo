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

var done = make(chan bool)

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

// TestEmitCUnsafeAddSliceLifetime is the lifetime matrix of unsafe.Add and
// unsafe.Slice: Add reaches what its pointer reaches, and a slice from Slice -- or
// one sliced from it, or an element's address in it -- is backed by the storage its
// pointer points at; each read through by frameRefOf, the summaries and callExprsIn
// as a conversion is (unsafeConvOperand, unsafeSliceAddrOperand). A store through a
// slice known to view a local marks the local (noteStoredThrough), which `s := a[:]`
// had not either. The controls over package storage and over reads stay taken.
func TestEmitCUnsafeAddSliceLifetime(t *testing.T) {
	const hdr = `import "unsafe"

var gs []uint32

var gp unsafe.Pointer

var gq *uint32

var garr [4]uint32

func keep(s []uint32) { gs = s }

func keepP(p unsafe.Pointer) { gp = p }

`
	for _, test := range []struct {
		name, src string
		refuse    bool
	}{
		{"a slice of a local returned", `func f() []uint32 { var a [4]uint32; return unsafe.Slice(&a[0], 2) }

func main() { f() }
`, true},
		{"a slice of a local stored", `func f() { var a [4]uint32; gs = unsafe.Slice(&a[0], 2) }

func main() { f() }
`, true},
		{"a slice of a local passed to a keeper", `func f() { var a [4]uint32; keep(unsafe.Slice(&a[0], 2)) }

func main() { f() }
`, true},
		{"a slice of a local held, then stored", `func f() { var a [4]uint32; s := unsafe.Slice(&a[0], 2); gs = s }

func main() { f() }
`, true},
		{"a slice of a local resliced", `func f() { var a [4]uint32; gs = unsafe.Slice(&a[0], 2)[1:] }

func main() { f() }
`, true},
		{"a slice of a local resliced twice", `func f() { var a [4]uint32; gs = unsafe.Slice(&a[0], 4)[1:][:2] }

func main() { f() }
`, true},
		{"a slice of a local resliced, returned", `func f() []uint32 { var a [4]uint32; return unsafe.Slice(&a[0], 2)[1:] }

func main() { f() }
`, true},
		{"a slice of a local resliced, passed", `func f() { var a [4]uint32; keep(unsafe.Slice(&a[0], 2)[1:]) }

func main() { f() }
`, true},
		{"an element's address of a slice of a local", `func f() { var a [4]uint32; gq = &unsafe.Slice(&a[0], 2)[1] }

func main() { f() }
`, true},
		{"a slice of a scalar local", `func f() { var x uint32; gs = unsafe.Slice(&x, 1) }

func main() { f() }
`, true},
		{"a slice through a pointer held", `func f() { var a [4]uint32; p := &a[0]; gs = unsafe.Slice(p, 2) }

func main() { f() }
`, true},
		{"a slice of a converted pointer", `func f() { var a [4]uint32; gs = unsafe.Slice((*uint32)(unsafe.Pointer(&a)), 2) }

func main() { f() }
`, true},
		{"a slice of an added pointer", `func f() { var a [4]uint32; gs = unsafe.Slice((*uint32)(unsafe.Add(unsafe.Pointer(&a), 4)), 2) }

func main() { f() }
`, true},
		{"an added pointer stored", `func f() { var a [4]uint32; gp = unsafe.Add(unsafe.Pointer(&a), 4) }

func main() { f() }
`, true},
		{"an added pointer passed to a keeper", `func f() { var a [4]uint32; keepP(unsafe.Add(unsafe.Pointer(&a), 4)) }

func main() { f() }
`, true},
		{"an added pointer converted and stored", `func f() { var a [4]uint32; gq = (*uint32)(unsafe.Add(unsafe.Pointer(&a), 4)) }

func main() { f() }
`, true},
		{"an added pointer returned", `func f() unsafe.Pointer { var a [4]uint32; return unsafe.Add(unsafe.Pointer(&a), 4) }

func main() { f() }
`, true},
		{"an added pointer held, then stored", `func f() { var a [4]uint32; p := unsafe.Add(unsafe.Pointer(&a), 4); gp = p }

func main() { f() }
`, true},
		{"a slice to a goroutine", `func f() { var a [4]uint32; go keep(unsafe.Slice(&a[0], 2)) }

func main() { f() }
`, true},
		{"a slice to a deferred keeper", `func f() { var a [4]uint32; defer keep(unsafe.Slice(&a[0], 2)) }

func main() { f() }
`, true},
		{"a callee slicing its parameter into a package variable", `func k(p *uint32) { gs = unsafe.Slice(p, 2) }

func f() { var a [4]uint32; k(&a[0]) }

func main() { f() }
`, true},
		{"a callee reslicing its parameter", `func k(p *uint32) { gs = unsafe.Slice(p, 2)[1:] }

func f() { var a [4]uint32; k(&a[0]) }

func main() { f() }
`, true},
		{"a callee storing an element's address", `func k(p *uint32) { gq = &unsafe.Slice(p, 2)[1] }

func f() { var a [4]uint32; k(&a[0]) }

func main() { f() }
`, true},
		{"a callee adding to its parameter", `func k(p unsafe.Pointer) { gq = (*uint32)(unsafe.Add(p, 4)) }

func f() { var a [4]uint32; k(unsafe.Pointer(&a)) }

func main() { f() }
`, true},
		{"a callee holding the slice", `func k(p *uint32) { s := unsafe.Slice(p, 2); gs = s }

func f() { var a [4]uint32; k(&a[0]) }

func main() { f() }
`, true},
		{"a callee returning the slice", `func k(p *uint32) []uint32 { return unsafe.Slice(p, 2) }

func f() { var a [4]uint32; gs = k(&a[0]) }

func main() { f() }
`, true},
		{"a callee returning the added pointer", `func k(p unsafe.Pointer) unsafe.Pointer { return unsafe.Add(p, 4) }

func f() { var a [4]uint32; gp = k(unsafe.Pointer(&a)) }

func main() { f() }
`, true},
		{"a callee passing the slice on", `func k(p *uint32) { keep(unsafe.Slice(p, 2)) }

func f() { var a [4]uint32; k(&a[0]) }

func main() { f() }
`, true},
		{"a callee keeping an element of what it slices", `func k(p **uint32) { gq = unsafe.Slice(p, 2)[0] }

func f() { var x uint32; b := [2]*uint32{&x, &x}; k(&b[0]) }

func main() { f() }
`, true},
		{"an address stored through a view, read through the array", `func f() { var a [4]*uint32; var x uint32; s := unsafe.Slice(&a[0], 2); s[0] = &x; gq = a[0] }

func main() { f() }
`, true},
		{"an address stored through a view, read through it", `func f() { var a [4]*uint32; var x uint32; s := unsafe.Slice(&a[0], 2); s[0] = &x; gq = s[0] }

func main() { f() }
`, true},
		{"an address stored through a reslice view, read through the array", `func f() { var a [4]*uint32; var x uint32; s := a[1:]; s[0] = &x; gq = a[1] }

func main() { f() }
`, true},
		{"an address stored through a slice view, read through the array", `func f() { var a [4]*uint32; var x uint32; s := a[:]; s[0] = &x; gq = a[0] }

func main() { f() }
`, true},
		{"control: a slice of package storage", `func f() { gs = unsafe.Slice(&garr[0], 2) }

func main() { f() }
`, false},
		{"control: a reslice of package storage", `func f() { gs = unsafe.Slice(&garr[0], 2)[1:] }

func main() { f() }
`, false},
		{"control: an added pointer into package storage", `func f() { gp = unsafe.Add(unsafe.Pointer(&garr), 4) }

func main() { f() }
`, false},
		{"control: a slice of a local read", `func f() { var a [4]uint32; s := unsafe.Slice(&a[0], 2); s[0] = 1; println(s[0], unsafe.Slice(&a[0], 2)[1:][0]) }

func main() { f() }
`, false},
		{"control: a callee only reading", `func k(p *uint32) { println(unsafe.Slice(p, 2)[1]) }

func f() { var a [4]uint32; k(&a[0]) }

func main() { f() }
`, false},
		{"control: an address stored through a view of a local, read locally", `func f() { var a [4]*uint32; var x uint32; s := unsafe.Slice(&a[0], 2); s[0] = &x; println(*a[0]) }

func main() { f() }
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
