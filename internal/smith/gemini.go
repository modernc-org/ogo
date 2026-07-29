// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"io"
	"sort"
	"strconv"
)

// newVarName returns a unique variable name (see Fuzzer.VarSeq for why a counter
// rather than a random suffix).
func (f *Fuzzer) newVarName(prefix string) string {
	f.VarSeq++
	return fmt.Sprintf("%s_%d", prefix, f.VarSeq)
}

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

	// 3. Generate the struct types, ahead of the functions and of main so both can
	// use them.
	for i, n := 0, f.Rand.Intn(3); i < n; i++ {
		f.genStructType()
	}

	// 4. Generate the functions main will call. They come first so that every call
	// site in main has one to draw on, and they take no part in the environment:
	// a body reads only its own parameters (see genPureExpr).
	for i, n := 0, f.Rand.Intn(4); i < n; i++ {
		f.genFuncDecl()
	}

	// 5. Generate the main function
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

	// 6. Retrieve the FINAL generation-time state of the checksum
	finalChecksum := mem.Load(f.ChecksumName)

	// 7. Emit the Oracle Assertion
	// If the compiled P2 binary gets a different result, it prints the error and halts.
	writeIndent(f.Out, 1)
	fmt.Fprintf(f.Out, "if %s != %s {\n", f.ChecksumName, finalChecksum.Literal())
	writeIndent(f.Out, 2)
	fmt.Fprintf(f.Out, "panic(\"OctoSmith Checksum Failure!\")\n")
	writeIndent(f.Out, 1)
	fmt.Fprint(f.Out, "}\n")

	// 8. Say so. On the host a clean exit is enough, but a program running on a
	// board has no exit status: reaching the end and a hang look identical over a
	// serial line. So success is printed, and the board oracle waits for it.
	writeIndent(f.Out, 1)
	fmt.Fprintf(f.Out, "println(%q)\n", OKMarker)

	mem.PopScope()
	f.CurrentEnv = f.CurrentEnv.Parent // Sync scope pop
	fmt.Fprint(f.Out, "}\n")           // Close main()

	return nil
}

// FuncDef is a generated top-level function: one int result, computed from its
// int parameters by a single expression. Keeping the whole body one expression is
// what lets the generation-time VM re-evaluate it at each call site, which is how
// the oracle knows a call's value without modelling a call stack.
type FuncDef struct {
	Name   string
	Params []string
	Body   Node // over Params and literals only; see genFuncDecl for why it is total
}

// pureOps are the operators a generated function's body may use.
//
// The restriction is what makes calls safe to generate at all. Everywhere else
// the VM evaluates an operator against the operands in hand and swaps in XOR when
// that combination is undefined in C (see genExpression). A function body cannot
// be fixed up that way: it is emitted once and then evaluated again at every call,
// with argument values it has never seen, so an operator that is undefined for
// *some* operands would eventually be reached with them. These four are total over
// int32 -- no division by zero, no shift-count range, no signed overflow -- so the
// body is defined for every argument the generator can pass it. Arithmetic is
// fuzzed thoroughly at the top level; what a call adds is the call itself.
var pureOps = []string{"&", "|", "^", "&^"}

// genFuncDecl generates a top-level function and writes it out, returning it for
// the call sites to draw on.
func (f *Fuzzer) genFuncDecl() *FuncDef {
	fn := &FuncDef{Name: f.newVarName("fn")}
	for i, n := 0, 1+f.Rand.Intn(3); i < n; i++ {
		fn.Params = append(fn.Params, f.newVarName("p"))
	}
	fn.Body = f.genPureExpr(fn.Params, 0)
	f.Funcs = append(f.Funcs, fn)
	(&FuncDeclNode{Name: fn.Name, Params: fn.Params, Body: fn.Body}).Write(f.Out, 0)
	fmt.Fprint(f.Out, "\n")
	return fn
}

// genPureExpr builds a function body: an expression over the parameters and
// integer literals, using only the total operators (see pureOps). It reads
// nothing from the environment, so a body can never reference a name that is out
// of scope where the function is declared.
func (f *Fuzzer) genPureExpr(params []string, depth int) Node {
	if depth > 2 || f.Rand.Float32() < 0.4 {
		if len(params) != 0 && f.Rand.Float32() < 0.6 {
			return &IdentNode{Name: params[f.Rand.Intn(len(params))]}
		}
		return &IntLitNode{Value: fmt.Sprintf("%d", f.Rand.Intn(100))}
	}
	return &BinaryExprNode{
		Left:  f.genPureExpr(params, depth+1),
		Op:    pureOps[f.Rand.Intn(len(pureOps))],
		Right: f.genPureExpr(params, depth+1),
	}
}

// evalCall evaluates a generated function's body with its parameters bound to a
// call's argument values, giving the oracle the call's result.
//
// The operators pureOps admits are total, so an evaluation error here is not a
// property of the arguments but a broken invariant -- a body built from something
// outside that set -- and panics rather than being papered over.
func (f *Fuzzer) evalCall(fn *FuncDef, args map[string]Int32, vm Machine) Int32 {
	var eval func(Node) Int32
	eval = func(n Node) Int32 {
		switch x := n.(type) {
		case *IntLitNode:
			v, _ := vm.Eval("int_lit", x.Value)
			return v.(Int32)
		case *IdentNode:
			return args[x.Name]
		case *BinaryExprNode:
			v, err := vm.Eval(x.Op, eval(x.Left), eval(x.Right))
			if err != nil {
				panic(todo("%s: body is not total: %v", fn.Name, err))
			}
			return v.(Int32)
		default:
			panic(todo("%s: unexpected body node %T", fn.Name, n))
		}
	}
	return eval(fn.Body)
}

