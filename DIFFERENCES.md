# Observable differences from Go

OctoGo means what Go means wherever the Propeller 2 lets it. Where the hardware does
not, it refuses a program rather than give it another meaning, as a rule. This page is
about the exceptions: programs that build both as OctoGo and as Go and run
differently. A program that only builds as one of them is not here. The list of what
is refused is at the end.

Every example on this page is a test. Each program is run on the host and on a P2
board, and must print what is written under "OctoGo prints". Under Go each must print
what is written under "Go prints", unless the entry says Go's output varies.
`printf` is OctoGo's builtin; in Go it stands for `fmt.Printf`.

### `float64` is a 32-bit float

The P2's C toolchain has no 64-bit floating point, so `float64` is `float32` under its
Go name. A program gets about 7 significant digits from either, not 15. Printing is
exact (`%v` gives the shortest decimal that reads back the same float), so it is the
value that is short of digits, not its rendering. A `float64` of 64 bits, computed in
software, is planned.

<!-- board-only: the host's C double has 64 bits -->

```go
func main() {
	var x float64 = 16777217
	println(int(x - 16777216))
}
```

OctoGo prints:

```
0
```

Go prints:

```
1
```

### `int` and `uint` have 32 bits

As on Go's 32-bit targets, `int`, `uint` and `uintptr` are 32 bits wide, and
arithmetic on them wraps there.

```go
func main() {
	x := 2147483647
	x++
	println(x)
}
```

OctoGo prints:

```
-2147483648
```

Go prints:

```
2147483648
```

### A channel is made by its declaration, one per declaration site

There is no heap to `make` a channel in, so `var ch chan T` is a live channel, its
cell allocated statically. A local declaration names one cell for every run of the
function: two calls, or two cogs running it at once, share it. In Go, `var ch chan T`
is a nil channel and every `make` a new one. Making a channel as Go makes one is
planned: `make(chan T)`, a cell of the frame in a function, refused where it would
outlive the call.

```go
func newChan() chan int {
	var ch chan int
	return ch
}

func main() {
	a, b := newChan(), newChan()
	close(a)
	_, ok := <-b
	println(a == b, ok)
}
```

OctoGo prints:

```
true false
```

Go prints:

```
panic: close of nil channel
```

### `append` does not grow a slice past its capacity

A slice's backing is fixed when it is made. An `append` that does not fit panics, where
Go would move the slice to a larger backing. The two-result form, `s, ok = append(s,
x)`, reports a full slice instead of panicking.

```go
func main() {
	s := make([]int, 0, 2)
	s = append(s, 1, 2, 3)
	println(len(s))
}
```

OctoGo prints:

```
panic: append: out of capacity
```

Go prints:

```
3
```

### At most seven goroutines run at once

A goroutine is a cog, and the P2 has eight, `main` running on one of them. A `go`
statement finding none free panics.

```go
var block chan int

func wait() { <-block }

func main() {
	for i := 0; i < 8; i++ {
		go wait()
	}
	println("started 8")
}
```

OctoGo prints:

```
panic: out of cogs
```

Go prints:

```
started 8
```

### A panic runs no deferred call

A panic prints its message and stops every cog without running the deferred calls on
its way out, and there is no `recover`; both are planned. Its message is shorter than
Go's, with no goroutine trace and none of Go's detail, such as the index and the
length of an index out of range.

```go
func main() {
	defer println("deferred")
	panic("boom")
}
```

OctoGo prints:

```
panic: boom
```

Go prints:

```
deferred
panic: boom
```

### A print writes what it evaluated before a later argument panics

`print`, `println` and `printf` write each argument as it is evaluated when no argument
can do anything but panic. Go evaluates every argument first, so its print writes
nothing when one panics.

```go
func main() {
	xs := []int{1, 2, 3}
	i := 5
	println("value:", xs[i])
}
```

OctoGo prints:

```
value: panic: index out of range
```

Go prints:

```
panic: runtime error: index out of range [5] with length 3
```

### `printf` writes the line as it formats it

Go's `fmt.Printf` formats the whole line before it writes any of it. Here a `String`
method that prints writes into the middle of the line being formatted.

```go
type T int

func (t T) String() string {
	println("in String")
	return "t"
}

func main() {
	printf("x=%v\n", T(1))
}
```

OctoGo prints:

```
x=in String
t
```

Go prints:

```
in String
x=t
```

### `println` of a slice prints its elements

Go's builtin `println` prints a slice's length, capacity and address. OctoGo's prints
the elements, as `fmt.Println` would.

<!-- go-varies: the address differs from run to run -->

```go
func main() {
	xs := []int{1, 2, 3}
	println(xs)
}
```

OctoGo prints:

```
[1 2 3]
```

Go prints:

```
[3/3]0xc000012120
```

### Memory shared between cogs is defined

One cog may write a variable while another reads it, with no lock. A read in a loop is
made on every pass, and a cog's writes reach memory in program order, so a flag
published after its payload is seen after it. In Go the same program is a data race,
and the loop may never end. specs.go, "Memory shared between cogs", has the rules.

<!-- go-varies: a data race -->

```go
var ready bool
var payload int

func producer() {
	payload = 42
	ready = true
}

func main() {
	go producer()
	for !ready {
	}
	println(payload)
}
```

OctoGo prints:

```
42
```

Go prints:

```
42
```

### A `select` takes its ready clauses in turn

When several clauses are ready, a `select` takes the one after the clause it chose
last. Go chooses among them at random. Neither gives a priority by the clauses'
order.

<!-- go-varies: Go chooses at random -->

```go
var a chan int
var b chan int

func main() {
	close(a)
	close(b)
	for i := 0; i < 6; i++ {
		select {
		case <-a:
			print("a")
		case <-b:
			print("b")
		}
	}
	println()
}
```

OctoGo prints:

```
ababab
```

Go prints:

```
abbaab
```

## Refused here, though Go takes it

These are compile-time errors, so they cannot change what a program does. They are
listed so a Go programmer is not surprised by them. The README lists what is not yet
supported.

- **Allocation**: `new`, `make` of anything but a slice, maps, a function literal
  capturing its surrounding scope, runtime string concatenation, and `string(b)` of a
  byte slice variable all need a heap. `Builder` assembles a string in storage the
  program owns.
- **A value into an interface**: an interface holds a pointer, `var s Shape = &q`,
  never `= q`. Go would copy `q` and this target has nowhere to copy it.
- **A method value of a local or of a value receiver**: the receiver is bound at
  compile time, by address.
- **A reference that outlives its storage**: the address of a local, or a slice of a
  local array, stored where it outlives the function. Go would move the local to the
  heap.
- **Unreachable code** is an error, which `go vet` only reports.
- **`defer` in a loop**, and **`go` of a builtin**, `go println(x)`.
- **`recover`** and **complex numbers** are planned. Generics are an open question.
