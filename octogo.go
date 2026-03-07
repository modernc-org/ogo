// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO 20260307 Keywords: +defer +?map +?range
// TODO 20260307 Operators and punctuation: +?++/--
// TODO 20260307 Numeric type: +float,float32
// TODO 20260307 Operators: +||/&&
// TODO 20260307 For statements: extend
// TODO 20260307 Return statements: ? disable naked returns

// # OctoGo Language Specification (Draft Mar 7, 2026)
//
// # Introduction
//
// This is the reference manual for the OctoGo programming language. For more
// information and other documents, see octogo.dev. (Planned, not active yet)
//
// OctoGo is a special-purpose language designed for the Parallax Propeller 2.
// It is strongly typed with no heap allocations and has explicit support for
// concurrent programming. Programs are constructed from packages, whose
// properties allow efficient management of dependencies.
//
// The syntax is compact and simple to parse, allowing for easy analysis by
// automatic tools such as integrated development environments.
//
// # Notation
//
// The syntax is specified using a variant of Extended Backus-Naur Form (EBNF):
//
//	Syntax      = { Production } ․
//	Production  = production_name '=' [ Expression ] '.' ․
//	Expression  = Term { '|' Term } ․
//	Term        = Factor { Factor } ․
//	Factor      = production_name | token | Group | Option | Repetition ․
//	Group       = '(' Expression ')' ․
//	Option      = '[' Expression ']' ․
//	Repetition  = '{' Expression '}' ․
//
// Productions are expressions constructed from terms and the following
// operators, in increasing precedence:
//
//	|   alternation
//	()  grouping
//	[]  option (0 or 1 times)
//	{}  repetition (0 to n times)
//
// Lowercase production names are used to identify lexical (terminal) tokens.
// Non-terminals are in CamelCase.
//
// Interpreted strings literals, like "foo", are tokens and will match
// literally, in this example the rune sequence "foo".
//
// Raw string literals, like `[0-9]`, are tokens and are interpreted as
// regexps, in this example matching a character class '0'-'9'. Repetitions,
// like in `re{min,max}` are not supported.
//
// Rune literals, like 'a', are tokens and will match literally, in this
// example the rune 0x61.
//
// # Source code representation
//
// Source code is Unicode text encoded in UTF-8. The text is not canonicalized,
// so a single accented code point is distinct from the same character
// constructed from combining an accent and a letter; those are treated as two
// code points. Each code point is distinct; for instance, uppercase and
// lowercase letters are different characters.
//
// # Characters, Letters, and Digits
//
// The following terms denote specific Unicode character categories:
//
//	unicode_digit  = `\p{Nd}` .
//	unicode_letter = `\pL` .
//
// The underscore character _ (U+005F) is considered a lowercase letter.
//
//	letter        = unicode_letter | "_" .
//	decimal_digit = `[0-9]` .
//	binary_digit  = "0" | "1" .
//	octal_digit   = `[0-7]` .
//	hex_digit     = `[0-9A-Fa-f]` .
//
// # Lexical elements
//
// # Comments
//
// Comments serve as program documentation. There are two forms:
//
//   - Line comments start with the character sequence // and stop at the end
//     of the line.
//   - General comments start with the character sequence /* and stop with the
//     first subsequent character sequence */.
//
// Formally:
//
//	white_space            = `/\*([^*]|\*+[^*/])*\*+/|//.*| |\t|\n|\r` .
//
// # Tokens
//
// Tokens form the vocabulary of the OctoGo language. There are four classes:
// identifiers, keywords, operators and punctuation, and literals. White space
// is ignored except as it separates tokens that would otherwise combine into a
// single token.
//
// # Semicolons
//
// The formal syntax uses semicolons ";" as terminators in a number of
// productions. Like Go, OctoGo programs may omit most of these semicolons
// using the standard insertion rules: when the input is broken into tokens, a
// semicolon is automatically inserted into the token stream immediately after
// a line's final token if that token is an identifier, a literal, a control
// flow keyword (return), or closing punctuation.
//
// # Identifiers
//
// Identifiers name program entities such as variables and types. An identifier
// is a sequence of one or more letters and digits, where the first character
// must be a letter.
//
//	identifier = letter { letter | unicode_digit } .
//
// # Keywords
//
// The following keywords are reserved and may not be used as identifiers.
// (Note: Keywords like package, defer, goto, map, and range have been
// intentionally omitted from OctoGo to simplify the grammar and runtime):
//
//	case        else        interface   switch
//	chan        for         return      type
//	const       func        select      var
//	default     go          struct      import
//	if
//
// # Operators and punctuation
//
// The following character sequences represent operators and punctuation.
// (Note: OctoGo omits operators like %, &^, ++, and --)
//
//	&    +     ==    !=    (    )
//	-    |     <     <=    [    ]
//	*    ^     >     >=    {    }
//	/    <<    =     :=    ,    ;
//	~    >>    !     <-    .    :
//
// # Integer literals
//
// An integer literal is a sequence of digits representing an integer constant.
// An optional prefix sets a non-decimal base: 0b or 0B for binary, 0o or 0O
// for octal, and 0x or 0X for hexadecimal.
//
// For readability, an underscore character _ may appear after a base prefix or
// between successive digits.
//
//	int_lit        = decimal_lit | binary_lit | octal_lit | hex_lit .
//	decimal_lit    = "0" | ( `[1-9]` ) [ [ "_" ] decimal_digits ] .
//	binary_lit     = "0" ( "b" | "B" ) [ "_" ] binary_digits .
//	octal_lit      = "0" [ "o" | "O" ] [ "_" ] octal_digits .
//	hex_lit        = "0" ( "x" | "X" ) [ "_" ] hex_digits .
//
//	decimal_digits = decimal_digit { [ "_" ] decimal_digit } .
//	binary_digits  = binary_digit { [ "_" ] binary_digit } .
//	octal_digits   = octal_digit { [ "_" ] octal_digit } .
//	hex_digits     = hex_digit { [ "_" ] hex_digit } .
//
// # Rune literals
//
// A rune literal represents a rune constant, an integer value identifying a
// Unicode code point. It is expressed as one or more characters enclosed in
// single quotes, as in 'x' or '\n'.
//
//	rune_lit         = '\'' ( `[^'\\\n\r]` | unicode_value | byte_value ) '\'' .
//	unicode_value    = little_u_value | big_u_value | escaped_char .
//	byte_value       = octal_byte_value | hex_byte_value .
//	octal_byte_value = "\\" octal_digit octal_digit octal_digit .
//	hex_byte_value   = "\\" "x" hex_digit hex_digit .
//	little_u_value   = "\\" "u" hex_digit hex_digit hex_digit hex_digit .
//	big_u_value      = "\\" "U" hex_digit hex_digit hex_digit hex_digit hex_digit hex_digit hex_digit hex_digit .
//	escaped_char     = "\\" ( "a" | "b" | "f" | "n" | "r" | "t" | "v" | "\\" | "'" | "\"" ) .
//
// # String literals
//
// A string literal represents a string constant obtained from concatenating a
// sequence of characters. There are two forms:
//
// Raw string literals are character sequences between back quotes, as in
// `foo`.
//
// Interpreted string literals are character sequences between double quotes,
// as in "bar"
//
//	string_lit             = raw_string_lit | interpreted_string_lit .
//	raw_string_lit         = '`' { `[^\x60]` } '`' .
//	interpreted_string_lit = '"' { `[^"\\\n\r]` | unicode_value | byte_value } '"' .
//
// # Constants
//
// There are boolean constants, rune constants, integer constants, and string constants.
//
//   - Constant values are represented by rune, integer, or string literals, or
//     identifiers denoting a constant.
//   - The boolean truth values are represented by the predeclared constants
//     true and false.
//   - The predeclared identifier iota denotes an integer constant.
//   - Numeric constants represent exact values of arbitrary precision and do not
//     overflow.
//
// # Variables and Memory Scoping
//
// A variable is a storage location for holding a value.
//
// The set of permissible values is determined by the variable's type.
//
// The static type (or just type) of a variable is the type given in its
// declaration.
//
// A variable's value is retrieved by referring to the variable in an
// expression; it is the most recent value assigned to the variable.
//
// If a variable has not yet been assigned a value, its value is the zero value
// for its type.
//
// # Hardware Scoping (Hub RAM vs. Cog RAM)
//
// (OctoGo Specific)
//
// OctoGo utilizes a strict zero-allocation model without Garbage Collection.
// Memory is statically allocated at compile time.
//
// Global/Package-Level Variables: Variables declared at the top level reside
// in the Propeller 2's shared Hub RAM. They are accessible by all physical
// Cogs but are subject to Hub access bottlenecks.
//
// Local Variables: Variables declared within a function or goroutine are
// scoped to the local execution stack. Depending on optimizations, these
// reside either in the limited Cog RAM (registers) for single-cycle access or
// as a reserved block in Hub RAM for the specific Cog's stack.
//
// Heap Allocation: There is no new or make built-in function for dynamic heap
// allocation in OctoGo. All memory must be deterministically bounded at
// compile time.
//
// # Types
//
// A type determines a set of values together with operations and methods
// specific to those values.
//
//	Type = [ identifier "." ] identifier
//		| "chan" Type
//		| "[" [ Expression ] "]" Type
//		| "*" Type
//		| InterfaceType
//		| StructType .
//
// # Boolean types
//
// A boolean type represents the set of Boolean truth values denoted by the
// predeclared constants true and false.
//
//   - The predeclared boolean type is bool.
//
// # Numeric types
//
// An integer type represents the set of integer values.
//
//   - The value of an n-bit integer is n bits wide and represented using two's
//     complement arithmetic.
//   - Explicit conversions are required when different numeric types are mixed in
//     an expression or assignment.
//
// (Note: OctoGo omits all floating-point and complex numeric types).
//
// # String types
//
// A string type represents the set of string values.
//
//   - A string value is a (possibly empty) sequence of bytes.
//   - The number of bytes is called the length of the string and is never
//     negative.
//   - Strings are immutable: once created, it is impossible to change the
//     contents of a string.
//   - A string's bytes can be accessed by integer indices 0 through len(s)-1.
//
// # Array types
//
// An array is a numbered sequence of elements of a single type, called the
// element type.
//
//   - The number of elements is called the length of the array and is never
//     negative.
//   - The length is part of the array's type; it must evaluate to a
//     non-negative constant representable by a value of type int.
//   - The elements can be addressed by integer indices 0 through len(a)-1.
//
// # Slice types
//
//   - A slice is a descriptor for a contiguous segment of an underlying array
//     and provides access to a numbered sequence of elements from that array.
//   - The value of an uninitialized slice is nil.
//   - A slice therefore shares storage with its array and with other slices of
//     the same array.
//
// (OctoGo Specific): In OctoGo, slices are strictly non-escaping stack views
// over fixed arrays. Because there is no GC or dynamic allocator, you cannot
// dynamically grow a slice. Slicing operations merely create a new view
// (pointer, length, capacity) pointing to pre-allocated Hub or Cog RAM.
//
// # Struct types
//
// A struct is a sequence of named elements, called fields, each of which has a
// name and a type.
//
//	StructType = "struct" "{" { FieldDecl ";" } [ FieldDecl ] "}" .
//	FieldDecl = "*" [ identifier "." ] identifier
//		| identifier [ "." identifier | { "," identifier } Type ] .
//
// Within a struct, non-blank field names must be unique.
//
// # Pointer types
//
// A pointer type denotes the set of all pointers to variables of a given type,
// called the base type of the pointer.
//
//   - The value of an uninitialized pointer is nil.
//
// # Interface types
//
// An interface type defines a type set.
//
//   - A variable of interface type can store a value of any type that is in the
//     type set of the interface.
//   - The value of an uninitialized variable of interface type is nil.
//
// (Note: OctoGo omits generic interface constraints, unions, and underlying
// type ~ operators. Interfaces strictly define method sets).
//
//	InterfaceType = "interface" "{" { MethodSpec ";" } [ MethodSpec ] "}" .
//	MethodSpec = identifier "(" [ ParameterList ] ")" [ Type | "(" ParameterList ")" ] .
//
// # Channel types
//
// A channel provides a mechanism for concurrently executing functions to
// communicate by sending and receiving values of a specified element type.
//
//   - The value of an uninitialized channel is nil.
//
// (OctoGo Specific): Channels map directly to Propeller 2 hardware locks and
// statically allocated Hub RAM buffers. They facilitate synchronous, lock-step
// communication without a software scheduler.
//
// # Blocks
//
// A block is a possibly empty sequence of declarations and statements within
// matching brace brackets.
//
//	Block = "{" { Statement ";" } [ Statement ] "}" .
//
// In addition to explicit blocks in the source code, there are implicit
// blocks:
//
//   - The universe block encompasses all OctoGo source text.
//   - The package block contains all OctoGo source text for all .ogo files
//     residing in the same directory.
//   - Each file has a file block containing all Go source text in that file.
//   - Each if, for, and switch statement is considered to be in its own
//     implicit block.
//   - Each clause in a switch or select statement acts as an implicit block.
//
// Blocks nest and influence scoping.
//
// # Declarations and Scope
//
//   - A declaration binds a non-blank identifier to a constant, type, variable,
//     or function.
//   - Every identifier in a program must be declared.
//   - No identifier may be declared twice in the same block.
//
// The grammar:
//
//	TopLevelDecl = FuncDecl | VarDecl | ConstDecl | TypeDecl .
//
// # Scope Rules
//
// OctoGo is lexically scoped using blocks:
//
//   - The scope of a predeclared identifier is the universe block.
//   - The scope of an identifier denoting a constant, type, variable, or
//     function (but not method) declared at the top level is the package
//     block.
//   - The scope of an identifier denoting a method receiver, function
//     parameter, or result variable is the function body.
//   - The scope of a constant or variable identifier declared inside a function
//     begins at the end of its specification and ends at the end of the innermost
//     containing block.
//   - An identifier declared in a block may be redeclared in an inner block.
//     While the identifier of the inner declaration is in scope, it denotes the
//     entity declared by the inner declaration (shadowing).
//
// # Exported Identifiers
//
// An identifier is exported to permit access from another package (imported
// package) if the first character of the identifier's name is a Unicode
// uppercase letter and the identifier is declared in the directory block or is
// a field/method name.
//
// # Variable Declarations
//
// A variable declaration creates one or more variables, binds corresponding
// identifiers to them, and gives each a type and an initial value.
//
// If an expression is given, the variables are initialized with that
// expression. Otherwise, each variable is initialized to its zero value.
//
// Grammar:
//
//	VarDecl = "var" ( VarSpec | "(" { VarSpec ";" } [ VarSpec ] ")" ) .
//	VarSpec = IdentifierList ( Type [ "=" Expression ] | "=" Expression ) .
//
// # Short Variable Declarations (:=)
//
// To satisfy the LL(1) constraints of the OctoGo parser, short variable
// declarations are syntactically parsed as a PostfixOp extending an
// AssignHead, but they act semantically as declarations.
//
//	Syntax mapping: { "," LhsItem } ":=" Expression
//
// It is shorthand for a regular variable declaration with initializer
// expressions but no types.
//
// # Redeclaration Rules
//
// Unlike regular variable declarations, a short variable declaration may
// redeclare variables provided they meet all of the following conditions:
//
//   - They were originally declared earlier in the same block (or the parameter
//     lists if the block is the function body).
//   - They are declared with the same type.
//   - At least one of the non-blank variables in the identifier list is new.
//
// As a consequence, redeclaration can only appear in a multi-variable short
// declaration. Redeclaration does not introduce a new variable; it merely
// assigns a new value to the original variable. Short variable declarations
// may appear only inside functions.
//
// # Constant Declarations
//
// A constant declaration binds an identifier to the value of a constant
// expression.
//
// (Note: In OctoGo's EBNF, ConstSpec binds a single identifier to a single
// expression, unlike Go which allows identifier lists. The iota mechanism
// operates sequentially across the ConstDecl group).
//
//	ConstDecl = "const" ( ConstSpec | "(" { ConstSpec ";" } [ ConstSpec ] ")" ) .
//	ConstSpec = identifier [ Type ] "=" Expression .
//
// # Type Declarations
//
// A type declaration binds an identifier, the type name, to a type. It
// supports both type definitions and alias declarations via the optional =
// operator.
//
//	TypeDecl = "type" ( TypeSpec | "(" { TypeSpec ";" } [ TypeSpec ] ")" ) .
//	TypeSpec = identifier [ "=" ] Type .
//
// # Function and Method Declarations
//
// A function declaration binds an identifier to a function. If a Receiver is
// provided, it acts as a method declaration binding the function to the
// receiver's base type.
//
//	FuncDecl       = "func" [ Receiver ] identifier "(" [ ParameterList ] ")" [ Type | "(" ParameterList ")" ] [ Block ] .
//	Receiver       = "(" identifier Type ")" .
//	ParameterList  = IdentifierList Type { "," [ IdentifierList Type ] } .
//	IdentifierList = identifier { "," identifier } .
//
// If the function declaration omits the Block, it provides the signature for a
// function implemented externally (e.g., in the transpiled C runtime or PASM).
//
// # Expressions
//
// An expression specifies the computation of a value by applying operators and
// functions to operands.
//
//	ExpressionList = Expression { "," Expression } .
//	Expression     = SimpleExpr [ RelOp SimpleExpr ] .
//	SimpleExpr     = Term { AddOp Term } .
//	Term           = UnaryExpr { MulOp UnaryExpr } .
//
// # Operands
//
// Operands denote the elementary values in an expression. An operand may be a
// literal, a (possibly qualified) non-blank identifier denoting a constant,
// variable, or function, or a parenthesized expression.
//
//	UnaryExpr  = { UnaryOp } Factor .
//	Factor     = identifier [ FactorSuffix ]
//		| int_lit
//		| string_lit
//		| rune_lit
//		| "(" Expression ")" .
//
//	FactorSuffix = { Selector | Index } [ CallSuffix ] .
//	Selector     = "." ( identifier | "(" "type" ")" ) .
//	Index        = "[" Expression "]" .
//
// # Operators
//
// Operators combine operands into expressions. OctoGo enforces a strict LL(1)
// evaluation precedence through its grammar productions:
//
//   - Factor: The highest precedence, encompassing identifiers, literals, and
//     parenthesized expressions (int_lit | string_lit | rune_lit | "(" Expression
//     ")").
//   - UnaryExpr: Unary operators (+, -, !, ^, *, &, <-, ~) applied to a Factor.
//   - Term (MulOp): Multiplication, division, and bitwise operators (*, /, <<,
//     >>, &).
//   - SimpleExpr (AddOp): Addition, subtraction, and bitwise operators (+, -,
//     |, ^).
//   - Expression (RelOp): Comparison operators (==, !=, <, <=, >, >=).
//
// (Note: OctoGo omits the logical && and || operators from the binary operator
// chain to simplify short-circuit evaluation in the transpiler).
//
//	UnaryOp    = "+" | "-" | "!" | "^" | "*" | "&" | "<-" | "~" .
//	RelOp = "==" | "!=" | "<" | "<=" | ">" | ">=" .
//	AddOp = "+" | "-" | "|" | "^" .
//	MulOp = "*" | "/" | "<<" | ">>" | "&" .
//
// # Function Calls
//
// Given an expression f of function type, f(a1, a2, … an) calls f with
// arguments a1, a2, … an. Arguments must be single-valued expressions
// assignable to the parameter types of the function and are evaluated before
// the function is called.
//
//	CallSuffix = "(" [ ArgumentList ] ")" .
//	ArgumentList = Expression { "," Expression } .
//
// # Statements
//
// Statements control execution.
//
//	Statement = VarDecl
//		| ConstDecl
//		| TypeDecl
//		| "if" Expression Block [ "else" Block ]
//		| "for" [ Expression ] Block
//		| "return" [ Expression ]
//		| "go" AssignHead { Selector | Index } CallSuffix
//		| SwitchStmt
//		| SelectStmt
//		| "<-" Expression
//		| AssignHead Postfix
//		| EmptyStatement .
//
// # Empty Statements
//
// The empty statement does nothing.
//
//	EmptyStatement = .
//
// # Assignment Statements
//
// An assignment replaces the current value stored in a variable with a new
// value specified by an expression. Due to LL(1) constraints, assignments in
// OctoGo are parsed via the AssignHead Postfix production, which natively
// handles both single assignments (=) and short variable declarations (:=).
//
//	AssignHead = { "*" } ( identifier | "(" Expression ")" ) .
//	Postfix    = { Selector | Index } PostfixOp .
//	PostfixOp  = CallSuffix
//		| "<-" Expression
//		| { "," LhsItem } ( "=" | ":=" ) Expression .
//	LhsItem    = AssignHead { Selector | Index } .
//
// # If Statements
//
// "If" statements specify the conditional execution of two branches according
// to the value of a boolean expression. If the expression evaluates to true,
// the "if" branch is executed, otherwise, if present, the "else" branch is
// executed.
//
// # For Statements
//
// A "for" statement specifies repeated execution of a block. In OctoGo, the
// iteration is strictly controlled by a single boolean condition:
//
// If the condition is absent, it is equivalent to the boolean value true.
//
// (Note: OctoGo does not support init/post statements or range clauses).
//
// # Switch Statements
//
// "Switch" statements provide multi-way execution. An expression is compared
// to the "cases" inside the "switch" to determine which branch to execute.
//
//	SwitchStmt = "switch" [ SwitchGuard ] "{" { CaseClause } "}" .
//	SwitchGuard = Expression [ ":=" Expression ] .
//	CaseClause = CaseHead ":" { Statement ";" } [ Statement ] .
//	CaseHead   = "case" ExpressionList | "default" .
//
// In an expression switch, the switch expression is evaluated and the case
// expressions are evaluated left-to-right and top-to-bottom. The first one
// that equals the switch expression triggers execution of the statements of
// the associated case.
//
// (Note: OctoGo does not support type switches or fallthrough statements).
//
// # Select Statements & Smart Pin Hardware Polling
//
// A "select" statement chooses which of a set of possible send or receive
// operations will proceed.
//
//	SelectStmt  = "select" "{" { CommClause } "}" .
//	CommClause  = CommHead ":" { Statement ";" } .
//	CommHead    = "case" CommOp | "default" .
//	CommOp      = "<-" Expression
//		| AssignHead PostfixComm .
//	PostfixComm = { Selector | Index } ( "=" "<-" Expression | "<-" Expression ) .
//
// (OctoGo Specific): The select statement transpiles into an infinite while(1)
// polling loop in C. It continuously checks non-blocking runtime functions
// (e.g., __octogo_chan_try_recv). Because OctoGo leverages Propeller 2 Smart
// Pins via the standard library, select loops seamlessly multiplex hardware
// locks (channels) and zero-overhead Smart Pin state checks (e.g.,
// _pinr(pin)), yielding via _waitx to prevent Hub RAM bus starvation.
//
// # Go Statements (Concurrency)
//
// A "go" statement starts the execution of a function call as an independent
// concurrent thread of control, or goroutine, within the same address space.
//
// (OctoGo Specific): The go statement transpiles to a block that allocates a
// fixed-size stack and explicitly invokes _cogstart_C. There is a strict 1:1
// hardware mapping to the Propeller 2's physical Cogs. If the 8-cog limit is
// exceeded, the octogo_rt runtime will panic.
//
// # Return Statements
//
// A "return" statement in a function F terminates the execution of F, and
// optionally provides one or more result values.
//
// # Concurrency
//
// OctoGo provides explicit support for concurrent programming through
// goroutines and channels. Unlike standard Go, which relies on a complex
// software scheduler to multiplex thousands of goroutines over fewer OS
// threads, OctoGo maps concurrency primitives directly to the Parallax
// Propeller 2 (P2) hardware.
//
// # Goroutines (Hardware Cogs)
//
// A "go" statement starts the execution of a function call as an independent
// concurrent thread of control, or goroutine, within the same address space.
//
// (OctoGo Specific): Every goroutine in OctoGo maps strictly 1:1 to a physical
// P2 Cog. There is no software-level thread scheduler or VM.
//
//   - Execution: When a go statement is evaluated, the transpiled C code
//     requests a fixed-size stack from the octogo_rt runtime and invokes
//     _cogstart_C via the flexprop compiler.
//   - Hardware Limit: The P2 hardware is strictly limited to 8 physical Cogs.
//     The main function consumes the first Cog. Attempting to spawn more
//     concurrent goroutines than there are available Cogs will result in a
//     runtime panic.
//   - Termination: When the invoked function terminates, its associated Cog is
//     freed and returned to the hardware pool. If the function has any return
//     values, they are discarded when the function completes.
//
// # Channel Types
//
// A channel provides a thread-safe conduit for concurrently executing Cogs to
// communicate by sending and receiving values of a specified type.
//
// (OctoGo Specific): * No Directional Channels: To maintain a strict LL(1)
// grammar, OctoGo simplifies channel types. All channels are bidirectional
// (chan Type).
//
//   - Hardware Representation: In the transpiled C runtime, a channel maps to
//     an octogo_chan_t structure. This structure is backed by shared Hub RAM
//     buffers and synchronized using the P2's native hardware locks (locks 0-15).
//   - Zero-Allocation: Because OctoGo has no dynamic memory allocator (no make
//     or new built-ins), channels are statically allocated and tracked by the
//     octogo_rt runtime during compilation.
//
// # Channel Operations (Send and Receive)
//
// Channels facilitate synchronous, lock-step data transfer between Hub RAM and
// Cog RAM.
//
//   - Send Operations: A send statement sends a value on a channel.
//
//     (Note: Bound contextually via CommOp and Statement left-factoring).
//
//     Both the channel and the value expression are evaluated
//     before communication begins. The send operation transpiles to an
//     __octogo_chan_send C function call. It blocks the current Cog until a
//     receiver is ready, utilizing a P2 hardware lock to ensure atomic data
//     transfer.
//
//   - Receive Operations: For an operand ch of channel type, the receive
//     operation receives a value from the channel.
//
//     (Note: Simplified assignment syntax).  The expression blocks
//     the current Cog until a sender is available, transpiling to an
//     __octogo_chan_recv C function call.
//
// # Synchronization via Hardware Locks
//
// Because there is no software thread scheduler, blocked Cogs do not "sleep"
// in the traditional OS sense. When a Cog blocks on a channel send, channel
// receive, or a select statement, the transpiled C code executes a tight
// polling loop wrapped around non-blocking runtime functions (e.g.,
// __octogo_chan_try_recv).
//
// To prevent Hub RAM bus starvation while a Cog is spinning on a lock, the
// compiler automatically inserts hardware yield instructions (e.g.,
// _waitx(1)). This guarantees that waiting Cogs do not bottleneck the
// performance of active Cogs.
//
// # Packages
//
// # Source file organization
//
// (Divergence from Go)
//
// OctoGo intentionally omits the package clause. A source file begins directly
// with a possibly empty set of import declarations, followed by a possibly
// empty set of top-level declarations. A package's namespace is implicitly
// inferred from the base name of its directory or import path.
//
//	SourceFile = { ImportDecl ";" } { TopLevelDecl ";" } .
//	ImportDecl = "import" ( ImportSpec | "(" { ImportSpec ";" } [ ImportSpec ] ")" ) .
//	ImportSpec = [ "." | identifier ] string_lit .
package main // import "modernc.org/octogo"
