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

**Complex numbers are PLANNED, for release 1.1** (the user's call, 2026-09-24: 1.0
is not rushed, and correctness comes before completeness, so they land after it as
a purely additive change with time to bake). They need no heap, so they are owed,
not excluded. specs.go's "Complex types (planned)" specifies them ahead of the work
-- Go's, a value two floats at the target's float precision, so complex128 is no
more precise than complex64 -- and nothing before 1.1 should foreclose any of it.
Until then an imaginary literal (the scanner) and complex64/complex128 where the
program declares no such name (`errUndefined`) say "complex numbers are not
supported yet"; flexcc has no `_Complex`, so the emitter will lower them itself.

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
ogo build --no-inline .                    # leave inlining to the C backend, as before 2026-09-29
```

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

   > **Backend regenerated 2026-10-02 at the same pin with a fix of its own, and with
   > go.mod's ccgo v4.36.1 and libc v1.77.1** -- spin2cpp `eb263961` (v7.7.3, still
   > upstream's master), flexprop `v7.7.0`, `internal/optimize_ir.c.diff` carried
   > again: TransformConstDst folded the flags of a CONDITIONAL instruction whose
   > operands became constants as though it always ran, so `if_e cmp f, #0 wz` --
   > run only where an earlier compare set Z -- made the jump after it
   > unconditional, and `(12 != g) || f` was false for a stored `f = (v < v)`,
   > silently (`doc/conditional-compare-fold.c`; found by fuzzer seed 1284, which
   > failed its checksum on the board and passed on the host). Five lines: return
   > where the instruction is conditional, as every other place applying known
   > flags already asks. Measured natively before adoption: test_offline 588/588; of
   > 1550 programs (the run cases and fuzzer seeds 1-400 dumped, the doc/ battery,
   > seeds 1001-1300) six build differently, all six right on a P2-EDGE with the fix
   > and seed 1284 wrong without; p2-11's binaries byte-identical. The first
   > transpile from ccgo v4.36.1: each builder's turn changed its own target only
   > (the undup fold expanded and hashed before and after each), and scripts/flexcc
   > of all five builds the 1551 programs to one cccorpus list, a native build's of
   > the same tree -- but for doc/register-limit-crash.c, which crashes both (139
   > native, a Go panic in the transpile). REPORTED 2026-10-02 as flexprop#118, one
   > issue bundling this fault with the suggested fix, the nine-deep hang below and
   > a question about the cost of pushregs_/popregs_ in hub code (prototypes
   > measured, not sent). The mcpp setjmp retry the ccgo note below names was not
   > attempted this time.
   >
   > **Base flexspin hangs at nine nested calls through functions without a frame**
   > (measured on a P2-EDGE 2026-10-02, a chain `int fN(int x) { return fN+1(x ^ N)
   > + N; }`, eight deep right and nine silent): a non-leaf function with nothing to
   > save gets no frame and keeps its return address on the 8-level hardware stack
   > while it calls. Not worked around; no generated program is known to reach it.
   > Reported in flexprop#118.
   >
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
   > the library (`go install modernc.org/ccgo/v4@<that version>` into a scratch
   > GOBIN first on PATH), so all five transpiles come from one ccgo. Every transpile
   > committed so far is ccgo v4.34.6's with libc v1.74.1; **go.mod pins ccgo v4.36.1
   > and libc v1.77.1 since 2026-09-27** (the upgrade alone changed no binary: 1218
   > programs byte-identical under `scripts/cccorpus.sh`, the host suite and `make
   > board` green), so the NEXT regeneration is from that pair, all five platforms in
   > one go. It is also where `mcpp_main.c.diff`'s removal of mcpp's setjmp can be
   > retried: the transpile arms no jump buffer and calls `tls.Longjmp` at mcpp's six
   > cfatal sites, so any preprocessor fatal error -- a line over NBUFF's 65,536 bytes
   > was one, until the emitter wrapped its lines -- is libc's "unsupported
   > setjmp/longjmp usage" panic instead of mcpp's message, under either libc.
   > v1.77.1's model (a Longjmp may target any armed buffer, not only the last, and
   > panics with a LongjmpRetval naming it) is what makes keeping the setjmp worth
   > trying.

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
  `format_table.go` (its alignment: gofmt's tabwriter, emulated).

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
  intrinsics; `unsafe`, `testing`, `strings`, `bytes` and `math` are likewise bare,
  module or not. There is no standard library beyond those.
- **Functions implemented in Spin2** (2026-09-30, `internal/octogo/spin2.go`) are
  OctoGo's .s files, asked for by p2-11's VGA text console (Eric Smith's MIT
  `vga_tile_driver.spin2`, used as it is). A package may carry `.spin2` files; a
  function declared without a body binds to the PUB method of its name, whatever the
  case, of one of them (`bindSpin2`, from `checkFuncBody`; `scanSpin2` reads the PUB
  declarations past comments and strings), the parameter and result counts checked.
  The emitter declares one instance per object, `struct __using("<path from the build
  root>")` under `__FLEXC__` and an `#error` for the host (`emitSpin2Objects`), and a
  bound function is a C function forwarding to the method (`emitSpin2Func`), its C
  prototype asked what crosses (`spin2Crosses`: an integer of 32 bits or fewer, a
  bool, a pointer). Lifetime: `seedSpin2Cross` seeds `crossParams`/`crossContents`
  with `leakCog|leakGlobal` for every parameter before the fixed point -- the method
  is out of sight and taken to keep what it is handed, which is what a driver does.
  `ogo build` and `ogo test` pass the build's root (`buildRoot`) to the backend as
  `-I`, so `__using` resolves and what the object names resolves beside it; ogo
  test's `overlayFS` needed a merging `ReadDir`, listing through Open having shown
  the runner alone. Tests: `TestScanSpin2`, `TestSpin2Binding`, `TestSpin2Emit`,
  `TestSpin2Refusals` (host), `TestBuildSpin2` and `TestTestSpin2` (the backend, no
  board), `TestOnBoardSpin2` (in `make board`). Found on the way and fixed the same
  day, both older than it and true of every function keeping a parameter: a relay
  ACROSS packages, `func relay(p *T) { lib.Keep(p) }`, recorded no summary edge,
  the summaries naming a callee by the name it is called by, so `relay(&local)` was
  accepted -- `qualifiedFuncCall` resolves `lib.F(args)` for `valueCall` and
  `eachQualifiedCall` for `stmtCalls` (`TestEmitCRelayAcrossPackages`); and `&s[0]`
  of a slice whose backing is the frame's, `s := xs[:]`, was no frame reference,
  `addrOfRoot` stopping at a slice's index and `sliceBackingIsFrame` asking of a
  slice's value only -- `addrOfSliceElem` answers `sliceElemRef` for it, and a
  pointer so marked carries `elemOriginPrefix`, pointing INTO s's backing and not at
  s, so `*p` is an element and not the slice (`TestEmitCSliceElemAddr`). A new way a
  value reaches the frame is a row of the matrices below, and so is a new way to
  name a callee.
- **`unsafe.Pointer`** (2026-10-01; asked for by p2-11, whose VGA driver reads
  addresses out of a parameter block of longs). `unsafe` is an embedded, intrinsic
  package (`unsafeSrc`) whose one name, `Pointer`, is the Kind
  `PredeclaredUnsafePointer`, put into its scope by NewPackage (`unsafePointerDecl`)
  as the universe's names are put into the universe: a Kind, as go/types makes it a
  basic type, so every Kind-gated rule asks it, and `catUnsafePointer` keeps it from
  mixing with anything but itself and nil. The checker reaches it through a
  qualifier (`unsafeQualifier`, `isUnsafePointer`; `typeKind`, `nameKind` and
  `canonicalType` each answer it, the last so no name `Pointer` is recorded beside
  the Kind) and through an alias. Conversions: `checkUnsafeConversion` for
  `unsafe.Pointer(x)` (a pointer, a uintptr, an unsafe.Pointer or nil), `uintptr(p)`
  in checkConversion, `(*T)(p)` in checkPtrConvOperand. The emitter spells it
  `void*` (`cUnsafePtr`), so nil, comparison and a field are a pointer's, and names
  it back in `goTypeName`/`typeNameForT`; an interface holds it as its data word,
  under the table of its pointee `void`. Lifetime: `unsafeConvOperand` is what
  frameRefOf, summaryReach and callExprsIn read through, an alias's conversion
  included, and `uintptr(x)` is a sink twice over -- refused in emitConversion where
  x reaches the frame, and a summary keeping whatever x reaches
  (`uintptrConvOperands`), since a number goes where no rule follows it. A STORE
  through a conversion, `*(*T)(p) = v`, which every write through an unsafe.Pointer
  is, binds the converted pointer first (`convTargetHead`, `bindPtrConv`). Not yet:
  a type DEFINED over unsafe.Pointer (refused at the declaration; an alias is
  taken), `Sizeof` and the rest of Go's unsafe ("not supported yet"), and printf
  verbs but `%v` and `%T`. Tests: `unsafe_pointer*.ogo`, `TestEmitCUnsafeLifetime`,
  three run cases, `TestOnBoardUnsafe` (a uint32 address read through by a Spin2
  method, and Hub RAM at 0x14 equal to `p2.ClockFreq()`). Building it found two
  lifetime holes older than it, both in the summaries: a callee storing `(*T)(p)`
  (ptrConvShape; frameRefOf saw through the conversion, the summaries did not), and
  one storing into ANOTHER package's variable, `lib.G = p` (outlivesByName). And two
  loud gaps: a type switch's `case *int:`, a pointer to a predeclared type -- closed
  2026-10-02, see below -- and a parenthesised qualified conversion, `(lib.T)(x)`,
  left: the emitter peels `(x)` only where x has no suffix of its own
  (unparenKidsOnce), and the checker's parenNameConv reads a bare name.
  A conversion to a POINTER to a type written out, `(*[16]uint32)(unsafe.Pointer(a))`
  and `(*[]T)(x)`, came the same day: the checker reads the bracketed type with typ
  (`ptrConvExpr.lit`, `litType`), asks the operand Go's rule (`checkLitPtrConvOperand`:
  a pointer to an identical type, nil, an unsafe.Pointer, and a slice of the element
  to a pointer to an array) and types the result for a variable through
  litOrConvType; the emitter names the typedef every `[N]T` shares (`starLitCAt`), and
  a slice's conversion is a per-type helper taking the slice whole and checking the
  length (`sliceArrayPtrC`, `arrPtrHelperDef`) -- never a `void*` a helper returns
  and the site casts, which the target's compiler loses for a pointer to an array
  (nilHelperDef), and never a cast at the site, which it refuses and then CRASHES on
  for a member of a compound literal, which `a[1:]` is (viaField of
  doc/complit-arg-in-cast.c; found by the `--unchecked` target build, the checked one
  having passed the member to a call uncast). The
  summaries' `ptrConvShape` read `(*Name)(x)` only, so every callee keeping what it
  converted to `*[N]T` was accepted until it read the bracketed form
  (`TestEmitCArrayPtrConvLifetime`). Found beside it and OLDER, and closed the same
  day: a pointer to an array had no type past the pointer. Its element and range
  value were unknown (a pointer's elemKind is its POINTEE's, so every rule guarded
  `!d.isPtr`; factorType, rangeElem and suffixedTargetKind now ask the written
  types, lenOperandType's walk, `stepsType`, reaching a parenthesised head too), a
  local declared `*[3]int` recorded no type (declareLocalVar resolved only listed
  shapes; `ptrLitType`), an array had no identity, so checkRefAssign compared no
  pointer to one (`arrayNodeLen` in typeNodeIdentity and typeNodeString), and append
  asked checkRefAssign nothing. 23 of 32 programs of the row were taken where Go
  refuses them; the one left, `f()[0]` of a call's result, which lenOperandType must
  not type -- `len(f())` is no constant in Go, and the len fold asks it -- was a
  row of its own, closed the next day (A CALL'S RESULT HAD NO STEPS).
  A TYPE SWITCH CASE or an ASSERTION naming a pointer to a predeclared type, to a
  type written out or to a defined array type, `case *int:`, `case *[3]int:`,
  `case *[]byte:`, `case *Row:` (2026-10-02): the assertion `i.(*int)` worked and
  the cases did not -- the emitter's caseTypeC read `*Name` of a struct or a
  registered defined type only, and the checker's caseTypeName a name after the
  star. caseTypeC names the concrete type as the interface's table does
  (starPredeclaredCAt, starLitCAt, isMethodBase); the checker reads a written-out
  case through starTypeLiteral and binds the clause's name at the whole pointer
  type (`typedVar`), as a variable declared from such an assertion is
  (`assertedPtrLit`, read by shape through assertionTypeNode, so `lib.B.(*int)` and
  `xs[i].(*int)` too; `takeType`). An array's ADDRESS could not go into an interface
  at all, varType knowing no array (ifaceOperand asks arrayVar). Such a pointer has
  no methods (`ptrToUnnamed`): a case, an assertion and checkImplements refuse one
  where a method is asked for, which reached the emitter before and was said in C's
  names. Duplicate keys and messages spell a predeclared type as Go prints it,
  `uint8` for a byte (typeAtMessage). On the board: the binding casts the
  interface's void* to a pointer to an array typedef, the cast nilHelperDef says the
  target's compiler lost, and it reads and writes right, int, string and struct
  elements alike. A type switch whose operand is no bare NAME, `switch
  f().(type)`, had no case of it checked at all -- closed the next day, see A CASE
  WAS A POSITION NOTHING WALKED.
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
  succeeds.** flexcc warns where it should refuse: given a duplicate declaration in
  one block it says `Redefining x`, ignores the second and produces a working binary
  holding the wrong value (that was `aa300e2`). So `boardBuild` treats any output
  from a *successful* build as a failure, which puts the check in `TestTargetBuild`
  -- no board needed, in the default `go test ./...`. A clean build is silent. A
  diagnostic examined and found harmless goes in the case's `backendWarning` field
  together with the reason; there are none since 2026-10-04, when the four recorded
  were found gone. And `ogo build` holds a USER's build to the same rule since that
  day (backendFault in internal/build): a backend warning about the emitted C fails
  the build, the C kept for a report, `--allow-backend-warnings` the way round it;
  a warning about the program's own `.spin2` object passes, and so does one in
  `harmlessWarnings`, examined and found to say nothing -- an unused CORDIC
  operation deleted, `go math.Sqrt(2)`'s. Each kind that had reached a build was a
  silent fault: Redefining, Bad number of parameters, incompatible pointer types in
  parameter passing, incompatible types in comparison.
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
  (482 passing, 18 outgrowing a cog). On 2026-09-24, with cbcba6a's generator still,
  seeds 25-1000 on a P2-EDGE, all clean (940 passing, 36 outgrowing a cog; 1-24 are
  `make board`'s), by a loop of `ogo smith`, `ogo build` and `ogo loadp2 -t` -- one
  compiler, and nothing else on the serial port meanwhile: a second loader on it
  talks over the first, and `Prop_Ver G` in a capture is that. On 2026-09-25,
  with the same generator and the compiler that names every constant string's
  header at file scope (78cb75a), seeds 25-400 on a P2-EDGE: 365 passing, 11
  outgrowing a cog, none failing -- five that outgrew before fit now. A generated program
  writes self-comparisons and `2 ^ 3` on purpose, so it is built with the oracle
  test's gcc flags, not probe.sh's `-Wall -Werror`: under those, 273 of 2000 fail on
  the program's own warnings.
  **Calls are counted** (2026-09-19): every generated function adds its weight -- a
  4-bit field per function -- to `octosmith_calls`, and main asserts the sum before
  the checksum. The functions are pure otherwise, so a call evaluated twice or not
  at all changed nothing the checksum could see: nine corpus programs carried a
  doubled call (`ch <- len(name())`, 3e2d5a1) and the oracle passed them all. The
  VM counts a call where the program makes it -- once per call whichever results are
  read, never in an `&&`/`||` operand the left one decides, and not in package
  initialization, main resetting the counter first. **A new call site in the
  generator calls `noteCall`**, once for each time the program runs the call.
  **Names are drawn from what C has spoken for** (2026-09-25): `newVarName` gives one
  of `cNames` -- a C keyword, a macro, a library or system-module function, a type
  -- one time in six, each at most once a program, since the emitted C keeps a
  program's names and every generated name had been a counter's, `v_12`. Its first
  run found two positions no sweep had (an unused parameter's `(void)` marker, an
  interface-typed local); every seed is a new program from that commit on. Swept
  with that generator the same day: seeds 1-2000 on the host shim, clean once a
  local named like a C type the emitter writes (`uint8_t`) was renamed; and 1-200 on
  a P2-EDGE, where FILE, DIR and div_t failed to BUILD -- the target's compiler
  cannot parse a declarator named like a typedef, which gcc allows -- and then 192
  passed and 8 outgrew a cog. Seeds 201-600 on a P2-EDGE the same day (8ff6d08): 384 passing, 15
  outgrowing a cog, and ONE failing, seed 479, the host passing it -- a float
  constant whose shortest decimal is exactly halfway between two float32s, which
  the target's C compiler reads in double arithmetic and so rounds either way
  (`doc/float-literal-tie.c`); such a decimal is written in hex since
  (`nearFloat32Tie`). **A float literal the emitter writes is one the target reads
  exactly**: a hex one always, a decimal only away from a tie. So is a STRING
  literal (2026-09-30, found by p2-11): the target's lexer reads an octal escape past
  three digits, so `cQuote` closes the literal after one a digit 0-7 follows, `"\033"
  "7"` (`doc/octal-escape-past-three-digits.c`; flexprop#115, filed 2026-10-01 with a
  tested fix, fixed upstream 2026-10-05 by spin2cpp 639a8c8e with the same rule and
  measured right on the board; the workaround stays, costing nothing, until a
  regeneration's pin carries it).
  **A call stands in a header** (2026-09-29): one time in four `genHeaderCall`
  writes one as the init of an if, of a switch and of a for, and as the for's post,
  its results unread, which the call counter accounts for; every seed is a new
  program from that commit on. Swept with that generator the same day: seeds
  1-2000 on the host shim, clean, 917 of them with such a call; and 25-200 on a
  P2-EDGE, 168 passing, 8 outgrowing a cog and none failing, 75 of them with one.
  **A function of several returns** (2026-09-30): every program declares one
  (`genGuardedFuncDecl`) -- two or three tests over its parameters, each returning,
  ahead of a last return, in a row, as an else chain, as a tagless switch's cases or
  nested (`guardShape`) -- which the emitter marks for the backend to inline and
  writes to leave through one return (`singleExit`): the lowering of v0.46.0 no seed
  had reached. The emitter marks a function only while its statements times its call
  sites stay within 96, and such a one has six, so it is drawn at `guardSites`,
  twelve, call sites at most: drawn like any other, it was called in 24 to 38 places
  and marked in none. A for's post may draw it past the cap, the post being written
  wherever its init is, both or neither -- which the cap broke first, four seeds of
  100 writing a for with an init and no post. **A new call site by name counts
  `fn.Sites`** beside `noteCall`. `TestOracle` fails when fewer than half its seeds
  reach the rewrite; all 100 do. Every seed is a new program from that commit on.
  Swept with that generator the same day: seeds 1-2000 on the host shim, clean, 1996
  with the rewrite (the other four never call the function); and 1-200 on a P2-EDGE,
  185 passing and 15 outgrowing a cog, none failing -- the generator before had 9 of
  200 outgrow, a program making up to twelve calls more now. The listings say the
  backend took the marks: in four seeds no call of the function is left, where
  `--no-inline` leaves all twelve.
  **Constants** (2026-09-30, `consts.go`): every program declares a group of untyped
  ones in each spelling a constant is read in (small, middling, past 2^31, 64-bit,
  negative, hex, rune, a float spelling, an expression of earlier ones), an iota
  group, and one to three typed ones, of a predeclared kind or a defined type over
  one, with the type or as a conversion (`genConstDecls`). They are read beside each
  integer type: an operand of a sized step or fold (`sizedOperand`), the left of `/`
  and `%`, either side of a comparison (`genSizedCompare`), a sized declaration's
  initializer, and an int expression's leaf (`constLeaf` -- integer spellings only,
  two constants in one expression being computed EXACTLY, and `2.5e3 / 3` is no
  integer). A sized block declares one of its own one time in three, half of them
  named like a package constant and read from it, `const k_3 = k_3 + 5` -- the
  block's name is in scope after its spec only -- the package's hidden from the
  block's expressions (`hiddenConsts`) and read again after it. An untyped constant
  meets a kind that holds it (`fitsKind`), a typed one its very type as written
  (`sizedType`). Its first sweep, seeds 1-2000 on the host, found two programs gcc
  refused: `x &^ 6e3` complemented as `(double)-1 ^ (6000)`, emitComplement having
  been typed by the operand alone (inferNode, by the level's inferNodes since), which
  the target built and, measured, computed right. Swept with the generator and the
  fix: seeds 1-2000 on the host shim, clean; and 1-200 on a P2-EDGE, 196 passing, 4
  outgrowing a cog, none failing.
  **Static tables** (2026-10-02, `tables.go`): two programs in three declare a
  package struct type of a one- or two-byte head, an ARRAY of one- or two-byte
  elements and now and then a tail field, and a variable of it from a constant
  literal -- half of them an array of two or three as well, a third a slice of one --
  every field and element folded into the checksum at the top of main
  (`genStaticTables`). It is the row flexprop#116 lives in, a static initializer laid
  out by another rule than its type, and no generated program had a package variable
  of a struct holding an array. The values are the generator's constants, so the
  fold's value is known without the VM modelling a struct. Of seeds 1-2000, 1315
  declare a table and 909 a type the backend lays out both ways, which the emitter
  fills at package initialization (declared bare and memcpy'd in the C); all 2000 pass on
  the host shim, and seeds 1-200 on a P2-EDGE, 91 of them with such a type, 192
  passing and 8 outgrowing a cog, none failing (six loads that timed out passed on a
  second try). The method expression went from 0.5% of
  statements to 2.5% with it: three seeds in a hundred had drawn one, and the shift
  of the draws left none in the coverage corpus.
  Swept on 2026-10-03 with the released v0.48.0, seeds 201-1000 on a P2-EDGE (a
  tool taking the board lock per seed, shared with the p2-11 session): 749 passing,
  48 outgrowing a cog, two refused by the backend, `x << 32 >> 32` of a uint64
  (doc/uint64-shift-32-pair.c, loud), and ONE built with a warning, "Bad number of
  parameters", which was a silent fault: an untyped constant typed 64-bit by its
  operands, passed to an int parameter as two words (A CONSTANT IS A VALUE, OF THE
  TYPE IT MEETS). The board passed that seed's checksum anyway, where a run case
  of the shape failed in every argument position, so **a sweep that runs only what
  built silently misses a fault the backend warned about**; the tool keeps WARNED
  apart. Seeds 1001-2000 with the released v0.48.1 on 2026-10-03/04, the same way:
  922 passing, 78 outgrowing a cog, none failing, refused or warned about; and seeds
  2001-3000 with 98b1306 on 2026-10-04: 934 passing, 66 outgrowing, none failing.
  Seeds 3001-3500 with c81020d the same day: 469 passing, 30 outgrowing, and seed
  3298 failing its checksum on the board alone -- a named wide constant divided by a
  uint64, spelled signed (d43ac82; A CONSTANT IS A VALUE). Seeds 3501-4000 with
  d43ac82: 475 passing, 25 outgrowing, none failing. Seeds 4001-4500 with 815f42a:
  461 passing, 39 outgrowing; 4501-5000 with eb4bedb: 468 passing, 32 outgrowing;
  none failing, warned about or refused by the backend. Seeds 5001-5500 with 40aa62b
  on 2026-10-05: 465 passing, 35 outgrowing, none failing; ten loads timed out
  ("timeout waiting for checksum", "sendAddressSize: timeout") and passed on a
  second, the loader's and not the program's. A seed that outgrows can take
  ten minutes to say so, the backend's allocator on one function of 200 lines, twice
  over when its functions were marked (seed 3146: 333 s and 244 s).
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

**A MUTATED PROGRAM IS A PROBE OF THE FRONT END** (2026-10-05). The run cases'
sources, each changed once -- a line deleted, duplicated or swapped with another, a
character dropped or inserted, a few deleted -- 28,000 of them through dumpc under
the cap: what an editor hands the compiler mid-keystroke. Three crashes, each a Go
panic: the scanner, finding no lexeme (an unterminated literal), recorded "invalid
token" and returned a token at an index no token had, which the next reader asked
of; a file whose parse FAILED was still walked by phases 2-5, its declarations not
declared and its tree not what was written (newFile discards it now -- its errors
were already dropped afterwards, BuildModule), which a unary operator left without
an operand met as a nil; and a constant declaration's evaluation had no case for a
dereference, `const g = step ** 3`. `ogo fmt` over 5,000 of them crashed on none.
**A front end is fed what is half written, not only what is right or wrong.**
A second round of 30,000, each changed up to three times, found one more crash,
and on a VALID program: a line swap moved `var y [4]byte = dev().rx` to package
level, and a package variable initialized from any field of a call's result --
`var n = dev().n` -- crashed the compiler in v0.48.1 as now, the temporary the call
is bound to recorded in a map of locals package scope had never made
(resolvePkgVarTypes, emitPackageVars make it). No sweep had written a package
initializer of that shape; a mutation moved a statement there by accident.

**A MUTATION WITH GO AS THE ORACLE IS A PROBE OF THE CHECKER** (2026-10-05). The
same run cases mutated by one TOKEN rather than a character -- a name swapped for
another in scope, an integer literal for a string, a float, a bool, nil or a rune,
a `&` added or dropped, `:=` for `=` and back, an operator for another, an argument
dropped -- each mutant through a Go twin (GOARCH=386) and dumpc. Of 3,000 mutants Go
refused 2,451 and this compiler took 89 of those; 39 are refused now. The rows they
opened, each a rule written and never asked in a position: nil as an index, a
bound, a print argument, a range, a min, a switch tag and a constant; an element of
a literal ELIDED inside another; a store through a pointer CONVERSION, bare and
parenthesised (checkConvTarget, convPlaceType); the arguments of a call through a
call's result or an element as a STATEMENT, where the same call as a value was
checked (checkValueCallSteps); a slice from `make` with no type for any walk
(madeSliceType); a package variable whose initializer names one declared BELOW it,
checked before that one had a type (demandVars resolves it first, as constants
are); a field PROMOTED through an embedded struct, or named by its type, which
every walk typing a selector skipped -- "not modelled here" -- so `o.n = true`
built in silence (fieldTypeVia, Go's shallowest-depth rule, across a package too);
and an array LITERAL's bound, evaluated where a type is declared and nowhere else,
`[true]int{1}` built in silence. **Go's verdict on a mutant is the cheapest oracle
there is: a program one token from a valid one is what a user writes by mistake.**
Of the 50 then still taken, 22 were Go's "declared and not used" -- `=` mistyped as
`:=`, the shadowing mistake Go's rule exists for, which built in silence. The rule
counted uses BY NAME ("at the cost of not distinguishing ... shadowing", its comment
said), so a read of the outer variable counted for the new one. A read counts for
the variable it RESOLVES to now (noteRef, from checkFactorNames and every
assignment head, noteHeadRefs, recorded in the scope the checker reads it in -- a
value is read before its own statement's names are declared); a name nothing
resolved -- a field, a key -- still counts by name, so the rule errs toward taking
a program. Two traps met: a type switch's clauses bind their own declaration of the
guard's name, which a read resolves to (clauseOf links it to the guard's, the one
recorded), and a lookup leaving a function literal records a CAPTURE, so resolving a
`:=` target's name there before the statement declares it refused six run cases as
captures (findQuiet looks up without the bookkeeping). The net was every probe
program of the scratchpad -- 356 flipped on the first version, all type switches,
and none on the last. Of the 89, 30 are taken now: the rest are loud at the backend
or rarer -- a builtin indexed, a non-function called, `&iota`, a type named as a
value, no `main`, a struct passed for a `*Builder`. The tools are throwaway
(gen.py, verdict.sh, tbuild.sh in the scratchpad); tbuild.sh builds each taken
mutant for the TARGET, which ranks them -- SILENT first.
A second batch, 3,000 more with three more edits -- a type name for another, a `*`
added or dropped, a field for another -- had 73 taken, 30 of them silent: a star
over a call as a statement (`*println(x)`, `*pf()`) and as `go`/`defer`'s operand,
the call run and the star dropped; `*n++` for an int n, the `=` form's question
never asked by `++` and `+=`; a pointer conversion heading a store, asked of its
operand as a value and not as a target; a range `=` of arrays of another length,
and of a literal's elements (rangeElemTypeAt typed no literal); an element of a
bracketed literal in an operation, which had no Kind (factorType); and a
conversion in PARENTHESES, `(int)(f)`, which had no Kind and no name, so a variable
declared from one was asked nothing. Following one false refusal the net showed
(the probe's own `unsafe/mp`, older than the batch): a method of another package's
type whose result that package spells qualified, `unsafe.Pointer`, was named as
the RECEIVER's package's, `hub.Pointer` (importedMethodResultType dropped the
result's qualifier and homeQual put the receiver's on it), and its Kind was read
nowhere, requalifiedSig failing on a qualified name (importedMethodKind reads it in
the method's own file). 37 are taken now, all loud at the backend but three:
`println` of an array or a struct field of one, which this compiler prints by
design, and a variable named `string` (a type name, which no read resolves, keeps
it "used").
A third batch -- two arguments swapped, a literal for a big one (300, 70000, 2^32,
`1 << 40`, `-129`, `1e3`, `0x80000000`), a value converted to another type, a result
dropped -- had 85 taken and FOUR COMPILER CRASHES, each out of memory: a constant
shift computed exactly at any count (`1 << 0x80000000`; go/types bounds it at 1074,
and so does the folder now, checkConstShiftBound asking it where a declaration's
folding drops the folder's report) and a literal laid out position by position up
to a key of `1 << 40` (litPositions; checkLitKeys bounds a key by int, by an
array's length and by the 512 KB of Hub RAM). **A constant's SIZE is a row**: the
sweeps had crossed a constant's spellings and types, never its magnitude, and every
position a constant sizes something -- a key, a length, a capacity, a shift, an
index -- took one past what the compiler or the target holds. Beside them: a
float-spelled index or bound, `a[1e3]`, which the emitter's bound check did not
read (foldIndexConst), constant slice bounds out of order on a slice, a switch tag,
a shift count past a uint, `int64(i)++` as a for post, and `-*p`, which had no
Kind. 15 of the 85 are taken now, eight of them the `println` extension. Over the
three batches, every one of the 1,838 mutants Go accepts and this compiler did is
accepted still.
What all four batches still left taken, 56 mutants once `println` of an array (the
extension) and a missing `main` (`ogo build`'s to refuse) are set apart, were loud
-- a C error about generated code each -- and two things about them are worth
keeping. Probed alone, a third of them were REFUSED: the mutant's line was taken
because of how a variable in it was DECLARED, not because of the rule -- a package
variable from one call of several results (varSpec typed only one value per name),
a variable from a call of a call's result, `a := pick()(4)`, recorded as a FUNCTION
(exprCallee answered for any number of calls and every caller read the callee's
results; exprCalleeCalls follows them now), both variables of a comma-ok assertion
to an interface (only a pointer's was typed), a `make` stored into a field.
**When a mutant is taken, probe its line alone before writing a rule: what is
missing may be the variable's type.** And a constant's operation over two classes,
`const x = 1 + "s"`, was folded by go/constant to an unknown value without a word,
so the constant was taken even where it was used, and a blank constant was never
evaluated at all (blankConsts). Of the 14 then left, six were the declaration row
again, and each opened one: an EMBEDDED field had no TypeNode, so a literal's value
for it was asked nothing (checkStructLit gives it the type it names); a variable
whose type is an int by its origin -- an untyped constant's `n := 1`, `len` or `cap`,
a range's index -- had no identity, and typeIdentity's silence let it into any
defined type over int (it answers for those three now, and for nothing else: a range
VALUE over a []Word is a Word, which is why the silence was there); a pointer from
`&x` of a Kind asked nothing of what was stored in it; a slice of a defined string
type lost the type, the string branch returning before the name was asked; and a
variable from a several-result call had a name and no type for varTypeAt
(typeFromResult records it). 8 are left, each loud or Go's own cascade -- an unnamed
struct type's field order, `&iota`, `**np` of an `*int`, `printf(...)` as a value. A
call of a call whose result is a slice, `var s string = mk()(1)`, has no type yet now
that exprCallee no longer misreads it.
A fifth batch -- a method name for another of the file's, an argument duplicated, a
literal's type for another, a field selector dropped, a return value duplicated --
had 111 taken, 66 of them `println` of a struct or an array (dropping `.x` leaves
one), and of the other 45, 28 are refused now. Rows: a TYPED CONSTANT as a receiver
was no receiver at all (checkMethodCall asked variables only); a call whose method is
chained, `pick(1, 1).Show()`, was asked as an expression and not as a statement,
deferred or started (leadingCall, which for a defer reads the statement's steps, its
first child being the keyword); a chain on a parenthesised value or a literal checked
its first call only (reportCallChainWalk -- with the pointer rule kept off, the walk
there beginning at no storage, which falsely refused `(c).Inc()` of a variable on the
first try); the predeclared Builder, with no declaration, stopped every walk that
reached it through a field (callChain.builderCalls) and had no result types
(methodSingleResultKind, zeroResultCall answer for it now). 17 are left, positional.
A spec file marked `// COMPILE` carried an `// ERROR` annotation nothing met until
`l.sb.Flush()` was checked (builder.ogo): **an ERROR line in a COMPILE file is an
expectation nobody checks** -- the marker was dropped.

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
A NAME crosses it the other way (2026-10-01, found by p2-11): main's globals are
keyed by their source names, main's prefix being empty, and the registries'
bare-name fallback -- meant for another package's global, which arrives already
mangled (qualifiedChainBase) -- answered a library's own name that is no variable
of the library with main's variable of that name: a's function f, called or
deferred or taken as a value in a, went through main's function value f, and
`range n` over a's constant ranged over main's array n, both silent on the host and
the board. `bareGlobal` keeps main's names out of another package's bare lookups
(`mainGlobals`, TestEmitCMainNamesInPackages); scalar constants had been safe only
because they are folded before any variable is asked. **A new bare-name lookup into
a package-level registry asks bareGlobal**, and a probe of the boundary declares in
main what the library uses by the same name. The CHECKER had the mirror of it, the
next day: every helper following a chain of type definitions -- typeKind, chanElem,
isPointerType, isArrayType, the element-name helpers, the cycle walk -- looked a
QUALIFIED name, `lib.Count`, up bare in the package asking, so it was main's Count
where main had one and no type otherwise. Valid programs were refused (a dereference
of a `lib.P` variable always), and wrong values into such a type taken.
`typeIdentDecl` resolves a written type name where it is declared, through the file
that wrote the qualifier; a helper returning an element's NAME answers nothing for
another package's type, whose names are that package's (TestCheckQualifiedTypes). A
METHOD of such a type was the same row: callResults read the receiver's type by its
bare name and its results in this scope (it resolves and requalifies them now,
requalifiedSig), and importedMethodResultType named the result of a struct's method
only, so a `type Count int`'s went unchecked.
**A new helper following type definitions goes through typeIdentDecl.**
A FIELD of such a struct was the same row (2026-10-02): fieldTypeNodeOf looked the
owner up by its bare name, so `var s string = q.N` for an int N of a `q lib.T` was
taken -- through a value, a pointer, an assertion, a type switch and a call's result,
9 of 16 programs -- or read main's own T of the name. The owner is resolved where it
is declared and the field's type requalified for this file; an assertion's variable
keeps the qualifier, `x.(*lib.T)` (exprNamedType had dropped it); and a field of
another package's CALL, `lib.Get().S`, is walked from the result
(qualifiedCallChainKind, stepsType).
And a name of the UNIVERSE read off another package's declaration took that
package's qualifier (2026-10-02, found by the type-switch probes): a function,
variable or method of package a typed `any`, `error` or `int` gave what was declared
from it the type `a.any`, `a.error`, `a.int`, which no rule took for an interface or
for int -- `i := a.Box(); i.(*int)` was "not an interface", and `var l Local =
a.Count()` passed for main's `type Local int`, the names differing and the kinds not
asked. `homeQual` qualifies only a name the package declares, or one of no scope it
can tell; 1 of 8 programs of the row agreed with Go before, all 8 after
(TestCheckQualifiedTypes).
A CALL'S ARGUMENTS were the row from both sides (2026-10-04, found by a
four-package domain program): a method of another package's type was asked whether
it exists and is exported and its arguments not at all, wherever it was called --
28 of 30 programs Go refuses were taken, `c.Add(5)`, `lib.C.Plus(1)`, `xs[0].Add(5)`,
an interface's, a promoted one (checkImportedMethodArgs) -- and a type defined over
a Kind, `lib.Count`, was asked nothing, importedStruct answering for structs only
(importedTypeDecl). Two causes under it, each wider than the boundary. A signature
carried across, `lib.T` with the qualifier this file's token and the NAME the
library's, was resolved in the file of its name, where lib imports nothing: every
rule said nothing of a call through `add := c.Add` or `f := lib.Use` (identFile;
**a file is found from a qualified name's QUALIFIER**). And a method called on what a
chain REACHES -- `xs[0].Inc()`, `w.c.Inc("x")`, `mk().c.Inc()`, in one package too --
was checked by nothing: callChainWalk typed every step and handed no method it passed
to the argument check (`callChain.calls`, checked in reportCallChainWalk), and it
stopped at a type of another package, and at an embedded field written out,
`w.Counter`, which it follows now. On the board such a program built with a warning,
"Bad number of parameters", and read the missing argument from whatever its register
held. **A walk that passes a call hands the call to the argument check.**
An exported VARIABLE of an UNEXPORTED type, `var ErrBusy = &errBusy{}` -- the
sentinel idiom, found by a domain program the same day -- was refused wherever it met
an interface, "lib.errBusy does not implement error (missing method Error)", in all
nine positions of the row: `implements` resolved the type through typeDeclNamed,
which answers nothing for a qualified name this file may not WRITE, and a type with
no declaration has no methods. The name came from the variable's declaration, not
from this file's spelling (typeDeclNamedIn; TestCheckUnexportedImplements). **A name
the checker reads off another package's declaration is resolved there whether or
not it is exported; only a name this file wrote is held to the export rule.** Its
IDENTITY was the same row the same day -- `var n int = lib.Room` for a `var Room
celsius`, the value into a type of this package of that shape, a struct of another
type passed where lib's unexported one is wanted, all taken -- and definedName,
kindlessCategory and checkDefinedType resolve such a name too. Not typeDeclNamed for
everyone: tried, it stopped refusing `(*lib.point)(p)`, `lib.point.Sum` and an
embedded `lib.point`, whose refusals came from FINDING NOTHING. **A rule that refuses
by failing to resolve is found by widening the resolver and running the rejects
again** -- 17 positions written with an unexported name, `scripts/rejects.sh`-style,
showed the three, and a fourth older one, `case *lib.point:` (checked since).

**A C NAME IS A ROW** (2026-09-25). The emitted C keeps a program's own names, so
every name a program may write is a row across everything else that names things in
that one translation unit: the target's headers (a function, a type, a MACRO, which
breaks a local or a field too), the library sources a program pulls in and the
backend's system modules (`_tx`, `bytefill`), the host's headers, and the names the
EMITTER mints by joining two with an underscore -- `Type_method`, `Iface_vt`,
`Iface_vt_Type`, the thunk `Iface_Type_method`, a global channel's `g_cell`, a local
type's `T_l1`, another package's `pkg_name`. The emitter renamed a hand-picked list
of library names and none of its own joins; a sweep of plausible names found `read`,
`write`, `open` and `close` failing inside the library's posixio.c, `FILE`,
`uint8_t` and a field `EOF` failing in generated C, `clock` and `gets` building with
a warning -- and ONE silent: a type `Shape_vt` merged with interface Shape's table,
`v.a+v.b` reading 0 on the board for Go's 3. The library's names come from
`cnames.go` (see Code generation), and a join that meets a program's name -- a
top-level symbol's, or any identifier it writes, since a local of the join's
spelling shadows it where it is called -- moves to `ogo_j_<join>` (`joinName`,
`collectUserSpellings`). Two corners are left, both contrived: a user name beginning
with `ogo_`, the compiler's prefix, which nothing enforces, and two joins meeting
each other, type `a_b`'s method `c` and type `a`'s `b_c`. A new place the emitter
joins names goes through `joinName`. A LOCAL is the same row by declaration: its C
spelling is `localIdent(name)` wherever the name is written into C, and the maps key
the name as written. An array, a slice, a channel, an array parameter, every named
result (`resultC`; curResultNames stays in source spelling for `returnValueStands`)
and a range value of an array or a struct had written the name raw, so a domain
program's `var long [260]byte` was a C error. A new declaration path writes
`localIdent`, and a sweep of one takes a keyword and a macro, `long` and `EOF`,
through each kind and each position. The fuzzer draws such names too (cNames in
internal/smith), which is what keeps the row covered where no sweep reaches.
A MEMBER the emitter names is the same row from the backend's side (2026-10-02,
found by a domain program): flexcc DROPS a struct or union member named like a
STATIC global declared before the type (doc/member-named-like-global.c, flexprop#117),
and the goroutine
runtime's argument blocks, written after the program's globals, had members `aN`,
`sN` and `fn` -- `var s0 = proto.Set{...}` made the union of blocks 0 bytes, a cog's
arguments sat on its stack, and `go feeder(3)` read 3 as an address, in silence; a
global `a0` was a build error, the trampoline naming that member. They are `ogo_aN`,
`ogo_sN`, `ogo_fn` (goDefs). The program's own struct types are written before its
globals and are not reached. **A type the emitter writes after the globals names its
members with the `ogo_` prefix** -- `TestEmitCInitSkew` holds the go blocks.

**A STATIC INITIALIZER IS LAID OUT BY ANOTHER RULE THAN ITS TYPE** (2026-10-02,
found by the same domain program, `ok 3 -3` on the board for Go's `-2`). flexcc's
layout aligns a member by PaddedTypeAlign -- four for anything of four bytes or more
-- and advances by its size (frontends/common.c, fixupVarOffset); its static
initializer aligns by TypeAlign, an array's ELEMENT alignment, and pads a member of
four bytes or more to a multiple of four (backends/dat/outdat.c, outputInitializer).
Every struct is a multiple of four with alignment four, so only an ARRAY field of
one- or two-byte elements of four bytes or more tells the two apart: `struct { uint8
ch; int16 s[4] }` initialized statically read s[0] as s[1]'s value, and a field
after a `uint8[6]` read 0, in silence; where the written total exceeds the type,
flexcc says "Bad initialization size" (doc/static-init-array-field.c, flexprop#116,
filed with a fix tested natively). Locals,
compound literals and stores are right in every shape, measured with a 40-element
local array. `flexccInitSkew` computes both models over a struct's fields,
recursively through struct fields and array elements (flexccStructSkew,
flexccShape: exact below four bytes, parity above), and a package variable of a type
they disagree about -- staticInitOK, the package array path, a slice literal's
backing and an elided row's (pkgSliceLitVar) -- is zeroed statically and filled at
package initialization from braces, which copy right. None of 1199 corpus programs
had such a static, and p2-11's binaries are byte-identical; the run case "package
variables of structs holding small arrays" holds it on the board. **A rule about the
target's layout is measured on the board in C first** (scripts/cboard.sh), across
member kinds, sizes and offsets, before the emitter is taught it.

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
category, until its initializer was asked which (sliceOrArrayOf). The cell the sweep
did not cross turned up on 2026-09-24: a Kind-less value into ANOTHER Kind-less
category -- a struct into a pointer, `hp = P{}` -- was taken in every position,
the rows having crossed a Kind into the categories and the categories into a Kind,
never the categories into one another (checkRefAssign asks it since). With it, an
element STORE asked only nil and a function's signature, where a variable asked all
of checkStoreInto; and a variable declared from a SLICE literal had no type for any
rule walking a variable's type (sliceLitType).
That fix was for a POINTER wanted, and a slice already was; the grid of every
category into every other, in a declaration and an assignment (2026-10-04, found by a
rejects batch over a domain program's libraries), had 62 of 154 cells taken -- a
struct, an array, a slice, a channel or an interface into a function, a channel or a
struct, and a function or a channel into an array, of which v0.48.1 refused 42 about
generated C, built 17 with a warning and 3 in silence (kindlessIntoOther, by
nonBoolOperand's answer and the written type where it has none). Beside it, an array
of another length (checkArrayIdentity; a deferred call's was the emitter's to refuse),
nil into a field, an element or a literal of a Kind and a send of one, which only an
argument and a return asked checkNilAssignable, and another package's struct literal,
whose fields were asked nothing but a channel, a slice and a pointer (checkLitValue
requalifies them). The rule's first version was a FALSE refusal twice, each the trap
WHAT HAS NO KIND IS ASKED NOTHING names: `pp := &p` for a `p := &arr[1]` recorded a
pointer to a P (addressOfInfo's literal fallback answered exprNamedType(&p); a pointer
to a pointer is left unmodelled there now, and valueTypeAt types an address), and
`put(&a)` for a `*uint16` read exprType of an address, its POINTEE's Kind -- p2-11's
prof showed it, the corpus did not until a run case did. **A new rule refusing on a
category asks it of each way the checker misreads one: a pointer to a pointer, an
address, the element of a slice expression.** The third was met
again the same day, by a domain program sorting through an interface: `SS(sa[:])`,
`for _, v := range sa[1:]` and `sa[:] != nil` for an array of structs were refused as
structs -- nonBoolOperand read `sa[:]` as the index `sa[1]` -- in the conversion,
range and comparison rules just written, never released. It answers "a slice" for a
slice expression since (sliceOfVar first), which every rule asking it hears at once;
the 21 rejects grids and batches of the scratchpad gave the same verdicts after, and
a batch of slice expressions went from 8 of 15 agreeing with Go to all of them. With
it, a slice expression as an assignment TARGET, `xs[1:] += 1`, `arr[1:]++`, taken as
a store into an element since long before, is refused (checkCallValueTarget).
The same grid over the two operations it did not cross, the same day: a CONVERSION
`T(v)` of each category to each, 120 to defined types and 72 to types written out,
had 30 taken -- a target of no Kind returned from checkConversion at once, and only
the emitter refused a defined struct's, slice's, function's or channel's; a defined
array's and pointer's it built, 19 of them in silence as C casts
(checkKindlessConversion, checkPtrConvOperand) -- and a COMPARISON of each pair,
196 programs, had 25 (a pointer against a struct, a channel or another pointer type,
a literal against nil or an interface, `nil == nil`: checkKindlessRelOp,
ifaceAgainstValue). Its first version refused `B("pkg")` for a `type B []byte`, a
run case's: **Go's exceptions are part of a category rule** -- a slice into an
array or a pointer to one, a string into bytes or runes, nil into what holds a
pointer, anything into an interface. nil to a defined slice, function or channel
type, `L(nil)`, was the emitter's loud gap (zeroInitC writes it now); a conversion
to `(chan int)`, to `[3]int` of an array literal, and to a function or struct type
in parentheses, which does not parse, are still refused, loudly. Grids of the
builtins and assertions (110), the unary operators and steps (156) and the
statements (110) over each category found three more: a literal called, `S{1, 2}()`,
and a range over a struct or an interface without a value variable, taken, and with
one refused in the emitter's words about an integer (checkRangeable asks
nonBoolOperand now). `len` and `cap` of a channel differ by design.

**A CALL'S RESULT HAD NO STEPS** (2026-10-02). The checker typed a call's result
where the call stands alone, `x := f()` for a result of a Kind, and nothing past it:
an element of a slice, an array or a pointer to an array a call returns, `f()[0]`,
named or not, through a function, a method, a function value and a call's own
result -- 202 of 292 programs Go refuses were taken, every position, every head --
and with it a range over one, a variable declared from one, a field of a struct
element, and the arguments of a call through a function value an index or a call
reaches, `handlers[0]("x")` and `pick()(1, 2)`, where only a name, a variable's
field and a method had theirs checked (checkValueCalls). The walk was there:
callChainWalk followed every step past a call to ask about storage and members, and
handed back no type (`callChain.t`). `valueTypeAt` is lenOperandType with calls --
one walk, `operandType`, the len fold keeping its own answer -- and factorType,
rangeElem, operandTypeAt and the category and name rules ask it. Three traps. A
variable's type from a call is resolved WHERE IT IS DECLARED and kept
(`inferredType`): varTypeAt is asked later, when the block may hold a name declared
after the initializer, and chasing the initializer then makes `b := a.get(); a :=
b.get()` answer each with the other. A slice step keeps a DEFINED slice type, `l[1:]`
of an L is an L -- the corpus guard caught the walk rebuilding it from the element,
and the run case's method on it refused. And a type reached in ANOTHER package is
spelled as that package spells it: right for a Kind and a category, wrong for a
NAME, so a walk from `a.F()` carries the qualifier it began with (`callChain.home`,
`qual`; chainNamed) and a name it ends on is requalified, or `var x a.A = a.Fa()[0]`
would compare a.A with main's A. The walk asks more than a type -- storage, pointer
methods, missing members -- and from another package's call it had asked none of
it: `&a.Arr()[0]` was taken, and a member that package does not export is asked
there now too (`unexpAt`). A range value that is itself an array, `for _, r := range
gg` over a variable, had no type either, which nobody had asked (rangeValueType).
**A walk that types a value hands its type back to every rule, and a rule asked of
a value asks the walk** -- the call result was a position no Kind rule was ever
asked in, as a case was (below).

**A CASE WAS A POSITION NOTHING WALKED** (2026-09-21). The rules above cross
operations with categories; they say nothing of a position no rule is ever asked in.
A case of an expression switch was one: folded to find a duplicate, asked for a
Kind, and walked by nothing else, so `case get(1, 2):`, `case f[0]:` and `case T:`
went through, and `case one:` in a switch on an int built a binary in silence. A case
is walked as a value now (checkCaseValues) and compared with its tag
(checkCasesAgainstTag). The trap met on the way: the checker takes a type switch
apart only when its operand is a bare NAME, so `switch v := xs[i].(type)` had been
read as an expression switch whose cases nobody looked at -- walked, its types were
refused as values, and only the run corpus showed it (typeSwitchShaped). It was
taken apart whole on 2026-10-02: typeSwitchParts keeps an operand that is no name as
the expression it is (`typeSwitchGuard.expr`, rebuilt by factorWithoutLastStep), its
interface asked of exprNamedType (typeSwitchIfaceName) and its names walked, so a
duplicate case, a bound name misused or unused, an impossible case, a fallthrough and
a non-interface operand -- `h.n.(type)`, `f().(type)` -- are refused in Go's terms
where 10 of 32 such programs were taken and the rest refused by the emitter in its
own; the emitter took two valid shapes it had refused, a parenthesised operand with
steps, `(h.sh).(type)`, and any other in parentheses, `(<-ch).(type)`
(parenTypeSwitchOperand). Following the row across a package found a field of
ANOTHER package's struct typed by nothing (fieldTypeNodeOf, A PACKAGE BOUNDARY IS A
ROW). So: **when
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
The sweep had crossed "another package's" with a function variable only. Another
package's variable with a STEP after it (2026-10-04, found following the call
argument row) came to both functions with the qualifier as its head: deferReceiver
captured nothing, so `defer lib.B.f(2)` for a field returning a result, `defer
lib.B.Show()` and `defer lib.Bs[1].Show()` for a value receiver read the chain at the
return -- 49, 67 and 63 for Go's 42, 65 and 62, in silence on the host and the
board -- and a field
without a result and an element were refused; emitGo refused every one of them. Both
fold the variable's global into the head now (`qualifiedChainBase`), as the
statement paths did; the run cases are in multiPkgProgram, so the board holds them.

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
A third round (2026-09-25) -- a serial console, an event scheduler, checksums, a
SLIP decoder -- matched Go but for one line: the decoder's `% x`, Go's hex dump of
a frame, refused with every flag, width and precision. Its row (the byte forms
under each flag) found the pad helper under `%-8s` counting runes by their LEAD
bytes, where the decoder a range uses counts as Go does -- two answers to "what is
a rune" in one program, and a stray byte padded wrong in silence. **One question,
one helper**: where the emitter answers something twice, the two answers are a row.
The row taken whole the same day, a PRINTF SWEEP: every verb, flag, width and
precision over each kind of value, 29,480 statements, compiled in-process to drop
the 1,183 shapes refused (loud) -- a tool of a few lines around `octogo.Build` and
`EmitC` checks 7,744 shapes in three seconds, where a process each takes an hour --
and the other 22,388 run in batches of 400 against Go. Two faults, both old: `%q` of
a negative integer, '\xffffffff' for Go's '�', silent on the board as well; and a
padded bool in a program with no string, whose helper came without the typedef it
takes. And the sweep's run cases did not build for the target: see the cost of a
bound temporary below. A second sweep over COMPOSITE values -- structs, slices,
arrays, Stringers, errors, interfaces, each under every verb and a few specs, 1,817
statements of which 739 compile -- found %T laid out by the C library's printf
(`%08T` "  main.P" on the host and "main.P" on the board for Go's "00main.P") and a
nil interface under `%8v` not padded. `scripts/`-style tools for both sweeps are
throwaway; the method is the point: filter the refusals in-process, then batch.
Two BOARD sweeps the same day found nothing, which is worth knowing: integer edge
semantics for all ten integer types (min, max and their neighbours under every
binary operator, shifts at and past the width, every conversion, the compound
assignments, `MinInt / -1`), and a float32 sweep of the target's SOFT-FLOAT library
(subnormals, -0, NaN, the infinities, the rounding of 16777217, sqrt, floor, ceil,
trunc, int to float) -- both byte-identical to Go on a P2-EDGE, as were a PID
controller in Q16.16 over int64 and a config parser.
A fourth round (2026-10-04) -- a temperature controller of four packages: a
stateFn frame parser, a ring of events, sensors embedding a base behind an
interface, hooks in another package's function variables, a cog producing events --
found two faults of INTERFACE-TO-INTERFACE values, both older than it. Two values of
DIFFERENT interface types compared, `s == p` for a Sensor and a Probe, compared
their tables, one per (interface, type) pair, so the same *Thermo in both was
unequal: false on a P2-EDGE with only "incompatible types in comparison", which
`ogo build` passes on, and a gcc error on the host, so no run case had one. The
operand assignable to the other's type is widened first (emitIfaceCompareC,
ifaceWidens). And widening named the SOURCE's table for every type implementing
the source interface, which exists only for types stored in it (ifaceRebindC asks
needVTable of both). **A rule that dispatches on a table asks for the table it
compares with, not only the one it stores.**
The same day, every probe program a sweep had left behind -- 4,250 of them, the
grids, the rejects batches, the domain rounds, old fuzzer seeds -- was built for the
TARGET with `ogo build`, keeping the builds that succeeded with any output and the
refusals that came from the backend: the programs the compiler took and the C
compiler did not. Eleven warned and 168 were refused, and beyond the cog's register
pool and doc/uint64-shift-32-pair.c it was all one statement, a blank assignment.
`(void)gc == gc` cast the left operand only -- a void compared with a channel, a
warning for pointers and a refused build for ints -- and a compound literal anywhere
under the cast is refused by the target, `_ = gs == S{1, 2}` and `_ = &S{1, 2}`
(doc/void-cast-compound-literal.c; emitDiscard binds such a value first). No C
compiler had seen them: a grid asks the checker what it takes, and compiles nothing.
**A probe's program is a target-build test too, and the warnings are read.**
A sixth round (2026-10-05) -- a JSON subset parsed into a fixed arena of nodes
linked by index, recursive descent, errors carrying an offset, path queries --
matched Go on the host and the board once two FALSE refusals were gone, both in
v0.48.1. A receiver's type was recorded without its scope (declareReceiver), so
varTypeAt and every walk over it said nothing of `p.src[i:j]`, and a variable
declared from it was "a slice"; the slice of a string had no case in the walk
either (stepsType, indexedStringKind). And the summaries read every field of a
local holding a parameter as the parameter: `p := parser{doc: doc}; fail(p.pos,
msg)` refused `Parse(src, &d)` for a fail keeping its int (fieldNameMayCarry: by
the field's name, the one thing a shape has, and by every struct declaring it).
Typing the receiver showed a third, older, a misreading it had hidden: soleUnaryExpr
walked into the parentheses of `(*p)(x)` and dropped the call, so `!(*p)(x)` was
"it is a function". **A value nothing typed hides every misreading of it**: giving
the receiver its type put each rule asking about one back to work, and the spec
tests and the matrices showed the one that answered wrong.
The lesson taken across every way a variable is declared (29 forms, a struct's
string field stored into an int through each, and the refusal read for its reason):
all held but a TYPE ASSERTION whose operand is no name, `pick().(*T)`, `h.i.(*T)`,
`xs[0].(*T)`, single-valued or comma-ok, and a field read off one, `i.(*T).s` --
exprNamedType and the comma-ok declaration read the asserted type through
typeAssertion, which wants a name before the dot (assertionTypeNode reads it from
any operand), the walk had no step for an assertion (stepsTypeIn, given the scope it
is written in), and factorType returned fieldKind's "unknown" for any chain ending in
a field before the walk could answer. `var k int = v.f` for a float field built and
ran on the board, 2 for Go's refusal. **A variable is a row of every declaration
form**: one with no type is skipped by every rule, in silence.
A fifth round (2026-10-05) -- a string-keyed hash table of open addressing with
tombstones under an LRU over a fixed node pool, three packages, what a program here
writes for want of a map -- matched Go on the host and the board once its first line
of error handling built: `panic(err)`, refused, "panic is supported only with a
string argument yet". specs.go had it as "panic(s) abort with a string message",
STATUS and not design: printing a value needs no heap. A panic of any value is
written as Go's printpanicval writes it now (emitPanicValue; an interface through a
helper minted after the last body, as the %v printers are, mintPanicIfaces), Go's
first line the oracle for 32 kinds of value. And that sweep found a SILENT fault
older than it: `panic(-0.0)` printed `-0` for Go's `0`, Go's constants having no
negative zero -- `x := -0.0; 1/x` was -Inf on the board for Go's +Inf, through a
declaration, an argument, a return, an operand, an element, a field and a package
initializer (negatedFloatZero). **A constant's value is exact, and a C spelling of it
that is not -- a sign on a zero -- is the C compiler's arithmetic, not Go's.**
A seventh round (2026-10-05) -- fixed-point physical units in a library, each
formatting itself through the predeclared Builder over a buffer of its own, printed
by `%v`, `%s` and String() -- matched Go on the host and the board once its first
line built: `NewBuilder` in any package but main was "cannot infer a type", the
builtin having been registered as a function OF MAIN'S (funcRet["NewBuilder"]), which
a library's call, mangled into the library, never found. Its key is its helper's C
name now, ogo_builder_new, answered by funcCallC where the name is the builtin's. And
the row it opened was a NAME OF THE UNIVERSE THE PROGRAM DECLARES: the checker
resolves a builtin's name through its scopes, and the emitter dispatched a call by the
name alone, so a function, a parameter, a local, a package variable or a type of the
program's named len, cap, copy, min, max, append, println was taken for the builtin --
`len("abc")` folded to 3 for a `len` returning 99, a parameter max and a package
variable min calling the builtins, a program's own println printing its argument, all
silent on the board in v0.48.1 -- and its own make, new, panic or print refused as
one. 72 programs (18 names, each as a function, a local function value, a parameter
and a package variable) agree with Go now. `universe` asks the emitter's environments
what the checker asks its scopes, at every site that dispatches on a builtin's name;
the checker's two by-name readers were termination, where a program's own panic ended
a function (ownCallees, recorded by checkCallee where the scope is known), and a
deferred call's "discards result". The rest of the universe was the same row (104
programs, its types and constants as a function, a local, a parameter and a package
variable): `int8()` of a program's own int8 was a conversion of the call, a
parameter `true` was C's 1 (convType and every reader of true, false and iota ask
`universe` now). `nil` as a program's name is still refused by the checker, loudly,
where Go takes it. **A dispatch on a name asks what the name means where it is
written**; the universe is the last scope, not the only one. The fuzzer draws the
universe's names it never writes as the universe's since (universeNames in
internal/smith: new, print, close, Builder, uintptr, ...), in 249 of 300 seeds, as a
function, a type, a constant, a variable or a parameter; its first host sweep found
four seeds failing, every one a program's own `type uintptr` -- a struct, a slice,
a uint32 -- which a TYPE position read as C's uintptr_t (cType asks universeType,
the types alone: `func f(int int) int` reads its types outside the parameters'
scope). Following it, a program's own `type Builder` had every method refused by
the checker (isPredeclaredBuilder). Swept with that generator (651a7f5): seeds
1-2000 on the host shim, clean, and 1-200 on a P2-EDGE, 192 passing, 8 outgrowing a
cog, none failing (four loads that timed out passed on a second try); 201-500 with
510c8fb, 280 passing, 19 outgrowing, seed 359 refused by the backend
(doc/uint64-shift-32-pair.c), none failing (twelve loads timed out under a busy
host and passed on a second try). Seeds 501-1000 with 835694b: 469 passing, 30
outgrowing, seed 852 refused by the backend the same way, none failing. Seeds
1001-1500 with 508e289: 463 passing, 37 outgrowing, none failing or refused (thirteen
loads with no output passed on a second); 1501-2000 with 7c883cd: 459 passing, 41
outgrowing, none failing (twelve passed on a second load).
An eighth round the same day -- a sort library over an interface, a heap of timers
holding function values, three packages -- matched Go on the host once a literal of
ANOTHER package's defined slice type was one: `order.Ints{5, 2, 9}` was a plain
slice to the emitter in 11 of 13 positions (namedSliceLitType read a bare name, and
the expression path's isNamedLitType too, so `lib.Ints{...}` went the struct
literal's way, its elements into the header). A keyed one with its type written,
`var l L = L{3: 1}`, was asked for struct fields in one package too (bindLitFuncFields,
emitLocalChanFieldCells), and `make(lib.Ints, n)` was "dynamic allocation" to the
checker (namesSliceType) and no make to the emitter (makeTypeAST). **A row swept in one package is swept with the qualified
spelling** -- A PACKAGE BOUNDARY IS A ROW said it of the checker; the emitter's
matchers of a literal's type are the same row. Left, loud: `var b Builder =
NewBuilder(...)` for a program's own Builder is taken by the checker, the two types
having one name, and refused by the target's compiler.
A ninth round -- COBS framing with a CRC-16, typed errors examined by a type switch,
a fixed-point moving average over an embedded struct, two packages -- met the
qualified row again on its first line, `n, err := frame.Encode(...)`: a destructured
call's result type took the library's qualifier even for a name of the universe,
`lib.error` (typeFromResult asks homeQual, which a single result had asked since
2026-10-02). Its neighbours, swept as 8 heads by 8 binding forms: another package's
VARIABLE at a several-result call's head was the qualifier to the emitter
(resultCallOf folds it, qualifiedChainBase, for a multiple assignment, a return and
an argument list), and a function ELEMENT's several results were written through
its out parameter by the multiple assignment alone -- `return tab[i]()` and
`take(tab[i]())` failed in one package too (valueOutCallC has the case now).
**One question, one helper**: the three places taking a call of several results
apart had each its own list of shapes, and each list lacked some.
The same row taken across the other places a value of another package's declared
type is read -- a receive, a range value, a variable passed as an argument -- had
the qualifier given unasked in three more (chanFactorElemInfo, rangeElemNamed,
qualifiedValueType); and its rejects grid found the category rules asking nothing
of a RECEIVED value or of another package's VARIABLE: nonBoolOperand, which every
rule asks what an operand is, had no case for either (nonBoolRecv; a qualified
read is no field). 27 of 35 received cells and 19 of 35 qualified ones were taken
where Go refuses them; of nine received cells built for the target, six built in
silence. **A
value of no name is asked what it is in every shape it comes in -- a receive is one
of them** (A RECEIVE HAD NO TYPE gave it a Kind; the categories had none).
A PACKAGE TABLE BUILT FROM ARRAYS (2026-10-05, from the open loud items): a package
struct literal's array field from a variable, a row or a call was refused, the
fixup that zeroes such an element and copies it in afterwards (litFixup) needing a
NAME for the storage and a package initializer's assignment giving it none --
pkgInitAssign gives it the variable where the initializer IS the literal, and the
slice literal's package path renders its temporary with the fixups too. Sweeping
the row's ORDER found an older hole: a package array typed by a CALL's result,
`var late = mk(4)`, was registered only where it was emitted, so every declaration
ABOVE it read the name as no array (resolvePkgVarTypes asks pkgArrayInitShape,
inferCType having no answer for an array). And its first board run found a third,
older still and in the target alone: `static T x = {0};` for a struct holding a
two-dimensional array, or a struct holding one that holds an array, is refused or
mislaid by the target's static initializer (measured in C: "expected initializer
list", "Bad initialization size: expected 56 got 96"), so `var g = mkGrid(4)` never
built; a package variable of a type holding an array is declared bare, C zeroing
it (staticZeroC). gcc took `{0}` in every shape, which is why no run case had met
it. **The host's compiler is no witness for an initializer's layout** -- the
target's static initializer has its own rules (A STATIC INITIALIZER IS LAID OUT BY
ANOTHER RULE THAN ITS TYPE).
The same day a Modbus RTU slave -- a register bank of arrays, function codes
dispatched through a package table of METHOD EXPRESSIONS, exceptions as a typed
error -- met the order row in the checker: methods were attached to their types
(registerMethod) in the same source-order walk that checks package initializers, so
an initializer above a method found the type without it -- a method expression, a
method value, a promoted one, a method's result, an interface the methods satisfy;
8 of 10 shapes refused in v0.48.1. Methods get a pass of their own after the type
bodies (methodDecls), as the bodies did on 2026-09-19 (typeBodies). **Package
declarations are in no order, and every pass that resolves them in source order is
a row of it.**

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
A RECEIVER is a position too (2026-09-24), found by two domain programs -- a bitset on
`type Set [4]uint32` and 3x3 matrices on `type Mat [3][3]float64`: `mk(5).Count()`
and `rot.Mul(rot).Mul(rot)` were refused in every position, the chain walks binding
a call's array result where steps INDEX it and not where a method is called on it
(`arrayOutCallC`, `chainResultCur`). Making that writable made `mkA().Put(1)`, a
POINTER method on the call's value, compile -- and the rule was nowhere:
`mka()[0].Set(1)` and `mkw().p.Set(1)` had compiled all along, calling the method on
the emitter's temporary (`callChainWalk`, which types every call of a chain now,
not only the last).

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
operands binding a call were probed the same way and held. A for clause's POST is
the one clause emitted inside another statement, and what it bound went into the
FOR's prologue, ahead of the loop, running once (2026-09-24: `i, j = i+1,
f(7)+f(8)`, an index with an effect and an array result beside another value, each
silent; emitOwnPrologue) -- a new lowering that binds is asked where its bindings
land. The STORE rules had it
as well (2026-09-24), the one place it cost memory safety: a reference to the frame
was refused in a package variable and judged by block in a local, both asked of the
target's ROOT -- so `p := &gq; p.p = &x`, `getq().p = &x`, `w.q.p = &x` and `*pp =
&x` put a local's address into a package variable in silence, and read through
after the function returned it was garbage. A store through a pointer or a call's
result writes what that reaches, which is refused unless the pointer is known to
point at a local of this frame, whose block then decides (`checkStoreThroughRef`,
`targetThroughRef`). Found while making targets through calls writable, which would
have widened it: the rule was asked on the way, not by a sweep.
**KNOWN MEANS MUST** (2026-09-24). The first version of that rule believed the holder
marks, `frameHolder` and `frameBacked` -- and a mark says what a variable MAY reach:
a pointer or a slice assigned again keeps the mark of its first value, and a slice's
mark is the union of its elements'. `p := &n; p = &gq; p.p = &x`, `s := loc[:]; s =
gs; s[0] = &x` and a range value over `[]*Q{&n, &gq}` all passed, the last two in
the SLICE rule, which had believed the marks all along. A rule that PERMITS on what
it knows needs what is true on every path: a local written ONCE (`bindWrites`), its
own address never taken nor a method called on it (`bindSelfAddr` -- not
`bindAliased`, which counts an element's method too and refused a run case), whose
one value (`bindValue`) is directly the frame's -- `&n`, `loc[:]`, a literal, a make.
A rule that REFUSES on what it knows may believe a mark; one that permits may not.
Finding the second version's false refusal found an old bug: `emitMain` never
cleared `curParamOrder`, so main ran with the previous function's parameters. The
receiver rules were the third place the same mark permitted (`storageBehind`,
`checkRecvAt`, `checkIntoArgs`): they now carry `sure` beside `local`, the store INTO
a receiver or through an argument relying on sure, the question whether the receiver
is KEPT still on local. `grep -n 'frameHolder\[\|frameBacked\['` lists the readers
of the marks; each is a refusal or asks onceBound first. The price, measured on local
linked lists: `nodes[i-1].next = &nodes[i]` and `n := &nodes[i-1]; n.next =
&nodes[i]` pass, a CURSOR, `cur.next = &nodes[i]; cur = cur.next`, is refused -- and
so is `p := &nodes[2]; p.next = &nodes[3]` in a function that declares another `p`
anywhere, `bindWrites` counting by NAME across the function (the scan has no scopes
the emitter's can be matched to); renaming is the way round it. The run corpus and
400 fuzzer seeds had none of either.
**A STORE RULE IS ASKED AT EVERY STORE SITE** (2026-09-25). The four store rules --
a package variable, a block, a slice's element, a pointer or a call's result -- were
asked by each place a program stores on its own, and each asked a subset: a list
form none of the slice's, a for clause and a range clause neither a pointer's nor a
slice's, a place a list fixed ahead none of the pointer's, and a target written
through a dereference, `(*p).p = &x`, nothing at all -- every gap a local's address
in a package variable, in silence, found in one sitting by writing each rule into
each site. They are one function now, `refuseStore` over a `lifeTarget` (a base,
stars and steps, or the pointer of a dereference, `derefPlace` answering what it is
KNOWN to hold), and the marks one beside it, `noteStoredThrough`, which also marks
what a known pointer points at. **A new place that stores calls both**; a for
clause's and a range clause's target are read by `exprStoreTarget`. Two traps met
on the way: a reference READ out of a holder built by readHolderRef named no
referent, so the block rule judged it by the block being emitted and refused `q.p =
xs[0]` in an if -- every reference from a mark is built by `originRef` now -- and
fixing that opened a hole the range clause had been refused by accident, which is
why the rules went in first. A PARENTHESISED target was the same row one level up:
the checker keeps targets by name, and `(*px), s = ...` -- or `*px, s = ...` --
shifted every value one target along, both ways (`addTarget`, `checkParenTarget`);
a batch of 27 went from 18 disagreements with Go to none. The SUMMARIES had the row
too, closed the same day: a callee's store through a pointer was read where it was
a statement of one target, and in a list, `n, w.xs = 1, v`, a for clause, `for
...; w.xs = v`, or a range clause's value, `for _, w.xs = range vs`, it kept its
caller's local array in package storage in silence --
through a local, a parameter or the receiver, a for clause's store into a package
variable included (`throughStores`, `clauseStores`, `storesInto`). **A new way a
callee stores is read by those two and sunk by that one.**

**A PARENTHESISED HEAD IS A ROW OF EVERY SCAN** (2026-09-25). The emitter reads
`(x)`, `(&x).f` and `(*p).x` as Go does, and the passes that read a body BEFORE it is
emitted read a head by name alone, where a parenthesised one names nobody -- a pass
that counts writes, calls or receivers takes such a one for none. The binding scan
first (the rule believing a pointer written once, KNOWN MEANS MUST): `p := &n; (p) =
&gq; p.p = &x` stored a local's address into gq in silence, as did the same write in
a list, a clause, an if init and a select -- and a switch's `=` init was no write
even unparenthesised, its case having asked for ":=". The summaries next: a callee
calling `(keep)(v)`, `(dev).onData(v)` or `(c).m()` for a method keeping its
receiver was summarised as calling nothing -- a direct call, a value, a defer and a
go alike -- and `f(a[:])` left a local in package storage. And the range copy: `s :=
(a)[:]` or `(b).bump()` wrote an array under a range over it, which handed out the
write where Go hands out its copy (scanAliasedLocals, stmtMayWriteMemory). The
EMITTER had one such reader of its own, the select clause's receive target: `case (x)
= <-ch:` received into nothing, and an array element into the head's variable
whatever the target said (`headTarget` reads it as a list's target now); and so did
a select SEND, `case (ch) <- v:`, a range operand, `range (arr)`, a method value,
`(&c).inc`, and a statement on a call's result, `(pc()).inc()` -- each refused, each
read now as the form without the parentheses. The CHECKER's sends asked nothing of a
channel reached through `*pch` or `(ch)` (checkSendWalked walks it). And reading
`(mk(1)).Count()` as `mk(1).Count()` in the emitter made it build where Go refuses a
pointer method on a call's result: **a shape the emitter learns to read is a shape the
checker must refuse the same way** -- a parenthesised call chain is walked as the
chain it holds (callChainOf). A scan reads a head by SHAPE through `scanHead`, a Factor through `parenFactorShape` and a call value
through `shapeCall` (no local has a type yet, so `derefHead` and `parenHeadName`,
which ask one, answer nothing there). **A new scan reads heads through them, and a
new shape the emitter reads through parentheses is a row in each scan.**
A CHAIN in parentheses as a receiver, `(xs[0]).Inc(1)` and `(h.c).Inc(1)`, was the
next such shape (2026-10-04): refused as a value with no address, "cannot call
pointer method", and as a statement, deferred or started, "unsupported call target",
where Go reads it as the chain. The emitter reads it so now (parenChainSteps,
spliceParenChain) in each of those paths and in checkDeferLeaks; and frameRefOf,
which every sink asks, reads through it -- a row the receiver matrix caught missing,
`g = (arr[0]).Self()` having been refused only by accident until then.

**A CALL'S RESULT IN A LOCAL IS A ROW OF THE SUMMARIES** (2026-09-25). A callee's
summary followed a call where a sink stood over it, `gs = pass(v)` (derived), and
not once the result was in a local, `x := pass(v); gs = x`, which kept the caller's
array in a package variable in silence -- through every sink. summaryHolds binds
such a local to a "call@" name standing for the call, and each sink follows it
there (heldCalls). And a call's result handed on as another call's ARGUMENT,
`keep(pass(v))`, local between or not, nested or through a function value: an edge
from the caller's parameter to the outer callee's, gated by every call in between
handing that parameter back (`crossEdge.via`, `viaHolds`) -- the gates are asked in
the fixed point, since what a callee hands back is known only there. **A new way a
value passes through a call is a gate there, not a new edge kind.** And a call's
result anywhere in a value's SHAPE -- a literal's element, an appended value, a
conversion's operand, a sliced base -- and a METHOD's or an interface method's
result: `callExprsIn` finds the calls a value carries, `callsOfExpr` resolves each to
its callees, and every sink, hold and gate asks both; only a whole value that was a
function's call had been followed.

**A STRUCT OF MORE THAN FOUR WORDS PASSED WHERE A CALL STANDS** (2026-09-25) arrives
corrupted on the P2 -- shifted a word -- with only "incompatible pointer types in
parameter passing" from the target's compiler (doc/struct-call-arg.c: a call's result,
a nested call, a field of a call's result; a variable, a field, an element, a
dereference and a compound literal are right). The emitter binds a function's struct
result before passing it (hoistStructCallArg) and a method's before it becomes the
next receiver (chainReceiver); the second was missing until a calendar program's
`t.add(45).print()` panicked on the board and matched Go on the host. **A new place a
struct-valued call is handed on binds it first** -- and a domain probe is not verified
until it has run on the board.
The cause (2026-09-26, flexprop#113, a one-line fix tested natively: test_offline
588/588, every reproducer line right on a P2-EDGE, 1169 corpus programs unchanged but
the two reproducers): a struct that TypeGoesOnStack -- over 16 bytes, or any sub-word
member -- is returned as a COPYREFTYPE pointer to a copy, and CoerceAssignTypes
(frontends/types.c) sizes the by-value duplicate of such a CALL by TypeSize of the
reference, 4 bytes; a `return` of such a call takes the same branch, which is
doc/return-nonword-struct.c's fault, and the warning is the wrapped
reference-to-a-reference failing CompatibleTypes. The workarounds stay until a
regeneration carries the fix. A member selected off such a call, `f(g().in)`, is a
DIFFERENT fault (the call's result dropped: 0 on the board, and a failed assembly at
a non-zero offset), unfiled -- the emitter binds the call (hoistStructCallArg), so no
program reaches it.

**A RECEIVE HAD NO TYPE** (2026-09-25). `exprType` answered "unknown" for `<-ch`, so
every store rule said nothing of a received value but the one asked of a bare name
(checkRecvAssign): `h.s = <-ci` put an int into a string as far as the C compiler, a
comma-ok's flag took any target at all, and a select clause asked its target only
by name. Found sweeping select receive TARGETS -- the statement forms were the same
row, and nobody had crossed a receive with them. A receive is typed by its channel's
element where that is a Kind, and a clause's value is built as the `<-ch` its
grammar keeps in two pieces (commRecvValue), so a clause's target is asked what a
statement's sole target is (checkRecvIntoTarget). **A value with no type is a row
across the targets**: before trusting a store rule, ask what it says of each kind of
value, a receive, a conversion, a call's result, as well as of each kind of target.

**A REVIEW IS A PROBE FROM OUTSIDE THE SWEEPS** (2026-09-26). A code review of the
tree (REVIEW.md, handed over by the user) found three silent compiler faults and two
wrong verdicts in one pass, none in a row any sweep had drawn. An integer range's
variable and bound were int whatever the operand: a uint32 bound above the signed
maximum ran zero iterations and an int64 one was truncated, silent on the host and
the board alike, and `use(i)` for a uint8 bound was refused -- the range sweeps had
crossed the operand's SHAPES (a variable, a call, a constant) and never its TYPE, and
**a rule that declares a variable is asked which type Go gives it** (the operand's;
a conversion to a named type, `range Count(3)`, is untyped to exprType and is
resolved from its name). A select's clause operands ran out of order: what rendering
a later clause's channel or value hoisted went into the statement's prologue, ahead
of the clauses before it -- the placement fault the for clause's POST had
(emitOwnPrologue), in the other statement that renders clauses inside itself; **a
statement that renders parts of itself asks where each part's hoisted statements
land** (selectCase.pro). And a spread append left its two operands to C's argument
order where the ordinary append bound its in order (bindEffectOperands): **a lowering
with two branches asks the ordering discipline of both**. The two verdicts: `ogo
test`'s runner asked Skipped() before Failed(), so a test that failed and then
skipped passed its package, and `ogo fmt` of a path it could not read exited 0. Each
fix is a run case or a unit test, and the range one a spec test of the refusals Go
makes as well: a float operand, and an untyped constant no int holds, which the
target had wrapped to 0. The select lesson, swept the same day across every other
place a statement evaluates a part of itself conditionally or late, found two more
of the same fault and nothing else: an else-if's INIT (`else if p := mk(mark(2),
mark(3)); p.x > 0` ran the marks ahead of the first test, and whether or not it
passed -- the else-if's TEST had its place, ifElse, and the init did not; the nested
if is emitted with emitOwnPrologue), and a receive clause's TARGET (`case
arr[idx(mark(2), mark(3))] = <-ch:` ran the marks before the select chose, and with
the default taken; the arm binds and stores with its own prologue). Else-if tests,
switch cases, loop conditions, `&&`/`||`, literal elements, nested arguments, binary
operands, index stores, copy, min and max all hold. **Where a statement renders a
part of itself that Go evaluates conditionally, later, or repeatedly, that part is
rendered inside emitOwnPrologue**; `grep -n 'emitOwnPrologue(' emit.go` lists the
places that do. The range lesson, swept the same way -- every statement that
DECLARES a variable (the range's forms, the select's receive, the comma-ok forms, the
type switch, the header inits, a multi-result declaration) crossed with sized and
named types and the variable's `%T` printed against Go's -- found one more: a string
range's rune was an int in the checker and in the C (int32 since, both), and beside
it two faults of `%T` itself, a channel spelled by its C name and a function type's
named types unqualified (typeNameForT spells a channel, an unnamed array and a
function type around what they hold now), and a program declaring an `error` with no
string of its own, which did not compile (the table names ogo_string; a string
method brings the typedef in). **`%T` against Go is the cheap oracle for what type a
declaration gives**: one printf per variable, and the twin says which are wrong.
The same day's domain round -- a command parser, a fixed-point PID, a varint
protocol with a ring buffer, a cog scheduler, a stack machine, an embedding
hierarchy, a sort library and a formatter, all matching Go on the host and the board
-- and a THREE-PACKAGE program (sensor, proto, cmd/rig; the Go twin built by a
script that prepends package clauses, `mktwin.sh` in the scratchpad) found a
REGRESSION of one day: the range's named-conversion fallback had asked
exprNamedType, which names the ELEMENT of an indexed or sliced aggregate, so `range
bs[:n]` over a byte array was "an integer". No run case had ranged over a sliced
array; one does now. Following it: a slice of a variable, `x := arr[:2]`, carried the
element's type (a slice suffix is an Index node; indexIsSlice tells them apart, and a
slice of a variable of a defined slice or string type keeps THAT type), and a method
called on a variable of an UNNAMED type -- `n.foo()` for `n := 5` -- was asked
nothing, the emitter answering "unknown package n". The rule that refuses it now
(unnamedTypeString) was WRONG first: it took "a kind and no recorded name" for an
unnamed type, and the checker records a kind while losing the name for a value
produced from one of a named type (`d := a + b`, an append, a conversion in
parentheses), so it refused six run cases. **A rule that refuses on what the checker
knows must ask what the checker fails to record, not only what it records** -- and
the corpus guard SHOWED it: 1166 files after, 1172 before. **`corpusdiff.sh` reports
programs with no twin in the BEFORE listing only; a newly refused program is a file
MISSING from the AFTER listing, so compare the two COUNTS as well.** Fuzzer seeds
1301-1500 on a P2-EDGE with this compiler: 197 passing, 3 outgrowing a cog, none
failing.

**TWO DEFINED TYPES OVER ONE KIND ARE TWO TYPES** (2026-09-26). The name-losing
shapes the rule above met were a row of their own: `type A int; type B int`, a value
of A produced by a conversion (`a := A(1)`) or an operation (`a + 1`, `-a`, `a << 2`,
`(a)`) into every position wanting a B -- a declaration, an argument, a return, a
comparison, a field, an element, a send, an append, a method of B. Thirteen of
fourteen programs Go refuses were accepted (`scripts/rejects.sh` over rej_named),
where the WRITTEN `var a A = 1` was refused in each. Two causes, one rule:
checkDefinedType asks for a KIND on both sides and the value's NAME of exprNamedType,
and a conversion to a defined type had no kind (factorType typed a predeclared
conversion and a function call, not a defined type's) while an operation had no name
(exprNamedType named a literal, a variable, a call, a conversion, a field -- and
nothing built by an operator). A conversion has its type's kind and an operation its
operands' named type now (operationNamedType: the shared name of the typed operands,
an untyped constant taking it; a shift its left operand's; a comparison none). So:
**when a rule is gated on what the checker records, sweep the SHAPES that produce a
value of the type -- a conversion, each operator, parentheses -- as well as the
positions the value goes into**; the positions had been swept before (FIFTEEN RULES)
with WRITTEN variables, and the shapes never. And an `append` into a slice EXPRESSION,
`append(back[:0], a)`, asked nothing of its element, where `bs := back[:0];
append(bs, a)` was checked: **a builtin that reads a variable's declaration reads the
sliced variable's too** (sliceOfVar). The full suite then failed two EMITTER tests
whose programs were invalid Go all along -- `return c + 0` of a Counter where an int
was declared, and `*c = v` of an int through a `*Counter` -- written when the checker
was lax and never checked since; the second was a gap of its own (checkDerefAssign
asks the pointee's name now). **When a new refusal fails an old test, ask Go about
the test's program before doubting the refusal.** The shapes beyond the operators,
swept next (rej_named3): a range value and an element of a slice or an array
declared FROM A LITERAL had the element's kind and not its name (inferVarFrom
recorded `elemKind` for `xs := []A{1, 2}` and `elemTypeName` only for the kindless
elements), a typed CONSTANT named nothing (`const ca A = 5` is a ConstDeclaration,
and exprNamedType read variables), a RECEIVE named nothing as a value (`v := <-ch`
recorded the element's name on v; `var b B = <-ch` never asked), `min` and `max`
had no kind and so no gate, and the range clause's `=` compared kinds where both
names were in hand (checkRangeAssign, after its kind switch). Seven of fourteen were
accepted; all refuse now. **The value-producing shapes of a type are a list to walk
whole**: a literal, a variable, a conversion, each operator, parentheses, a call, a
method, a field, an element, a range value, a receive, a typed constant, a
dereference, a type assertion, min and max, append -- and each rule about a type is
asked of every one of them. The KINDLESS categories, swept the same way (rej_named5:
a struct, an array and a slice defined twice, through every shape and position), held
in 16 of 17 -- kindlessCategory has decided them by name since 2026-09-20 -- and the
one gap was `append`, whose result is its first argument's type. Across a PACKAGE
BOUNDARY the row was open again, whole: ten programs against a Go twin module (the
scratchpad's `mktwin.sh`, since rejects.sh builds single-file twins), and another
package's call result, typed constant, element and range value, and operations on
them, all passed as a second type of that package or as a local one. The rule was
the same and the names were in hand (qualifiedValueNamedType); what failed was the
KIND gate in front of it, three ways: qualifiedKind answered for a variable only (a
call, an element and a constant now), rangeElem asked local declarations only (an
imported slice, array or channel now), and the parameter of another package's
function is spelled `lib.Count` here, which nameKind's plain lookup does not find
(resolved where the type is declared now). **Every row swept in one package is
swept across the boundary too, with the qualified spelling in every position** -- the
2026-09-20 boundary sweep asked what a correct program does, not what an incorrect
one earns. The interface and member rules (rej_iface) and constants into defined
types (rej_const) hold through every shape.
A CONSTANT DECLARATION was the row's last position (2026-10-05, found by a domain
program's `panic(err)` sweep reaching `const c = S("q")`): a conversion of a constant
to a string or a bool type was no constant at all to the checker, "S is not a
constant" (constConversion folded numbers only; the emitter's string fold took
`string(x)` and no defined type, constRuneStringFactor), and with it open, what had
been refused by that accident came through -- and its integer twins had been taken
all along: a constant declaration's initializer was folded and walked by no rule
(resolveConst keeps the walk's "mismatched types" and asks checkDefinedType), a
constant declared without a type named nothing (exprNamedType reads its initializer,
where it is declared, once), a conversion to a PREDECLARED type named nothing where a
variable of it did (`int(a) + b`), and `&&`, `||` and `!` over a defined bool type
were no operation of it (operationNamedType, sharedNamedType). 8 of 8 int and float
programs of the row, 5 of 6 variable ones and all 4 of a third row were taken. That
third: `&&` and `||` were not FOLDED, "no constant meaning", so `const d = true &&
false` had no value and no type and `var x int = d` built; they fold with Go's
precedences now, comparisons first, then && and ||, which the grammar's one flat
level does not carry -- left to right, `true || false == false` is false. The
emitter folds a bool constant the same way (foldConstBool) and writes its value: it
had written the expression into a static initializer, `ogo_string_eq(...)` for a
string comparison, which no C compiler takes there. **A rule refused by accident
is a rule nobody wrote**: widening what is a constant showed four rejections that
had come from "not a constant", and the verdicts of 5,348 probe programs, HEAD's
against the tree's, showed nothing else moved.
The emitter had the operators' half SILENT: `x := p && q` of two Flags was typed a
plain bool (inferNodes answered every RelOp with bool), so `%v` printed `false` for
Go's String() of the Flag -- the value-producing shapes of a type are a list to walk
whole, and the logical operators were not on it (logicalNamedBool).

**A PROGRAM OF SIZE ASKS WHAT NO SWEEP ASKS** (2026-09-27). p2-11, a PDP-11 emulator
and the first OctoGo program of any size, handed over six findings
(`../p2-11/OCTOGO.md`, measured on a P2-EC at 160 MHz), and not one was a wrong
answer: the sweeps ask what a program computes, and a program of size asks what it
COSTS -- build time and memory, the length of a line of C, clocks an operation. Three
closed in one sitting. (1) `l.rx, l.request = control(l.rx, 64, l.request)`, a call's
results assigned to the fields it reads, took 22.8 s to build, and two such statements
ran the compiler out of memory in collectFuncCross: each target HOLDS the call's
result, and the walk over what a call's arguments hold (viaEdges) came back to the call
itself at every level, branching per argument -- exponential in their number, and
correct C at the end of it, which no corpus guard or run case can see. **A walk over
what names hold carries its path** (a call on the path is not entered again); the hold
graph has a cycle the moment a value is stored into what its call reads. And **a
build's TIME is a verdict**: the run case that holds the shape asserts the output, so
read the time of a domain program's build. (5) An array literal of 8000 uint16 was one
line of C of 72,000 bytes; mcpp's line buffer, NBUFF, is 65,536, and its fatal-error
path is `longjmp(error_exit)`, whose setjmp `internal/mcpp_main.c.diff` removed -- so
what the user saw was libc's "unsupported setjmp/longjmp usage" panic under `flexcc
crashed:`, the NAME of that path, not the preprocessor's message. A line over 4000
bytes is wrapped at `, ` outside literals and a string constant written as adjacent
pieces of 1000 (wrapLongLines, cQuote; TestEmitCLongLines): **a line's length is a
limit of the backend** like a cog register. Any other mcpp fatal error still ends in
that panic, until a regeneration keeps the setjmp (the ccgo pin note under Code
generation). (6) `ogo fmt` disagreed with gofmt in three places -- the comment column
of a const block where only the first spec has a value, `for i := 0; ; i++`, the
second line of a call's arguments -- on sources that ARE Go once given a package
clause. TestFormatMatchesGofmt runs the RUN CASES through gofmt, and no run case had
those shapes; **a domain program's source through both formatters is the formatter's
probe** (TestFormatGofmtColumns holds these; gofmt's tabwriter rule is that a trailing
comment is a CELL, so the cells before it align). The other three are the backend's
handling of the C, all in clocks. (2) A named constant was `static const int n =
1000000;`, so the backend read the constant from hub RAM (`rdlong`) where it was
used: 71 clocks an iteration for a loop bound against 60.5 for the literal -- and the
read decided INLINING, the backend inlining by size: `return m.traps&3 != 0` inlined,
`return m.traps&aborts != 0` for `const aborts = 3` called. CLOSED the next day, with
the finding below: a constant is its value where it is read, and the named program
builds to the literal's binary. (4) A runtime check is a call:
`ogo_nil_machine_ptr(m)` at a receiver's use and the index check each call a helper
with a branch in it, which by (3) is not inlined, and a function calling one grows
past the inline threshold itself -- a method testing a field of its receiver 215
clocks checked and 24 with `--unchecked`, `m.r[i&7] += uint16(i)` 79 against 36.
CLOSED 2026-09-29 for the small functions, which the backend is TOLD to inline (see
"A CALL IS DEAR" below). (3) A call and its return cost about fifty instructions, a
switch is a chain of compares in case order (one of sixteen cases 160 clocks on
average), and only a body without a branch is inlined by the backend on its own;
that is the backend's, and stays as a fact programs are written against (the
emulator, rewritten for it, runs 5.3x).

**A CALL IS DEAR, AND THE BACKEND INLINES WHAT IT IS TOLD TO** (2026-09-29). The
check was the least of what a runtime check cost: the backend inlines by itself a
function of about six IR instructions (`AnalyzeInlineEligibility`, spin2cpp's
backends/asm/optimize_ir.c), which a one-line method is until it has a check in it,
so the checked build CALLED what the unchecked one inlined, and a call and its return
are pushregs and popregs in hub execution, 100 clocks and more. flexcc reads the
keyword `inline` and ignores it; what it listens to is `__attribute__((inline))`,
written after the declarator of a DEFINITION and nowhere else (before the type it
breaks the type, on a prototype it does not parse), which raises the limit to a
hundred instructions. The emitter writes it as `OGO_INLINE`, a macro that is nothing
off `__FLEXC__`, on a function `inlineCandidate` takes: six statements at most at any
depth, parameters AND result scalars of the C, called by name somewhere, statements
times call sites 96 at most, and no defer, go, select, label, goto or function
literal. On a P2-EDGE at 160 MHz, checked: a method testing a field 51 clocks a call
for 211, a setter 67 for 229, two accessors 102 for 395.
Every number in that policy is a COST measured, and the costs are the backend's
registers and the program's size, not time: **every inlined copy brings the registers
of its locals to where it lands**, and nothing gives them back between the arms of a
switch. Marking every function failed four run cases and p2-11; a budget of eight
statements failed p2-11 (`fit 480 failed: pc is 486`) where six builds; a helper of
six statements called in four hundred places made 20 KB into 81; a result of two
ints is a struct of the C, four registers a copy (84 for 36 in a switch of twelve
cases); and an array or a struct holding one, lowered to a pointer and copied to a
local, is a function the backend inlines under no mark, so the mark would say
nothing. **A policy is measured by what it breaks**: the corpus dump read through a
classifier (the marks stripped, 1185 programs the C they were), both dumps through
`scripts/cccorpus.sh` (no run case and 11 of 400 fuzzer seeds outgrow the cog
marked, all "fit 480 failed"; of 1156 built both ways 285 are smaller and 191
larger, 0.35% in all), p2-11's build (38 marked, 371,924 bytes for 365,320,
4.7 s for 3.3), and the backend's inliner itself on bodies larger than it takes on
its own -- `make board` with every function of three statements marked, green --
before the policy was written.
And **no program fails to build for having been made faster**: `ogo build` and `ogo
test` compile the marked C, and where the backend answers "fit 480 failed" or
"exceeded local register limit" they emit again without the marks and compile that,
saying nothing of the first attempt (`compileMarked`, `outgrewCog`) -- a program
that fits neither way is told what the second said, which is what it was told
before, and since 2026-10-03 which of its own functions hold the most local
registers, read from the listing (`cogHint`; the backend names none); any other
failure is reported as it is. A domain program's `main` of six sections, 140
registers, built checked and outgrew the cog only `--unchecked`: the C is the same
and the backend's allocation is not. `--no-inline` asks for it outright and builds v0.45.0's
binary, for the benchmark and for p2-11 alike, which is how a regression is told
from the inlining. The fallback is whole -- a program near the limit loses every mark
at once -- and a stepped one is where to go if a program shows the cliff.
What the backend does with a mark is its own: a recursive function stays a call, and
so does one that leaves through more than one jump, a label needing a unique jump to
be removed -- ONE early return is inlined and two are not, so `nz(v, sign)` of two
tests and three returns was marked and called, 83 clocks. A single exit reached by a
label for EACH return is inlined (measured in C first, with the if-else chain, the
nested ifs and a flag, which are inlined as well), and `singleExit` writes a marked
function of three returns or more so: its value in `_ogo_rv`, `{ _ogo_rv = v; goto
_ogo_exitN; }` for each return, the labels and one return at the end -- 29 clocks
for nz, 27 for 68 for clamp. It rewrites the TEXT of the body, which emitFuncDecl
holds in a buffer, and nothing it is not sure of: a body where the word return stands
anywhere but on a line of its own is left alone. The rewriting is the same on the
host, which is what verifies its meaning; the mark is nothing there, so **that the
copies are right where they land is verified on the board and nowhere else**, and
what holds the inlining off the board is the LISTING: `TestBuildInlines` asks that
the checked program has no call of an accessor left, and that `--no-inline` has
five. The fuzzer writes a small function of several returns into every program
since 2026-09-30 (see the smith notes): the rewrite is in 1996 of seeds 1-2000, and
seeds 1-200 on a P2-EDGE pass with it inlined, 15 outgrowing a cog and none failing
-- until then the lowering was in 7 run cases and no seed. The marks before it:
seeds 25-200 on a P2-EDGE with this compiler, 168 passing, 8 outgrowing a cog as
they did before and none failing, 167 of the 176 programs with a function marked.
It is an OPTION of the emitter, `Inline()`, as `Checked()` is, and not its default:
thirteen tests pin the C of a small program verbatim, and bare `EmitC` stays the
plain C they pin. **Whatever emits what a build emits passes both** -- `ogo build`
and `ogo test`, the run tests, the fuzzer's oracle, `scripts/dumpc`, dumpcorpus.sh's
test -- and a new such place that passes only `Checked()` tests a program nobody
builds.

**A CHECK THE PROGRAM PROVES IS NOT WRITTEN** (2026-10-02). p2-11's profile (its
OCTOGO.md, 3) put a checked build 14% behind an unchecked one, and two of the
reasons were checks that could not fail. A receiver read a field at a time was
nil-checked at every read: `ogo_nil_Machine_ptr(m)` six times in a method of six
statements, each a branch, and enough of them that the backend would not inline
the method. And `m.R[ir>>6&7]` over eight registers was bound-checked, the backend
seeing no more through `ogo_bound` than that it is a compare. A pointer parameter or
receiver the function never writes, declares again or takes the address of
(nilQualifies: bindWrites, bindSelfAddr) is checked by the first statement of the
body's own list that dereferences it on every path, and by none after it
(emitTopStatement, nilDerefStmt): what that list has run is run on every path to the
statements after it and to whatever is nested in them, a label clears it, a goto
reaching it past them, and a lifted literal starts its own. "On every path" is read
off the AST and confirmed by the check having been emitted; not counted: a
branch, a loop, the operands after the first && or ||, an address taken, a deferred
or a started call. The gate is in nilCheckedC, by the pointer's C text, since a dozen
sites call it with their own. An index is unchecked where its shape keeps it below a
constant extent (indexInRange, indexBound): a constant, `x & k`, unsigned `x % k`
and `x >> k`, an operand of an unsigned type. Measured statically on a method of the
emulator's shape: 131 instructions and 22 calls checked, 68 and 8 without the two,
and inlined. Of the corpus, 353 of 1201 programs lost 663 nil and 32 bound checks
and changed in nothing else but one lowering: a compound assignment through a
checked pointer had bound the target's address (`_ogo_t = &guard(f)->x`), and
through a plain one writes `f->x op= v` (scripts in the scratchpad: the classifier
unwraps every check in both dumps and asks that the rest be equal). **A check the
emitter writes is a cost the program pays at every run; one Go's own compiler
proves away is one this one should**, and the board, not the host, says what it
was worth.
The same day a reader of the Parallax forum thread pointed at the `getword`s in
p2-11's listing, and the same reasoning took two more: a cast narrowCType asks for
is not written where the level's value cannot leave its UNSIGNED narrow type
(levelFits, operandFits: an AND where either side is in range, a right shift, a
division, a remainder and an AND NOT where the left is, an OR and an XOR where both
are; a longer level's prefix is its own level, cast or proved, so it is in range),
and a switch on an integer narrower than int holds its tag in an `int` (the
switch-guard path of emitSwitchGuard), which the backend compares without extending
it again at every case. The backend zero-extends at each cast AND at each read of a
narrow variable, so the second matters as much as the first. On the host gcc
promotes every narrow operand to int, so a dropped cast changes no C type there; the
target's compiler types mixed operations its own way (flexprop#114), so **a change
to how narrow values are written is measured on the board**: 55 generated programs,
1,400 expressions over uint8 and uint16 with every operator, edge operands and
nesting, all equal to Go there. p2-11: +4.1% checked, +3.6% unchecked. Not done: a
narrow LOCAL held in an int, which would end the read-side extensions too and has to
narrow at every store.

**A READ IN A LOOP IS MADE ON EVERY PASS** (2026-10-03). p2-11's PASM core work asked
whether the backend may keep a package variable in a register across a loop, which a
cog spinning on a flag another cog writes, and its console's ring buffer, rely on not
happening. Go calls such a program a data race and lets the compiler hoist; OctoGo has
no `volatile` and no sync/atomic. The user chose to DEFINE it (specs.go, "Memory shared
between cogs"): a loop makes its reads on every pass, none kept across a wait; a cog's
writes reach Hub RAM in program order, so a value written before its flag is read
after a test of the flag; with no write, branch, loop or wait between them, reads may
be merged or reordered and two writes of one variable merged; a value of 32 bits or
fewer is accessed whole. What makes it true is the backend at its DEFAULT -O1, which
`ogo build` passes (no -O2): its read merging (FindNextRead, optimize_ir.c) stops at a
label, a branch, a write and a wait, its CORDIC reordering moves reads past reads only,
and -O1 has no CSE. **The rule is known at -O1 only**: -O2 adds CSE, which may reuse
inside a loop a value computed before it (loopCSE, cse.c), and aggressive-mem, which
lets FindNextRead cross conditional jumps -- one spin shape tried at -O2 kept its read,
which says nothing of the rest -- so a change of flags is measured against
TestBuildSharedVariables
(internal/build), which reads the listing: every loop of a ring-buffer program reads hub
RAM or calls what does, checked, unchecked and `--no-inline`. The run case "package
variables two cogs share" holds what it does on the host and the board. Measured first
on the board: five spin shapes (a global, fields through an inlined pointer method, a
bool with work in the body, an element through an inlined call, a loop writing another
global), checked and unchecked, each with its read inside the loop.
Checking the rule found a SILENT backend fault older than it: flexcc's simple loop
conversion (CheckSimpleIncrementLoop, loops.c, -O1) rewrites `for (i = 0; i < n; i++)`
whose body does not use i as a count down from n read ONCE, asking nothing of n but
that it is a constant or a name -- a local the body lowers, a global, a global a callee
lowers all ran their first count (doc/loop-bound-read-once.c: 5 5 5 for gcc's 3 3 3; flexprop#119, reported the same
day with a fix tested natively: test_offline unchanged, of 1261 programs only the
reproducer built differently).
OctoGo reached it through a for whose first clause ASSIGNS a variable declared before
it, `var i int; for i = 0; i < n; i++`; `for i := 0; ...` is a declaration, which the
conversion does not match. The emitter writes an assigned first clause ahead of the
loop since, `i = 0; for (; i < n; i++)` (one corpus program of 1210 changed); the run
case "a for loop whose body lowers its bound" printed 5 5 5 on the board before it.

**A CONSTANT IS A VALUE, OF THE TYPE IT MEETS** (2026-09-28). p2-11's next finding
was a silent one: `b-a < patience`, for a `const patience = 10000` and a uint32
difference past 2^31, was true on the board and false in Go and on the host. A named
constant of 32 bits or fewer was a C OBJECT, a `static const int`, and an object has
a type of its own where a Go constant takes the type of what it meets. flexcc
compares an unsigned operand with a signed one as SIGNED numbers unless the signed
one is a constant expression of no negative value (CompileComparison and
IsUnsignedConst, frontends/types.c), saying "signed/unsigned comparison may not work
properly"; and it types an operation of two operands of one size by its LEFT one
(MatchIntegerTypes), so `c / u` was a signed division for a signed c -- on the host
as well, where the divisor's zero check handed it back as an int
(`doc/mixed-sign-operands.c`, measured on a P2-EDGE; flexprop#114, reported the same
day with a change giving C its own rules, tested natively: test_offline 588/588, and
of 1222 programs 121 binaries differ, each printing on the board what it printed
before; fixed upstream 2026-10-05 by spin2cpp bf72f733, 7.8.0-beta, with that change,
its two comparison rules widened from C to every language -- measured against the
C-only form on the same tree, no binary of ours moved: 1302 C programs, the run
cases', the fuzzer's and doc/'s, and 492 Spin and BASIC sources, spin2cpp's tests
and p2-11's objects, for P1 and P2). The emitter's spellings stay whatever upstream
does, the C they write being right under either rule. A bare literal had
been taught this long before (a `u` suffix in an unsigned level); the row is the
SHAPES an untyped constant is written in -- a literal, a rune literal, a name,
another package's name, an iota name, a parenthesised expression of them, a constant
written as a float -- crossed with the operators whose answer depends on the sign
(`< <= > >=`, `/`, `%`) on either side, for each unsigned type and a defined type
over one. Swept with a generated program per type, every shape but the bare literal
was wrong for uint32, uint and a defined type, and for uint64 the literal was too,
whose guard handed the divisor back signed (ogoNonzero64u). **One question, one
helper**: untypedOperandC spells an untyped constant operand for the integer type it
meets, in a level and across a comparison, the type resolved past its definition
(levelUnderlying). And the constant is no object any more: an integer constant is
its VALUE where it is read (intConstRef), as the 64-bit ones, the floats and the
strings were, which closed the clocks of a named constant with it: on a P2-EDGE a
loop bounded by one ran 69.0 clocks a pass for the literal's 56.5, a loop calling a
method of one line reading one 72 for 32, and both build to the literal's binary
now. **Two programs that should cost the same are compared by their BINARIES**: the
same sha256 is the same clocks, and needs no board.
Every reader of the fold maps matters once a constant is read from them, and a
sweep of SCOPES found them wrong three ways, each older than the change. The maps
were asked for the bare name first, and the main package's constants ARE keyed by
their bare names, so a library's `const Name` was main's inside the library's own
code (constKey resolves a name as a name is resolved: a block's constant, then the
CURRENT package's). A block constant's value, width and class outlived its block
(enterScope restored constInt and not constVal, constWide or constHuge). And a
declaration recorded over an older one of its name kept the older one's flags, the
maps being set and never cleared. The checker had its part: a block's constant is in
scope AFTER its spec, `const n = n + 1` reading the n outside it, so its expression
is read before its name is declared (declareConst in check.go), and the emitter
reads a spec whole before it records any of its names (evalConst, then declareConst:
`const n, m = n + 2, n + 3`). **A map keyed by a source name is asked through the
one function that knows what the name means here.**
A constant written as a FLOAT is the same row from the other side: `var u uint32 =
3e9` stored 2147483648 on the board, flexcc clamping a double converted to a 32-bit
unsigned, `var t int64 = 1e9` did not build, and `u + big` for a `const big = 1e4`
was 1333788678 for Go's 3000. There the type wanted is known to
the checker alone, at the one funnel every constant meeting a type goes through
(checkValueOverflow), which records it for the emitter by the expression's place in
the AST, as shiftTypes is recorded (wholeConsts, wholeConstC). NOT at a conversion,
whose operand is the conversion's to lower: recorded there, `int64(float64(x))`
handed ogo_f2i64 a long long, which the host took and TestTargetBuild refused for
the backend's warning. Traps met. A change that touches every program using a
constant is past the corpus guard's reading, 140 of 1173 programs: it is read
through a CLASSIFIER -- each constant's value written for its name in the BEFORE
text, suffixes and temporaries normalised, then diffed -- which left 22 to read one
by one, and the 81 fuzzer seeds among them were run on the board. A sweep program
with a package variable nothing reads fails the host build on -Werror, which is the
fixture's. And dumpcorpus.sh numbers its files by POSITION across the tables, so
three cases added to the first table renumber the ones after: corpusdiff compares by
content and is right, a tool pairing files by name is not. And the fuzzer declared NO
constant, which is how the row lived through every sweep on the board, until
2026-09-30 (see the smith notes): its first sweep found `x &^ 6e3` complemented as a
double, the one operator of the row nobody had crossed with a float spelling.
An untyped constant's OWN type was the row's last cell (2026-10-03, OctoSmith seed
722 in a board sweep of v0.48.0): evalConst typed one by its operands, so `zero =
big - big` for a `big` past 32 bits was a 64-bit constant of value 0, spelled `0LL`
wherever it was read, and the target's compiler passes a 64-bit CONSTANT to a 32-bit
parameter as two words -- every argument from it on read wrong, `f(1, zero-38, 3)`
-1177923906 for -277, with only "Bad number of parameters" said, which `ogo build`
passes on and does not fail. An untyped constant whose value fits an int is an int
now, Go's default type for it; a 64-bit EXPRESSION argument is converted by the
prototype and was right. **A warning the backend gives a user's build is a fault
until shown harmless**: the sweep tool sets WARNED apart, and this was its one.
And a NAMED constant past 32 bits on the LEFT of an unsigned 64-bit `/` or `%`
(2026-10-04, OctoSmith seed 3298 in a board sweep of c81020d, 3001-3500: 469
passing, 30 outgrowing, this one failing): `big / u` was `408166956050LL / u`,
the row's literal having been taught and the name left to the fold, which spells
it signed -- 2^64 - big on the board for Go's 0, the host right. A constant past
the int64s, `1 << 63`, was the same in every spelling, untypedIntOperand reading
it as no int64. untypedOperandC spells both `ULL` beside a uint64
(untypedUint64Operand); only TestEmitCWideConstUnsigned holds it off the board.
**The fuzzer's board sweep finds what the host oracle cannot: a spelling only the
target's typing rule reads wrong.** The row was then generated whole, each unsigned
type and int64 and int32, plain and defined, with a constant as a literal, a name,
another package's, an iota, in parentheses, typed and as a float, on either side of
/, %, <, <=, > and >=, against a variable at its edges: some two thousand
expressions, all equal to Go on the board, four lines of the uint64 one wrong
before the fix. Its first draft found the neighbour Go REFUSES: an untyped
constant shifted by a variable count takes the type it would have alone and must
fit it, and `x := 408166956050 >> n`, `var y uint8 = 300 >> n` were taken --
18132866 and 5 on the board (typeShiftOperands asks intKindRange since).

**WHAT A CALL HANDS BACK IS ASKED ONCE** (2026-09-28). p2-11 could not return an
error that came of a call given a local buffer, `if err := fill(buf[:]); err != nil
{ return 0, err }`: a declaration marks its variable with what its initializer
reaches of the frame, asking frameRefOf -- which asks the callee's summary -- and
then WALKED INTO the initializer as into a composite literal, a call's arguments
among its parts. So a result declared from a call held whatever the call was given,
in the seven of eleven binding forms that declare. The walk stops at a call whose
callee is summarised (summarisedCall); one nothing summarises is walked into still
(NewBuilder(back[:])). **Two answers to one question are a false refusal or a hole,
and the second answer hides what the first lacks**: the walk had stood in for what
frameRefOf did not ask, and the row -- every form a result is bound by, against a
callee handing its argument back or not, reached each way a callee is -- found 44 of
184 programs handing a reference to the frame out ACCEPTED, none through the walk: a
call through an INTERFACE handed back nothing (ifaceRetSummary); the values a
variadic call packs are what its variadic parameter HOLDS and the pack is an array
of the CALLER's frame, and they were asked as parameters of their own positions
(packAt, packedRef); a literal called where it stands, a method expression and a
method on a conversion were asked which parameters they hand back and not what those
hold. All ask callArgsRef. **A new way to call is a row there, and a new kind of
parameter -- packed, received, bound -- is asked what it HOLDS.** The checker's part
was found on the way: an array, a struct, a pointer, a channel and a function's name
were taken where a slice is wanted in every position, the mirror of a rule that was
there (checkRefAssign's "pointer wanted"), and a spread was held to the ELEMENT's
type. Its first version asked nonBoolOperand what the operand is, which names the
ELEMENT of `back[:]`, and refused sixteen run cases; the corpus guard's count showed
it. The destructured forms, `n, err := r.Read(buf[:])`, are the same row and held.
Known and not a fault: `return buf[0], fill(buf[:])` reads buf[0] before the call
here and after it in gc, an order Go leaves unspecified.

**A STATEMENT IN A HEADER IS THE STATEMENT** (2026-09-29). p2-11 wrote `if two();
ok {` and met a syntax error, which specs.go had on record: "Go also admits a send
or a call standing alone there; those are not provided". Status, not design. The
row is Go's SIMPLE STATEMENT -- an expression standing alone (a call, a receive), a
send, a step, nothing at all -- in every place a header takes one: an if's init and
an else-if's, a switch's, a for's init and its post. Probed before a line was
written, it held a SILENT fault older than the gap: the for TOOK a call for its
init and lost the loop. Its second ";" was read as its first is, in the checker and
in the emitter alike, which made an init of the condition it followed and a loop
with none of the loop: `for one(); n < 2; n++` was C's `for (n < 2; ; n++)`, built
for the target without a word, never calling one and never ending. The host's
compiler refused it, a statement with no effect, so no run case could hold the shape
and none had been tried on the board. **A form the host's compiler refuses is a form
nothing tests, and is run on the board by hand.**
The grammar reads a header as an expression first and decides by what follows, so
a statement standing there is not in the tree as the statement it is. Rather than a
rule for it in every pass, **the statement is put back once** (headerStmt,
headerstmt.go): an AssignHead and its Postfix made of the header's own tokens, built
once for a header and kept, since what the checker records of its parts by their
place in the tree is what the emitter looks up there. The checker hands it to
checkStatement and the emitter to emitStatement, in the block the header opens, and
a for's post that is more than one expression goes to the end of the body behind the
label a `continue` jumps to, as a post that needs a temporary does. So a call's
several results are discarded, a send is asked what a send is asked, and `x + 1` is
"evaluated but not used", with no rule written for any of it -- the for's post had
asked nothing of an expression but its names, and `for ; n < 2; x + 1` built.
The passes that read a body BEFORE it is emitted read shapes, and are the rows a new
shape has to be swept across: the statement in each header position against what it
does with a reference to the frame, direct and through a callee's summary
(TestEmitCHeaderStmtEscape). One hole, where it was looked for: a call is found in
the expression a header begins with, by every walker of expressions, and a SEND is
not an expression -- a callee sending its parameter from an if's init was
summarised as keeping nothing, and a local's slice went to another cog through it.
`eachStmt`, the walker the summaries share, hands out a header's statements and a
parenthesised one as the statements they are, and the binding scan reads them so.
The range copy, the init order, termination and scopes held. A statement in
PARENTHESES, `(f())` and `(<-ch)`, was the neighbour the rejects batch found:
refused on a line of its own as in a header, where Go allows both (parenStmt). 212
programs of the batch agree with Go. And the fuzzer writes a call in a header since,
which is what keeps the row where no probe reaches (see the smith notes above).

**THE FORMATTER'S ORACLE IS GOFMT ON A PROGRAM'S OWN SOURCES** (2026-09-28). p2-11
reported one shape, `b < c` on the line after `return a < b &&` coming back under
the `return`. It was a row -- every continuation line but a call's arguments and a
literal's body -- and go/printer's rule is per NODE of Go's tree: an indent at a
line break, taken back after what followed, so the levels add and follow Go's
precedences where the grammar has three flat levels (markIndents, indentBinary).
Then p2-11's sources went through both formatters (`scripts/fmtcmp.sh`), and eleven
of thirty-six differed where TestFormatMatchesGofmt agreed on all 777 run cases:
the sources were in `ogo fmt`'s layout, so `ogo fmt -l` said nothing, and the run
cases have no table of values with comments, no keyed literal of several lines, no
field without a comment between two with one. Every difference was a COLUMN. gofmt
aligns nothing itself: go/printer ends a cell with a tab, text/tabwriter makes a
column of a run of consecutive lines with a cell there, and a form feed begins a
new section. The formatter had four mechanisms, each measuring or remembering its
own way with its own idea of a run; it has one now, the tabwriter's
(format_table.go): cells and sections decided ahead of the walk (cellsBefore,
commentCells, sectionBefore), marks recorded where tokens LANDED, the text laid out
afterwards (alignCells, layoutTable). **A width is read off what was written, never
measured from the tree**: the measured one was four columns out for `[]int{1,
2}[0]`. **What begins a section is go/printer's and is copied, not derived**:
exprList's arithmetic over element sizes (a line of several elements, a size out of
proportion to the geometric mean of its neighbours'), a case, a parameter, the
operand continuing an expression, the statement after one of several lines, a
comment on a line of its own. A run is lines of ONE indentation, which is not the
tabwriter's rule and is safer than it: gofmt indents a selector chain after a line
with a trailing comment two levels, an accident of an indentation cell sharing a
column with text, left alone here with the blank line gofmt puts ahead of a comment
written in the column of a group's closing parenthesis. The line breaks came
last (format_break.go): what begins a line decides what is indented and what is
aligned, so it is decided first (markBreaks), and every test of "is this written
across lines" asks the OUTPUT's lines (spansLines), not the source's. Found on the
way, each
older than the change: a comment at the end of a file was written TWICE, the EOF
token being the tree's last and the flush after the walk writing its separator
again; a comment ahead of any closing brace stood with the brace; gofmt's blank line
between declarations of different kinds was never put in -- nor is it after a
declaration with a trailing comment, which is go/printer's doing and what gofmt
writes. **A formatter test holds its expectation against gofmt where there is one**
(gofmtCheck): an expectation written by hand is the formatter's own opinion.

**PRINTING A VALUE IS A ROW** (2026-09-23). `printf("%v", x)` of an ARRAY printed the
address of its storage wherever x was not a bare name -- a literal, a field, an
element, a row, a call's field, a deferred capture -- silently on the board, and
only a variable and another package's array printed its elements, which is all the
run cases printed. Found by a probe of something else: the fix for the binding above
printed an array argument, and the array read wrong for a reason of its own. So a
print is swept like a store, across every way a value is reached, for each kind of
value; and for arrays the multi-dimensional ones too (%v prints them row by row since,
and the element-wise verbs since 2026-09-25, emitElementwiseArrayND). A TYPING question asked of every printed
argument must be cheap and pure: `arrayShapeOf` renders a call it passes, which lifted
a function literal argument a second time -- the corpus guard showed it as one changed
program -- so it is asked only of what `inferCType` cannot type. A CALL's array
result was the cell nobody printed until 2026-09-24, `println(mk(1))` refused where a
deferred print took it: the print binds it with its other arguments now
(`hoistPrintArgs`, `arrayCallArg`).

**A TEMPORARY IS A COG REGISTER** (2026-09-20). flexcc gives every C local one
(`local_N res 1` in COG_BSS) out of a pool the assembler checks with `fit 480`,
shared across functions so the deepest call chain is what spends it -- and overflow
is a build ERROR, not a warning: `error: fit 480 failed: pc is 483`. Binding one
more value per print, to get printf's argument order right, broke `TestTargetBuild`
on a run case whose C was correct; the fix was to bind only the arguments a method
called while formatting can REACH (`printArgUnreachable`). So an emitter change that
binds values asks which of them it has to, and `grep -c '\tres\t1' prog.p2asm`
measures what a program spends. It gives back a SCALAR temporary -- a hundred
blocks of `int t = f(k)` spend 21 -- and never a STRUCT-valued one or a compound
literal, so those are paid at every site: forty-five hex dumps in one function,
each binding a string header for the helper, spent 173 and failed (2026-09-25). And
flexcc INLINES a small static function with its locals, so a thin wrapper that
builds a struct for a helper puts the struct at every call. Measured on those 45
dumps: a converter call 127, a compound literal of pointer and length 61, a string
passed whole to a helper it is not inlined into about two a call, and a pointer and
a length read twice from a variable 19 in all -- so the byte helpers' bodies take
the pointer and the length (`_pl`) and the string forms are the wrappers
(`emitBytesVerb`). The costliest shape was the commonest: every constant string was
the compound literal `(ogo_string){"s", n}`, two registers at each call taking it,
so a command dispatcher of 25 `strings.HasPrefix(line, "...")` cases and 25 prints
spent 276 and failed, and a function of 200 such calls took FIVE MINUTES to be
refused, "exceeded local register limit" -- flexcc's time grows about as the cube of
such a function. A constant string is a file-scope header, named where it is used,
since (`stringLitName`); the same programs build in a second, at 35 whatever their
size. So a new lowering is asked what it hands a call BY VALUE. The rest of the row
is OPEN, measured the same day at 60 calls in one function: a slice of an array,
`sum(arr[:])`, 194 and failing; a struct literal, `area(P{k, 1})`, 250 and failing;
an interface conversion, `show(&gq)`, 131; and even a struct VARIABLE, `area(q)`,
133 -- in plain C that one is free, and the cost is flexcc INLINING the small callee
at each site with its struct parameter as a new local. A constant one could be a
static as the strings are; a general fix is the backend's allocator, which gives
back scalars and not structs.

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
parameter for it -- an array BESIDE another result is one since 2026-09-24, held in
the result struct as its typedef and written through an out parameter,
resultCTypeIn); a function literal called and then read through, `func() P { ...
}().x` ("a function literal may only be called where it stands"), for any result
type, and one returning an ARRAY called at all, `func() Set { ... }()`; a method of
a call's array result deferred or started on a cog, `defer mk(5).Count()` and `go
mk(5).Count()` (as a value and a statement it works since 2026-09-24, and with the call
parenthesised, `(mk(1)).Len()`, since 2026-09-25, spliceParenArrayCall); an index or a method called directly on a literal of a DEFINED slice type,
`IS{4, 5}[1]` and `IS{6, 7}.Sum()`, "this form is not supported yet" (factorLitIndexed
and factorStructLitChain take a bracketed type and a struct; the literal is typed as a
value since 2026-09-25, when a print of one had read its header as an integer); a
field a BRACKETED literal lacks, `[]P{gp}[0].in.nosuch`,
"a []P literal cannot be read through this suffix" from the emitter, where the valid
form works (a call's result, a parenthesised value and a named literal are checked at
any depth since 2026-09-25, `getp().in.nosuch` and `(&gp).in.nosuch` being "type Q
has no field nosuch": walkSteps' missingAt); `[]byte(s)`
and `[]rune(s)` of a string VARIABLE (a copy of a length known at run time; a
constant's converts since
2026-09-23, constBytesConv); a type switch behind an init statement, `switch
f(); x := v.(type)` (every other header takes every simple statement since
2026-09-29, see A STATEMENT IN A HEADER IS THE STATEMENT); a parenthesised function value started on a cog,
`go (h.f)(2)`, which works as a value, a statement and deferred; a deferred or
started call through a dereference with an index, `defer (*ps)[0](x)` and `go
(*pa)[0](x)` ("unsupported call target", and go's "only `go f(args)` ..."), where
the statement and the value work since 2026-09-22; the method expression of an INTERFACE type, `Shape.Area` (refused by name; a concrete
type's works since 2026-09-19); a method value on a local or a call's result (design: a method value binds its
receiver at compile time); an unnamed struct type mixed with the SECOND of two
declared structs of its fields (its typedef names the first, aliasAnonStructs), which
Go admits and the target's compiler refuses; printf's `%v` of an
interface under a width, and of a struct whose fields reach a pointer, an interface or
an exported String() (an interface value prints what it holds since 2026-09-20,
ifaceHeldPrintC, but the chain writing it cannot pad; a struct of numbers, strings,
bools and structs, arrays and slices of them prints under a spec since 2026-09-25,
a helper per type and spec, needStructSpecPrint -- written out at each print, a
function of thirteen overflowed the cog register pool); a PARENTHESISED HEAD as a TARGET where peeling does not reach it:
a conversion's `(*T)(p).x = 5` and `*(*T)(p) = 5` (`(p).x = 5`,
`(&p).x = 3` and the same through an index, an increment and a compound assignment
work since 2026-09-20, parenTargetBase, and a head that is a CHAIN, the send
`(&bus.ports[1]).ch <- 5` among them, since 2026-09-22, addrChainSteps). READING through one
works since 2026-09-20 (`emitParenChain` binds the head and walks the steps from
it), and a declaration from one since 2026-09-22 (`parenChainType`): `(&p).x`, `(get()).x`, `(*getp()).x`, `(getq()).a`, `(arr[1:])[1]`,
`("hello")[1:]` and `(&arr)[1]`, beside the `(*p).x`, `(a)[i]`, `(v).m()`,
`(&v).m()`, `(*T)(p).m()` and `(a - b).m()` that always did. A struct head is a
VALUE there, so a slice step on one is refused as Go refuses it; a dereference of
a conversion parenthesised as a TARGET, `(*(*[]int)(p))[0] = 4` ("only assignment to
a simple variable"; bound to a variable first, it works); `len` or `cap` of a BRACKETED conversion and a
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
these; reading the grammar did not. Two more on 2026-09-24, the same way: a later
target of a list took no call, `gp.y, getp().x = 7, 8` (LhsItem), and a select
clause required a semicolon before its closing brace, `select { case v = <-ch: a = 1
}`, which a switch clause and a block did not (CommClause) -- the three statement
lists are meant to read alike, and one did not. And one on 2026-09-29, from a
program of size: a header took no statement standing alone, `if two(); ok {`, which
specs.go had recorded as "not provided". **Check Factor and HeaderFactor
against each other when either changes**; they are meant to differ by one production
(HeaderFactor has no literal after a name, which is what keeps `if x == T {` a block).
(`ogo fmt` kept a statement's body on the line it was written on, `if c { v = 1 }`,
until 2026-09-28: gofmt decides every line break but an expression's, and
format_break.go decides them with it -- a function's body stays on its line where
go/printer's arithmetic says it is short, which is known once it is WRITTEN, so
FormatFile runs a pass more for each body found too long. The run cases were
gofmt's layout already; the fuzzer's programs were not, 21 of the first 150.) An
array a program only MEASURES, `var bound [n]int` read by `len(bound)` alone, is a C
local nothing reads, the length being folded: the target builds it and the host
harness's -Werror refuses it, so such a program cannot be a run case as it stands. (A
header's "=" list, `if a, n = k(3), n+1; ...`, was spaced as one value until
2026-09-25: the depth rule, headerInitTightOps, predated "=" in headers. A run case
met it first -- TestFormatMatchesGofmt runs the run cases through gofmt, which is how
formatter gaps surface.)
Latent ones, measured and not faults today: a store through a chain, `r.m[a()][b()]
= v()`, leaves its calls to C's operand order, which gcc 14 and flexcc both take left
to right (only the bare `name[i] = v` path binds them); and a value's call does not
run ahead of an index or nil panic in the target, `arr[bad()] = side()`, where Go
runs `side()` and then panics -- the comment in emitIndexAssign says otherwise and
describes one C compiler's choice. Only a program about to panic can tell. A method
called on a CONVERSION as a print argument, `println(T(3).bump(), g)`, was a fourth
until 2026-09-24: `pureCall` answered for the conversion and not for the call after
it, so the print was not bound and the host read g first (nodeHasEffect). A
variable read beside a call, `println(g, f(), g)`, is read in lexical order here,
1 4 2, where gc reads it after the call, 2 4 2: Go leaves that order unspecified.
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
there, and is crossed with `defer` and `go`: a deferred call asked the rule only of a
receiver one step from its variable (checkDeferLeaks), so `defer h.c.Save()` and
`defer arr[1].Save()` kept a local's address in silence until 2026-10-04 -- 8756 on
the board for Go's 7, read after the function returned.
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
An ARRAY's element was no receiver to the summaries (2026-10-04, found probing the
parenthesised chain): methodCallOf followed an index into a SLICE only, so a callee
calling a method that keeps its receiver on `p[1]` for a `p *[2]T`, on `p.arr[1]`, on
a defined array type's element or on its own receiver's, was summarised as keeping
nothing, and the caller's local array was left in a package variable -- 8796 on the
board for Go's 7. An array's element is its own storage, no hop; a pointer-to-array
parameter is a root as a slice parameter is (`TestEmitCSummaryArrayElems`). Beside it,
three readers that took a narrower shape than their neighbours: summaryReach read no
address through a parenthesised head, `&(*p)[1]`, and a deferred or started method
was a call to the summaries only one step from its variable, `defer p.m()`. An array
of arrays is followed a row at a time (`curArr`, arrDim.row), and a method ending on a
row is its defined type's, `p[1].KeepFirst()` for a `*[2]Row`. **A new way to reach a
receiver is a row in methodCallOf, under a plain call, `defer` and `go`.**
A TYPE ASSERTION and a HEADER'S DECLARATION were rows nobody had written into
the lifetime matrices (2026-10-05, found by a domain program's decoder, which hands
out pointers into a caller's local pool, and the escapes written beside it). An
assertion hands back the pointer its operand holds, and no reader looked through
it: `keepT = r.(*T)`, the comma-ok and the type switch's variable kept a local's
address in a package variable in silence, -60641451 on the board for Go's 7
(assertionOperand, read by frameRefOf, summaryReach and callExprsIn as
unsafeConvOperand is; typeSwitchNameMark gives each clause's name the operand's
mark, a bound operand's from its expression). And the summaries read no binding a
header makes: eachStmt handed out a header's expression and send statements
(headerStmtsIn), never `if p := r;` or `switch p, n := r, 1;`, so a callee keeping
its parameter through one was summarised as keeping nothing -- 8664 for 7, in
v0.48.1 too. headerBindingsIn builds the statement such a header binds by, the
values rewrapped in the ExpressionList a statement writes, for the six passes that
read statements by shape. **A new place a pointer can stand -- an operand, a
header, a clause's name -- is a row in TestEmitCAssertLifetime's matrix, and a
statement form a header can hold is a row of eachStmt.**
The same day the emitter learned an assertion in the MIDDLE of a chain on any
operand, `h.i.(*T).s`, `pick().(*T).bump()`: chainCText binds the value reached and
asserts on the binding (hoistAssert), once per occurrence by the step's token in the
statement's memo -- a compound assignment renders its target twice, and the first
version bound and checked twice -- and the field walkers hand a chain holding an
assertion to it (emitAccessChain, accessChainType through renderedChainType). On the
chain's own variable, `e.(*P).x`, the variable is the operand, no copy: the first
version copied it, one cog register more, and the corpus guard showed it. Enabling
the lowering let a program Go refuses through, `var k int = pick().(*T).s`, which
the emitter had refused in its own words: the checker's walk past a call had no
assertion step (walkSteps, given the scope the chain is written in, callChain.at).
**A lowering that makes a shape writable makes it a row of the checker's walks.**
And an address through what a local HOLDS, `&h.p.n` for a pointer field p, was
the address of h to both the checker (escapesFrame) and the emitter (addrOfRoot),
which asked the root -- A RULE ASKED OF A ROOT MISSES WHAT A POINTER REACHES, from
the permissive side: a false refusal of an address into a package variable. An
address whose steps reach a pointer or a slice before the last is the root's holder
mark's question now (addrCrossing, addrThroughRef). Asking accessChainType from
frameRefOf reached chainCText's assertion step in a pass with no locals, a nil map
written -- the unchecked run of a run case crashed; the step answers nothing there.
A FIELD of another package's struct with a method on it, `f.ID.Node()` for an `f
*lib.Frame` (2026-10-05, a frame router of two packages): fieldTypeName answered a
field's type name only where it was this package's, so the method was asked of the
Kind the type is defined over, "type uint16 has no method Node", in v0.48.1 too; and
callChainWalk gave up at any variable of another package's type, so once the method
was found its arguments and result were asked nothing. The walk goes on through
such a field and takes the package as its home there, as its method step does.
A store through a pointer no NAME holds was the summaries' next row (2026-10-04,
found making `(*f())[i] = v` writable, which the emitter had refused): a callee
storing its parameter through a call's result, `gethp().p = v`, `fa()[0] = v`, in a
list or a for clause, was summarised as storing nothing, and `keep(&x)` left a
local's address in package storage -- v0.48.1 builds it. Such a store is taken to
outlive every frame (storedThroughUnnamed, exprThroughUnnamed for a clause), the
pointer's end being out of sight; TestEmitCSummaryThroughUnnamed. **Making a shape
WRITABLE asks the summaries what they read of it** -- the callee form is a store
nothing had reached while it did not compile, and the shorter spelling beside it had
been open all along.
The row whole, the next day: a star over a call (`*pq() = v`, its call the last step),
a method on a call's result keeping its argument (`gethp().set(v)`: methodCallOf had
no case for a receiver that is a function's result, and fieldMethodSuffix took no
call ahead of the method), `copy` -- read as no store at all (copyStores: the
source's ELEMENTS into the destination, a package's or an unnamed one sunk, a name's
through it) -- and an element of a slice PARAMETER (pointerParamSlots takes one
whose elements can carry a reference, so the call site decides by what it passed).
Five holes, all in v0.48.1. The summaries read SHAPES, so the first version of the
slice slot refused an encoder, `dst[i] = p.payload[i]`, as keeping p's contents --
the corpus guard's one new .err file -- and the element type decides since
(carriesReference; copyMayCarry for a copy's destination, a package array's from
arrayVar). The price left: pointers copied into a LOCAL buffer through a callee are
refused, the summary having no slot for contents stored through a parameter. The
net for false refusals was every probe program of the scratchpad, 4,250 of them,
through HEAD's compiler and the tree's: identical verdicts, and p2-11's binary
byte-identical. **A new summary rule is run over every program the probes left
behind, not only the corpus.**

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
since 2026-10-03 -- see A READ IN A LOOP IS MADE ON EVERY PASS.

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
