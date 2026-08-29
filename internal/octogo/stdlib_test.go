// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// stringsExercise calls every function the strings package exports, on the
// arguments where a plausible implementation and a correct one part company: an
// empty needle, a needle longer than the haystack, overlapping matches, a
// multi-byte rune, invalid UTF-8, and the white space that is white space only to
// Unicode.
//
// It is written as ONE program rather than a table because that is what makes it
// runnable by Go: the answers are not written down here at all. Whatever Go prints
// is the expectation, so nothing in this file can pin a wrong one.
const stringsExercise = `import "strings"

// One group per function rather than one long main: flexspin gives a function's
// locals COG RAM, of which there are 480 longs for all of them, and a main holding
// every call at once overran it -- "fit 480 failed". Splitting is what a program
// does about that, and reads better anyway.

func searching() {
	printf("%t %t %t %t\n", strings.Contains("seafood", "foo"), strings.Contains("seafood", "zz"),
		strings.HasPrefix("Gopher", "Go"), strings.HasSuffix("Amigo", "go"))
	printf("%d %d %d %d\n", strings.Index("chicken", "ken"), strings.Index("chicken", "zz"),
		strings.LastIndex("go gopher", "go"), strings.LastIndex("go gopher", "zz"))
}

// An EMPTY needle. Index says 0, LastIndex says len(s), and Count says one more
// than the number of runes -- three answers, none of them guessable.
func emptyNeedle() {
	printf("%d %d %d %d\n", strings.Index("abc", ""), strings.LastIndex("abc", ""),
		strings.Count("five", ""), strings.Count("", ""))
}

// A needle LONGER than the haystack, and an empty haystack.
func sizes() {
	printf("%d %d %t %t\n", strings.Index("ab", "abcd"), strings.LastIndex("ab", "abcd"),
		strings.HasPrefix("ab", "abcd"), strings.HasSuffix("", "x"))
}

// Count is NON-overlapping: "aa" appears once in "aaa", not twice.
func counting() {
	printf("%d %d %d\n", strings.Count("aaa", "aa"), strings.Count("cheese", "e"),
		strings.Count("banana", "na"))
}

// A multi-byte rune: every answer is a BYTE index.
func runes() {
	printf("%d %d %d\n", strings.Index("héllo", "llo"), strings.IndexRune("héllo", 'é'),
		strings.IndexRune("héllo", 'z'))
	printf("%d %d\n", strings.IndexByte("golang", 'l'), strings.LastIndexByte("golang", 'g'))
	printf("%d %d %t\n", strings.IndexAny("chicken", "aeiouy"), strings.IndexAny("chicken", ""),
		strings.ContainsAny("failure", "ui"))
	printf("%t %t\n", strings.ContainsRune("chicken", 'k'), strings.ContainsRune("chicken", 'z'))
}

// Trimming returns a SUBSTRING, so none of it allocates.
func trimming() {
	printf("[%s] [%s] [%s]\n", strings.TrimSpace("  \t hello \n "), strings.TrimSpace("   "),
		strings.TrimSpace(""))
	printf("[%s] [%s] [%s]\n", strings.TrimPrefix("Hello, world", "Hello, "),
		strings.TrimPrefix("Hello", "nope"), strings.TrimSuffix("Amigo", "go"))
}

// White space Unicode calls white space and ASCII does not: a non-breaking space, a
// line separator, and an ideographic space. A byte-wise trim leaves every one of
// them behind and looks right until it does not.
func unicodeSpace() {
	printf("[%s] [%s]\n", strings.TrimSpace("\u00a0\u2028 x \u3000"), strings.TrimSpace("\u00a0\u2000"))
}

func cutting() {
	b, a, ok := strings.Cut("key=value", "=")
	printf("%s|%s|%t\n", b, a, ok)
	b2, a2, ok2 := strings.Cut("nosep", "=")
	printf("%s|%s|%t\n", b2, a2, ok2)
	b3, a3, ok3 := strings.Cut("abc", "")
	printf("%s|%s|%t\n", b3, a3, ok3)
}

func cuttingFixes() {
	p, hadp := strings.CutPrefix("Gopher", "Go")
	printf("%s %t\n", p, hadp)
	q, hadq := strings.CutSuffix("Gopher", "er")
	printf("%s %t\n", q, hadq)
	r, hadr := strings.CutPrefix("Gopher", "zz")
	printf("%s %t\n", r, hadr)
}

func comparing() {
	printf("%d %d %d\n", strings.Compare("a", "b"), strings.Compare("b", "a"), strings.Compare("a", "a"))
}

// Invalid UTF-8. Ranging a string yields U+FFFD for each bad byte, so asking for
// U+FFFD finds the first one -- which is what Go's IndexRune does.
func invalidUTF8() {
	bad := "a\xffb"
	printf("%d %d %d\n", strings.IndexRune(bad, 0xFFFD), strings.IndexByte(bad, 'b'),
		strings.Count(bad, ""))
}

func main() {
	searching()
	emptyNeedle()
	sizes()
	counting()
	runes()
	trimming()
	unicodeSpace()
	cutting()
	cuttingFixes()
	comparing()
	invalidUTF8()
}
`

