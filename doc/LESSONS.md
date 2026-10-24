# Lessons

The journal CLAUDE.md carried until 2026-10-14: what each probe, sweep, mutation
batch, domain program and review found, how it was found, and the rule each finding
left. It is the record behind the rules, not the authority on the code: `specs.go`
is the language, `internal/octogo/octogo.go` the compiler's design, and CLAUDE.md the
current state, the working rules and the index of the rules this file explains. A
paragraph here describes the code as it was when it was written; where it disagrees
with the code or with CLAUDE.md, they are right and it is history. Its pointers
"above" and "below", and to sections such as "Code generation" or "the smith
notes", are to CLAUDE.md as it stood, now split between it, this file and
`doc/REGEN-HISTORY.md`.

Every rule CLAUDE.md's index lists is in bold here, word for word, so `grep -n` of
its first words finds the story behind it. A new finding goes into the section of
its row, or into a new section at the end of "The probe journal"; the rule it leaves
goes into CLAUDE.md's index as well.

The sections: The probe journal; Implementation status, as CLAUDE.md had it on
2026-10-13; The language feature policy's history.

## The probe journal

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
The 17 closed in the next sitting (mutation_refusals6.ogo), each a value or a target
some rule had never been asked about: a comma-ok flag and a select clause's whole
target of no Kind (checkOkFlagTarget, checkRecvIntoTarget), `&*p` (addressOfInfo), a
method with no result at the end of a chain (zeroResultCall asks callChainWalk), a
PROMOTED method's results (callResults), a variable of a defined slice type appended
to (checkAppendValues reads the definition), a range over a sliced literal
(bracketLitStepsType) and over a variable of a defined array type (rangeElem). And the
66 `println` of a struct were NOT the extension this file had called them: specs.go
refuses a struct, and alone one was refused. Among other arguments one printf took
them all, and every argument but a string, a slice and a bare array VARIABLE went
under `%d` (isScalarPrint) -- a nil pointer `0`, an interface a word, an array
field's address, `1 26412 2` on the board for `1 [0 7] 2`. **A note calling a row
"by design" is checked against specs.go before it is believed.** The one left was a
parameter named like its type, `func take(reading reading)`, and it was a row: the
checker resolved a variable's recorded type NAME where the variable is used, which
the variable hides -- a result type named like the receiver was "not a type", a
method of a Kind type missing, and every rule asked of a struct type answered nothing.
A function's receiver and parameters are typed in the package scope now (Go's scope
for them is the body), a local hiding a type keeps the scope it was declared in
(declaredAt), and a lookup by a variable's type name passes a variable named like it
(typeDeclNamedIn, varTypeDecl) -- which a written head asking "is this a type" must
not, so the method-expression reader asks for the variable first. The emitter had
the same row: `cb(2)` for a parameter `cb cb` was a conversion (convType asks
localName). 75 programs of the row agree with Go; of the 10,347 mutants both refused
across the batches one was taken again, an older regression the list kept: a range
over a variable of a defined array type had no element. **A batch's REJ/REJ list is a
net too**: re-run it, and a mutant taken since is a row lost.
A sixth batch (gen6.py in the scratchpad: a struct field's type changed, a type
wrapped where a variable is declared, `T` to `[]T`, `*T`, `[2]T` or `chan T`, an
element added to a sized array literal, a name dropped from a several-name `:=`, a
control keyword swapped, an argument for nil or a literal of another kind) had 140
taken of 3,000, 54 of them `println` of a slice, the extension. The rest were twenty
rows, and the largest was the commonest literal there is: a SLICE literal had no
category for any rule -- nonBoolOperand typed an array literal through valueTypeAt
and a slice literal not at all -- so `num([]int{1})`, `int([]int{1})` and `var s
string = []int{1}` were taken (bracketLitCategory reads the brackets, as
sliceOrArrayOf reads a variable's initializer). Beside it, a function literal and a
pointer conversion `(*Sq)(nil)` had none either; a literal of a type written out, a
variable of one and a named struct literal were asked nothing where an interface is
wanted (`show(failure{})` was emitted as a struct passed for the interface, C no
compiler takes); a receiver was read by its NAME, so `func (t []T) m()` declared a
method of T; and an `if` or `switch` header declaring several names from one value
typed none of them, where the `for` header typed a call's (headerValueTypes). Two
false refusals came out of the nets and were older: `ws := make([]W, 1); ws[0].done
<- v` was "not a channel", the send rule refusing what it could not resolve, and
answering for a typed constant's method made `Two.Add(One).Int()` "type int64 has no
method Int" -- methodSingleResultName asked a variable's declaration only, which the
run corpus showed (accdiff). **A rule that refuses on failing to resolve is a false
refusal waiting for a shape it does not know**; the send keeps the refusal only
where the walk fails too.
A seventh batch (gen7.py: an index or a call added to a name, `defer` or `go` put
before a statement, a receive arrow added or dropped, a slice type made an array and
back, the range operand changed, a receiver toggled between value and pointer) had 22
taken of 3,000 and 177 refused that Go takes -- all of those by design (a value
receiver's method value, a local's address handed to a goroutine, a defer in a loop,
a printf verb checked at compile time) but one loud gap: `go` of a BUILTIN, `go
println(x)`, `go close(ch)`, "only `go f(args)` ... is supported yet", 142 of them,
which Go takes and no program seen needs. The taken rows: the element of a STRING,
`b[0]`, had no type where it stood -- factorType typed an index of an array or a
slice and of nothing else, so `var s string = b[0]` was written as a byte stored in a
string header; `++` and `op=` asked their index target nothing the `=` form asks
(checkIndexBase, shared now); names declared from a call of several results through a
function VALUE or a function FIELD were typed by nothing (callResults reads a
variable's funcSig, exprCallResults the callee's type through valueTypeAt); and the
constant folder took `big()` and `big[0]` of a number for an unknown value.
An eighth batch (gen8.py: a keyed literal made partly positional, an untyped constant
given a type, a case duplicated, a parameter dropped, var and const swapped, a key
duplicated, a receiver retyped) was nearly all refused alike -- 4 taken, 10 refused
that Go takes (string building from a var, by design; a duplicate `case true`, an
implementation restriction Go allows) -- and found one CRASH on a valid program:
`const Two int16 = 2.0` kept its float value, and `xs[Two]` asked Int64Val of a float
(checkConstOverflow stores the integer now). The four taken: a call leading a select
receive target asked nothing, and a type switch on the empty interface WRITTEN OUT,
`var e interface{}`, had no interface name for its cases to be asked against
(typeSwitchIface reads it as any). A non-empty interface written out as an
operand's type was asked nothing there until 2026-10-11 (AN INTERFACE WRITTEN OUT
IS A TYPE). Sweeping the crash's row -- a constant
whose value is of another class than its type, or an untyped whole float, in every
position wanting an integer (91 programs) -- found no more crashes and three rows:
a literal KEY and an array LENGTH from `const K = 2.0` were refused (the emitter's
litKeyIndex and the checker's arrayBound asked for an integer constant, where Go asks
for one representable by an int; constLengthValue, foldIndexConst), and a switch on
an untyped constant compared its cases with the untyped kind, where Go converts the
tag to its default type first. **A constant's CLASS against its TYPE is a row**:
`2.0` for an int16 and `2.0` where an int is wanted are each a spelling Go folds.
A ninth batch (gen9.py: a second result given, make's, len's and copy's arguments
changed, a field renamed, `&` on a call's result, nil for 0, "" or false) had 16 taken
of 3,000 and none refused that Go takes. The rows: a value of a predeclared Kind into
an interface was asked by no rule but a variable's declaration (checkImplements asked
names and pointers; `return false` for an error COMPILED), the constant folder dropped
a unary `&`, `&` of a function literal or its call was taken where `&f()` was refused,
and a method of several results reached through a chain typed none of its names
(exprCallResults asks callChainWalk's calls). The board sweep of fuzzer seeds
2001-2500 with 331f03a, the generator unchanged since 651a7f5: 470 passing (three on a
second load, the loader's "sendAddressSize: timeout" and "Error writing port"), 30
outgrowing a cog, none failing; and 2501-3000 with e28a36f: 463 passing (six on a
second load, the first printing nothing), 37 outgrowing, none failing; 3001-3500
with 244d414: 469 passing (seven on a second load), 31 outgrowing, none failing.
**The mutation sweep turned on a DOMAIN program** (2026-10-06): two programs written
for the round -- a bitmap library (a font table, a canvas of byte rows, Bresenham,
midpoint circles, flood fill on an explicit stack) and a stack machine (iota opcodes
with String(), a fault kept in the machine since a literal's address cannot be
returned, an assembler over the strings package), three packages each, matching Go on
the host and the board -- each mutated 1,500 times in ONE file with every edit above
(semdom/gendom.py, verdom.sh: the Go twin is a module). 34 taken, and the rows were
the boundary's and the shapes' the run cases lack: a conversion to ANOTHER package's
defined type asked only its operand count (checkConversionToKind), a struct FIELD or
ELEMENT into an interface asked nothing (storageOperand), a case constant its tag's
type cannot hold, a string's element as a list target, a for post's `+= nil`. And one
false refusal of a valid program: `len` of an array type Go makes a constant was
recorded for the emitter by the constant folder only, so `n == len([2]int{})` was
refused (checkFactorNames records it wherever the call stands). **A program's own
mutations are the run cases' mutations for the shapes it brings**: the single-file
batches had no second package to convert into.
A third, a worker pool on two cogs (jobs behind an interface in a package table,
embedded base types, requests over an array of channels selected beside a quit
channel), matched Go on the host and the board once one line was written another way:
`name := job.Table[2].Name`, a method value of an INTERFACE value, was "cannot infer a
type" -- refused by design, Go saving the interface value as it saves a pointer's
(methodValueSavesPtr), and said so now (reportIfaceMethodValue). Its 1,500 mutants had
8 taken: a value received from an ELEMENT of an array of channels or from a channel
field had no type in any declaring form (chanFactorElemInfo and recvElemType walk the
channel now), and a star over a channel in a send. A range over such an element
channel and a parenthesised receive's field, `(<-reqs[0]).ID`, were left untyped,
and were closed on 2026-10-06 with their row: 11 of 18 programs Go refuses were
taken -- a range over a channel FIELD declared its variable an int index -- and a
receive had no case in operandType, the walk every rule asks (recvElemType answers
it, `<-<-cc` an arrow at a time); a parenthesised receive is walked from its type as
a literal is, so `(<-in).Arr[1:]` and `(<-in).bump()` are refused as for a call's
result (checkParenChain, isRecvExpr). Probing the valid side found a SILENT fault
older than it: a method called on a parenthesised call or receive with an argument
of an effect ran the argument first, `(get()).add(<-in)` and `(<-in).add(<-in)`, on
the host and the board -- emitParenMethod left the head inline in the C while
argsCText bound the argument ahead of the statement; it binds an effectful head
first now -- and a method called on a parenthesised receive as a STATEMENT,
`(<-pc).set(7)`, was "unsupported call target" (emitCall writes it through
emitParenMethod). Still loud: a field selected on a channel, `in.n`, refused by the
emitter in its own words, and `defer`/`go` of a method on a parenthesised receive.
**A head the emitter writes as text is asked where its effects land against the
arguments'**: the unparenthesised chain had been right all along.

**A MUTANT GO ACCEPTS IS A PROBE OF THE EMITTER** (2026-10-06). The mutation
batches had judged each mutant by whether it COMPILES; the 5,366 run-case mutants Go
and this compiler both accept are valid programs of odd shapes, and none had been
run. Each was run against its Go twin (GOARCH=386, the batches' twin.go.txt for
printf and the Builder) on the host shim -- outcmp2.sh in the scratchpad, an
address normalized, a runtime panic named by its kind, a panic's trace and the exit
status after it dropped, the host's stdout unbuffered (stdbuf) so a panic lands
where Go's does, a package `var ch chan T` made in the twin, Go's println of a
slice set apart. 4,350 matched at once; of the rest most were Go's or by design: a
println of a slice (the extension), an append past capacity, out of cogs, a panic
halting its cog with the defers unrun and every cog printing its own, float64 being
32 bits, Go/386's own mis-rounding of int64 to float32, an order Go leaves
unspecified, goroutine timing and poll counts, and a print whose later argument
panics, which writes what came before it (Go evaluates first) -- that one left as
it is. Six were faults, five of them SILENT and in every release: a constant
prefix of a level computed in C's int (constPrefixLevel), a range key the body
writes being the loop's counter (bodyWrites, keyDecl), a `continue` in a select
clause taking the select's own C loop (loopContLabel), a defer in a select clause
or past a goto replayed unflagged (deferConditional), an array of Stringers under
printf not binding what follows it (formatCallsMethod asks arrayShapeOf), and a
call through a NIL function value calling address zero (fnValueChecked, and the go
trampoline's check). **A mutant that compiles is run**: the oracle that judged its
acceptance has an output too, and the shapes a mutation makes -- a key written, a
`continue` moved into a clause, a constant made wide -- are the ones no run case
writes. The domain programs' 6,053 mutants of that kind, run the same way the next
day with those fixes in, had 5,579 match and nothing silent among the rest: goroutine
races, endless loops on both sides, Go's stack overflow, the partial print, a panic
in the middle of a line (the harness's), the twins' Builder panicking where the
spec's truncates. Two loud gaps were left: `%q` of a rune Go's tables call
unprintable, U+202C, written raw where Go escapes it (the tables would cost the
target kilobytes), and a call of `main`, which C does not see declared.
The batches had been written to find what is REFUSED, and most of their mutants are:
a mutator meant to keep programs VALID (genv.py: a literal moved, an operator
swapped within its class, a statement duplicated, wrapped in an always-true if or
swapped, a continue or a break behind an always-true test, a defer inserted) made
6,000 of the run cases of which 5,114 both compilers accept. Run against Go, none
differed but by design or by a race -- most were the defers a panic leaves unrun, a
`defer println` inserted ahead of one -- and judging them found one compiler CRASH,
out of memory padding `[2147483647]Named{...}` to its length (refused now as larger
than Hub RAM), and three checker rows: a compound `/=` and `%=` by a constant zero,
`-*pp` of a pointer to a pointer, and a value of a defined type an operation, a
conversion or a call MADE into an interface (`take(y * 2)` reached C), a variable in
parentheses with it (isMadeValue). **A mutator that keeps programs valid is the
emitter's probe; one that breaks them is the checker's** -- and each finds a little
of the other's. The same mutator over the 34 domain programs (genvdom.py, 200 each,
5,862 both accept) found three more: a function LITERAL with results was never asked
for a missing return, `func() int { }` taken, its C falling off the end; a
conversion of a constant to ANOTHER package's type was no constant
(`lib.Perm(1) << 3` "is not a constant", its overflow unasked; constConversion reads
the qualified form); and a program calling its own `main`, `go main()`, re-ran every
package initializer at each call, silent on the board (mainReferenced guards it, and
declares main for the host). Its differences were otherwise races, timing, sizes the
host's stack cannot hold, and one known: a printf whose argument's String() prints
writes that output in the middle of the line, Go's fmt formatting the whole line
before it writes it.

**A MUTANT IS RUN ON THE BOARD, AND SO IS EVERY CHANGE TO A CHECK** (2026-10-07).
The valid mutants had matched Go on the host and had never run on the target; the
run-case mutants whose change is a constant or an operator -- value edges, the
target's typing rules -- were built and loaded one by one (bmut/ in the scratchpad:
prep.sh writes Go's normalized output and builds, brun loads under the board lock
and reads until the output equals Go's). The first board-only difference was not
the mutant's: its parent run case panicked, "nil pointer dereference". The check of
a call through a function value written the day before was `p == 0`, and the
target's compiler compares a FUNCTION pointer by calling its system module's
funcptr_cmp, which takes the ADDRESSES of two method pointers and is handed the
values, so it compares the memory at p with the memory at 0. A function named in a
static initializer is stored as its method-table index shifted up by 20, which is
address 0 again: every entry of a table of functions in a package variable compared
nil. The check panicked, and a program's own `if h != nil { h(x) }` had skipped the
call in every release (doc/funcptr-compare-nil.c). Every nil test of a function
value is a test of its word, `!f` (emitFuncNilTriple, nilHelperDef, the go
trampoline's); no listing of the corpus calls funcptr_cmp now. The run case stood in
the table the board runs, the host's compiler compared right, and `make board` was
not run after the check went in, so v0.49.0 was tagged with it. **A change to what
the emitter writes around every call or every dereference -- a check, a helper -- is
run through `make board` before it is committed.** Of the 2,031 run-case mutants
that built, 1,918 matched Go on the board at once and 87 more once rebuilt with the
fixes (most first lost to the loader, the port shared). What differs is known: a
print whose later argument panics writes its earlier ones first (Go evaluates them
all), two cogs' panics interleaved, a race the mutation made (two receivers on one
channel), and loops of 2^31 passes the 15 s the runner waits cannot see end.
The same run's refusals were the target's, each about a package table: a package
array with its type WRITTEN was a static initializer whatever its elements, `var ga
[2]int = [2]int{gx, 1}` naming a variable in one (the inferred form asked
staticLitElementsOKLevels; this one never did); an array of rows of ROWS padded
past its written rows took `{0}` for a row, which the target's static initializer
reads as a partial row and fails on (doc/partial-aggregate-init.c; braced to the
row's rank now); and a table of functions with its type left out was copied from a
local of the whole table, at 65535 entries a store instruction each, past what a
call reaches -- a function's name counts as a static initializer now (staticInitOK),
which moved every fuzzer seed's `var agFn = agMod` and 16 run cases into data. The
assembler's "Operand for call is out of range" is the program's size, as "exceeds
maximum of" is (callOutOfRange): a local array of 512 KB met it. gcc took every one
of these, so no run case had held them; TestTargetBuild does now.
The domain mutants' run added one more refusal: `hist[v>>12]++` of a byte v, the
count past the width going to a helper with an `(int64_t)` cast, and the target
refusing `++` of ANY target holding a cast to or from a 64-bit type -- an index, a
call's argument, under a dereference -- "bad cast of expression", where `+= 1` of the
same target builds (doc/incdec-64-cast.c). Every increment of a target that is no
plain name or field path is `+= 1` (incDecC), 261 corpus programs changed in that
alone, and the backend compiles the compound form no larger (137 of 152 the same
size, the rest smaller).

**A GOROUTINE'S STACK IS READ OFF THE LISTING** (2026-10-07). A domain program --
Conway's Life on a 64x64 torus of uint64 rows, one generation a 512-byte local --
matched Go on the host and on the board printed every line right and then
"goroutine stack overflow": its goroutine had run past the 256 longs of its slot,
and the fence the runtime keeps at the slot's end (ad90bef; specs.go's TODO had
said it was abandoned) was read only as the goroutine ENDED. p2-11's disk cog is
the same by its listing -- turn, find, fat.Volume.Find (a 708-byte frame), file,
next, hold come to 1488 bytes before the device call -- and never ends, so nothing
looked. The backend's listing has the whole story: a frame is `mov COUNT_, #n` and
`call #pushregs_` (n registers and three longs of its own) plus `add ptra, ##m`, a
function with no frame keeps its return in the cog's hardware stack, a call is
`call #name`. internal/build/stack.go reads it (readStackNeeds) and compileSized
compiles the same C again with slots that fit (octogo.WithGoStack), every slot the
deepest goroutine's size, unless --gostack asked for one. Three things a call graph
of a listing has to get right, each met: a call through a register is a call of
every taken function ON ITS SIDE -- the library's FILE functions reach the library's
handlers, the program's function values and tables the program's (the program's
side is what the C defines); taking one set for both joined everything into one
component through the I/O handlers. Recursion is a cycle of DIRECT calls, and
answers "unknown" (the slot stays 256); a cycle through an indirect call is the
library's -- flush calls a FILE function that calls flush -- and is taken as its
deepest simple path, a search over (function, visited set) for a component of 20 or
fewer and the sum of its frames past that. And a library function that reads ptra
into a register formats into the memory past it unreserved (the number printer):
256 bytes. Measured: dom29's goroutine reaches 272 longs on the board (prefilled
slot, high-water read as it ended) and the listing says 413; five run cases the
listing puts past 256 reach 23 to 120 -- the bound counts the panic's print chain and
every interface method an indirect call might reach, which is what a bound costs.
Of 478 run cases starting goroutines the median need is 158 longs and 5 exceed 256;
p2-11's is 809, 15 KB more Hub RAM for its seven slots. The same reading bounds the
MAIN cog, whose stack is the rest of Hub RAM: a program whose image and deepest main
stack exceed 512 KB is refused (the corpus's largest is 471 KB; main needs 5 KB at
most there). Where main reaches a recursion its need has no bound, and it was asked
nothing (2026-10-11, the board run of mutants: a 512 KB array result beside a
recursive function built and printed nothing); the deepest chain of DIRECT calls,
entering each component once, is a bound from below and is asked there
(mainAtLeast) -- **a bound with no answer still has a floor**. p2-11's 809 was 432
with each interface call resolved to its
implementations by hand (the startup path turn, find, fat.Volume.Find of 708 bytes,
file, next, hold and the card's read; Serve's transfers need 206), and the old
256-long slot was overrun on every boot that mounted a card, into an unclaimed
slot. **A listing is a probe of what the C costs**: cogHint reads registers off it,
this reads stacks.
The interface calls are resolved since (octogo.ScanCalls, narrowTargets): the
listing has no types and no C lines, and the C says exactly where an interface call
goes -- `((const X_vt*)...)->ogo_m_M(` reaches what the static tables of X_vt hold
in M. ScanCalls tokenizes the C and reads each function's calls by name, its
interface calls and its OTHER calls, those not proved by name (a variable, a
parameter or a local named like a function, an element, a member, a parenthesised
callee); a program function whose calls through a register are all interface calls
-- in its own C and in what was inlined into it, the functions its C calls and its
listing does not -- and whose listing makes exactly as many reaches their slots,
and any other keeps every taken function. Two nets: a misread call costs
precision unless it is taken for a call BY NAME, which TestScanCallsAgainstGCC
rules out over the run corpus -- gcc -O0 makes one indirect call instruction per
indirect call, and no function may count fewer than gcc's (327 functions, 326
exact); and the listing's count. p2-11 is 579 now, the rest the panic's print
chain through the library's FILE functions, which keeps the deepest-simple-path
bound; of 480 corpus programs starting goroutines, 6 came down, and on the board
none reached past its new bound (79 longs the most, for 244). **The decision a
precise bound rests on is made where it is verifiable**: the emitter's
bookkeeping would have been one more place to forget a site, the C text is the
one place every site is written, and gcc counts them.
The slot was CAPPED at its largest, 8192 longs, where the need passed it, and the
build said nothing: a board mutant's `var seen [65535]uint32` in a goroutine ran past
its slot, the pool and what followed, and printed nothing at all. Such a program is
refused now, the goroutine named (goroutineName: the trampoline's deepest direct
callee), `--gostack` the way past. **A bound that is capped is a bound that is not
kept**: where a size is clamped to a limit, the clamp is a refusal.
A SLOT OF ITS OWN (2026-10-17, the review's "separate worker classes"): every slot
had the deepest goroutine's size, 256 longs at least, seven of them whatever the
program started -- p2-11's four goroutines, started once each, kept seven slots of
512 longs, 14 KB. A go statement that runs at most once has a slot of its own now,
sized for its goroutine, where every one of the program's does (goslots.go), and
the shared pool's slots have no least size: p2-11 keeps 512 + 3 x 160 longs, its
binary 10,400 bytes smaller, and 486 of the 507 corpus programs starting goroutines
take slots of their own. Below 256 the bound had never been what a slot held, so
it was asked: the 191 goroutine run cases and seeds 1-200 on a P2-EDGE, each slot
followed by 512 painted longs checked as its goroutine ended and as main returned,
none touched -- and, asked to fail first, every slot at half its bound, the case
whose locals outgrow the default stack wrote into its moat. **A floor a measured
bound replaces is asked, with a moat past the bound, what it was hiding.** The
pool was first rewritten in the new slots' form, each stack an array behind a table
of pointers, and obeam's VM -- seven schedulers started in a loop, 1-2 KB short of
the 496 KB the P2 ROM boots from an SD card -- grew by 244 bytes: the slot's struct
shrank, and its guards were indexed apart from it. Kept in its old form for the
shared pool, the VM built to the same bytes. **A runtime made general for a new
shape keeps the old shape's C: a program at its limit pays for a generality it does
not use.** Two smaller finds: the target's compiler takes no `long *const` for a
cog's stack ("Expected pointer to stack as last parameter to coginit/cogid"); and a
function counted as named once by its SPELLING across the program was not, p2-11's
`drives()` beside rk11's constant `drives` -- a name is resolved where it is
written, bare in its own package and `q.drives` through an import of it.
The report the same review asked for, `ogo build --report`, reads what a binary
takes off the backend, and two of its first answers were the backend's. Its
listing of addresses (`--listing`) writes the C's lines into the assembly as
comments and the optimizer works around them -- an FCACHE load gone, registers
chosen otherwise -- so obeam's VM built with one is 4 bytes smaller than the VM
shipped, every address after the first change moved. The build's own assembly
assembled again with a listing is the shipped binary byte for byte, for obeam;
p2-11's the assembler refuses, "Changing hub value for symbol
_rk11_Controller_read", and its report reads the listed build and says by how many
bytes it differs. **A listing asked for to look at a program is a different build:
its binary is compared with the one shipped.** And the emitter's model of the
target's layout, written for unsafe.Sizeof, sized a channel's cell as a pointer
and knew nothing of the runtime's structs; a second compile carries a table of
`sizeof` of every object at file scope behind a mark, read out of its binary, and
all of p2-11's 269,260 bytes of data but 1,273 are attributed -- those the C
library's stdio state and the bytes of strings its tables point at. **A size the backend gives is asked of
the backend.**
ONE PASS THROUGH AN INTERFACE (2026-10-17, the same evening, reading the stack
reader again for the review's entry check): with the floor gone a slot is what the
listing says, and the listing's reading of a cycle had been written for the
library. Recursion was a cycle of DIRECT calls; any cycle through a call through a
register was "the library's", flush reaching a FILE function reaching flush, and
walked once -- and the program's interface calls had been resolved to their tables'
slots (narrowTargets) without the rule being asked of them. An expression tree
evaluated through an interface on a goroutine, `a.l.Eval() + a.r.Eval()`, was given
176 longs, one pass's need; 200 deep, the P2-EDGE printed nothing at all, 5 deep it
printed 6, and in the 256 longs v0.57.0 gave every slot 20 deep printed 21 and 60
deep nothing. The moat sweep that let the floor go had found nothing because no
program swept recursed through an interface on a goroutine. A cycle of direct and
interface calls is a recursion now, refused unless --gostack sizes the slots, the
error naming the method, not its table's thunk. A cycle a call through a FUNCTION
VALUE closes is not, and that is the open part: such a call is taken to reach every
function whose address the program takes, so it closes cycles no run makes --
obeam's interpreter calls its BIFs through `bifs[b].f` and its handlers through
`handlers[op]`, two tables of one signature each taken to reach the other's
functions, a cycle through `apply` that obeam's `bifJump` trampoline exists to
avoid; the corpus's two (a state machine's call reaching an interface's thunk,
testing's name table reaching the tests) are as spurious. Refusing them refused the
VM; telling a `bif` from a `handler` needs the function values' flow by their
defined types, which nothing records. A second hole came with it: a component
such a call joins that held a DIRECT recursion was walked as a cycle, the recursion
inside ignored, where the same recursion reached outside a cycle was "unknown" --
case 39's main, whose test runner calls a test calling the recursive `deeper`. A
recursion a function reaches by the calls it is known to make is one, cycle or not.
**A rule a call graph's bound rests on is asked of every kind of edge the graph
has: one written for direct calls is a row of the interface's and the function
value's.** **A moat sweep measures the programs it sweeps: each rule the bound
rests on is asked by a program written to break it.**
A RECURSION IS CHECKED WHERE IT RUNS (2026-10-18, the review's entry check): a
recursion's depth is the data's, so no listing bounds it, and a goroutine reaching
one was refused unless --gostack sized the slots -- and then ran over what followed
its slot -- while main's cog, the rest of Hub RAM its stack, ran on past the top:
`depth(1000000)` on main printed the line before it and nothing more. The C can read
the stack pointer, `_PTRA`, and `(unsigned)_PTRA > K` for a constant K is `cmp ptra,
##K` and a conditional call, no register taken. So the build finds the recursions in
the listing of a first compile, has the program emitted again with a check at the
entry of each function on one (octogo.StackChecks), and compiles it again until each
check's margin covers what its listing says: the function's frame and what its calls
take up to the next check, the graph read with every checked function calling
nothing. The panic is not in the margins but past them, as the reserve, since
whatever calls it does so within a margin of a check; counted in both, a frame of 16
bytes asked for 1152. On main's cog the end is Hub RAM's and the check one compare;
on a goroutine of a --gostack slot the trampoline records its slot's end, the slot's
high guard, for its cog, and a check reads its cog's -- ten instructions, and only
there. A reserve is what Go keeps too, a stack guard at the bottom of every
goroutine's stack for the runtime's own calls, and it costs what it costs: 30 levels
of a function of 24 bytes fitted a slot of 256 longs and do not now, 568 of its 1024
bytes held for the panic. An interface's thunk with the method inlined into it is
the one function on the recursion in the listing, the check written in the method,
so a function whose one call is to a checked function the backend inlined counts as
checked, by the method's margin. What went wrong first was elsewhere: the goroutine
test printed nothing because main printed what it received (A PRINT HOLDS A LOCK).
**A check the program pays at run time is written where the listing proves it
needed, and sized off the listing of the program it is in.**
WHAT A FUNCTION VALUE'S TYPE MAY HOLD (2026-10-18, the hole ONE PASS THROUGH AN
INTERFACE left): `main` recursing through a callback printed its first line on a
P2-EDGE and then nothing, the stack check never written, since a call through a
function value reached every function whose address the program takes and its
cycle was walked once. What tells obeam's `bif` from its `handler` is their defined
types, and the checker had never asked two defined function types to differ: the
signatures were compared, and a `handler` went wherever a `bif` was wanted, thirty
positions Go refuses; four positions -- a package `var` of a written type, an
element appended, results forwarded whole, a name a `:=` list assigns -- asked no
type's identity at all, a struct of another layout going in; `h, n := lookup()` of
a defined function type could not be called, a local `var a, b = f()` typed
neither name, and another package's function type was checked in this package's
scope, refusing `c(lib.Celsius(3))` against main's own Celsius. With those fixed
(7b9e8b5, 18210a1) a value of one defined type reaches another only by a
conversion or through the type written out, and a call through a value reaches
what its type may hold: the functions the checker saw made values of that type
(noteFuncValueType, by the value's first token), those used where it saw none, and
every function of the signature where the C declares anything of the type written
out or converts between two of them (funcclasses.go). The edges are read for a
recursion and nothing else: a stack is sized as before, so no slot shrinks on the
strength of them. The first version recorded each call's type beside the C as the
emitter wrote it, and a function making one call counted two: the type inference
renders a call to learn its type and throws the text away (callChainType). So the
type rides in the call's text, `OGO_FV(type, callee)(args)`, a macro that is its
callee, and a rendering thrown away takes its mark with it -- not a comment, which
the emitter's own readers of the callee text (valueResultC's isCIdent, cOperatorTop
taking a `/` for an operator) would have answered otherwise. Every program of the
corpus builds the binary it built without the macro and the table, and so do
p2-11, its ten test binaries and obeam's VM, checked and unchecked. Of the corpus
two programs read differently at first, both spurious: a struct literal's KEY named
like a function was that function used as a value (collectFuncValues, older than
this, widening the lifetime unions too), and a local bound to one literal, called
through the type written out, reached every function of its signature -- the
knowledge its nil check is left out on (boundFunc) now names the call's one
function. With both fixed none of 1380 does. **A fact
about the emitted code rides in its text: the emitter renders text it throws away,
and a record kept beside the text counts renderings that never reached the C.**
**A finer edge found for one question is read for that question: what a recursion
is told is no stack's size until the size can rest on it.**
dom53 (2026-10-18, the same evening, a domain program in the shape just changed: a
state machine of `type State func(*Machine, Event) State`, a message bus of
handler tables with method values, a console's command table, comparators, an
evaluator recursing through an operator table, a worker cog taking jobs as
functions over a channel) matched Go on the host and the P2-EDGE once four faults
were out of its way. The first was the day's own: the edges read for a recursion
were added beside the wide ones a stack is sized by, and need answered both
questions, so a goroutine calling through any value reached the evaluator's
recursion through its wide edge and was refused for one it never makes -- the rule
written that afternoon, kept in the size and broken in the walk. The size and the
recursion are two walks now (stackGraph.recurses), and the corpus reads as it did.
The second: a function inlined twice was visited once for its calls, so `evAdd`,
calling Eval in two places, was read as making one call through the table where
its listing makes two, the counts disagreed, and its recursion went unchecked --
long a precision loss for interface calls, a missed check for this. The third, a
call of `strings.ToUpper`, which the package does not have, went to the C compiler
from a literal in a table's initializer: a function literal's scope hangs off the
file's, where the imports are, and every rule that takes a name the scope does not
find for a qualifier found the import itself -- a call of any count, an unexported
function, a constant called and a store of the wrong type into another package's
variable were all taken in a literal (foundOrQualifier, and a sweep of 76
statements asked in a body and in two literals). The fourth was the lifetime
rules': for a type naming itself the union a call through a value of it is judged
by was asked under the key the type was minted by, `self:State`, which holds
nothing, so a state function storing the pointer it was handed took a local's
through a package variable, a table, a field or a state's result -- the same
program over a type that does not name itself was refused. **A gate that takes "not
found" for "a qualifier" is a row of every scope a name can be found in: a
function literal's finds the import itself.** **A question answered off a graph is
asked of the edges it is about: a size's edges answered a recursion.** **A copy is
counted as many times as it is made: a function inlined twice brings its calls
twice.** **Two keys for one type are two tables: the reader asks by the key the
writer wrote.**
Its 1,600 mutants then had nine holes, all one: the elements of a literal of a
struct type written out, `[...]struct{ name string; h H }{{"a", f}}`, the table a
loop ranges over, were asked nothing -- checkElidedStructLit answered only an
element type with a name, so a value of any type went into a field and a function
literal there was a body no rule read (**A rule asked by name is a row of every
type that has none**). The other 66 differences are the ones by design: unreachable
code, a method value of a value receiver, a panic's shorter message, a deadlock no
detector stops. On the P2-EDGE the 352 distinct binaries of the 724 both sides took
printed what Go printed, three ending in `panic: goroutine stack overflow` where
Go's ran out of its 250 MB -- one of them through a state function of the type
naming itself, a recursion only once its union held its functions.
The board run after the four commits were pushed found what the host had not: the
run case written for the classes, a recursion through `v` that main enters twice,
printed `100` and no panic. The backend had inlined walk into main twice and folded
away, in each copy, the call its constant argument does not make, so main's listing
made two calls through a register where its C, counted copy by copy, made four. The
count is narrowTargets' test that the calls it knows are all the calls, which a
stack's size may rest on, and failing it main's calls were taken to reach every
taken function -- an edge the recursion walk does not follow, so nothing reached
walk, and a goroutine entering walk the same way was given a slot sized for one
pass. The first fix followed such edges for a recursion, and TestStackNeeds said why
not: a call nothing narrowed reaches every taken function, most of them never
there, and a recursion reached only so is walked once by design (ONE PASS THROUGH
AN INTERFACE) -- walking it otherwise refused obeam's VM. The count decides only
whether the known targets may stand for the call in a size; whatever it says, they
are what the C calls, and they are read for a recursion now (narrowTargets' known
beside exact). None of the 1380 corpus programs reads differently for it, nor
p2-11, its ten test binaries or obeam's VM; but none of the 1380 is this run case:
the corpus was kept before the case was written, and the commits had been called
unchanged in their binaries off it and off dumpc's C, which carries no table.
**A test that a fact is exact answers whether it may stand for the whole, not
whether it is true: a copy that lost a call still makes the others.** **A claim that
a change leaves the binaries alone is measured by building them, the newest run
cases among them.**
dom55 (2026-10-20, a serial command console a firmware carries: a lexer unescaping
quoted strings in place, a table of parameters with ranges, a table of commands of a
defined `Handler` function type filled in an init, matched by unique prefix,
sentinel errors and two wrapping ones with Unwrap, classified with errors.Is, As and
a type switch, and the console on a cog of its own fed lines over channels) printed
Go's output on the host and the board, and of its 1,600 mutants none was a hole and
no output a fault: the differences were panics' wording, deadlocks Go reports and a
cog waits out, and `go b.WriteByte(...)` races. Two refusals a user meets were by
design and said so -- `strings.HasPrefix(s, string(b))`, where Go's compiler pays
for any string longer than its 32-byte buffer, and a concatenation -- and one C a
user never sees warned: a conjunction among a tagless case's conditions, `case k ==
2 && !fixed, k != 1:`, went out bare inside the ||, gcc's -Wparentheses failing the
host table. What it could not do was build without `--gostack`: a goroutine making a
call through a function value is sized as reaching every function the program takes
the address of, and Go's way of wrapping an error, an Error calling `e.Err.Error()`,
makes the Error methods recurse through the interface, so the console's stack had no
bound -- refused, in the name of a method it never calls. The classes a call through
a value may reach were asked at run time whether they hold what the calls reach:
`OGO_FV` redefined to look the callee up in its type's class, over the 119 run cases
and 1,286 domain programs and mutants that make such calls, missed nothing once a
value's WRAPPER counted as its member -- a method value's `ogo_mvN`, a result
adapter's `_ogo_fv`, which the table names by the function they call and a size
resting on it would have to count. Left to the user's call: the size is the
classes' only on that evidence. Its board pass, built with `--gostack 400`, found
one mutant printing nothing at all, a `var t [65535]byte` in the number printer the
console calls, and the build had known: `--gostack` took a slot as asked,
unmeasured, as a need off a listing whose register calls reach everything is a bound
from above that would refuse sound programs -- but what the chains of direct calls
need is a bound from below, the one main's stack has been held to since a mutant's
512 KB result, and a goroutine of 1160 longs built at `--gostack 400`, one needing
more than the largest slot at `--gostack 8192`. The floor is a slot's now too.
**A size taken as asked is still held to the floor the build knows: what the user
gave is no reason to run past it in silence.**

A VALUE'S TYPE IS A STACK'S SIZE (2026-10-21, the user's call on dom55's evidence):
a call through a function value is sized by what the value's type may hold, as an
interface call is by what its table's slot holds. Two things stood between the
classes and a size. The table named the function a value was made of, and the value
is not always that: a method value is a wrapper the emitter lifts with its receiver
bound, one by receiver, and a function of several results is taken through one
writing them out, and a call lands in the wrapper, whose frame is the call's. The
table names the wrappers now (fvHeld), a wrapper the C names only where it declares
and defines it being no value's -- a rendering thrown away lifts one too -- and the
run-time check asked again without counting a wrapper for its member missed 23
calls of the run cases on the old table and none of 119 run cases and 1,286
programs on the new. The other was the moat sweep's: eleven programs written to
break the rule, each a goroutine calling a Handler's deep function as an Op, found
that a pointer to one function type read as a pointer to another makes a value of
one type a value of the other -- through unsafe.Pointer, `*(*Op)(unsafe.Pointer(&h))`,
the value's bits copied as a uintptr, a struct's field, unsafe.Add, across a
package, or converted with no unsafe at all, `(*Op)(&h)`, which Go allows of two
types of one signature. Every one missed on the host, and built with the size
narrowed and the hole open every one printed nothing at all on a P2-EDGE, its slot
160 longs where it needs 460 -- a hole the recursion's reading had had since
2026-10-18. Such a conversion opens the signature of every function type either
pointer reaches (noteFuncValuePun), and the eleven print Go's output in the slots
the wide reading gave them. Of 717 programs starting goroutines -- the run cases
that do, the domain programs and a sample of their mutants -- none has a slot that
grew or is refused where it built: 80 have smaller slots, dom53's goroutine 160
longs where it had 304, 86 a smaller main stack, and the 192 of dom55's console and
its mutants, refused unless `--gostack` sized their slots, build. obeam's seven
scheduler slots are 704 longs where they were 800, and its demo prints on a P2-EDGE
what it printed in the larger ones but for the line that says which scheduler ran
which process, which differs from run to run of one binary; p2-11 and its ten test
binaries, calling through no function value, are the bytes they were. The
assumption the exactness test rests on was measured, not argued: over 158 of those
programs the listing of a program function never made more calls through a
register than its C accounts for -- 838 functions, 337 calling through values,
two making fewer and left wide -- so no call of the library's inlined into one
can stand in for a call of the program's the backend dropped. **A table read
for a size names what the value IS: a wrapper the emitter lifts for it is the value,
and its frame is the call's.** **A bound a value's type gives is asked of every way
memory is read as another type: a pointer converted, through unsafe.Pointer or not,
makes a value of one type a value of another.**

A POINTER TYPE IN PARENTHESES IS ANY TYPE (2026-10-21, found writing the moat's
programs): `(**Op)(unsafe.Pointer(&slot))` was "cannot use type Op as a value". Go
converts to a pointer type of any shape, and the checker and the emitter read four,
`*T`, `*lib.T`, `*int` and `*[N]T`; the parenthesised half of every other was walked
as an expression. Of sixteen probes Go took fifteen and the compiler refused eleven:
`**T` in five spellings ("cannot use type int as a value"), `*chan int` and
`*error` (a declaration the emitter could not type), `*interface{...}`,
`*unsafe.Pointer` (refused as converting an unsafe.Pointer), and the two that do not
parse, `*struct{...}` and `*func(...)`. The expression is rebuilt as a Type node over
its own tokens (pointerTypeAST), which the checker's typ and the emitter's cType
read as they read a declaration's type, the operand checked as Go checks it -- the
base types' underlying types identical, Go's words when they are not -- and a
local declared from one typed whole (typedVar). The sweep of where such a value
goes found a method called on what a pointer to an interface points at,
`(*p).Area()`, emitted as the shorthand a pointer to a struct takes, `p.Area()`: a
call of the interface's own `S_Area`, which no C defines -- through a name, a
call's result, a field, two pointers, deferred and started -- and `p.Area()`
itself, which Go refuses, went to the C compiler the same way. The value is bound
and called through its table now (derefShorthand, emitParenMethod), and the second
is refused in Go's words. Two regressions were caught by the nets before the
commit. The emitter's recogniser asked cType of the stars' name before asking that a
call followed, and in a deferred call's argument, where `p` is no variable the
replay knows, cType reported "unsupported type p": `defer show((*p).Area())`,
which built, was refused; it asks the call first and the type quietly. And the
refusal of a method on a pointer to an interface read the variable's isPtr, a mark
a type switch's clause binding of an interface carries too: the errors package's
`case wrapper: x.Unwrap()` was refused, and with it 191 of the fuzzer's first 400
seeds and seven run cases -- which corpusdiff did not report, a refused program
being a file the after-dump lacks: the listings' counts did. The rule asks the
variable's type (ptrToIface). Measured after: the corpus's 1,388 programs the C
they were, the 1,452 programs Go rejects the rows they were, and of 3,257 domain
programs and mutants none with another verdict or message. **A mark a refusal reads
is asked what else carries it: isPtr marks a type switch's binding of an interface
as well as a pointer.** Left, loud: a conversion to a pointer to a struct or a
function type written out, which the grammar does not parse in an expression (a
struct type there wants a literal after it, a func type a body), nor a conversion
to either; a store through two dereferences, `(**q).b = 7`, "only assignment to a
simple variable" (taken the same day, dom56 below); an assertion on a parenthesised value but a name, `(*p).(*R)` and
`(get()).(*R)`; a send whose channel is a conversion, `*(*chan int)(p) <- 5`; and a
method on a field pointing to an interface, `h.p.Area()`, refused by the emitter in
its own words where Go's are "pointer to interface, not interface".

dom56 (2026-10-21, a domain program in a shape none had taken: calendar time and a
report, module example.com/clog -- dates from day numbers and back by Hinnant's
algorithms, which divide negative numbers, leap years, ISO weeks, timestamps and
Go-style durations written into fixed buffers, a log of records sorted, searched
and summarised per channel, parse errors wrapped and asked by errors.Is and As, a
report in printf's widths, flags and %v of structs and String methods, and a clock
cog ticking through a select) matched Go on the host and on a P2-EDGE as written,
once its Error method built its text in a buffer rather than by `+`, which needs a
heap. Its 1,600 mutants found no compiler fault, on the host or on the board -- the
830 both compilers take built without `--gostack`, sized by the listing, and of
their 463 binaries 448 printed Go's output on a P2-EDGE and 15 a known difference
of the host's below: no program Go rejects
taken, 23 refused that Go takes, every one by design (unreachable code, the
compile-time printf and errors.As checks, the lifetime rule), and the 72 that ran
differently each a known difference -- a panic's wording, a hang or a deadlock Go
detects, a String method printing into the line printf writes -- but one, which was
not written down: fmt recovers a panic in the String or Error method it calls and
writes `%!v(PANIC=String method: ...)` in the value's place, and here the program
ends there. It is DIFFERENCES.md's now, recover being planned. Besides, writing its
run cases, a store through two dereferences, `(**q).b = 7`, and a read through one
where the struct holds an array, `(*get()).arr[1]` -- the head took one star, and a
struct holding an array is no value the target's compiler copies, where the pointer
read through is what is bound now (derefExprOperand). A star over such a chain,
`*(**q).p = 6`, was taken the next day (dom57).

dom57 (2026-10-21, a domain program in a shape none had taken: a codec, module
example.com/codec -- LZSS over hash chains with a multiplicative hash past
INT32_MAX, canonical Huffman codes limited to 12 bits by JPEG's adjustment and
decoded as zlib's puff decodes, base64 as RFC 4648 has it with an encoding copied
and repadded, naturals of 256 bits as `type Nat [8]uint32` to a Lucas-Lehmer test,
Adler-32 and a bitwise CRC-16, and a checksum cog fed blocks over channels) matched
Go on the host and on a P2-EDGE once written as the target allows: an error
declared in a package variable, a name from a table where `string(b)` was passed, a
copy where `[]byte(s)` of a variable was, and its error tests split in four where
one function held 167 of the cog's 480 registers -- each refused in words that say
what to write. Its 1,600 mutants found no compiler fault on the host or the board:
no program Go rejects taken, 26 refused that Go takes, all by design (unreachable
code, the printf checks, the lifetime rule, a defer in a loop); of the 557 distinct
binaries 552 printed Go's output on a P2-EDGE, four hung as Go hangs, one was slow
and printed it given two minutes, and three outgrew Hub RAM. What it found was in
the report and the messages: a method of a defined array type was
`(*mp.[8]uint32).Add` and a refused comparison of one named the shape, `[3][]int
cannot be compared`, where Go names the type, `a == b (Row cannot be compared)`
(goArrayTypeName). And, sweeping `*(**q).p = 6`'s row, a store through stars over a
parenthesised head, `*(*q).p = 6`, `*(t).p += 2`, `**(get()).in.pp = v`, was "only
assignment to a simple variable" where `*q.p = 6` worked (starParenTarget), and a
read through a parenthesised call's result whose struct holds an array,
`(getv()).n`, "this form is not supported yet" where getv().n worked
(peelCallChain); a callee storing through the new target was nobody's to the
summaries (starParenHead), and `ogo fmt` kept the doubled pair of `*((p)) = 5`.
Left: a list form with a starred chain, `*t.p, y = 1, 2`, with parentheses or
without ("a dereferenced target with a field or index is not supported yet").

**A TYPE SWITCH BEHIND AN INIT STATEMENT** (2026-10-07), `switch s := pick(); v :=
s.(type)`, the last header shape on the open list: the grammar's SwitchTag takes `[
HeaderExpression ":=" ] HeaderExpression` (egg's ambiguity count unchanged, 10),
switchGuardParts reads the guard's name into tagName (readSwitchTag),
tagIsTypeSwitch tells the form, the checker checks the init first in the
statement's scope and gives the guard a scope of its own, so `switch v := f(); v :=
v.(type)` shadows (checkSwitchInit, split out of checkSwitchGuard), and the emitter
writes the init in a block before it renders the operand, which may name it
(emitSwitchInit; typeSwitchNames reads the form for the summaries, or `keep(&x)`
through a callee's `switch q := p; v := q.(type)` was taken). Three older holes
next to it: `switch v := v.(type)`, Go's idiom, was `Sq* v = (Sq*)v.data;`, which C
reads as the new v -- gcc refused it and the target resolved the name outside by
luck, so no run case could hold it (the operand is bound first now); `switch _ :=
x.(type)` was taken, Go's "no new variables"; and a bare-name operand of a Kind and
`x := v.(type)` were refused by the emitter in its own words (the checker asks
both, "is not an interface" and "use of .(type) outside type switch"). A switch
binding its operand and naming no type in any clause wrote an unused temporary
(`(void)` now).
Two domain programs the same day, Conway's Life on uint64 rows (dom29) and a
message bus over cogs (dom30: static handler tables, state functions, a 64-bit
clock from a wrapping counter, CRC-32 over payload structs), matched Go on the
host and the board; their 6,000 mutants had one row taken, a range over an
ADDRESS, `range &m.Len` of a uint8 ranged as the integer -- the address's Kind
being its pointee's to the rule asking (checkRangeable asks what an address
points at: an array only) -- and one message wrong, `[lib.n]int` of an
unexported constant "non-constant array bound".

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
A THIRD package is the row's next column (2026-10-06, found mutating a DSP program of
three packages, q15, filt and main): a type filt writes as `q15.Q` could not be
carried into main's spelling -- requalifiedType answered false for a name qualified
THERE -- so a value going into filt's field, element, literal or variable of a q15.Q
was asked nothing, and one read out of it, `var x int16 = filt.G`, was named
`filt.Q`, a type of no package. It is spelled with main's own import of q15 where
there is one (foreignQual, carriedQual; importQualTok finds a token of this file
spelling the name, an alias or a use, which an import must have), and where there is
none by the qualifier filt wrote, which resolves through filt's file (identFile) --
unless main has a name of that spelling, when a literal's elements are asked their
Kind only (checkForeignElemKinds). The walks carried it too: a field or an element
of filt's variable (namedAcross), a method of q15's type on one (the walk's method
step, which stopped at a third package), and chainNamed. A row of 70 programs, each
position with main importing q15 and not, went from 23 taken to 14, all of the 14
an int16 taken for a q15.Q, the same Kind, where main does not import q15: the
identity strings are SPELLINGS, `q15.Q`, which main cannot spell. And a value
RECEIVED from another package's channel had no Kind at all, `var b bool = <-filt.Ci`
built -- exprType's receive asked exprChan, which knows this package's channels
(chanFactorElemInfo answers now, and for a channel reached through another package's
field, element or call, `<-filt.V.In`, `<-filt.Get()`). Beside it,
in two packages: another package's VARIABLE as a target, `filt.G = true` and `filt.G
+= true` (targetTypeNode had no case for a qualifier, which no scope finds), an
element of another package's array type, `t[0] = true` (indexedTypeNode read no
qualified name), and that package's array literal, `filt.Taps{1, true}`, whose
elements were checked only where they were predeclared. **A boundary sweep crosses
two packages and three**: what one package writes qualified is the case two never
reach.
A fourth domain program the same day, a BMP280-style pressure sensor driver over a
fake bus (four packages: register constants with a Stringer mode, a bus interface
and its fake, the driver's 64-bit fixed-point compensation, main), matched Go on the
host and the board with the datasheet's worked example, 25.08 C and 1006.53 hPa.
Its 1,500 mutants had 5 taken: what a pointer points at into an interface, `return
*P` for an error (derefOperand; the declaration form was the emitter's refusal and
the return and argument built), `&lib.ErrX` of another package's pointer variable
(addrOfQualifiedPtr), and a float constant shifted, `2.5 << 2`. The last was a row
in both directions: Go makes a constant shift of an untyped float an INTEGER
constant, and the folder refused `2.0 << 2` while the walk took `2.5 << 2` -- the
folder's report dropped outside a declaration, as checkConstShiftBound's comment
says of the count. The checker types the level an int at the shift (operandsType),
the emitter likewise (inferNodes), and the float spellings on the left are recorded
as the integers they are (noteShiftedWholeConsts), which foldIntToken now reads --
for EVERY recorded whole constant, so four fuzzer seeds and one run case spell `7e1
% z` as `70 % z` since, each checked on the board.
A fifth, a stepper-motor trapezoidal planner in Q16.16 whose step generator runs on
another cog behind three channels of another package (three packages), matched Go
on the host and the board. Its 1,500 mutants found TWO COMPILER CRASHES, one row:
a type named through a name that is no package, `r int32.Report`, panicked the
type parser (it is Go's "int32.Report is not a type" now). And three taken: a
compound assignment's suffixed target was asked its Kind and not its DEFINED type,
`r.P += n` (the bare variable's was); a target headed by a type, a constant or a
function, `R.P = 1`, was asked nothing (checkNonValueTargetHead); and `const One = 1
<< true`, the folder leaving a bool count unmodelled without a word. **A name in a
type position is a row of every declaration that resolves to no package** -- a
predeclared type, a variable, a constant.
A sixth, a serial command shell (a line editor taking backspace and Ctrl-U, words
split as byte slices, a command table of functions in another package, typed errors
through a type switch), matched Go on the host and the board. Its mutants had 3
taken beside the extension: a call's argument count was skipped wherever the one
argument LOOKED like a call forwarding several results -- `two(g()[:])`, the slice
being one value whatever g returns (exprWholeCall asks that the call be the LAST
step), and `two(lib.G())` and `two(lib.V.Line())`, whose results nothing resolved
across the boundary (exprCallResults) -- and `"s"[0]`, a string literal's element,
had no type (factorType).
The OLDER domain programs of the scratchpad (dom1..dom28, up to 600 lines and five
packages, written in earlier rounds) had never been mutated; three of them, a
firmware of four packages, a controller's hooks and state machine, and a cache over
a hash table, 1,000 mutants each, had two rows between them, both in the
controller: an address stored where its pointee's Kind is wanted -- exprType
answers `&x` with x's Kind, and checkPointerValue, which knows better, was asked
at a declaration, a variable, a return and an argument only (checkPointerInto,
isAddrOperand: a field, an element, a walked target, a literal, a send) -- and a
variable typed only by its initializer, `e := r.buf[0]` of a struct, called.
**A rule asked at some store sites is a row of the others**, as A TARGET IS A
POSITION had it of the store rules.
Fourteen more of them, 500 mutants each, then had 33 taken between them and two
FALSE REFUSALS. The false refusals were one row: a constant shift took its type from
the COUNT where Go takes the left operand's, `15 << classShift` for a `const
classShift int16` an int16 (foldBinary). The taken were mostly one row too: a
variable declared from a FIELD's element, `rt := &r.routes[i]`, had a walked type
and no NAME, which the rules asking a method, a field function, a member or an
interface read -- inferVarFrom names it from the walk now (inferredTypeName), a
named function type with its signature, or `f := tab.fs[0]; f()` was "cannot call
non-function". The 26 batches and the nets showed no valid program lost. Beside
it: a conversion as the whole argument list was taken for a call of several
results (exprCallResults answers one); a call of several results under an
operator or as a condition built for strings.Cut (multiValueOperandErr); `**z` of
a single pointer; a constant index past an array's length that `len` folds before
the emitter sees it (checkConstIndexes); a method promoted from an embedded
INTERFACE had no results (callResults asks promotedIfaceMethod); and across the
boundary, an ARRAY type could not be requalified at all, its length an expression
of the other package -- carried across as the constant it evaluated to now, which
put `bank.Holding[i] = uint64(x)` and `range *bank.Holding` in reach of every rule
-- and a method of another package's type called on a variable was typed by
nothing (headStepsType asks callResults). **The older programs were the
cheapest probe left**: written for other rounds, matching Go, and never mutated.
The last twelve had five more rows: NewBuilder's argument asked nothing
(checkNewBuilderArg), an interface method of no result as a value (zeroResultCall
asks callResults, which knows interfaces and promotion), a TYPED constant declared
of another type (resolveConst), an append's destination that is no name
(checkAppendValues asks the walk), and an untyped operand beside a typed one in a
constant operation, which Go converts to the typed one's type before folding --
`3*MB + -129*KB` overflows at `-129` whatever the whole comes to (foldBinary).
Across all 38 batches, 22,000 mutants, only by-design rows remain taken -- `println`
of an array, a missing `main` (`ogo build`'s), Go's 64 kB channel-element limit --
and one contrived: a constant named `iota` in its own group, `iota` refers to itself.
A new domain program the same day, an LZ77 compressor over a bit writer and reader
with a CRC-32 table, a CRC worker on a second cog and typed errors (four packages:
bits, crc, lz, main), met a FALSE refusal on its first build: lz's `var ErrShort =
&bits.Error{...}` was a `*lz.Error` from main, no type of any package, so `err ==
lz.ErrShort` was "does not implement error". qualifiedValueType named another
package's variable by its home package alone, where its neighbour addrOfQualifiedPtr
asked the variable's own qualifier (carriedQual); the range element reader had the
same gap. And where main does not import bits, the spelling `b.Error` that
foreignQual hands out resolved nowhere, every lookup by a qualified name asking this
file's imports: foreignQual records the package it means (foreignPkgs, unambiguous
names only) and typeDeclNamedIn resolves through it -- which closed a cell the
2026-10-06 third-package row had left, `var c int = l.C` of a `b.Code` where main
imports no b. A row of 28 programs agrees with Go (TestCheckThirdPackageVars). It
matched Go on the host and the board, and its 1,500 mutants had 6 taken, two rows:
a SLICE of a string had no type where it stood -- `s[0]` was a byte to factorType
and `s[1:]` nothing, so `take(s[1:])` for a []byte and `var n int = s[1:]` built --
and an untyped constant BESIDE a non-constant shift, `r.acc & (1<<n - -1)`, which Go
converts to the context's type as it does the shifted one (untypedConstFits), and a
float spelling of the shifted one, `b8 + 1e3>>n`. **A constant in a shift's
expression is asked what the shifted one is.**
A Connect-Four solver the same day -- Pons's bitboards over uint64, negamax with
alpha-beta and a transposition table in a package struct of two arrays, the
20-byte position passed by value at every level of the recursion and through an
interface method -- matched Go on the host and the board, node counts included
(124,209 and 73,028). Its 1,500 mutants had 2 rows taken. A TYPED constant was
range-checked as a whole and never at a step, `uint32(1)<<32 - 1` fitting at the
end; Go checks every operation. The folder's own reports are dropped wherever a
value is folded only to be read, so the step's overflow is recorded rather than
reported (foldBinary under trackedFold) and the two checks that fold to ask --
checkConstFits where a constant meets a type, resolveConst for a declaration --
report the first. Returning an unknown from the folder instead, the first try,
silenced the end check for nine programs it had refused. And a METHOD VALUE the
compiler does not bind, of a local or a value receiver, had no category at all,
exprFuncSig answering only for one it binds: `var n int = p.can` was emitted as
`int n = ogo_mv0;` (nonBoolOperand answers "a function" for any).
A regular-expression and glob matcher (Pike's matcher, recursive over string
slices, and classes with ranges and negation, three packages) matched Go on the
host and the board; of its 1,500 mutants one row was taken: an ADDRESS as an
operand of `&&` or `||`, `&anchored && i > 0` -- the logical operators asked
exprType first, which answers `&x` with x's Kind (WHAT HAS NO KIND IS ASKED
NOTHING's trap, in the one rule a sweep of `&b` in every bool position had left).
A two-pass assembler with a VM to run what it assembles found nothing; a Huffman
coder (a binary heap in another package, the tree in a fixed array of nodes linked
by index, canonical codes, a bit buffer) matched Go on the host and the board, and
its mutants had two rows: a call of several results as a COMPARISON's operand,
`h.Pop() > 1` -- multiValueOperandErr was asked by the arithmetic operators,
the unary ones and a condition, and not by checkComparison -- and a shift count of
a TYPED float, `0x80 >> float64(k)`, checkShiftCount asking only a constant one,
in a binary operation and in a compound assignment alike.
An XMODEM-like transfer between two cogs (frames of 16 bytes with a CRC-16 over a
channel, acks back on another, damaged frames retransmitted) and a text-mode widget
tree (an interface, embedded bases, children as interface values, a table of
handlers, a method value of a promoted method) both matched Go on the host and the
board. Their mutants had one row taken, the compound shift by nil or a bool,
`crc <<= true`, and one gap: a PARENTHESISED TYPE is in no type position of the
grammar, `func f() (a, b (int))` among them, which Go reads as two results of type
int. Rare in Go programs -- `chan <-chan int` already means `chan (<-chan int)` --
and a "(" starting a type meets a result list's under LL(1); left as a gap.
A conversion library's (strconv-like parsing and formatting in bases 2-36 with
overflow, fixed-point decimals) mutants found nothing, and its BOARD build found two
faults no sweep had: an index of 64 bits, `digits[u % uint64(base)]`, went to
`ogo_bound(int, int)` as written, which the target's compiler passes as two words
("Bad number of parameters", refused by the build since backendFault) and which
cut an index past 2^31 to its low word on the host and the board alike, reading
the element there where Go panics -- an int64 variable too. ogo_bound64 and
ogo_bound64u ask it whole, and a slice bound of 64 bits is made an int through
ogo_sbound64(u), panicking where it is none. And probing make's length beside it:
a VARIABLE length was written into the header unasked, `make([]int, n, 8)` for an
n of 20 a slice of 20 over 8 elements, read and written past the backing in
silence; ogo_mklen asks 0 <= n <= cap (emitMakeLen). None of the 1,259 corpus
programs had a 64-bit index, bound or length, nor a variable make length.
**A domain program is run on the board even when the host matched**: the host's
compiler converts a 64-bit argument for its prototype, and the target's does not.
The lesson taken whole the same day: every mutant of every batch that Go accepts
and this compiler took, 5,891 programs of odd shapes, built for the TARGET with
`ogo build` (tbuild1.sh in the scratchpad). 5,860 built silently, 11 exceeded Hub
RAM (mutated sizes, now said as the program's), and 20 were faults of ours, five
rows, each loud now or right: a local array of AGGREGATES given fewer elements than
its length, which the target refuses where C zeroes the rest, and an array of ROWS
the same even static (padLocalAggregates pads with zero elements, measured in C
first); a division by a NEGATIVE constant, `x / -129`, guarded because foldConstInt
read a UnaryExpr's children as no constant, and the constant then passed to the
64-bit helper as one word -- "Bad number of parameters", the one warning; a
deferred call of ANOTHER package's function taking an interface, its argument
captured as the bare pointer; a call's result of a predeclared type into `any`,
exprNamedType naming it by its written type `int`; and a METHOD VALUE of a local
outside a declaration -- refused by design and asked only of `f := c.m`, so a print
argument, an argument, a field or a typed declaration lifted a function naming the
local where it is none, and where a PACKAGE variable of the name existed, bound
that one, in silence (a fuzzer mutant's `gm := M{}; var th thunk = gm.tick`).
**The target's build of every program the probes left behind is a probe of its
own**, and it is cheap: 40 minutes for six thousand programs.

The run cases' own mutants, 5,368 of the front-end batches, built the same way had
7 refused by the backend, and two were one fault of the target's parser: a STRUCT
parameter or receiver passed by value and named like a member of its own type that
holds an aggregate -- `func (n Named) String() string { return n.n }` over a string
field -- is "Expecting identifier after '.'" (doc/param-named-like-member.c); a
scalar member, a pointer parameter and a local of the name are right. Such a
parameter is received as `_ogo_<name>` and copied into a local of its name on entry
(memberShadowParam). The rest were rows, each closed the same day. A slice, an
array, a function or a channel of a type written out into `any`, refused by design
and asked by the checker of named types, Kinds and literals only (checkImplements
asks nonBoolOperand now); only the declaration had been refused, by the emitter. A
method of SEVERAL results through an interface as a STATEMENT, `r.Read()`, went out
without the storage its results are written through ("Bad number of parameters"),
and through a chain was refused; with it, an interface reached by a CALL,
`get().Read()`, was typed by nothing in any position -- statement, declaration,
return, argument, `go` -- ifaceChainMethod asking accessChainType, which knows no
call head (chainResultCur answers now), and `get().Arr()` called get twice, the
table and the data each reading the text (ifaceRecvText binds what is no lvalue).
A package slice literal whose elements call, `var xs = []int{f(1), f(2), f(3)}`:
emitArrayLitVar bound the values for their order where no package-init step
renders, and the step named temporaries nothing declared (bound inside the step's
render now, pkgInitRender). And a struct holding an array of no elements: `{0}` for
a struct LEADING with one is refused by both compilers, the target "Cannot handle
memref of size 0" (leadsZeroSized writes the zero out, the array's as `{}`). The
target still WARNS, "Extra initializers for array", about `{}` for an array of no
SCALAR elements, `_ [0]func()` -- measured harmless on the board in every position,
designated initializers warning alike -- so that shape is refused by `ogo build`,
loudly.

**A ROW OF 64-BIT ELEMENTS INDEXED BY A CALL IS LOST ON THE TARGET** (2026-10-06,
found by a domain program: a Kalman filter over 2x2 matrices of Q16.16 in int64,
zeros on the board where the host and Go agreed). flexcc reads and writes
`r[f(i)][j]` of an int64_t or uint64_t array of two dimensions or more wrong -- the
store lost, the read garbage -- where one dimension, int32_t elements, a call in the
column alone, a struct row and arithmetic in the row are right
(doc/wide-row-call-index.c). A checked build's bound check is a call, so every
element of such a table with a variable row index was wrong in silence, in every
release; no run case, fuzzer program or domain program before had a 64-bit table of
two dimensions indexed by a variable. The row is written through its first
element's address, `(&r[I][0])[J]` (wideRowC, from accessDeref), which the target
reads right. **A numeric domain program crosses the element WIDTHS with the
dimensions**; the sweeps had crossed the widths with the positions.
Its 1,500 mutants, judged once the board run had passed, had 31 taken, two rows.
Arguments of a method of ANOTHER package's type called on an element of its DEFINED
array type, `dk[1].Mul(q)`, or on a conversion, `lib.Q(3).Mul(q)`, were asked
nothing: callChainWalk returned at once for a variable of another package's type
whose first step was an index, and the index step carried no home package where the
field step did. Beside it, a value two steps or more from its variable with no call
between, `m[0][1]` of a `type M [2][2]Q`, `h.k[0]`, `w.in.v`, had no type NAME at
all -- localValueNamedType names a value one step away, plainChainNamed walks the
rest -- so `var x Q2 = m[0][1]` for a Q went through, 60 of 80 programs of each grid,
in one package and across two. And a method of several results called on a literal,
`_ = P{1, 2}.Two()`, `take(M{...}.Inv())`, a typed assignment, was asked no count
(exprCallResults answers a literal's and a parenthesised value's method now), where
the valid forwarding of one, `take2(P{1, 2}.Two())`, was refused as an argument
short; and a call of several results as one value of a return, `return two()` from
a function of one result, was the emitter's to refuse, and is the checker's. Making
the forwarding valid in the checker handed it to an emitter that could not take a
literal's call apart -- C passing the result struct for its fields: resultCallOf
binds the literal, once per statement (litMethodHead's memo), and only for a method
of several results (litMethodMulti), so a literal's method of one result costs no
temporary more. The last mutant taken was the result's row from the other side: a
result of NO Kind -- an array, a struct, a slice, a pointer -- assigned from a call
of several results into a variable of a Kind, or forwarded to a parameter of one,
was asked nothing, checkResultsAssign skipping every result without a Kind, for
every callee (kindlessResult, resolved in the file that wrote the type). **A false refusal fixed in the checker is a program the emitter is
handed next**: the valid twin of every shape a new count or type rule settles is run.

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
to a channel type, `(chan int)` and `(chan<- int)`, to `[3]int` of an array
literal, and to a function or struct type in parentheses, which does not parse,
are still refused, loudly. Grids of the
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

A round of 2026-10-15, the first since the language took the errors package, Go's
make for channels, buffered channels and out parameters for the results the backend
returned on a heap -- a rheometer pipeline (a Q32.32 viscosity, a float32 IIR filter,
samples through a buffered channel from a cog, wrapped errors examined by errors.Is
and errors.As), a command interpreter (parse errors through errors.As, a struct
holding an array returned beside an error, embedded registers behind an interface,
variadic calls) and a motor controller (a type switch over events, a deferred note,
a ring from a cog, a biquad printed by %g, %e and %v) -- matched Go on the host and
the board but for the coefficients a float64 computes (a float32 today,
DIFFERENCES.md) and three loud refusals. The first was the interpreter's first line,
a command word dispatched by `switch string(word)`: "a string conversion needs
allocation", in every position, `string(w) == "SET"` and `len(string(w))` too, where
Go's compiler builds no string but reads the bytes where they are (order.go's
OBYTES2STRTMP for a comparison, walk/switch.go's for a switch whose cases are
constants, isByteCount for len). Those positions take a view now (byteStringView,
ogo_bytes_view), a comparison only where the other operand calls nothing, which
could change the bytes before they are compared; anywhere else the conversion is the
allocation it is, and the refusal says where it is not. Sweeping the spellings found
the checker refusing `string(nb)` of a `type Bytes []byte` as "cannot convert", a
conversion Go takes (byteOrRuneSlice). The view's first version failed the run case
built --unchecked, "fit 480 failed": the backend inlines a function as small as the
helper by itself, untold, and every inlined copy brought five registers of its own
to where it landed -- twenty comparisons took 120 registers where twenty of a string
took 27. Kept a call (noinline) they take 27 (TestBuildByteViewFrugal reads the
listing). **A refusal for what a construct costs asks
what Go's compiler pays for it where it stands: an allocation Go does not make is
not one.** The third: a goto anywhere in main, a forward one too, took from main's
outermost block the lifetime that lets a channel made there go to a goroutine, the
refusal advising to make the channel "in main", where it was made. Only a goto back
can run a declaration again, Go refusing a forward one that jumps over a
declaration into its scope, and only that one is asked now (backwardGoto, one
helper for the checker and the emitter); the advice says "main's outermost block".
And a method value of an element or a field, `regs[3].get` or `dev.Reg.get`, was
taken by the checker and refused by the emitter as "rs[2] has no field get" or
"cannot infer a type for the declaration" -- the checker asked a method value's
rules of `head.field` alone, at a declaration and, in any other position, only of a
suffix of one selector. It refuses the chain in every position now, naming a
function literal as the way round (reportChainMethodValue); binding a constant
element's or a field's address, which the rule's reason allows, is left for later.
Its 4,800 mutants (gendom and genvdom, 800 of each per program) found one hole: of
the three Go refused and ogo took, all were the checker of each statement form, the
arguments of a method called on a name and
followed by more steps, `p.Add("x").Show()` or `k.Mul(int64(k)).show()`, were
checked in a value (leadingMethodCall, added for `gt.bump(1.5).n`) and asked nothing
standing alone or under defer and go -- three copies of the same arms, and two
lacked the one the third had learned. **One question, one helper**, and the
statement forms ask it now (leadingMethodCallOf for a node that begins with its
keyword). The 110 Go took and ogo refused were the rules as written -- unreachable
code (62), printf's arguments, a defer in a loop, a local's address kept, a non-pointer
errors.As target, `go println(...)` -- but for one message naming a channel by its C
type, "%d wants an integer, not ogo_chan__Bool" (goTypeName). Of the 2,455 both took,
234 printed otherwise than Go and every one was the noise the method knows: a pointer
printed, the panic's wording, the motor's loop timed against its sampler cog, a loop
run away; none silent. The false-refusal nets (12,085 programs) refused nothing new.
A round of size followed the same day: a module of five packages written as firmware
is -- Q16.16 fixed point formatting itself through the predeclared Builder, sample
sources behind an interface, filters chained through an interface array with a
decimator embedding a moving average, a protocol parser of typed errors answered
through a Builder, and a main filtering what a cog produces through a buffered
channel -- with a test file in each package. It matched Go on the host and the board,
`ogo test` of every package against `go test -v` line for line (the times aside: the
runner prints none). Of its 1,600 mutants, one was silent where Go panics: a nil
*Builder handed to the formatter crashed the host and wrote into the first longs of
Hub RAM on the board. The Builder's methods are C helpers, and a checked build's nil
check lives where a body dereferences: they had none. **A method the compiler
writes in C asks of its receiver what a body's dereference would** -- the helpers
begin with the check now, in a checked build.
A round the same evening wrote what the others had not: a pipeline of three cogs
over channels -- `defer close(out)`, `for range` of the closed channel, an ARRAY of
channels a select drains, each set to nil when it closes, a labeled break out of
the loop around the select -- set up from a configuration a stateFn lexer reads,
and reports dispatched by a type switch. It matched Go on the host and the board
once its four channels were made in main's block (made in a loop, `outs[k] =
make(...)`, one make site is one cell, refused as such), and a probe of select's
evaluation (every case's channel and sent value once, in source order, a nil
channel never ready) matched too. Its 1,600 mutants found the checker's hole:
`c := l.src[l.pos]`, a byte of a string reached through a field, had no type --
indexedStringKind typed the SLICE of such a string and not the index -- so `c[0]`,
`c()`, `c - '=' || x` and `c = "x"` were taken, where the same of `s[0]` were
refused; and `l.src[i] + 1` was nothing where it stood (exprType). One helper,
indexesString, answers both now, through parentheses too. **A value nothing typed
hides every misreading of it**, again: the shape that typed was the variable, and
its field was the row.
A data logger the same night -- records packed into bytes with a CRC-32 table its
init computes, `1 << iota` flags with a String method, a log that is an array type
with methods, an insertion sort through a function value, a binary search,
fallthrough, a labeled continue, a goto -- matched Go on the host and not on the
board: its histogram, `hist[r.Ch][k]++`, printed zeros. The C was right; the target's
C compiler loses `m[f(i)][j] += 1` -- a compound assignment to an element of two or
more dimensions whose index before the last calls -- for 16- and 32-bit elements,
and refuses it for 8 and 64 (doc/row-call-compound.c). An increment is written that
way where its target is no plain name (incDecC), and `r.Ch` through a pointer is a
call, the nil check's. hoistCompoundTarget had bound such a target's address for a
`+= v` and left `++` alone, "the backend gets those right"; doc/wide-row-call-index.c
had measured the 32-bit element right -- for a PLAIN store. **A fault measured in one
statement form is measured in the others: a plain store right says nothing of its
compound form.** Binding the address for an increment whose index before the last
calls (rowIndexCalls) fixes it; a byte table's `++`, refused before, builds. Its
1,600 mutants found nothing more: the 828 both took, 427 distinct, matched Go on the
host and the board but for what the method knows -- a panic's wording, a line
printed in part before an argument panics, a String method printing into printf's
line, a race a mutant wrote (`go put32(b[16:], ...)` over main's buffer), and a
table grown past Hub RAM, refused as the program's size -- and the 12 Go took and
the compiler refused were unreachable code and printf's argument counts. Nine more
the host harness refused: `c&1 != 256` is a comparison gcc calls always true, an
error under -Werror and a warning TestEmitCRun fails on, while the target builds it
silently -- another valid program that cannot be a run case as it stands.
The rule's other direction was asked the same night: `[]byte(s)` and `[]rune(s)` of
a string variable were refused wherever they stood, and `go build -gcflags=-m` calls
the first a "zero-copy string->[]byte conversion" ranged over, measured, copied and
appended from -- and passed to a function whose parameter neither escapes nor is
written, a judgement across calls the summaries here do not make, so that one stays
refused -- while `len([]rune(s))` is countrunes (walk/builtin.go, isRuneCount). Those
read the string where it is now (stringSliceConvOf), and so does a range over
`[]rune(s)`, which Go copies past 32 runes and which needs no copy: its elements are
the string's runes, decoded in turn. **What Go's compiler copies is asked of it,
`-gcflags=-m`, not remembered.**
A rheometer's command interpreter on 2026-10-16 -- SCPI lines from a receiving cog,
a parser of views into the line in one package, fixed-point quantities with units in
another, a table of handlers holding function values and a method expression, a
queue of typed errors read back through errors.As and errors.Is, a heater cog on a
buffered channel -- met the method value left for later at its first build: a table
of switches over the instrument's parts, `[...]func(bool){inst.motor.Enable,
inst.heater.Enable}`. With a function literal in its place everything else matched
Go on the host and the board, `range []byte(s)` and a `switch string(buf[:n])` among
it. A receiver reached from a package-level variable through fields of struct values
and constant indexes into arrays is bound now as the variable is (methodValueRecv):
Go takes the same address when the value is taken, and nothing moves it. What Go
reads then -- a pointer on the way, a slice's header, an index -- the checker refuses
by name (chainRecvUnfixed). Sweeping the receivers found three the checker had
passed to the emitter, "cannot infer a type" with no position, each a type the
method value rule did not resolve because it asked by name: a variable of a
defined array type, `row.bump`, which varType does not answer for; a field of
another package's type, `s.q.Inc`; and a method promoted through an unnamed struct,
`x.get` of an `x struct{ *Reg }`, taken where its way passed a pointer. **A rule
asked by name is a row of every type that has none**, again. A method value in
another package's own initializer, `var Fns = [2]func(){V.In.Inc}`, was found by the
same sweep as the binding's first version refused it: a chain's base is asked
whether it is a package variable by the name it is registered under
(isPackageVar), not the one written.
The sweep's positions found a silent fault older than all of it: a print of two
calls of a parenthesised method value, `println((rs[2].get)(), (rs[2].get)())`,
printed `2 1` on the host. Any function value called in parentheses, `(fn)()`,
`(next)()`, `(fs[1])(5)`, was no call to the walk asking whether an operand calls
(nodeHasEffect), which knew a bare name's call, a method on a parenthesised receiver
and a call through a written-out dereference, so two of them in a print, in one
call's arguments or in one level were left to C's order: right to left in gcc's
arguments, and the count first in the target's shift, `(f)(1) << uint((f)(2))`, on
the board. A parenthesised value of a function type called right after the
parenthesis is a call now (parenValueCalled), a conversion's parenthesis holding a
type. It is the fifth shape of the one question that walk answers, each found by a
probe after the walk was believed whole -- `T.M(x)`, `T(3).bump()`, `(&v).Sum()`,
`(*pf)(x)`, and now this one. **A call is asked of every head a call can follow: a name, a
parenthesis, a dereference, a conversion, a call.**
Rewriting the probe's statements into another package found a third: a call
STATEMENT through another package's function table, field or call, `lib.Fns[0]()`,
`lib.V.F()`, `lib.Pick(1)()`, of no result or of several, was "only <pkg>.<Func>(args)
... call statements are supported yet" -- the in-package forms and the deferred and
started ones all worked. The statement's path types the callee by the chain typer,
which starts from no import qualifier, and fell back to rendering it only for a chain
that begins with a call; it falls back for a qualified one too now. **Every row swept
in one package is swept across the boundary too, with the qualified spelling in
every position**, again, and the statement is a position.
Its mirror was the checker's: a call of NO result where a value is wanted --
`var x int = fns[0]()`, `println(h.cb())`, `take(lib.Void())` -- was refused by Go
and passed by the checker for every callee but a function and a function variable
named alone, the C compiler refusing the void (zeroResultCall, which asked a
method's results by name and answered false for whatever else it met). The callee is
typed through whatever steps reach it now (calleeSig), and the sweep of thirteen
callees by eight positions is refused in every cell, in Go's words.
The program's 1,600 mutants (gendom and genvdom, 800 each) found one hole: of the
one Go refused and the compiler took, `part[0], line, more = scpi.Next(line)` stored
a call's results into a byte of a string, which the statement's rule asked of a list
of values and not of a call's results (checkWalkedTargets left those to
checkResultsAssign, which asks types); the rule asked of each target there now, and
the sweep of its row found every header and a for's post statement taking one too,
`if s[0] = 'x'; ...` and `for ...; s[0]++ {` -- separate checks again, none asking
it (stringElemTarget, one helper for all of them). **A STATEMENT IN A HEADER IS THE
STATEMENT**, again. The 56 Go took and the compiler refused were the rules as
written: unreachable code (53), a printf verb its argument does not fit, a value
receiver's field's address returned. Of the 782 both took, 83 printed otherwise
than Go: 78 a panic's wording, three a deadlock Go detects and the host waits out,
and two a deferred println -- in the receiving cog, racing main's line, and in the
String method printf calls, printing mid-line as DIFFERENCES.md says. On a P2-EDGE
the 505 distinct ones both took, but two deadlocks Go reports, printed what Go
prints: 489 at the first load and the ten the loader lost at the second, the other
four the same noise -- a deferred println in a cog and in a String method, and two
loops grown to 2^31 passes that the board had not finished when the capture ended.
A firmware over Spin2 drivers on 2026-10-16 -- the first domain program carrying
objects of its own: a package whose bodyless functions bind to PUB methods of inline
PASM driving the CORDIC (QMUL, QDIV after a SETQ, QSQRT), one of Spin2 byte routines
with a pointer result into its DAT, a generator whose PASM runs on a cog of its own
serving a mailbox the OctoGo side spins on, a child object beside it, and two
packages carrying the same counter object, each its own instance; a controller
computing the generator's increments through the CORDIC package, an interface over
the counters, a driver goroutine fed from a profile and read over a channel. Built,
it printed on the board what its Go twin prints, Go functions standing in for the
objects. Building it found a wall and a fault. The wall: a CRC over a function's
local buffer was refused, a function a Spin2 object implements being taken to keep
every pointer it is handed -- right for a driver, wrong for a routine that reads the
buffer and returns, and Go's own answer for a function without a body is
`//go:noescape`. The fault: `cordic.DivMod(uint64(rpm*Poles)<<32, d)` was refused
for a backend warning, "Unable to widen function result", and the warning, read in
the backend's source (FixupFunccallTypes), was the cause of doc/call-arg-after-expr.c
a month after its workaround: an argument the compiler computes as a pair of words
-- every 64-bit operation, a negative constant, a shift by 32 -- is matched word by
word against the parameters, and every argument after it is converted to the type of
the parameter after its own. Silent on the board, the host right: a float after `-m`
arrived converted to an int64, 0 for 2.5; an int32 as the bits of a float; an
integer constant to a float parameter unconverted -- through a method, an interface,
a function value and a variadic call, and a 64-bit value receiver is the first
argument too. The workaround of 2026-08-30 had been written from the symptom it was
found by, a constant to a 64-bit parameter losing its width, and its table measured
a float after the expression LAST in its call, where nothing follows to be misread:
it covered one row of the fault. An operation passed where an argument follows it is
bound to a variable now (pairArg), as the table's own `t = (0 - m); mix(t, 3)` row
measured right. **A workaround written from a symptom covers the symptom: the
backend's source is read for the cause before the workaround is called whole**, and
**a row measured last in its argument list says nothing of the same argument with
another after it**.
The program's first build had met something smaller: a method on a literal of an
empty struct, `Soft{}.High(a, b)`, a stateless implementation of an interface, was
"cannot infer a type" with no position. The literal's path asked whether its type was
a struct by the struct's field list, which for an empty struct is nil; four more
places asked the same way, and a struct holding an empty one, an array of them and an
array of such structs were compared with C's ==, which C refuses for a struct. Asked
by presence, they had a second answer to give: an interface is in the same table with
no fields, and its helper would have called any two equal -- a struct holding an
interface, and an array of interfaces, had been refused by C the same way, and compare
by table and data now, as two interface values do. **A table read by its value is
asked by its key, and asked what else it holds: an empty struct's fields are nil,
and so are an interface's.**
The wall became the directive the same day, Go's own, `//go:noescape`, on the
user's call: a function a Spin2 object implements keeps nothing under it, and the
CRC of the program's local buffer builds and matches Go on the board. Its placement
is held to gc's, measured case by case -- a blank line between it and the
declaration, other comments around it and an indented one apply; above another
declaration, in a body and after code on its line it is "misplaced compiler
directive"; above a body, "can only use //go:noescape with external func
implementations"; with a space it is a comment. And it was the formatter's next
probe: gofmt rewrites every doc comment (go/printer's formatDocComment, its text
through go/doc/comment and its directives moved last behind a `//` line), which no
program had shown, their doc comments being canonical prose already -- a golden had
pinned `//   This is a worker function` as written, where gofmt writes one space.
The port's condition was read off go/printer's source, and read wrong once: "not
after an import", where `lastTok` is set to the token ABOUT to be printed before it
is asked, so the comment left alone is the one above an import. **A variable the
oracle's source asks is read where it is set, not by its name.**
Running every source of the tree and of p2-11 through both formatters after the
port left one difference, p2-11's `[Rows + 1]string{...}`: the formatter gave an
array type's length a subscript's depth where go/printer prints it with `p.expr`,
at depth 1, a comment in the code calling it "a subscript position". The sources a
user keeps are the formatter's corpus as much as the run cases are.
The fault had a second member the fix did not reach, and the fuzzer found it on
its first board sweep: a part written for the shape (genPairs: functions of a
64-bit parameter and then others, handed an operation first, directly, through a
function value, an interface, a variadic call and a receiver) failed `pairs` on
seven of the first ninety-three seeds and was refused by the backend on seven
more, every one passing on the host. A checked build calls a function value
through its nil check, `ogo_nil_..(cb)(...)`, a pointer a CALL returns, and there
the target's C compiler passes the HIGH word of every 64-bit operation wrong -- in
the last place too, where the first fix left it, and with a single parameter,
`c1(m*3)` -- and refuses a 64-bit constant outright. In every release:
`cb(m, 30, m*3)` was 110104252 for 868 under v0.56.0. A call through a function
value binds every 64-bit operation and constant now (valueCallArgC). The reproducer
written for it first printed right, its callee reading only the low word of the
argument the fault garbles: **a callee in a reproducer reads every word of what it
is handed**. And the part had been asked to fail before it was believed -- built by
the compiler before the first fix it failed or was refused on every seed carrying
it -- which says it can see the fault it was written for and nothing of what else
it can see: **a fuzzer part written for one fault is run on the board before its
fix is called whole**.
The same evening the row was swept on its other side, and the fix was not whole
after all. Through a pointer a call returns, a 64-bit argument arrives wrong
unless it is READ from storage -- a widening conversion, `cv(int64(i32))`, and a
call's result, `cv(id(3))`, as well as an operation, measured on the board, where
v0.56.1, released that afternoon, had bound only operations and constants -- and a
64-bit RESULT of such a call is read as one word in an operation, `w(x) + 1`, a
refusal. Every 64-bit argument there that is no name is bound now
(valueCallArgC), and so is a result read in an operation (valueResultC). The fuzzer
part, extended to those shapes, failed on its first host run, and not for them: a
wide constant beside an argument that does something, `take(1<<40, side())`, was
bound at the type it defaults to, `int`, when the call bound its arguments in order
-- 1 for 1099511627777, in every release, on the host as on the board (hoistArgs).
The one path that binds every argument had never been handed a constant wider than
an int beside an effect. **A row is swept on both sides, what goes in and what
comes back, before its fix is released**; **a fuzzer part widened to the shapes
next to the fault finds the faults next to it**.
That night a domain program of fixed-point signal processing (dom51: a Q32.32
type, a biquad, a CIC decimator, Goertzel detectors, a PID loop over a plant, a
statistics accumulator, every intermediate an int64) met the fault's last member
on its first build for the target: `(int64(num) << 32) / int64(den)`, a Q32.32
division as Go programs write it, refused by `ogo build` over the backend's
"Unable to widen function result". pairArg binds a shift by 32 handed to a call of
the PROGRAM's; this one was handed to `ogo_div_int64_t`, the emitter's own, and a
sweep of the shift through the helpers found every one of them warning -- a
remainder, a guarded shift, min and max, a conversion to a float, a print, a send,
an append, a division assigned -- in any place, alone included. The code was
right (the program built past the warning printed Go's values on a P2-EDGE), so
the fault was loud; the cure was to stop writing the shape the backend misreads
at all, the shift spelled as the call of an inline helper that the backend
reduces to the same two moves (shl32C), rather than to bind it at a dozen sites.
**A backend fault in a call is a row of every call the C makes, the emitter's own
helpers among them**; **where the backend misreads a spelling everywhere it
stands, the spelling is changed where it is written, not bound where it is
used**. The program was otherwise right on the board the first time, its 64-bit
rows (operations before floats, through function values, interfaces, variadic,
deferred and started calls, sends, multi-results, `MinInt64 / -1`, shifts past 64)
matching Go, the fixes of the day holding. Printing `string(b)` of a byte slice is
refused as documented; `%s` of the slice itself is the spelling that needs no
copy, in Go as here.
The same night a switch on an integer whose every case is a constant became a C
switch (switchCaseLabels), asked for by another project that had seen the chain of
compares in the C and measured on p2-11's emulator, whose decoding is such switches.
The backend dispatches a dense one through a table, `fle` to bound the index and
`jmprel` into a row of jumps, and a sparse one through compares; its switch on a
64-bit value is unbounded and hangs the board (doc/switch-64-bit-tag.c), so those
stay chains. The emulator came out where it was: its benchmark 0.3% faster, the
mean of its 51 instruction shapes 0.2%, and the shapes spread from 18% faster
(`BCS` not taken, deep in its chain) to 5.5% slower (`SOB`, first in its own) --
the table's two taken jumps, each a refill of the FIFO in hub execution, against a
first case's one compare. The emulator's switches were ordered by a profile, "the
ones that come most often are asked for first", which is a measure of the chain and
not of the table; a hybrid testing the first clause before the table won the first
cases back and lost the rest, its mean -0.3%. The plain switch stayed for what Go's
own compiler does (cmd/compile/internal/walk/switch.go sorts constant cases and
dispatches through a table or a binary search), which makes order no cost, and for
the fallthrough it no longer copies. And the journal had it the other way round:
item (3) of A PROGRAM OF SIZE (2026-09-27) calls a switch's chain of compares the
backend's, a fact programs are written against -- it was the emitter's, and the
emulator had been written against it. **A speedup is measured shape by shape on the
program that asked for it: a mean hides who pays**; **a source ordered for one
lowering is a measure of that lowering, not of the one replacing it**; **a cost
written down as the backend's is checked against the C the emitter writes**.
The suite found a gain nobody measured for:
TestBuildInlineFallback's program, thirty-two marked helpers called from the arms
of one switch, outgrew a cog because the backend gives no register back between the
arms of a chain, and fits as a C switch, whose arms it does give them back between;
the test writes its arms as a switch with no tag now, a chain still. And the board
sweep of the changed seeds found the gain run the other way: seeds 26, 72 and 73
fitted a cog before and outgrew it with C switches, main holding fifteen locals more
than it had as chains. The fallback that keeps a program from failing to build for
having been made faster had one step, the marks; it is a list now (compileMarked,
plainerC), the switches as chains first and then without the marks, each tried only
where the one before outgrew and skipped where its C is the C tried, and the three
seeds build to the binaries they built to before. Seed 73 is the fallback's test:
it outgrew with its marks before, and the one step left it unmarked and still
switched, over the limit. **A change that saves registers in one function is asked
what it costs in another, and a fallback with one step is asked what a second
change needs of it.**
The fuzzer had been asked nothing the new lowering changed: genSwitchStmt's switch
is on an int, its cases the tag and the three values after it, one clause with a
body and none falling through -- the C switch's smallest table and none of its
labels, falls or placements. A part of its own was written the next morning
(genSwitches, swRand): tags of every integer width and both signs, a defined type's
constants, a tag shifted, masked, xored or calling, cases next to the tag, far from
it and at the ends of the type, a default first, between and last, fallthrough into
the clause written next and out of a default, a break inside a clause, a switch in
one, a switch in a loop continued and broken out of by its label. What it computes
was run as Go first -- the generated programs are Go but for a Builder, and all 50
of seeds 1-80 that Go builds and that carry the part print OctoSmith OK under Go --
and then asked to fail: a compiler that writes a break where a clause falls through
fails 24 of the 47 seeds of 1-60 carrying it, one that writes the default last 12,
and no seed without the part fails under either. **A generator's model of a
statement is held to Go before it is held to the compiler.** On the board the part found what
the host could not: of the 140 seeds of 1-200 carrying it, 127 passed, 10 outgrew a
cog as they had before, and three were killed by the sweep's cap of fifteen minutes,
where the compiler before the C switch built the same programs in one to five
seconds. Each came down to one switch of an int32 whose cases lay further apart
than MaxInt32 -- a cluster near 152696 with 2147483647 and -1585330326 among them --
and that to the backend's sort of the cases, `return a->val - b->val` in
frontends/case.c, a difference that overflows, so the order the sort is handed
contradicts itself and the transpiled qsort runs on without end
(doc/switch-case-span-overflow.c). Such a switch is a chain now. The table the
backend builds next reads the first case and the last as the least and the
greatest, so a native build, whose sort may return, could build a wrong table from
the same switch. **A build the sweep's cap killed is built by the compiler before
the change before it is called slow.**
The fuzzer's arithmetic had a rule nobody had written down as one: the VM's
operators refuse what C leaves undefined -- an int that overflows, the most negative
value over -1, a shift by a count outside the width, a negative value shifted left
-- and fall back to XOR, each with a comment that the emitter never emits such a
form. Emitting exactly those is the emitter's business, Go defining every one, and
so the guards it writes for them -- ogo_shl_T, ogo_div_T, ogo_nonzero -- had never
been reached by a generated program. A part of its own (genWraps, wrRand,
2026-10-17) writes them: overflow at every integer type and int among them, the
most negative value over -1 through variables, constants and calls, shifts by
counts of every integer type at the width and past it, truncating and extending
conversions, each assigned through a local, a package variable, an element at a
constant index and at a calling one and a pointer, and the comparisons that test
for an overflow. What it computes is Go's own arithmetic in Go, held to Go first:
the 150 seeds of 1-300 that carry it and that Go builds print OctoSmith OK under Go.
Asked to fail, it did: a compiler whose shift helpers ignore the width fails 33 of
the 64 seeds of 1-100 carrying the part, one whose division forgets the most
negative value over -1 traps in 33, and none of the 36 without the part fails under
either.
On the host oracle 11 of the 255 seeds of 1-400 carrying it failed, every one at a
shift assigned through a pointer, `*wp1 <<= wrKU16b_1`: the statement writing `*p
op= v` set the target's type only for a target without a star, and the guarded
form, given no type to name a helper by, fell back to C's own `<<=` -- on the board
a count of 32 or more taken modulo 32. Through a field of the pointee the type was
known, as it was in every other target form. Two probes of the same row, written by
hand beside the part, found two more: a uint64 count of 2^63 or more was handed to
the helper's int64_t and panicked as negative, and a division assigned to an
unsigned integer had no check of its divisor at all -- a signed one's lives in its
helper, an unsigned one has no helper, and `z /= d` went out as written: 255 on the
board for a uint8 divided by zero, where `z / d` panicked. **A fuzzer that keeps to
what C defines never reaches a guard the emitter writes for what C does not: the
oracle's arithmetic is the language's.**
On the board the part found what the host could not: of 201 seeds carrying it, 179
passed, 17 outgrew a cog, and five failed its checksum, each at a uint32 or a uint
divided by 4294967295 or 4294967294 -- `wrAUn_1[wrIdx_1(0)] /= WrMaxUn_1`, `(wrU32b_1
/ 4294967295)` -- found by printing the running value after each statement on the
board beside the generator's. In C alone the board printed 4294967291 for `5 /
0xffffffff` and 2 for `5 / 0xfffffffe`, and CompileDiv in outasm.c says why: its
fast path takes an immediate that is a power of two or the negative of one before
it asks whether the division is signed, and an immediate is an int32_t, so every
unsigned divisor 2^32 - 2^k is -2^k to it -- a NEG, a shift right by k, an AND with
2^k - 1 (doc/udiv-negative-power.c). It is the backend's second misreading of an
unsigned constant in a day: the case sort of switch-case-span-overflow.c holds a
case past MaxInt32 as the same int32_t. Such a division is written as the compare
it comes to, a quotient by a divisor of 2^31 or more being 0 or 1. **An unsigned
constant past INT32_MAX is a negative number to the backend: every place it reads a
constant's value is asked of one.**
Three days on, the next board sweep (seeds 9001-9500: 438 passed, 61 outgrew a cog)
found the same misreading one word wider, in the emitter's own fold. Seed 9051 failed
the part's checksum at `wrU64c_1 % WrMaxU64_1`, a uint64 by the largest uint64, and
so did the host, whose oracle corpus no seed carrying it is in. The emitter folds a
constant to an int64 and holds a uint64 past the int64 range by its bits, so 1<<64 -
1 is -1 there, and the remainder by 1 or -1, which is written as the multiplication
by zero it is (the target answers x), wrote `x * 0`: 0 where Go has x, through every
spelling of the value with a name in it, `math.MaxUint64` and an untyped `1<<64 - 1`
among them. A -1 in an unsigned type is that value, and the rewrite asks the
remainder's type now (remByOne). The row, the uint64 constants past MaxInt64 under
every operator, both ways round, compound and shifted, printed Go's 465 lines on
the board and led to the next shape: the checker held a count of a TYPED constant to
a uint's range, which Go asks only of an untyped one, so `x >> M` was refused where
Go has 0; let through, the guarded shift took its bits as an int64's -1 and panicked
"negative shift amount" (shiftCountU64 hands it to the helper of 64 unsigned bits).
The row's next column, the
same constants as sub-expressions in every position a value goes -- operands,
initializers, elements, fields, returns, cases, conversions, arguments -- found the
argument: a constant handed to a 64-bit parameter is spelled at its width
(wideConstArg) with the int64 fold's value, so `f(M / 3)` passed 0, `f(M % 7)` the
largest uint64; it is folded at the parameter's type now (constIntValueIn), as an
initializer always was. **An unsigned constant past INT64_MAX is a negative number to the emitter's fold: a
decision taken on a folded value's sign, or on its being -1, asks the type it is
of.** **A rule written for an untyped constant asks first whether the constant is
typed.**
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
A MULTIPLE ASSIGNMENT was a position nobody wrote one into until 2026-10-07, when a
domain program -- SHA-256 with HMAC beside 2^256 arithmetic on eight limbs, three
packages, the published vectors as its oracle -- wrote `a, b := d.Sum(),
sha256.Of(p)` and `x, y = u256.From(0), u256.From(1)`: "cannot infer the type of a
value in a multiple assignment", with no position, for any call returning an array,
in one package as in two (emitValueList binds it into a temporary in its turn, as
emitDiscard does). The list declaration beside it, `var p, q [2]int = mk(1), mk(2)`,
was "not supported yet" where the two lines it stands for built (emitVarList is the
single declaration name by name for an array type). The program then matched Go on
the host and the board, a million 'a's hashed on another cog in 90 s. Its 1,500
mutants had three rows taken: a destructured call's result of no Kind into a
target of another category or another array, asked only against a target of a
Kind (kindlessTargetOf, otherKindless; a `:=` assigning an existing name asks
checkResultsAssign too, and two defined types are told apart by their declarations
past any alias, definedDeclOf); a TYPED float constant as an array or a make
length, the whole-float rule for an untyped one having taken it (arrayBound,
checkMakeBounds); and a SLICE literal, which litOrConvType typed as nothing -- an
array literal had a type and a slice literal none, so a range value over one was
asked nothing. One cell refuses where gc takes it: `1 << F` for a typed float
constant F of whole value, which go/types checks for an integer type only where
the count is not a constant -- the spec's words refuse it, and so does this.
Its valid mutants, run against Go, showed one order of ours: `result{j.id, d.Sum(),
d.Blocks}` with Sum a pointer method read d.Blocks before Sum ran. A call filling an
ARRAY element runs after the whole literal (its litFixup), so bindLitValues binds
it in its turn wherever a value after it reads anything, even as the last value
that does something. Probing the row found the literal itself refused as a value,
`take(R{1, la})`, `R{1, la}.s[1]`, "bind the literal to a variable first": the
expression path renders a struct literal with an owner for its copies once, and
where it collected some it is a temporary declared ahead with the copies after it
(hoistStructLit for the paths that hoisted a literal already). And the OWNER
leaked: a declaration captured the copies of any literal anywhere in its
initializer, `x := take(R{5, la})` writing `memcpy(x.s, ...)` for an int x, and so
did a literal's element rendering a call -- emitVarDeclInit captures only for the
literal itself or its address, as pkgInitAssign did, and notLitOwner clears the
owner for an element that is no literal. **An owner of deferred work is the
storage the literal fills, not whatever is being rendered when it is found.**
A DSP program the same day (dom32: a Q15 radix-2 FFT of 64 points, scaled at every
stage, windowed, its peaks found, a round trip through the inverse on another cog,
a Goertzel detector beside it; three packages) matched Go on the host and the board
once its Peaks took the caller's buffer -- a slice of a local returned is refused by
design. Its 1,500 mutants had two rows taken: a header's step, `for ...; i <<= true`,
`if x >>= f; ...`, asked nothing of a shift's count or a division's constant zero
divisor, the statement form's checks living in the statement path alone
(checkShiftAssignCount, asked by checkPostOp too); and an element store into a
numeric constant, `K[0] = 1`, asked nothing (checkNonValueTargetHead read a
selector only). Its 237 accepted mutants matched Go but for the panic's wording,
on the host and on the board. A CAN codec the same day (dom33: CRC-15, bit
stuffing, standard and extended identifiers, a flipped bit detected, arbitration,
the decoder on another cog) matched Go on the host and the board once its errors
were package sentinels -- `d.err = &Error{...}` keeps a literal's address past its
frame, refused by design -- and its 1,500 mutants had nothing taken. An audit of
specs.go and the README against the compiler the same evening found five stale
notes (ranging a channel and close are in the language; an array beside another
result, a select's send to a call's channel and `bytes`/`unsafe` work), and one
listed limitation worth closing: a call returning an array in an append, a send, a
select's send and a store through a pointer was refused -- arrayCallTemp writes it
into a temporary ahead of the statement, arraySourceC answering for a call through
it. **A limitation listed for users is checked against the compiler like any other
note**: three of the README's had been closed weeks before.
A second FFT (dom34: four packages, the transform a method of a pointer to a frame
array, the frames sent by value to a worker cog and the results back as a struct
holding an array of peaks) matched Go on the host and the board. Its 1,500 mutants
had one taken, `q15.Half(a, b)` of a constant, and its row was whole: another
package's variable or constant CALLED, `lib.V(1)`, was asked nothing in any
position, a function variable's arguments neither, where one package refused both
-- checkQualifiedRef handled a function, a type and a member of a variable, and
not the variable itself (varHoldsNoFunc is the local rule's, an interface written
out added to it). Its 1,200 valid-keeping mutants found the other: a constant
divided by a constant zero, `fft.N/0`, and with it `_ = 64 / 0` in one package, was
taken outside a constant declaration and an array length -- the binary rule had
left a constant dividend to the folder, whose reports stand only there
(constOverConstZero). Their 1,189 mutants both compilers accept matched Go but for
races the mutations made. Two of its refused mutants were a loud gap, `%d` of a
struct, which every verb but %v was: the spec printers of %v under a width
(needStructSpecPrint) keep a verb other than %v per field now, '+' a sign there,
a byte row as one text, String() asked only under fmt's verbs for it -- and a field
of a defined type had been handed to the scalar printer by that type, so `%5v` of
one was "%v wants an integer, not R" (emitValueSpec passes the type underneath). A
sweep of ten struct shapes under fourteen verbs and seven specs, 980 programs
against Go, had 407 match and none differ; the refusals are a field the verb does
not suit (fmt's `%!d(...)`, refused by design), '#' (Go-syntax %#v among them), a
float under %x or %b, and an exported String() field under the text verbs.
The board sweep of fuzzer seeds 5501-6000 the same day, with 6586c9d -- 456 passing
(five on a second load, the loader's), 43 outgrowing a cog -- found a SILENT backend
fault in seed 5849 ("negative shift amount" on the board, OK on the host): a
cast to a 64-bit type of a 32-bit UNSIGNED expression with a zero constant on its
left, `(int64_t)(0u + u)` and any operator, writes no high word -- the shift count
of `x << ((28 &^ 28) + (55 >> g))` was folded to `0u + ...` and widened for the
helper -- and `int64(0 - v)` of a uint32 printed garbage, in every release
(doc/widen-zero-plus-unsigned.c). Bounded on the board over 180 shapes first: a
zero on the right, an int, a narrower unsigned, a variable, a call and every
implicit conversion are right, and so is a cast to uint32_t first, which the emitter
writes now wherever it widens such an expression (widenU32: the conversion, the
int-to-float helper's operand, a shift's count). 422 corpus programs gained the
inner cast and all build to byte-identical binaries, so the price is nothing; none
of them had been hit. Recorded among the faults pending upstream. A conversion sweep
beside it, 11 integer types to 12 types over 17 expression shapes (396 lines),
matched Go on the board with the fix and had 27 lines wrong without it, all from
uint32, uint or uintptr to int64, uint64 or float32.
No generated program had WIDENED an expression, so the fuzzer found the row only
through a shift count's fold: genWideFold folds the high word of a narrower block's
fold expression converted to int64 or uint64, `int(uint64(0 + e) >> 32)`, a zero on
its left three times in four, drawing from its own source (wideRand) so the rest of
each seed's program is what it was. Its first board run, with the pre-fix compiler
to see it catch the zero-left fault, failed 4 of 20 seeds -- and failed the same 4
with the fix: a SECOND silent fault, `(uint64_t)x >> 32` of any value of 32 bits or
fewer, whose result the backend reads from a register nothing writes, `mov result1,
_var02` in the listing (doc/uint64-widened-shift-32.c). Every other count goes
through __system___int64_shr and is right, an int64's through __system___int64_sar,
and so is the high word of a computed value, `((uint64_t)a * (uint64_t)b) >> 32`;
`uint64(x) >> 32` of a uint32 printed 4052137992 for Go's 0. The emitter writes the
value without the shift (wideShift32C): zero, x evaluated, or the sign word of a
signed x. **A run that reads a register nothing wrote can look right by luck: the
listing is the measure, and a C harness passing a constant after a 64-bit argument
is a second fault of its own** -- the first reductions here judged by `chku(..., e,
0ULL)` and were misled. Seeds 1-2000 on the host shim with the new generator: clean;
on a P2-EDGE with c73d569, seeds 1-200 192 passing and 8 outgrowing a cog, and
6001-6500 450 passing and 50 outgrowing, none failing (six loads that timed out or
could not write the port passed on a second). The next day a CROSS-conversion fold
beside it (genCrossFold, convRand): the fold expression converted to another sized
kind, half the time with an operation in it, the high word too at 64 bits -- the
fuzzer had converted to int and to the 64-bit kinds only. Seeds 1-2000 on the host
shim: clean; 1-200 on a P2-EDGE with ee4168c, 192 passing (three on a second load)
and 8 outgrowing a cog, none failing; 6501-7000 the same way, 461 passing (seven on
a second load), 38 outgrowing and ONE failing, seed 6978, "Call Count Failure" on
the board alone and passing `--no-inline`: a SILENT backend fault. The CORDIC
reordering pass (OptimizeCORDIC, -O1) moves a block down past a QMUL taking a
CONDITIONAL move for a definition of its register, so a max's `if_be mov result1,
#69 ; mov t, result1` went below code that overwrote result1, and a value computed
before a multiplication read as one computed after it -- here the guard of an
inlined call, which then ran once more (doc/cordic-reorder-conditional-move.c, d
119 for 142; it needs inlined code on both sides, which the emitter's marks give).
A two-line fix in FindBlockForReorderingDownward -- only an unconditional write
meets a dependency, a conditional one adds its dst as one -- printed 142 on the
board natively and is carried since the same day (the regeneration of 2026-10-08,
under Code generation). Seeds 7001-7500 on a P2-EDGE with 15fa698 (2026-10-11, the
generator unchanged): 454 passing (eight on a second load), 46 outgrowing a cog,
none failing, warned about or refused by the backend. Seeds 7501-8000 with be2f790
the same way: 446 passing (four on a second load), 54 outgrowing, none failing. Seeds
8001-8500 with 67448ad: 457 passing (four on a second load), 43 outgrowing, none
failing.
A parenthesised head, `(uint64(x)) >> 32`, was the rewrite's first miss
(unparenExpr). And the family is bounded on the board: a value widened and then
compared, masked, added, multiplied, or shifted by any other count is right, and so
are `uint64(hi)<<32 | uint64(lo)` and a matrix of 64-bit expressions narrowed to
every type (192 lines against Go).
A Reed-Solomon codec the same day (dom35: GF(2^8) by log/exp tables, polynomials as
a struct of an array and a length passed by value, Berlekamp-Massey, Chien, Forney,
the decoder on another cog) matched Go on the host and the board once written as the
target wants -- no closure over a local, no runtime concatenation, `%s` of a byte
slice for `string(b)` -- and two of those refusals named no position, `emit: string
concatenation ... needs allocation` (five e.fail sites, failAt now). Its 1,500
mutants had 14 taken, two rows: one call's several results into a FIELD, an ELEMENT
or a POINTEE, `v.pos, v.n, v.err = rs.Correct(...)`, were asked nothing, a bare name
being the only target checkResultsAssign asked (walkedTargets, checkResultInto: Kind
into Kind with defined types told apart, Kind into category and back, an interface
into what is none), and a bare variable of a struct type took a number; and
`poly.Rem(x).Reverse()` with an argument dropped was taken, checkQualifiedRef
returning at the member it reached before asking the call. Its 1,200 valid-keeping
mutants found a TYPE no storage can hold: `[2147483647]uint8` beside an int, which
Go's own compiler cannot lay out, built here without a word, and so did `[1 <<
30]uint32` -- the backend sizes a type in 32-bit arithmetic, 4 GB is 0 bytes and a
frame past 2 GB is negative, `add ptra, ##-1073741808`, which the stack reader's
pattern skipped. A type of more than 1 GB is refused where it is written
(checkTypeSize, typeMinBytes: a lower bound, 0 for what is not resolved yet), and a
negative frame reads as larger than Hub RAM. **A probe that makes the backend lay
out a gigabyte of data costs the machine**: one such build wrote a 1 GB image, and
the user asked about the load the same hour: the dev machine also hosts the builder
VMs, so a batch runs at `xargs -P 4` at most, and not beside the full suite.
A fixed-point library (2026-10-08, dom36: Q16.16 with a software CORDIC for sin,
cos and atan2, a bit-by-bit sqrt over uint64, log2 and exp2, and a resolver's
angle-tracking loop on a second cog behind channels of structs; three packages)
matched Go on the host and the board, checked and unchecked. Its 1,500 mutants
had one false refusal and one row taken. `One / 1e3` for a typed integer One was
"constant 65.536 truncated": Go converts the untyped operand to the typed one's
type before the operation, and foldBinary asked only whether it overflowed; the
emitter's exact fold (foldValSeq, constNodeType) had the same rule missing, so an
array length `[One / 1e4]int` was "unsupported type" -- and `const k = One * 2.5`,
whole at the end, was taken. And a method of a defined type over a Kind called on
an INTERFACE method's result, `f.Eval(2).Put(b)`, was "type int32 has no method
Put" (methodSingleResultName asked the variable's type's own methods, which an
interface's are not; callResults knows them); across packages its arguments were
asked as an operand only, callChainWalk stopping at a method of another package's
type -- a statement, a defer and a typed declaration took a `[]bool` for a
`[]byte` (the walk goes on from the variable now, the first call left to
checkImportedMethodArgs). Its valid mutants found the printf ORDER: a printf of
ONE argument wrote the format's text before evaluating it, emitPrintf having
copied println's "a single argument has nothing written before it" --
`printf("got %d\n", <-ch)` wrote "got " and waited mid-line, the hang-shaped
interleaving the several-argument hoist exists for. A Czech text program beside it
(dom37: UTF-8 runes, word wrap and justification by rune count, a dictionary
collation with "ch" a letter of its own, two packages) matched Go on the host and
the board, and its first draft found a hole by being idiomatic Go:
`coll.Sort(coll.Strs(ws))`, sort.Sort's shape, reached C -- a value MADE by a
conversion, a call or a literal into an interface was refused only for a type of
this package over a Kind (madeDefinedValue: any defined type that is no pointer
and no interface, any chain ending in a call, another package's literal).
`ss := coll.Strs(ws); coll.Sort(&ss)` is the spelling here. Its 2,700 mutants had
nothing else taken. Nets for the false refusals: 6,719 probe programs of the
earlier rounds, HEAD's verdict against the tree's, none moved; 5,366 mutants both
compilers accepted, all still accepted; the corpus changed in four printf programs
only. **A domain program written as Go is written finds what one written for this
compiler does not**: the conversion into an interface was the commonest sort idiom
there is, and every domain program before had passed `&x`.
An outside review of the language design (2026-10-08, kept out of git) led to two
changes the user asked for. The Builder's writes return Go's strings.Builder
results, (int, error) and error, a write that does not fit answering "short write"
(builderHelpersC; the C types of `error` and the pair are minted in needBuilder,
so a program with no Builder carries neither; the checker's answer is
builderResults, read by callResults and zeroResultCall). And a select of two
clauses or more starts its tests after the clause it chose last (a try phase over
a per-site counter, ogo_selrotN, then the arm of the clause chosen, so a break in
a body still leaves the select): measured on a P2-EDGE, two busy channels gave
1000 of 1000 values to the first clause in source order, 500 of each since -- the
host's threads had shared them fairly all along, so only the board showed it.
Its implementation claims were checked first: three were stale notes, fixed
(c41a47b). **A review's claim about the compiler is probed before it is answered:
a note it read may be months behind the code.** Its smaller items came next. A
channel of receive-only channels is written as Go writes it, `chan (<-chan T)`,
`chan<- <-chan T` and `<-chan (<-chan T)`: an arrow after "chan" is always the
channel's own, so the element after it may begin with one (ArrowElemType), and an
element may be parenthesised after any channel's keyword or arrow (ChanElemType's
"(" Type ")"), egg's ambiguities still 10. The parentheses are the parser's alone:
`it` hands a semantic reader the Type inside them, and the formatter and the token
spans read `itRaw`. Factor's "chan" took Type, so `(chan<- int)(c)` was a syntax
error and is the emitter's loud "not supported yet" now, as `(chan int)(c)` was.
Writing the forms found an older row: a channel RECEIVED from a channel of channels
had no type node, so `c := <-pipe; c <- 1` for a receive-only element, `close(c)` and
`var d chan int = <-pipe` were taken, a named element too (exprChanTypeNode answers
a receive now; 34 programs agree with Go). A goroutine that recurses is refused
without --gostack (compileSized, goRecurse): its slot was the default 256 longs and
the fence spoke only at its end. The lock rule was measured before it was written
into specs.go: a record of three words under p2.TryLock and p2.Unlock, written by
one cog and read 2000 times by another, torn 0 times on the board and 53 times
without the lock, and the listing keeps every write between locktry and lockrel
(TestBuildLockedRecord). `%T` of a directional channel printed it both ways,
`chan int` for a `chan<- int`, the C type having no direction: the checker records
each level's for a format's argument (noteChanTDirs, chanTDirs) and the emitter
spells it (chanTypeNameForT). And a select's send clause on a closed channel
panicked on entering the select whatever else was ready, its offer asking on the
way in, where Go's is a ready clause, chosen among the others -- 504 of 1000 Go runs
panicked beside a ready receive: the standing offer's clause asks in its turn now
(ogo_chan_sendclosed), as the gated one's trysend already did.

**AN INTERFACE WRITTEN OUT IS A TYPE** (2026-10-11). `case interface{ Unwrap()
error }:`, Go's errors.Is spelling, was a syntax error: a case is an Expression and
Factor had no InterfaceType. Factor and HeaderFactor take `InterfaceType [
CallSuffix [ FactorSuffix ] ]` now (egg's ambiguities still 10); the checker reads
the case (caseIfaceLit, bound at the type written by typedVar) and the emitter
mints its anonymous type (caseIfaceC, anonInterfaceType), so the case was an
afternoon. The row behind it was not: every interface rule asks by NAME --
interfaceMethodsNamed, implements, checkImplements -- and a type written out has
none, so a value going into one was asked nothing in any position, a value OF one
was asked nothing either, nor were its methods' calls or a type switch's cases on
one. `var v interface{ Code() uint32 } = &e` for a `Code() int` built for the target
in silence, as did `interface{ Code() }`; a missing method and a value where a
pointer is held were the emitter's, in C's names. A written-out interface has a
name now, Go's spelling, `interface{Code() int; Error() string}` (ifaceLitName),
recorded with its type and scope where it is spelled (File.ifaceLits) and resolved
by interfaceMethodsNamed, so each rule asking by name asks it -- handed over at the
store sites only (ifaceTypeName, varIfaceLit: a declaration, an assignment, an
argument, a return, a field, an element, an append, a send, a comparison) and NOT by
typeNodeString, which every comparison of two types asks, so no other rule's verdict
moved. Method calls on one (checkMethodCall, callResults, the walk's method step,
checkCallResultMethod), a comma-ok variable of one (assertedLit) and a type switch
on one (typeSwitchIface, the default clause's binding). Three grids against Go, 238
programs, agree: 132 values into one (37 taken before), 66 type switches on one (12
taken and 12 valid refused), 40 method calls (12 taken). The conversion
`interface{ M() }(x)` parses with the case and is refused by the checker as not
supported yet (reportIfaceLitValue): every reader of a conversion finds one by its
callee's NAME (directCall, convChainHead, the lifetime readers), which this has
none of; it crashed the constant folder until the folder took InterfaceType.
**A rule asked by name is a row of every type that has none.**

**A CALL'S RESULTS FORWARDED WHOLE ARE A ROW** (2026-10-11, found by a domain
program -- a record store of CRC-checked pages with typed errors, a command parser
and the two on cogs behind buffered channels, classified by errors.Is, errors.As and
written-out interface cases, three packages, matching Go on the host and the board).
Its 1,500 mutants had two taken: a type switch's operand named bare was noted by no
read (checkTypeSwitchOperand asks noteRef now), so a clause declaring that name had
its unused variable counted used by NAME, the fallback for a token nothing resolved;
and a call's result of a Kind into an interface target of a destructuring
assignment was asked nothing in any target shape (checkResultIntoIface, from
checkResultInto and the bare-variable branch of checkResultsAssign). Its 985 valid
mutants matched Go but for prints of two cogs interleaving. Running the valid twin
of the second row found the emitter storing a pointer result into an interface's two
words, `e = _ogo_t0._0`, for every target, `take(mk())` and `return mk()` alike --
loud, and valid Go: widenValC builds the value, a concrete pointer beside its table
or another interface rebound, for emitStore (storeTargetCType), widenForwarded and
emitWidenedForward. And probing what the widening newly let compile showed a
SILENT hole older than all of it: the forwarded-argument path in emitCallArgs
returned before checkCrossArgs and checkIntoArgs, so `keepP(id2(&l))` kept the
local's address in a package variable with no word -- checkForwardedIn asks each
result that can carry a reference against the parameter it fills (which carries what
the call hands back is unknown, so each may). The summaries mapped the forwarded
call to parameter 0 (fwdArgs spreads it over the positions, owned by nothing
nameable). And beside it a model gap: a parameter held as a PART of an argument --
a literal's element, `keepC([]*E{p})`, a value packed into a variadic parameter,
`keepV(p)` -- went to the callee's parameter as the parameter itself, so a callee
keeping its parameter's CONTENTS kept nothing of the caller's; crossEdge.part maps
those contents onto the caller's parameter (partEdges; a spread, `keepV(xs...)`,
is told by the token after the last argument). Both in v0.54.0. **A shape that
newly compiles is asked every lifetime question**: the C compiler's refusal had
been hiding that none was asked. `defer f(g())` and `go f(g())` of a g of several
results came the same day: captureForwarded binds g where the defer stands and
captures each result as the parameter it fills (deferArg.text), the replay passing
them together (emitCallArgs), a literal's path too; the go statement gives it a slot
per result and stores them widened. checkCrossArgs routes a forwarded list to
checkForwardedIn, which checkDeferLeaks reaches. A deferred PRINT of one first
printed the first capture alone (the host's -Werror caught it as an unused one);
print and println of one came the next day, plain and deferred
(emitPrintForwarded: the call bound and its fields handed to the printers as the
hoisted arguments, hoistedArg, which every printer asks before the expression; the
replay printing the captures), printf of one refused -- its format would be the
call's first result. And a call of several results beside other arguments is the checker's
refusal now (checkCallArgs asks multiValueOperandErr), the emitter's own in a
literal's element, a send or an index carrying a position.
A maze program the next round (dom41: Kruskal's algorithm over a union-find of
int16 parents, walls as Dir bits of an iota group, A* over a binary heap of structs
in a fixed array with sentinel errors, BFS on another cog; three packages) matched
Go on the host and the board at the first try. Its 1,500 mutants had two taken,
each a row: a NAMED untyped rune constant was an integer to the checker -- constVal
carried no class, a constant.Int being UntypedInt -- so `const H = 'c'; x := H`,
`x := W * H` and `x := H << 2` were ints and took one (constVal.isRune: a rune
literal sets it and sameTypeAs carries it, Go's integer-beside-rune and the shift's
left operand); and an address as a header's assignment target, `for ...; &x = i`
and the for, if and switch inits (checkForPost, addrTargetErr). The C had typed the
constants right all along -- `%T` printed int32 -- so only the checker was wrong.
Its 1,065 valid mutants matched Go but for panics alike and races the mutations
made. Left: `*&x = v` is a syntax error, the target's grammar taking no `&` after a
star, where Go takes it.
A synthesizer the round after (dom42: phase-accumulator oscillators behind an
interface over an embedded base -- a quarter-wave sine table, a square, LFSR noise
-- ADSR envelopes as a state machine in Q15, saturating int16 mixing, the score
rendered on another cog into a buffered channel of 64-sample blocks; three
packages) matched Go on the host and the board once its own race was gone (main
read the envelopes' states while the cog wrote them; the block carries a snapshot
now). Its 1,500 mutants had two rows taken: a range over a bool CONSTANT
(checkRangeable asked a variable's Kind only), and a variable ranged out of a
channel typed by its element's name alone -- `b.n` read, `range b.samples` declaring
nothing (inferredType from the channel's written element now, for a channel named
here). Its 1,039 valid mutants matched Go but for prints of two cogs interleaving.
The valid mutants of dom40 and dom41, 2,042 that built, were run on the board the
same day (bmut in the scratchpad: prep.sh, brun-bin): 1,908 matched Go, the rest
differed on the host as well -- races and panics -- and the 99 the loader lost
matched on a second or third load, none differing on the board alone. What the run
did find was the panic's order (A program ends as Go's does, above): a doubled
`close(cmds)` printed its panic and ran on, on the board, and exited 0 on the host.
A second board run the same evening, the 2,031 run-case token mutants of the
earlier batches and dom42's 1,039 valid ones with the compiler of 59ceae6: 347
were the old channel rule's, 3 too large for Hub RAM, and of the 2,720 that built
2,648 matched Go (43 on a second or third load). The rest were dom42's cog races,
differing on the host too, and the known kinds -- a print whose later argument
panics writing the earlier ones first, loops of 2^31 passes past the runner's 15 s
-- but for one, a 512 KB array result beside a recursive function that built and
printed nothing: main's stack was asked nothing where a recursion left it unbounded
(b6f9e9d, A GOROUTINE'S STACK IS READ OFF THE LISTING).
A lock-in amplifier the round after (dom43: an NCO over a sine table built by
integer rotation, a CIC decimator in wrapping uint32 on an "ADC" cog, quadrature
demodulation, CORDIC atan2 and a bit-by-bit isqrt, sweeping a simulated resonator;
four packages) matched Go on the host and the board, checked and unchecked, at the
first build. Its 1,500 mutants had two disagreements, both by design (println of an
array, a printf argument count); its 184 and 1,069 accepted mutants matched Go but
for cogs' prints interleaving, races a `break` made and loops the host could not
finish. **A round that finds nothing ranks the next task**: the checker held for
this kind of program, so the next was a listed limitation.

**AN ALIAS OF A TYPE WRITTEN OUT IS THAT TYPE** (2026-10-11). `type Buf = []byte`,
`type Pair = struct{ X, Y int }`, `type Fn = func(int) int`, an array, a pointer, a
channel -- refused until then, "cannot alias a type literal yet", the README's
listed limitation. Lifting the refusal alone showed the split: the checker's SHAPE
rules already read the literal through the alias's declaration, as they read a
defined type's -- all 21 programs of a rejects batch were refused as Go refuses
them -- and only its IDENTITY rules were wrong, taking the alias for a defined type:
`var d D = b` for a `type D []byte` refused. definedName answers "no defined type"
for one (aliasesLiteral, namesAliasedLiteral; checkDefinedType and append's name
comparison ask it), and one reaching itself, `type T = []T`, is Go's "invalid
recursive type" (aliasReaches; a defined type between ends the walk). The emitter
gives the name the literal's C type: aliasOf for all but an array, which has none
and goes in namedArrays under no name, aliasLits marking every one, so
namedSliceType, convType, qualConvType, cType's qualified branch, a make of a
channel and a type switch case (aliasLitCType) answer the literal; a forward of a
struct tag is not written for one. Two grids measured it, 290 programs moving a
value between an alias, a second alias, the literal and a type defined over it in
ten positions, and 165 naming one in each way a type is named, in a package,
locally and across a boundary; a third, 18 programs of interface dispatch. The one
silent fault was in the making: `case *Row:` for a `type Row = [2]int` named the
alias's table where `&gr` was stored under [2]int's and never matched -- **a type
that names no defined type is stored under the literal's table, and every place
naming a table asks the same name**. Rows found older than it, each closed: the
address of an element of a slice PARAMETER of structs, `func find(items []Item, id
int) *Item { ... return &items[i] }`, refused as the parameter's own slot --
escapesFrame took a slice for a pointer only where its element had a Kind, and the
emitter's address rules read a defined slice type as no slice (underlyingCType); a
lifetime matrix of six slice spellings by eleven shapes holds both directions
(TestEmitCSliceElemAddr). %T of a struct written out printed its C name,
`ogo_anon0`, in silence (typeNameForT). A value of a defined pointer type received
from a channel was no pointer, and one appended to a slice of such a type was "of
type int" (the pointee's Kind, WHAT HAS NO KIND IS ASKED NOTHING). `make(lib.C, n)`
of another package's channel type by name, defined or alias, was "dynamic
allocation". Two pointers to a function compared, `p == q` of `*func(int) int`, was
refused, isFuncCType reading the pointer's prefix. And a pointer to an anonymous
struct took no typedef dependency, so a slice of one could precede its typedef once
aliasAnonStructs moved it after a declared struct. Left, loud: a type switch case
of a pointer to a struct or a function WRITTEN OUT, `case *struct{ X int }:` and
`case *func(int) int:`, is a syntax error, where the assertion parses.

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
**A STORE A SUMMARY RECORDS IS ASKED HOW FAR IT GOES** (2026-10-21, found writing the
summary rows of the star targets, `*(h).pp = v`). The store rules inside a function
have asked what a pointer reaches since 2026-09-24 (A RULE ASKED OF A ROOT MISSES
WHAT A POINTER REACHES); the summaries of what a callee keeps had not. A store
through a parameter's name was recorded as one through that parameter whatever the
chain after it, and the caller, passing `&lu` for a local lu, took the store to land
in lu -- where `h.q.p = v` lands in what lu.q points at, a package variable as
likely as not: `put(&lu, &x)` left x's address there, and read after run returned
it printed 32766 on the host for Go's 42. Twenty-two shapes did the same: a field of
the pointer a parameter points at, `(*hh).p` for a `**T`, a local loaded out of
one, `p := *hh` and `q := h.q`, a package variable's pointer `q := gu.q`, a field of
a parameter passed by value, the receiver's pointer, value and pointer receivers
both, a slice field's element and `copy` into one, a list and a for clause, a local
holding a call's result, `**hh = T{p: v}` -- and through a call, a method storing
into its receiver called on `p := *hh`, `fill(u.q, v)`, `fill(*hh, v)`, `fill(q, v)`
for `q := u.q`, `fill(get(), v)`. The summaries now type a store's target from its
root -- a parameter, the receiver, a package variable, a local declared with a
defined type, or one every binding of which types from those -- and count the
pointers it goes through (storesPast): one, from the value of a name the caller or
this frame chose, is the call site's question as before; any other lands where no
rule can name, and the value stored is kept where it outlives every frame, as Go's
compiler moves such a local to the heap. An argument is asked the same of where the
callee's store through it lands (argPast), and a local holding a pointer is a
receiver by what it holds (localRecv). Three things the first version got wrong,
each caught by the tests: what a name's value IS was asked of what it holds, which
counts what is stored INTO it -- `*ps = (*ps)[1:]` made ps hold its own contents,
and a stack's pop through a pointer to its slice was refused (valueKnown reads the
bindings); a Swap exchanging elements through `(*s)[i]` was refused for a store two
pointers deep of values holding no reference (a place that can hold none keeps
nothing); and typing a target minted the typedef of a `[2][2]int` row into two run
cases' C (unminted). The corpus's 1,393 programs emit the C they did and none is
refused; of 4,858 domain programs and mutants none has another verdict (one is refused either
way under another first message), and p2-11 and its tests build to the same bytes. Left: a local
the summaries cannot type -- bound by a range clause, by a call of several results,
by a type assertion -- keeps the old reading (closed the next day, A BINDING IS EVERY
FORM THAT GIVES A NAME A VALUE); and `(*hh).set(v)` for a `**T`, valid
Go, is "unknown package hh", `(**hh).set(v)` "cannot call pointer method set on T".
The rules: **one pointer from a value the caller or this frame chose is the call
site's question; any other lands where no rule can name**, and **what a name's value
is, is asked of its bindings, not of what is stored into it**.
**A BINDING IS EVERY FORM THAT GIVES A NAME A VALUE** (2026-10-22, closing what the
entry above left). The summaries typed a store's target from its root's bindings, and
read a binding where a statement wrote one: a range clause's element, a for clause's
init and post, one of a call's several results, a comma-ok form, a receive, a type
switch's name, a declaration's written type, a literal, a method's and a function
value's result were no binding at all, and a root bound only by them was not typed --
and an untyped root's store was read as before, as one into the local's own storage.
`for _, h := range hs { h.p = v }` left run's local address in what an element of hs
pointed at, a package variable; so did `for h := u.q; ...`, `h, n := u.two()`,
`h := x.(*U); h.q.p = v` and its comma-ok and type switch forms, `h := <-ch`, `for h
:= range ch`, `q := u.next()`, `q := pick(u)`, `var ps [2]*T; ps[0] = u.q; ps[0].p =
v`, `w := W{q: u.q}; w.q.p = v`, `ps := [2]*T{u.q, nil}` -- 27 shapes, each silent,
each refused by its row of TestEmitCStoreBindingForms since. Each form is a binding
now (ranged, results, declared, the type switch's name typed by its clause,
clauseAt), and what it binds is asked as valueKnown asks: an element read, a result
and a value received point where nothing here can name; an assertion on a name
points where the name's interface does; a literal is this frame's. That still leaves
names no walk types -- one name declared twice with two types, `{ h := W{} } { h :=
u.q; h.p = v }`, was the probe that showed it -- so a target the walk cannot type is
taken to go through a pointer it cannot see, where it lands no rule can name: KNOWN
MEANS MUST, and "not typed" had been a permission. What the first versions got
wrong: a literal written out, `ps := [2]*int{v, nil}; ps[1] = v`, was untyped and so
refused, and a slice literal's backing, the frame's own, counted as storage nothing
names (the controls caught both); and typing an assertion's type and a declaration's
through cType MINTED what they wrote out -- the interface of `x.(interface{ Unwrap()
error })`, a struct written out -- which emission then took for minted and never
wrote: six run cases' C lost a typedef they use, which only the corpus diff showed
(the summaries read a type off its tokens since, summaryCType; the rule is the old
one, never ask cType from a pass gathering facts). And two run cases were refused,
which the dump's listing showed and its diff did not (a refused program is a file
missing). One was right: `case ***M: **d = src` in a function also asked `case **M:`
stores two pointers deep, which the entry above refuses, and Go moves both locals
handed to it to the heap; the run case's two-deep store takes a package variable
since. The other was a summary that had no level: `func (p *pool) alloc() *node {
return &p.nodes[i] }` was recorded as handing back its receiver (retRecv), so
`n := p.alloc(v); n.next = p.head; p.head = n`, now typed, stored the receiver where
it outlives every frame, and a linked list over a pool was refused for main's local
pool -- where Go says "leaking param: p to result ~r0 level=1", one dereference
past p. A return past the receiver's pointee is its contents now (deepRecvReturn,
retRecvShallow), read so where the summaries follow a result -- a method on it, a
relay, a sink -- and the call-site rules keep the receiver reading, which refuses
more; `n := p.at(1); n.ptr = v` keeping a local's address in a package pool's
backing, silent before, is refused by the same change. Measured: the corpus's 1,393
programs emit the C they did but the changed run case's, none refused; of 4,858
domain programs and mutants none has another verdict; p2-11 builds to the same
bytes. Found beside it and left for its own change (closed the same day, AN EMBEDDED
POINTER IS A POINTER THE CHAIN DOES NOT SPELL): a field promoted through an embedded pointer, `type P struct{ *T }` and
`h.p = v`, is a store through that pointer which neither the summaries nor the
store rules inside a function count -- `pp := P{T: &g}; pp.p = &local` keeps the
address in g -- and a method promoted through any embedded field, `h.set(v)` for a
`type E struct{ T }`, is no call to the summaries at all (methodCallOf names the
method on E, which declares none), so `put(&ge, &local)` kept the address in ge. The
rules: **a binding is every form that gives a name a value -- a
statement, a clause, a header, a guard -- and a rule reading a name's bindings reads
every form**, **a target no walk types is asked as one through a pointer**, and
**what a call hands back of its receiver is asked how far past it the result is**.
**AN EMBEDDED POINTER IS A POINTER THE CHAIN DOES NOT SPELL** (2026-10-22, found probing
the binding forms beside it). A field promoted through an embedded pointer, `pp.p`
for a `type P struct{ *T }`, is `pp.T.p`: written, read and addressed through what
pp.T holds. Every rule asking whether a chain's step goes through a pointer asked
the type the step applies to, P, and P is no pointer -- the store rule inside a
function (targetThroughRef), the address test (addrCrossing) and the summaries'
walk -- so `pp := P{T: &g}; pp.p = &x` kept a local's address in g, and so did `func
put(h *P, v *int) { h.p = v }`, a value parameter's and a value receiver's, `w :=
h; w.p = v`, `fillp(&h.p, v)` and `fillp(&pp.p, &x)` in silence, in every release.
The method half was wider: a method promoted through ANY embedded field, `h.set(v)`
for a `type E struct{ T }`, was named on E, which declares none, so the summaries
took it for no call at all and `put(&ge, &x)` kept x in the package variable ge
(methodCallOf resolves it through its path since, promotedMethod, each embedded
pointer on the way storage it cannot name). Thirteen shapes, each refused since by
its row of TestEmitCEmbeddedPointerStores; a member embedded by value is the storage
of what it is part of, as before. What the change newly refuses, its explicit
spelling was refused already: `pp := P{T: &t}; pp.p = &x` for a local t is
`u.q.p = &x`, which the store rule trusts no more than any pointer loaded out of
storage, and Go moves x to the heap for it. The CHECKER had the other half, a false
refusal: its address test (addrThroughRef) read `&pp.n` as the address of local pp,
refusing `gn = &pp.n` and `return &pp.n` for a pp.T pointing into a package
variable, which Go takes; it asks fieldLookup's embedded-pointer answer since, and
the emitter decides by what pp holds, refusing it for a pp.T into a local. The rule: **a promoted field or method is asked as the
chain its promotion spells: every pointer step a rule asks of, it asks of the
members the promotion goes through**.
**A STAR IS GO'S SHORTHAND OVER ONE POINTER** (2026-10-22, the open item the entry
above left). Go reads `(*p).m()` as `p.m()` because a selector dereferences one
pointer; over a pointer to a POINTER, `(*hh).m()` for a `**T`, the dereference is
the point of the call: the method is called on the pointer hh points at. Two readers
dropped the star regardless. The emitter's (derefShorthand) wrote `hh.m()` and named
a package hh, "unknown package", read `(**hh)` as a value with no address, "cannot
call pointer method", and refused the `defer` and `go` forms -- loud refusals of
valid Go. Behind them, the summaries' readers (scanHead, parenFactorShape) hand a
head's name on without its stars and named no method of a `**T` at all. Making the
emitter right (bindDeref, as for a pointer to a slice or an interface) made seven
shapes compile that the summaries said nothing of: a callee calling `(*hh).keep()`,
`(**hh).keep()`, deferring or starting it, or storing through `(*hh).put(v)`, each
kept the caller's local in a package variable; and the temporary bindDeref binds,
carrying no mark, let `(*hh).keep()` keep it in the function itself. **A shape that
newly compiles is asked every lifetime question**: the refusal had stood in front of
all seven. The summaries read a head's stars since (methodCallThrough): over a
pointer to a pointer the receiver is a pointer loaded out of storage, a store into
it lands where no rule can name, and what the method keeps of it the caller keeps of
hh's contents (recvLoaded); the temporary carries what the pointee is marked to hold
(pointeeMarks, which frameRefOf's `*p` asks too). Sweeping the row found three older
holes, silent in every release. `gp = (*h).self()`, for a method handing back its
receiver, kept a local's address where `gp = h.self()` was refused: frameRefOf read
no dereference head, and neither did summarisedCall, which asks it the same question
for a declaration (one helper for the two since, derefCall). `(*q).Keep()` through a
pointer to an interface holding a local's address let the method keep it, in the
function and in a callee handed q (ifaceMethodCallsThrough). And `p := *hh;
p.keep()` in a callee: localRecv takes such a local for storage that outlives, a
store INTO which is a leak -- and keeping storage that outlives is none, so the
callee kept nothing of the caller's. KNOWN MEANS MUST once more: "outlives" was the
store rule's assumption, read by the keeping rule as a permission; what the method
keeps of such a receiver, the caller keeps of what the local holds (recvName). A
diagnostic names a bound dereference as written, `*hh`, never `_ogo_t0`
(tmpShown). TestEmitCDerefPointerToPointerCalls holds 26 rows, of which HEAD failed
24. Its run case found the checker's half the same day: `p := *hh` for an hh
declared `**Node` was typed a Node -- a declaration records THAT its type is a
pointer, not to how many, and exprNamedType took a dereference for a value -- so
the cursor walk `for p := *hh; p != nil; p = p.next` was refused, a pointer to a
pointer being the first of the ways the checker misreads a category (FIFTEEN RULES
ACROSS THE CATEGORIES); a dereference is asked what the walk types it as since
(derefIsPointer). And a method VALUE through one, `f := (*hh).get`, was the
emitter's "cannot infer a type": the checker asks the method-value rule of a
dereference written out since (reportDerefMethodValue), at a declaration and
wherever a value stands, `take((*gp).get)` -- Go saves what the dereference reads
when the value is taken. The rule: **a reader that drops a dereference asks whether Go's shorthand
drops it: one star over a pointer to a struct or an array is the shorthand, and
any other is what the call is made on**.
The address side of the same flag, a day later (2026-10-23, dom59, a timer wheel
and an LRU cache over static node pools, refused on its first build): `pp :=
&w.head` and `pp := &slots[i]`, the cursor of the list idiom `for pp := &l.head;
*pp != nil; pp = &(*pp).next`, were recorded as one pointer to a Node -- the
address and the field's own pointer folded into the flag -- and `*pp != nil` was
"nil is no value of struct". The first fix left such a variable unnamed, as `pp :=
&hd` for a pointer variable had been left since the flag was found wanting, and the
mistakes asked of the probe then showed what that costs: `(*pp).m(1)` of a method
nobody declared and `(*pp).get("a")` went to the C compiler, for `pp := &hd` in
every release. A declared `var pp **Node` was right everywhere, its type written
whole beside the flags and read first; the inferred one is recorded the same way
since, the walk's type taken whole where this package wrote it. **A type the
flags cannot say is recorded whole, as a declaration would write it: an unnamed
variable is one every rule asking a name skips.** Across the package boundary the
row was still open, `pp := &lib.L.Head`, the first version leaving another
package's type on the old path because its unnamed fallback read this file's
value through lib's tokens; it is written as this file names it, `**lib.Node`,
since (acrossPointerType), and 24 rows of three shapes agree with Go.
**A VIEW IS AN ADDRESS** (2026-10-22, found by dom58, a driver framework of
embedded Bases, Buses and Rings, in the first lifetime variant of its run). `gs =
view(&u)` for a `func view(u *UART) []byte { return u.buf[:] }` kept a slice of a
local's array in a package variable, in every release, while `gp = first(&r)` for
`return &r.buf[0]` was refused: the summaries read an address rooted at a parameter
as the parameter's value, and a slice step ending a chain as its CONTENTS -- right
for a slice field, `r.xs[1:]`, whose backing is what the field holds, and wrong for
an array field, which is in the storage the chain reaches. Handed back, stored by a
callee, sent to a cog, through a method, a promoted method, `(*r).buf[:]`, an
`append` into `r.buf[:0]` or a relay, each kept nothing of the caller's. The
summaries ask the pass's walk where a sliced array is since (arrayView over
walkTarget): in the root's own storage or what it points at, the root's value; past
another pointer, its contents; untyped, both. The rule inside a function had the
mirror fault, a false refusal: `gs = ue.buf[:]` for a `UE{ *R }`, `v.p.buf[:]` and a
value parameter's `u.buf[:]` read the array as the root's own storage where it is in
what a pointer on the way holds, the question addrCrossing answered for `&h.p.n` and
sliceBackingIsFrame never asked (one helper for the two since, chainCrosses). Go
takes all three, and where the pointer holds a local's address the refusal names
that local. TestEmitCArrayViews holds 21 rows, of which HEAD failed 18, 11 in
silence. The rule: **a slice of an array is the address of its first element:
every rule asking where an address points asks it of the view**.
The list forms the same evening, closing the open item of a starred chain: `*t.p,
y = 1, 2` was refused -- the single-target path claimed every star over a chain
before the list was recognised, and the list's stores refused one -- and, writing
its probes, `*pu, k = 1<<40, 2` for a `*uint64` turned out to store 0, on a P2-EDGE
as on the host, in every release: assignTargetCType gave a starred target no type
("neither has a pointee here"), so its constant was bound at int's width. A
starred target is typed by what its stars point at since (storeTargetCType), and a
star over a chain is stored through the pointer the chain reaches, fixed with the
other places before the first store (fixTargetAddrs). The sweep's last row was a
store through stars over parentheses with no step after them, `*(t.p) = v` and
`*(p) = v`, refused in both forms. **A type a rule does not know is asked which
rules read it as a default**: an unknown target type was a refusal for a nil and
a default width for a constant, and the default was the miscompile.
That last row shipped with a hole (2026-10-23, found making the same targets work
in a list). `*(t.pp) = v` compiled from that evening, and the store rules inside a
function asked it; the CALLEE's summary did not: assignThrough and summaryBinding
read the head under the stars as a name or as nothing, and `(t.pp)` is neither, so
`func put(t *T, v *int) { *(t.pp) = v }` stored nothing, and `put(&gt, &x)` kept a
local's address in a package variable -- in v0.58.0, released the morning after.
`*(t).pp = v` had been read, the step standing outside the parentheses. The scans
had read the inner chain by shape all along (scanHead, parenInnerShape); the two
summary readers do since (parenChainShape). The sweep that found it asked each
store three ways, alone, as a list's head and after another target, and a callee's
too: of 25 rows HEAD took 3 in silence and refused the 46 list forms, which agree
with the single form since. **A shape made to compile is asked of a callee's
summary as well as of the function it stands in: the summary reads the statement
by its own readers, and a head none of them knows stores nothing.** The same day
the parenthesised chain itself compiled, `(t.p) = v` and `(t.np).q = 7`, and the
same sweep caught its first version: a list's item, `k, (t.p) = 1, v`, was read
through lhsItemTarget, which asked the variable's type, and a callee's summary
reads its body before its locals have one -- the item was nobody's, and
`put(&gt, &x)` was taken. The summaries read it by shape since (parenChainShape),
as the rule written down for the fact-gathering passes says: never ask a type there.
**WHAT GOES NOWHERE MAY STAY BEHIND** (2026-10-23, found by dom59's lifetime
variants, written by hand, the mutators having gone with the scratchpads). `func
get() *Timer { var w Wheel; w.Init(); return w.alloc() }` built and handed out a
pointer into the dead frame's pool: Init threads the free list through the wheel's
own array, `w.free = &w.pool[i]`, and the summaries took a receiver stored into
its own storage for nothing -- "into its own receiver, the receiver goes nowhere",
the edge propagation said, which is true of outliving and says nothing of what the
storage now holds. Written in the function, `w.free = &w.pool[0]` marked w; through
a function, `initW(&w)`, the parameter stored through itself marked it too; as a
method, `w.Init()`, nothing did, and `return w.free`, `g = w.first()` and a copy
`lw := w; g = lw.free` handed the address out, in every release. The receiver has
the parameter's slot since (recvSelf): recorded where a store through the receiver
carries its value, carried to a method calling it on its own receiver, to a
function calling it on its parameter and to a method handing its receiver to one,
and read at the call as the same store's mark. Go moves w to the heap in each, and
in the four of the probes the mark newly refuses -- a local pool used through
methods storing its contents through a pointer, and `defer w.Init()` before a
return -- Go moves it too. **A store a summary
drops for keeping nothing is asked what it leaves behind: the receiver into itself
goes nowhere, and its storage holds a pointer into itself.**
**A RUN OF PREFIX OPERATORS IS ONE NODE** (2026-10-23, found asking those list
forms what a nil pointer under them does). `**pp, y = 1, 2` of a pp holding nil
crashed the host where Go panics, and so did `**pp = 1` and `y = **pp`, in every
form: the stores checked the outer pointer only (a comment said "a deeper `**pp`
guards nothing yet"), and the read checked neither. The read's cause was wider.
UnaryExpr is `{ UnaryOp } Factor`, so `**pp`, `-*p` and `!*pb` are one node of
three children, and every lowering in the unary case was written for an operator
and its operand, `len(kids) == 2`: the nil check, the narrow negation and
complement, the 64-bit negation, the unary plus on a float. A star after another
operator took no nil check -- `!*pb` of a nil pb read address zero on the target
-- and a narrow value through a pointer was C's int: on a P2-EDGE `-*p8` of an
int8 -128 printed 128, and `-*pu8` and `^*pu8` of a uint8 5 printed 4294967291 and
4294967290. The receive had met the run already, `<-*p`, and re-entered the rest as
a UnaryExpr of its own (afterFirstChild); the unary case does the same since, the
operand of the first operator the rest of the run, and each store's stars are
checked a level at a time (starsC). Twelve run cases changed C, each a read or a
store two or three stars deep; no fuzzer seed did, the generator writing no star
after another operator. **A rule written for an operator and its operand asks what
the grammar's repetition hands it: a run of prefix operators is one node, and the
operand of the first is the rest of the run.**
**A RUN HAS A TYPE AT EVERY OPERATOR** (2026-10-23, the open item the paragraph
above left: the checker's readers of a run). The emitter re-entered a run as its
first operator over the rest; the checker's three readers of a value's type did
not. valueTypeAt (operandType) knew an operator over a Factor, a run of stars and a
run of arrows, and nothing mixed, so `x := <-*pch`, `x, ok := <-*pch`, `x :=
-**pp8` and `x := *&t` declared a variable no rule asked anything of: 46 of 144
declaration-and-use pairs differed from Go -- `x = 300` into an int8 built and
printed 44 on a P2-EDGE, `x = "a"` into a struct and `x.nope()` reached C -- and
the emitter, typing no receive inside a run, refused `x := ^<-*pc8` as "cannot
infer a type". The Kind reader (exprType) and operandTypeAt had the same shapes.
And checkUnaryExpr asked the innermost operator of the factor and the next one of
nothing but a pointer, so what a dereference or a receive yields was asked nothing:
`-*pb` negated a bool, `!*p8` was C's logical not, and `-<-*pch`, `&<-*pch` and
`<-*pchs` of a send-only channel went to C -- 32 of 74 runs across the
categories, where `^f` of a float turned up beside them: checkUnaryOp let `^` take
any number, and on a P2-EDGE `println(^f, ^g)` of 2.5 and 1 printed `-3 -2`. Each
reader reduces a run to its first operator over the rest since, an arithmetic
operator keeping its operand's type, and checkUnaryRun asks each operator out from
the innermost of the type of what follows it. **A check asked of a run's innermost
operator is asked of each operator out: the operand of each is what the operators
inside it yield.**
**WHAT THE FLAGS SAY IS A TYPE** (2026-10-23, the prefix-run sweep's `!**ppf`). A
variable declared with no type written and none a walk could give it, `f := 7`, `f
:= 2.5`, `s := "x"`, `f := int8(7)`, `c := C(5)`, `fn := two`, carried flags -- a
Kind, a type's name, a signature -- and varTypeAt answered nothing for it, so a
pointer to it was a pointer to nothing and a pointer to that untyped: `f := 7; pf :=
&f; ppf := &pf; **ppf = 1.5` built and printed 1 on a P2-EDGE, and `var s string =
**ppf` reached C -- 93 of 221 pairs of seventeen declaration forms by thirteen uses.
varTypeAt answers what the flags say since: the predeclared type of the Kind, the
type the name declares where the variable is, the signature. The first version of
it answered nothing either: the universe's predeclared types are declared under
tokens scanned from a list of their names (preclaredNames) that had not been kept in
step with the declarations, and float32, float64, int64, uint64, string and Builder
had a zero token -- a type named by one was named "", which no scope declares.
**A walk that types by what was written answers for a variable that has only flags:
the flags say a type, and every pointer to the variable is untyped until the walk
reads them.** **A list of names kept beside the declarations it names is asked of
each declaration: six predeclared types had none.**
**A HEAD IS READ BY EVERY TARGET READER** (2026-10-23, the prefix-run sweep across
the package boundary). The store readers dispatch on a target's head -- a name, a
star over one, a name and a field -- and each read another package's variable as
one of its own: `*lib.PK` matched "a star over a field of a variable lib"
(derefFieldAssignTarget), which no variable lib answers; a sole `*lib.PK = v` was
handed on to checkDerefAssign, written for a local pointer; the compound path's
suffixedTargetKind ended at a head that was no name; and targetTypeNode's qualified
branch refused any type lib's package scope had not written -- the predeclared one
a Kind says, `var K = 7`, and one lib's file wrote, `var K = Two()`. So `lib.K =
"a"` for a `var K = 7` was taken and `lib.K = 1.5` stored 1 on a P2-EDGE, `*lib.P8
= 300` of an int8 printed 44, and a read through two stars, `var y int8 =
**lib.PPKc` of a lib.C, was taken: 94 of 192 stores and reads of twelve
declarations. The parenthesised chain beside it, made a target by d8f0c6f, stored
1 too, `(*h.p).F = 1.5`: the checker's target readers took a dereference behind
steps for nothing, and asked it nothing -- 45 of 110 targets by forms, `(**h.pp).F`
and the header form among them, and 21 of 66 stores into another package's
composites. Each reader asks the qualifier since, and a chain in parentheses is
typed by walking what is between them and then the steps after
(parenTargetType). **A target reader that dispatches on its head asks what a
qualifier is: `*lib.P` is a star over another package's variable, not over a field
of one named lib.** **A shape that newly compiles is asked every type question too:
a reader that reads it as nothing stores whatever it is handed.**
**A VALUE IS ASKED WHERE IT GOES, IN EVERY SHAPE IT COMES IN** (2026-10-23, the
prefix runs put into positions). ef10d27 typed a run where it declares a variable;
prefix-pos puts eighteen runs into the 45 positions a value goes into -- a typed
declaration, an argument, a return, a send, a literal's field, an operand, a
condition, an index -- and 129 of the 810 differed from Go at 9b7550d. Most were
one hole: typeIdentity walked runs of stars only, so `var y T = -*p16` of an int16
went into a `type T int16`, and so did `**pl` of a `var l int16`, the predeclared
type written where the pointee was declared being read as no name. A sweep of
bodies over runs -- ranges, pointer stores and conversions, a select's receive,
builtins -- found the readers beside it (33 of 82): isAddrOperand and
addressOperand knew `&x` as two children, so `(*int8)(&**pp16)` read an int16 as
an int8 and `for ...; &*p8 = 2 {` was taken; the emitter's derefKids the same, so
`range **ppa` and `range *<-ca` of a pointer to an array were "ranging an
integer"; a select's `case y := <-ca:` and a range over a channel that is no name
recorded what the flags could say and no type -- rangeElem took `*pch` of a chan
int16 for an integer range and its variable for an int, and `y = 70000` printed
4464 on a P2-EDGE in every release; and typeIdentity asked names, so an element of
a variable of a defined array type, `arr[0]` of a `type A [3]int16`, went into a T,
and so did a variable declared from one. The worst was no run's at all: a chain in
parentheses over an ARRAY a pointer points at, `(*h.p)[1]`, `(*get())[2]`,
`(**ppa)[0]`, was bound to a temporary of the array's type, `A t = (*p);`, which is
not C -- gcc refused it, and the target's compiler took it and read garbage,
`-2558 -25744 1` for `2 3 1` on a P2-EDGE -- and `(*h.p)[1:]` sliced the copy, so a
store through the slice never reached the array. v0.58.0 built every one; no run
case read an array through a dereference in parentheses of anything but a
variable, and the sweep reached one only because a run in parentheses is such a
dereference. The pointer is bound since and the steps walk through it
(emitParenChain), and a received array is walked from the temporary its receive
fills.
**A sweep that types a shape where it declares a variable asks it in every position
a value goes into.**
**A question answered by a name is asked of the walk where no name answers: an
element of a defined array has a type and no name.**
**A binding a clause makes is typed as the statement's: a select's receive and a
range's are declarations.**
**A head the emitter binds to a temporary is asked whether C copies it: an array a
pointer points at is reached through the pointer.**
**A VIEW THROUGH A POINTER IS OF WHAT IT POINTS AT** (2026-10-23, the paragraph
above's last fix made `(*h.p)[1:]` a view of the array). Asked whether such a view
dangles, the lifetime rules said no: sliceBackingIsFrame reads a slice by its root,
and a struct holding a local array's address is no array -- `h := H{p: &a}; return
h.p[1:]` built, and read `104 836` for `5 6` on a P2-EDGE, the dead frame reused by
the next call, in every release; and the parenthesised and run spellings,
`(*h.p)[1:]`, `(**ppa)[1:]`, `(*ps[0])[1:]`, read no root at all. ptr-view, twenty
ways to reach an array through a pointer by eleven sinks over a local and a package
array, found 164 dangling views of 528 at 93d40a6, 22 through a callee whose summary
read `return (*h.p)[1:]` as handing back nothing (summaryReach knew a dereference in
parentheses over a name only), and 11 false refusals the other way: `(*ppa)[1:]` of
a ppa pointing at a pa that holds a package array's address was a view of pa. A
slice whose operand is a pointer to an array, or a dereference of one, asks what the
pointer reaches since (arrayPtrViewRef), as every sink asks a pointer, and a
reslice asks what it reslices. What the per-variable holder mark costs stays: `hh :=
&H{p: &ga}` holds the frame's literal, and a view through any of its fields is
refused. **A view's storage is asked of what is sliced: a slice of a pointer to an
array views what the pointer reaches, which the pointer's root is not.**
An address is a view of one element, and ptr-addr (seventeen addresses by eight
sinks) found the same shapes unasked: `&(*pa)[1]`, `&(*h.p)[1]`, `&(**ppa)[1]`
stored into a field, an element or a declared variable kept a local's address --
`ghp.p = &(*pa)[1]` printed `-32768` for `5` on a P2-EDGE -- where the checker did
not refuse them first as "a value" (exprPointerness asked an address its root, and
had none, so it fell to the pointee's Kind): 58 false refusals and 22 dangling
addresses of 240 at e8db0ee. Fixing the false refusal handed the emitter a shape it
copied: `&(*h.ps).f` of a struct took a field of a temporary copy, so `*f = 40` left
the struct alone -- `4 0` for `40 7` on the board, in v0.58.0 behind a declaration
the checker asked nothing of -- the struct counterpart of the array copy above. A
dereference in parentheses is bound as its pointer since, a struct's as an array's,
and an address through one asks what the pointer reaches (parenDerefAddrPtr), the
callee's summary as well. **A false refusal fixed in the checker is a program the
emitter is handed next**, and this one was a copy where Go names storage:
**a dereference is storage, and a head the emitter binds asks whether Go could take
its address.** So is a chain in parentheses, `(h.s).n`: bound as the value it reads,
`&(h.s).n` was the address of a copy too, in every release, and the same fix of the
checker made `var f *int = &(h.s).n` compile into it; the parentheses group nothing,
and the emitter writes the chain without them since (spliceParenSteps), the lifetime
readers and the summaries splicing it alike. Sweeping its lifetimes found a hole
older than any of it: `gs = a[:][1:]` of a local array, a reslice typed as a slice
and asked for marks no variable holds. And the board found what the host could not:
the splice moved a run case onto the plain chain path, which wrote `*s[idx()].ps[2]
= 9` with the slice's text twice (idx() ran twice on the host as well) and read
`s[idx()].ps` by binding its header, three words, from text holding the call -- the
target's compiler copies such a value a word at a time and evaluated idx() for each,
333 for 3, in v0.58.0 too. The walks bind it in one word now (emitAccessChainAt,
chainCText, chainHoistPointAt), and a statement's target renders once whoever reads
it first (emitAssignTailOrCopy). Bound before a method call as well, the first
version changed the C of 239 corpus programs, most fuzzer seeds, a temporary each
for nothing -- a call takes its receiver once -- and bound only where a field or an
element is read after it, five.
**A call on the way to a value of several words is bound in the one word it yields:
a pointer, or a struct element's address.**
**A VALUE OF SEVERAL WORDS IS A ROW OF EVERY POSITION, ON THE BOARD** (2026-10-24,
the mw-effect sweep, build/probes/sweeps/mw-effect). The fuzzer's genMultiWord hands
a value of two to four words where a call stands to an ARGUMENT, and the fault
7560f41 fixed was a call in an index on the way to one in a declaration; the sweep
asks the rest: eight types -- a string, a slice, an int64, structs of two, three and
five words, an interface, an array -- read through 24 shapes that call on the way
(`ga[at(1)]`, `pt(1).v`, `gq[at(1)][at(2)]`, `ghs[at(1)].ps[at(2)]`, a receive, a
method), in 47 positions a value goes into, about 7,000 rows held to Go on the host
and on a P2-EDGE, and a second part with the shapes as a statement's TARGET, 1,680
rows. On the host 108 rows differed at first. An interface compared or called
through an index that calls, `ga[at(1)] == x`, `case ga[at(1)]:`, `ga[at(1)].m()`,
wrote the element once for each of its two words and called at(1) twice
(ifaceOperandOnceC, chainCText); an interface in a BUFFERED channel came out as the
last value sent from every receive, the compiler's struct table holding an
interface as a struct of no fields and goZeroSized reading that as no bytes, every
value going into one slot (in every release since buffered channels) -- **A table
read by its value is asked by its key, and asked what else it holds** once more; a
string or a slice reached through an index that calls and indexed on,
`gsl(1)[at(2)][1]`, wrote the header for its .str and its .len. The board found
more than the host: `gq[at(1)][at(2)]` of anything wider than a long read garbage
and called at(2) first, and so did a plain checked program, `c := grid[y][x]` of a
`[3][4]Cell` -- the backend fault doc/wide-row-call-index.c measured for an int64
on 2026-10-06 is a fault of every element of more than four bytes, a struct, a
string, an interface, a slice header, the store of a whole element lost besides,
and wideRowC had been written for the int64 alone. The address of such an element is
right, `&m.pages[space][a>>13]` in p2-11's MMU, and keeps its C: rewritten too, the
first version changed p2-11's binary by four instructions for nothing. **A backend fault measured for
one type is asked of every type of its size: the target's compiler lays out bytes,
not Go's types.** And C's order of evaluation is no order: the target's compiler
evaluated the later of two indexes first, gcc the right side of `ghp.ps[at(1)] =
ga[at(4)]` before its target, and a target's index after a call of several results,
`ghp.ps[at(1)], y = two()`, in every release; the earlier is bound first now
(indexOnceC, bindTargetOrder, emitMultiAssign, emitValueList), the order measured,
not assumed. **Two operands that do something in one C expression are bound in Go's
order, whichever compiler builds it.** The array rows found the last of it: an array
bound for an argument's order was initialized from another, which C has no
initializer for -- gcc refused it, the target's compiler built it and the callee read
garbage -- a row of an array of arrays was not bound at all, and a list's or a loop
header's declaration from a receive, `x, n := <-rc, at(3)`, printed garbage on the
board; arrayShapeOf, called to check an argument's shape, bound the call it read
through into the statement, so `dig2(at(3), pt(1).v)` called pt(1) first -- **A walk
that answers a question must not change the program**, and its doc comment said it
was pure. The compiler crashed on `mkh(1).v[1:]`, a pre-pass typing a call it had no
memo for, which 769f5e1 had brought (capturePrologue lends one). Open, all loud or
right on the board: an array reached through a call or a pointer, `(*pt(1)).v`,
`gsl(1)[at(2)]`, `pa(1)[at(2)]`, `(*ghp.pa)[at(1)]`, is typed as nothing, or as an
int in a range, so it is refused a dozen ways, and `*q = (*pt(1)).v` is a C array
assignment the target's compiler gets right and gcc refuses. **A sweep of a value's
shapes is run on the board before it is called clean: the host's compiler evaluates
in another order what C leaves to it, and reads right what the target's reads
wrong.**
**A FLOAT64 OF TWO WORDS** (2026-10-24, the `float64` branch, built for the user
to weigh before it is merged). float64 as IEEE binary64 in software: `ogo_f64`, a
struct of two words, its arithmetic in words after SoftFloat-2 (an earlier version
in 64-bit C met three of the backend's 64-bit faults on the board and was
rewritten), held to Go on 39.5 million vectors on the host and 5,002 on a P2-EDGE
before it was wired in; printing Go's strconv, ported; math Go's own pure Go
source. Being a struct, every operator the emitter missed was a C compile error,
not a silent wrong value, and the host suite went from 38 failing run cases to none
that way. Then the board: the multi-package program printed 2 for 100 where a
method converted its float64 receiver, a fuzzer seed's pairs part failed its
checksum, six run cases differed, math.Copysign(1, 0) was +Inf in one program and 1
in another. Each was a backend fault the emitter already routes around for a type
of the same width, which the new type had not been given. The runtime read the
second word of a pair a call returned straight off the call
(doc/member-of-struct-result.c): `int(t)` was t's high word. The conversion to an
int64 cast the helper's 64-bit result to a 64-bit type, and emitMathArgs cast
Float64frombits' argument, `(uint64_t)(a | b)` -- a cast to a 64-bit type of a
64-bit expression, which emitConversion's comment has named since August; with the
same C, Copysign was right or +Inf by what the registers held. A call through a
function value returning one was read as one word (valueResultC knew the int64s), a
float64 element of an array in a literal was refused, the target taking no
unbraced aggregate there (structBraceC knew the structs), and a compound literal
inside a conversion helper was what emitConversion had bound since its first
board run. **A new representation is asked every workaround its width already has:
grep the gates for the int64 and the struct, and add the type, before the board
finds them one at a time.** The fuzzer had no float64 a float32 could not hold --
the pairs part drew quarter-integers -- so genF64 draws across the range,
subnormals, 2^53 and its neighbours, long mantissas, hex constants, and computes
each step in Go with an explicit conversion, Go's spec allowing a multiply and an
add to fuse otherwise; asked to fail first, it caught ties rounded up instead of to
even in five seeds of a hundred. Go's math recursed in one place, `Pow(-Inf, y)` as
`Pow(-0, -y)`, which made a goroutine calling Pow unbounded: written out. The cost
is the user's to weigh: an addition 860 clocks to a float32's 660, a division 2,900
to 560, math.Sin 34,000 to 4,000 and Pow 108,000 to 5,900, the master's math being
the C library's float32 with the CORDIC behind it; 6 KB of code for the arithmetic
and printing; a goroutine printing one 1,536 longs of stack to 671; obeam 14 KB
larger, past the 507,904 bytes the ROM boots from an SD card. A program with no
float64 builds byte for byte as before, p2-11 included.
**A WALK ASKS WHAT ITS VALUE CAN HOLD** (2026-10-24, porting the rest of Go's math).
math.FMA's source, a dozen statements rebinding uint64s through calls, `pm2, c =
add64(pm2, zm2, 0)`, made every program importing math hang the compiler: the
lifetime summaries followed each argument of a call through every call its names
held, a branch per argument at each of eight levels, and with no answer kept for a
name already asked; eight statements of `p1, p2 = g(p1, z2)` took 3.8 GB, in every
release. A number or a bool can hold no reference, so the walk does not enter one,
by the callee's parameter type first, an argument becoming its type whatever the
expression, and by the pass's own typing otherwise; a uintptr is still walked. The
binaries of all 995 run cases are the bytes they were. **A walk for what reaches
the frame asks first whether the value can hold a reference: a number reaches
nothing, and walking it costs what the walk's branching costs.**
**A LIBRARY FUNCTION IS BUILT ALONE FOR THE TARGET** (2026-10-24, the same port).
The rest of Go's math, 33 functions, matched Go on the host on all 832 values of a
test, and Lgamma could not be used: a program calling it and printing the result
needed 522 of a cog's 480 registers, Lgamma alone 151, each float64 local and the
backend's temporaries of its nested polynomials two longs. Its three arms are
functions of their own now, the same operations in the same order, and it fits. A
minimal program of each of the 36 functions was built for the target; only Lgamma
had failed. The tables of the new functions cost a program that calls none of them
nothing, because of the pass that leaves out what main does not reach -- which,
measured on obeam, had given up on every program whose C defines a helper in both
branches of an `#ifdef`, and keeps it whole now. **A function ported into a
library is built alone for the target before it is called done: a function no
program can fit in a cog is no function.**
**A FLOAT64 IS ASKED WHAT A DOUBLE WAS** (2026-10-24, dom61's mutants against their
Go twins). Of 400, two differed: a deadlock Go reports and the host waits out, and
`2*math.Pi*230*t` turned into `2/math.Pi*230*t`, which printed 21.71249907149966
for Go's 21.712499071499664, a ULP. A level of float64s was written a step at a
time (emitF64Level), ahead of emitFloatPrefixFold, which has folded the constant
operands leading a double's level as Go does since September and was not reached
for the new type: `2/3*x` was 0.666... where Go divides the untyped integers to 0,
`1e300*1e300/1e300*x` +Inf, and `1<<3*x` was refused. The emitter's tests of
`"double"` held two more. roundConstTo rounded a typed constant to a double and
not to the ogo_f64, so `const B float64 = 0.1; B*3` was 0.3 where Go's is
0.30000000000000004, the tie rounded to even; and convFoldVal folded no conversion
to a float64, so `const X = float64(0.1) * 3` wrote helper calls into a static
initializer, which C refuses. Under them, the fold rounded a typed float at no
step, the float32 too, in releases: `const F float32 = 0.1`, `F*3*11` printed 3.3
on a P2-EDGE built by v0.58.0, where Go rounds F*3 first and prints 3.3000002.
foldValSeq rounds now after each operation of a typed float, and an untyped operand
meeting one is rounded as Go converts it. And `*p = 1 << 41` through `p :=
&a[1]` of a float64 element wrote the integer to C: the checker spells a constant
as the float64 it becomes where it checks the store, and checkDerefAssign read the
type NAME the walk gives an element's address (nameInferred) as a defined type's
and asked only checkDefinedType -- so `*p = 300` of an int8 element was taken too,
and printed 44 on a P2-EDGE built by v0.58.0. Five run cases build differently, a
constant conversion now folded, and print the same on the board; no fuzzer seed's C
changed, genF64 never putting two constants ahead of a variable. CLAUDE.md's rule
for a migration was asked of the workarounds (A FLOAT64 OF TWO WORDS) and not of
the folds. **A new spelling of a type is grepped for in every test of the old one:
each `"double"` the float64 was not added to was a question it had not been
asked.** **A name a walk records is a mark like the flags: a check reading a name
as a defined type's asks the type the walk gave first.**
**A COUNTER THAT COUNTS PASSES EVERY ORDER** (2026-10-24, the same evening). Probing
the positions the fold reaches, `k(1) + k(2)` of two float64 calls ran k(2) first
on the host: an operation of float64s is a helper's call, `ogo_f64_add(k(1),
k(2))`, C leaves the order of its arguments open, and bindOperandsInOrder bound no
operand of a type bindableCType did not know -- `"double"` was there, the ogo_f64
was not. gcc reversed a store's value, a compound's, a return's, a method's
argument, a condition, a switch, a literal and a for header; the board's compiler
evaluates left to right, but `k(3) > k(4)` is `ogo_f64_lt(k(4), k(3))`, so on a
P2-EDGE that one ran k(4) first. None of it shipped, the float64 being a day old,
and no run case or seed had seen it: genF64's fdId_1 counted its calls, and a count
is the same in every order. It folds its argument's bits into the counter now,
`fdCalls_1*31 + h`, each expression carrying its calls in Go's order to the
statement that writes it; built by the emitter before the fix, seed 29 fails its
checksum. Two more that evening. The checker rounded no typed float at all, so
`const K = int(B*3*10)` of a `const B float64 = 0.1` built and printed 3 on a
P2-EDGE built by v0.58.0, Go refusing 3.0000000000000004; typedAs rounds now, where
a constant is declared, converted and computed. And the fix of the evening before,
pushed, made the compiler PANIC on obeam: checkDerefAssign asked the store through
`t.f.checkStoreInto`, the FILE the pointer's type was written in, which read the
value's tokens by its own table -- index 3022 of 2014 in obeam's vm package, and
where an index happened to fit, another token, silently. The value's file asks
now, the type going by its scope, as checkHeaderAssign always had it, and
checkConvTarget had the same fault latent; TestCrossFileConsts puts the store in a file
longer than the type's. The suite had passed: no run case nor spec is one package
of several files of any size, and obeam was built only after the push. **A
fuzzer's counter records the order of its calls: one that counts them passes every
order the emitter writes.** **A check reading a value against a type reads each by
its own file: the value's file asks, and the type goes by its scope.** **A change to
the checker is built against obeam and p2-11 before it is pushed: they are the
packages of several files the suite does not have.**
**dom63, linear algebra over float64** (2026-10-25). A 4x4 matrix type with
methods, elimination with partial pivoting, an LU factorization behind an
interface beside a Jacobi iteration, an inverse, least squares, a power iteration,
norms from a table of function values, a product's rows from goroutines -- written
as Go. One find: the row swap through pointers, `*x, *y = *y, *x`, was "a
dereferenced array target is not supported yet" in a multiple assignment, where
`*x = *y` built (5b90c09). The no-heap refusals named their remedies -- an
errors.New in a function, a make of a non-constant length -- and main, as one
function, held 132 locals checked and did not fit a cog, the backend's printf
expansions holding every argument and the checks' panic paths; split into three
stages it matched Go on the host and the board. Of its 400 mutants one was a
deadlock (now DIFFERENCES.md's), and one Go's own compiler crashed on, go1.27.3 on
every GOARCH: a loop storing zero into the diagonal of a zeroed local 2-D array
(build/probes/go-ice, not reported). **A program written as Go that fails the
cog's registers is split where its message says, and the split is the
program's: the backend keeps every printf argument live through the call.**
**A MARK IS MADE IN EMISSION ORDER, AND A LOOP RUNS ITS BODY AGAIN** (2026-10-08,
found probing unsafe.Slice's views). The holder marks are set as statements are
emitted, so a statement read a variable's mark as the statements BEFORE it had left
it -- and a loop runs the statements before again: `for ... { gq = p; p = &x }`, a
three-clause loop's own variable given the address by its post, a range loop, a
backward goto, all stored a local's address in a package variable in silence, in
every release. EmitC emits a program again while a loop's body marks a variable its
head can see (loopSeeds: emitFor and a goto target record their head's marks,
noteLoopSeeds at the body's end or at a goto back compares, applyLoopSeeds gives
the next pass's head the difference -- a for's own variable after its init), until
nothing new is marked; each pass a fresh emitter, so nothing of the first leaks into
the C. The corpus and p2-11 build byte-identically, p2-11 in the same time. Beside
it, the marks of a slice and the local it VIEWS were two: `s := a[:]; a[0] = &x; gq
= s[0]`, and the store the other way, `s[0] = &x; gq = a[0]`, were taken -- s held
a's marks as they stood when it was bound. A read of a slice's elements asks the
marks of whatever any statement may bind it to view (viewedHolder over bindViews,
the function's summaryHolds), through copies, reslices, rows, fields and
unsafe.Slice; a store through a view known to view a local marks it
(noteStoredThrough, viewedLocal). **A mark is a fact about a variable at a point;
a rule reading one asks what reaches that point -- a loop's back edge and an alias
both do.** TestEmitCLoopCarriedMarks, TestEmitCSliceViewMarks.
The pointer row the same day: `*p` was read through a pointer's mark and `(*ps)[0]`,
`(*ph).s[0]` -- the parenthesised dereference with steps -- by nothing (frameRefOf
reads factorDerefChain now); a reslice of it was asked whether the pointer pointed
at ANY local, refusing `(*ps)[1:]` of an `s := gs` (sliceBackingIsFrame asks the
slice it points at); and the CONTENTS of `&s`, a header viewing a local array, were
no reference to a callee keeping `*ps` (contentsRef answers the backing, `view`).
That last opened a false refusal of the commonest pointer idiom, `func pop(ps *[]T)
{ *ps = (*ps)[1:] }`: a parameter's contents stored through a pointer parameter
were "kept" (the summary has no slot for the storage the caller chose), and stored
back through the SAME parameter they keep nothing -- refused for a slice of pointers
to locals and a struct holding a view in every release, and for every view of a
local array once contents answered the backing. TestEmitCPointerToSliceMarks.

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

**A STRUCT THE BACKEND DOES NOT RETURN IN REGISTERS IT RETURNS ON A HEAP**
(2026-10-13, found sizing the testing package's subtest naming). spin2cpp's
functions.c gives a function whose result TypeGoesOnStack (frontends/common.c: over
16 bytes, an array of non-longs, or a struct not longOnly -- a member that is not one
long, a nested struct of more than one long included) a COPYREFTYPE result and
no_inline, and CoerceAssignTypes (frontends/types.c) makes EVERY `return` of one copy
the value into `_gc_alloc_managed(size)`, "to persist beyond this function": a block
of a garbage-collected heap of 1 KB a call, the collector (sys/gcalloc.spin) scanning
Hub RAM from 0 to the stack pointer when it fills, and a failed allocation answering
0, which the copy then writes over. Parameters are not so, a by-value struct argument
going to a stack temporary. Measured on a P2-EDGE at 160 MHz, a function of (int,
bool): 1689 clocks a call at least, one call in sixty-odd 883,401 (5.5 ms), and 11.3
million (71 ms) in a program padded to p2-11's 378 KB; written through an out
parameter, 141 every call in both, the small program 17848 bytes before and 9532
after. It was Go's commonest results -- (int, bool), (T, error),
(string, bool), (int64, bool), ([]T, bool) -- any struct with a byte or a bool, the
Builder's writes, append's ok form and a channel's struct element: 87 run cases,
none of the 321 fuzzer seeds counted (the generator's aggregates hold arrays, which
took the out parameter already), and p2-11 in 26 places (fat's (File, error), rk11's
report, dl11's control), each program carrying some 8 KB of collector besides. Every
such result is written through the out parameter a struct holding an array already
took (structOutOf asks heapReturned, which mirrors the rule over cLayout's layout,
longsOfFields), the runtime helpers write theirs through a pointer, and
chanStructByPtr takes such an element. The out path had been walked by structs
holding arrays alone, and widening it met four rows none had: `return f()`
forwarding a call it could not name (forwardedOutCallC assigns the value) or
widening the results into interfaces (widenForwardInto), a literal of several
results called as a statement, and a PROMOTED method -- declaredCalleeC named the
outer type's, and outCallC handed the outer receiver (outRecvC). And `make board`
found a fifth the host could not: a VALUE receiver read through a call,
`bus.ch(1).which()`, went to the out-parameter call as `*ogo_nil_Chan_ptr(ch(...))`,
and the target's compiler passes a struct of N longs by value from such an
expression evaluating it N times -- the call ran twice, once under gcc. outCallC
binds what a call answers first, as the by-value path did. The same backend fault
is OPEN in the emitter for a by-value ARGUMENT, `take(*getp())`, a call through a
function value and a send: N calls for N longs, 2 to 4, measured on a P2-EDGE; a
return, a literal's element, a field store and a struct with a sub-word member are
right (the held list of backend faults, 13). Held:
TestTargetBuild reads every run case's listing for the allocator (heapFree in
board_test.go, so every test building for the target does), and
TestBuildNoBackendHeap holds the rule both ways, no heap for the shapes the backend
puts there and no out parameter for those it returns in registers -- each failing
on its planted fault, the guard on HEAD's Builder. p2-11's binary 11 KB smaller, its
test binaries 8 KB where they had a site, its registers and build time unchanged.
**A promise the language makes about the hardware is checked in the listing**: "no
heap" was true of OctoGo's C and not of the binary.

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
A constant passing 64 bits on the way, `var a uint64 = 1<<64 - 1`, was the row's
POSITION cell (2026-10-13, found checking flexprop#122's fix): a local declared
64-bit took the one literal of its value (emitVarDeclInit), and a package variable
(emitGlobalInit) and a literal's element (emitLitElement) rendered the untyped
level, which has no type to fold at -- the shift helper in a static initializer, a
literal no C type holds, a negated long long in an aggregate, each a build the
target refused. Both fold at the declared type now (staticWideLit). **A step one
declaration form takes is asked of the others**: a local, a package variable and
an element are three writers of one initializer.

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
A PRINT HOLDS A LOCK (2026-10-18, found writing the stack checks' goroutine test):
`go work(5); println(<-out)`, with work printing a number before it sends, printed
nothing at all on a P2-EDGE, and so did a goroutine's panic while main waited in such
a print -- the simplest program a goroutine can be in, and no run case had it: each
one printing from a goroutine while main received printed a string, which goes out
through putchar without the lock, or bound the receive first. The target's compiler
writes a printf of a constant format as its own print calls between `__lockio` and
the store that frees the lock, and evaluates the arguments in between, so main took
the lock and waited in the receive while the goroutine waited for the lock. A wait of
500 ms put into main's argument delayed the goroutine's panic by exactly that, and
plain C with a cog printing while main waited outside a print showed nothing of it. A
print's argument with an effect is evaluated before the print now, one argument as
several were (hoistPrintArgs): 175 of 1378 corpus programs changed, each by that
binding alone. **What the backend makes of a library call is read in the listing for
what it holds while the arguments run.** **A goroutine's output is asked with main
waiting in each way it can wait, a print of a receive among them.**

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
parenthesised, `(mk(1)).Len()`, since 2026-09-25, spliceParenArrayCall); a STATEMENT
beginning with a literal of a defined type, `Taps{}.Zero()`, a syntax error (as a value
an index or a method called directly on a literal of a DEFINED array or slice type,
`IS{4, 5}[1]`, `IS{6, 7}.Sum()`, works since 2026-10-06, namedArrayLitChain); a
field a BRACKETED literal lacks, `[]P{gp}[0].in.nosuch`,
"a []P literal cannot be read through this suffix" from the emitter, where the valid
form works (a call's result, a parenthesised value and a named literal are checked at
any depth since 2026-09-25, `getp().in.nosuch` and `(&gp).in.nosuch` being "type Q
has no field nosuch": walkSteps' missingAt); `[]byte(s)`
and `[]rune(s)` of a string VARIABLE where the copy is kept (a copy of a length
known at run time; a constant's converts since 2026-09-23, constBytesConv, and one
ranged over, measured by len or, the bytes, copied or appended from reads the
string where it is since 2026-10-15, stringSliceConvOf); a parenthesised function value started on a cog,
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
A RECEIVER STORED THROUGH A PARAMETER was a row with no slot at all (2026-10-10,
found closing the errors package's open item): crossInto says which parameters a
parameter is stored through, and nothing said it of the receiver, so `func (m *M)
Reg(p **M) { *p = m }` was summarised as keeping nothing and `lm.Reg(&gp)` left a
dead frame's address in gp -- directly, through a relay `reg(m, p)`, a method
calling it, an interface, a method expression, a defer, a promotion: every shape of
the row was taken in v0.54.0. `recvInto` is the receiver's slot (storesInto sets
it, the recvEdges carry it with the arguments' owners, methodExprSummary shifts it
to parameter 0), and the call site asks it (checkRecvInto, from checkRecvAt and
checkIfaceRecvKept): a receiver of this frame stored through an argument that
outlives it, or that nothing here can name, is refused; through a local, the local
holds the receiver's address from there on. Beside it, an INTERFACE parameter is a
slot (pointerParamSlots), a store reaching it through an assertion or a type
switch's name -- errors.As's shape, `if d, ok := dst.(**E); ok { *d = src }` -- and
the caller's receiver handed on as an argument is an owner of its own, argRecv, so
`func (b *B) Set(v []int) { setInto(b, v) }` is a store INTO the receiver
(leakRecv), where it was a store through a local, which dies with the frame. The
call results were the same omission one level over: derivedEdges, retEdges and
viaEdges ran from a parameter to a parameter, so `gp = id(m)`, `gp = m.Self()`,
`keep(id(m))`, `return id(m)` and `x := id(m); gp = x` in a method kept m with
nothing recorded. An edge's end may be the receiver now, from < 0 for the caller's
and to < 0 for the callee's (handsBack, gatesHold; valueCallT.recv for a method's
receiver, recvEdge.via). **A slot a summary has for a parameter, it has for the
receiver**: the receiver is a parameter in all but name, and each summary written
for parameters alone left it out. TestEmitCRecvIntoEscape, TestEmitCRecvKeptEscape.

Asking it through an interface found the receiver rule for an interface value asked
at two statement sites alone: `defer s.Save()`, `h.s.Save()` and `ss[0].Save()`
kept a local's address for a Save keeping its receiver, and `go s.Bump()` handed it
to a cog, receiverFrameRef never asked of an interface (checkIfaceRecvKept at the
defer and chain sites, a chain judged by its root's mark; receiverFrameRef in
emitGo's interface branch) -- and receiverFrameRef had not heard of main's
outermost block, refusing `go lm.Bump()` there (outlivesCogs). Found writing the
probes, each older: `case **E:` was refused as naming no type where the assertion
`x.(**E)` was taken (casePtrToPtr, casePtrToPtrC); `**pp` of a `pp **int` had no
type, operandType taking one star and giving up at two, so `var s string = **pp`
reached C; and `n := 5; take(&n)` for an `any` was refused as "a value of type
int", the rule refusing a Kind's value going into an interface reading `&n` by its
pointee's Kind and only a variable with a written type having a name to say
otherwise.
The same day, the parameter's rule and the receiver's were found holding the same
leniency the other way round. checkIntoArgs let through a target it could not NAME
-- a call's result, a field or an element read out, a dereference -- as "left alone
rather than refused on suspicion", where only the callee's store is a finding and
the target's lifetime is the question: `fill(&le, pp())` for a pp returning &gp
read 1007 on the host for Go's 7. It refuses one now, as checkRecvInto did from the
start. And a receiver a CALL hands back, `id(&lb).Save()`, `gbAt().set(a[:])`,
`asSaver(&lb).Save()`, was `opaque` to checkRecvAt and asked nothing at all: what
the call returns is frameRefOf's answer now, a may (checkOpaqueRecv,
checkIfaceOpaqueRecv), the receiver rebuilt from the chain's own tokens
(opaqueRecvExpr: an import qualifier's member, which chainCText takes off the
steps, put back from the tokens before them -- compared as `e.f.ch(t) == PERIOD`,
a token's kind being a Symbol: `== '.'` compiles and never matches) and, for a
value the method takes the address of,
`mkHB(&lh).inner.Save()`, asked of the pointer it is addressed through
(opaqueRecvAsk); an argument stored into such a receiver is refused whatever it
returns. **"Nothing here can name it" is an answer for a refusal, never for a
permission**: a rule that lets an unknown through is the hole KNOWN MEANS MUST
names, from the other side. Next, frameRefOf answered nothing for a call's result
WITH STEPS, nor for a call through a call's result or an element, at any sink --
`gbp = idH(&lh).c`, `&idH(&lh).inner`, `mkS(&lb).p`, `idSl(a[:])[1:]`,
`id(&lb).Self()`, `pick()(&lb)`, `tab[0](&lb)`: 20 of 20 such programs built. It
answers now (callStepsRef: where the steps reach -- the result itself, what it
points at, or past a further pointer -- decides between what the call returns and
what that storage holds, callContentsRef; methodOnCallAt for a method on a call's
result, which directCall declines; valueCallSummary, the function type's union,
where callResultInfo types a value callee and names none). What a pointer result
points at is never the callee's frame, a return of that being refused, so its
contents are the handed-back arguments' contents and no part analysis is needed.
The callee's side of the same row came next, in the summaries, by shape: 15 of 18
programs a function writes with its parameter -- `gbp = idH(h).c`, `&idH(h).inner`,
`mkS(b).p`, `id(b).Self()`, `keep(idH(h).c)`, `x := idH(h).c; gbp = x` -- were
summarised as keeping nothing. A derivedEdge says how its sink reads the result now
(res: whole, contents, both -- resModeOf, by the callee's declared result type, no
local's type being needed: read through a pointer, a slice or a string it is what
the result points at holds, one step into a value a part of it, anything longer
either), and carries gates as the cross edges do (via), for a method called on
what a call returns (valueCallT.recvCall, recvCallOf; methodOfResult where
methodCallOf cannot walk the head, `pick()(b).Self()`). callExprsIn takes a method
on a call's result as the call it is, viaEdges walks a value with steps by its mode,
summaryHolds holds one by its whole value, and a return reads it by mode (modeRets),
the return's own calls no longer saying it of the whole result (precise). Writing
the controls found the return's over-approximation older than any of it: every call
in a return statement was taken to be handed back whole (stmtCalls), so
`func valueAt(list *Node, v int) int { return find(list, v).val }` refused `g =
valueAt(&a, 2)` as storing a's address in an int, in v0.54.0 -- a function whose
every result is a number or a bool hands nothing back now (returnsNoRef; a uintptr
is an address, and counts). **A summary is asked what the value it records can
hold**: the shape says which parameter a value came from, and only its type says
whether anything of it can arrive.

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

## Implementation status, as CLAUDE.md had it on 2026-10-13

The section in full, before it was condensed to the current state; CLAUDE.md's
version is the current one.

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
  change is where it is read from. Since 2026-10-08 it has Go's Log, Logf, Error,
  Errorf, Fatal, Fatalf, Skip, Skipf, FailNow, SkipNow, Name and Helper. The
  formatting ones are declared without bodies and written at each call as printf is
  (emitTestingCall: go test's "    file:line: " decoration, %v per operand for the
  non-f forms, then Fail, FailNow or SkipNow), their arguments checked as printf's
  are where the call is checked (checkArgsDeclaredIn -> checkTestingFormatArgs), not
  against `args ...any`, which no value of a Kind goes into here; no prototype is
  written for them. FailNow and SkipNow leave a test by longjmp to `run`, the
  testing package's other bodiless function, whose C the emitter writes behind its
  own header (emitTestingIntrinsic) -- with the hardware-stack save (see recover
  above); on another cog they end the goroutine as its trampoline does
  (ogo_test_goexit, after the pool, testingRuntimeC). The runner calls each test
  through testing.RunTest, which prints go test -v's `=== RUN` line and its result.
  The host shim models `_cogid` and a cog stopping itself for it. SUBTESTS since
  2026-10-11: t.Run is ordinary source in the testing package (a T per subtest in
  Run's frame, its full name built in a buffer of its own, a table of 256 subtests a
  test, printed under the test's result in the order they began, as go test -v
  prints them, names rewritten and numbered `#01` as Go's), `=== NAME` written ahead
  of a message where output turns to another test (named, which emitTestingCall
  calls first), and run nesting -- a jump target and a hardware-stack save per depth,
  nine, exit taking the innermost, t.Run on another cog a panic. The subtest's name
  is kept in a package table, so the lifetime rules refuse one viewing a local
  buffer where it is passed. Measured against go test -v's own output, on the host
  and the board. **A library function a goroutine may call does not recurse**, met
  again: the full name printed recursively made every goroutine calling t.Log a
  recursion to the stack bound. And what EVERY test runs costs what it did: p2-11's
  tests outgrew the cog by three registers, the Builder's and the rune decoder's
  leaf temporaries (`_varNN`, ten of them) pulled in by the result lines -- so
  RunTest, the result and NAME lines reach a subtest's name only through a function
  the runner hands in (names, below), only t.Run's setup (begin) uses a Builder,
  which a program running no subtest leaves out, and
  nothing of the testing package is inlined (inlineCandidate), its functions
  standing on every test's call chain (TestBuildTestingFrugal reads the listing).
  **A runtime shared by every program of a kind is held to its old register count**:
  p2-11 builds near the cog's limit, and its tests are the measure. BENCHMARKS
  since 2026-10-12: a *testing.B of the same package source (its Log to Skipf
  written at each call as T's are, testingCallOf taking either base), BenchFuncs
  beside TestFuncs (testingFuncs, vet's message), `ogo test -bench`, `-benchtime`
  (a duration or `Nx`) and `-benchmem`, the runner calling StartBenchmarks (the
  labels: `pkg:` the import path, `cpu: Propeller 2 at 160 MHz` the clock built)
  and RunBenchmark per benchmark after the tests -- run1, then go test's predictN
  ramp, through runB, a second jump target user of run's -- and the capture's
  timeout grown with them. The time is clock(), the 64-bit counter (`_cnthl`, words
  joined in a variable), turned to nanoseconds only when printed, and ns/op is
  computed in integers (printNsPerOp), a float64 being 32 bits here; MB/s and a
  metric are printed by prettyPrint's own formats. The host shim's OGO_FAKE_CLOCK
  makes time move only where a program waits, by what it asked, which is what lets
  TestBenchmarkFakeClock expect the ramp and hold every result line to what Go's
  own testing.BenchmarkResult prints for the same N and time; on the board,
  TestOnBoardOgoTestBench times waits of known length, at 160 and 200 MHz. Measuring
  the counter in C first found the cast fault in two printf verbs (89b9dbe):
  **a workaround is a row of every site writing its shape** -- emitConversion and
  the integer verbs routed around a cast to a 64-bit type of a 64-bit expression,
  and %q and %U, written later, did not (emitWideArg;
  doc/cast-of-64bit-expression.c, the fault cut out of shift64-by-variable.c's).
  b.Loop and b.Run came the same day, each go test's algorithm transcribed --
  loopSlowPath's ramp inside one call, its poison bit in loopI while the timer is
  stopped, run1's "a benchmark with subs prints no line", the width's slack of 8,
  the subtest table reused for the names -- and held byte for byte to go test -v
  -cpu 1's output for the same programs (a run case; TestBenchmarkFakeClock for
  the timed ramps). Transcribing found go test's quirk, kept: a SkipNow in a run
  after the first prints `0  NaN ns/op`, the Goexit killing launch before it records
  a result. The testing package calls its own Fatal and Error now, the checker
  asking a print's arguments there too (checkMethodCallOn). Loop alone of the
  package is marked for inlining (inlineCandidate), its fields after N and its first
  statement a read, so the receiver is checked once: 88 clocks a pass on the board,
  144 called with four checks, a loop to b.N 48 -- moving N off offset 0 cost that
  one 8 (TestBuildBenchLoopInlined). The whole testing package's C is in every
  test program and the backend drops the functions nothing reaches, but not a
  constant string's file-scope header (stringLitName): the first version grew each
  of p2-11's test binaries by 240 bytes for strings only benchmarks print. A string
  only a benchmark's code prints is a printf FORMAT, a C literal inside the
  function, and goes with it -- p2-11's tests are the size they were. **What a
  library adds for one kind of program is measured in a program of the other kind,
  by its binary.** A table-driven benchmark or subtest cannot
  capture its case in a literal (no closures); it goes through a package variable.
  `ogo test -timeout` came with them, sub-benchmarks being uncountable ahead.
  -run and -bench SELECT SUBTESTS since 2026-10-13, as go test's match.go does: the
  pattern split at each slash and bar outside brackets and parentheses
  (splitTestPattern, octogo/testfilter.go), its levels rewritten, the test's own
  level matched where the runner is generated (TestFilter.Top, which also says a
  benchmark matches only in part, testing.OnlySubs), and the levels below on the
  board by a Pike machine in the testing package over a table regexp/syntax
  compiled (CompileTestFilter: a literal folding case spelled out as its orbit, so
  the board needs no Unicode tables). A subtest not selected takes its slot, counted
  toward a name used again (verdict notRun, skipped by finish). The runner HANDS
  the machine in, `testing.SetFilter(prog, state, alive, testing.MatchSub)`, its
  tables its own package arrays: flexcc keeps every function whose address is taken
  anywhere, even in a function nothing calls, and every global such a function
  touches, so a machine called from begin would be in every program running
  subtests, and one assigned inside the testing package in every test program.
  Measured: a test running no subtest grew 40 bytes (the four variables, finish's
  skip), one running subtests 252 (the checked indirect call in begin), cog
  registers unchanged (TestBuildTestingFrugal reads the listing for the machine).
  **What only some runs use is handed in by the code those runs generate.** Held to
  go test by TestFilterMatchesGoTest -- 73 patterns since, each through go test's
  own binary and through the runner on the host shim -- whose first list passed a
  planted fault in the in-part answer until a LEAF sub-benchmark matched in part
  was on it (**an oracle comparison is asked to fail before it is believed**), and
  on the board by TestOnBoardOgoTestFilter.
  A SUBTEST'S NAME is go test's since 2026-10-13, both halves its matcher's. Its
  count is Go 1.27's unique, which keeps a count per BASE -- the parent's full name,
  a slash, the name rewritten -- and skips a count a name was written with, `b#01`
  after two `b` being `b#01#01`, `b` after an explicit `b#01` `b#02`: the first
  version was the unique remembered from an older Go, a chain of counts, and
  TestD's oracle cases showed it (**the oracle's source is read, not remembered**:
  $(go env GOROOT)/src/testing/match.go). No table of counts is kept -- every array
  the testing package declares is in every test program -- so unique scans the
  slots before (baseTries: the latest of a base says its count; isBase compares two
  bases rendered past the full name both begin with, common, in windows of 128
  bytes, the candidate's first window cached). And its rewrite is exact: every rune
  strconv.IsPrint says does not print escaped as QuoteRune writes it, a byte of no
  UTF-8 U+FFFD (decodeRune, Go's validity rules: a range's own decoder spent 17 leaf
  temporaries where the whole old printer spent 9). Past Latin-1 which runes print
  is a table, octogo.NotPrintTable, made from the strconv of the Go that built ogo
  (1477 changes, 1750 bytes: blocks of 32 uvarint distances under a binary-searched
  index), which the RUNNER hands in with the function every test's result lines
  reach a name through, `testing.SetSubtests(ogoNotPrint[:], testing.FullName)`:
  finish, NAME and a benchmark's line call `names`, nil in a program running no
  subtest, so such a program carries none of the naming. The runner hands them in
  where the source names Run (MayRunSubtests, sound: every way to reach T.Run or
  B.Run spells it) and the emitted C reaches testing_T_Run or testing_B_Run
  (RunsSubtests); p2-11's emulator has a Run of its own, and emitting again without
  is cheaper than its pdp11 tests' second flexcc build, 10.8 s. Run cases calling
  RunTest themselves hand in no table, `testing.SetSubtests(nil, testing.FullName)`,
  and print a rune past U+00FF as itself. The old printer had brought the BACKEND'S
  HEAP into every test program: flexcc returns a 12-byte struct, Builder.WriteString's
  (int, error), in a copy it allocates from a garbage-collected heap
  (`_gc_alloc_managed` in the listing, `__system___gc_*` and `__heap_base` with
  it), and rewriteUnit reached it -- so the naming writes byte by byte. Measured by
  binaries: a test running no subtest 30832 bytes before and 19700 after, cog
  registers 38 and 23; one running a subtest 33864 and 28452 plus the table, 43 and
  41; each of p2-11's ten test binaries 3 KB smaller, 11 KB where nothing else
  needed the heap. **A function a program calls may bring the backend's heap: the
  listing says, `__system___gc_`.** (Not only the Builder's: every struct the
  backend does not return in registers, which an out parameter carries since the
  same day -- A STRUCT THE BACKEND DOES NOT RETURN IN REGISTERS, below.) Held to go
  test by TestSubtestNamesMatchGoTest
  (the runes on both sides of every change, the corners of Latin-1, invalid UTF-8,
  names past a window), TestD's counts and TestOnBoardOgoTestNames, each failing
  on its planted fault -- a block search off by one, a count's check skipped, a
  window compared alone (a loop, every count looking taken). Met on the way: a
  static cycle render -> writeBase -> render, never taken at run time, is a
  recursion to the stack reader, and a goroutine calling t.Log would have been
  refused for it; and the emitter joins a tagless switch's comma cases with `||`
  leaving an `&&` operand unparenthesized, which C reads right and gcc -Wall warns
  about, so no run case can hold the shape (open). Not yet: Cleanup, RunParallel,
  Elapsed,
  a deferred or started t.Log (refused with the literal that does it), the deferred
  calls FailNow skips, and Helper's attribution of a line to the caller.
- **Interfaces are done and devirtualization is not.** A method call through an
  interface is an indirect call through a static vtable, always; the WPO pass that
  would make it direct where the concrete type is provable is design-only. Nothing
  is rejected for failing to prove one -- rejection is spent on lifetime.
  A case or an assertion naming an INTERFACE tests every type of the program that
  implements both, and the emitter enumerates them (implementsIface): asked by
  method NAME only until 2026-10-09, a `Unwrap() []error` met a case of a named
  `interface{ Unwrap() error }` and was chosen, silent on the board, and a method
  through an embedded interface field did not count. It compares the C signature
  since (methodHasSig: a defined type is a typedef of its own, an array parameter
  compared by its extents). **A question the checker answers for an assignment is
  asked again wherever the emitter enumerates on its own** -- found writing Go's
  errors.Is by hand, as a probe of whether an errors package could be ordinary
  source.
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
  intrinsics; `unsafe`, `testing`, `strings`, `bytes`, `math` and `errors` are
  likewise bare, module or not. There is no standard library beyond those.
- **The `errors` package** (2026-10-09, `errorsSrc` in build.go, the compiler's part
  in `internal/octogo/errorspkg.go`): Go's but for Join, held to Go by
  TestErrorsMatchesGo and TestOnBoardErrors. Two functions have no body. `New` is
  written at the call (errorsNewC) as the address of an errorString made where the
  call stands -- a static object and a static interface value in a package
  variable's initializer (the sentinel), two temporaries of the block in a function,
  never a compound literal -- and frameRefOf answers errorsNewRef for one in a
  function, so the lifetime rules treat it as `&T{}`; `defer`/`go errors.New` are the
  checker's refusal. `asTarget`, As's assignability test, is minted after the last
  body (mintErrorsAsTarget): target's table against every table of the empty
  interface (`ifaceConcretes(any)`), rewritten until minting it makes no new one, and
  its summary is seeded (seedErrorsAsTarget: param 0 stored through param 1), which
  is what refuses `errors.As(localErr, &gp)`. A target vet's errorsas refuses is the
  emitter's refusal (checkErrorsAsTarget). Is and As walk iteratively (`walk`, an
  explicit stack of 8 lists): written recursively as Go's, every goroutine calling
  errors.Is was refused for want of a stack bound, which only the board build showed
  -- **a library function a goroutine may call does not recurse**. Building it by
  hand first as an ordinary package found four faults older than it: the
  signature-blind interface case (above), a `**T` table named `..._vt_T*`, a struct
  literal holding an array of interfaces refused (emitLitElement read the array
  field's ELEMENT type as the field's), and `fill(&le, &p)` with both local refused
  (checkIntoArgsIn looked through `&p` as through a pointer `p`). Two were left
  open, each closed with its row: a callee storing through an ASSERTION of an `any`
  parameter (A RECEIVER STORED THROUGH A PARAMETER, below), and a type switch case
  of an interface WRITTEN OUT, `case interface{ Unwrap() error }:`, a syntax error
  where the assertion parsed (AN INTERFACE WRITTEN OUT IS A TYPE, below).
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
  taken), `String`, `StringData` and `SliceData` ("not supported yet"), and
  printf verbs but `%v` and `%T`. `Add` and `Slice` came on 2026-10-08
  (checkUnsafeAddSlice; emitUnsafeAddSlice, a byte offset and a per-element helper,
  usliceHelperDef, panicking where Go's runtime.unsafeslice64 does): the checker
  types them through unsafeSliceChain wherever operandType walks, steps after the
  call included, and the lifetime readers read Add as its pointer and a Slice, a
  reslice of one and an element's address in one as the storage the pointer points
  at (unsafeConvOperand, unsafeSliceAddrOperand), the summaries an element of one as
  the pointer's contents. `Sizeof`, `Alignof` and `Offsetof` came on
  2026-10-08 (unsafelayout.go): constants of type uintptr the checker computes from
  the TARGET's layout (targetLayout) -- recorded by the call's parentheses in
  lenConsts as a constant len is, from the fold (constUnsafe) and wherever the call
  stands (checkUnsafeLayoutCall) -- and the emitter writes the value. The layout was
  measured on the board in C first and then read off spin2cpp's source: TypeAlign
  (a scalar's size, 8 giving 4; 4 for a pointer and a struct; an array's element's),
  PaddedTypeAlign for a member (4 for four bytes or more), and a struct laid out
  TWICE (frontends/common.c): the declaring pass aligns a member of 2 bytes on 2 and
  of 4 or more on 4, nothing else, and rounds to 4, which is the size; the placing
  pass (fixupVarOffset) aligns each on its PaddedTypeAlign, gives the offsets, and
  RAISES the size, unrounded, where it ends past the first. They part on a zero-sized
  member, which only the second aligns -- and a struct the second sized larger was
  laid out by its containers with the first pass's size, its last field overwritten
  by the next field of the container, in silence (doc/zero-length-member-moved.c).
  Such a member is declared over uint8_t where it would move (zeroSizedDecl,
  zeroMoves over cLayout, the emitter's copy of the rules), so the two passes agree
  and every struct is a multiple of four; a struct of zero-sized fields alone gets
  `_ogo_empty`. Each use of Sizeof whose operand has a C type the emitter names adds
  `sizeof(T) == v` to one `static char ogo_layout_check[...]` under `__FLEXC__`, a
  negative size failing the build where the model is wrong -- flexcc takes a
  negative array size in a typedef in silence and no offsetof in an array size, so
  the check is a sized global and covers sizes only; the offsets are pinned by two
  run cases measured against the target's offsetof. The check fired on its first
  use, which is how the second pass was found. Tests: `unsafe_pointer*.ogo`, `TestEmitCUnsafeLifetime`,
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
  parameter passing, incompatible types in comparison. A build the backend REFUSES keeps
  its C too since 2026-10-06 (backendRefusal): the error named a line of a file in
  the build's removed directory, so a refusal such as doc/uint64-shift-32-pair.c's
  could be neither read nor reported; one about the program's own .spin2 object is
  left alone. And the backend's "final output size ... exceeds maximum of 524288"
  -- a warning, the binary written all the same -- is the PROGRAM's size, which the
  rule above had called a fault of ogo's (hubOverflow).
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
  measured right on the board; the pin carries it since 2026-10-08, and the
  workaround stays, costing nothing).
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
  none failing, warned about or refused by the backend. Seeds 201-500 with cf3bf5b
  on 2026-10-06, the aggregate generator with all five of its shapes: 280 passing
  (three on a second load), 19 outgrowing, seed 359 refused by the backend
  (doc/uint64-shift-32-pair.c), none failing. Seeds 5001-5500 with 40aa62b
  on 2026-10-05: 465 passing, 35 outgrowing, none failing; ten loads timed out
  ("timeout waiting for checksum", "sendAddressSize: timeout") and passed on a
  second, the loader's and not the program's. A seed that outgrows can take
  ten minutes to say so, the backend's allocator on one function of 200 lines, twice
  over when its functions were marked (seed 3146: 333 s and 244 s).
  **Aggregates** (2026-10-06, `aggregates.go`): no generated program had passed or
  returned a struct or an array, which is the family the target's compiler has been
  wrong about in silence most often (a struct over four words or of a sub-word
  member returned through a reference sized wrong, flexprop#113; a struct holding
  an array assigned at some sizes; a static initializer laid out otherwise). Two
  programs in three declare a struct of mixed widths -- a sub-word field, an array
  of 8- to 32-bit elements, a 64-bit scalar, three to seven fields -- with agMk
  (built from an int), agMod (a by-value parameter updated and returned), agSum,
  agPtr, agArr (an array of three by value), agPair (beside an int), a value and a
  pointer method, and agRun calling them where a call stands, through a field of a
  call's result, a slice of the array and equality; half of those send it to a cog
  over a channel and back (agCog, drawn last and written after agRun). What agRun
  returns is computed by the generator in Go with Go's integer semantics, int32
  arithmetic included, as the tables are, not by the VM. **It draws from its own
  source (`aggRand`, from the seed) and mints no name from VarSeq**: drawing from
  Rand shifted every later draw, and two rare constructs fell out of the coverage
  corpus; this way every other part of a seed's program is what it was, but for
  the expected values that depend on the checksum. Swept the same day: seeds
  1-2000 on the host shim, clean; 1-200 on a P2-EDGE, 141 with an aggregate and 71
  with the cog, 192 passing and 8 outgrowing a cog, none failing. Seeds 201-300
  the same way: 95 passing (four on a second load) and 5 outgrowing, none failing.
  Three more shapes the same day, each half the time and each drawn after the cog,
  in a function of its own so agRun's registers stay what they were: AN, a struct
  of a sub-word scalar, the aggregate, an array of two of it and another scalar,
  built by a keyed literal and compared, and an array of two of THAT (agNest);
  agTrio, returning `[3]AG` read where the call stands -- an argument, an element's
  method, an element's field, a range (agTrioRun); and agLate, deferring a function,
  a function literal and a value-receiver method each given the aggregate, which it
  changes afterwards (each copy made at its defer). A hand-written probe of all
  three matched Go on the host and the board before the generator wrote one. Seeds
  1-2000 on the host shim, clean; 1-200 on a P2-EDGE, 72 nesting, 65 returning the
  array and 70 deferring, 192 passing and 8 outgrowing, none failing. And a
  fifth, agVia: the aggregate by value through function values -- a local, a
  package variable, a table of two, a literal -- an interface's methods taking and
  returning it, and a method value, each a call through a pointer, where the
  backend has dropped and misconverted arguments before. Seeds 1-2000 on the host
  shim, clean; 1-200 on a P2-EDGE, 76 with it, 192 passing and 8 outgrowing. A
  sixth, agIdx: the array field read by a uint64 and an int64 index, sliced by
  64-bit bounds and copied into a make of a 64-bit length -- no generated program had
  an index of 64 bits, which is why the sweeps never met ogo_bound's. Seeds 1-2000
  on the host shim, clean; 1-200 on a P2-EDGE, 78 with it, 192 passing and 8
  outgrowing. A seventh, agWide: tables of int64 and uint64 of two dimensions
  written and read through a call in the row index (agRow) -- the shape of the
  silent row fault (A ROW OF 64-BIT ELEMENTS INDEXED BY A CALL), which no generated
  program had. Seeds 1-2000 on the host shim, clean; 1-200 on a P2-EDGE, 70 with
  it, 192 passing and 8 outgrowing. Seeds 3501-4000
  with aab09a0, the generator before it: 472 passing, 28 outgrowing, none failing.
  A domain program of the same family -- sample frames of mixed widths filled on
  one cog, passed by value through a channel and a single-producer ring to another
  -- matched Go on the host and the board, and its 1,000 mutants had one row
  taken, a predeclared type as a target's head, `int32.Count++`.
- **Fixed miscompile (found by the oracle):** a shadowing local whose initializer
  references the shadowed name — `var x = x + 5` with an outer `x` in scope — used
  to miscompile, because the emitter names locals verbatim so the C initializer read
  the new (uninitialized) variable instead of the outer one. `emitVarDeclInit` (and
  the array/slice copy paths) now capture the initializer into a fresh temporary
  before the same-named C variable shadows it (`initRefsName` + `newTmp`), covering
  scalar, struct, array (`var a [N]T = a`) and slice (`var xs []T = xs`) forms; see
  the two shadowing `emitRunCases`. This is a targeted capture-before-shadow, not
  the general scope-aware C-naming layer the emitter still lacks.

## The language feature policy's history

Two paragraphs of CLAUDE.md's policy section in full, as they stood on 2026-10-13;
CLAUDE.md keeps the decisions and the rules, condensed.

**A channel is made as Go makes one** (the user's call, 2026-10-08, the review's
first item; done 2026-10-09): `var ch chan T` is nil, `make(chan T)` makes one
(emitMakeChan), its cell static in a package variable's initializer and the
block's in a function, held there by the lifetime rules as a composite literal's
address is (chanMakeRef; a channel counts as a reference, carriesReference and
noteDeclFrameHolder), so a channel a function makes is not returned, kept,
sent or handed to a goroutine -- but for main's outermost block, below. Every
cell one make site makes shares the site's lock, taken first thing in package
initialization (chanSiteInit), so a make costs no lock at run time. A buffered
channel is refused (checkChanMake), and so is a channel variable declared
without a value that nothing writes or addresses and that a channel operation
uses (neverMade: chanUse/written marked where the checker meets each, the
package's reported once every body is checked), which is what every program of
the old rule meets. Gone: every cell a declaration made -- a variable's, an
array element's, a struct field's, nested -- and DIFFERENCES.md's
declaration-site entry. The migration: 184 run cases, 66 spec tests, an example,
the fuzzer's three channels and DIFFERENCES.md by a script (the scratchpad's
migrate.py), ten run cases by hand -- channels in struct fields and arrays,
which nothing refuses unmade and which hung. p2-11 has no channel. Migrating the
spec tests showed `var ch = make(chan T)` asked less than `var ch chan T`: five
files lost a diagnostic, `*ress` of a channel among them, the inferred variable
having no walked type until valueTypeAt answered a make. **A migration that moves
every program to a new spelling asks the new spelling every question the old one
was asked** -- the spec tests are that net when they are migrated rather than
rewritten. Probing the redesign afterwards (a position sweep of make, a sensor
pipeline of three packages written as Go writes channel code, matching Go on the
host and the board, and its 2,700 mutants) found: a lock per make SITE, eagerly,
let twenty sites hand p2.NewLock() a lock channels used -- self-deadlock for a cog
holding it while it sent; the sites take eight locks at most (chanSiteLocks) and the
host shim's _locknew hands out lock 15 again past sixteen, as the target does. The
host's program end was main returning, its threads running on over main's popped
frame, which goroutines may share now -- a crash under load; ogo_end_program _Exits
on the host. And three checker rows: `go lib.T(x)` across packages, a select
clause's `=` targets not counted as writes (so a later clause's unused `v :=` passed),
a type as its comma-ok target; and an array literal's size. The nets (6,719 probes,
5,366 mutants) flipped only programs of the old rule, to "never made", and the
main-frame rule's 54 acceptances, each a referent of main's outermost block.
**Buffered channels** (the user's call, 2026-10-09, undecided since 2026-07-25):
`make(chan T, n)` for a constant n declares n slots beside the cell
(emitMakeChan, chanInitBufCName) and the cell carries a ring -- cap, head, count,
buf -- only for an element some buffered make uses (chanBufElems), so a program
with none is the C it was. Each helper branches on ch->cap: ring put and get under
the lock (ogo_chan_bufput_/bufget_, memcpy whatever the element), a send waiting
for a slot, a receive for a value or a closed and drained ring; a select's lone
send clause stands no offer on a buffered channel and is tested in its turn
(ogo_chan_sendnow_), or a buffer with room would win every round; len and cap of
any channel (ogo_chan_len_/cap_). The standing offer's readiness check is a helper
(ogo_chan_ready_), which found the nil receive clause it read at address 0. The
fuzzer's worker channel is buffered one time in two (chanRand). Measured against Go
on the host and a P2-EDGE before the run cases were written. A sweep of the size's
SPELLINGS found the emitter folding it for itself (foldConstInt), which read `3.0`
and `uint64(2)` as no constant and made those channels unbuffered, the first send
waiting for ever: the checker records the size it computed (chanSizes, by make's
second argument) and the emitter reads that, refusing loudly where it finds none.
**A value the checker computes is handed to the emitter, not computed again** --
two folds are two answers. An element of no bytes in Go (goZeroSized: struct{},
[0]T, a struct of such) shares one slot, `make(chan struct{}, 2147483647)` having
been a buffer of 2 GB; and probing it found two older loud faults, `ch <- [0]int{}`
(a compound literal of an array of no elements, which the target takes in no
spelling, is bound to a temporary as an argument is) and `Z{}` of a struct leading
with such a field written `{0}` (emitCompositeLit asks zeroInitC). The never-made
rule had a hole of its own: a `:=` target was noted written before its declaration,
so it marked the OUTER variable of its name, and `var done chan bool` passed
wherever a function declared a local `done`. A log queue over three cogs (dom39)
matched Go on the host and the board; of its 2,100 mutants, only the 2 GB buffer
was ours.

**A program ends as Go's does, every cog with it** (the user's call, 2026-10-07): a
debug build's panic stops every other cog and then its own (panicHaltC), and a
return from main stops every other cog (ogo_end_program, written where main returns
in a program that starts a cog -- a goroutine or a Spin2 object, whose driver may --
and nowhere else, so no other program's C changed; a program calling its own main
ends only in the run of main it began with, ogo_main_top). Before, the panicking cog
stopped alone and main ran on, and the target's startup code stopped main's cog
alone after it returned, so goroutines printed and drove their pins on -- where
specs.go promised them stopped and the host's exit stopped them; mutants run on the
board showed it, a deferred println after main's return. A stopped cog releases its
pins and holds its smart pins in reset, measured on a P2-EDGE (a goroutine's 625
kHz smart pin, 6250 edges in 10 ms, none after its panic), so an ended program
leaves the pins as a reset does. TestOnBoardProgramEnd reads on past the expected
text and asks that nothing followed -- the table's board check stops at it, and
passed the old behaviour. `ogo test` stops reading at a panic's line (loadp2.Capture)
and says so, where a goroutine's panic left the runner waiting out two minutes.
The ORDER inside the panic was the next fault (2026-10-11, found running domain
mutants on the board): the message, then a ten-millisecond drain, then the others
stopped -- so they ran on for the drain, main printing three more lines on the board,
and on the host a main returning in that time `_Exit(0)`ed before the panicking
thread aborted: status 0, the panic in the middle of the output, which a test
reading the status takes for a clean run. The others are stopped right after the
message now, the drain kept before the cog stops ITSELF (a stopped cog resets its
smart pins, the line's among them) and dropped on the host; not before the message,
the target's stdio taking a lock (__lockio) a stopped cog could hold. And
ogo_panicking, set first thing in every panic (ogo_panic, ogo_panic_begin ahead of a
value's or an assertion's message), makes main's return wait for a panic under way
on another cog (ogo_end_program), which ends the program itself as Go's does once
begun. **A change to how a program ends is asked which cog wins a race between the
end and a panic.**
