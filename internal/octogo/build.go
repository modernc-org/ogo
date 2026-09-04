// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"go/token"
	"io/fs"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing/fstest"
)

var (
	noPkg = &Package{Scope: newScope(Universe, PackageScope)}
)

// intrinsicImports are the compiler-known packages that have no .ogo source
// directory: their symbols are provided by the emitter (p2's hardware intrinsics),
// so an import of one resolving to noPkg is expected, not a missing-package error.
var intrinsicImports = map[string]bool{"p2": true}

// embeddedPkgs are packages whose source the compiler carries rather than reads
// from a directory. They are ORDINARY OctoGo, compiled and mangled like any other
// package -- nothing about them is intrinsic -- so the day one of them ships as
// source on disk, the only change is where it is read from.
var embeddedPkgs = map[string]string{"testing": testingSrc, "p2": p2Src, "strings": stringsSrc, "math": mathSrc}

// mathSrc is the math package. Every function whose body is missing is one call of
// the C backend's math library, substituted at the call site (mathIntrinsics in
// emit.go) rather than emitted -- so a call costs the library call and nothing more.
// The two that HAVE bodies are written in OctoGo over the others, because the
// library's own are unusable: see Round and Trunc below.
//
// Go's signatures, spelled with float64 as Go spells them. The target has no
// double-precision hardware, so a float64 is 32 bits here and every result carries
// about seven significant digits rather than sixteen -- which is true of float64
// arithmetic in this language generally and is not particular to this package. A
// program written against Go's math compiles and means the same thing; what it
// loses is precision it could not have had on this hardware anyway.
const mathSrc = `// Package math is the allocation-free part of Go's math: the elementary functions,
// each meaning exactly what Go's of the same name means.
//
// The target has no double-precision hardware, so a float64 is 32 bits here and a
// result carries about seven significant digits. That is a property of float64 in
// this language rather than of this package -- see the Numeric types section of the
// language spec -- and it is why a program that compares a result against a literal
// should compare to the precision it has.
//
// What is missing is what needs more than a call: Inf, NaN, IsNaN, IsInf and
// Signbit, which answer questions about a bit pattern rather than computing a value,
// and the Max/Min float constants, which name values a 32-bit float cannot hold.

// Mathematical constants, as Go declares them. Each is exact until it is used: the
// compiler evaluates constant expressions exactly, so Pi/2 is right to the last bit
// the target can hold.
const (
	E   = 2.71828182845904523536028747135266249775724709369995957496696763
	Pi  = 3.14159265358979323846264338327950288419716939937510582097494459
	Phi = 1.61803398874989484820458683436563811772030917980576286213544862

	Sqrt2   = 1.41421356237309504880168872420969807856967187537694806841700392
	SqrtE   = 1.64872127070012814684865078781416357165377610071014801157507931
	SqrtPi  = 1.77245385090551602729816748334114518279754945612238712821380779
	SqrtPhi = 1.27201964951406896425242246173749149171560804184009624861664038

	Ln2    = 0.693147180559945309417232121458176568075500134360255254120680009
	Log2E  = 1 / Ln2
	Ln10   = 2.30258509299404568401799145468436420760110148862877297603332790
	Log10E = 1 / Ln10
)

// Abs returns the absolute value of x.
func Abs(x float64) float64

// Ceil returns the least integer value greater than or equal to x, and Floor the
// greatest integer value less than or equal to it.
func Ceil(x float64) float64

func Floor(x float64) float64

// Sqrt returns the square root of x.
func Sqrt(x float64) float64

// Pow returns x raised to the power y.
func Pow(x float64, y float64) float64

// Exp returns e raised to the power x.
func Exp(x float64) float64

// Log returns the natural logarithm of x, Log2 the binary logarithm and Log10 the
// decimal one.
func Log(x float64) float64

func Log2(x float64) float64

func Log10(x float64) float64

// Sin, Cos and Tan return the sine, cosine and tangent of the radian argument x.
func Sin(x float64) float64

func Cos(x float64) float64

func Tan(x float64) float64

// Asin, Acos and Atan return the inverse sine, cosine and tangent of x, in radians.
func Asin(x float64) float64

func Acos(x float64) float64

func Atan(x float64) float64

// Atan2 returns the arc tangent of y/x, using the signs of the two to determine the
// quadrant of the return value.
func Atan2(y float64, x float64) float64

// Mod returns the floating-point remainder of x/y. The magnitude of the result is
// less than y and its sign agrees with that of x.
func Mod(x float64, y float64) float64

// Copysign returns a value with the magnitude of f and the sign of sign.
func Copysign(f float64, sign float64) float64

// Trunc returns the integer value of x, rounding toward zero.
//
// Written here rather than mapped to the library's, which this target does not
// provide at all.
func Trunc(x float64) float64 {
	if x < 0 {
		return Ceil(x)
	}
	return Floor(x)
}

// Round returns the nearest integer, rounding half away from zero.
//
// Written here rather than mapped to the library's round(), which on this target is
// a compiler builtin that yields an INTEGER: correct where something converts it,
// and clamped at 2147483647 for anything larger -- Round(1e30) came back as 2.1e9.
// Building it from Floor has no such limit. See doc/round-is-an-integer.c, reported
// as flexprop issue 108.
//
// The sign comes from x, so that Round(-0) is -0 and Round(-0.2) is -0, as Go has
// them: a sign test on x answered +0 for both.
func Round(x float64) float64 {
	return Copysign(Floor(Abs(x)+0.5), x)
}
`