// stringsGoTwin turns the exercise into the Go program it is modelled on: a package
// clause, fmt beside the strings import, and printf spelled fmt.Printf. Go's own
// strings package answers the same calls, which is the whole design of the test.
func stringsGoTwin(src string) string {
	src = strings.Replace(src, `import "strings"`, "package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)", 1)
	return printfCall.ReplaceAllString(src, "fmt.Printf(")
}

// TestStringsMatchesGo runs the exercise twice, once compiled by this compiler
// against the embedded strings package and once by Go against its own, and
// requires the two to print the same bytes.
//
// This is how a standard library ought to be checked here. The package's promise is
// not "these functions are useful", it is "each means exactly what Go's of the same
// name means" -- a promise a table of hand-written expectations cannot test, because
// whoever writes the table is the same person who wrote the function and will make
// the same mistake twice. Running Go instead makes the expectation impossible to
// get wrong.
func TestStringsMatchesGo(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found; skipping the compare-with-Go test")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool found; skipping the compare-with-Go test")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	goSrc := filepath.Join(dir, "twin.go")
	if err := os.WriteFile(goSrc, []byte(stringsGoTwin(stringsExercise)), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command("go", "run", goSrc).CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, want)
	}

	fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(stringsExercise)}}
	pkg, err := Build(-1, []string{"main.ogo"}, fsys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var buf bytes.Buffer
	if err := EmitC(pkg, &buf, Checked()); err != nil {
		t.Fatalf("EmitC: %v", err)
	}
	csrc := filepath.Join(dir, "main.c")
	if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	ccOut, err := exec.Command(cc, "-std=gnu11", "-Wall", "-Wextra",
		"-Wno-unused-function", "-Wno-format", "-I", shim, "-o", bin, csrc, "-lpthread").CombinedOutput()
	if err != nil {
		t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, ccOut, buf.String())
	}
	if len(bytes.TrimSpace(ccOut)) != 0 {
		t.Errorf("cc warned:\n%s", ccOut)
	}
	got, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	if g, w := strings.ReplaceAll(string(got), "\r\n", "\n"), string(want); g != w {
		t.Errorf("strings differs from Go's:\n%s", firstDiff(g, w))
	}
}

// TestOnBoardStrings runs the same exercise on real hardware. The strings package
// is the first library code here that is neither a hardware wrapper nor a test
// harness, and it is loops over bytes -- exactly the shape the two C compilers have
// disagreed about before, so a host-green library says nothing about the target.
func TestOnBoardStrings(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool found; skipping the compare-with-Go test")
	}
	ogo := buildOgoCLI(t)
	dir := t.TempDir()

	goSrc := filepath.Join(dir, "twin.go")
	if err := os.WriteFile(goSrc, []byte(stringsGoTwin(stringsExercise)), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command("go", "run", goSrc).CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, want)
	}

	bin := filepath.Join(dir, "prog.binary")
	if err := boardBuild(ogo, dir, "prog", stringsExercise, bin, ""); err != nil {
		t.Fatalf("build: %v", err)
	}
	var out string
	var matched bool
	for attempt := 0; attempt < boardAttempts && !matched; attempt++ {
		if attempt > 0 {
			t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
		}
		out, matched = boardLoad(ogo, port, bin, string(want))
	}
	if !matched {
		t.Errorf("board output does not match Go's after %d attempts:\n%s",
			boardAttempts, firstDiff(out, string(want)))
	}
}

