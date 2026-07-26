# Changelog

Notable changes per release. The **Language** section is what changes for code you
write; the rest is the toolchain around it.

OctoGo is an early preview and its version numbers say so: releases are cut from
`master` roughly weekly, and each one may reject a program the last one accepted —
those are called out under **Behaviour changes**, and they are almost always the
compiler catching something it should have caught before.

Releases before v0.9.0 predate this file; see
[the releases page](https://github.com/modernc-org/ogo/releases).

## Unreleased

### Language

- **`switch` with an init statement**, `switch v := f(); v`, as `if` gained last
  release. The name is scoped to the whole statement — the expression switched on,
  every case expression and every clause body — and not beyond, so it may shadow an
  outer one while its own initializer still reads that outer one. The expression
  switched on may be left out, `switch v := f(); { case v > 3: }`, which switches on
  true with the name in scope. Only the `:=` form; Go also admits an assignment or
  an increment there, and one is now rejected by name rather than as a parse error.

  This is the portable spelling of OctoGo's own `switch v := f()`, which Go rejects
  as a syntax error. That form still works and means the same thing.

- **A list may end with a trailing comma**, which is what lets it be written across
  lines — the form gofmt produces, and the only readable way to spell a table:

  ```go
  var table = []P{
  	{1, 2},
  	{3, 4},
  }
  ```

  Go takes one in a composite literal, a call's arguments, a parameter list and a
  result list, and so does this; the lists where Go does not — an assignment's
  right-hand side, a `return`, a list of names — are unchanged. `ogo fmt` keeps a
  trailing comma where it does that work and drops one whose list closes on the same
  line, as gofmt does.
- **An array literal may be a composite literal's element**, `P{1, [2]int{2, 3}}`,
  which is how a record holding a fixed array is written — and so how a lookup table
  of them is. It was refused with `a [2]int literal is only supported as a
  variable's initializer`, true of a bare one but not of a nested aggregate. A
  package-level one is laid out statically, costing no start-up work.
- **A slice reached through an index may be read and written**, `s[i].v[j]`, and
  sliced again, `s[i].v[1:]`. The index before it left the field's header with
  nothing to measure a bounds check against, so the whole shape was refused; it is
  bound to a temporary now. A header is a view, so a write through it lands in the
  storage the field names. Still out: the same over a row of a multi-dimensional
  array, `m[0][:][1]`, an array being the one value C cannot name.
- **A slice-typed field of an indexed element may be assigned**, `s[i].v = xs` and
  `a[i].v = xs`. Its other fields already could; a slice-valued one was refused with
  `only simple and field assignment targets are supported yet`. What is assigned is
  the header, so the field ends up viewing the same storage the right-hand side does.
- **A slice expression may be indexed or sliced again**, `a[1:6][2]` and
  `a[1:6][1:4]`, over an array, a slice, a struct field or a string. Both had to be
  written as two statements before. The index is checked against the length of the
  expression it applies to, not the operand's. One shape is still out: `m[0][:][1]`,
  where an index has already consumed the operand the slice would be built from.
- **A slice expression may set the result's capacity**, `a[low:high:max]`. Without a
  heap this is how a region of a package-level buffer is handed out: appending to the
  region stops at its own end instead of running on into the next one's storage.

  ```go
  var pool [256]byte

  head := pool[0:0:64]     // appending stops at 64, not at 256
  tail := pool[64:64:128]
  ```

  Only `low` may be left out of the three, so `a[l::m]` and `a[l:h:]` are rejected in
  Go's words, and a string has no capacity for a bound to set. The new bound is
  checked with the other two, `0 <= low <= high <= max <= cap`.

### Behaviour changes

- **A `switch` guard may no longer be the blank identifier.** `switch _ := f()`
  declared `_` and then switched on it — reading a name Go says cannot be read — and
  compiled to a comparison against a C variable named `_`. What a switch switches on
  is now resolved like any other expression, which reports it.

### Behaviour changes

- **A package array's element may no longer be given a reference to a local.**
  `table[1] = local[:]`, where `table` is a package-level array, left it pointing at
  storage the function was free to reuse — the rule that refuses this for every other
  package variable was asking one environment for the target and arrays live in
  another, so every array target went unchecked. Declare the buffer at package scope
  and slice that, which is what the diagnostic asks for.

### Fixed

- **A string byte is unsigned.** `s[i]` is a `byte` in Go, but it was read straight
  off the string's `const char*`, whose signedness C leaves to the implementation —
  so on one where `char` is signed, any byte over 127 came out negative: summing the
  bytes of `"hé"` gave −44 where Go gives 468. The target's C compiler happens to
  make `char` unsigned, so this was right on a board and wrong under the host test
  compiler; the read is now cast either way. The runtime helpers already did.
- **A loop condition is evaluated on every iteration again.** Some expressions need
  a line emitted ahead of the statement they appear in — a field read off a call's
  struct result, arguments put in Go's order, a bounds-checked slice — and ahead of
  the statement is the wrong place when the statement is a loop and the expression
  is its condition. `for i := 0; i < mk().y; i++` called `mk` once and then tested
  that one value for the rest of the loop, where Go calls it before every test. Both
  conditional forms now carry the test at the top of the loop body instead, so
  `continue` still reaches the post step and `break` still leaves the loop.

  The post statement has nowhere to move to — it runs after the body and on every
  `continue`, which is what C's third clause is for, and that clause can declare
  nothing — so `for i := 0; i < 3; i = mk().y` is now refused rather than computing
  its value once. Compute it in the loop body.
- **A slice expression's bounds are now checked.** Slicing was the one indexing
  form that trapped on nothing: over a `[4]int`, `a[1:9]` produced a length-8 view
  of storage the array does not own and `a[3:1]` a length of −2, and both compiled
  quietly. On a part with no memory protection, writing through either is a write
  into whatever sits next in Hub RAM. The rule is Go's, `0 <= low <= high <= cap`,
  and a bound outside it panics with `slice bounds out of range`. Bounds a
  compile-time extent already settles — `a[:]`, or constants within an array's
  length — carry no check, and `--unchecked` and `--release` omit them as they do
  for indexing.
- **A slice bound is now evaluated once.** The header names the low bound in all
  three of its fields, so `a[next():5]` called `next` three times and built a
  header whose pointer, length and capacity were each computed from a different
  value. Go evaluates each bound once. This one is fixed in unchecked builds too.
- **A package variable could not be initialized from a call's field.** `var corner
  = mk().y` needs the result bound to a temporary first, and at package scope the
  line declaring that temporary was dropped — the emitted C named a variable it
  never declared, and the build failed. Package initialization runs from a
  synthesized function rather than from a statement, which is where the temporary
  used to be placed.
- **A switch guard is now declared like the variable it is.** The emitter wrote its
  declaration out by hand instead, and three things went wrong. A `switch v := v + 1`
  whose initializer names the variable it shadows read the new, uninitialized one
  rather than the outer one, so the switch silently took the wrong branch — the same
  bug `var x = x + 5` had. A Unicode-named guard, `switch δ` or `switch ε := δ * 2`,
  was declared under its source spelling while every use of it was escaped, so the
  build failed with an undeclared identifier. And a guard that is neither a plain
  variable nor a `:=` binding, `switch greet()`, bound a temporary that was not
  recorded as a variable, so a string one was compared with C's `==` on the header
  struct — which the backend rejects — rather than by content.

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
