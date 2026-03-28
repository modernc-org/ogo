// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package octogo implements the mechanism that the CLI in modernc.org/ogo
// uses.
//
// # Static Checks & Semantic Analysis Overview
//
// This document outlines the pipeline for static type checking and semantic
// analysis in the OctoGo compiler. The concepts define the semantics of the
// outcomes, not necessarily a particular implementation.
//
// To maximize throughput and avoid mutex contention, the analysis is split
// into alternating, possibly parallel and strictly serial phases. This
// architecture leverages Go 1.23+ AST iterators and ensures that the heavily
// constrained, zero-allocation semantics of the Parallax Propeller 2 (P2) are
// statically verified before emitting C code for the flexcc backend.
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
// possibly checked in parallel.
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
// Propeller 2 hardware constraints, ready to be passed to the WPO pass and C
// emitter.
package octogo // import "modernc.org/ogo/internal/ogo"
