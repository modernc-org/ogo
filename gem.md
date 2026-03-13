# OctoGo

We are designing the OctoGo programming language and implementing the OctoGo compuler. OctoGo ia s special puprose, Go-inspired language targetting the Parallax Propeller 2 microcontroller.

* The compiler is developed in Go.
* OctoGo grammar is strictly LL(1) and is available in the octogo.ebnf file attached to the Gem knowledge for your reference. The grammar accepts more that what the language specification allows. We defer the narrowing later to the semantic checks.
* We generate the lexer and parser for OctoGo using modernc.org/egg  - a EBNF Expression Generator. It takes  octogo.ebnf and outputs parser.go.
* OctoGo language specification is written as a package level Go documentation comment and is available in the octogo.go file attached to the Gem knowledge for your reference.
* OctoSmith is a fuzzer we are developing. Its design document as attached to the Gem knowledge for your reference.

# modernc.org/egg  EBNF grammar

Expression grammars are a slightly modified version of the EBNF used by the Go language specification:

```
Syntax      = { Production } .
Production  = production_name '=' [ Expression ] '.' .
Expression  = Term { '|' Term } .
Term        = Factor { Factor } .
Factor      = production_name | token | Group | Option | Repetition .
Group       = '(' Expression ')' .
Option      = '[' Expression ']' .
Repetition  = '{' Expression '}' .
```

Productions are expressions constructed from terms and the following operators, in increasing precedence:

```
|   alternation
()  grouping
[]  option (0 or 1 times)
{}  repetition (0 to n times)
```

* Lowercase production names are used to identify lexical (terminal) tokens. Non-terminals are in CamelCase.
* Interpreted strings literals, like "foo", are tokens and will match literally, in this example the rune sequence "foo".
* Raw string literals, like `[0-9]`, are tokens and are interpreted as regexps, in this example matching a character class '0'-'9'. Repetitions, like in `re{min,max}` are not supported.
* Rune literals, like 'a', are tokens and will match literally, in this example the rune 0x61.
* Comments in the expression grammar start with the '#' character and stop at the end of the line.
* Lexical production names starting with '_' are reserved.



# OctoGo Compiler Architecture & Design Decisions

**Target Hardware:** Parallax Propeller 2 (P2). 8 Cogs, 512KB shared Hub RAM, 512 longs (2KB) local Cog RAM per cog.

**Execution Model:** Generating C code for the flexcc compiler (part if the github.com/totalspectrum/flexprop project).

**Language Semantics (Go Subset):**

* **Memory:** Zero-allocation/No Garbage Collection. Strict hardware scoping (Hub RAM vs. Cog RAM). Slices are non-escaping stack views over fixed arrays.  
* **Concurrency:** Strict 1:1 mapping of go statements to the 8 physical P2 cogs. Exceeding 8 cogs results in a compile-time error or runtime panic.  
* **Channels:** Maps directly to P2 hardware locks and Hub RAM buffers for synchronous, lock-step communication without a software scheduler.  
* **Control Flow:** Includes switch (values only) and select (mapped to non-blocking hardware polling/spinlocks via WAIT instructions).

**Frontend Architecture:**

* **Parser:** Generated via modernc.org/egg. Grammar is strictly LL(1) via aggressive left-factoring of assignments, function calls (Postfix), and channel operations (CommOp).  
* **AST Raw Representation:** Zero-pointer, cache-local flat \[\]int32 slice.  
* **AST Traversal:** Uses Go 1.23+ iterators (func(yield func(node) bool)) to abstract the \[-SymbolID, Size, Children...\] encoding. The node struct cleanly separates Non-Terminals (sym \!= 0\) from Terminals (tok index).

**Compiler Pipeline:**

1. Lex/Parse (egg) \-\> Flat \[\]int32 AST.  
2. AST Iterator Walk \-\> Populate Symbol Table.  
3. AST Iterator Walk \-\> Whole Program Optimization Pass devirtualizes and removes interfaces. Interface values do not exist at run-time.
4. AST Iterator Walk \-\> Emit C code.

**Design Advantages of using the C backend:**

