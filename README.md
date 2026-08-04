![logo_png](logo.png)

*Mascot based on the [Go gopher](https://go.dev/blog/gopher) by Renée French, licensed [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).*

[![Go Reference](https://pkg.go.dev/badge/modernc.org/ogo.svg)](https://pkg.go.dev/modernc.org/ogo)

**A modern, zero-allocation, Go-like programming language built specifically for the Parallax Propeller 2 (P2) multi-core microcontroller.**

OctoGo brings the elegance of Go's concurrency model to embedded hardware. It compiles an LL(1) subset of a Go-like language into C99/C11, which is then compiled to native P2 assembly using the industry-standard flexprop toolchain.

There is no software scheduler and no garbage collector. Goroutines map 1:1 to physical silicon.

The repository you are currently viewing might be a mirror. Please review the guidelines below based on where you are viewing this:

| Platform | Role | Contributing Guidelines |
| :--- | :--- | :--- |
| **GitLab** | **Primary Source** | This is the canonical repository (`cznic/ogo`). CI pipelines and main development happen here. |
| **GitHub** | **Mirror** | This is a mirror (`modernc-org/ogo`). We **do accept** Issues and Pull Requests here for your convenience! <br> *Note: PRs submitted here will be manually merged into the GitLab source, so please allow extra time for processing.* |

### **The Hook: Concurrent Blinky**

Forget manual state machines and timer interrupts. In OctoGo, spawning a parallel hardware process is as simple as calling go myfunc(). Channel communication uses the familiar \<- operator, allowing you to synchronize hardware locks seamlessly.

```
import "p2"

// blinkWorker runs independently on its own dedicated Cog
func blinkWorker(pin int, rateChan chan int) {
	delay := 500
	for {
		// Non-blocking hardware poll via select
		select {
		case delay = <-rateChan:
		default:
		}

		p2.PinHigh(pin)
		p2.WaitMs(delay)
		p2.PinLow(pin)
		p2.WaitMs(delay)
	}
}

func main() {
	// A channel is created by its declaration. OctoGo has no allocator, so
	// make(chan int) is rejected: the declaration is what allocates the
	// rendezvous cell and acquires its hardware lock.
	var rateChan chan int

	// Spawns directly to a new hardware Cog!
	go blinkWorker(56, rateChan)

	// Update the blink rate from the main Cog via hardware-locked channel
	rateChan <- 100
}
```

## **Installation**

```sh
go install modernc.org/ogo@latest
```

Building `ogo` needs Go 1.25 or newer. It is built and tested on **linux/amd64**,
**linux/arm64**, **windows/amd64**, **darwin/arm64** and **darwin/amd64** — if you
need another platform, please say so, it helps decide which to do first. Note that the two
halves come apart: `ogo build` emits an ordinary P2 `.binary`, so you can compile
on one machine and flash with whatever loader you already have on another.

> **On Windows, run `ogo` from `cmd.exe` or PowerShell — not a Unix-emulation
> shell** (git-bash, MSYS2, Cygwin). The board-facing commands (`run`, `loadp2`)
> drive the serial port through the native Windows console and are unreliable
> under those shells: the P2 handshake times out intermittently and the
> terminal's exit key can stop responding (use Ctrl-C to escape). Building
> (`build`, `fmt`) is unaffected and works in any shell.

That is the whole toolchain. `ogo` embeds the C backend (flexspin's compiler,
transpiled to Go) and the P2 loader, so there is no flexprop installation, no
separate loader and no SDK path to configure.

## **Usage**

```
ogo <command> [arguments]
```

The commands are:

| Command | |
| :--- | :--- |
| `build` | compile packages and dependencies |
| `fmt` | reformat source files |
| `help` | show help for a command |
| `loadp2` | load a program onto a Propeller 2 board (loadp2 passthrough) |
| `run` | compile and run a program on a connected board |
| `smith` | output a random program for compiler testing |
| `test` | test packages |
| `version` | print the ogo version |

Run `ogo help` for this list at the terminal, or `ogo help <command>` — for
example `ogo help build` — for one command's flags and detail. A typical session:

1. Write your `.ogo` code.
2. Run `ogo build blinky/` to compile and generate your P2 binary.
3. Run `ogo run blinky/` to compile it, load it onto a connected board and open a terminal.
4. Run `ogo test blinky/` to run that package's `_test.ogo` tests, on the board.

Tests run on the board and nowhere else. A test is `func TestSomething(t
*testing.T)`, and `testing` is imported by name with nothing on disk:

```go
import "testing"

func TestPop(t *testing.T) {
	if got, ok := pop(); !ok || got != 3 {
		println("pop:", got, ok, "want 3 true")
		t.Fail()
	}
}
```

There is no `Errorf` — formatting needs allocation this target does not have — so a
test prints with the builtin `println` and calls `t.Fail()`. `ogo test -c` builds
the tests without running them, for a machine with no board attached.

> **Seeing garbled serial output?** `ogo run` sets a precise 200 MHz clock and
> reads at 230400 baud, so `println` output is readable out of the box. If you
> load with the raw `ogo loadp2` passthrough instead, it uses loadp2's own
> defaults — which leave the P2 on its imprecise internal oscillator, so the
> output is garbled at *every* baud. Pass a real clock and the matching baud:
> `ogo loadp2 -f 200000000 -b 230400 -t prog.binary` (the `-f` is the key part;
> `200000000` assumes the usual 20 MHz P2 crystal — use your board's value if it
> differs). This is a P2 clocking detail, not an ogo bug.

### Drawing on an oscilloscope

`_examples/gopher` puts the Go gopher on a scope in X/Y mode: two smart pins run as
DACs, one X and one Y, and the beam walks the outline while persistence does the
rest. There is no framebuffer and no display controller — the figure *is* the two
voltages, redrawn about a thousand times a second. Eight frames make it dance.

```sh
ogo run _examples/gopher     # then probe the two pins it names, scope in X/Y mode
```

## **Language Specification**

The language is specified in the package documentation:
[**OctoGo Language Specification**](https://pkg.go.dev/modernc.org/ogo#hdr-OctoGo_Language_Specification).
It is a draft and is reconciled with the implementation as the compiler moves;
the **Status** section below is the shorter answer to what works today, and
[CHANGELOG.md](CHANGELOG.md) records what changed in each release — including the
cases where a new release rejects a program the last one accepted.

## **Architecture & Design**

OctoGo is designed to be a zero-cost abstraction over the Propeller 2's unique 8-cog architecture.

* **Native Hardware Concurrency:** The go keyword transpiles to a scoped block that claims a pooled slot holding the goroutine's stack and its arguments, then invokes \_cogstart\_C. The pool has one slot per available Cog, so the P2's 8-cog limit is enforced by construction; exceeding it is a runtime panic.

* **Hardware-Backed Channels:** Channels (chan) are not software queues. Each is a rendezvous cell in Hub RAM guarded by one of the P2's native hardware locks (0-15), giving atomic, lock-step transfer. A channel is created by its declaration, not by make: there is no allocator to make it with. Past the sixteenth channel the locks are shared rather than exhausted, which costs contention and nothing else — twenty-four channels each completing a rendezvous run correctly on a P2-EDGE.  
* **Zero-Allocation & No GC:** OctoGo operates without a Garbage Collector. Memory scoping is strict (Hub RAM vs. Cog RAM), and slices are implemented as non-escaping views over fixed arrays.  
* **Select Statements:** The select statement is transpiled into an efficient polling loop, utilizing flexprop's \_waitx yield instructions to prevent bus starvation during non-blocking hardware polling.

* **Implicit Namespaces:** To keep the grammar clean and strictly LL(1), there is no package keyword. A package's namespace is implicitly inferred from its directory name, mapping cleanly to a single C translation unit.

## **The Compiler Pipeline**

OctoGo is a source-to-source compiler (transpiler) written in Go.

1. **Frontend:** Lexical analysis and parsing are generated via modernc.org/egg using a LL(1) grammar. The AST is represented as a zero-pointer, cache-local flat \[\]int32 slice.

2. **Semantic Pass:** Uses Go 1.23+ iterators (`func(yield func(node) bool)`) to traverse the AST, calculate memory offsets, and resolve scope.  
3. **Transpilation:** Emits standard C. The runtime it needs -- bounds and divide-by-zero traps, slice and channel helpers, the goroutine pool -- is emitted into the same file, per program, so only what is used is paid for.  
4. **Backend Generation:** The ogo build command automatically feeds the emitted C into flexprop, delegating register allocation, instruction scheduling, and P2 binary generation to a proven, hardware-aware backend.

## **Status: early preview**

OctoGo compiles and runs real programs on real Propeller 2 hardware, and every
change is verified by a suite that builds each test program with the real backend,
loads it onto a P2 and checks its serial output. It is also unfinished. This
section is the honest inventory — please read it before deciding the compiler is
broken.

**Works** (all of it exercised on hardware):

* Integers (sized and unsized, including 64-bit), `byte`, `rune`, `bool`, `string`,
  structs, fixed arrays including multi-dimensional, slices, named types, channels.
  Structs may refer to themselves, to each other, and to a type declared later, so
  linked lists, trees and graphs build. A named type behaves as what it is defined
  over — `type Name string` indexes, slices, ranges and compares as a string, `type
  List []int` as a slice, `type Named Point` has Point's fields and takes Point's
  literals — and carries its own methods. That holds for every kind a type can be
  defined over: a pointer dereferences, a function is called. A literal may name
  such a type, `Row{1, 2, 3}` for `type Row [3]int`.
* A slice literal stands wherever a value may — `sum([]int{1, 2, 3})` — its backing
  array a local of the function that wrote it, with the lifetime that implies.
* Slicing, including the capacity bound: `pool[0:0:64]` hands out a region of a
  package-level buffer that appending cannot grow past, which is how you sub-divide
  storage with no heap to allocate from.
* Composite literals: structs positionally (`P{1, 2}`, `P{}`, nested) or by field
  name (`P{y: 2}`, any subset in any order, the rest zeroed), and arrays and slices
  positionally, by index or mixed (`[4]int{10, 20}`, `[]int{1, 2, 3}`,
  `[5]int{0: 1, 4: 9}`), including multi-dimensional (`[2][3]int{{1, 2, 3}, {4, 5,
  6}}`) and at package scope, which is how you write a lookup table. One place a
  literal may not appear bare is the top level of an `if`, `for` or `switch` header,
  where `{` is the block — parenthesize there, as in Go: `if p == (P{}) {`. That is
  only for a literal whose type is a bare name; `for _, v := range []int{1, 2, 3}`
  needs nothing, a `[` not being able to begin a block.
* `var` (including several names and a value list), `const` with `iota`, `type`,
  functions and methods with value or pointer receivers. A package may declare
  several `init` functions; they run in the order they are written, before `main`.
* Named and unnamed parameters and results, multiple return values, naked returns.
  Several values may be destructured out of a plain call, a method call or an
  imported package's call alike, `b, ok := r.pop()`.
* A declared function used as a **value**: a variable, parameter, result, array
  element or struct field of a function type holds one and a call through it is a
  call, `chosen = add; chosen(1, 2)`. It becomes a C function pointer, so it costs
  nothing and allocates nothing, and one is always safe to send to another cog — it
  names code, not the frame it was made in.
* `if`/`else` and `switch` including an init statement (`if v := f(); v > 0`,
  `switch v := f(); v`), which may declare several names from one call
  (`if v, ok := m.get(k); ok`), all `for` forms including `range`, `switch` with or without
  a guard, `fallthrough`, `break` and `continue` (including labeled), `defer`
  (including in nested blocks, capturing its arguments).
* The full operator set, compound assignment, multiple assignment, and equality on
  strings, structs and arrays. A multiple assignment writes to anything a single one
  can — an element, a field, a pointee — so `xs[i], xs[j] = xs[j], xs[i]` swaps.
* A call's result may be used directly: a field read off it (`mk().y`), a method
  called on it, or an index into it (`mk()[1]`, `mk().d[1]`).
* `len`, `cap`, `append`, `copy`, `clear`, `min`, `max`, `make` for a
  fixed-capacity slice, `panic`, `print`/`println`.
* A predeclared **`Builder`** for assembling a string at run time without a heap:
  `NewBuilder(back[:])` puts a write cursor over a byte array you own, and
  `WriteString`, `WriteByte`, `WriteRune`, `Write`, `String`, `Len` and `Reset`
  behave as `strings.Builder`'s do, except that a write past the backing is
  truncated rather than growing it — you chose the size. It is meant to become
  `strings.Builder` once there is a standard library to put it in.
* `go`, `chan` and `select`, mapped to cogs and hardware locks. A method may be
  launched too, `go w.run(ch)`, its receiver evaluated and copied where the `go`
  stands — including one reached through fields and indexes, `go ws[i].run(ch)`,
  which is a worker per cog — as may an imported package's function,
  `go driver.Poll(ch)`. A `select`
  multiplexes receives and one send, `case ch <- v:`. Channels may be declared at
  package level as well as locally, and the P2's locks are reachable directly through
  the `p2` package.
* Runtime traps for out-of-range indexing and slicing, division and remainder by
  zero, a shift by a negative count, appending past a slice's capacity, and cog
  exhaustion. Each prints `panic: <what>` and halts the offending cog; `--release`
  reboots the board instead, and `--unchecked` omits the checks.
* A package is a directory: `ogo build` compiles every `.ogo` file in it together,
  and a program may span several packages. A value of an imported package's struct
  type is written the way you would expect, `geo.Point{1, 2}`.

**Does not work yet**, in rough order of how likely you are to hit it:

* **There is no standard library.** The `p2` package wraps twenty-four intrinsics
  (pin control, smart pins, timing, the hardware locks), and `testing` carries the
  state a test reports through. That is the whole of it. Your own packages do import
  and build; there is just nothing else to import yet.
* A **goroutine's stack is a fixed 256 longs** and cannot be sized per `go`
  statement. Recursion works — `main` runs on the cog's own stack and a goroutine on
  its pool slot's — but a deep enough call chain in a goroutine overruns that slot
  with no diagnostic, this part having no memory protection. Measured on a P2-EDGE, a
  goroutine recursing 200 deep is fine and one recursing 2000 deep prints nothing at
  all.
* A **channel held in a struct field**: a send or a receive on one is refused,
  whether the field is written `chan T` or a defined type over one. A channel in a
  variable, a parameter or a package-level declaration is fine. What has to be
  settled first is where the rendezvous cell lives — a channel variable gets one
  allocated beside it, and a field cannot without deciding what declaring the struct
  allocates, and what a copy of it then shares.
* A **method on a defined type over a channel**, `func (c Ch) tag()`. Sends,
  receives and select clauses on such a type all work.
* A conversion to a defined array type may not be **indexed where it stands**,
  `row(g)[2]`; bind it first. C has no cast to an array type. The conversion itself,
  and every other use of the result, works.
* An **array** literal may not stand as a general value — it initializes a variable,
  fills a slot in another composite literal, or is what a `range` walks, and nothing
  else, C having no array value for it to become. A *slice* literal has no such
  limit; its backing array is a local, so it carries that local's lifetime.
* A `select` may carry at most one send clause, and none alongside a `default` —
  both need a "receiver is ready" signal the rendezvous does not carry.
* A function with more than **one result used as a value**; `go` through a variable
  holding a function rather than through the function's own name; and a **method
  value whose receiver is not a package-level variable** — the receiver is bound at
  compile time, which is what keeps a function value one word (`doc/funcval-cost.c`
  prices the alternative).
* An array as a function result, a slice whose element is an array, and `goto`.
* A `range` clause written with `=` accepts only variables, not an element or a
  field: `for xs[0], a[0] = range xs` is refused. Plain variables are fine.
* A three-clause `for` header declares **one** variable: `for a, b := f(); a < b;`
  does not parse. `if` and `switch` headers take several, `if v, ok := f(); ok`.

Floating point (float32/float64) is supported, exponent literals included
(`1e3`, `1.5e-3`): the P2's C toolchain provides it,
so float arithmetic, comparison, int conversions and printing all work. Note the
target has no double-precision hardware, so `float64` is 32-bit here, same as
`float32` (~7 significant digits, not ~15) -- the name is kept for Go source
compatibility but carries no extra precision.

**Not planned**, because the target does not permit them: a garbage collector, a
heap, maps, closures that capture their environment (a function *value* is fine —
it is a pointer to code, not to a frame), and runtime string concatenation.
Constant string concatenation folds at compile time; to assemble one at run time,
write into a `Builder` over storage you own.

Having no heap has one consequence worth knowing before you meet it: a reference
must not outlive what it refers to. Where Go would move the referent to the heap and
say nothing, there is nowhere to move it to, so the program is refused instead —
returning a local's address or a slice backed by a local, storing either in a package
variable, or handing either to another cog as a `go` argument or through a channel. A
struct holding such a reference counts as one, and the requirement follows a
parameter back to the call sites that chose the storage. Declare the buffer at
package scope and pass a slice of it, which is what the diagnostics ask for:

```
var buf [64]byte

func main() {
	var done chan int
	go fill(buf[:], done)   // buf outlives every frame
	<-done
}
```

Everything else Go has is meant to be here eventually — anything missing above is
work not yet done rather than a decision taken. Generics are the one open question:
not supported, not planned, and not ruled out either.

Interfaces work: an interface value is a pointer to what it carries beside a
pointer to a statically emitted vtable, with type assertions and type switches on
top. A pointer is what goes into one -- `var s Shape = &q`, never `= q` -- because
there is no heap to copy into and a silently aliasing value form would mean
something Go does not. The whole-program-optimization pass that would devirtualize
the calls is still an open question, and opinions are welcome.
