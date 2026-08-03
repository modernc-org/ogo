// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package octogo implements the mechanism that the 'ogo' CLI command in
// modernc.org/ogo uses.
//
// # Static Checks & Semantic Analysis Overview ====
//
// This document outlines the pipeline for static type checking and semantic
// analysis in the OctoGo compiler. The concepts define the semantics of the
// outcomes, not necessarily a particular implementation.
//
// To maximize throughput and avoid mutex contention, the analysis is split
// into alternating, possibly parallel and strictly serial phases. This
// architecture leverages AST iterators and ensures that the heavily
// constrained, zero-allocation semantics of the Parallax Propeller 2 (P2) are
// statically verified before code emitting.
//
// # Pre-requisite: Phase 0 - Dependency Resolution
//
// Before semantic analysis begins, the compiler performs the equivalent of
// parsing the import declarations of all files to construct a package
// dependency graph. This graph is topologically sorted. Packages are analyzed
// bottom-up, ensuring that a package's imported dependencies have completely
// finished Phases 1-5 before the current package begins Phase 3.
//
// # Phase 1: Local Scope Population (Parallel)
//
// Each input File in the current package is processed in parallel goroutines.
//
// Action: We walk the AST to extract all top-level declarations (TLDs).
//
// Scoping: Import qualifiers are inserted directly into their respective
// File.Scope. To avoid synchronization locks across goroutines, other TLDs
// (funcs, vars, consts, types) are inserted into a temporary, private scope
// map: File.tld.
//
// Validation: File-local redeclarations within the import block or the tld map
// are immediately reported as errors.
//
// # Phase 2: Package Scope Merging (Serial)
//
// Phase 2 is strictly serial. All File objects from Phase 1 are processed in
// the order their respective filenames appeared in the build context.
//
// Action: We merge all declared names from every File.tld into a unified
// Package.Scope.
//
// Hierarchy: Package.Scope is set as the direct parent of each File.Scope. The
// temporary File.tld maps are discarded.
//
// Validation: Top-level redeclarations resulting from cross-file merging are
// reported.
//
// Names in File.Scope (imports) are verified to ensure they do not shadow or
// clash with names in Package.Scope.
//
// # Phase 3: Top-Level Type & Constant Evaluation (Serial)
//
// Processed serially to ensure deterministic evaluation order. We attempt to
// establish types, constant values, and initializer expressions for all TLDs.
//
// Type Resolution: Custom type declarations (structs, interfaces, channels)
// are resolved first. Invalid recursive struct definitions (which would break
// OctoGo's static memory layout) are detected and reported.
//
// Dependency Gates: We use a [gate] state machine embedded in declarations to
// detect invalid type checking dependencies/initialization cycles among types,
// variables and constants.
//
//   - none: Unvisited.
//   - opened: Currently resolving (If encountered, an invalid cycle exists).
//   - closed: Fully resolved or determined invalid.
//
// Shallow Function Checks: Functions and methods are evaluated for their
// signatures only (parameter and result types).
//
// Annotation: Because function bodies are skipped, we cannot fully detect
// variables initialized by functions that reference other variables. TLDs are
// annotated with a list of functions/methods they invoke.
//
// State Lock: After Phase 3, TLD signatures and constants are immutable.
//
// # Phase 4: Body Checking & Hardware Constraints (Parallel)
//
// With all package-level signatures locked, function and method bodies are
// checked in parallel.
//
// Type Checking: Local variables, assignments, and expressions are fully
// type-checked.
//
// OctoGo Hardware Semantics: The zero-allocation model is strictly enforced
// here:
//
// Closures: Function literals are verified to ensure they do not capture their
// surrounding lexical scope.
//
// Defers: defer statements are verified to ensure they do not appear inside
// for loops or unbounded control flow blocks.
//
// Interfaces: an assignment into an interface variable is checked for the one
// thing the representation cannot express -- a data pointer into storage that does
// not outlive the interface value. Which concrete type it holds is not a
// constraint: the vtable answers dispatch, and proving the type is an
// optimization (see the interfaces section below).
//
// Annotation: Function and method bodies are annotated with a list of the TLDs
// (excluding imports) they mention or mutate.
//
// # Phase 5: Deep Initialization Cycle Detection (Serial)
//
// Processed serially across all package files.
//
// Action: We combine the annotations from Phase 3 (TLDs -> Functions) and
// Phase 4 (Functions -> TLDs) to construct a complete initialization
// dependency graph.
//
// Validation: A graph traversal is performed to detect and report any deep
// initialization cycles (e.g., var A = foo(), where foo() references var B,
// and var B = A).
//
// # The Result
//
// After completing Phases 1 through 5 for the main package and its transitive
// dependencies (without errors), the compiler has successfully established all
// types, constant values, variable initializations, and method scopes. The AST
// is now guaranteed to be semantically valid OctoGo code that adheres to the
// Propeller 2 hardware constraints, ready to be passed to the escape analysis,
// the WPO pass and the C emitter.
//
// # Escape & Lifetime Analysis (Static Guarantees)
//
// Status: largely implemented. A reference to this frame's storage -- the address
// of a local, a slice over a local array or literal, or a variable holding either
// in a field -- is refused at every sink that would outlive it: a return, a store
// into a package variable, a go argument and a channel send. Each is checked where
// the storage is known, which for the slice-backed forms is the emitter.
//
// The summaries below are implemented for both leaking sinks: a parameter is
// marked when the callee lets it reach another cog or stores it where it outlives
// every frame, and the mark is closed over the call graph, so a leak two or three
// calls away is reported at the call that chose the storage.
//
// The RESULT half is implemented too: a function that returns what it was given
// hands the argument's provenance back out, summarised per parameter as "a result
// derives from this one" and closed over the same call graph. It is consulted by
// the shared provenance predicate rather than by each sink, so `return id(&x)`,
// `g = id(&x)`, `go f(id(&x))` and `ch <- id(&x)` are all refused by one rule.
//
// A call through a function VALUE is judged by the function the variable was given:
// `f := keep`, `var f Fn = keep` and a later `f = other` each bind the variable, and
// anything else clears the binding rather than leaving a stale one. A method is
// resolved for the result half the way the call itself resolves it, so
// `return r.id(&x)` is refused as the plain-function shape is.
//
// A reference bound to a local first is not a hole either: `q := id(&x); return q`
// is refused, the local carrying the same holder mark a struct field and a slice
// backing carry.
//
// What is left is where the callee cannot be named at all, and each of these
// compiles today with a dangling reference:
//
//   - a function value held in a struct FIELD, `b.run(&x)` -- the binder tracks
//     variables, and a field is not one;
//   - a function value arriving as a PARAMETER, `call(keep, &x)` -- which function
//     it holds is the caller's fact, so judging it needs the summary to travel with
//     the argument rather than with the name;
//   - a method result reached through another call, `return mid(&x)` where mid
//     returns `r.id(p)` -- the seeds that build the result graph resolve plain names
//     only, so the edge into the method is never recorded.
//
// All three want the same thing the first two increments wanted: a callee identity
// where the call site has only a value. That is the shape of the work, and it is
// also what an interface dispatch will need, which makes it worth doing once.
//
// Purpose. On a target with no heap and no GC, every reference -- a pointer, a
// slice header, or a zero-copy string view -- borrows storage owned by some frame.
// The hardware offers no place to promote an escaping value to, so the single
// invariant the pass enforces is: a reference must never be stored where the store
// outlives the referent's storage. Where Go's escape analysis has a fallback --
// move the referent to the heap -- OctoGo has none, so what Go silently
// heap-promotes, OctoGo reports as a compile-time error.
//
// The lattice. Each value carries an escape level:
//
//   - does-not-escape: every use is bounded by the frame that created the
//     referent. The default and the common case.
//   - escapes-to-caller: the value flows out through a result, so its referent
//     must live at least as long as the caller's frame. Only a value whose storage
//     already does -- a parameter's target, a global -- may take this level; the
//     address of a local may not.
//   - escapes-forbidden: the value would have to outlive every frame that could
//     own its storage. No heap exists to satisfy this, so it is a static error.
//
// Passing a reference DOWN is safe; leaking it UP or SIDEWAYS is not. Taking the
// address of a local and passing it as a pointer argument -- x := ...; f(&x) -- is
// does-not-escape, because the callee's execution is strictly nested inside the
// caller's frame: the referent outlives every use f makes of it during the call.
// This downward-borrow pattern (a mutable reference handed to a callee) stays
// legal and must stay legal -- forbidding it would gut the language. The address
// escapes only when the CALLEE leaks it past the call: stores it into a global,
// returns it, sends it on a channel that outlives the call, or captures it in a
// go/deferred context that outlives the call. This is precisely why the analysis
// is interprocedural rather than a local check.
//
// Interprocedural summaries. Each function is summarized once, per reference-typed
// parameter, with a single fact: does this parameter leak beyond the call? A
// parameter leaks if the body stores it into anything that outlives the call,
// returns it, or forwards it to another parameter that (transitively) leaks. Given
// the summaries a call site is cheap: f(&local) is legal iff the matching
// parameter does not leak; otherwise the escape propagates to &local and the
// local's address is escapes-forbidden. Summaries are computed bottom-up over the
// call graph (already built for Phase 5 init-cycle detection and for WPO); a
// strongly connected component -- mutual recursion -- is solved to a fixed point
// seeded leak=false.
//
// Reference sources (what creates a borrow): the address-of operator (&x); a slice
// of an array or another slice (a[:], s[i:j] -- the header borrows the backing);
// Builder.String() and every future zero-copy view (the string borrows the
// caller's []byte); a closure capture; and the argument marshalling of go and
// defer.
//
// Escape channels (how a borrow leaves a frame): a return; an assignment whose
// left-hand storage outlives the referent (a global, a field reached through a
// longer-lived pointer, or the target of a caller-supplied pointer parameter that
// itself escapes); a channel send; and capture by a go statement or by a defer
// whose execution outlives the enclosing frame.
//
// First clients. The pass is the stated precondition for the interface-strategy
// decision below: a fat-pointer (Option C) representation is sound only if the
// pointed-to data is proven to stay in scope, and even monomorphization (Option B)
// needs lifetime facts once an interface value holds a pointer. Its first concrete
// duty is Builder.String(): the returned view must be proven not to outlive its
// backing []byte -- a guarantee nothing checks today.
//
// Placement. The pass runs after the semantic checks (Phases 1-5), over the same
// call graph WPO uses, and before or as the opening sub-pass of WPO: lifetime
// facts are an input to devirtualization, not an output of it.
//
// # Interfaces: Representation and Dispatch ====
//
// DECIDED 2026-08-03, after the escape analysis was completed: an interface value
// is a FAT POINTER -- a data pointer beside a pointer to a statically emitted
// vtable -- and devirtualization is an optimization applied on top of it, not the
// rule that makes it work. This section replaces an earlier design that erased
// interfaces entirely by requiring each interface variable to hold one concrete
// type ("strict monomorphization"); the reasoning for the change is below, because
// it decides what the implementation may and may not assume.
//
// Three strategies were on the table. Bounded tagged unions compute the closed set
// of concrete types a variable can hold and emit a tag plus a union: it needs the
// whole-program analysis to SUCCEED, wastes padding sized to the largest member in
// a machine with 2 KB of Cog RAM, and brings the lifetime question straight back
// for any pointer member. Strict monomorphization erases interfaces by forbidding a
// variable to hold more than one concrete type: it costs nothing at run time and
// forbids the canonical use -- a heterogeneous collection -- even where the
// compiler can prove the set exactly. Static vtables cost two words and an indirect
// call, and express everything.
//
// The governing rule is that what the compiler cannot PROVE safe may be rejected,
// and that the set of handled cases may grow over time. That rule is what rules out
// monomorphization as a language rule rather than as an optimization: it rejects
// programs whose concrete types ARE provable, because its representation cannot
// hold two of them, and growing past that is not an increment but a change of
// representation. Deriving an interface's dynamic type everywhere is not achievable
// in general, which is precisely why the representation must not depend on the
// analysis succeeding.
//
// So rejection is spent where proof genuinely fails, and that is LIFETIME, not
// dispatch. An interface value holding a pointer into storage that does not outlive
// it is the one thing this target cannot express and has nowhere to promote to; the
// escape analysis above is what answers it, and an interface value carrying a
// pointer is a reference like any other -- the shared provenance predicate has to
// see it.
//
// # Representation
//
// An interface type I becomes a two-word struct: the data pointer, and a pointer to
// a vtable holding one function pointer per method of I, in a fixed order. For each
// concrete type T assigned to an I, one static vtable is emitted, its slots filled
// with the C names the method emitter already mints (T_M). Both ingredients exist
// today: per-signature function-pointer typedefs, and a per-type method namespace.
// A vtable naming those typedefs orders itself, the typedef section being sorted by
// dependency.
//
// A method call through an interface is an indirect call: i.vt->m(i.data, args...).
// The receiver reaches the method as the pointer it was stored as, so a value
// receiver takes a copy at the point the value entered the interface, and a pointer
// receiver reaches the caller's storage -- which is the distinction the deferred and
// go paths already draw, and the reason the lifetime question is about the data
// pointer rather than about the call.
//
// # Devirtualization
//
// Where the concrete type behind an interface value is provable at a call site, the
// indirect call is replaced by the direct one. This is the monomorphization idea
// applied per call site rather than as a language rule: it makes the common case on
// this target -- one implementation, chosen at build time -- cost exactly what a
// direct call costs, and it can be strengthened over time without any program
// changing meaning. A site it cannot prove keeps the indirect call and stays
// correct. Nothing is rejected for failing to devirtualize.
//
// The analysis it needs is the one the escape summaries already want: a callee
// identity where the call site has only a value. Three shapes are open there today
// -- a function value in a struct field, one arriving as a parameter, and a method
// result reached through another call -- and they are the same question, so they are
// worth answering once for both.
//
// # What the checker needs first, and it is the bulk of the work
//
// None of the three strategies is the expensive part. The checker has no notion of
// an interface type, a method set, or whether a concrete type implements an
// interface, and every strategy needs all three. That work is the same whichever
// representation is chosen, which is why the choice was not urgent and the checker
// work is.
//
// # Deferred, deliberately
//
// Type switches and type assertions are reachable under this representation -- a
// type id in the vtable answers both -- and are not part of the first increment.
// Variadic parameters are a separate question that the earlier design folded in
// here; they are not an interface feature and do not belong in this section.
package octogo