// p2Src is the p2 package: the Propeller 2's hardware, as declarations. Every
// function here is BODYLESS -- the form the grammar provides for a function
// implemented elsewhere -- because each is one flexcc intrinsic and the emitter
// substitutes the call. What the source buys is that the checker sees real
// signatures and real constants: an argument is checked, a misspelt name is caught
// by the compiler rather than by C, and a constant is usable wherever a constant is.
//
// The constants are the pin-configuration bits, from flexcc's smartpins.h, which is
// embedded in this repository and is the authority. They are named rather than
// written as hex because the hex is unforgiving in a way that looks like working
// code: _examples/gopher was first written with a mode word missing P_OE, which
// compiles, runs and drives nothing.
var p2Src = `// Package p2 is the Propeller 2's hardware: its pins, its timing, its random
// number generator and its sixteen hardware locks. Each function is one intrinsic
// of the C backend, so a call costs what the instruction costs and nothing more.
//
// There is no init and nothing to configure. A pin is a number, 0..63 on a P2.

` + p2ConstDecls() + `
// PinHigh drives a pin high, PinLow low, PinToggle inverts it and PinFloat releases
// it.
func PinHigh(pin int)

func PinLow(pin int)

func PinToggle(pin int)

func PinFloat(pin int)

// PinIn reads a pin's input.
func PinIn(pin int) int

// PinWrite drives a pin to v.
func PinWrite(pin int, v int)

// WaitMs, WaitUs and WaitCycles pause the calling cog. Only this cog: there is no
// scheduler, so the other seven are unaffected.
func WaitMs(ms int)

func WaitUs(us int)

func WaitCycles(n int)

// WaitUntil pauses until the system counter reaches t, where WaitCycles pauses for a
// number of cycles. That is the difference between a loop that keeps time and one
// that drifts: the work a loop body does is inside the wait rather than after it.
//
//	next := p2.GetCt() + period
//	for {
//		p2.WaitUntil(next)
//		next += period
//		...                    // however long this takes, the period holds
//	}
//
// t is an absolute counter value, so it wraps with the counter; a wait for a t
// already past returns at once rather than waiting a whole wrap around.
func WaitUntil(t uint32)

// PinStart brings a smart pin up: its mode, its X and Y registers and its direction
// bit in one call. WritePinMode, WritePinX and WritePinY set the parts separately.
func PinStart(pin int, mode int, x int, y int)

func WritePinMode(pin int, mode int)

func WritePinX(pin int, x int)

func WritePinY(pin int, y int)

// ReadPin reads a smart pin's result and acknowledges it; AckPin acknowledges
// without reading.
func ReadPin(pin int) uint32

func AckPin(pin int)

// GetCt is the system counter, GetMs, GetUs and GetSec the milliseconds,
// microseconds and seconds since reset. GetCt is the one to measure with: it counts
// every clock, and is what WaitUntil takes.
func GetCt() uint32

func GetMs() uint32

func GetSec() uint32

func GetUs() uint32

// Rnd is the hardware random number generator. Rev reverses the bits of x.
func Rnd() uint32

func Rev(x uint32) uint32

// SetBaud sets the baud rate of the serial link the console -- print, println and
// printf -- goes out on. The loader leaves it at 230400; a host expecting another
// rate needs this called before anything is written, and the host must be reading
// at the new rate by then.
func SetBaud(baud int)

// ReadByte reads one byte from the serial link the console goes out on -- the other
// direction of SetBaud's -- and is the only way in. It returns the byte as 0..255,
// or -1 if the wait ran out, so a caller tests for a negative rather than for a
// value no byte can take.
//
// timeout is how long to wait. Zero waits FOREVER, which parks the cog until a byte
// arrives; any other value gives up after that many milliseconds. They are BINARY
// milliseconds, 1/1024 of a second rather than 1/1000: the toolchain divides the
// clock by a shift, so a wait measures about 2.4% short of what its number says.
// Near enough for a protocol timeout and not near enough to keep time with.
//
// NOTHING IS BUFFERED BEHIND THIS, and at speed that is not a caution but the
// governing fact. A byte arriving while the cog is doing anything else is gone.
// Measured on a P2-EDGE at 230400 baud, where a byte takes 43us: a loop reading into
// an array caught all eight of "PING\rOK\r" in order, and the same loop with one
// printf per byte in it caught "PNO\r" -- half of them, silently, with no error
// anywhere.
//
// So a program that must not miss a byte gives a cog to reading and to nothing else.
// AND IT MUST NOT HAND THEM OVER ON A CHANNEL, which is the obvious thing to reach
// for and does not work: a channel here is a rendezvous, so the send parks the
// reader until the far side arrives, and the line does not wait -- measured, that
// costs every byte of the next two commands while the worker is printing the answer
// to the first. The handover is a single-producer/single-consumer ring, the reader
// only ever storing and bumping head, the worker only ever reading and bumping tail.
// That reads all of "PING\rSTATUS\rGO\r" where the channel version read one command
// and two fragments.
func ReadByte(timeout int) int

// WriteByte puts one byte on the serial link exactly as given, which is what a
// protocol carrying anything but text needs and what none of print, println and
// printf will do.
//
// printf's %c is the near miss. It writes a RUNE, so it is right for text and wrong
// for a packet: every value from 0x80 up comes out UTF-8 encoded as two bytes, and a
// length field of 200 becomes 0xC3 0x88. Measured on a P2-EDGE, this writes 1..255
// as exactly those 255 bytes.
//
// The C beneath is not putchar, which is the other near miss and a worse one,
// because it looks right until a byte happens to be 10: putchar TRANSLATES that to a
// carriage return and a newline, so the same 255 values come out as 256 bytes with a
// 13 inserted. A framing byte, a length or a checksum that lands on 10 would be
// corrupted and nothing would say so.
func WriteByte(b byte)

// Reboot restarts the board.
func Reboot()

// The sixteen hardware locks, the same pool the channel runtime draws from.
// TryLock is the only way to take one -- the hardware offers no blocking acquire --
// so a caller that must wait spins on it.
//
// NewLock does NOT report exhaustion: after handing out 0..15 it returns 15 for
// every further call, measured on a P2-EDGE. Two logically distinct locks can
// therefore alias, which costs contention where they are shared and hangs where a
// program nests two it believes are independent. See doc/locknew-never-fails.c.
func NewLock() int

func TryLock(l int) bool

func Unlock(l int)

func FreeLock(l int)
`

