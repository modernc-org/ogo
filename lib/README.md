![heart](sponsors/heart.png "heart")
[Github Sponsors Account](https://github.com/sponsors/j-modernc-org) /  j-modernc-org

| Platform | Role | Contributing Guidelines |
| :--- | :--- | :--- |
| **GitLab** | **Primary Source** | This is the canonical repository (`cznic/octogo/lib`). CI pipelines and main development happen here. |
| **GitHub** | **Mirror** | This is a mirror (`modernc-org/octogo/lib`). We **do accept** Issues and Pull Requests here for your convenience! <br> *Note: PRs submitted here will be manually merged into the GitLab source, so please allow extra time for processing.* |

![logo_png](logo.png)

# **OctoGo**

**A modern, zero-allocation, Go-like programming language built specifically for the Parallax Propeller 2 (P2) multi-core microcontroller.**

OctoGo brings the elegance of Go's concurrency model to embedded hardware. It compiles a  LL(1) subset of a Go-like language into C99/C11, which is then compiled to native P2 assembly using the industry-standard flexprop toolchain.

There is no software scheduler and no garbage collector. Goroutines map 1:1 to physical silicon.

### **The Hook: Concurrent Blinky**

Forget manual state machines and timer interrupts. In OctoGo, spawning a parallel hardware process is as simple as calling go myfunc(). Channel communication uses the familiar \<- operator, allowing you to synchronize hardware locks seamlessly.

```

import "p2"

// blinkWorker runs independently on its own dedicated Cog  
func blinkWorker(pin int, rateChan chan int) {  
    delay := 500  
    for {  
        // Non-blocking hardware poll via select  
        select {  
        case delay \= \<-rateChan:  
        default:  
        }  
          
        p2.PinHigh(pin)  
        p2.WaitMs(delay)  
        p2.PinLow(pin)  
        p2.WaitMs(delay)  
    }  
}

func main() {  
    rateChan := make(chan int)  
      
    // Spawns directly to a new hardware Cog\!  
    go blinkWorker(56, rateChan) 

    // Update the blink rate from the main Cog via hardware-locked channel  
    rateChan \<- 100   
}
```

## **Architecture & Design**

OctoGo is designed to be a zero-cost abstraction over the Propeller 2's unique 8-cog architecture.

* **Native Hardware Concurrency:** The go keyword transpiles to a scoped block that requests a stack from the octogo\_rt runtime and invokes \_cogstart\_C. We strictly enforce the P2's 8-cog limit—exceeding it results in a compile-time error or runtime panic.

* **Hardware-Backed Channels:** Channels (chan) are not software queues. They are thread-safe conduits that map directly to the P2's native hardware locks (0-15), ensuring atomic, lock-step data transfer between Hub RAM and Cog RAM.  
* **Zero-Allocation & No GC:** OctoGo operates without a Garbage Collector. Memory scoping is strict (Hub RAM vs. Cog RAM), and slices are implemented as non-escaping views over fixed arrays.  
* **Select Statements:** The select statement  is transpiled into an efficient polling loop, utilizing flexprop's \_waitx yield instructions to prevent bus starvation during non-blocking hardware polling.

* **Implicit Namespaces:** To keep the grammar clean and strictly LL(1), there is no package keyword. A package's namespace is implicitly inferred from its directory name, mapping cleanly to a single C translation unit.

## **The Compiler Pipeline**

OctoGo is a source-to-source compiler (transpiler) written in Go.

1. **Frontend:** Lexical analysis and parsing are generated via modernc.org/egg using a LL(1) grammar. The AST is represented as a zero-pointer, cache-local flat \[\]int32 slice.

2. **Semantic Pass:** Uses Go 1.23+ iterators (`func(yield func(node) bool)`) to traverse the AST, calculate memory offsets, and resolve scope.  
3. **Transpilation:** Emits standard C code alongside our custom octogo\_rt.h runtime header.  
4. **Backend Generation:** The octogo build command automatically feeds the emitted C into flexprop, delegating register allocation, instruction scheduling, and P2 binary generation to a proven, hardware-aware backend.

## **Getting Started**

*(TODO TBD  Link to pre-compiled binaries for Windows, macOS, and Linux will go here)*

1. Download the latest OctoGo binary and install flexprop.  
2. Write your .ogo code.  
3. Run octogo build blinky/ to compile and generate your P2 binary.

*(Note: Testing is built-in. Files ending in \_test.ogo are automatically recognized as test files).*

## **Sponsorship & Support**

OctoGo is an open-source labor of love dedicated to the Parallax community. Its continued development, standard library expansion, and maintenance rely on community and corporate support.

If OctoGo makes your Propeller 2 development faster, easier, or more enjoyable, please consider supporting the project:

* **☕ Hobbyist ($5/mo):** Support ongoing open-source development.  
* **🛠️ Maker ($15/mo):** Funds the purchase of test hardware, sensors, and dev boards to expand the p2 standard library.  
* **🏢 Corporate Sponsor ($100+/mo):** For businesses relying on OctoGo. Includes priority bug fixes, roadmap input, and prominent logo placement.
