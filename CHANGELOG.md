# Changelog

Notable changes per release. The **Language** section is what changes for code you
write; the rest is the toolchain around it.

OctoGo is an early preview and its version numbers say so: releases are cut from
`master` roughly weekly, and each one may reject a program the last one accepted —
those are called out under **Behaviour changes**, and they are almost always the
compiler catching something it should have caught before.

Releases before v0.9.0 predate this file; see
[the releases page](https://github.com/modernc-org/ogo/releases).

## v0.10.0

### Language

- **A bare receive statement**, `<-ch`, discarding the value — how one goroutine
  waits for another. It previously had to be written `_ = <-ch`.
- **`if` with an init statement**, `if v := f(); v > 0`. The name is scoped to the
  whole statement — the condition, the `then` block and every `else if` branch — and
  not beyond, so it may shadow an outer one. Only the `:=` form, which is what
  nearly every use is.
- **Array equality**, `a == b` and `a != b`, comparing element by element. Works for
  any comparable element type — scalars, strings, structs — and at any rank.
- **A call's result may be indexed**, `mk()[1]`, including through a field,
  `mk().d[1]`.
- **A field may be read off a call's struct result**, `mk().y`. It was refused
  because the target's C compiler miscompiles that read directly; the result is now
  bound to a temporary first, which reads correctly.

### Behaviour changes

- **A reference to a local may no longer be handed to another cog.** `go f(&x)` and
  `ch <- &x`, and the slice forms `go f(a[:])` and `ch <- s` where the backing array
  is a local — all four compiled and produced a program reading storage its owner was
  free to reuse. The two earlier rules of this kind are about a reference that
  outlives its referent; this one is about a reference that leaves the frame's
  control, since a goroutine runs until it returns and a receiver keeps what it took.
  Declare the buffer at package scope and pass a slice of it:

  ```go
  var buf [64]byte

  func main() {
  	var done chan int
  	go fill(buf[:], done)   // buf outlives every frame
  	<-done
  }
  ```

  An ordinary call and a deferred one are unaffected: both read the argument while
  the frame is still alive.

  A parameter may still cross — whose storage it is, is the caller's business — but
  **the requirement now travels to the caller**. A function that lets a parameter
  reach another cog, itself or by passing it to a function that does, may only be
  called with storage that outlives the goroutine, and it is the *call* that is
  refused:

  ```go
  func spawn(p []int) { go work(p) }   // parameter p reaches another cog

  func setup() {
  	var local [4]int
  	spawn(local[:])             // refused here, where the storage was chosen
  }
  ```

  That holds however many calls separate the two, across package boundaries, and
  through mutual recursion.

  **A reference wrapped in a struct counts as one.** Assigning a local's address or a
  slice of a local array to a field — or filling the field in a composite literal —
  marks the variable, and a copy carries the mark, so all four sinks refuse it:

  ```go
  var a [4]int
  var b buf
  b.data = a[:]
  go work(b)          // refused: b holds a pointer into local a
  ```

  The mark is per variable and never cleared, which is what makes it sound without
  per-field tracking. It is also the one place the rule refuses more than it must: a
  variable whose only such field is later overwritten with package-level storage stays
  marked. Two smaller holes closed with it — `s = a[:]` by plain assignment (only the
  declaration form was checked before), and any check on a scalar or pointer field
  target, which used to leave through a path that ran neither.

### Fixed

- **A goroutine argument wider than one word corrupted the goroutine's stack.** The
  block a `go` statement marshals its arguments into was sized at one word per
  argument, so a slice (three), an `int64` or `float64` (two), or a struct overflowed
  it — and it sits directly above the stack the new cog is about to run on. `go
  fill(buf[:], done)` produced a program that printed nothing and hung, with no
  diagnostic from either compiler. The block is now a union of every `go` site's
  arguments, so its size and alignment come from the types themselves.
- **A program could only ever start seven goroutines.** The seven-cog limit is meant
  to bound how many run at one time, and a finished goroutine's slot to be reused,
  but a goroutine that has just handed its result to the receiver is a few
  instructions short of stopping — and the eighth `go` statement reported `panic: out
  of cogs` rather than waiting out those instructions. So `for i := 0; i < 20; i++ {
  go worker(ch, i); sum += <-ch }`, twenty goroutines one after another with never
  more than one alive, died on the eighth. Only on hardware: off-target the shim's
  thread wins the same race.
- **A locally declared channel's storage is now static.** Its cell used to be a
  local of the declaring function, so `var ch chan int; go worker(ch)` — the
  ordinary way to write this — handed another cog a pointer into a frame the spawner
  was free to leave. The same change fixes a lock leak: the cell's lock was acquired
  on every call and never released, so a function declaring a channel could be
  called about fifteen times before the P2 ran out of hardware locks. The cell now
  belongs to the declaration site, so two concurrent calls of one function share its
  channel rather than each having one.
- **A call's arguments are now evaluated left to right**, as Go specifies. C leaves
  the order unspecified and the two compilers disagreed: the P2 backend went left to
  right, the host's gcc right to left, so a program whose arguments had side effects
  answered differently depending on which built it.
- **A deferred call in `main` could not capture an argument.** `defer f(x)` there
  failed to build with `Unknown symbol '_ogo_defer0_a0'`: the capture was emitted
  and the temporary it assigned to never declared. `main` was the only function
  missing that step, so the same code in any other function already worked.
- **Array equality silently answered `false`.** `a == b` on two arrays emitted C's
  `a == b`, where both operands decay to pointers, so it asked whether they were the
  same array. It compiled cleanly and was always false. Array ordering (`a < b`),
  which Go does not define, is now refused rather than doing the same thing.
- **A mixed short declaration silently computed the wrong answer.** In `a, b :=
  f()` with `a` already declared, Go assigns to `a` and declares only `b`; the
  emitter declared both, so the C had two declarations of `a` in one block. The
  target's C compiler accepts that with a warning and then ignores the second, so
  the build *succeeded* and `a` kept its old value. Affected every form — a
  multi-result call, a value list, a receive, and `s, ok := append(s, x)`.

## v0.9.0

### Language

- **`fallthrough`** in an expression switch. It is legal only as the last statement
  of a clause that is not the switch's last. Note it is now a **reserved word**.
- **`&^`**, the bit-clear operator, is a real operator rather than two tokens that
  happened to compute the same answer. `ogo fmt` no longer rewrites `x &^ y` into
  `x & ^y`.
- **`break` leaves a `select`**, as it does in Go.
- **Multi-dimensional array literals**: `[2][3]int{{1, 2, 3}, {4, 5, 6}}`, at any
  rank, with rows written type-elided or with their type.
- **Package-level tables**. An array or slice literal may initialize a package
  variable, with the type written or inferred — `var sizes [4]int = [4]int{1, 2, 4,
  8}`, `var primes = []int{2, 3, 5, 7}`. Each is laid out statically, so it costs no
  start-up work.
- **Slicing a row** of a multi-dimensional array, `m[i][:]`. The result aliases the
  array, so a write through it is a write to the array.
- **Structs may refer to themselves**, to each other, and to a type declared later:
  linked lists, trees and graphs now build.
- **`nil`** as a slice value (`s = nil`, `var s []T = nil`, `return nil`), compared
  against a slice (`s == nil`), and passed as a slice argument (`f(nil)`).
- **`append(s, a, b, c)`** with several values, and **`b := a`** copying an array by
  value.
- **The `p2` package gained the hardware locks**: `NewLock`, `TryLock`, `Unlock`,
  `FreeLock` over the P2's 16 locks — enough to write a multi-producer structure in
  user code.

### Behaviour changes

- **A slice may no longer outlive the storage it views.** Returning one whose
  backing array is a local of the frame is refused, as is storing one in a
  package-level variable or a field of one. The header outlived the storage, and with
  no heap there is nowhere to promote that storage to, so the reader saw a dead frame
  — usually getting the right answer anyway, until something reused the memory. Use a
  slice over a package-level array, or over one the caller passed in.

- **A constant that does not fit its type is reported wherever it meets one.**
  Previously only a written target type was checked, so `var x int = 1 << 40` was
  rejected while `var x = 1 << 40`, `x := 1 << 40`, `return 1 << 40`,
  `println(1 << 40)` and a later assignment to an inferred variable all passed and
  silently truncated.
- **`nil` is only assignable to a pointer, slice or channel.** `var n int = nil` and
  `return nil` from a string function used to be accepted and emit a zero; the
  string forms used to fail in the C backend instead, naming C you never wrote.
- **A `break` makes a `switch` or `select` non-terminating**, so a function that can
  fall past one without returning is now "missing return" rather than a C function
  that runs off its end.
- **`fallthrough` is a reserved word**, so it can no longer be used as an identifier.

Each of these rejects a program the previous release accepted. All were verified
against real Go, which rejects them too.

### Fixed

- A package-level name matching a field of the goroutine runtime — `done`, `used`,
  `cog`, `args`, `stack`, `slot` — broke the build outright as soon as the program
  used `go`. `done` is about the commonest name in Go for a completion channel.
- `struct` equality miscompiled when a field was named `a` or `b`.

### Tooling

- The `ogo smith` oracle fuzzer, which compiles generated programs and checks a
  self-verifying checksum, now covers the full integer operator set, compound
  assignment, fixed arrays, slices (`make`/index/`append`/`len`/`cap`) and function
  calls.
- The language-feature policy is written down, in `specs.go` for language users and
  in `CLAUDE.md` for contributors: OctoGo aims to support what pre-generics Go
  supports wherever that is feasible on the target, and a construct that does not
  work is work not yet done rather than a decision taken. The deliberate exceptions
  are the hardware-rooted ones — no heap, a goroutine is a cog, a channel is a
  hardware lock — plus interfaces, whose dispatch model is unsettled, and generics,
  which are not supported, not planned, and not ruled out.
