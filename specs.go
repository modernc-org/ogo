// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Reconciled with the implementation 20260720. Done since the list below was
// written: composite literals for structs, positional and keyed; array and slice
// literals; package-level channels; "defer" (including in a nested block); "++"
// and "--"; the compound assignment operators; "%"; and the concurrency layer
// (channels, "go", "select"). "&&" and "||" are parsed and rejected with a
// diagnostic, which is deliberate -- see Operators.
//
// Maps and floating point are not on the list because they are not pending: the
// body of this document omits both deliberately (see Keywords and Types), and the
// README says so as well. They were TODO items until 20260720, which read as work
// owed rather than as a decision taken.
//
// TODO 20260317 labels and gotos. Note that Keywords says "goto" is intentionally
// omitted, so this contradicts the body and one of the two is wrong.
// TODO 20260719 Select: send clauses, and smart-pin clauses
// TODO 20260719 Go statements: methods and qualified callees, per-goroutine stack size
// TODO 20260719 Break statements: allowed in a switch once switch stops lowering to if/else
// TODO 20260720 Composite literals: Go's indexed form, "[3]int{2: 5}"
// TODO 20260720 Arrays: an array as a function result; slicing a multi-dimensional array
// TODO 20260720 Imports: only "p2" resolves, so a program is one package in one directory

