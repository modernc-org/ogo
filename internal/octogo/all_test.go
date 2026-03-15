// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

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
	pkg := NewBuildContext(fsys, -1).NewPackage([]string{"src0"}, fsys)
	for _, v := range pkg.Files {
		if err := v.Err; err != nil {
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
}`

func TestFormat(t *testing.T) {
	var out bytes.Buffer
	if err := FormatFile("test.go", []byte(testInput), &out); err != nil {
		t.Fatalf("err=%v", err)
	}

	if g, e := out.String(), testExpected; g != e {
		t.Errorf("Formatting output did not match expected.\n\n=== GOT ===\n%s\n\n=== EXPECTED ===\n%s\n", g, e)
	}
}
