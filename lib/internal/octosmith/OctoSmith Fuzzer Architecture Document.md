# OctoSmith Architecture & Design Document

## **1\. Core Concept: The Oracle Fuzzer**

Because OctoGo is a custom language without a mature reference compiler (like GCC or Clang) to perform differential testing, OctoSmith operates as an **Oracle Fuzzer** (or Executable Generator).

Instead of generating a random program and hoping it compiles, OctoSmith *interprets the program at the exact same time it generates the AST*. By doing this, the fuzzer calculates the final state of the program deterministically. It then emits a main function that asserts the compiled Propeller 2 binary's state matches the fuzzer's generation-time state via a running checksum. If the binary fails the assertion, the OctoGo compiler/backend is guaranteed to be at fault (zero false positives).

## **2\. Separation of Concerns**

The architecture is split into two distinct domains to handle the evolving nature of the OctoGo language:

* **The Generator (gemini.go / octosmith.go):** Handles the LL(1) grammar, type-directed AST generation, and determinism (seeded RNG).  
* **The Virtual Machine (vm.go):** Tracks scope, memory, and performs mathematical evaluations during generation.

## **3\. The Virtual Machine API (To Be Implemented)**

To support the Oracle Fuzzer, the VM must implement the following interfaces. This API keeps the heavy lifting simple and Go-idiomatic, avoiding complex byte-slice serialization where possible.

Go

package octosmith

// Type represents an OctoGo data type used during fuzzing.  
type Type interface {  
	String() string  // e.g., "int", "bool"  
	IsNumeric() bool   
}

// Value represents a typed value known at generation time.  
type Value interface {  
	Type() Type      // Returns the octosmith.Type  
	Literal() string // Returns the OctoGo literal string (e.g., "42", "true")  
	Value() any      // Returns the underlying Go value (e.g., int64, bool)  
}

// Memory abstraction for the fuzzer to track state and lexical scope.  
type Memory interface {  
	PushScope()  
	PopScope()  
	Store(name string, val Value)  
	Load(name string) Value  
}

// Machine evaluates operations to compute the checksum during generation.  
type Machine interface {  
	// Eval performs a language operation.  
	// op is the OctoGo operator string (e.g., "int\_lit", "+", "-", "==", "^")  
	// If the operation is invalid (e.g., overflow, division by zero), it returns an error,  
	// allowing the fuzzer to discard the attempt and fallback to a safe literal.  
	Eval(op string, operands ...Value) (Value, error)  
}

## **4\. Execution Flow (GenerateProgram)**

1. **Initialize Checksum:** The fuzzer requests a 0 value from the VM and emits a global variable declaration (var octosmith\_checksum int \= 0).  
2. **Generate Block:** The fuzzer generates a sequence of deterministic statements (e.g., variable declarations, arithmetic mutations).  
3. **Simultaneous Evaluation:** As each statement is generated, it is passed to Machine.Eval(). The result is stored in Memory.  
4. **Final Assertion:** After generating the block, the fuzzer queries Memory.Load("octosmith\_checksum"). It emits a final if statement into the generated source code that panics if the compiled binary's checksum does not match the VM's calculated checksum.

## **5\. Roadmap & Future Complexity**

* **Phase 1 (MVP):** Strictly sequential integer arithmetic and variable assignments. No control flow.  
* **Phase 2 (Control Flow):** Introduce if and for loops. The VM will need to track "dead code" vs. "live code" branches. If a generated if condition evaluates to false in the VM, the fuzzer still generates the AST for the else block but *does not* apply its memory mutations to the VM state.  
* **Phase 3 (Concurrency):** Introduce go routines and chan communication, strictly bounded by the Propeller 2's 8-Cog limit.
