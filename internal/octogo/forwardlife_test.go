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

// TestEmitCForwardedLifetime asks what a call of several results passed whole as
// another call's arguments, `keep(two(&x))`, hands its callee of this frame: the
// forwarded path returned before any argument was asked (checkForwardedIn), and the
// summaries mapped the one argument to the first parameter alone (fwdArgs). With
// them, a parameter held as a PART of an argument -- a literal's element, a value
// packed into a variadic parameter -- whose callee keeps that parameter's contents
// (crossEdge.part). Every refused row was taken in v0.54.0.
func TestEmitCForwardedLifetime(t *testing.T) {
	const head = `type E struct{ n int }

func id2(p *E) (*E, int) { return p, 1 }

func id2r(p *E) (int, *E) { return 1, p }

func idpp(p *E) (*E, *E) { return p, p }

func ids(s []*E) ([]*E, int) { return s, 1 }

func pair(p *E, q **E) (*E, **E) { return p, q }

var ge = E{5}
var gp *E
var gpp *E

func keepP(p *E, n int) { gp = p }

func keep2(n int, p *E) { gp = p }

func keepV(ps ...*E) { gp = ps[0] }

func keepC(s []*E, n int) { gp = s[0] }

func fill(src *E, dst **E) { *dst = src }

type T struct{}

func (t *T) keepM(p *E, n int) { gp = p }

type Keeper interface{ Keep(p *E, n int) }

func (t *T) Keep(p *E, n int) { gp = p }

var gt T

func relay(p *E) { keepP(id2(p)) }

func relay2(p *E) { keep2(id2r(p)) }

func first(p *E, n int) *E { return p }

func look(p *E, n int) { println(p.n, n) }

func keepC1(s []*E, n int) { gp = s[0] }

`
	for _, test := range []struct {
		name, src string
		refused   bool
	}{
		{"a forwarded result into a kept parameter", `func run() { var l E; keepP(id2(&l)) }`, true},
		{"the second result into a kept second parameter", `func run() { var l E; keep2(id2r(&l)) }`, true},
		{"a method's", `func run() { var l E; gt.keepM(id2(&l)) }`, true},
		{"an interface method's", `func run() { var l E; var k Keeper = &gt; k.Keep(id2(&l)) }`, true},
		{"a function value's", `func run() { var l E; f := keepP; f(id2(&l)) }`, true},
		{"packed into a variadic kept", `func run() { var l E; keepV(idpp(&l)) }`, true},
		{"contents into kept contents", `func run() { var l E; keepC(ids([]*E{&l})) }`, true},
		{"stored through another result", `func run() { var l E; fill(pair(&l, &gpp)) }`, true},
		{"a relay forwarding to the first parameter", `func run() { var l E; relay(&l) }`, true},
		{"a relay forwarding to the second parameter", `func run() { var l E; relay2(&l) }`, true},
		{"a relay forwarding to a method", `func relayM(p *E) { gt.keepM(id2(p)) }

func run() { var l E; relayM(&l) }`, true},
		{"a relay forwarding into a variadic", `func relayV(p *E) { keepV(idpp(p)) }

func run() { var l E; relayV(&l) }`, true},
		{"a relay forwarding contents", `func relayC(s []*E) { keepC(ids(s)) }

func run() { var l E; relayC([]*E{&l}) }`, true},
		{"a relay forwarding a store through another result", `func relayF(p *E) { fill(pair(p, &gpp)) }

func run() { var l E; relayF(&l) }`, true},
		{"a relay returning through a forwarded call", `func relayR(p *E) *E { return first(id2(p)) }

func run() { var l E; gp = relayR(&l) }`, true},
		{"a relay packing its parameter", `func relayP(p *E) { keepV(p) }

func run() { var l E; relayP(&l) }`, true},
		{"a relay putting its parameter in a literal", `func relayL(p *E) { keepC1([]*E{p}, 1) }

func run() { var l E; relayL(&l) }`, true},
		{"control package storage", `func run() { keep2(id2r(&ge)); gt.keepM(id2(&ge)); var k Keeper = &gt; k.Keep(id2(&ge)); f := keepP; f(id2(&ge)) }`, false},
		{"control package storage packed", `func run() { keepV(idpp(&ge)); keepC(ids([]*E{&ge})); fill(pair(&ge, &gpp)); relay(&ge); relay2(&ge) }`, false},
		{"control a reader", `func relayN(p *E) { look(id2(p)) }

func run() { var l E; l.n = 3; relayN(&l) }`, false},
		{"control a relay's result read", `func relayR(p *E) *E { return first(id2(p)) }

func run() { var l E; println(relayR(&l).n) }`, false},
		{"control a spread variadic", `func relayS(ps []*E) { keepV(ps...) }

func run() { var b [1]*E; b[0] = &ge; relayS(b[:]) }`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := head + test.src + "\n\nfunc main() {\n\trun()\n\tprintln(gp != nil, gpp != nil, ge.n)\n}\n"
			pkg, err := Build(-1, []string{"main.ogo"}, fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}})
			if err == nil {
				err = EmitC(pkg, io.Discard, Checked())
			}
			switch {
			case test.refused && (err == nil || !strings.Contains(err.Error(), "local")):
				t.Fatalf("got %v, want a lifetime refusal", err)
			case !test.refused && err != nil:
				t.Fatalf("refused: %v", err)
			}
		})
	}
}
