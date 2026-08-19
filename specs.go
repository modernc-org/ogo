// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Reconciled with the implementation 20260728. What landed in each release,
// including the changes that reject a program an earlier one accepted, is in
// CHANGELOG.md; this list is only what is still owed.
//
// TODO 20260317 goto. Labels and labeled break/continue are supported (see Break
// and Continue Statements); "goto" itself stays out (Keywords), pending the
// jump-over-declaration safety analysis its unrestricted form needs.
// TODO 20260719 Select: smart-pin clauses; a send clause with a default, and more
// than one send clause, both of which need a "receiver ready" signal the rendezvous
// cell does not carry (see Select Statements)
// TODO 20260719 Go statements: per-goroutine stack size. Every goroutine gets the
// same fixed stack in its pool slot (OGO_STACK_LONGS, 256 longs), which a deep call
// chain can overrun with no diagnostic -- there is no guard page on this part. A
// recursive function is the way to reach it: measured on a P2-EDGE, a goroutine
// recursing 200 deep returns normally and one recursing 2000 deep prints nothing at
// all, having overrun the slot before it could report anything.
//
// A guard word at each end of the slot, checked when the slot is reclaimed, was
// tried and abandoned: it costs little, but the case it was built for -- the deep
// recursion above -- dies before the check is ever reached, so it could not be shown
// to catch anything. What would work is a depth check at function entry, or a stack
// whose size the "go" statement can choose, which is what this TODO is really for.
// TODO 20260806 Arrays: a fixed-array result may not stand BESIDE another result,
// `func f() ([3]int, int)`. That would need a struct holding an array, which this
// backend cannot assign, and handing back a pointer instead would name the callee's
// dead frame. Every other use of an array result works.
// TODO 20260725 Complex numbers (see Types). They need no heap, so their absence
// is work owed, unlike that of maps.
// TODO 20260804 A conversion to an unnamed composite type must be PARENTHESISED:
// `([]int)(xs)` and `([3]int)(q)` work, `[]int(xs)` does not parse. That is the
// parenthesis restriction above, taken because the bare form is the one variant that
// costs LL(1) conflicts -- it adds `Signature` on "(" and `Type` on "." to the
// grammar's own eight, where the parenthesised form adds none. Both spellings are
// valid Go, so obeying it costs nothing but the two characters. A conversion that
// would change the REPRESENTATION -- `([]byte)(s)` from a string -- is refused after
// parsing, as it allocates.
// When measuring any grammar change, confirm make actually REGENERATED -- `touch
// specs.go` can land in the same second as a preceding checkout and leave parser.go
// "up to date", which reports zero warnings and has twice produced a false baseline.
// TODO 20260808 Three diagnostics still read differently from Go's, in shape rather
// than in content: an "invalid operation:" prefix on "cannot index"/"cannot slice",
// which Go drops there and keeps on "cannot indirect"; "type int has no field f",
// where Go writes "n.f undefined (type int has no field or method f)"; and "cannot
// call non-function q.n", where Go writes "cannot call q.n (variable of type int):
// int is not a function". A COMPOSITE operand gets no type in the parenthetical --
// a Kind names only a predeclared type, and a wrong name is worse than none.

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
// # Relationship to Go
//
// The goal is to support what pre-generics Go supports, wherever that is feasible
// on the target. A construct missing here is therefore work not yet done rather
// than a decision taken, unless it is listed below or said to be one where it is
// described. This matters when reading the rest of this document: a note that
// OctoGo "does not support X" records the state of the implementation when it was
// written, and is retired as X lands -- it is not, by itself, evidence that X was
// ruled out.
//
// Generics are the one part of Go held at arm's length: not supported, not
// planned, and not ruled out either. They are a question for after v1, on three
// counts -- whether an LL(1) grammar can describe them at all, whether they earn
// their complexity in Propeller 2 programming, and how they would interact with
// the whole-program specialization the compiler already intends to do. Nothing
// here is designed to exclude them.
//
// The deliberate deviations are rooted in the Propeller 2 hardware, not in
// keeping the language small:
//
//   - No heap. Nothing allocates at run time, which is ordinary for a
//     microcontroller. This is what rules out "new", every "make" form but the
//     slice one, maps, run-time string concatenation, and a function literal that
//     captures its surrounding scope. It is also why a reference must not outlive
//     what it refers to: where Go moves a referent to the heap and says nothing,
//     there is nowhere to move it to, so the program is refused instead. One rule,
//     met wherever a reference could leave its referent behind -- returning a
//     local's address or a slice backed by a local, storing either in a package
//     variable, handing either to another cog, and calling a function that hands the
//     argument on. A struct holding such a reference counts as one.
//   - A goroutine is a physical cog, of which the P2 has eight. There is no
//     scheduler and no preemption, so "go" starts a real core, not a task.
//   - A channel is a P2 hardware lock over statically allocated Hub RAM, giving a
//     synchronous rendezvous with no scheduler behind it.
//   - An interface value holds a POINTER, so a pointer is what goes into one: "&x",
//     not "x". Go accepts either and copies the value in, allocating for it; there
//     is no heap here to allocate into, so the value form is refused rather than
//     quietly made a reference to the variable it was written from. What that buys
//     is that a program which compiles here means exactly what it means in Go --
//     "&x" puts a pointer in the interface in both languages, and mutations through
//     x are visible through the interface in both. "&T{...}" works too, its storage
//     being a temporary of the frame rather than an allocation, and so subject to
//     the lifetime rule above. A type assertion "x.(*T)" and a type switch both
//     recover the concrete pointer. See internal/octogo/octogo.go.
//
// # Parentheses where the parser needs them
//
// The compiler's parser is LL(1), generated from the grammar below. A few Go
// spellings cannot be described in that class without making unrelated decisions
// ambiguous, and where a spelling has an equivalent that carries its own
// delimiters, THE PARENTHESISED FORM MAY BE REQUIRED.
//
// This is a restriction on the implementation, not a change to the language. The
// parenthesised form is valid Go as written, so a program using it compiles both
// here and there; a more powerful parser -- a different generator, or a hand-written
// one once the grammar has settled -- must accept BOTH forms, and this section is
// what it is allowed to relax, never something it has to keep. Go itself restricts
// the other way for a related reason, refusing a bare composite literal in an "if",
// "for" or "switch" header where the "{" would be read as the block; C requires
// parentheses around a type in a cast, and around far more besides.
//
// Where it applies, today, is one place: a conversion whose target is an UNNAMED
// composite type must be written `([]int)(xs)` or `([3]int)(q)` rather than
// `[]int(xs)`. Measured, the bare form is the only variant that costs the grammar
// anything -- it makes `func() []int` and a conversion compete for one decision --
// while the parenthesised one is free. A conversion whose target is a NAME,
// `Row(r)`, needs no parentheses and never did.
//
// The rule for adding another: it is allowed only when the parenthesised spelling
// means the same thing in Go, so that obeying the restriction never produces a
// program Go reads differently.
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
// a line's final token if that token is an identifier, a literal, one of the
// control-flow keywords that can end a statement ("break", "continue",
// "fallthrough", "return"), an increment or decrement operator, or closing
// punctuation.
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
// Three of Go's are absent, for three different reasons: "package" because a
// source file carries no package clause (see Packages), "map" because a map
// allocates and the target has no heap (see Relationship to Go), and "goto"
// because the analysis its unrestricted form needs is not written yet -- labels
// and labeled break/continue, which need no such analysis, are supported:
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
// A floating-point literal is an untyped floating-point constant. It has a decimal
// form -- an integer part, a fraction and an exponent -- and a hexadecimal one,
// whose exponent is a power of two and is required, as in Go.
//
//	float_lit = decimal_digits "." [ decimal_digits ] [ exponent ]
//		| decimal_digits exponent
//		| "." decimal_digits [ exponent ]
//		| hex_float_lit .
//	exponent  = ( "e" | "E" ) [ "+" | "-" ] decimal_digits .
//
//	hex_float_lit = "0" ( "x" | "X" ) hex_mantissa hex_exponent .
//	hex_mantissa  = [ "_" ] hex_digits "." [ hex_digits ]
//		| [ "_" ] hex_digits
//		| "." hex_digits .
//	hex_exponent  = ( "p" | "P" ) [ "+" | "-" ] decimal_digits .
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
//   - A conversion of a constant to a numeric type is itself a constant, so
//     "const one = int32(1) << 16" is a constant expression and may be used
//     wherever one is, an array bound included. The value must be representable in
//     the target type ("int8(200)" overflows) and a float converted to an integer
//     type must be whole ("int32(2.5)" is truncated, and refused).
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
//   - Division of two integer constants is integer division, as in Go: "7 / 2" is 3.
//     A constant expression with a floating-point operand divides as a float.
//   - A constant expression is computed in arbitrary precision and then converted to
//     the type it is used at, as in Go, so "var x int64 = 1 << 40" is 1099511627776
//     and not what a 32-bit shift would give.
//   - A shift by a count at least as wide as the value's type yields 0, or -1 for a
//     right shift of a negative value, as in Go -- not C's count-modulo-the-width. A
//     negative count is a run-time panic. A count that is a constant already inside
//     the width costs nothing extra; any other goes through a guard.
//   - The most negative value of a signed type divided by -1 is itself, with a
//     remainder of 0, as in Go: the quotient is not representable, so the two's-
//     complement overflow stands rather than being undefined as it is in C.
//   - Explicit conversions are required when different numeric types are mixed in
//     an expression or assignment.
//   - int and uint are the target's word, 32 bits wide, and are types of their own:
//     int is NOT int32 and uint is NOT uint32, so mixing them takes a conversion
//     even though the two have the same width and representation here. byte and
//     rune are the two exceptions, being aliases as they are in Go -- byte IS uint8
//     and rune IS int32 -- and mix with what they name without one.
//   - An untyped constant takes the type of the context it appears in, so no
//     conversion is wanted where one fits: "var x int32 = 42" and "var y int64 = 42"
//     are both legal. Written on its own it takes its default type -- int for an
//     integer literal, rune for a rune literal, float64, bool, string -- so "x := 42"
//     is an int and "x := 'a'" is a rune.
//   - A conversion in a constant expression makes the constant TYPED, and it types
//     what it is combined with: with "const one = int32(1) << 16", "50 * one" is an
//     int32 and "scale := 50 * one" declares one.
//
// A floating-point type represents the set of IEEE-754 values: float32 and
// float64. Float literals, arithmetic, comparison, and conversion to and from the
// integer types are supported; unlike integer division, float division by zero is
// not a runtime panic (it yields the IEEE infinity or NaN). Complex numeric types
// are not implemented yet.
//
// (OctoGo Specific): the Propeller 2 has no double-precision hardware, and its C
// toolchain implements C double as 32-bit. So on this target float64 has the same
// representation and precision as float32 -- 32-bit IEEE single precision, about 7
// significant decimal digits, NOT the ~15 of a 64-bit double. float64 is kept as a
// distinct type name for Go source compatibility (Go's default float), but it does
// not carry extra precision here. Programs needing more than ~7 digits must scale
// to integers.
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
// target has no heap. For the same reason a conversion that would BUILD a string at
// RUN TIME -- string(r) from a rune variable, string(b) from a byte slice -- is
// rejected. A conversion that builds nothing is free and is allowed: string(s) of a
// string, and one to or from a defined type over string, are the same bytes.
//
// A CONSTANT operand builds nothing either: string('A') is a constant string, as it
// is in Go, and folds to the literal bytes at compile time. Every spelling of a
// constant works -- a rune literal, an integer, a named constant, a constant
// expression -- and the result stands wherever a string literal does, a constant
// concatenation included:
//
//	var greet = "hi" + string('!')   // folds to "hi!"
//
// The bytes are Go's: the UTF-8 encoding of the code point, and "\uFFFD" for a value
// that is not one. An integer constant beyond U+10FFFF converts to that replacement
// rather than failing, because the conversion is to string; writing rune(1 << 40) is
// what fails, and it fails at that conversion. string(rune(0)) is a string of length
// one holding a NUL, a string here carrying its length rather than ending at one.
//
// That holds of conversions generally: one between two types of the same
// representation costs nothing and is the operand itself, while one between scalar
// types is a conversion of the value and truncates as Go says.
//
// Two DISTINCT struct types convert between each other exactly when their underlying
// types are identical -- the same fields, in the same order, with the same names and
// types -- which is Go's rule. Neither need be defined over the other:
//
//	type Point struct{ X, Y int }
//	type Vec struct{ X, Y int }
//
//	v := Vec(p)   // one shape under two names
//
// The value is COPIED, the two being separate C types however alike; a difference in
// any field's name, type or position is refused in Go's own words, "cannot convert p
// (variable of struct type Point) to type Other".
//
// A target in ANOTHER
// package is named as it is anywhere else, "geo.Celsius(20)" -- a type spelled where
// a call looks like it stands, and a conversion rather than a call for every kind of
// target: a defined scalar, an array, a slice, a struct or an interface.
//
// To build a string at run time without allocation, use the predeclared Builder
// over a caller-owned backing array -- the allocation-free counterpart to Go's
// strings.Builder:
//
//	var back [64]byte
//	sb := NewBuilder(back[:])   // a write cursor over the backing
//	sb.WriteString("value=")
//	sb.WriteRune('4')
//	s := sb.String()           // "value=4"
//
// NewBuilder(back []byte) wraps a fixed byte slice. The methods append into the free
// tail, truncating a write that would exceed the backing (the caller sized it):
//
//	WriteString(s string)   append a string's bytes
//	WriteByte(c byte)        append one byte
//	WriteRune(r rune)        append a rune, UTF-8 encoded (U+FFFD for an invalid one)
//	Write(p []byte)          append a byte slice's bytes
//	Reset()                  rewind to empty, reusing the backing
//	Len() int                the number of bytes written
//	String() string          a view of the written prefix
//
// String() returns an ordinary string aliasing the backing, so it makes no copy and
// is only valid until the next write into the same Builder, exactly as a
// strings.Builder's result is invalidated by further building. A *Builder may be
// passed to a function, so building can be factored into helpers.
//
// Intent: Builder is predeclared for now, but it is meant to become strings.Builder
// once packages exist; the method names follow Go's (Write, not WriteBytes, so a
// later Builder can satisfy io.Writer). OctoGo is pre-v1 with no compatibility
// promise, so the name and the method results (currently none, where Go returns
// (int, error)) may change; if type aliases (type T = U) arrive first, the move can
// keep the predeclared name working via `type Builder = strings.Builder`.
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
// "s, ok = append(s, x)" instead reports a full slice through ok — a bool — and
// leaves s unchanged. len(s) and cap(s) report the header's length and capacity.
//
// A whole slice is appended with Go's spread, "s = append(s, xs...)", where xs is a
// slice of the same element type; and, as in Go, a STRING spreads onto a []byte,
// "bs = append(bs, \"hi\"...)". It takes no other value beside the spread. Either the
// whole of it fits or none of it is appended: the ok form has one bool to report
// with, so a partial append would leave the caller nothing to read the truth from.
// The source and the destination may overlap, so "append(s, s...)" means what it
// means in Go.
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
// A struct type may be written where a type is wanted rather than declared with a
// name of its own -- an ANONYMOUS struct:
//
//	var p struct {
//		x, y int
//	}
//
// As in Go, two of them are the same type when their fields match, so a value of
// one may be assigned to a variable of the other.
//
// Within a struct, non-blank field names must be unique.
//
// A field written as a bare type name is EMBEDDED: its own fields and methods are
// reachable on the outer type without naming it, and it is still reachable by that
// name when you want to be explicit.
//
//	type base struct{ n int }
//
//	func (b base) Get() int { return b.n }
//
//	type derived struct {
//		base
//		d int
//	}
//
//	var x derived
//	x.n = 1        // the promoted field
//	x.base.n = 1   // the same storage, named explicitly
//	println(x.Get())
//
// Go's rule for a selector is one search over fields AND methods together: the
// shallowest wins, so a field declared on the outer type shadows a promoted one of
// the same name, and two reachable at the same depth make the selector ambiguous
// rather than one of them being picked. Both are as in Go.
//
// (OctoGo Specific): the embedded type must be a struct of this package. An
// embedded POINTER, "*base", is not supported yet -- Go promotes through it and
// panics at the selector when it is nil -- nor is an embedded predeclared type,
// "struct{ int }", nor one named through an import.
//
// # Pointer types
//
// A pointer type denotes the set of all pointers to variables of a given type,
// called the base type of the pointer.
//
//   - The value of an uninitialized pointer is nil.
//
// Dereferencing a nil pointer panics, "panic: nil pointer dereference", and halts the
// cog — one of the runtime checks, alongside an out-of-range index or slice, a
// division or remainder by zero, a shift by a negative count and appending past a
// capacity. It has to be a check rather than a trap the hardware springs: address
// zero on this target is ordinary Hub RAM, so without one a read yields whatever
// lives there and a WRITE stores into the boot area, both silently. "--unchecked"
// omits it with the rest, and then those are again what happens.
//
// A pointer to an ARRAY is the one that carries no such check, which is a limit of
// the C backend rather than a rule: it drops a store made through a pointer-to-array
// that has been through a function, so the guard would cost the write it guards.
// "p[i]" on a nil "*[N]T" therefore still reads or writes address zero.
//
// A pointer to an ARRAY, "*[3]int", is the one pointer an index applies to, and it
// abbreviates the dereference exactly as Go does: "p[i]" is "(*p)[i]", and so are
// "len(p)", "cap(p)", "range p" and "p[lo:hi]". It is how an array is passed by
// reference without a slice header:
//
//	func fill(p *[3]int) {
//		for i := range p {
//			p[i] = i
//		}
//	}
//
// The pointer is a value, so copying it aliases the same array, while dereferencing
// it -- "b := *p" -- copies the array, as assigning one does. A defined array type
// takes a pointer the same way, "*Row".
//
// No OTHER pointer is indexable, which C would not say for itself: it indexes any
// pointer as the array it is not, so "p[1]" off a "*int" would read past the
// pointee. What a pointer points at is reached by "*p", and a field of a pointed-to
// struct by "p.field".
//
// The DEREFERENCE may be written out and carry a suffix, "(*p).x" and "(*p)[i]",
// which is what Go's "p.x" and "p[i]" abbreviate. For a pointer to a slice or a
// string it is the only spelling there is, an index on the pointer itself not being
// an operation in either language:
//
//	xs := []int{1, 5, 9}
//	p := &xs
//	println((*p)[1], len(*p))   // "p[1]" is not an operation, here or in Go
//	(*p)[2] = 4
//
// The ADDRESS may be written out around a method call, "(&v).m(args)", which is the
// mirror of that and means what "v.m(args)" means: a value receiver copies what the
// pointer points at, and a pointer receiver is what "v.m()" already takes the address
// for. It is admitted around a CALL only -- "(&v)[i]" is not "v[i]", the first being
// illegal Go for a slice v while the second is not, so accepting it would let through
// a program Go refuses.
//
// A method call written that way, "(*p).m()", is the same call as "p.m()".
//
// A composite literal may be ADDRESSED, "p := &T{...}", which is how a value is made
// without naming a variable for it. Go allocates; there is no heap here, so the
// literal is given a temporary of the enclosing function and the pointer is that
// temporary's. The lifetime rule therefore applies to it as to any local -- returning
// such a pointer, storing it in a package variable, sending it, handing it to a cog,
// or passing it to a function that keeps it are each refused, and what to do instead
// is what the diagnostic says: assign the value to a package variable and use that.
// Using it in the frame that made it, or passing it to a function that returns first,
// is what the form is for and costs nothing.
//
// # Interface types
//
// An interface type defines a type set.
//
//   - A variable of interface type can store a value of any type that is in the
//     type set of the interface.
//   - The value of an uninitialized variable of interface type is nil.
//
// (OctoGo Specific): An interface value is two words -- a pointer to the value it
// carries, beside a pointer to a statically emitted vtable, one table per (concrete
// type, interface) pair. Assigning one interface value to another copies both
// words, as in Go.
//
// What goes INTO one is a pointer, and only a pointer:
//
//	var s Shape = &q      // this
//	var s Shape = q       // not this: "an interface holds a pointer here; write &q"
//
// Go accepts both, copying the value into storage it allocates. There is no heap
// here to allocate into, so the value form is refused rather than quietly made a
// reference to q -- which would differ from Go the moment anything assigned to q
// afterwards. Refusing it is what makes a program that compiles here mean exactly
// what it means in Go: "&q" puts a pointer in the interface in both languages, and
// what is written through q is visible through the interface in both.
//
// A composite literal may be addressed, "&T{...}", and is the way to put a fresh
// value in an interface. Its storage is a temporary of the enclosing function
// rather than an allocation, so the lifetime rule applies to it as to any local: an
// interface made from one may not outlive the function.
//
// ANY expression already of pointer type goes in as it stands -- there is nothing to
// address and nothing to copy:
//
//	var s Shape = New()   // a call's result
//	var t Shape = b.p     // a pointer field, an element, a chain reaching one
//
// What Go has no syntax for is taking the ADDRESS of a call's result, "&f()"; bind it
// to a variable and take that.
//
// An interface type may be written where a type is WANTED rather than declared with
// a name of its own -- as a parameter, a variable's type, a struct field -- and the
// empty one is spelled "any" as well:
//
//	func measure(s interface{ area() int }) int { return s.area() }
//
//	var e any = &q                  // the same type as interface{}
//	var f interface{} = e
//
// Identity is by METHOD SET, not by where the type was written or in what order its
// methods were listed: two anonymous interfaces with the same methods are one type,
// and a value passes between them. "any" is exactly the empty one, so it and
// "interface{}" are interchangeable.
//
// A CONVERSION names the interface where the position does not:
//
//	s := Shape(&q)        // the same two words "var s Shape = &q" builds
//	a := any(&q)          // and the empty one under the name for it
//	w := Shape(n)         // an interface whose method set covers Shape's
//
// It stands wherever an expression does -- a declaration, an assignment, an
// argument, a return, a literal's element, a send, the operand of a method call or
// a type switch. What it does not do is get past the rules the assignment obeys:
// "Shape(q)" is refused exactly as "var s Shape = q" is, and a conversion of a
// local's address reaches that local exactly as the address itself does, so it may
// no more outlive the frame.
//
// Go's method-set rule is kept, since it is what makes the address correct: a value
// of T carries the methods declared on T and *T carries all of them. Taking the
// address is therefore never the thing that makes a type fail to implement an
// interface, only the thing that makes it succeed.
//
// A type assertion recovers the pointer an interface value carries:
//
//	q, ok := s.(*sq)   // ok reports whether it held; q is nil when it did not
//	q := s.(*sq)       // panics when it does not hold, as Go's does
//
// The asserted type is a pointer type, since a pointer is what went in. It has to
// supply the interface's method set, or the assertion could never hold and the
// program says something it cannot have meant: that is Go's "impossible type
// assertion", reported here as one. One vtable is emitted per (concrete type,
// interface) pair, so the test is a pointer comparison of the second word -- there
// is no type id to read and no name to compare.
//
// A type switch asks that question several times, and each clause binds the name
// at the type that clause proved:
//
//	switch v := s.(type) {
//	case *sq:            // one type named, so v is that pointer
//		println(v.n)
//	case *rect, *circle: // several, so v keeps the interface type
//		println(v.Area())
//	case nil:            // the zero interface value, which carries no type
//		println(0)
//	default:
//		println(v.Area())
//	}
//
// That is Go's rule, and the reason a clause is a scope of its own. The name may be
// left out, "switch s.(type)", when the clauses only need to select. A case naming
// a type that could not supply the method set is Go's "impossible type switch
// case", reported here as one, as is a case named twice.
//
// A case may name an INTERFACE instead, written bare -- "case T:" where a concrete
// case is "case *X:", the star being there because what an interface holds is a
// pointer. It matches on the METHOD SET: any dynamic type implementing T takes the
// clause, and the name binds at T.
//
// Matching a SET is what makes clause order matter. Where a type satisfies two of
// them, the first clause wins:
//
//	switch v.(type) {
//	case T:   // taken for a type implementing both
//	case U:
//	}
//
// The program is closed, so "implements T" is a question this compiler answers by
// listing the types that do -- there is no run-time method lookup, only the same
// table comparison a concrete case makes, once per type that qualifies.
//
// An assertion is a value, so a suffix may be applied to it where it stands --
// "e.(*P).foo()", "e.(*P).n", "e.(*P).xs[i]" -- and each is checked against the
// ASSERTED type. That includes an assignment target, "e.(*P).n = 1", the assertion
// yielding a pointer through which the field is addressable.
//
// An interface value may be assigned to a variable of ANOTHER interface type when
// the target's method set is a subset of the source's -- widening, since anything
// the source holds already has the target's methods. It is the same two words: the
// data pointer unchanged, beside the table for the target and whatever concrete
// type the value holds. The other direction is what an assertion is for.
//
// A type ASSERTION to an interface, "s.(T)", asks the same question of one type,
// in both forms -- "t := s.(T)" panics when it does not hold, "t, ok := s.(T)"
// reports it. Written without a star for the same reason a case is: "*T" would be a
// pointer TO the interface. What comes back is another interface VALUE, not the
// pointer that went in.
//
// Generic interface constraints, unions and the underlying-type
// "~" operator belong to generics, which is a separate question entirely; an
// interface here strictly defines a method set.
//
// An interface may EMBED another, written as its name standing alone, and takes
// that one's methods as its own:
//
//	type Z interface {
//		T
//		U
//		baz() int
//	}
//
// Two embedded interfaces may declare the same method, which is one method -- a
// table has one slot per name. The name embedded may be declared anywhere in the
// package, before or after. An interface that embeds itself, directly or through
// others, defines its method set in terms of itself and is refused; embedding a
// type that is not an interface is Go's type-constraint syntax, which belongs to
// generics and is refused as well. Only a same-package name may be embedded.
//
//	InterfaceType = "interface" "{" { MethodSpec ";" } [ MethodSpec ] "}" .
//	MethodSpec = identifier [ "(" [ ParameterList ] ")" [ Type | "(" ResultList ")" ] ] .
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
// A channel's storage is static wherever it is declared. A local declaration binds
// the variable to a cell belonging to that declaration *site*, not to the call, and
// the cell's lock is taken once before the program starts. So a channel may be
// passed to a goroutine or sent through another channel without any question of
// whether the declaring function has returned -- which is what makes the ordinary
// "var ch chan T; go worker(ch)" safe here. The consequence of a per-site cell is
// that two concurrent calls of one function share its channel rather than each
// having one; the hardware bounds channels to the 16 locks in any case.
//
// A receive may stand alone as a statement, "<-ch", discarding the value. The
// receive still happens, so on a rendezvous channel that is how one goroutine
// waits for another.
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
// An initializer must have the declared type, exactly as an assignment's right-hand
// side must; an untyped constant takes the declared type, and is bounded by it. The
// element type of an array or a slice is part of this, so writing the wrong type
// into an element is reported wherever the container was declared.
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
// The variable takes the type of its initializer, and a named one is carried over
// in full, so "p := P{1, 2}" is checked exactly as "var p P = P{1, 2}" is -- its
// fields, its methods and the types they take. The four forms that name a type are
// a composite literal, the address of one, a copy of a variable that has one, and
// a call whose single result has one:
//
//	p := P{1, 2}      // P
//	p := &P{1, 2}     // *P
//	q := p            // P
//	p := mk()         // whatever mk returns
//
// # Redeclaration Rules
//
// Unlike regular variable declarations, a short variable declaration may
// redeclare variables provided they meet all of the following conditions:
//
//   - They were originally declared earlier in the same block (or the parameter
//     lists if the block is the function body).
//   - They are declared with the same type.
//   - At least one of the non-blank variables in the identifier list is new. A left
//     side that is nothing but blanks therefore introduces nothing and is rejected,
//     wherever the declaration stands -- a statement of its own, an init statement,
//     a range variable, or a select's short receive.
//
// As a consequence, redeclaration can only appear in a multi-variable short
// declaration. Redeclaration does not introduce a new variable; it merely
// assigns a new value to the original variable. Short variable declarations
// may appear only inside functions.
//
// A declared variable must be used, as in Go, and it does not matter where the
// declaration stands: the ones inside a statement's header count too -- an "if",
// "switch" or "for" init statement, a range variable, and the one a select's
// "case v := <-ch" receives. A parameter and a result are not variables in this
// sense and may go unused, again as in Go. The one form outside the rule is the
// ":=" switch guard with no init statement, described under Switch Statements,
// where the name declared is what the switch switches on.
//
// # Constant Declarations
//
// A constant declaration binds an identifier to the value of a constant
// expression.
//
// A ConstSpec binds a list of identifiers to a list of expressions, which must be
// of equal length. Within a parenthesized group a ConstSpec may omit its expression
// list, in which case it repeats the previous spec's, positionally, together with
// its type. iota is a predeclared integer constant equal to the zero-based index of
// the ConstSpec in its group -- of the spec, not of the name, so every name on one
// line sees the same iota -- and a repeated expression therefore takes a new value
// at each spec.
//
//	ConstDecl = "const" ( ConstSpec | "(" { ConstSpec ";" } [ ConstSpec ] ")" ) .
//	ConstSpec = IdentifierList [ Type ] [ "=" ExpressionList ] .
//
// # Type Declarations
//
// A type declaration binds an identifier, the type name, to a type.
//
// It must stand at PACKAGE SCOPE. A type declared inside a function is refused,
// "statement TypeDecl is not supported yet", though Go admits one.
//
// The ALIAS form, "type A = B", parses and is then treated as a definition: the "="
// is read and discarded, so A is a distinct type rather than another name for B, and
// "var i int = a" over "type A = int" is refused where Go accepts it. The two
// spellings should differ and do not; write the definition form and mean it until
// they do.
//
//	TypeDecl = "type" ( TypeSpec | "(" { TypeSpec ";" } [ TypeSpec ] ")" ) .
//	TypeSpec = identifier [ "=" ] Type .
//
// A defined type is a distinct type that may carry methods, and it has the
// representation of the type it is defined over -- so a value of "type Name string"
// prints as a string, carries a length, indexes to a byte, slices, ranges over its
// runes and compares as one, and a value of "type List []int" is a slice in every
// one of those ways. Only its identity differs: which methods it has, and that it
// is not the same type as what it is defined over.
//
// A defined type over a struct is written and used as that struct is -- its fields
// are read and set, a literal fills it the same way, and it compares -- and it is a
// distinct type all the same, exactly as one over an int is. It passes, assigns,
// returns, is sent and compares only where its own name is wanted; a conversion is
// what carries a value across, in either direction, and copies the fields as any
// struct value does. Two names over one shape are two types whether or not either
// was defined over the other.
//
// It is also type-checked as the type it is defined over, following a chain of
// definitions to reach it: a value of one is bounded, converted, compared and passed
// as that type's values are. Its own name is what a diagnostic says and what carries
// its methods, so "var c Celsius = \"a\"" reads as Celsius, not as int.
//
// A defined type over an ARRAY carries methods too, in both receiver forms, and a
// value receiver is a COPY as Go's is. It travels as a pointer -- a parameter of
// array type corrupts unrelated code on this target -- and the method copies from it
// on entry, so writing to a value receiver leaves the caller's array alone. The
// receiver may be a variable, a package-level one, a pointer to either, a written out
// "(&v).m()", or a struct FIELD of the type -- "h.f.sum()", at any depth and through
// a pointer to the struct. An array ELEMENT is the one that is not wired up:
// "pool[1].m()" over a "[2]Row" is refused, an array of a defined array type being
// resolved to its extents, so nothing of the element type's name survives to hang a
// method on.
//
// A defined type over a channel is a channel too: a send, a receive and a select
// clause all reach it. The one thing such a type gives up is a method of its own,
// which is refused where it is written -- it is answered for by the channel cell's
// own name in the emitted C, and so has no type there to hang a method on.
//
// # Function and Method Declarations
//
// A function declaration binds an identifier to a function. If a Receiver is
// provided, it acts as a method declaration binding the function to the
// receiver's base type.
//
//	FuncDecl       = "func" [ Receiver ] identifier Signature [ Block ] .
//	Signature      = "(" [ ParameterList ] ")" [ Type | "(" ResultList ")" ] .
//	Receiver       = "(" ParamDecl ")" .
//	ParameterList  = ParamDecl { "," ParamDecl } [ "," ] .
//	ResultList     = ParamDecl { "," ParamDecl } [ "," ] .
//	ParamDecl      = "..." Type | Type [ "..." ] [ Type ] .
//	IdentifierList = identifier { "," identifier } .
//
// The final parameter may be written "...T", which makes the function variadic: it
// takes the rest of the call's arguments, however many, and inside the body the
// parameter IS a []T -- len, cap, range and indexing all ask a slice. A call may
// supply none of them. Only the final parameter may be one, only one name may share
// it ("a, b ...int" would make both variadic), and a result never is.
//
// A call passes an existing slice instead of values by writing "f(xs...)".
//
// (OctoGo Specific): Go allocates the pack a call builds. There is no heap here, so
// it is an array of the CALLING function, which the lifetime rules see exactly as
// they see a slice literal's backing: a callee that lets its variadic parameter
// outlive the call is refused, and told to take a slice of a package array instead.
// The spread form is judged by where its slice came from, as any slice argument is.
//
// A parameter or result list may name its entries or leave them unnamed, but not
// both: "(a, b int)" and "(int, int)" are the two-entry forms, while
// "(a int, string)" is illegal. Each ParamDecl is one type, optionally preceded
// by a name; the whole list is named when any ParamDecl carries a name, in which
// case a bare ParamDecl is a name sharing the next named entry's type
// ("(a, b int)"). Parameters and results share this grammar; an unnamed parameter
// is simply one the body does not refer to.
//
// A Receiver is a single ParamDecl, so it too may be named or unnamed: "(p Point)"
// binds the receiver to p, while "(Point)" and "(*Point)" declare a method whose
// body does not name its receiver -- the natural spelling for a method on an
// empty struct or any type used only to group behaviour. Reusing ParamDecl keeps
// the choice LL(1): the "(" Type" prefix is shared, and a second Type (the base
// type after the name) is what distinguishes the named form, exactly as for a
// parameter.
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
//	Factor     = identifier [ FactorSuffix ] [ CompositeLit ]
//		| int_lit
//		| float_lit
//		| string_lit
//		| rune_lit
//		| "(" Expression ")" [ FactorSuffix ]
//		| "[" [ Expression ] "]" Type [ CompositeLit [ FactorSuffix ] ]
//		| "chan" Type
//		| FuncLiteral [ FactorSuffix ] .
//	CompositeLit = "{" [ ElementList ] "}" .
//	ElementList  = Element { "," Element } [ "," ] .
//	Element      = CompositeLit | Expression [ ":" ElementValue ] .
//	ElementValue = CompositeLit | Expression .
//
// A list may end with a trailing comma, which is what lets it be written across
// lines. The four that take one are the four Go allows it in -- a composite
// literal's elements, a call's arguments, a parameter list and a result list --
// and it is what the formatter writes for a list it spreads over lines, dropping
// one whose list closes on the same line. The lists Go does not allow it in are
// unchanged here: an assignment's right-hand side, a "return", a list of names.
//
// A composite literal's type may be qualified, "pkg.T{a, b}", naming an exported
// struct type of an imported package. The rules another package's names follow
// apply inside it: the type has to be exported to be named at all, a key has to
// name an exported field, and a positional literal, which fills every field in
// order, is refused outright for a struct that has an unexported one.
//
// A composite literal "T{a, b}" builds a value of the named struct type from its
// fields in declaration order. An Element may instead carry a key naming the field
// it fills, "T{b: 2}", in which case every Element must: the two forms may not be
// mixed, because once one Element names its field, position stops meaning anything.
// A keyed literal may name any subset of the fields in any order, and a field it
// does not name takes its zero value.
//
// An element value that is itself a composite literal may elide its type when that
// type is implied by position -- the element type of an array or slice literal, or
// a field's type in a struct literal -- so "[]P{{1}, {2}}" means "[]P{P{1}, P{2}}"
// and "Outer{{5}}" means "Outer{Inner{5}}". Because a "{" cannot begin an
// Expression, the elided form is distinguished from a keyed or ordinary element with
// one token of lookahead, keeping the grammar LL(1).
//
// (OctoGo Specific): Go admits the elision only inside an array, slice or map
// literal, and reports "missing type in composite literal" for a struct literal's
// field value. OctoGo takes it there too, for a struct-typed field and an
// array-typed one alike. The written form is what both languages accept, so a
// program that spells the type out is portable.
//
// A bracketed type may carry one too, giving an array literal "[N]T{a, b}" or a
// slice literal "[]T{a, b}". Elements may be positional, indexed ("[5]int{0: 1,
// 4: 9}", "[]int{2: 5}") or a mixture ("[]int{1, 4: 9}"); an index must be a
// constant, and the elements it skips are zeroed. An array literal may supply
// fewer values than its length, zeroing the rest, and no more; a slice literal's
// length and capacity are its highest index plus one.
//
// An array literal may stand as an element of a composite literal too, filling an
// array-typed field or an element of an array of arrays, since a nested aggregate
// is written where it stands, and either may be the operand of a "range", which
// binds it to a local of its own.
//
// A slice literal may stand anywhere a value may, since a slice is a header: it is
// bound to a local declared ahead of the statement, which is where its backing
// array comes from, and so has that local's lifetime -- returning one, storing one
// in a package variable, handing one to another cog or sending one on a channel is
// refused, exactly as it is for a slice of a local array.
//
// An ARRAY literal stands in every one of those positions as well: as an
// initializer, as an element of another literal, as a "range" operand, as an
// argument, as a result, as the operand of an index, and -- since 2026-08-16 -- in
// an "append" argument and a channel send. The last two used to be refused, on the
// ground that C has no array value for the literal to become; it has one, the
// compound literal "(T){a, b}", which a literal of a DEFINED array type had been
// emitting there all along. The unnamed spelling was refused only for having no name
// to write, and the compiler mints that name.
//
// What may NOT stand in those two positions is a call RETURNING an array. Bind the
// result to a variable and use that, which is what the diagnostic asks for.
//
// That declaration may be at package scope as well as inside a function, with the
// type written or inferred, which is how a program states a lookup table:
//
//	var sizes [4]int = [4]int{1, 2, 4, 8}
//	var primes = []int{2, 3, 5, 7}
//
// A package-level table is laid out statically, so it costs no start-up work: an
// array is a static array, and a slice is a static backing array plus a header
// over it.
//
// An array literal of more than one dimension nests, each element being a literal
// of the row type. That row may be written with its type or with it elided, as Go
// allows, and the elision is the usual form:
//
//	var m = [2][3]int{{1, 2, 3}, {4, 5, 6}}
//
// A row shorter than its extent zeroes the rest, and an outer index that skips a
// row zeroes that row entirely, both following the one-dimensional rule. A slice
// of arrays IS supported, its element reached through a pointer to an array; the
// helpers that would take such an element by value take a pointer instead, a
// function parameter of array type corrupting unrelated code on this target (see
// doc/array-param-corrupts.c). An array of slices is supported too, each element
// being an ordinary slice header.
//
// A function PARAMETER of array type follows the same rule and is written as Go
// writes it, of any rank -- "func f(m [3][2]int)". It travels as a pointer and the
// callee copies from it into a local of its own, so it is the value Go passes; the
// pointer is how it crosses, not what it means.
//
// A row of such an array may be sliced -- "m[i][:]", or any sub-range of it --
// giving a slice over the row's own storage, so a write through it is a write to
// the array. The ARRAY ITSELF may be sliced the same way: "m[:]" over a [4][2]int
// is a [][2]int over that storage, the elements being its rows. Rank is no limit on
// either -- a row of a [2][3][4]int is a [3][4]int and slices to a [][4]int -- and
// the element of the result is named by the generated typedef above, so a slice
// made by slicing and one made by a literal are the same type and interchange.
//
// Every base a slice expression takes reaches this: a variable, a pointer to an
// array, a struct field, and a row reached through a chain. That is what makes the
// idiom for a slice with no heap -- a package-scope backing array, sliced where it
// is used -- available for an array element as for every other.
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
//		| "[" [ Expression ] "]" Type [ CompositeLit ]
//		| "chan" Type
//		| FuncLiteral .
//
// A slice or array type may appear as a Factor so that the type argument such as
// the "[]int" in "make([]int, 0, cap)" parses. A bare type used as a value is
// rejected by the semantic checker, as is new; make is accepted only for a slice
// with a constant capacity (see Slice types).
//
//	FactorSuffix = { Selector | Index | CallSuffix } .
//	Selector     = "." ( identifier | "(" ( "type" | Type ) ")" ) .
//	Index        = "[" ( Expression [ ":" [ Expression ] [ ":" [ Expression ] ] ]
//		| ":" [ Expression ] [ ":" [ Expression ] ] ) "]" .
//
// A single-expression Index "a[i]" indexes an element. The colon forms are slice
// expressions "a[low:high]", "a[low:]", "a[:high]" and "a[:]", which create a new
// { pointer, length, capacity } view over the operand's storage; an omitted low
// bound is 0 and an omitted high bound is the operand's length. For a slice operand
// the high bound may reach its capacity rather than only its length, and the
// result's capacity is the operand's capacity less low. Slicing a string yields a
// string (a string has no capacity).
//
// A third bound, "a[low:high:max]", sets the result's capacity to max less low
// instead, so that appending to it stops there rather than running on into storage
// the operand shares with someone else. That is the way to hand out a region of a
// package-level buffer, there being no heap to allocate one from:
//
//	var pool [256]byte
//
//	head := pool[0:0:64]    // appending stops at 64, not at 256
//	tail := pool[64:64:128]
//
// This form writes both of the bounds it follows: "a[l::m]" and "a[l:h:]" are not
// slice expressions. A string has no capacity to set, so it does not take one.
//
// The bounds must satisfy 0 <= low <= high <= max <= capacity. Constant bounds
// against an operand whose extent is known at compile time -- an array's -- are
// checked there and refused, as in Go; anything else is checked as the program runs
// and is a panic, the way an out-of-range index is. Bounds a compile-time extent
// settles as being in range carry no check at all. Each bound is evaluated once.
//
// A slice expression may itself be indexed or sliced again, "a[1:6][2]" and
// "a[1:6][1:4]", the index being checked against the length of the expression it
// applies to rather than the operand's. The same holds for a slice reached through
// an index, "s[i].v[j]", read or written.
//
// The one operand that cannot be sliced this way is a row of a multi-dimensional
// array reached through an index, "m[0][:][1]": what the slice would view has to be
// named before the steps after it can be written, and an array is the one value
// with no C type to name it by. "m[0][:]" on its own is fine.
//
// # Function types and function values
//
// A function type "func" Signature denotes the set of functions with that
// signature. Parameter and result names are no part of it, so "func(a int) int"
// and "func(b int) int" are one type.
//
// A declared function's name used as a value has the function's own type. It may be
// assigned to a variable, passed as an argument, returned as a result, or stored in
// an array element or a struct field, and a call through any of them is a call:
//
//	func add(a int, b int) int { return a + b }
//
//	type op struct {
//		fn   func(int, int) int
//		name string
//	}
//
//	func run(f func(int, int) int, a int, b int) int { return f(a, b) }
//
//	var chosen func(int, int) int   // the zero value is nil
//
//	func main() {
//		chosen = add
//		println(chosen != nil, chosen(1, 2), run(add, 3, 4))
//		o := op{add, "add"}
//		println(o.fn(5, 6))
//	}
//
// The zero value is nil and may be compared with nil. A function of another type is
// not assignable; the check is by signature, so it holds however the two were
// written. A function value transpiles to a C function pointer -- it costs nothing
// at run time and allocates nothing, the function being already there and only its
// address travelling -- which is also why it is always safe to send one to another
// cog: it names code, not the frame it was made in.
//
// A function with more than one result is a value like any other: the struct its
// results travel in is named after the result TYPES, so two functions of one
// signature return the same C type and the signature has something to name. A "go"
// statement takes a value too, "go g(21)" -- a cog's entry point is generated per
// function TYPE in that case, and the pointer travels in the argument block.
//
// A method value, "gp.bump", is taken with its receiver BOUND: the compiler lifts
// it to a function of its own that names the receiver, so the value stays an
// ordinary one-word function pointer and costs nothing that any other function
// value pays.
//
// Two forms are refused, and neither is an omission:
//
//   - a VALUE-receiver method. Go copies the receiver at the moment the value is
//     made, and there is no heap to copy into; binding the address instead would
//     alias the variable, so the program would answer differently the moment
//     anything wrote to it.
//   - a receiver that is not a package-level variable, whose address does not
//     outlive the value.
//
// Go carries the receiver IN the value instead, which handles any receiver. The
// representation that would do that here -- a function value pointing at a struct
// whose first word is the code pointer -- costs about a quarter of the time of every
// call through a function value on this target, measured rather than guessed. See
// doc/funcval-cost.c, which also records how to revisit it.
//
// # Function Literals
//
// A function literal represents an anonymous function.
//
//	FuncLiteral = "func" Signature Block .
//
// (OctoGo Specific): Because OctoGo strictly enforces a zero-allocation memory
// model without a Garbage Collector, function literals cannot act as dynamic
// closures. A literal MAY NOT read a local or a parameter of the surrounding
// function -- there is no heap to hold a captured frame, and no frame that
// outlives the call, so the pointer would be the only honest part of a closure.
// Doing so is refused where it is written:
//
//	k := 5
//	f := func(a int) int { return a * k }   // a function literal may not capture k
//
// A package-level name is not a capture: it is there for every function, and a
// literal reads one as any function does.
//
// Each literal is lifted to a function of file scope with a name of the compiler's
// choosing, and the expression becomes that name -- so what a literal costs is a
// function pointer and nothing else. It may be bound to a variable, handed to a
// parameter, returned, stored in a field or an element, and called where it stands:
//
//	println(func() int { return 7 }())
//
// A literal may stand as the callee of "go" or "defer" -- both take a declared
// function, and a lifted literal is one:
//
//	defer func() { ... }()
//	go func() { ... }()
//
// Neither takes arguments yet. A cog usually wants none: what it shares, it shares
// through a channel.
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
//     (*, /, %, <<, >>, &, &^).
//   - SimpleExpr (AddOp): Addition, subtraction, and bitwise operators (+, -,
//     |, ^).
//   - Expression (RelOp): Comparison operators (==, !=, <, <=, >, >=).
//
// Equality is defined on more than the scalars. Two strings compare by content,
// two structs field by field, and two arrays element by element, each through a
// helper the compiler emits for that type; ordering ("<") is defined on the
// numeric types and on strings, which compare lexicographically, but not on
// structs or arrays. An array's element type must itself be comparable, and a
// slice may only be compared with nil, never with another slice.
//
// The bit-clear operator "a &^ b" is Go's AND NOT: the bits of a that b does not
// have set. Until it was made an operator of its own it still computed the right
// answer, because "&^" lexes as "&" followed by the unary complement "^" and "a &
// ^b" is the same value -- but the two tokens were what the formatter then wrote
// back, rewriting a program's "&^" into "& ^". C has no such operator, so it
// lowers to "a & ~(b)".
//
// (Note: the logical operators && and || sit at the RelOp level in the grammar,
// alongside the comparisons rather than below them. The grammar is therefore
// looser than the language: precedence is imposed by the checker and the emitter,
// which read the flat operand/operator chain as Go groups it, so "x > 0 && x < 10"
// joins two comparisons rather than comparing 0 with x. Both short-circuit, as
// their C counterparts do).
//
//	UnaryOp    = "+" | "-" | "!" | "^" | "*" | "&" | "<-" | "~" .
//	RelOp = "==" | "!=" | "<" | "<=" | ">" | ">=" | "&&" | "||" .
//	AddOp = "+" | "-" | "|" | "^" .
//	MulOp = "*" | "/" | "%" | "<<" | ">>" | "&" | "&^" .
//
// A slice must not outlive the storage it views. Returning one whose backing array
// is a local of the frame is refused, as is storing one in a package-level variable
// or a field of one, which outlives every call: the header would point at the
// frame's storage long after it is gone, and there is no heap to promote that
// storage to. These are the slice counterparts of the same two refusals for a local
// variable's address. A slice over a package-level array or slice, or one reached
// through a parameter -- the caller's -- travels freely.
//
// Handing one to another Cog is refused too: a slice backed by a local may not be
// an argument of a go statement, nor be sent on a channel. The first two refusals
// are about a reference that outlives its referent; this one is about a reference
// that leaves the frame's control. A goroutine runs until it returns and the go
// statement says nothing about when that is, and a receiver keeps what it took long
// after the rendezvous that delivered it -- so either may read the backing array
// once the frame that owned it has returned. The same applies to a local's address:
// "go f(&x)" and "ch <- &x" are refused where "f(&x)" and "defer f(&x)" are not,
// because an ordinary call returns before the frame does and a deferred one runs on
// the way out of it.
//
// What that leaves is the shape to write: give the buffer a lifetime at least as long
// as the goroutine's by declaring it at package scope, and hand the Cog a slice of
// it.
//
//	var buf [64]byte
//
//	func main() {
//		var done chan int
//		go fill(buf[:], done)   // buf outlives every frame
//		<-done
//	}
//
// A parameter may cross: whose storage it is, is the caller's business. The
// requirement travels there instead. A function that lets one of its parameters reach
// another Cog -- itself, or by passing it to a function that does -- may only be
// called with storage that outlives the goroutine, and it is the call that is
// refused:
//
//	func spawn(p []int) { go work(p) }   // parameter p reaches another Cog
//
//	func setup() {
//		var local [4]int
//		spawn(local[:])             // refused here, where the storage was chosen
//	}
//
// That holds however many calls separate the two, across package boundaries, and
// through mutual recursion.
//
// The same summary answers a second question: whether a function KEEPS what it is
// given. A store through a pointer parameter -- "h.d = d" in a function taking an
// "h *H" -- or the same store through a method's receiver puts the reference in
// storage the callee did not choose and cannot see the lifetime of. Whether that
// outlives the caller's frame is the CALL's business, so it is the call that is
// judged, and one function is both fine and refused depending on what it is handed:
//
//	func fill(h *H, d []int) { h.d = d }
//
//	func setup() {
//		var a [4]int
//		var local H
//		fill(&local, a[:])   // fine: the struct and the backing die together
//		fill(&g, a[:])       // refused: g outlives the frame a lives in
//	}
//
// The question is asked per parameter, so a function that stores one argument and
// merely measures another constrains only the one it stores. As with the Cog
// crossing, it holds however many calls separate the two.
//
// A call through an INTERFACE is judged the same way, against every implementation
// at once. Which one it reaches is not known until it runs, so the requirements of
// all of them apply: if any implementation of the method keeps what it is given, a
// frame-backed argument is refused at the call, whichever value the interface
// happens to hold. That refuses some programs a reader can see are safe, and it is
// the conservative half of a choice whose other half is a dangling reference. An
// interface whose implementations keep nothing constrains nothing.
//
// A reference must not outlive the BLOCK of the variable it points at, which is a
// finer question than outliving the function and matters for the same reason. Go
// gives a for statement's variable a fresh instance per ITERATION, and a variable
// declared in a loop body has been a fresh one per iteration since Go 1.0; keeping
// a reference to either past the iteration would need one instance per iteration, of
// a count not known until the loop runs. That is an allocation, and this target has
// no heap, so it is refused -- exactly as new and a map are refused:
//
//	for i := 0; i < 3; i++ {
//		ps[i] = &i          // refused: ps outlives the iteration i belongs to
//		x := i * 10
//		ps[i] = &x          // refused for the same reason
//		bump(&i)            // fine: the address does not outlive the call
//	}
//
// The rule is what makes the loop variable mean here what it means in Go rather than
// quietly meaning what it meant before Go 1.22. Where a reference does not outlive
// the iteration, one instance and a fresh one are indistinguishable, so every
// program that compiles has Go's meaning; the programs that would tell them apart
// are the ones that need the heap. It applies to the same doors the function-level
// rule does: a store, a store through a pointer parameter, and a store into a
// method's receiver.
//
// A reference wrapped in a struct counts as one. Assigning a local's address or a
// slice of a local array to a field -- or filling the field in a composite literal --
// marks the variable, and a copy of it carries the mark, so returning it, storing it
// in a package variable or handing it to another Cog is refused just as the bare
// reference would be:
//
//	var a [4]int
//	var b buf
//	b.data = a[:]
//	go work(b)                          // refused: b holds a pointer into local a
//
// The mark is on the variable, not on the field, and it is never cleared. That is
// what makes it safe without tracking each field separately -- a struct with one
// field holding a frame reference and one not must stay marked -- and it is the one
// place the rule refuses more than it strictly must: a variable whose only such field
// is later overwritten with package-level storage stays marked. Using the
// package-level storage from the start is the answer, and it is what the program
// wanted anyway.
//
// A call that returns a struct may have a field selected from its result,
// "mk().y", a method called on it, "mk().sum()", and its result indexed,
// "mk()[1]" or "mk().d[1]".
//
// A call's arguments are evaluated left to right, before the call. Where an
// argument can change state -- it calls something, or receives from a channel --
// each argument is evaluated into a temporary in that order, because C leaves the
// order unspecified and the compilers this passes through do not agree on it.
//
// # Function Calls
//
// Given an expression f of function type, f(a1, a2, … an) calls f with
// arguments a1, a2, … an. Arguments must be single-valued expressions
// assignable to the parameter types of the function and are evaluated before
// the function is called.
//
//	CallSuffix = "(" [ ArgumentList ] ")" .
//	ArgumentList = Expression { "," Expression } [ "..." ] [ "," ] .
//
// The trailing "..." spreads a slice into a variadic parameter, "sum(xs...)",
// instead of packing the arguments written. It is legal only in a call to a
// variadic function, and the slice's element type is the parameter's.
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
//	append(s, x…)       append one or more elements to a slice
//	copy(dst, src)      copy elements between two slices, returning the count
//	clear(s)            set every element of a slice to its zero value
//	min(x, y, …)        the smallest of its ordered arguments
//	max(x, y, …)        the largest of its ordered arguments (ordered: an
//	                    integer, a float or a string)
//	panic(s)            abort with a string message
//	print(args…)        write the arguments to the serial console
//	println(args…)      like print, but space-separated and newline-terminated
//	printf(f, args…)    write the arguments under the control of a format
//
// The names are predeclared in the universe block, so a local or package-level
// declaration of the same name shadows the built-in — min, max and clear are the
// likely collisions.
//
// make and append are where the fixed-memory model shows through. make performs
// no heap allocation: it reserves a backing array whose size is fixed at compile
// time, so n and m must be constants, and it is admitted only as the initializer
// of a slice variable, "var s []T = make([]T, n, m)"; the two-argument form
// "make([]T, n)" sets the capacity equal to the length. The type may be written as
// a DEFINED slice type rather than as the "[]T" shape — "var d List = make(List, n,
// m)" over "type List []int", following a chain of definitions — and the variable
// then has that type, methods and all. append takes one or more
// elements, appending each in turn — or a whole slice with Go's spread,
// "append(s, xs...)", a string spreading onto a []byte — and cannot grow a slice
// past its capacity, so it has two forms: the one-result form "s = append(s, x)"
// traps at run time if an element does not fit, while the two-result form
// "s, ok = append(s, x)" never traps and reports through ok whether the element was
// appended. copy copies
// min(len(dst), len(src)) elements between two slices of the same element type —
// which may overlap — and yields that count. clear zeroes a slice's elements in
// place. panic takes a string, writes "panic: " and that message to the serial
// console and halts the cog; with --release it reboots the board instead.
//
// print, println and printf are the only I/O built-ins; they write to the board's
// serial output. print and println each take any number of arguments, either
// scalar values or a whole slice or array of a scalar element type. println
// separates its arguments with a space and ends with a newline; print writes them
// adjacently with no terminator. A value that does not print as itself — a struct
// — is refused, as Go refuses it; a pointer, a func value and an interface print
// as an address, the interface as its two words, as in Go.
//
// printf writes its arguments under the control of a format, which must be a
// CONSTANT string: there is no heap to build one in, and a format known here is
// what lets every verb be checked against its argument at compile time rather than
// going wrong on the board. It is the built-in fmt.Printf would be if there were a
// fmt package, and it formats as fmt does:
//
//	%d %x %X    an integer, in decimal or hexadecimal
//	%s          a string
//	%t          a bool, as the word true or false
//	%f          a float, fixed-point
//	%c          the character an integer names, encoded as UTF-8
//	%v          the value in its default form — what println would print, down to
//	            "[1 2 3]" for a slice. Not a pointer, a func value, an interface
//	            or a struct: fmt renders those differently from the built-in
//	            println, and this does not render them yet
//	%T          the value's type
//	%%          a literal percent
//
// A verb may carry fmt's flags, width and precision — "%6.2f", "%-8s", "%+05d",
// "%.3s" — which mean what they mean in fmt. For a string that is a count of RUNES,
// not of bytes: "%.1s" of "héllo" is "h" and never half of a character. The "%*d"
// forms, which take the width from an argument of their own, are not accepted: the
// verb count is what pairs each verb with an argument to check it against.
//
// Two flags are refused because the C backend ignores them, and a program that
// compiles here is meant to mean what it means in Go rather than approximately
// that: "#", which would write a base prefix, and "0" on a float, which would pad
// with zeros — "%08.3f". "0" on the integer verbs is honoured, so "%05d" is fine.
//
// Two verbs do not take a width yet, and say so where they are written: %v, whose
// rendering is the built-in println's and does its own spacing, and %x of a SIGNED
// integer, which prints as a sign and a magnitude here — Go puts the fill on
// different sides of that sign depending on the flag, and getting it subtly wrong
// would be worse than declining. %x of an unsigned integer takes a width.
//
// A verb that does not suit its argument, an unknown verb, and a count of verbs
// that does not match the count of arguments are each refused where the call is
// written.
//
// Each verb renders as fmt does rather than as C does, where the two differ: %x of
// a negative integer is a sign and a magnitude, "-ff", not the two's complement C
// would print; %c writes the UTF-8 encoding of the character an integer names, not
// one byte of it.
//
// %T is answered at compile time for everything but an interface, whose dynamic
// type is read from the value at run time and costs one pointer. A type prints
// unqualified — "Celsius", where Go would print "main.Celsius" — there being no
// package clause here to qualify it with. An interface holding nothing prints
// "<nil>".
//
// The other Go built-ins are recognized by the checker but not yet emitted:
// close, complex, delete, imag, real and recover each report "the X built-in is
// not supported yet". The exception is new, which — together with every make form
// other than the slice form above — is rejected outright as "dynamic allocation
// not supported", a heap having no place on the target.
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
//		| "break" [ identifier ]
//		| "continue" [ identifier ]
//		| "fallthrough"
//		| "return" [ ExpressionList ]
//		| "go" ( AssignHead | FuncLiteral ) { Selector | Index | CallSuffix }
//		| SwitchStmt
//		| SelectStmt
//		| "<-" Expression
//		| AssignHead Postfix
//		| "defer" ( AssignHead | FuncLiteral ) { Selector | Index | CallSuffix }
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
//	for i, v = range xs { ... }    // the same, into variables that already exist
//	for range n { ... }            // repeat n times, no variable
//
// Ranging an integer yields only the index; the two-variable form is available
// for a slice, an array or a string, where the second variable is a copy of the
// element -- for a string, the rune (see the range clause below). Ranging a
// channel is not provided: it has no close.
//
// A clause written with "=" rather than ":=" assigns variables that already
// exist instead of declaring new ones. They are written at the top of each
// iteration, so after the loop they hold the last index and element, and a
// "break" leaves them at the iteration it broke on. Each such target must be a name
// or a struct FIELD; an element target is not supported, indexing rendering a
// bounds-checked read rather than a place to write.
//
// (OctoGo Specific): To stay LL(1), a header is parsed as an expression first
// and what follows it decides how to read it: a "{" makes it the condition, and
// a ";" or an assignment operator makes it the init statement of the three-clause
// form. This is the same left-factoring SwitchGuard uses, and it is why the
// grammar spells the header out rather than naming the three parts directly.
//
// # Break and Continue Statements
//
// A "break" statement terminates execution of the innermost enclosing "for",
// "switch" or "select" statement. A "continue" statement begins the next
// iteration of the innermost enclosing "for" statement -- only a loop, so a
// switch or a select is not something it can name. Both appear in the Statement
// production above.
//
// Either statement may name an enclosing labeled statement, and then acts on that
// one instead of the innermost: "break Label" leaves the labeled "for" or "switch",
// and "continue Label" begins the next iteration of the labeled "for". A label is
// an identifier prefixing a statement, "Label:", as in Go -- syntactically it is a
// ":" continuation of the leading identifier, so the grammar stays LL(1). The
// labeled statement a "break" names must be an enclosing "for" or "switch", and the
// one a "continue" names must be an enclosing "for"; a label that names neither, or
// one that is not in scope, is rejected.
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
// A return evaluates its expressions and assigns them to the results first, and the
// deferred calls run after that -- so a deferred call cannot change what a return
// expression computed, and it can still change a NAMED result, which is what the
// caller then receives.
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
//		| { "," LhsItem } ( "=" | ":=" ) ExpressionList
//		| ":" Statement .
//	AssignOp   = "+=" | "-=" | "*=" | "/=" | "%="
//		| "&=" | "|=" | "^=" | "&^="
//		| "<<=" | ">>=" .
//	LhsItem    = AssignHead { Selector | Index } .
//	ForHeader  = ";" [ HeaderExpression ] ";" [ ForPost ]
//		| "range" HeaderExpression
//		| HeaderExpression [ ForRest ] .
//	ForRest    = ";" [ HeaderExpression ] ";" [ ForPost ]
//		| ( "=" | ":=" ) ForAssignRest
//		| "," HeaderExpression { "," HeaderExpression } ( "=" | ":=" ) ( "range" HeaderExpression
//			| HeaderExpression { "," HeaderExpression } ";" [ HeaderExpression ] ";" [ ForPost ] ) .
//	ForAssignRest = "range" HeaderExpression
//		| HeaderExpression ";" [ HeaderExpression ] ";" [ ForPost ] .
//	ForPost    = HeaderExpression { "," HeaderExpression }
//		[ ( "=" | ":=" ) HeaderExpression { "," HeaderExpression } | "++" | "--" ] .
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
// A target may be anything a single assignment can write to -- a variable, a
// struct field, an element, a dereferenced pointer -- so "xs[i], xs[j] = xs[j],
// xs[i]" swaps two elements. A ":=" is the exception: it declares names, so every
// target of one must be a name.
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
//	IfStmt = "if" HeaderExpression [ IfInit ] Block [ "else" ( IfStmt | Block ) ] .
//	IfInit = { "," LhsItem } ":=" HeaderExpression ";" HeaderExpression .
//
// An "if" may carry an init statement, "if v := f(); v > 0". The name it declares
// is scoped to the whole statement -- the condition, the "then" block and every
// branch of an "else if" chain -- and not beyond it, so it may shadow a name from
// outside without disturbing it. Only a ":=" init is provided, which is the form
// nearly every use takes; Go also admits an assignment or an increment there.
//
// The grammar reaches the form by left-factoring, as the "for" header does: what
// follows "if" is parsed as an expression, and the next token decides what it was
// -- "{" makes it the condition, ":=" makes it the target of an init statement.
//
// # For Statements
//
// A "for" statement specifies repeated execution of a block. Iteration is
// controlled by a single boolean condition, by an init/condition/post clause
// triple, or by a range clause.
//
// If the condition is absent, it is equivalent to the boolean value true.
//
// A variable introduced by the init clause ("for i := 0; i < n; i++") is scoped
// to the loop, so two loops may each declare their own.
//
// A range clause iterates over an integer, an array or a slice, and over a
// string by rune: "for range n", "for i := range x", and "for i, v := range x",
// where for a string i is each rune's start byte index and v the rune itself.
// The operand is evaluated once. Ranging over a map or a channel is not
// implemented (a map needs a heap; a channel range needs a close, which the
// rendezvous does not model yet).
//
// # Switch Statements
//
// "Switch" statements provide multi-way execution. An expression is compared
// to the "cases" inside the "switch" to determine which branch to execute.
//
//	SwitchStmt = "switch" [ SwitchGuard ] "{" { CaseClause } "}" .
//	SwitchGuard = HeaderExpression [ { "," LhsItem } ":=" HeaderExpression ] [ SwitchTag ] .
//	SwitchTag  = ";" [ HeaderExpression ] .
//	CaseClause = CaseHead ":" { Statement ";" } [ Statement ] .
//	CaseHead   = "case" ExpressionList | "default" .
//
// In an expression switch, the switch expression is evaluated and the case
// expressions are evaluated left-to-right and top-to-bottom. The first one
// that equals the switch expression triggers execution of the statements of
// the associated case.
//
// A switch may carry an init statement, "switch v := f(); v", as an "if" may.
// Either may declare SEVERAL names from one call, "if v, ok := m.get(k); ok" and
// "switch q, r := split(n); q", which is how a two-result call is usually asked.
// The name it declares is scoped to the whole statement -- the expression
// switched on, every case expression and every clause body -- and not beyond it,
// so it may shadow a name from outside without disturbing it. The expression may
// be left out, "switch v := f(); { case v > 3: }", which switches on true with v
// in scope. Only a ":=" init is provided, the form nearly every use takes; Go
// also admits an assignment or an increment there.
//
// (OctoGo Specific): the ":=" guard without an init statement, "switch v := f()",
// declares v and switches on it. Go rejects that text, so the portable spelling
// of the same thing is "switch v := f(); v".
//
// A TYPE switch takes neither form: its guard is the whole statement, so nothing
// may follow it. "switch x := v.(type); x {" is refused, as Go refuses it -- there
// is no expression for a type switch to also switch on.
//
// As in Go, a case body does not fall through to the next, and "break" leaves the
// switch rather than any enclosing loop.
//
// A "fallthrough" statement transfers control to the first statement of the next
// case clause. It is legal only as the last statement of a clause that is not the
// switch's last: anywhere else -- before another statement, inside a nested block,
// loop or if, in a select's communication clause, or outside a switch entirely --
// is "fallthrough statement out of place", and one in the final clause, which has
// nothing to fall into, is "cannot fallthrough final case in switch". The clause
// it continues into is the one written next, which for a default clause need not
// be the one tested next. A clause ending in a fallthrough counts as terminating
// for the switch's own termination, since control continues into the next clause
// rather than out of the bottom of the switch.
//
// A type switch, "switch v := s.(type)", switches on an interface value's dynamic
// type rather than on a value. Its rule is the type assertion's, asked once per
// clause; see Interface types.
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
// A send clause offers its value to the channel and waits for a receiver to take
// it, so its body runs because the value was delivered and not merely deposited.
// The offer stands between rounds and is taken back whenever another clause is
// ready to proceed, since proceeding there would otherwise communicate twice.
//
// Two limits follow from the rendezvous having no scheduler behind it, and both
// are refused rather than approximated. A select may carry at most one send
// clause: two offers cannot stand at once, because a receiver taking each would
// send twice, and offering them by turns would let a receiver polling one miss it
// while the other is up. And a send clause may not be combined with a default,
// which asks whether a receiver is ready at this instant -- a receiver here
// reveals itself only by taking a value, so there is nothing to ask.
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
// A goroutine's slot comes free in its epilogue, after its body ends, so a "go"
// that finds every slot busy waits for one before it panics: a caller can learn a
// goroutine's body is over -- by receiving the value it sent last -- before the
// epilogue has run. A slot held by a goroutine that really is running stays held,
// so the wait ends in the same panic, only later.
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
//   - The bound is on goroutines running at one time, not on how many a program
//     may start: a finished goroutine's slot is reused, so a loop may spawn any
//     number of them in turn. A go statement whose predecessor is still in the
//     act of stopping waits for it rather than reporting exhaustion; only seven
//     goroutines that have all genuinely started and none finished panic.
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
//     cell rather than copying it. Past the sixteenth channel the locks are
//     shared rather than exhausted -- the toolchain's _locknew hands out lock 15
//     repeatedly instead of reporting failure (see doc/locknew-never-fails.c) --
//     which costs contention and nothing else, a lock being needed only for
//     atomicity around the cell. Twenty-four channels each completing a
//     rendezvous have been run on a P2-EDGE and are correct.
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
// package-level variables are initialized in a deterministic topological order
// based on their dependencies, as Go initializes them: a variable whose
// initializer reads another is initialized after it, wherever the two are written.
// Variables that depend on nothing keep their source order.
//
// An initializer that is a constant expression is folded into the variable's own
// definition; anything else -- a reference to another variable, arithmetic over
// one, a call -- is assigned by the synthesized initializer that runs before main,
// which is where the ordering applies.
//
// A CYCLE among the initializers is refused, as Go refuses one: there is no order
// in which every initializer sees the value it reads. The diagnostic names the
// variables and the edges between them, so which pair closes the ring is not left to
// be worked out:
//
//	initialization cycle for a
//		a refers to b
//		b refers to a
//
// A variable whose initializer reads itself, "var a int = a + 1", is the same rule at
// its shortest and says so.
//
// Variables may also be initialized using functions named init declared in
// the package block, with no arguments and no result parameters:
//
//	func init() { … }
//
// A signature that takes or returns anything is an error: an init function is
// called by the program's startup, which has nothing to pass it and nowhere to
// put what it returns.
//
// Multiple such functions may be defined per package, even within a single
// source file. In the package block, the init identifier can be used only to
// declare init functions, yet the identifier itself is not declared. Thus
// init functions cannot be referred to from anywhere in a program, and a second
// one is no redeclaration of the first.
//
// All init functions across all files in the directory are gathered and
// executed sequentially, in the order they are written, by the transpiled
// runtime before the package is considered fully initialized. Each therefore
// runs on the state the ones before it left.
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
