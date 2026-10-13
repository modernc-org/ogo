// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// TestBenchmarkFakeClock runs the testing package's benchmarks on the host shim with
// its clock simulated (OGO_FAKE_CLOCK): time moves only where a program waits, by
// what it asked, so how long each run of a benchmark takes is known, and so are the
// runs go test makes of it and what it prints. The b.N of each run is go test's
// predictN, copied below, and each result line is what Go's own
// testing.BenchmarkResult prints for the same N, time, bytes and metrics -- so the
// ns/op the testing package computes in integers, prettyPrint's places and widths,
// MB/s, the metrics' order and the allocation columns are all held to Go's. So is
// the ramp Loop makes inside one call of a benchmark, go test's loopSlowPath, and a
// sub-benchmark's, named, laid out and run as go test's are.
func TestBenchmarkFakeClock(t *testing.T) {
	type bench struct {
		name  string
		body  string
		nanos func(n int64) int64 // how long a run of n takes
		bytes int64
		extra map[string]float64
		mem   bool
		goal  int64 // the time each runs for, in nanoseconds
	}
	ceil := func(n, d int64) int64 { return (n + d - 1) / d }
	benches := []bench{
		{name: "BenchmarkWait200", body: "\tfor i := 0; i < b.N; i++ {\n\t\tp2.WaitCycles(200)\n\t}\n",
			nanos: func(n int64) int64 { return n * 1000 }, goal: 10_000_000},
		{name: "BenchmarkWait20Bytes", body: "\tb.SetBytes(16)\n\tb.ReportAllocs()\n\tfor i := 0; i < b.N; i++ {\n\t\tp2.WaitCycles(20)\n\t}\n",
			nanos: func(n int64) int64 { return n * 100 }, bytes: 16, mem: true, goal: 10_000_000},
		{name: "BenchmarkThird", body: "\tfor i := 0; i < b.N; i++ {\n\t\tif i%3 == 0 {\n\t\t\tp2.WaitCycles(1)\n\t\t}\n\t}\n",
			nanos: func(n int64) int64 { return ceil(n, 3) * 5 }, goal: 10_000_000},
		{name: "BenchmarkHundredth", body: "\tfor i := 0; i < b.N; i++ {\n\t\tif i%100 == 0 {\n\t\t\tp2.WaitCycles(1)\n\t\t}\n\t}\n",
			nanos: func(n int64) int64 { return ceil(n, 100) * 5 }, goal: 100_000},
		{name: "BenchmarkSlow", body: "\tfor i := 0; i < b.N; i++ {\n\t\tp2.WaitMs(1234)\n\t\tp2.WaitUs(567)\n\t}\n",
			nanos: func(n int64) int64 { return n * 1_234_567_000 }, goal: 1_000_000_000},
		{name: "BenchmarkOdd", body: "\tfor i := 0; i < b.N; i++ {\n\t\tp2.WaitCycles(2469)\n\t}\n",
			nanos: func(n int64) int64 { return n * 12345 }, goal: 50_000_000},
		{name: "BenchmarkTimer", body: "\tp2.WaitCycles(1000000)\n\tb.ResetTimer()\n\tfor i := 0; i < b.N; i++ {\n\t\tb.StopTimer()\n\t\tp2.WaitCycles(1000)\n\t\tb.StartTimer()\n\t\tp2.WaitCycles(10)\n\t}\n",
			nanos: func(n int64) int64 { return n * 50 }, goal: 10_000_000},
		{name: "BenchmarkMetrics", body: "\tfor i := 0; i < b.N; i++ {\n\t\tp2.WaitCycles(4)\n\t}\n" +
			"\tb.ReportMetric(3.5, \"clk/op\")\n\tb.ReportMetric(1234.5, \"a/op\")\n\tb.ReportMetric(0.001234, \"z/op\")\n" +
			"\tb.ReportMetric(-12.5, \"neg/op\")\n\tb.ReportMetric(99.996, \"b/op\")\n\tb.ReportMetric(7, \"clk/op\")\n",
			nanos: func(n int64) int64 { return n * 20 },
			extra: map[string]float64{"clk/op": 7, "a/op": 1234.5, "z/op": 0.001234, "neg/op": -12.5, "b/op": 99.996}, goal: 1_000_000},
		{name: "BenchmarkOverrides", body: "\tb.SetBytes(1)\n\tfor i := 0; i < b.N; i++ {\n\t\tp2.WaitCycles(4)\n\t}\n" +
			"\tb.ReportMetric(12.25, \"ns/op\")\n\tb.ReportMetric(2.5, \"MB/s\")\n\tb.ReportMetric(3, \"B/op\")\n\tb.ReportMetric(4, \"allocs/op\")\n\tb.ReportAllocs()\n",
			nanos: func(n int64) int64 { return n * 20 }, bytes: 1, mem: true,
			extra: map[string]float64{"ns/op": 12.25, "MB/s": 2.5, "B/op": 3, "allocs/op": 4}, goal: 1_000_000},
		{name: "BenchmarkNoTime", body: "\tfor i := 0; i < b.N; i++ {\n\t\tp2.WaitCycles(4)\n\t}\n\tb.ReportMetric(0, \"ns/op\")\n",
			nanos: func(n int64) int64 { return n * 20 }, extra: map[string]float64{"ns/op": 0}, goal: 1_000_000},
	}
	width := 0
	for _, b := range benches {
		width = max(width, len(b.name)+1)
	}
	var src, want strings.Builder
	src.WriteString("import (\n\t\"p2\"\n\t\"testing\"\n)\n\n")
	for _, b := range benches {
		fmt.Fprintf(&src, "func %s(b *testing.B) {\n%s}\n\n", b.name, b.body)
	}
	src.WriteString("func main() {\n\ttesting.SetSubtests(nil, testing.FullName)\n")
	want.WriteString("pkg: fake\ncpu: simulated\n")
	for i, b := range benches {
		labels := ""
		if i == 0 {
			labels = "pkg: fake\\ncpu: simulated\\n"
		}
		fmt.Fprintf(&src, "\ttesting.StartBenchmarks(\"%s\", %d, %d, 0, false)\n", labels, width, b.goal)
		fmt.Fprintf(&src, "\tvar b%d testing.B\n\ttesting.RunBenchmark(&b%d, %q, %s)\n", i, i, b.name, b.name)

		// go test's runs: one of 1, then predictN's until a run takes the goal.
		n := int64(1)
		d := b.nanos(n)
		for n < 1e9 && d < b.goal {
			n = predictN(b.goal, n, d, n)
			d = b.nanos(n)
		}
		r := testing.BenchmarkResult{N: int(n), T: time.Duration(d), Bytes: b.bytes, Extra: b.extra}
		line := r.String()
		if b.mem {
			line += "\t" + r.MemString()
		}
		fmt.Fprintf(&want, "%s\n%-*s\t%s\n", b.name, width, b.name, line)
	}
	// Loop, sub-benchmarks and a skip after the first run, under one StartBenchmarks
	// as the runner calls it: a sub-benchmark's name past the column widens it for
	// every line after it, by go test's slack of 8.
	type sub struct {
		name string
		per  int64 // the nanoseconds one op takes
		loop bool  // timed by Loop rather than b.N
	}
	const goal = 1_000_000
	topWidth := len("BenchmarkLoopTimer") + 1
	width = topWidth
	line := func(name string, n, d int64) {
		if len(name)+1 > width {
			width = len(name) + 9
		}
		r := testing.BenchmarkResult{N: int(n), T: time.Duration(d)}
		fmt.Fprintf(&want, "%-*s\t%s\n", width, name, r.String())
	}
	// ramp is go test's runs of a b.N benchmark; loopRamp is Loop's, whose time
	// is that of every op it has run since its first call.
	ramp := func(per int64) (n, d int64) {
		n, d = 1, per
		for n < 1e9 && d < goal {
			n = predictN(goal, n, d, n)
			d = n * per
		}
		return n, d
	}
	loopRamp := func(per int64) (n, d int64) {
		n, d = 1, per
		for d < goal {
			next := predictN(goal, n, d, n)
			if next <= n {
				break
			}
			n, d = next, next*per
		}
		return n, d
	}
	fmt.Fprintf(&src, "\ttesting.StartBenchmarks(\"\", %d, %d, 0, false)\n", topWidth, goal)
	src.WriteString(`	var l0, l1, l2, l3, l4 testing.B
	testing.RunBenchmark(&l0, "BenchmarkLoop", func(b *testing.B) {
		p2.WaitCycles(1000000)
		for b.Loop() {
			p2.WaitCycles(7)
		}
		p2.WaitCycles(1000000)
		b.Logf("N=%d", b.N)
	})
	testing.RunBenchmark(&l1, "BenchmarkLoopTimer", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			p2.WaitCycles(100)
			b.StartTimer()
			p2.WaitCycles(3)
		}
	})
	testing.RunBenchmark(&l2, "BenchmarkParent", func(b *testing.B) {
		b.Run("fast", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				p2.WaitCycles(1)
			}
		})
		b.Run("a sub-benchmark named long enough to widen", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				p2.WaitCycles(40)
			}
		})
		b.Run("loop", func(b *testing.B) {
			for b.Loop() {
				p2.WaitCycles(9)
			}
		})
	})
	testing.RunBenchmark(&l3, "BenchmarkAfter", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			p2.WaitCycles(20)
		}
	})
	testing.RunBenchmark(&l4, "BenchmarkSkipLater", func(b *testing.B) {
		if b.N > 1 {
			b.Skip("late")
		}
		for i := 0; i < b.N; i++ {
			p2.WaitCycles(1)
		}
	})
	println(l2.Failed(), l4.Skipped())
`)
	n, d := loopRamp(35)
	fmt.Fprintf(&want, "BenchmarkLoop\n    main.ogo:LINE: N=%d\n", n)
	line("BenchmarkLoop", n, d)
	n, d = loopRamp(15)
	want.WriteString("BenchmarkLoopTimer\n")
	line("BenchmarkLoopTimer", n, d)
	want.WriteString("BenchmarkParent\n")
	for _, s := range []sub{{"fast", 5, false}, {"a_sub-benchmark_named_long_enough_to_widen", 200, false}, {"loop", 45, true}} {
		full := "BenchmarkParent/" + s.name
		want.WriteString(full + "\n")
		if s.loop {
			n, d = loopRamp(s.per)
		} else {
			n, d = ramp(s.per)
		}
		line(full, n, d)
	}
	n, d = ramp(100)
	want.WriteString("BenchmarkAfter\n")
	line("BenchmarkAfter", n, d)
	fmt.Fprintf(&want, "BenchmarkSkipLater\n    main.ogo:LINE: late\n%-*s\t       0\t               NaN ns/op\nfalse true\n", width, "BenchmarkSkipLater")

	// A count of runs, and what a benchmark logs, fails or skips.
	src.WriteString(`	testing.StartBenchmarks("", 16, 0, 5, true)
	var c0, c1, c2, c3, c4, c5, c6, c7, c8 testing.B
	testing.RunBenchmark(&c0, "BenchmarkCount", func(b *testing.B) {
		b.Logf("N=%d", b.N)
		for i := 0; i < b.N; i++ {
			p2.WaitCycles(2)
		}
	})
	testing.RunBenchmark(&c1, "BenchmarkFails", func(b *testing.B) {
		b.Error("bad")
	})
	testing.RunBenchmark(&c2, "BenchmarkSkips", func(b *testing.B) {
		b.Skip("later")
	})
	testing.RunBenchmark(&c3, "BenchmarkFatal", func(b *testing.B) {
		if b.N > 1 {
			b.Fatalf("N=%d", b.N)
		}
	})
	testing.RunBenchmark(&c4, "BenchmarkOnce", func(b *testing.B) {
		b.Log(b.Name(), b.Failed(), b.Skipped())
	})
	testing.RunBenchmark(&c5, "BenchmarkLoopN", func(b *testing.B) {
		for b.Loop() {
			p2.WaitCycles(2)
		}
		b.Logf("N=%d", b.N)
	})
	testing.RunBenchmark(&c6, "BenchmarkSubN", func(b *testing.B) {
		b.Logf("N=%d", b.N)
		b.Run("x", func(b *testing.B) {
			b.Logf("N=%d %s", b.N, b.Name())
		})
	})
	testing.RunBenchmark(&c7, "BenchmarkStops", func(b *testing.B) {
		b.StopTimer()
		for b.Loop() {
		}
	})
	testing.RunBenchmark(&c8, "BenchmarkBreaks", func(b *testing.B) {
		for b.Loop() {
			break
		}
	})
	println(c0.Failed(), c1.Failed(), c2.Skipped(), c3.Failed(), c4.Failed(), c5.Failed(), c6.Failed(), c7.Failed(), c8.Failed())
}
`)
	want.WriteString("BenchmarkCount\n    main.ogo:LINE: N=1\n    main.ogo:LINE: N=5\nBenchmarkCount  \t       5\t        10.00 ns/op\t       0 B/op\t       0 allocs/op\n")
	want.WriteString("BenchmarkFails\n    main.ogo:LINE: bad\n--- FAIL: BenchmarkFails\n")
	want.WriteString("BenchmarkSkips\n    main.ogo:LINE: later\n--- SKIP: BenchmarkSkips\n")
	want.WriteString("BenchmarkFatal\n    main.ogo:LINE: N=5\n--- FAIL: BenchmarkFatal\n")
	want.WriteString("BenchmarkOnce\n    main.ogo:LINE: BenchmarkOnce false false\n    main.ogo:LINE: BenchmarkOnce false false\nBenchmarkOnce   \t       5\t       0 B/op\t       0 allocs/op\n")
	want.WriteString("BenchmarkLoopN\n    main.ogo:LINE: N=5\nBenchmarkLoopN  \t       5\t        10.00 ns/op\t       0 B/op\t       0 allocs/op\n")
	want.WriteString("BenchmarkSubN\n    main.ogo:LINE: N=1\nBenchmarkSubN/x\n    main.ogo:LINE: N=1 BenchmarkSubN/x\n    main.ogo:LINE: N=5 BenchmarkSubN/x\nBenchmarkSubN/x \t       5\t       0 B/op\t       0 allocs/op\n")
	want.WriteString("BenchmarkStops\n    testing.ogo:LINE: B.Loop called with timer stopped\n--- FAIL: BenchmarkStops\n")
	want.WriteString("BenchmarkBreaks\n    testing.ogo:LINE: benchmark function returned without B.Loop() == false (break or return in loop?)\n--- FAIL: BenchmarkBreaks\n")
	want.WriteString("false true true true false false false true true\n")

	got := runFakeClock(t, src.String())
	got = fakeLineRE.ReplaceAllString(got, "$1.ogo:LINE:")
	if got != want.String() {
		t.Errorf("got\n%s\nwant\n%s", got, want.String())
	}
}