// p2ConstDecls renders the p2 package's constants from p2Constants, which is the
// one place their values live: the checker reads them from this source and the
// emitter substitutes them at the use, and two tables would drift.
func p2ConstDecls() string {
	var b strings.Builder
	b.WriteString("// The pin-configuration bits, from flexcc's smartpins.h. OutputEnable is the one\n")
	b.WriteString("// that is easy to leave out of a hex constant: a mode without it configures a pin\n")
	b.WriteString("// that is switched off, which on a scope looks like a bug in whatever fed it.\nconst (\n")
	names := make([]string, 0, len(p2Constants))
	for k := range p2Constants {
		names = append(names, k)
	}
	sort.Strings(names)
	w := 0
	for _, nm := range names {
		if len(nm) > w {
			w = len(nm)
		}
	}
	for _, nm := range names {
		fmt.Fprintf(&b, "\t%-*s = %s\n", w, nm, p2Constants[nm])
	}
	b.WriteString(")\n")
	return b.String()
}

// testingSrc is the testing package: what a test needs, and no more than this
// target can provide. There is no heap and no formatting, so there is no Errorf --
// println is a builtin that already prints mixed types, and duplicating it in a
// package that cannot allocate would be worse than pointing at it.
const testingSrc = `// Package testing provides the state a test reports its outcome through. A test is
// a function named Test<Something> taking a *testing.T, in a file whose name ends
// _test.ogo; "ogo test" builds them into a program of their own and runs it.
//
// A failure is reported by calling Fail, and what went wrong is printed with the
// builtin println, which takes mixed types:
//
//	func TestPop(t *testing.T) {
//		if got := pop(); got != 3 {
//			println("pop:", got, "want 3")
//			t.Fail()
//		}
//	}
//
// There is no Errorf: formatting needs allocation this target does not have.

type T struct {
	failed  bool
	skipped bool
}

// Fail marks the test as failed and lets it keep running.
func (t *T) Fail() { t.failed = true }

// Failed reports whether Fail has been called.
func (t *T) Failed() bool { return t.failed }

// Skip marks the test as skipped. It does NOT stop the test: there is no panic to
// unwind with, so a skipping test returns on its own.
func (t *T) Skip() { t.skipped = true }

// Skipped reports whether Skip has been called.
func (t *T) Skipped() bool { return t.skipped }
`

