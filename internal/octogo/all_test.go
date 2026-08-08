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

// TestFormatGofmtAgreement pins the two spacings a differential run against gofmt
// caught: a three-clause "for" with no init statement keeps the space before its
// first ";", and a keyed element whose value is an elided composite literal keeps
// the space after its ":". Both were written tight, which gofmt does not do.
//
// The run that found them is worth repeating rather than describing: extract every
// emitRunCases source, prepend a package clause, format one copy with gofmt and one
// with FormatFile, and diff. 150 of 193 agree exactly; what is left is operator
// spacing at depth (go/printer's cutoff rule) and gofmt's alignment of consecutive
// one-line function declarations, neither of which this formatter implements.
func TestFormatGofmtAgreement(t *testing.T) {
	const src = `type pair struct {
	a int
	b int
}

type box struct {
	p pair
}

func main() {
	k := box{p: {13, 14}}
	s := "abc"
	i := 0
	for ; i < len(s); i++ {
		println(s[i])
	}
	println(k.p.a)
}
`
	var b bytes.Buffer
	if err := FormatFile("main.ogo", []byte(src), &b); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if got := b.String(); got != src {
		t.Errorf("FormatFile is not idempotent on the gofmt-canonical form:\n got %q\nwant %q", got, src)
	}
}

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

