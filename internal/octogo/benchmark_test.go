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
// MB/s, the metrics' order and the allocation columns are all held to Go's.
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
	src.WriteString("func main() {\n")
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
	// A count of runs, and what a benchmark logs, fails or skips.
	src.WriteString(`	testing.StartBenchmarks("", 16, 0, 5, true)
	var c0, c1, c2, c3, c4 testing.B
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
	println(c0.Failed(), c1.Failed(), c2.Skipped(), c3.Failed(), c4.Failed())
}
`)
	want.WriteString("BenchmarkCount\n    main.ogo:LINE: N=1\n    main.ogo:LINE: N=5\nBenchmarkCount  \t       5\t        10.00 ns/op\t       0 B/op\t       0 allocs/op\n")
	want.WriteString("BenchmarkFails\n    main.ogo:LINE: bad\n--- FAIL: BenchmarkFails\n")
	want.WriteString("BenchmarkSkips\n    main.ogo:LINE: later\n--- SKIP: BenchmarkSkips\n")
	want.WriteString("BenchmarkFatal\n    main.ogo:LINE: N=5\n--- FAIL: BenchmarkFatal\n")
	want.WriteString("BenchmarkOnce\n    main.ogo:LINE: BenchmarkOnce false false\n    main.ogo:LINE: BenchmarkOnce false false\nBenchmarkOnce   \t       5\t       0 B/op\t       0 allocs/op\n")
	want.WriteString("false true true true false\n")

	got := runFakeClock(t, src.String())
	got = fakeLineRE.ReplaceAllString(got, "main.ogo:LINE:")
	if got != want.String() {
		t.Errorf("got\n%s\nwant\n%s", got, want.String())
	}
}

// fakeLineRE is the line of a message, which TestBenchmarkFakeClock does not pin.
var fakeLineRE = regexp.MustCompile(`main\.ogo:\d+:`)

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