// stringsSrc is the strings package: the allocation-free part of Go's. It is
// ordinary OctoGo, compiled like any other package -- nothing in it is intrinsic
// and nothing in it is C -- which is the point as much as the functions are. A
// standard library a language cannot express is a standard library written in
// something else.
const stringsSrc = `// Package strings is the allocation-free part of Go's strings.
//
// Everything here either answers a question about a string -- a bool, an int -- or
// returns a SUBSTRING of one, which costs nothing: a string is a pointer and a
// length, so a slice of one points into the same bytes and allocates nothing at
// all. What is missing is what allocates. Split, Join, Repeat, Replace, ToUpper and
// the rest need somewhere to put a string that did not exist before, and there is
// no heap here to put it; Builder is how a program makes one, over memory it owns.
//
// Each function here means exactly what Go's of the same name means, including for
// an empty argument and for invalid UTF-8. That is worth more than breadth: a
// function that is nearly Go's is worse than one that is missing, because it
// compiles.

// Compare returns -1 if a sorts before b, 0 if they are equal, and 1 if a sorts
// after b. It is included for symmetry with Go's; use ==, < and > directly, which
// is what Go says too.
func Compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// Contains reports whether substr is within s.
func Contains(s, substr string) bool {
	return Index(s, substr) >= 0
}

// ContainsAny reports whether any rune of chars is within s.
func ContainsAny(s, chars string) bool {
	return IndexAny(s, chars) >= 0
}

// ContainsRune reports whether r is within s.
func ContainsRune(s string, r rune) bool {
	return IndexRune(s, r) >= 0
}

// Count counts the non-overlapping instances of substr in s. If substr is empty it
// returns 1 plus the number of runes in s, as Go's does.
func Count(s, substr string) int {
	if len(substr) == 0 {
		n := 1
		for range s {
			n = n + 1
		}
		return n
	}
	n := 0
	i := 0
	for i+len(substr) <= len(s) {
		if s[i:i+len(substr)] == substr {
			n = n + 1
			i = i + len(substr)
		} else {
			i = i + 1
		}
	}
	return n
}

// Cut slices s around the first instance of sep, returning what precedes it and
// what follows it. found reports whether sep appears at all; if it does not, Cut
// returns s, "", false.
func Cut(s, sep string) (string, string, bool) {
	i := Index(s, sep)
	if i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// CutPrefix returns s without its leading prefix and reports whether it had one.
// If it did not, CutPrefix returns s, false.
func CutPrefix(s, prefix string) (string, bool) {
	if HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// CutSuffix returns s without its trailing suffix and reports whether it had one.
func CutSuffix(s, suffix string) (string, bool) {
	if HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}

// HasPrefix reports whether s begins with prefix.
func HasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// HasSuffix reports whether s ends with suffix.
func HasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// Index returns the byte index of the first instance of substr in s, or -1. An
// empty substr is at 0, as Go has it.
func Index(s, substr string) int {
	n := len(substr)
	if n == 0 {
		return 0
	}
	if n > len(s) {
		return -1
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == substr {
			return i
		}
	}
	return -1
}

// IndexAny returns the byte index of the first rune of s that is also in chars, or
// -1 if there is none.
func IndexAny(s, chars string) int {
	if len(chars) == 0 {
		return -1
	}
	for i, c := range s {
		if ContainsRune(chars, c) {
			return i
		}
	}
	return -1
}

// IndexByte returns the index of the first instance of c in s, or -1.
func IndexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// IndexRune returns the byte index of the first instance of r in s, or -1. Ranging
// a string yields U+FFFD for each byte of an invalid encoding, so asking for
// U+FFFD finds the first such byte -- which is what Go's does.
func IndexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

// LastIndex returns the byte index of the last instance of substr in s, or -1. An
// empty substr is at len(s), as Go has it.
func LastIndex(s, substr string) int {
	n := len(substr)
	if n == 0 {
		return len(s)
	}
	for i := len(s) - n; i >= 0; i-- {
		if s[i:i+n] == substr {
			return i
		}
	}
	return -1
}

// LastIndexByte returns the index of the last instance of c in s, or -1.
func LastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// TrimPrefix returns s without its leading prefix. If it has none, s is returned
// unchanged.
func TrimPrefix(s, prefix string) string {
	if HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

// TrimSuffix returns s without its trailing suffix.
func TrimSuffix(s, suffix string) string {
	if HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// TrimSpace returns s without leading and trailing white space, as Unicode defines
// it -- not merely as ASCII does, which would leave a non-breaking space behind and
// look right until it did not.
func TrimSpace(s string) string {
	start := len(s)
	for i, c := range s {
		if !isSpace(c) {
			start = i
			break
		}
	}
	if start == len(s) {
		return ""
	}
	end := start
	for i, c := range s {
		if i >= start && !isSpace(c) {
			end = i + runeLen(c)
		}
	}
	return s[start:end]
}

// isSpace reports whether r is white space, which is Unicode's White_Space
// property: a short, closed list, so this is exact rather than an approximation of
// what unicode.IsSpace answers.
func isSpace(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' {
		return true
	}
	if r == 0x85 || r == 0xA0 || r == 0x1680 {
		return true
	}
	if r >= 0x2000 && r <= 0x200A {
		return true
	}
	return r == 0x2028 || r == 0x2029 || r == 0x202F || r == 0x205F || r == 0x3000
}

// runeLen is how many bytes r takes in UTF-8, which is what turns a rune's start
// index into the index just past it. An invalid rune is one byte, matching what
// ranging a string yields for one.
func runeLen(r rune) int {
	if r < 0x80 {
		return 1
	}
	if r < 0x800 {
		return 2
	}
	if r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF) {
		return 1
	}
	if r < 0x10000 {
		return 3
	}
	return 4
}
`

type importTask struct {
	sync.Mutex
	p     *Package
	ready chan struct{}
}

