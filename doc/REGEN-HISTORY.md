# Backend regeneration history

The record of every regeneration of the flexcc backend
(`internal/flexcc/ccgo_*.go`), newest first, as CLAUDE.md's Code generation section
carried it until 2026-10-14: the pins, why each was adopted, what was measured before
adoption and on the board, and the faults each cleared or carried. CLAUDE.md keeps
the procedure, the latest entry and the facts that still stand; the next
regeneration's record goes here, at the top, and replaces the entry CLAUDE.md keeps.

**Backend regenerated 2026-10-25 at the same pin, spin2cpp `b5993a84`, with a third
fix of its own** (inside flexprop `v7.7.0`; ccgo v4.36.1, libc v1.77.1):
`internal/outasm.c.diff`'s second hunk, SplitLocalStructs, for a cost, not a fault. A
local of several longs held in registers -- a struct, a slice or string header, a
64-bit value -- is a base register with a subregister a long past the first, and
RenameSubregs keeps its longs in consecutive registers; ReuseLocalRegisters gives no
subregister back, so every such local held its registers to the end of its function,
and every slice a function passed to a call, a temporary of its own, held three. A
Go-shaped main outgrew the cog's 480: dom63's and dom65's had to be split
(doc/complit-arg-registers.c, the limit's reproducer since 2026-09-25, 210
registers). The hunk splits such a local, before the function's first
optimization, into one local a long wherever nothing reads its longs as a block --
its register's address taken for an indexed access (an IMM_COG_LABEL of its name),
inline assembly, an instruction after a SETQ -- and ReuseLocalRegisters then treats
them as it treats an int's. Measured natively before adoption, b5993a84 with the
three carried diffs against the same with the hunk: forty slice arguments of every
spelling took 120 registers and take none, their longs moved straight into the
argument registers (build/probes/regs); the reproducer builds in 21, dom65's unsplit
main takes 9 for 117. spin2cpp's `make test_offline`: 15 of its asm goldens changed,
each read -- nine only reserve no register for a local the optimizer had already
removed, six shorter and right, two of them making functions identical that the
backend then merges -- and 552 of 552 pass on the updated goldens. Of 1503 programs
(the run cases, seeds 1-400, doc/) 1165 build differently, 44 that did not build now
do, and on a P2-EDGE every one prints what the old backend's build printed (the run
cases and seeds held to the host's run of the same C, the reproducers to the old
backend's own run; build/probes/s2c/boardcmp.py): a test's elapsed time aside, the
held faults' lines read registers nothing wrote both ways, and two loads lost to the
loader matched rerun. doc/register-limit-crash.c, the backend's crash past its
register limit, no longer reached the limit in its shape of thirty struct locals; it
is a hundred local arrays indexed in place now, which the hunk leaves together, and
crashes both ways. Each builder's turn changed its own target only (the fold
expanded and hashed after each), and the five scripts/flexcc lists are the native
build's but for doc/switch-case-span-overflow.c, which the linux transpiles' qsort
never finishes, before the change as after. Programs come out smaller where they fit
before -- p2-11's 390 KB main 2.4 KB, obeam's checked VM 3.8 KB -- but a marked
function now small enough to inline is inlined: obeam's unchecked VM, its arena sized
to the room its checks leave, outgrew Hub RAM by 3 KB with its marks, so a build
outgrowing the hub is built plainer as one outgrowing the cog is (compileMarked,
outgrewHub), and it builds unmarked 3.4 KB smaller than before. p2-11's 72 tests
pass on the board; host suite and `make board` (1031 passing, seed 6 outgrowing a cog as before) green.

