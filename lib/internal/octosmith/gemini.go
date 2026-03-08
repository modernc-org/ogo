// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"io"
)

// GenerateProgram builds the AST and computes the final expected state.
// It directly outputs the generated source code to f.Out.
func (f *Fuzzer) GenerateProgram(vm Machine, mem Memory) error {
	// 1. Initialize the global environment
	mem.PushScope()
	defer mem.PopScope()

	// 2. Generate our checksum variable: var octosmith_checksum int = 0
	// TopLevelDecl = VarDecl
	checksumVal, _ := vm.Eval("int_lit", "0") // VM helper to create a base Value
	mem.Store(f.ChecksumName, checksumVal)

	fmt.Fprintf(f.Out, "var %s int = 0\n\n", f.ChecksumName)

	// 3. Generate the main function
	// FuncDecl = "func" identifier "(" ")" Block
	fmt.Fprint(f.Out, "func main() {\n")
	mem.PushScope() // Scope for main() variables

	// Generate 20 sequential statements to mutate the checksum
	for i := 0; i < 20; i++ {
		stmtNode := f.genStatement(vm, mem)
		stmtNode.Write(f.Out, 1) // write with 1 level of indent
		fmt.Fprint(f.Out, "\n")
	}

	// 4. Retrieve the FINAL generation-time state of the checksum
	finalChecksum := mem.Load(f.ChecksumName)

	// 5. Emit the Oracle Assertion
	// If the compiled P2 binary gets a different result, it prints the error and halts.
	// Note: We use a simple `if` here assuming the standard Go-like print exists,
	// or we can map this to a P2 serial write later.
	writeIndent(f.Out, 1)
	fmt.Fprintf(f.Out, "if %s != %s {\n", f.ChecksumName, finalChecksum.Literal())
	writeIndent(f.Out, 2)
	fmt.Fprintf(f.Out, "panic(\"OctoSmith Checksum Failure!\")\n")
	writeIndent(f.Out, 1)
	fmt.Fprint(f.Out, "}\n")

	mem.PopScope()
	fmt.Fprint(f.Out, "}\n") // Close main()

	return nil
}

// genStatement generates either a new variable declaration or mutates the checksum.
func (f *Fuzzer) genStatement(vm Machine, mem Memory) Node {
	// 30% chance to declare a new local variable, 70% chance to mutate checksum
	if f.Rand.Float32() < 0.30 {
		return f.genVarDecl(vm, mem)
	}
	return f.genChecksumMutation(vm, mem)
}

// genVarDecl generates: var <name> int = <expr>
func (f *Fuzzer) genVarDecl(vm Machine, mem Memory) Node {
	varName := fmt.Sprintf("v_%d", f.Rand.Intn(10000))

	// Generate an integer expression
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)

	// Store it in our generation-time VM memory
	mem.Store(varName, exprVal)

	return &VarDeclNode{
		Name: varName,
		Type: "int", // OctoGo numeric type
		Expr: exprNode,
	}
}

// genChecksumMutation generates: octosmith_checksum = octosmith_checksum ^ <expr>
func (f *Fuzzer) genChecksumMutation(vm Machine, mem Memory) Node {
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)

	currentChecksum := mem.Load(f.ChecksumName)

	// Evaluate the mutation in our VM
	// We use bitwise XOR (^) as it avoids overflow/sign panics common with * or <<
	newChecksum, _ := vm.Eval("^", currentChecksum, exprVal)
	mem.Store(f.ChecksumName, newChecksum)

	return &AssignStmtNode{
		Lhs: f.ChecksumName,
		Op:  "=",
		Rhs: &BinaryExprNode{
			Left:  &IdentNode{Name: f.ChecksumName},
			Op:    "^",
			Right: exprNode,
		},
	}
}

// genExpression is the core Type-Directed Generator.
// depth prevents infinite recursion when generating binary operations.
func (f *Fuzzer) genExpression(targetType Type, vm Machine, mem Memory, depth int) (Node, Value, error) {
	// Base cases: Literal or existing Variable
	if depth > 3 || f.Rand.Float32() < 0.5 {
		if f.Rand.Float32() < 0.5 {
			// Generate int_lit
			valStr := fmt.Sprintf("%d", f.Rand.Intn(100))
			val, _ := vm.Eval("int_lit", valStr)
			return &IntLitNode{Value: valStr}, val, nil
		} else {
			// Pull an existing variable from memory (if any exist)
			// For MVP, we'll just fall back to literals if we don't have a specific `mem.GetRandomVarOfType()` API yet.
			valStr := fmt.Sprintf("%d", f.Rand.Intn(100))
			val, _ := vm.Eval("int_lit", valStr)
			return &IntLitNode{Value: valStr}, val, nil
		}
	}

	// Recursive case: Binary Operation (AddOp or MulOp)
	op := []string{"+", "-", "^"}[f.Rand.Intn(3)] // Safe operators that won't easily div-by-zero

	leftNode, leftVal, _ := f.genExpression(targetType, vm, mem, depth+1)
	rightNode, rightVal, _ := f.genExpression(targetType, vm, mem, depth+1)

	resultVal, err := vm.Eval(op, leftVal, rightVal)
	if err != nil {
		// If VM rejects it (e.g. overflow, though OctoGo constants are arbitrary precision,
		// runtime ints are 32-bit), fallback to a safe literal.
		val, _ := vm.Eval("int_lit", "1")
		return &IntLitNode{Value: "1"}, val, nil
	}

	return &BinaryExprNode{
		Left:  leftNode,
		Op:    op,
		Right: rightNode,
	}, resultVal, nil
}

// --- Minimal AST Node Implementations ---

type VarDeclNode struct {
	Name string
	Type string
	Expr Node
}

func (n *VarDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "var %s %s = ", n.Name, n.Type)
	n.Expr.Write(w, 0)
}

type AssignStmtNode struct {
	Lhs string
	Op  string // "=" or ":="
	Rhs Node
}

func (n *AssignStmtNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s %s ", n.Lhs, n.Op)
	n.Rhs.Write(w, 0)
}

type BinaryExprNode struct {
	Left  Node
	Op    string // "+", "-", "^", "*", etc.
	Right Node
}

func (n *BinaryExprNode) Write(w io.Writer, indent int) {
	fmt.Fprint(w, "(") // Parenthesize to guarantee order of operations matches VM
	n.Left.Write(w, 0)
	fmt.Fprintf(w, " %s ", n.Op)
	n.Right.Write(w, 0)
	fmt.Fprint(w, ")")
}

type IntLitNode struct{ Value string }

func (n *IntLitNode) Write(w io.Writer, indent int) { fmt.Fprint(w, n.Value) }

type IdentNode struct{ Name string }

func (n *IdentNode) Write(w io.Writer, indent int) { fmt.Fprint(w, n.Name) }