// BuildContext coordinates creating a package tree.
type BuildContext struct {
	errMu     sync.Mutex
	importsMu sync.Mutex

	errList     ErrList
	fsys        fs.FS
	modulePath  string                 // the module import paths are written against, "" when there is none
	mainDir     string                 // the main package's directory within fsys
	importTasks map[string]*importTask // package identity (see importIdent): importTask
	importGraph map[string]map[string]bool
	limit       int

	// srcFiles maps a source to the file parsed from it, for every file of every
	// package this build reads, so that a token -- which carries its source --
	// leads back to the file that wrote it, and a name written there resolves in
	// that file's scope (see File.fileOfToken). Packages are parsed concurrently,
	// hence the lock.
	filesMu  sync.Mutex
	srcFiles map[*source]*File

	noDeclarationChecks bool
}

// NewBuildContext returns a newly created BuildContext. 'limit' is the maximum
// desired concurrency for individual package building when > 0.
func NewBuildContext(fsys fs.FS, limit int) (c *BuildContext) {
	return &BuildContext{
		fsys:        fsys,
		srcFiles:    map[*source]*File{},
		importTasks: map[string]*importTask{},
		importGraph: map[string]map[string]bool{},
		limit:       limit,
	}
}

// importDir is the directory within c.fsys that an import path names, and whether
// the path belongs to the module being built at all. Without a module the path IS
// the directory, which is what every build did before ogo.mod existed: a program of
// several packages is one directory tree and the one being built is its root.
//
// With a module the path is written as Go writes one, module path and all, and the
// prefix is stripped here. That is what makes an import mean the same thing in every
// file that writes it, whichever directory the build was started from.
func (c *BuildContext) importDir(importPath string) (dir string, ok bool) {
	if c.modulePath == "" {
		return importPath, true
	}

	switch {
	case importPath == c.modulePath:
		return ".", true
	case strings.HasPrefix(importPath, c.modulePath+"/"):
		return importPath[len(c.modulePath)+1:], true
	}

	return "", false
}

// pkgPath is a package's identity inside the compiler: its directory within the
// module. The module path is surface syntax and is stripped before this, so a
// dotted or hyphenated one never reaches the C mangler and example.com/proj/sensor
// is the same package, and the same C symbols, that sensor was before modules.
//
// The module root is a package too, and takes the module path's last element -- the
// name a Go program would import it under. A directory of that name beside it would
// claim the same identity, which importCollision refuses rather than silently merge.
func (c *BuildContext) pkgPath(dir string) string {
	if dir == "." {
		return path.Base(c.modulePath)
	}

	return dir
}

// notInModule explains an import path the module does not contain. A path that
// names a real directory of the module, written without the module prefix, is the
// mistake a Go programmer does not make and everyone else does, so it is named.
func (c *BuildContext) notInModule(importPath string) string {
	if fi, err := fs.Stat(c.fsys, importPath); err == nil && fi.IsDir() {
		return fmt.Sprintf("cannot find package %q in module %q, did you mean %q?", importPath, c.modulePath, c.modulePath+"/"+importPath)
	}

	return fmt.Sprintf("package %q is not in module %q", importPath, c.modulePath)
}

func (c *BuildContext) syncErr(pos token.Position, s string, args ...any) {
	c.errMu.Lock()

	defer c.errMu.Unlock()

	c.err(pos, s, args...)
}

func (c *BuildContext) err(pos token.Position, s string, args ...any) {
	c.errList.AddErr(pos, s, args...)
}

// findCycle performs a DFS to see if 'target' is reachable from 'current'.
// It returns the path of the cycle if one exists.
func (c *BuildContext) findCycle(current, target string, visited map[string]bool) []string {
	if current == target {
		return []string{current}
	}
	visited[current] = true

	for next := range c.importGraph[current] {
		if !visited[next] {
			if cycle := c.findCycle(next, target, visited); cycle != nil {
				return append([]string{current}, cycle...)
			}
		}
	}
	return nil
}

// importIdent is a package's identity within a build -- what the import graph, the
// cycle check and the one-build-per-package map must agree to call it -- together
// with the directory to read it from. The identity is the directory, not the path
// written to reach it: the module prefix is surface syntax, and 'fromPath' arrives
// here as a package's ImportPath, which is already stripped of it. A graph with one
// spelling on one side of an edge and the other on the other side connects nothing,
// and an import cycle then deadlocks on its own task instead of being reported.
//
// An embedded or intrinsic package (testing, p2, strings) has no directory and
// stands for itself, module or no module, as std does in Go.
func (c *BuildContext) importIdent(importPath string) (key, dir string, ok bool) {
	if _, embedded := embeddedPkgs[importPath]; embedded || intrinsicImports[importPath] {
		return importPath, "", true
	}

	if dir, ok = c.importDir(importPath); !ok {
		return "", "", false
	}

	return c.pkgPath(dir), dir, true
}

