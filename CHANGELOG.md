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

### Behaviour changes

- **The address of a local array's element no longer escapes its frame.**
  `return &a[i]`, storing one in a package variable, and handing one to a goroutine
  were all accepted, leaving a reference to storage that is gone — the shape the
  lifetime rules exist to stop. The question "is this a variable of this frame" asked
  only one of the two environments a local can be in, and a local array is in the
  other. All three sinks refuse it now, and the address of *package* storage is
  handed out as freely as before.

### Bug fixes

- **A string constant could not be indexed or sliced.** `digits[9]` and `lit[1:3]`
  emitted C naming something no declaration had ever produced: a string constant is
  folded to its literal at every use — a Go constant has no address, so there is
  nothing to point at — while both paths read `.str` and `.len` off the name as
  though a variable stood there. A constant string is the natural place to keep a
  digit table or a prompt, and `len()` and `range` over one always worked, which is
  why nothing had noticed.
- **`for ; cond; post` was broken twice over.** A three-clause loop with an empty
  init clause was read as a conditionless one — a loop that never ends — so
  everything after it was reported as unreachable code; and once that no longer
  stopped the build, the emitter dropped the post clause and the loop ran forever.
  Such a header carries both semicolons and the post as its own children rather
  than in the tail node, which neither walk read.
- **A function value in a struct field could not be called through an index.**
  `table[i].run(arg)` — a dispatch table, which is what a command loop is built
  from — was refused with "unsupported call in expression". The chain walk took any
  selector-then-call for a method and gave up when the type had no method of that
  name, instead of falling through to the field, which the unindexed `x.run(arg)`
  had always done.
- **The Builder's method set was not checked, and `var b Builder` did not work.**
  A misspelled method (`b.WriteInt(5)`) reached the C compiler as a call to a
  function nothing declares, while a Builder held in a variable of written type had
  *every* method rejected — the compiler knows this one type's methods rather than
  reading them from a declaration, and neither path knew that. A zero Builder also
  emitted `= 0`, which is not C's zero for a struct, and named a type whose
  definition was only pulled in by a `NewBuilder` call.
- **A partly-hoisted argument list left a stray temporary.** When a call's
  arguments are evaluated into temporaries to pin Go's left-to-right order, an
  argument whose type the emitter cannot infer abandons the attempt — and the
  temporaries already emitted stayed, at best as a variable nothing reads and at
  worst as a second evaluation of an argument that changes something.
- **A conversion could not appear in a constant expression.** `const one = int32(1)
  << 16` — the ordinary way to write a fixed-point scale — was rejected with "int32
  is not a constant", package-level or local, for every target type. Such a
  conversion is now folded like any other constant expression and is usable
  wherever a constant is, an array bound included. The value must be representable
  in the target (`int8(200)` overflows) and a float converted to an integer type
  must be whole (`int32(2.5)` is refused), both worded as Go words them.
- **`len` and `cap` of a struct's array field were refused.** Both resolved an array
  only through a bare variable name, so `len(r.buf)` fell through to the
  string/slice header path and failed with "len is only supported for strings,
  arrays and slices yet" — about an array. A plain field, one reached through a
  pointer receiver, a nested one and a multi-dimensional one all work now, and the
  bound stays a compile-time constant, so nothing is read to produce it.
- **A struct field's array bound could not name a constant.** `buf [maxFrame]uint8`
  inside a struct did not compile, failing as `unsupported type ""`: struct typedefs
  are emitted before the constants they might mention, so the bound was out of
  reach. A local or package-level array of the same shape always worked, which is
  why nothing noticed — no test had put one in a struct. Constant *values* are now
  collected ahead of the typedefs; their types still are not, and an array bound has
  no use for one.
- **Arithmetic on `int8`, `uint8`, `int16` and `uint16` did not stay in the type.**
  Go computes in the operands' own type; C promotes anything narrower than `int` to
  `int` and keeps the extra bits, so with `var a uint8 = 200`, `a * 3` printed 600
  where Go says 88, and `-a` printed 4294967096 where Go says 56. Every operator
  that can carry a value out of a narrow type was affected — `+`, `-`, `*`, `<<`
  and unary `-` — on locals, array elements, struct fields, defined types and
  function results. Storing the result back into a narrow variable truncated it,
  which is why this only showed for a value that was used without being stored, and
  why the corpus missed it: it assigned before it printed.
