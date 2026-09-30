// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import (
	"fmt"
	"io"
	"math"
	"strings"
)

// ConstDef is a constant the program declares: its name, the value the generator
// gave it, and its type when it has one. Every value is an integer, however it is
// written. Float marks one written as a float, `2.5e3`: an untyped FLOAT constant of
// an integral value, which meets a variable and never another constant, since Go
// computes constants exactly and `2.5e3 / 3` is no integer.
type ConstDef struct {
	Name  string
	Val   int64     // the value; a typed constant's is the pattern its kind holds (see Sized)
	Typed bool      // declared with a type, the one type it meets
	Kind  BasicKind // a typed constant's kind
	Type  string    // a typed constant's type as written: predeclared, or defined over Kind
	Float bool      // untyped and written as a float
}

// constLimit bounds an untyped constant's value, so that the generator's own
// arithmetic on one -- an expression of two, a block's shadow of one -- stays exact
// in an int64.
const constLimit = 1 << 62

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// fitsKind reports whether the untyped constant value v is representable in the kind
// k, which is where Go converts it to k and refuses the program when it is not. An
// int is 32 bits on the target.
func fitsKind(v int64, k BasicKind) bool {
	bits, signed, ok := sizedInfo(k)
	switch {
	case !ok: // int
		return v >= math.MinInt32 && v <= math.MaxInt32
	case bits == 64:
		return signed || v >= 0 // below constLimit either way
	}
	lo, hi := sizedRange(k)
	return v >= lo && v <= hi
}

// genConstDecls declares the package's constants: an untyped group written every
// way a constant is read -- a small and a middling decimal, one past 2^31 and one of
// 64 bits, a negative one, hex, a rune, a float spelling and an expression of the ones
// before it -- an iota group, and a typed constant or three, of a predeclared kind or
// of a defined type over one, written with the type or as a conversion. The values
// are kept, so a use is predicted as a literal's is.
func (f *Fuzzer) genConstDecls() {
	var b strings.Builder
	b.WriteString("const (\n")
	for i, n := 0, 4+f.Rand.Intn(5); i < n; i++ {
		c := &ConstDef{Name: f.newVarName("k")}
		fmt.Fprintf(&b, "\t%s = %s\n", c.Name, f.untypedConstText(c))
		f.Consts = append(f.Consts, c)
	}
	b.WriteString(")\n\n")
	// An iota group: a spec's value is its index, and a spec with no expression
	// repeats the one before it.
	m, add := int64(1+f.Rand.Intn(9)), int64(f.Rand.Intn(20))
	b.WriteString("const (\n")
	for i, n := int64(0), int64(2+f.Rand.Intn(3)); i < n; i++ {
		c := &ConstDef{Name: f.newVarName("k"), Val: i*m + add}
		if i == 0 {
			fmt.Fprintf(&b, "\t%s = iota*%d + %d\n", c.Name, m, add)
		} else {
			fmt.Fprintf(&b, "\t%s\n", c.Name)
		}
		f.Consts = append(f.Consts, c)
	}
	b.WriteString(")\n\n")
	kinds := append([]BasicKind{KindInt}, sizedKinds...)
	for i, n := 0, 1+f.Rand.Intn(3); i < n; i++ {
		k := kinds[f.Rand.Intn(len(kinds))]
		c := &ConstDef{Name: f.newVarName("k"), Typed: true, Kind: k, Type: BasicType{Kind: k}.String()}
		if d, ok := f.SizedDefined[k]; ok && f.Rand.Intn(2) == 0 {
			c.Type = d
		}
		if k == KindInt {
			c.Val = int64(int32(f.Rand.Uint32()))
		} else {
			c.Val = f.pickSized(k)
		}
		if text := sizedLitText(c.Val, k); f.Rand.Intn(3) == 0 {
			fmt.Fprintf(&b, "const %s = %s(%s)\n", c.Name, c.Type, text)
		} else {
			fmt.Fprintf(&b, "const %s %s = %s\n", c.Name, c.Type, text)
		}
		f.Consts = append(f.Consts, c)
	}
	b.WriteString("\n")
	io.WriteString(f.Out, b.String())
}