// The C backend and the board loader are embedded, so no separate flexprop
// installation is needed.
//
// Usage:
//
//	ogo <command> [arguments]
//
// The commands are:
//
//	build       compile packages and dependencies
//	fmt         reformat source files
//	help        show help for a command
//	loadp2      load a program onto a Propeller 2 board (loadp2 passthrough)
//	run         compile and run a program on a connected board
//	smith       output a random program for compiler testing
//	test        test packages
//	version     print the ogo version
//
// Run "ogo help <command>" for more information about a command.
//
// Installation is "go install modernc.org/ogo@latest", which needs Go 1.25 or
// newer. The rest of this document is the language specification.
//
// # OctoGo Language Specification
//
// Draft of Jul 19, 2026.
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
// (Note: Keywords like package, goto and map have been intentionally omitted
// from OctoGo to simplify the grammar and runtime):
//
//	break       chan        default     fallthrough go          import      range       select      switch
//	case        const       defer       for         if          interface   return      struct      type
//	continue    else        func                                                                     var
//
// # Operators and punctuation
//
// The following character sequences represent operators and punctuation.
//
//	&    +     ==    !=    (    )
//	-    |     <     <=    [    ]
//	*    ^     >     >=    {    }
//	/    <<    =     :=    ,    ;
//	%    >>    !     <-    .    :
//	~    ++    --
//
//	+=   -=    *=    /=    %=
//	&=   |=    ^=    &^=   <<=   >>=
//
// There is no "&^" operator, and none is needed: "x &^ y" parses as "x & ^y",
// applying the unary complement, which is the value Go's AND NOT produces. The
// compound form "&^=" is a single token, since no such decomposition applies to
// an assignment.
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
// # Floating-point literals
//
// OctoGo does not support floating-point types (see Types). A floating-point
// literal is nonetheless recognized by the grammar and folds to an untyped
// floating-point constant, so that an unsupported use -- such as a
// float-typed declaration -- is reported with a clear semantic diagnostic
// instead of a confusing parse error on the literal.
//
//	float_lit = decimal_digits "." decimal_digits .
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
// Heap Allocation: There is no dynamic heap allocation in OctoGo. The new
// built-in is rejected outright; the make built-in is admitted only for a slice
// with a constant capacity, "make([]T, len, cap)", which reserves a fixed,
// compile-time-sized backing array rather than allocating on a heap. All memory
// thus stays deterministically bounded at compile time.
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
//		| StructType
//		| "func" Signature .
//
// # Boolean types
//
// A boolean type represents the set of Boolean truth values denoted by the
// predeclared constants true and false.
//
//   - The predeclared boolean type is bool.
//   - A bool is a distinct type, not an alias for an integer: it may not be used
//     in arithmetic, and it transpiles to C99 _Bool -- one byte, normalized to
//     0 or 1 -- so a bool packs tightly in a struct or array and prints as true
//     or false rather than as 1 or 0.
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
// (OctoGo Specific): Concatenation with "+" is limited to compile-time constants,
// which fold to a single literal. A concatenation with a non-constant operand is
// rejected, since building a new string at run time needs allocation and the
// target has no heap.
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
// (OctoGo Specific): In OctoGo, slices are strictly non-escaping views over a
// fixed array's storage in pre-allocated Hub or Cog RAM -- a { pointer, length,
// capacity } header. Because there is no GC or dynamic allocator, a slice never
// acquires new backing memory. It may still grow or be re-sliced up to its
// capacity (the length of the backing from its pointer to the end); a slice's
// upper index bound reaches cap, not just length. Growing past cap has nowhere to
// go and is a runtime error, not a reallocation.
//
// A slice's backing comes either from slicing an existing array ("var a [N]T"
// then "a[i:j]") or from "make([]T, len, cap)", which reserves a fixed, compile-
// time-sized backing array. "append(s, x)" grows the length in place: the form
// "s = append(s, x)" panics when the slice is already at capacity, while
// "s, ok = append(s, x)" instead reports a full slice through ok and leaves s
// unchanged. len(s) and cap(s) report the header's length and capacity.
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
//	MethodSpec = identifier "(" [ ParameterList ] ")" [ Type | "(" ResultList ")" ] .
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
//   - A declaration binds a non-blank identifier to a constant, type,
//     variable, function or package.
//   - Every identifier in a program must be declared.
//   - No identifier may be declared twice in the same block and and no
//     identifier may be declared in both the file and package block.
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
// If expressions are given there must be one per identifier, each variable
// taking its own ("var a, b = 1, 2"); alternatively a single call yielding one
// value per identifier may stand in for the whole list ("var q, r = divmod(17,
// 5)"). Otherwise each variable is initialized to its zero value.
//
// Grammar:
//
//	VarDecl = "var" ( VarSpec | "(" { VarSpec ";" } [ VarSpec ] ")" ) .
//	VarSpec = IdentifierList ( Type [ "=" ExpressionList ] | "=" ExpressionList ) .
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
// expression, unlike Go which allows identifier lists. Within a parenthesized
// group a ConstSpec may omit its expression, in which case it repeats the
// previous spec's expression and type. iota is a predeclared integer constant
// equal to the zero-based index of the ConstSpec in its group, so a repeated
// expression takes a new value at each spec).
//
//	ConstDecl = "const" ( ConstSpec | "(" { ConstSpec ";" } [ ConstSpec ] ")" ) .
//	ConstSpec = identifier [ Type ] [ "=" Expression ] .
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
//	FuncDecl       = "func" [ Receiver ] identifier Signature [ Block ] .
//	Signature      = "(" [ ParameterList ] ")" [ Type | "(" ResultList ")" ] .
//	Receiver       = "(" identifier Type ")" .
//	ParameterList  = ParamDecl { "," ParamDecl } .
//	ResultList     = ParamDecl { "," ParamDecl } .
//	ParamDecl      = Type [ Type ] .
//	IdentifierList = identifier { "," identifier } .
//
// A parameter or result list may name its entries or leave them unnamed, but not
// both: "(a, b int)" and "(int, int)" are the two-entry forms, while
// "(a int, string)" is illegal. Each ParamDecl is one type, optionally preceded
// by a name; the whole list is named when any ParamDecl carries a name, in which
// case a bare ParamDecl is a name sharing the next named entry's type
// ("(a, b int)"). Parameters and results share this grammar; an unnamed parameter
// is simply one the body does not refer to.
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
//	Expression     = SimpleExpr { RelOp SimpleExpr } .
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
//	Factor     = identifier [ CompositeLit | FactorSuffix ]
//		| int_lit
//		| float_lit
//		| string_lit
//		| rune_lit
//		| "(" Expression ")"
//		| "[" [ Expression ] "]" Type [ CompositeLit ]
//		| "chan" Type
//		| FuncLiteral .
//	CompositeLit = "{" [ ElementList ] "}" .
//	ElementList  = Element { "," Element } .
//	Element      = Expression [ ":" Expression ] .
//
// A composite literal "T{a, b}" builds a value of the named struct type from its
// fields in declaration order. An Element may instead carry a key naming the field
// it fills, "T{b: 2}", in which case every Element must: the two forms may not be
// mixed, because once one Element names its field, position stops meaning anything.
// A keyed literal may name any subset of the fields in any order, and a field it
// does not name takes its zero value.
//
// A bracketed type may carry one too, giving an array literal "[N]T{a, b}" or a
// slice literal "[]T{a, b}". Their elements are positional; Go's indexed form
// ("[3]int{2: 5}") is not supported. An array literal may supply fewer values than
// its length, zeroing the rest, and no more; a slice literal's length and capacity
// are however many it supplies. Both are a variable's initializer and nothing else:
// an array cannot be assigned, and a slice literal's backing storage belongs to the
// declaration it initializes.
//
// A "chan" type may stand where a type-as-value may, so that "make(chan T)"
// parses and is then refused by the checker, which can name the real problem;
// left out of the grammar it would break the parse instead and be reported as
// something else entirely. Because the grammar is LL(1), a composite literal
// may not appear at the top level of an "if", "for" or "switch" header, where the
// "{" would be indistinguishable from the block that follows: those headers use
// HeaderExpression below, which is the ordinary expression grammar minus this one
// production. Parenthesizing restores it, exactly as in Go: "if p == (P{}) {".
//
//	HeaderExpression = HeaderSimpleExpr { RelOp HeaderSimpleExpr } .
//	HeaderSimpleExpr = HeaderTerm { AddOp HeaderTerm } .
//	HeaderTerm       = HeaderUnaryExpr { MulOp HeaderUnaryExpr } .
//	HeaderUnaryExpr  = { UnaryOp } HeaderFactor .
//	HeaderFactor     = identifier [ FactorSuffix ]
//		| int_lit
//		| float_lit
//		| string_lit
//		| rune_lit
//		| "(" Expression ")"
//		| "[" [ Expression ] "]" Type
//		| "chan" Type
//		| FuncLiteral .
//
// A slice or array type may appear as a Factor so that the type argument such as
// the "[]int" in "make([]int, 0, cap)" parses. A bare type used as a value is
// rejected by the semantic checker, as is new; make is accepted only for a slice
// with a constant capacity (see Slice types).
//
//	FactorSuffix = { Selector | Index | CallSuffix } .
//	Selector     = "." ( identifier | "(" "type" ")" ) .
//	Index        = "[" ( Expression [ ":" [ Expression ] ] | ":" [ Expression ] ) "]" .
//
// A single-expression Index "a[i]" indexes an element. The colon forms are slice
// expressions "a[low:high]", "a[low:]", "a[:high]" and "a[:]", which create a new
// { pointer, length, capacity } view over the operand's storage; an omitted low
// bound is 0 and an omitted high bound is the operand's length. For a slice operand
// the high bound may reach its capacity rather than only its length, and the
// result's capacity is the operand's capacity less low. Slicing a string yields a
// string (a string has no capacity).
//
// # Function Literals
//
// A function literal represents an anonymous function.
//
//	FuncLiteral = "func" Signature Block .
//
// (OctoGo Specific): Because OctoGo strictly enforces a zero-allocation memory
// model without a Garbage Collector, function literals cannot act as dynamic
// closures. They may not capture or reference variables from their surrounding
// lexical scope. In the transpiled C code, function literals are treated
// strictly as statically allocated, pure function pointers.
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
//   - Term (MulOp): Multiplication, division, remainder, and bitwise operators
//     (*, /, %, <<, >>, &).
//   - SimpleExpr (AddOp): Addition, subtraction, and bitwise operators (+, -,
//     |, ^).
//   - Expression (RelOp): Comparison operators (==, !=, <, <=, >, >=).
//
// (Note: OctoGo does not support the logical && and || operators. They are
// recognized by the grammar so that a use is rejected with a clear semantic
// diagnostic rather than a confusing parse error, but they carry no meaning).
//
//	UnaryOp    = "+" | "-" | "!" | "^" | "*" | "&" | "<-" | "~" .
//	RelOp = "==" | "!=" | "<" | "<=" | ">" | ">=" | "&&" | "||" .
//	AddOp = "+" | "-" | "|" | "^" .
//	MulOp = "*" | "/" | "%" | "<<" | ">>" | "&" .
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
// # Built-in functions
//
// A few functions are predeclared: they are called like ordinary functions but
// belong to no package and need no import. Constrained to fit a zero-allocation,
// no-GC target, the set OctoGo implements today is:
//
//	len(s)              length of a string, array or slice
//	cap(s)              capacity of a slice (the length of its backing array)
//	make([]T, n[, m])   a length-n, capacity-m slice over a fresh backing array
//	append(s, x)        append one element to a slice
//	copy(dst, src)      copy elements between two slices, returning the count
//	clear(s)            set every element of a slice to its zero value
//	min(x, y, …)        the smallest of its ordered arguments
//	max(x, y, …)        the largest of its ordered arguments
//	print(args…)        write the arguments to the serial console
//	println(args…)      like print, but space-separated and newline-terminated
//
// The names are predeclared in the universe block, so a local or package-level
// declaration of the same name shadows the built-in — min, max and clear are the
// likely collisions.
//
// make and append are where the fixed-memory model shows through. make performs
// no heap allocation: it reserves a backing array whose size is fixed at compile
// time, so n and m must be constants, and it is admitted only as the initializer
// of a slice variable, "var s []T = make([]T, n, m)"; the two-argument form
// "make([]T, n)" sets the capacity equal to the length. append adds a single
// element and cannot grow a slice past its capacity, so it has two forms: the
// one-result form "s = append(s, x)" traps at run time if the element does not
// fit, while the two-result form "s, ok = append(s, x)" never traps and reports
// through ok whether the element was appended. copy copies min(len(dst),
// len(src)) elements between two slices of the same element type — which may
// overlap — and yields that count. clear zeroes a slice's elements in place.
//
// print and println are the only I/O built-ins; they write to the board's serial
// output. Each takes any number of arguments, either scalar values or a whole
// slice or array of a scalar element type. println separates its arguments with a
// space and ends with a newline; print writes them adjacently with no terminator.
//
// The other Go built-ins are recognized by the checker but not yet emitted:
// close, complex, delete, imag, panic, real and recover each report "the X
// built-in is not supported yet". The exception is new, which — together with
// every make form other than the slice form above — is rejected outright as
// "dynamic allocation not supported", a heap having no place on the target.
//
// # Statements
//
// Statements control execution.
//
//	Statement = VarDecl
//		| ConstDecl
//		| TypeDecl
//		| IfStmt
//		| "for" [ ForHeader ] Block
//		| "break"
//		| "continue"
//		| "return" [ ExpressionList ]
//		| "go" AssignHead { Selector | Index | CallSuffix }
//		| SwitchStmt
//		| SelectStmt
//		| "<-" Expression
//		| AssignHead Postfix
//		| "defer" AssignHead { Selector | Index | CallSuffix }
//		| Block
//		| EmptyStatement .
//
// # For Statements
//
// A "for" statement specifies repeated execution. Three forms are provided: a
// conditionless loop, a loop repeating while a condition holds, and one with an
// init statement, a condition and a post statement.
//
//	for { ... }                    // until a break or return
//	for i < n { ... }              // while the condition holds
//	for i := 0; i < n; i++ { ... } // init, condition, post
//
// A variable introduced by the init statement is scoped to the whole "for" --
// its condition, its post statement and its body -- and not to the block
// containing it.
//
// A fourth form ranges over an integer, a slice or an array:
//
//	for i := range n { ... }       // i = 0, 1, ... n-1  (n an integer)
//	for i := range xs { ... }      // i indexes the slice or array
//	for i, v := range xs { ... }   // i is the index, v a copy of each element
//	for range n { ... }            // repeat n times, no variable
//
// Ranging an integer yields only the index; the two-variable form is available
// for a slice or an array, where the second variable is a copy of the element.
// Ranging a channel or a string is not provided: a channel has no close, and
// string iteration would require decoding.
//
// (OctoGo Specific): To stay LL(1), a header is parsed as an expression first
// and what follows it decides how to read it: a "{" makes it the condition, and
// a ";" or an assignment operator makes it the init statement of the three-clause
// form. This is the same left-factoring SwitchGuard uses, and it is why the
// grammar spells the header out rather than naming the three parts directly.
//
// # Break and Continue Statements
//
// A "break" statement terminates execution of the innermost enclosing "for"
// statement. A "continue" statement begins the next iteration of the innermost
// enclosing "for" statement. Both appear in the Statement production above.
//
// (OctoGo Specific): Unlike Go, "break" may not appear in a "switch" statement.
// A switch is lowered to a chain of conditionals rather than to a C switch, so
// the two constructs do not share a notion of what "break" leaves; a break there
// is rejected rather than silently leaving an enclosing loop. A "continue" inside
// a switch is unaffected, since it names the enclosing loop either way.
//
// Neither statement takes a label, since OctoGo has no labels.
//
// # Defer Statements
//
// A "defer" statement invokes a function whose execution is deferred to the
// moment the surrounding function returns, either because it executed a return
// statement or reached the end of its function body.
//
//	"defer" AssignHead { Selector | Index | CallSuffix }
//
// Deferred functions are executed in LIFO (last-in, first-out) order
// immediately before the surrounding function returns.
//
// (OctoGo Specific): To maintain deterministic memory usage and comply with
// the language's zero-allocation model, defer statements are resolved
// statically at compile time and transpiled into direct C "goto" cleanup
// blocks.
//
// Strict Restriction: "defer" statements are forbidden inside "for" loops or
// any dynamically unbounded control flow blocks. In a zero-allocation
// environment, accumulating an unknown number of deferred calls would require
// dynamic heap allocation or an infinitely growing Hub RAM stack. Bounding
// "defer" to the static block scope guarantees safe, predictable execution on
// the Propeller 2 hardware.
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
//	Postfix    = { Selector | Index | CallSuffix } [ PostfixOp ] .
//	PostfixOp  = "<-" Expression
//		| "++"
//		| "--"
//		| AssignOp Expression
//		| { "," LhsItem } ( "=" | ":=" ) ExpressionList .
//	AssignOp   = "+=" | "-=" | "*=" | "/=" | "%="
//		| "&=" | "|=" | "^=" | "&^="
//		| "<<=" | ">>=" .
//	LhsItem    = AssignHead { Selector | Index } .
//	ForHeader  = ";" [ HeaderExpression ] ";" [ ForPost ]
//		| "range" HeaderExpression
//		| HeaderExpression [ ForRest ] .
//	ForRest    = ";" [ HeaderExpression ] ";" [ ForPost ]
//		| ( "=" | ":=" ) ForAssignRest
//		| "," HeaderExpression ( "=" | ":=" ) "range" HeaderExpression .
//	ForAssignRest = "range" HeaderExpression
//		| HeaderExpression ";" [ HeaderExpression ] ";" [ ForPost ] .
//	ForPost    = HeaderExpression [ ( "=" | ":=" ) HeaderExpression | "++" | "--" ] .
//
// The "++" and "--" forms are the increment and decrement statements "x++" and
// "x--"; they take no operand of their own (the target is the AssignHead) and,
// unlike Go's, are statements only -- never expressions.
//
// An assignment may have several targets and several values: "a, b = c, d"
// assigns each value to the corresponding target. The values are all evaluated,
// in the usual order, before any assignment happens, so "a, b = b, a" swaps.
// As a special case, the right-hand side may be a single call returning as many
// values as there are targets, which distributes its results: "a, b = f()".
//
// The AssignOp forms are the compound assignments. "x op= y" is equivalent to
// "x = x op y", except that the target is evaluated only once -- which is
// observable when the target contains an index expression, as in "a[i()] += 1".
// The operators mirror the binary ones and carry their operand rules: the
// arithmetic forms ("+=", "-=", "*=", "/=", "%=") require numeric operands of
// the same type, "+=" additionally concatenating strings; the bitwise forms
// ("&=", "|=", "^=", "&^=") require integers of the same type; and the shifts
// ("<<=", ">>=") take an unsigned or untyped-constant shift count that need not
// match the target's type. "&^=" is the AND NOT form, clearing in the target
// every bit set in the operand.
//
// Unlike "=", a compound assignment takes exactly one target: "a, b += 1" is
// not a valid statement.
//
// # If Statements
//
// "If" statements specify the conditional execution of two branches according
// to the value of a boolean expression. If the expression evaluates to true,
// the "if" branch is executed, otherwise, if present, the "else" branch is
// executed. An "else" may be followed by another "if" statement, forming an
// "else if" chain, or by a block.
//
//	IfStmt = "if" HeaderExpression Block [ "else" ( IfStmt | Block ) ] .
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
//	SwitchGuard = HeaderExpression [ ":=" HeaderExpression ] .
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
//	PostfixComm = { Selector | Index } ( ( "=" | ":=" ) "<-" Expression | "<-" Expression ) .
//
// (OctoGo Specific): A select polls its clauses in order, retrying the
// non-blocking form of each communication. A default clause makes the select
// non-blocking: the clauses are tried once and the default runs if none was
// ready. Without a default the poll repeats, yielding via _waitx between rounds
// to prevent Hub RAM bus starvation. Because OctoGo reaches Propeller 2 Smart
// Pins through the standard library, the same loop can multiplex channels and
// zero-overhead Smart Pin state checks (e.g. _pinr(pin)).
//
// # Go Statements (Concurrency)
//
// A "go" statement starts the execution of a function call as an independent
// concurrent thread of control, or goroutine, within the same address space.
//
// (OctoGo Specific): The go statement transpiles to a block that claims a
// pooled slot holding a fixed-size stack and the call's arguments, then invokes
// _cogstart_C. There is a strict 1:1 hardware mapping to the Propeller 2's
// physical Cogs. Exceeding the 8-cog limit is a runtime panic.
//
// # Return Statements
//
// A "return" statement in a function F terminates the execution of F, and
// optionally provides one or more result values.
//
// The number of result operands must equal the number of the function's
// results, each assignable to its result type. A "return" with no operands is
// allowed in two cases: a function with no results, and a function whose results
// are all named -- there the bare "return" (a "naked" return) supplies the
// current values of the named results, which are ordinary variables the body may
// have assigned. A named result is zero-initialized, so a naked return before
// any assignment yields the zero value.
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
//   - Execution: A go statement claims a slot from a statically allocated pool,
//     marshals the call's arguments into it, and invokes _cogstart_C. The slot
//     holds the goroutine's stack as well as its arguments, because the launched
//     Cog reads both after the go statement has returned, so neither can live in
//     the launching function's frame. The pool holds one slot per available Cog,
//     which makes running out of slots and running out of Cogs one condition.
//   - Hardware Limit: The P2 hardware is strictly limited to 8 physical Cogs.
//     The main function consumes the first Cog. Attempting to spawn more
//     concurrent goroutines than there are available Cogs is a runtime panic.
//     A go statement inside a loop is therefore legal, unlike a defer inside
//     one: the hardware bounds it, and exhaustion is reported rather than
//     silently exceeding anything.
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
//   - Hardware Representation: A channel is a reference to a rendezvous cell in
//     Hub RAM, synchronized by one of the P2's native hardware locks (0-15).
//     Because a channel is a reference, passing one to a goroutine shares the
//     cell rather than copying it. Acquiring a lock can fail once all 16 are in
//     use, which is a runtime panic.
//   - Zero-Allocation: OctoGo has no dynamic memory allocator, and channels are
//     not created with make -- doing so is rejected as a dynamic allocation. A
//     channel is created by its declaration, which is what allocates its cell and
//     acquires its lock, so the lock's lifetime is the variable's.
//   - Unbuffered: A channel holds one value in flight. A send completes only once
//     a receiver has taken that value, so the two meet in lock step, which is
//     what makes a buffer unnecessary.
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
//     before communication begins. A send blocks the current Cog until a
//     receiver has taken the value, using the channel's hardware lock to keep
//     each hand-off atomic.
//
//   - Receive Operations: For an operand ch of channel type, the receive
//     operation receives a value from the channel.
//
//     (Note: Simplified assignment syntax).  The expression blocks
//     the current Cog until a sender has deposited a value.
//
// # Synchronization via Hardware Locks
//
// Because there is no software thread scheduler, blocked Cogs do not "sleep"
// in the traditional OS sense. When a Cog blocks on a channel send, channel
// receive, or a select statement, it polls: it retries the non-blocking form of
// the operation, which reports whether it succeeded rather than waiting.
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
//
// # The zero value
//
// When storage is allocated for a variable, either through a declaration or
// as a struct field, and no explicit initialization is provided, the variable
// or value is given a default value. Each element of such a variable or value
// is set to the zero value for its type: false for booleans, 0 for numeric
// types, "" for strings, and nil for pointers, functions, interfaces, slices,
// and channels.
//
// (OctoGo Specific): Because OctoGo maps memory directly to Hub RAM or Cog
// RAM, the mechanism for zero-initialization depends on scope:
//   - Package-level variables (Hub RAM) are statically allocated into the
//     BSS segment and automatically zero-initialized by the runtime prior
//     to program execution.
//   - Local variables allocated on the Cog stack are explicitly
//     zero-initialized by the compiler via emitted assignment statements
//     if no initializer expression is provided in the source.
//
// # Program initialization and execution
//
// # Package initialization
//
// Within a package (which in OctoGo maps strictly to a single directory),
// package-level variable initialization proceeds sequentially.
//
// Because OctoGo intentionally omits the package clause and merges all source
// files within a directory into a single Abstract Syntax Tree,
// global variables are initialized in a deterministic topological order based
// on their dependencies.
//
// Variables may also be initialized using functions named init declared in
// the package block, with no arguments and no result parameters:
//
//	func init() { … }
//
// Multiple such functions may be defined per package, even within a single
// source file. In the package block, the init identifier can be used only to
// declare init functions, yet the identifier itself is not declared. Thus
// init functions cannot be referred to from anywhere in a program.
//
// All init functions across all files in the directory are gathered and
// executed sequentially by the transpiled runtime before the package is
// considered fully initialized.
//
// # Program initialization
//
// A complete OctoGo program is created by compiling an unimported main package
// along with all the packages it imports, transitively. The ogo tool builds
// packages using standard OS file paths (e.g., ogo build <import-path>).
//
// Import paths must be slash-separated, entirely lower-case ASCII letters, the
// '_' character c and digits, and must not begin with a "." or "/" or end with
// a "/". Import paths without dots in their first segment are reserved for the
// standard library.
//
// The main package must declare a function main that takes no arguments and
// returns no value:
//
//	func main() { … }
//
// Program initialization begins by initializing the imported packages. If
// multiple packages import the same package, the imported package will be
// initialized only once.
//
// After all imported packages are initialized, the package-level variables of
// the main package are initialized, followed by the execution of all init
// functions within the main package.
//
// # Program execution
//
// Execution begins by invoking the function main on the first available
// physical Propeller 2 Cog (the Boot Cog).
//
// When main returns, or execution falls through the end of the main block,
// the program terminates.
//
// (OctoGo Specific): Standard Go semantics dictate that when main terminates,
// the program exits and all other goroutines are immediately stopped. Because
// OctoGo maps goroutines directly to physical Propeller 2 Cogs,
// the transpiled main function is guaranteed to emit a hardware-level reset
// or shutdown signal (e.g., _clkset(0, 0)) immediately prior to returning.
// This prevents orphaned worker Cogs from continuing hardware I/O
// indefinitely.
//
// If an OctoGo program is intended to run indefinitely (e.g., as a daemon
// handling hardware interrupts or channels on worker Cogs), the main function
// must intentionally block before terminating, typically via an empty select
// statement:
//
//	select {}
package main
