// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith // import "modernc.org/octogo/lib/internal/smith"

import (
	"fmt"
	"io"
	"sort"
)

// GenerateProgram builds the AST and computes the final expected state.
// It directly outputs the generated source code to f.Out.
func (f *Fuzzer) GenerateProgram(vm Machine, mem Memory) error {
	// 1. Initialize the global environment
	mem.PushScope()
	defer mem.PopScope()

	// 2. Generate our checksum variable: var octosmith_checksum int = 0
	// TopLevelDecl = VarDecl
	checksumVal, _ := vm.Eval("int_lit", "0")
	mem.Store(f.ChecksumName, checksumVal)

	// Register the checksum in the global environment so we don't accidentally flush it
	f.CurrentEnv.Declare(f.ChecksumName, BasicType{Kind: KindInt}, false)
	f.CurrentEnv.Lookup(f.ChecksumName).Used = true

	fmt.Fprintf(f.Out, "var %s int = 0\n\n", f.ChecksumName)

	// 3. Generate the main function
	// FuncDecl = "func" identifier "(" ")" Block
	fmt.Fprint(f.Out, "func main() {\n")

	// Keep f.CurrentEnv perfectly in sync with the VM memory scopes
	mem.PushScope()
	f.CurrentEnv = NewScope(f.CurrentEnv)

	// Generate 20 sequential statements to mutate the checksum
	for i := 0; i < 20; i++ {
		stmtNode := f.genStatement(vm, mem)
		stmtNode.Write(f.Out, 1)
		fmt.Fprint(f.Out, "\n")
	}

	// Flush unused variables for the main block
	flushNodes := f.flushUnused(vm, mem)
	for _, n := range flushNodes {
		n.Write(f.Out, 1)
		fmt.Fprint(f.Out, "\n")
	}

	// 4. Retrieve the FINAL generation-time state of the checksum
	finalChecksum := mem.Load(f.ChecksumName)

	// 5. Emit the Oracle Assertion
	// If the compiled P2 binary gets a different result, it prints the error and halts.
	writeIndent(f.Out, 1)
	fmt.Fprintf(f.Out, "if %s != %s {\n", f.ChecksumName, finalChecksum.Literal())
	writeIndent(f.Out, 2)
	fmt.Fprintf(f.Out, "panic(\"OctoSmith Checksum Failure!\")\n")
	writeIndent(f.Out, 1)
	fmt.Fprint(f.Out, "}\n")

	mem.PopScope()
	f.CurrentEnv = f.CurrentEnv.Parent // Sync scope pop
	fmt.Fprint(f.Out, "}\n")           // Close main()

	return nil
}

// flushUnused finds all unused variables in the current scope, generates mutations
// to flush them into the checksum, and returns the AST nodes.
func (f *Fuzzer) flushUnused(vm Machine, mem Memory) []Node {
	var unusedNames []string
	for name, sym := range f.CurrentEnv.Symbols {
		if !sym.Used && name != f.ChecksumName {
			unusedNames = append(unusedNames, name)
		}
	}

	// Sort to guarantee deterministic code generation across runs!
	sort.Strings(unusedNames)

	var flushNodes []Node
	for _, name := range unusedNames {
		sym := f.CurrentEnv.Lookup(name)
		sym.Used = true // Mark as used

		// Evaluate the mutation in our VM
		currentChecksum := mem.Load(f.ChecksumName)
		varVal := mem.Load(name)
		newChecksum, _ := vm.Eval("^", currentChecksum, varVal)
		mem.Store(f.ChecksumName, newChecksum)

		// Generate the AST Node: octosmith_checksum = octosmith_checksum ^ unused_var
		flushNodes = append(flushNodes, &AssignStmtNode{
			Lhs: f.ChecksumName,
			Op:  "=",
			Rhs: &BinaryExprNode{
				Left:  &IdentNode{Name: f.ChecksumName},
				Op:    "^",
				Right: &IdentNode{Name: name},
			},
		})
	}

	return flushNodes
}

// genStatement generates a new variable declaration, an if statement, or mutates the checksum.
func (f *Fuzzer) genStatement(vm Machine, mem Memory) Node {
	r := f.Rand.Float32()
	if r < 0.10 {
		return f.genForStmt(vm, mem) // 10% chance for a loop
	} else if r < 0.25 {
		return f.genIfStmt(vm, mem) // 15% chance for an if
	} else if r < 0.45 {
		return f.genVarDecl(vm, mem) // 20% chance for var
	}
	return f.genChecksumMutation(vm, mem)
}