// genCall generates a call to an already-declared function, its arguments being
// ordinary generated integer expressions. The VM re-evaluates the callee's body
// against those argument values, so the oracle predicts the result of the compiled
// call -- which is what puts argument passing, parameter binding and the returned
// value under test.
func (f *Fuzzer) genCall(vm Machine, mem Memory, depth int) (Node, Value) {
	fn := f.Funcs[f.Rand.Intn(len(f.Funcs))]
	args := map[string]Int32{}
	var argNodes []Node
	for _, p := range fn.Params {
		node, val, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, depth+1)
		argNodes = append(argNodes, node)
		args[p] = val.(Int32)
	}
	return &CallNode{Fn: fn.Name, Args: argNodes}, f.evalCall(fn, args, vm)
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

		// Fold the unused variable into the checksum with XOR. Neither an array nor a
		// slice can be an operand, so fold an array's first element and a slice's
		// length -- len is defined even for the empty slice, whose element 0 is not;
		// a scalar folds directly.
		currentChecksum := mem.Load(f.ChecksumName)

		// A bool is not an operand of "^", so fold it by what it selects: the
		// checksum takes a constant when the variable is true. That is also the
		// only way a bool reaches the generated program as a condition of its
		// own, without a comparison wrapped around it.
		if bt, ok := sym.Type.(BasicType); ok && bt.Kind == KindBool {
			k := Int32(f.Rand.Int31())
			if mem.Load(name).Value().(bool) {
				newChecksum, _ := vm.Eval("^", currentChecksum, k)
				mem.Store(f.ChecksumName, newChecksum)
			}
			flushNodes = append(flushNodes, &IfStmtNode{
				Cond: &IdentNode{Name: name},
				Body: &BlockNode{Statements: []Node{&AssignStmtNode{
					Lhs: f.ChecksumName,
					Op:  "=",
					Rhs: &BinaryExprNode{
						Left:  &IdentNode{Name: f.ChecksumName},
						Op:    "^",
						Right: &IntLitNode{Value: k.Literal()},
					},
				}}},
			})
			continue
		}

		var varVal Value
		var right Node
		switch sym.Type.(type) {
		case ArrayType:
			varVal = mem.Load(name).(*ArrayVal).Elems[0]
			right = &IndexNode{Name: name, Index: 0}
		case SliceType:
			varVal = Int32(len(mem.Load(name).(*SliceVal).Elems))
			right = &BuiltinCallNode{Fn: "len", Arg: name}
		case StructType:
			sv := mem.Load(name).(*StructVal)
			fld := sv.Def.Fields[0]
			varVal = sv.Fields[fld]
			right = &FieldNode{Name: name, Field: fld}
		default:
			varVal = mem.Load(name)
			right = &IdentNode{Name: name}
		}
		newChecksum, _ := vm.Eval("^", currentChecksum, varVal)
		mem.Store(f.ChecksumName, newChecksum)

		// Generate: octosmith_checksum = octosmith_checksum ^ <unused var or a[0]>
		flushNodes = append(flushNodes, &AssignStmtNode{
			Lhs: f.ChecksumName,
			Op:  "=",
			Rhs: &BinaryExprNode{
				Left:  &IdentNode{Name: f.ChecksumName},
				Op:    "^",
				Right: right,
			},
		})
	}

	return flushNodes
}

// genStatement generates a new variable declaration, an if statement, or mutates the checksum.
func (f *Fuzzer) genStatement(vm Machine, mem Memory) Node {
	switch r := f.Rand.Float32(); {
	case r < 0.10:
		return f.genForStmt(vm, mem) // 10% chance for a loop
	case r < 0.22:
		return f.genIfStmt(vm, mem) // 12% chance for an if
	case r < 0.25:
		return f.genSwitchStmt(vm, mem) // 3% chance for a switch
	case r < 0.28:
		return f.genBoolVarDecl(vm, mem) // 3% chance for a bool variable
	case r < 0.32:
		return f.genSizedStmt(vm, mem) // 4% chance for sized-integer arithmetic
	case r < 0.34:
		return f.genStringStmt(vm, mem) // 2% chance for string reads
	case r < 0.37:
		return f.genVarDecl(vm, mem) // 12% chance for var
	case r < 0.44:
		return f.genArrayDecl(vm, mem) // 7% chance for a fixed array declaration
	case r < 0.53:
		return f.genArrayWrite(vm, mem) // 9% chance for an array element write
	case r < 0.60:
		return f.genSliceDecl(vm, mem) // 7% chance for a slice declaration
	case r < 0.69:
		return f.genSliceWrite(vm, mem) // 9% chance for a slice element write
	case r < 0.76:
		return f.genAppend(vm, mem) // 7% chance for an append
	case r < 0.82:
		return f.genStructDecl(vm, mem) // 6% chance for a struct declaration
	case r < 0.89:
		return f.genFieldWrite(vm, mem) // 7% chance for a struct field write
	case r < 0.93:
		return f.genStructCopy(vm, mem) // 4% chance for a by-value struct copy
	case r < 0.97:
		return f.genCompoundAssign(vm, mem) // 4% chance for a compound assignment
	}
	return f.genChecksumMutation(vm, mem)
}