* **Delegation of Hardware Quirks:** flexprop already understands the P2's unique pipeline, hub RAM bottlenecking, and instruction timing. OctoGo gets these optimizations for free.  
* **Rapid Iteration:** Emitting C is vastly easier to debug than raw machine code or assembly. If the output is wrong, human-readable C is available for inspection.  
* **Minimal PASM Footprint:** We only need to write PASM for the octogo\_rt runtime package—specifically for channel synchronization (hardware locks) and goroutine bootstrapping (Cog initialization).

**Mapping Language Semantics to C:**

* **go func()**: Transpiles to a block that allocates a fixed-size stack and calls a flexprop threading function (e.g., \_cogstart\_C). Strict 8-cog limits are enforced by the octogo\_rt runtime; if \_cogstart\_C fails, the runtime panics.  
* **Channels (\<-)**: Translates into synchronous, lock-step C function calls (e.g., \_\_octogo\_chan\_send, \_\_octogo\_chan\_recv) backed by P2 hardware locks inside octogo\_rt.  
* **Select Statements (select)**: Translates into an infinite while(1) polling loop checking non-blocking C runtime functions (\_\_octogo\_chan\_try\_recv), utilizing flexprop's \_waitx or similar yield instructions to prevent bus starvation.

# AST Nodes and Iterators

We defined a simple node type and an iterator:

```
type node struct {
    ast []int32       // Valid if .sym != 0
    sym import_Symbol // Valid if != 0
    tok int32         // Valid if sym == 0
}

func iterator(ast []int32) func(yield func(node) bool) {
    return func(yield func(node) bool) {
        for len(ast) != 0 {
            switch v := ast[0]; {
            case v < 0:
                // Non-Terminal: [-SymbolID, Size, Children...]
                n := ast[1]
                if !yield(node{ast: ast[2 : 2+n], sym: import_Symbol(-v)}) {
                    return
                }
                ast = ast[2+n:] // Advance past the node
            default:
                // Terminal: Token Index
                if !yield(node{tok: v}) {
                    return
                }
                ast = ast[1:] // Advance past the token
            }
        }
    }
}

```

We can then walk productions like `Expression = Term { '|' Term } .` simply as:

```
// Expression = Term { '|' Term } .
func (m *importer) expression(n node) {
    for n := range iterator(n.ast) {
        switch n.sym {
        case import_Term:
            m.term(n)
        case 0:
            m.w(" |")
        }
    }
}
```

Just an approach example. Note: Lookahead within the AST is possible via `iter.Pull`.

#  Future Vision: Concurrent Blinky

**Concept:** Demonstrating the OctoGo-to-C transpilation pipeline through a canonical "Concurrent Blinky" program, illustrating how Go-style concurrency primitives map to the Parallax Propeller 2 (P2) hardware via flexprop.

**Language Semantics Illustrated:**

* **Goroutines (go blinkWorker()):** Transpiles to a scoped C block that requests a stack from the octogo\_rt runtime and invokes \_cogstart\_C. It explicitly enforces the P2's 8-cog hardware limit.  
* **Channels (chan int):** Act as thread-safe conduits between physical Cogs. In the transpiled C, they map to octogo\_chan\_t structures managed by the runtime.  
* **Hardware Interaction (p2.PinHigh):** OctoGo handles hardware I/O by treating the p2 package as a zero-cost abstraction namespace over flexprop's highly optimized built-in C macros (e.g., \_pinh, \_waitms).  
* **Synchronization:** Channel sends (rateChan \<- 100\) and receives (delay \= \<-rateChan) are transpiled into \_\_octogo\_chan\_send and \_\_octogo\_chan\_recv C functions. Under the hood, these utilize the P2's native hardware locks (0-15) to ensure atomic, lock-step data transfer between Hub RAM and Cog RAM without requiring a software-level thread scheduler.

**Compiler Implications:**

The transpiler remains remarkably simple. It does not need to understand P2 instruction timing or register allocation. It simply translates the LL(1) AST into semantically equivalent C loops, variable assignments, and runtime function calls, leaving the heavy lifting of P2 binary generation entirely to flexprop.

# Compiler Architecture & Design Decisions (Package Topology & Simplification)

**The Ideal MVP Shape:**

