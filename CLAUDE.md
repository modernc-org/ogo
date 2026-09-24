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
strict LL(1) grammar" -- it took one extra production, 2026-09-18). So was the bare
conversion `[]int(x)`, parenthesised by rule for the parser's sake until an
alternative measured at one First/Follow decision, settled as Go settles it, took it
(2026-09-21). **Before treating any such note as settled, check whether the
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
   transpile; a fix carried ahead of upstream goes in the same list, as
   `optimize_ir.c.diff` did from 2026-09-15 until upstream's own landed -- and goes
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
   linux/other-unix counterpart, tagged `!windows && !darwin`); see the windows note
   below. Darwin has its own `internal/flexcc/supplement_darwin.go` (the libc
   functions — `stpcpy`, `wcrtomb`, `asctime`, `fseeko`/`ftello`, `powl`/`frexpl`,
   `freopen`, and the `ungetc`/`abort` todo-stub redirects — that libc lacks or
   stubs for both darwin arches).

   > **Backend regenerated 2026-09-21 at spin2cpp `v7.7.3`, and the carried diff is
   > gone** — `eb263961`, the tag flexprop's master points at, inside flexprop
   > `v7.7.0`, ccgo v4.34.6. Upstream fixed all three faults `optimize_ir.c.diff`
   > carried: #109 and #110 closed 2026-09-20, and #111 was fixed by spin2cpp
   > `3911e536` while the issue was still open. The fixes are upstream's own, not the
   > suggested ones: #109's leaves an instruction alone when it SETS C, or merges no
   > pair where either sets a flag, read or not (broader); #110's asks only about the
   > flags the READ's condition uses (narrower, and exact, since CondIsSubset has
   > already said the read runs only where the write did); #111's is the same. So
   > they were measured before adoption, native builds of the old pin with the diff
   > against v7.7.3 without it: on a P2-EDGE the three reproducers, and the two older
   > ones that are #109, print gcc's values; the 34 compiling `doc/` reproducers are
   > byte-identical and the four refusals refuse alike; of 1632 programs (the 632 run
   > cases, fuzzer seeds 1-1000) 1619 come out alike, 35 of them a "fit 480" refusal
   > under both, and 13 keep two immediate adds setting a carry nothing reads, which
   > the diff merged (8 bytes) -- all 13 pass on the board under both; test_offline
   > 588/588 under both. Faithful: the regenerated flexcc builds the battery and all
   > 1632 exactly as a native v7.7.3 does, the refusals included.
   >
   > **All five platforms, the same day**: linux/amd64 and windows/amd64 on the
   > second dev machine, linux/arm64 on `rpi5` and both darwin backends on
   > `darwin-m1` from the first, the one that reaches the builders -- each builder's
   > 2026-09-15 clone removed first, since the generator then reused any clone it
   > found (it refuses one at another pin now, `checkClone`).
   > `scripts/flexcc` built for each platform compiles doc/ and a corpus dump, 1670
   > programs, to five identical `scripts/cccorpus.sh` lists, equal to a native
   > v7.7.3's (windows's under wine, its builder being off). Host suite and `make
   > board` (657 run cases) are green on linux/amd64.
   >
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
  re-open that without re-reading it. The binding is an ADDRESS, so a receiver Go
  would SAVE is refused: a value receiver (Go copies it), a local (its address dies),
  and since 2026-09-23 a POINTER -- a pointer variable or an embedded pointer the
  method is promoted through (`methodValueSavesPtr`, checker and emitter): a pointer
  variable's bound the pointer's own address and printed garbage on the board, and an
  embedded one was read at each call rather than when the value was taken. A function
  literal calling the method is the spelling that reads the pointer at each call.
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
  intrinsics; `testing`, `strings`, `bytes` and `math` are likewise bare, module or
  not. There is no standard library beyond those.
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
  `oracleSeeds` to hunt for new bugs. What was swept is swept FOR A GENERATOR: a
  change to it makes every seed that draws the changed choice a new program from
  there on. On 2026-09-22, all clean: with cbcba6a's generator, seeds 1-2000 on the
  host shim; with the one before it, 1001-3000 on the host and 1001-1500 on a P2-EDGE
  (482 passing, 18 outgrowing a cog). A generated program writes self-comparisons and
  `2 ^ 3` on purpose, so it is built with the oracle test's gcc flags, not probe.sh's
  `-Wall -Werror`: under those, 273 of 2000 fail on the program's own warnings.
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
  PROGRAM GO REJECTS IS A ROW" below): every PARENT/NAME/main.ogo through `go build`
  and through the tree's compiler, a row wherever the two disagree, and the exit
  status 1 when the compiler FAULTED on any. `DUMPC=` takes an older commit's dumpc,
  which is how a sweep's finds are counted after its fixes are in.
- `scripts/capped.sh [-t SECS] [-m KB] CMD...` -- a memory cap and a SIGKILL timeout
  around one command. probe.sh, stmtprobe.sh, rejects.sh and dumpcorpus.sh run the
  compiler through it; an ad-hoc loop over probe programs must too. dumpcorpus.sh did
  not until 2026-09-22 -- sixteen uncapped compiles at a time, and a crashed one
  deleted like a refused one -- and was the command running when the machine was
  next exhausted; rerun capped afterwards it measured small (~20 MB a seed, 880 MB
  for a fresh compile of the test binary) and found no fault, so the cause was never
  shown to be it. A seed that FAULTS leaves smithNNNN.fault now.