**Backend regenerated 2026-10-14 at the same pin, spin2cpp `b5993a84`, with two
fixes of its own** (inside flexprop `v7.7.0`; ccgo v4.36.1, libc v1.77.1):
`internal/functions.c.diff` and `internal/outasm.c.diff` beside optimize_ir.c.diff's
CORDIC hunk, both for a value of two to four longs, which the target passes and
returns in registers. An ARGUMENT of one whose expression has a side effect was
evaluated once per long -- `take2(*get2())` called get2 twice, OctoGo's
`takeS(strs[idx()])` idx twice and `takeX(rows[idx()])` three times, a value
receiver, a send and a string comparison alike -- because CheckFunctionCalls passes
such an argument through `_parm_` temporaries by a multiple assignment and
BuildExprlistFromObject splits a non-call right side into one member reference per
long, each evaluating it again (doc/multi-long-arg-evaluated-per-long.c, the held
fault #13 widened); the fix evaluates the side effects of what the argument reads
through, an index and a pointer, into temporaries of one long first
(ExtractArgSideEffects), and splits the 64-bit branch's identifier rather than the
original expression. A MEMBER past the first long of one a call returns was read
from a register nothing wrote, AST_METHODREF offsetting the call's first result
(doc/member-of-struct-result.c); the fix takes the call's result of that index. The
emitter had bound a call before reading such a field everywhere but `len(<-ch)` and
`cap(<-ch)`, fixed the same day in the emitter as well. Both faults are old: the
backend before built the reproducers to the same binaries. Measured natively before
adoption, b5993a84 with the CORDIC hunk against the same with both fixes: spin2cpp's
`make test_offline` 588 of 588 under both; of 1423 programs (the run cases, seeds
1-400, doc/) 192 build differently, and every one prints on a P2-EDGE what it
printed before (ten loads lost to the loader and rerun) -- most of them a nil check
of the pointer an argument is read through, run once where it ran once per long --
while the two reproducers, an OctoGo probe and the new run case print gcc's and Go's
values. A first version of the functions.c fix handed the argument to
ExtractSideEffects, the assignment's helper, which copies an array whose base has a
side effect into a temporary of the array's type: the run case "probe round 20:
bit-packed telemetry" printed 23 mismatches on the board, and the narrower
extraction replaced it. Each builder's turn changed its own target only (the fold
expanded and hashed after each), and the five scripts/flexcc lists are a native
build's of the same tree but for doc/register-limit-crash.c, a crash both ways. Sent
upstream the same day as spin2cpp#519. Host suite and `make board` (999 passing, seed
6 outgrowing a cog as before) green, the run cases "a value of several words passed
where its expression calls" and "len and cap of a value received" among them.

**Backend regenerated 2026-10-13 at spin2cpp `b5993a84`** (upstream's master of
that day, 7.8.0-beta, inside flexprop `v7.7.0`; ccgo v4.36.1, libc v1.77.1),
`internal/optimize_ir.c.diff` carrying the CORDIC hunk alone: #118's went
upstream as our PR #516 (63ef4515), and upstream's optimize_ir.c with the
CORDIC hunk is the very file the old pin and both hunks made (blob c28ff3bc).
Adopted for upstream's fix of flexprop#122 (`(z << 32) >> 32` of an unsigned
long long refused, doc/uint64-shift-32-pair.c), which also carries our PR #517
(-Oinline-pushregs, off unless asked for; `ogo build` does not) and a BASIC
comparison change (708d178b) that leaves C as it was. Nothing under include/,
sys/ or Lib/ changed, so cnames.go needed nothing. Measured natively before
adoption, old pin plus both hunks against the new plus the CORDIC one, over
1419 programs (the run cases, seeds 1-400, doc/): five run cases build
differently and print on a P2-EDGE what they printed before, and the reproducer
and seed 359 build and pass where they were refused (seed 852 likewise). The
fix is narrow: #8 and #12 of the held faults (doc/uint64-widened-shift-32.c,
doc/cast-of-64bit-expression.c) build to the same binaries. Each builder's turn
changed its own target only (the fold expanded and hashed after each), and the
five scripts/flexcc lists are a native build's of the same tree but for
doc/register-limit-crash.c, a crash both ways. TestBuildBackendRefusal's
program built from then on; it is a name past mcpp's 64 KB line buffer since.
Host suite and `make board` (997 passing, seed 6 outgrowing a cog as before)
green, the run case "a uint64 shifted left by 32 and back" among them.