* **Filesystem:** Adheres strictly to the "one directory = one package" convention to simplify transpilation and emulate Go's clean project structure.  
* **Imports:** Relies on local file resolution without internet dependency management or a go.mod equivalent.  
* **Standard Library:** Import paths without dots (e.g., import "p2") resolve to the built-in standard library.  
* **CLI Commands:** The octogo compile command generates the intermediate C code and headers, while octogo build acts as a wrapper that automatically invokes flexprop to generate the final Propeller 2 binary. However, with WPO octogo might be no more possible.

**Why No PackageClause?**

* **Grammar Simplification:** The language intentionally omits the Go-style package keyword entirely. Parsing begins directly with imports and top-level declarations (SourceFile = { ImportDecl } { TopLevelDecl } .).
* **Implicit Naming:** A package's namespace is implicitly inferred from the base name of its directory or import path, pushing the naming burden away from the library author and onto the filesystem structure.  
* **Collision Resolution:** If a directory's base name is not a valid identifier, the consumer must resolve it explicitly using import aliasing (e.g., import alias "invalid-name").  
* **Unified Translation Units:** Because there is no package declaration to logically separate scopes within a folder, all .octo files within a single directory are merged into a single AST. This cleanly maps to emitting one cohesive C translation unit per directory. Note: Probably no more possible with WPO.
* **Testing:** Testing code is in `*_test.ogo` files.

# Compiler Architecture & Design Decisions (Smart Pin Abstraction)

**Concept:** Leveraging the existing, highly optimized C built-ins provided by the flexprop (flexcc) compiler to expose the Parallax Propeller 2 (P2) Smart Pins in OctoGo. By wrapping these existing macros, OctoGo achieves zero-overhead hardware control without requiring custom assembly generation in the backend.

**The Wrapper Approach (p2 Standard Library Package):**

Because OctoGo transpiles directly to C, we do not need to reinvent the wheel for hardware I/O. flexprop already provides intrinsic C functions that map 1:1 with the P2's low-level hardware instructions. OctoGo's standard library will simply provide a strongly typed, Go-idiomatic wrapper around these intrinsics.

Here is how the mapping will look in the transpiler:

* **Pin Configuration:** \* OctoGo: p2.WritePinMode(pin, mode) → C: \_wrpin(pin, mode)  
  * OctoGo: p2.WritePinX(pin, xVal) → C: \_wxpin(pin, xVal)  
  * OctoGo: p2.WritePinY(pin, yVal) → C: \_wypin(pin, yVal)  
* **Pin Data & Acknowledgment:**  
  * OctoGo: val := p2.ReadPin(pin) → C: val \= \_rdpin(pin)  
  * OctoGo: p2.AckPin(pin) → C: \_akpin(pin)  
* **Basic I/O & State:**  
  * OctoGo: p2.PinHigh(pin) → C: \_pinh(pin)  
  * OctoGo: p2.PinLow(pin) → C: \_pinl(pin)  
  * OctoGo: state := p2.PinIn(pin) → C: state \= \_pinr(pin)

**Integration with the select Statement:**

As discussed, Smart Pins raise their IN signal when an autonomous event completes (like a timer firing, an ADC conversion finishing, or a UART byte arriving).

In OctoGo, a select statement waiting on a Smart Pin timer will transpile into the standard C while(1) polling loop. Instead of checking a channel lock, it will simply evaluate \_pinr(pin):

```
// Transpiled OctoGo 'select' loop multiplexing a channel and a Smart Pin  
while(1) {  
    // 1\. Check Hardware Channel (Lock-based)  
    if (\_\_octogo\_chan\_try\_recv(rateChan, \&tempVal)) {  
        delay \= tempVal;  
        break;   
    }  
    // 2\. Check Smart Pin Timer (Zero-overhead IN state check)  
    if (\_pinr(TIMER\_PIN)) {  
        \_akpin(TIMER\_PIN); // Acknowledge to clear the IN signal  
        // Execute timer case block  
        break;  
    }      
    \_waitx(1); // Yield to prevent Hub bus starvation  
}
```

**Architectural Benefits:**

* **Simplicity:** The OctoGo compiler frontend doesn't need to know the binary encoding for WRPIN or RDPIN. It just emits the corresponding flexprop C function calls.  
* **Performance:** flexprop directly inlines these C functions into native single-cycle P2 instructions. There is absolutely no software translation layer at runtime.  
* **Extensibility:** Because Smart Pins handle everything from basic PWM to USB natively in hardware, OctoGo gets access to a massive library of hardware capabilities just by exposing a few basic p2.WritePin\* functions.

