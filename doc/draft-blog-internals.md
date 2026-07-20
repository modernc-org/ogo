# Building OctoGo: A Go-like Language Where Goroutines Are Cores

*DRAFT — not published. Revisit and re-verify every number before publishing;
the compiler moves fast. See the checklist at the end.*

The last few posts here have been about pieces of a toolchain without saying
what they were for. [egg][egg-post], the LL(1) parser generator. The
[flat AST][flat-ast-post] and the ergonomics of walking one when you have
decided that allocation is the enemy. And underneath both, ccgo, which turns C
into Go.

This post is what they were for.

OctoGo is a Go-like language for the Parallax Propeller 2. egg generates its
parser. Its AST is a flat `[]int32`. Its backend is a C compiler that ccgo
transpiled into the binary you install. And the thing that makes it worth
writing about is not any of that plumbing — it is that on this target, a
goroutine is not a scheduler entry. It is a core.

## The target, briefly

Most readers will not have met a Propeller 2, so: it is an eight-core
microcontroller with no interrupts in the usual sense, no MMU, and no operating
system. The cores are called cogs. There are eight of them, they are symmetric,
and they run genuinely in parallel. There is 512 KB of shared hub RAM, each cog
has 512 longs of private memory, and the silicon provides 16 hardware locks.

That last sentence is the whole design brief. Eight cores and sixteen locks are
not an implementation detail to abstract over — they are the resource budget,
and a language for this chip should make them visible rather than pretend they
are infinite.

So OctoGo has no garbage collector, no heap, and no scheduler. What it has is a
mapping:

- `go f()` starts a real cog.
- A `chan` is a rendezvous cell guarded by a real hardware lock.
- Running out of either is a runtime panic, not a queue.

Everything below follows from that.

## LL(1) as a design force

egg accepts LL(1) grammars and rejects everything else, with detailed
diagnostics. It was not softened for OctoGo. The grammar lives in a doc
comment in `specs.go`, gets extracted to EBNF, and egg turns it into the parser.
If a language feature is not LL(1), it does not go in.

Most language designers would call that a bad trade. You give up real
expressiveness to keep the parser simple. What it actually costs is worth
spelling out, because the answer is more interesting than "you lose some syntax."

Sometimes it costs nothing and you just have to be clever. Consider parsing a
`for` header. After `for`, an identifier could begin a condition (`for x {`) or
an init statement (`for i := 0; ...`). One token of lookahead cannot tell them
apart. The fix is to stop trying: parse a leading `Expression`, then let the
*next* token decide what you were parsing. A `{` means it was a condition; a `;`
or an assignment operator means it was an init. The same trick handles switch
guards. LL(1) holds, and the language does not lose anything — the only cost is
that the condition is no longer a direct child of the for-statement, so the
checker has to go find it.

And sometimes it looks like it costs something real, right up until it doesn't.

Composite literals are Go's classic parsing ambiguity. After a type name, a `{`
might open a literal or might open a block:

```go
p := Point{1, 2}   // literal
if x {             // block
```

One token of lookahead sees the same thing in both. Go's own parser resolves it
with a parser-level flag tracking whether a composite literal is currently
permitted — precisely the context-sensitivity an LL(1) grammar cannot express.
For a long time OctoGo simply had no composite literals, and I assumed that was
the price of the constraint.

It isn't, and the way out is worth the detour. Go's actual *rule* — as opposed to
its implementation — is that a composite literal may not appear at the top level
of an `if`, `for` or `switch` header unless you parenthesize it. That rule is
perfectly context-free. It just has to be said in the grammar rather than in the
parser. So there are two expression grammars:

```ebnf
Factor           = identifier [ CompositeLit | FactorSuffix ] | ...
CompositeLit     = "{" [ ExpressionList ] "}" .

HeaderExpression = HeaderSimpleExpr { RelOp HeaderSimpleExpr } .
HeaderFactor     = identifier [ FactorSuffix ]      // no CompositeLit
                 | "(" Expression ")"               // ... but parentheses restore it
                 | ...
```

The ordinary chain allows composite literals. The header chain is the same
grammar with that one production removed, and it is what `if`, `for` and `switch`
headers parse. Inside parentheses you are back to the full expression, which is
where Go's escape hatch comes from for free: `if p == (P{}) {` works, for exactly
the reason it works in Go. egg accepted the whole thing without a single new
conflict.

The obvious objection is that duplicating five productions means duplicating
every consumer of them, and the checker and emitter switch on node kinds all over
the place. That turned out to be a non-problem, because the two chains are
*structurally identical* — the header one is a strict subset. So the AST iterator
maps the header productions onto the ordinary ones as it yields them:

