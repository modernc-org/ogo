// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"io"
	"strings"
	"testing"
	"testing/fstest"
)

// TestCheckUniverseAcrossPackages: a name of the universe read off another
// package's declaration is the universe's here (homeQual) -- a destructured call's
// error, a range value, a receive, a variable of type any -- where each was of a
// type "a.error" or "a.any", which no rule took for an interface. And the category
// rules ask what another package's variable and a received value ARE
// (nonBoolOperand): `var n int = a.G` and `var n int = <-a.Ch` for an error were
// taken. Programs Go refuses beside the ones it takes.
func TestCheckUniverseAcrossPackages(t *testing.T) {
	const lib = `type E struct{ n int }

func (e *E) Error() string { return "E" }

type P struct{ X int }

var theE E

var G error = &theE

var Held any = &theE

var Errs = []error{&theE, nil}

var Ch = make(chan error)

var PCh = make(chan P)

var Fn func() (int, error) = Two

var GP P

func Two() (int, error) { return 3, &theE }
`
	for _, test := range []struct {
		name, body string
		want       string // "" for a program Go takes
	}{
		{"destructured", "n, err := a.Two()\n\t_ = n\n\tshow(err)", ""},
		{"destructured through a variable", "n, err := a.Fn()\n\t_ = n\n\tshow(err)", ""},
		{"range value", "for _, e := range a.Errs {\n\t\tshow(e)\n\t}", ""},
		{"receive", "e := <-a.Ch\n\tshow(e)", ""},
		{"comma-ok receive", "e, ok := <-a.Ch\n\t_ = ok\n\tshow(e)", ""},
		{"any", "held(a.Held)", ""},
		{"error into an int", "var n int = a.G\n\t_ = n", "it is an interface"},
		{"received error into an int", "var n int = <-a.Ch\n\t_ = n", "it is an interface"},
		{"received struct as a condition", "if <-a.PCh {\n\t}", "non-bool"},
		{"struct variable in arithmetic", "_ = a.GP + 1", "it is a struct"},
		{"any into an error", "var e error = a.Held\n\t_ = e", "does not implement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "import \"a\"\n\nfunc show(err error) bool { return err != nil }\n\nfunc held(x any) bool { return x != nil }\n\nfunc main() {\n\t" + test.body + "\n}\n"
			fsys := fstest.MapFS{
				"main.ogo": &fstest.MapFile{Data: []byte(src)},
				"a/a.ogo":  &fstest.MapFile{Data: []byte(lib)},
			}
			pkg, err := Build(-1, []string{"main.ogo"}, fsys)
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked(), Inline())
			}
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}