# Technical Note: "OctoSmith" – A Deterministic Fuzzer for OctoGo

## **1\. Core Philosophy and Feasibility**

Creating a CSmith-like fuzzer for OctoGo is highly feasible, and arguably easier than CSmith itself, for three primary reasons:

1. **Memory Safety:** OctoGo utilizes a strict zero-allocation model without Garbage Collection and no dynamic heap allocation. This completely eliminates the need for the fuzzer to track malloc/free lifetimes, use-after-free scenarios, or complex pointer arithmetic.  
2. **No Undefined Behavior (UB):** Unlike C, where random operations easily trigger UB (which CSmith spends 80% of its codebase avoiding), OctoGo's Go-inspired semantics are deterministic.  
3. **LL(1) Simplicity:** The grammar is aggressively left-factored and strictly LL(1). This makes generating syntactically valid code straightforward.

The absolute golden rule for OctoSmith is **No False Positives**. If the fuzzer outputs a program, that program *must* be semantically valid OctoGo code that compiles via your pipeline and executes deterministically on the Propeller 2\.

## **2\. Architecture: Separating Structure from Semantics**

To handle the evolving nature of the language, OctoSmith should not just randomly pick rules from the EBNF. It must be a **Type-Directed Generator**.

Instead of saying "generate an expression," the fuzzer says "generate an expression of type int."

### **Phase A: The Environment (Scope Tracking)**

OctoSmith needs to maintain a runtime Env struct during generation. This environment tracks:

* Currently declared variables and their types in the active Block.  
* Available functions and their signatures.  
* Available constants.  
* The current depth of loops/blocks to ensure bounded generation.

### **Phase B: Type-Directed Generation Rules**

When the fuzzer needs to generate a Statement, it rolls a weighted random number (seeded via the CLI argument) to pick an action:

1. **Declare a Variable:** Generate a VarDecl. Pick a random type (e.g., int), generate an identifier, and recursively request an initializer expression of that exact type. Add it to Env.  
2. **Assign to a Variable:** Pick an existing variable from Env. Generate an AssignHead and request an expression matching its type.  
3. **Control Flow:** Generate an if or for statement. Request an expression of type bool for the condition. Push a new Env scope, generate a few inner statements, and pop the scope.

### **Phase C: Expression Generation**

To fulfill a request for an int expression, the fuzzer rolls the dice:

* **Base Case (30%):** Return an int\_lit.  
* **Variable (40%):** Return an identifier from Env that has type int.  
* **Computation (30%):** Generate an AddOp or MulOp. Recursively request two new int expressions to act as the left and right operands.

*To prevent infinite AST growth, you pass a depth integer down the recursive calls. Once depth hits a threshold, the probabilities shift to 100% Base Case/Variable.*

## **3\. Ensuring Determinism (The Oracle)**

For the fuzzer to be useful, we need to know if the compiler messed up the execution. We use a **Checksum Accumulator**.

1. OctoSmith generates a global variable: var checksum int \= 0  
2. Throughout the generated blocks, OctoSmith randomly inserts assignment statements that mutate this checksum using local variables. Example: checksum \= checksum ^ (local\_var\_a \+ 3\)  
3. At the end of the main function, the program simply prints or transmits the final checksum value via standard out (or a specific P2 serial pin).

Because the program contains no undefined behavior, no uninitialized variables (they default to zero values), and predictable control flow, the generated source code intrinsically defines the "correct" checksum. You compile it, run it on the P2, and verify the output matches your reference interpreter or standard Go implementation.

## **4\. Tackling the Hardware Quirks (Concurrency)**

OctoGo maps go routines strictly 1:1 to the 8 physical P2 Cogs. This is where a naive fuzzer would fail by causing a runtime panic.

**Initial Fuzzer Scope:** Leave concurrency out. Start by fuzzing the sequential language features (for, if, arithmetic, structs).

**V2 Fuzzer Scope:** \* OctoSmith must track a global cog\_count.

* It can generate a FuncDecl and invoke it via a go statement, incrementing the cog\_count.  
* If cog\_count \== 8, the fuzzer temporarily disables the go statement generation path.  
* For chan types, generate global channels. To prevent deadlocks, the fuzzer must guarantee that every \<-chan (receive) is paired with a guaranteed chan\<- (send) in a separate cog, or utilize select statements with a default case.

