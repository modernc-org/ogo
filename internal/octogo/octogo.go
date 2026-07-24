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
// Interfaces: If using the Monomorphization WPO strategy, interface
// assignments are checked to ensure a single concrete type per variable
// lifetime.
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
// Status: design intent, NOT YET IMPLEMENTED (2026-07-24). No escape analysis
// exists today. The zero-heap model is currently enforced only at allocation
// sites (make/new/slice literals reserve a compile-time-sized backing; a
// value-recursive, infinite-size type is refused) plus a few syntactic lifetime
// guards (a closure may not capture its surrounding scope; a defer may not sit in
// a loop). Nothing tracks whether a reference outlives its referent, so return
// &local, return localArray[:] and global = &local all compile silently into
// dangling references. This section specifies the pass that closes that gap.
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
// # Whole Program Optimization (WPO) & Devirtualization ====
//
// The WPO phase runs after the AST has passed all static and semantic checks and
// the escape analysis above (whose lifetime facts it consumes). Its primary
// objective is to enforce OctoGo's zero-allocation model by completely eliminating
// interface types, type assertions, and type switches before emitting C code.
//
// To achieve this uniformly, the WPO treats all polymorphic and variadic
// function calls as accepting a "Type Vector" or "Conceptual Tuple.
//
// # Phase 1: Global Call Graph & Type Vector Extraction
//
// Before specializing code, we trace how concrete types flow into interface
// variables and variadic parameters.
//
//   - Entry Points: The analysis begins at main(), accumulated init() blocks,
//     and any function invoked via a go statement.
//   - The Tuple Concept: Every function invocation is conceptually treated as
//     passing a Type Vector.
//
// Examples
//
//	foo(42) $\rightarrow$ [int]
//	foo(42, "x") $\rightarrow$ [int, string]
//	Printf("%v %v", 42, true) $\rightarrow$ [string, int, bool]
//
// ---
//   - The Monomorphization Rule: If a single lexical interface variable (e.g.,
//     an element in an array) is assigned different concrete types across
//     dynamic control flow branches, the WPO throws a strict compile-time error.
//     This guarantees 100% compile-time devirtualization.
//
// # Phase 2: Signature Specialization
//
// Using the Type Vectors extracted in Phase 1, we clone and specialize the AST
// nodes for functions accepting any or ...any.
//
//   - Cloning: If func Printf(s string, args ...any) is called with [string,
//     int, bool], the AST is cloned to create Printf_int_bool.
//   - Parameter Flattening: The ...any slice is erased. The signature is
//     rewritten to accept discrete, statically typed parameters based on the
//     vector.
//
// Example
//
//	func Printf_int_bool(s string, _0 int, _1 bool)
//
// ---
//   - Call Site Patching: The original generic call sites are updated to point
//     directly to these newly generated, concretely typed signatures.
//
// # Phase 3: Variadic Loop Rewriting (The Runtime Switch)
//
// Because the ...any slice was flattened into discrete parameters, any for i,
// arg := range args loops inside the specialized function must be rewritten to
// access the conceptual tuple safely at runtime.
//
//   - Length Resolution: The len(args) is statically known for this
//     specialization (e.g., 2). The range loop is rewritten as a standard
//     bounded integer loop: for i := 0; i < 2; i++.
//   - Index Dispatching: The slice index access (args[i]) is replaced by a
//     compiler-generated switch statement on i.
//   - Body Duplication: To satisfy C's static typing, the AST nodes
//     representing the body of the loop are duplicated inside each case, binding
//     to the specific concrete parameter.Go
//
// Example
//
//	// Conceptual WPO AST transformation for Printf_int_bool:
//	for i := 0; i < 2; i++ {
//		switch i {
//		case 0:
//			// original loop body using _0 (int)
//		case 1:
//			// original loop body using _1 (bool)
//		}
//	}
//
// # Phase 4: Type Switch & Assertion Erasure
//
// With functions specialized and interfaces replaced by concrete types,
// dynamic type checks are statically resolved and erased.
//
//   - Type Assertions (val.(T)): Since val is now a known concrete type, the
//     compiler statically evaluates the assertion. If it matches, the assertion
//     node is replaced by the underlying value. If it fails, it is replaced by a
//     compiler-injected panic (if reachable).
//   - Type Switches (switch v := i.(type)): The compiler identifies the single
//     case matching the newly specialized concrete type. The entire switch AST
//     node is discarded and replaced only by the statements of the matching
//     case.
//
// # Phase 5: Devirtualization & Dead Code Elimination (DCE)
//
// The final cleanup stage before handing the AST to the backend.
//
//   - Direct Method Dispatch: Interface method calls (e.g., i.DoWork()) are
//     rewritten as direct, static function calls to the concrete type's method
//     (e.g., ConcreteType_DoWork(&i)). This ensures no VTables exist at runtime.
//   - Pruning: The original generic functions containing any or ...any are
//     pruned from the AST. Any unused methods or interface definitions are
//     stripped to conserve Propeller 2 ROM space.
package octogo
