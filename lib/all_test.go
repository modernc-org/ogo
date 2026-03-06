// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/octogo/lib"

import (
	"fmt"
	"os"
	"testing"

	_ "modernc.org/ccgo/v4/lib" // generator.go
	_ "modernc.org/gc/v3"       // generator.go
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

/// var globalStatus bool = false
/// var sharedBus, outputStream chan byte
/// var multiChan chan chan int
/// var pinBuffer [32]int
/// 
/// // FuncDecl with ParameterList and Return Type
/// func worker(id int, dataChan chan byte, signal chan chan int) bool {
///     // Nested Types and Declarations
///     var localBuf [16]byte
///     var active bool = true
///     var count int = 0
///     var nestedToken chan int
///     var val, val2 byte
///     
///     // For Loop (Expression)
///     for active {
///         // If / Else Statement
///         if count == MAX_COGS {
///             active = false
///         } else {
///             count = count + 1
///         }
///         
///         // SwitchStmt with ExpressionList
///         switch count {
///         case 1, 2, 3:
///             localBuf[0] = 255
///         case 4:
///             localBuf[1] = 127
///         default:
///             localBuf[2] = 0
///         }
/// 
///         // SelectStmt with diverse CommClauses
///         select {
///         case <- dataChan:             // Bare receive (CommOp -> "<-" Expression)
///         case val = <- dataChan:       // Receive assignment (PostfixComm)
///         case dataChan <- 255:         // Send literal (PostfixComm)
///         case signal <- nestedToken:   // Send channel down a channel
///         default:
///             count = count | 1         // Bitwise fallback
///         }
///     }
///     
///     // Return Statement
///     return active
/// }
/// 
/// func compute(a int, b int) int {
///     // Deep Expression tree climbing: AddOp, MulOp, RelOp, and grouped Factors
///     // Precedence test: bitwise, arithmetic, and logical boundaries
///     return (a * b) + (a / b) - (a << 2) ^ (b >> 1) & 255
/// }
/// 
/// func emptyReturnTest() {
///     // Statements: return without expression
///     return
/// }
/// 
/// func main() {
///     var a int = 10
///     var b int = 20
///     var c int
///     
///     // Identifier Postfix (Assignment & CallSuffix)
///     c = compute(a, b)
///     
///     // Goroutine invocation (CallSuffix)
///     go worker(c, sharedBus, multiChan)
///     
///     // Statement: Channel Send
///     sharedBus <- 255
///     
///     // Factor -> "<-" Expression. 
///     // NOTE: Because "<-" Expression is a Factor, '<- sharedBus + 10' 
///     // would parse as '<- (sharedBus + 10)'. The parens are required here 
///     // to avoid a type-check error (adding 10 to a channel).
///     c = (<- sharedBus) + 10
///     
///     // Complex L-value resolution (Index and Selector in Postfix)
///     // Assumes p2 package has a function ReadPin that returns int
///     pinBuffer[c] = p2.ReadPin(a)
///     
///     // Boolean literal Factor
///     var isDone bool = true
///     
///     return 
/// }
`
)

func TestMain(m *testing.M) {
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

func TestNewPackage(t *testing.T) {
	pkg := newPackage(-1, []string{"src0"}, map[string][]byte{"src0": []byte(src0)})
	for _, v := range pkg.Files {
		if err := v.Err; err != nil {
			t.Error(err)
		}
	}
}
