package octogo

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestTestFuncs pins which functions "ogo test" takes for tests and which it
// refuses, by Go's rules: the name "Test" alone or followed by a character that is
// not a lowercase letter, over the signature (t *testing.T) and no other; a method
// is never one; Testfoo is quietly not one, as "go test" would not run it; and a
// name that says test over a wrong signature is the error vet reports.
func TestTestFuncs(t *testing.T) {
	build := func(src string) (*Package, error) {
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte("import \"testing\"\n\n" + src + "\nfunc main() {}\n")}}
		return Build(-1, []string{"main.ogo"}, fsys)
	}
	pkg, err := build(`type X struct{ n int }

func Test(t *testing.T)     {}
func TestFoo(t *testing.T)  {}
func Test1(t *testing.T)    {}
func Test_x(t *testing.T)   {}
func Testfoo(t *testing.T)  {}
func TestÖ(t *testing.T)    {}
func (x X) TestM(t *testing.T) {}
func helper(t *testing.T)   {}
`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := TestFuncs(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(names, " "), "Test TestFoo Test1 Test_x TestÖ"; got != want {
		t.Errorf("tests %q, want %q", got, want)
	}
	for _, bad := range []struct{ src, want string }{
		{"func TestNoArg() {}\n", "main.ogo:3:6: wrong signature for TestNoArg, must be: func TestNoArg(t *testing.T)"},
		{"func TestExtra(t *testing.T, n int) {}\n", "wrong signature for TestExtra, must be: func TestExtra(t *testing.T)"},
		{"func TestWrongType(n int) {}\n", "wrong signature for TestWrongType, must be: func TestWrongType(t *testing.T)"},
		{"func TestResult(t *testing.T) int { return 0 }\n", "wrong signature for TestResult, must be: func TestResult(t *testing.T)"},
		{"func TestValue(t testing.T) {}\n", "wrong signature for TestValue, must be: func TestValue(t *testing.T)"},
	} {
		// The malformed function first, at line 3, and a well-formed one after it
		// so that the testing import is used and the checker lets the file through.
		pkg, err := build(bad.src + "\nfunc TestOK(t *testing.T) {}\n")
		if err != nil {
			t.Fatalf("%s: Build: %v", bad.src, err)
		}
		if _, err := TestFuncs(pkg); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%s: error %v, want one containing %q", bad.src, err, bad.want)
		}
	}
}

// TestBenchFuncs pins which functions "ogo test -bench" takes for benchmarks, by the
// rules of tests over the name Benchmark and the signature (b *testing.B): a method
// is never one, Benchmarkfoo is quietly not one, and a name that says benchmark
// over a wrong signature is vet's error -- found whether or not -bench asks for any,
// as go test finds it.
func TestBenchFuncs(t *testing.T) {
	build := func(src string) (*Package, error) {
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte("import \"testing\"\n\n" + src + "\nfunc main() {}\n")}}
		return Build(-1, []string{"main.ogo"}, fsys)
	}
	pkg, err := build(`type X struct{ n int }

func Benchmark(b *testing.B)          {}
func BenchmarkFoo(b *testing.B)       {}
func Benchmark_x(b *testing.B)        {}
func Benchmarkfoo(b *testing.B)       {}
func (x X) BenchmarkM(b *testing.B)   {}
func TestFoo(t *testing.T)            {}
func helper(b *testing.B)             {}
`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := BenchFuncs(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(names, " "), "Benchmark BenchmarkFoo Benchmark_x"; got != want {
		t.Errorf("benchmarks %q, want %q", got, want)
	}
	if names, err := TestFuncs(pkg); err != nil || strings.Join(names, " ") != "TestFoo" {
		t.Errorf("tests %q, %v", names, err)
	}
	for _, bad := range []struct{ src, want string }{
		{"func BenchmarkNoArg() {}\n", "main.ogo:3:6: wrong signature for BenchmarkNoArg, must be: func BenchmarkNoArg(b *testing.B)"},
		{"func BenchmarkT(t *testing.T) {}\n", "wrong signature for BenchmarkT, must be: func BenchmarkT(b *testing.B)"},
		{"func BenchmarkResult(b *testing.B) int { return 0 }\n", "wrong signature for BenchmarkResult, must be: func BenchmarkResult(b *testing.B)"},
		{"func BenchmarkValue(b testing.B) {}\n", "wrong signature for BenchmarkValue, must be: func BenchmarkValue(b *testing.B)"},
	} {
		pkg, err := build(bad.src + "\nfunc TestOK(t *testing.T) {}\n")
		if err != nil {
			t.Fatalf("%s: Build: %v", bad.src, err)
		}
		if _, err := BenchFuncs(pkg); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%s: error %v, want one containing %q", bad.src, err, bad.want)
		}
	}
	// A test over a benchmark's signature is a test of the wrong signature.
	pkg, err = build("func TestB(b *testing.B) {}\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TestFuncs(pkg); err == nil || !strings.Contains(err.Error(), "wrong signature for TestB, must be: func TestB(t *testing.T)") {
		t.Errorf("TestB: %v", err)
	}
}
