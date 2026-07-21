// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"testing"
	"testing/fstest"

	_ "modernc.org/ccgo/v4/lib" // generator.go
	_ "modernc.org/gc/v3"       // generator.go
)

var (
	reString = flag.String("re", "", "regexp filter")
	re       *regexp.Regexp
)

const (
	src0 = `import . "a"
import abc "def"
import ("x"; "y";)
import ("x2"; "y2")
import (
	"p2"
	"runtime"
)

// TopLevelDecl: Constants and Variables
const MAX_COGS = 8
const DEFAULT_FLAG = true

var globalStatus bool = false
var (
	sharedBus, outputStream chan byte
	multiChan chan chan int
	pinBuffer [32]int
)

// FuncDecl with ParameterList and Return Type
func worker(id, n int, dataChan chan byte, signal chan chan int) bool {
	// Nested Types and Declarations
	var localBuf [16]byte
	var active bool = true
	var count int = 0
	var nestedToken chan int
	var val, val2 byte
	i := 42
     
	// For Loop (Expression)
	for active {
		i = 24
		// If / Else Statement
		if count == MAX_COGS {
			active = false
		} else {
			count = count + 1
		}
		
		// SwitchStmt with ExpressionList
		switch count {
		case 1, 2, 3:
			localBuf[0] = 255
		case 4:
			localBuf[1] = 127
		default:
			localBuf[2] = 0
		}
		
		// SelectStmt with diverse CommClauses
		select {
		case <- dataChan:             // Bare receive (CommOp -> "<-" Expression)
		case val = <- dataChan:       // Receive assignment (PostfixComm)
		case dataChan <- 255:         // Send literal (PostfixComm)
		case signal <- nestedToken:   // Send channel down a channel
		default:
			count = count | 1         // Bitwise fallback
		}
	}
	
	// Return Statement
	return active
}

func compute(a int, b int) (c, d int) {
	// Deep Expression tree climbing: AddOp, MulOp, RelOp, and grouped Factors
	// Precedence test: bitwise, arithmetic, and logical boundaries
	return (a * b) + (a / b) - (a << 2) ^ (b >> 1) & 255
}

func emptyReturnTest() {
	// Statements: return without expression
	return
}

func main() {
	var a int = 10
	var b int = 20
	var c int
	var d = 42
	
	// Identifier Postfix (Assignment & CallSuffix)
	c = compute(a, b)
	
	// Goroutine invocation (CallSuffix)
	go worker(c, sharedBus, multiChan)
	
	// Statement: Channel Send
	sharedBus <- 255
	
	// Factor -> "<-" Expression. 
	// NOTE: Because "<-" Expression is a Factor, '<- sharedBus + 10' 
	// would parse as '<- (sharedBus + 10)'. The parens are required here 
	// to avoid a type-check error (adding 10 to a channel).
	c = (<- sharedBus) + 10
	
	// Complex L-value resolution (Index and Selector in Postfix)
	// Assumes p2 package has a function ReadPin that returns int
	pinBuffer[c] = p2.ReadPin(a)
	
	// Boolean literal Factor
	var isDone bool = true
	
	return 
}
`
)

func TestMain(m *testing.M) {
	flag.Parse()
	if s := *reString; s != "" {
		re = regexp.MustCompile(s)
	}
	os.Exit(m.Run())
}

func TestSemicolonInjection(t *testing.T) {
	imp := Token{Ch: rune(TOK_import)}
	str := Token{Ch: rune(string_lit)}
	semi := Token{Ch: rune(TOK_003b)}
	eof := Token{}
	for itest, test := range []struct {
		src  string
		toks []Token
	}{
		{"", []Token{eof}},
		{"import", []Token{imp, eof}},
		{"import `main`", []Token{imp, str, semi, eof}},
		{"import `main`\n", []Token{imp, str, semi, eof}},
		{"import `main`;", []Token{imp, str, semi, eof}},
		{"import `main`;\n", []Token{imp, str, semi, eof}},
	} {
		var p Parser
		sc := NewRecScanner(fmt.Sprintf("%v.ogo", itest), []byte(test.src), p.scan, int(white_space))
		var toks []Token
		for {
			tok := sc.Scan()
			toks = append(toks, tok)
			if tok.Ch == 0 {
				break
			}
		}
		if g, e := len(toks), len(test.toks); g != e {
			t.Errorf("%v: toks, got %v, expected %v", itest, g, e)
			continue
		}

		for i, g := range toks {
			e := test.toks[i]
			if g, e := g.Ch, e.Ch; g != e {
				t.Errorf("%v: toks[%v].Ch, got %#U, expected %#U", itest, i, g, e)
			}
		}
	}
}

