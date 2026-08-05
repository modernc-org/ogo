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

### Language

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
