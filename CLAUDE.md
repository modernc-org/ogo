# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

OctoGo is a special-purpose, Go-inspired programming language and its compiler,
targeting the Parallax Propeller 2 (P2) 8-core microcontroller. The compiler is a
source-to-source transpiler written in Go: it parses a strict LL(1) subset of a
Go-like language, statically checks it against the P2's zero-allocation/no-GC
hardware model, and emits C that is compiled to a P2 binary by an in-process,
transpiled copy of the flexspin/flexcc C compiler.

This is early-stage work in progress, but the whole pipeline is connected: the
frontend (scanner, parser, formatter), the checker, C emission and the backend
all work, and `ogo build` produces a P2 binary while `ogo run` loads it onto a
board. What is missing is breadth, not a stage -- see **Implementation status**.

## Working rules

Each of these was learned by breaking it; the stories are in `doc/LESSONS.md` (see
**Lessons** below).

- **The board is shared.** Every use of the P2's serial port -- `make board`, `ogo
  run`, `ogo test`, `ogo loadp2`, `scripts/board.sh`, `scripts/cboard.sh` -- runs
  under `flock -w 900 /tmp/p2-board.lock` (`flock -o` where the command starts
  children), as agreed with the p2-11 session that uses the same board. A second
  loader on the port talks over the first; `Prop_Ver G` in a capture is that.
- **The machine is shared**: the user's builder VMs run on it. A batch runs at
  `xargs -P 4` at most, 2 beside `make test`, one heavy batch at a time.
- **A compiler run in a sweep is capped** (`scripts/capped.sh`), and dumpc's exit
  status is the verdict: 0 compiled, 1 refused, anything else a compiler FAULT --
  never logged as a refusal.
- **A commit is gated on the suite log's `exit=0` line and the absence of `FAIL`**,
  never on `cat log &&`. **A change to the checker is built against obeam and
  p2-11 before it is pushed**: they are the packages of several files the suite
  does not have. No edits to `internal/octogo/*.go` while a full suite
  runs: `TestTargetGoStack` and `TestTargetBuildExamples` build `ogo` from the
  working tree as they go.
- **A change to what the emitter writes around every call or every dereference --
  a check, a helper -- is run through `make board` before it is committed**, and so
  is a change to how narrow values or constants are spelled: the host's compiler is
  no witness for the target's typing rules.
- **A new goroutine run case is run with `-count=5`** before it is committed.
- **A change for one shape touches only that shape's programs**:
  `scripts/dumpcorpus.sh` before and after, `scripts/corpusdiff.sh` between them,
  and the two listings' COUNTS compared -- a newly refused program is a file missing
  from the after listing, which corpusdiff does not report.
- Before/after and provenance checks use a **git worktree, never `git stash`**.
- p2-11 (`../p2-11`) is built and tested from a `git archive` copy in a scratch
  directory, never in its own tree (the p2-11 session's request).
- **Pushing and tagging only when the user asks.** Anything posted upstream
  (spin2cpp, flexprop) is shown to the user and approved first, and spin2cpp
  commits carry no Claude trailers.

## The v1.0 exit criterion

v1.0 is gated on a measured criterion, not a date (the user's call, 2026-10-25).
A ROUND is a new domain program written as Go (A DOMAIN PROGRAM IS A PROBE), its
mutants -- about 400 -- against Go on the host and on the board, and a board
sweep of any fuzzer part changed since the round before. A round is CLEAN when it
finds no silent fault: no wrong output, on the host or the board, and no program
Go rejects accepted. A refusal of valid Go is a gap, fixed or listed, not a
fault. A release candidate follows three clean rounds in a row; a round with a
silent fault puts the count back to 0. Each round's account is in
`doc/LESSONS.md`, its count here:

- dom61 (2026-10-24), a measurement chain over float64: silent (float64 folds
  and call order, a typed float constant's rounding) -- 0
- dom62 (2026-10-24), an expression calculator: clean -- 1
- dom63 (2026-10-25), linear algebra over float64: clean, one refusal (the row
  swap through pointers) -- 2
- dom64 (2026-10-25), motor control in float32: silent (a float32 compound
  constant computed in doubles on the host) -- 0
- dom65 (2026-10-25), a firmware update path over Intel HEX: clean -- 1
- dom66 (2026-10-25), text on a character display: silent (%q wrote a rune that
  does not print as it stood) -- 0
- dom67 (2026-10-25), a vacuum system's PLC: clean, one false refusal fixed (an
  inner block's slice holding a view of an outer block's storage) -- 1
- dom68 (2026-10-26), a G-code interpreter and motion planner: clean -- 2
- dom69 (2026-10-26), settings in NOR flash: silent (the lifetime summaries: a
  bare return handed back nothing, and a callee's store through a local's address
  was not the local's), one false refusal fixed (a call's error beside a view of a
  local buffer) -- 0
- dom70 (2026-10-26), a CAN network: silent (an array of structs holding an array,
  a literal's field, copied as one element) -- 0
- dom71 (2026-10-26), a sealed telemetry link (SHA-256, HKDF, ChaCha20-Poly1305):
  silent (a goto back's declarations kept from the first pass; a deferred call's
  change to a named result lost after a forwarded call), three false refusals
  fixed (a parameter given a view of the body's local, an error beside a view
  through a field, an interface and a forwarded call), one listed (an if's local
  kept by a parameter) -- 0
- dom72 (2026-10-26), an SD card's FAT16 file system under a data logger: silent
  (a string a Builder over a local array hands out, and the Builder itself held in
  a field, kept past the frame), two false refusals fixed (a store through a name
  that holds a pointer), two loud C refusals fixed (a by-value struct named like a
  member, on the board; an array of Builders), one listed (a callee keeping a
  string field beside a buffer) -- 0
- dom73 (2026-10-27), the network side of a sensor node (ARP, IPv4 fragments,
  DHCP, DNS, TCP over a cable that loses frames): silent (a slice of a struct's
  array of a defined array type typed by its extents, `%T` of it wrong), two false
  refusals fixed (a callee's spread into append taken to keep the slice; copy
  between the two typings) -- 0
- dom74 (2026-10-27), a polyphonic synthesizer (a MIDI track, six voices, a DAC
  cog): clean, one loud fault fixed (the lifetime summaries' walk through the calls
  a rebound buffer holds never ended) -- 1
- dom75 (2026-10-27), compressed assets in flash (Go's flate, zlib and gzip
  readers ported, an LZ77 and Huffman encoder, a flash cog, a logger cog): clean,
  one false refusal listed (a field kept out of a struct holding a view of the
  frame in another) -- 2
- dom76 (2026-10-27), configuration and telemetry over JSON-RPC (encoding/json's
  scanner, strconv's parsers and utf8 ported, COBS frames over a spoiling line, a
  retransmitting client cog): clean, one loud host fault fixed (the host shim did
  not say a panic does not return) -- 3
- **v1.0.0-rc.1** (2026-10-27), tagged after dom76: rounds against it count from
  0 of 3, and its two weeks in public from its release
- dom77 (2026-10-27), a sensor node's messages in protobuf's wire format
  (protowire and encoding/base64 ported, a producer cog framing batches onto
  base64 lines): silent (a callee's view of a view of its parameter kept past the
  frame; what a call hands back holds, asked nowhere it is bound), one false
  refusal fixed (a []byte's contents held to a holder's marks), a loud fault
  fixed (a slice of slices of pointers named a typedef nobody declared, and two
  element types were one C name), two
  listed (a store two pointers past a receiver; a slice literal's backing dated
  to the inner block it is read in) -- 0

**From a release candidate to v1.0** (the user's call, 2026-10-27, after an
outside review read three clean rounds as the start of a stabilization, not its
end): a release candidate freezes `master` to fixes -- of silent and loud faults,
and of a false refusal whose fix is local; a redesign waits on a branch until
v1.0.0 is tagged, the per-field lifetime marks (dom75's listed refusal) among
them. The rounds go on against `master` as it stands. v1.0.0 is tagged when a
candidate has passed three more clean rounds and two weeks in public with no
silent fault found; a silent fault is fixed, the fix is the next candidate, and
both counts start again. Each candidate and v1.0.0 publish their validation with
their notes: the commit, the host suite's and `make board`'s results, p2-11's on
the board, obeam's builds, the rounds since the last.

## Language feature policy (read before assuming something is excluded)

**The goal is to support what pre-generics Go supports, wherever that is feasible
on the target.** A construct that does not work is therefore *work not yet done*,
not a decision taken -- unless it is one of the hardware-rooted exceptions below.

This distinction matters because it is easy to get backwards. The grammar was
frozen early to reach a proof of concept, and the notes written then ("OctoGo does
not support X") have been read since as design decisions when they were only
status. Floats, the sized integer types, `&&`/`||`, three-clause and range `for`,
labels, multi-package programs and directional channels were all once "not
supported" and are now in the language (specs.go called the last "To maintain a
strict LL(1) grammar" -- it took one extra production, 2026-09-18). So was the bare
conversion `[]int(x)`, parenthesised by rule for the parser's sake until an
alternative measured at one First/Follow decision, settled as Go settles it, took it
(2026-09-21). **Before treating any such note as settled, check whether the
feature actually works** -- and if a stale note is found, fix the note.

The deliberate exceptions, all rooted in the Propeller 2 hardware:

- **No heap.** Nothing allocates at run time. This is what rules out `new`, every
  `make` form but the slice and the (unbuffered) channel one, maps, runtime string
  concatenation, and a function literal that captures its surrounding scope.
- **A goroutine is a physical cog** (there are eight). No scheduler, no
  preemption; `go` starts a real core, not a task.
- **A channel is a P2 hardware lock** over a cell in Hub RAM, static or the
  frame's -- a synchronous rendezvous with no scheduler behind it.
- **An interface value holds a POINTER**, and only a pointer: `var s Shape = &q`,
  never `= q`. Shipped 2026-08-03 -- a data pointer beside a pointer to a statically
  emitted vtable, one table per (concrete type, interface) pair, with type
  assertions and type switches on top. Go copies the value in and allocates for it;
  there is no heap here, so the value form is refused rather than made a silently
  aliasing reference. That is what keeps "a program that compiles here means what it
  means in Go". Devirtualization is the piece still open. See
  `internal/octogo/octogo.go`.

**Generics are a separate category: not supported, not planned, not ruled out.**
A question for after v1 -- whether an LL(1) grammar can describe them at all,
whether they earn their complexity on a microcontroller, and how they meet the
whole-program specialization the compiler already intends. Do not build toward
them, and do not design them out.

**Complex numbers are PLANNED, for release 1.1** (the user's call, 2026-09-24: 1.0
is not rushed, and correctness comes before completeness, so they land after it as
a purely additive change with time to bake). They need no heap, so they are owed,
not excluded. specs.go's "Complex types (planned)" specifies them ahead of the work
-- Go's, a value two floats of its float type, so complex128 is as precise as
float64 is -- and nothing before 1.1 should foreclose any of it.
Until then an imaginary literal (the scanner) and complex64/complex128 where the
program declares no such name (`errUndefined`) say "complex numbers are not
supported yet"; flexcc has no `_Complex`, so the emitter will lower them itself.

**float64 is IEEE double precision, in software** (the user's call, 2026-10-08,
following an outside review; built on a branch 2026-10-24, its costs weighed by the
user and merged the same day, c9eb76c, with obeam's arenas shrunk to keep its VMs
within the SD card's boot limit and the speed of the math functions left for
later: a faster multiplication, which every one of them rests on, and a square
root seeded by the CORDIC). A float64 is `ogo_f64`, a struct of its two words
(`internal/octogo/float64.go`): the arithmetic in words (SoftFloat-2's structure),
the conversions, Go's strconv ported for printing, all emitted only where the C
names the type (f64Section), so a program with no float64 has the C it had. Every
untyped constant meeting a float64 is spelled as the float64 Go rounds it to
(the checker's f64Consts, f64ConstOf), no literal converting to a struct. `math` is
Go's own pure Go source (mathSrc), but Sqrt and the bits functions. The P2 has no
floating-point hardware, so float32 is software already and binary64 costs a
factor more: an addition 860 clocks to a float32's 660, a division 2,900 to 560,
math.Sin 34,000 to the CORDIC-assisted C library's 4,000. Refusing float64 was
declined, since an untyped float constant would then default to float32 and `x :=
0.5` would differ from Go in its type. **A new representation is asked every
workaround its width already has**: each board fault the branch met was one the
emitter routes around for an int64 or a struct, which the two-word float64 had not
been given (a member of a call's result, a 64-bit cast of a 64-bit expression, a
function value's two-word result, an aggregate element). The whole account is in
`doc/LESSONS.md`, "A FLOAT64 OF TWO WORDS".

**A channel is made as Go makes one** (the user's call, 2026-10-08, the review's
first item; done 2026-10-09): `var ch chan T` is nil, `make(chan T)` makes one
(emitMakeChan), its cell static in a package variable's initializer and the
block's in a function, held there by the lifetime rules as a composite literal's
address is (chanMakeRef; a channel counts as a reference, carriesReference and
noteDeclFrameHolder), so a channel a function makes is not returned, kept, sent or
handed to a goroutine -- but for main's outermost block, below. Every cell one make
site makes shares the site's lock, taken first thing in package initialization
(chanSiteInit), so a make costs no lock at run time; the sites take eight locks at
most (chanSiteLocks). A channel variable declared without a value that nothing
writes or addresses and that a channel operation uses is refused as never made
(neverMade). **Buffered channels** (the user's call, 2026-10-09): `make(chan T, n)`
for a constant n declares n slots beside the cell (emitMakeChan, chanInitBufCName),
and the cell carries a ring only for an element some buffered make uses
(chanBufElems), so a program with none is the C it was. The size is the one the
checker computed (chanSizes, by make's second argument), and an element of no bytes
in Go shares one slot (goZeroSized). Two rules the work left: **A migration that
moves every program to a new spelling asks the new spelling every question the old
one was asked** -- the spec tests are that net when they are migrated rather than
rewritten -- and **A value the checker computes is handed to the emitter, not
computed again**: two folds are two answers. The whole story is in
`doc/LESSONS.md`, "The language feature policy's history".

**Main's outermost block outlives every cog** (the user's call, 2026-10-09, the
first step of the channel design): every return from main stops the other cogs
inside main's frame (ogo_end_program) and a panic stops them all or reboots, so a
reference to a variable declared directly in main's body, or to what a statement
there makes, may go to a goroutine, a send or a package variable -- `var buf
[64]byte; go fill(buf[:])` and `ch := make(chan int); go worker(ch)` in main. The emitter's frameRefOf answers no reference for it
(outlivesCogs: main's own body, blockDepthOf <= 1) and the checker's escapesFrame
the same (mainOutlivesCogs, File.mainScope); neither where the program references
its own main (Package.mainReferenced, cached) or main has a goto back
(backwardGoto), which can run a declaration again over storage a goroutine holds. 108 subtests of twelve lifetime
tests had written their refused shape in main and were moved into a `run()` main
calls: **a lifetime test writes its refused shape in a function other than main**.

**Later, from the same review** (recorded at the user's request, not scheduled): a
`go` that reports a cog it could not start instead of panicking; explicit Load/Store operations
beside the defined plain accesses; a `tryAppend` name for the ok form of append; and splitting
specs.go into the rules, the implementation status and the rationale. Done since:
stack slots sized per goroutine (2026-10-17, goslots.go): a go statement that runs
at most once has a slot of its own, sized for its goroutine, where every one of the
program's does; any other program keeps seven slots of the deepest one's size. And
a resource report, `ogo build --report` (2026-10-17, internal/build/report.go): Hub
RAM by its parts, goroutine slots and their needs, locks, data by package variable
and the largest functions, every size the backend's -- the layout off a listing of
the binary's own assembly assembled again (or, where the assembler will not take
it back, of the C compiled with a listing, which the backend optimizes a little
differently and the report says so), each object's size off a `sizeof` table built
into a second compile and read out of its binary (octogo.SizeProbe), the names off
the emitter (octogo.WithReport). And a check at the entry of each function on a
recursion (2026-10-18, stackcheck.go), where it may run: main's cog and a goroutine
of a --gostack slot panic "goroutine stack overflow" before a recursion writes past
the end of the stack, what the panic needs kept free; the margins are the listing's,
in rounds of compiles. A recursion through a function value is seen by what the
value's type may hold (funcclasses.go): the build spells each such call
`OGO_FV(type, callee)` and the C carries a table of each type's functions, two
defined types being two types to the checker since the same day -- and a stack's
size by the same table since 2026-10-21 (the user's call): it names what a value
IS, a method value's lifted wrapper and a several-result function's among them
(fvHeld), and a pointer to one function type read as a pointer to another, through
unsafe.Pointer or converted, opens the signature (noteFuncValuePun).

**A program ends as Go's does, every cog with it** (the user's call, 2026-10-07): a
debug build's panic stops every other cog and then its own (panicHaltC), and a return
from main stops every other cog (ogo_end_program, written where main returns in a
program that starts a cog -- a goroutine or a Spin2 object, whose driver may -- and
nowhere else, so no other program's C changed; on the host it `_Exit`s). A program
calling its own main ends only in the run of main it began with (ogo_main_top). A
stopped cog releases its pins and holds its smart pins in reset, so an ended program
leaves the pins as a reset does. Inside a panic the order is the message, then the
other cogs stopped, then a ten-millisecond drain before the cog stops ITSELF (a
stopped cog resets its smart pins, the line's among them; no drain on the host) --
not the others before the message, the target's stdio taking a lock (__lockio) a
stopped cog could hold. ogo_panicking, set first thing in every panic, makes main's
return wait for a panic under way on another cog. TestOnBoardProgramEnd reads past
the expected text and asks that nothing followed, and `ogo test` stops reading at a
panic's line (loadp2.Capture). **A change to how a program ends is asked which cog
wins a race between the end and a panic.**

**recover is PLANNED, for after v1** (the user's call, 2026-10-07), and with it
the deferred calls a panic runs on its way out. Neither needs a heap -- a record of
each frame with deferred calls, set up on entry, which a panic unwinds to -- so they
are owed, not excluded; but they touch the defer lowering, the goroutine
trampolines, the inlined copies and the stack sizing, cost every call of a function
that defers, and rest on setjmp/longjmp on the target -- measured since 2026-10-08,
when testing's FailNow was built on them: the target's longjmp restores the frame
pointer, the hub stack and the frames' registers and NOT the cog's eight-level
hardware return stack, where a frameless function keeps its return address while it
calls, so a stale address survives the jump and a later return takes it (main's
returned into a test long finished). run saves all eight levels after its setjmp and
restores them where the longjmp lands (testingHWStackC); recover needs the same, at
every frame it can land in. Until then
recover is "not supported yet" and a panic runs no deferred call. Nothing before
then should foreclose it; the one thing that would pull the deferred calls forward
is firmware wanting a `defer` to put a pin safe on a panic.

`specs.go`'s "Relationship to Go" section states the same policy for language
users; keep the two in step.

## Module path vs. directory (important)

- Repo directory on disk: `modernc.org/ogo`
- `go.mod` module path: `modernc.org/ogo`
- Installed binary / CLI command: `ogo` (`go install modernc.org/ogo@latest`)
- Internal packages import as `modernc.org/ogo/internal/...`

The package name inside `internal/octogo` is `octogo` while the module is `ogo`;
that mismatch is deliberate and is the only one left. The stale `// import "..."`
comments that used to contradict all of this have been removed.

The canonical repository is GitLab (`cznic/ogo`); GitHub (`modernc-org/ogo`) is a
mirror that accepts issues/PRs which are then manually merged upstream.

## Commands

```sh
go build ./...              # build everything
go install                 # build + install the `ogo` CLI onto PATH
go test ./...              # fast test of all packages
make test                  # full suite: gofmt, go install, `ogo fmt`, then go test -timeout 24h -count=1 -failfast ./...
make -C internal/octogo race   # go test -race + golint + staticcheck for the compiler core
gofmt -l -s -w .           # canonical Go formatting (CI enforces this)
```

Running one semantic-check spec test (files live in `internal/octogo/testdata/*.ogo`):

```sh
go test ./internal/octogo/ -run 'TestOctoGoSpecs/05_scope_shadowing.ogo' -v
go test ./internal/octogo/ -re '05_' -run TestOctoGoSpecs -v   # -re is a custom flag filtering which .ogo files run
```

CLI subcommands (`ogo <cmd>`): `build`, `run`, `test`, `fmt`, `loadp2`, `smith`,
`help` and `version` all work. There is no stub left.

```sh
ogo fmt -l -w --exclude='\/testdata\/' .   # gofmt-style reformat of .ogo sources in place
ogo smith -seed 12345                      # emit a random OctoGo program to stdout (compiler fuzzer)
ogo smith -seed 12345 -o DIR               # the same into DIR, with a package of its own (pkx) where drawn
ogo build --clock 200MHz .                 # ask for a system clock; default is 160 MHz
ogo build --no-inline .                    # leave inlining to the C backend, as before 2026-09-29
ogo build --report .                       # what the binary takes: Hub RAM, stacks, locks, data, code
```

The run cases -- `emitRunCases` in `internal/octogo/run_test.go`, the programs most
changes are measured by -- run three ways. A subtest is the case's name with its
spaces as `_`, and the pattern is a regexp, so `(`, `)`, `.` and `*` are escaped:

```sh
go test ./internal/octogo -run 'TestEmitCRun/<name>'      # host: gcc and the testdata/hostp2 pthread shim
go test ./internal/octogo -run 'TestTargetBuild/<name>'   # built by the real backend, no board needed
flock -o -w 900 /tmp/p2-board.lock make board             # every case on a P2 (OGO_BOARD_PORT, default /dev/ttyUSB0)
OGO_BOARD_PORT=/dev/ttyUSB0 flock -o -w 900 /tmp/p2-board.lock go test -v ./internal/octogo -run 'TestOnBoard/<name>'
```

## The board

**The clock is a BUILD-time choice and 160 MHz is the default**, which surprises
people (the `ogo run` help claimed 200 MHz until 2026-08-12 and was simply wrong).
flexcc falls back to 160 MHz -- mode `0x010007fb`, a 20 MHz crystal times eight --
whenever the program declares no `_clkfreq`/`_clkmode` pair, and it reads those two
by name from the program's own constants (`GetClkFreq` in the transpiled sources).
`--clock` computes the PLL divisors and emits that pair; `internal/octogo/clock.go`
holds the arithmetic, and its test pins the 160 MHz word against the backend's own
fallback so the encoding cannot drift from the compiler that consumes it. A program
asks what it was built for with `p2.ClockFreq()` (2026-09-28), the backend's
`_clockfreq` -- a FUNCTION and not a constant, the clock being an option of the
emitter's that the checker folding constants has not seen; measured on a P2-EDGE at
the default and at `--clock` 200, 180 and 100 MHz.

**loadp2's `-f` does NOT set the program's clock**, whatever the name suggests: a
flexcc program sets its own as it starts, and the same binary measures 160061416 Hz
with `-f 200000000` or with no `-f`. `-f` is for the loader's own timing.

**`Could not find a P2 on port` can mean the board, not the loader** (2026-09-17, first
session on the second dev machine). This P2-EDGE's flash holds TAQOZ Reloaded, which
boots ~218 ms after any reset that no loader handshake follows and talks at 921600 baud,
so garbage at other bauds after a reset is TAQOZ, not noise. The ROM answers the loader's
`Prop_Chk` for ~130 ms after a reset and the loader's first try lands at ~24 ms, so a
healthy board is found on the first try; the reset is the DTR deassert edge, at any pulse
width from 1 ms. When every load fails while `ogo loadp2 -xTERM -b 921600 -p /dev/ttyUSB0`
plus Ctrl-C still prints the TAQOZ banner, the chip is alive and the DTR-to-RESn path is
dead: it degraded over hours and only a board power cycle restored it (24/24 loads and a
green `make board` afterwards). The transpiled loader measured identical to a native
loadp2 and to a hand-written replica throughout -- do not start there.

**A load lost to "Error writing port" or a timeout was the loader's until
modernc.org/loadp2 v0.3.2** (2026-10-19): loading in a Go process, it took a write
the runtime's preemption signals cut short for a failure and a select they
interrupted for a timeout (obeam lost 8 loads of 10 on macOS). The board tests'
"transient serial flake" retries were 5 to 26 a `make board` before it and 2 in the
first run after; a retry that remains is not this until a load under signals says so
(the harness: SIGURG to every thread every 100 µs, the loader's CLAUDE.md record).

## Code generation

Four generated artifacts are checked in. Regenerate only when changing their
inputs, and never hand-edit the outputs.

1. **Grammar → parser.** `internal/octogo/parser.go` (marked `DO NOT EDIT`) is
   produced by [`modernc.org/egg/v2`](https://gitlab.com/cznic/egg) from
   `internal/octogo/octogo.ebnf`, which is in turn *extracted from the package
   doc comment of `specs.go`* (root). The grammar is authored as `//\t`-prefixed,
   ` .`-terminated EBNF lines inside that doc comment. **To change the language
   syntax, edit `specs.go`'s doc comment**, then:
   ```sh
   go install modernc.org/egg/v2@latest   # v2.0.1 and v1.4.1 emit the same parser; v1.2.3 does not
   make -C internal/octogo parser.go   # extract_grammar.go -> octogo.ebnf -> egg -> parser.go (+ sed cleanup)
   ```
   Note: the grammar is intentionally looser than the language — it accepts more
   than is legal, and the semantic checker narrows it down.

2. **Stringers.** `internal/octogo/stringer.go` (`stringer -type Kind,ScopeKind,gate`),
   regenerated by `make -C internal/octogo editor`.

3. **flexcc backend.** `internal/flexcc/ccgo_<goos>_<goarch>.go` (~12 MB, ~455k
   lines each) is the flexspin/flexcc C compiler transpiled to Go by
   `modernc.org/ccgo`. Five targets are committed — `ccgo_linux_amd64.go`,
   `ccgo_linux_arm64.go`, `ccgo_windows_amd64.go`, `ccgo_darwin_arm64.go` and
   `ccgo_darwin_amd64.go` — plus `ccgo.go` and `ccgo_g_*.go`, the shared decls
   `undup` (`modernc.org/undup`) folds out of them under shared build tags. The
   same-ABI LP64 linux amd64+arm64 pair now shares a lot (like loadp2's ~2x),
   while the LLP64 windows and the darwin transpiles share little cross-ABI. Do not
   hand-edit any of them. `internal/generator.go`
   (build-tagged `//go:build
   ignore`) drives both: it clones `totalspectrum/flexprop` (the wrapper pinned to
   tag **`v7.7.0`** via `flexpropRef`, and inside it the `spin2cpp` submodule — the
   compiler itself — checked out at **`spin2cppRef`**, a commit past the tag since
   2026-08-29, because a fix lands in spin2cpp weeks before a flexprop release
   carries it), applies `internal/mcpp_main.c.diff` (an adaptation to the
   transpile; a fix carried ahead of upstream goes in the same list --
   `optimize_ir.c.diff`, `functions.c.diff` and `outasm.c.diff` today -- and goes
   with the pin that carries upstream's), transpiles, and rewrites the emitted
   `main` package into a reusable `flexcc`
   library (threading a `*CC` state struct through the C globals, via `main2lib`).
   The linux backend is transpiled natively (`ccgo -exec make`, `transpileLinux`);
   the windows backend is cross-compiled on a linux/amd64 host with MinGW
   (`transpileWindows`: a native `make` to produce the bison/xxd-generated C sources,
   then a direct `ccgo --goos windows --goarch amd64 --cpp x86_64-w64-mingw32-gcc`
   pass over the explicit flexcc source list). The two darwin backends are generated
   natively on a darwin host (`transpileDarwin`, same native-make-then-direct-ccgo
   shape as windows but with `--cpp clang`): darwin/arm64 directly, darwin/amd64 on
   an arm64 mac under Rosetta 2 (the amd64 go+ccgo toolchain via `arch -x86_64`; the
   generator uses the homebrew `gmake`/`gsed` since macOS ships BSD make/sed).
   transpileDarwin shadows `<mach-o/dyld.h>` with a one-symbol shim (its real form
   drags in `<mach/message.h>`, which `modernc.org/cc` cannot size) and passes
   `-D_FORTIFY_SOURCE=0` (macOS defaults it to 2, emitting `__builtin___*_chk`
   fortify calls ccgo cannot resolve). The linux run also emits two sibling
   artifacts that keep the in-repo compiler self-contained (the windows run reuses
   them, being target-independent): `internal/flexcc/p2include.tar.gz` — the installed
   flexprop P2 include/lib tree (headers, libc sources, `libc.a`) packed as a
   deterministic gzip'd tar, `go:embed`ed by `flexcc/p2include.go` and extracted at
   runtime so `flexcc.Main` needs no external flexprop install — and
   `internal/flexcc/LICENSE-flexprop` for attribution. Regeneration is `cd internal &&
   go generate` (or `go run generator.go`) on a linux host of the target
   architecture — linux/amd64 or linux/arm64, native either way (`ccgo -exec make`,
   no ccgo CLI needed); `cd internal && TARGET_GOOS=windows TARGET_GOARCH=amd64
   go run generator.go` (windows, needs `x86_64-w64-mingw32-gcc` and the `ccgo` CLI
   on PATH); or `cd internal && go run generator.go` on a darwin host for
   darwin/arm64 and `arch -x86_64 <amd64-go> run generator.go` (with an amd64 `ccgo`
   on PATH) for darwin/amd64 — heavy and network-dependent; to adopt a changed pin,
   `rm -rf internal/flexprop internal/flexprop_install` first so it is re-cloned. A
   clone the generator finds is used only at the pins with exactly its diffs applied
   (`checkClone`); any other is refused with that command.
   Generating a second target after a first (without resetting between) accumulates
   both into the fold; e.g. run darwin/amd64 after darwin/arm64 on the mac.
   The generator handles the `undup` fold itself so the steps can't be run out of
   order: it `undup.Expand`s the prior fold to full per-target files, regenerates this
   target, `gofmt -s`s so the shared decls are byte-canonical across targets, then
   `undup.Dedup`s to re-fold (`modernc.org/undup/lib`, pinned via go.mod's `tool
   modernc.org/undup` directive so `go mod tidy` keeps it despite the import living
   only in the `//go:build ignore` generator). No manual expand/gofmt/dedup needed.
   The windows backend also needs two hand-written companions:
   `internal/flexcc/supplement_windows_amd64.go` (the CRT/Win32 functions
   `modernc.org/libc` lacks or stubs for windows) and `freopen_notwindows.go` (the
   linux/other-unix counterpart, tagged `!windows && !darwin`); see the windows
   record of 2026-07-22 in `doc/REGEN-HISTORY.md`. Darwin has its own `internal/flexcc/supplement_darwin.go` (the libc
   functions — `stpcpy`, `wcrtomb`, `asctime`, `fseeko`/`ftello`, `powl`/`frexpl`,
   `freopen`, and the `ungetc`/`abort` todo-stub redirects — that libc lacks or
   stubs for both darwin arches).

   > **Backend regenerated 2026-10-25 at the same pin, spin2cpp `b5993a84`, with a third
   > fix of its own** (inside flexprop `v7.7.0`; ccgo v4.36.1, libc v1.77.1):
   > `internal/outasm.c.diff`'s second hunk, SplitLocalStructs, for a cost, not a fault. A
   > local of several longs held in registers -- a struct, a slice or string header, a
   > 64-bit value -- is a base register with a subregister a long past the first, and
   > RenameSubregs keeps its longs in consecutive registers; ReuseLocalRegisters gives no
   > subregister back, so every such local held its registers to the end of its function,
   > and every slice a function passed to a call, a temporary of its own, held three. A
   > Go-shaped main outgrew the cog's 480: dom63's and dom65's had to be split
   > (doc/complit-arg-registers.c, the limit's reproducer since 2026-09-25, 210
   > registers). The hunk splits such a local, before the function's first
   > optimization, into one local a long wherever nothing reads its longs as a block --
   > its register's address taken for an indexed access (an IMM_COG_LABEL of its name),
   > inline assembly, an instruction after a SETQ -- and ReuseLocalRegisters then treats
   > them as it treats an int's. Measured natively before adoption, b5993a84 with the
   > three carried diffs against the same with the hunk: forty slice arguments of every
   > spelling took 120 registers and take none, their longs moved straight into the
   > argument registers (build/probes/regs); the reproducer builds in 21, dom65's unsplit
   > main takes 9 for 117. spin2cpp's `make test_offline`: 15 of its asm goldens changed,
   > each read -- nine only reserve no register for a local the optimizer had already
   > removed, six shorter and right, two of them making functions identical that the
   > backend then merges -- and 552 of 552 pass on the updated goldens. Of 1503 programs
   > (the run cases, seeds 1-400, doc/) 1165 build differently, 44 that did not build now
   > do, and on a P2-EDGE every one prints what the old backend's build printed (the run
   > cases and seeds held to the host's run of the same C, the reproducers to the old
   > backend's own run; build/probes/s2c/boardcmp.py): a test's elapsed time aside, the
   > held faults' lines read registers nothing wrote both ways, and two loads lost to the
   > loader matched rerun. doc/register-limit-crash.c, the backend's crash past its
   > register limit, no longer reached the limit in its shape of thirty struct locals; it
   > is a hundred local arrays indexed in place now, which the hunk leaves together, and
   > crashes both ways. Each builder's turn changed its own target only (the fold
   > expanded and hashed after each), and the five scripts/flexcc lists are the native
   > build's but for doc/switch-case-span-overflow.c, which the linux transpiles' qsort
   > never finishes, before the change as after. Programs come out smaller where they fit
   > before -- p2-11's 390 KB main 2.4 KB, obeam's checked VM 3.8 KB -- but a marked
   > function now small enough to inline is inlined: obeam's unchecked VM, its arena sized
   > to the room its checks leave, outgrew Hub RAM by 3 KB with its marks, so a build
   > outgrowing the hub is built plainer as one outgrowing the cog is (compileMarked,
   > outgrewHub), and it builds unmarked 3.4 KB smaller than before. p2-11's 72 tests
   > pass on the board; host suite and `make board` (1031 passing, seed 6 outgrowing a cog as before) green.
   >
   > Every regeneration's record, this one included, is in `doc/REGEN-HISTORY.md`,
   > newest first; the next one goes there and replaces the entry above. What still
   > stands from those records: a regeneration is measured natively before it is
   > adopted (upstream's `make test_offline`; the run cases, fuzzer seeds and `doc/`
   > through `scripts/cccorpus.sh` against the old pin; every program that builds
   > differently run on a P2-EDGE), each builder's turn is checked to change its own
   > target only (the undup fold expanded and hashed after each), and the five
   > platforms' `scripts/flexcc` must build the corpus to a native build's cccorpus
   > list. Every emitter workaround stays until a regeneration measures its fault
   > gone -- most of them cost nothing and stay even then -- so the `doc/`
   > reproducers are re-run each time. Afterwards `rm -rf internal/flexprop
   > internal/flexprop_install`: git-ignored clones that otherwise break `go build
   > ./...` and `go test ./...`. The flexcc `--help` golden needs no refresh
   > (`versionLineRE`). The windows pass's `main2lib` fixups and the
   > `supplement_windows_amd64.go` redirects are explained in the 2026-07-22 record.
   > Base flexspin hangs at nine nested calls through functions without a frame
   > (flexprop#118; not worked around, no generated program known to reach it). And
   > `mcpp_main.c.diff` removes mcpp's setjmp, so any preprocessor fatal error is
   > libc's "unsupported setjmp/longjmp usage" panic; libc v1.77.1's longjmp model
   > makes keeping the setjmp worth a try at a regeneration.

4. **C library names.** `internal/octogo/cnames.go` (marked `DO NOT EDIT`) lists
   every macro and every file-scope name the target's C library speaks for, and the
   host's, which the emitter renames when a program uses one (`cUnusable` for a
   macro and for a TYPE, in every position -- the target's compiler cannot parse a
   declarator named like a typedef in scope, where gcc lets a local shadow one, so
   a local `FILE` passed the host and failed the board; `cReserved` for the rest,
   at top level). The main
   package's symbols keep their source names in the C, so a program naming a
   function `read`, `close`, `clock` or `sleep`, a type `FILE` or a field `EOF`
   collided with the library: flexcc refuses some of these inside the library's own
   source (`posixio.c: redefining function or subroutine close`), only warns about
   others, takes the rest in silence, and a macro turns a field's declaration into
   `_Bool (-1);`. The hand-written lists covered what a program seemed likely to
   name until 2026-09-25. `TestCNames` derives the target's part from the embedded
   `p2include.tar.gz` -- the headers the emitter includes, read from `emit.go`, and
   every `libc/` and `libsys/` source, preprocessed with gcc under flexcc's
   predefined macros -- and fails when the tree names something the lists lack, so a
   backend regeneration that adds a library function fails the suite until this is
   rerun **with the new pin's spin2cpp checkout**, whose system modules (`sys/*.spin`,
   `_tx`, `bytefill`, ...) are compiled into every program and are in no include tree:
   ```sh
   go test ./internal/octogo -run TestCNames -args -cnames-update -cnames-spin2cpp <spin2cpp checkout>
   ```
   Names are only ever added, so an update on another host or without the checkout
   keeps what an earlier one found. `TestFileScopeNames` pins the scanner: static
   declarations are left out (a user function named like posixio.c's static
   `_txputc` builds), and an old-style definition's parameter declarations name
   nothing (`wcsncat(dst, src, n) wchar_t *dst; ...` put `dst` and `n` on the list
   until it knew them).

## Architecture

`main.go` is a thin CLI dispatcher (arg parsing via `modernc.org/opt`) that routes
subcommands to `internal/*` packages.

- **`internal/octogo`** — the compiler core. `Build()` in `build.go` orchestrates a
  concurrent, multi-phase pipeline over a package's `.ogo` files, coordinated by a
  `BuildContext` that resolves imports and detects import cycles (namespaces are
  implicit from directory name; there is no `package` keyword). The intended
  phase structure — parallel local-scope population, serial package-scope merge,
  serial top-level type/const evaluation (with a `gate` state machine for cycle
  detection), parallel body checking of hardware constraints, then a serial deep
  init-cycle pass — is documented in detail in the package doc comment of
  `octogo.go`, which also specifies the later Whole-Program-Optimization /
  monomorphization / devirtualization design.

  The **AST is a flat `[]int32` slice** (zero pointers, cache-local), not a tree
  of node structs. Traverse it with the `it(ast []int32) iter.Seq[Node]` iterator
  (Go 1.23+ range-over-func); each `Node` carries a `sym Symbol` for non-terminals
  or a `tok` for terminals. Tokens are named `TOK_<keyword>` or `TOK_<hex-of-rune>`
  (e.g. `TOK_003b` is `;`); readable aliases (`ARROW`, `SEMICOLON`, `IDENT`, …)
  are defined at the top of `scanner.go`. Key files: `scanner.go` (lexer with
  Go-style automatic semicolon insertion), `check.go` (semantic analysis, the
  largest file), `decl.go`/`type.go`/`value.go`/`const.go` (declarations, the type
  and value model), `format.go` (the `.ogo` pretty-printer behind `ogo fmt`) with
  `format_table.go` (its alignment: gofmt's tabwriter, emulated) and
  `format_doc.go` (a doc comment as go/printer's formatDocComment writes it).

- **`internal/format`** — thin `ogo fmt` subcommand wrapper: walks paths, runs
  files through `octogo.FormatFile` concurrently, handles `-l`/`-w`/`-exclude`.

- **`internal/smith`** (package `octosmith`) — the `ogo smith` fuzzer. It is an
  *oracle fuzzer*: it interprets the program as it generates it, so it knows the
  expected final state and emits a self-checking checksum assertion into the
  output. A compiled binary that fails the assertion implicates the compiler/backend
  with zero false positives. `gemini.go` drives type-directed generation over the
  LL(1) grammar; `vm.go`/`vmapi.go`/`env.go`/`types.go` are the generation-time VM
  (scope, memory, arithmetic). Design notes in `OctoSmith Fuzzer Architecture Document.md`.

- **`internal/flexcc`** — the C backend as a library. `flexcc.go`'s `Main()` runs
  the transpiled compiler in-process over a `libc.TLS`, capturing stdout/stderr
  into Go writers. Wired into `ogo build` through `internal/build`, which is how
  every board binary is produced; `all_test.go`'s `--help` golden exercises it
  standalone too.

Data flow: `.ogo` sources → scanner/parser (`internal/octogo`) → flat AST →
semantic checks → emitted C → `internal/flexcc` → P2 binary → `loadp2` →
board. That whole path runs today (`ogo build`, `ogo run`); WPO is the one stage
still design-only.

## Implementation status (where the TODOs are)

The current state, condensed. The full text it was condensed from, with what each
step found on the way, is `doc/LESSONS.md`'s "Implementation status, as CLAUDE.md
had it on 2026-10-13"; the sections named in capitals below are its journal's.

- `Build()` runs phases 1–3 fully, and phase 4 carries the full statement
  type-checker (`checkBlock` and everything under it). Phase 5 reports
  value-recursive (infinite-size) types; global initialization-order cycles are
  refused by the EMITTER's ordering pass (`internal/octogo/initorder.go`, since
  2026-09-04), which also gives Go's dependency ORDER through function and method
  bodies -- so neither is an open gap, they just live where names are resolved.
  The hardware-side refusals (escape across cogs, the ABI walls, frame lifetime)
  likewise live in emitter passes. WPO is design-only. C emission is **not** a
  stub -- `internal/octogo/emit.go` is the largest single piece of the compiler
  and is wired through `internal/build` to flexcc and loadp2.
- `TestOctoGoSpecs` (`internal/octogo/tests_test.go`) runs every `*.ogo` file in
  `internal/octogo/testdata` — there is no skip list (the historical one was
  retired as the checker caught up). Each file is annotated `// COMPILE` or
  `// ERROR <regexp>`; all currently pass, but the checker is still partial, so a
  green spec is not proof a whole feature is finished — the testdata covers only
  what has been wired up.
- `ogo test` runs a package's `*_test.ogo` tests **on the board** and nowhere else
  (a host mode was considered and rejected -- see `internal/build/test.go`): it
  builds them with a generated runner, loads the result and reads the verdict back
  over the serial line, the P2 returning no exit status. `-c` builds without
  running, which is what CI with no board can honestly do. Its flags are go test's:
  `-run` and `-bench` (selecting subtests and sub-benchmarks as go test's match.go
  does: splitTestPattern and TestFilter in `internal/octogo/testfilter.go`, the
  levels below the test matched on the board by a Pike machine the runner hands
  in), `-benchtime`, `-benchmem`, `-timeout`.
  The `testing` package is EMBEDDED SOURCE, not an intrinsic: `embeddedPkgs` in
  `internal/octogo/build.go` maps the import path to ordinary OctoGo, compiled and
  mangled like any other package. T has Log, Logf, Error, Errorf, Fatal, Fatalf,
  Skip, Skipf, FailNow, SkipNow, Name, Helper and Run; B the same messages, b.N's
  ramp, Loop, Run, ns/op computed in integers, MB/s and metrics. The formatting
  methods have no body and are written at each call (emitTestingCall, go test's
  "    file:line: " decoration), their arguments checked as printf's are; `run`, the
  package's other bodiless function, is written behind its own header
  (emitTestingIntrinsic), and FailNow and SkipNow longjmp to it, which saves and
  restores the cog's hardware return stack (testingHWStackC; see recover above). A
  subtest is named and numbered as go test names it (Go 1.27's unique, every rune
  strconv.IsPrint says does not print escaped). Output, names, counts and
  benchmark lines -- and since 2026-10-15 each result's time, `PASS` and the
  package's line by import path with the run's time (testTimeLine, TestTestTimes)
  -- are held byte for byte to go test -v's on the host
  (TestFilterMatchesGoTest, TestSubtestNamesMatchGoTest, TestD,
  TestBenchmarkFakeClock under the shim's OGO_FAKE_CLOCK) and on the board
  (TestOnBoardOgoTest, ...Filter, ...Bench, ...Names). Not yet: Cleanup,
  RunParallel, Elapsed, a deferred or started t.Log (refused), the deferred calls
  FailNow skips, Helper's attribution of a line to the caller. A table-driven test
  or benchmark cannot capture its case in a literal (no closures); it goes through
  a package variable.
  The rules it left: **A library function a goroutine may call does not recurse**.
  **A runtime shared by every program of a kind is held to its old register
  count**: p2-11's tests build near the cog's limit and are the measure, and
  TestBuildTestingFrugal reads the listing. **What only some runs use is handed in
  by the code those runs generate** (`testing.SetFilter`, `testing.SetSubtests`):
  flexcc keeps every function whose address is taken anywhere, and every global
  such a function touches. **What a library adds for one kind of program is
  measured in a program of the other kind, by its binary.** **An oracle comparison
  is asked to fail before it is believed**, and **the oracle's source is read, not
  remembered** (`$(go env GOROOT)/src/testing/match.go`).
- **Interfaces are done and devirtualization is not.** A method call through an
  interface is an indirect call through a static vtable, always; the WPO pass that
  would make it direct where the concrete type is provable is design-only. Nothing
  is rejected for failing to prove one -- rejection is spent on lifetime. A case or
  an assertion naming an INTERFACE tests every type of the program that implements
  both (implementsIface), compared by C signature (methodHasSig). **A question the
  checker answers for an assignment is asked again wherever the emitter enumerates
  on its own.** An interface written out, `interface{ Unwrap() error }`, is a type
  named by its spelling (ifaceLitName; AN INTERFACE WRITTEN OUT IS A TYPE).
- **Method values bind their receiver at compile time**, which is why they cost
  nothing that other function values pay. Go's representation (a value pointing at a
  struct whose first word is the code pointer) was measured on hardware and declined:
  `doc/funcval-cost.c` has the numbers, the attribution and how to revisit it. Do not
  re-open that without re-reading it. The binding is an ADDRESS, so a receiver Go
  would SAVE is refused: a value receiver (Go copies it), a local (its address dies),
  and since 2026-09-23 a POINTER -- a pointer variable or an embedded pointer the
  method is promoted through (`methodValueSavesPtr`, checker and emitter): a pointer
  variable's bound the pointer's own address and printed garbage on the board, and an
  embedded one was read at each call rather than when the value was taken. A function
  literal calling the method is the spelling that reads the pointer at each call.
  A receiver reached from a package-level variable through fields of struct values
  and constant indexes into arrays, `dev.Reg.get`, `rs[2].get`, is bound the same
  way since 2026-10-16, its address being fixed (methodValueRecv; the checker's
  reportChainMethodValue and chainRecvUnfixed refuse, each by name, a pointer on
  the way, a slice element and a variable index, which Go reads when the value is
  taken).
- Composite literals cover positional and keyed structs (`P{1, 2}`, `P{x: 1}`),
  positional array/slice literals (`[3]int{1, 2, 3}`, `[]int{1, 2, 3}`), and
  indexed array/slice literals (`[]int{2: 5}`, `[5]int{0: 1, 4: 9}`, mixed
  `[]int{1, 4: 9}`) with constant indices -- expanded to positional C initializers
  (gaps zero-filled), a slice's length being the highest index plus one. A
  non-constant index is refused.
- Multi-package programs work, and **`ogo.mod` says where import paths are read
  from** (2026-08-20). A one-line `module example.com/proj` marks the root;
  `moduleContext` (`internal/build/module.go`) searches upward from the package
  being built for the nearest one and hands `octogo.BuildModule` the module path,
  the package's directory within the root, and `os.DirFS(<root>)`, and an import is
  written as Go writes it, prefix and all. It is deliberately **not** go.mod:
  searching for one would find *this repo's* go.mod from `_examples/` and adopt
  `modernc.org/ogo` as the root. **With no ogo.mod** the directory being BUILT is
  the root and paths are relative to it (`import "sensor"`), which is what every
  test, the fuzzer and every `_examples/` program does. A package's identity inside
  the compiler is its **module-relative directory**, not the path written to import
  it (module paths carry dots, which `cIdent` would hex-escape into C symbols), and
  `importGraph`/`importTasks` are keyed by it -- keyed by the written path, an import
  cycle **deadlocks** instead of being reported. Importing the main package is
  refused (`mainDir`). The whole program is emitted into **one C translation unit**
  in dependency order, top-level symbols mangled into their package's namespace.
  `import "p2"` is the one dotless, directory-less import, of the hardware
  intrinsics; `unsafe`, `testing`, `strings`, `bytes`, `math` and `errors` are
  likewise bare, module or not. There is no standard library beyond those.
- **The `errors` package** (2026-10-09, `errorsSrc` in build.go, the compiler's part
  in `internal/octogo/errorspkg.go`): Go's but for Join, held to Go by
  TestErrorsMatchesGo and TestOnBoardErrors. `New` has no body and is written at
  the call (errorsNewC) as the address of an errorString made where the call stands
  -- static in a package variable's initializer (the sentinel), two temporaries of
  the block in a function, which the lifetime rules treat as `&T{}`
  (errorsNewRef). `asTarget`, As's assignability test, is minted after the last body
  (mintErrorsAsTarget) and its summary seeded (seedErrorsAsTarget), which is what
  refuses `errors.As(localErr, &gp)`. Is and As walk iteratively, an explicit stack:
  written recursively, every goroutine calling errors.Is was refused for want of a
  stack bound.
- **Functions implemented in Spin2** (2026-09-30, `internal/octogo/spin2.go`) are
  OctoGo's .s files, asked for by p2-11's VGA console. A package may carry
  `.spin2` files; a function declared without a body binds to the PUB method of
  its name, whatever the case, of one of them (`bindSpin2`, `scanSpin2`), the
  parameter and result counts checked. The emitter declares one instance per
  object, `struct __using("<path from the build root>")` under `__FLEXC__` and an
  `#error` for the host (`emitSpin2Objects`), and a bound function forwards to the
  method (`emitSpin2Func`); what crosses is an integer of 32 bits or fewer, a bool
  or a pointer (`spin2Crosses`). The method is out of sight and taken to keep what
  it is handed (`seedSpin2Cross`) -- but under Go's `//go:noescape` (the user's
  call, 2026-10-16, the review's "contracts" item), which says it keeps nothing, so
  a local buffer may go to a checksum in PASM: the directive is read off the `func`
  token's separator (`noescapeOf`) and refused anywhere else in gc's words
  (`checkDirectives`, `testdata/noescape_directive.ogo`). `ogo build` and `ogo
  test` pass the build's root to the backend as `-I`. Tests: TestScanSpin2,
  TestSpin2Binding, TestSpin2Emit, TestSpin2Refusals (host), TestBuildSpin2 and
  TestTestSpin2 (the backend), TestOnBoardSpin2. A new way a value reaches the frame
  is a row of the lifetime matrices, and so is a new way to name a callee.
- **`unsafe.Pointer`** (2026-10-01, asked for by p2-11): `unsafe` is an embedded,
  intrinsic package (`unsafeSrc`) whose `Pointer` is the Kind
  `PredeclaredUnsafePointer` (`unsafePointerDecl`), mixing with nothing but itself
  and nil (`catUnsafePointer`), and `void*` in C (`cUnsafePtr`). Its conversions
  are checked (`checkUnsafeConversion`, `checkPtrConvOperand`); `uintptr(x)` of
  what reaches the frame is refused and a summary keeps whatever x reaches
  (`uintptrConvOperands`), a number going where no rule follows it; a store through
  a conversion binds the converted pointer first (`bindPtrConv`). `Add` and `Slice`
  since 2026-10-08 (`checkUnsafeAddSlice`, `emitUnsafeAddSlice`). `Sizeof`,
  `Alignof` and `Offsetof` (`unsafelayout.go`) are constants computed from the
  TARGET's layout (`targetLayout`; the emitter's copy is `cLayout`): spin2cpp lays
  a struct out twice, and the passes part on a zero-sized member, which is declared
  over `uint8_t` where it would move (`zeroSizedDecl`). Each Sizeof of a type the
  emitter names adds `sizeof(T) == v` to `ogo_layout_check` under `__FLEXC__`, so
  a wrong model fails the build. A conversion to a pointer to a type written out,
  `(*[16]uint32)(unsafe.Pointer(a))`, works (`starLitCAt`, `sliceArrayPtrC`), so
  does one to a pointer of any other shape a parenthesised type has, `(**T)(p)`,
  `(*chan T)(p)`, `(*error)(p)` (`pointerTypeAST`, 2026-10-21) but `*struct{...}`
  and `*func(...)`, which do not parse in an expression, and so do a case and an
  assertion of a pointer to a predeclared, written-out or
  defined array type, `case *int:`, `case *[3]int:`. Not yet: a type DEFINED over
  unsafe.Pointer (an alias is taken), `String`, `StringData`, `SliceData`, printf
  verbs but `%v` and `%T`, and a parenthesised qualified conversion `(lib.T)(x)`.
- **Two test suites.** `TestEmitCRun` builds each program in the `emitRunCases`
  table with the host C compiler and runs it against a pthread shim
  (`testdata/hostp2`). `TestOnBoard` builds the *same* table with the real
  backend and runs it on a real P2, gated on `OGO_BOARD_PORT` (`make board`).
  The second exists because flexcc and gcc have been observed to disagree on
  semantics, not just warnings -- a host-green emit feature is not verified.
  `make board` builds checked; the table was first run on the board built
  `--unchecked` on 2026-10-04 (a scratchpad tool taking the board lock per case):
  all 754 cases that do not panic printed what they print checked.
- **A backend diagnostic fails the target-build tests, even when `ogo build`
  succeeds.** flexcc warns where it should refuse -- given a duplicate declaration
  in one block it says `Redefining x` and builds a binary holding the wrong value --
  so `boardBuild` treats any output from a *successful* build as a failure, which
  puts the check in `TestTargetBuild`: no board needed, in the default `go test
  ./...`. A clean build is silent. A diagnostic examined and found harmless goes in
  the case's `backendWarning` field with the reason; there are none. `ogo build`
  holds a USER's build to the same rule (backendFault in internal/build): a backend
  warning about the emitted C fails the build, the C kept for a report,
  `--allow-backend-warnings` the way round it; a warning about the program's own
  `.spin2` object passes, and so do those in `harmlessWarnings`. A refused build
  keeps its C too (backendRefusal), and "final output size ... exceeds maximum of
  524288" is the PROGRAM's size, not a fault of ogo's (hubOverflow). Every test
  building for the target also reads the listing for the backend's heap
  (`heapFree` in board_test.go; A STRUCT THE BACKEND DOES NOT RETURN IN REGISTERS IT
  RETURNS ON A HEAP).
- `smith` (`internal/smith`, package `octosmith`; `ogo smith -seed N`) is an
  *oracle fuzzer*: it interprets the program as it generates it, so it knows the
  final state and emits a self-checking checksum, and a binary failing it
  implicates the compiler or the backend with no false positives. Every seed is
  reproducible. `TestOracle` compiles a fixed seed corpus to C and runs it on the
  host shim. **`TestGeneratorCoverage` guards the fuzzer's own blind spot**: a
  generator that produces less still passes the oracle, so **adding a construct to
  the generator means adding it to `generatedConstructs`**. Calls are counted --
  every generated function adds its weight to `octosmith_calls`, asserted before
  the checksum -- so **a new call site in the generator calls `noteCall`**, once for
  each time the program runs the call, and **a new call site by name counts
  `fn.Sites`**. A part added since 2026-10-06 draws from its own source (`aggRand`,
  `wideRand`, `convRand`, `mwRand`, `emRand`, `lbRand`, `pkRand`, `paRand`,
  `swRand`, `wrRand`; the
  aggregates mint no name from VarSeq either), so every other part of a seed's
  program stays what it was.
  Beyond plain statements it
  writes constants in every spelling (consts.go), static tables (tables.go), structs
  and arrays passed
  and returned by value (aggregates.go), values of several words where a call
  stands (multiword.go), embedded structs, promoted methods, variadic calls and
  string(b) views (embed.go), the strings, bytes and errors packages and a Builder,
  their answers Go's own (library.go, its imports drawn first, drawImports), a
  package of the program's own reached by qualified names (pkg.go, only with
  `ogo smith -o`/Files; TestOraclePackages), a 64-bit operation handed on before
  arguments of other types (pairs.go), switches of constant cases on every integer
  type (switches.go), what Go defines and C does not -- overflow, the most negative
  value over -1, shifts at and past the width -- in every target form (wrap.go; the
  VM's own arithmetic keeps to what C defines), names C has spoken for and names of the
  universe (`cNames`, `universeNames`), calls in headers, a function of several
  returns for the inliner's single-exit rewrite, and widened and cross-converted
  folds. **What was swept is swept FOR A GENERATOR**: a change to it makes every
  seed that draws the changed choice a new program. A generated program is built
  with the oracle test's gcc flags, not probe.sh's `-Wall -Werror`. On the board a
  sweep loads one seed at a time under the board lock and keeps the builds the
  backend warned about apart -- **a sweep that runs only what built silently misses
  a fault the backend warned about**. Seeds 1-10000 have been swept on a P2-EDGE
  with the generators of their day, the latest (9501-10000, f4399f4) with none
  failing; 9051 of the one before found the emitter reading the largest uint64 as
  -1. A seed that outgrows a cog can take ten minutes to say so.
- A shadowing local whose initializer reads the shadowed name, `var x = x + 5`, is
  captured into a fresh temporary before the C variable shadows it (`initRefsName`,
  `newTmp`), and a declaration changing the KIND of the name it shadows forgets the
  other kinds (`shadow`) and reads its value first (`declareCopy`). These are
  targeted captures, not the general scope-aware C-naming layer the emitter still
  lacks.

## Test conventions

Semantic-check tests are table-driven over `.ogo` files in
`internal/octogo/testdata`, annotated with directives read by `tests_test.go`:

- `// COMPILE` — the whole file must type-check with zero errors.
- `// ERROR <regexp>` — the *next* line must produce an error matching `<regexp>`.

`etc.go` in each package provides the `todo()`/`trc()` position-tagged debug
helpers (guarded with `//lint:ignore U1000`); prefer them for temporary tracing.

`DIFFERENCES.md` (2026-10-08) is read by `internal/octogo/differences_test.go`: each
"### " entry's program joins `emitRunCases` in an init, named "DIFFERENCES.md:
<title>", so it runs on the host, builds for the target and runs in `make board`;
`TestDifferencesGo` runs it under Go and holds the "Go prints" block to what Go
prints (a panic's trace dropped). `<!-- board-only: ... -->` keeps an entry off the
host (its stack has no check, `boardOnly`), `<!-- go-varies: ... -->` leaves
Go's output unchecked, and `<!-- hangs: ... -->` marks a program OctoGo never ends
(`hangs`): the host runs it for three seconds, built with the shim's
`OGO_UNBUFFERED`, and asks that it did not end. **A behaviour that differs from Go is an entry there**, not
only a paragraph of specs.go -- the page is what a Go programmer reads first.

## Probe harness (`scripts/`)

The method that finds most bugs is not the test suite but a PROBE: a program a
user would write, compared against real Go, with every accessor bumping a package
counter that is printed beside the values so a double or a late evaluation shows as
a number. The scripts are portable (paths from the repo root, tools built from the
tree being probed) and read from the top of each file. The Go tool the probes build is
`scripts/dumpc` (DIR in, the checked C of TestEmitCRun out, a refusal on stderr); it
reads an `ogo.mod` above the directory as `ogo build` does, so a MULTI-PACKAGE
program can be probed on the host -- without that the whole cross-package dimension
had no sweep, and the first one written found an elided literal filling another
package's unexported field (2026-09-20). It
was first committed as `./build/dumpc`, and `/build/` is git-ignored, so the harness
reached the second machine without it; it lives under `scripts/` for that reason:

- `scripts/probe.sh DIR [SED]` -- one program vs its Go twin (GOARCH=386, so int is
  32 bits) on the host, with TestEmitCRun's exact gcc flags. Verdicts MATCH, DIFFER,
  REFUSED, GCC FAIL. SED rewrites the twin only, for `var out chan T` (Go needs make).
- `scripts/stmtprobe.sh DIR HEAD TAIL < statements` -- a position sweep, one program
  per statement line; a shape gcc refuses is also built for the target, since flexcc
  accepting what gcc refuses is the silent kind.
- `scripts/dumpcorpus.sh OUT [SEEDS]` + `scripts/corpusdiff.sh BEFORE AFTER` -- the C
  of every run case and fuzzer seed before and after an emitter change, compared by
  content with temporaries renumbered. A change for one shape must touch only that
  shape's programs.
- `scripts/board.sh DIR OUT` -- build with the tree's compiler, load, capture the
  serial output. A board match is the verdict; a host match is a candidate.
- `scripts/cboard.sh FILE.c` -- compile one C file with the in-process flexcc and run
  it on the board: how a reproducer in `doc/` is measured, and re-measured after a
  backend regeneration.
- `scripts/rejects.sh [-v] PARENT...` -- the sweep of programs Go REJECTS (see "A
  PROGRAM GO REJECTS IS A ROW" in `doc/LESSONS.md`): every PARENT/NAME/main.ogo
  through `go build` and through the tree's compiler, a row wherever the two
  disagree, and the exit status 1 when the compiler FAULTED on any. `DUMPC=` takes an older commit's dumpc,
  which is how a sweep's finds are counted after its fixes are in.
- `scripts/capped.sh [-t SECS] [-m KB] CMD...` -- a memory cap and a SIGKILL timeout
  around one command. probe.sh, stmtprobe.sh, rejects.sh and dumpcorpus.sh run the
  compiler through it; an ad-hoc loop over probe programs must too. dumpcorpus.sh did
  not until 2026-09-22 -- sixteen uncapped compiles at a time, and a crashed one
  deleted like a refused one -- and was the command running when the machine was
  next exhausted; rerun capped afterwards it measured small (~20 MB a seed, 880 MB
  for a fresh compile of the test binary) and found no fault, so the cause was never
  shown to be it. A seed that FAULTS leaves smithNNNN.fault now.
- `scripts/fmtcmp.sh [-v N] FILE.ogo...` -- each source through gofmt (a package
  clause prepended) and through the tree's `ogo fmt`, naming the files the two lay
  out differently. NOT `ogo fmt -l`, which lists nothing for a program already in
  `ogo fmt`'s layout however far that is from gofmt's.
- `scripts/flexcc` + `scripts/cccorpus.sh [-I INC] [-k KEEP] FLEXCC OUT DIR...` --
  the tree's in-process backend as a command, and every C file of some directories
  compiled by ONE flexcc into sorted `NAME RC SHA` lines; `LC_ALL=C join` two lists
  and the lines that disagree are the programs two backends build differently. How
  a backend regeneration is measured (2026-09-21): FAITHFUL to a native build of its
  spin2cpp commit (`-I` that checkout's include/), against the backend it replaces
  (which programs to run on the board), and across the five platforms, with
  scripts/flexcc built under GOOS/GOARCH -- over doc/ and a dumpcorpus.sh dump, about
  1670 programs in four minutes.

## Lessons (the index of `doc/LESSONS.md`)

`doc/LESSONS.md` is the journal behind the rules: what each probe, sweep, mutation
batch, domain program and review found, and how. Below are the rules it left, each
word for word as it stands there in bold, so `grep -n '<its first words>'
doc/LESSONS.md` finds the story. A rule in capitals is the title of a journal
section. A new finding goes there; the rule it leaves goes here too.

**Finding faults**

- **A DOMAIN PROGRAM IS A PROBE**: **write a program a user would write, and when it
  hits something, sweep the row it belongs to.** **A domain program written as Go is
  written finds what one written for this compiler does not**. **A domain program
  is run on the board even when the host matched**. A sweep's accessors record
  ORDER, `calls = calls*10 + k`, not a count ("Process rules learned the hard way").
- **A MUTATED PROGRAM IS A PROBE OF THE FRONT END**; **A MUTATION WITH GO AS THE
  ORACLE IS A PROBE OF THE CHECKER**; **A MUTANT GO ACCEPTS IS A PROBE OF THE
  EMITTER** -- **A mutant that compiles is run**; **A MUTANT IS RUN ON THE BOARD, AND
  SO IS EVERY CHANGE TO A CHECK**. **A mutator that keeps programs valid is the
  emitter's probe; one that breaks them is the checker's**. **A program's own
  mutations are the run cases' mutations for the shapes it brings**. **A batch's
  REJ/REJ list is a net too**. **When a mutant is taken, probe its line alone before
  writing a rule: what is missing may be the variable's type.** **A harness's
  header is checked against what it runs** (run.py's twins, amd64 for 386).
- **A PROGRAM GO REJECTS IS A ROW** (`scripts/rejects.sh`): **What `ogo build` does
  with such a program is the measure** -- **And measure with a struct of MORE THAN
  ONE WORD, and RUN the binary**.
- **A PROGRAM OF SIZE ASKS WHAT NO SWEEP ASKS**: **a build's TIME is a verdict**;
  **a line's length is a limit of the backend**.
- **A refusal for what a construct costs asks what Go's compiler pays for it where
  it stands: an allocation Go does not make is not one** (`switch string(word)`).
  **What Go's compiler copies is asked of it, `-gcflags=-m`, not remembered**
  (`range []byte(s)`).
- **A REVIEW IS A PROBE FROM OUTSIDE THE SWEEPS**. **A review's claim about the
  compiler is probed before it is answered: a note it read may be months behind the
  code.** **A note calling a row "by design" is checked against specs.go before it
  is believed.** **A limitation listed for users is checked against the compiler
  like any other note**. **A limit kept for its cost is measured at its cost: the
  table "this target has no room for" was 1750 bytes.**
- **The target's build of every program the probes left behind is a probe of its
  own**; **A probe's program is a target-build test too, and the warnings are
  read.** **A form the host's compiler refuses is a form nothing tests, and is run
  on the board by hand.** **What the target's runtime does, the host's compiler is
  told: a panic it took for a call that returns made every function of a result
  ending in one a form nothing tested** (dom76). **The fuzzer's board sweep finds
  what the host oracle cannot: a spelling only the target's typing rule reads
  wrong.**
- **When a new refusal fails an old test, ask Go about the test's program before
  doubting the refusal.** **A false refusal fixed in the checker is a program the
  emitter is handed next**. **A new summary rule is run over every program the
  probes left behind, not only the corpus.** **an ERROR line in a COMPILE file is an
  expectation nobody checks**.
- **A probe that makes the backend lay out a gigabyte of data costs the machine**.
- **A VALUE OF SEVERAL WORDS IS A ROW OF EVERY POSITION, ON THE BOARD**: **A sweep of
  a value's shapes is run on the board before it is called clean: the host's
  compiler evaluates in another order what C leaves to it, and reads right what the
  target's reads wrong.**
- Open items: "Known open items, all loud refusals or design walls" and "Latent
  ones" (measured, not faults today).

**Rows: what a rule is asked of**

- **A DEFINED TYPE OVER U IS A ROW**: **A name a resolution keeps for one question
  is asked by every question of the type: the element's name was kept for its
  methods and not for its slices** (dom73). **A PACKAGE BOUNDARY IS A ROW**: **A
  boundary sweep crosses two packages and three**; **Every row swept in one package
  is swept across the boundary too, with the qualified spelling in every
  position**; **A row swept in one package is swept with the qualified spelling**.
- **A C NAME IS A ROW**. **NIL IS A ROW**. **A VARIADIC CALL IS A ROW**. **A FUNCTION
  VALUE'S CALL IS A ROW**. **AN ARRAY RESULT IS A ROW**. **PRINTING A VALUE IS A
  ROW**. **A CALL'S RESULTS FORWARDED WHOLE ARE A ROW**. **A DEFERRED OR STARTED
  CALL IS A ROW**: **a new callee shape is swept under plain, `defer` and `go`, with
  a store into the callee after the statement**.
- **WHAT HAS NO KIND IS ASKED NOTHING**: **when a rule is written against a Kind, ask
  what it says of each category that has none**. **FIFTEEN RULES ACROSS THE
  CATEGORIES**: **A new rule refusing on a category asks it of each way the checker
  misreads one: a pointer to a pointer, an address, the element of a slice
  expression.** **Go's exceptions are part of a category rule**.
- **A CASE WAS A POSITION NOTHING WALKED**: **when a rule is written, ask which
  positions its walk never reaches**. **A TARGET IS A POSITION**: **a new store rule
  is asked at every site its siblings are**; **A rule asked at some store sites is a
  row of the others**.
- **A RECEIVE HAD NO TYPE**: **A value with no type is a row across the targets**.
  **A value of no name is asked what it is in every shape it comes in -- a receive
  is one of them**. **A variable is a row of every declaration form**. **A value
  nothing typed hides every misreading of it**.
- **TWO DEFINED TYPES OVER ONE KIND ARE TWO TYPES**: **when a rule is gated on what
  the checker records, sweep the SHAPES that produce a value of the type -- a
  conversion, each operator, parentheses -- as well as the positions the value goes
  into**; **The value-producing shapes of a type are a list to walk whole**; **a
  builtin that reads a variable's declaration reads the sliced variable's too**.
- **A CONSTANT IS A VALUE, OF THE TYPE IT MEETS**. **A constant's SIZE is a row**.
  **A constant's CLASS against its TYPE is a row**. **A constant in a shift's
  expression is asked what the shifted one is.** **A step one declaration form
  takes is asked of the others**. **A constant's value is exact, and a C spelling
  of it that is not -- a sign on a zero -- is the C compiler's arithmetic, not
  Go's.**
- **AN INTERFACE WRITTEN OUT IS A TYPE**: **A rule asked by name is a row of every
  type that has none.** **AN ALIAS OF A TYPE WRITTEN OUT IS THAT TYPE**: **a type
  that names no defined type is stored under the literal's table, and every place
  naming a table asks the same name**. **A name in a type position is a row of every
  declaration that resolves to no package**.
- **A numeric domain program crosses the element WIDTHS with the dimensions**.
  **Package declarations are in no order, and every pass that resolves them in
  source order is a row of it.**

**The checker**

- **One question, one helper**. **Two answers to one question are a false refusal or
  a hole, and the second answer hides what the first lacks**.
- **A table read by its value is asked by its key, and asked what else it holds: an
  empty struct's fields are nil, and so are an interface's** (isStruct, fieldEqCmp).
  **Two keys for one type are two tables: the reader asks by the key the writer
  wrote** (funcTypeID, dom53).
- **A mark a refusal reads is asked what else carries it: isPtr marks a type
  switch's binding of an interface as well as a pointer** (A POINTER TYPE IN
  PARENTHESES IS ANY TYPE). **A type the flags cannot say is recorded whole, as a
  declaration would write it: an unnamed variable is one every rule asking a name
  skips** (`pp := &w.head`, dom59). **A name a walk records is a mark like the
  flags: a check reading a name as a defined type's asks the type the walk gave
  first** (`p := &a[1]; *p = 300`, A FLOAT64 IS ASKED WHAT A DOUBLE WAS). **A check
  reading a value against a type reads each by its own file: the value's file asks,
  and the type goes by its scope** (A COUNTER THAT COUNTS PASSES EVERY ORDER).
- **WHAT THE FLAGS SAY IS A TYPE**: **A walk that types by what was written answers
  for a variable that has only flags: the flags say a type, and every pointer to the
  variable is untyped until the walk reads them.** **A list of names kept beside the
  declarations it names is asked of each declaration: six predeclared types had
  none.**
- **A RUN HAS A TYPE AT EVERY OPERATOR**: **A check asked of a run's innermost
  operator is asked of each operator out: the operand of each is what the operators
  inside it yield.**
- **A HEAD IS READ BY EVERY TARGET READER**: **A target reader that dispatches on
  its head asks what a qualifier is: `*lib.P` is a star over another package's
  variable, not over a field of one named lib.** **A shape that newly compiles is
  asked every type question too: a reader that reads it as nothing stores whatever
  it is handed.**
- **A VALUE IS ASKED WHERE IT GOES, IN EVERY SHAPE IT COMES IN**: **A sweep that
  types a shape where it declares a variable asks it in every position a value goes
  into.** **A question answered by a name is asked of the walk where no name
  answers: an element of a defined array has a type and no name.** **A binding a
  clause makes is typed as the statement's: a select's receive and a range's are
  declarations.**
- **A rule that refuses on failing to resolve is a false refusal waiting for a shape
  it does not know**; **A rule that refuses by failing to resolve is found by
  widening the resolver and running the rejects again**; **A rule refused by
  accident is a rule nobody wrote**; **A rule that refuses on what the checker knows
  must ask what the checker fails to record, not only what it records**.
- **A CALL'S RESULT HAD NO STEPS**: **A walk that types a value hands its type back
  to every rule, and a rule asked of a value asks the walk**. **A walk that passes a
  call hands the call to the argument check.** **A lowering that makes a shape
  writable makes it a row of the checker's walks.** **a shape the emitter learns to
  read is a shape the checker must refuse the same way**.
- **A new helper following type definitions goes through typeIdentDecl.** **a file
  is found from a qualified name's QUALIFIER**. **A name the checker reads off
  another package's declaration is resolved there whether or not it is exported;
  only a name this file wrote is held to the export rule.** **A new bare-name lookup
  into a package-level registry asks bareGlobal**. **A map keyed by a source name is
  asked through the one function that knows what the name means here.** **A
  dispatch on a name asks what the name means where it is written**. **A gate that
  takes "not found" for "a qualifier" is a row of every scope a name can be found
  in: a function literal's finds the import itself** (foundOrQualifier, dom53).
- **a rule that declares a variable is asked which type Go gives it**; **`%T`
  against Go is the cheap oracle for what type a declaration gives**.
- **A LABELED BREAK REFERS FROM ANY DEPTH** (a termination rule is written into a
  missing-return sweep and a reachability sweep both). **A STATEMENT IN A HEADER IS
  THE STATEMENT**: **the statement is put back once** (headerStmt). **Check Factor
  and HeaderFactor against each other when either changes**.

**Lifetime**

- **The lifetime rules are asked of SHAPES, and a shape nobody asked about is a
  hole**: **A new binding form, or a new way to write a value, is a new row or column
  there** (TestEmitCFrameRefForms and its siblings).
- **KNOWN MEANS MUST**: a rule that PERMITS on what it knows needs what is true on
  every path, one that refuses may believe a mark. **"Nothing here can name it" is an
  answer for a refusal, never for a permission**.
- **A STORE RULE IS ASKED AT EVERY STORE SITE**: **A new place that stores calls
  both** (refuseStore and noteStoredThrough); **A new way a callee stores is read by
  those two and sunk by that one.**
- **A RULE ASKED OF A ROOT MISSES WHAT A POINTER REACHES**. **A STORE A SUMMARY
  RECORDS IS ASKED HOW FAR IT GOES**: **one pointer from a value the caller or this
  frame chose is the call site's question; any other lands where no rule can name**,
  and **what a name's value is, is asked of its bindings, not of what is stored into
  it**. **A BINDING IS EVERY FORM THAT GIVES A NAME A VALUE**: **a binding is every
  form that gives a name a value -- a statement, a clause, a header, a guard -- and a
  rule reading a name's bindings reads every form**, **a target no walk types is
  asked as one through a pointer**, and **what a call hands back of its receiver is
  asked how far past it the result is**. **AN EMBEDDED POINTER IS A POINTER THE CHAIN
  DOES NOT SPELL**: **a promoted field or method is asked as the chain its promotion
  spells: every pointer step a rule asks of, it asks of the members the promotion
  goes through**. **A STAR IS GO'S SHORTHAND OVER ONE POINTER**: **a reader that
  drops a dereference asks whether Go's shorthand drops it: one star over a pointer
  to a struct or an array is the shorthand, and any other is what the call is made
  on**. **A VIEW IS AN ADDRESS**: **a slice of an array is the address of its first
  element: every rule asking where an address points asks it of the view**; **A
  VIEW THROUGH A POINTER IS OF WHAT IT POINTS AT**: **A view's storage is asked of
  what is sliced: a slice of a pointer to an array views what the pointer reaches,
  which the pointer's root is not.** **A slice holds no storage: a rule asking
  where a view's storage lives asks what it views, not where the slice is
  declared** (dom67, markBacked). **A rule read off one step is asked of a chain
  of them: a view of a view is the view, at any length** (dom77). **A
  type a rule does not know is asked which rules read it as a default**. **A MARK IS MADE IN EMISSION ORDER, AND A LOOP RUNS ITS BODY AGAIN**: **A mark is a fact about a
  variable at a point; a rule reading one asks what reaches that point -- a loop's
  back edge and an alias both do.**
- **A summary of a call is asked by result: one answer for the whole call gives
  every result every argument** (resultPos). **A return is asked in every form: a
  bare one returns the named results, and under a defer every return does.** **A
  binding a callee makes is asked of the closed summaries: a local whose address a
  call hands on holds what the callee stores there** (applyIntoCands; dom69). **A
  position a summary keeps is asked wherever a call's results are taken apart: at a
  call site, in a callee's summary, through an interface, and where a return hands
  them on.** **A return is asked in every form, a forwarded call among them.**
  **A block is counted as Go declares it: the parameters are of the body's
  outermost block.** **A loop is every way a statement runs again: a goto back is
  one** (dom71). **A store through a name is asked what the name IS, not what it
  holds: a pointer stored into it is reached only by a target that goes through
  it.** **A value that may be a reference is asked at every gate that asks a type
  whether it can carry one: a string a Builder hands out was followed only where
  `sb.String()` stood written** (dom72). **What a spread keeps is what its
  elements reach: append's last argument under `...` is read as copy reads its
  source, never as the slice it spreads** (dom73). **A cost a comment calls rare
  is swept before it is believed: the per-variable mark was to refuse one program,
  and it refuses every field kept out of the holder** (dom75, sweeps/field-holder;
  listed). **What a call hands back is asked where it is bound as where it is
  written, and a method is asked what its receiver holds as well as what it is**
  (dom77, sweeps/result-contents).
- **A PARENTHESISED HEAD IS A ROW OF EVERY SCAN**: **A new scan reads heads through
  them, and a new shape the emitter reads through parentheses is a row in each
  scan.**
- **A CALL'S RESULT IN A LOCAL IS A ROW OF THE SUMMARIES**: **A new way a value
  passes through a call is a gate there, not a new edge kind.** **WHAT A CALL HANDS
  BACK IS ASKED ONCE**: **A new way to call is a row there, and a new kind of
  parameter -- packed, received, bound -- is asked what it HOLDS.** **A walk over
  what names hold carries its path**; **A walk whose answer is a set is kept by
  what it answers, not by the path it took: a path through the calls a rebound
  name holds was walked in every order of them** (dom74).
- **A WALK ASKS WHAT ITS VALUE CAN HOLD**: **A walk for what reaches the frame asks
  first whether the value can hold a reference: a number reaches nothing, and
  walking it costs what the walk's branching costs.**
- **A slot a summary has for a parameter, it has for the receiver**. **WHAT GOES
  NOWHERE MAY STAY BEHIND**: **A store a summary drops for keeping nothing is asked
  what it leaves behind: the receiver into itself goes nowhere, and its storage holds
  a pointer into itself.** **A new way to
  reach a receiver is a row in methodCallOf, under a plain call, `defer` and `go`.**
  **A summary is asked what the value it records can hold**. **A new place a pointer
  can stand -- an operand, a header, a clause's name -- is a row in
  TestEmitCAssertLifetime's matrix, and a statement form a header can hold is a row
  of eachStmt.** **Making a shape WRITABLE asks the summaries what they read of
  it**. **A shape that newly compiles is asked every lifetime question**. **A shape
  made to compile is asked of a callee's summary as well as of the function it
  stands in: the summary reads the statement by its own readers, and a head none of
  them knows stores nothing.**

**The emitter: order and binding**

- **A walk that answers a question must not change the program**. **An owner of
  deferred work is the storage the literal fills, not whatever is being rendered
  when it is found.** **What a question forgets of what it minted, it forgets
  whole: a name cached and a flag set are minting too** (dom72).
- **Where a statement renders a part of itself that Go evaluates conditionally,
  later, or repeatedly, that part is rendered inside emitOwnPrologue**; **a
  statement that renders parts of itself asks where each part's hoisted statements
  land**; **a lowering with two branches asks the ordering discipline of both**; **A
  head the emitter writes as text is asked where its effects land against the
  arguments'**. **A call is asked of every head a call can follow: a name, a
  parenthesis, a dereference, a conversion, a call** (nodeHasEffect).
- **A RUN OF PREFIX OPERATORS IS ONE NODE**: **A rule written for an operator and
  its operand asks what the grammar's repetition hands it: a run of prefix
  operators is one node, and the operand of the first is the rest of the run.**
- **A head the emitter binds to a temporary is asked whether C copies it: an array
  a pointer points at is reached through the pointer** (emitParenChain; A VALUE IS
  ASKED WHERE IT GOES); **a dereference is storage, and a head the emitter binds
  asks whether Go could take its address** (a struct's too, `&(*h.ps).f`; A VIEW
  THROUGH A POINTER IS OF WHAT IT POINTS AT).
- **A rule that dispatches on a table asks for the table it compares with, not only
  the one it stores.** **A type the emitter writes after the globals names its
  members with the `ogo_` prefix**. **A helper is written by the gate of what it
  serves: two float32 helpers in the float64 runtime were missing from every
  program that called them and had no float64** (dom64).
- **A method the compiler writes in C asks of its receiver what a body's dereference
  would** (the Builder's helpers and a nil *Builder).
- **A call on the way to a value of several words is bound in the one word it
  yields: a pointer, or a struct element's address** (the target's compiler copies
  such a value a word at a time; A VIEW THROUGH A POINTER IS OF WHAT IT POINTS AT).
- **Two operands that do something in one C expression are bound in Go's order,
  whichever compiler builds it** (indexOnceC, bindTargetOrder; A VALUE OF SEVERAL
  WORDS IS A ROW OF EVERY POSITION, ON THE BOARD).
- **A STRUCT HOLDING AN ARRAY IS COPIED, NEVER ASSIGNED**: **A check asked of a
  position's C type asks first whether the position is an array: an array's ctype
  is its element's** (dom70). **A STRUCT OF MORE THAN
  FOUR WORDS PASSED WHERE A CALL STANDS**: **A new place a struct-valued call is
  handed on binds it first**.

**The target and its backend**

- **A LIBRARY FUNCTION IS BUILT ALONE FOR THE TARGET**: **A function ported into a
  library is built alone for the target before it is called done: a function no
  program can fit in a cog is no function.**
- **A FLOAT64 OF TWO WORDS**: **A new representation is asked every workaround its
  width already has: grep the gates for the int64 and the struct, and add the type,
  before the board finds them one at a time.** **A FLOAT64 IS ASKED WHAT A DOUBLE
  WAS**: **A new spelling of a type is grepped for in every test of the old one:
  each `"double"` the float64 was not added to was a question it had not been
  asked.**
- **A TEMPORARY IS A COG REGISTER**: every C local takes one of a pool of 480
  (`grep -c '\tres\t1' prog.p2asm` counts them), so a new lowering is asked what it
  hands a call BY VALUE. **A program written as Go that fails the cog's registers is
  split where its message says, and the split is the program's: the backend keeps
  every printf argument live through the call** (dom63).
- **A CALL IS DEAR, AND THE BACKEND INLINES WHAT IT IS TOLD TO**: **every inlined
  copy brings the registers of its locals to where it lands**; **A policy is
  measured by what it breaks**; **no program fails to build for having been made
  faster**; **that the copies are right where they land is verified on the board
  and nowhere else**; **Whatever emits what a build emits passes both** (`Checked()`
  and `Inline()`); **A change that makes code smaller makes more of it small enough
  to inline: a build at its limit is asked again without the marks** (A STRUCT'S
  REGISTERS ARE GIVEN BACK).
- **A CHECK THE PROGRAM PROVES IS NOT WRITTEN**: **A check the emitter writes is a
  cost the program pays at every run; one Go's own compiler proves away is one this
  one should**; **a change to how narrow values are written is measured on the
  board**.
- **A READ IN A LOOP IS MADE ON EVERY PASS**: **The rule is known at -O1 only**
  (TestBuildSharedVariables reads the listing).
- **A STRUCT THE BACKEND DOES NOT RETURN IN REGISTERS IT RETURNS ON A HEAP**: **A
  promise the language makes about the hardware is checked in the listing**
  (`__system___gc_` in it is the backend's heap).
- **A GOROUTINE'S STACK IS READ OFF THE LISTING**: **A listing is a probe of what
  the C costs**; **a bound with no answer still has a floor**; **A size taken as
  asked is still held to the floor the build knows: what the user gave is no reason
  to run past it in silence** (dom55); **A bound that is capped is a bound that is
  not kept**; **The decision a precise bound rests on is
  made where it is verifiable**; **A floor a measured bound replaces is asked, with
  a moat past the bound, what it was hiding**; **A runtime made general for a new
  shape keeps the old shape's C: a program at its limit pays for a generality it
  does not use** (goslots.go); **A listing asked for to look at a program is a
  different build: its binary is compared with the one shipped**; **a size the
  backend gives is asked of the backend** (`ogo build --report`); **A rule a call
  graph's bound rests on is asked of every kind of edge the graph has: one written
  for direct calls is a row of the interface's and the function value's**; **A moat
  sweep measures the programs it sweeps: each rule the bound rests on is asked by a
  program written to break it** (ONE PASS THROUGH AN INTERFACE); **A check the
  program pays at run time is written where the listing proves it needed, and
  sized off the listing of the program it is in** (A RECURSION IS CHECKED WHERE IT
  RUNS); **A fact about the emitted code rides in its text: the emitter renders text
  it throws away, and a record kept beside the text counts renderings that never
  reached the C**; **A finer edge found for one question is read for that question:
  what a recursion is told is no stack's size until the size can rest on it** (WHAT
  A FUNCTION VALUE'S TYPE MAY HOLD); **A question answered off a graph is asked of
  the edges it is about: a size's edges answered a recursion**; **A copy is counted
  as many times as it is made: a function inlined twice brings its calls twice**
  (dom53); **A test that a fact is exact answers whether it may stand for the
  whole, not whether it is true: a copy that lost a call still makes the others**;
  **A claim that a change leaves the binaries alone is measured by building them,
  the newest run cases among them**; **A table read for a size names what the value
  IS: a wrapper the emitter lifts for it is the value, and its frame is the call's**;
  **A bound a value's type gives is asked of every way memory is read as another
  type: a pointer converted, through unsafe.Pointer or not, makes a value of one
  type a value of another** (A VALUE'S TYPE IS A STACK'S SIZE).
- **Two programs that should cost the same are compared by their BINARIES**.
- **What the backend makes of a library call is read in the listing for what it
  holds while the arguments run**; **A goroutine's output is asked with main
  waiting in each way it can wait, a print of a receive among them** (A PRINT HOLDS
  A LOCK).
- **A backend fault measured for one type is asked of every type of its size: the
  target's compiler lays out bytes, not Go's types** (wideRowC; A VALUE OF SEVERAL
  WORDS IS A ROW OF EVERY POSITION, ON THE BOARD).
- **A STATIC INITIALIZER IS LAID OUT BY ANOTHER RULE THAN ITS TYPE**: **A rule about
  the target's layout is measured on the board in C first**; **The host's compiler
  is no witness for an initializer's layout**. **A ROW OF 64-BIT ELEMENTS INDEXED BY
  A CALL IS LOST ON THE TARGET**. **A float literal the emitter writes is one the
  target reads exactly**.
- **A fault measured in one statement form is measured in the others: a plain store
  right says nothing of its compound form** (doc/row-call-compound.c).
- **A workaround written from a symptom covers the symptom: the backend's source is
  read for the cause before the workaround is called whole**; **a row measured last
  in its argument list says nothing of the same argument with another after it**
  (doc/call-arg-after-expr.c).
- **A callee in a reproducer reads every word of what it is handed**; **a fuzzer
  part written for one fault is run on the board before its fix is called whole**
  (genPairs, valueCallArgC).
- **A row is swept on both sides, what goes in and what comes back, before its fix
  is released**; **a fuzzer part widened to the shapes next to the fault finds the
  faults next to it** (valueResultC, hoistArgs).
- **A backend fault in a call is a row of every call the C makes, the emitter's own
  helpers among them**; **where the backend misreads a spelling everywhere it
  stands, the spelling is changed where it is written, not bound where it is used**
  (shl32C).
- **A speedup is measured shape by shape on the program that asked for it: a mean
  hides who pays**; **a source ordered for one lowering is a measure of that
  lowering, not of the one replacing it**; **a cost written down as the backend's
  is checked against the C the emitter writes** (switchCaseLabels).
- **A change that saves registers in one function is asked what it costs in another,
  and a fallback with one step is asked what a second change needs of it**
  (compileMarked, plainerC).
- **A generator's model of a statement is held to Go before it is held to the
  compiler** (genSwitches). **A fuzzer's counter records the order of its calls:
  one that counts them passes every order the emitter writes** (A COUNTER THAT
  COUNTS PASSES EVERY ORDER).
- **A build the sweep's cap killed is built by the compiler before the change before
  it is called slow** (doc/switch-case-span-overflow.c).
- **A fuzzer that keeps to what C defines never reaches a guard the emitter writes
  for what C does not: the oracle's arithmetic is the language's** (genWraps).
- **An unsigned constant past INT32_MAX is a negative number to the backend: every
  place it reads a constant's value is asked of one** (doc/udiv-negative-power.c,
  doc/switch-case-span-overflow.c).
- **An unsigned constant past INT64_MAX is a negative number to the emitter's fold: a
  decision taken on a folded value's sign, or on its being -1, asks the type it is
  of** (remByOne, shiftCountU64). **A rule written for an untyped constant asks
  first whether the constant is typed** (checkShiftCount).
- **A warning the backend gives a user's build is a fault until shown harmless**.
  **A run that reads a register nothing wrote can look right by luck: the listing is
  the measure, and a C harness passing a constant after a 64-bit argument is a
  second fault of its own**.

**The formatter**

- **THE FORMATTER'S ORACLE IS GOFMT ON A PROGRAM'S OWN SOURCES**: **A width is read
  off what was written, never measured from the tree**; **What begins a section is
  go/printer's and is copied, not derived**; **A formatter test holds its
  expectation against gofmt where there is one**; **a domain program's source
  through both formatters is the formatter's probe**.
- **A variable the oracle's source asks is read where it is set, not by its name**
  (go/printer's lastTok, format_doc.go).

## Notes

- `specs.go`'s doc comment is the authoritative **language spec + grammar**;
  `internal/octogo/octogo.go`'s doc comment is the authoritative **compiler-internals
  design** (check phases + WPO). `web/` holds logo assets only — the landing-page
  outline that used to live there was deleted 2026-07-18 (it was a raw LLM chat
  transcript pitching a paid-license model that the BSD LICENSE and the README's
  GitHub Sponsors tiers had already superseded). (`gem.md`, the original Gemini
  design corpus, was deleted 2026-07-10; its unique, still-unreconciled bits are
  salvaged in the appendix below.)
- Requires Go 1.26+ (the go directive follows modernc.org/libc's, 1.26 since
  2026-09-27; the code itself wants 1.23's iterators, `maps`/`slices` and
  range-over-func).

## The `p2` package — 1:1 mapping to flexprop C intrinsics


The `p2` package (resolved from the dotless import `"p2"`) is a thin, strongly-typed
wrapper over flexcc's built-in P2 intrinsics — zero runtime overhead, no custom PASM.

**It is EMBEDDED SOURCE**, like `testing`: `p2Src` in `internal/octogo/build.go`
declares every function BODYLESS (the grammar's form for a function implemented
elsewhere) and every constant, so the checker sees real signatures and a misspelt
name is caught by the compiler rather than by C. Nothing of it is emitted — it is in
`intrinsicImports`, which `reachablePackages` skips — because every declaration is
substituted at the use: a function by its C intrinsic (`p2Intrinsics`, which carries
the result C type so a uint32 one like `Rnd` prints unsigned), a constant by its
value. The constant VALUES live once, in `p2Constants`, and `p2ConstDecls` renders
the source's const block from them so the two cannot drift. Currently wired (verified on the P2 via `TestOnBoard`, and off-target
via the `testdata/hostp2` shim which now stubs these):

| OctoGo | intrinsic (→ result) | OctoGo | intrinsic (→ result) |
| --- | --- | --- | --- |
| `p2.PinHigh(pin)` | `_pinh` | `p2.WritePinMode(pin,m)` | `_wrpin` |
| `p2.PinLow(pin)` | `_pinl` | `p2.WritePinX(pin,x)` | `_wxpin` |
| `p2.PinToggle(pin)` | `_pinnot` | `p2.WritePinY(pin,y)` | `_wypin` |
| `p2.PinFloat(pin)` | `_pinf` | `p2.ReadPin(pin)` | `_rdpin`→uint32 |
| `p2.PinIn(pin)` | `_pinr`→int | `p2.AckPin(pin)` | `_akpin` |
| `p2.PinWrite(pin,v)` | `_pinw` | `p2.GetCt()` | `_cnt`→uint32 |
| `p2.PinStart(p,m,x,y)` | `_pinstart` | | |
| `p2.WaitMs(ms)` | `_waitms` | `p2.GetMs()` | `_getms`→uint32 |
| `p2.WaitUs(us)` | `_waitus` | `p2.GetSec()` | `_getsec`→uint32 |
| `p2.WaitCycles(n)` | `_waitx` | `p2.GetUs()` | `_getus`→uint32 |
| `p2.WaitUntil(t)` | `_waitcnt` | `p2.ClockFreq()` | `_clockfreq`→uint32 |
| `p2.Rnd()` | `_rnd`→uint32 | | |
| `p2.Rev(x)` | `_rev`→uint32 | `p2.Reboot()` | `_reboot` |
| `p2.ReadLUT(a)` | `ogo_rdlut`→uint32 | `p2.WriteLUT(a,v)` | `ogo_wrlut` |
| `p2.SetBaud(n)` | `_setbaud` | `p2.ReadByte(t)` | `_rxraw`→int |
| `p2.WriteByte(b)` | `_txraw` | | |
| `p2.NewLock()` | `_locknew`→int | `p2.TryLock(l)` | `_locktry`→bool |
| `p2.Unlock(l)` | `_lockrel` | `p2.FreeLock(l)` | `_lockret` |

`ReadLUT`/`WriteLUT` (2026-10-03, asked for by p2-11, whose emulated registers live
in Hub RAM) are the one pair that is no flexcc intrinsic: `lutHelperDef` emits
`ogo_rdlut`/`ogo_wrlut` where a program calls one (`usesLUT`), each an `__asm const`
RDLUT or WRLUT marked `__attribute__((inline))`, which the backend inlines to the
bare instruction -- the listing shows no call -- and a `__thread` array of 256 on the
host, a thread being a cog there. The address is 0 to 255: LUT $200-$2FF, which
flexcc leaves to the program while no function is placed in LUT (nothing OctoGo
emits is); $300 up is the backend's. A checked build compares and panics before the
instruction, a constant address folds the compare away, and a constant past 255 is
refused by the emitter (`lutAddrOK`). Measured on a P2-EDGE in hub execution, eight
in a row at eight alignments: RDLUT 3 clocks and WRLUT 2, against RDLONG 15 and
WRLONG 8 (15 for one alone); and a goroutine's cog sees its own LUT (the run case
"the LUT RAM of each cog").

The package also exports the pin-configuration CONSTANTS a smart pin is brought up
with -- `p2.DAC990R3V`, `p2.DACDitherPWM`, `p2.OutputEnable` and the rest of the DAC
set, and since 2026-08-12 the ADC one that mirrors it: the input ranges `p2.ADC1X`
through `p2.ADC100X`, the sampling modes `p2.ADCSample`/`ADCSampleExt`/`ADCScope`,
and `p2.ADCGround`/`p2.ADCSupply`/`p2.ADCFloat`, the internal references a
ratiometric reading has to be scaled between; and the pin DRIVE strengths,
`p2.DriveHigh15K` through `p2.DriveHighFloat` and their `DriveLow` mirrors -- in
`p2Constants`
(`internal/octogo/emit.go`), values from flexcc's `smartpins.h`. **`ADCSample`'s X
is a sample period of 2^X clocks and is usable to 13 and no further**: measured on a
P2-EDGE the doubling is exact up to there and at 14 every reading is 0, above that
noise, whatever the Y register says. So the mode's best is ~10640 counts between the
references, a little over 13 bits. Nothing reports the overrun.

**A pull-up is a weak HIGH drive plus a floating LOW one** -- the P2 has no pull-up
bit and nothing named like one, so `p2.DriveHigh15K|p2.DriveLowFloat` through
`WritePinMode` and then `PinHigh` is the whole recipe, and the mirror with `PinLow`
is a pull-down. Verified on the board: pulled down a pin read 0 where the same pin
left floating read 1. Reading an input with neither a pull nor an external driver is
what these exist to prevent. They are emitted as literals, since the p2 package has no source to
define a symbol in. They exist because the hex is unforgiving in a way that looks
like working code: `_examples/gopher` was written with `0x140006`, which is the DAC
range and the mode and no OUTPUT ENABLE, and drives nothing. They work in a `const`
declaration as any package's constants do (qualifiedConst in check.go,
foldedQualifiedInt in emit.go), and a call into p2 -- like a call into any imported
package -- is checked against its signature (checkQualifiedRef -> checkArgsIn,
which resolves the parameter types in the CALLEE's scope).

The four lock entries are the P2's 16 hardware locks, the same pool the channel
runtime draws from. **`NewLock` does not report exhaustion**: after handing out
0..15 the toolchain's `_locknew` returns 15 for every further call rather than -1,
measured on a P2-EDGE (`doc/locknew-never-fails.c`). So a caller cannot detect it,
and two logically distinct locks alias -- harmless where sharing only costs
contention, as in the channel rendezvous, and a hang where a program nests two
locks it believes are independent, `_locktry` not being reentrant. There is no blocking
acquire in the hardware, so waiting is a spin on `TryLock`, which is why it is the
one intrinsic typed `bool`. They are what lets user code write a multi-producer
structure; a single-producer/single-consumer ring buffer needs no lock and works
(verified on the board), which specs.go's "Memory shared between cogs" makes a rule
since 2026-10-03 -- see A READ IN A LOOP IS MADE ON EVERY PASS in `doc/LESSONS.md`.

~~A `select` waiting on a Smart Pin~~ SETTLED 2026-09-04: no pin clause. The
default-arm polling idiom (select over channels, `default:` polls `p2.PinIn` +
`p2.AckPin`) is the supported form -- hardware-verified in `_examples/pinselect`
and specified in specs.go's select section. Revisit only if real programs show the
idiom wearing badly.

## Appendix: salvaged design notes from gem.md (unreconciled — process later)

> **Status: historical design intent, NOT current authority.** Extracted from
> `gem.md` (a Google Gemini "Gem" design corpus) before it was deleted on
> 2026-07-10. It predates the `octogo → ogo` / `.octo → .ogo` rename. Most of
> gem.md already migrated into `specs.go` (language spec) and
> `internal/octogo/octogo.go` (checker/WPO design); the items below are the ones
> that lived *nowhere else*. Reconcile against those two files before acting on
> any of this, and delete each item from here once it has been folded into the
> real spec or implemented.

### 1. Dropped CLI split: `compile` vs `build`

gem.md intended two backend commands: `ogo compile` (emit intermediate C + headers
only) and `ogo build` (wrap flexprop to produce the P2 binary). Current `main.go` has
only `build`. gem.md itself flagged the doubt — *"with WPO `compile` might be no
[longer] possible"* — because whole-program optimization fights per-package separate
compilation. Decide `compile`'s fate deliberately when wiring the backend.

### 2. Misc hardware / codegen intent

- **P2 budget:** 8 Cogs; 512 KB shared Hub RAM; 512 longs (2 KB) local Cog RAM per Cog.
- **Local zero-init:** Cog-stack locals are NOT auto-zeroed by C, so the emitter must
  emit an explicit `= 0` for every `var x T` without initializer (globals rely on the
  BSS segment — already covered in `specs.go`).
- **`init()` stitching:** WPO should topologically sort global-var initializers and
  concatenate all `init()` bodies into one synthesized `__octogo_init()`, injected at
  the top of the C `main()`. (This mechanism name appears only in gem.md.)
- **Translation-unit tension:** the "one directory = one package = one C translation
  unit" model may not survive WPO, whose passes cross the per-directory boundary.
  gem.md flagged this as unresolved.