// untypedConstText draws an untyped constant, gives c its value and returns how it
// is written.
func (f *Fuzzer) untypedConstText(c *ConstDef) string {
	switch f.Rand.Intn(9) {
	case 0: // small
		c.Val = int64(1 + f.Rand.Intn(127))
	case 1: // past what a byte holds
		c.Val = int64(128 + f.Rand.Intn(65536-128))
	case 2: // past 2^31, which of the kinds only uint32 and the 64-bit ones hold
		c.Val = 1<<31 + f.Rand.Int63n(1<<31)
	case 3: // of 64 bits
		c.Val = 1<<32 + f.Rand.Int63n(1<<40)
	case 4: // negative, which no unsigned kind holds
		c.Val = -(1 + f.Rand.Int63n(1<<15))
	case 5:
		c.Val = f.Rand.Int63n(1 << 32)
		return fmt.Sprintf("0x%x", c.Val)
	case 6:
		r := 'A' + rune(f.Rand.Intn(26))
		if f.Rand.Intn(2) == 0 {
			r += 'a' - 'A'
		}
		c.Val = int64(r)
		return fmt.Sprintf("'%c'", r)
	case 7: // a float spelling of an integral value: 2.5e3 is 25 * 10^2
		mant, exp := int64(11+f.Rand.Intn(89)), 1+f.Rand.Intn(9)
		c.Val, c.Float = mant, true
		for i := 1; i < exp; i++ {
			c.Val *= 10
		}
		return fmt.Sprintf("%d.%de%d", mant/10, mant%10, exp)
	default:
		if text, ok := f.constExprText(c); ok {
			return text
		}
		c.Val, c.Float = int64(1+f.Rand.Intn(127)), false
	}
	return fmt.Sprint(c.Val)
}

// constExprText writes c as an expression of the untyped constants declared before
// it, `(k_1 + k_2)`, `(k_1 - k_2)` or `(k_1 * 3)`, the value computed exactly; ok is
// false when there are none, or the draw would leave constLimit.
func (f *Fuzzer) constExprText(c *ConstDef) (string, bool) {
	var ops []*ConstDef
	for _, d := range f.Consts {
		if !d.Typed {
			ops = append(ops, d)
		}
	}
	if len(ops) == 0 {
		return "", false
	}
	a, b := ops[f.Rand.Intn(len(ops))], ops[f.Rand.Intn(len(ops))]
	switch f.Rand.Intn(3) {
	case 0:
		c.Val, c.Float = a.Val+b.Val, a.Float || b.Float
		return fmt.Sprintf("(%s + %s)", a.Name, b.Name), abs64(c.Val) < constLimit
	case 1:
		c.Val, c.Float = a.Val-b.Val, a.Float || b.Float
		return fmt.Sprintf("(%s - %s)", a.Name, b.Name), abs64(c.Val) < constLimit
	default:
		m := int64(2 + f.Rand.Intn(8))
		if abs64(a.Val) >= constLimit/m {
			return "", false
		}
		c.Val, c.Float = a.Val*m, a.Float
		return fmt.Sprintf("(%s * %d)", a.Name, m), true
	}
}