// windowsReserved are the MS-DOS device names Windows still reserves. No file or
// directory may carry one, in any case, with or without an extension -- NUL.txt is
// NUL -- so a repository holding con/ or aux.ogo cannot be checked out on Windows
// at all. Git reports the failure against the checkout, far from whoever wrote it,
// and on Linux there is nothing to notice: the names are ordinary words there, and
// con, aux and prn are plausible package names on a microcontroller.
//
// The list is Microsoft's own (Naming Files, Paths, and Namespaces), COM0 and LPT0
// included. The superscript variants COM1 and so on are outside the ASCII a package
// directory is already held to.
var windowsReserved = func() map[string]bool {
	r := map[string]bool{"con": true, "prn": true, "aux": true, "nul": true}
	for c := '0'; c <= '9'; c++ {
		r["com"+string(c)] = true
		r["lpt"+string(c)] = true
	}
	return r
}()

// reservedFileName returns the device name a source file's name collides with on
// Windows, or "". The extension does not save it: aux.ogo is aux.
func reservedFileName(name string) string {
	base := strings.TrimSuffix(path.Base(name), ".ogo")
	if windowsReserved[strings.ToLower(base)] {
		return base
	}

	return ""
}

// validPkgDir reports whether a module-relative package DIRECTORY can exist, beside
// its siblings, on every filesystem this compiler targets. It is a narrower rule
// than the one an import path as a whole obeys, and deliberately so: the module
// prefix of a path is a repository name that never reaches a filesystem, and
// example.com/BurntSushi/toml has to remain spellable, while what follows it is
// created by `mkdir` on macOS and Windows too.
//
// Lower case is the load-bearing part. A module holding both foo/ and Foo/ cannot be
// checked out on a case-insensitive filesystem at all, let alone built, and the
// developer who wrote it -- on Linux, where both exist happily -- learns this from
// whoever clones it. Refusing the capital reports it on the first build instead.
func validPkgDir(dir string) (bad, why string) {
	if dir == "" || dir == "." {
		return "", ""
	}

	for _, v := range strings.Split(dir, "/") {
		if strings.HasPrefix(v, "-") {
			return v, "must not begin with '-'"
		}

		for _, c := range v {
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
				// ok
			case c >= 'A' && c <= 'Z':
				return v, "must be lower case: a directory differing from another only in case is the SAME directory on macOS and Windows"
			default:
				return v, "must be made of lower-case ASCII letters, digits, '_' and '-'"
			}
		}

		if windowsReserved[v] {
			return v, "is a device name Windows reserves, where no directory may carry it"
		}
	}

	return "", ""
}

func (c *BuildContext) importPkg(fromPath, importPath string, importPathToken Token) (p *Package) {
	if c == nil {
		return noPkg
	}

	key, dir, inModule := c.importIdent(importPath)
	if !inModule {
		// An intrinsic import (p2) has no source directory by design and no module
		// prefix either, and is handled by the emitter.
		if !intrinsicImports[importPath] {
			c.syncErr(importPathToken.Position(), "%s", c.notInModule(importPath))
		}
		return noPkg
	}

	if bad, why := validPkgDir(dir); bad != "" {
		c.syncErr(importPathToken.Position(), "invalid import path %q: package directory %q %s", importPath, bad, why)
		return noPkg
	}

	// The main package is a program, not a library. Importing it would compile its
	// files a second time under a package name and send its own imports back round
	// to whoever imported it, which is a cycle reported as one somewhere far from
	// the mistake.
	if dir != "" && dir == c.mainDir {
		c.syncErr(importPathToken.Position(), "import of main package %q", importPath)
		return noPkg
	}

	c.importsMu.Lock()

	if c.importGraph[fromPath] == nil {
		c.importGraph[fromPath] = make(map[string]bool)
	}
	c.importGraph[fromPath][key] = true

	if cycle := c.findCycle(key, fromPath, make(map[string]bool)); cycle != nil {
		c.importsMu.Unlock()

		// To complete the visual circle for the error message, put 'fromPath' at the front.
		fullCycle := append([]string{fromPath}, cycle...)
		c.syncErr(importPathToken.Position(), "import cycle not allowed: %s", strings.Join(fullCycle, " -> "))
		return noPkg
	}

	task := c.importTasks[key]
	if task == nil {
		task = &importTask{}
		c.importTasks[key] = task
	}

	c.importsMu.Unlock()

	task.Lock()

	if task.ready == nil {
		task.ready = make(chan struct{})
		go func() {
			defer close(task.ready)

			if src, embedded := embeddedPkgs[importPath]; embedded {
				fn := path.Join(importPath, importPath+".ogo")
				task.p = c.NewPackage(importPath, []string{fn}, fstest.MapFS{fn: &fstest.MapFile{Data: []byte(src)}})
				return
			}

			dirEntries, err := fs.ReadDir(c.fsys, dir)
			if err != nil {
				task.p = noPkg
				// A non-intrinsic import that names no readable directory is a
				// mistake -- a typo, or a package that is not present. An intrinsic
				// import (p2) has no source directory by design and is handled by the
				// emitter, so it is not reported here.
				if !intrinsicImports[importPath] {
					c.syncErr(importPathToken.Position(), "cannot find package %q", importPath)
				}
				return
			}

			if dir == "." {
				if fi, err := fs.Stat(c.fsys, path.Base(c.modulePath)); err == nil && fi.IsDir() {
					task.p = noPkg
					c.syncErr(importPathToken.Position(), "cannot import module root %q: directory %s beside it claims the same package name %q", importPath, path.Base(c.modulePath), path.Base(c.modulePath))
					return
				}
			}

			var files []string
			for _, v := range dirEntries {
				if v.IsDir() {
					continue
				}

				switch nm := v.Name(); path.Ext(nm) {
				case ".ogo":
					if strings.HasSuffix(nm, "_test.ogo") {
						break
					}

					// The importing statement is the position there is: a file
					// has none of its own, and this is what pulled it in.
					if bad := reservedFileName(nm); bad != "" {
						c.syncErr(importPathToken.Position(), "package %q has file %s: %q is a device name Windows reserves, where no file may carry it", importPath, nm, bad)
						task.p = noPkg
						return
					}

					files = append(files, path.Join(dir, nm))
				}
			}

			task.p = c.NewPackage(key, files, c.fsys)
		}()
	}

	task.Unlock()
	<-task.ready
	return task.p
}