- **`x = mask &^ x` gave 0 on the board.** Clearing bits in a variable from that
  same variable — the shape a driver writes — was miscompiled by the backend to a
  constant zero, whatever the operands were: it computes the left operand into the
  destination register and then reads that register back as the complemented
  operand, so what runs is `A &^ A`. A local, a parameter, a struct field and an
  array element indexed by a constant all reproduced it. Every host C compiler gets
  it right, so nothing on the host could have caught it; the fuzzer's oracle running
  on a real P2 did, on its tenth seed. Go's `^x` is now emitted as an explicit XOR
  with all ones rather than C's `~x`, which steps around it.
- **`^x` on a sized unsigned type had the wrong width.** `^uint8(200)` gave
  4294967095 instead of Go's 55, C's `~` having promoted the operand to `int` and
  kept it there. The all-ones constant carries the operand's own type now.

### Testing

- **The target's `printf` truncates `%.*s` at 62 characters**, so `print` of any
  longer string silently lost its tail — on the board only, the host being exact.
  A string is not null-terminated, so `%s` is not an option either; the bytes go
  out one at a time now, which is right at any length and costs nothing next to a
  serial line. Found by a console command loop whose output is 102 characters.
- **A field read off a struct-returning call needed a temporary and only got one
  after a reslice.** `len(b.String())` printed −251214335 on the board and 102 on
  the host: the backend reads a field at a nonzero offset off a return value as
  garbage, and the workaround was keyed on one specific helper rather than on
  there being a call at all.
- **A console command loop is a run case** — a dispatch table of name/handler
  pairs, a tokenizer over a fixed line buffer, an integer parser, and replies
  formatted into a caller-owned Builder. It is what found six of the entries
  above.
- **A fixed-point PID controller is a run case** — Q16.16 throughout, with a scaled
  multiply through a 64-bit intermediate, saturation on both rails, integral
  anti-windup and a derivative over a signed difference. It is what found the
  constant conversion above, on its first line. The tail pins the arithmetic the
  loop rests on: `mul` over negatives and the extremes, an int32 product that
  overflows coming back down, and that a signed shift of a negative value rounds
  toward minus infinity where a division by the same power of two does not.
- **A work-queue scheduler over the whole cog pool is a run case**, and a
  goroutine that starts goroutines is another. Seven workers over three rounds is
  twenty-one cogs started and retired, with the dispatcher multiplexing "hand out
  the next job" against "take a result back" — sending them all first deadlocks, a
  worker holding a finished result cannot take another job. Between them they cover
  the pool full rather than partly used, every slot recycled twice, a struct
  crossing a channel with a 64-bit field in it, and two cogs claiming pool slots at
  once, which nothing had done: every other case spawns from main alone. Both agree
  with real Go and run correctly on a P2, repeatedly. No bug behind them — the
  concurrency layer took the load as built.
- **A byte-oriented framing receiver is a run case** — SLIP-style escaping around a
  payload with a CRC-8 trailer, driven by a state machine — and it is what found the
  array bound above. It is the first thing a P2 program that talks to anything
  needs, and its output matches real Go byte for byte.
- **The fuzzer's programs are compiled for the target and run on the board.** The
  oracle checked a running checksum the generator computed as it emitted the code,
  but only ever against the host C compiler — so it said nothing about the machine
  the language is for, and that is exactly where it found the `&^` miscompile above
  on its first run. A generated program prints that it finished, since a board has
  no exit status and a program that ran to the end is otherwise as silent as one
  that hung.
- **The fuzzer generates booleans, and with them `&&`, `||` and `!`.** Every
  generated condition used to be a single comparison, so short-circuit evaluation —
  where the operand on the right is not evaluated at all — was never fuzzed. Bool
  variables are declared, combined, negated and used as conditions of their own, and
  an unused one folds into the checksum through what it selects. Generating a
  short-circuit is safe because nothing inside a generated expression has an effect:
  a generated function is a pure expression over its parameters.
- **The fuzzer generates `switch` statements.** The oracle only checks that whatever
  was generated matches the VM's prediction, so a construct it never emits is a
  construct it never tests — and `switch` was one, despite being in the language from
  the start. Generated switches carry the clause shapes that make the statement more
  than an `if`: a case listing several values, an empty clause, and a `default` that
  is sometimes taken and sometimes skipped. Only one clause has a body, so the VM's
  prediction stays exact; the others exist to be stepped over. 800 seeds agree.