**Backend regenerated 2026-10-08 at spin2cpp `1d709b25`** (upstream's master of
2026-10-05, 7.8.0-beta, inside flexprop `v7.7.0`; ccgo v4.36.1, libc v1.77.1),
with `internal/optimize_ir.c.diff` carrying a SECOND hunk beside #118's: the
CORDIC reordering's FindBlockForReorderingDownward took a CONDITIONAL write for a
definition that meets a dependency, so a max's `if_be mov result1, #69 ; mov t,
result1` moved below a QMUL past code overwriting result1, silently
(doc/cordic-reorder-conditional-move.c; fuzzer seed 6978, see the smith notes).
#118's hunk went upstream as our PR, spin2cpp#516, merged unchanged on
2026-10-11 (63ef4515), so a pin at or past it drops that hunk; the CORDIC
one is not filed yet. The pin carries upstream's fixes of #114 (bf72f733) and #115 (639a8c8e); the
emitter's workarounds for both stay, costing nothing, and so does widenU32, whose
fault (doc/widen-zero-plus-unsigned.c) bf72f733 fixed too. Measured before
adoption, natively: test_offline 588/588; of 1366 programs (the run cases and
fuzzer seeds 1-400 dumped, doc/) 131 build differently from the old pin, and on a
P2-EDGE each prints what it printed before but four reproducers, which print
gcc's values now (the CORDIC one, mixed-sign-operands, octal-escape and
widen-zero); the second hunk alone moved only its reproducer, p2-11
byte-identical. Each builder's turn changed its own target only (the fold
expanded and hashed before and after each), and scripts/flexcc of all five
builds the 1366 programs to one cccorpus list, a native build's of the same tree
but for doc/register-limit-crash.c, a crash both ways. Since macOS 27 the Mac's
darwin/amd64 turn runs with DEVELOPER_DIR and SDKROOT naming the Universal CLT
26.6 (its script, regen-darwin.sh, sets them). No new C library names. Host
suite and `make board` (928 cases) green; seed 6978 passes on the board, and
seeds 1-400 on a P2-EDGE: 377 passing (nine on a second load), 22 outgrowing a
cog, seed 359 refused by the backend (doc/uint64-shift-32-pair.c), none failing.

**Backend regenerated 2026-10-02 at the same pin with a fix of its own, and with
go.mod's ccgo v4.36.1 and libc v1.77.1** -- spin2cpp `eb263961` (v7.7.3, still
upstream's master), flexprop `v7.7.0`, `internal/optimize_ir.c.diff` carried
again: TransformConstDst folded the flags of a CONDITIONAL instruction whose
operands became constants as though it always ran, so `if_e cmp f, #0 wz` --
run only where an earlier compare set Z -- made the jump after it
unconditional, and `(12 != g) || f` was false for a stored `f = (v < v)`,
silently (`doc/conditional-compare-fold.c`; found by fuzzer seed 1284, which
failed its checksum on the board and passed on the host). Five lines: return
where the instruction is conditional, as every other place applying known
flags already asks. Measured natively before adoption: test_offline 588/588; of
1550 programs (the run cases and fuzzer seeds 1-400 dumped, the doc/ battery,
seeds 1001-1300) six build differently, all six right on a P2-EDGE with the fix
and seed 1284 wrong without; p2-11's binaries byte-identical. The first
transpile from ccgo v4.36.1: each builder's turn changed its own target only
(the undup fold expanded and hashed before and after each), and scripts/flexcc
of all five builds the 1551 programs to one cccorpus list, a native build's of
the same tree -- but for doc/register-limit-crash.c, which crashes both (139
native, a Go panic in the transpile). REPORTED 2026-10-02 as flexprop#118, one
issue bundling this fault with the suggested fix, the nine-deep hang below and
a question about the cost of pushregs_/popregs_ in hub code (prototypes
measured, not sent). The mcpp setjmp retry the ccgo note below names was not
attempted this time.

**Base flexspin hangs at nine nested calls through functions without a frame**
(measured on a P2-EDGE 2026-10-02, a chain `int fN(int x) { return fN+1(x ^ N)
+ N; }`, eight deep right and nine silent): a non-leaf function with nothing to
save gets no frame and keeps its return address on the 8-level hardware stack
while it calls. Not worked around; no generated program is known to reach it.
Reported in flexprop#118.

**Backend regenerated 2026-09-21 at spin2cpp `v7.7.3`, and the carried diff is
gone** — `eb263961`, the tag flexprop's master points at, inside flexprop
`v7.7.0`, ccgo v4.34.6. Upstream fixed all three faults `optimize_ir.c.diff`
carried: #109 and #110 closed 2026-09-20, and #111 was fixed by spin2cpp
`3911e536` while the issue was still open. The fixes are upstream's own, not the
suggested ones: #109's leaves an instruction alone when it SETS C, or merges no
pair where either sets a flag, read or not (broader); #110's asks only about the
flags the READ's condition uses (narrower, and exact, since CondIsSubset has
already said the read runs only where the write did); #111's is the same. So
they were measured before adoption, native builds of the old pin with the diff
against v7.7.3 without it: on a P2-EDGE the three reproducers, and the two older
ones that are #109, print gcc's values; the 34 compiling `doc/` reproducers are
byte-identical and the four refusals refuse alike; of 1632 programs (the 632 run
cases, fuzzer seeds 1-1000) 1619 come out alike, 35 of them a "fit 480" refusal
under both, and 13 keep two immediate adds setting a carry nothing reads, which
the diff merged (8 bytes) -- all 13 pass on the board under both; test_offline
588/588 under both. Faithful: the regenerated flexcc builds the battery and all
1632 exactly as a native v7.7.3 does, the refusals included.

