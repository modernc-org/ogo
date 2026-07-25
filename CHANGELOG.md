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

- **A bare receive statement**, `<-ch`, discarding the value — how one goroutine
  waits for another. It previously had to be written `_ = <-ch`.

### Language

- **Array equality**, `a == b` and `a != b`, comparing element by element. Works for
  any comparable element type — scalars, strings, structs — and at any rank.

### Fixed

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

Each of these rejects a program the previous release accepted. All were verified
against real Go, which rejects them too.

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