- **The clause shapes a `switch` is built from are a run case**, including a
  `default` written before a later `case` — Go allows it anywhere, so the if/else
  lowering cannot assume it is last. Output matches real Go.
- **Recursion has a test, and it runs on the board.** Nothing exercised it before,
  which matters most on the target: `main` runs on the cog's own stack and a
  goroutine on its pool slot's fixed 256 longs, so a recursive call chain is bounded
  by something the program cannot see. A quicksort and `fib(15)` stay well inside it.
  A bit-banged SPI driver joins them, for the pin intrinsics driving a protocol.
- **Two more realistic programs are run cases**: a CRC-16 over a byte slice with a
  256-entry table and Q8.8 fixed-point arithmetic, and formatting into a
  caller-owned buffer through a `*Builder` parameter. Both are the kind of thing the
  target is for, and the second is what found the missing pointer check above.
- **The `p2` package's locks and clocks are now exercised**, on the host and on real
  cogs: a hardware lock held while three cogs contend for one counter, and the
  millisecond clock a driver paces itself by. Neither had a test of any kind.
- **The host shim's waits and clocks are real.** `_waitms` and `_waitus` returned at
  once, and `_getms`/`_getsec`/`_cnt` read *CPU* time — which a waiting program does
  not consume. So "wait two milliseconds, then check the clock moved" was true on the
  board and false on the host: the shim disagreed with the hardware about the one
  thing a driver does most. They wait, and read a monotonic clock, now.

### Diagnostics

- **Pointer-ness is checked** wherever a value is assigned to something of a known
  type: an argument, a variable declaration, a return and an assignment. `f(p)` where
  `f` wants a `*P`, `var q *P = p`, `return p` from a `*P` result and `q = p` were all
  accepted and left to the C compiler — so the most common mistake in the language
  came back as a complaint about C the program never wrote. Reported now as Go
  reports it, naming what was passed and what was wanted.

  An assignment whose target carries a selector, an index or a leading `*` is left to
  the field, index and deref checks: the name in hand there is the target's *base*,
  and what is assigned belongs to the field, element or pointee.


- **Three messages now read as Go's do.** `break is not in a loop, switch or select`
  gains Go's comma; `undefined label nope` becomes `break label not defined: nope`
  (or `continue label not defined:`, as Go words each); and an assignment mismatch
  whose values come from a single call names it — `2 variables but f returns 1 value`
  — which is what says where the count came from.

  A message that says the same thing as Go's in different words is worse than it
  looks: what a reader knows from Go stops carrying over, and a search for the text
  finds nothing.

### Tooling

- **`ogo fmt` indented a comment one level too deep** when it stood before a `case`
  clause — and, once grouped declarations began indenting, before a `const ( … )`
  keyword. A separator's indent did not take the token's indent delta, so a comment
  went with the body rather than with the token it precedes.
- **`ogo fmt` outdented every spec of a grouped declaration.** A `const ( … )`,
  `var ( … )`, `type ( … )` or `import ( … )` written the gofmt way came back with
  its specs at the keyword's own level — the tool meant to produce that shape was
  the one destroying it. There was no indent rule for a grouped declaration at all.
  It went unseen because no `.ogo` source in the repo outside `testdata` has one, and
  the formatting check excludes `testdata`.

### Language

- **A method's receiver now carries its type.** It was declared with none at all, so
  every check that keys on one was skipped for it — and the escape rule, which asks
  whether the root of `&p.nodes[i]` is an inline value whose address dies with the
  frame, read a *pointer* receiver as one. That refused `return &p.nodes[i]`, which
  is how a fixed node pool hands out a node, and with it the whole no-heap way of
  building a linked structure.

  The emitter's half of the rule learned the same distinction: through a pointer,
  `&p.f` and `&p[i]` reach what the pointer points at, which is the caller's storage.
  A bare `&p` still takes this frame's, and a value receiver or local is refused as
  before.
- **A string held in a struct field may be sliced**, `l.line[a:b]`. The slice paths
  had an answer for a slice field and an array field and none for a string one, so
  the whole of a tokenizer was `cannot infer a type`.
