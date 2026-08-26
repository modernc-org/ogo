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
	// 3.5. One interface type, satisfied by every struct above. It is declared only
	// when there is something to satisfy it: an interface no type implements can be
	// declared but never usefully held, and an unused declaration is noise in a
	// failing seed.
	if len(f.Structs) != 0 {
		fmt.Fprintf(f.Out, "type %s interface {\n\t%s() int\n}\n\n", ifaceTypeName, ifaceMethod)
	}

	// 3.6. Defined types over the sized integer kinds, for genSizedStmt to declare
	// its variables with. They must be here rather than beside the variable: a type
	// declared inside a function is refused ("statement TypeDecl is not supported
	// yet"), so package scope is the only scope there is.
	//
	// Not every kind draws one, so a seed exercises both spellings and a failing
	// seed says which was involved.
	f.SizedDefined = map[BasicKind]string{}
	for _, k := range sizedKinds {
		if f.Rand.Intn(2) != 0 {
			continue
		}
		name := f.newVarName("D")
		f.SizedDefined[k] = name
		fmt.Fprintf(f.Out, "type %s %s\n", name, BasicType{Kind: k}.String())
	}
	if len(f.SizedDefined) != 0 {
		fmt.Fprint(f.Out, "\n")
	}

	// 3.7. Defined types over []int, for genSliceDecl. Package scope for the same
	// reason as above, and a variable of one is made with "make(L_7, n, c)" -- the
	// capacity headroom the append generator needs, which a literal could not give.
	for i, n := 0, f.Rand.Intn(3); i < n; i++ {
		name := f.newVarName("L")
		f.SliceDefined = append(f.SliceDefined, name)
		fmt.Fprintf(f.Out, "type %s []int\n", name)
	}
	if len(f.SliceDefined) != 0 {
		fmt.Fprint(f.Out, "\n")
	}

	// 4. Generate the functions main will call. They come first so that every call
	// site in main has one to draw on, and they take no part in the environment:
	// a body reads only its own parameters (see genPureExpr).
	for i, n := 0, f.Rand.Intn(4); i < n; i++ {
		f.genFuncDecl()
	}

	// 4.5. A procedure carrying a deferred call, for main to call. It has to be a
	// procedure rather than one of the functions above: a defer runs at its
	// function's exit, and main's exit is after the checksum assertion, so a defer
	// written there could not be observed by the oracle at all.
	if f.Rand.Float32() < 0.5 {
		f.genDeferProc()
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
	// Body2 makes this a two-result function, `func f(p int) (int, int)`. It is a
	// second expression over the same parameters, so the VM predicts both results
	// the same way it predicts one. Such a function is not usable in expression
	// position, so genCall skips it and genDestructure is what calls it.
	Body2 Node
}

// results reports how many values a generated function returns.
func (d *FuncDef) results() int {
	if d.Body2 != nil {
		return 2
	}
	return 1
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
	if f.Rand.Float32() < 0.3 {
		// A two-result function, for the destructuring call sites. The second body
		// is generated the same way and is total for the same reason.
		fn.Body2 = f.genPureExpr(fn.Params, 0)
	}
	f.Funcs = append(f.Funcs, fn)
	(&FuncDeclNode{Name: fn.Name, Params: fn.Params, Body: fn.Body, Body2: fn.Body2}).Write(f.Out, 0)
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
	return f.evalBody(fn, fn.Body, args, vm)
}

// evalBody is evalCall over one of a function's result expressions, so a two-result
// function's second result is predicted the same way its first is.
func (f *Fuzzer) evalBody(fn *FuncDef, body Node, args map[string]Int32, vm Machine) Int32 {
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
	return eval(body)
}

// funcsWithResults returns the generated functions returning exactly n values. A
// two-result function is not a value, so it may only be called where two names
// receive it -- which is what keeps the two call sites apart.
func (f *Fuzzer) funcsWithResults(n int) []*FuncDef {
	var out []*FuncDef
	for _, fn := range f.Funcs {
		if fn.results() == n {
			out = append(out, fn)
		}
	}
	return out
}