func consolidateErrors(use ErrList, errors ...error) (e ErrList) {
	e = use
	for _, v := range errors {
		switch x := v.(type) {
		case nil:
			// nop
		case ErrList:
			e = append(e, x...)
		default:
			e = append(e, ErrWithPosition{Err: x})
		}
	}
	return e
}

// Build builds the main package consisting of files in 'files' within 'fsys'.
// 'limit' is the maximum desired concurrency for individual package building
// when > 0.
//
// 'files' must be base names within fsys. Build resolves an import path a/b/c as
// the path a/b/c within fsys, which is the module-less form: fsys is the package
// being built and every package of the program is a directory in it. See
// BuildModule for a program that declares one.
func Build(limit int, files []string, fsys fs.FS) (main *Package, err error) {
	return BuildModule(limit, "", ".", files, fsys)
}

// BuildModule builds the main package of a module: 'modulePath' is what its import
// paths are prefixed with, 'dir' is the main package's own directory within 'fsys'
// and 'files' are its base names there. An import path is resolved by stripping
// 'modulePath' and reading the rest as a directory of 'fsys', so an import means the
// same directory in every file that writes it, whichever package is being built.
//
// An empty 'modulePath' with dir "." is the module-less form Build passes.
func BuildModule(limit int, modulePath, dir string, files []string, fsys fs.FS) (main *Package, err error) {
	for _, v := range files {
		if path.Base(v) != v {
			return noPkg, fmt.Errorf("not a base name: %s", v)
		}

		if bad := reservedFileName(v); bad != "" {
			return noPkg, fmt.Errorf("%s: %q is a device name Windows reserves, where no file may carry it", v, bad)
		}
	}

	c := NewBuildContext(fsys, limit)
	c.modulePath = modulePath
	c.mainDir = path.Clean(dir)

	defer func() {
		var errs ErrList
		for _, v := range c.importTasks {
			errs = v.p.consolidateErrors(errs)
		}
		if main != nil {
			errs = main.consolidateErrors(errs)
		}
		errs = consolidateErrors(errs, c.errList)
		// Establish stable order
		sort.Slice(errs, func(i, j int) bool { return errs[i].less(errs[j]) })
		// A file the parser could not read is not checked: every checker error it
		// yields is derived from a tree that is not what was written, and says so
		// somewhere the reader has to work to connect to the cause. `a := [3]int(q)`
		// -- a conversion the grammar does not accept -- reported "undefined: Row"
		// against an unrelated declaration three lines above the syntax error, the
		// broken parse having cost the whole file its type declarations. Go stops
		// after parsing for the same reason.
		//
		// Only that file is silenced: another file in the package parsed fine, and
		// its errors are about what it says.
		broken := map[string]bool{}
		for _, v := range errs {
			if v.Parse && v.Pos.IsValid() {
				broken[v.Pos.Filename] = true
			}
		}
		if len(broken) != 0 {
			w := 0
			for _, v := range errs {
				if !v.Parse && v.Pos.IsValid() && broken[v.Pos.Filename] {
					continue
				}
				errs[w] = v
				w++
			}
			errs = errs[:w]
		}
		// Remove multiple errors for the same line, keeping the parser's where there
		// is one -- the same rule within a line, for the files that DID parse: a
		// checker error there is a consequence hiding its cause. `if v, ok := f(); ok`
		// said "undefined: v" and swallowed the parse error at the comma that
		// explains why v was never declared.
		w := 0
		for _, v := range errs {
			if w == 0 {
				errs[w] = v
				w++
				continue
			}

			if !v.sameFileAndLine(errs[w-1]) {
				errs[w] = v
				w++
				continue
			}
			if v.Parse && !errs[w-1].Parse {
				errs[w-1] = v
			}
		}
		errs = errs[:w]
		err = errs.Err()
	}()

	// The main package has no import path: its C symbols are unprefixed, whether it
	// sits at the module root or in a subdirectory of one.
	main = c.NewPackage("", joinDir(dir, files), fsys)
	return main, nil
}