- **A top-level name that C has already spoken for moves out of its way.** A program
  is entitled to a function called `atoi` or `abs`, a variable called `index` or a
  type called `union` — every one of which is declared by a header the emitted C
  includes, or is a C keyword — and each used to be a C compile error naming a
  collision the program never made. Only the colliding names move, so everything
  else in the output reads as it did.

  A local, a parameter or a struct field needs no such help against a *library*
  name, shadowing being enough. One against a C *keyword* still emits invalid C; see
  the TODO list in `specs.go`.
- **A method or an imported package's function may yield several values into a
  destructuring assignment**, `b, ok := r.pop()` and `q, r := mathy.Divmod(17, 5)`.
  Only a plain function of the same package could before, so the `(value, ok)` shape
  a container wants — a ring buffer's `pop`, a lookup — had to be written as a
  function taking the receiver by hand.
- **A `const` declaration may bind a list**, `const a, b = 1, 2`, as Go's does. A
  spec that omits its expression list repeats the previous spec's positionally, and
  `iota` counts specs rather than names — so every name on one line sees the same
  value, which is what makes `h, i = iota, iota * 10` mean what it says.

  The two arity diagnostics now read as Go's do: `missing init expr for b` and
  `extra init expr 2`. The first replaces `missing constant value for b`, which said
  the same thing in different words and reached the same condition from the other
  side.
- **A float literal has a hexadecimal form**, `0x1p-2`, `0x1.8p1`, `0X2p+3`, whose
  exponent is a power of two and is required, as in Go. It is how an exact value is
  written on a target whose `float64` is 32 bits wide.
- **A defined type over a channel is a channel**, `type Ch chan int`: a send, a
  receive and a `select` clause all reach it, through a chain of definitions if there
  is one. It was the one kind left out when a defined type gained the behaviour of
  what it is defined over — the element lookup keyed on a written `chan T` and found
  a name instead, so every send on one was `cannot send to non-channel`.

  Such a type gives up one thing: a method of its own, refused where it is written.
  It is answered for by the channel cell's own name in the emitted C, so it has no C
  type there to hang a method namespace on.

## v0.12.0

### Language

- **A declared function may be used as a value.** A variable, parameter, result,
  array element or struct field of a function type holds one, and a call through any
  of them is a call:

  ```go
  func add(a int, b int) int { return a + b }

  func run(f func(int, int) int, a int, b int) int { return f(a, b) }

  var chosen func(int, int) int // the zero value is nil

  func main() {
      chosen = add
      println(chosen != nil, chosen(1, 2), run(add, 3, 4))
  }
  ```

  It lowers to a C function pointer: nothing is allocated and nothing costs anything
  at run time, the function being already there and only its address travelling.
  That is also why one is always safe to send to another cog — it names code, not the
  frame it was made in. A function of another type is not assignable, and the check
  is by signature, so it holds however the two were written; parameter names are no
  part of a function type.

  Four forms are refused, each named where it is written: a function literal, a
  method value (`t.get`), a function with more than one result used as a value, and
  `go` through a variable holding a function. All four are wanted; see the TODO list
  at the top of `specs.go`.
- **A `select` may carry a send clause**, `case ch <- v:`, so one statement can
  multiplex receiving and sending — the shape a worker loop wants. The clause offers
  its value and waits for a receiver to take it, so its body runs because the value
  was *delivered*, not merely deposited; the offer stands between polling rounds and
  is taken back whenever another clause is ready to proceed.

  Two shapes are refused rather than approximated, both because the rendezvous has
  no scheduler behind it. **At most one send clause**: two offers cannot stand at
  once, since a receiver taking each would send twice where Go sends once. **No send
  alongside a `default`**: that asks whether a receiver is ready at this instant, and
  a receiver here reveals itself only by taking a value, so there is nothing to ask.