**All five platforms, the same day**: linux/amd64 and windows/amd64 on the
second dev machine, linux/arm64 on `rpi5` and both darwin backends on
`darwin-m1` from the first, the one that reaches the builders -- each builder's
2026-09-15 clone removed first, since the generator then reused any clone it
found (it refuses one at another pin now, `checkClone`).
`scripts/flexcc` built for each platform compiles doc/ and a corpus dump, 1670
programs, to five identical `scripts/cccorpus.sh` lists, equal to a native
v7.7.3's (windows's under wine, its builder being off). Host suite and `make
board` (657 run cases) are green on linux/amd64.

**Backend regenerated 2026-09-15 with two fixes of its own** — spin2cpp
`3840014f` (7.7.3-beta, its master of 2026-09-05) inside flexprop `v7.7.0`, plus
`internal/optimize_ir.c.diff`, ccgo v4.34.6. Adopted for two SILENT optimizer
faults, each reported upstream with its part of the diff as the fix: a
regression the 08-29 regeneration let out, two rewrites that disturb the carry
of a 64-bit add or subtract of a constant (`doc/add-immediate-carry.c`,
flexprop#109), hidden until then by the `-Ono-inline-small` that went with that
regeneration; and an old one, the second of two `if` bodies updating one global
starting from a stale register (`doc/conditional-load-dropped.c`,
flexprop#110). Both found by widening the on-board fuzzer sweep, to 120 seeds
and then 1000. The flag was not restored: it costs 1.4x-3.3x on hot loops. The
`doc/` battery, pinned against regenerated on a P2-EDGE: five reproducers
changed -- the carry fault and the two older entries that turned out to be it
(`doc/negate64-then-add.c`, `doc/int64-min-spelling.c`, whose emitter
workarounds stay, costing nothing), #107 cleared, and #108 only partly (`round`
still clamps outside int range, so `math.Round` stays built on Floor). The other
25 compile byte-identically under both, and the three compile-time refusals
refuse as before. Faithful: every compiling reproducer is byte-identical under
the in-process flexcc and a native build of the same commit with the same diff,
and upstream's `make test_offline` passes 588/588 with the diff, as without.

**Regenerated again the same evening for a third** -- an OLD silent fault, a
signed compare of two values the optimizer knows decided by their overflowing
32-bit difference: `-7 < 2147483647` false (`doc/signed-compare-overflow.c`,
flexprop#111; found comparing wide constants against Go on the board, and in
OctoGo a saturation check inlined with a constant argument). Its hunk joined
the diff; the same checks, the same way: only that reproducer changed in the
battery (31 byte-identical, the three refusals as before), 32/32 faithful to a
native build, test_offline 588/588, five platforms byte-identical, and of 1438
programs -- the run corpus and fuzzer seeds 1-1000 -- no binary changed.

**Backend regenerated 2026-08-29 at upstream's tip** — spin2cpp `2bd01c4c`
(7.7.2-beta, its master of that day) inside flexprop `v7.7.0`, the wrapper and
the compiler pinned separately (`flexpropRef` / `spin2cppRef`), ccgo v4.34.6.
Adopted because flexprop#105 was fixed upstream on 2026-08-20 and no release
carried it nine days later, which was the standing decision. What it cleared,
each measured on a P2-EDGE pinned against regenerated with the whole `doc/`
battery re-run: the unwritten-element multiply of flexprop#105, the constant
divide (`doc/const-divide-miscompile.c`), the compound assignment of
flexprop#106, and the two optimizer faults (#103, #104) that `ogo build` had
passed `-Ono-inline-small -Ono-peephole` to avoid — **both flags are gone**, and
with them the up-to-87% hot-loop tax (`internal/build/build.go` keeps the
numbers as history). Nothing else moved: eighteen of the twenty-four reproducers
compile to byte-identical binaries under both backends, so every other
workaround stays. The transpile is faithful — every reproducer compiles
byte-identically under the in-process flexcc and a natively built spin2cpp at
the same commit. One regression carried in knowingly: unary plus on a float
(flexprop#107) is a *warning* upstream where v7.7.0 refused it, and it
miscompiles; the emitter never writes one (`doc/unary-plus-float.c`).

**Backend first generated 2026-07-20** against the `v7.7.0` pin (flexprop repo
and `spin2cpp` submodule both at `v7.7.0`) using **ccgo v4.34.6**;
`mcpp_main.c.diff` applied cleanly then and again in 2026-08. Post-regen chore:
`rm -rf internal/flexprop internal/flexprop_install` (git-ignored build clones
that otherwise break `go build ./...` / `go test ./...`). The flexcc `--help` golden in
`internal/flexcc/all_test.go` needs no manual refresh — its volatile
`Version … Compiled on: …` line is normalized (`versionLineRE`), so even a
version bump doesn't force a golden edit.

**v7.7.0 changed nothing we depend on** (measured 2026-07-20). Every flexcc bug
the compiler worked around was re-measured against it, on a P2-EDGE, and every
one still reproduced identically: the dropped argument slot for an unnamed parameter,
the miscompiled `static inline` rendezvous, and the four compile-time refusals
around structs holding arrays and designated initializers. The upgrade is
hygiene, not a fix — keep the workarounds.

The channel hang that used to head that list was **not** a flexcc bug at all.
It was a livelock in this compiler's own rendezvous, exposed rather than
caused by FCACHE; see `doc/rendezvous-livelock.c` and `chanRuntimeDefs` in
`internal/octogo/emit.go`. Builds no longer pass `--fcache=0`.

**windows/amd64 added 2026-07-22** (`ccgo_windows_amd64.go`), cross-compiled on
linux/amd64 with MinGW (GCC 14) and the ccgo CLI v4.34.7 against the same v7.7.0
pin. `GOOS=windows GOARCH=amd64 go build ./...` is clean and `go vet` shows only
the usual generated-code noise. Verified on the windows/amd64 builder + a real
P2-EDGE: `ogo.exe build` produced a **byte-identical** binary to the linux flexcc
(same sha256), and `ogo.exe loadp2` detected the P2 on COM5 and transferred it,
`chksum: bc OK`. The cross pass needs `main2lib` fixups the linux one does not
(all gated on `goos == "windows"` in `main2lib`, so linux is untouched): two
codegen rewrites (a `libc.Xgetcwd` int32→Tsize_t length, and a miniz `~mask`
that folds to a uint32-overflowing -4096), plus redirects of the six libc
functions that are `panic(todo())` stubs for windows to
`supplement_windows_amd64.go` — `XGetModuleFileNameA`, `Xtime`, `Xungetc`,
`Xabort`, `Xstat` (real but forwards to the `Xstat64` stub), and the
`freopen`-of-`flexcc.go` split. The nine `--prefix-undefined=_` danglers
(`_remove`, `_powl`, `_strncat`, …) are also defined there. `ungetc` is done via
`fseek(-1)`, valid because flexcc only ungets just-read bytes of seekable source
files. The generated file is **not** gofmt-clean out of ccgo; the generator
gofmt's the per-target files itself before the fold, whichever path produced
them. The ccgo CLI version the cross passes use is the one `go.mod` pins for
the library (`go install modernc.org/ccgo/v4@<that version>` into a scratch
GOBIN first on PATH), so all five transpiles come from one ccgo. Every transpile
committed so far is ccgo v4.34.6's with libc v1.74.1; **go.mod pins ccgo v4.36.1
and libc v1.77.1 since 2026-09-27** (the upgrade alone changed no binary: 1218
programs byte-identical under `scripts/cccorpus.sh`, the host suite and `make
board` green), so the NEXT regeneration is from that pair, all five platforms in
one go. It is also where `mcpp_main.c.diff`'s removal of mcpp's setjmp can be
retried: the transpile arms no jump buffer and calls `tls.Longjmp` at mcpp's six
cfatal sites, so any preprocessor fatal error -- a line over NBUFF's 65,536 bytes
was one, until the emitter wrapped its lines -- is libc's "unsupported
setjmp/longjmp usage" panic instead of mcpp's message, under either libc.
v1.77.1's model (a Longjmp may target any armed buffer, not only the last, and
panics with a LongjmpRetval naming it) is what makes keeping the setjmp worth
trying.
