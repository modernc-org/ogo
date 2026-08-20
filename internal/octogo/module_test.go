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

// modulePkgProgram is the layout a module exists for and the one that cannot be
// written without it: the main package is NOT the root, a library it imports is
// reached by the same path from two different importers, and one of those importers
// is the module root itself. Every import names the same directory whoever writes
// it, which is what an import path relative to the package being built could not do.
var modulePkgProgram = map[string]string{
	"lib.ogo": `func Tag() int { return 7 }`,

	"sensor/sensor.ogo": `func Read() int { return 42 }`,

	// A shared dependency, imported here and by main under the identical path.
	"util/util.ogo": `import "example.com/proj/sensor"

func Twice() int { return 2 * sensor.Read() }`,

	// A multi-element path, and a package whose last element is what names it.
	"deep/a/b/b.ogo": `func Deep() int { return 9 }`,

	"cmd/app/main.ogo": `import "example.com/proj"
import "example.com/proj/deep/a/b"
import "example.com/proj/sensor"
import "example.com/proj/util"

func main() {
	println(sensor.Read(), util.Twice(), proj.Tag(), b.Deep())
}`,
}

const modulePkgWant = "42 84 7 9\n"

// TestBuildModuleLayout builds and runs the layout above. Compiling it is not
// enough: a shared dependency reached by two importers must be ONE package in the
// emitted translation unit, and the answer is what says so.
func TestBuildModuleLayout(t *testing.T) {
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
	fsys := fstest.MapFS{}
	for name, src := range modulePkgProgram {
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}
	pkg, err := BuildModule(-1, "example.com/proj", "cmd/app", []string{"main.ogo"}, fsys)
	if err != nil {
		t.Fatalf("BuildModule: %v", err)
	}
	var buf bytes.Buffer
	if err := EmitC(pkg, &buf, Checked()); err != nil {
		t.Fatalf("EmitC: %v", err)
	}
	dir := t.TempDir()
	csrc := filepath.Join(dir, "main.c")
	if err := os.WriteFile(csrc, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	out, err := exec.Command(cc, "-std=gnu11", "-Wall", "-Wextra",
		"-Wno-unused-function", "-Wno-format", "-I", shim, "-o", bin, csrc, "-lpthread").CombinedOutput()
	if err != nil {
		t.Fatalf("cc: %v\n%s\n--- emitted ---\n%s", err, out, buf.String())
	}
	if len(bytes.TrimSpace(out)) != 0 {
		t.Errorf("cc warned:\n%s\n--- emitted ---\n%s", out, buf.String())
	}
	got, runErr := exec.Command(bin).CombinedOutput()
	if runErr != nil {
		t.Fatalf("run: %v\n%s", runErr, got)
	}
	if g := strings.ReplaceAll(string(got), "\r\n", "\n"); g != modulePkgWant {
		t.Errorf("output:\n got %q\nwant %q\n--- emitted ---\n%s", g, modulePkgWant, buf.String())
	}
}

// TestBuildModuleResolution covers what an import path may and may not name once a
// module says where the root is. The cycle cases are here because the identity a
// package is known by changed with modules: an import graph keyed by what was
// WRITTEN while its edges arrive keyed by what a package IS connects nothing, and a
// cycle then deadlocks on its own build instead of being reported.
func TestBuildModuleResolution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		module string
		dir    string
		files  map[string]string
		want   string // "" == builds
	}{
		{
			name:   "bare path inside a module",
			module: "example.com/proj", dir: ".",
			files: map[string]string{
				"main.ogo":          "import \"sensor\"\n\nfunc main() { println(sensor.Read()) }",
				"sensor/sensor.ogo": "func Read() int { return 1 }",
			},
			want: `did you mean "example.com/proj/sensor"`,
		},
		{
			name:   "a path outside the module",
			module: "example.com/proj", dir: ".",
			files: map[string]string{
				"main.ogo": "import \"other.com/x\"\n\nfunc main() { println(x.V()) }",
			},
			want: `package "other.com/x" is not in module "example.com/proj"`,
		},
		{
			name:   "importing the main package",
			module: "example.com/p", dir: ".",
			files: map[string]string{
				"main.ogo":  "import \"example.com/p/lib\"\n\nfunc main() { println(lib.V()) }",
				"lib/l.ogo": "import \"example.com/p\"\n\nfunc V() int { return 1 }",
			},
			want: `import of main package "example.com/p"`,
		},
		{
			name:   "a directory claiming the module root's name",
			module: "example.com/proj", dir: "cmd/app",
			files: map[string]string{
				"lib.ogo":          "func Tag() int { return 1 }",
				"proj/p.ogo":       "func X() int { return 2 }",
				"cmd/app/main.ogo": "import \"example.com/proj\"\n\nfunc main() { println(proj.Tag()) }",
			},
			want: "claims the same package name",
		},
		{
			name:   "an import cycle under a module",
			module: "m.example/c", dir: ".",
			files: map[string]string{
				"main.ogo": "import \"m.example/c/a\"\n\nfunc main() { println(a.V()) }",
				"a/a.ogo":  "import \"m.example/c/b\"\n\nfunc V() int { return b.W() }",
				"b/b.ogo":  "import \"m.example/c/a\"\n\nfunc W() int { return a.V() }",
			},
			want: "import cycle not allowed",
		},
		{
			name:   "the module root imported as a package",
			module: "example.com/proj", dir: "cmd/app",
			files: map[string]string{
				"lib.ogo":          "func Tag() int { return 1 }",
				"cmd/app/main.ogo": "import \"example.com/proj\"\n\nfunc main() { println(proj.Tag()) }",
			},
		},
		{
			name:   "an intrinsic import needs no module prefix",
			module: "example.com/proj", dir: ".",
			files: map[string]string{
				"main.ogo": "import \"p2\"\n\nfunc main() { p2.PinHigh(56) }",
			},
		},
		{
			name:   "a capital in a package directory",
			module: "example.com/proj", dir: ".",
			files: map[string]string{
				"main.ogo":     "import \"example.com/proj/Sensor\"\n\nfunc main() { println(Sensor.Read()) }",
				"Sensor/s.ogo": "func Read() int { return 1 }",
			},
			want: "must be lower case",
		},
		{
			// The prefix is a repository name and never reaches a filesystem.
			name:   "capitals in the module prefix",
			module: "example.com/BurntSushi/proj", dir: ".",
			files: map[string]string{
				"main.ogo":          "import \"example.com/BurntSushi/proj/sensor\"\n\nfunc main() { println(sensor.Read()) }",
				"sensor/sensor.ogo": "func Read() int { return 1 }",
			},
		},
		{
			// The last element is the qualifier and must be an identifier; an
			// element that is not has no such constraint.
			name:   "a hyphen above the package directory",
			module: "example.com/proj", dir: ".",
			files: map[string]string{
				"main.ogo":             "import \"example.com/proj/my-libs/sensor\"\n\nfunc main() { println(sensor.Read()) }",
				"my-libs/sensor/s.ogo": "func Read() int { return 1 }",
			},
		},
		{
			// The rule is the path's, not the module's: without one the whole path
			// is the directory.
			name:   "a capital with no module at all",
			module: "", dir: ".",
			files: map[string]string{
				"main.ogo":     "import \"Sensor\"\n\nfunc main() { println(Sensor.Read()) }",
				"Sensor/s.ogo": "func Read() int { return 1 }",
			},
			want: "must be lower case",
		},
		{
			name:   "an embedded package needs no module prefix",
			module: "example.com/proj", dir: ".",
			files: map[string]string{
				"main.ogo": "import \"strings\"\n\nfunc main() { println(strings.Contains(\"abc\", \"b\")) }",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for name, src := range tc.files {
				fsys[name] = &fstest.MapFile{Data: []byte(src)}
			}
			_, err := BuildModule(-1, tc.module, tc.dir, []string{"main.ogo"}, fsys)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.want != "" && err == nil:
				t.Errorf("got no error, want one matching %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}