func mapFS(files map[string][]byte) fs.FS {
	mfs := make(fstest.MapFS)
	for name, data := range files {
		mfs[name] = &fstest.MapFile{Data: data}
	}
	return mfs
}

func TestNewPackage(t *testing.T) {
	fsys := mapFS(
		map[string][]byte{
			"src0": []byte(src0),
		},
	)
	bc := NewBuildContext(fsys, -1)
	bc.noDeclarationChecks = true
	pkg := bc.NewPackage("", []string{"src0"}, fsys)
	for _, v := range pkg.Files {
		if err := v.errList.Err(); err != nil {
			t.Error(err)
		}
	}
}

//TODO- func TestTmp(t *testing.T) {
//TODO- 	const src = "func f(a T,) {}"
//TODO- 	pkg := newPackage(-1, []string{"params.ogo"}, map[string][]byte{"params.ogo": []byte(src)})
//TODO- 	for _, v := range pkg.Files {
//TODO- 		if err := v.Err; err != nil {
//TODO- 			t.Error(err)
//TODO- 		}
//TODO- 	}
//TODO- }

// testInput contains deliberately mangled OctoGo code.
// It features:
// - Inconsistent structural indentation.
// - Missing and excessive spaces around binary operators.
// - Spaces incorrectly inserted after unary operators.
// - Trailing spaces on comments and excessive blank lines.
// - Misaligned case/default clauses.
const testInput = `import "p2"

var   globalCount int= 1+2

//   This is a worker function   
func blinkWorker( rateChan chan int){
delay:=<- rateChan
  for {
p2.PinHigh( 5 )
    _waitms(delay )
 i=1+2*3+4
  i=1*3+3*4
 i = 1 + 2 * 3 + 4
  i = 1 * 3 + 3 * 4
p2.PinLow(5)


	// Wait for a rate change or loop   
select {
    case delay =<- rateChan:
a=b	    
    default :
  // Do nothing
   c,d=e( f )
}
    }
}

func main(  ) {
	var rateChan chan int
	go blinkWorker( rateChan )
  rateChan<-100
}`

// testExpected contains the canonical, correctly formatted output.
const testExpected = `import "p2"

var globalCount int = 1 + 2

//   This is a worker function
func blinkWorker(rateChan chan int) {
	delay := <-rateChan
	for {
		p2.PinHigh(5)
		_waitms(delay)
		i = 1 + 2*3 + 4
		i = 1*3 + 3*4
		i = 1 + 2*3 + 4
		i = 1*3 + 3*4
		p2.PinLow(5)

		// Wait for a rate change or loop
		select {
		case delay = <-rateChan:
			a = b
		default:
			// Do nothing
			c, d = e(f)
		}
	}
}

func main() {
	var rateChan chan int
	go blinkWorker(rateChan)
	rateChan <- 100
}
`

func TestFormat(t *testing.T) {
	var out bytes.Buffer
	if err := FormatFile("test.go", []byte(testInput), &out); err != nil {
		t.Fatalf("err=%v", err)
	}

	if g, e := out.String(), testExpected; g != e {
		t.Errorf("Formatting output did not match expected.\n\n=== GOT ===\n%s\n\n=== EXPECTED ===\n%s\n", g, e)
	}
}