- **A goroutine may be launched on an imported package's function**, `go
  driver.Poll(ch)`. It resolves to the same mangled name an ordinary call into that
  package does, and the rule that refuses handing a reference to a local across to
  another cog applies to its arguments unchanged. With this and `go w.run(ch)`, every
  callee an ordinary call accepts may be launched.
- **A slice literal may stand as a value**, not only as a variable's initializer:
  passed to a function, measured by `len` and `cap`, assigned, and nested inside
  another literal's element. It is bound to a local declared before the statement,
  which is where its backing array comes from — the same two declarations `s :=
  []int{...}` has always emitted.

  Its lifetime is that local's, so the four rules that govern a reference leaving
  its frame apply to it by name: returning one, storing one in a package variable,
  handing one to another cog and sending one on a channel are each refused, since
  the backing array belongs to the function that wrote the literal.

  An *array* literal is unchanged. An array is not a C value — binding one would
  only move `assignment to expression with array type` into the emitted C — so it
  stays a variable's initializer, a slot in another literal, and a `range` operand.
- **A float literal may carry an exponent**, `1e3`, `1.5e-3`, `2E+10`. The scanner did
  not recognize the form at all, so it was a syntax error — and one syntax error made
  every name in the file read as undefined afterwards, which is how it first looked
  like something else. The forms with an empty side, `1.` and `.5`, come with it.
  Go's hexadecimal form, `0x1p-2`, is still not recognized.
Each of these changes what a program that already compiled does, or reports
something the last release let through to the C backend. Every one is the compiler
agreeing with Go where it had not.

- **Division of two integer constants produced a float.** `7 / 2` was 3.5 rather than
  3, as Go has it, so a perfectly ordinary `[MB / KB]int` was rejected with
  `invalid array bound` and any constant reached through a division carried the wrong
  type. Float constant division is unaffected: `7.0 / 2` is still 3.5.
- **A shift by a count at or past the operand's width gave C's answer, not Go's.**
  `x << 40` on an `int32` was `x << 8` — both compilers take the count modulo the
  width, where Go defines the result as 0 (or −1 for a right shift of a negative
  value). Silent wrong answers. A count that is not a constant already inside the
  width now goes through a guarded helper; one that is stays a plain C shift, so
  ordinary code costs nothing. A negative count is a run-time panic, as in Go.

  The compound form `x <<= n` is guarded the same way, on a plain variable and on an
  element whose index can be named twice. An element whose index is itself a call is
  refused rather than emitted wrong — the guarded form names its target twice.
- **A constant too wide for a 32-bit int was computed wrong.** `var x int64 = 1 << 40`
  gave **0**, and `2000000000 * 3` gave 1705032704. Go computes a constant expression
  in arbitrary precision and then converts; the emitter wrote the expression out as C
  source, where it is a shift or a multiply of an `int`. A constant whose value does
  not fit an int is now emitted as that value. `const big = 1 << 40` takes the width
  it needs too, instead of `static const int`.

  A negative one is written as its bit pattern rather than as a negation, because the
  target's C compiler folds no unary minus in a global initializer — and because the
  most negative value has no negation that is a literal in any C.
- **A value of a named type printed as a meaningless number.** `type Name string`
  printed `1428869128` — the first word of the string header read as `%d` — and
  `type Flag bool` printed `1` rather than `true`. Every decision about how a value
  is *represented* read the typedef's name instead of the type it stands for. This
  was the worst kind of bug: a silent wrong answer in a program that ran.

  Fixing it made the rest of a named type work as well. A value of `type Name string`
  now carries a length, indexes to a byte, slices, ranges over its runes, compares
  and switches as a string does; a value of `type List []int` indexes, ranges, and
  answers `len`/`cap` as a slice does. What stays keyed on the name is identity —
  which methods the type has, and what its C declaration is called — which is the
  distinction the language draws.
- **Deferred calls ran before a return's expressions were evaluated.** Go evaluates
  the expressions, assigns them to the results, and only then runs the defers — so a
  `return n + 1` alongside a deferred call that multiplies `n` by 10 gave 11 where Go
  gives 20. Binding the results first also gives a *named* result its point: a defer
  may still change it, and that change is what the caller receives.
- **A shadowing declaration outlived its scope.** The emitter keeps a variable's
  type, extents and provenance in maps keyed by source name and had no scopes of its
  own, so after `{ s := 5 }` shadowing a package-level string, `s` was still recorded
  as an int — and the next read of the real `s` printed the first word of its header
  as a number. The same held for a name declared in an `if`, `for`, `switch`,
  `range` or `select` header. Every one of those now ends where it should.
- **A defined type is now type-checked.** A variable of one — `type Celsius int` —
  carried no type category at all, so *every* check keyed on one was skipped for it:
  `var c Celsius = "a"`, `c = "a"`, `f("a")` for a `Celsius` parameter and
  `var s Small = 300` for a `type Small uint8` all went unreported and were left to
  the C backend. The type is now followed to the one it is defined over, through a
  chain of definitions if need be, and reads as itself in the message rather than as
  its underlying type.

  Known gap, unchanged: a defined type over a *channel* is not usable as a channel —
  a send or receive on one is refused. A defined type over a struct, slice, array or
  function is unaffected, having no type category to gain.
- **`var n int = "a"` was not reported.** A variable declaration's initializer was
  checked for nothing but constant overflow — an assignment was checked and a call
  argument was checked, so `n = "a"` and `f("a")` were caught while the declaration
  form was left to the C backend, which said `incompatible types when initializing`.
  Both the local and the package-level form now report it, in the same words the
  assignment does.

  Two container gaps in the same family closed with it: a **slice** variable never
  recorded its element type at all, so `xs[0] = "oops"` was unchecked however `xs`
  was declared; and a short declaration from an **array or slice literal**,
  `a := [2]int{1, 2}`, recorded none either. Both do now.

  A variable of a *named* type still carries no type category, so neither
  `var c Celsius = "a"` nor `c = "a"` is reported yet — that is the checker's
  predeclared-only type model, and is separate work.
- **`p := P{1, 2}` was not type-checked.** A short declaration recorded no named
  type, so every check that keys on one — the type of a field assignment, an unknown
  field, an unknown method — was silently skipped for the ordinary way of writing it,
  while `var p P = P{1, 2}` was checked. The errors were not lost, only misplaced:
  they surfaced from the C backend instead, as `incompatible types when assigning`
  or an implicit declaration of a method that does not exist. The type is now carried
  over from a composite literal, the address of one, a copy of a variable that has
  one, and a call whose single result has one.
### Fixed

- **The most negative value divided by −1 crashed.** Go defines it to be itself, with
  a remainder of 0 — the quotient is not representable, so the two's-complement
  overflow stands. C leaves it undefined, and the host traps on it with SIGFPE, which
  is a crash where Go prints a number. Signed `/` and `%` now go through a guard,
  alongside the divide-by-zero check the divisor already carried; unsigned division
  and a constant divisor that is neither 0 nor −1 are untouched.
- **A 64-bit `go` argument was silently truncated.** A goroutine's arguments travel
  through a per-site block whose fields took the type of each *argument expression*
  rather than of the parameter it is assigned to, so `go sender(1234567890123)`
  stored the literal as the `int` it defaults to and the cog received 1912276171.
  The block now holds each value as its parameter's type. `defer`, which marshals
  arguments too, was already correct.

  Two things found alongside it, both needed to make a `go` of a 64-bit struct work
  at all: a composite literal is built in a variable before it is stored into the
  block, the target's C compiler refusing one as an assignment's right-hand side;
  and a negated wide constant is folded in that literal too, which needed the fold to
  run over the whole unary expression rather than its operands.
- **A conversion to a 64-bit type of a 64-bit expression was miscompiled** by the
  target's C compiler, yielding a value that varied from run to run — `int64(a - b)`
  on `uint64` operands. The same cast applied to a *variable* is correct, so the
  operand is now bound to one first. Only the board shows this; the host compiler
  computes either form correctly.
- **A `for` loop's condition was compiled against the wrong `s`** when its own
  variable shadowed one of a different type: the condition was rendered before the
  loop variable's type was recorded, so `for s := 0; s < 2; s++` inside the scope of
  a string `s` compared two ints as strings.
- **`go` could panic `out of cogs` while a cog was in fact free.** A goroutine marks
  its slot free in its epilogue, after the body ends — but the code that started it
  can learn the body is over *before* that: a receive of the value the goroutine sent
  last returns first. `go` then found every slot busy and gave up on the spot. It now
  waits for a slot before declaring exhaustion, which is sound because a slot held by
  a goroutine that really is running stays held, so the wait only delays a diagnosis
  the program was going to get anyway. Measured on the host shim: a program that
  starts seven goroutines, drains them and repeats failed 6 times in 400 runs before,
  and 0 in 400 after.
- **Several `init` functions in one package did not compile.** A package may declare
  as many as it likes and Go runs each in turn, but two of them emitted two C
  functions both named `init`, which the backend rejected as a redefinition. Each now
  takes its own name and they run in the order they are written, each on the state
  the ones before it left. A `func init` with a parameter emitted broken C, and one
  with a result was accepted silently; both are now refused with Go's message,
  `func init must have no arguments and no return values`.
- **A `select` over more than one channel did not compile.** Multiplexing several
  channels is the whole point of the statement, and the emitted C put each clause's
  value declaration between the previous clause's closing brace and this clause's
  `else` — which is not C, so the backend rejected it outright. The declarations now
  stand ahead of the chain. Every `select` test had used a single clause, which is
  how a shipped feature's headline use went uncompiled.
- **A `string`, slice or struct named result, or defer argument, emitted invalid C.**
  Both were zeroed with `= 0`, which C accepts for a scalar and rejects for an
  aggregate, so `func f() (s string)` and `defer g("x")` did not compile at all.
- **A conversion between two types of the same representation now works.**
  `bool(f)` emitted a call to a function named `bool` — invalid C — and
  `string(n)` where `n` is a defined type over string was refused as needing an
  allocation it does not need. Such a conversion builds nothing: it is the operand
  itself, and now emits no C cast at all, which also removes a cast to a non-scalar
  type that C does not have and only gcc was accepting. A scalar conversion keeps
  its cast, so a narrowing one still truncates as Go says.

  The conversions that would have to *build* a string, `string(r)` from a rune and
  `string(b)` from a byte slice, are still refused — the checker reports the first
  (it now types a conversion, so `string(rune(r))` is judged by what it converts
  from) and the emitter the second, where the representation is known.
- **A digit separator in a float literal did not compile.** `1_0.5` reached the C
  backend as written, where it is not a float at all but an integer with an invalid
  suffix. The integer forms had been normalized all along; the float one had not.
- **`--unchecked` left a negative shift count undefined.** The guarded shift kept its
  Go panic in a checked build, but an unchecked one has no panic to raise and fell
  through to a C shift by a negative count. It now compares the count unsigned, so a
  negative one yields what an enormous one does — 0 — rather than anything undefined.
- **A panic printed nothing when its output was not a terminal.** `abort()` discards
  a buffered stream, so through a pipe the panic line — and everything the program
  had printed before it — was lost, leaving a bare `signal: aborted`. `ogo_panic`
  now flushes first. The test suite's panicking cases assert the message from now on,
  which is what would have caught this.
### Tooling

- **`ogo fmt` dropped the space after a comma before a nested composite literal**,
  writing `{{1, 2},{3, 4}}` where gofmt writes `{{1, 2}, {3, 4}}`. The rule that
  binds a literal's braces to what they enclose fired on the brace after the comma
  too.
- **`ogo fmt` indented a label** with the statements it labels. gofmt stands it one
  level out, as `case` already did here — including a label that opens a `case`
  clause's body.

  Two gofmt behaviours remain unimplemented and are noted rather than fixed: binary
  operators inside a call argument are rendered tight by gofmt (`println(a+b)`), and
  consecutive one-line declarations have their bodies aligned.
### Testing

- **Every runtime trap now has a run case.** Divide by zero, remainder by zero and
  appending past a slice's capacity were emitted by the compiler and exercised by
  nothing, so the messages and the conditions that raise them were unverified. Divide
  by zero is covered through each of its four lowerings — signed division and
  remainder, which carry the check inside their guarded helper, and the unsigned and
  64-bit forms, whose divisor goes through a separate one.
- **Channels are now tested under contention**, on the host and on real cogs: two
  consumers drawing from one producer through a shared channel, a `select` over two
  channels fed by two more cogs at once, and a `select` whose send and receive
  clauses have to make progress against another cog doing the mirror image. Every
  other channel case has one sender and one receiver, so nothing pinned what happens
  when several cogs reach the same rendezvous together — which on this target is a
  hardware lock and a spin, with no scheduler to arbitrate. All of it passes.
- **A multi-package program is now compiled for the target and run on hardware.** It
  was exercised only by the host C compiler, so the one program shape whose lowering
  is about nothing but names crossing a package boundary — every top-level symbol
  mangled into its package's namespace, the whole program emitted as one translation
  unit — had never been through flexcc or onto a board. The program, and what it
  prints, are now shared by all three.
- **`ogo fmt` is run over the whole run-case corpus** and its output re-formatted, so
  a rule that chokes on, or churns, a real program fails a test. That is how the two
  formatter bugs above were found.
- **The `--unchecked` and `--release` builds are now tested.** Every run test built
  with the checks on, so a whole configuration of `ogo build` was pinned by two
  emission goldens and nothing else — a lowering that is correct only because a check
  stands in front of it would not have been noticed. The run corpus is now executed
  unchecked as well, and compiled for the target under each flag. All of it passes as
  it stood; the coverage is the point.

## v0.11.0

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

- **A method may be launched as a goroutine**, `go w.run(ch)`. Only a plain function
  could be before, so a worker with a method had to be wrapped in one. The receiver
  is evaluated and copied where the `go` statement stands, as Go evaluates it, so a
  write to it afterwards is not what the goroutine sees.

  The lifetime rule reaches the receiver like any other value crossing to a cog: a
  *pointer* receiver hands out the address of the receiver itself, so a local one is
  refused exactly as `go f(&x)` is, while a value receiver is a copy and crosses
  nothing unless the value itself holds a reference to the frame.
- **A composite literal may be ranged over**, `for _, v := range []int{1, 2, 3}`,
  which is how the idiom is written in Go. A literal is kept out of an `if`, `for`
  or `switch` header because its `{` would be the block's — but only when its type
  is a bare name: a `[` cannot begin a block, so the bracketed form has no such
  trouble and is now allowed there. The operand is bound to a local first, which is
  where a slice literal's backing array comes from.
- **A composite literal's type may be qualified**, `geo.Point{1, 2}`, so a value of
  an imported package's struct type can be written directly instead of only through
  a constructor that package supplies. Positional, keyed and empty forms all work,
  including nested and at package scope, where the value is laid out statically. The
  export rules apply inside: the type must be exported, a key must name an exported
  field, and a positional literal is refused for a struct with an unexported field,
  which it would be assigning to.
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
- **A slice bound that is a constant out of range is now refused.** `a[1:9]` over a
  `[4]int` compiled and trapped when it ran; it is wrong however the program runs, and
  Go rejects it. The four shapes it covers — a bound past the extent, a negative one,
  and a pair out of order in either direction — are reported in Go's words at Go's
  column. Only an array operand qualifies: a slice's length and capacity are run-time
  values, so a bound against one can still only be checked as the program runs, which
  it is.
- **A `:=` whose left side is all blanks is now refused.** `_ := f()` introduces no
  variable, which every short declaration must do at least one of, and Go says so:
  it is `_ = f()` written wrongly. The rule reaches every form — a statement of its
  own, an `if`, `switch` or `for` init, a range variable, and a select's `case _ :=
  <-ch` — all eight of which compiled before. A blank alongside a name that is new,
  `a, _ := f()`, is unaffected, as are the forms that write no `:=` at all.
- **A variable declared in a statement's header must be used.** The rule every
  short declaration follows did not reach the ones that declare inside a header, so
  `if v := f(); true {}`, `switch v := f(); {}`, `for i := 0; ; {}`, `for i := range
  3 {}` and `case v := <-ch:` all compiled with the name unused. Go reports each of
  them, in the same words and at the same column this now does, and the emitted C
  drew an unused-variable warning besides.

  The one form left out is OctoGo's own `switch v := f()` with no init statement,
  where the name declared is what the switch switches on: its declaration is its use.
- **A package array's element may no longer be given a reference to a local.**
  `table[1] = local[:]`, where `table` is a package-level array, left it pointing at
  storage the function was free to reuse — the rule that refuses this for every other
  package variable was asking one environment for the target and arrays live in
  another, so every array target went unchecked. Declare the buffer at package scope
  and slice that, which is what the diagnostic asks for.

### Fixed

- **A receiver or parameter the body never uses no longer warns.** `func (b box)
  tag() int { return 7 }` and `func pick(a int, b int) int { return a }` are both
  legal Go — an unused parameter is not an unused variable — and both emitted C the
  host compiler reports as `unused parameter`, which the run tests fail on. The
  emitter already wrote a `(void)` for a receiver the source left unnamed and for a
  parameter with no name; a named one that is simply ignored is the same situation.
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
