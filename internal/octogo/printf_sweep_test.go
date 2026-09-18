// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"math"
	"strings"
)

// The printf sweeps cross every integer and float verb with every combination of
// flags, width and precision fmt takes, over values chosen for their edges, and hold
// the output to what fmt itself prints -- computed here, by the Go running the test,
// so the expectation is fmt's by construction and never a copy of it. They join
// emitRunCases, so each is built for the host, built by the real backend and run on
// the board like every other program there.
//
// They exist because printf's layout had three authors, the emitter, the host's C
// library and the target's, and the last two disagree with fmt and with each other
// (see intPrintHelper). Written on 2026-09-18, the integer sweep found 87 of its 159
// lines wrong on the host and more on the board, and the float sweep one layout,
// +Inf under the space flag.
//
// A sweep is split into a program per verb: one program of every integer layout,
// '#' included, is more code than the target links ("Operand for call is out of
// range").
func init() {
	emitRunCases = append(emitRunCases, printfIntSweeps()...)
	emitRunCases = append(emitRunCases, printfFloatSweep())
}

// sweepVal is one value a sweep prints: as the OctoGo program spells it, and as Go
// types it, which is what fmt formats.
type sweepVal struct {
	src string
	v   any
}

// sweepSpecs is every flag, width and precision combination of a sweep, in order.
func sweepSpecs(flags, widths, precs []string) (r []string) {
	for _, f := range flags {
		for _, w := range widths {
			for _, p := range precs {
				r = append(r, f+w+p)
			}
		}
	}
	return r
}

// sweepProgram renders a sweep: each value under each verb and spec, perLine
// verbs to a printf and perFunc printfs to a function -- a target function holds
// only so many temporaries -- and the text fmt prints for it. setup is the
// statement that puts a value in x, when the values are assigned to one variable
// rather than written as arguments; decls is written ahead of the functions.
func sweepProgram(decls, xdecl string, verbs []string, vals func(verb string) []sweepVal, specs []string, perLine, perFunc int) (src, want string) {
	var funcs []string
	var body []string
	var out strings.Builder
	line := 0
	flush := func() {
		if len(body) != 0 {
			funcs = append(funcs, fmt.Sprintf("func f%d() {\n%s%s\n}\n", len(funcs), xdecl, strings.Join(body, "\n")))
			body = nil
		}
	}
	for _, verb := range verbs {
		for _, v := range vals(verb) {
			for i := 0; i < len(specs); i += perLine {
				chunk := specs[i:min(i+perLine, len(specs))]
				var format strings.Builder
				fmt.Fprintf(&format, "L%d ", line)
				fmt.Fprintf(&out, "L%d ", line)
				args := make([]string, len(chunk))
				for j, spec := range chunk {
					format.WriteString("[%" + spec + verb + "]")
					fmt.Fprintf(&out, "[%"+spec+verb+"]", v.v)
					args[j] = v.src
				}
				out.WriteByte('\n')
				stmt := fmt.Sprintf("\tprintf(%q, %s)", format.String()+"\n", strings.Join(args, ", "))
				if xdecl != "" {
					stmt = fmt.Sprintf("\tx = %s\n\tprintf(%q, %s)", v.src, format.String()+"\n", strings.TrimSuffix(strings.Repeat("x, ", len(chunk)), ", "))
				}
				body = append(body, stmt)
				line++
				if len(body) == perFunc {
					flush()
				}
			}
		}
	}
	flush()
	var calls strings.Builder
	for i := range funcs {
		fmt.Fprintf(&calls, "\tf%d()\n", i)
	}
	return decls + strings.Join(funcs, "\n") + "\nfunc main() {\n" + calls.String() + "}\n", out.String()
}

// printfIntSweeps are %d, %x, %X, %o and %b over the widths' edges, signed and not,
// under every flag, width and precision -- '#' among them, whose prefix fmt puts
// after the zeros a '0' flag pads with -- a program per verb.
func printfIntSweeps() (r []emitRunCase) {
	signed := []sweepVal{{"-42", -42}, {"0", 0}, {"42", 42}, {"int8(-5)", int8(-5)},
		{"int64(-123456789012)", int64(-123456789012)}, {"int64(-9223372036854775808)", int64(math.MinInt64)}}
	unsigned := []sweepVal{{"uint8(200)", uint8(200)}, {"uint16(4096)", uint16(4096)}, {"uint32(0)", uint32(0)},
		{"uint32(4000000000)", uint32(4000000000)}, {"uint64(18446744073709551615)", uint64(math.MaxUint64)}}
	vals := func(verb string) []sweepVal {
		switch verb {
		case "X", "b":
			return []sweepVal{signed[0], signed[1], signed[5], unsigned[3]}
		}
		return append(append([]sweepVal{}, signed...), unsigned...)
	}
	specs := sweepSpecs([]string{"", "-", "+", " ", "0", "-0", "+0", " 0", "-+", "- ", "+ ", "#", "#0", "#-", "#+"},
		[]string{"", "1", "6"}, []string{"", ".0", ".3"})
	for _, verb := range []string{"d", "x", "X", "o", "b"} {
		src, want := sweepProgram("", "", []string{verb}, vals, specs, 12, 18)
		r = append(r, emitRunCase{name: "printf sweep: %" + verb + " under every flag, width and precision, against fmt", src: src, want: want})
	}
	return r
}

// printfFloatSweep is %f, %e, %E, %g and %G of float32 values -- zero, the signs,
// the extremes, the infinities and NaN -- under every flag, width and precision.
func printfFloatSweep() emitRunCase {
	inf := float32(math.Inf(1))
	vals := func(string) []sweepVal {
		return []sweepVal{{"float32(0)", float32(0)}, {"float32(1.5)", float32(1.5)}, {"float32(-1.5)", float32(-1.5)},
			{"float32(1e-07)", float32(1e-07)}, {"float32(123456.7)", float32(123456.7)},
			{"float32(-0.000123)", float32(-0.000123)}, {"float32(3.4e+38)", float32(3.4e+38)},
			{"1 / zero", inf}, {"-1 / zero", -inf}, {"zero / zero", float32(math.NaN())}}
	}
	specs := sweepSpecs([]string{"", "-", "+", " ", "0", "-0", "+0", " 0", "- ", "+ "},
		[]string{"", "1", "9"}, []string{"", ".0", ".3"})
	src, want := sweepProgram("var zero float32\n\n", "\tvar x float32\n", []string{"f", "e", "E", "g", "G"}, vals, specs, 10, 15)
	return emitRunCase{name: "printf sweep: float verbs under every flag, width and precision, against fmt", src: src, want: want}
}
