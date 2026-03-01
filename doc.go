// Command octogo is the compiler for the OctoGo programming language.
//
// OctoGo brings Native Go Concurrency for the Parallax Propeller 2.
//
// Some other text example 1.
//
// # OctoGo Language Specification
//
//	# OctoGo Expression Grammar for modernc.org/egg
//	# Target: Parallax Propeller 2
//
//	# Enforce imports before other top-level declarations
//	SourceFile = { ImportDecl } { TopLevelDecl } .
//
//	ImportDecl = "import" string_literal .
//
//	TopLevelDecl = FuncDecl | VarDecl | ConstDecl .
//
//	# Simple const declaration for now
//	ConstDecl = "const" identifier "=" Expression .
//
//	Type = "int" | "bool" | "byte" | "chan" Type | "[" [ Expression ] "]" Type .
//	VarDecl = "var" identifier Type [ "=" Expression ] .
//	FuncDecl = "func" identifier "(" [ ParameterList ] ")" [ Type ] Block .
//	ParameterList = identifier Type { "," identifier Type } .
//
//	Block = "{" { Statement } "}" .
//	# Left-factored statements to resolve LL(1) ambiguities
//	# between assignment, function calls, and channel sends.
//	Statement = VarDecl
//	          | ConstDecl
//	          | "if" Expression Block [ "else" Block ]
//	          | "for" [ Expression ] Block
//	          | "return" [ Expression ]
//	          | "go" identifier CallSuffix
//	          | SwitchStmt
//	          | SelectStmt
//	          | "<-" Expression
//	          | identifier Postfix .
//
//	# Handles L-value resolution for Assignment (=), Channel Send (<-), or Call ()
//	Postfix = { Selector | Index } ( "=" Expression | "<-" Expression | CallSuffix ) .
//	Selector = "." identifier .
//	Index = "[" Expression "]" .
//	CallSuffix = "(" [ ArgumentList ] ")" .
//	ArgumentList = Expression { "," Expression } .
//
//	# Switch Statement
//	SwitchStmt = "switch" [ Expression ] "{" { CaseClause } "}" .
//	CaseClause = CaseHead ":" { Statement } .
//	CaseHead   = "case" ExpressionList | "default" .
//	ExpressionList = Expression { "," Expression } .
//
//	# Select Statement
//	SelectStmt = "select" "{" { CommClause } "}" .
//	CommClause = CommHead ":" { Statement } .
//	CommHead   = "case" CommOp | "default" .
//
//	# Left-factored CommOp to handle `<-ch`, `v = <-ch`, and `ch <- v`
//	CommOp = "<-" Expression
//	       | identifier PostfixComm .
//	PostfixComm = { Selector | Index } ( "=" "<-" Expression | "<-" Expression ) .
//
//	# Expressions (Standard Precedence Climbing)
//	Expression = SimpleExpr [ RelOp SimpleExpr ] .
//	SimpleExpr = Term { AddOp Term } .
//	Term       = Factor { MulOp Factor } .
//
//	Factor = identifier [ FactorSuffix ]
//	       | number
//	       | string_literal
//	       | "true"
//	       | "false"
//	       | "<-" Expression
//	       | "(" Expression ")" .
//
//	# FactorSuffix allows function calls and indexing inside expressions
//	# without clashing with Statement-level assignments.
//	FactorSuffix = { Selector | Index } [ CallSuffix ] .
//
//	RelOp = "==" | "!=" | "<" | "<=" | ">" | ">=" .
//	AddOp = "+" | "-" | "|" | "^" .
//	MulOp = "*" | "/" | "<<" | ">>" | "&" .
//
//	# Lexical tokens
//	identifier     = `[a-zA-Z_][a-zA-Z0-9_]*` .
//	number         = `[0-9]+` .
//	string_literal = `"(?:[^"\\]|\\.)*"` .
//	white_space    = `/\*([^*]|\*+[^*/])*\*+/|//.*| |\t|\n|\r` .
//
// Some other text example 2.
package main // import "octogo.dev/octogo"