```go
func baseSym(s Symbol) Symbol {
	switch s {
	case HeaderExpression:
		return Expression
	case HeaderFactor:
		return Factor
	// ...
	default:
		return s
	}
}
```

Nothing outside the parser knows the distinction exists. The entire checker and
the entire emitter were untouched by this change; the whole test suite went from
red to green the moment that function was added. The duplication buys LL(1) and
then disappears.

That is the shape of the constraint in practice. It does not usually forbid
things. It forbids *saying* them in a particular way, and often the rewrite is
clearer about what the language actually means than the original would have been
— Go's composite-literal rule is real, and here it is written down instead of
being an emergent property of a parser flag.

The payoff for all this discipline is that the frontend is boring in the way you
want a frontend to be boring. The grammar is machine-checked. The parser is
generated. When the language changes, the doc comment is edited and the parser
regenerated, and egg reports immediately if the new grammar is ambiguous.

## Concurrency compiled to silicon

Here is a complete OctoGo program:

```go
func worker(ch chan int, n int) {
	ch <- n * 10
}

func main() {
	var ch chan int
	go worker(ch, 1)
	println(<-ch)
}
```

It compiles to a 16 KB P2 binary. When it runs, `worker` is executing on a
different physical core than `main`, and the two meet through a hardware lock.

Two details in that program are not Go, and both are forced by the target.

**There is no `make(chan int)`.** The checker rejects it. There is no allocator
to make a channel with, so the *declaration* is what creates it: `var ch chan
int` allocates the rendezvous cell and acquires its hardware lock, and the
lock's lifetime is tied to the variable's. This bothered me for about a day and
then stopped bothering me. On a chip with sixteen locks, a channel is a scarce
named resource, and declaring one should look like declaring one.

The cell itself is unglamorous:

```c
typedef struct { int lock; int full; int taken; int val; } ogo_chan_int;
```

`full` says a value is waiting. `taken` counts how many values have been
consumed — which is there for a specific reason. A sender needs to know that
*its own* handoff completed. If it watched `full` alone, then with several
senders sharing a channel it could see another sender's deposit and conclude its
own value had been received. Counting consumed values lets a sender snapshot
`taken` when it deposits and wait for that number to change.

**`go` cannot fail to be bounded.** The goroutine machinery is a statically
allocated pool with one slot per available cog:

```c
typedef struct { int used; long args[OGO_ARG_LONGS]; long stack[OGO_STACK_LONGS]; } ogo_cog_slot;
```

Each slot holds both the goroutine's stack and its marshalled arguments. That is
not tidiness — it is necessary. The launched cog reads its arguments *after* the
`go` statement has returned, so they cannot live in the launching function's
frame. Putting the stack and the args in the same pooled slot makes their
lifetime the goroutine's by construction.

Because the pool has exactly one slot per cog, "out of slots" and "out of cogs"
are the same condition, and the check is one comparison:

```c
if (slot < 0) { ogo_panic("out of cogs"); }
```

Note what this buys: `go` inside a loop is legal in OctoGo. It cannot run away,
because the silicon bounds it and exhaustion is reported. `defer` inside a loop,
by contrast, is a compile error — that one really is unbounded, since each
deferred call would need to be recorded somewhere with no upper limit.

## The whole toolchain is inside the binary

`go install modernc.org/ogo@latest` gives you a complete cross-toolchain for the
P2. No flexprop installation, no separate loader, no environment variables
pointing at an SDK.

That works because of ccgo. The backend is flexspin's C compiler, transpiled
from C to Go: 456,215 lines of it, about 12 MB of generated source, compiled
into the `ogo` binary as a library rather than shelled out to. The P2 loader,
loadp2, is transpiled the same way. And the P2 include tree — headers, libc
sources, `libc.a` — is packed into a 1.1 MB `go:embed`ed tarball that gets
unpacked at runtime.

The result is a 13 MB binary that contains a C compiler, a loader, and a P2
standard library, and the pipeline is:

```
.ogo → check → emit C → flexcc (in-process) → .binary → loadp2 → board
```

`ogo build` runs the first four stages; `ogo run` adds the last one. Both are a
single Go binary talking to a serial port.

This matters beyond convenience. Vendoring a toolchain this way means the
compiler under test is the compiler users get, pinned to a specific upstream
revision, with no "works on my machine, I have flexprop 7.4 installed." For a
project with one maintainer, that removes an entire category of bug report.