// genForStmt generates a bounded loop that executes exactly once
// to maintain VM and generation-time synchronization.
func (f *Fuzzer) genForStmt(vm Machine, mem Memory) Node {
	loopVar := f.newVarName("i")

	// The returned AST wraps the loop variable and the loop in a block (see the
	// BlockNode below), so scope them in a matching block here. Declaring the loop
	// variable in the parent env instead leaked it: later statements picked it and
	// referenced it out of scope in the generated program.
	mem.PushScope()
	f.CurrentEnv = NewScope(f.CurrentEnv)

	// 1. Setup the loop variable BEFORE the loop
	zeroVal, _ := vm.Eval("int_lit", "0")
	mem.Store(loopVar, zeroVal)
	f.CurrentEnv.Declare(loopVar, BasicType{Kind: KindInt}, false)
	f.CurrentEnv.Lookup(loopVar).LoopVar = true // read-only in the body: see Symbol.LoopVar

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

	// Pop the body scope, then the block scope that wraps the loop variable.
	mem.PopScope()
	f.CurrentEnv = f.CurrentEnv.Parent
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
// relOps are the comparisons a boolean expression is ultimately built from.
var relOps = []string{"==", "!=", "<", "<=", ">", ">="}

// genBoolExpr builds a boolean expression together with the value it has, so a
// caller can place it where the outcome must be known.
//
// Short-circuiting is safe to generate because nothing the generator emits inside
// an expression has an effect the VM would have to take back: a generated function
// is a pure expression over its parameters (genPureExpr), and everything else is a
// read. So the operand "&&" skips at run time can still be evaluated here.
func (f *Fuzzer) genBoolExpr(vm Machine, mem Memory, depth int) (Node, bool) {
	if depth < 2 {
		switch r := f.Rand.Float32(); {
		case r < 0.15: // !x
			x, v := f.genBoolExpr(vm, mem, depth+1)
			return &UnaryExprNode{Op: "!", X: x}, !v
		case r < 0.35: // x && y, x || y
			left, lv := f.genBoolExpr(vm, mem, depth+1)
			right, rv := f.genBoolExpr(vm, mem, depth+1)
			op, val := "&&", lv && rv
			if f.Rand.Float32() < 0.5 {
				op, val = "||", lv || rv
			}
			return &BinaryExprNode{Left: left, Op: op, Right: right}, val
		}
	}

	// An existing bool variable, when there is one.
	if syms := f.CurrentEnv.GetSymbolsOfType(BasicType{Kind: KindBool}); len(syms) != 0 && f.Rand.Float32() < 0.4 {
		sym := syms[f.Rand.Intn(len(syms))]
		sym.Used = true
		return &IdentNode{Name: sym.Name}, mem.Load(sym.Name).Value().(bool)
	}

	leftNode, leftVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	rightNode, rightVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	op := relOps[f.Rand.Intn(len(relOps))]
	val, _ := vm.Eval(op, leftVal, rightVal)
	return &BinaryExprNode{Left: leftNode, Op: op, Right: rightNode}, val.Value().(bool)
}

// genBoolVarDecl generates: var <name> bool = <bool expression>
func (f *Fuzzer) genBoolVarDecl(vm Machine, mem Memory) Node {
	name := f.newVarName("b")
	node, val := f.genBoolExpr(vm, mem, 0)
	mem.Store(name, Bool(val))
	f.CurrentEnv.Declare(name, BasicType{Kind: KindBool}, false)
	return &VarDeclNode{Name: name, Type: "bool", Expr: node}
}

func (f *Fuzzer) genIfStmt(vm Machine, mem Memory) Node {
	condNode, isTrue := f.genBoolExpr(vm, mem, 0)

	// The condition is forced to true so the body always runs and the VM's state
	// after the statement is the body's. Negating is what makes an arbitrary
	// boolean expression usable here: flipping a comparison operator only works
	// when the condition IS one, and it no longer always is.
	if !isTrue {
		condNode = &UnaryExprNode{Op: "!", X: condNode}
	}

	mem.PushScope()
	f.CurrentEnv = NewScope(f.CurrentEnv)

	var stmts []Node
	for i := 0; i < 1+f.Rand.Intn(3); i++ {
		stmts = append(stmts, f.genStatement(vm, mem))
	}
	stmts = append(stmts, f.flushUnused(vm, mem)...)

	mem.PopScope()
	f.CurrentEnv = f.CurrentEnv.Parent

	return &IfStmtNode{
		Cond: condNode,
		Body: &BlockNode{Statements: stmts},
	}
}

// genSwitchStmt generates a switch over an integer expression whose value the
// generator already knows. Exactly one clause carries the body, so the VM's state
// after the statement is that body's -- the same bargain genIfStmt makes by forcing
// its condition true. Every other clause is empty.
//
// The clauses that do NOT run are what makes this worth generating: a switch with a
// single case says nothing about how the guard is compared, how a skipped clause is
// stepped over, or that control leaves at the end of a clause instead of falling
// through. The body lands in a "case" or in the "default" by coin flip, so both the
// default-taken and the default-skipped paths are covered, and the matching case is
// sometimes written with two values.
func (f *Fuzzer) genSwitchStmt(vm Machine, mem Memory) Node {
	tagNode, tagVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	match := tagVal.Value().(int32)

	// Values the tag is not. Adding wraps at the extreme, which still yields
	// distinct values, so no clause can accidentally match twice.
	miss1, miss2, miss3 := match+1, match+2, match+3

	mem.PushScope()
	f.CurrentEnv = NewScope(f.CurrentEnv)
	var stmts []Node
	for i := 0; i < 1+f.Rand.Intn(3); i++ {
		stmts = append(stmts, f.genStatement(vm, mem))
	}
	stmts = append(stmts, f.flushUnused(vm, mem)...)
	mem.PopScope()
	f.CurrentEnv = f.CurrentEnv.Parent
	body := &BlockNode{Statements: stmts}

	n := &SwitchStmtNode{Tag: tagNode}
	n.Clauses = append(n.Clauses, SwitchClause{Values: []int32{miss1}})
	switch {
	case f.Rand.Float32() < 0.34: // the body is the default clause
		n.Clauses = append(n.Clauses,
			SwitchClause{Values: []int32{miss2, miss3}},
			SwitchClause{Body: body})
	default: // the body is a case, with the default skipped after it
		n.Clauses = append(n.Clauses,
			SwitchClause{Values: []int32{miss2, match}, Body: body},
			SwitchClause{})
	}
	return n
}

// stringCorpus is what a generated string variable is drawn from. The multibyte
// entries are the point of having a corpus at all rather than random ASCII: they
// put the UTF-8 decode behind `range` and the byte-vs-rune distinction behind
// indexing under the oracle, where an off-by-one in either shows as a wrong
// checksum rather than as mojibake nobody is reading.
var stringCorpus = []string{
	"",
	"a",
	"hello",
	"0123456789",
	"the quick brown fox",
	"caf\u00e9",                   // 2-byte
	"\u00e4\u00f6\u00fc\u00df",    // 2-byte throughout
	"na\u00efve r\u00e9sum\u00e9", // mixed
	"\u4e16\u754c",                // 3-byte
	"\U0001f680go",                // 4-byte, then ASCII
	"a\u00e9\u4e16\U0001f680z",    // one of each width
}

// genStringStmt generates a block that declares a string and folds what can be
// read out of it into the checksum: its length, a byte at an index, the length of
// a slice of it, a comparison, and a range over it.
//
// A string is immutable and has no arithmetic, so every one of those is exactly
// predictable -- the VM computes them with Go's own string operations over the
// same literal, which is the same definition OctoGo's are meant to have. `range`
// in particular yields a BYTE offset and a rune, and the VM gets that from ranging
// over the Go string, so a decoder that disagreed would show up immediately.
func (f *Fuzzer) genStringStmt(vm Machine, mem Memory) Node {
	lit := stringCorpus[f.Rand.Intn(len(stringCorpus))]
	name := f.newVarName("t")
	stmts := []Node{&VarDeclNode{
		Name: name,
		Type: "string",
		Expr: &StringLitNode{Value: lit},
	}}
	fold := func(n Node, v Int32) {
		newChecksum, _ := vm.Eval("^", mem.Load(f.ChecksumName), v)
		mem.Store(f.ChecksumName, newChecksum)
		stmts = append(stmts, &AssignStmtNode{
			Lhs: f.ChecksumName,
			Op:  "=",
			Rhs: &BinaryExprNode{Left: &IdentNode{Name: f.ChecksumName}, Op: "^", Right: n},
		})
	}

	// len is defined for the empty string, so it always goes in.
	fold(&BuiltinCallNode{Fn: "len", Arg: name}, Int32(len(lit)))

	if len(lit) != 0 {
		if f.Rand.Float32() < 0.5 { // a byte at an index: s[i] is a byte, not a rune
			i := f.Rand.Intn(len(lit))
			fold(&ConvNode{Type: "int", X: &IndexNode{Name: name, Index: i}}, Int32(lit[i]))
		}
		if f.Rand.Float32() < 0.5 { // the length of a byte slice of it
			a := f.Rand.Intn(len(lit) + 1)
			b := a + f.Rand.Intn(len(lit)+1-a)
			fold(&BuiltinCallNode{Fn: "len", Arg: fmt.Sprintf("%s[%d:%d]", name, a, b)}, Int32(b-a))
		}
	}

	// A comparison, against the same literal as often as against another: both
	// outcomes are worth emitting, and a wrong answer either way is a wrong
	// checksum.
	other := stringCorpus[f.Rand.Intn(len(stringCorpus))]
	if f.Rand.Float32() < 0.5 {
		other = lit
	}
	eq := Int32(0)
	if lit == other {
		eq = Int32(f.Rand.Int31())
	}
	k := eq
	if k == 0 {
		k = Int32(f.Rand.Int31())
	}
	if lit == other {
		newChecksum, _ := vm.Eval("^", mem.Load(f.ChecksumName), k)
		mem.Store(f.ChecksumName, newChecksum)
	}
	stmts = append(stmts, &IfStmtNode{
		Cond: &BinaryExprNode{Left: &IdentNode{Name: name}, Op: "==", Right: &StringLitNode{Value: other}},
		Body: &BlockNode{Statements: []Node{&AssignStmtNode{
			Lhs: f.ChecksumName,
			Op:  "=",
			Rhs: &BinaryExprNode{Left: &IdentNode{Name: f.ChecksumName}, Op: "^", Right: &IntLitNode{Value: k.Literal()}},
		}}},
	})

	// A range over it, whose index is a byte offset and whose value is a rune.
	if len(lit) != 0 && f.Rand.Float32() < 0.3 {
		acc := Int32(0)
		for i, r := range lit {
			acc = Int32(int32(acc) ^ int32(i) ^ int32(r))
		}
		newChecksum, _ := vm.Eval("^", mem.Load(f.ChecksumName), acc)
		mem.Store(f.ChecksumName, newChecksum)
		iv, rv := f.newVarName("i"), f.newVarName("r")
		stmts = append(stmts, &RangeFoldNode{
			Index: iv, Value: rv, Over: name, Checksum: f.ChecksumName,
		})
	}
	return &BlockNode{Statements: stmts}
}

// genSizedStmt generates a block that declares a variable of a sized integer type,
// puts it through a few operations, and folds the result into the checksum.
//
// It is self-contained rather than woven into genExpression because the point is
// the TYPE: Go computes in the operands' own type while C promotes anything
// narrower than int, so `var a uint8 = 200; a * 3` is 88 in Go and was 600 here
// until v0.13.0. Every operator that can carry a value out of its type is offered,
// and the starting value is drawn from the type's extremes, where wrapping happens.
//
// The fold is through `int(z)`, which is also the conversion a program writes to
// get such a value into an ordinary integer -- and, being a conversion of a
// variable rather than a constant, is the shape that truncates rather than the one
// the checker range-checks.
func (f *Fuzzer) genSizedStmt(vm Machine, mem Memory) Node {
	k := sizedKinds[f.Rand.Intn(len(sizedKinds))]
	lo, hi := sizedRange(k)
	name := f.newVarName("z")

	// Start at an extreme as often as anywhere: that is where an operation leaves
	// the type, which is the whole question being asked.
	var start int64
	switch f.Rand.Intn(5) {
	case 0:
		start = lo
	case 1:
		start = hi
	case 2:
		start = hi / 2
	default:
		start = lo + f.Rand.Int63n(hi-lo+1)
	}
	cur := NewSized(start, k)

	stmts := []Node{&VarDeclNode{
		Name: name,
		Type: BasicType{Kind: k}.String(),
		Expr: &IntLitNode{Value: fmt.Sprint(cur.v)},
	}}

	bits, _, _ := sizedInfo(k)
	for i, n := 0, 1+f.Rand.Intn(3); i < n; i++ {
		node, next, ok := f.genSizedStep(name, cur, bits)
		if !ok {
			continue // an operator the emitted C leaves undefined; skip this step
		}
		stmts = append(stmts, node)
		cur = next
	}

	// The fold is over an expression, not over the variable, whenever one can be
	// built. That distinction is the whole point: storing a result back into a
	// narrow variable TRUNCATES it, in C as in Go, so a generator that only ever
	// reads the variable agrees with a compiler that has lost the type -- which is
	// exactly the blind spot the hand-written corpus had, and why `a * 3` printing
	// 600 instead of 88 went unnoticed for so long. Only a value used WITHOUT being
	// stored can tell the two apart.
	folded, foldNode := cur.Int32(), Node(&IdentNode{Name: name})
	if node, v, ok := f.genSizedFold(name, cur, bits); ok {
		folded, foldNode = v.Int32(), node
	}
	newChecksum, _ := vm.Eval("^", mem.Load(f.ChecksumName), folded)
	mem.Store(f.ChecksumName, newChecksum)
	stmts = append(stmts, &AssignStmtNode{
		Lhs: f.ChecksumName,
		Op:  "=",
		Rhs: &BinaryExprNode{
			Left:  &IdentNode{Name: f.ChecksumName},
			Op:    "^",
			Right: &ConvNode{Type: "int", X: foldNode},
		},
	})
	return &BlockNode{Statements: stmts}
}

// genSizedFold builds an expression over the sized variable whose value is never
// stored back -- `z * 7`, `-z`, `^z`, `z << 3` -- for the checksum to take. See the
// note at its caller: a stored result is truncated by the store, so only an
// unstored one can show a compiler computing in the wrong width.
func (f *Fuzzer) genSizedFold(name string, cur Sized, bits int) (Node, Sized, bool) {
	id := &IdentNode{Name: name}
	lit := func(v int64) Node { return &IntLitNode{Value: fmt.Sprint(v)} }
	switch f.Rand.Intn(6) {
	case 0:
		return &UnaryExprNode{Op: "-", X: id}, cur.neg(), true
	case 1:
		return &UnaryExprNode{Op: "^", X: id}, cur.not(), true
	case 2: // a shift, whose result leaves the type at the top end
		n := int64(f.Rand.Intn(bits))
		r, err := cur.binOp("<<", NewSized(n, cur.k))
		if err != nil {
			return nil, cur, false
		}
		return &BinaryExprNode{Left: id, Op: "<<", Right: lit(n)}, r.(Sized), true
	default: // z <op> <literal in range>, the arithmetic that overflows the width
		lo, hi := sizedRange(cur.k)
		v := lo + f.Rand.Int63n(hi-lo+1)
		ops := []string{"+", "-", "*"}
		op := ops[f.Rand.Intn(len(ops))]
		r, err := cur.binOp(op, NewSized(v, cur.k))
		if err != nil {
			return nil, cur, false
		}
		return &BinaryExprNode{Left: id, Op: op, Right: lit(v)}, r.(Sized), true
	}
}

// genSizedStep is one operation on a sized variable, `z = z * 7` or `z = ^z`,
// returning the statement and the value it leaves behind. ok is false when the
// operands would reach something the emitted C leaves undefined, which the caller
// skips rather than papering over.
func (f *Fuzzer) genSizedStep(name string, cur Sized, bits int) (Node, Sized, bool) {
	assign := func(rhs Node, v Value, err error) (Node, Sized, bool) {
		if err != nil {
			return nil, cur, false
		}
		return &AssignStmtNode{Lhs: name, Op: "=", Rhs: rhs}, v.(Sized), true
	}
	lit := func(v int64) Node { return &IntLitNode{Value: fmt.Sprint(v)} }
	switch f.Rand.Intn(9) {
	case 0: // z = -z
		return &AssignStmtNode{Lhs: name, Op: "=", Rhs: &UnaryExprNode{Op: "-", X: &IdentNode{Name: name}}}, cur.neg(), true
	case 1: // z = ^z
		return &AssignStmtNode{Lhs: name, Op: "=", Rhs: &UnaryExprNode{Op: "^", X: &IdentNode{Name: name}}}, cur.not(), true
	case 2, 3: // z = z <op> <literal in range>
		lo, hi := sizedRange(cur.k)
		v := lo + f.Rand.Int63n(hi-lo+1)
		ops := []string{"+", "-", "*", "&", "|", "^", "&^"}
		op := ops[f.Rand.Intn(len(ops))]
		r, err := cur.binOp(op, NewSized(v, cur.k))
		return assign(&BinaryExprNode{Left: &IdentNode{Name: name}, Op: op, Right: lit(v)}, r, err)
	case 4: // z = z / <nonzero literal>, z = z % <nonzero literal>
		lo, hi := sizedRange(cur.k)
		v := lo + f.Rand.Int63n(hi-lo+1)
		if v == 0 {
			v = 1
		}
		op := "/"
		if f.Rand.Float32() < 0.5 {
			op = "%"
		}
		r, err := cur.binOp(op, NewSized(v, cur.k))
		return assign(&BinaryExprNode{Left: &IdentNode{Name: name}, Op: op, Right: lit(v)}, r, err)
	case 5, 6: // z = z << n, z = z >> n
		n := int64(f.Rand.Intn(bits))
		op := "<<"
		if f.Rand.Float32() < 0.5 {
			op = ">>"
		}
		r, err := cur.binOp(op, NewSized(n, cur.k))
		return assign(&BinaryExprNode{Left: &IdentNode{Name: name}, Op: op, Right: lit(n)}, r, err)
	default: // a compound assignment, the other spelling of the same operation
		lo, hi := sizedRange(cur.k)
		v := lo + f.Rand.Int63n(hi-lo+1)
		ops := []string{"+=", "-=", "*=", "&=", "|=", "^=", "&^="}
		op := ops[f.Rand.Intn(len(ops))]
		r, err := cur.binOp(op[:len(op)-1], NewSized(v, cur.k))
		if err != nil {
			return nil, cur, false
		}
		return &AssignStmtNode{Lhs: name, Op: op, Rhs: lit(v)}, r.(Sized), true
	}
}

// genVarDecl generates: var <name> int = <expr>
func (f *Fuzzer) genVarDecl(vm Machine, mem Memory) Node {
	varName := f.newVarName("v")

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
// genArrayDecl declares a fixed integer array `var a [N]int`, zero-initialized (the
// emitter zero-inits too). Its elements are read and written by index elsewhere.
func (f *Fuzzer) genArrayDecl(vm Machine, mem Memory) Node {
	name := f.newVarName("a")
	n := 2 + f.Rand.Intn(3) // length 2..4
	mem.Store(name, &ArrayVal{Elems: make([]Int32, n)})
	f.CurrentEnv.Declare(name, ArrayType{Len: n, Elem: BasicType{Kind: KindInt}}, false)
	return &ArrayDeclNode{Name: name, Len: n}
}

// genArrayWrite assigns an integer expression to one element of an existing array,
// `a[c] = e`, with a constant in-bounds index, exercising the element-store codegen.
func (f *Fuzzer) genArrayWrite(vm Machine, mem Memory) Node {
	arrays := f.CurrentEnv.GetArraySymbols()
	if len(arrays) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	arr := arrays[f.Rand.Intn(len(arrays))]
	arr.Used = true
	idx := f.Rand.Intn(arr.Type.(ArrayType).Len)
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	mem.Load(arr.Name).(*ArrayVal).Elems[idx] = exprVal.(Int32)
	return &AssignStmtNode{Lhs: fmt.Sprintf("%s[%d]", arr.Name, idx), Op: "=", Rhs: exprNode}
}

// genSliceDecl declares an integer slice over a backing array of fixed capacity,
// `var s []int = make([]int, L, C)`. The length may be zero -- the empty slice is
// worth exercising -- but the capacity is always strictly greater, so every live
// slice can take at least one append (see genAppend for why capacity is tracked).
func (f *Fuzzer) genSliceDecl(vm Machine, mem Memory) Node {
	name := f.newVarName("s")
	n := f.Rand.Intn(4)         // length 0..3
	c := n + 1 + f.Rand.Intn(3) // capacity, one to three elements of headroom
	mem.Store(name, &SliceVal{Elems: make([]Int32, n), Cap: c})
	f.CurrentEnv.Declare(name, SliceType{Elem: BasicType{Kind: KindInt}}, false)
	return &SliceDeclNode{Name: name, Len: n, Cap: c}
}

// genSliceWrite assigns an integer expression to one element of an existing slice,
// `s[c] = e`, with a constant index below the slice's current length, exercising
// the element-store codegen through a slice header. A zero-length slice has no
// element to write, so it is not a candidate.
func (f *Fuzzer) genSliceWrite(vm Machine, mem Memory) Node {
	var targets []*Symbol
	for _, s := range f.CurrentEnv.GetSliceSymbols() {
		if len(mem.Load(s.Name).(*SliceVal).Elems) != 0 {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	sl := targets[f.Rand.Intn(len(targets))]
	sl.Used = true
	sv := mem.Load(sl.Name).(*SliceVal)
	idx := f.Rand.Intn(len(sv.Elems))
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	sv.Elems[idx] = exprVal.(Int32)
	return &AssignStmtNode{Lhs: fmt.Sprintf("%s[%d]", sl.Name, idx), Op: "=", Rhs: exprNode}
}

// genAppend grows an existing slice by one element, `s = append(s, e)`.
//
// Only a slice with spare capacity is a candidate. The target has no heap, so the
// emitted ogo_append cannot reallocate a full backing array and panics instead;
// tracking each slice's capacity in the VM is what keeps the generated program
// within that limit. Every element ever appended stays known to the VM, so a later
// index read of the grown slice still resolves to a value the oracle can predict.
func (f *Fuzzer) genAppend(vm Machine, mem Memory) Node {
	var targets []*Symbol
	for _, s := range f.CurrentEnv.GetSliceSymbols() {
		if sv := mem.Load(s.Name).(*SliceVal); len(sv.Elems) < sv.Cap {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	sl := targets[f.Rand.Intn(len(targets))]
	sl.Used = true
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	sv := mem.Load(sl.Name).(*SliceVal)
	sv.Elems = append(sv.Elems, exprVal.(Int32))
	return &AssignStmtNode{Lhs: sl.Name, Op: "=", Rhs: &AppendNode{Slice: sl.Name, Value: exprNode}}
}

// genCompoundAssign mutates an existing integer variable in place with a compound
// assignment (`x += e`, `x *= e`, `x <<= e`, ...), exercising the compound-assignment
// lowerings across the full operator set. The operator's binary form is evaluated in
// the VM so the oracle stays in sync; an operand combination undefined in C (see
// binOp) falls back to XOR, which is always defined.
func (f *Fuzzer) genCompoundAssign(vm Machine, mem Memory) Node {
	var targets []*Symbol
	for _, s := range f.CurrentEnv.GetSymbolsOfType(BasicType{Kind: KindInt}) {
		if !s.IsConst && !s.LoopVar {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	target := targets[f.Rand.Intn(len(targets))]
	target.Used = true
	op := []string{"+", "-", "*", "/", "%", "<<", ">>", "&", "|", "&^", "^"}[f.Rand.Intn(11)]
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	cur := mem.Load(target.Name)
	result, err := vm.Eval(op, cur, exprVal)
	if err != nil {
		op = "^" // undefined for these operands; XOR-assign the same operand instead
		result, _ = vm.Eval(op, cur, exprVal)
	}
	mem.Store(target.Name, result)
	return &AssignStmtNode{Lhs: target.Name, Op: op + "=", Rhs: exprNode}
}

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
		// Read an array element a[c] at a constant, in-bounds index. Its value is known
		// from the VM, so the oracle stays in sync with the compiled array read.
		if arrays := f.CurrentEnv.GetArraySymbols(); len(arrays) > 0 && f.Rand.Float32() < 0.25 {
			arr := arrays[f.Rand.Intn(len(arrays))]
			arr.Used = true
			idx := f.Rand.Intn(arr.Type.(ArrayType).Len)
			return &IndexNode{Name: arr.Name, Index: idx}, mem.Load(arr.Name).(*ArrayVal).Elems[idx], nil
		}
		// Read from an existing slice: an element s[c] at a constant index below the
		// current length, or len(s) / cap(s). The VM knows all three, so the oracle
		// stays in sync with the compiled slice.
		if slices := f.CurrentEnv.GetSliceSymbols(); len(slices) > 0 && f.Rand.Float32() < 0.25 {
			sl := slices[f.Rand.Intn(len(slices))]
			sl.Used = true
			sv := mem.Load(sl.Name).(*SliceVal)
			if n := len(sv.Elems); n > 0 && f.Rand.Float32() < 0.6 {
				idx := f.Rand.Intn(n)
				return &IndexNode{Name: sl.Name, Index: idx}, sv.Elems[idx], nil
			}
			if f.Rand.Float32() < 0.5 {
				return &BuiltinCallNode{Fn: "cap", Arg: sl.Name}, Int32(sv.Cap), nil
			}
			return &BuiltinCallNode{Fn: "len", Arg: sl.Name}, Int32(len(sv.Elems)), nil
		}
		// Read a field of an existing struct, `v.f`. The VM holds every field's
		// value, so the oracle stays in step with the compiled field read -- which
		// is where a wrong field offset would show.
		if structs := f.CurrentEnv.GetStructSymbols(); len(structs) != 0 && f.Rand.Float32() < 0.25 {
			sym := structs[f.Rand.Intn(len(structs))]
			sym.Used = true
			sv := mem.Load(sym.Name).(*StructVal)
			fld := sv.Def.Fields[f.Rand.Intn(len(sv.Def.Fields))]
			return &FieldNode{Name: sym.Name, Field: fld}, sv.Fields[fld], nil
		}
		// Call one of the generated functions. Its arguments are themselves
		// generated expressions, and the VM re-evaluates the callee's body against
		// their values, so the whole call is predicted by the oracle.
		if len(f.Funcs) != 0 && f.Rand.Float32() < 0.2 {
			node, val := f.genCall(vm, mem, depth)
			return node, val, nil
		}
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

	// Recursive case: a binary operation over the full integer operator set. The VM
	// evaluates each as it is generated (the oracle knows the operands' values), so a
	// choice that is undefined for those operands -- division/modulo by zero, a shift
	// amount outside [0, 32) -- is rejected below and swapped for XOR, which is always
	// defined; the emitter never emits an undefined form.
	op := []string{"+", "-", "*", "/", "%", "<<", ">>", "&", "|", "&^", "^"}[f.Rand.Intn(11)]

	leftNode, leftVal, _ := f.genExpression(targetType, vm, mem, depth+1)
	rightNode, rightVal, _ := f.genExpression(targetType, vm, mem, depth+1)

	resultVal, err := vm.Eval(op, leftVal, rightVal)
	if err != nil {
		op = "^" // undefined for these operands; XOR the same operands instead
		resultVal, _ = vm.Eval(op, leftVal, rightVal)
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

// UnaryExprNode is a prefix operator applied to an expression. The operand is
// always parenthesised: it can be a binary expression, and "!" binds tighter.
type UnaryExprNode struct {
	Op string
	X  Node
}

func (n *UnaryExprNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s(", n.Op)
	n.X.Write(w, 0)
	fmt.Fprint(w, ")")
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

type ArrayDeclNode struct {
	Name string
	Len  int
}

func (n *ArrayDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "var %s [%d]int", n.Name, n.Len)
}

// IndexNode is an element of an array or a slice at a constant index.
type IndexNode struct {
	Name  string
	Index int
}

func (n *IndexNode) Write(w io.Writer, indent int) {
	fmt.Fprintf(w, "%s[%d]", n.Name, n.Index)
}

type SliceDeclNode struct {
	Name string
	Len  int
	Cap  int
}

func (n *SliceDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "var %s []int = make([]int, %d, %d)", n.Name, n.Len, n.Cap)
}

// AppendNode is `append(s, v)`, generated only as the right-hand side of the
// assignment `s = append(s, v)` back to the same slice.
type AppendNode struct {
	Slice string
	Value Node
}

func (n *AppendNode) Write(w io.Writer, indent int) {
	fmt.Fprintf(w, "append(%s, ", n.Slice)
	n.Value.Write(w, 0)
	fmt.Fprint(w, ")")
}

// BuiltinCallNode is a one-argument builtin applied to a variable: len(s), cap(s).
type BuiltinCallNode struct {
	Fn  string
	Arg string
}

func (n *BuiltinCallNode) Write(w io.Writer, indent int) {
	fmt.Fprintf(w, "%s(%s)", n.Fn, n.Arg)
}

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

// SwitchClause is one clause of a SwitchStmtNode. An empty Values means "default";
// a nil Body means the clause does nothing when selected.
type SwitchClause struct {
	Values []int32
	Body   Node // Expected to be a BlockNode or nil
}

// SwitchStmtNode is a switch over Tag in which at most one clause has a body. The
// non-matching clauses come first, so a guard compared wrongly lands somewhere that
// does nothing and the checksum comes out wrong, rather than in the body by luck.
type SwitchStmtNode struct {
	Tag     Node
	Clauses []SwitchClause
}

func (n *SwitchStmtNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprint(w, "switch ")
	n.Tag.Write(w, 0)
	fmt.Fprint(w, " {\n")
	for _, c := range n.Clauses {
		writeIndent(w, indent)
		switch {
		case len(c.Values) == 0:
			fmt.Fprint(w, "default:\n")
		default:
			fmt.Fprint(w, "case ")
			for i, v := range c.Values {
				if i != 0 {
					fmt.Fprint(w, ", ")
				}
				fmt.Fprintf(w, "%d", v)
			}
			fmt.Fprint(w, ":\n")
		}
		if c.Body == nil {
			continue
		}
		for _, stmt := range c.Body.(*BlockNode).Statements {
			stmt.Write(w, indent+1)
			fmt.Fprint(w, "\n")
		}
	}
	writeIndent(w, indent)
	fmt.Fprint(w, "}")
}

// StringLitNode is a string literal, quoted the way Go quotes one -- which is what
// the scanner reads.
type StringLitNode struct{ Value string }

func (n *StringLitNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprint(w, strconv.Quote(n.Value))
}

// RangeFoldNode is `for i, r := range s { checksum = checksum ^ i ^ int(r) }`: the
// one shape that reads a string as runes rather than as bytes.
type RangeFoldNode struct {
	Index, Value, Over, Checksum string
}

func (n *RangeFoldNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "for %s, %s := range %s {\n", n.Index, n.Value, n.Over)
	writeIndent(w, indent+1)
	fmt.Fprintf(w, "%s = ((%s ^ %s) ^ int(%s))\n", n.Checksum, n.Checksum, n.Index, n.Value)
	writeIndent(w, indent)
	fmt.Fprint(w, "}")
}

// ConvNode is a conversion, `int(x)`.
type ConvNode struct {
	Type string
	X    Node
}

func (n *ConvNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s(", n.Type)
	n.X.Write(w, 0)
	fmt.Fprint(w, ")")
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

// FuncDeclNode writes a generated function: int parameters, one int result, and a
// body that is a single return of an expression.
type FuncDeclNode struct {
	Name   string
	Params []string
	Body   Node
}

func (n *FuncDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "func %s(", n.Name)
	for i, p := range n.Params {
		if i != 0 {
			fmt.Fprint(w, ", ")
		}
		fmt.Fprintf(w, "%s int", p)
	}
	fmt.Fprint(w, ") int {\n")
	writeIndent(w, indent+1)
	fmt.Fprint(w, "return ")
	n.Body.Write(w, 0)
	fmt.Fprint(w, "\n")
	writeIndent(w, indent)
	fmt.Fprint(w, "}\n")
}

// CallNode is a call to a generated function in expression position.
type CallNode struct {
	Fn   string
	Args []Node
}

func (n *CallNode) Write(w io.Writer, indent int) {
	fmt.Fprintf(w, "%s(", n.Fn)
	for i, a := range n.Args {
		if i != 0 {
			fmt.Fprint(w, ", ")
		}
		a.Write(w, 0)
	}
	fmt.Fprint(w, ")")
}

// genStructType generates a struct type declaration and writes it out. All fields
// are int, which keeps the VM's model one Int32 per field while still exercising
// what struct codegen actually gets wrong: field offsets, by-value copies and the
// per-type equality helper.
func (f *Fuzzer) genStructType() *StructDef {
	def := &StructDef{Name: f.newVarName("S")}
	for i, n := 0, 1+f.Rand.Intn(3); i < n; i++ {
		def.Fields = append(def.Fields, f.newVarName("f"))
	}
	f.Structs = append(f.Structs, def)
	(&StructTypeNode{Def: def}).Write(f.Out, 0)
	fmt.Fprint(f.Out, "\n")
	return def
}

// genStructDecl declares a struct variable, `var v S`, zero-initialized as the
// emitter zeroes it.
func (f *Fuzzer) genStructDecl(vm Machine, mem Memory) Node {
	if len(f.Structs) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	def := f.Structs[f.Rand.Intn(len(f.Structs))]
	name := f.newVarName("st")
	sv := &StructVal{Def: def, Fields: map[string]Int32{}}
	for _, fld := range def.Fields {
		sv.Fields[fld] = 0
	}
	mem.Store(name, sv)
	f.CurrentEnv.Declare(name, StructType{Def: def}, false)
	return &StructDeclNode{Name: name, TypeName: def.Name}
}

// genFieldWrite assigns an integer expression to one field, `v.f = e`.
func (f *Fuzzer) genFieldWrite(vm Machine, mem Memory) Node {
	structs := f.CurrentEnv.GetStructSymbols()
	if len(structs) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	sym := structs[f.Rand.Intn(len(structs))]
	sym.Used = true
	sv := mem.Load(sym.Name).(*StructVal)
	fld := sv.Def.Fields[f.Rand.Intn(len(sv.Def.Fields))]
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	sv.Fields[fld] = exprVal.(Int32)
	return &AssignStmtNode{Lhs: sym.Name + "." + fld, Op: "=", Rhs: exprNode}
}

// genStructCopy declares a new struct variable from an existing one, `w := v`.
// Go copies a struct by value, so the two are independent afterwards -- which is
// what the VM's Copy models and what a miscompile here would get wrong.
func (f *Fuzzer) genStructCopy(vm Machine, mem Memory) Node {
	structs := f.CurrentEnv.GetStructSymbols()
	if len(structs) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	src := structs[f.Rand.Intn(len(structs))]
	src.Used = true
	name := f.newVarName("st")
	sv := mem.Load(src.Name).(*StructVal)
	mem.Store(name, sv.Copy())
	f.CurrentEnv.Declare(name, StructType{Def: sv.Def}, false)
	return &ShortDeclNode{Name: name, Rhs: &IdentNode{Name: src.Name}}
}

// StructTypeNode writes a generated struct type declaration.
type StructTypeNode struct{ Def *StructDef }

func (n *StructTypeNode) Write(w io.Writer, indent int) {
	fmt.Fprintf(w, "type %s struct {\n", n.Def.Name)
	for _, f := range n.Def.Fields {
		writeIndent(w, indent+1)
		fmt.Fprintf(w, "%s int\n", f)
	}
	fmt.Fprint(w, "}\n")
}

// StructDeclNode is `var v S`, zero-initialized.
type StructDeclNode struct {
	Name     string
	TypeName string
}

func (n *StructDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "var %s %s", n.Name, n.TypeName)
}

// ShortDeclNode is `name := rhs`, used for the by-value struct copy.
type ShortDeclNode struct {
	Name string
	Rhs  Node
}

func (n *ShortDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s := ", n.Name)
	n.Rhs.Write(w, 0)
}

// FieldNode reads one field of a struct variable, `v.f`.
type FieldNode struct {
	Name  string
	Field string
}

func (n *FieldNode) Write(w io.Writer, indent int) {
	fmt.Fprintf(w, "%s.%s", n.Name, n.Field)
}