// mathExercise calls every function and names every constant the math package
// exports, on the arguments where a plausible implementation and a correct one part
// company: a negative half-way value for each of the four roundings, a negative zero
// out of Trunc, a negative operand and a negative modulus for Mod, and the quadrants
// Atan2 exists to tell apart.
//
// It prints to FOUR decimals, and that is the whole of what this target's precision
// costs the test. A float64 is 32 bits here, so a result carries about seven
// significant digits where Go's carries sixteen; four decimals on values of this
// magnitude is inside that, and everything below then matches Go's output byte for
// byte -- on the host and on real hardware.
const mathExercise = `import "math"

// One group per few functions rather than one long main: a function's locals live
// in 480 longs of cog RAM, and every call at once overran it for the strings
// exercise too.

func rounding() {
	printf("%.4f %.4f %.4f %.4f\n", math.Abs(-3.5), math.Abs(3.5), math.Abs(0.0), math.Abs(-0.25))
	printf("%.4f %.4f %.4f %.4f\n", math.Floor(-3.5), math.Floor(3.5), math.Ceil(-3.5), math.Ceil(3.5))
	printf("%.4f %.4f %.4f %.4f\n", math.Trunc(-3.5), math.Trunc(3.5), math.Trunc(-0.5), math.Trunc(0.5))
	printf("%.4f %.4f %.4f %.4f\n", math.Round(-3.5), math.Round(3.5), math.Round(-2.4), math.Round(2.6))
	printf("%.4f %.4f %.4f\n", math.Round(0.5), math.Round(-0.5), math.Round(0.0))
}

func powers() {
	printf("%.4f %.4f %.4f\n", math.Sqrt(2.0), math.Sqrt(9.0), math.Sqrt(0.0))
	printf("%.4f %.4f %.4f\n", math.Pow(2.0, 10.0), math.Pow(2.0, 0.5), math.Pow(9.0, -1.0))
	printf("%.4f %.4f %.4f\n", math.Exp(0.0), math.Exp(1.0), math.Exp(-1.0))
	printf("%.4f %.4f %.4f\n", math.Log(1.0), math.Log(2.0), math.Log10(100.0))
	printf("%.4f %.4f\n", math.Log2(8.0), math.Log2(0.5))
}

func trig() {
	printf("%.4f %.4f %.4f\n", math.Sin(0.0), math.Sin(0.5), math.Sin(1.0))
	printf("%.4f %.4f %.4f\n", math.Cos(0.0), math.Cos(0.5), math.Cos(1.0))
	printf("%.4f %.4f\n", math.Tan(0.0), math.Tan(0.5))
	printf("%.4f %.4f %.4f\n", math.Asin(0.5), math.Acos(0.5), math.Atan(0.5))
	printf("%.4f %.4f %.4f\n", math.Atan2(1.0, 1.0), math.Atan2(1.0, -1.0), math.Atan2(-1.0, 1.0))
}

func misc() {
	printf("%.4f %.4f %.4f\n", math.Mod(7.5, 3.0), math.Mod(-7.5, 3.0), math.Mod(7.5, -3.0))
	printf("%.4f %.4f\n", math.Copysign(2.0, -3.0), math.Copysign(-2.0, 3.0))
	printf("%.4f %.4f %.4f\n", math.Pi, math.E, math.Phi)
	printf("%.4f %.4f %.4f\n", math.Sqrt2, math.SqrtE, math.SqrtPi)
	printf("%.4f %.4f %.4f %.4f\n", math.Ln2, math.Log2E, math.Ln10, math.Log10E)
}

func main() {
	rounding()
	powers()
	trig()
	misc()
}
`