// visibleConsts are the constants a name reads here: the block's, then the
// package's that none of the block's hides.
func (f *Fuzzer) visibleConsts() []*ConstDef {
	out := append([]*ConstDef(nil), f.blockConsts...)
	for _, c := range f.Consts {
		if !f.hiddenConsts[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// constFor draws a constant that may stand beside the sized variable of kind k,
// whose type is written f.sizedType -- an untyped one whose value k holds, or a typed
// one of that very type -- and whose value in k accept takes, nil taking any.
func (f *Fuzzer) constFor(k BasicKind, accept func(Sized) bool) (*ConstDef, Sized, bool) {
	var cands []*ConstDef
	for _, c := range f.visibleConsts() {
		switch {
		case c.Typed:
			if c.Kind != k || c.Type != f.sizedType {
				continue
			}
		case !fitsKind(c.Val, k):
			continue
		}
		if accept != nil && !accept(NewSized(c.Val, k)) {
			continue
		}
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		return nil, Sized{}, false
	}
	c := cands[f.Rand.Intn(len(cands))]
	return c, NewSized(c.Val, k), true
}

// sizedOperand draws the constant operand of a step or a fold on the sized variable
// of kind k: one time in three a named constant that may stand there (see constFor),
// and a literal drawn by pick otherwise. accept is what the operation needs of it.
func (f *Fuzzer) sizedOperand(k BasicKind, pick func() int64, accept func(Sized) bool) (Node, Sized) {
	if f.Rand.Intn(3) == 0 {
		if c, v, ok := f.constFor(k, accept); ok {
			return &IdentNode{Name: c.Name}, v
		}
	}
	v := NewSized(pick(), k)
	return &IntLitNode{Value: sizedLitText(v.v, k)}, v
}

// genBlockConst declares a constant at the head of a sized block, for its steps to
// draw on beside the package's: a new one, or, one time in two, one named like a
// package constant and read from it, `const k_3 = k_3 + 5` -- whose initializer
// reads the PACKAGE's k_3, the block's being in scope only after its spec. The
// package constant is hidden while the block is generated (see hiddenConsts) and
// read again after it, which is where a block constant that outlived its block
// showed (v0.44.0).
func (f *Fuzzer) genBlockConst(k BasicKind) Node {
	var shadowable []*ConstDef
	for _, c := range f.Consts {
		if !c.Typed && !f.hiddenConsts[c.Name] {
			shadowable = append(shadowable, c)
		}
	}
	if len(shadowable) != 0 && f.Rand.Intn(2) == 0 {
		p := shadowable[f.Rand.Intn(len(shadowable))]
		d := int64(1 + f.Rand.Intn(9))
		f.hiddenConsts[p.Name] = true
		f.blockConsts = append(f.blockConsts, &ConstDef{Name: p.Name, Val: p.Val + d, Float: p.Float})
		return &ConstDeclNode{Name: p.Name, Expr: fmt.Sprintf("%s + %d", p.Name, d)}
	}
	v := f.pickSized(k)
	if !fitsKind(v, k) {
		v &= constLimit - 1 // a uint64 pattern past int64's maximum, which no int64 value is
	}
	c := &ConstDef{Name: f.newVarName("k"), Val: v}
	f.blockConsts = append(f.blockConsts, c)
	return &ConstDeclNode{Name: c.Name, Expr: fmt.Sprint(v)}
}

// genSizedCompare is an if comparing the sized variable, or a difference or a sum
// over it, with a constant on either side, folding a value into the checksum where
// it holds: `b-a < patience` for a uint32 difference past 2^31 was true on the board
// and false in Go (v0.44.0), the constant compared as a signed number. nil when no
// constant may stand beside the variable.
func (f *Fuzzer) genSizedCompare(name string, cur Sized, vm Machine, mem Memory) Node {
	c, cv, ok := f.constFor(cur.k, nil)
	if !ok {
		return nil
	}
	var x Node = &IdentNode{Name: name}
	xv := cur
	if f.Rand.Intn(2) == 0 {
		v, op := f.pickSized(cur.k), []string{"-", "+"}[f.Rand.Intn(2)]
		if r, err := cur.binOp(op, NewSized(v, cur.k)); err == nil {
			x, xv = &BinaryExprNode{Left: x, Op: op, Right: &IntLitNode{Value: sizedLitText(v, cur.k)}}, r.(Sized)
		}
	}
	op := []string{"<", "<=", ">", ">=", "==", "!="}[f.Rand.Intn(6)]
	var l, r Node = x, &IdentNode{Name: c.Name}
	lv, rv := xv, cv
	if f.Rand.Intn(2) == 0 {
		l, r, lv, rv = r, l, rv, lv
	}
	res, err := lv.binOp(op, rv)
	if err != nil {
		return nil
	}
	fold := Int32(1 + f.Rand.Int31n(1<<30))
	if res.(Bool) {
		sum, _ := vm.Eval("^", mem.Load(f.ChecksumName), fold)
		mem.Store(f.ChecksumName, sum)
	}
	return &IfStmtNode{
		Cond: &BinaryExprNode{Left: l, Op: op, Right: r},
		Body: &BlockNode{Statements: []Node{&AssignStmtNode{
			Lhs: f.ChecksumName,
			Op:  "=",
			Rhs: &BinaryExprNode{Left: &IdentNode{Name: f.ChecksumName}, Op: "^", Right: &IntLitNode{Value: fmt.Sprint(fold)}},
		}}},
	}
}

// constLeaf draws a package constant for an int expression: an untyped one of an
// integer spelling whose value an int holds, or a typed int. A float spelling is
// left out, two constants in one expression being computed exactly (see
// ConstDef.Float), and so is a constant a block hides.
func (f *Fuzzer) constLeaf() (Node, Int32, bool) {
	var cands []*ConstDef
	for _, c := range f.Consts {
		if f.hiddenConsts[c.Name] {
			continue
		}
		if c.Typed && c.Kind == KindInt || !c.Typed && !c.Float && fitsKind(c.Val, KindInt) {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return nil, 0, false
	}
	c := cands[f.Rand.Intn(len(cands))]
	return &IdentNode{Name: c.Name}, Int32(int32(c.Val)), true
}

// ConstDeclNode is a constant declared in a block, `const k_3 = k_3 + 5`.
type ConstDeclNode struct {
	Name string
	Expr string
}

func (n *ConstDeclNode) Write(w io.Writer, indent int) {
	writeIndent(w, indent)
	fmt.Fprintf(w, "const %s = %s", n.Name, n.Expr)
}
