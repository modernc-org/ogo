package octogo // import "octogo.dev/octogo/lib"

import (
	"os"
	"testing"

	_ "modernc.org/ccgo/v4/lib" // generator.go
	_ "modernc.org/gc/v3"       // generator.go
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestCheck(t *testing.T) {
	const src = `package main

import "p2"
import "runtime"

// TopLevelDecl: Constants and Variables
const MAX_COGS = 8
const DEFAULT_FLAG = true

var globalStatus bool = false
var sharedBus chan byte
var multiChan chan chan int
var pinBuffer [32]int

// FuncDecl with ParameterList and Return Type
func worker(id int, dataChan chan byte, signal chan chan int) bool {
    // Nested Types and Declarations
    var localBuf [16]byte
    var active bool = true
    var count int = 0
    var nestedToken chan int
    var val byte
    
    // For Loop (Expression)
    for active {
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

func compute(a int, b int) int {
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

	t.Log("TODO")
}
