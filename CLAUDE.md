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
strict LL(1) grammar" -- it took one extra production, 2026-09-18). **Before treating any such note as settled, check whether the
feature actually works** -- and if a stale note is found, fix the note.

The deliberate exceptions, all rooted in the Propeller 2 hardware:

- **No heap.** Nothing allocates at run time. This is what rules out `new`, every
  `make` form but the slice one, maps, runtime string concatenation, and a
  function literal that captures its surrounding scope.
- **A goroutine is a physical cog** (there are eight). No scheduler, no
  preemption; `go` starts a real core, not a task.
- **A channel is a P2 hardware lock** over statically allocated Hub RAM -- a
  synchronous rendezvous with no scheduler behind it.
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
ogo build --clock 200MHz .                 # ask for a system clock; default is 160 MHz
```

**The clock is a BUILD-time choice and 160 MHz is the default**, which surprises
people (the `ogo run` help claimed 200 MHz until 2026-08-12 and was simply wrong).
flexcc falls back to 160 MHz -- mode `0x010007fb`, a 20 MHz crystal times eight --
whenever the program declares no `_clkfreq`/`_clkmode` pair, and it reads those two
by name from the program's own constants (`GetClkFreq` in the transpiled sources).
`--clock` computes the PLL divisors and emits that pair; `internal/octogo/clock.go`
holds the arithmetic, and its test pins the 160 MHz word against the backend's own
fallback so the encoding cannot drift from the compiler that consumes it.

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

## Code generation

Three generated artifacts are checked in. Regenerate only when changing their
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
   transpile) and `internal/optimize_ir.c.diff` (fixes carried ahead of upstream,
   flexprop#109, #110 and #111 -- drop each with the pin that carries upstream's own),
   transpiles, and rewrites the emitted `main` package into a reusable `flexcc`
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
   on PATH) for darwin/amd64 — heavy and network-dependent; to adopt a changed
   `flexpropRef` you must `rm -rf internal/flexprop` first so the pin is re-cloned.
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
   linux/other-unix counterpart, tagged `!windows && !darwin`); see the windows note
   below. Darwin has its own `internal/flexcc/supplement_darwin.go` (the libc
   functions — `stpcpy`, `wcrtomb`, `asctime`, `fseeko`/`ftello`, `powl`/`frexpl`,
   `freopen`, and the `ungetc`/`abort` todo-stub redirects — that libc lacks or
   stubs for both darwin arches).

   > **Backend regenerated 2026-09-15 with two fixes of its own** — spin2cpp
   > `3840014f` (7.7.3-beta, its master of 2026-09-05) inside flexprop `v7.7.0`, plus
   > `internal/optimize_ir.c.diff`, ccgo v4.34.6. Adopted for two SILENT optimizer
   > faults, each reported upstream with its part of the diff as the fix: a
   > regression the 08-29 regeneration let out, two rewrites that disturb the carry
   > of a 64-bit add or subtract of a constant (`doc/add-immediate-carry.c`,
   > flexprop#109), hidden until then by the `-Ono-inline-small` that went with that
   > regeneration; and an old one, the second of two `if` bodies updating one global
   > starting from a stale register (`doc/conditional-load-dropped.c`,
   > flexprop#110). Both found by widening the on-board fuzzer sweep, to 120 seeds
   > and then 1000. The flag was not restored: it costs 1.4x-3.3x on hot loops. The
   > `doc/` battery, pinned against regenerated on a P2-EDGE: five reproducers
   > changed -- the carry fault and the two older entries that turned out to be it
   > (`doc/negate64-then-add.c`, `doc/int64-min-spelling.c`, whose emitter
   > workarounds stay, costing nothing), #107 cleared, and #108 only partly (`round`
   > still clamps outside int range, so `math.Round` stays built on Floor). The other
   > 25 compile byte-identically under both, and the three compile-time refusals
   > refuse as before. Faithful: every compiling reproducer is byte-identical under
   > the in-process flexcc and a native build of the same commit with the same diff,
   > and upstream's `make test_offline` passes 588/588 with the diff, as without.
   >
   > **Regenerated again the same evening for a third** -- an OLD silent fault, a
   > signed compare of two values the optimizer knows decided by their overflowing
   > 32-bit difference: `-7 < 2147483647` false (`doc/signed-compare-overflow.c`,
   > flexprop#111; found comparing wide constants against Go on the board, and in
   > OctoGo a saturation check inlined with a constant argument). Its hunk joined
   > the diff; the same checks, the same way: only that reproducer changed in the
   > battery (31 byte-identical, the three refusals as before), 32/32 faithful to a
   > native build, test_offline 588/588, five platforms byte-identical, and of 1438
   > programs -- the run corpus and fuzzer seeds 1-1000 -- no binary changed.
   >
   > **Backend regenerated 2026-08-29 at upstream's tip** — spin2cpp `2bd01c4c`
   > (7.7.2-beta, its master of that day) inside flexprop `v7.7.0`, the wrapper and
   > the compiler pinned separately (`flexpropRef` / `spin2cppRef`), ccgo v4.34.6.
   > Adopted because flexprop#105 was fixed upstream on 2026-08-20 and no release
   > carried it nine days later, which was the standing decision. What it cleared,
   > each measured on a P2-EDGE pinned against regenerated with the whole `doc/`
   > battery re-run: the unwritten-element multiply of flexprop#105, the constant
   > divide (`doc/const-divide-miscompile.c`), the compound assignment of
   > flexprop#106, and the two optimizer faults (#103, #104) that `ogo build` had
   > passed `-Ono-inline-small -Ono-peephole` to avoid — **both flags are gone**, and
   > with them the up-to-87% hot-loop tax (`internal/build/build.go` keeps the
   > numbers as history). Nothing else moved: eighteen of the twenty-four reproducers
   > compile to byte-identical binaries under both backends, so every other
   > workaround stays. The transpile is faithful — every reproducer compiles
   > byte-identically under the in-process flexcc and a natively built spin2cpp at
   > the same commit. One regression carried in knowingly: unary plus on a float
   > (flexprop#107) is a *warning* upstream where v7.7.0 refused it, and it
   > miscompiles; the emitter never writes one (`doc/unary-plus-float.c`).
   >
   > **Backend first generated 2026-07-20** against the `v7.7.0` pin (flexprop repo
   > and `spin2cpp` submodule both at `v7.7.0`) using **ccgo v4.34.6**;
   > `mcpp_main.c.diff` applied cleanly then and again in 2026-08. Post-regen chore:
   > `rm -rf internal/flexprop internal/flexprop_install` (git-ignored build clones
   > that otherwise break `go build ./...` / `go test ./...`). The flexcc `--help` golden in
   > `internal/flexcc/all_test.go` needs no manual refresh — its volatile
   > `Version … Compiled on: …` line is normalized (`versionLineRE`), so even a
   > version bump doesn't force a golden edit.
   >
   > **v7.7.0 changed nothing we depend on** (measured 2026-07-20). Every flexcc bug
   > the compiler worked around was re-measured against it, on a P2-EDGE, and every
   > one still reproduced identically: the dropped argument slot for an unnamed parameter,
   > the miscompiled `static inline` rendezvous, and the four compile-time refusals
   > around structs holding arrays and designated initializers. The upgrade is
   > hygiene, not a fix — keep the workarounds.
   >
   > The channel hang that used to head that list was **not** a flexcc bug at all.
   > It was a livelock in this compiler's own rendezvous, exposed rather than
   > caused by FCACHE; see `doc/rendezvous-livelock.c` and `chanRuntimeDefs` in
   > `internal/octogo/emit.go`. Builds no longer pass `--fcache=0`.
   >
   > **windows/amd64 added 2026-07-22** (`ccgo_windows_amd64.go`), cross-compiled on
   > linux/amd64 with MinGW (GCC 14) and the ccgo CLI v4.34.7 against the same v7.7.0
   > pin. `GOOS=windows GOARCH=amd64 go build ./...` is clean and `go vet` shows only
   > the usual generated-code noise. Verified on the windows/amd64 builder + a real
   > P2-EDGE: `ogo.exe build` produced a **byte-identical** binary to the linux flexcc
   > (same sha256), and `ogo.exe loadp2` detected the P2 on COM5 and transferred it,
   > `chksum: bc OK`. The cross pass needs `main2lib` fixups the linux one does not
   > (all gated on `goos == "windows"` in `main2lib`, so linux is untouched): two
   > codegen rewrites (a `libc.Xgetcwd` int32→Tsize_t length, and a miniz `~mask`
   > that folds to a uint32-overflowing -4096), plus redirects of the six libc
   > functions that are `panic(todo())` stubs for windows to
   > `supplement_windows_amd64.go` — `XGetModuleFileNameA`, `Xtime`, `Xungetc`,
   > `Xabort`, `Xstat` (real but forwards to the `Xstat64` stub), and the
   > `freopen`-of-`flexcc.go` split. The nine `--prefix-undefined=_` danglers
   > (`_remove`, `_powl`, `_strncat`, …) are also defined there. `ungetc` is done via
   > `fseek(-1)`, valid because flexcc only ungets just-read bytes of seekable source
   > files. The generated file is **not** gofmt-clean out of ccgo; the generator
   > gofmt's the per-target files itself before the fold, whichever path produced
   > them. The ccgo CLI version the cross passes use is the one `go.mod` pins for
   > the library (`go install modernc.org/ccgo/v4@v4.34.6` into a scratch GOBIN
   > first on PATH), so all five transpiles come from one ccgo.

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
  and value model), `format.go` (the `.ogo` pretty-printer behind `ogo fmt`).

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
semantic checks → emitted C → `internal/flexcc` → P2 binary → `internal/loadp2` →
board. That whole path runs today (`ogo build`, `ogo run`); WPO is the one stage
still design-only.

## Implementation status (where the TODOs are)

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
  (`-run <regexp>` selects which, compiling only those):
  it builds them with a generated runner, loads the result, and reads the verdict
  back over the serial line (the P2 returns no exit status). `-c` builds without
  running, which is what CI with no board can honestly do. A host mode was
  considered and rejected -- see `internal/build/test.go`.
  The `testing` package is EMBEDDED SOURCE, not an intrinsic: `embeddedPkgs` in
  `internal/octogo/build.go` maps the import path to ordinary OctoGo that is
  compiled and mangled like any other package. The day it ships on disk, the only
  change is where it is read from.
- **Interfaces are done and devirtualization is not.** A method call through an
  interface is an indirect call through a static vtable, always; the WPO pass that
  would make it direct where the concrete type is provable is design-only. Nothing
  is rejected for failing to prove one -- rejection is spent on lifetime.
- **Method values bind their receiver at compile time**, which is why they cost
  nothing that other function values pay. Go's representation (a value pointing at a
  struct whose first word is the code pointer) was measured on hardware and declined:
  `doc/funcval-cost.c` has the numbers, the attribution and how to revisit it. Do not
  re-open that without re-reading it.
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
  the package's directory within the root, and `os.DirFS(<root>)`. An import is
  then written as Go writes it, prefix and all, and `BuildContext.importDir` strips
  the prefix to get the directory. So `cmd/firmware` and `cmd/calib` both
  `import "example.com/proj/sensor"` and share ONE copy of it, and neither the
  importing package's location nor the working directory takes any part in it.
  It is deliberately **not** go.mod: searching for one would find *this repo's*
  go.mod from `_examples/` and adopt `modernc.org/ogo` as the root.
  **With no ogo.mod nothing changed** -- the directory being BUILT is the root and
  paths are relative to it (`import "sensor"`), which is what every test, the
  fuzzer and every `_examples/` program still does; `Build` is `BuildModule` with
  an empty module path.
  Two traps found while building this: the package's identity inside the compiler
  is its **module-relative directory**, not the path written to import it (module
  paths carry dots, which `cIdent` would hex-escape into the C symbols), and
  `importGraph`/`importTasks` must be keyed by that identity -- keyed by the
  written path while `fromPath` arrives as the identity, cycle detection connects
  nothing and an import cycle **deadlocks** instead of being reported. Importing
  the main package is refused for the same reason (`mainDir`).
  The whole program -- the main package plus every package it
  imports, transitively -- is emitted into **one C translation unit** in dependency
  order, with top-level symbols mangled into their package's namespace. `import
  "p2"` remains the one dotless, directory-less import, mapping to the hardware
  intrinsics; `testing` and `strings` are likewise bare, module or not. There is no
  standard library beyond those.
- **Two test suites.** `TestEmitCRun` builds each program in the `emitRunCases`
  table with the host C compiler and runs it against a pthread shim
  (`testdata/hostp2`). `TestOnBoard` builds the *same* table with the real
  backend and runs it on a real P2, gated on `OGO_BOARD_PORT` (`make board`).
  The second exists because flexcc and gcc have been observed to disagree on
  semantics, not just warnings -- a host-green emit feature is not verified.
- **A backend diagnostic fails the target-build tests, even when `ogo build`
  succeeds.** flexcc warns where it should refuse: given a duplicate declaration in
  one block it says `Redefining x`, ignores the second and produces a working binary
  holding the wrong value (that was `aa300e2`). So `boardBuild` treats any output
  from a *successful* build as a failure, which puts the check in `TestTargetBuild`
  -- no board needed, in the default `go test ./...`. A clean build is silent. A
  diagnostic examined and found harmless goes in the case's `backendWarning` field
  together with the reason; there is exactly one, in `empty struct type`.
- `smith` is seed-reproducible and generates compilable, self-checking programs.
  `ogo smith -seed N` emits the same program every run (the last non-determinism,
  map-iteration order in `Scope.GetSymbolsOfType`, was sorted), and `TestOracle`
  (`internal/smith/oracle_test.go`) generates a fixed seed corpus, compiles each
  to C and runs it on the host shim, so a miscompile (the program panics on a
  checksum mismatch) or a generator regression fails the test.
  **`TestGeneratorCoverage` guards the fuzzer's own blind spot:** a generator that
  produces *less* still passes `TestOracle`, which only checks that whatever was
  generated matches the VM. It asserts every construct still appears across a seed
  corpus. **Adding a construct to the generator means adding it to
  `generatedConstructs`** -- that entry is what makes its coverage tested rather
  than assumed. It catches what `staticcheck` cannot: a generator still called from
  a dispatch case whose probability range can no longer be reached. The earlier
  out-of-scope-loop-variable, bare-block and `panic` gaps that kept generated
  programs from compiling are fixed, and the generator now mints unique variable
  names (a counter, not a random suffix) so it never accidentally shadows. Widen
  `oracleSeeds` to hunt for new bugs.
  **Calls are counted** (2026-09-19): every generated function adds its weight -- a
  4-bit field per function -- to `octosmith_calls`, and main asserts the sum before
  the checksum. The functions are pure otherwise, so a call evaluated twice or not
  at all changed nothing the checksum could see: nine corpus programs carried a
  doubled call (`ch <- len(name())`, 3e2d5a1) and the oracle passed them all. The
  VM counts a call where the program makes it -- once per call whichever results are
  read, never in an `&&`/`||` operand the left one decides, and not in package
  initialization, main resetting the counter first. **A new call site in the
  generator calls `noteCall`**, once for each time the program runs the call.
- **Fixed miscompile (found by the oracle):** a shadowing local whose initializer
  references the shadowed name — `var x = x + 5` with an outer `x` in scope — used
  to miscompile, because the emitter names locals verbatim so the C initializer read
  the new (uninitialized) variable instead of the outer one. `emitVarDeclInit` (and
  the array/slice copy paths) now capture the initializer into a fresh temporary
  before the same-named C variable shadows it (`initRefsName` + `newTmp`), covering
  scalar, struct, array (`var a [N]T = a`) and slice (`var xs []T = xs`) forms; see
  the two shadowing `emitRunCases`. This is a targeted capture-before-shadow, not
  the general scope-aware C-naming layer the emitter still lacks.

## Test conventions

Semantic-check tests are table-driven over `.ogo` files in
`internal/octogo/testdata`, annotated with directives read by `tests_test.go`:

- `// COMPILE` — the whole file must type-check with zero errors.
- `// ERROR <regexp>` — the *next* line must produce an error matching `<regexp>`.