// TestFormatTrailingComment pins a trailing comment being separated from the code
// by exactly one space, as gofmt does, and an aligned run -- struct fields -- still
// padding to its target column.
//
// Two independent paths in formatSep each emitted a space: the inline whiteSpace
// item, and the comment item's "ensure at least one space". Both fired, so
// "x := 1 // c" came out as "x := 1  // c". The whiteSpace one was asking a
// meaningless question anyway, since currTok there is the token *after* the
// comment.
//
// Not pinned here, and still differing from gofmt: a run of consecutive trailing
// comments on statements is not aligned to a common column the way struct fields
// are. That needs the field-measurement machinery extended to statement runs.
func TestFormatTrailingComment(t *testing.T) {
	const in = `type T struct {
a int    // field comment
bbbb string  // second
}

func f() {
// own-line comment
x := 1    /* general inline */
z := 3       // trailing
println(x + z)
}
`
	const want = `type T struct {
	a    int    // field comment
	bbbb string // second
}

func f() {
	// own-line comment
	x := 1 /* general inline */
	z := 3 // trailing
	println(x + z)
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("trailing comment spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatIndexSpacing pins the two spacings a '[' can take. A '[' opening an
// array or slice *type* is spaced off the name it follows ("var a [3]int"), while
// one opening an *index* binds tight to its base ("a[1]"). needsSpace had no case
// for a leading '[' at all, so every one fell through to its closing "return true"
// and an index came out as "a [1]"; the ':' of a slice expression had the matching
// problem on its right, giving "s[0: 1]".
//
// The bug survived because the older golden above contains no '[' whatsoever, and
// the Makefile runs `ogo fmt` with --exclude='\/testdata\/' -- which is exactly
// where the index-heavy .ogo sources live.
func TestFormatIndexSpacing(t *testing.T) {
	// Input is deliberately mis-spaced in both directions: the bug's own output
	// ("a [1]", "s[0: 1]") and the opposite ("var r[2]int").
	const in = `type buf struct {
arr [3]int
grid [2][3]int
data []int
}

func f(a [3]int, s []int) [2]int {
var b buf
b.arr [1] = 3
b.data = s[0: 1]
t := s[: 2]
u := s[2 :]
switch a [0] {
case 1:
println(t [0])
default:
println(u[0] + b.grid [1] [2])
}
var r[2]int
return r
}
`
	const want = `type buf struct {
	arr  [3]int
	grid [2][3]int
	data []int
}

func f(a [3]int, s []int) [2]int {
	var b buf
	b.arr[1] = 3
	b.data = s[0:1]
	t := s[:2]
	u := s[2:]
	switch a[0] {
	case 1:
		println(t[0])
	default:
		println(u[0] + b.grid[1][2])
	}
	var r [2]int
	return r
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("index spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatAssignOps pins the compound assignment operators being spaced like the
// plain "=" -- they reach the same isAssignOp spacing rule -- and the result being
// a fixed point, which catches an operator that round-trips to different text.
func TestFormatAssignOps(t *testing.T) {
	const in = `func main() {
x:=1
x+=2
x-=1
x*=3
x/=2
x%=3
x&=6
x|=1
x^=2
x&^=4
x<<=2
x>>=1
println(x)
}
`
	const want = `func main() {
	x := 1
	x += 2
	x -= 1
	x *= 3
	x /= 2
	x %= 3
	x &= 6
	x |= 1
	x ^= 2
	x &^= 4
	x <<= 2
	x >>= 1
	println(x)
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("compound assignment formatting:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatCompositeLit pins a composite literal's braces binding to what they
// enclose, which is the opposite of a block's braces being spaced off the header
// they follow. The formatter is token-based and had no rule for the distinction, so
// every literal came out as "P { Q { 1 }, 2 }". The func-literal element is the
// case the rule must not overreach into: those braces are a block's, however deep
// inside a literal they sit.
//
// The "func () int" in want is a separate, pre-existing bug -- a function literal
// should be "func()", while the "func (r R)" of a method declaration does want its
// space, so telling them apart needs context this rule does not have. It is spelled
// out here so the expectation reads as a record of it, not as approval.
func TestFormatCompositeLit(t *testing.T) {
	// Input is deliberately mis-spaced in both directions: the bug's own output
	// and the opposite ("P{n:1,q:Q{v:2}}").
	const in = `type Q struct {
v int
}

type P struct {
q Q
n int
}

func main() {
a := P   {   Q  {  1  }  ,   2   }
b := P{n:1,q:Q{v:2}}
c := P{}
d := P { n : 3 }
if a == (P{Q{1}, 2}) {
println(a.n, b.n, c.n, d.n)
}
e := func() int {
p := P{Q{4}, 5}
return p.n
}
println(e())
}
`
	const want = `type Q struct {
	v int
}

type P struct {
	q Q
	n int
}

func main() {
	a := P{Q{1}, 2}
	b := P{n: 1, q: Q{v: 2}}
	c := P{}
	d := P{n: 3}
	if a == (P{Q{1}, 2}) {
		println(a.n, b.n, c.n, d.n)
	}
	e := func () int {
		p := P{Q{4}, 5}
		return p.n
	}
	println(e())
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("composite literal spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatIncDec pins "++" and "--" binding to their operand. The formatter had
// no rule for them, so they fell through to the default space and every increment
// came out as "i ++". They are statements in this language, never expressions, so
// there is no prefix form for the rule to get wrong.
func TestFormatIncDec(t *testing.T) {
	const in = `type B struct {
arr [3]int
data []int
n int
}

func main() {
i := 0
i ++
i  --
var b B
b.n ++
b.arr[i] ++
b.data = make([]int, 2, 2)
b.data[i] --
for j := 0; j < 2; j ++ {
i ++
}
println(i, b.n)
}
`
	const want = `type B struct {
	arr  [3]int
	data []int
	n    int
}

func main() {
	i := 0
	i++
	i--
	var b B
	b.n++
	b.arr[i]++
	b.data = make([]int, 2, 2)
	b.data[i]--
	for j := 0; j < 2; j++ {
		i++
	}
	println(i, b.n)
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("increment spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatStructBraces pins gofmt's line-based rule for a struct or interface
// type's opening brace: it binds tight to the keyword for an empty or one-line
// body ("struct{}", "struct{ v int }", "interface{ M() }") and is spaced off it
// only when the body spans lines. The rule is purely line-based, so an empty body
// written across lines keeps the space ("struct {\n}").
func TestFormatStructBraces(t *testing.T) {
	const in = `type Empty struct {}

type OneLine struct { v int }

type Multi struct {
a int
}

type EmptyMulti struct {
}

type IfaceEmpty interface {}

type IfaceOne interface { M() }

type IfaceMulti interface {
M()
}
`
	const want = `type Empty struct{}

type OneLine struct{ v int }

type Multi struct {
	a int
}

type EmptyMulti struct {
}

type IfaceEmpty interface{}

type IfaceOne interface{ M() }

type IfaceMulti interface {
	M()
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("struct/interface brace spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatPointerType pins that "*" in a type or parameter position binds tight
// to the element type ("[]*int", "[3]*int", "func() *int", "var a *int"), while
// multiplication in an expression stays spaced ("3 * 2"). The formatter used to
// read a "*" after "]" or ")" as binary multiplication, emitting "[]* int" and
// "func() * int".
func TestFormatPointerType(t *testing.T) {
	const in = `type T struct {
p *int
s []* int
a [3]* int
pp **int
}

func f(x *int) * int {
return x
}

func run() {
var v *int
_ = v
a := 3 * 2
_ = a
}
`
	const want = `type T struct {
	p  *int
	s  []*int
	a  [3]*int
	pp **int
}

func f(x *int) *int {
	return x
}

func run() {
	var v *int
	_ = v
	a := 3 * 2
	_ = a
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("pointer-type spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatLeadingUnary pins that a unary operator binds tight to its operand at
// the start of a statement ("*p = x", "<-ch") and after an assignment operator
// ("x = *p"), while binary multiplication keeps its spaces and its grouping
// ("2 * 3", "a*q + 1"). A statement-leading "*" used to be read as multiplication
// against the previous statement's last token (a skipped synthetic semicolon left
// the token history stale), and "= *p" lost its space when the RHS had an AddOp.
func TestFormatLeadingUnary(t *testing.T) {
	const in = `func run(ch chan int, q *int) {
<-ch
* q = 3
x := <- ch
_ = x
* q =*q + 1
a := 2 * 3
b := a*q + 1
_ = b
}
`
	const want = `func run(ch chan int, q *int) {
	<-ch
	*q = 3
	x := <-ch
	_ = x
	*q = *q + 1
	a := 2 * 3
	b := a*q + 1
	_ = b
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("leading-unary spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatCallAfterIndex pins that a call binds tight after an index or slice
// suffix ("h[0]()", "m[1](5)", "go h[0]()"), which used to be spaced ("h[0] ()").
// The "func ()" in the signature is deliberately recorded as-is: a func type's
// "func()" losing its tightness is the separate func/method-receiver backlog item,
// not this fix.
func TestFormatCallAfterIndex(t *testing.T) {
	const in = `func run(h []func(), m [3]func(int)) {
h[0] ()
m[1] (5)
go h[0] ()
x := h[0]
_ = x
}
`
	const want = `func run(h []func (), m [3]func (int)) {
	h[0]()
	m[1](5)
	go h[0]()
	x := h[0]
	_ = x
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("call-after-index spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}