// fakeLineRE is the line of a message, which TestBenchmarkFakeClock does not pin.
var fakeLineRE = regexp.MustCompile(`(main|testing)\.ogo:\d+:`)

// predictN is go test's, from testing/benchmark.go.
func predictN(goalns int64, prevIters int64, prevns int64, last int64) int64 {
	if prevns == 0 {
		prevns = 1
	}
	n := goalns * prevIters / prevns
	n += n / 5
	n = min(n, 100*last)
	n = max(n, last+1)
	n = min(n, 1e9)
	return n
}

// runFakeClock builds a program for the host shim with OGO_FAKE_CLOCK, runs it and
// returns what it printed.
func runFakeClock(t *testing.T, src string) string {
	t.Helper()
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}})
	if err != nil {
		t.Fatalf("Build: %v\n%s", err, src)
	}
	var buf bytes.Buffer
	if err := EmitC(pkg, &buf, Checked(), Inline()); err != nil {
		t.Fatalf("EmitC: %v", err)
	}
	dir := t.TempDir()
	csrc := filepath.Join(dir, "main.c")
	if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if out, err := exec.Command(cc, "-std=gnu11", "-fwrapv", "-Wall", "-Wextra", "-Wno-unused-function", "-Wno-format",
		"-DOGO_FAKE_CLOCK", "-I", shim, "-o", bin, csrc, "-lpthread", "-lm").CombinedOutput(); err != nil || len(bytes.TrimSpace(out)) != 0 {
		t.Fatalf("cc: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	return string(out)
}