`etc.go` in each package provides the `todo()`/`trc()` position-tagged debug
helpers (guarded with `//lint:ignore U1000`); prefer them for temporary tracing.

## Probe harness (`scripts/`)

The method that finds most bugs is not the test suite but a PROBE: a program a
user would write, compared against real Go, with every accessor bumping a package
counter that is printed beside the values so a double or a late evaluation shows as
a number. The scripts are portable (paths from the repo root, tools built from the
tree being probed) and read from the top of each file. The one Go tool they build is
`scripts/dumpc` (DIR in, the checked C of TestEmitCRun out, a refusal on stderr). It
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

Process rules learned the hard way (2026-09-16): gate a commit on the suite log's
`exit=0` line and the absence of `FAIL`, never on `cat log &&`; make no edits to
`internal/octogo/*.go` while a full suite runs, since `TestTargetGoStack` and
`TestTargetBuildExamples` build `ogo` from the working tree as they go; a new
goroutine run case is run with `-count=5` before it is committed; and the receiver
that is a CALL's result (`bus.reg(i).M()`) has been the hole in six sweeps in a row
-- every receiver-shape sweep includes it, in a `defer` and a `go` too. A statement
sweep includes the statement as a `for` INIT and POST clause as well (2026-09-17):
the clauses had thinner lowerings of their own, and seven silent faults lived in them
and in the end-of-body placement behind the post -- a lost guard, an untyped shift
typed `int` in either clause, the body's variables read by the post, a `continue`
after an inner loop that skipped the post and never ended, init names stored one
after another, and a declared init name shadowing what its neighbour read. A sweep's
accessors record ORDER, `calls = calls*10 + k`, not a count: a count matched Go for
operands C leaves unsequenced. And a wide constant belongs in every sweep of a store:
`a, b = 1<<40, 5` lost it on the board in silence, where the host's compiler refused.
A sweep of DECLARATIONS changes the KIND of the name it shadows -- a slice over an
array, an array over a slice, a scalar over a struct, a variable over a constant, a
local over a package variable, in a block, a clause and a parameter list -- and has
the value read the shadowed name: the emitter's maps are keyed by source name, one
per kind, and twelve of twenty-four such shapes were wrong until a declaration
learned to forget the other kinds (`shadow`) and to read its value first
(`declareCopy`). The checker had the same trap (2026-09-19): the literal-capture rule
tested a literal's identifiers BY NAME, so it refused a literal's own `i`, a field
`p.x` and a key `P{x: 1}`, and let a package variable of a captured name through --
the literal read the package's x where Go reads the local. It is asked of the lookup
now (`Scope.find2` records what leaves a literal's scope, `litOf`); a rule about what
a name MEANS belongs where names are resolved.

Known open items, all loud refusals or design walls (2026-09-17): an
array-returning call as a package literal element; the method expression of an
INTERFACE type, `Shape.Area` (refused by name; a concrete type's works since
2026-09-19); a method value on a local or a call's result (design: a method value binds its
receiver at compile time); an unnamed struct type mixed with the SECOND of two
declared structs of its fields (its typedef names the first, aliasAnonStructs), which
Go admits and the target's compiler refuses; printf's `%v` of an
interface value (fmt prints what it holds, `&{1 2}` for a struct pointer) and of a
struct under a width; a named ARRAY result returned by name, `func f() (r
[2]int) { ...; return r }`; a `switch` or `for` init that declares an array, `switch a :=
[2]int{1, 2}; len(a) {` (the `if` form works); a PARENTHESISED HEAD the emitter cannot peel -- one holding a unary
operator or a suffix of its own -- read or written through a suffix: `(&p).x`,
`(&arr)[1:]`, `(get()).x`, `(*get()).x`, `(arr[1:])[1:]`, `("hello")[1:]`, the targets
`(p).x = 5` and `(&p).x = 3` and a conversion's, `(*T)(p).x = 5` and `*(*T)(p) = 5`,
and the send `(&bus.ports[1]).ch <- 5` (`(*p).x`, `(a)[i]`, `(v).m()`, `(&v).m()`,
`(*T)(p).m()` and `(a - b).m()` work); a conversion to a pointer to a type written
out, `(*[]byte)(p)` or `(*[4]byte)(s)` (refused by name; a named type's converts);
an ELEMENT or another package's variable as a `range` clause's target, `for _,
q[0] = range ptrs`, `for _, geo.P = range ptrs` ("a range target must be a variable
or a struct field"); a chain after a NAMED
string constant's slice,
`hexdigits[1:][0]` (the literal's works), and after a slice step of an array of
arrays, `grid[1][1:][1]` ("this combination of indexes and fields is not supported
yet"; the slice of slices' `rows[0][1:][1]` works). No grammar gap is
known: the ones recorded before all closed that day, and two nobody had recorded --
HeaderFactor had dropped the suffix from three of Factor's alternatives, and a string
literal took none at all, `"0123456789abcdef"[n&15]`. Two more surfaced the next day
from semantic batteries, not from the grammar: a select's comma-ok flag took a bare
name only, `case v, r.ok = <-ch:`, and a NAMED composite literal took no suffix, `P{1,
2}.x` (the bracketed one had). A battery that writes what Go programs write finds
these; reading the grammar did not. **Check Factor and HeaderFactor
against each other when either changes**; they are meant to differ by one production
(HeaderFactor has no literal after a name, which is what keeps `if x == T {` a block).
Two LATENT ones, measured and not faults today: a store through a chain, `r.m[a()][b()]
= v()`, leaves its calls to C's operand order, which gcc 14 and flexcc both take left
to right (only the bare `name[i] = v` path binds them); and a value's call does not
run ahead of an index or nil panic in the target, `arr[bad()] = side()`, where Go
runs `side()` and then panics -- the comment in emitIndexAssign says otherwise and
describes one C compiler's choice. Only a program about to panic can tell.

**The lifetime rules are asked of SHAPES, and a shape nobody asked about is a hole**
(2026-09-17, found while closing a grammar gap). `TestEmitCFrameRefForms` crosses
every form that binds a value with every kind of reference to the frame, each cell
beside a control over package storage; it found that parentheses hid a value from
every rule (`return (a[:])` compiled), that `var s []int = a[:]` recorded nothing,
and that no list form, destructured call, swap or `for` clause asked the rules at
all. **A new binding form, or a new way to write a value, is a new row or column
there** -- the same discipline `generatedConstructs` gives the fuzzer. The SINKS
have the same property (`TestEmitCCalleeKeepsEscape`, 2026-09-18): a deferred call
was checked at its replay, when the local was already forgotten; a function literal
had no escape summary; a callee storing into a local receiver left the local
unmarked; and a local pointer was taken for the storage it points at. A new way to
CALL something is a new row there. `TestEmitCFrameRefSinks` is the forms matrix turned
around, five kinds through eight sinks with controls; it found the summaries following
no helper that returns its argument, `g = id(v)`. A new SINK is a new row there.
A method KEEPING ITS RECEIVER is the same hole from the other side
(`TestEmitCRecvKeptEscape`, 2026-09-19): the summaries asked what a method did with
its parameters and never with its receiver, so `lc.Save()` on a local, where `Save`
stores `c` in a package variable, left a dangling pointer in silence while `keep(&lc)`
was refused. `recvLeaks`/`recvEdges`/`retRecv` summarise it now; a new way to REACH a
receiver -- a field, an element, a slice, an interface, a pointer -- is a new row
there.
What `make` allocates in a function is a backing array of the frame wherever the
slice is bound (`makeRef`, 2026-09-19; `TestEmitCMakeEscape`): only the declaration
from make was modelled, so a package variable given one from a function -- `main`
included -- held a view of a dead frame, and `s = make(...)` then `gs = s` compiled.
A SLICE has two marks answering two questions (2026-09-19): `frameBacked` says its
BACKING is this frame's, its holder mark says what its ELEMENTS reach
(`noteSliceElemRefs`, `sliceElemOrigin`). Only the first was kept, so an element read
out -- `g = s[0]` for `s := []*int{&x}`, or ranged over -- passed after a literal, a
copy, a reslice, a slice of a marked array, an append or a copy into it, and was
refused for the backing where only package variables were pointed at.
`TestEmitCSliceElemEscape` crosses the binding forms with element kinds; a new way to
bind a slice is a new row there. Writing a reference INTO an element of storage the
frame does not own is the same rule from the other side (`checkStoreThroughSlice`,
`checkCopyElems`, `checkAppendBacking`; rows in `TestEmitCSliceStoreEscape`).
The SUMMARIES follow a parameter through the callee's own locals and through every
shape a value is written in (`summaryRoots`, `summaryHolds`, 2026-09-19): they read
the parameter where it was named, so `w := v; gs = w`, `gs = v[1:]`, `gb = B{v}` and
a list, an append, a for clause or a send of any of them kept a view of the caller's
frame in silence. The analysis is by SHAPE -- no local has a type when summaries are
collected -- so how a value reaches a name has a KIND (`heldKind`): as its value, as
a part of something larger, or as its CONTENTS, an element or a field read out of it.
Contents are summarised apart (`crossContents`, `retContents`, `recvContents`) and a
call site asks what the argument's contents reach (`contentsRef`): `gp = v[0]` of a
`v []*int` refuses `keep([]*int{&x})`, while `gn = v[0]` of a `[]int` refuses
nothing. The kinds cannot be one: a copy's contents are the original's contents, a
struct literal's contents are the original itself, and taking one for the other
either lets a reference through or refuses every caller passing a local slice. A new
way for a callee to hold or write a value is a row in `TestEmitCSummaryThroughLocals`,
and a new way to read contents out of one a row in `TestEmitCSummaryContents`.
A call the summary pass cannot name by the callee's own name is followed too
(`TestEmitCSummaryIndirect`): an interface method is a call of every implementation
(`ifaceMethodCallsOf`), and a function value is followed through the holds to the
declared functions it may hold (`valueCalls`). A callback received as a PARAMETER
and called, `func each(v []int, f func([]int)) { f(v) }`, is recorded as a
`paramCall` -- which parameter is called with what of the others -- and the call site,
which knows the function it passes, asks that function's summary
(`checkCallbackArgs`); a callback handed on pairs the two edges of one call by their
`site` (`TestEmitCSummaryCallbacks`). A function calling its callback with its OWN
frame, `func each(f func([]int)) { var b [4]int; f(b[:]) }`, is recorded while its
body is emitted -- where `frameRefOf` answers exactly, which a shape cannot -- as a
`frameCall`, and every function handed to it is asked after all bodies are emitted
(`checkPendingCallbacks`), since the call site may come first.
A call through a function VALUE nothing can name -- a package variable set somewhere,
a field, an element, a call's result, a variable written on more than one path -- is
judged by EVERY function of its type the program uses as a value (2026-09-19;
`typeSummary` over `funcValueMembers`, collected by `collectFuncValues`: declared,
another package's, literals, method values, method expressions). Whatever reaches a
function value was one of those once, so the union is sound; it is by TYPE, so a table
of harmless functions is refused when a keeper of that type is a value anywhere, and a
per-location may-hold analysis is how that price would come down. A binding
`bindFuncValue` records is believed only where `boundFunc` says nothing can have
changed it (`scanBindings`): written once, or only by plain assignments in one block
of a function without a goto, never address-taken or method-called, never a package
variable -- before, a rebinding in a branch, a loop, a select or through `&f` left the
first binding in force. `TestEmitCFuncValueUnion` (and `...Packages`) is the matrix: a
new place a function value can live, or a new way to write one, is a new row there.
The SUMMARIES follow such a call too (`TestEmitCSummaryFuncValues`): a "type:" callee
is an edge target like a function (`typeCallee`), its union refreshed on every pass of
the fixed point as its members' summaries grow (`unionSummary`). This pass has no
locals, so it types what it can -- a parameter (`funcParams`), the receiver, a package
variable -- and a local through what it holds; a value only a call's result type
names is held as `?<type>` (`summaryCallResult`). A deferred call and a literal called
where it stands are calls here as well; neither was, so `defer keep(v)` and
`func(w []int) { gs = w }(v)` laundered a parameter. Still open: a call through a
method's result, `s.handler()(v)`, and a chain whose base is a literal's parameter.

## Notes

- `specs.go`'s doc comment is the authoritative **language spec + grammar**;
  `internal/octogo/octogo.go`'s doc comment is the authoritative **compiler-internals
  design** (check phases + WPO). `web/` holds logo assets only — the landing-page
  outline that used to live there was deleted 2026-07-18 (it was a raw LLM chat
  transcript pitching a paid-license model that the BSD LICENSE and the README's
  GitHub Sponsors tiers had already superseded). (`gem.md`, the original Gemini
  design corpus, was deleted 2026-07-10; its unique, still-unreconciled bits are
  salvaged in the appendix below.)
- Requires Go 1.25+ (uses iterators, `maps`/`slices`, range-over-func).

## Appendix: salvaged design notes from gem.md (unreconciled — process later)

> **Status: historical design intent, NOT current authority.** Extracted from
> `gem.md` (a Google Gemini "Gem" design corpus) before it was deleted on
> 2026-07-10. It predates the `octogo → ogo` / `.octo → .ogo` rename. Most of
> gem.md already migrated into `specs.go` (language spec) and
> `internal/octogo/octogo.go` (checker/WPO design); the items below are the ones
> that lived *nowhere else*. Reconcile against those two files before acting on
> any of this, and delete each item from here once it has been folded into the
> real spec or implemented.

### 1. ~~Open decision — WPO interface handling~~ — SETTLED 2026-08-03

Resolved in favour of **static vtables** (the old option C), with monomorphization
kept as a per-call-site optimization rather than a language rule. The reasoning, the
representation, and what the checker needs first are now in
`internal/octogo/octogo.go`, which was rewritten rather than amended -- it had
described option B in detail, and two documents disagreeing about a design is how
this repository has produced bugs before.

The deciding argument, worth keeping because it generalizes: the governing rule is
that what the compiler cannot **prove** safe may be rejected, and the handled set may
grow over time. Monomorphization rejects programs whose concrete types ARE provable,
because its representation cannot hold two of them -- rejecting what it can prove is
the opposite of that rule, and growing past it is not an increment but a change of
representation. So the representation must not depend on the analysis succeeding;
rejection is spent on lifetime, where proof genuinely fails.

### 2. `p2` standard library — 1:1 mapping to flexprop C intrinsics (PARTLY BUILT)

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
| `p2.WaitCycles(n)` | `_waitx` | `p2.Rnd()` | `_rnd`→uint32 |
| `p2.Rev(x)` | `_rev`→uint32 | `p2.Reboot()` | `_reboot` |
| `p2.SetBaud(n)` | `_setbaud` | `p2.ReadByte(t)` | `_rxraw`→int |
| `p2.WriteByte(b)` | `_txraw` | | |
| `p2.NewLock()` | `_locknew`→int | `p2.TryLock(l)` | `_locktry`→bool |
| `p2.Unlock(l)` | `_lockrel` | `p2.FreeLock(l)` | `_lockret` |

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
structure; a single-producer/single-consumer ring buffer needs no lock and already
works (verified on the board), though it leans on the backend not hoisting the
shared index out of the poll -- there is no `volatile` in the language.

~~A `select` waiting on a Smart Pin~~ SETTLED 2026-09-04: no pin clause. The
default-arm polling idiom (select over channels, `default:` polls `p2.PinIn` +
`p2.AckPin`) is the supported form -- hardware-verified in `_examples/pinselect`
and specified in specs.go's select section. Revisit only if real programs show the
idiom wearing badly.

### 3. Dropped CLI split: `compile` vs `build`

gem.md intended two backend commands: `ogo compile` (emit intermediate C + headers
only) and `ogo build` (wrap flexprop to produce the P2 binary). Current `main.go` has
only `build`. gem.md itself flagged the doubt — *"with WPO `compile` might be no
[longer] possible"* — because whole-program optimization fights per-package separate
compilation. Decide `compile`'s fate deliberately when wiring the backend.

### 4. Misc hardware / codegen intent

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