// genForStmt generates a bounded loop that executes exactly once
// to maintain VM and generation-time synchronization.
func (f *Fuzzer) genForStmt(vm Machine, mem Memory) Node {
	loopVar := fmt.Sprintf("i_%d", f.Rand.Intn(10000))

	// 1. Setup the loop variable BEFORE the loop
	zeroVal, _ := vm.Eval("int_lit", "0")
	mem.Store(loopVar, zeroVal)
	f.CurrentEnv.Declare(loopVar, BasicType{Kind: KindInt}, false)

	initNode := &VarDeclNode{
		Name: loopVar,
		Type: "int",
		Expr: &IntLitNode{Value: "0"},
	}

	// 2. The Condition: i < 1
	condNode := &BinaryExprNode{
		Left:  &IdentNode{Name: loopVar},
		Op:    "<",
		Right: &IntLitNode{Value: "1"},
	}

	// 3. The Body (Push Scope)
	mem.PushScope()
	f.CurrentEnv = NewScope(f.CurrentEnv)

	var stmts []Node
	numStmts := 1 + f.Rand.Intn(2)
	for i := 0; i < numStmts; i++ {
		stmts = append(stmts, f.genStatement(vm, mem))
	}

	// Flush unused variables in this loop scope
	stmts = append(stmts, f.flushUnused(vm, mem)...)

	// 4. The Increment: i = i + 1
	currVal := mem.Load(loopVar)
	oneVal, _ := vm.Eval("int_lit", "1")
	newVal, _ := vm.Eval("+", currVal, oneVal)
	mem.Store(loopVar, newVal)

	incNode := &AssignStmtNode{
		Lhs: loopVar,
		Op:  "=",
		Rhs: &BinaryExprNode{
			Left:  &IdentNode{Name: loopVar},
			Op:    "+",
			Right: &IntLitNode{Value: "1"},
		},
	}
	stmts = append(stmts, incNode)

	// Pop Scope
	mem.PopScope()
	f.CurrentEnv = f.CurrentEnv.Parent

	// Return a Block containing the initialization AND the loop
	// This ensures the loop variable doesn't leak into the parent scope's AST awkwardly
	return &BlockNode{
		Statements: []Node{
			initNode,
			&ForStmtNode{
				Cond: condNode,
				Body: &BlockNode{Statements: stmts},
			},
		},
	}
}

// genIfStmt generates an if statement, forcing the condition to be true
// to avoid desynchronizing the fuzzer's VM memory with dead code for now.
func (f *Fuzzer) genIfStmt(vm Machine, mem Memory) Node {
	leftNode, leftVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	rightNode, rightVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)

	ops := []string{"==", "!=", "<", "<=", ">", ">="}
	op := ops[f.Rand.Intn(len(ops))]

	condVal, _ := vm.Eval(op, leftVal, rightVal)
	isTrue := condVal.Value().(bool)

	// Force the condition to be true to evaluate the inner block
	if !isTrue {
		switch op {
		case "==":
			op = "!="
		case "!=":
			op = "=="
		case "<":
			op = ">="
		case "<=":
			op = ">"
		case ">":
			op = "<="
		case ">=":
			op = "<"
		}
	}

	condNode := &BinaryExprNode{
		Left:  leftNode,
		Op:    op,
		Right: rightNode,
	}

	// Push Scope for the block
	mem.PushScope()
	f.CurrentEnv = NewScope(f.CurrentEnv)

	var stmts []Node
	numStmts := 1 + f.Rand.Intn(3) // Generate 1-3 statements inside the block
	for i := 0; i < numStmts; i++ {
		stmts = append(stmts, f.genStatement(vm, mem))
	}

	// Flush any unused variables created strictly within this if-block
	stmts = append(stmts, f.flushUnused(vm, mem)...)

	// Pop Scope
	mem.PopScope()
	f.CurrentEnv = f.CurrentEnv.Parent

	return &IfStmtNode{
		Cond: condNode,
		Body: &BlockNode{Statements: stmts},
	}
}

// genVarDecl generates: var <name> int = <expr>
func (f *Fuzzer) genVarDecl(vm Machine, mem Memory) Node {
	varName := fmt.Sprintf("v_%d", f.Rand.Intn(10000))

	// Generate an integer expression
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)

	// Store it in our generation-time VM memory
	mem.Store(varName, exprVal)

	// Track the new variable in the environment
	f.CurrentEnv.Declare(varName, BasicType{Kind: KindInt}, false)

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
			// Pull an existing variable from the environment
			symbols := f.CurrentEnv.GetSymbolsOfType(targetType)
			if len(symbols) > 0 {
				// Pick a random available symbol
				sym := symbols[f.Rand.Intn(len(symbols))]
				sym.Used = true

				// Pull its generation-time value from VM memory
				val := mem.Load(sym.Name)
				return &IdentNode{Name: sym.Name}, val, nil
			}

			// Fallback to a literal if no symbols of that type exist yet
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
		// If VM rejects it, fallback to a safe literal.
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
	Op    string // "+", "-", "^", "*", "==", "!=", etc.
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

type IfStmtNode struct {
	Cond Node
	Body Node // Expected to be a BlockNode
}

func (n *IfStmtNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprint(w, "if ")
	n.Cond.Write(w, 0)
	fmt.Fprint(w, " ")
	n.Body.Write(w, indent)
}

type BlockNode struct {
	Statements []Node
}

func (n *BlockNode) Write(w io.Writer, indent int) {
	fmt.Fprint(w, "{\n")
	for _, stmt := range n.Statements {
		stmt.Write(w, indent+1)
		fmt.Fprint(w, "\n")
	}
	writeIndent(w, indent)
	fmt.Fprint(w, "}")
}

type ForStmtNode struct {
	Cond Node
	Body Node // Expected to be a BlockNode
}

func (n *ForStmtNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprint(w, "for ")
	if n.Cond != nil {
		n.Cond.Write(w, 0)
	}
	fmt.Fprint(w, " ")
	n.Body.Write(w, indent)
}