## The bug that only silicon could find

Now the part that actually matters.

OctoGo's test suite compiles every emitted program with the host C compiler and
runs it, checking the output. Concurrency is covered too, via a small shim that
backs cogs with pthreads and hardware locks with mutexes, at the real eight-cog
and sixteen-lock limits. It is a good suite. It catches real bugs.

Unnamed function parameters landed recently — Go's `func f(int, int)`, where a
parameter has no name because the body does not use it. The emitted C leaves the
parameter unnamed too. gcc accepts that in a definition, flexcc accepts it, and
the host suite went green across the board.

Then it ran on the board:

```go
func mix(_ int, b bool, c byte) int {
	if b {
		return int(c)
	}
	return 0
}

// mix(9, true, 65) should return 65.
```

It returned `1`.

Not garbage, not a crash — `1`. Which is a suspiciously meaningful number, and
if you stare at the call for a moment you can see where it came from. `true` is
1. The function was returning `int(c)` where `c` had somehow received the value
of `b`.

flexcc had compiled the definition with the unnamed leading parameter by
*dropping its argument slot entirely* and shifting everything after it. `b` got
argument 0 (`9`, nonzero, so the branch was taken), `c` got argument 1 (`true`,
which is 1), and the `65` fell off the end. The generated code was internally
consistent and completely wrong, and it produced no diagnostic from either
compiler.

gcc handles that same C correctly. So no amount of host testing — not more
cases, not more assertions, not a better shim — could ever have caught it. The
divergence was between two C compilers, and only one of them was going to run on
the hardware.

The fix is small: give every unnamed parameter a synthetic name so flexcc
allocates its slot like any other, and emit a `(void)` reference so the forced
name does not trip `-Wunused-parameter` on the host:

```c
int mix(int _ogo_unused0, _Bool b, uint8_t c) {
	(void)_ogo_unused0;
	...
}
```

The lesson is not the fix. It is this: **a cross-compiler that is verified only
on the host is not verified.** I had been treating the host suite as ground
truth and the board as a demo. It is the other way around. So OctoGo now has a
second suite — the same table of programs, but built with the real backend,
loaded onto a real P2, and checked against the serial output. Twenty-eight
programs, about thirty seconds, and it is the suite that actually means
something.

There is a coda. The first version of that harness killed the loader with
SIGKILL as soon as it matched the expected output. That works fine — until it
does not, and the board stops responding entirely, and no amount of retrying
brings it back without physically power-cycling the thing. An abruptly killed
loader leaves the serial port in a state the next load cannot recover from.

The fix was to stop killing it. loadp2 prints its own escape hatch when it
enters terminal mode: *"Press Ctrl-] to exit."* So the harness now writes
`0x1d` to the loader's stdin, and the loader shuts the port down properly and
exits 0. Five consecutive full runs, a hundred-odd loads, no wedging. SIGKILL
survives only as a last resort for a genuinely hung load.

I spent a while assuming the board was flaky hardware. It was my test harness.

## Where it is

OctoGo compiles and runs real programs on real Propeller 2 hardware today:
structs, methods with value and pointer receivers, slices and fixed arrays,
`defer`, the full control-flow set, `iota`, multiple return values, goroutines,
channels, and `select`. There are 119 semantic-check specs and 28 programs
verified on silicon on every change.

It is also unfinished. `ogo test` is a stub. The `p2` package wraps nine intrinsics
and needs to be a real standard library. Interfaces are designed but not built,
and the whole-program-optimization strategy behind them is still an open
question I would genuinely like opinions on.

If you have a P2 and any interest in writing Go-shaped code for it, I would like
to hear what breaks.

<!-- Links to fill in before publishing -->
[egg-post]: TODO
[flat-ast-post]: TODO

---

## Pre-publication checklist

Re-verify against the repo at publish time — all of these have moved during
development:

- [ ] flexcc line count and size (`wc -l internal/flexcc/ccgo_linux_amd64.go`)
- [ ] embedded include-tree size (`ls -lh internal/flexcc/p2include.tar.gz`)
- [ ] `ogo` binary size
- [ ] board suite case count (`grep -c 'name: "' internal/octogo/run_test.go`)
- [ ] spec testdata count (`ls internal/octogo/testdata/*.ogo | wc -l`)
- [ ] p2 intrinsic count
- [ ] the sample program's compiled binary size
- [ ] feature list in "Where it is" — add anything landed since
- [ ] limitations list — remove anything fixed since
- [ ] fill in the two blog backlinks
- [ ] confirm the repo is public and the install path works before linking it