- `scripts/flexcc` + `scripts/cccorpus.sh [-I INC] [-k KEEP] FLEXCC OUT DIR...` --
  the tree's in-process backend as a command, and every C file of some directories
  compiled by ONE flexcc into sorted `NAME RC SHA` lines; `LC_ALL=C join` two lists
  and the lines that disagree are the programs two backends build differently. How
  a backend regeneration is measured (2026-09-21): FAITHFUL to a native build of its
  spin2cpp commit (`-I` that checkout's include/), against the backend it replaces
  (which programs to run on the board), and across the five platforms, with
  scripts/flexcc built under GOOS/GOARCH -- over doc/ and a dumpcorpus.sh dump, about
  1670 programs in four minutes.

**A COMPILER RUN IN A SWEEP IS CAPPED, AND A CRASH IS NOT A REFUSAL** (2026-09-20).
A probe program is written to find a fault, and a fault is not always a wrong
answer: `type A struct{ A }` sent the emitter down an embedding at 4 GB a second, the
sweep ran the compiler bare, and the machine froze for four hours -- a plain
`timeout` does not help, the process being too deep in swap by then to die when
told. `ulimit -v 4000000` does (1.5 GB is too little for a Go program to start its
threads): the runaway dies in half a second with the looping stack on stderr, which
is the diagnosis. And dumpc's exit status is the verdict -- 0 compiled, 1 REFUSED,
anything else a COMPILER FAULT. The sweep had asked whether stderr was empty, so the
compiler that crashed AGREED with Go about a program Go rejects, and the row that
froze the machine was logged as one more agreement. The first probes written under
the cap found three more crashes on LEGAL programs the same afternoon.

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

**A DEFINED TYPE OVER U IS A ROW** (2026-09-20). `type D U` for every underlying
kind -- int, float, bool, string, slice, array, struct, pointer, func, chan,
interface -- crossed with the ways one is declared (package and local, written and
inferred, from a literal, a make, a conversion and a zero value) and used (a method
on it, an index, len, a field, a call). Thirteen programs; six faults in two of the
rows and one in a third. A defined SLICE type was three different things at package
scope -- refused from make, "cannot infer a type" with no type written, and
`var g L = L{1, 2, 3}` compiled to a HEADER of {1, 2, 3}, a slice pointing at
address 1 -- a short declaration from make lost the type and with it the methods,
and make in a package initializer crashed the compiler. A defined INTERFACE type
refused every value going into it, and a defined POINTER type lost its pointerness
through a short declaration. The local declared forms, which is what everything
written until then used, were right in every case. A conversion sweep beside it
(`T(x)` for each of those types, in a declaration, a call, a chain and under a
suffix) found only the recorded grammar gap: a conversion to a type WRITTEN OUT,
`[]int(x)`, `[]byte(x)` and `[3]int(x)`, was a syntax error, where a named type's
converts (the bare form parses since 2026-09-21). The row has a second column,
OPERATIONS (2026-09-21): a value of a defined type kept it where the type was
written and lost it through anything that produced one -- a reslice, append, a
call's result -- being typed as the header or the string underneath. So its methods
were gone (`c := l[1:]; c.Sum()` read as a package qualifier), and `%T` printed
`[]int` for a main.L in silence. The chain walk carries a defined slice type in
`accessCur.name`, the field a defined array already had for the same reason.

**A PACKAGE BOUNDARY IS A ROW** (2026-09-20). Everything that can cross one, each
declared in a library package and used from a program: an interface satisfied by a
LOCAL type, an embedded struct and its promoted method, an error, a method value and
a method expression, a channel, a constant as an array bound, a defined slice type,
a struct as an array element, and printf's `%v` and `%T` of each. All match Go, on
the host and on a P2-EDGE -- the one fault the sweep found was an ELIDED literal
filling another package's unexported field, which is a hole in the literal check
rather than in the boundary. The harness is `dumpc`, which reads an `ogo.mod` above
the directory; before that the dimension could not be probed on the host at all.

**A PROGRAM GO REJECTS IS A ROW** (2026-09-20). The sweeps above ask what a correct
program does; this one asks what an incorrect one earns, which is the direction that
fails SILENTLY -- an accepted mistake reaches the C compiler, which reports it about
generated code, or does not report it at all. Seven batches of about thirty, 212
programs, each through Go and through this compiler (`scripts/rejects.sh`). The
count below is a RECOUNT against the compiler as it stood before the sweep
(4906d4f): the first one judged by `go vet` and by whether stderr was empty, and
was off in both directions. 186 agreeing; 8 refused where Go takes them -- 7 BY
DESIGN (new, map, len and cap of a channel, a VALUE stored in an interface, which
holds a pointer here, and unreachable code twice, an error here where `go vet`
reports it, as specs.go says) and one a gap, the no-op `s = append(s)`; one compiler
FAULT; and 17 programs of 13 shapes taken where Go refuses them -- an append of the
wrong element type, the integer-only operators on a float, the address of a call's
result, a constant declared a type that cannot hold its class, a struct converted to
a number, a fallthrough in a type switch, a send to an unnamed element type, a
string written into, a struct indexed, a channel compared with a number, a make
whose length exceeds its capacity, a range over what has no elements, and a
conditionless switch whose case is not a boolean. Every one
of them reached the C compiler, as a diagnostic about generated code or -- the make
-- as C that is valid and wrong. Keep the two-column shape (what Go says, what this
says) and add a row whenever a check is written. The fault did NOT reach the C
compiler, and is the row to remember: `type A struct{ A }`, a struct EMBEDDING
itself (and any LOCAL type holding itself), which the recursive-type walk did not
follow and the emitter followed without end -- 4 GB a second until the machine that
compiled it froze, for four hours. The sweep logged it as a refusal. Run over the
same 212 programs after the sweep's fixes, the script also found two of them STILL
taken, each a neighbour of a shape that was fixed: `append(ps, nil)` into a slice
of STRUCTS, and `for range f` over a LOCAL func value. Re-run a batch after its
fixes; a fix is for the program that was looked at. The second was the visible end
of something larger: a variable bound to a function LITERAL had no type at all to
the checker, so `f("x")`, `f(1, 2)` and `f = otherSignature` went through as well
(both fixed the same day, funcLitSig and checkNilValue; the 212 stand at 204 agreeing,
none taken, and 8 differing -- the 7 by design and `s = append(s)`). **What `ogo build` does with such a program is the
measure**, not what gcc does: of twelve accepted mistakes built for the target that
afternoon -- `gp = nil` for a struct, `return nil`, `take(nil)`, `if p {` for a
pointer, `f + 1` and `f[0]` for a func value -- flexcc refused ONE and warned about
one; ten built a binary in silence. **And measure with a struct of MORE THAN ONE
WORD, and RUN the binary**: those nil programs used `type P struct{ x int }`, which
flexcc treats as the int it is made of, and what they "did" was inferred, never run
-- and was published wrong, in the changelog, a comment and the release notes. With
three fields flexcc REFUSES the stores ("Expected multiple values"), builds `return
nil` in silence with the caller receiving garbage, and warns about `take(nil)`, the
callee reading garbage. A claim about what a binary does is a claim to measure on the
board before it is written down, however obvious it looks.

**WHAT HAS NO KIND IS ASKED NOTHING** (2026-09-20). The checker's type model is a
Kind -- a predeclared type -- and most of its rules are gated on one: `if k, ok :=
f.exprType(s, e); ok && ...`. A pointer, a function, a channel, a slice, an array, a
struct, an interface and nil have none, so the gate answers "unknown" and the rule
says nothing, which is the right answer to "I cannot tell" and the wrong one to "this
is a struct". Each Kind-less category got its checks one position at a time, as
someone met it -- checkPointerRelOp, compositeOperandMismatch, chanOperandMismatch,
checkFuncAssign, checkRangeable -- and a position nobody met is a hole. Sweeping a
RULE across the categories, rather than a category across positions, found three rows
in one afternoon, each taken in every cell: a CONDITION (`if p {`, `!f`, `ch && ok`:
28 shapes, nonBoolOperand), NIL into a struct or an array (14 places, checkNilValue),
and a function LITERAL, which had no type as a value (funcLitSig). So: **when a rule
is written against a Kind, ask what it says of each category that has none**, and run
the row through `scripts/rejects.sh`. Two traps met on the way. The helpers that name
an expression's type carry ONE level of pointer, so `*npp` for a pointer to a pointer
reads as the struct two steps away: a new refusal asks a bare variable's DECLARATION
(varIsValueComposite), and believes an inferred type only where the initializer does
not dereference. And the corpus guard -- `scripts/dumpcorpus.sh` before and after,
through the checker -- shows a newly refused program as a MISSING file, not as an
`.err`: compare the listings. The function-value column and `if *p {` of a struct
pointee, left open that day, closed the next with the sweep below.

**FIFTEEN RULES ACROSS THE CATEGORIES** (2026-09-21). The lesson above, taken whole:
every operation crossed with every Kind-less category -- a pointer, a function, a
channel, a slice, an array, a struct, an interface, each at package scope and in a
function, and nil -- 217 programs, 91 of them taken and 22 more refused only by the
emitter. Whole rows, not cells: `x++` for all seven categories, arithmetic and shifts
for six, a value of a Kind stored into a function, a channel or a struct, an ordering
of a function or a channel, `int(x)` of four. All refused now (90e872f..e47e705), and
so are the neighbours the rows led to -- a string's `s++`, a bool's `b &= false`, a
float's `x %= 2`, the other direction of every store (`n = f`, `take(gp)`, `return
xs`), and the result of a call through a function value, typed at last. One helper
answers "what is this operand" for every rule, `nonBoolOperand` (with `nonBoolVar`
for a declaration), so a shape taught to it -- a dereference, `*p`, was the last --
is taught to all of them at once; which also means a MISREADING there reaches every
rule at once: `v := []int{4, 5, 6}[1]` recorded the literal's element on v, v was an
"array or slice" to all of them, and only the run corpus showed it (exprLitElemKind).
Three traps in writing such a sweep. DISCARD the result, `_ = x + 1`: a printed one
was refused by the emitter ("cannot print a value of type P"), which made cells look
refused that the checker took. A local only STORED to is "declared and not used" in
both compilers, which agree for the wrong reason: add `_ = x`. And run the corpus
guard after EVERY rule: it caught two false refusals the 212 and the sweep could not,
a spread `sum(xs...)` checked as an element, and the literal element above. What v0.42.0
built of the 167 programs it took (measured with `ogo build`, the struct probes of ONE
word): 60 binaries without a word, 20 with a warning, 87 refused by the backend about
generated C. Still loud rather than checked: a string from a []byte or []rune (legal
Go, refused by design as the allocation it needs) and a value stored into an
interface (by design). An inferred `xs == ys` of two slices was a third until
2026-09-22: a variable with no written type was "an array or a slice", not one
category, until its initializer was asked which (sliceOrArrayOf).

**A CASE WAS A POSITION NOTHING WALKED** (2026-09-21). The rules above cross
operations with categories; they say nothing of a position no rule is ever asked in.
A case of an expression switch was one: folded to find a duplicate, asked for a
Kind, and walked by nothing else, so `case get(1, 2):`, `case f[0]:` and `case T:`
went through, and `case one:` in a switch on an int built a binary in silence. A case
is walked as a value now (checkCaseValues) and compared with its tag
(checkCasesAgainstTag). The trap met on the way: the checker takes a type switch
apart only when its operand is a bare NAME, so `switch v := xs[i].(type)` had been
read as an expression switch whose cases nobody looked at -- walked, its types were
refused as values, and only the run corpus showed it (typeSwitchShaped). So: **when
a rule is written, ask which positions its walk never reaches**, and find them by
writing mistakes INTO each position, not by reading the walk. Done afterwards for one
mistake the checker knows, too many arguments to a call, written into 57 positions a
value stands in -- select clauses, composite literal keys and elements, range
expressions, defer and go arguments, every for and if and switch clause, sends,
index stores, compound assignments, closures, return lists, package initializers --
it was the checker's in every one but a literal's KEY, which the emitter refuses as a
non-constant index, as Go does too. The case had been the only hole.

**A FUNCTION VALUE'S CALL IS A ROW** (2026-09-22). A call through a function value
asked its parameters of a DECLARED callee's tables, which a value names only where
the summaries bind it to one -- so an interface argument went unwrapped, nil to a
slice unconverted, a forwarded multi-result call was miscounted, and a VARIADIC
value's arguments went out unpacked: `f(1, 2, 3)` printed 2 on the board, three ints
read as a slice header. Every call through a value now hands its type's parameters
and its typedef to the arguments (`valueArgsCText`, `callFuncType`), and a variadic
function type is a typedef of its own. The same sweep found a BACKEND fault: a
function NAME passed to a call through a function pointer reaches the callee as
garbage on the P2 (`doc/funcptr-arg-to-indirect-call.c`), worked around by binding
it first (`indirectFuncArg`). Sweep a new call shape through every place a function
value lives -- a variable bound once and twice, a literal, a parameter, a package
variable, a table's slot, a field, a call's result, deferred and on a cog -- and RUN
it on the board: the host was right about every one of these. A DEFERRED variadic
call packs its captures at the replay, and a GOROUTINE's in its trampoline, on its
own stack (2026-09-22); a replay writes what it binds ahead of itself
(`emitReplayedCall`), which is where a pack's array had gone missing.
A board sweep of where function values LIVE, the same day, found two more backend
faults, each silent and each right on the host. A subscript through a POINTER to
function pointers drops its index -- every slice of functions read and wrote
element 0, `doc/funcptr-subscript.c`, whose cause is one line of spin2cpp's
outasm.c with a fix measured natively -- so a function element of a slice is
spelled `(*(s.ptr + (i)))` (`elemAtC`); arrays were fine, which is why the run
cases never saw it. And an argument through a function pointer whose type leaves
its parameters UNNAMED is not converted, so `h(3)` for a float64 parameter
arrived as the int's bits (`doc/unnamed-param-no-conversion.c`); every function
type the emitter writes names its number and bool parameters (`cFuncTypeParams`),
and not its structs, which named are passed wrong the other way. Two emitter holes
the same sweep led to, both in the HEAD of a call: an element called through a
pointer to an array, `pa[k](3)`, indexed the pointer (chainCText named the head
where accessBase had entered the array); and `(*p)` before steps was taken for `p`
whatever followed, which Go allows only before a selector or an index through a
pointer to an ARRAY (`derefShorthand`) -- `(*ps)[i](x)` for a slice came out as
`;`, the call gone, because the call paths' false was ignored at nine sites. They
refuse now (`callOrFail`): a false there is a shape nothing lowered.

**A LABELED BREAK REFERS FROM ANY DEPTH** (2026-09-22). Go's terminating-statement
rule for a for, a switch and a select is that no break REFERS to it, and a labeled
break refers to it from inside a nested select, switch or loop. The checker counted
only an unlabeled break at the statement's own level (`containsBreak`), which was
wrong in BOTH directions at once: the statement after `loop: for { select { ... break
loop } }` -- the usual way out of a select loop -- was refused as unreachable, and a
function ENDING in such a loop, which Go refuses as "missing return", compiled with no
return on the break's path and returned 0 on the board in silence. Found by a board
sweep of control flow, not by the rejects batches, which had no labeled break in a
select. A select was no break target at all (`labelSelect` since). A termination rule
is a rule a missing-return sweep and a reachability sweep BOTH exercise: write each
new shape into both.

A parenthesised ADDRESS is peeled by the paths that lower it, `(&v).f` as `v.f`, and
the peel asked nothing of v: `(&pp).x = 3` for a pointer, `(&ps)[0] = 1` for a slice,
`(&f)(1)` and 11 more shapes Go refuses compiled as though the & were not there
(`addrStepRefused`, `TestEmitCAddrStepRefused`). A head that is a CHAIN, `(&h.v).x
= 9`, `(&h.a)[0] = 8`, `(&ga[k]).x = 6` and `(&h.v).m()` as a statement, was refused
outright and is peeled the same way since (`addrChainSteps`): the steps that reach
the addressed value lead the ones written after it, and what the CHAIN reaches
decides whether the next step is Go's (`addrChainStepRefused`). `(**ppp).x = 5` is
the one left, refused as "only assignment to a simple variable is supported yet".

**NIL IS A ROW** (2026-09-22). nil alone is the null pointer and a slice is a
header struct, so every position has to say which nil it is -- and the positions
were fixed one at a time. Writing nil into every place a value stands (a
declaration, an assignment, a field store, a literal member, an argument, a
variadic element, a return, a comparison), for every nil-able type (a slice, a
DEFINED slice type, an interface, a function, a channel, a pointer), found four
places that took the null pointer for a header: a defined slice type's return,
assignment, field store and argument, plus a variadic element, and a literal member
of any slice type. The target's compiler refused each ("Expected multiple values",
"incompatible types in assignment"), so these were build failures rather than wrong
answers -- but nothing in the tests held one, since gcc refuses them too and no run
case can. A rule about a REPRESENTATION (a header, two words, a copied array) is a
rule to sweep across the positions, since each position writes the value itself.

**A VARIADIC CALL IS A ROW** (2026-09-23). The pack a variadic call builds is
written by every place a call can be made -- a direct call, a function value, an
interface slot, a deferred call's replay, a goroutine's trampoline -- and each
wrote it on its own. Sweeping one REPRESENTATION, an array, through them found all
five broken for `xs ...A` (the callee copied the parameter as though it were an A,
and every pack assigned arrays; `packStoreC` copies them), and the written `...[3]int`
refused outright (`variadicElemCType`). The interface slot never packed at all, for
any element (`ifaceMethod.vararg`, `callVararg`), and a deferred or started call
through an interface did not compile in any shape (`deferredCall.ifaceMethod`,
`goSite.ifaceMethod`). The keeper check that makes a pack safe has to follow each
new place a call is made -- it did not follow the interface slot until the pack
existed -- and an EMPTY pack is the nil slice, which the check read a position off
and crashed the compiler on (`keep()`).

**A TARGET IS A POSITION** (2026-09-23). A store is asked what it stores where the
checker knows the target's type -- a bare name, one field, one element, the pointee
of a Kind -- and a target reached through MORE steps was asked nothing at all: `h.s.n
= "x"` put a string into an int as far as the C compiler, `h.s.f = 5` built for the
target with a warning and called address 5, and `h.s.n *= 1.5` built without a word
and multiplied. So were the second target of a list, a pointee of no Kind (`*pf =
5`) and a range clause's `=` targets. A target is walked from its base variable's
type now (`targetTypeNode`) and asked what a literal's field is asked
(`checkStoreInto`); a range target by `checkRangeAssign`. A function's SIGNATURE was
the same hole along the other axis: asked by a declaration, an assignment to a
variable and an argument, and by no return, send, literal, field or element store,
append or package initializer -- a return and a literal's field, built with a warning
and run on the board, called a function of two ints as one of a pointer and printed
garbage (`checkFuncAssign` is asked at every one since). A
slice declared by `:=` from a literal of functions or structs recorded no element
type, only a Kind -- it records the written type now, and that exposed the value
ranged out of a table of a named function type as "cannot call non-function h",
which a DECLARED table had always been (`rangeValueFunc`). So: **a new store rule is
asked at every site its siblings are** -- `grep -n 'checkChanAssign(\|checkNilValue('
check.go` lists them -- and a new target shape is a row in the target batch.

**A DEFERRED OR STARTED CALL IS A ROW** (2026-09-23). Go evaluates a deferred
call's function and arguments where the defer stands, and a goroutine's at the go
statement; `deferReceiver` and `emitGo` capture what they RECOGNISE, and a callee
they do not is left to the replay -- read at the return, or on the cog when it runs
-- which is a wrong answer, not a refusal, whenever something between changes it.
Three such callees were found by storing into the callee after the statement: a
function field at the end of a CHAIN on a package variable (`defer gh.in.f(4)`, 89
for 84 on the board), and another package's function VARIABLE under both statements
(`defer lib.Hook(1)`, `go lib.Launch(2)`, 89 and 9 for 81 and 2), which were taken
for function names. And a NEW expression shape becomes deferrable through the replay
the moment it compiles: `(pick())(x)` did, and ran pick() at the return, until the
defer learned to capture it the same day. So **a new callee shape is swept under
plain, `defer` and `go`, with a store into the callee after the statement** -- a
variable, a package variable, another package's, a field, a field of a chain, an
element, a call's result, a parenthesised value, a received one.

**A DOMAIN PROGRAM IS A PROBE** (2026-09-23). Three programs written as a user
would write them -- a tokenizer with a recursive-descent evaluator, a sensor
pipeline behind a filter interface, a worker pool of cogs selecting over jobs and a
quit channel -- found what a day of sweeps had not: a false lifetime refusal, the
error copied out of a parser whose OTHER field pointed at a local lexer (every
struct type counted as able to carry a reference, carriesReference; a struct carries
what its fields do now, an interface field included -- which the first version of
the fix forgot, opening a hole the escape tests caught), and a grammar gap specs.go
had recorded as "the form provided", `if s, keep = f.Apply(s); !keep` (an if or
switch init takes "=" now). Following the gap found the for clause's assignments
checked for nothing. The same afternoon's classic-semantics probes all matched Go on
the board: a range over an array iterating its copy, tuple assignment order,
overlapping copy and the append-removal idiom, struct equality over padding, a
deferred call changing a named result. The worker pool's first version sent a job
with a payload, `struct{ id int; data [4]int }`, refused as a channel's element
(works since), and a second round the same day -- CRC tables, vector math, an event
queue, a config parser, an embedding hierarchy -- found the CRC check string
`[]byte("123456789")` refused as an allocation (a constant's converts since), and one
SILENT fault: `&o.Base` into an interface held `&o` under the OUTER type's table,
calling the outer type's method where both implement it.
ifaceOperand had typed the address by `addrOfRoot`, which answers which variable's
STORAGE an address reaches -- the lifetime question, and right for every other
caller -- where the type is what the address ADDRESSES (`addrOperand`, a bare
name's; anything longer is a pointer expression). So: **write a program a user
would write, and when it hits something, sweep the row it belongs to.**

**AN ARRAY RESULT IS A ROW** (2026-09-23). A call returning an array is a STATEMENT in
C -- the caller hands the callee storage to write -- so every position one stands in
has to supply that storage, and one probe after another the same afternoon found
positions that did not: a return copied its operand after the defers had run
(silent: `return ga` returned what a deferred write left); a call whose value nobody
reads was refused, or -- through a chain, deferred, on a cog -- sent without the out
parameter, which the target only warned about while writing the result through the
next argument; an interface's slot refused one outright; a deferred or started
call's argument refused one. And the deepest, the ORDER: the storage is bound ahead
of the statement, so the call ran ahead of every value the statement evaluated
before it -- in a call's arguments, a print's, a return list, a deferred call's and a
goroutine's -- because the TYPING walk bound it as it typed it, and the lists that
bind their values in order (hoistArgs, a defer's captures, a go statement's slots)
left it to a path that bound it first. **A walk that answers a question must not
change the program**: `arrayResultCallSteps` types without binding, and binding
happens once, where the value is emitted. The same sweep found a
return with a defer storing its named results one after another, `return b, a`
returning 2, 2 for every type: a list of stores is a simultaneous assignment, which
`emitSimultaneous` knew and the return did not (`returnValueStands`).

**A STRUCT HOLDING AN ARRAY IS COPIED, NEVER ASSIGNED** (2026-09-23). The target's C
compiler copy-initializes and assigns one only at some SIZES -- "Unable to multiply
assign this target" at 12 and 16 bytes, where 3, 8 and 20 build -- so a test struct
of the wrong size says nothing: a variadic of 8-byte ones built and one of 12 did
not. A sweep of 26 positions at 12 bytes found five that assigned (a range value, a
list's temporaries, a variadic pack, append's helper, a blank target's temporary),
each copying with memcpy since (`holdsArray`), and passing and returning by value
fail at every size (`byRefParam`). Measure a struct rule at 3, 8, 12, 16 and 20 bytes.
The boundaries came down the same day, each the way an array's does: a parameter and
a value receiver received by pointer and copied on entry (`byRefParam`,
`recvByRef`), a result written through an out parameter (`funcStructRet`), a
channel's element crossing by pointer (`chanStructByPtr`) -- and a literal of one
NESTED in a compound literal is braced, since a compound literal of its own is a copy
into the member (`&W{Buf{...}, k}` did not build) -- and one BESIDE another result
came down the next day, the result struct holding the array too and taking the same
out parameter (`structOutOf`, `emitTupleOutReturn`): no by-value boundary is refused
now. A domain program's first draft, a ring buffer's `Pop() (Frame, bool)`, had hit
that refusal before anything else, which is what ranked it. Two lessons from the
result. A lowering makes new shapes WRITABLE, and each is a row for the rejects
sweep: `mk(1).data[1:]` and
`&mk(1).data` compiled where Go refuses both, and their neighbour `&mkp().x`, a plain
struct result's field, always had (`callValueAddressing`). And a chain is RENDERED by
every question asked about it -- `len(mk(1).data)` reads the extent and then
evaluates the operand -- so a call bound out of a chain is bound once per occurrence
(`structOutCallC`, the statement's memo), or it runs once per question.

**A RULE ASKED OF A ROOT MISSES WHAT A POINTER REACHES** (2026-09-23). A range over an
array iterates a copy, made where the body may write the array -- and "may write" was
asked of the operand's ROOT name: `for i, v := range p.data { gb.data[2] = 99 }` for
`p := &gb` read the write live, 99 on the board for Go's 3, and so did `range
getp().data` and an array in a slice's element. Storage reached through a pointer, a
slice or a call is any name's (`rangeThroughRef`). A rule that asks whether a name's
storage is written has to ask what else names it; found by reading the rule while
fixing its neighbour, not by a sweep, so the other rules asking a root are the place
to look next. The next one looked at had it too, the same day: a printf binds the
arguments after one whose String() may run, and took a LOCAL read by name to be out
of the method's reach -- whose receiver held its address, 77 on the board for Go's 1
(`printArgUnreachable` asks `aliasedLocals` since; its own comment had named the
receiver as the way in). Tuple assignment, a return against its defers, and `&&`/`||`
operands binding a call were probed the same way and held.

**PRINTING A VALUE IS A ROW** (2026-09-23). `printf("%v", x)` of an ARRAY printed the
address of its storage wherever x was not a bare name -- a literal, a field, an
element, a row, a call's field, a deferred capture -- silently on the board, and
only a variable and another package's array printed its elements, which is all the
run cases printed. Found by a probe of something else: the fix for the binding above
printed an array argument, and the array read wrong for a reason of its own. So a
print is swept like a store, across every way a value is reached, for each kind of
value; and for arrays the multi-dimensional ones too (%v prints them row by row since,
the element-wise verbs still refuse one). A TYPING question asked of every printed
argument must be cheap and pure: `arrayShapeOf` renders a call it passes, which lifted
a function literal argument a second time -- the corpus guard showed it as one changed
program -- so it is asked only of what `inferCType` cannot type.

**A TEMPORARY IS A COG REGISTER** (2026-09-20). flexcc gives every C local one
(`local_N res 1` in COG_BSS) out of a pool the assembler checks with `fit 480`,
shared across functions so the deepest call chain is what spends it -- and overflow
is a build ERROR, not a warning: `error: fit 480 failed: pc is 483`. Binding one
more value per print, to get printf's argument order right, broke `TestTargetBuild`
on a run case whose C was correct; the fix was to bind only the arguments a method
called while formatting can REACH (`printArgUnreachable`). So an emitter change that
binds values asks which of them it has to, and `grep -c '\tres\t1' prog.p2asm`
measures what a program spends.

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
array-returning call as a package literal element; a function returning an ARRAY
taken as a value, `mb := mkb`, "cannot infer a type" and, with the type written,
"cannot return an array beside another result" (a function value's type has no out
parameter for it); an ARRAY beside another result, `func f() ([3]int, bool)`
("cannot return an array beside another result"), whose result struct would hold the
array -- as the result struct of a struct holding one does, which travels through an
out parameter since 2026-09-24 (structOutOf), and that is how this one would come
down; a function literal called and then read through, `func() P { ... }().x` ("a
function literal may only be called where it stands"), for any result type; a store
into a field of a call's
VALUE, `mk(1).n = 5`, refused as Go refuses it but in the emitter's words ("only
simple and field assignment targets are supported yet"), and a field a call's
pointer result lacks, `getp().x`, as "unsupported call in expression"; the
address of, or a slice of an array in, a call's value where the receiver is a
variable declared from a SLICE literal, `hs[0].get().data[1:]` for `var hs =
[]H{...}`, which Go refuses and this takes (callValueAddressing walks the receiver
from a written type, or an array literal's, and asks nothing of what it cannot
type); an
element-wise printf verb of a multi-dimensional array, `printf("%d", grid)` ("cannot
tell the type of this argument"; %v prints one); `[]byte(s)`
and `[]rune(s)` of a string VARIABLE (a copy of a length known at run time; a
constant's converts since
2026-09-23, constBytesConv); an if or a switch init that is a compound assignment, an
increment or a send (a for init from one call's several results works since
2026-09-23, emitForInitMulti); a deferred
or started call through a function field of ANOTHER package's variable, `defer
lib.B.F(1)` ("only <pkg>.<Func>(args) ...") and `go lib.C.F(2)` ("unsupported
receiver in a go statement"), where the same through this package's works since
2026-09-23; a parenthesised function value started on a cog,
`go (h.f)(2)`, which works as a value, a statement and deferred; a deferred or
started call through a dereference with an index, `defer (*ps)[0](x)` and `go
(*pa)[0](x)` ("unsupported call target", and go's "only `go f(args)` ..."), where
the statement and the value work since 2026-09-22; the method expression of an INTERFACE type, `Shape.Area` (refused by name; a concrete
type's works since 2026-09-19); a method value on a local or a call's result (design: a method value binds its
receiver at compile time); an unnamed struct type mixed with the SECOND of two
declared structs of its fields (its typedef names the first, aliasAnonStructs), which
Go admits and the target's compiler refuses; printf's `%v` of a
struct or an interface under a width (an interface value prints what it holds since
2026-09-20 -- `&{1 2}` for a struct pointer as the argument, its address or its
String() at depth, ifaceHeldPrintC -- but the chain writing it cannot pad); a PARENTHESISED HEAD as a TARGET where peeling does not reach it:
a conversion's `(*T)(p).x = 5` and `*(*T)(p) = 5` (`(p).x = 5`,
`(&p).x = 3` and the same through an index, an increment and a compound assignment
work since 2026-09-20, parenTargetBase, and a head that is a CHAIN, the send
`(&bus.ports[1]).ch <- 5` among them, since 2026-09-22, addrChainSteps). READING through one
works since 2026-09-20 (`emitParenChain` binds the head and walks the steps from
it), and a declaration from one since 2026-09-22 (`parenChainType`): `(&p).x`, `(get()).x`, `(*getp()).x`, `(getq()).a`, `(arr[1:])[1]`,
`("hello")[1:]` and `(&arr)[1]`, beside the `(*p).x`, `(a)[i]`, `(v).m()`,
`(&v).m()`, `(*T)(p).m()` and `(a - b).m()` that always did. A struct head is a
VALUE there, so a slice step on one is refused as Go refuses it; a conversion to a
POINTER to a type written out, `(*[]byte)(p)` and `(*[4]byte)(s)` ("not supported
yet": the bare `[]int(x)` and `[3]int(s)` parse since 2026-09-21, and `[]byte(s)` is
refused as the allocation it is); `len` or `cap` of a BRACKETED conversion and a
reslice after one, `len([3]int(s))`, `len(([5]int)(a))`, `t := []int(s)[1:]`, and an
index of or a range over a slice-to-array one, `[3]int(s)[2]`, `A(s)[1]` (a named
type's `len(A(a))` works, and so does `[]int(s)[0]`); a method of a defined slice
type called where the value stands on append's result, `append(l, 9).Sum()`, or
started on a cog on a reslice, `go l[3:].Run(done)` (bound to a variable, both work);
an ELEMENT or another package's variable as a `range` clause's target, `for _,
q[0] = range ptrs`, `for _, geo.P = range ptrs` ("a range target must be a variable
or a struct field"); a chain after a slice step of an ARRAY OF ARRAYS,
`grid[1][1:][1]` ("this combination of indexes and fields is not supported yet"):
the walk would bind the row to a temporary, and C has no array value to bind (the
slice of slices' `rows[0][1:][1]` works, and a NAMED string constant's
`hexdigits[1:][0]` since 2026-09-20, which reads the folded value where the
literal's form reads its own); a LOCAL type that shadows a package type an EARLIER
local type named -- `type A struct{ n int }` at package level, then in a function
`type B struct{ A }` and after it `type A struct{ B }` -- which is legal, B's A being
the package's, and is refused ("type A has no field n"): the checker's scopes answer
a name with whatever the block holds when it is ASKED, not with what it held where
the name was written, so every later question about B's embedding finds the local A
(it was a stack overflow of the compiler until 2026-09-20; the emitter, lowering in
order, reads it right); a defined type that names ITSELF other than through a struct
-- `type Tree []Tree`, `type Step func(int) (int, Step)`, `type F func() *F`, `type
Pipe chan Pipe`, `type P *P` -- "emit: unsupported type", with no position: C names a
type inside itself through a struct's tag, and these have none (a struct holding its
own kind every legal way works, locally too, and so do an interface whose method
returns it and two structs through each other's pointers). The one such FUNCTION type
Go programs write, the state-function idiom `type stateFn func(*lexer) stateFn`, works
since 2026-09-23 (collectRecFuncType): its typedef returns a generic function
pointer, `ogo_anyfn`, a function named as a value is cast into it, and a call through
a value is made through `<typedef>_call`, the type of the functions it holds, whose
result is the type itself -- the callee cast, never the result, which is a cast of a
call the target refuses when an argument is a struct literal
(`doc/complit-arg-in-cast.c`). A PARAMETER of the type, `type V func(v V) V`, is
refused by name. The if and switch headers took only a ":=" init until 2026-09-23,
a gap specs.go RECORDED as the form provided, until a domain program wrote `if s,
keep = f.Apply(s); !keep`. No other grammar gap is
known: the ones recorded before all closed that day, and two nobody had recorded --
HeaderFactor had dropped the suffix from three of Factor's alternatives, and a string
literal took none at all, `"0123456789abcdef"[n&15]`. Two more surfaced the next day
from semantic batteries, not from the grammar: a select's comma-ok flag took a bare
name only, `case v, r.ok = <-ch:`, and a NAMED composite literal took no suffix, `P{1,
2}.x` (the bracketed one had). A battery that writes what Go programs write finds
these; reading the grammar did not. **Check Factor and HeaderFactor
against each other when either changes**; they are meant to differ by one production
(HeaderFactor has no literal after a name, which is what keeps `if x == T {` a block).
Latent ones, measured and not faults today: a store through a chain, `r.m[a()][b()]
= v()`, leaves its calls to C's operand order, which gcc 14 and flexcc both take left
to right (only the bare `name[i] = v` path binds them); a method called on a
CONVERSION as a print argument, `println(T(3).bump(), g)` for a bump writing g, is
not bound first, so gcc reads g before the call and the HOST differs from Go, while
flexcc takes printf's arguments left to right and the board agrees (2026-09-23); and a value's call does not
run ahead of an index or nil panic in the target, `arr[bad()] = side()`, where Go
runs `side()` and then panics -- the comment in emitIndexAssign says otherwise and
describes one C compiler's choice. Only a program about to panic can tell.
A printf that FORMATTED as it goes was a third, and is fixed (2026-09-20): Go
evaluates every argument first and formats afterwards, so a String() with a side
effect was seen by a LATER argument reading what it wrote -- `printf("%v|%d",
stringer, calls)` printed the bumped count. A print whose formatting may call a
method binds every argument first now (`formatCallsMethod`), which is what the
hoist already did for an argument whose own expression had an effect. Found by a
domain probe over error values, not by the suite: the counter a probe bumps in
String() is exactly what makes it visible.

**The lifetime rules are asked of SHAPES, and a shape nobody asked about is a hole**
(2026-09-17, found while closing a grammar gap). `TestEmitCFrameRefForms` crosses
every form that binds a value with every kind of reference to the frame, each cell
beside a control over package storage; it found that parentheses hid a value from
every rule (`return (a[:])` compiled), that `var s []int = a[:]` recorded nothing,
and that no list form, destructured call, swap or `for` clause asked the rules at
all. **A new binding form, or a new way to write a value, is a new row or column
there** -- the same discipline `generatedConstructs` gives the fuzzer. The address
of an ARRAY or a SLICE literal, `&[3]int{...}` and `&[]int{...}`, was a value nobody
had written into it (2026-09-22): the addressed literal was recognised by a type
NAME, so every sink passed the bracketed spelling and `return &[3]int{7, 8, 9}`
returned a dead frame; it is two kinds there now (`addrOfArrayLit`). The SINKS
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
A literal whose signature names a LOCAL type is typed by nothing before the bodies
are walked, so it joins its type's members when it is LIFTED and the union is
computed afresh at each call site; a call written before that literal is the one
place it is missed. A binding
`bindFuncValue` records is believed only where `boundFunc` says nothing can have
changed it (`scanBindings`): written once, or only by plain assignments in one block
of a function without a goto, never address-taken or method-called, never a package
variable -- before, a rebinding in a branch, a loop, a select or through `&f` left the
first binding in force. `TestEmitCFuncValueUnion` (and `...Packages`) is the matrix: a
new place a function value can live, or a new way to write one, is a new row there.
The SUMMARIES follow such a call too (`TestEmitCSummaryFuncValues`): a "type:" callee
is an edge target like a function (`typeCallee`), its union refreshed on every pass of
the fixed point as its members' summaries grow (`unionSummary`). This pass has no
locals, so it types what it can -- a parameter (a declared function's from `funcParams`,
a literal's read from its signature, `funcInfo.paramCType`), the receiver, a package
variable -- and a local through what it holds, a literal it holds among them (its
summary key resolves as a name would); a value only a call's result type
names is held as `?<type>` (`summaryCallResult`). A deferred call and a literal called
where it stands are calls here as well; neither was, so `defer keep(v)` and
`func(w []int) { gs = w }(v)` laundered a parameter. A call through a METHOD's
result, `s.handler()(v)`, is a call of the union its result names (`chainUnions`,
2026-09-21), as `pick()(v)` always was; the other item once listed here, a chain
whose base is a literal's parameter, no longer reproduced in any shape tried that day.
Calling a method's result as a STATEMENT is still a call shape the emitter refuses.
A method that keeps what its receiver HOLDS, called on a COPY, keeps what the
original holds (`TestEmitCRecvContentsCopies`, 2026-09-21): none of the copies was
followed -- a value parameter matched no receiver case at all (its method's arguments
went unfollowed too), a local's edges named what was stored INTO it and never what it
held (`w := W{v}; w.save()`), a value receiver handed nothing on, a field's, an
element's and a range value's method was no method call, and a goroutine's receiver
was not sunk. `copyRecvEdge` carries the callee's `recvContents` to whatever the
copy's contents carry -- a parameter's VALUE where the copy holds it as a part, which
no `recvEdge` flavour could say -- and a receiver reached past a pointer or into a
slice's backing is `recvParam` one hop behind a parameter and `recvOutlives` beyond.
A new way to reach a receiver's contents through a copy is a new row there. The
summaries record ONE level of contents, so a field behind a pointer, `through(T2{&lw})`
for a `t.p.save()`, is refused even when lw holds package storage -- as the direct
`gs = t.p.xs` always was; a deeper model is how that price would come down.

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