// mathGoTwin turns the exercise into the Go program it is modelled on, exactly as
// stringsGoTwin does: a package clause, fmt beside the math import, and printf
// spelled fmt.Printf.
func mathGoTwin(src string) string {
	src = strings.Replace(src, `import "math"`, "package main\n\nimport (\n\t\"fmt\"\n\t\"math\"\n)", 1)
	return printfCall.ReplaceAllString(src, "fmt.Printf(")
}

// TestMathMatchesGo runs the exercise twice, once compiled by this compiler against
// the embedded math package and once by Go against its own, and requires the two to
// print the same bytes. Same design as TestStringsMatchesGo, and for the same
// reason: whoever writes a table of expected values is the person who wrote the
// function, and will make the same mistake twice.
//
// It is worth running against Go rather than against the C library the calls are
// substituted with, because the substitution is the part that can be wrong: `Mod`
// maps to fmodf and not fmod, which the target does not have, and `Round` is built
// from Floor because the target's round() is a builtin that yields an integer.
func TestMathMatchesGo(t *testing.T) {
	cc := ""
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler found; skipping the compare-with-Go test")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool found; skipping the compare-with-Go test")
	}
	shim, err := filepath.Abs(filepath.Join("testdata", "hostp2"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	goSrc := filepath.Join(dir, "twin.go")
	if err := os.WriteFile(goSrc, []byte(mathGoTwin(mathExercise)), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command("go", "run", goSrc).CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, want)
	}

	fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(mathExercise)}}
	pkg, err := Build(-1, []string{"main.ogo"}, fsys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var buf bytes.Buffer
	if err := EmitC(pkg, &buf, Checked()); err != nil {
		t.Fatalf("EmitC: %v", err)
	}
	csrc := filepath.Join(dir, "main.c")
	if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	// -lm: the host's math functions live in libm, where the target's are compiler
	// builtins and need no library at all.
	ccOut, err := exec.Command(cc, "-std=gnu11", "-Wall", "-Wextra",
		"-Wno-unused-function", "-Wno-format", "-I", shim, "-o", bin, csrc, "-lpthread", "-lm").CombinedOutput()
	if err != nil {
		t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, ccOut, buf.String())
	}
	if len(bytes.TrimSpace(ccOut)) != 0 {
		t.Errorf("cc warned:\n%s", ccOut)
	}
	got, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	if g, w := strings.ReplaceAll(string(got), "\r\n", "\n"), string(want); g != w {
		t.Errorf("math differs from Go's:\n%s", firstDiff(g, w))
	}
}

// TestOnBoardMath runs the same exercise on real hardware. The host and the target
// do not share one math library -- the host calls libm, the target lowers each name
// to a compiler builtin -- so a host-green math package says even less about the
// target than a host-green string one does. It is what caught Mod: the host's libm
// has fmod, the target has only fmodf, and the program did not build.
func TestOnBoardMath(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool found; skipping the compare-with-Go test")
	}
	ogo := buildOgoCLI(t)
	dir := t.TempDir()

	goSrc := filepath.Join(dir, "twin.go")
	if err := os.WriteFile(goSrc, []byte(mathGoTwin(mathExercise)), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command("go", "run", goSrc).CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, want)
	}

	bin := filepath.Join(dir, "prog.binary")
	if err := boardBuild(ogo, dir, "prog", mathExercise, bin, ""); err != nil {
		t.Fatalf("build: %v", err)
	}
	var out string
	var matched bool
	for attempt := 0; attempt < boardAttempts && !matched; attempt++ {
		if attempt > 0 {
			t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
		}
		out, matched = boardLoad(ogo, port, bin, string(want))
	}
	if !matched {
		t.Errorf("board output does not match Go's after %d attempts:\n%s",
			boardAttempts, firstDiff(out, string(want)))
	}
}
