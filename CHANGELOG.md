# Changelog

Notable changes per release. The **Language** section is what changes for code you
write; the rest is the toolchain around it.

OctoGo is an early preview and its version numbers say so: releases are cut from
`master` roughly weekly, and each one may reject a program the last one accepted —
those are called out under **Behaviour changes**, and they are almost always the
compiler catching something it should have caught before.

Releases before v0.9.0 predate this file; see
[the releases page](https://github.com/modernc-org/ogo/releases).

A released section is frozen: it says what that release did, and a later fix to the
same area is a new entry under **Unreleased**, not an edit to the old one. Amending a
shipped section tells a reader on that version that they have behaviour they do not.
`git show vX.Y.Z:CHANGELOG.md` is the check.

## Unreleased

### Added

- **The fuzzer generates goroutines and channels.** A `go` statement starts a
  real core on this target, and nothing in the generated corpus had ever exercised
  the lowering behind it -- claiming a cog, marshalling the arguments into its
  slot, the rendezvous itself -- though every one of those has had a bug. They are
  generated now: a worker sends what it computes from the argument it was started
  with, main takes each value and folds it into the checksum. A channel is a
  synchronous rendezvous, so the values arrive in the order they were sent
  whatever the two cores do, which is what lets the oracle predict them at all. At
  most two workers per program, each started once at the top level of main: a cog
  is a physical core, and a spawn inside a loop asks for one per iteration.

- **The fuzzer generates calls through a function value.** A value is a C
  function pointer, and one of a function returning SEVERAL results points at a
  wrapper that writes them through a parameter -- a different lowering from the
  call by name, and the newest one in the compiler. Both forms are generated now,
  bound with a written type and called with generated arguments, so every seed
  exercises them. 300 programs on the board and 20,000 on the host agree with the
  oracle.

- **The fuzzer generates 64-bit arithmetic.** `ogo smith` exercised int8 through
  uint32 and stopped there, so int64 and uint64 -- where this compiler and the C
  backend have both been wrong most often -- were tested only by hand-written
  cases. They are generated now, with the values drawn full-width and the
  checksum taking BOTH halves of the result (`int(z)` is a 32-bit truncation on
  this target, so a value wrong only in its top half would have agreed with the
  oracle). It found two miscompiles in its first run on hardware, both fixed
  below.

### Fixed

- **`ogo fmt` printed nothing for a file that needed no change.** With no flags
  the formatted source goes to standard output, as gofmt does and as the
  command's own help says -- but only a CHANGED file was printed, so
  `ogo fmt x.ogo > y.ogo` wrote an empty y.ogo whenever x was already formatted.
  `-l` and `-w` are unchanged: they have nothing to list or rewrite there.

- **`- -v` decremented v, and `ogo fmt` turned it into a program that would not
  build.** Two unary signs in a row were written out as they stand, in both
  places: the emitter sent `- -v` to the C compiler as `--v` -- a pre-decrement,
  which changed the variable and yielded one less than Go's double negation, with
  `+ +w` incrementing w the same way -- and the formatter rewrote the source
  itself as `--v`, which this language has no prefix decrement to parse. A sign
  whose operand begins with the same sign now takes parentheses when emitted and
  keeps its space when formatted.

- **`ogo fmt` spaced a unary sign off the operator before it.** `v%-1`, `v*-2`,
  `f(a&^-5)`, `a[v*-1+7]` came back as `v% -1` and so on, which gofmt does not
  write: where gofmt has bound the operator to what precedes it, it binds the sign
  too. Both sides of a `-` before a `-` stay spaced, as gofmt writes them.

- **`x % 1` gave x instead of zero.** A remainder is smaller than its divisor, so
  a division by one leaves nothing over -- and the target's C compiler answered
  the dividend, for every integer type up to 32 bits, signed and unsigned
  (`doc/modulo-by-one-returns-the-dividend.c`; the 64-bit case goes through a
  runtime call and was right). `x % -1` the same. The operation is written as the
  multiplication by zero it is, which is the same value, evaluates its operand
  exactly once as Go does, and leaves nothing for a compiler to fold wrongly;
  `x %= 1` becomes `x *= 0` for the same reason. Found by the oracle fuzzer, on
  the board.

- **`x &^= K` for a wide constant left x unchanged.** The target's C compiler
  computes `~` wrong in the HIGH word of a 64-bit value -- for a constant and for
  a variable alike, `doc/complement64-high-word.c` has the measurements -- and
  that was the one complement the emitter still spelled with `~`; every other
  took the long form already. The complement of a constant is a constant, so it
  is folded now and no operator is emitted at all. A compound assignment also
  tells its operand what type it is being complemented in, which an untyped
  constant could not say for itself.

## v0.32.0

### Behaviour changes

- **A package with no test files is compiled anyway.** `ogo test` reported
  "ok ... [no test files]" of a package it never compiled, so `ogo test ./...`
  over a tree could report every package green while the program did not build --
  and a package with no tests yet is exactly where that hides. Such a package is
  checked now and its errors reported; a package that does not build fails the run.
  The C stage is still left to a build, which is what compiles it for real.

- **A test function with the wrong signature is refused, and `Testfoo` is not a
  test.** `func TestNoArg()` in a `_test.ogo` was quietly never a test -- `ogo test`
  built the package and ran everything else -- where `go test` stops with vet's
  `wrong signature for TestNoArg, must be: func TestNoArg(t *testing.T)`. It says
  the same now, positioned at the name, for a missing, extra, wrongly typed or
  by-value parameter and for a result. A function named `Testfoo` -- "Test" followed
  by a lowercase letter -- was run as a test where Go would not run it, and is not
  one now; `Test` alone is, as in Go. Found by trying the runner's edges: a package
  with no tests, a type error in a test file, two test files, and a test of the
  `main` package all behaved.

- **A constant operand a typed operand's type cannot hold is refused, and so is an
  integer division by a constant zero.** `x + 4294967296`, `x & 0x1FFFFFFFF`,
  `x == (1 << 32)` and `x + 1.5` for an int32 `x` all compiled; Go refuses each, and
  now so does this compiler, in Go's words: `constant 4294967296 overflows int32`,
  `constant 1.5 truncated to int32`. `x / 0`, `x % (1 - 1)` and `x / Zero` compiled
  too, and panicked at run time; they are `invalid operation: division by zero` where
  they are written. A binary operand was the one position a constant met a type in
  without the representability rule being asked -- a declaration, an assignment, an
  argument, a return, a send and a conversion all asked it. A float divided by a
  constant zero is still an infinity, as in Go.

### Language

- **A compound assignment can be a `for` statement's post statement.** `for i :=
  0; i < n; i += 2 {` did not parse -- "expected '{'" at the `+=` -- the grammar's
  ForPost admitting `=`, `:=`, `++` and `--` and nothing else, so stepping by two
  was written as `i = i + 2`. Every compound operator is admitted now, `i /= 2`,
  `i <<= 1` and `i &^= 4` included, and lowered exactly as the statement form is,
  guards included. The grammar in `specs.go` says so.

### Added

- **A method with several results may be used as a value.** `m := g.Next` for a
  `Next() (int32, bool)` was "a method with more than one result cannot be used as
  a value yet", and the declaration could not even be typed. It takes the same
  void wrapper a plain function value of several results takes, with the receiver
  bound into it, so it is still one word: held in a package variable, a struct
  field or a local, passed as an argument, and taken again on another cog.

- **An interface method may have several results.** `Next() (int32, bool)` was
  "an interface method with more than one result is not supported yet" -- the one
  shape a reader, a scanner or a queue is written in. The values travel in the
  struct a direct call to such a method already returns; the vtable slot writes it
  through a trailing pointer rather than returning it, since a struct with padding
  comes back wrong from a call through a function pointer on a spawned cog and
  takes the program down with it (`doc/struct-return-through-pointer-on-cog.c`).
  A multi-result method reached through a longer chain is refused for now, and so
  is a multi-result FUNCTION VALUE on a cog, which is the same backend fault by
  another road.

- **`ogo test ./...` tests every package under a root.** A program of several
  packages had to be tested a directory at a time; a pattern ending in `...` now
  walks the tree and tests each package it finds, in path order, one board run
  each. A package that fails to build is reported and the rest still run, as
  under `go test`, and the run fails as a whole if any package did. A pattern
  matching no package is an error rather than a silent success -- a run that
  tested nothing must not read like one that passed.

### Fixed

- **Calling what a call returned was refused for its results.** `a := pick()(3)`
  was "cannot infer a type for the declaration of a", and `b, ok := pick2()(5)`
  was "assignment mismatch: 2 variables but pick2 returns 1 value" -- of a second
  call that returns exactly two. Both sides read the FIRST callee's results, where
  the values are the second call's: the function type the first call yields is
  what declares them. The forms that ask nothing -- `println(pick()(3))`, `var a
  int = pick()(4)`, an argument -- always worked, which is what made the gap look
  like a type error rather than a missing shape.

- **A function value with several results was miscompiled on a goroutine, and
  drew a backend diagnostic everywhere.** `fn := two; a, b := fn(3)` for a `two`
  returning `(int32, bool)`: every assignment of such a function to a value drew
  "expected function of 1 args returning ... but got ... unknown type" from the C
  backend -- which cannot match a struct-returning function against a function
  pointer, whatever the struct is called -- and calling one on a spawned cog took
  the whole program down, the fault documented in
  `doc/struct-return-through-pointer-on-cog.c`. Such a value now points at a void
  wrapper that writes the results through an out parameter and calls the function
  directly, so no struct is returned through a pointer. Held in a package
  variable, a struct field, a local, an argument or another function's result, and
  called on either cog.

- **`ogo fmt` wrote an interface method's result list tight.** `Next() (int32,
  bool)` came back as `Next()(int32, bool)`: a method spec is a signature written
  without the word `func`, and the rule that spaces a parameter list from a result
  list keyed on the `func` form alone, so every interface method with several
  results was reformatted into something gofmt does not write.

- **`%T` of a type from another package printed the compiler's symbol.**
  `printf("%T", t)` for a `lib.Temp` printed `lib_Temp`, the mangled C name, where
  Go prints `lib.Temp` -- and every diagnostic that names a type said the same.
  Each type records what a program calls it, so the spelling comes from the
  declaration rather than from guessing at the symbol.

- **A type of another package lost half its identity at the boundary.** Four
  ways: a method PROMOTED from an embedded field was "type lib.Box has no method
  Sum" and a promoted field "has no field X", so embedding worked inside a
  package and vanished outside it; passing `&s` to an imported function whose
  parameter is that package's interface was "cannot use &s (an address) as Shape
  value", of the one spelling an interface takes here, because the parameter's
  type was resolved in the CALLER's scope and so was not recognised as an
  interface at all; and `p := &s` dropped the qualifier from p's type, so `p` was
  "variable of type Sq" and passing it where its interface was wanted reported
  that Sq implements nothing -- of a type whose method is in the package it came
  from. The messages name the qualified type now, and the promotion walk runs in
  the owning package's scope.

- **Printing an imported package's array printed its address.** `println(lib.
  Names)` and `printf("%v", lib.Names)` reached the %d default and printed a
  number where Go prints `[aa bb]` -- the branch that views a bare array as a
  full-length slice asked for a sole identifier, and a qualified name is not one.
  A silent wrong answer, not a refusal.

- **A method's receiver and a function value named an unmangled symbol outside
  `main`.** Two more of the same kind as the package-variable fix below, found by
  re-running the sweep with the code in an imported package: `G.Sum()` on a
  package struct variable emitted `lib_P_Sum(G)` where the variable is `lib_G`,
  and `f := Double` emitted the bare `Double` where a CALL of it beside was
  correctly `lib_Double`. Both named a symbol that does not exist, so a package
  could not call a method on its own variable, nor take its own function as a
  value -- unless it was `main`, whose prefix is empty.

- **A refusal named the compiler's symbol for an imported variable.** A store
  the escape rule refuses reported "in package variable `geo_Sl`" of a program
  that says `geo.Sl` -- the name reaches those checks already mangled -- and so
  did the two chain refusals that name their base. They spell it as it was
  written now. The rule itself was right across the boundary: a package variable
  outlives every frame, whichever package declares it, and a test pins both.

- **An imported package's array or slice variable could not be used at all.**
  `geo.Table[1]` was "geo is not a value with fields or elements",
  `len(geo.Table)` was "len is only supported for strings, arrays and slices
  yet", `geo.Ints[0] = 5` was "only simple and field assignment targets are
  supported yet", `for i, v := range geo.Ints` was "ranging an integer yields
  only the index", `copy(dst, geo.Ints[:])` was "copy's arguments must both be
  slices", `p := &geo.Ints` could not be typed, and `xs := geo.Ints[:2]` could
  not either -- every shape, refused with a different sentence, because each one
  resolves the head of a chain as a NAME and a package qualifier is not one. The
  qualifier is folded into the member's C name where the chain is split, so all
  of them are the shapes the emitter already writes for a global of its own
  package. A lookup table declared in one package and read from another is the
  reason this matters, and it did not work.

- **A package could not use its own array, slice or string variable -- unless it
  was `main`.** Every name of a package variable that a program reaches INTO --
  `Ints[0]`, `Ints[i] = v`, `range Ints`, `Ints[:]`, `&Ints`, `copy(dst, Sl)`,
  `Sl[1:3]`, `Tag[1]`, `println(Ints)` -- was emitted as the source name rather
  than the package-mangled one, so it named a symbol that does not exist and the
  build failed with `Unknown symbol 'Ints'` or `Ints is not an array`. In `main`
  the two spellings are the same text (its prefix is empty), which is why the
  whole suite, the fuzzer and every example missed it: they are all one package.
  A package-level array is the one variable kind that is in no registry the
  name-rendering path consulted -- an array has no C value type -- so an array
  was wrong in every position, and a slice or a string in the positions that
  reach through the header.

- **A method could not be called on an element of an imported package's array
  variable.** `geo.Table[1].Int()` was "geo is not a value with fields or
  elements": the chain renderer's import-qualifier head knew that package's
  constants, functions and plain variables, and an array is none of those. It
  enters the chain as this package's own arrays do now, and so does the rest of
  that family (below).

- **`7 / 2.0` was 3, and `2 * 3.5` printed as an integer's bits.** A binary
  operation over two untyped constants took the kind of its FIRST operand, where
  Go takes the wider of the two in the order int, rune, float: `x := 7 / 2.0` was
  refused as "constant 3.5 truncated to int", `const Z = 7 / 2.0` reached C as an
  integer division, and `printf("%v", 2*3.5)` computed a double and printed it
  through an int -- 1306764736. Checker and emitter now both take the wider kind,
  whichever side it stands on; a shift's count contributes nothing, as before.

- **A constant expression is evaluated exactly, as Go's are.** `0.1 + 0.2` is
  three tenths, so `0.1+0.2 == 0.3` is true in Go, and `1/3.0*3 == 1`; both were
  handed to C as written, computed in doubles, and false. The emitter folds a
  constant expression with `go/constant` and spells the result once, rounded to
  the level's type -- the longest constant prefix of each level, since Go folds
  `0.1 + 0.2 + x` as `0.3 + x` and `x + 0.1 + 0.2` not at all. A float constant
  is now inlined at each use, as a string or 64-bit constant is, and declares
  nothing, which is also what a static initializer needed of it: the target's C
  compiler takes neither a `static const` name nor a unary minus there.

- **A constant beside a `float32` operand is a `float32`.** `f == 0.3` for a
  float32 f compares two float32s in Go, and compared f's promotion with the
  double 0.3 in C: false where Go says true, and `g + 0.2 == 0.3` the same. Such
  a constant is spelled as a float32 literal, `0.3f`, in a float32 level and in a
  comparison against one.

- **An integral float constant serves where an integer is wanted.** `1 << Two`,
  `x[Two]`, `x[Two:]` and `make([]int, Two)` for a `const Two = 2.0` are all Go;
  the make was "needs a constant capacity", and the shift handed its helper the
  double 2.0, which the target's C compiler converts to the helper's `int64_t` by
  the bits -- `1 << Two` was 0 on the board and 4 on the host. Each such position
  now spells the integer the constant is. The array LENGTH position, `[Two]int`,
  is still refused ("invalid array bound"), and `len(make([]int, 3))` is still
  unsupported.

- **`+x` on a float could not be built for the target.** The C backend refuses a
  unary plus on a double -- "Bad number of parameters in call to _float_add:
  expected 2 found 1" (`doc/unary-plus-float.c`) -- so `(+Neg).Abs()` failed to
  build. The operator is the identity in Go and is now dropped for a float
  operand.

- **A method could not be called on a string constant, nor a chain hung off one,
  and a local bound to an imported string constant lost its type.** For a `type Cmd
  string` with methods and `const Start Cmd = "start"`, `Start.Len()` reached the C
  backend naming a symbol that does not exist -- a string constant is inlined at
  each use -- and `Start.Twice().Len()` was "unsupported call in expression"; the
  same through an import qualifier, `geo.Unit.Len()`; and `x := geo.Unit` typed x
  a plain string, so `x.Len()` found no method and `string(geo.Unit.Upper())` was
  refused as needing allocation, its operand having no type to be a string by.
  Every position that renders a name -- a method's receiver, a chain's head, a
  switch tag -- now asks for an inlined constant's literal first, for string and
  64-bit constants alike, and a chain from an imported package's constant or
  function is typed by rendering it. Found by the sweep of every position a string
  constant can stand in, the sequel to the same sweep for a 64-bit one.

- **`append(b, s...)` refused a string of a defined type.** `append(b, Start...)`
  for a `Start` of `type Cmd string` was "cannot append Cmd... to []uint8", where Go
  spreads any string type's bytes onto a `[]byte`; a defined slice type spreads
  its elements likewise now.

- **A method could not be called on an imported package's constant, or on the
  result of its function.** `geo.Huge.Twice()`, `geo.Huge.Int()` and
  `geo.Sum(a, b).Int()` were each "unsupported call in expression" -- the chain
  lowering knew a variable, a same-package function and a conversion at its head,
  and not an import qualifier -- while `x := geo.Huge; x.Int()` worked. A chain may
  now start from an imported package's constant, function or variable.

- **An integer converted to a float rounded a tie away from zero.** `float32(16777217)`
  was 16777218 on the board where IEEE 754 and Go round to even, 16777216; half of
  the integers between 2²⁴ and 2²⁵ are such ties, so a counter past sixteen million
  converted to a float was one unit off half the time. The C backend's conversion is
  at fault; its float arithmetic rounds correctly. The compiler now converts an
  integer of 32 bits or more itself, rounding in integer arithmetic to the width of
  the float being made -- 24 bits for a float32, and for a float64 the width the
  target's double actually has, read at run time, so the same code is right on a host
  with 64-bit doubles. A constant converting to a float32 is folded outright.

- **A package-level slice literal refused a constant element that was not a bare
  literal.** `var s = []int64{-5}`, `{1 << 40}`, `{int64(7)}`, `{K}` and `{-1.5}` were
  all "a package slice literal's elements must be constant" while `{4294967295}` was
  accepted: the element test knew a bare literal and nothing else. It asks the
  constant fold now, and a folded string counts too. A named constant standing in a
  static initializer is written as its value, since to the C backend the constant's
  own symbol is an object rather than a constant expression -- "Bad constant
  expression" for a bare `K` in the backing array, "Illegal operation on relocatable
  value" for `K << 2` -- and a signed float literal is written as one literal there,
  a unary minus being refused in any aggregate initializer.

- **`-9223372036854775808` written out did not build inside an array literal.** The
  magnitude of the most negative int64 fits no int64, so the constant did not fold
  and was written as a minus in front of a `ULL` literal, which the C backend refuses
  in any aggregate initializer. The pair folds now, and takes the bit-pattern spelling
  there like every other negative constant too wide for an int.

- **A store through a nil pointer, and any use of a nil pointer to an array, said
  nothing.** The nil check that a read through a pointer and a field access have
  carried since v0.27 missed the assignment `*p = v` -- on the board a silent write
  into hub address 0, the boot area -- and every dereference of a `*[N]T`: `p[i]`,
  `p[i] = v`, `range p` with a value, `p[lo:hi]`, `*p` copied out. All of them now
  panic `nil pointer dereference`; `len(p)` and the index-only `for i := range p`
  still do not, as in Go, which dereferences nothing there.

  The pointer to an array had been left out on purpose: the C backend drops a store
  made through the guard's *call* into an element of one, so the guard cost the
  write it guarded. Measured again, the drop takes a struct-valued element, and the
  same read of one fails to assemble, while a word goes through either way -- so
  such a pointer is now checked by a statement of its own ahead of the one that
  dereferences it, and the dereference stays as it was.

  Two spellings came along: `(*p)++` and `(*p) += v` were refused as unsupported
  targets, and `*pa = [4]int{1, 2, 3, 4}` through a pointer to an array emitted a C
  assignment to an array, which is not C.

- **A 64-bit constant expression computed garbage on the board.** `int64(5) + 1`
  printed 4294967296000006, `int64(1000) * 1000` printed 4294967302,
  `uint64(5) + 1` 4294967302, `Ticks(3) * 4` 4294967308 and `int64(-3) * int64(7)`
  51539607531, silently. The C backend mis-folds nearly every 64-bit constant
  expression inside a function body -- `(int64_t)(5) + 1`, `5LL + 1`,
  `4294967296LL - 1` -- at every optimisation level, while the same expression with
  a variable operand is right, a lone literal is right, and gcc is right about all
  of it; `doc/int64-constant-fold.c` is the reproducer, and spin2cpp master has the
  fault too. The compiler left such an expression as written whenever its value
  fit an int, on the premise that C folds it the same way. It now folds every
  64-bit constant expression itself and emits one literal, and a 64-bit named
  constant is inlined at each use rather than declared -- where it stands as a
  method's receiver too, `One.Div(x)`, and as a switch tag, `switch One {`, both of
  which the first cut of this left naming a symbol that no longer existed; a pointer
  method on a constant is refused in Go's words. The fold computes a `uint64`
  level as unsigned: its first cut divided and shifted the bits as signed, and
  `uint32(U >> 40)` for a `const U uint64 = 1 << 63` was 4286578688 where Go gives
  8388608. A comparison of two such constants is folded to its answer, and a bare
  literal compared with a 64-bit expression is written at that width, `0LL`: the C
  backend folds the first through a helper of its own and does not widen the
  literal for either, "Bad number of parameters in call to _int64_cmps".

- **A negative constant wider than an int compared as unsigned.** It was spelled
  as its bit pattern, `0xFFFFFFFF00000001ULL`, which makes any expression it stands
  in unsigned: `v > -4294967295` was false for an int64 `v` of 5, on the host and
  on the board alike. In an expression it is now spelled signed, `(-4294967295LL)`;
  the bit pattern stays inside aggregate initializers, where the backend refuses a
  unary minus and the element's type does the converting.

- **`-2147483648` was the negation of an unsigned.** Its magnitude does not fit an
  int, so it was written `2147483648U`, and negating an unsigned gives it back:
  `var a int64 = -2147483648` was 2147483648 on the host, and
  `int64(-2147483648) + 1` was garbage on the board. The most negative int is now
  spelled `(-2147483647 - 1)`.

- **A float converted to a 64-bit integer gave the float's bits, and to a 32-bit
  unsigned one was clamped at 2147483647.** `int64(v)` for a `v` of 3.0 was
  1077936128 -- 0x40400000, the IEEE encoding of 3.0 -- and `uint32(v)` for a `v` of
  3000000000.0 was 2147483647, on the board, silently. Every narrower conversion was
  right, which is why it survived: `int(v)` is what most programs write.

  The fault is the C backend's, at every optimisation level, and it is joined by
  two more of its faults that the fix had to route around: a cast to a 64-bit type
  as the operand of a `return` leaves the high word uninitialised whenever the value
  is narrower, and `return v < 0 ? -r : r` over an int64 returns garbage. All three
  are eleven lines of C each in `doc/float-to-int64.c`, and gcc compiles every one
  of them correctly. The conversions now go through helpers built on the 32-bit
  signed conversion, which that compiler gets right, a value past 2^31 being taken
  apart at 2^32 and converted in halves.

  Found by the same lock-in probe: the check that a converted result matched Go's
  did, on the host, and did not on the board.

- **A division by a constant that is not a bare literal was guarded at run time, and
  the guard could be the wrong width.** `x / (1 << 32)` on an int64 -- the way a
  CORDIC angle in turn/2^32 units is scaled to degrees -- panicked
  `integer divide by zero` in a checked build, which is the default: only a bare
  literal was recognised as constant, so the divisor went through the zero guard,
  and the guard's int truncated 2^32 to its low word. `x / (1 << 31)` divided by the
  int's most negative value instead, silently. `n % N` for a constant N paid for a
  check on every pass of a loop, correctly.

  The guard's width was also read from the divisor's own type *name*, so a
  `type U uint64` -- spelled `U`, not `uint64_t` -- took the 32-bit guard: `a / b`
  over two of them panicked for a `b` whose low word is zero and silently divided by
  that word for any other, and a `type F float64` had its divisor truncated to an
  integer, `5 / 2.5` computing 2.5. A divisor that folds to a constant is now not
  guarded whatever its spelling, and one that does not is guarded at the width of
  the type the division computes in, resolved past its definition.

  Found by writing a lock-in detector -- a CORDIC sine table, int64 accumulators, an
  integer square root -- and running it on the board; the host C compiler warns
  about the truncation, and the sweep that found it treats a warning as a failure.

## v0.31.1

### Documentation

- **The v0.31.0 section was labelled "Unreleased" in the tag that shipped it.** The
  heading is renamed before tagging, and this once it was not, so
  `git show v0.31.0:CHANGELOG.md` shows the release's own entries under a heading
  that says they have not shipped. The tag itself is left alone: proxy.golang.org had
  already cached it, and a module proxy's cache is immutable, so moving a tag it
  serves would diverge from the repository permanently. The label is corrected here
  instead.

## v0.31.0

### Documentation

- **The import rule was documented backwards.** The README said an imported package
  must be a subdirectory of the package that imports it — which is the one layout
  that never worked. Import paths have always been read against a root, and a
  program's packages sit below it; see **`ogo.mod`** under Language for where that
  root now comes from.

  A package importing another package was also untested. `chain/` in the
  multi-package fixture pins it now, on the host and on the board.

### Fixed

- **A compound assignment reading an array field through a pointer computed the
  wrong answer.** `m.sum -= m.ring[m.at]` — a ring buffer's accumulator, through a
  pointer receiver — left `m.sum` holding garbage, or left it untouched, and said
  nothing. A moving average built on it returned a wrong average.

  The fault is the C backend's, and it needs all three of a **compound** operator,
  an **array** member, and a pointer returned by a **call**: `x = x - p.ring[1]` is
  right, and so is the same compound assignment through a pointer variable. Remove
  any one and the answer is correct, which is why it survived — nothing about the
  source looks unusual. `doc/compound-call-index.c` is the eleven lines of C that
  show it, and gcc compiles them correctly.

  It reached ordinary programs because the call is the compiler's own: a **checked
  build, which is the default**, wraps every pointer dereference in the nil guard,
  and that guard is the call. The compiler now binds such an operand to a temporary
  first, which is what the same expression already compiles to when a program writes
  it in two statements.

  **The same fault applies with the array field as the assignment's TARGET**, where
  it is a silent no-op: `h.bins[v%4] += v` added nothing at all, so any per-bin or
  per-channel total kept in an array field and updated through a pointer simply
  stayed at zero. `h.bins[i]++` was unaffected, and so was the same statement
  through a value, which is what kept it looking like something other than a
  compiler fault. A slice field was never affected — it is reached through its own
  backing pointer, so no call stands between the pointer and the index.

  **The host C compiler gets this right**, so no host test could have found it. It
  was found by writing a rheometer's control loop — fixed-point maths, a moving
  average, a PID loop, a framed protocol, six packages — and diffing what the board
  printed against what the same program printed under Go.

- **`ogo build` said little about a mistyped path.** Anything that is not a
  directory is taken for a source file, so `ogo build ./sensr` arrived as one and
  was reported by whoever failed to open it — `open sensr: no such file or
  directory`, naming the base and not the path that was typed. It now names the
  path, and a named file that is not a `.ogo` says that instead of being parsed as
  one.

- **`ogo build` on a package with no `func main` reported a C compiler's
  complaint.** It ran the whole pipeline and reached flexcc, which said
  `error: could not find function main` — about a C program the user never wrote.
  Such a package is a **library**: OctoGo has no package clause, so declaring a
  `main` or not is the whole of what tells a program from one. A library is now
  checked and emitted — the lifetime and escape refusals are made during emission,
  so checking one any less thoroughly than a program would be a different standard
  for the same code — and then stops, there being nothing to link:

      $ ogo build ./sensor
      ok      ./sensor        [no func main, checked only]

  `-o` on a library says it has nothing to write, and `ogo run` says there is no
  `func main` to run. Both used to reach the backend too.

- **`ogo build --gostack N` sets a goroutine's stack**, 64 to 8192 longs, 256 as
  before by default. It is the other half of the fence below: the panic names the
  limit, and this is how the limit moves. Measured on a P2-EDGE, a goroutine
  recursing 400 deep panics at the default and returns cleanly at 2048. Seven slots
  of it sit in hub RAM for the whole run, which is why the default did not simply
  grow.

- **A goroutine that overruns its stack says so.** Each pool slot's 256 longs are
  now fenced, and a goroutine that runs past them and still returns ends with
  `panic: goroutine stack overflow` — the one failure in this runtime that had no
  diagnostic, where cog exhaustion and a stalled stop both have one.

  Measuring it corrected the documentation as well: a goroutine recursing a *hundred*
  deep already overruns, where the README said two hundred was fine. It printed the
  right answer and quietly overwrote the neighbouring slot, which is why it read as
  fine. A goroutine that overruns by much more loses control before the fence can be
  read, and no check without memory protection can catch that one.

- **`printf` matched `fmt` in two fewer places than it should have.** `%+d` of an
  *unsigned* value dropped the sign — C's `+` flag applies to its signed
  conversions, so `%+u` writes none where fmt writes `+255`. The value is now
  printed through the signed conversion, which carries the same digits and honours
  the flag; a `uint64` has no signed type wide enough and is refused instead, which
  is the third such refusal and says so where it is written.

  And `%08d` of a *negative* value printed nine characters where Go prints eight:
  the backend pads the digits to the width and adds the sign on top. The field is
  now written by the compiler. The host C compiler is right about both, which is
  what kept them hidden — they were found by a generated cross of every verb
  against every type, run on the board, which is now three run cases.

### Language

- **A channel another package declares can be used.** `pkg.Ch <- v` and `<-pkg.Ch`
  were both refused outright — "a send statement needs a channel on the left" and
  "unsupported operand", from the emitter — so two packages had no way to share one:

      // work
      var Out chan int32
      func Square(id int32) { Out <- id * id }

      // main
      go work.Square(3)
      println(<-work.Out)

  With no heap this is not the exotic spelling it would be in Go. `make` allocates,
  so a constructor has nothing to return, and a package-level channel is simply how
  a channel gets shared. A qualified channel is spelled like a struct field and is
  not one, which is why it fell through: the field lookup refused a package
  qualifier and nothing else claimed it.

  The send is now **checked** as a send to the package's own channel is — the target
  must be a channel, a constant must fit the element type, and the value's type must
  be assignable to it. Making the path reachable made it checkable: before, a
  mismatch was reported by the C compiler, about C the user never wrote. A channel
  of a NAMED element type declared elsewhere is checked for kind but not identity;
  the name belongs to the callee's scope, and resolving it here would name a
  different type or none.

  Verified on a P2-EDGE: seven cogs — the whole chip beside `main` — each running an
  imported package's function and rendezvousing on that package's channels in both
  directions, matching the same program under Go.

- **`ogo.mod` says where a program's root is**, and with it a `cmd/` layout and a
  package shared by two programs both work. The file holds one line —
  `module example.com/proj` — and a build takes the nearest one at or above the
  package it is given. Import paths are then written as Go writes them, module path
  included, and name the same directory whoever writes them:

      proj/ogo.mod              module example.com/proj
      proj/sensor/
      proj/cmd/firmware/        import "example.com/proj/sensor"
      proj/cmd/calib/           import "example.com/proj/sensor"

  Both programs compile against the *one* copy of `sensor/`. That could not be
  written before: the root was whatever directory you happened to build, so a
  library had to sit below each program that used it, one copy each, kept in step by
  hand. Neither the importing package's own location nor the working directory takes
  any part in resolving a path now — the same package built from inside itself
  produces a byte-identical binary.

  **Without an `ogo.mod` nothing changes.** The directory being built is the root and
  paths are relative to it (`import "sensor"`), which is every program written so
  far. A bare path *inside* a module is refused with the path that would have
  worked, since it is the mistake anyone arriving from Go's rules will not make and
  everyone else will.

  There is no versioning and there are no external dependencies to resolve: every
  package of a program is a directory inside the module, so a directive `ogo.mod`
  does not implement — `require`, say — is refused rather than quietly ignored. It
  is deliberately not `go.mod`; an OctoGo project inside a Go repository would find
  the *surrounding* module and take the wrong root from it.

  Two things are newly refused, both only inside a module: an import path the module
  does not contain, and an import of the main package, which is a program and not a
  library. The second used to be unreachable and would otherwise have hung the
  compiler.

  Two more are refused everywhere, module or not.

  **The device names Windows reserves** — `con`, `prn`, `aux`, `nul`, `com0`–`com9`
  and `lpt0`–`lpt9` — as a package directory and as a source file's name, where the
  extension does not save it (`aux.ogo` is `aux`). Nothing on Windows may carry one,
  in any case, so a repository holding `con/` or `aux.ogo` cannot be checked out
  there at all. On a unix they are ordinary words, and `con`, `aux` and `prn` are
  plausible names for a package on a microcontroller — so the mistake is invisible
  until somebody else clones the project. `console/` and `com/` are unaffected; only
  the exact names are reserved.

  And **a capital in a package
  directory**. `foo/` and `Foo/` are one directory on macOS and Windows, so a
  program holding both cannot be checked out there, let alone built — and the
  developer who writes it on Linux, where both live happily, would learn that from
  whoever clones it. The module prefix keeps its capitals, being a repository name
  that never reaches a filesystem, so `example.com/BurntSushi/proj/sensor` is a
  path and `example.com/proj/Sensor` is not.

- **`string(r)` works for a rune the program COMPUTES**, not only a constant one —
  which is what `for _, r := range s { print(string(r)) }` needs, about as ordinary
  as Go gets and refused outright until now. A rune's UTF-8 is at most four bytes,
  so the storage is four bytes of the frame, hoisted beside the statement. That
  bound is the whole argument: `string(b)` for a byte *slice* needs as many bytes as
  the slice is long, and is still refused.

  The result is a **view** of those four bytes, so the lifetime rules govern it as
  they govern a slice over a local array. It may be printed, compared, switched on
  and passed to a function that does not keep it; it may not be returned, stored
  where it outlives its block, sent on a channel, or handed to a cog. The case worth
  naming is `out[i] = string(r)` inside a loop, which looks like it should work and
  is refused: every iteration would hand back the same four bytes.

### Behaviour changes

- **A package directory must be lower case, and may not carry a name Windows
  reserves.** Both refusals apply with or without an `ogo.mod`, so a program that
  built on v0.30.0 with a `Foo/` package — or with a source file called `aux.ogo` —
  does not build now:

      invalid import path "example.com/proj/Sensor": package directory "Sensor" must
      be lower case: a directory differing from another only in case is the SAME
      directory on macOS and Windows

  Rename the directory or the file. They are refused rather than merely discouraged
  because neither survives a checkout elsewhere: `foo/` and `Foo/` are one directory
  on macOS and Windows, and nothing there may be called `con`, `prn`, `aux`, `nul`,
  `com0`–`com9` or `lpt0`–`lpt9`, extension or not — `aux.ogo` is `aux`. Refusing
  reports it on the machine that writes the program rather than on the machine that
  clones it. The module prefix keeps its capitals, being a repository name, so
  `example.com/BurntSushi/proj/sensor` is a path and `example.com/proj/Sensor` is
  not.

- **Two failures became diagnostics.** Importing the main package used to hang the
  compiler and is now refused by name, and `ogo build -o … ./somelibrary` used to
  reach the C backend and report `could not find function main` — a C compiler's
  complaint about a C program you never wrote — where it now says the package
  declares no `func main` and there is nothing to write. Neither could have been in
  a working build, so this breaks nothing; it is listed because the message a script
  sees has changed.

## v0.30.0

Two silent wrong answers on the board, and the last hole in the lifetime rules.

A constant on the LEFT of an unsigned operand was typed *signed* by the C backend.
The value it produced was right, which is what hid it: nothing looked wrong until a
division, a shift or an ordering comparison read the type, and then each took the
signed branch and answered wrongly. The same expression with the operands the other
way round had been right all along. And a guarded division read the type of its own
left operand, so `3 / b` for a 64-bit `b` was typed `int`, chose a 32-bit zero-guard,
and aborted a program that divides by a perfectly good number.

Neither was visible on the host, whose C compiler is correct about both. Both are now
covered by generated crosses that run on the board: mixed-type arithmetic, every
sized type against every operator with the constant on each side, which found them;
and 1152 integer conversions, which found nothing and is kept as the cheap half of
re-checking the backend after a regeneration.

A reference could still outlive its frame through a method on a LOCAL receiver — the
last receiver shape the crossing summary could not name, because the declaration is
in the body and naming it takes a scan of the body. With it the escape matrix closes:
every reference kind, against every sink, through every receiver a call can be made
on.

Elsewhere: a `Builder` may live in a struct field, a package array literal may have
computed elements instead of failing to build at all, a method may be called on a
parenthesised expression — which is how fixed-point arithmetic reads — and
`p2.WaitUntil` makes the drift-free control loop expressible, the intrinsic having
been there all along.

### Language

- **A `Builder` may be held in a struct field** — the shape a line parser that owns
  its buffer is written with. Two things stood in the way, and neither was about the
  field. The Builder *typedef* was emitted after the struct typedefs, on the stated
  grounds that it embeds the string and byte-slice types — it embeds neither, its
  helpers do — so `struct Line { ogo_builder sb; }` named a type C had not seen and
  the program did not compile at all. And the Builder's method set is the
  compiler's rather than a declaration's, which the path resolving a method on a
  *variable* knew and the path resolving one on a *field* did not: `l.sb.Len()` was
  *type Builder has no method Len*, of a method it certainly has. A misspelled one
  is still refused.

- **A package ARRAY literal may have computed elements** — `var curve =
  [3]Point{{FromInt(0), FromInt(10)}, …}`, and `[2]int{seed(), 5}`. C evaluates a
  static initializer at compile time, so a call in one is not a program the backend
  will take — and it said so in words about generated C the program never wrote
  (*global initializers … must be constant*), which is worse than any refusal. The
  table is now zeroed and filled at package initialization, ordered against the
  variables it reads, exactly as the scalar and struct forms already were. An
  all-constant literal is still a static table.

  A package *slice* literal is refused instead, naming the shape that works
  (declare an array and slice it): its backing is a file-scope table with no fill
  yet. A local slice literal is unaffected — it has a frame backing and a statement
  to fill it in.

- **A method may be called on a PARENTHESISED expression** — `(raw - lo).Div(span)`,
  and a chain of them, `(a - b).Add(1).Scale(2)`. Every other receiver shape already
  worked (a variable, a parenthesised variable, a field, an element, a call's
  result), and so did binding the arithmetic to a variable first — so the workaround
  was accepted while the plain spelling drew *this form is not supported yet*. It is
  how fixed-point arithmetic reads, which is how this was found.

  The expression may itself be a POINTER, `(&P{5, 6}).Sum()`: a value method takes
  what it points at and a pointer method takes it as it stands, and the call is
  typed by the *method's* result rather than by the address it is called on. A
  pointer method on a non-pointer expression is refused, as Go refuses it — there
  is nothing to take the address of. Arguments are still evaluated left to right,
  which needed the effect analysis to learn this call shape.

- **An integer constant may be RETURNED as a float** — `return 0` from a `float64`
  function, which Go accepts as an untyped constant taking the result's type. Every
  other position already did: a variable declaration, an assignment, an argument, a
  struct or array literal element, a package variable and a comparison. Only the
  return refused it, having asked the predicate that drives the integer *range*
  checks, which floats have no place in.

- **`p2.WaitUntil` waits until the counter REACHES a value**, where `WaitCycles`
  waits a number of cycles. That is the difference between a loop that keeps time
  and one that drifts — the work a body does is inside the wait rather than after
  it — and the `p2` package had only the second, so the periodic sampler a control
  program is built around could not be written. flexcc had the intrinsic
  (`_waitcnt`) all along; it was simply unwrapped. A deadline already past returns
  at once rather than waiting a counter wrap, verified on the board.

  `p2.GetUs` comes with it, completing the `GetMs`/`GetSec` family.

### Fixed

- **`3 / b` for a 64-bit `b` panicked with *integer divide by zero*.** The guarded
  division helper was chosen from the type of the *left* operand, so a constant
  dividend typed the whole thing `int` — which picked the 32-bit zero-guard, and that
  truncated the divisor to its low word. For `0x1000000000000000` the low word is
  zero, so a program dividing by a perfectly good number aborted. The type is now read
  from the level, where an untyped constant takes the other operand's type; that is
  the rule the expression typer already stated, and only this reader of it took the
  first operand.

- **Unsigned arithmetic with a constant on the LEFT gave signed answers on the
  board.** `4 * u` for a `uint32` u was typed *signed* by the C backend — the
  product's value was right, so nothing looked wrong until something signedness-
  sensitive read it, and then `4 * u / 3` returned 3937053355 where Go returns
  1073741824, `4 * u >> 1` returned 3758096384 where Go returns 1610612736, and
  `v >= 4*u` answered true where Go answers false. Measured on a P2-EDGE; the host
  C compiler is correct, so the host tests said nothing.

  Writing the same expression the other way round, `u * 4`, was right all along —
  which is why it went unnoticed, and why an operand order is worth probing both
  ways whenever a type can be lost. A constant operand of an unsigned level is now
  spelled unsigned, `4u * u`, which settles the shapes reordering could not: `100 -
  u` was wrong the same way.

### Testing

- **Two generated crosses now run on the board.** Mixed-type integer arithmetic —
  every sized type against every operator, with the constant on each side — which
  found both defects above; and 1152 integer conversions, every sized type to every
  other, with nesting, defined types and a call's result, which found nothing and is
  kept as the cheap half of re-checking the backend after a regeneration. Float
  *printing* is deliberately outside both: `float64` is 32-bit on this target, so
  the digits past the seventh are the host's and not the board's.

### Behaviour changes

- **A reference to a local may no longer be laundered through a method on a LOCAL
  receiver.** `var scratch H; scratch.stash(d)` recorded no call edge at all, so a
  method that stores its parameter in a package variable — or hands it to a cog —
  said nothing to the callers of the function holding the scratch struct, and the
  reference outlived its frame silently. It was the last receiver shape the crossing
  summary could not name: the declaration is in the body, so naming it takes a scan
  of the body.

  What the method stores into the receiver ITSELF is still fine — the receiver is a
  local and dies with the frame — and a read-only method, or one handed storage that
  outlives the call, is unaffected. A local name declared twice with different types
  names neither: the scan has no scopes, and a wrong type would name a wrong method.

- **A float constant that is not whole is refused where an integer is wanted.**
  `var n int = 1.5` compiled and stored 1; so did `n = 1.5`, `return 1.5`,
  `take(1.5)`, `S{1.5}`, `[]int{1.5}`, `ch <- 1.5` and `int32(2.5)` — every position
  a constant meets a type except a constant declaration, which was the one that
  already refused it. Go accepts a constant only where it is *representable*, and
  1.5 is not an int. `2.0` is whole and still converts wherever an integer constant
  does, and truncating a float *variable*, `int(x)`, is a run-time conversion that
  is legal and unchanged.

  Found by writing an ordinary control program; the compiler's own test corpus
  contained `println(int(3.75))`, which Go rejects — the golden had been written
  from the implementation and agreed with it.

- **A constant SENT on a channel is range-checked.** `ch <- 200` on a `chan int8`
  stored −56 and `ch <- 1.5` on a `chan int` stored 1, both silently. The send was
  the one position that never asked whether the value fits the element type, though
  an assignment, an argument and a return all did.

## v0.29.0

Lifetime rules that hold at every spelling, and the array as a value.

A reference could still reach storage that had gone, by routes the rules did not
recognise: nested in a composite literal, carried out by an `append`, stored into a
method's receiver, stored through a pointer parameter, and — past all of them —
through an interface, where the call names no function and so nothing was asked at
all. Each was a build that succeeded and then read a dead frame. The rules
themselves were right; what was missing was the set of spellings they had to see.

Two of them change what compiles rather than only what is caught. A call through an
interface is judged against every implementation at once, so one that keeps its
argument constrains the calls even where the value assigned is one that does not —
refusing some correct programs, because proving which implementation runs is a pass
that does not exist yet. And a reference may no longer outlive the *block* of the
variable it points at, which is what makes Go's per-iteration loop variable mean here
what it means in Go rather than quietly meaning what it meant before Go 1.22: where
the reference does not outlive the iteration the two are indistinguishable, and where
it does, matching Go would need a heap.

The second arc is the array reached by a longer route than its own name. A method may
return one, be called on an element, yield several results from one, run on a cog and
survive a defer; a package array variable may be initialized by anything a local can
be; and a call's array result may fill a composite literal's element. A defined array,
slice or channel type is a type of its own, an argument of the wrong shape no longer
builds, and a literal's elements are type-checked.

Elsewhere: a constant rune converts to a string, as Go makes it a constant; a channel
declared from another names that channel rather than hanging; a send may name one two
fields deep; and another package's interface, string constants and conversions are
usable.

### Language

- **A constant rune converts to a string** — `string('A')`, `string(rune(66))`,
  `string(67)`, `string(c)` for a named constant, and `string('x'+1)`. Go makes such
  a conversion a *constant string*, and this refused every one of them as *a string
  conversion needs allocation, which the target does not have* — true of the run-time
  conversion, and of nothing here: the bytes are known at compile time and are folded
  into the literal, allocating and copying nothing. `println(string('A'))` was an
  error.

  The result stands wherever a string literal does — a local, an argument, a struct
  or array literal element, a comparison, a switch operand, a package variable — and
  takes part in constant concatenation, `"hi" + string('!')`.

  The encoding is Go's, one to four UTF-8 bytes, with `"\uFFFD"` for a value that is
  no code point: a surrogate half, a negative, or one past U+10FFFF. That last is a
  *conversion*, not an error, because the target type is string — `string(1 << 40)`
  is a legal program printing the replacement character, while `rune(1 << 40)` is
  refused at that conversion, as in Go. `string(rune(0))` is a string of length one
  holding a NUL.

  The run-time conversion — `string(r)` for a rune *variable*, and `string(b)` from
  a byte slice — is still refused; it needs storage the caller must choose.

- **A constant string element of a package array literal builds on the target
  again** — `var parts = [2]string{pre + "y", "a" + "b"}` was emitted with a compound
  literal per element, which the backend rejects in a file-scope initializer (*Bad
  constant expression*) though the host C compiler accepts it, so the program did not
  build at all — while the same array written with plain literals did. The brace form
  the element needs was chosen by asking whether it was *written* as a bare string
  literal rather than whether it **folds** to one, which a constant concatenation
  does not satisfy.

- **A package ARRAY variable may be initialized by anything a local can be** —
  `var d = mk()` for a call's result, `var d = src` for another array, `var d = h.f`
  for a field, a method's result, and `var d = *p`, each with or without the type
  written. Only a literal was taken: an array has no assignable C value type, so the
  inferred form was refused as *cannot infer a type for the package variable* and the
  typed form as *a package array initializer must be an array literal*. C admits
  neither a call nor an array copy in a static initializer, so the storage stays a
  file-scope table — zeroed, which is the right starting value — and only the fill
  moves, to a step of the package initializer ordered against the variables it reads.
  A table may therefore be declared above the rows it copies. A call's array result
  filling a composite literal's element at package scope works for the same reason.

- **A call's array result may fill a composite literal's element** — `[]Row{mkRow()}`,
  `[2][2]int{mk(3), {1, 2}}`, `B{mk(4), 9}` and a method's result beside them. It is
  not a copy like the other deferred elements: the result travels through an out
  parameter, so the element *is* the storage the callee fills and the call writes
  through it. Still refused at PACKAGE scope, for a reason that is not this one — no
  package variable can be initialized from an array-returning call at all, `var d =
  mk()` included.

- **A method returning an ARRAY works on an array receiver** — `d := g.doubled()` for
  a `type Row [2]int`, a type returning its own type. It was *cannot infer a type for
  the declaration*, and the assigned form emitted C the host compiler rejects: an
  array result travels through an out parameter, and the lookup deciding whether the
  call is one asked for a C type an array receiver has not got. The same method on a
  struct receiver, and a plain function with an array result, always worked. Still
  refused on an *element* receiver, `pool[1].doubled()`, whose suffix is one step
  longer than that path takes.

- **A multi-result method may be called on an element** — `a, b := ps[1].two()`. The
  call shape a destructuring assignment recognises was a run of *selectors*, which
  admits `m.st.pop()` but not one element in, so this was *multiple assignment
  requires a single function call on the right-hand side* — of a call. It had nothing
  to do with arrays: a plain struct element was refused the same way, through a slice
  of them, with a field on the way to the index, with a pointer receiver and with
  arguments.

- **An ARRAY receiver may be launched on a cog** — `go pool[1].run()`, `go g.run()`,
  `go h.r.run()`. `go ws[i].run()` for a *struct* element was enabled deliberately —
  one cog per element is the worker-pool shape — and every array spelling was
  *unsupported receiver in a go statement*, the array itself included. A value
  receiver crosses as a copy, which is what a goroutine's receiver is, and a pointer
  receiver crosses as the address and writes the array the spawner named.

- **A method may be called on an array ELEMENT** — `pool[1].sum()` over a
  `[2]Row`, through a slice of them, with a pointer receiver, on one two indexes
  in, and on a copy or a `range` value of one. An array of a defined array type is
  resolved to its extents when the declaration is read — a `[2]Row` is a `[2][2]int`
  by then — so the element's *name*, the only thing carrying its method set, was gone
  before any walk reached the element. The shape now carries the element's name and
  how many extents it accounts for, which is what tells `[2]Row` (one index reaches a
  Row) from `[2][2]Row` (two do).

  `t := pool[1].sum()` types too. Inferring a declaration from a method call on an
  array receiver asked for a C type the receiver has not got, so it was *cannot infer
  a type* — even for `t := g.sum()` on the array itself, where the call was fine.

- **A deferred method on an ARRAY receiver no longer reads it at the return.**
  `defer g.show()` for a `type Row [3]int` g printed what g held at the *end* of the
  function, and so did `defer h.r.show()` for an array field. Go evaluates a deferred
  call's receiver where the `defer` stands, and this one was not captured at all: an
  array variable has no C type, and the capture asked for one. The slot now holds a
  copy — a `memcpy`, C assigning no array — and a *pointer* receiver goes on capturing
  the address, so it still sees later writes.

- **A SEND may name a channel two fields deep** — `p.in.cmd <- v`. The send's model
  carried one field and looked its name up on the *head's* type, so this was read as
  `p.cmd`: refused as *cannot send to non-channel* where the outer struct had no such
  field, and checked against the wrong element type where it had one of another type.
  The helper that found the field already reported that there had not been exactly
  one; both callers dropped that answer. The whole run is walked now, so the channel
  checked is the channel sent on — in an ordinary send and in a `select` clause
  alike — and a wrong value on a nested channel is reported against the right element
  type.

- **A channel declared from another no longer HANGS.** `var c chan int = ch` wrote the
  alias and then gave the variable a private cell one line later, so the receive on it
  waited on a channel nothing could send to — a program that built, ran and said
  nothing. `c := ch` and `c = ch` always aliased; the typed declaration was the one
  spelling of three that did not. The same bug sat one level down in a channel FIELD a
  declaration's literal fills, `var w W = W{ch}`, and two levels down through a nested
  literal.

  What still creates a channel is a declaration that fills nothing — `var ch chan int`,
  `var w W`, `W{}`, `W{In{}}` — which is where this language's channel-is-storage rule
  lives. A struct copied from another value keeps minting as before.

- **A conversion to an ARRAY type is an array value.** `c = Col(r)` emitted `c = r;`,
  which is not C, and `return Col(r)`, `Col(r) == c` and `[1]Col{Col(r)}` each refused
  it for want of an array they were looking straight at. Such a conversion changes
  nothing about the value — the typedef stands for the same storage, and Go admits one
  between array types only where the underlying types are identical — so it is an
  array wherever one may stand. The declaration form, `d := Col(r)`, always worked,
  the chain walk having seen through it for as long as it has had `arrayConvChain`,
  which is why the spelling reached for first was the one that was fine. The unnamed
  spelling `([2]int)(r)`, which the grammar admits only parenthesised, is read the
  same way.

- **An aggregate VARIABLE may be a composite literal's element** — `[2][2]int{a, b}`
  and `[][2]int{a, b}` for a table built from named rows, `B{a}` and `B{n: 5, xs: a}`
  for an array-typed field, and `[]A{g}` for a struct that itself holds an array. None
  of it compiled: an array element was *an element of a [2]int literal must itself be
  a literal*, a slice literal's and a struct field's emitted an initializer C rejects,
  and a struct holding an array was refused as an ABI boundary it is not.

  C copies no array in an initializer, and the target's C compiler copies no
  array-holding struct by assignment at all, so each such element is zeroed at its
  position and copied in after the declaration — the same `memcpy` every other copy of
  one takes. At file scope there is no "afterwards" in C, so the copies become steps of
  the package initializer and are ordered against the variables they read, which means
  a table may be declared above its rows. A literal written where nothing names storage
  for it — a channel send, an append — is refused rather than emitted: there is nowhere
  to copy into.

- **A defined array type's literal is right where nothing declares a variable for
  it.** `ch <- Row{1: 5}` sent ZEROS and `append(xs, Row{2: 7})` appended them —
  silently, with a working binary — while `r := Row{1: 5}` was right all along. Such a
  literal reads exactly like a struct's, a name and a brace, and in the positions that
  hoist nothing to point at it went through the struct walk, which knows nothing about
  indexes or rows; the declaration form goes to the array walk. A defined
  *multi-dimensional* type was refused there outright, its rows reported as *a
  type-elided composite literal element*. Both now take the array walk.

- **A multiple assignment may move ARRAYS** — `table[i], table[j] = table[j], table[i]`,
  the swap every sort of a table of rows is written with, and `p, q := m[0], m[2]`,
  a field target, a literal value and a mixed list beside it. Every value of a
  multiple assignment is bound to a temporary first, which is what makes `a, b = b, a`
  a swap; an array has no C value type to declare a temporary of, so the whole
  statement was refused with *cannot infer the type of a value in a multiple
  assignment*. The temporary is now the copy `b := a` already is, and each target
  takes its own.

- **`range` takes an array reached through a chain** — `for _, v := range b.xs` over an
  array-typed field, a nested one, `range pool[1]` for a row, and `range xs[1].xs` for
  a chain that starts at a slice. The operand walk took a bare name, a pointer to an
  array and that dereference written out, so every longer route fell past it to the
  integer case and was reported as *ranging an integer yields only the index* — of an
  array whose type the program had written down. Iterating a struct's own buffer is
  ordinary Go. The chain is evaluated once, as Go evaluates a range expression, and
  the array case is now decided by the operand's own shape rather than by the C type
  of what the chain starts from.

- **`range` over an array of ARRAYS yields rows** — `for _, row := range table` over a
  `[3][2]int`. The loop declared its value with the array's innermost element type,
  `int row = table[i]`, which no C compiler accepts. The same loop over a *slice* of
  rows was always right: a slice's element type is the row's typedef, and that is what
  tells the loop it has an array to copy, where an array container handed it the
  innermost element. Rows are copied, as Go copies a range value, in the `:=` form and
  the `=` one alike.

- **An array may be RETURNED through a chain** — `return cal.rows[i]` for a row of a
  table, `return h.f` for an array-typed field, a nested one, and `return *p`. The
  return knew an array variable and an array literal and nothing else, so every
  longer route was refused with *an array result must be returned as a variable or an
  array literal*, of a value whose type the program had written down. Returning a row
  is how a table is read. A source in the returning frame — a field of a local struct
  — is sound for the reason a returned literal is: the copy into the caller's storage
  *is* the return.

- **Two arrays compare correctly when either is reached through a chain.**
  `table[0] == table[1]` answered **false** for two identical rows, and so did
  `a.f == b.f`, `a.rows[0] == b.rows[0]` and `*p == v`. The operand reader took a bare
  variable and a literal and nothing else, and an operand it declined was not
  refused — the comparison fell through to C's own `==`, which asks whether the two
  decayed pointers are equal, never true for two distinct arrays. Neither compiler
  warns: comparing two pointers is an ordinary thing to write. The reader now takes
  every shape the copy does.

- **A whole ARRAY may be written through an index** — `m[1] = r` for a row of an
  array of arrays, `xs[1] = r` for an element of a slice of them, `arr[1].f = r` for
  an array-typed field of an element, `h.rows[1] = r` for an element of an
  array-typed field. Filling a table a row at a time did not compile: `m[1] = r` was
  refused with *a multi-dimensional array must be indexed in every dimension*, the
  chain targets with *only simple and field assignment targets are supported yet*,
  and the slice element emitted an assignment to an array type, which is not C. All
  four are the `memcpy` a whole array's assignment already was, reached through a
  target the plain-variable and whole-field shapes cannot name. `m[1]++` and
  `m[1] += r` are refused, as Go refuses them: no operator applies to an array.

- **An array reached through a chain may be ASSIGNED, not only declared** — `d = h.f`,
  `d = pool[1]`, `d = h.rows[1]`, and the same sources written into a field. The
  assignment knew two array sources, a variable and a dereferenced pointer to one, and
  anything longer fell past both to the ordinary path, which emitted `d = h.f;`. That
  is not C — gcc says *assignment to expression with array type* — and it is precisely
  the output the plain `a = b` shape is a `memcpy` to avoid. flexcc accepts it as an
  extension and copies, so the board was right and silent while the emitted C was not
  C; only a host build could see it. The declaration form, `x := h.f`, already copied
  these; the assignment now reads from the same resolution.

- **A package variable's initializer may name one declared BELOW it**, or in another
  file of the same package. Go's package block has no order — the variables are
  initialized in *dependency* order, whatever the source order — and `var b = a + 1`
  above `var a = 5` was refused with `cannot infer a type for the package variable b`.

  The ordering was already right, and had been since it was written: the initializers
  are topologically sorted into the synthesized package init, and that code's own
  comment gives `var a = b + 1` above `b` as the example. Only the TYPES were bound to
  source order — the pass that emits the variables typed each one as it arrived — so
  the example did not compile. Every kind of dependency now resolves, each of which is
  typed by a different path: a scalar chain, a slice of a later array (whose extents
  live in an environment of their own), the address of a later variable, a call's
  result, a field of a later struct value, and `len` of a later array.

- **A struct VALUE may stand as an array or slice literal's element**, `[]B{b}` — a
  variable, a call's result, a conversion, anything that is not itself a literal. It
  reached the C compiler, which reports `expected int but got _struct__B` about
  generated code the program never wrote: the target refuses a non-braced aggregate
  inside an *array* initializer, which is the same limit that already makes a slice
  element and a string element brace there. Its members braced is the spelling it
  takes, recursively — a nested struct, a string, a slice and an interface are each
  aggregates too, and an interface would otherwise have been zeroed rather than
  copied. A struct holding an array is still refused, with the reason it always gave.

- **Two DISTINCT struct types convert between each other** when their underlying types
  are identical — the same fields, in the same order, with the same names and types,
  which is Go's rule and needs neither type defined over the other. `Vec(p)` was
  refused as `cannot convert to Vec`: the conversion emitter compares
  *representations*, and two struct types are two C types however alike their fields.
  C has no cast between them, so the value is copied. A mismatch in any field's name,
  type or position is now refused in Go's own words — `cannot convert p (variable of
  struct type Point) to type Other` — where all of them, the legal one included, said
  the same unhelpful thing before.

- **Another package's STRING constant is usable**, `geo.Tag`. Every other constant
  type crossed the boundary — int, float, bool, rune, a wide one, a typed one — and a
  string did not: it is inlined at each use, a Go constant having no address, so
  unlike an integer constant (which emits a C `static const`, a name the ordinary
  cross-package read finds) there was no symbol to resolve to. The read fell through
  to the chain walker, whose base is a variable, and reported `geo is not a value with
  fields or elements` — of a package, about a constant that is there.

  It now reads, declares, assigns, passes, compares, `len`s, ranges and initializes a
  package variable. Indexing and slicing one, `geo.Tag[0]` and `geo.Tag[1:3]`, bind
  the value to a temporary first, since every chain walker reads its base by name.
  Concatenation folds: `const banner = geo.Tag + ": " + geo.Prompt` was reported as
  needing an allocation, of an expression both Go and this compiler evaluate at
  compile time when the operands are one package's.

- **An indexed literal's INDEX may be any constant expression**, which is what the
  spec has always said. `[]int{N + 1: 9}`, `[]int{1 << 2: 7}` and a qualified
  `[]int{geo.K: 9}` were each refused as `an array or slice literal index must be a
  non-negative integer constant` about one that is: the index was read as a single
  token — a literal or a bare name — rather than folded. A non-constant index is
  still refused, which is the folder answering no.

- **Any expression of POINTER type may become an interface value**, not just the
  three shapes that could. `var s Shape = New()` — the ordinary way a constructor is
  used — was refused with `an interface holds a pointer: write the address of a
  variable`, advice that does not apply to a value already pointing at one; so were a
  pointer field, `var s Shape = b.p`, and an element of an array of pointers. Only
  `&x`, `&T{...}` and a bare name of pointer type were recognised.

  As an ARGUMENT it was worse than a refusal: `take(New())` reached the C compiler,
  which reported `expected _struct__Shape but got pointer to _struct__Quad` about
  generated code the program never wrote. That is the one shape of failure this
  compiler is meant not to have.

  The lifetime rules already saw through the new shapes — a pointer field of a struct
  holding a local's address is refused at every sink, as it was before — and the
  package-storage counterpart of each is pinned beside it. A bare name that is *not* a
  pointer is still refused: that is the value form, and there is nowhere to copy to.

- **An INTERFACE-typed argument may cross to a cog**, `go show(&q)` for a
  `show(Shape)`. It had never worked in any spelling: a `go` statement's argument
  block holds each value as its *parameter's* type, and the raw pointer was stored in
  a slot of interface type, which the target's C compiler refused — `expected
  _struct__Shape but got pointer to _struct__Quad`, about generated code the program
  never wrote. Every other position wrapped the two words; this one alone did not. All
  five ways a value gets there work now, including an interface widened from a wider
  one, which needs a temporary declared where the cog can still see it.

- **Another package's INTERFACE is usable**, which it was not: `var s geo.Shape = &pq`
  was refused as `cannot use &pq (an address) as geo.Shape value`, and so were the
  assignment, the argument and the return. Nor was there a way back out — an assertion
  or a type-switch case naming a qualified concrete type, `s.(*geo.Quad)` and
  `case *geo.Quad:`, was reported as `geo (package name) is not a type` or as naming
  no type at all. A case may also name an imported interface, `case geo.Sizer:`.

  One cause under all of it: every method-set question is asked BY NAME, and the name
  asked with was the bare `Shape`, which resolves in the asking package and finds
  nothing. So `geo.Shape` was not recognised as an interface anywhere, and the
  pointer-ness rule — which an interface is exempt from, since what satisfies it is a
  method set — spoke in its place. Four things had to carry the qualifier with the
  name: the type lookup, a package-level variable's record of its own type (a LOCAL
  of an imported type already carried it, which is what made the gap look like
  something else), the type a case or an assertion names, and the variable either of
  those binds — `q, ok := s.(*geo.Quad)` gives q geo's type, so what is read off q is
  checked rather than left to the C compiler.

  Diagnostics improve with it: a refusal now names the type as the program spelled it,
  `geo.Plain does not implement geo.Shape`, where it used to say `Plain`.

- **A QUALIFIED conversion compiles**, `geo.Celsius(20)` for a type of an imported
  package — and it was refused for *every* kind of target, not just an interface: a
  defined scalar, an array, a slice, a struct and an interface all reported `cannot
  infer a type for the declaration`. The resolver behind a conversion takes one
  identifier, so a qualified name reached the call machinery instead, which found the
  import qualifier and went looking for a function of that name. It is a conversion in
  every position now, including a method called on the result,
  `geo.Celsius(24).Double()`.

  Two mechanisms had to learn the spelling along with it, or accepting the program
  would have been worse than refusing it. A conversion to a defined ARRAY type is the
  operand itself, the two having one representation, and the recogniser that says so
  reads the unqualified shape — without it `geo.Row(a)` copies one element and leaves
  the rest garbage. And the lifetime rules: `geo.L(a[:])` and `geo.Shape(&q)` over a
  local reach that local exactly as the plain spellings do, so a conversion they did
  not follow would launder the reference past every sink.

- **A conversion to an INTERFACE type compiles**, in every position an expression
  stands in: `s := Shape(&q)`, a long declaration, an assignment, an argument, a
  return, a struct literal's field, a slice or array literal's element, a channel
  send, a `go` argument, the receiver of a method call, the operand of a type
  assertion and of a type switch. Every one was refused with `cannot convert to
  Shape` — the conversion emitter compares REPRESENTATIONS, and a `Quad*` operand is
  not the two-word interface struct, so a program had to bind a variable of the
  interface type and use that. It now builds the same pair the assignment builds.

  `any(x)` is the same conversion under the name the universe holds rather than a
  declaration, and is the ordinary way a program says "as an interface"; it was
  refused with a different diagnostic (`cannot infer a type`) for the same reason.
  Interface-to-interface works too, `Shape(n)` for an `n` whose method set covers
  Shape's.

  It gets past none of the rules the assignment obeys. `Shape(q)` for a value `q` is
  refused as `var s Shape = q` is, and says so in those words rather than reporting a
  failed conversion. A conversion of a LOCAL's address reaches that local exactly as
  the address does, so it may no more be stored, returned, sent, launched or passed
  to a function that keeps it — that route is closed at all five sinks, and would
  otherwise have opened one, the shape being what `L(a[:])` had for slices.

### Behaviour changes

- **A reference may no longer outlive the BLOCK of the variable it points at.** Go
  gives a `for` statement's variable a fresh instance per *iteration* (since 1.22),
  and a variable declared in a loop *body* has been a fresh one per iteration since
  Go 1.0. OctoGo gave each declaration one cell and reused it, so `ps[i] = &i` and
  `x := i * 10; ps[i] = &x` both printed the last value where Go prints one per
  iteration — silently, and the second of those diverged from *every* version of Go,
  not just from 1.22.

  Keeping such a reference past the iteration needs one instance per iteration, of a
  count not known until the loop runs. That is an allocation, so it is refused, as
  `new` and a map are refused. Where a reference does *not* outlive the iteration,
  one cell and a fresh one are indistinguishable — which is why Go's own compiler
  keeps one until it escapes — so every program that still compiles means what Go
  means. `f(&i)` for a callee that keeps nothing is unaffected, and so is any
  reference stored where it dies with, or before, what it points at.

  The rule covers the same doors the function-level one does: a store, a store
  through a pointer parameter, and a store into a method's receiver. Choosing to
  reject rather than document the divergence keeps the differential-against-Go tests
  meaningful, which is how much of this compiler's behaviour is verified.

- **A reference to a local may no longer be laundered through an INTERFACE.** Which
  function a call through an interface reaches is the vtable's answer at run time, so
  there is no callee to look an escape summary up by — and none was asked for, which
  made an interface the way around every lifetime rule a direct call obeys. All three
  leaks went through it silently: `s.take(a[:])` for an implementation that stores
  its parameter in a package variable, in its receiver, or hands it to a cog, each
  leaving a header over a dead frame where the same call written directly on the
  concrete type had been refused since those rules landed.

  The summaries of **every** implementation are unioned instead. That is
  conservative in a way worth stating: an implementation that keeps the argument
  constrains the calls through the interface even where the value assigned is one
  that does not, so a program that was correct — and compiled — may now be refused.
  Proving which implementation runs is devirtualization, which does not exist yet;
  until it does, the choice is between refusing some correct programs and accepting
  some dangling ones. An interface whose implementations keep nothing is unaffected,
  and storage that outlives the call may be passed to any of them.

  `leakRecv` becomes a global leak here for the same reason: a store into the
  receiver is a leak to whoever owns it, and an interface value is a pointer to
  storage the call site cannot name.

- **A reference to a local may no longer be stored through a POINTER PARAMETER.**
  `func fill(h *H, d []int) { h.d = d }` is the ordinary setter written as a plain
  function rather than a method, and `fill(&g, a[:])` for a package-level `g` left a
  header over a dead frame — accepted, where the same store through a *receiver* had
  been refused since the summary learned about receivers. The rule was never about
  methods: a receiver is just the parameter a method call writes to the left of the
  dot, and `leakRecv` could say "into the receiver" while nothing could say "into
  parameter 2".

  The summary now carries which parameters each parameter is stored through, so the
  call site asks after the lifetime of the argument at *that* position — which is
  what makes `fill(&local, a[:])` still compile, the struct and the backing dying
  together. Being per-parameter also keeps a callee that stores one argument and
  merely measures another from refusing the second: only the stored position is
  asked about. The requirement propagates through chains of plain functions, and a
  callee that names the package variable itself settles the question there rather
  than passing it on.

  A METHOD called on a `*T` parameter is followed too, which it was not: naming it
  needs the receiver's type, and reading the type *as written* asks nothing of the C
  type an array parameter has not got — the reason the case was skipped.

  A written-out dereference counts as a store through the parameter, `func set(p
  *[]int, d []int) { *p = d }`, which is the only shape a pointer to a slice has.

- **A reference to a local may no longer be laundered through a METHOD.** The
  crossing summary ties a caller's parameter to a callee's by recording a call edge,
  and it recorded none for a method — it resolves a callee by *name*, and a method has
  none until the receiver's type is known. So one method delegating to another carried
  no requirement at all: `func (t *H) set(d []int) { t.inner(d) }` where `inner`
  stores its parameter in the receiver was accepted for a package-level receiver, and
  left a header over a dead frame. A plain function calling a method on a package
  variable was the same.

  The edge carries *whose* receiver it is, because that is what says whether the
  callee's store is a leak here too: delegating to the caller's own receiver asks the
  question again one call further out, while a package-level receiver answers it
  outright. A local receiver stays accepted — the storage dies with the call — and so
  do a delegation that only reads, a scalar argument, and a recursive method.

  A method called on a LOCAL still records no edge — naming it means finding the
  declaration in the body. The direct store through such a receiver is checked at the
  call site regardless. (A method on a *parameter* is followed; see the entry above.)


- **A method may no longer store a frame reference into its RECEIVER.** `h.set(a[:])`
  for a package-level `h` and a `func (t *H) set(d []int) { t.d = d }` left a header
  over a dead frame in storage that outlives the call. The plain-function form of the
  same store, and a method storing into a *global*, were both caught already; only the
  receiver was not, its lifetime being something the callee cannot see.

  So the call site decides. A receiver declared in the calling function keeps
  compiling — the two die together, which is what a scratch struct is — while one
  reached through a *parameter* does not: its own storage is the frame's, but what it
  points at is the caller's, so the same store is the same leak one level up. A method
  that only reads its parameter, a scalar argument, and a value receiver are all
  untouched.

  Still not followed: a method that passes the parameter to a *second* method which
  stores it. The analysis records no call edge through a method at all, which it
  documents as erring towards accepting.


- **An APPEND may no longer put a frame reference into a longer-lived slice.**
  `gs = append(gs, a[:])` for a local `a` and a package-backed `gs` left a header over
  a dead frame in storage that survives it — through a door that writes no variable
  name, so the store check never saw it. The same for the address of a local, and for
  a struct holding either.

  Appending into a backing that is *itself* this frame's is unaffected: the two die
  together, which is what a scratch list built in a function is. So is a spread of
  scalars, `append(gs, a[:]...)` — it copies the source's elements, not the header
  naming them, so nothing of the local survives the call.


- **A frame reference nested in a composite LITERAL no longer escapes.** `Box{a[:]}`
  is a struct holding a slice of this frame, so handing the struct on hands the slice
  on — and every door let it through: stored in a package variable, returned, sent on
  a channel, launched on a cog, and passed to a function that keeps it. All five were
  silent, and binding the value to a variable first was refused all along, the
  declaration being the only path that looked inside the literal. The workaround was
  checked and the plain spelling was not.

  Only a literal is descended into: an expression may *mention* a frame reference
  without the value carrying it out — `P{len(a[:]), 2}` is two ints — and a literal
  over package-level or caller-supplied storage carries nothing that dies.


- **A Builder's `String()` view may no longer outlive its backing array.** A `Builder`
  is a pointer into a backing array the caller owns, and `String()` hands that storage
  out as a string — so one built over a *local* array was a view of a dead frame the
  moment the function returned. `g = sb.String()` stored it in a package variable and
  printed the frame's leftovers; returning it was the same. Both are now refused, with
  the message naming the local whose storage it is, exactly as a slice of a local is.

  What is counted is the *provenance*, not the type: a string that came out of a
  Builder built over frame storage. An ordinary string — a field, a constant, a
  method's result — carries no reference and is untouched, and a Builder over a
  package-level or caller-supplied backing hands its view out freely, which is the
  idiom the type exists for.

- **An array or slice literal's ELEMENTS are type-checked.** `[]int{1, "x"}` reached
  the C compiler, and `[]Col{r}` for another defined array type — or `[]B{a}` for
  another defined struct — was accepted outright. Such a literal's values were left
  to the emitter, which knows the bound and counts them and knows nothing about their
  types. The element type is written down in every one of these, so an element now
  answers the three questions a struct literal's field answers: does it satisfy an
  interface element, is it the same defined type, and does its value fit. A
  type-elided element (`[]P{{1, 2}}`) names no type and is not this check's.

- **A defined SLICE or CHANNEL type is a type of its own too.** `type L []int` and
  `type M []int` were interchangeable, and so were two defined channel types. It is
  the array rule one step further: the identity check is gated on a `Kind`, and a
  slice, a channel and a pointer have none any more than a struct or an array does —
  the note saying they were "left to the checks that own them" described checks that
  do not exist. Like is now compared against like, by name.

  An INTERFACE is deliberately excluded: Go assigns to one by *method set*, not by
  name, so two differently named interfaces stay assignable wherever their methods
  line up. Two defined POINTER types are still interchangeable, and a defined `func`
  type is unchecked — the type model carries no node for one.

- **A defined ARRAY type is a type of its own.** `type Row [2]int` and
  `type Col [2]int` were interchangeable — `c = r`, `var d Col = r`, `takeCol(r)`,
  `return r`, `cols <- r` and `r == c` all compiled, where Go wants a conversion. An
  array carries no `Kind`, and the identity check is gated on one, so it could never
  see an array at all: the same blind spot a defined type over a *struct* sat in
  until it was admitted for having no Kind either. `Col(r)` is how the two meet and
  is unaffected.

  A defined array type and the unnamed spelling of the same shape stay assignable
  both ways, as they are in Go — one of the two is not a defined type, so there is
  nothing to tell apart. The emitter's own array checks compare by shape and never by
  name, and keep doing so; identity is the checker's question.

- **An ARGUMENT of the wrong array shape no longer builds.** `use(s)` passed a
  `[3]int` to a `[2]int` parameter, and a `[2]uint8` to a `[2]int` one. Go rejects
  both, and an array parameter is a pointer the callee `memcpy`s the *parameter's* own
  size out of, so a shorter argument was read past the end of. It was the last
  position an array flows into that carried no check: the extents cannot be read off
  the parameter's C type — every `[N]int` parameter is the same `int*` — so they are
  now recorded beside it. A method's parameter is checked too.

- **A copy between arrays of different SHAPE no longer builds.** `var d [2]int; d = s`
  for a `[3]int` `s` compiled, printed the first two elements and said nothing; so did
  `b.g = a.f`, `pool[1] = [3]int{1, 2, 3}` and `var d [2]int = s`. Go rejects every one
  — *cannot use s (variable of type [3]int) as [2]int value in assignment* — and the
  copy here is sized by the DESTINATION, so what got through read past the end of a
  shorter source or dropped what did not fit. The element type counts as much as the
  extents: a `[2]uint8` into a `[2]int` was two bytes read as two ints.

  The comparison is by shape and never by name, so a defined array type and the
  unnamed spelling of it stay assignable in both directions, as they are in Go. What
  is checked is what the program wrote down: a source whose shape cannot be read off
  the expression is passed, not refused. Two *different* defined names of one shape
  are still accepted, where Go refuses them — that is the named-type distinctness
  question, and it is open.

- **A cycle among the package variables' initializers no longer builds.** `var a int =
  b` beside `var b int = a` compiled and left both zero, each having read the other
  before it was written; Go refuses such a program, there being no order in which
  every initializer sees the value it reads. The ordering pass had detected the cycle
  all along and said nothing — `specs.go` recorded that as a known gap. The diagnostic
  is Go's, naming the variables and the edges between them so that which pair closes
  the ring is not left to be worked out, and `var a int = a + 1` is reported as the
  same rule at its shortest.

  Two things that are *not* cycles came with it. What an initializer depends on is
  read off the identifiers it mentions, and a member name is not one of them: `var a =
  s.a` and `var mine = v.mine()` were each reported as referring to themselves the
  moment that list started deciding this rather than only ordering. A keyed literal's
  key is not a reference either. The dependency on the variable being *selected from*
  is unaffected, which is what keeps the ordering right.

- **A diagnostic raised while the C file is assembled is no longer dropped.** The
  error check ran before the function bodies and nothing re-checked afterwards, so
  anything the package initializer, the goroutine trampolines or the include
  computation reported was computed and discarded, and the program compiled as though
  nothing had been said. It is what made the cycle report above visible.

- **A program that let the address of a composite literal outlive its frame no longer
  builds.** `&T{...}` has no variable, so the literal is given a temporary of the
  enclosing function and the address is that temporary's — `Quad* p = &(Quad){7, 8};`,
  whose lifetime in C is the enclosing block. Binding it to a variable first was
  refused already; every *direct* form was accepted, and all five were wrong: stored
  in a package variable, returned, sent, launched on a cog, and passed to a function
  that keeps it. `specs.go` has said this was refused since interfaces shipped.

  It is not a rule about interfaces. `func mk() *Quad { return &Quad{1, 2} }` returns
  the same dead temporary and is refused with them. What `&T{...}` is *for* — a fresh
  value in an interface, used in the frame that made it, or handed to a function that
  returns first — is unaffected. The fix the diagnostic asks for is to assign the
  value to a package variable and use that.

### Diagnostics

- **Three lifetime refusals told the reader to move a backing array that was not
  there.** A struct's address refused at a send, a return or a `go` was answered with
  "declare the backing array at package scope", which is right for a slice and
  nothing else. Each sink phrased its own advice while the one that asked the value
  what it needed — the interprocedural argument check — got it right. All four ask
  now, so the refusal names the variable: "declare q at package scope".

## v0.28.0

References that outlive their frame, and arrays reached by a longer route.

Six ways a program could hand out a reference to storage that had already gone are
closed. Each was a build that succeeded and then read a dead frame: a slice literal of
a defined type, stored or returned or sent or launched; the same header laundered
through a conversion; a struct built by a long `var` declaration rather than the short
one; reading a reference back *out* of a struct that held one; slicing a local through
a chain instead of by its bare name; and a callback kept in a struct field, whose call
consulted no leak summaries at all. The lifetime rules were right — what was missing
was the set of spellings they had to recognise. The fix each diagnostic asks for is the
one it always was: declare the backing array at package scope.

The second arc is the array reached by a longer route than its own name. A method on a
defined array type did not compile at all, an array carrying no C type to hang one on;
a copy or an address through a field, a nested field or an element fell through to type
inference, which types no array operand and reported that it could not infer a type the
program had written down; a parameter of rank above one was refused as unsupported. The
shape's name travels with its extents now, so a defined array type is spelled by its
own name in the emitted C and carries its method set through a field.

A defined type over a struct is a type of its own, in the six positions where it was
interchangeable with the struct it was defined over. Nothing was miscompiled by that —
the two have one representation — but a program built here that Go refuses, which is
the contract backwards.

### Language

- **The address of an array reached through a chain can be bound to a variable** —
  `p := &h.f` for an array-typed struct field, a nested one, and `r := &pool[1]` for
  an element. `p := &a` over a bare array variable worked, and handing `&h.f` to a
  *parameter* worked, since the parameter's type says what it is; a declaration has
  only the type inference to go on and that read a bare name only. The pointer
  aliases, as Go's does — writing through it is seen in the field, and a
  pointer-receiver method through it writes there too. The lifetime rules are
  unaffected: the address of a field of a *local* struct still may not outlive it.

- **A method may be called on an array-typed struct field** — `h.f.sum()` and
  `h.f.set(0, 7)` for a `type Row [2]int` field, at any depth, through a pointer to
  the struct, and in the multiple-assignment form `a, b := h.f.pair()`. The same
  method on a *struct*-typed field has always worked in both positions, so this was
  the array case alone: the chain walk reaches an array with no C value type, and the
  dispatch keyed on that type being non-empty. The field's defined name travels with
  its extents now. A value receiver is still a copy, so a method writing to one
  leaves the field alone.

  An array **element** receiver, `pool[1].m()` over a `[2]Row`, is still refused: an
  array of a defined array type is resolved to its extents, so nothing of the element
  type's name survives to hang a method on.

- **An array reached through a chain can be copied** — `x := h.f` for an array-typed
  struct field, a nested one, `z := pool[1]` for an element of an array of arrays,
  and `w := h.rows[1]`. `b := a` over a whole array variable has always been the
  memcpy Go's copy is; every longer route fell through to the type inference instead,
  which types no array operand, and reported "cannot infer a type" of a field whose
  type the program had written down. A copy through a *field* keeps the type's name,
  so it carries its method set; one through an *index* cannot, an array of a defined
  array type being flattened to its extents.

- **A defined ARRAY type carries methods**, in both receiver forms. `type Row [2]int`
  with `func (r Row) sum() int` and `func (r *Row) set(i, v int)` did not compile at
  all: an array carries no C type — its extents live in a map of their own and
  nowhere else — so nothing said which type a variable of one was, and therefore
  which methods it had. `g.set(0, 3)` was read as a package qualification and
  reported as `unknown package "g"`. The shape's name now travels with its extents.

  A **value receiver is a copy**, as Go's is. It travels as a pointer, since a
  parameter of array type corrupts unrelated code on this target, and the method
  copies from it on entry — so writing to a value receiver leaves the caller's array
  alone. Verified on hardware, which is where that ABI defect shows.

  A defined array type is also spelled by its **own name** in the emitted C now
  rather than by a minted one, which is what let the method be found: it used to emit
  as `ogo_arr_2_int_set`, a name no call site would look for, beside a duplicate
  typedef of the same `int[2]`.

  The receiver may be a variable, a package-level one, a pointer to either, or the
  written-out `(&v).m()`. Reaching one through a struct **field** or an array
  **element** is not wired up yet.

- **A function parameter may be a multi-dimensional array**, `func f(m [3][2]int)`
  or `[3]R` over a `type R [2]int`, of any rank. The one-dimensional form has always
  worked — an array parameter travels as a pointer and the callee copies from it,
  since a parameter of array type miscompiles on this target — and the helper that
  recognised one returned false for any rank above 1, so the rest were refused as an
  unsupported type. It is still the value Go passes: the pointer is how it crosses,
  not what it means.

- **An element of an array of a DEFINED slice type can be indexed.** `named[0][0]`
  over a `[2]L` for `type L []int` was refused where the unnamed `[2][]int` spelling
  of the same thing indexed twice without trouble — the chain walker recognised a
  slice element by the header's own C name and not through a definition. Every other
  way of reaching it worked (`len`, a copy into a local, a range), which is what made
  the shape look supported.

- **A composite literal whose elements are SLICES compiles** — `[3][]int{r0[:],
  r1[:], r2[:]}`, a table of rows, which is how a program states one without a heap.
  Each element rendered as a compound literal, `(ogo_slice_int){r0, 3, 3}`, and the
  target's C compiler refuses one inside an *array* initializer while accepting it
  inside a *struct* initializer — so the shape did not compile at all, though the
  host compiler takes the identical C. The array path now braces the header, as it
  already did for a string element. A slice *literal* element is unaffected: it has
  to hoist a backing array first, which is the opposite spelling.

- **A select's SEND clause carries an interface or an array element.** The blocking
  `ch <- v` handled both and the clause handled neither: an interface element took
  the raw pointer where the two words go, and an array element was bound with
  `elem tmp = arr`, which is not C. Past that, the offer helper the clause uses took
  a parameter of array type — which miscompiles on this target — and stored it with
  an assignment C does not have, where the blocking send crosses by pointer and
  memcpys. It now takes its value exactly as the blocking send does.

- **A callback held in a struct field is checked like any other callee.** `b.run =
  keep; b.run(&x)` was accepted where `f := keep; f(&x)` was refused, and stored a
  dangling pointer in a package variable — measured, `4242` read back as `32767`. The
  binder that records which function a value holds tracked variables only, so a call
  through a field consulted *no* leak summaries at all. A struct of callbacks is an
  ordinary firmware shape, so this is not a corner. Both spellings that bind one are
  covered — `b.run = keep` and `b := B{run: keep}` — and the call is judged by the
  bound function's own summaries, so a callback that does not leak still takes a
  local's address freely.

- **A long `var` declaration records what the short one always did.** `var b B =
  B{a[:]}` marked nothing where `b := B{a[:]}` marked `b` as holding a reference into
  the frame, so storing that struct, or reading the field back out of it, escaped —
  the same declaration one spelling apart.

- **Slicing a local through a chain is refused, as slicing it directly always was.**
  Only the bare `a[:]` shape was recognised as viewing this frame, so `b.arr[:]` for
  a local struct with an array field — the ordinary way a program carries a buffer
  around — was a dangling slice at every sink, and so were `m[0][:]`, `bs[1].arr[:]`,
  `p[:]` and `(*p)[:]`.

  What is being sliced decides. An array's storage is wherever the array lives, so
  slicing one reached from a local root views this frame and one reached from a
  package root does not; a slice has a backing of its own and its mark says where;
  and through a pointer the chain reaches what the pointer points at, which only the
  holder mark knows — reading the pointer's own storage instead would refuse
  `p := &pkgArray`, which is fine. The package-scope counterpart of every refused
  shape is pinned beside it.

- **Reading a reference out of a struct that holds one is refused.** `b.d = a[:]`
  for a local array `a` marks `b` as holding a reference into this frame, and handing
  `b` on was already refused — but `g = b.d` is the same slice header by another
  spelling, and it was accepted at every sink. Measured: a package variable filled
  with `11 22 33 44` read back as garbage the moment the filling function returned,
  where Go prints the four values. A pointer field, `g = b.p` after `b.p = &x`, went
  the same way.

  The mark is per *variable* and there is no per-field provenance, so the field's own
  type is what tells a reference-carrying read from a harmless one: a slice, a
  pointer or a struct field of a marked holder is refused, and a scalar field is not.
  A struct given package-level storage is not marked at all, so reading its slice
  field out stays free.

  **Every other way of getting the value out went with it**, since they were all the
  same gap: an element of a marked array (`xs[0]`, `bs[1].d`), a copy into a local
  (`s := b.d`), and the value a `range` binds. And one that made all of them
  bypassable — **a mark applied inside a nested block did not survive the block**, so
  `if c { v = s }` followed by `g = v` was accepted. The marks are monotone, so a
  scope now merges them back rather than restoring wholesale; a name the block
  *declared* still takes its mark with it, so a later sibling block's same-named
  variable inherits nothing. Eight ways of obtaining the reference against all four
  sinks — thirty-two programs — are refused.

- **A DEFINED slice type no longer bypasses the lifetime rules.** This is the
  serious one. `type L []int` and then `g = L{1, 2}` into a package variable was
  accepted — where the identical `g = []int{1, 2}` was refused — and stored a slice
  header over the *calling function's dead frame*. The program built and read back
  whatever had since been written there: measured, `11 22 33` came back as
  `32765 3 2`. Go accepts that program and prints `11 22 33`, so this was the one
  thing the no-heap rules exist to prevent, happening silently.

  **Every sink had the hole** — returning, storing in a package variable, storing in
  a package variable's field, sending on a channel, and handing to a goroutine. The
  check read the literal's type with the predicate that matches the written `[]T`
  shape and not a name defined over one, so a single word of syntax turned the whole
  escape analysis off.

  A **conversion** laundered it the same way and is closed with it: `g = L(a[:])`
  for a local `a` passed where the plain `g = a[:]` was refused. A conversion to a
  slice type is not a call — it renames the same header over the same storage — so
  the operand is followed through it, and the diagnostic still names the origin
  (`local a`) rather than the conversion. Converting a slice over *package* storage
  is unaffected, as is a conversion to a scalar or an array, which yields a value
  that refers to nothing.

  So did **declaring a local of a defined slice type**: `var s L = a[:]` recorded
  nothing about where the backing lived, because the branch that records it takes
  the written `[]T` spelling and a defined type is a name. All three initializers
  that reach it — an existing header, a conversion of one, and a literal — are now
  recorded. Every combination of the four ways to make a frame-backed slice of a
  defined type against the four sinks is refused, and slices over package storage
  still pass freely.

- **A method call may be written out through an address**, `(&v).m(args)`, the
  mirror of the `(*p).x` form already supported. It means what `v.m(args)` means — a
  value receiver copies what the pointer points at, a pointer receiver is what
  `v.m()` already takes the address for — so the shorthand is the lowering. It is
  admitted around a CALL only: `(&v)[i]` is not `v[i]` (the first is illegal Go for a
  slice `v`), so that stays refused rather than quietly accepted.

- **A method on a struct FIELD or a call RESULT of a defined type is found.**
  `g.t.F()` and `mk().F()` for a `type Celsius int` with a method `F` were refused
  with `type int has no method F` — naming a type the program never wrote, of a
  method it had declared. Reaching the same value through a local (`v := g.t;
  v.F()`) always worked, and so did a method on an element, which is what made the
  shape look supported.

  The two checks involved had only the field's or the result's **Kind** to go on,
  and a Kind is precisely what a defined type resolves *through*: `typeKind` follows
  the definition down to `int` on purpose, since that is what makes every Kind-keyed
  check work for a defined type at all. The cost is that the name — which is what
  carries the method set — is gone by then. Both checks now look the method up by
  name before reporting, and name the written type when they do report, so a
  genuinely missing member reads `type Celsius has no method nope`.

- **An array literal stands in an `append` and a channel send.** `append(rows,
  [2]int{1, 2})` and `ch <- [3]int{1, 2, 3}` were refused, on the ground that C has
  no array value for the literal to become. It has one — the compound literal
  `(T){a, b}` — and a literal of a DEFINED array type had been emitting exactly that
  in both positions all along, so `ch <- Row{1, 2}` compiled where the identical
  value written `ch <- [2]int{1, 2}` did not. The unnamed spelling was refused for
  having no name to write, and the compiler already mints that name for every
  `[][2]int` element. A call RETURNING an array still has to be bound to a variable
  first, which is what its own diagnostic asks for.

- **An array whose element is an array can be sliced.** `m[:]` over a `[4][2]int` is
  a `[][2]int` over that storage, and so is `d[1][:]` one rank further in. `[][2]int`
  was already a type a literal could make, so what was missing was the language's own
  idiom for a slice with no heap — a package-scope backing array, sliced where it is
  used — for this one element type. Every base a slice expression takes now reaches
  it: a variable, a pointer to an array, a struct field, and a row through a chain.

  The refusal rested on a belief that had stopped being true — that a slice of arrays
  has no element type C can name. It has one: the same generated typedef a `[][2]int`
  literal has always been built over, `typedef int ogo_arr_2_int[2]`, so a slice made
  by slicing and one made by a literal are one C type and interchange.

  The struct-field base was the one that did not refuse, and was worse for it. It
  named the header after the INNERMOST type, building an `ogo_slice_int` over an
  `int(*)[2]`; flexcc only warned about the pointer, so `ogo build` succeeded and
  every later use of the result was refused for a reason that named C rather than the
  program.

- **A defined type over a STRUCT is a distinct type**, which it was not. `type Loc Pt`
  over a struct `Pt` now passes, assigns, returns, is sent and compares only where a
  `Loc` is wanted, and a conversion is what carries a value across in either
  direction — `Pt(l)`, `Loc(p)` — copying the fields as any struct value does. Two
  names over one shape are two types whether or not either was defined over the
  other, so a same-shaped `Other` is refused where a `Pt` is wanted for the same
  reason.

  The rule had held for an `int` and a `string` base since the day it shipped and
  reached structs nowhere, for a reason worth naming: the check was keyed on a Kind,
  and a struct HAS no Kind — the enum names the predeclared scalars and nothing else
  — so the scalar gate could never admit one, and the exclusion read as deliberate
  because it was written down as such. Identity is decided by NAME, which is in hand
  without a Kind, so the struct case is now answered ahead of that gate rather than
  behind it. Both sides must be structs, which leaves an interface target to the
  check that owns it and a mismatch of shape to the checks that own that.

- **A variadic of INTERFACES did not compile.** `total(&gq, &gr)` for
  `total(ss ...Shape)` reached the C backend, which refused it. A concrete value
  handed to an interface parameter is wrapped where it stands — the two words the
  parameter is, the value's address and the table for that pair — and the pack did
  not wrap, storing the raw pointer where the two words go. Each element carries its
  own table, so two concrete types in one call dispatch to their own methods; an
  interface variable passed in is already the two words and is copied as it stands.

- **A variadic argument that was a VARIABLE of an aggregate type did not compile** —
  a string, a struct or a slice alike. `count(s, "ccc")` for a `string` `s`,
  `firsts(p, P{8})` for a struct `p`, and `lens(xs, pool[:])` for a slice `xs` each
  reached the C backend and drew a diagnostic about C the program never wrote. The
  pack a call builds was written as an array INITIALIZER, and the target's compiler
  takes an aggregate there only when it is itself braced — which covers a composite
  literal and nothing else. The values are assigned into the array one at a time
  now, a form it accepts for every element type.

  The earlier fix in this area (v0.26.0) reached the literal spelling only, and its
  own note says why it was missed: the tests varied the variadic's SHAPE — pack,
  spread, empty, a fixed parameter before it, a method — and used a literal for
  every argument of every one of them. Varying the shape and not the spelling is
  what left the second half standing.

- **A variadic argument is checked as the fixed parameter it stands for.** The
  checks a Kind cannot express — a defined type against its base, a value where a
  pointer is wanted and the reverse, and whether a concrete type satisfies an
  interface element — run ahead of the known-Kind guard for a fixed parameter and
  ran behind it for a variadic one. So `sum(c)` for `sum(ns ...int)` with a
  `Celsius` `c` was accepted, and the whole class of element types with no Kind —
  a struct, an interface, a pointer — was left to the C compiler, which then
  complained about C the program never wrote.

### Documentation

- **The README named one of the two shapes a method value is refused in.** It said
  "a method value whose receiver is not a package-level variable", which is one rule;
  the other is that a **value receiver** is refused wherever the receiver lives, a
  package variable included — Go copies the receiver where the value is taken and
  there is nowhere to copy to. `specs.go` had both, with their reasons, all along.
  Found by testing each claim in the gap list rather than reading it: every other
  claim there still holds. The distinguishing case, a value-receiver method value on
  a package variable, is now pinned by a spec test — which is what was missing for
  the wording to drift in the first place.

- **The lifetime paragraph now says that reading a reference back OUT of a struct
  counts too** — `b.d` is the same slice header `b` carries. That became true this
  release, and a reader meeting the refusal would not have found it described.

### Behaviour changes

- **A program that handed a local's address to a callback held in a struct field no
  longer builds**, if that callback stores it somewhere outliving the frame. It built
  before and read a dead frame afterwards. The same applies to a struct built by a
  long `var` declaration from a local's storage.

- **A program that let a chained slice of a local escape no longer builds.**
  `b.arr[:]` for a local `b`, and the rest of the chained forms, built before and
  were wrong. Move the backing to package scope, which is what the diagnostic asks
  for. The same expressions over package storage are unaffected, and slicing a local
  buffer to work on it inside the function still compiles — that is what the rule is
  careful to keep legal.

- **A program that read a reference out of a frame-holding struct no longer builds.**
  `g = b.d` after `b.d = a[:]` built before and was wrong. If a program relied on it,
  it was reading whatever later occupied the frame. The fix the diagnostic asks for is
  the same one: declare the backing array at package scope. A field of a struct that
  was never given frame storage is unaffected, and so is a scalar field of one that
  was.

- **A program that stored, returned, sent or launched a slice literal of a DEFINED
  type no longer builds.** It built before and was wrong: the header pointed into a
  frame that had already returned. If a program relied on it, it was reading whatever
  later occupied that memory, and the fix is the one the diagnostic asks for —
  declare the backing array at package scope and slice it. The same applies to a
  conversion of a frame-backed slice, `L(a[:])`, which was the other way past the
  check.

- **A program that used a defined struct type and the struct it was defined over
  interchangeably no longer builds.** Six positions changed at once — argument,
  variable declaration, return, assignment, send and `==` — in both directions.
  Nothing was miscompiled by the old behaviour, the two having one representation;
  what it admitted was a program that built here and not under Go, which is the
  contract in reverse. Write the conversion Go wants: `takePt(Pt(l))`.

- **A type ALIAS over a struct is refused along with it.** `type A = B` still parses
  as a definition — the `=` is read and discarded — so an alias over a struct is now
  two types where Go has one, exactly as an alias over an `int` already was. That is
  a false alarm rather than a wrong answer, which is the safe direction of a gap the
  README already documents; the definition form is what to write until aliases are
  implemented.

- **A variadic call that passed the wrong element type no longer builds.** A defined
  type where its base is the element, a value where a pointer element is wanted or
  the reverse, and a concrete type that does not satisfy an interface element are
  each reported now instead of being passed on — the first two silently, the rest as
  a flexcc diagnostic naming C. Go refuses all of them.

### Testing

- **The ELEMENT axis of a variadic is swept in one program now.** Twice a defect
  there has been a spelling the table never varied: first every element was an
  `int`, which has nothing to brace, and then every argument was a literal, which is
  the one thing an array initializer's braces take. Both looked whole because the
  SHAPES — pack, spread, empty, a fixed parameter before it, a method — were covered
  thoroughly and the element was not. The new case runs each element type in the two
  spellings that differed, and its output is byte-identical to the same program
  under Go.

## v0.27.0

A nil pointer dereference stops the program.

### Language

- **A nil pointer dereference panics** — `panic: nil pointer dereference`, halting the
  cog, one of the runtime checks alongside an out-of-range index or slice, a division
  or remainder by zero, a shift by a negative count and appending past a capacity.
  `--unchecked` omits it with the rest.

  It has to be a check rather than a trap the hardware springs, because address zero
  on this target is ordinary Hub RAM. Measured before the fix: a read through a nil
  pointer yielded whatever lives at 0 and the program carried on, and a WRITE stored
  into Hub address 0 — the boot area — and carried on from there too. Both silent,
  where Go panics for each. It was the last place a program that compiles here could
  mean something other than what it means in Go while saying nothing about it.

  Two emission paths needed it, and they are not obviously the same thing: the `p.f`
  shorthand, and the written-out `*p` — where the star and the name are emitted as
  unrelated tokens, so the shape is only visible before the walk.

  **A pointer to an ARRAY is the exception, and it is the backend's fault rather than
  a choice.** flexcc drops an assignment made through a pointer-to-array that came
  out of a function -- `(*guard(po))[0] = x` writes nothing at all, silently, where
  the host compiler writes -- so wrapping that dereference costs the store it was
  meant to protect. The comma form fails identically, so there is no formulation that
  leaves the write intact. `doc/ptr-to-array-through-call.c` reduces it to a dozen
  lines and is the check for when it can be lifted. Nothing else reaches that shape:
  an assignment through a call's result is refused outright, so not adding the
  wrapper is what keeps the defect unreachable.

### Behaviour changes

- **A program that dereferenced a nil pointer used to keep running.** It now stops at
  the dereference. Anything relying on reading zero from address 0, or on a write
  there being harmless, changes behaviour — which is the point, but it is a change:
  a program that appeared to work may now panic, and what it was really doing was
  reading or writing the boot area. `--unchecked` restores the old behaviour if a
  measurement needs it.

### Documentation

- **`ogo help build` listed the runtime checks and named four of six.** Cog
  exhaustion had been missing since it shipped and the nil dereference since this
  release; a list introduced as what is "on by default" reads as complete, so being
  short is being wrong. The pointer-to-array exception is named there too, since a
  reader relying on the check needs to know the one place it does not apply.

- **A defined type over a STRUCT is not distinct from that struct, and now says so.**
  Found by testing the claim rather than reading it: `type A B` with `B` a struct
  lets an `A` pass, assign, return and compare where a `B` is wanted and the reverse,
  six positions Go refuses in both directions. Over an `int` or a `string` base the
  rule is enforced, so this is a gap in the struct path, not a policy — but `specs.go`
  had said flatly that a defined type "is not the same type as what it is defined
  over", which is true of every base except the one most people will use it on.
  Nothing is miscompiled, the two having one representation; the risk is the other
  way, a program that builds here and not under Go. Documented rather than pinned by
  a test, since a test written now would agree with the bug.

## v0.26.0

Things that did not compile, and the documentation that said they did.

Six programs Go accepts were refused here, all of them found by asking a construct to
stand in every position it can rather than in the one somebody happened to write: an
interface in a composite literal, `make` over a defined slice type, a method on a
defined slice type declared the short way, a variadic of strings or of structs, and
two more in named slice types. One of the named-slice defects was a wrong ANSWER
rather than a refusal, and a channel of pointers had a payload the compiler was free
to cache — the field two cogs poll being the one word that was not marked volatile.

The pattern behind most of them is worth stating: where a construct is parameterised
by a type, the tests varied its SHAPE and not its element. Every variadic test used
an `int` element, which has nothing to brace; every array-literal diagnostic used an
`int` element, whose C name is the same in both languages. Both hid a defect that a
`string` or a struct exposed at once.

The documentation was swept against the compiler afterwards, which is how the last
few entries below arrived: a spec sentence listing three of the six positions an
array literal may stand in, a diagnostic naming a rule that had stopped being true,
and a README note repeating two claims the help had already been corrected on.

### Testing

- **The fuzzer generates defined SLICE types too**, `type L_7 []int`, and makes
  variables with them -- `var s_9 L_7 = make(L_7, 2, 4)`. This is the class four
  defects were found in by hand this week, so it is the one worth having under the
  oracle rather than under a person remembering to try a spelling. Every operation
  the slice generator performs goes through one when a variable draws it: the element
  writes, the appends, `len` and `cap`, the index reads.

  It needed `make` over a defined slice type to exist first, which is above: a
  literal would have given `cap == len` and left the append generator nothing to
  grow into.

- **The fuzzer generates DEFINED types**, `type D_3 uint8`, and declares its sized
  integer variables with them. Every operation it already performs on a sized
  variable -- the arithmetic, the compound assignments, the shifts, the unary forms,
  and the fold of an unstored expression -- now runs through a defined type as well
  as a predeclared one, checked against the oracle's own answer.

  Sized kinds are where this is worth doing: a defined type has to be read two ways
  at once, as a distinct type for identity and as what it is defined over for
  arithmetic, and the sized kinds are also where Go and C disagree about the width a
  computation happens in. Get the resolution wrong there and it shows up as a wrong
  number rather than as a compile error. Four such defects were found by hand in a
  day; this is what looks for the rest.

- **Fixed a generator crash that had always been reachable.** `genInterfaceStmt`
  multiplied a struct field by its type-switch weight and discarded the error, so a
  field large enough to overflow an int32 left the value nil and panicked the next
  evaluation -- "interface conversion: interface is nil". The VM declines to model an
  overflow rather than guess, and what it cannot predict must not be generated, so
  the type switch is now dropped instead. Found at seed 486 once the change above
  shifted the random stream far enough to reach a large field; 3000 seeds are clean
  either way now, where before that seed aborted `ogo smith` outright.

### Documentation

- **Where an array literal may stand, said correctly.** `specs.go` had it as "an
  initializer, an element, and a `range` operand", which is three of the six: it also
  stands as an argument, as a result and as the operand of an index. The two it may
  NOT stand in are an `append` argument and a channel send, and the diagnostic said
  so as "only supported as a variable's initializer" — a rule a reader would find
  contradicted by the first argument they passed. It now names the fix instead: bind
  it to a variable and use that. The README's entry is narrowed to match.

- **`specs.go` claimed type ALIASES work. They do not.** "type A = B" parses and is
  then treated as a definition -- the `=` is read and discarded, there being no alias
  flag anywhere -- so `A` is a distinct type rather than another name for `B`, and
  `var i int = a` over `type A = int` is refused where Go accepts it. Recorded in the
  spec and the README rather than half-implemented: making an alias transparent means
  threading identity through the whole type system, which is a feature and not a fix.
- **A `type` declaration must stand at package scope**; one inside a function is
  refused, "statement TypeDecl is not supported yet", where Go admits it. Now said
  out loud in both places.

  Both came out of sweeping defined types over every kind one can be defined over --
  string, pointer, func, channel, integer, struct. Those all match Go exactly, so the
  blind spot behind this release's fixes really was slice-shaped; the two gaps above
  are in the DECLARATION rather than in any kind.

### Language

- **`make` takes a defined slice type**: `var d List = make(List, n, c)` over `type
  List []int`, which Go allows and this refused. It was refused three layers deep,
  each with a message about something else -- the checker read only the `[]T` shape
  and called a type name "dynamic allocation not supported"; having learned the name,
  the bare-type-name rule called the argument a value; and the emitter's make path
  wanted the declared type to be `[]T` too. A chain of definitions works, `type Alias
  List` over `type List []int`.

  The variable keeps its OWN name as its C type rather than the slice header's, so a
  method on the type still has something to hang off -- `d.total()` works on one
  declared this way. `append` had to learn to look through a defined type for the
  same reason: it read the written name and refused.

### Examples and tests

- **`_examples/protocol`**: a framed binary protocol over the serial line, host sends
  a frame and the board answers with one. It is the first example putting `go`, a
  ring, `p2.ReadByte` and `p2.WriteByte` together, and it exists because the two
  things that make it work are not guessable from the language:

  A whole cog does nothing but read — nothing is buffered behind `p2.ReadByte`, and
  at 230400 baud a byte is 43 microseconds, less than one `printf`. And the handover
  is a RING and not a channel: a channel here is a rendezvous, so the send parks the
  reader until the far side arrives, which is exactly the pause the line does not
  wait through.

  Both are measurements rather than advice, and the README records them. Verified on
  a P2-EDGE: three frames round-trip byte for byte, one of them carrying a payload
  byte that increments `0xFF` to `0x00` — a NUL on the wire, which no text path here
  can write.

### Fixed

- **A variadic whose element is a string or a struct did not compile.** A call packs
  its trailing arguments into an array of the calling frame, and an array
  initializer wants its aggregates BRACED rather than written as compound literals --
  `(ogo_string){"a", 1}` and `(P){9}` were both refused inside the braces, the
  target's compiler naming the compound literal's own anonymous type. `count("a",
  "bb")` and `firsts(P{9})` are ordinary Go and neither built.

  Every existing variadic test uses an `int` element, which has nothing to brace,
  which is why the feature looked whole: the shapes were all covered -- pack, spread,
  empty, a fixed parameter before it, a variadic method -- and the element type was
  the one axis nobody varied. The host compiler accepts a compound literal there as
  well, so only the board answered for it.

- **A channel of pointers had a payload the compiler could cache.** The cell's field
  was declared `volatile T* val`, which qualifies what the pointer POINTS AT rather
  than the field itself — so for `chan *P` the one word two cogs poll was the one
  word not marked volatile, the opposite of what a rendezvous needs. It is
  `T* volatile val` now, and reads the same for every other element type
  (`int volatile val` is `volatile int`).

  Whether the target's compiler actually cached it is unknown and beside the point:
  the intent was wrong. The host compiler said so — "initialization discards volatile
  qualifier" — where the target's said nothing, which is the second time this week
  the stricter compiler earned its place in the suite.

- **An interface in a composite literal.** A literal put whatever was written
  straight into an interface-typed slot, where the two words `{data, table}` belong,
  so `Box{&gr}` over `type Box struct{ in Shape }` was refused -- "expected
  _struct__Shape but got pointer to _struct__Rect" -- and `[2]Shape{&gq, &gr}`
  likewise. Both are accepted by Go. A brace initializer wants its members braced
  rather than a compound literal, which is why building the value the ordinary way
  did not fit and a brace-form sibling was needed.

  Found by sweeping an interface through every position one can stand in -- variable,
  argument, result, struct field, reassignment, comparison with nil and with another
  interface, both comma-ok assertions, and a type switch over an array of them. The
  other nine were already right, and the whole set now runs on the board as a case:
  the vtable is per (concrete, interface) pair, so which position built a value
  decides which table it carries.

- **A method on a defined slice type, reached through the short form.** `d := List{1,
  2, 3}` recorded the variable as the slice HEADER's type rather than as a `List`, so
  `d.total()` had nothing to hang off and came out as `unknown package "d"` -- a
  message naming neither the type nor the method, and sending the reader after an
  import that was never there. Every other way of making one always worked (`var d
  List = make(...)`, `var l List = back[:]`, `var v List = List{...}`, package
  scope), so the same program was accepted or refused depending on which spelling
  introduced the variable. Go accepts them all, and now so does this.

- **The README repeated two claims the help had already been corrected on**: that
  `ogo run` "sets a precise 200 MHz clock", and that `-f` is "the key part" of
  reading a board's output. Neither is true -- a compiled program sets its own clock,
  and the baud is the whole fix -- and the note is rewritten around what was
  measured. It was missed when the help was audited because the audit read the
  Status section rather than the whole file.

- **Four more defects in a named slice type**, all one cause: a defined type was read
  by the name written rather than by what it is defined over, so every table keyed on
  the slice header's own C name missed it. Over `type List []int`:
  - `var l List` with no initializer emitted `List l = 0;`, a scalar assigned to a
    struct, which the C backend refuses outright — **a variable of a named slice type
    could not be declared without an initializer at all**.
  - `Box{List{1, 2, 3}}` emitted `Box b = {{1, 2, 3}}`, filling the header's own
    pointer, length and capacity with 1, 2 and 3. **A silent wrong answer**, like the
    v0.25.0 one it is kin to.
  - `b.in[0]` was refused, "cannot index b.in", for a struct field Go indexes.
  - `l == nil` emitted `l == 0` against a three-word header, which the host compiler
    refuses and the target's miscounts.

  Found by sweeping six literal kinds against eight syntactic positions, after the
  v0.25.0 fix showed that a construct correct in one position can be broken in
  another. All 48 cells now agree with Go, bar one that is a lifetime refusal by
  design. `make(List, n)` remains refused and is listed in the README — it needs the
  checker to accept a type NAME where it looks for `[]T`, which is a change of a
  different size.

## v0.25.0

Talking to the hardware, and to whatever is on the other end of the wire.

A P2 program could drive an analog output and not read one, could stream text at a
host and could not be told anything by one, and ran at whatever clock the C backend
happened to pick. All three are closed: the ADC half of the smart-pin vocabulary and
the pin drive strengths that let an input be read at all, `p2.ReadByte` and
`p2.WriteByte` for the serial line in both directions and in binary, and `ogo build
--clock` for the frequency. A binary request/response protocol — a frame in, a frame
out, checksummed — now runs on a P2-EDGE, which is the thing none of these pieces
could do alone.

One miscompile, and it was found by testing a sentence in the README rather than by a
test: a literal of a named slice type, in the one spelling of five that names the type
twice, wrote its elements into the slice header instead of into storage. Auditing the
rest of what this project claims about itself — every line of `ogo help`, then the
README, then `specs.go` — turned up that bug, a second one in the diagnostics, a flag
that was documented and refused, and five sentences that were simply false. The
sections below say which.

### Language

- **The ADC half of the smart-pin vocabulary**, mirroring the DAC set that was
  already there: input ranges `p2.ADC1X` through `p2.ADC100X`, sampling modes
  `p2.ADCSample`, `p2.ADCSampleExt` and `p2.ADCScope`, and the internal references
  `p2.ADCGround`, `p2.ADCSupply` and `p2.ADCFloat`. Analog output could be written
  and analog input could not, which left half of a measuring instrument unreachable.
  The converter is ratiometric, so a raw count means nothing on its own -- read the
  two references and scale between them, which is what those are for, and neither
  needs a wire. Verified on a P2-EDGE: a floating pin read 1647 mV, mid-rail to three
  digits.

  `ADCSample`'s X argument is a sample period of 2^X clocks and **is usable to 13 and
  no further**. Up to there the doubling is exact; at 14 and 15 every reading is 0 and
  above that it is noise, whatever the Y register says. Nothing reports the overrun,
  so the number is in the docs. At X=13 the mode gives about 10640 counts between the
  references, a little over 13 bits, for 8192 clocks.

- **The pin DRIVE strengths**: `p2.DriveHigh15K` through `p2.DriveHighFloat`, the
  current-source variants, and the `DriveLow` mirrors. They are what a digital INPUT
  needs, which is not obvious from the name: the P2 has no pull-up bit and nothing
  called one, so **a pull-up is a weak HIGH drive together with a floating LOW one**
  and a pull-down is its mirror. Reading a switch was possible before only if
  something external held the pin, and a pin held by nothing does not read a stable
  anything -- which is a poor way to learn the state of a stop button.

	p2.PinFloat(pin)
	p2.WritePinMode(pin, p2.DriveHigh15K|p2.DriveLowFloat)
	p2.PinHigh(pin)   // a weak 1 a switch to ground can overpower

  Verified on a P2-EDGE at both 15K and 150K: the pulled-down pin read 0 where the
  same pin left floating read 1.

- **`p2.ReadByte(timeout)` reads from the serial line** -- the first way IN. Every
  one of the two dozen intrinsics was output or pins or time, and the three print
  built-ins only write, so a program could stream measurements at a host and could
  not be told anything by one. A command protocol, which is what a host talking to
  an instrument is, could not be written at all.

  It returns the byte as 0..255 or **-1** if the wait ran out, so a caller tests for
  a negative. A timeout of zero waits forever. They are BINARY milliseconds, 1/1024
  of a second rather than 1/1000, the toolchain dividing the clock by a shift -- a
  wait measures about 2.4% short of what its number says, which is fine for a
  protocol timeout and not fine for keeping time.

  **Nothing is buffered behind it**, and that is the governing fact rather than a
  caution. Measured on a P2-EDGE at 230400 baud: a loop reading into an array caught
  all eight bytes of `PING\rOK\r` in order, and the same loop with one `printf` per
  byte caught four of them -- silently, with nothing reported anywhere. A program
  that must not miss a byte gives a cog to reading and to nothing else.

  And it must not hand them over on a **channel**, which is the obvious thing to
  reach for: a channel here is a rendezvous, so the send parks the reader until the
  far side arrives, and the line does not wait. Measured, that loses every byte of
  the next two commands while the worker prints the answer to the first. The handover
  is a single-producer/single-consumer ring -- the reader only stores and bumps head,
  the worker only reads and bumps tail -- which took all of `PING\rSTATUS\rGO\r`
  where the channel version took one command and two fragments.

- **`p2.WriteByte(b)` puts a byte on the serial line exactly as given**, which no
  other path here will do. It is what a protocol carrying anything but text needs,
  and it pairs with `p2.ReadByte` below: without both, a packet could be received and
  not sent.

  The two near misses are why it exists, and both corrupt silently. `printf("%c", b)`
  writes a RUNE, so everything from 0x80 up goes out UTF-8 encoded as two bytes and a
  length field of 200 becomes 195 136. C's `putchar` looks right until a byte happens
  to be 10, which it TRANSLATES into 13 10. Measured on a P2-EDGE: this wrote 1..255
  as exactly those 255 bytes where `putchar` wrote 256. A frame of `AA 04 0A C8 AA 0D`
  plus checksum -- chosen to contain both traps -- arrived byte for byte.

- **`p2.SetBaud(n)`** sets the baud rate of the link `print`, `println` and `printf`
  go out on, mapping to the backend's `_setbaud`. The loader leaves it at 230400,
  and a host expecting another rate had no way to ask for one -- which ruled out
  talking to anything with a fixed protocol speed. The host must be reading at the
  new rate by the time anything is written.

- **`printf` takes flags, a width and a precision** -- `%6.2f`, `%-8s`, `%+05d`,
  `%.3s`. They were rejected outright before, so `%.2f` -- printing a number to two
  decimals -- could not be written at all, and the error said "unknown formatting
  verb %." as though the trouble were the verb. They mean what they mean in `fmt`,
  and for a string the width and precision count RUNES rather than bytes, so `%.1s`
  of `"héllo"` is `"h"` and never half a character.

  Two flags are refused, because the C backend ignores them and printing something
  narrower than asked for is worse than saying so: `#`, which would write a base
  prefix, and `0` on a float, which would zero-pad. `0` on the integer verbs works,
  so `%05d` is fine. Both are the backend's, not the language's --
  `doc/printf-flags-ignored.c` measures them, and the refusal goes when that comes
  back clean. The `%*d` forms, which take a width from an argument of their own,
  stay unaccepted: the count of verbs is what pairs each one with an argument to
  type-check it against.

### Fixed

- **Fixed a miscompile of a literal of a named slice type.** `var l List = List{10,
  20, 30}` over `type List []int` wrote the elements into the slice header's own
  fields, so `len(l)` answered 20, `cap(l)` 30, and `l[0]` read whatever lives at
  address 10. A brace initializer cannot fill a slice -- it is a header pointing at
  storage, and the literal has to put the elements somewhere first.

  Only that one spelling was wrong. `l := List{...}`, `var l = List{...}` with no
  type written, a literal passed as an argument and one at package scope all took
  other paths and were always right, which is how it lasted: the broken form names
  the type twice. Found by checking a README claim rather than by a test, and the
  regression case now covers all five positions.

### Diagnostics

- **Diagnostics naming an array type no longer spell it in C.** Five of them reported
  the element as the emitted C holds it -- `[2]uint8_t` for a `[2]byte`, `[2]int32_t`
  for a `[2]rune` -- naming a type that does not exist in this language, and one
  managed both spellings in a single message: `cannot use a [2]int literal as
  [2]uint8_t`. The helper that renders a type in OctoGo's spelling already existed;
  those messages simply did not call it. (`byte` and `rune` are aliases here as in
  Go, so the `uint8`/`int32` now printed name the same types; Go tracks which
  spelling you wrote and says `[2]byte`, and this does not.)

### Tooling

- **`ogo build --clock`** picks the system clock, which was not selectable at all.
  Programs ran at 160 MHz because that is what the C backend falls back to when a
  program asks for nothing -- a 20 MHz crystal times eight, a round multiplier rather
  than any limit of the part. Verified on a P2-EDGE: 160061416 Hz by default,
  180068584 with `--clock 180MHz`, 200075240 with `--clock 200MHz`.

  It takes plain Hz or a suffix (`--clock 200MHz`), and applies to `ogo run` and
  `ogo test` too -- a test binary should take the clock the program it tests ships
  with, since running tests at another speed is how a timing bug hides. `--xtal`
  states the crystal when it is not the usual 20 MHz; nothing can ask the board, so
  an unstated crystal is believed. A frequency the crystal cannot make **exactly** is
  refused rather than rounded to the nearest it can: every wait, baud rate and sample
  period is scaled by this number, and a board running one percent fast reports
  nothing at all. Above ~200 MHz is refused as well, that being the fastest confirmed
  here and above the part's rating -- a P2 will go faster, but a compiler should not
  overclock one because a number was typed.

  Doing it at BUILD time is what keeps the console readable: the backend derives the
  serial divisor from a clock it can see, where a run-time change would leave
  everything written before the baud was re-set as line noise.

- **Fixed: `ogo fmt -exclude` was documented but refused.** Only `--exclude` worked,
  so following `ogo help fmt` got `unexpected flag: -exclude`. Both spellings are
  accepted now, as `--release`/`-release` already were on `ogo build`.

### Documentation

- **Fixed: `ogo run --help` said it "sets a precise 200 MHz clock".** It does not and
  never did. The loader is passed a `-f` frequency, but a flexcc-compiled program
  sets its own clock as it starts, so `-f` does not decide what it runs at -- the
  same binary reports 160061416 Hz whether loaded with `-f 200000000` or with no `-f`
  at all. The frequency is the build's to choose, which is what `--clock` is for.

- **Corrected help.** Every command's help was read against what the tool does, after
  two claims in it turned out to be false. Three more were:
  - `ogo smith` said "generation is not yet reproducible from a seed". It is, and has
    been: the same `-seed` writes a byte-identical program. The note survived the fix
    that made it wrong, and it is the kind that costs real work -- it tells you not to
    bother re-running the seed that failed.
  - `ogo loadp2` said a passthrough load leaves the P2 "on its imprecise internal
    oscillator" so output is "garbled at every baud", and prescribed `-f 200000000 -b
    230400`. Only the baud matters: measured, the same binary prints cleanly with `-b
    230400` whatever `-f` says, and rubbish without it whatever `-f` says. loadp2
    reads at 115200 where an ogo program writes at 230400, and the program is on the
    crystal PLL either way because it sets its own clock.
  - `ogo fmt` said "the formatted result is compared and nothing is written", which
    reads as though it prints nothing. It prints the formatted source, as gofmt does.

  What the help gets right was checked too, not assumed: the binary naming, `-c`
  leaving `<pkg>.test.binary`, the 0/1 exit status, and each of the five documented
  runtime checks panicking on the board with the message it advertises.

## v0.24.0

Types this compiler could not tell apart, and a handful of wrong answers nobody was
looking for.

`int` and `int32` shared one kind, and so did a defined type and the type it is
defined over, so nothing here could tell a `Celsius` from an `int` or refuse `var y
int32 = x`. Both are types of their own now, and `var x = expr` — which carried no
type at all, where `x := expr` has always inferred one — is type-checked like its
twin. `specs.go` had required those conversions from the start, so this is the
checker catching up to the spec rather than a change of rule; it is also why this
release refuses more programs than any before it, all of them programs Go refuses
too.

Interfaces were wrong from the other end. Two of them compared equal when they held
different pointers, silently, because an interface is a struct with no fields here
and the struct-equality helper compared nothing at all. And nil was not an interface
value in any position except the one everybody writes, `var i I`, which is exactly
why the gap held for so long.

Most of the rest was found by running ordinary Go-shaped programs under Go and under
this compiler and diffing the bytes rather than the exit status: a defer inside a
branch that never ran, printing part of itself from arguments that were never
written; a value inferred from `1 + v` taking the type of the literal and truncating
an int64 to 1; and, on hardware, the two backend faults behind every 64-bit shift by
a variable count.

### Language

- **`int` and `uint` are types of their own**, distinct from `int32` and `uint32`
  even though all four are 32 bits wide here — Go's rule, and the one the checker
  was missing. `byte` and `rune` are the two exceptions and stay aliases, being the
  same type twice named in Go, so they mix with `uint8` and `int32` without a
  conversion. An untyped constant still goes anywhere it fits, so `var x int32 =
  42` needs nothing written.
- **A rune literal defaults to `rune`**, where an integer literal defaults to
  `int`: `x := 'a'` is a `rune` and `%T` says `int32`, as in Go. It used to be an
  `int`.
- **A constant written through a conversion is a TYPED constant**, and types what
  it is combined with. With `const one = int32(1) << 16`, `50 * one` is an `int32`
  and `scale := 50 * one` declares one. The conversion's type used to be dropped
  as soon as it was folded.

### Fixes

- **`var x = expr` gave the variable no type at all**, where `x := expr` -- the
  same declaration in Go -- infers one. Every check keyed on a type was therefore
  skipped for such a variable: a `bool` was accepted where an `int` was wanted, a
  `string` likewise, `if n {}` compiled for an integer `n`, and the same held for a
  package-level `var`. The two forms now share one inference, so a `var` without a
  written type carries exactly what a `:=` would give it -- a scalar type, a
  pointer, a named type with its methods, a composite literal's element type, or a
  function's signature.
- **A defined type is a type of its own**, distinct from the type it is defined
  over: with `type Celsius int`, an `int` goes into a `Celsius` only through a
  conversion, and the reverse likewise. The two shared a Kind here -- which is what
  makes every Kind-keyed check work for a defined type at all -- so nothing could
  tell them apart, and `var i int = c` compiled. Checked now in the eight positions
  a value meets a written type (declaration, assignment, argument, return, field
  assignment, struct literal, send, and both operands of an operator), and two
  defined types over one underlying type are two types.

  It answers only where the value's type is KNOWN. A range value and a method
  result carry no type name, so those go unchecked rather than misreported: `for _,
  w := range ws` over a `[]Word` yields a `Word` that the checker cannot name, and
  reading that silence as "int32" would refuse `t += w` for a `Word` t -- a value
  of exactly the right type. A field read is named, its type being written down in
  the struct. Still unchecked for the same reason: an element assignment and a
  switch case, whose destinations carry no name either.
- **A slice from `make` carries its element type.** It was the one container that
  did not: a composite literal writes its element type in its own brackets, which
  was read, and `make` writes it in an argument, which was not -- so `xs :=
  make([]int32, 3); xs[0] = n` for an `int` `n` compiled, whichever way `xs` was
  declared. Writing `var xs []int32 = make(...)` was always checked.
- **An interface case reached through embedding is answered rather than skipped.**
  `case N:` in a type switch and the assertion `r.(N)` ask which concrete types
  satisfy both interfaces, and asked it with a lookup a PROMOTED method is
  invisible to. A `sensor` embedding a `base` takes `v()` from the base, so it did
  not count as implementing `R`, no candidate was left to test, and the test became
  a constant false -- the case skipped and the assertion answering no, silently,
  for a value that satisfies both.
- **A type assertion accepts an expression operand** in both forms, `p, ok :=
  rs[i].(*A)` and `p := b.r.(*A)`, where only a name worked before. The operand is
  bound once, so one with a side effect is evaluated once; in the one-value form
  the binding goes to the statement prologue, which is carried into a loop body, so
  an operand that changes per iteration is bound per iteration.
- **A type switch may switch on any interface expression**, not only on a name:
  `switch t := shapes[i].(type)` is how a dispatch loop is written, and an index or
  a field operand used to be refused -- with a message that named neither the limit
  nor the workaround. The operand is bound to a temporary, which also makes it
  evaluated exactly once however many cases test it, as in Go.
- **Two interface values compared equal when they were not.** An interface is a
  struct here and was registered as one with no fields -- its words are the data
  pointer and the table, not anything the source declared -- so the struct-equality
  helper compared nothing and returned whatever was in the return register. Two
  interfaces holding different pointers came out equal, silently. They now compare
  by dynamic type and value, as Go does.
- **nil written into a FIELD works**, for the two types whose nil is a whole struct
  rather than a word: `h.s = nil` for a slice field and `h.i = nil` for an interface
  one each emitted `= 0`. Assigning nil to a plain *variable* was right, which is
  what hid it -- the branch that knew about nil asked only about a bare name.
- **nil works as an interface value.** `i == nil`, `i = nil` and `return nil` from
  a function returning an interface each failed to compile, in three different
  ways; only `var i I` with no initializer was right, which is why the gap held --
  the common spelling of the zero interface was the one that worked.
- **A constant that fits an unsigned int but not a signed one no longer widens to
  64 bits.** `0xFFFFFFFF` and anything else above 2^31 was written with an `LL`
  suffix, which made `m ^ 0xFFFFFFFF` for a `uint32` `m` a `long long` -- and the
  target's C compiler refuses the `printf` that feeds, so the build failed outright
  with "Bad number of parameters in call to `_basic_print_unsigned`". It was never a
  wrong answer, but it was a build that only failed on a board: gcc accepts the same
  C, so nothing off-target saw it. Such a constant now carries a `U` suffix and
  stays 32 bits wide.
- **A defer inside a branch that never ran still printed part of itself.** A defer
  written in an `if` is replayed under a runtime flag recording whether the branch
  executed, and the flag was written as a statement PREFIX -- `if (flag) f(...);`
  -- on the assumption that a call is one C statement. `println` of several
  arguments is one `printf` per argument, so the flag guarded the first and let the
  rest run: a branch that never executed printed the tail of its deferred `println`
  from capture temporaries that were never written, producing a bare ` 0` line out
  of nowhere. The flag now opens a block.
- **A value inferred from a mixed-type expression took the type of the FIRST
  operand**, so an untyped constant written on the left named a type the
  expression does not have and the value was truncated to fit it: `b := 1 + v` for
  an `int64` `v` was declared `int` and printed `1` instead of `1099511627777`,
  `d := 2 * f` dropped a `float64`'s fraction, and `w := 1 + u` wrapped a `uint32`
  past 2^31 to a negative. Written the other way round each was already correct,
  which is why no test had caught them.
- **A composite literal's values were checked against nothing.** Field names, the
  two forms not being mixed and the value count were all checked; the values
  themselves were not, so `S{f: true}` put a `bool` in an `int32` field and
  compiled. They are now checked against the field's type, and a constant is
  range-checked against it too.

- **A 64-bit shift by a variable count was miscompiled on the target**, and is
  fixed. `v << n` on an `int64` or `uint64` with a count that is not a compile-time
  constant came back wrong on a P2 for every count, and `(v << 62) >> n` could
  panic with "negative shift amount" for a count of 3. Two backend faults were
  behind it, neither about shifting: a cast to a 64-bit type applied to a 64-bit
  expression (the left-shift helper's `(int64_t)((uint64_t)v << n)`, which is why
  the right shift was right all along), and an argument narrower than an `int64_t`
  parameter not being widened when another argument at the call is a 64-bit
  expression, so the callee read the count's high word out of the frame. Both are
  routed around; `doc/shift64-by-variable.c` is the reproducer for each.

  It had been broken since 64-bit integers shipped. A run case did shift an `int64`
  by a variable, but only by a count past the width, so the guard returned before
  reaching the fault — the shape was covered and the path was not.

### Known issues

- **Two more backend optimizer defects**, found by widening the on-board fuzzer
  sample to 400 seeds: seeds 74 and 323 compute the wrong answer on a P2 and the
  right one on the host, so the emitted C is correct and the target's compiler is
  not. Both predate the release that found them, and `-O0` corrects both, so both
  are the optimizer. They are not the same fault, and only one is still open
  upstream:

  - Seed 74 is a **division by a constant** miscompiled next to a never-taken call,
    reduced to 25 lines in `doc/const-divide-miscompile.c`. spin2cpp master already
    computes it correctly, so what would clear it here is regenerating
    `internal/flexcc` once a release carries the fix.
  - Seed 323 is an **unwritten local array element meeting a general multiply**,
    reduced to eight lines in `doc/array-multiply-miscompile.c`. It is wrong on
    master too, so it is live upstream. It is close kin to flexprop issue 103 and
    strictly simpler than that one -- no call and no global -- and the #103 fix does
    not cover it: master corrects #103's reproducer and leaves this one wrong.
    Reported upstream as
    [flexprop#105](https://github.com/totalspectrum/flexprop/issues/105).

  No affordable flag covers either: `-Ono-regs` corrects seed 74 but costs 68% more
  code and does not correct 323, and eleven other passes turned off individually
  correct neither. See the `smithSeeds` comment in `internal/octogo/board_test.go`.

### Behaviour changes

- **Mixing `int` with `int32` (or `uint` with `uint32`) now needs a conversion**,
  in all eleven positions where a value meets a type: a declaration, an
  assignment, an argument, a return, a binary operation, a comparison, an element
  or field assignment, a send, a case, and a composite literal. Every such program
  was refused by Go already; none of them computed a wrong answer here, `int`
  being 32 bits, but a program that compiles here is meant to compile in Go.
  `specs.go` has required this from the start — "explicit conversions are required
  when different numeric types are mixed" — so this is the checker catching up to
  the spec rather than a change of rule.
- **A composite literal with a value of the wrong type is refused**, where it used
  to compile and write the value's bytes into the field.
- **A variable declared `var x = expr` is type-checked**, where it used to be
  checked nowhere. Programs that passed such a variable to a parameter of another
  type, or used an integer one as a condition, were accepted and are now refused --
  as Go refuses them, and as the `:=` form here already did.
- **An element of a slice from `make` is type-checked**, where it used to be
  checked only when the variable was written with its type.
- **Mixing a defined type with the type it is defined over now needs a
  conversion**, in the eight positions listed above. Every such program was refused
  by Go already; none computed a wrong answer here, the two having one
  representation.

## v0.23.0

A standard library, a formatted print, and an example that is also a test against Go.

`strings` is the first library code here that is neither a hardware wrapper nor a
test harness, and it is written in OctoGo. `printf` joins `print` and `println`, with
`%T` — the verb worth having — answered at compile time for everything but an
interface, whose vtable now carries the name of the type it was built for.
`_examples/life` imports nothing at all and is the same program in Go: the tests
perform the two substitutions that make it one, run the twin under `go run`, run this
one on a real P2-EDGE, and require the same bytes from all three.

Running the same program under Go is what most of this release was checked with, and
it is what found most of it. `append(s, xs...)` had been accepted with its ellipsis
ignored since the day `append` shipped. A string literal was passed through to C on
the belief that the two languages share their escapes, which they do until `\xff` is
followed by a `b`. `println` of a struct printed a garbage integer where Go refuses
the program. And a method promoted from an embedded field could be called but could
not satisfy the interface it was written for.

### Language

- **`printf` is a built-in**, beside `print` and `println`. It writes its arguments
  under the control of a format and formats as `fmt.Printf` does — `%d %x %X %s %t
  %f %c %v %T %%`, no flags or width yet. The format must be a CONSTANT string:
  there is no heap to build one in, and a format known at compile time is what lets
  every verb be checked against its argument where the call is written rather than
  going wrong on the board. A wrong verb, an unknown verb and a verb count that does
  not match the argument count are each refused there.
- **`%T` prints the type**, which is the verb this is really for. For everything but
  an interface it is a compile-time constant and costs nothing at all. For an
  interface it is the dynamic type, read from the value: each vtable now leads with
  the name of the type it was built for, so the answer is one pointer away and is
  exact. A type prints unqualified — `Celsius`, where Go prints `main.Celsius`,
  there being no package clause here — and an interface holding nothing prints
  `<nil>`, as in Go.
- `%v` prints what `println` prints, by calling the same code rather than restating
  it: `[1 2 3]` for a slice, `true` for a bool, the shortest form for a float. Not a
  pointer, a func value, an interface or a struct — `fmt` prints `<nil>` for a nil
  one where the built-in `println` prints `0x0`, and `&{1 2}` for a pointer to a
  struct. `printf` is `fmt`'s function, so rather than print a third thing that is
  neither, `%v` declines those and says that `%T` answers for the type.
- Each verb renders as `fmt` does rather than as C does, where the two differ.
  `%x` of a negative integer is a sign and a magnitude, `-ff`, not the two's
  complement `ffffff01` C prints for the same value, and `%c` writes the UTF-8
  encoding of the character an integer names rather than one byte of it.

- **`append(s, xs...)` works.** The spread form parsed and type-checked from the day
  `append` shipped, and its ellipsis was then IGNORED: the whole slice was emitted
  where one element belonged, so both C compilers refused the result. It was a build
  break rather than a wrong answer, but it was a build break in C the user never
  wrote. A slice of the same element type spreads, and — as in Go — a **string
  spreads onto a `[]byte`**, `bs = append(bs, "hi"...)`. The source and the
  destination may overlap, so `append(s, s...)` means what it means in Go.
  The two-result form spreads too, and is all-or-nothing: either the whole spread
  fits or none of it is appended, `ok` being one bool for the call.

### Standard library

- **A `strings` package**, the first library code here that is neither a hardware
  wrapper nor a test harness. It is the allocation-free part of Go's: `Compare`,
  `Contains`, `ContainsAny`, `ContainsRune`, `Count`, `Cut`, `CutPrefix`,
  `CutSuffix`, `HasPrefix`, `HasSuffix`, `Index`, `IndexAny`, `IndexByte`,
  `IndexRune`, `LastIndex`, `LastIndexByte`, `TrimPrefix`, `TrimSuffix` and
  `TrimSpace`. Each either answers a question about a string or returns a SUBSTRING
  of one, which costs nothing — a string is a pointer and a length, so a slice of it
  points into the same bytes. What is absent is what allocates: `Split`, `Join`,
  `Repeat`, `Replace`, `ToUpper` and the rest need somewhere to put a string that
  did not exist before. `Builder` is how a program makes one.
- It is **written in OctoGo**, compiled like any other package. Nothing in it is an
  intrinsic and nothing in it is C, which is as much the point as the functions are:
  a standard library a language cannot express is a standard library written in
  something else.
- **`EqualFold` is deliberately absent.** Go's folds Unicode, not ASCII, and an
  ASCII-only one would differ on the inputs nobody tests. A function that is nearly
  Go's is worse than one that is missing, because it compiles. `TrimSpace`, by
  contrast, trims what *Unicode* calls white space — a short, closed list — so it is
  exact rather than an approximation.
- Checked by running the same program under Go and comparing the bytes
  (`TestStringsMatchesGo`, `TestOnBoardStrings`), on the arguments where a plausible
  implementation and a correct one part company: an empty needle, a needle longer
  than the haystack, overlapping matches, a multi-byte rune, invalid UTF-8, and the
  white space that is white space only to Unicode.

### Examples and tests

- **The fuzzer generates interfaces.** It generated none at all before, which meant
  the newest and least-exercised part of the compiler was the one part `ogo smith`
  could not reach. Every generated struct now implements one fixed-name method, so a
  single interface type is satisfied by all of them — that is what makes the dispatch
  DYNAMIC to the compiler while staying static to the generator, which is what an
  oracle fuzzer needs to predict the answer. Each generated interface statement binds
  the interface to a struct variable and then reads the same field back three ways:
  a plain call through the vtable, a type assertion with the call on its result, and
  a type switch with one case per concrete type — so the cases that must NOT be taken
  are exercised as well as the one that must. All three have to agree, and the VM
  knows what they agree on.

- **`_examples/life`** — Conway's Game of Life, and the first example that imports
  nothing at all: no pin, no cog, no intrinsic, just arrays, structs, methods and
  loops. Give the source a package clause and spell `printf` `fmt.Printf` and it
  compiles as Go, which the tests do not take on trust — `TestExampleMatchesGo`
  performs exactly those two substitutions, runs the twin under `go run`, runs this
  one through the compiler, and requires the same bytes out of both.
  `TestOnBoardExample` makes the same comparison with a real P2-EDGE standing in for
  the host, and `TestExampleTwinIsGofmtClean` requires the twin to be gofmt-clean,
  so the example reads as Go rather than merely computing what Go computes.
- **Every example is now built by `go test ./...`**, with the real backend and to
  the standard every target build is held to: a successful build must also be
  silent. Nothing compiled them before — the run corpus is its own table, and the Go
  tool skips a directory named with a leading underscore — so an example that
  stopped building would have been found by a reader.

### Fixes

- **An embedded type's method satisfies an interface**, as it does in Go. It always
  satisfied a direct call — `b.get()` reached the embedded `A`'s `get` — while the
  interface check read the type's OWN methods only, so one method-set question was
  answered two different ways: a method you could call was not a method you could put
  behind the interface it was written for. Both the checker and the emitter had their
  own copy of the check and both now resolve through the embedding chain, breadth
  first as Go promotes. The vtable thunk is where it shows: a promoted method takes
  the EMBEDDED sub-object as its receiver, not the whole struct, so the thunk walks
  the field path in.

- **`ogo fmt` tightens a binary operand inside a multi-argument call**, as gofmt
  does: `f(a+b, c)` where `f(a + b)` keeps its spaces. That reads like an
  inconsistency and is the rule — gofmt raises its expression DEPTH for an argument
  list of more than one argument, and at depth the add- and mul-level operators
  render tight. A comparison and a logical operator stay spaced at any depth. It is
  the same rule `ogo fmt` already applied inside a subscript, now applied where it
  is met far more often; `_examples/life` had been written around it and is spelt
  naturally again.
- **A unary operator is no longer tightened by that rule.** `&` and `*` and `-` are
  mul- and add-level operators AND unary ones, so a purely symbolic test dropped the
  space after the comma in `f(&a, &b)`. Latent in the subscript rule since it
  shipped; a subscript rarely holds an address.
- **A float literal ends an operand.** `isOperandEnd` had no case for one, so the
  `/` in `10.0/4.0` was not recognised as a division.
- **A string literal is decoded and re-quoted for C** rather than passed through.
  Go and C share the common escapes and part company on the rest, and the
  passthrough was wrong wherever they do: C's `\x` has no length limit, so `"a\xffb"`
  read there as an `a` and ONE escape of value `0xffb` — a compiler warning, a
  two-byte string, and a program that could not then find its own `b`. Go's
  `"\u2028"` is three UTF-8 bytes here and a universal character name there.

### Behaviour changes

- **The `ok` of a two-result `append` is a `bool`.** It was emitted as an `int`, so
  `println(ok)` printed `1` where a type assertion's `ok` prints `true`. The checker
  had always typed append's `bool` (`var b bool = ok` was accepted); only the
  emitter disagreed. Code that stored it
  in a variable is unaffected; code that printed it sees `true`/`false` now.
- **`println` of a struct is refused.** It used to compile and print the struct's
  first word as an integer — a garbage number, with nothing said. Go rejects the
  same program (`illegal types for operand: print`), so this is one more place where
  a program that compiles here means what it means in Go.
- **A pointer, a func value and an interface print as an address**, as Go prints
  them, the interface as its two words `(0x0,0x0)`. They used to print as a signed
  decimal, or — for an interface — as only the first of the two.

## v0.22.0

Interfaces, taken as far as a program written in Go would expect them to go.

`any` is spelled `any`. An interface may be written where a type is wanted rather
than declared with a name. One may embed others. A type switch case may name an
interface, and so may an assertion — both matching on the METHOD SET, which the
whole-program view turns into a list of table comparisons the compiler writes out.
One interface value assigns to a variable of another when the methods allow it, and
an assertion is a value like any other, so a suffix applies to it where it stands.

Almost all of it came from one reader's test programs, written the way Go would be
written and then run here. That is a better generator of work than a backlog: each
failure was a place where the language stopped short of what someone reasonably
expected, and two of them were not the gaps they appeared to be. `println(v.foo())`
on a method with no results COMPILED, printing an extra line per call, because the
emitted C passes a void expression to printf — which gcc refuses, so no host test
could have seen it, and the target's compiler accepts. And
`switch x := v.(type); x {` compiled too, which Go rejects outright; it gave the
right answer, which is the worst way for that to be wrong.

The one to be careful about is interface WIDENING. An interface carries a pointer,
and on a target with no heap the lifetime rules are what keep that pointer honest —
so widening must not become the way to launder one into a package variable. It does
not, but making it so needed a second fix: a type switch binds a NEW NAME to what it
switched on, and storing that name is storing whatever the operand was, which the
leak summary did not follow.

### Language

- **A suffix may be applied to a type assertion's result where it stands**,
  `e.(*P).foo()`, `e.(*P).n`, `e.(*P).xs[i]`, and each of the writable ones as an
  assignment target, `e.(*P).n = 1`. Interface targets too, `e.(T).foo()`.

  All of them used to be `type any has no method foo` — the assertion's Selector
  carries a type rather than a field name, so it was not counted and the suffix was
  read against the OPERAND's type. It is checked against the asserted type now, and
  emitted by binding the assertion to a temporary so the rest has a base to apply
  to, which is what the interface form already did for its own value.

- **An interface value may be assigned to a variable of another interface type**,
  `var e any = z` — widening, which Go allows when the target's method set is a
  subset of the source's. It works wherever a value can stand: a variable, an
  argument, a result and a package variable. The other direction is what an
  assertion is for, and narrowing is still refused with Go's message.

  It is the same two words — the data pointer unchanged beside the table for the
  target — so it is the rebind the type switch's binding and the assertion already
  make.

  **The lifetime rules see through it.** An interface carries a pointer, and
  widening must not become the way to launder one into a package variable, so
  provenance travels with the value. That needed one more thing: a type switch BINDS
  a new name to what it switched on, and storing that name is storing whatever the
  operand was, which the leak summary did not follow. `switch x := p.(type)` then
  `global = x` was silently accepted; it now reports at the call site, exactly as
  storing the parameter itself does.

- **An interface may EMBED another**, `type Z interface { T; U }`, taking its
  methods as its own. Two embedded interfaces may declare the same method, which
  stays one method — a vtable has one slot per name — and the embedded name may be
  declared anywhere in the package, before or after.

  The grammar's `MethodSpec` gains the bare-name form,
  `identifier [ "(" … ] `, which left-factors: the parenthesis is what tells a
  method from a name standing alone, and the regenerated parser reports the same
  eight ambiguities as before, none new.

  Embedding a type that is NOT an interface is Go's type-constraint syntax, which
  belongs to generics and is refused. An interface that embeds itself, directly or
  through others, is `invalid recursive type` — the existing cycle pass stops at an
  interface on purpose, since one is a fixed size whatever it carries, but embedding
  is about the method set rather than the size.

- **A type switch case may name an INTERFACE**, `case T:`, matching on the method
  set: any dynamic type implementing `T` takes the clause, and the name binds at
  `T`. Written bare, where a concrete case is `case *X:` — the star is there because
  what an interface holds is a pointer, so a concrete case names one and an
  interface case names the interface.

  Matching a *set* is what makes clause order matter, and it is the property to
  watch: where a type satisfies two of them the first clause wins, so the same value
  through `case T:` then `case U:` and through `case U:` then `case T:` answers
  differently. Both are exercised on hardware.

  The program is closed, so "implements T" is a question this compiler answers by
  listing the types that do. There is no run-time method lookup — only the same
  table comparison a concrete case makes, once per type that qualifies, which is
  also why the empty interface needs no list at all: it asks only that the value
  hold something.

- **A type ASSERTION to an interface**, `s.(T)`, in both forms: `t := s.(T)` panics
  when it does not hold, `t, ok := s.(T)` reports it and leaves `t` the zero
  interface value. Written without a star for the same reason a case is — `*T` would
  be a pointer TO the interface — and it asks the same question, of one type.

  What comes back is another interface VALUE rather than the pointer that went in,
  so it is two words built from two: the data carries over unchanged, and the table
  becomes the one for the asserted interface paired with whatever concrete type the
  operand turned out to hold. That pairing is what the run case checks by asserting
  the same interface over two different dynamic types.

  A panic from one names the shape now — `interface conversion: interface{} is not
  U` — where an anonymous interface used to leak the generated name it was minted
  under.

- **An interface type may be written where a type is wanted**, rather than only
  declared with a name of its own: `func measure(s interface{ area() int })`, a
  variable, a struct field, and the empty `interface{}`. It used to be `unsupported
  type ""` -- a refusal naming nothing, because the tokens carry no identifier for
  the message to report.

  Everything the interface machinery does is keyed by a NAME -- the method table, the
  vtable struct, the one static table per (concrete type, interface) pair -- so
  giving the shape a name is the whole of what it needed. That is the move
  `anonStructType` already makes for `struct{ x, y int }`.

  Identity is by METHOD SET, so two anonymous interfaces with the same methods are
  one type and a value passes between them, whatever order the methods were written
  in.

- **`any`**, Go's name for the empty interface, and interchangeable with
  `interface{}` because it is the same type rather than a parallel one. It is
  registered in the universe as a type declaration over an interface with no
  methods, so everything that keys on a variable's type being an interface works for
  it with no case of its own. Being predeclared rather than a keyword, it can still
  be shadowed: existing code using `any` as an identifier is unaffected.

- **`len` and `cap` of an array reached through a chain**: a ROW of a
  multi-dimensional one, `len(m[0])`, a struct's array field indexed to its row,
  `len(g.rows[0])`, a row through a pointer to the array, and a field reached past an
  index, `len(gs[i].rows)`. Only the outermost extent of a variable or of a plain
  field answered before; everything else was `len is only supported for strings,
  arrays and slices yet`, about an operand that is an array.

  One walk answers all of them, which is why it is one change rather than six: what
  the chain walk reports having reached carries the extents still remaining — one
  index into a `[2][3]int` leaves a `[3]int` — so the answer is the outermost of
  those, exactly as it is for a variable. A slice reached the same way carries no
  extents and still reads its header, which is where a length that is not a constant
  lives.

### Behaviour changes

- **A type switch guard may not be followed by an expression**, `switch x :=
  v.(type); x {`. A type switch's guard is the whole statement, so there is nothing
  for a tag to be; Go rejects the text outright.

  It used to compile — the tag was read and ignored, so the statement ran as an
  ordinary type switch and gave the answer you would expect. Accepting a program Go
  has no meaning for is the problem, and giving the right answer while doing it is
  the worst version: the habit travels to Go, where it does not build. The message
  names the spelling that works.

- **A call that yields no values is refused where a value is wanted**,
  `println(v.m())` for a method declared without results. Go rejects it —
  `v.m() (no value) used as value` — and so does this now, in every position: an
  argument, an operand, a `return`, and the three assignment forms.

  It used to compile and RUN, printing an extra line per call. The emitted C passes
  a void expression where an `int` is wanted; gcc refuses that, so the host tests
  never saw it, and the target's compiler accepts it and prints whatever was in the
  register. Reported from a P2, where two `println(v.foo())` calls each printed a
  spurious `0` after their real output.

- **A conversion records the type it converts to**, so `v := X(0)` gives `v` the type
  `X`. It used to leave the variable with no recorded type at all, which silently
  skipped every check that keys on one — a method call on such a variable went
  unchecked to the C compiler, including the no-value call above. `v.nosuch()` on one
  is now `type X has no method nosuch`. The same silence `p := P{1, 2}` had before
  v0.9.0, in the one initializer shape that still had it.

### Fixed

- **A slice expression that is refused says "cannot slice", not "cannot index".**
  `n[0:1]` on an `int` was told it could not be *indexed* — an operation the program
  does not contain. The verb now follows what was written, as Go's does.

- **A refused index, slice or dereference names the operand's type**, `cannot index n
  (variable of type int)`, where it used to name only the operand. That is the part
  the reader does not already have: the operation is refused *because* of the type.
  It reaches the pointer cases too, `cannot index p (variable of type *int)`.

  A COMPOSITE operand still gets no type. The checker reduces a type to a
  predeclared `Kind` to name it, and a slice, an array or a struct reduces to none —
  so those say `cannot indirect xs` with no parenthetical, rather than inventing a
  name. Three messages in the family also still differ from Go's in shape; both are
  written down in `specs.go`.

- **A suffix that does not apply to its operand now names the operand.** `q.n[0]`,
  `q.n.f`, `q.n()` for an `int` field, and the assignment forms — all programs Go
  rejects — used to answer `unsupported expression node FactorSuffix`, naming an
  internal AST node in source that contains no such thing. One of them, `q.n.f = 1`,
  reached the C backend instead. They now say `cannot index q.n`, `type int has no
  field f`, `cannot call non-function q.n`, at the position Go reports.

  A field's type is read off the struct declaration, so the checker answers for a
  predeclared one — the same twin-of-`checkResultSuffix` treatment a call's result
  already had. A composite field type reduces to no `Kind` at all, so those reach
  the emitter, which walks the chain and reports the first step the value cannot
  take: `q.xs has no field f`, `cannot index q.xs[0]`. Every position was checked
  against Go and matches.

### Testing

- **The fuzzer generates pointers to arrays.** `ogo smith` had no pointer variables
  at all, so the whole dereference surface v0.21.0 added was covered by hand-written
  tests only. It now emits `p := &a` and, in one block, writes an element through the
  pointer, reads that element back through the ARRAY's own name, reads one through
  the pointer, takes `len(p)` and ranges it — so the checksum disagrees if a
  dereference is dropped, if the pointer copies instead of aliasing, or if the extent
  is wrong.

  The aliasing needed no modelling: the generation-time VM already represents an
  array by a pointer type, so binding the pointer to the same value is what makes a
  write through either name visible from the other. 5,600 seeds and 24 programs on a
  P2-EDGE found nothing, which is the expected result for code that shipped with
  tests — the point is that it is now covered on every run, and
  `TestGeneratorCoverage` asserts all four operations still appear.

## v0.21.0

Pointers — and the operations C will perform on one that this language does not.

The feature is a pointer to an ARRAY. `*[3]int` passes an array by reference without
a slice header, and it abbreviates the dereference exactly as Go does, so `p[i]`,
`len(p)`, `range p` and `p[lo:hi]` all mean the array it points at. The
representation was settled and measured in v0.20.0; what was owed was the
dereference surface, and that turned out to be the whole cost of the feature.

Adding it needed the other half done first. `p[i]` was accepted for **every** pointer
type and emitted C's index, and C indexes any pointer as the array it is not: off a
`*int` it read whatever storage followed the pointee and wrote there, silently, in a
program Go rejects outright. That had to be refused before the one indexable pointer
could be added, because the emitter cannot tell Go's abbreviation from C's pointer
arithmetic — in C they are the same operation. Dereferencing a non-pointer was the
same shape of hole one operator over, and is refused now too, wherever the operand is
written rather than only where it is a bare name.

The written-out dereference `(*p).x` also works, which is not a nicety: for a pointer
to a slice or a string it is the only spelling there is, so those two types had no
element access at all. It had never worked because the parentheses were being peeled
as redundant — and they are not, `(*p).x` peeled to `*p.x` being `*(p.x)`.

### Language

- **A pointer to an ARRAY**, `*[3]int` and `*Row`, which passes an array by
  reference without a slice header — what the array-by-value refusals used to point
  at second-best. It abbreviates the dereference exactly as Go does: `p[i]` is
  `(*p)[i]`, and so are `len(p)`, `cap(p)`, `range p` and `p[lo:hi]`. `*p` copies
  the array, as assigning one does, while copying the pointer aliases it. It works
  as a parameter, a result, a struct field and a package variable, at any rank, and
  the index is bounds-checked against the pointee's extent like any other.

  C spells the type `int (*p)[3]` — the name in the middle of the declarator — so
  the pointee takes the same generated typedef a slice's array element does. That
  representation was settled and measured in v0.20.0; what this release adds is the
  dereference surface, which is where the whole cost of the feature was. Landing the
  type alone would have been worse than the refusal it replaced, since `p[1]` then
  emits C's `p[1]` — the ARRAY at offset 1 — which compiles silently and prints
  nothing like what Go prints.

- **A dereference may be written out and carry a suffix**, `(*p).x`, `(*p)[i]`,
  `(*p).m()` — what Go's `p.x` and `p[i]` abbreviate. For a pointer to a SLICE or a
  STRING it is the only spelling there is, an index on the pointer itself being no
  operation in either language, so those two types had no element access at all
  before this. Reading, writing, `len`, `cap`, `range`, a slice expression, a method
  call and a nested `(*(p)).x` all work, for every kind of pointee.

  The family used to fail with `unsupported expression node FactorSuffix`, naming a
  node the source does not contain. The cause was a peel: the parentheses were
  dropped as redundant, and they are not — `(*p).x` peeled to `*p.x`, which Go reads
  as `*(p.x)`. They are kept now, and a leading unary operator is what marks them as
  load-bearing.

### Behaviour changes

- **Dereferencing something that is not a pointer is refused wherever the operand is
  written**, not only where it is a bare name: `*q.xs` for a field, `*q.xs[0]` for an
  element, `*mk()` for a call's result, and the assignment forms of each. Go rejects
  all of them.

  They used to reach the C backend, which answered `invalid type argument of unary
  *` — a diagnostic about the emitted C in a program that should never have got that
  far. The cause was that the check asked for the operand's *Kind*, and a Kind
  answers only for a predeclared type: a slice, an array or a struct operand read as
  "type unknown", and unknown is not the same as "not a pointer". It now asks about
  pointerness, which a field's written type answers even when it is composite. A
  bare name is also named in the message where it used to say the word `operand`.

- **Indexing a pointer that is not one to an array is refused**, `p[i]`, on both the
  read and the assignment side — the other half of the entry above, and the reason
  it had to be written first. Go admits an index on a pointer to an ARRAY and on no
  other pointer. What a pointer points at is still reached by `*p`, and a field of a
  pointed-to struct by `p.field`, neither of which changes.

  It was accepted before, and what it did was C's: the emitter rendered the index as
  C's, and C indexes any pointer as the array it is not. `p[1]` off a `*int` read
  whatever storage happened to follow the pointee, `p[1] = v` wrote there, and both
  compiled and ran without a word — the silent-wrong-program case the compiler exists
  to prevent. Off a `*string` it read the header's own bytes as a number, off a
  `*struct` it read past the struct, and off a `*[]int` it emitted C that does not
  compile, so an internal defect surfaced as a diagnostic from the C backend. Go
  rejects every one of them.

  The refusal is split across the two stages that can each see part of the answer:
  the checker names the variable for a pointee its type model resolves, and the
  emitter — which carries the complete model of array types — refuses what reaches
  it. A pointer to a SLICE was the case that needed both, since `ogo_slice_int*`
  shares a prefix with the header type and was being mistaken for one.

### Fixed

- **`ogo fmt` spaced a `[` off the `*` or `&` in front of it**, writing `* [3]int`
  for `*[3]int` and `& [2]int{1, 2}` for `&[2]int{1, 2}`. Its rule for `[` asks only
  whether the previous token could end an operand — which is what tells an index
  from a type, and a `*` or `&` cannot. The pointer form was unwritable until this
  release, so nothing had run into it. The binary spellings keep their spaces,
  `n * [3]int{1, 2, 3}[0]` being a multiplication; both were checked against gofmt.

- **`ogo fmt` spaced an index off a composite literal**, `[3]int{1, 2, 3} [0]`. The
  same rule, and the same kind of gap: a `}` was not among the tokens an index may
  follow.

### Documentation

- **Where each backend is generated, and what a release does not test.**
  `internal/generator.go` said how each of the five flexcc backends is made and never
  which machine makes it; it now carries a table of target, machine and command, plus
  the prerequisites per target. Nothing regenerates them automatically and there is no
  CI here — deliberately, since the backends are committed Go and `make release`
  cross-builds every target from one host.

  `scripts/release.sh` now says what a release does not verify: of the five zips it
  publishes, exactly one can be run on the machine that built it. All five compile and
  their test packages typecheck under `GOOS=... go vet`; nothing else about the other
  four is checked per release, so a defect that compiles everywhere and misbehaves on
  one platform would ship unnoticed.

## v0.20.0

Arrays as element types. A slice or a channel may have an array element of any rank
— `[][2]int`, `chan [3]int`, `chan [2][3]int` — with every operation over them:
`make`, indexing, `range`, `append`, `copy`, reslicing, literals, a struct field of
that type, and `select` on the channel forms.

The slice half shipped in v0.18.0 and was withdrawn in v0.19.0 as unsound. That
withdrawal was right about the code and **wrong about the reason**, which is the more
useful half of this release: a pointer to a typedef'd array is fine on this target,
and what actually corrupted the program was a generated helper taking the element by
value — a parameter whose *type* is a typedef'd array corrupts unrelated code
elsewhere in the same translation unit, silently and non-locally.
`doc/array-param-corrupts.c` reduces that to thirty lines, gcc against board. Every
such helper takes a pointer now, which is also what made the channel forms possible.

The rule the fix came from is written down in `internal/octogo/octogo.go`: readable
generated C is a debugging aid, not the objective, and when a generated identifier or
type unblocks a construct the language needs, it wins — at the known cost of one name
per shape, consistent substitution, and care at link time.

### Language

- **A channel may have an ARRAY element**, `chan [3]int`, `chan Row`, `chan [2][3]int`. The rendezvous
  cannot copy one by value — C has no array assignment, and a parameter of a
  typedef'd array type miscompiles here — so the cell holds the array and the helpers
  take a pointer both ways. A receive therefore has no expression: `v := <-ch`,
  `w = <-ch` and `s.v = <-ch` write into storage the receiver already owns, and a
  receive in expression position binds a temporary. A `select` clause receives one
  too — its temporary is declared with the element's extents and the try-receive
  fills it — in the declaring and assigning forms, with or without a `default`.

- **A slice may have an ARRAY element**, `[][2]int`, `[]Row`, `[][2][3]int`. `make`,
  reading, writing, `range`, `append`, `copy`, reslicing, passing to a function, a
  struct field of that type and a literal all work. C cannot spell an array inline
  where the slice header's pointer goes, so the element takes a typedef; the helpers
  that would take it *by value* take a pointer instead, since a parameter whose type
  is a *typedef'd* array corrupts unrelated code on this target — the spelled-out
  `int v[2]` is fine, which is why nothing else in the compiler was affected. This shipped in v0.18.0, was
  reverted in v0.19.0 on a wrong diagnosis, and is back with the actual cause fixed —
  the whole surface is checked against `go run` and on a P2-EDGE.

### Documentation

- **The slice-of-arrays revert was misdiagnosed**, and the record is corrected. A
  pointer to a typedef'd array is *fine* on this target — measured on a P2-EDGE. What
  broke the implementation was the generated `append` helper taking the element by
  value: **a parameter whose type is a *typedef'd* array corrupts unrelated code
  elsewhere in the same program**, silently, and the wrong value is not even stable
  across unrelated edits. The spelled-out `int v[2]` and `int v[]` are both fine,
  which is why nothing else in the compiler was affected. `doc/array-param-corrupts.c` isolates it in thirty lines, gcc
  against board. ogo has always avoided array parameters for user functions — a
  `func take(a [3]int)` is emitted as `int take(int* _ogo_a)` — so the only one it
  ever emitted was in that helper. The feature is implementable after all; the revert
  itself stands, since the code that shipped was genuinely wrong.

- **A pointer to an array is documented as unsupported**, `*[3]int`. It always was,
  but no bullet said so, and it is what the array-by-value refusals point at. The
  representation is settled and measured; what is missing is that Go's `p[i]` means
  `(*p)[i]`, so every path that indexes, assigns through, measures or ranges a base
  would have to render the dereference. Pass a slice of the array or a pointer to a
  struct holding it.

## v0.19.0

The release that went looking for miscompiles and found seven. A struct holding a
field narrower than a machine word — a `bool`, an `int8` — was mishandled in five
places on the target, silently: returned from a call, passed by value, through an
interface method, compared, and as a value receiver. Each gave the wrong answer for
the narrow field while the `int` beside it survived, so half of every value was
right. Two functions whose result lists spelled the same struct name shared one
struct, truncating an `int64` result. All are fixed, and
`doc/return-nonword-struct.c` tabulates which of the eight positions the backend
warned about and which were actually broken — five and three, so its diagnostic is a
signal rather than a verdict.

One feature was withdrawn. A slice whose element is an array shipped in v0.18.0 and
is gone: the emitted C is correct and gcc runs it, but the target's compiler
mismodels a pointer to an array and indexes by the wrong size — not uniformly, which
is what makes it unshippable. A small program answered correctly and a slightly
larger one silently did not.

What was added is mostly reach: an array result is now used like any other value, a
`go` statement starts a function held in a value, a `for` header declares and
assigns several names, and array literals stand where array variables do. With `go`
through a value, every function-valued form works.

All three documents were re-measured rather than remembered — the README, the
language spec and this file — and eight claims across them turned out to be stale or
never true.

### Language

- **A `range` clause may assign into a struct field**, `for s.i, s.v = range xs`.
  The emission already rendered its targets as lvalues; only the guard ahead of it
  required a bare name. An element target, `for a[0] = range xs`, is still refused —
  indexing renders a bounds-checked read, which is not a place to write.

- **A call returning an array may be used like any other value.** Read where it
  stands — `mk()[1]`, `x := mk()[0]`, `for i, v := range mk()`, `t.row()[0]` — and
  handed on whole: `b = mk()`, `g = mk()`, `s.v = mk()`, `take(mk())`, and
  `return mk(k)`. C cannot return an array, so the result travels through an out
  parameter the caller supplies; where the target *is* storage the call writes
  straight through it and nothing is copied, and where it is not the result binds to
  a temporary. The one form still refused is an array *beside* another result.

- **`go` may start a function held in a value**, `go g(21)` for a variable or a
  struct field holding one. A cog's entry point is generated per function, which is
  what left this out: a value has no name to generate one against. It is generated
  against the function *type* instead, and the pointer travels in the argument block
  with the arguments — read at the `go` statement, so reassigning the variable after
  it changes nothing, as in Go. Every function-valued shape is accepted now.

- **A three-clause `for` header may declare and assign several names**,
  `for i, j := 0, 9; i < j; i, j = i+1, j-1`, any number of them. A multiple assignment cannot be C's
  third clause — Go assigns simultaneously, which needs temporaries, and that clause
  is an expression — so the post statements go at the end of the body behind a label
  that `continue` jumps to. A multi-name init becomes a block around the loop, which
  is also where Go scopes those names.

- **A pointer to an array is refused by name**, `*[3]int` and `&a` for an array
  variable, where the messages used to be "cannot infer a type for the declaration
  of p" and "unsupported type" with an empty name. It is the same C shape the entry
  below is about — `int (*p)[3]` puts the name in the middle of the declarator — so
  it was never supported; only the diagnostic is new. The refusal names the two
  forms that do work: a slice of the array, `a[:]`, and a pointer to a struct
  holding it.

- **A slice or channel whose element is an ARRAY is refused by name**, `[][2]int`
  and `chan [3]int`, rather than reported as "unsupported type" with an empty type
  name. The slice form was implemented and then reverted: the emitted C is correct
  and gcc runs it, but the target's compiler models a pointer to a typedef'd array
  as a pointer to a *pointer* and indexes it by the wrong size — not uniformly,
  which is what makes it unshippable. On a P2-EDGE a small program gave the right
  answers and a slightly larger one silently gave `36` where Go gives `14`. The
  measurement and what a viable representation would have to avoid are in
  `doc/slice-of-arrays.c`; the workaround is a struct wrapping the array.

- **An array literal may be a comparison operand**, `a == [3]int{1, 0, 0}`, in
  either position and with `!=`. It binds to a temporary and the per-type helper
  compares the two, so an array literal now stands wherever an array variable does.

- **A literal read through more than one step types**, `[2]P{{1, 2}, {3, 4}}[1].y`
  as a declaration's initializer. The first index reaches the element and the rest is
  walked by the same chain typer a variable's suffix uses; only a single index was
  handled before.

- **A multi-result call may be forwarded as a return**, `return f()`, where the
  callee's results are exactly the caller's. Both functions return the same C struct
  — result structs are keyed by the result types — so the call is the return value.

### Documentation

- **The README's status list re-measured.** Every claim in it was built rather than
  read, and four were stale: a multi-result function as a value, `go` through a
  variable, the message a slice or channel of arrays gives, and a call returning an
  array. Two "works" bullets were understating. The claims that survived — `goto`, a
  select with a send and a default, an array beside another result — were measured
  too.

- **`specs.go` re-measured.** The language spec still said a function with more than
  one result could not be a value and that `go` needed a declared callee — both have
  worked for several commits — and that an **array of slices** is unsupported, which
  it never was: `var m [2][]int` works, each element being an ordinary slice header,
  and there is now a run case for it on real hardware. The slice-of-arrays sentence
  beside it gave the wrong reason as well; the measured one is in
  `doc/slice-of-arrays.c`.

### Fixed

- **A parenthesised expression may carry a suffix through any number of layers**,
  `((a))[1]`. v0.18.0 peeled one pair and refused the rest.

- **A parenthesised operand is checked like the bare one**, so `(s).nosuch` is
  reported as the field that does not exist. In v0.18.0 it reached the backend, which
  answered `unsupported expression node FactorSuffix`: the checker's field, method
  and call checks all key on a leading identifier, and a parenthesised operand has
  none.

- **Two functions whose result lists spell the same struct name shared one struct**,
  silently. The struct is named after the result types run together, and `(a_b, int)`
  and `(a, b_int)` both read as `a_b_int` once a type name contains an underscore —
  so the second function got the first one's layout, and an `int64` result came back
  truncated to 32 bits. A name already standing for a different list is numbered
  apart now, and the lookup is by the list rather than by the name so one list always
  answers with one struct. Present in v0.18.0, which is where result structs became
  shape-keyed.

- **A struct holding an array is refused at every by-value boundary**, not just at a
  parameter and a result. A **value receiver**, a **channel element** and a
  **literal's element** reached the backend instead, which answered `Internal error,
  couldn't find object variable with offset 4` or `incompatible types`. They are
  reported where they are written now, with the reason and the advice the other two
  already gave — use a pointer. Nothing that worked stops working: copying such a
  struct between variables is a `memcpy` the emitter writes itself, a pointer
  receiver copies nothing, and a literal written in place *is* the storage.

- **A struct with a field narrower than a machine word was mishandled in five
  places**, on the target only, and a sweep of every position it can reach found
  them all. Beside the direct return below: passing one **by value from a call**,
  `take(mk(3))`; returning one **through an interface method**; **comparing** two,
  `mk(3) == mk(3)`, which answered false for equal values; and using one as a
  **value receiver**, `mk(-5).flag()`. Each gave the wrong answer for the narrow
  field on a P2-EDGE while the `int` beside it survived, so half of every value was
  right. All five are fixed by binding the call to a temporary. Two run cases
  exercise the lot on real hardware, and `doc/return-nonword-struct.c` tabulates
  which of the eight positions warned and which were actually broken — eight, five
  — so the backend's diagnostic is a signal, not a verdict.

- **A struct returned straight from a call lost any field narrower than a machine
  word**, on the target only. `func fwd(n int) S { return mk(n) }` for an `S` with a
  `bool` field gave `false` for it on a P2-EDGE where Go and gcc give `true` — the
  `int` beside it survived, so half the value was right. The backend warned
  (`incompatible pointer types in return`) and the warning turned out to be a true
  signal. The call is bound to a temporary before the return now, which is correct
  and compiles silently; `doc/return-nonword-struct.c` has the measurement. Present
  in v0.18.0 and earlier.

## v0.18.0

A release about what a value may be and where it may stand. An array can be a
function result, an array literal stands wherever the position copies, a channel can
live in a struct field, a defined channel type can have methods, and a function with
more than one result is finally a value like any other.

Two of them came from reading a refusal's own comment and finding the obstacle had
been removed by an earlier commit — a defined channel type's methods, and the
"documented limits" note that turned out to describe two bugs already fixed.

The `p2` package is embedded OctoGo source now rather than a table the compiler took
on trust, so a misspelt intrinsic is a compiler error and a call into it is checked
against a real signature. Cross-package calls are checked the same way.

The parser gained a rule of its own: where an LL(1) grammar cannot describe a
spelling, the parenthesised form may be required. `([]int)(xs)` works, `[]int(xs)`
does not, and both are valid Go — so a program obeying the restriction compiles in
both places. See "Parentheses where the parser needs them" in `specs.go`.

Diagnostics got attention too. A file that does not parse is no longer type-checked,
so a syntax error stops being buried under the errors it caused, and an identifier
used as a type now says what it actually is.

### Language

- **A conversion to a defined array type may be indexed, measured and ranged where
  it stands.** `Row(r)[2]`, `len(Row(r))`, `cap(Row(r))` and `for i, v := range
  Row(r)` all work for `type Row [3]int`, including through more than one step --
  `Grid(g)[1][2]`, `Pts(ps)[1].y`. It previously had to be bound to a variable
  first. C has no cast to an array type, so the conversion is unwrapped rather than
  emitted: the typedef stands for the same storage, so the operand's own access
  chain is what the steps apply to.

  The three shapes Go rejects are refused with Go's reason rather than accepted: a
  conversion's result is a value, not a variable, so `Row(r)[0] = 7`, `&Row(r)[1]`
  and `Row(r)[0:2]` each say it is not addressable.

- **A deferred print may take arguments.** `defer println("x:", x)` was refused;
  Go evaluates a deferred call's arguments at the `defer` and runs the call at the
  return, which is the whole point of deferring a print, and it is now what happens.

  The refusal was hiding a real problem rather than a missing feature. The print
  path renders per-type `printf` calls of its own and never consulted the
  temporaries the defer machinery captures, so the arguments would have been
  re-evaluated at the return — and worse, a deferred call is emitted *after* the
  body's block scope has been left, where a local's name no longer types at all. A
  string would have printed as the first word of its header and a bool as `1`. The
  captured temporary carries its type, so that is now what chooses the format.
- **The `p2` package is real source now.** It was a table in the compiler that the
  checker took on trust: any exported `p2.X` was admitted, and a misspelt intrinsic
  reached the C compiler as an unknown symbol. It is now an embedded package like
  `testing`, declaring every function *bodyless* — the grammar's form for a function
  implemented elsewhere — and every constant.

  So the checker sees real signatures, `p2.Nope()` is *undefined: p2.Nope* from the
  compiler, an unused `import "p2"` is reported like any other, and the constants
  work in a `const` declaration, which is where a pin mode belongs. Nothing of the
  package is emitted: every declaration is substituted at the use, a function by its
  C intrinsic and a constant by its value.

- **A defined type over a channel may have methods.** `type Ch chan int` with
  `func (c Ch) Send(v int)` — which is what makes a channel a named thing with
  behaviour rather than a bare pipe.

  It was refused because such a type had no C name of its own: it was answered for
  by the channel cell's, so two defined types over the same element would have
  shared one method namespace. It has a typedef now, which it could not have while
  the channel's own typedef was emitted *after* the typedef section — moving that
  (for a channel held in a struct field) is what made this possible, and a
  dependency is what orders the two.
- **An array may be a function result.** `func mk() [3]int` was refused; it now
  works, for a function and for a method, in one dimension and in several.

  C cannot return an array, and the obvious workaround — wrapping it in a struct, as
  a multi-result function's results already are — is a shape this backend refuses to
  assign (*Unable to multiply assign this target*, measured again before designing
  around it). So the result travels through an **out parameter**: the caller owns the
  storage and the callee fills it, which is the answer C has always had. Binding one
  therefore costs a call and no copy — the declaration *is* the storage.

  What that leaves is that the call is a statement rather than a value. `a := mk()`
  and `var a [3]int = mk()` work; `take(mk())`, `b = mk()` and `mk()[1]` are refused
  with the way out. An array *beside another result* stays refused outright, since
  handing back a pointer would name the callee's dead frame.
- **A call into another package is checked against that package's signature.** The
  argument count always, and an argument's type where the parameter's resolves —
  which for a cross-package call means resolving the parameter types in the
  *callee's* scope, since `geo.Vec` is `Vec` there. Before this, `geo.Twice(1, 2)`
  reached the C compiler as *Bad number of parameters in call*.
- **A constant of an imported package may be used in a `const` declaration.**
  `const limit = geo.MaxPoints` reported *undefined: geo* — imports live in the file
  scope, which a constant expression's resolution never reached. A constant is a
  value and not a symbol, so there is nothing to link: it folds to its literal, which
  is what C needs at file scope anyway.

  `p2` is the exception and now says so instead of calling the package undefined: it
  has no source, so its constants are a table in the compiler with no scope to find
  them in. Written where they are used they are ordinary values, which is how
  `_examples/gopher` spells its pin mode.
- **A channel may be held in a struct field.** `p.tx <- v` and `<-p.rx` work, for a
  written `chan T` and for a defined type over one, on a package-level variable and
  on a local, nested structs included.

  There turned out to be no design question. A channel is already a *pointer* to its
  rendezvous cell — it has to be, or handing one to a goroutine would hand it a copy
  — so a field holding one needs no new representation, and **copying the struct
  shares the channel**, which is exactly what copying a channel does in Go. The one
  rule is where the cell comes from, and it is the rule a channel variable already
  obeys: the *declaration* owns it. A struct type allocates nothing; two variables of
  one type have a channel each.

  **A `select` takes one too** — a receive clause binding a value, a send clause and
  a bare receive alike — which is what a driver's ports want: several channels
  belonging to one thing, waited on together.

  **An array of such structs** works too — `ws[i].cmd <- v`, a constant index or a
  variable one — which is a worker per element, each with channels of its own. On a
  part with eight cores that is the shape the hardware suggests. The array's
  declaration owns a cell per element per field, the same rule applied once per
  element.
- **Two documented limits were gone and nobody had noticed.** A call *three deep*,
  `chooser()(0)(6)`, computed 0 on the board at every optimization level while the
  host was right — a silent wrong answer — and a call written directly on an array
  element of function type, `fns[0](8)`, was refused by the target's C compiler.
  Both were fixed by the temporary added for the dispatch-table defect in v0.17.0,
  which binds an intermediate rather than calling straight through it: the manual
  workaround both notes prescribed is now what the compiler emits.

  Re-measured on hardware and pinned as a run case, since the first was the silent
  kind. The other eleven claims in the README's "does not work yet" list were
  re-measured at the same time and all eleven still hold.
- **The `p2` package exports named pin-configuration constants** — `p2.DAC990R3V`,
  `p2.DACDitherPWM`, `p2.OutputEnable` and the rest of the DAC set, values from
  flexcc's `smartpins.h`.

  They exist because the hex they replace is unforgiving in a way that looks like
  working code. `_examples/gopher` was first written with the mode word `0x140006`
  — the DAC range and the smart-pin mode, and no output enable — which compiles,
  runs, drives nothing, and puts about twenty millivolts of dither ripple on the
  pin. On a scope that reads as a bug in the drawing rather than as a pin that was
  never switched on. `p2.DAC990R3V | p2.DACDitherPWM | p2.OutputEnable` cannot be
  written with a bit missing without the name of the missing bit being absent from
  the line.

  Usable wherever a value is, including `var` and `:=`. Not in a `const`
  declaration: the constant evaluator does not resolve an import qualifier, which is
  a general gap rather than a `p2` one.

- **A function with more than one result may be used as a value.** `f := divmod`,
  `var g func(int) (int, bool) = flags`, a struct field of that type and a parameter
  of it all work, and a call through one destructures as a direct call does. The
  result struct is keyed by the result *types* rather than by the function, so two
  functions of one signature return the same C type and the signature has a typedef
  to name — which is what the refusal was about. `go` through a function value is
  still refused: starting a cog needs the callee's name.

- **A parenthesised expression may carry a suffix**, `(a)[1]`, `(s).v`, `(dbl)(21)`.
  These are ordinary Go and were rejected as syntax errors: the grammar's
  parenthesised alternative had no suffix at all.

- **A literal of a bracketed type may be read where it stands**, `[]int{1, 2, 3}[1]`,
  `[3]int{4, 5, 6}[2]`, `[2]P{{1, 2}, {3, 4}}[1].y`. The literal binds to a temporary
  and the suffix reads that, which is the only way an array literal can be indexed --
  C has no array value for an index to apply to.

- **A conversion to an unnamed composite type works when parenthesised**,
  `([]int)(xs)` and `([3]int)(q)`. Both are valid Go. The bare `[]int(xs)` still does
  not parse, and now says so as a deliberate restriction rather than an accident: it
  is the one spelling that costs the LL(1) grammar conflicts, and specs.go's
  "Parentheses where the parser needs them" records when such a restriction is
  allowed at all. `f := divmod`,
  `var g func(int) (int, bool) = flags`, a struct field of that type and a parameter
  of it all work, and a call through one destructures as a direct call does. The
  result struct is keyed by the RESULT TYPES rather than by the function, so two
  functions of one signature return the same C type and the signature has a typedef
  to name -- which is what the refusal was about. `go` through a function value is
  still refused: starting a cog needs the callee's name.

- **An array literal stands as a value where the position copies.** `take([3]int{1,
  2, 3})`, `a = [3]int{1, 2, 3}`, `s.a = [3]int{1, 2, 3}` and `return [3]int{1, 2,
  3}` all work; each binds the literal to a temporary of the frame that the copy
  then reads. It is still refused as an operand of an expression, where there is no
  copy to bind it for and C has no array value for it to become.

### Behaviour changes

- **Assigning one array to another emits a copy**, `memcpy`, where it emitted C's
  `a = b`. That is not valid C -- gcc rejects it with "assignment to expression
  with array type" -- but flexcc accepts it as an extension and copies, so the
  board was right while no host test could cover the form at all. It applies to a
  struct field target too, `s.a = b`. Behaviour on the board is unchanged; the
  emitted C is now C.

- **A name C has reserved is renamed in the emitted C wherever it appears**, not
  only at file scope. A local, parameter, struct field or method named `static`,
  `char`, `long`, `do`, `printf`, `NULL` or any other C keyword or unshadowable
  macro used to reach the backend verbatim and fail there, with a C syntax error
  against generated code. They are ordinary OctoGo identifiers and now work in
  every position. Top-level names of C *type* keywords (`var char = 7`) were
  broken too, and are fixed by the same change.

  An ordinary library function -- `memcpy`, `atoi`, `strlen` -- is still left
  alone as a local or a field, since shadowing is all C needs there; only a
  file-scope symbol of that name moves, as before. The exception is `memcpy`,
  which the emitter itself calls inside a user function body to copy an array,
  so a local of that name would shadow the emitter's own call.

### Diagnostics

- **An identifier used as a type says what it is instead**, `int (local variable)
  is not a type` rather than `int is not a type`. A predeclared name is not
  reserved -- `int := 7` is legal, here as in Go -- but it costs the name its
  meaning for the rest of the block, and the error then lands on a later use that
  can be a long way from the declaration that took it. Each wording matches gc's
  for the same program: a parameter, a result variable, a local and a
  package-level variable are named as themselves, and a built-in function is
  distinguished from a declared one.

- **A package name used as a type is reported by the checker**, `p2 (package name)
  is not a type`. `var a [2]p2` reached the backend instead and was called an
  unsupported type: the checker recorded the qualifier and waited for a `.T` that
  never came, and nothing reported the wait.

- **A file that does not parse is no longer type-checked**, so a syntax error is
  reported alone instead of beneath the errors it caused. `a := [3]int(q)` reported
  `undefined: Row` against a declaration three lines *above* the syntax error --
  the broken parse had cost the whole file its type declarations, so the checker
  complained about the consequence and the reader had to find the cause. A file
  that parses is still checked: one bad file does not silence its siblings.

## v0.17.0

The release interfaces landed in, and with them everything that was waiting behind
them: type assertions and type switches, and then the four language features that
had been on the "not yet" list longest — variadic parameters, function literals,
struct embedding and anonymous struct types. `ogo test` runs a package's tests on
real hardware, which was the last CLI stub.

### Language

- **Interfaces work.** An interface value is a pointer to what it carries beside a
  pointer to a statically emitted vtable; a method call through it is an indirect
  call; a pointer meeting an interface parameter is wrapped where it stands; and one
  interface value assigned to another is the two words, copied. One vtable is emitted
  per (concrete type, interface) pair, with a thunk per method — that is where the
  receiver difference is spent, so the call site never has to know whether it reached
  a value or a pointer receiver.

  What goes into one is a pointer and only a pointer; see the entry below for why.
  The variable it points at must outlive it — recorded as the ordinary provenance
  mark, so every lifetime sink already asks about it.

  Go's method-set rule is kept: a value of `T` carries the methods declared on `T`
  and `*T` carries all of them. Since only `&x` is admitted and `*T` carries every
  method, the rule now rejects nothing — it is what makes the address correct rather
  than a source of diagnostics. Type switches, type assertions and devirtualization
  are not part of this.
- **Only a pointer goes into an interface.** `var s Shape = &q`, never `= q`. Go
  accepts both and copies the value in, allocating for it; there is no heap here to
  allocate into, so the value form would have had to be a *reference* to `q` — and
  then writing to `q` afterwards would show through the interface, where Go kept the
  old value:

  ```go
  var q sq
  q.n = 3
  var s Shape = q
  q.n = 5
  println(s.Area())   // Go: 9. This compiler printed 25.
  ```

  Matching Go would need a copy per *assignment*, and per assignment is what a heap
  is for — one temporary per assignment *site* is not the same thing, since a site
  that runs twice with both values live needs two. So there is no version of the
  value form that means what Go means, and a silently different answer on a board is
  the worst thing this compiler can produce. Refusing it costs a `&` that Go does not
  need and buys the property worth having: **a program that compiles here means
  exactly what it means in Go.** The diagnostic names the fix.
- **An interface value goes where a value goes**, not only into a variable of its
  own: returned from a function, held in a struct field, held in an array or slice
  element, and sent on a channel to another cog. Each is the same two-word copy —
  there is nothing to box — but each asked a different part of the emitter to know
  that the *target's* type is an interface, which is what says the concrete value
  reaching it has to be wrapped.

  A method call through a chain (`sc.first.Name()`, `shapes[1].Area()`) is typed by
  the slot the interface declares. Before this it went untyped and fell back to the
  base identifier's type, so a `string` result printed as the two integers of its
  header and a short declaration off one was refused outright.
- **`&T{...}` may meet an interface**, and is how a fresh value gets into one. Go
  allocates for it; here it is a temporary of the enclosing function, which is
  exactly what a local is, so the lifetime rules already cover it — an interface made
  from one may not outlive the function. A call's result has no address in Go either:
  bind it to a variable and take that.
- **A package variable of interface type is initialized.** `var g Shape = &gq` used
  to reach the C compiler as invalid C, in the backend's words. An address is not a
  C constant expression, so its two words are written at package initialization
  instead. `&T{...}` is refused there, a temporary at package scope being a local of
  the synthesized init function.
- **A type assertion**, `x.(*T)`, recovers the pointer an interface value carries,
  in both of Go's forms — `q, ok := s.(*sq)` reports whether it held and leaves `q`
  nil when it did not, and `q := s.(*sq)` panics when it does not, as Go's does.
  The asserted type is a pointer type, a pointer being what went in.

  One vtable is emitted per (concrete type, interface) pair, so the whole test is a
  pointer comparison of the value's second word: no type id to read, no name to
  compare, no registry to keep in step.

  Four things are refused with the reason: asserting a type that could not supply
  the interface's method set (Go's *impossible type assertion*, which says the
  program means something it cannot have meant), asserting on an operand that is not
  an interface, writing the value form `s.(sq)`, and binding more than two names.
  The asserted value carries its type, so a field read off it is checked too.
- **A type switch**, `switch v := s.(type)`, switches on an interface value's
  dynamic type. Each clause binds the name at the type that clause proved — the
  concrete pointer where one type was named, the interface value where several were
  or none — which is Go's rule and the reason a clause is a scope of its own. `case
  nil` takes the zero interface value. The name may be left out, `switch s.(type)`,
  when the clauses only need to select.

  It lowers to the chain of table comparisons it is, one per case, so it costs what
  the assertion costs times the number of clauses tried.

  Refused with the reason: a case naming a type that could not supply the
  interface's method set (Go's *impossible type switch case*), a case named twice, a
  switch on something that is not an interface, and a bound name no clause uses.

- **Function literals.** `f := func(a int) int { return a * 2 }`, and the literal
  handed to a parameter, returned, stored in a field or an element, or called where
  it stands: `func() int { return 7 }()`. C has no nested functions and this
  language has no closures to need them, so each literal is lifted to a function of
  file scope and the expression becomes its name — a function pointer and nothing
  else.

  A literal may not read a local or a parameter of the surrounding function. There
  is no heap to hold a captured frame and no frame that outlives the call, so the
  pointer would be the only honest part of a closure; the attempt is refused where
  it is written, naming what was captured. A package-level name is not a capture.

  A literal may also stand as the callee of `go` or `defer` — `go func() { ... }()`
  and `defer func() { ... }()`. Both take a declared function, and a lifted literal
  is one. Neither takes arguments yet; a cog usually wants none, since what it
  shares it shares through a channel.
- **Method values**, with the receiver bound. `f := gp.bump` takes a method as a
  value; the compiler lifts it to a function of its own naming the receiver, so the
  value stays an ordinary one-word function pointer — usable in a variable, an
  argument, a dispatch table — and costs nothing that any other function value pays.

  Two forms are refused, with the reason: a *value*-receiver method, because Go
  copies the receiver at the moment the value is made and there is no heap to copy
  into (binding the address would alias, and diverge the moment anything wrote to
  the variable); and a receiver that is not a package-level variable, whose address
  does not outlive the value.

  Go carries the receiver *in* the value, which handles any receiver. That
  representation was measured on a P2-EDGE first: it costs about a quarter of the
  time of **every** call through a function value, so it was declined and the bound
  form built instead. `doc/funcval-cost.c` has the numbers and how to revisit it.
- **Variadic parameters.** `func sum(xs ...int) int` takes the rest of a call's
  arguments, however many — none included — and inside the body the parameter *is* a
  `[]int`, so `len`, `cap`, `range` and indexing all ask a slice. A call may spread
  an existing one instead, `sum(xs...)`. Functions and methods both.

  Go allocates the pack a call builds. There is no heap here, so it is an array of
  the *calling* function, and the lifetime rules see it exactly as they see a slice
  literal's backing: a callee that lets its variadic parameter outlive the call is
  refused, and told to pass a slice of a package array instead. The spread form is
  judged by where its slice came from, as any slice argument is.

  Refused with the reason: a `...` that is not the final parameter, one shared by
  several names, one in a result list, a call missing the fixed arguments before it,
  and a trailing argument of the wrong element type.

  `ogo fmt` writes `xs ...int` and `sum(xs...)`, which is what gofmt writes — the
  same token spaced two ways, told apart by whether it sits in a parameter.
- **Struct embedding.** A field written as a bare type name puts its own fields and
  methods on the outer type, at any depth, and is still reachable by that name when
  you want to be explicit. In C it is an ordinary member named after the type; what
  promotion costs is the members the source did not write — `d.n` becomes
  `d.middle.base.n`, and `d.Get()` becomes `base_Get(&d.middle.base)`.

  Go's selector rule is kept whole: one search over fields *and* methods together,
  shallowest wins, so a field on the outer type shadows a promoted one — and two
  reachable at the same depth are an **ambiguous selector**, refused rather than
  resolved by picking one.

  Not yet: an embedded pointer (`*base`), an embedded predeclared type
  (`struct{ int }`), or one named through an import. Each is refused where it is
  written rather than quietly embedded by value.
- **Anonymous struct types.** `var p struct{ x, y int }`, written where a type is
  wanted rather than declared with a name of its own — as a variable, a field of
  another struct, a parameter, or an array's element. As in Go, two of them are the
  same type when their fields match, so one is assignable to the other; the typedef
  is minted once per *shape* rather than once per mention.

### Tooling

- **`ogo test` runs tests, on the board.** It builds a package together with its
  `*_test.ogo` files and a generated runner, loads the result on a connected P2, and
  reports what the tests printed — Go's `--- PASS` / `--- FAIL` lines, and an exit
  status of 1 if any failed. The last CLI stub is gone.

  A test is `func TestSomething(t *testing.T)`, and `testing` is imported by name
  with nothing on disk: the compiler carries its source and compiles it like any
  other package, so the day a real one ships the only change is where it is read
  from. There is no `Errorf` — formatting needs allocation this target does not have
  — so a test prints with the builtin `println` and calls `t.Fail()`.

  **Tests run on the board and nowhere else.** A host emulation would be faster and
  would sometimes be wrong: the two C compilers disagree about semantics and not
  only about warnings, and this compiler has already shipped a feature that passed
  on the host and failed on hardware. A test reporting "ok" from somewhere the
  program will never run is worse than a test that did not run. `ogo test -c` builds
  without running, which is what CI without a board can honestly claim.
- **`p2.PinStart(pin, mode, x, y)`**, the canonical way to bring a smart pin up:
  mode, X, Y and the direction bit in one call, where doing it by hand took four.
- **A new example, `_examples/gopher`**, which draws the Go gopher on an
  oscilloscope in X/Y mode — two smart pins as DACs, one X and one Y, and the beam
  walking an outline that persistence turns into a picture. Eight frames of a dance:
  the head leans, the ears wag and the eyes look about. There is no framebuffer;
  the figure *is* the two voltages. `doc/gopher-gen.go` generates the point table
  and draws the same picture to a PNG, so the geometry can be checked without a
  scope.

  Photographed working on an FNIRSI 2C53T handheld. Getting there took two fixes
  worth knowing if you drive a scope from anything: the pin mode needs `P_OE` or the
  DAC does not drive at all (~20 mV of dither ripple looks convincingly like a
  signal), and a *digital* scope in X/Y mode still captures a window of samples — so
  the figure only appears if a whole frame fits inside it, and each point must be
  held long enough that the scope cannot miss it. Drawing fewer points and holding
  each one longer gives a better picture than the reverse.

  Every `_examples` program is now built by the test suite, with the same
  no-output-from-a-successful-build rule the on-board suite uses.

### Fixed

- **Fixed, on the hardware: a dispatch table did not dispatch.** A call made
  directly through an array element of function-pointer type reached the *wrong
  function* on the P2 — every element called whatever the first one held, with a
  constant index and a variable one alike, whether the table was filled by
  assignment or at package initialization. Silently, and since function values
  shipped in v0.13.0.

  The host C compiler gets the direct form right, which is why the emit-and-run
  tests passed and only the board disagreed — the reason that second suite exists.
  A call through an element now binds it to a temporary first, which is correct on
  both. Reduced to a dozen lines of C in `doc/call-through-array-element.c`, and the
  shape is now a run case in its own right.

### Diagnostics

- **An interface's method set is checked.** The declaration was already accepted and
  its duplicate methods caught, but nothing read the set: a call through a variable
  of interface type was "type Shape has no method Area" — true of the methods
  declared *on* the name, of which there are none — and a concrete value assigned to
  such a variable was not checked at all. A call is now resolved against the set and
  checked against that method's signature, and an assignment reports a type that does
  not implement the interface, in Go's words and with Go's `have`/`want` pair for a
  method whose signature differs.

  The rule is asked wherever a value meets an interface type — an assignment, a
  variable declaration, an argument and a return, the four positions the pointer-ness
  rule already covers — and an interface satisfies another when its own method set
  contains it, and a struct field of interface type asks it as well — filled by a
  keyed literal, by a positional one, or assigned afterwards. A channel send does not
  ask it yet: `chan Shape` does not retain its element type by name.

  This is the checker half. The representation is settled (a data pointer beside a
  static vtable) and not emitted yet, so a program that passes these checks stops at
  `interface types are not emitted yet` — which is also what replaced the emitter's
  `unsupported type ""`, a message that named the empty string for a type that has a
  name.
- **A channel send checks the interface it sends to.** `ch <- t` where `t` does not
  implement `chan Shape`'s element type came back from the emitter, in the emitter's
  words, rather than from the checker in Go's. It is now the same diagnostic every
  other position gives — a send statement and a `select` send clause alike, and
  through a defined type over a channel. What that took was retaining the element
  type's *name* on the declaration: a predeclared kind was all it kept, and a named
  interface has none.
- **A field read off an interface value is checked.** `s.n` on a variable of
  interface type went unchecked and surfaced from the emitter as a puzzle about C.
  An interface has methods and no fields; what it carries is reached by an assertion
  or a type switch.

### Behaviour changes

Programs v0.16.0 accepted that this release refuses. All three are the lifetime
rules reaching where they always meant to: a reference that does not outlive what
holds it, found one call further away than before.

- **A reference to a local may no longer be laundered through a call.** The
  lifetime rules refused `g = &x` — storing a local's address where it outlives the
  frame — but `keep(&x)`, where `keep` does that store, was accepted, and so was the
  same thing two calls away, through a slice, into a field or an element of a package
  variable, or wrapped in a struct. Every one of those built and ran with a dangling
  reference.

  The per-parameter summary that already carried the *cog-crossing* requirement back
  to the call sites now carries this one too: a parameter is marked when the callee
  stores it where it outlives every frame, and the mark travels the same call edges to
  a fixed point. Passing package storage to the same callee stays legal, as does a
  callee that only reads its parameter — the requirement is on the leak, not on the
  pointer.
- **A reference may no longer be laundered through a call's result either.**
  `func id(p *int) *int { return p }` made `return id(&x)` compile, and the same
  single call carried a frame reference past every other sink — into a package
  variable, to a goroutine, onto a channel. A second summary records, per parameter,
  whether a result derives from it, closed over the same call graph; the shared
  provenance predicate consults it, so all five sinks see it at once rather than each
  growing a case.
- **A call through a function value is judged by the function it holds.**
  `f := keep; f(&x)` was accepted where `keep(&x)` was refused, because the call site
  resolves to a variable and there was no summary to consult — a package-level
  function variable and a slice argument the same. The variable is bound where it is
  given a function outright, and rebinding it rebinds which summary answers; anything
  else clears the binding rather than leaving a stale one. A method is now resolved
  for the result half too, so `return r.id(&x)` is refused as the plain-function shape
  is.

  Three shapes remain open, all of them the callee this cannot name: a function value
  in a struct field, one arriving as a parameter, and a method result reached through
  another call. They are listed in `octogo.go`.

## v0.16.0

### Language

- **The typedef section is emitted in dependency order.** It was fixed groups —
  struct forward declarations, function typedefs, scalar slice headers, the named
  and struct typedefs, struct slice headers — and real dependencies cut across them,
  so each of these named a type C had not seen: a function type naming a defined
  type by value (`type Scale func(Word) Word`) or a slice of one, a struct field
  whose struct is declared further down the file, a struct holding a slice of such a
  struct, and a multi-result function whose results are defined types. A further
  group could not have fixed it — a struct holding a function type needs that
  typedef *between* two entries of the group it is in.

  Each declaration now carries the names it must see first, and the section is
  sorted on that, moving a declaration later and never earlier: a program whose
  declarations already ordered themselves emits what it emitted before. A pointer to
  a struct is the one use that depends on nothing, since every struct's forward
  declaration leads the section; a pointer to anything else names a typedef, which C
  wants declared first. That rule replaces the hand-written scalar/struct split of
  the slice headers, and the inline emission a struct-element slice field needed.

- **A `select` clause may receive into anything an assignment can write to**,
  `case s.last = <-a:`. The clause read only the head identifier off its target, so
  the received value was assigned to the *struct*, and the C compiler is what caught
  it. The plain `s.last = <-a` outside a select always worked.

- **`min` and `max` order what Go orders**, not integers alone: a float and a
  string are ordered too, so a control loop can clamp with `min(max(v, lo), hi)` —
  which is the reason most programs reach for them at all. `specs.go` already said
  "ordered arguments"; the emitter took integers. A string is ordered by the same
  byte comparison `s < t` uses. Each argument is still evaluated exactly once.
- **A goroutine may be launched on a method whose receiver is reached through
  fields and indexes**, `go ws[i].run(ch)` and `go p.ws[i].run(ch)` — one cog per
  element, which is what a worker pool looks like here. Only a method on a plain
  variable could be launched, so a pool had to be copied out to a variable one
  worker at a time. The receiver is evaluated where the `go` stands, as Go says, and
  the lifetime rule reads it as before: a pointer receiver on a *local* array's
  element is still refused, the cog outliving the frame.
- **A deferred method may be called on a local receiver.** `defer b.show()` for a
  local `b` did not compile at all — `unknown package "b"` — while the same call on a
  package-level variable worked.
- **Package variables are initialized in dependency order**, as Go does and as
  `specs.go` already claimed. They ran in source order, so a variable whose
  initializer named one declared below it read a zero — `var top int = mid + 1` was
  1 rather than 11, silently. Written-out variables also kept a non-constant
  initializer where C evaluates one at compile time, so the backend refused the
  program with a message about generated C; such an initializer is assigned at
  package initialization now, which is where the inferred form already went. A
  variable whose *type* must be inferred from a variable declared below it is still
  refused, by name.

### Testing

- **Two realistic programs are run cases.** A binary heap over a caller's array —
  sift up, sift down, a struct payload, pushes refused at capacity — and an
  integrator built from value-receiver methods returning structs, chained. Between
  them they cover most of what this release changed: element swaps through a slice
  held in a struct field, a two-result method on that field, an `if r := l + 1;
  r < h.n && …` header declaration, and a struct copy that has to stay a copy.
  Neither found a bug, which is what makes them worth keeping: they say those paths
  compose.
- **The fuzzer generates an element swap**, `a[i], a[j] = a[j], a[i]` — the one
  statement shape whose targets are lvalues rather than names, and the path most of
  this release's assignment work rests on. The VM swaps the same two values, so a
  compiler that assigned the first target before evaluating the second right-hand
  operand would leave both elements equal: an ordinary-looking answer and a wrong
  checksum. It appears in about a quarter of seeds; 1000 seeds agree, and the corpus
  still builds with the real backend and runs on the board.
- **The fuzzer generates `min` and `max`**, over two to four arguments. The builtin
  is lowered as a two-argument helper applied left to right, so `min(a, b, c)` is
  `min(min(a, b), c)` — an argument evaluated twice, or folded in the wrong order,
  would still look like an ordinary number. The VM picks the smallest of the same
  values, so it does not.
- **The fuzzer generates a deferred call**, in two shapes — a plain call and a
  method on a value receiver — and pins the one thing about `defer` a reader cannot
  see: the argument, and the receiver, are evaluated where the `defer` stands, not
  where the deferred call runs. The generated procedure changes the variable afterwards and
  folds the new value in itself, so both values reach the checksum — a compiler that
  re-read the variable at the return would fold the same one twice. It lives in a
  procedure `main` calls, because a `defer` in `main` runs after the checksum
  assertion and could not be observed at all. Every generated struct type gained a
  fourth method for the receiver shape: a value receiver that folds its field into
  the checksum, the only one whose *running* is observable without reading a result.
- **The fuzzer generates a two-result function and destructures its call**,
  `a, b := fn(x)`. Every multiple-value form in the language lowers through that one
  path — the header declaration, a `select` clause's receive and the plain statement
  alike — and none of it was under the oracle. Both results are predicted, so a
  compiler that mixed them up or dropped one answers with a wrong checksum. About
  two seeds in five carry one.

### Tooling

- **`ogo fmt` writes `for ; i < n; i++`**, not `for; i < n; i++`. A three-clause
  loop with no init statement had its first `;` bound tight, as a separator after a
  statement would be — but there is no statement there.
- **`ogo fmt` writes `box{p: {13, 14}}`**, not `box{p:{13, 14}}`. A keyed element
  whose value is an elided composite literal lost the space after its `:`, every
  brace inside a literal having been bound tight to what precedes it.

### Documentation

- **`p2.NewLock` does not report exhaustion**, and the docs said it did. The
  toolchain's `_locknew` hands out locks 0..15 and then returns 15 for every further
  call rather than -1, measured on a P2-EDGE — so a caller cannot detect it, and two
  logically distinct locks alias. Harmless where sharing only costs contention, as in
  the channel rendezvous (twenty-four channels each completing one run correctly);
  a hang where a program nests two locks it believes are independent, `_locktry`
  not being reentrant. `doc/locknew-never-fails.c` is the reproducer, and the
  channel runtime's "out of hardware locks" panic is documented as unreachable.

### Diagnostics

- **A parse error is no longer hidden behind a checker error on the same line.**
  Only one error per line is reported, and the checker's came first — so `if v, ok
  := f(); ok`, which the grammar does not admit, was reported as `undefined: v`:
  true of the tree that got built, and no help at all. The parse error describes the
  cause and now outranks every other error on its line.
- **A comparison the language does not define is refused by the compiler**, not by
  the C compiler behind it. `p < q` on structs, `s == t` on slices, and equality on
  an array or struct with a slice inside it all reached the backend, which said
  `Expected integer type for parameter of comparison` about generated C the reader
  never wrote. Each is now reported where it stands, in Go's words: `operator < not
  defined on struct`, `slice can only be compared to nil`, `struct containing []int
  cannot be compared`, `[2][]int cannot be compared`. The array messages gained a
  position too, and name the type as OctoGo spells it rather than as the emitted C
  does.

### Behaviour changes

- **A non-name target on the left of `:=` in a `select` clause is refused**,
  `case p.x := <-ch:`, as it already is in an ordinary short declaration. Those
  targets are all legal with `=`.

### Fixed

- **`print` separated its arguments with a space; only `println` should.**
  `print(n, " ")` in a loop wrote three spaces between values instead of one, and
  `print("a", "b")` wrote `a b`. Go's `print` writes its arguments adjacently — which
  is the whole reason to reach for it rather than `println` — and `specs.go` said so
  already; only the emitter disagreed. `println` was and is space-separated and
  newline-terminated.
- **A zero-length array declared an element it does not have.** `var e [0]byte`
  emitted `= {0}`, which the target's C compiler accepts silently and a host compiler
  warns about. It is declared without an initializer now, having nothing to zero.
- **A deferred call evaluated its receiver, and its callee, at the wrong time.**
  Go evaluates both where the `defer` stands — they are arguments, and the arguments
  were already captured there — but they were read again at the return, so
  `defer ws[0].show()` reported what `ws[0]` held at the *end* of the function, and
  `defer f()` through a function variable ran whatever `f` held by then. Silent:
  ordinary values, printed at a plausible time. A value receiver captures a copy now
  and a pointer receiver the address, which is the distinction Go draws.
- **A conversion applied to a call taking a slice expression did not compile.**
  `int(total(xs[:]))` — a slice expression handed to a call becomes a compound
  literal in C, and the conversion becomes a cast around it, which the backend
  cannot do: it warns `Bad number of parameters` and generates a call that passes
  nothing, refuses the program outright once the call goes through a function value,
  and crashes on a field read straight off a literal. The cast alone is fine and the
  literal alone is fine. The operand is bound to a temporary now, which puts the
  literal outside the cast. `doc/complit-arg-in-cast.c` is the reproducer.

## v0.15.0

### Language

- **A multiple assignment may write to anything an assignment can.** `xs[i],
  xs[j] = xs[j], xs[i]` — the swap every sort is written with — did not compile, nor
  did a field, a pointee, or an element of a slice held in a struct: only a bare
  variable name was a target. Every shape the single-target paths already reached is
  reached here now.
- **A multi-result method may be called on a struct field**, `m.st.pop()`, which
  was "multiple assignment requires a single function call on the right-hand side"
  — of a call. Only a method on a plain variable was recognized, so a container
  held as a field could not hand back its `(value, ok)`.
- **A function type naming a struct did not compile.** `func(m *Machine) bool`
  emitted its typedef ahead of the struct's forward declaration, so C had not seen
  the name — which is every callback that takes a pointer to your own type. The
  forward declarations are emitted first now, which is all a pointer to a struct
  needs.
- **A call through a function-valued variable has a type.** `b := a(0)`, where `a`
  holds a function that returns a function, was "cannot infer a type for the
  declaration of b" — only a call of a *named* function had its result type read,
  in the checker and the emitter alike. This is also the workaround the three-deep
  call chain lacked: that chain miscompiles on the target and, until now, could not
  be broken into variables either.
- **Calling the result of a call now checks the right arguments.** `choose(0)(5)`
  was "too many arguments in call to choose": the call walk took the *last* argument
  list as the named callee's, so `choose` was checked against `(5)`. The first list
  is the named callee's; a later one belongs to the previous call's result. A
  genuine arity error is still reported, and the names in later lists are still
  resolved.
- **A defined pointer type is a pointer**, `type PP *Point` — `var q PP = &p` was
  refused as "cannot use &p (an address) as PP value", the check believing PP wanted
  a value. With this, a defined type behaves as what it is defined over for *every*
  kind: scalar, string, array, slice, struct, channel, function and now pointer.
- **A conversion to a defined array type works**, `row(a)` for `type row [3]int`.
  It was the one kind of defined type whose name did not name a conversion, so
  `r := row(a)` was "cannot infer a type" and `sum(row(a))` put the type name in the
  emitted C as though it were a function. Indexing the conversion where it stands,
  `row(g)[2]`, is still refused — C has no cast to an array type, so the value needs
  a name first.
- **An `if` or `switch` header may declare several names from one call**,
  `if v, ok := m.get(k); ok` — how a two-result call is usually asked, and it did not
  parse: the grammar admitted a single name before the `:=`, so the comma was a parse
  error and the idiom had to be written as two statements, which also leaked the
  names into the enclosing scope. They are scoped to the statement, so they shadow;
  the `else` branch sees them; a blank is allowed. The three-clause `for` init still
  takes one name.
- **A `range` clause may write to variables that already exist**, `for i, v = range
  xs`. The `=` form parsed and ran, and left the variables it named untouched — it is
  listed under Fixed below; what is new here is that it works at all, in every range
  form, and that a blank target is allowed in it.
- **A defined function type is a function**, `type Fn func(int) int` — a call
  through a variable, parameter, struct field or package variable of one was
  "cannot call non-function". A callback named once and used everywhere is the
  reason to write such a type. Four lookups now follow the definition to what it is
  defined over: the checker's signature, the emitter's is-it-a-function test, the
  result type behind a chain (keyed by the function typedef a defined name only
  stands for), and a `:=` copy, which took the type's name and left the signature
  behind.

### Behaviour changes

- **A non-name target on the left of `:=` is refused.** `xs[0], y := f()` was
  accepted and quietly assigned to the element instead of declaring anything. `:=`
  declares names; the element, field and pointee targets it used to swallow are all
  legal with `=`.

### Fixed

- **`*p++` incremented the pointer, not the pointee.** It emitted `*p++`, which C
  reads as `*(p++)`: the pointer moved and the load was thrown away, so everything
  after it wrote one past the variable. C's `++` binds tighter than its unary `*`
  where Go's does not; `*p = v` and `*p += v` were always right.
- **An assigning range clause did not assign.** `for i, v = range xs` — no `:=` —
  declared fresh variables that shadowed `i` and `v` for the loop's length, so the
  loop ran and the variables it named came out untouched. They are written at the
  top of each iteration now, which is where Go assigns them: after the loop they
  hold the last index and element, and a `break` leaves them at the iteration it
  broke on. A `:=` clause is unaffected.
- **`for _, v = range xs` was refused** with "cannot use _ as value or type". A blank
  in an assigning range clause is the same discard it is on the left of an `=`.
- **A struct field named after a type did not compile.** `type logger
  struct{...}` beside `type app struct{ logger logger }` is ordinary Go, and C keeps
  member names in a namespace of their own — but the backend refuses one, with
  `Unable to combine types` pointed at the line before and nothing in the OctoGo
  source to tie it to. Any field named after any type declared anywhere in the
  program hit it. Such a field is renamed in the emitted C now, and only when it
  does collide, so every other program emits exactly what it did before.
  `doc/field-named-like-a-type.c` is the reproducer.
- **A dereferenced target in a multiple assignment wrote the pointer, not the
  pointee.** `*p, *q = *q, *p` compiled and assigned to `p` and `q` themselves: the
  leading star was read off the target and then dropped. The target's C compiler
  warns about it, which is the only reason it was not silent.
- **A package variable initialized with a function did not compile.** `var tick Fn
  = onTick` emitted the variable before the function's prototype, so C reported
  `'onTick' undeclared here (not in a function)` — whichever order the source
  declared them in, and for a written `func(int) int` as much as a defined type.
  The prototypes precede the globals now; nothing in a prototype can name a global,
  a signature being types only, so the reverse cannot break.

## v0.14.0

### Language

- **A method or a field may be reached on the result of a conversion**,
  `Celsius(5).f()`, which was "unsupported call in expression": the chain walk took
  its base to be a variable or a function, and a conversion is neither — it looks
  like a call of the type's own name. A converted value had to be put in a variable
  first. Still not reachable: a conversion to a defined *array* type indexed where
  it stands, C having no cast to an array type.
- **A defined type over a struct now behaves as that struct.** `type Named Point`
  was not modelled at all: field access, literals, conversions and methods failed
  together, the first of them as "unsupported expression node FactorSuffix". One
  cause lay behind all four — each asks the emitter's struct table for the fields,
  keyed by C type name, and a defined type was not in it. Resolving the name once,
  after every type is collected, fixes the family and makes declaration order
  irrelevant, so a type may be defined over a struct declared below it. A conversion
  *back*, `Point(n)`, needed the struct's own name admitted as a conversion type as
  well.
- **A slice whose element is a defined type did not compile.** `[]Celsius` for
  `type Celsius int` emitted its header typedef ahead of the typedef declaring
  `Celsius`, so C refused the program with "unknown type name". Slice headers were
  already split into those that may precede the typedef section and those that must
  follow it, and a defined type belonged on the second side of that split. An
  *array* of the same element always worked, which is why nothing noticed.
- **A composite literal may name a defined array or slice type**, `Row{1, 2, 3}`
  for `type Row [3]int`, which was refused as "Row is not a struct type" — the
  literal's type had to be written out in full. A defined type behaves as what it
  is defined over everywhere else, and this was the hole in that. It works through
  a chain of definitions, at package scope, with index-keyed values, and passed
  straight to a call.

### Fixed

- **Two defects in the backend's optimizer are worked around**, and `ogo build`
  turns off the two passes behind them. One stored a value the program never
  computed into a package-level variable — silently, with the host compiler right
  and the build saying nothing — and the other emitted a branch to a label it did
  not define, so the assembler refused the program. Both are reduced to a dozen
  lines of C in `doc/`, and both were live at upstream's tip when this was written:
  flexprop's master *was* the pinned v7.7.0, and a flexcc built from spin2cpp's
  master reproduced each identically. (Both were fixed upstream on 2026-08-03, after
  this release; the flags stay until the transpiled backend is regenerated.)

  Each defect needs two passes cooperating, so turning either one off is enough;
  `-Ono-inline-small -Ono-peephole` covers both. The cost runs between nothing and
  about 15% more code depending on the program — 13360 → 13232 bytes on the
  framing-receiver test case, 10292 → 11792 on a fuzzer-generated one. Turning off
  the register allocator instead would have covered both too, at 68%.

  The whole corpus, the on-board suite and all forty seeds of the widened fuzzer
  sample now pass, including the two seeds that reproduce the defects and used to
  be skipped. The skip is gone with them: a recurrence has to fail loudly. A sweep
  to 160 seeds on a P2-EDGE — 320 builds and runs — turned up no failure and no
  skip at all, so the routine sample is doubled to 24.

### Diagnostics

- **A `go` or `defer` whose operand the parser cannot read was reported at line 1,
  column 1.** `defer func() {}()` — a function literal, which the grammar has no
  rule for — leaves a head holding no token of its own, and that head's position is
  the file's *first* token, so the reader was sent to the wrong end of the program.
  It is reported at the keyword now. The operand of such a statement always follows
  its keyword, which is what tells a real position from an inherited one.

### Tooling

- **`ogo fmt` aligns runs of trailing comments on statements**, which it did not
  align at all — each sat one space after its line:

  ```
  id := 1             // the id
  sequenceNumber := 2 // the sequence
  ```

  A run is a maximal group of consecutive lines carrying one, and three things end
  it, exactly as in gofmt: a line without a comment, a change of indentation (a
  nested block is a table of its own), and a line built from a different number of
  cells — a `/* … */` sharing the line adds one, which puts its trailing comment in
  a different column. A statement's comment cannot be placed by measuring the
  source the way a struct field's is, so these are aligned from where they actually
  landed, in a pass over the finished text.
- **`ogo fmt` aligns the specs of a grouped `const ( … )` or `var ( … )`**, in
  gofmt's three columns — names, type, `= value`:

  ```
  const (
  	frameEnd uint8 = 0xC0
  	frameEsc uint8 = 0xDB
  	maxFrame       = 16
  )
  ```

  There was no alignment of these at all, so every `=` in a const block wandered
  with the length of its name.
- **`ogo fmt` put a struct's trailing comments one column too far right**, and
  ignored blank lines when deciding what to align together. Both come from the same
  rule, which gofmt gets from its tabwriter: a cell that ends its line is not part
  of an aligned column, and the blank line that ends an alignment block arrives as
  *one* newline rather than two — a field or spec ends at an inserted semicolon,
  which carries its own line's newline away. The blank-line test looked for two and
  so never matched: a struct with a blank line in it was aligned as though it had
  none.

### Testing

- **The fuzzer generates methods, with both receiver kinds.** Every generated
  struct type gets three: a value-receiver getter, a pointer-receiver setter, and a
  value-receiver method with the setter's body. The third is the point — a value
  receiver writes to a *copy*, so the caller's field must be unchanged after it,
  while the pointer receiver's must not be. The field is read back after every call,
  so a wrong receiver adjustment is a wrong checksum either way round.
- **The fuzzer generates strings.** A string is immutable and has no arithmetic, so
  everything readable out of one is exactly predictable: its length, a byte at an
  index, the length of a slice of it, a comparison, and a `range` yielding a byte
  offset and a rune. The corpus it draws from is deliberately not random ASCII — it
  carries 2-, 3- and 4-byte characters, so the UTF-8 decode behind `range` and the
  byte-versus-rune distinction behind indexing are under the oracle, where an
  off-by-one shows as a wrong checksum rather than as mojibake nobody is reading.
- **A generated program that outgrows the cog's code window is skipped, not
  failed.** One very long `main` eventually will not fit ("fit 480 failed"), which
  is a property of the target rather than a defect — and generated programs sit
  close enough to that ceiling that anything added to the generator pushes some
  seed over it. Failing there would make the fuzzer's coverage hostage to program
  size. A hand-written case must still never hit it.
- **The fuzzer generates the sized integer types.** `int8`, `uint8`, `int16`,
  `uint16` and `uint32` are declared, put through every operator that can carry a
  value out of the type, and folded into the checksum — starting from the type's
  extremes, where the wrapping happens. That is the family v0.13.0 found wrong in
  every operator at once, and the generator had no way to reach it.

  The fold is over an expression rather than over the variable, `int(z * 7)` rather
  than `int(z)`, and that distinction is the whole point: storing a result back into
  a narrow variable truncates it, in C as in Go, so a generator that only ever read
  the variable would agree with a compiler that had lost the type. Checked by
  reverting the v0.13.0 fix, at which the oracle fails on its second seed; with the
  fold written the easy way it passes all 800.

## v0.13.0

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

### Behaviour changes

- **The address of a local array's element no longer escapes its frame.**
  `return &a[i]`, storing one in a package variable, and handing one to a goroutine
  were all accepted, leaving a reference to storage that is gone — the shape the
  lifetime rules exist to stop. The question "is this a variable of this frame" asked
  only one of the two environments a local can be in, and a local array is in the
  other. All three sinks refuse it now, and the address of *package* storage is
  handed out as freely as before.

### Fixed

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

### Testing

- **A priority scheduler over a fixed node pool is a run case** — nodes in a
  package-level array, a free list threaded through them, and a ready queue kept in
  priority order. It reaches three things the fuzzer cannot generate: pointers into
  a package array threaded through struct fields, a labeled break leaving a nested
  search, and a deferred call that runs after the result is fixed and must not
  change it.
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