// TestFormatCommentColumn pins which rows set the width of the type column: only
// those whose type is followed by something.
//
// gofmt aligns through a tabwriter, where a cell that ends its line is not part of
// an aligned column. So a long type on a comment-less row does not push its
// neighbours' comments right -- here "int32" is longer than "bool" and "int", and
// the comments sit one past "bool" rather than one past "int32", which is what
// gofmt does with the same input. Every row used to set the width, so every
// comment came out a column too far right.
func TestFormatCommentColumn(t *testing.T) {
	const in = `type packet struct {
id int
sequenceNumber int32
ok bool // whether it checksummed
n int // how many bytes
}
`
	const want = `type packet struct {
	id             int
	sequenceNumber int32
	ok             bool // whether it checksummed
	n              int  // how many bytes
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("comment column:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatGroupedAlign pins the three-column layout of a grouped declaration's
// specs -- names, type, "= value" -- against what gofmt produces for the same
// source. There was no alignment of these at all: every spec came out with single
// spaces, so a const block's "=" signs wandered.
//
// The widths follow the tabwriter rule (see TestFormatCommentColumn): a cell that
// ends its line is not part of an aligned column. That is what leaves the iota run
// alone -- its specs are bare names, one cell each, so there is nothing to align --
// and what stops "maxFrame", which has no type, from widening the type column.
func TestFormatGroupedAlign(t *testing.T) {
	const in = `const (
a = 1
longerName = 2
c uint8 = 3
d = 4 // trailing
)

const (
p = iota
q
rrrr
)

const (
first = 1

afterBlank = 2
x = 3
)

var (
w, v int
single = 1
both int64 = 2
arr [4]int
)
`
	const want = `const (
	a                = 1
	longerName       = 2
	c          uint8 = 3
	d                = 4 // trailing
)

const (
	p = iota
	q
	rrrr
)

const (
	first = 1

	afterBlank = 2
	x          = 3
)

var (
	w, v   int
	single       = 1
	both   int64 = 2
	arr    [4]int
)
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("grouped declaration alignment:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatBlankLineBreaksAlignment pins that a blank line ends an alignment
// block, in a struct as in a grouped declaration.
//
// The test for it looked for TWO newlines in the separator and so never matched: a
// field or spec ends at an inserted semicolon, which carries its own line's newline
// away, leaving only the blank line's. Every struct was aligned as though it had no
// blank lines in it.
func TestFormatBlankLineBreaksAlignment(t *testing.T) {
	const in = `type T struct {
a int

bb string
c bool // trailing
}
`
	const want = `type T struct {
	a int

	bb string
	c  bool // trailing
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("blank line in a struct:\n got %q\nwant %q", g, want)
	}
}

// TestFormatStatementComments pins the alignment of trailing comments on
// statements, which had none: each sat one space after its line.
//
// gofmt aligns maximal runs of CONSECUTIVE lines that carry one. Three things end
// a run, and all three are here: a line with no comment, a change of indentation
// (a nested block is a table of its own), and a line built from a different number
// of cells -- the "/* general */" line carries an extra one, so its trailing
// comment is in a different column and does not align with the line above it.
//
// Every expectation below is gofmt's actual output for the same source.
func TestFormatStatementComments(t *testing.T) {
	const in = `func main() {
p := 1 // one
qq := 2 // two
println(p, qq)
a := 1 // first
bbbbbb := 2 // second

rrr := 3 // three
if p > 0 { // condition
s := 4 // inner
tt := 5 // inner two
println(s, tt)
}
println(p, qq, rrr) // after
/* general */ u := 6 // mixed
println(u)
}
`
	const want = `func main() {
	p := 1  // one
	qq := 2 // two
	println(p, qq)
	a := 1      // first
	bbbbbb := 2 // second

	rrr := 3   // three
	if p > 0 { // condition
		s := 4  // inner
		tt := 5 // inner two
		println(s, tt)
	}
	println(p, qq, rrr) // after
	/* general */ u := 6 // mixed
	println(u)
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("statement comment alignment:\n got %q\nwant %q", g, want)
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

// TestFormatPointerToArraySpacing pins a "[" binding tight to the "*" of a pointer
// type and to the "&" of an address-of, "*[3]int" and "&[2]int{1, 2}", where the
// index rule alone spaced them off as "* [3]int". The rule asks only whether the
// previous token could end an operand, which is right for an index and wrong here,
// and nothing wrote a pointer to an array until it was supported.
//
// The BINARY forms are pinned beside them, since telling the two apart is the whole
// content of the rule: "n * [3]int{1, 2, 3}[0]" is a multiplication by an element of
// a literal, and gofmt spaces it. Both spellings were checked against gofmt.
func TestFormatPointerToArraySpacing(t *testing.T) {
	const in = `type box struct {
p * [3]int
q *[2][3]int
r *int
}

func f(p * [3]int, n int) int {
a := & [2]int{1, 2}
b := n*[3]int{1, 2, 3}[0]
c := n&[3]int{7, 7, 7}[1]
return p[0] + a[1] + b + c
}
`
	const want = `type box struct {
	p *[3]int
	q *[2][3]int
	r *int
}

func f(p *[3]int, n int) int {
	a := &[2]int{1, 2}
	b := n * [3]int{1, 2, 3}[0]
	c := n & [3]int{7, 7, 7}[1]
	return p[0] + a[1] + b + c
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("pointer-to-array spacing:\n got %q\nwant %q", g, want)
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
// The func-literal element also exercises func-literal spacing: "func() int" binds
// tight, while a method declaration's "func (r R)" keeps its space (see
// TestFormatFuncReceiver).
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
	e := func() int {
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

// TestFormatNestedLitComma pins the space after a comma that separates two nested
// composite literals. The rule that binds a literal's braces to what they enclose
// fired on the "{" after the comma too, so "{{1, 2}, {3, 4}}" came out
// "{{1, 2},{3, 4}}" -- the separator between two elements is no different for a
// nested literal than for anything else, and gofmt writes the space.
func TestFormatNestedLitComma(t *testing.T) {
	const in = `type pt struct {
x int
y int
}

var corners = []pt{{1,2},{3,4}}
var grid = [2][3]int{{1,2,3},{4,5,6}}

func main() {
a := []pt{{1,2},{3,4}}
b := [2]pt{{x:1,y:2},{x:3,y:4}}
println(a[1].x, b[0].y, corners[0].x, grid[1][2])
}
`
	const want = `type pt struct {
	x int
	y int
}

var corners = []pt{{1, 2}, {3, 4}}
var grid = [2][3]int{{1, 2, 3}, {4, 5, 6}}

func main() {
	a := []pt{{1, 2}, {3, 4}}
	b := [2]pt{{x: 1, y: 2}, {x: 3, y: 4}}
	println(a[1].x, b[0].y, corners[0].x, grid[1][2])
}
`
	formatCheck(t, in, want)
}

// TestFormatLabel pins a label standing one level out from the statements it
// labels, as gofmt writes it and as "case" already did here. It was indented with
// them. A label is told from a composite literal's key by where it stands: an
// identifier and ":" where a statement may begin, which a key never is.
func TestFormatLabel(t *testing.T) {
	const in = `type P struct {
n int
}

func main() {
outer:
for i := 0; i < 3; i++ {
inner:
for j := 0; j < 3; j++ {
if j == 1 {
continue inner
}
if i == 2 {
break outer
}
}
}
p := P{n: 1}
switch p.n {
case 1:
done:
for {
break done
}
}
}
`
	const want = `type P struct {
	n int
}

func main() {
outer:
	for i := 0; i < 3; i++ {
	inner:
		for j := 0; j < 3; j++ {
			if j == 1 {
				continue inner
			}
			if i == 2 {
				break outer
			}
		}
	}
	p := P{n: 1}
	switch p.n {
	case 1:
	done:
		for {
			break done
		}
	}
}
`
	formatCheck(t, in, want)
}

// TestFormatGroupedDecl pins a grouped declaration indenting its specs, with the
// keyword and the closing ")" staying at the level around it. There was no rule for
// it at all, so every spec of a "const ( ... )" came back at the keyword's level --
// the shape gofmt writes, mangled by the tool meant to produce it. It went unseen
// because no .ogo source in the repo outside testdata has a grouped declaration, and
// the fmt check excludes testdata.
//
// The keyword rather than the "(" is what stays put: the "(" merely follows it on
// the same line, and undenting that would have moved the keyword instead.
func TestFormatGroupedDecl(t *testing.T) {
	const in = `import (
"p2"
)

const (
A = iota
B
)

const single = 1

var (
x int
y string
)

type (
P struct {
a int
}
Q int
)

func f() {
const (
L = 1
M = 2
)
var (
u int
)
println(A, B, single, x, y, L, M, u, p2.GetMs() > 0)
}
`
	const want = `import (
	"p2"
)

const (
	A = iota
	B
)

const single = 1

var (
	x int
	y string
)

type (
	P struct {
		a int
	}
	Q int
)

func f() {
	const (
		L = 1
		M = 2
	)
	var (
		u int
	)
	println(A, B, single, x, y, L, M, u, p2.GetMs() > 0)
}
`
	formatCheck(t, in, want)
}

// TestFormatCommentIndent pins a comment standing with the token it precedes, for
// the two tokens that sit one level out from what surrounds them: a "case" clause
// and a grouped declaration's keyword. A separator's indent did not take the token's
// indent delta, so such a comment was indented with the body instead -- one level too
// deep before a "case", and, once grouped declarations began indenting at all, one
// level too deep before a "const".
func TestFormatCommentIndent(t *testing.T) {
	const in = `// Before a grouped declaration.
const (
// Before a spec.
A = iota
B
)

func main() {
n := 1
switch n {
// Before a case clause.
case 1:
println("one")
// Before the default clause.
default:
println("other")
}
println(A, B)
}
`
	const want = `// Before a grouped declaration.
const (
	// Before a spec.
	A = iota
	B
)

func main() {
	n := 1
	switch n {
	// Before a case clause.
	case 1:
		println("one")
	// Before the default clause.
	default:
		println("other")
	}
	println(A, B)
}
`
	formatCheck(t, in, want)
}

// TestFormatCorpus formats every run-case program and formats the result again.
// The corpus is the widest body of OctoGo source there is, so it is the best answer
// available to "does the formatter choke on, or churn, real programs" -- and it is
// how the two rules above were found. It does not require the sources to be in
// canonical form: gofmt's depth rule for binary operators inside a call, and its
// alignment of consecutive one-line declarations, are not implemented here yet, so
// many of them are not.
func TestFormatCorpus(t *testing.T) {
	for _, c := range emitRunCases {
		t.Run(c.name, func(t *testing.T) {
			var once bytes.Buffer
			if err := FormatFile("t.ogo", []byte(c.src), &once); err != nil {
				t.Fatalf("FormatFile: %v", err)
			}
			var twice bytes.Buffer
			if err := FormatFile("t.ogo", once.Bytes(), &twice); err != nil {
				t.Fatalf("FormatFile round 2: %v\n%s", err, once.String())
			}
			if g, e := twice.String(), once.String(); g != e {
				t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
			}
		})
	}
}

// formatCheck formats in, compares it with want, and formats the result again to
// pin idempotence -- the property every one of these rules has to keep.
func formatCheck(t *testing.T, in, want string) {
	t.Helper()
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("format:\n got %q\nwant %q", g, want)
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
func TestFormatCallAfterIndex(t *testing.T) {
	const in = `func run(h []func(), m [3]func(int)) {
h[0] ()
m[1] (5)
go h[0] ()
x := h[0]
_ = x
}
`
	const want = `func run(h []func(), m [3]func(int)) {
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

// TestFormatFuncReceiver pins that "func" binds tight to its signature for a type
// or literal ("func()", "func(int) bool", "func() int"), while a method declaration
// spaces it off the receiver ("func (r T) m()"). The formatter used to space every
// "func (", including types and literals; the receiver is told apart by a context
// flag set on the Receiver node.
func TestFormatFuncReceiver(t *testing.T) {
	const in = `type H func (int) bool

type T struct {
	cb func ()
}

func(r T) m(x int) bool {
	return x > 0
}

func F(g func (int) bool, h func ()) func () {
	var v func (int) bool
	_ = v
	return h
}
`
	const want = `type H func(int) bool

type T struct {
	cb func()
}

func (r T) m(x int) bool {
	return x > 0
}

func F(g func(int) bool, h func()) func() {
	var v func(int) bool
	_ = v
	return h
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("func/receiver spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatIndexArith pins that binary operators inside an index or slice
// subscript render tight ("xs[n+1]", "xs[:n+1]", "xs[a+b*c]", "m[a+1][b+1]",
// "xs[a&b]"), matching gofmt, while the same expression outside a subscript keeps
// its normal spacing ("a + b*c").
// Slice-colon spacing is covered by TestFormatSliceColon.
func TestFormatIndexArith(t *testing.T) {
	const in = `func run(xs []int, m [3][3]int, a int, b int, c int, n int, i int, j int) {
	_ = xs[n + 1]
	_ = xs[: n+1]
	_ = xs[n+1 :]
	_ = xs[a + b*c]
	_ = m[a+1][b + 1]
	_ = xs[a & b]
	_ = xs[i+1 : j-1]
	x := a + b*c
	_ = x
}
`
	const want = `func run(xs []int, m [3][3]int, a int, b int, c int, n int, i int, j int) {
	_ = xs[n+1]
	_ = xs[:n+1]
	_ = xs[n+1:]
	_ = xs[a+b*c]
	_ = m[a+1][b+1]
	_ = xs[a&b]
	_ = xs[i+1 : j-1]
	x := a + b*c
	_ = x
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("index-arith spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatCallArgArith pins gofmt's depth rule as it applies to a CALL: an
// argument list of more than one argument raises the expression depth, and at depth
// the add- and mul-level operators render tight. One argument does not raise it, so
// "f(a + b)" keeps its spaces where "f(a+b, c)" loses them -- which reads like an
// inconsistency and is the rule.
//
// Only those two levels tighten. A comparison and a logical operator stay spaced at
// any depth, which is what "f(a == b, c)" is here for.
//
// Every want below was taken from gofmt on the same source rather than written from
// what seemed right. The unary cases are the ones that make it subtle: "&" and "-"
// are mul- and add-level operators AND unary ones, so "f(&a, &b)" must keep the
// space after its comma.
func TestFormatCallArgArith(t *testing.T) {
	const in = `func run(a int, b int, c int, p *int, q *int) {
	f(a + b)
	f(a + b, c)
	f(a == b, c)
	f(a && true, c)
	f(a | b, c)
	f(a << b, c)
	f(g(a + b), c)
	f(g(a + b))
	f(a + b*c, c)
	f(&p, &q)
	f(-a, -b)
	x := a + b
	_ = x
}
`
	const want = `func run(a int, b int, c int, p *int, q *int) {
	f(a + b)
	f(a+b, c)
	f(a == b, c)
	f(a && true, c)
	f(a|b, c)
	f(a<<b, c)
	f(g(a+b), c)
	f(g(a + b))
	f(a+b*c, c)
	f(&p, &q)
	f(-a, -b)
	x := a + b
	_ = x
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("call-argument spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatSliceColon pins gofmt's rule for spacing a slice ":": spaced when the
// slice writes more than one bound and at least one of them is a binary expression
// ("xs[i+1 : j-1]", "xs[a+1 : b]", "xs[a : b+1]", "xs[a+1 : b : n]"), tight
// otherwise ("xs[a:b]", "xs[:n+1]", "xs[n+1:]", "xs[a:b:n]"). One written bound is
// never enough, however it is spelled, and a bound that is a call or a unary
// expression is not binary. The bounds themselves still render their operators
// tight, and a ":" straight after the "[" keeps that side tight even when the rest
// are spaced ("xs[: n+1 : b]").
func TestFormatSliceColon(t *testing.T) {
	const in = `func run(xs []int, a int, b int, n int, i int, j int) {
	_ = xs[a : b]
	_ = xs[i+1:j-1]
	_ = xs[a+1:b]
	_ = xs[a:b+1]
	_ = xs[ : n+1]
	_ = xs[n+1 : ]
	_ = xs[a]
	_ = xs[a:b:n]
	_ = xs[a+1:b:n]
	_ = xs[a : b : n+1]
	_ = xs[:n+1:b]
	_ = xs[-a:b:n]
}
`
	const want = `func run(xs []int, a int, b int, n int, i int, j int) {
	_ = xs[a:b]
	_ = xs[i+1 : j-1]
	_ = xs[a+1 : b]
	_ = xs[a : b+1]
	_ = xs[:n+1]
	_ = xs[n+1:]
	_ = xs[a]
	_ = xs[a:b:n]
	_ = xs[a+1 : b : n]
	_ = xs[a : b : n+1]
	_ = xs[: n+1 : b]
	_ = xs[-a:b:n]
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("slice-colon spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatMultilineList pins the indentation of a list written across lines: a
// composite literal, a parameter or result list, and a call's arguments. gofmt
// indents what stands between the delimiters and leaves the delimiters themselves
// at the level of what they belong to, which is what a block and a struct type
// already did here and what these did not -- every continuation line came out at
// column zero, so formatting a table destroyed it.
//
// The nested call is the case that says why the test is "does the first element
// start a line" rather than "does the list span lines": in `g(h(\n1))` both lists
// span lines and gofmt indents once, for the inner one.
func TestFormatMultilineList(t *testing.T) {
	const in = `type P struct {
	x int
	y int
}

var table = []P{
{1, 2},
{3, 4}}

var m = [2][2]int{
{1, 2},
{3, 4}}

func f(
a int,
b int) (
int,
int) {
	return a, b
}

func g(a int) int { return a }

func main() {
	x, y := f(
1,
2)
	println(x, y, g(
3))
	if x > 0 {
		q := []P{
{5, 6}}
		println(q[0].x)
	}
	println(len(table), len(m))
}
`
	const want = `type P struct {
	x int
	y int
}

var table = []P{
	{1, 2},
	{3, 4}}

var m = [2][2]int{
	{1, 2},
	{3, 4}}

func f(
	a int,
	b int) (
	int,
	int) {
	return a, b
}

func g(a int) int { return a }

func main() {
	x, y := f(
		1,
		2)
	println(x, y, g(
		3))
	if x > 0 {
		q := []P{
			{5, 6}}
		println(q[0].x)
	}
	println(len(table), len(m))
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("multi-line list indentation:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatTrailingComma pins gofmt's rule for a trailing comma: kept where it is
// what lets the list span lines, dropped where the list closes on the same line.
func TestFormatTrailingComma(t *testing.T) {
	const in = `func f(a int, b int,) int { return a + b }

func g(
	a int,
	b int,
) int {
	return a + b
}

var s = []int{1, 2,}

var t = []int{
	1,
	2,
}

func main() { println(f(1, 2,), g(1, 2), len(s), len(t)) }
`
	const want = `func f(a int, b int) int { return a + b }

func g(
	a int,
	b int,
) int {
	return a + b
}

var s = []int{1, 2}

var t = []int{
	1,
	2,
}

func main() { println(f(1, 2), g(1, 2), len(s), len(t)) }
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("trailing comma:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}

// TestFormatDotImport pins a dot-import spacing its "." on both sides
// ("import . \"p2\""), while a selector's "." stays tight ("a.b"). The formatter's
// blanket "." rule used to tighten the dot-import to `import."p2"`.
func TestFormatDotImport(t *testing.T) {
	const in = `import."p2"
import _ "p2"

func run(a T) {
	a . b = 1
	_ = a.b
}
`
	const want = `import . "p2"
import _ "p2"

func run(a T) {
	a.b = 1
	_ = a.b
}
`
	var out bytes.Buffer
	if err := FormatFile("t.ogo", []byte(in), &out); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if g := out.String(); g != want {
		t.Errorf("dot-import spacing:\n got %q\nwant %q", g, want)
	}

	var again bytes.Buffer
	if err := FormatFile("t.ogo", out.Bytes(), &again); err != nil {
		t.Fatalf("FormatFile round 2: %v", err)
	}
	if g, e := again.String(), out.String(); g != e {
		t.Errorf("formatting is not idempotent:\n first %q\nsecond %q", e, g)
	}
}