// genCall generates a call to an already-declared function, its arguments being
// ordinary generated integer expressions. The VM re-evaluates the callee's body
// against those argument values, so the oracle predicts the result of the compiled
// call -- which is what puts argument passing, parameter binding and the returned
// value under test.
func (f *Fuzzer) genCall(vm Machine, mem Memory, depth int) (Node, Value) {
	one := f.funcsWithResults(1)
	fn := one[f.Rand.Intn(len(one))]
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
		return f.genMethodCall(vm, mem) // 3% chance for a method call
	case r < 0.46:
		return f.genVarDecl(vm, mem) // 9% chance for var
	case r < 0.52:
		return f.genArrayDecl(vm, mem) // 6% chance for a fixed array declaration
	case r < 0.58:
		return f.genArrayWrite(vm, mem) // 8% chance for an array element write
	case r < 0.60:
		return f.genElementSwap(vm, mem) // 2% chance for an element swap
	case r < 0.615:
		return f.genDestructure(vm, mem) // 1.5% chance for a two-result call
	case r < 0.62:
		return f.genMinMax(vm, mem) // 0.5% chance for a min or max
	case r < 0.63:
		return f.genDeferCall(vm, mem) // 1% chance for a call that defers
	case r < 0.66:
		return f.genSliceDecl(vm, mem) // 3% chance for a slice declaration
	case r < 0.74:
		return f.genSliceWrite(vm, mem) // 8% chance for a slice element write
	case r < 0.80:
		return f.genAppend(vm, mem) // 6% chance for an append
	case r < 0.85:
		return f.genStructDecl(vm, mem) // 5% chance for a struct declaration
	case r < 0.91:
		return f.genFieldWrite(vm, mem) // 6% chance for a struct field write
	case r < 0.94:
		return f.genStructCopy(vm, mem) // 3% chance for a by-value struct copy
	case r < 0.97:
		return f.genCompoundAssign(vm, mem) // 3% chance for a compound assignment
	case r < 0.985:
		// 1.5% chance for a pointer to an array. Taken from the checksum-mutation
		// filler below rather than from a neighbour, so no other construct's share
		// moved: this one emits a block of several statements, and thinning an
		// existing construct to pay for it would trade coverage for coverage.
		return f.genArrayPtrStmt(vm, mem)
	case r < 0.995:
		// 1% chance for an interface, taken from the checksum-mutation filler for
		// the same reason the line above was: it emits a block of several
		// statements, and paying for it out of a neighbour would trade coverage
		// for coverage.
		return f.genInterfaceStmt(vm, mem)
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
		start = int64(uint64(hi) / 2) // by the type's own reading; see pickSized
	default:
		start = f.pickSized(k)
	}
	cur := NewSized(start, k)

	// The type is written as the DEFINED one over this kind when the program drew
	// one, and as the predeclared kind otherwise. Nothing else about the block
	// changes: a defined type has the representation of what it is defined over, so
	// every step and the fold read the same, and the VM -- which keys values by name
	// and never sees this string -- is untouched.
	typeName := BasicType{Kind: k}.String()
	if d, ok := f.SizedDefined[k]; ok {
		typeName = d
	}
	stmts := []Node{&VarDeclNode{
		Name: name,
		Type: typeName,
		Expr: &IntLitNode{Value: sizedLitText(cur.v, k)},
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
	// At 64 bits the fold above carries only the LOW word: `int(z)` on this target
	// is a 32-bit truncation, so a value wrong only in its top half would agree with
	// the VM and the run would pass. The high half goes in as well, through the
	// shift the program itself must compute -- which is also the operation the
	// backend has been wrong about before.
	if bits == 64 {
		hi, err := cur.binOp(">>", NewSized(32, k))
		if err == nil {
			hiSized := hi.(Sized)
			c2, _ := vm.Eval("^", mem.Load(f.ChecksumName), hiSized.Int32())
			mem.Store(f.ChecksumName, c2)
			stmts = append(stmts, &AssignStmtNode{
				Lhs: f.ChecksumName,
				Op:  "=",
				Rhs: &BinaryExprNode{
					Left: &IdentNode{Name: f.ChecksumName},
					Op:   "^",
					Right: &ConvNode{Type: "int", X: &BinaryExprNode{
						Left:  &IdentNode{Name: name},
						Op:    ">>",
						Right: &IntLitNode{Value: "32"},
					}},
				},
			})
		}
	}
	return &BlockNode{Statements: stmts}
}

// pickSized draws a value of kind k as the BIT PATTERN the VM holds it by. At 64
// bits the range does not fit the arithmetic a range pick is made of -- an unsigned
// maximum is not an int64, and a signed span overflows it -- so a full-width draw
// stands in, which is the same distribution the narrow kinds get from their range.
func (f *Fuzzer) pickSized(k BasicKind) int64 {
	if bits, _, ok := sizedInfo(k); ok && bits == 64 {
		return int64(f.Rand.Uint64())
	}
	lo, hi := sizedRange(k)
	return lo + f.Rand.Int63n(hi-lo+1)
}

// genSizedFold builds an expression over the sized variable whose value is never
// stored back -- `z * 7`, `-z`, `^z`, `z << 3` -- for the checksum to take. See the
// note at its caller: a stored result is truncated by the store, so only an
// unstored one can show a compiler computing in the wrong width.
func (f *Fuzzer) genSizedFold(name string, cur Sized, bits int) (Node, Sized, bool) {
	id := &IdentNode{Name: name}
	lit := func(v int64) Node { return &IntLitNode{Value: sizedLitText(v, cur.k)} }
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
		v := f.pickSized(cur.k)
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
	lit := func(v int64) Node { return &IntLitNode{Value: sizedLitText(v, cur.k)} }
	switch f.Rand.Intn(9) {
	case 0: // z = -z
		return &AssignStmtNode{Lhs: name, Op: "=", Rhs: &UnaryExprNode{Op: "-", X: &IdentNode{Name: name}}}, cur.neg(), true
	case 1: // z = ^z
		return &AssignStmtNode{Lhs: name, Op: "=", Rhs: &UnaryExprNode{Op: "^", X: &IdentNode{Name: name}}}, cur.not(), true
	case 2, 3: // z = z <op> <literal in range>
		v := f.pickSized(cur.k)
		ops := []string{"+", "-", "*", "&", "|", "^", "&^"}
		op := ops[f.Rand.Intn(len(ops))]
		r, err := cur.binOp(op, NewSized(v, cur.k))
		return assign(&BinaryExprNode{Left: &IdentNode{Name: name}, Op: op, Right: lit(v)}, r, err)
	case 4: // z = z / <nonzero literal>, z = z % <nonzero literal>
		v := f.pickSized(cur.k)
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
		v := f.pickSized(cur.k)
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

// genDeferProc writes a procedure whose body defers a call, and records the effect
// that call has on the checksum so a call site can predict it.
//
// What it pins is the one thing about defer a reader cannot see: the ARGUMENT is
// evaluated where the defer stands, not where the deferred call runs. The body
// changes the variable afterwards and folds the new value in itself, so the two
// values are different and both reach the checksum -- a compiler that re-read the
// variable at the return would fold the same one twice and answer wrong.
//
// The sink is a procedure of its own because a deferred call's result is discarded:
// the only way its running is observable is a write to a package variable.
func (f *Fuzzer) genDeferProc() {
	proc := f.newVarName("dp")
	captured := int32(f.Rand.Intn(1 << 20))
	changed := int32(f.Rand.Intn(1 << 20))

	if len(f.Structs) != 0 && f.Rand.Float32() < 0.5 {
		// A deferred METHOD call on a value receiver. Go evaluates the receiver
		// where the defer stands and copies it, so the method runs on the struct as
		// it was THEN -- the field is changed afterwards and folded in by the body,
		// so a compiler that re-read the receiver at the return folds the same value
		// twice. That is the bug this release fixed for a receiver reached through a
		// chain, and nothing generated could reach it.
		def := f.Structs[f.Rand.Intn(len(f.Structs))]
		recv := f.newVarName("dr")
		fmt.Fprintf(f.Out, "var %s %s\n\n", recv, def.Name)
		fmt.Fprintf(f.Out, "func %s() {\n", proc)
		fmt.Fprintf(f.Out, "\t%s.%s = %d\n", recv, def.Fields[0], captured)
		fmt.Fprintf(f.Out, "\tdefer %s.%s()\n", recv, def.Emit)
		fmt.Fprintf(f.Out, "\t%s.%s = %d\n", recv, def.Fields[0], changed)
		fmt.Fprintf(f.Out, "\t%s = %s ^ %s.%s\n", f.ChecksumName, f.ChecksumName, recv, def.Fields[0])
		fmt.Fprint(f.Out, "}\n\n")
		f.DeferProcs = append(f.DeferProcs, deferProc{name: proc, folds: []int32{changed, captured}})
		return
	}

	sink := f.newVarName("sink")
	fmt.Fprintf(f.Out, "func %s(v int) { %s = %s ^ v }\n\n", sink, f.ChecksumName, f.ChecksumName)
	fmt.Fprintf(f.Out, "func %s() {\n", proc)
	fmt.Fprintf(f.Out, "\tv := %d\n", captured)
	fmt.Fprintf(f.Out, "\tdefer %s(v)\n", sink)
	fmt.Fprintf(f.Out, "\tv = %d\n", changed)
	fmt.Fprintf(f.Out, "\t%s = %s ^ v\n", f.ChecksumName, f.ChecksumName)
	fmt.Fprint(f.Out, "}\n\n")

	// In call order: the body's own fold, then the deferred one at the return.
	f.DeferProcs = append(f.DeferProcs, deferProc{name: proc, folds: []int32{changed, captured}})
}

// genDeferCall calls one of the generated defer-carrying procedures, folding its
// two checksum effects in the order the program will apply them.
func (f *Fuzzer) genDeferCall(vm Machine, mem Memory) Node {
	if len(f.DeferProcs) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	dp := f.DeferProcs[f.Rand.Intn(len(f.DeferProcs))]
	for _, fold := range dp.folds {
		cur := mem.Load(f.ChecksumName)
		v, _ := vm.Eval("int_lit", fmt.Sprint(fold))
		next, _ := vm.Eval("^", cur, v)
		mem.Store(f.ChecksumName, next)
	}
	return &CallStmtNode{Fn: dp.name}
}

// CallStmtNode is a call in statement position, its result (if any) discarded.
type CallStmtNode struct{ Fn string }

func (n *CallStmtNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s()", n.Fn)
}

// deferProc is a generated procedure carrying a deferred call, and the checksum
// folds calling it performs, in the order they happen.
type deferProc struct {
	name  string
	folds []int32
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

// genArrayPtrStmt takes the address of an existing array, `p := &a`, and exercises
// every way a pointer to one is read and written: an element written through the
// pointer, an element read back through the ARRAY'S OWN NAME, `len(p)`, and a range
// over it.
//
// A pointer to an array is the one pointer an index applies to -- `p[i]` abbreviates
// `(*p)[i]` -- so this covers the dereference every use of one carries. The oracle
// property that matters is the read-back: the write goes through the pointer and the
// checksum reads the array, so a compiler that dropped the dereference (or wrote to
// the wrong place) leaves the old value there and the checksum says so.
//
// The whole thing is one block, like the string and sized-integer generators: the
// pointer is declared and used inside it and never enters the environment, so no
// later statement can name a variable whose scope has closed.
func (f *Fuzzer) genArrayPtrStmt(vm Machine, mem Memory) Node {
	arrays := f.CurrentEnv.GetArraySymbols()
	if len(arrays) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	arr := arrays[f.Rand.Intn(len(arrays))]
	arr.Used = true
	at := arr.Type.(ArrayType)
	av := mem.Load(arr.Name).(*ArrayVal)
	name := f.newVarName("pa")

	stmts := []Node{&ArrayPtrDeclNode{Name: name, Array: arr.Name}}
	fold := func(n Node, v Int32) {
		newChecksum, _ := vm.Eval("^", mem.Load(f.ChecksumName), v)
		mem.Store(f.ChecksumName, newChecksum)
		stmts = append(stmts, &AssignStmtNode{
			Lhs: f.ChecksumName,
			Op:  "=",
			Rhs: &BinaryExprNode{Left: &IdentNode{Name: f.ChecksumName}, Op: "^", Right: n},
		})
	}

	// A write THROUGH the pointer, then the same element read back through the
	// array. Both names reach one ArrayVal here, which is what the target must do
	// too -- the pointer aliases the array rather than copying it.
	idx := f.Rand.Intn(at.Len)
	exprNode, exprVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
	av.Elems[idx] = exprVal.(Int32)
	stmts = append(stmts, &AssignStmtNode{
		Lhs: fmt.Sprintf("%s[%d]", name, idx),
		Op:  "=",
		Rhs: exprNode,
	})
	fold(&IndexNode{Name: arr.Name, Index: idx}, av.Elems[idx])

	// Read back through the POINTER as well, and its length: len(p) is the extent
	// of what it points at, not the size of a pointer.
	j := f.Rand.Intn(at.Len)
	fold(&IndexNode{Name: name, Index: j}, av.Elems[j])
	fold(&BuiltinCallNode{Fn: "len", Arg: name}, Int32(at.Len))

	// A range over the pointer iterates the array it points at. Unconditional, so
	// every occurrence covers all three ways one is read -- index, len and range --
	// which is what keeps the construct's coverage from depending on two dice.
	{
		acc := Int32(0)
		for i, v := range av.Elems {
			acc = Int32(int32(acc) ^ int32(i) ^ int32(v))
		}
		newChecksum, _ := vm.Eval("^", mem.Load(f.ChecksumName), acc)
		mem.Store(f.ChecksumName, newChecksum)
		iv, vv := f.newVarName("i"), f.newVarName("v")
		stmts = append(stmts, &RangeFoldNode{
			Index: iv, Value: vv, Over: name, Checksum: f.ChecksumName,
		})
	}
	return &BlockNode{Statements: stmts}
}

// genElementSwap exchanges two elements of one array or slice through a multiple
// assignment, `a[i], a[j] = a[j], a[i]` -- the swap every sort is written with, and
// the one statement shape whose targets are LVALUES rather than names.
//
// The oracle earns its keep here: a compiler that assigned the first target before
// evaluating the second right-hand operand would leave both elements holding the
// same value, which is an ordinary-looking answer and a wrong checksum.
func (f *Fuzzer) genElementSwap(vm Machine, mem Memory) Node {
	type candidate struct {
		sym  *Symbol
		vals []Int32
	}
	var cands []candidate
	for _, s := range f.CurrentEnv.GetArraySymbols() {
		if av := mem.Load(s.Name).(*ArrayVal); len(av.Elems) >= 2 {
			cands = append(cands, candidate{s, av.Elems})
		}
	}
	for _, s := range f.CurrentEnv.GetSliceSymbols() {
		if sv := mem.Load(s.Name).(*SliceVal); len(sv.Elems) >= 2 {
			cands = append(cands, candidate{s, sv.Elems})
		}
	}
	if len(cands) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	c := cands[f.Rand.Intn(len(cands))]
	c.sym.Used = true
	i := f.Rand.Intn(len(c.vals))
	j := f.Rand.Intn(len(c.vals) - 1)
	if j >= i {
		j++ // a distinct index: swapping one with itself proves nothing
	}
	c.vals[i], c.vals[j] = c.vals[j], c.vals[i]
	return &SwapStmtNode{
		A: fmt.Sprintf("%s[%d]", c.sym.Name, i),
		B: fmt.Sprintf("%s[%d]", c.sym.Name, j),
	}
}

// genDestructure calls a two-result function and binds both results to new names,
// `a, b := fn(x)`. It is the shape every multiple-value form in the language shares
// -- the header declaration, a select clause's receive and the plain statement all
// lower through it -- and the oracle predicts both results, so a compiler that
// mixed them up, or dropped one, answers with a wrong checksum.
func (f *Fuzzer) genDestructure(vm Machine, mem Memory) Node {
	two := f.funcsWithResults(2)
	if len(two) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	fn := two[f.Rand.Intn(len(two))]
	args := map[string]Int32{}
	var argNodes []Node
	for _, p := range fn.Params {
		node, val, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
		argNodes = append(argNodes, node)
		args[p] = val.(Int32)
	}
	a, b := f.newVarName("d"), f.newVarName("d")
	mem.Store(a, f.evalBody(fn, fn.Body, args, vm))
	mem.Store(b, f.evalBody(fn, fn.Body2, args, vm))
	f.CurrentEnv.Declare(a, BasicType{Kind: KindInt}, false)
	f.CurrentEnv.Declare(b, BasicType{Kind: KindInt}, false)
	return &DestructureNode{A: a, B: b, Call: &CallNode{Fn: fn.Name, Args: argNodes}}
}

// genMinMax declares a variable holding min or max over two to four generated
// integer expressions.
//
// It is worth generating for the same reason the variadic fold exists: the builtin
// is lowered as a two-argument helper applied left to right, so `min(a, b, c)` is
// `min(min(a, b), c)`, and an argument evaluated twice or folded in the wrong order
// would still look like an ordinary number. The VM picks the smallest of the same
// values, so it does not.
func (f *Fuzzer) genMinMax(vm Machine, mem Memory) Node {
	name := f.newVarName("v")
	which := "min"
	if f.Rand.Intn(2) == 0 {
		which = "max"
	}
	var args []Node
	var best Int32
	for i, n := 0, 2+f.Rand.Intn(3); i < n; i++ {
		node, val, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 1)
		args = append(args, node)
		v := val.(Int32)
		switch {
		case i == 0:
			best = v
		case which == "min" && v < best, which == "max" && v > best:
			best = v
		}
	}
	mem.Store(name, best)
	f.CurrentEnv.Declare(name, BasicType{Kind: KindInt}, false)
	return &VarDeclNode{Name: name, Type: "int", Expr: &CallNode{Fn: which, Args: args}}
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
	// The environment records it as a slice of int either way: a defined slice type
	// behaves as one for every operation the other generators perform on it, and
	// none of them assigns one slice variable to another, which is the only place
	// the distinct identity would show.
	f.CurrentEnv.Declare(name, SliceType{Elem: BasicType{Kind: KindInt}}, false)
	typ := ""
	if len(f.SliceDefined) != 0 && f.Rand.Intn(2) == 0 {
		typ = f.SliceDefined[f.Rand.Intn(len(f.SliceDefined))]
	}
	return &SliceDeclNode{Name: name, Type: typ, Len: n, Cap: c}
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
		if len(f.funcsWithResults(1)) != 0 && f.Rand.Float32() < 0.2 {
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

// SwapStmtNode is a two-target multiple assignment exchanging two element values,
// `a[i], a[j] = a[j], a[i]`. Both targets are lvalues rather than names, and every
// right-hand operand is read before either is written.
type SwapStmtNode struct{ A, B string }

func (n *SwapStmtNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s, %s = %s, %s", n.A, n.B, n.B, n.A)
}

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

// ArrayPtrDeclNode takes the address of an array, `p := &a`. The pointer aliases
// the array rather than copying it, which is what the statements around it check.
type ArrayPtrDeclNode struct {
	Name  string
	Array string
}

func (n *ArrayPtrDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s := &%s", n.Name, n.Array)
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
	// Type is the slice type as written, "[]int" or a defined name over it. The
	// same name goes in the make, which is what "make(L_7, n, c)" needs to be.
	Type string
	Len  int
	Cap  int
}

func (n *SliceDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	t := n.Type
	if t == "" {
		t = "[]int"
	}
	fmt.Fprintf(w, "var %s %s = make(%s, %d, %d)", n.Name, t, t, n.Len, n.Cap)
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
	Body2  Node // non-nil for a two-result function
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
	if n.Body2 != nil {
		fmt.Fprint(w, ") (int, int) {\n")
	} else {
		fmt.Fprint(w, ") int {\n")
	}
	writeIndent(w, indent+1)
	fmt.Fprint(w, "return ")
	n.Body.Write(w, 0)
	if n.Body2 != nil {
		fmt.Fprint(w, ", ")
		n.Body2.Write(w, 0)
	}
	fmt.Fprint(w, "\n")
	writeIndent(w, indent)
	fmt.Fprint(w, "}\n")
}

// DestructureNode is a two-result call bound to two new names, `a, b := fn(x)`.
type DestructureNode struct {
	A, B string
	Call Node
}

func (n *DestructureNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s, %s := ", n.A, n.B)
	n.Call.Write(w, 0)
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
	def.Get = f.newVarName("get")
	def.Set = f.newVarName("set")
	def.Shadow = f.newVarName("shadow")
	def.Emit = f.newVarName("emit")
	def.Checksum = f.ChecksumName
	f.Structs = append(f.Structs, def)
	(&StructTypeNode{Def: def}).Write(f.Out, 0)
	fmt.Fprint(f.Out, "\n")
	(&MethodsNode{Def: def}).Write(f.Out, 0)
	fmt.Fprint(f.Out, "\n")
	return def
}

// genMethodCall calls one of a struct variable's three methods and folds what it
// returns into the checksum.
//
// The three exist to pin the receiver rule, which is the part of a method that
// codegen can get wrong: a POINTER receiver reaches the caller's struct and a
// VALUE receiver reaches a copy of it. So the same body -- write the argument into
// the first field, return it -- is generated with each, and after the call the
// field is read back: through the pointer receiver it changed, through the value
// receiver it did not. A wrong receiver adjustment shows as a wrong checksum
// either way round.
func (f *Fuzzer) genMethodCall(vm Machine, mem Memory) Node {
	structs := f.CurrentEnv.GetStructSymbols()
	if len(structs) == 0 {
		return f.genChecksumMutation(vm, mem)
	}
	sym := structs[f.Rand.Intn(len(structs))]
	sym.Used = true
	sv := mem.Load(sym.Name).(*StructVal)
	f0 := sv.Def.Fields[0]

	var call Node
	var result Int32
	switch f.Rand.Intn(3) {
	case 0: // v.get()
		call = &MethodCallNode{Recv: sym.Name, Method: sv.Def.Get}
		result = sv.Fields[f0]
	case 1: // v.set(e) -- through a pointer receiver, so the field really changes
		argNode, argVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
		call = &MethodCallNode{Recv: sym.Name, Method: sv.Def.Set, Arg: argNode}
		result = argVal.(Int32)
		sv.Fields[f0] = result
	default: // v.shadow(e) -- through a value receiver, so the field does NOT change
		argNode, argVal, _ := f.genExpression(BasicType{Kind: KindInt}, vm, mem, 0)
		call = &MethodCallNode{Recv: sym.Name, Method: sv.Def.Shadow, Arg: argNode}
		result = argVal.(Int32)
	}

	stmts := []Node{&AssignStmtNode{
		Lhs: f.ChecksumName,
		Op:  "=",
		Rhs: &BinaryExprNode{Left: &IdentNode{Name: f.ChecksumName}, Op: "^", Right: call},
	}}
	newChecksum, _ := vm.Eval("^", mem.Load(f.ChecksumName), result)
	mem.Store(f.ChecksumName, newChecksum)

	// Read the field back: this is what tells the two receivers apart.
	stmts = append(stmts, &AssignStmtNode{
		Lhs: f.ChecksumName,
		Op:  "=",
		Rhs: &BinaryExprNode{
			Left:  &IdentNode{Name: f.ChecksumName},
			Op:    "^",
			Right: &FieldNode{Name: sym.Name, Field: f0},
		},
	})
	newChecksum, _ = vm.Eval("^", mem.Load(f.ChecksumName), sv.Fields[f0])
	mem.Store(f.ChecksumName, newChecksum)
	return &BlockNode{Statements: stmts}
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

// MethodsNode writes a struct's three methods. Their bodies are fixed; see
// StructDef and genMethodCall for what each is for.
type MethodsNode struct{ Def *StructDef }

func (n *MethodsNode) Write(w io.Writer, indent int) {
	d, f0 := n.Def, n.Def.Fields[0]
	fmt.Fprintf(w, "func (r %s) %s() int { return r.%s }\n\n", d.Name, d.Get, f0)
	fmt.Fprintf(w, "func (r *%s) %s(v int) int {\n\tr.%s = v\n\treturn r.%s\n}\n\n", d.Name, d.Set, f0, f0)
	fmt.Fprintf(w, "func (r %s) %s(v int) int {\n\tr.%s = v\n\treturn r.%s\n}\n\n", d.Name, d.Shadow, f0, f0)
	fmt.Fprintf(w, "func (r %s) %s() { %s = %s ^ r.%s }\n", d.Name, d.Emit, d.Checksum, d.Checksum, f0)
	// Val is the one method every struct spells the SAME way, which is what lets a
	// single interface type be satisfied by all of them -- and therefore what makes
	// the dispatch dynamic rather than a call with extra steps. A POINTER receiver,
	// because a pointer is what an interface holds here.
	fmt.Fprintf(w, "\nfunc (r *%s) %s() int { return r.%s }\n", d.Name, ifaceMethod, f0)
}

// MethodCallNode is `v.m()` or `v.m(arg)`.
type MethodCallNode struct {
	Recv, Method string
	Arg          Node
}

func (n *MethodCallNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s.%s(", n.Recv, n.Method)
	if n.Arg != nil {
		n.Arg.Write(w, 0)
	}
	fmt.Fprint(w, ")")
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

// ifaceTypeName and ifaceMethod name the one interface every generated program
// declares, and the method every generated struct implements. Both are fixed rather
// than counted: the point of an interface here is that MORE THAN ONE concrete type
// satisfies it, and two types cannot share a method set if their methods are named
// apart.
const (
	ifaceTypeName = "Valuer"
	ifaceMethod   = "Val"
)

// IfaceDeclNode is `var i Valuer = &v`, binding an interface to a struct variable.
type IfaceDeclNode struct {
	Name, Recv string
}

func (n *IfaceDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "var %s %s = &%s", n.Name, ifaceTypeName, n.Recv)
}

// IfaceCallNode is `i.Val()`, a call THROUGH the interface -- an indirect call via
// the vtable, where a direct one would be a load.
type IfaceCallNode struct {
	Name string
}

func (n *IfaceCallNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s.%s()", n.Name, ifaceMethod)
}

// AssertCallNode is `i.(*S).Val()`: an assertion to the concrete type, and the call
// on its result where it stands. The assertion must SUCCEED -- the generator knows
// the dynamic type -- because a failing one panics, and a panicking program tells
// the oracle nothing it can check.
type AssertCallNode struct {
	Name, Type string
}

func (n *AssertCallNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "%s.(*%s).%s()", n.Name, n.Type, ifaceMethod)
}

// IfaceSwitchNode is a type switch over an interface, with one case per concrete
// type the program declares and a default. Exactly one arm runs, and which one is
// what the vtable comparison decides; the VM knows which, so a wrong dispatch shows
// as a wrong checksum.
type IfaceSwitchNode struct {
	Iface, Checksum string
	Types           []string // one case per concrete type, in order
	Weights         []int    // the multiplier each arm folds with, so the arms differ
}

func (n *IfaceSwitchNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "switch x := %s.(type) {\n", n.Iface)
	for i, t := range n.Types {
		writeIndent(w, indent)
		fmt.Fprintf(w, "case *%s:\n", t)
		writeIndent(w, indent+1)
		fmt.Fprintf(w, "%s = %s ^ (x.%s() * %d)\n", n.Checksum, n.Checksum, ifaceMethod, n.Weights[i])
	}
	writeIndent(w, indent)
	fmt.Fprint(w, "default:\n")
	writeIndent(w, indent+1)
	fmt.Fprintf(w, "%s = %s ^ 99\n", n.Checksum, n.Checksum)
	writeIndent(w, indent)
	fmt.Fprint(w, "}")
}

// genInterfaceStmt binds an interface to a struct variable and then reads its field
// back through it three ways: a plain call, an assertion to the concrete type, and a
// type switch. All three must agree, and the VM knows what they agree ON, because
// the generator chose the concrete type and can look the field up directly.
//
// This is what makes an interface fuzzable at all despite the oracle needing to
// predict everything: dispatch is dynamic to the COMPILER and static to the
// generator.
func (f *Fuzzer) genInterfaceStmt(vm Machine, mem Memory) Node {
	if len(f.Structs) == 0 {
		return f.genChecksumMutation(vm, mem) // no concrete type to hold
	}
	// A struct VARIABLE is what an interface is bound to, and there may be none in
	// scope yet. Declaring one rather than giving up is what makes this construct
	// appear as often as its share of the dispatch says it should: bailing turned
	// most of the draws into a plain checksum mutation.
	//
	// It is declared in a SCOPE OF ITS OWN, pushed and popped around this block,
	// because the block is what it is written inside: registered in the enclosing
	// scope instead, the generator went on referring to a name C had already
	// dropped at the closing brace.
	var stmts []Node
	structs := f.CurrentEnv.GetStructSymbols()
	if len(structs) == 0 {
		mem.PushScope()
		f.CurrentEnv = NewScope(f.CurrentEnv)
		defer func() {
			f.CurrentEnv = f.CurrentEnv.Parent
			mem.PopScope()
		}()
		stmts = append(stmts, f.genStructDecl(vm, mem))
		if structs = f.CurrentEnv.GetStructSymbols(); len(structs) == 0 {
			return f.genChecksumMutation(vm, mem)
		}
	}
	sym := structs[f.Rand.Intn(len(structs))]
	sym.Used = true
	sv := mem.Load(sym.Name).(*StructVal)
	val := sv.Fields[sv.Def.Fields[0]]

	name := f.newVarName("if")
	stmts = append(stmts, &IfaceDeclNode{Name: name, Recv: sym.Name})

	fold := func(n Node, v Int32) {
		stmts = append(stmts, &AssignStmtNode{
			Lhs: f.ChecksumName,
			Op:  "=",
			Rhs: &BinaryExprNode{Left: &IdentNode{Name: f.ChecksumName}, Op: "^", Right: n},
		})
		cs, _ := vm.Eval("^", mem.Load(f.ChecksumName), v)
		mem.Store(f.ChecksumName, cs)
	}
	fold(&IfaceCallNode{Name: name}, val)
	fold(&AssertCallNode{Name: name, Type: sv.Def.Name}, val)

	// The type switch, over every concrete type the program has. Only the arm for
	// this variable's type runs; the others are there to be NOT taken, which is the
	// half of a vtable comparison a single-case switch would never exercise.
	sw := &IfaceSwitchNode{Iface: name, Checksum: f.ChecksumName}
	var taken Value = Int32(0)
	for i, def := range f.Structs {
		sw.Types = append(sw.Types, def.Name)
		sw.Weights = append(sw.Weights, i+2)
		if def == sv.Def {
			// The arm multiplies the field by its weight, and the VM refuses to
			// model an int32 overflow rather than guess at it. What it cannot
			// predict must not be GENERATED: the oracle's whole claim is that it
			// knows the answer, so the switch is dropped instead.
			//
			// The error was discarded here, which left taken nil and panicked the
			// generator on the next Eval -- "interface conversion: interface is
			// nil" -- for any seed whose field grew large enough. Reachable all
			// along; seed 486 found it once a change upstream shifted the random
			// stream far enough to try one.
			t, err := vm.Eval("*", val, Int32(i+2))
			if err != nil {
				return &BlockNode{Statements: stmts}
			}
			taken = t
		}
	}
	stmts = append(stmts, sw)
	cs, _ := vm.Eval("^", mem.Load(f.ChecksumName), taken)
	mem.Store(f.ChecksumName, cs)
	return &BlockNode{Statements: stmts}
}