// joinDir places a package's base names in its directory within the module. The
// module-less build passes ".", where the names are already where they belong.
func joinDir(dir string, files []string) (r []string) {
	if dir == "" || dir == "." {
		return files
	}

	r = make([]string, len(files))
	for i, v := range files {
		r[i] = path.Join(dir, v)
	}
	return r
}

type limiter chan struct{}

func newLimiter(limit int) limiter {
	if limit > 0 {
		return make(limiter, limit)
	}

	return nil
}

func (n limiter) limit() func() {
	if n == nil {
		return func() {}
	}

	n <- struct{}{}
	return func() { <-n }
}

// Package represents a single OctoGo package.
type Package struct {
	Files      []*File
	ImportPath string
	Scope      *Scope
	ctx        *BuildContext
}

// fileOf is the file parsed from src, or nil for a source no package of this
// build was read from (the universe's own declarations).
func (c *BuildContext) fileOf(src *source) *File {
	c.filesMu.Lock()
	defer c.filesMu.Unlock()
	return c.srcFiles[src]
}

// NewPackage returns a newly created Package consisting of files in 'files'
// within 'fsys'.
func (c *BuildContext) NewPackage(importPath string, files []string, fsys fs.FS) (p *Package) {
	p = &Package{
		Files:      make([]*File, len(files)),
		ImportPath: importPath,
		Scope:      newScope(Universe, PackageScope),
		ctx:        c,
	}

	// Phase 1: Local Scope Population (Parallel)
	limiter := newLimiter(c.limit)
	var wg sync.WaitGroup
	for i, v := range files {
		release := limiter.limit()

		wg.Add(1)
		go func(i int, fn string) {
			defer release()
			defer wg.Done()

			p.Files[i] = p.newFile(fn, fsys)
		}(i, v)
	}
	wg.Wait()
	c.filesMu.Lock()
	for _, f := range p.Files {
		if f != nil && f.parser.tok.source != nil {
			c.srcFiles[f.parser.tok.source] = f
		}
	}
	c.filesMu.Unlock()
	if c.noDeclarationChecks { // Testing support
		return p
	}

	// Phase 2: Package Scope Merging (Serial)
	for _, f := range p.Files {
		for _, spec := range f.ImportSpecs {
			// An import that resolves to a real package is eligible for the
			// later unused-import report; a failed import (missing directory,
			// cycle) resolves to noPkg and is reported at import time instead. The
			// resolved package is retained on the spec so the checker can resolve
			// qualified names against it and the emitter can emit its declarations.
			spec.Pkg = c.importPkg(p.ImportPath, spec.ImportPath, spec.ImportPathToken)
			spec.resolved = spec.Pkg != noPkg
		}
		// Merge file top level declarations into package scope.
		for _, nm := range slices.Sorted(maps.Keys(f.tld.Declarations)) {
			d := f.tld.Declarations[nm]
			if err := p.Scope.add(d); err != nil {
				c.syncErr(d.Token().Position(), "%v", err)
			}
		}
		f.tld.Declarations = nil
		f.Scope.Parent = p.Scope // Rewire/repair the scope hierarchy (Block->File->Package->Universe)
	}
	// Ensure "no identifier may be declared in both the file and package block".
	for _, v := range p.Files {
		for _, nm := range slices.Sorted(maps.Keys(v.Scope.Declarations)) {
			if ex := p.Scope.Declarations[nm]; ex != nil {
				d := v.Scope.Declarations[nm]
				c.err(ex.Token().Position(), "cannot declare %v both in package and file scope (%v:)", nm, d.Token().Position())
			}
		}
	}

	// Phase 3: Top-Level Type & Constant Evaluation (Serial)
	for _, v := range p.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.sourceFile(p.Scope, n)
			}
		}
	}

	// Phase 4: Body Checking & Hardware Constraints (Parallel)
	//
	// Partial: bodies are walked to declare parameters and local variables
	// (reporting redeclarations) and to descend into nested blocks. Statement
	// type checking and the hardware-constraint checks are not implemented yet.
	for _, v := range p.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.checkBodies(p.Scope, n)
			}
		}
	}

	// Phase 5: Deep Initialization Cycle Detection (Serial)
	//
	// Partial: value-recursive types (infinite size) are reported. Global
	// initialization-order cycles are not implemented yet.
	for _, v := range p.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.checkTypeCycles(p.Scope, n)
			}
		}
	}

	// Semantic import diagnostics. Dot imports are unsupported and rejected in
	// every package. Unused imports are reported only for the package under
	// compilation (import path ""); a recursively-built dependency's own unused
	// imports are reported when that dependency is compiled directly, not here.
	// This runs after body checking, so usage in function bodies has been seen,
	// and below the noDeclarationChecks early return, so a parse-only pass does
	// not see these semantic diagnostics.
	for _, v := range p.Files {
		v.rejectDotImports()
		if p.ImportPath == "" {
			v.reportUnusedImports()
		}
	}
	return p
}

func (p *Package) consolidateErrors(use ErrList) (e ErrList) {
	e = use
	for _, v := range p.Files {
		e = v.consolidateErrors(e)
	}
	return e
}