## **5\. Handling Language Evolution**

Because the language features are not yet complete, OctoSmith should be modular.

* Write the generator functions to directly map to your EBNF Non-Terminals (e.g., func (f \*Fuzzer) genSimpleExpr(targetType Type)).  
* When you add a new feature (like floating-point numbers, currently omitted), you simply update the Type enum in the fuzzer and add a case to the expression generator.

# Architecture Note: OctoGo Code Formatter (octogo format)

## **1\. Motivation and Philosophy**

Standard formatting tools often struggle with comment alignment and whitespace preservation because they treat the Abstract Syntax Tree (AST) and the source text as loosely coupled entities. For example, go/parser detaches comments into a separate CommentMap, requiring complex positional arithmetic to reconstruct the source code.

octogo format takes a **Full-Fidelity Token** approach. The parser generates a contiguous stream of Token objects where every lexeme inherently "owns" its preceding whitespace and comments.

## **2\. The Token API**

The foundation of the formatter is the Token API, which guarantees that no source text is lost during parsing.

* **Src() / SrcBytes()**: Returns the exact semantic source form of the token (e.g., func, foo, {).  
* **Sep()**: Returns the "Separator"—the exact, unmodified string of whitespace, line breaks, and comments that immediately *precedes* the token.  
* **Next() / Prev()**: Allows linear traversal of the token stream independently of the AST.

By definition, printing the sequence of Token.Sep() \+ Token.Src() from the start of the file to the final EOF token will perfectly reconstruct the original source file, byte-for-byte.

## **3\. Formatting Pipeline: The "Mutate-in-Place" Strategy**

Because of the flat \[\]int32 AST and Go 1.23+ iterator design, octogo format does not need to allocate a completely new AST or string buffer during the formatting phase. Instead, it operates via an in-place mutation pipeline:

### **Phase 1: AST-Directed Structural Formatting**

The formatter walks the AST using the standard iterator (func(yield func(node) bool)). When it encounters structural nodes (e.g., Block, FuncDecl, VarDecl), it tracks the current indentation level.

As the iterator yields terminal nodes (Token indices), the formatter looks up the corresponding Token and updates its separator using Token.SetSep().

* *Example:* If a } token closes a block, the formatter updates its Sep() to ensure it ends with \\n followed by exactly (indent \- 1\) tabs.

### **Phase 2: Token-Stream Micro-Formatting**

Certain formatting rules do not require full AST context and can be applied by sliding a window over the linear token stream using Token.Next():

* **Operator Spacing:** Ensuring spaces around binary operators (e.g., changing a+b to a \+ b). The formatter finds the \+ token and calls SetSep(" ").  
* **Punctuation:** Ensuring no space before a comma, and exactly one space after.  
* **Blank Line Normalization:** Collapsing multiple consecutive blank lines inside a Token.Sep() into a single blank line.

### **Phase 3: Linear Emission**

Once the AST walk and token stream adjustments are complete, the actual code generation is trivial. The formatter simply iterates from the first token to the EOF token:

```
for t := firstToken; t.IsValid(); t \= t.Next() {  
    out.WriteString(t.Sep())  
    out.WriteString(t.Src())  
}
```

## **4\. Handling Comments (Sep Parsing)**

Because Token.Sep() contains both whitespace *and* comments, SetSep() must be context-aware so it doesn't accidentally erase a user's documentation.

When formatting a token that contains comments in its Sep():

1. The formatter parses the Sep() string to isolate the comment blocks (// ... or /\* ... \*/).  
2. It strips out the user's arbitrary spaces/tabs.  
3. It rebuilds the Sep() string by injecting the standardized structural indentation, followed by the preserved comment text, followed by the standardized spacing before the Src().

## **5\. Advantages for OctoGo**

* **Cache Locality & Speed:** Since tokens are just structs modified in place, and the AST is a flat \[\]int32 slice, the entire formatting pass is highly cache-local and runs with zero-to-minimal allocations.  
* **Ease of Refactoring:** Future AST rewriting tools (like an octogo fix or a language server) can simply move sub-trees in the \[\]int32 array. The attached comments will automatically travel with the tokens.

