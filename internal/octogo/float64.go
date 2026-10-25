// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"fmt"
	"go/constant"
	"go/token"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// float64 is IEEE 754 binary64, in software: the target has no floating-point
// hardware at all, and its C compiler's double is 32 bits, so a float64 is a pair of
// words the emitter's helpers compute on, the same C on the host as on the board
// (cF64). A program that uses no float64 carries none of it.

// cF64 is the C type of a float64: its two words, the low one first, as the target
// lays out a 64-bit integer. A struct rather than a uint64_t, so that a C operator
// applied to one is refused by both compilers instead of computing on its bits.
const cF64 = "ogo_f64"

// f64Typedef leads the typedef section, as the string's does: a struct field, an
// array element and a result may be a float64.
const f64Typedef = "typedef struct { uint32_t lo, hi; } ogo_f64;\n"

// The places in the assembled C the float64 typedef and its runtime go (f64Section):
// spelled with no "ogo_f64" in them, which is what says whether the C uses one.
const (
	f64TypedefMark = "/*ogo:f64:typedef*/\n"
	f64RuntimeMark = "/*ogo:f64:runtime*/\n"
)

// f64Section fills the two places with the float64 typedef, the constants the
// program names and the runtime where the C names an ogo_f64, and empties them
// otherwise: the C type is spelled in too many places for each to say so, and the
// text is what was written.
func (e *emitter) f64Section(src []byte) []byte {
	used := bytes.Contains(bytes.ReplaceAll(bytes.ReplaceAll(src, []byte(f64TypedefMark), nil), []byte(f64RuntimeMark), nil), []byte(cF64))
	typedef, runtime := "", ""
	if used {
		var b bytes.Buffer
		b.WriteString("#include <stdint.h>\n" + f64Typedef)
		// The constants the text names, and only those: a rendering thrown away
		// named some too.
		names := map[string]bool{}
		for _, m := range f64ConstRE.FindAll(src, -1) {
			names[string(m)] = true
		}
		for _, nm := range sortedKeys(names) {
			k, _ := strconv.ParseUint(strings.TrimPrefix(nm, f64ConstPrefix), 16, 64)
			fmt.Fprintf(&b, "static const ogo_f64 %s = %s;\n", nm, f64Braces(k))
		}
		typedef, runtime = b.String(), f64Runtime+"\n"
	}
	// A float32's bits, math.Float32bits and Float32frombits, are no float64's:
	// written with the float64 runtime, a program calling one with no float64 in it
	// named a function the C did not define.
	for _, h := range f32BitsHelpers {
		if bytes.Contains(src, []byte(h.name+"(")) {
			runtime += h.def
		}
	}
	src = bytes.Replace(src, []byte(f64TypedefMark), []byte(typedef), 1)
	return bytes.Replace(src, []byte(f64RuntimeMark), []byte(runtime), 1)
}

// f32BitsHelpers are math's Float32bits and Float32frombits, through a union the
// target reads as memory.
var f32BitsHelpers = []struct{ name, def string }{
	{"ogo_f32_bits", "static uint32_t ogo_f32_bits(float x) { union { float f; uint32_t u; } v; v.f = x; return v.u; }\n"},
	{"ogo_f32_frombits", "static float ogo_f32_frombits(uint32_t b) { union { float f; uint32_t u; } v; v.u = b; return v.f; }\n"},
}

// f64ConstName names the static const holding the float64 of the bits b.
func f64ConstName(b uint64) string { return fmt.Sprintf(f64ConstPrefix+"%016x", b) }

const f64ConstPrefix = "ogo_f64k_"

var f64ConstRE = regexp.MustCompile(f64ConstPrefix + "[0-9a-f]{16}")

// f64Braces is the initializer of the float64 of the bits b: its words, low first.
func f64Braces(b uint64) string { return fmt.Sprintf("{0x%08xu, 0x%08xu}", uint32(b), uint32(b>>32)) }

// f64ConstC spells the float64 v: in a static initializer as its words in braces,
// which is what one takes, and elsewhere as the static const holding them -- a
// value an operand, an argument and a declaration take alike. A Go constant has no
// negative zero, so v has none either.
func (e *emitter) f64ConstC(v float64) string {
	b := math.Float64bits(v)
	if e.declInit {
		return f64Braces(b)
	}
	return f64ConstName(b)
}

// f64ConstOf is the float64 a constant expression converts to, rounded once from its
// exact value as Go rounds it, spelled (f64ConstC); false for anything that does not
// fold.
func (e *emitter) f64ConstOf(ast []int32) (string, bool) {
	v, ok := e.foldConstVal(ast)
	if !ok || (v.Kind() != constant.Int && v.Kind() != constant.Float) {
		return "", false
	}
	f, _ := constant.Float64Val(constant.ToFloat(v))
	if math.IsInf(f, 0) {
		return "", false
	}
	return e.f64ConstC(f), true
}

// f64ConstNode is f64ConstOf of a node, folded as the node it is: a prefix
// operator, `-1`, belongs to the node rather than standing first in a sequence.
func (e *emitter) f64ConstNode(n Node) (string, bool) {
	v, ok := e.foldValNode(n)
	if !ok || (v.Kind() != constant.Int && v.Kind() != constant.Float) {
		return "", false
	}
	f, _ := constant.Float64Val(constant.ToFloat(v))
	if math.IsInf(f, 0) {
		return "", false
	}
	return e.f64ConstC(f), true
}

// f64TypedConstC spells a constant expression of a float64 type, `Boil * 2`,
// `-Neg`, `(+Neg)`, as the one float64 Go folds it to, exactly and rounded once. An
// untyped one is a float64 only where it meets one, which the checker records
// (noteF64Const) and the operations spell (f64OperandC).
func (e *emitter) f64TypedConstC(n Node) (string, bool) {
	if !e.f64TypedConst(n) {
		return "", false
	}
	return e.f64ConstNode(n)
}

// f64TypedConst reports whether n is a constant of a float64 type, which it is
// wherever it stands.
func (e *emitter) f64TypedConst(n Node) bool {
	// The fold first: it gives up at the first operand that is no constant, where
	// asking whether the node is untyped walks all of it, at every level of a deep
	// polynomial (J0's, Erf's).
	if v, ok := e.foldValNode(n); !ok || !numericConst(v) {
		return false
	}
	if e.operandUntyped(n) {
		return false
	}
	ct, ok := e.inferNode(n)
	return ok && e.underlyingCType(ct) == cF64
}

// emitValueAs emits ast where a value of the C type ct is wanted -- a temporary
// typed by inferring ast, an argument of a helper: a constant there as the float64
// it converts to where ct is one (f64ConstOf), since no literal converts to a
// float64, and anything else as itself.
func (e *emitter) emitValueAs(ct string, ast []int32) {
	switch e.underlyingCType(ct) {
	case cF64:
		if c, ok := e.f64ConstOf(ast); ok {
			e.emit(c)
			return
		}
	case "float":
		// A float32's constant is the float32 Go rounds it to, once.
		if v, ok := e.foldConstVal(ast); ok && numericConst(v) {
			if lit, ok := floatSpelling(v, "float"); ok {
				e.emit(e.parenNegative(lit))
				return
			}
		}
	}
	e.emitExpr(ast)
}

// isFloatParam reports whether a parameter of the C type ct is a float of either
// width.
func (e *emitter) isFloatParam(ct string) bool {
	u := e.underlyingCType(ct)
	return u == cF64 || u == "float"
}

// f64OperandC renders an operand of a float64 operation: a constant as the float64
// it converts to, anything else as itself.
func (e *emitter) f64OperandC(n Node) string {
	if c, ok := e.f64ConstNode(n); ok {
		return c
	}
	return e.captureC(func() { e.emitExprNode(n) })
}

// isF64Node reports whether n computes a float64 that is no constant: what makes an
// operation one of float64s, a constant beside it taking its type.
func (e *emitter) isF64Node(n Node) bool {
	if _, isConst := e.foldValNode(n); isConst {
		return false
	}
	ct, ok := e.inferNode(n)
	return ok && e.underlyingCType(ct) == cF64
}

// f64Ops are the helpers an arithmetic operator of float64s calls.
var f64Ops = map[string]string{"+": "ogo_f64_add", "-": "ogo_f64_sub", "*": "ogo_f64_mul", "/": "ogo_f64_div"}

// emitF64Level writes an arithmetic level of float64s, `a*b/c`, as the calls
// computing it left to right, ogo_f64_div(ogo_f64_mul(a, b), c), and reports whether
// the level is one. The operands' evaluation order is the level's (bindEffectOperands
// ran first).
func (e *emitter) emitF64Level(kids []Node) bool {
	if len(kids) < 3 {
		return false
	}
	any := false
	for i, c := range kids {
		if i%2 == 1 {
			if c.sym != AddOp && c.sym != MulOp {
				return false
			}
			continue
		}
		any = any || e.isF64Node(c)
	}
	if !any {
		return false
	}
	acc, first := e.f64ConstPrefix(kids)
	if first == 1 {
		acc = e.f64OperandC(kids[0])
	}
	for i := first; i+1 < len(kids); i += 2 {
		op := e.opText(kids[i].ast)
		fn, ok := f64Ops[op]
		if !ok {
			e.failAt(kids[i].ast, "operator %s is not defined on float64", op)
			return true
		}
		acc = fn + "(" + acc + ", " + e.f64OperandC(kids[i+1]) + ")"
	}
	e.emit(acc)
	return true
}

// f64ConstPrefix folds the constant operands leading a level of float64s, two or
// more, as Go folds them: one constant expression, `2/math.Pi*230` in
// `2/math.Pi*230*t`, computed exactly and rounded once where it meets the float64
// -- or, where a typed constant takes part, rounded at each step of that type, as
// foldValSeq computes it. It answers the folded operand and the index of the
// operator following it, or 1 where nothing folds. Computed a step at a time in
// float64s it was a ULP off there, `2/3*x` was 0.666... where Go's is 0 (an
// integer division), `1e300*1e300/1e300*x` was +Inf, and `1<<3*x` was refused, the
// shift taken for one of float64s.
func (e *emitter) f64ConstPrefix(kids []Node) (string, int) {
	if _, ok := e.foldValNode(kids[0]); !ok {
		return "", 1
	}
	k := 1
	for k+1 < len(kids) {
		if _, ok := e.foldValNode(kids[k+1]); !ok {
			break
		}
		k += 2
	}
	if k < 3 || k == len(kids) {
		return "", 1
	}
	v, ok := e.foldValSeq(kids[:k])
	if !ok || !numericConst(v) {
		return "", 1
	}
	f, _ := constant.Float64Val(constant.ToFloat(v))
	if math.IsInf(f, 0) {
		return "", 1 // past float64, which the checker refuses
	}
	return e.f64ConstC(f), k
}

// f64CompareAt reports whether kids[i..i+2] compares two float64s, one of which is
// no constant, and its operator.
func (e *emitter) f64CompareAt(kids []Node, i int) (string, bool) {
	if i+2 >= len(kids) || kids[i+1].sym != RelOp || e.isLogicalOp(kids[i+1]) {
		return "", false
	}
	if !e.isF64Node(kids[i]) && !e.isF64Node(kids[i+2]) {
		return "", false
	}
	return e.opText(kids[i+1].ast), true
}

// f64ConstCompareC answers a comparison of two constants, one of a float64 type,
// "1" or "0": Go compares them as the float64s they are.
//
// Two untyped constants are compared exactly, as Go compares them: `0.1+0.2 == 0.3`
// is true. Handed to C, the float64 they default to was computed in C's doubles.
func (e *emitter) f64ConstCompareC(kids []Node, i int, op string) (string, bool) {
	l, r := kids[i], kids[i+2]
	typed := e.f64TypedConst(l) || e.f64TypedConst(r)
	if !typed && (!e.operandUntyped(l) || !e.operandUntyped(r)) {
		return "", false
	}
	x, okX := e.foldValNode(l)
	y, okY := e.foldValNode(r)
	if !okX || !okY || !numericConst(x) || !numericConst(y) {
		return "", false
	}
	tok, known := map[string]token.Token{"==": token.EQL, "!=": token.NEQ, "<": token.LSS, "<=": token.LEQ, ">": token.GTR, ">=": token.GEQ}[op]
	if !known {
		return "", false
	}
	if typed {
		fx, _ := constant.Float64Val(constant.ToFloat(x))
		fy, _ := constant.Float64Val(constant.ToFloat(y))
		x, y = constant.MakeFloat64(fx), constant.MakeFloat64(fy)
	}
	if constant.Compare(x, tok, y) {
		return "1", true
	}
	return "0", true
}

// numericConst reports whether v is a constant of a number, an integer or a float.
func numericConst(v constant.Value) bool {
	return v.Kind() == constant.Int || v.Kind() == constant.Float
}

// emitF64CompareTriple writes a comparison of float64s as Go defines it on IEEE
// values: a NaN is unequal to everything, itself included, and less than nothing;
// the two zeros are equal.
func (e *emitter) emitF64CompareTriple(op string, l, r Node) {
	lt, rt := e.f64OperandC(l), e.f64OperandC(r)
	switch op {
	case "==":
		e.emit("ogo_f64_eq(" + lt + ", " + rt + ")")
	case "!=":
		e.emit("!ogo_f64_eq(" + lt + ", " + rt + ")")
	case "<":
		e.emit("ogo_f64_lt(" + lt + ", " + rt + ")")
	case "<=":
		e.emit("ogo_f64_le(" + lt + ", " + rt + ")")
	case ">":
		e.emit("ogo_f64_lt(" + rt + ", " + lt + ")")
	case ">=":
		e.emit("ogo_f64_le(" + rt + ", " + lt + ")")
	default:
		e.fail("operator %s is not defined on float64", op)
	}
}

// f64Runtime is the arithmetic: every operation rounds to nearest, ties to even, as
// Go's float64 does, and was held to Go's answers on 39.5 million vectors on the
// host and on 5,002 on a P2-EDGE (2026-10-24). The structure is Berkeley
// SoftFloat-2's 32-bit one, written in words because the target's C compiler
// miscompiles several shapes of 64-bit integer arithmetic and spends more on it than
// on the words. Written for that compiler: no local has its address taken (the word
// arithmetic is macros on locals), no parameter is assigned, there is no goto, and no
// value of two words is chosen by a conditional expression -- `c ? f(x) : g(x)` of a
// struct called f twice and read the high word from a register nothing wrote, on the
// board, which is why every such choice is an if.
const f64Runtime = `// A float's bits, through a union the target reads as memory: a 64-bit shift by 32
// is a shape its C compiler gets wrong.
typedef union { ogo_f64 f; uint64_t u; } ogo_f64u;
static uint64_t ogo_f64_bits(ogo_f64 x) { ogo_f64u v; v.f = x; return v.u; }
static ogo_f64 ogo_f64_frombits(uint64_t b) { ogo_f64u v; v.u = b; return v.f; }
typedef struct { uint32_t w0, w1; } ogo_w2;

static inline ogo_f64 ogo_f64_w(uint32_t hi, uint32_t lo) { ogo_f64 r; r.lo = lo; r.hi = hi; return r; }

#define OGO_ADD64(z0, z1, a0, a1, b0, b1) do { uint32_t _ad_l = (a1) + (b1), _ad_h = (a0) + (b0) + (_ad_l < (a1)); (z1) = _ad_l; (z0) = _ad_h; } while (0)
#define OGO_SUB64(z0, z1, a0, a1, b0, b1) do { uint32_t _sb_l = (a1) - (b1), _sb_h = (a0) - (b0) - ((a1) < (b1)); (z1) = _sb_l; (z0) = _sb_h; } while (0)
#define OGO_SHL64(z0, z1, a0, a1, c) do { uint32_t _sl_a0 = (a0), _sl_a1 = (a1); int _sl_c = (c); (z1) = _sl_a1 << _sl_c; (z0) = _sl_c == 0 ? _sl_a0 : (_sl_a0 << _sl_c | _sl_a1 >> (32 - _sl_c)); } while (0)
#define OGO_GE64(a0, a1, b0, b1) ((a0) > (b0) || ((a0) == (b0) && (a1) >= (b1)))

static int ogo_clz32(uint32_t v) {
	uint32_t x = v;
	int n = 0;
	if (x == 0) { return 32; }
	if ((x & 0xFFFF0000u) == 0) { n += 16; x <<= 16; }
	if ((x & 0xFF000000u) == 0) { n += 8; x <<= 8; }
	if ((x & 0xF0000000u) == 0) { n += 4; x <<= 4; }
	if ((x & 0xC0000000u) == 0) { n += 2; x <<= 2; }
	if ((x & 0x80000000u) == 0) { n += 1; }
	return n;
}

// a0:a1:a2 shifted right by c into z0:z1:z2, the bits shifted out OR-ed into the
// last word. A statement on locals: a struct of three words handed back from a
// function lost its words on the target.
#define OGO_SHR96J(z0, z1, z2, a0, a1, a2, c) do { \
	uint32_t _r3_a0 = (a0), _r3_a1 = (a1), _r3_a2 = (a2), _r3_z0, _r3_z1, _r3_z2; int _r3_c = (c), _r3_n = -_r3_c & 31; \
	if (_r3_c == 0) { _r3_z0 = _r3_a0; _r3_z1 = _r3_a1; _r3_z2 = _r3_a2; } \
	else if (_r3_c < 32) { _r3_z2 = _r3_a1 << _r3_n | (_r3_a2 != 0); _r3_z1 = _r3_a0 << _r3_n | _r3_a1 >> _r3_c; _r3_z0 = _r3_a0 >> _r3_c; } \
	else if (_r3_c == 32) { _r3_z2 = _r3_a1 | (_r3_a2 != 0); _r3_z1 = _r3_a0; _r3_z0 = 0; } \
	else { _r3_a2 |= _r3_a1; if (_r3_c < 64) { _r3_z2 = _r3_a0 << _r3_n | (_r3_a2 != 0); _r3_z1 = _r3_a0 >> (_r3_c & 31); } else { _r3_z2 = _r3_c == 64 ? (_r3_a0 | (_r3_a2 != 0)) : ((_r3_a0 | _r3_a2) != 0); _r3_z1 = 0; } _r3_z0 = 0; } \
	(z0) = _r3_z0; (z1) = _r3_z1; (z2) = _r3_z2; } while (0)

// a0:a1 shifted right by c into z0:z1, the bits shifted out OR-ed into the last bit.
#define OGO_SHR64J(z0, z1, a0, a1, c) do { \
	uint32_t _r2_a0 = (a0), _r2_a1 = (a1), _r2_z0, _r2_z1; int _r2_c = (c), _r2_n = -_r2_c & 31; \
	if (_r2_c == 0) { _r2_z0 = _r2_a0; _r2_z1 = _r2_a1; } \
	else if (_r2_c < 32) { _r2_z1 = _r2_a0 << _r2_n | _r2_a1 >> _r2_c | ((_r2_a1 << _r2_n) != 0); _r2_z0 = _r2_a0 >> _r2_c; } \
	else if (_r2_c == 32) { _r2_z1 = _r2_a0 | (_r2_a1 != 0); _r2_z0 = 0; } \
	else if (_r2_c < 64) { _r2_z1 = _r2_a0 >> (_r2_c & 31) | (((_r2_a0 << _r2_n) | _r2_a1) != 0); _r2_z0 = 0; } \
	else { _r2_z1 = (_r2_a0 | _r2_a1) != 0; _r2_z0 = 0; } \
	(z0) = _r2_z0; (z1) = _r2_z1; } while (0)

// The 64-bit product z0:z1 of two words, from four 16-bit products the hardware makes.
#define OGO_MUL32W(z0, z1, a, b) do { \
	uint32_t _mw_a = (a), _mw_b = (b), _mw_ah = _mw_a >> 16, _mw_al = _mw_a & 0xFFFF, _mw_bh = _mw_b >> 16, _mw_bl = _mw_b & 0xFFFF; \
	uint32_t _mw_lo = _mw_al * _mw_bl, _mw_ma = _mw_al * _mw_bh, _mw_mb = _mw_ah * _mw_bl, _mw_hi = _mw_ah * _mw_bh; \
	_mw_ma += _mw_mb; _mw_hi += ((uint32_t)(_mw_ma < _mw_mb) << 16) + (_mw_ma >> 16); _mw_ma <<= 16; _mw_lo += _mw_ma; _mw_hi += _mw_lo < _mw_ma; \
	(z0) = _mw_hi; (z1) = _mw_lo; } while (0)

static inline ogo_f64 ogo_f64_pack(uint32_t sign, int exp, uint32_t s0, uint32_t s1) {
	return ogo_f64_w((sign << 31) + ((uint32_t)exp << 20) + s0, s1);
}

static ogo_f64 ogo_f64_round(uint32_t sign, int exp, uint32_t s0, uint32_t s1, uint32_t s2) {
	uint32_t z0 = s0, z1 = s1, z2 = s2;
	int ze = exp, up = (s2 & 0x80000000u) != 0;
	if ((unsigned)ze >= 0x7FD) {
		if (ze > 0x7FD || (ze == 0x7FD && z0 == 0x001FFFFF && z1 == 0xFFFFFFFFu && up)) {
			return ogo_f64_pack(sign, 0x7FF, 0, 0);
		}
		if (ze < 0) {
			OGO_SHR96J(z0, z1, z2, z0, z1, z2, -ze);
			ze = 0;
			up = (z2 & 0x80000000u) != 0;
		}
	}
	if (up) {
		OGO_ADD64(z0, z1, z0, z1, 0, 1);
		if ((z2 << 1) == 0) { z1 &= ~1u; }
	} else if ((z0 | z1) == 0) {
		ze = 0;
	}
	return ogo_f64_pack(sign, ze, z0, z1);
}

static ogo_f64 ogo_f64_normround(uint32_t sign, int exp, uint32_t s0, uint32_t s1) {
	uint32_t z0 = s0, z1 = s1, z2 = 0;
	int ze = exp, c;
	if (z0 == 0) { z0 = z1; z1 = 0; ze -= 32; }
	c = ogo_clz32(z0) - 11;
	if (c >= 0) {
		OGO_SHL64(z0, z1, z0, z1, c);
	} else {
		OGO_SHR96J(z0, z1, z2, z0, z1, 0, -c);
	}
	return ogo_f64_round(sign, ze - c, z0, z1, z2);
}

// A subnormal's significand a0:a1 shifted to put its leading bit at bit 52, and
// the exponent e that makes up for it.
#define OGO_F64_NORM(e, a0, a1) do { \
	uint32_t _nm_a0 = (a0), _nm_a1 = (a1); int _nm_c; \
	if (_nm_a0 == 0) { _nm_c = ogo_clz32(_nm_a1) - 11; if (_nm_c < 0) { (a0) = _nm_a1 >> -_nm_c; (a1) = _nm_a1 << (_nm_c & 31); } else { (a0) = _nm_a1 << _nm_c; (a1) = 0; } (e) = -_nm_c - 31; } \
	else { _nm_c = ogo_clz32(_nm_a0) - 11; OGO_SHL64(a0, a1, _nm_a0, _nm_a1, _nm_c); (e) = 1 - _nm_c; } } while (0)

#define OGO_F64_EXP(x) ((int)((x).hi >> 20 & 0x7FF))
#define OGO_F64_F0(x) ((x).hi & 0x000FFFFF)
#define OGO_F64_ISNAN(x) (OGO_F64_EXP(x) == 0x7FF && (OGO_F64_F0(x) | (x).lo) != 0)

static ogo_f64 ogo_f64_qnan(ogo_f64 a, ogo_f64 b) {
	if (OGO_F64_ISNAN(a)) { return ogo_f64_w(a.hi | 0x00080000, a.lo); }
	return ogo_f64_w(b.hi | 0x00080000, b.lo);
}

static ogo_f64 ogo_f64_defnan(void) { return ogo_f64_w(0xFFF80000u, 0); }

static ogo_f64 ogo_f64_addmags(ogo_f64 a, ogo_f64 b, uint32_t sign) {
	uint32_t a0 = OGO_F64_F0(a), a1 = a.lo, b0 = OGO_F64_F0(b), b1 = b.lo, z0, z1, z2 = 0;
	int ea = OGO_F64_EXP(a), eb = OGO_F64_EXP(b), d = ea - eb, ez;
	if (d == 0) {
		if (ea == 0x7FF) { if ((a0 | a1 | b0 | b1) != 0) { return ogo_f64_qnan(a, b); } return a; }
		OGO_ADD64(z0, z1, a0, a1, b0, b1);
		if (ea == 0) { return ogo_f64_pack(sign, 0, z0, z1); }
		OGO_SHR96J(z0, z1, z2, z0 | 0x00200000, z1, 0, 1);
		return ogo_f64_round(sign, ea, z0, z1, z2);
	}
	if (d > 0) {
		if (ea == 0x7FF) { if ((a0 | a1) != 0) { return ogo_f64_qnan(a, b); } return a; }
		if (eb == 0) { d--; } else { b0 |= 0x00100000; }
		OGO_SHR96J(b0, b1, z2, b0, b1, 0, d);
		a0 |= 0x00100000;
		ez = ea;
	} else {
		if (eb == 0x7FF) { if ((b0 | b1) != 0) { return ogo_f64_qnan(a, b); } return ogo_f64_pack(sign, 0x7FF, 0, 0); }
		if (ea == 0) { d++; } else { a0 |= 0x00100000; }
		OGO_SHR96J(a0, a1, z2, a0, a1, 0, -d);
		b0 |= 0x00100000;
		ez = eb;
	}
	OGO_ADD64(z0, z1, a0, a1, b0, b1);
	if (z0 >= 0x00200000) {
		OGO_SHR96J(z0, z1, z2, z0, z1, z2, 1);
		return ogo_f64_round(sign, ez, z0, z1, z2);
	}
	return ogo_f64_round(sign, ez - 1, z0, z1, z2);
}

static ogo_f64 ogo_f64_submags(ogo_f64 a, ogo_f64 b, uint32_t sign) {
	uint32_t a0, a1, b0, b1, z0 = 0, z1 = 0, t0, t1, zs = sign; // z set on every path that reads it, which the target's compiler cannot see
	int ea = OGO_F64_EXP(a), eb = OGO_F64_EXP(b), d = ea - eb, ez;
	OGO_SHL64(a0, a1, OGO_F64_F0(a), a.lo, 10);
	OGO_SHL64(b0, b1, OGO_F64_F0(b), b.lo, 10);
	if (d > 0) {
		if (ea == 0x7FF) { if ((a0 | a1) != 0) { return ogo_f64_qnan(a, b); } return a; }
		if (eb == 0) { d--; } else { b0 |= 0x40000000; }
		OGO_SHR64J(t0, t1, b0, b1, d);
		OGO_SUB64(z0, z1, a0 | 0x40000000, a1, t0, t1);
		ez = ea;
	} else if (d < 0) {
		if (eb == 0x7FF) { if ((b0 | b1) != 0) { return ogo_f64_qnan(a, b); } return ogo_f64_pack(zs ^ 1, 0x7FF, 0, 0); }
		if (ea == 0) { d++; } else { a0 |= 0x40000000; }
		OGO_SHR64J(t0, t1, a0, a1, -d);
		OGO_SUB64(z0, z1, b0 | 0x40000000, b1, t0, t1);
		ez = eb;
		zs ^= 1;
	} else {
		if (ea == 0x7FF) { if ((a0 | a1 | b0 | b1) != 0) { return ogo_f64_qnan(a, b); } return ogo_f64_defnan(); }
		ez = ea == 0 ? 1 : ea;
		if (b0 < a0 || (b0 == a0 && b1 < a1)) {
			OGO_SUB64(z0, z1, a0, a1, b0, b1);
		} else if (a0 < b0 || a1 < b1) {
			OGO_SUB64(z0, z1, b0, b1, a0, a1);
			zs ^= 1;
		} else {
			return ogo_f64_w(0, 0);
		}
	}
	return ogo_f64_normround(zs, ez - 11, z0, z1);
}

static ogo_f64 ogo_f64_add(ogo_f64 a, ogo_f64 b) {
	uint32_t sa = a.hi >> 31;
	if (sa == b.hi >> 31) { return ogo_f64_addmags(a, b, sa); } return ogo_f64_submags(a, b, sa);
}

static ogo_f64 ogo_f64_sub(ogo_f64 a, ogo_f64 b) {
	uint32_t sa = a.hi >> 31;
	if (sa == b.hi >> 31) { return ogo_f64_submags(a, b, sa); } return ogo_f64_addmags(a, b, sa);
}

static ogo_f64 ogo_f64_mul(ogo_f64 a, ogo_f64 b) {
	uint32_t a0 = OGO_F64_F0(a), a1 = a.lo, b0 = OGO_F64_F0(b), b1 = b.lo, sign = (a.hi ^ b.hi) >> 31;
	uint32_t z0, z1, z2, c, t0, t1, p0, p1, q0, q1, r0, r1, s0, s1;
	int ea = OGO_F64_EXP(a), eb = OGO_F64_EXP(b), ez;
	if (ea == 0x7FF) {
		if ((a0 | a1) != 0 || (eb == 0x7FF && (b0 | b1) != 0)) { return ogo_f64_qnan(a, b); }
		if (((uint32_t)eb | b0 | b1) != 0) { return ogo_f64_pack(sign, 0x7FF, 0, 0); } return ogo_f64_defnan();
	}
	if (eb == 0x7FF) {
		if ((b0 | b1) != 0) { return ogo_f64_qnan(a, b); }
		if (((uint32_t)ea | a0 | a1) != 0) { return ogo_f64_pack(sign, 0x7FF, 0, 0); } return ogo_f64_defnan();
	}
	if (ea == 0) {
		if ((a0 | a1) == 0) { return ogo_f64_pack(sign, 0, 0, 0); }
		OGO_F64_NORM(ea, a0, a1);
	}
	if (eb == 0) {
		if ((b0 | b1) == 0) { return ogo_f64_pack(sign, 0, 0, 0); }
		OGO_F64_NORM(eb, b0, b1);
	}
	ez = ea + eb - 0x400;
	a0 |= 0x00100000;
	OGO_SHL64(b0, b1, b0, b1, 12);
	// a0:a1 times b0:b1, the high three words of 128, the fourth folded in.
	OGO_MUL32W(s0, s1, a1, b1);
	OGO_MUL32W(p0, p1, a1, b0);
	OGO_MUL32W(q0, q1, a0, b1);
	OGO_MUL32W(r0, r1, a0, b0);
	OGO_ADD64(p0, p1, p0, p1, 0, s0);
	OGO_ADD64(t0, t1, p0, p1, q0, q1);
	c = t0 < p0 || (t0 == p0 && t1 < p1);
	OGO_ADD64(z0, z1, r0, r1, c, t0);
	OGO_ADD64(z0, z1, z0, z1, a0, a1);
	z2 = t1 | (s1 != 0);
	if (z0 >= 0x00200000) {
		OGO_SHR96J(z0, z1, z2, z0, z1, z2, 1);
		return ogo_f64_round(sign, ez + 1, z0, z1, z2);
	}
	return ogo_f64_round(sign, ez, z0, z1, z2);
}

static ogo_f64 ogo_f64_div(ogo_f64 a, ogo_f64 b) {
	uint32_t a0 = OGO_F64_F0(a), a1 = a.lo, b0 = OGO_F64_F0(b), b1 = b.lo, sign = (a.hi ^ b.hi) >> 31;
	uint32_t q0 = 0, q1 = 0;
	int ea = OGO_F64_EXP(a), eb = OGO_F64_EXP(b), ez, i;
	if (ea == 0x7FF) {
		if ((a0 | a1) != 0) { return ogo_f64_qnan(a, b); }
		if (eb == 0x7FF) { if ((b0 | b1) != 0) { return ogo_f64_qnan(a, b); } return ogo_f64_defnan(); }
		return ogo_f64_pack(sign, 0x7FF, 0, 0);
	}
	if (eb == 0x7FF) { if ((b0 | b1) != 0) { return ogo_f64_qnan(a, b); } return ogo_f64_pack(sign, 0, 0, 0); }
	if (eb == 0) {
		if ((b0 | b1) == 0) { if (((uint32_t)ea | a0 | a1) != 0) { return ogo_f64_pack(sign, 0x7FF, 0, 0); } return ogo_f64_defnan(); }
		OGO_F64_NORM(eb, b0, b1);
	}
	if (ea == 0) {
		if ((a0 | a1) == 0) { return ogo_f64_pack(sign, 0, 0, 0); }
		OGO_F64_NORM(ea, a0, a1);
	}
	ez = ea - eb + 0x3FE;
	a0 |= 0x00100000;
	b0 |= 0x00100000;
	if (!OGO_GE64(a0, a1, b0, b1)) { ez--; OGO_SHL64(a0, a1, a0, a1, 1); }
	// 53 quotient bits and the round bit, the remainder the sticky ones.
	for (i = 0; i < 54; i++) {
		q0 = q0 << 1 | q1 >> 31;
		q1 <<= 1;
		if (OGO_GE64(a0, a1, b0, b1)) { OGO_SUB64(a0, a1, a0, a1, b0, b1); q1 |= 1; }
		OGO_SHL64(a0, a1, a0, a1, 1);
	}
	return ogo_f64_round(sign, ez, q0 >> 1, q1 >> 1 | q0 << 31, q1 << 31 | ((a0 | a1) != 0));
}

static ogo_f64 ogo_f64_sqrt(ogo_f64 a) {
	uint32_t a0 = OGO_F64_F0(a), a1 = a.lo, r0 = 0, r1 = 0, m0 = 0, m1 = 0, t0, t1, pair;
	int ea = OGO_F64_EXP(a), e, i, sh;
	if (ea == 0x7FF) {
		if ((a0 | a1) != 0) { return ogo_f64_qnan(a, a); }
		if ((a.hi >> 31) != 0) { return ogo_f64_defnan(); } return a;
	}
	if ((a.hi >> 31) != 0) { if (((uint32_t)ea | a0 | a1) != 0) { return ogo_f64_defnan(); } return a; }
	if (ea == 0) {
		if ((a0 | a1) == 0) { return a; }
		OGO_F64_NORM(ea, a0, a1);
	}
	a0 |= 0x00100000;
	e = ea - 1075;
	if (e & 1) { OGO_SHL64(a0, a1, a0, a1, 1); e--; }
	// The root of a0:a1 * 2^56, two bits of the radicand a step: 55 root bits.
	for (i = 0; i < 55; i++) {
		sh = 52 - 2 * i;
		if (sh >= 32) { pair = a0 >> (sh - 32) & 3; } else if (sh > 0) { pair = (a0 << (32 - sh) | a1 >> sh) & 3; } else if (sh == 0) { pair = a1 & 3; } else { pair = 0; }
		OGO_SHL64(m0, m1, m0, m1, 2);
		m1 |= pair;
		OGO_SHL64(t0, t1, r0, r1, 2);
		t1 |= 1;
		OGO_SHL64(r0, r1, r0, r1, 1);
		if (OGO_GE64(m0, m1, t0, t1)) { OGO_SUB64(m0, m1, m0, m1, t0, t1); r1 |= 1; }
	}
	// 55 bits: the 53 kept, the round bit and one more, the remainder sticky.
	return ogo_f64_round(0, e / 2 + 1048, r0 >> 2, r1 >> 2 | r0 << 30, (r1 & 3) << 30 | ((m0 | m1) != 0));
}

static ogo_f64 ogo_f64_neg(ogo_f64 x) { return ogo_f64_w(x.hi ^ 0x80000000u, x.lo); }

static int ogo_f64_eq(ogo_f64 a, ogo_f64 b) {
	if (OGO_F64_ISNAN(a) || OGO_F64_ISNAN(b)) { return 0; }
	return (a.lo == b.lo && a.hi == b.hi) || ((a.hi | b.hi) << 1 | a.lo | b.lo) == 0;
}

static int ogo_f64_lt(ogo_f64 a, ogo_f64 b) {
	uint32_t sa = a.hi >> 31, sb = b.hi >> 31;
	int below;
	if (OGO_F64_ISNAN(a) || OGO_F64_ISNAN(b)) { return 0; }
	if (sa != sb) { return sa != 0 && ((a.hi | b.hi) << 1 | a.lo | b.lo) != 0; }
	if (a.hi == b.hi && a.lo == b.lo) { return 0; }
	below = a.hi < b.hi || (a.hi == b.hi && a.lo < b.lo);
	return (int)sa ^ below;
}

static int ogo_f64_le(ogo_f64 a, ogo_f64 b) {
	uint32_t sa = a.hi >> 31, sb = b.hi >> 31;
	int below;
	if (OGO_F64_ISNAN(a) || OGO_F64_ISNAN(b)) { return 0; }
	if (sa != sb) { return sa != 0 || ((a.hi | b.hi) << 1 | a.lo | b.lo) == 0; }
	if (a.hi == b.hi && a.lo == b.lo) { return 1; }
	below = a.hi < b.hi || (a.hi == b.hi && a.lo < b.lo);
	return (int)sa ^ below;
}

// A 64-bit integer as its two words: a union, which the target reads as memory.
typedef union { int64_t i; uint64_t u; uint32_t w[2]; } ogo_w64;

static ogo_f64 ogo_f64_from_i32(int32_t v) {
	uint32_t s = v < 0, m = s ? 0u - (uint32_t)v : (uint32_t)v;
	if (m == 0) { return ogo_f64_w(0, 0); }
	return ogo_f64_normround(s, 0x432, 0, m);
}

static ogo_f64 ogo_f64_from_u32(uint32_t v) {
	if (v == 0) { return ogo_f64_w(0, 0); }
	return ogo_f64_normround(0, 0x432, 0, v);
}

static ogo_f64 ogo_f64_from_i64(int64_t v) {
	ogo_w64 x;
	uint32_t s, h, l;
	x.i = v;
	h = x.w[1];
	l = x.w[0];
	s = h >> 31;
	if (s) { l = 0u - l; h = ~h + (l == 0); }
	if ((h | l) == 0) { return ogo_f64_w(0, 0); }
	return ogo_f64_normround(s, 0x432, h, l);
}

static ogo_f64 ogo_f64_from_u64(uint64_t v) {
	ogo_w64 x;
	x.u = v;
	if ((x.w[1] | x.w[0]) == 0) { return ogo_f64_w(0, 0); }
	return ogo_f64_normround(0, 0x432, x.w[1], x.w[0]);
}

// Truncated toward zero, as Go converts, in words; out of range is Go's to leave
// undefined: the most negative int64 here, as gc's on 386.
static ogo_w2 ogo_f64_trunc_w(ogo_f64 a, int bits) {
	ogo_w2 z;
	uint32_t s0 = OGO_F64_F0(a) | 0x00100000, s1 = a.lo;
	int e = OGO_F64_EXP(a) - 1075;
	z.w0 = 0; z.w1 = 0;
	if (OGO_F64_EXP(a) == 0 || e <= -53) { return z; }
	if (e >= bits - 53) { z.w0 = 0x80000000u; return z; }
	if (e >= 0) {
		OGO_SHL64(s0, s1, s0, s1, e);
	} else if (-e >= 32) {
		s1 = s0 >> (-e - 32); s0 = 0;
	} else {
		s1 = s1 >> -e | s0 << (32 + e); s0 >>= -e;
	}
	if ((a.hi >> 31) != 0) { s1 = 0u - s1; s0 = ~s0 + (s1 == 0); }
	z.w0 = s0; z.w1 = s1;
	return z;
}

static int64_t ogo_f64_to_i64(ogo_f64 a) {
	ogo_w64 x;
	ogo_w2 t = ogo_f64_trunc_w(a, 64);
	x.w[1] = t.w0;
	x.w[0] = t.w1;
	return x.i;
}

static uint64_t ogo_f64_to_u64(ogo_f64 a) {
	ogo_w64 x;
	ogo_w2 t = ogo_f64_trunc_w(a, 65);
	x.w[1] = t.w0;
	x.w[0] = t.w1;
	return x.u;
}

// A word of the pair a call returns is read from the pair bound first: read off the
// call, the second was a register nothing wrote (doc/member-of-struct-result.c), and
// int(t) of a float64 parameter t was t's high word on the board.
static int32_t ogo_f64_to_i32(ogo_f64 a) { ogo_w2 t = ogo_f64_trunc_w(a, 64); return (int32_t)t.w1; }

static uint32_t ogo_f64_to_u32(ogo_f64 a) { ogo_w2 t = ogo_f64_trunc_w(a, 65); return t.w1; }

static ogo_f64 ogo_f64_from_f32(float f) {
	union { float f; uint32_t u; } b;
	uint32_t s, fr;
	int e;
	b.f = f;
	s = b.u >> 31;
	e = (int)(b.u >> 23 & 0xFF);
	fr = b.u & 0x7FFFFF;
	if (e == 0xFF) { if (fr != 0) { return ogo_f64_w(s << 31 | 0x7FF80000u | fr >> 3, fr << 29); } return ogo_f64_pack(s, 0x7FF, 0, 0); }
	if (e == 0) {
		if (fr == 0) { return ogo_f64_w(s << 31, 0); }
		while ((fr & 0x800000) == 0) { fr <<= 1; e--; }
		fr &= 0x7FFFFF;
		e++;
	}
	return ogo_f64_pack(s, e + 0x380, fr >> 3, fr << 29);
}

static float ogo_f64_to_f32(ogo_f64 a) {
	union { float f; uint32_t u; } r;
	uint32_t f0 = OGO_F64_F0(a), f1 = a.lo, s = a.hi >> 31, sig, bits;
	int e = OGO_F64_EXP(a);
	if (e == 0x7FF) { r.u = (f0 | f1) != 0 ? (s << 31 | 0x7FC00000u | (f0 << 3 | f1 >> 29)) : (s << 31 | 0x7F800000u); return r.f; }
	sig = (f0 << 10 | f1 >> 22) | ((f1 & 0x3FFFFF) != 0);
	if (((uint32_t)e | sig) == 0) { r.u = s << 31; return r.f; }
	sig |= 0x40000000u;
	e -= 0x381;
	bits = sig & 0x7F;
	if ((unsigned)e >= 0xFD) {
		if (e < 0) {
			sig = (-e < 31) ? (sig >> -e | ((sig << (32 + e)) != 0)) : (sig != 0);
			e = 0;
			bits = sig & 0x7F;
		} else if (e > 0xFD || sig + 0x40 >= 0x80000000u) {
			r.u = s << 31 | 0x7F800000u;
			return r.f;
		}
	}
	sig = (sig + 0x40) >> 7;
	if (bits == 0x40) { sig &= ~1u; }
	if (sig == 0) { e = 0; }
	r.u = (s << 31) + ((uint32_t)e << 23) + sig;
	return r.f;
}
`

// f64ConversionC is a conversion to or from a float64, `float64(i)`, `int64(f)`,
// `float32(f)`, through the helper converting between the two representations: a
// float64 is no C type a cast converts. A constant converted to a float64 is the
// float64 Go converts it to. A conversion between two float64 types -- a defined
// one, `Celsius(f)` -- is the value itself, every such type being the one C struct.
// false when neither side is a float64.
func (e *emitter) f64ConversionC(ct string, arg Node) (string, bool) {
	dst := e.underlyingCType(ct)
	src, srcOK := e.exprReprCType(arg.ast)
	if srcOK {
		src = e.underlyingCType(src)
	}
	if dst != cF64 && (!srcOK || src != cF64) {
		return "", false
	}
	if dst == cF64 {
		if c, ok := e.f64ConstOf(arg.ast); ok {
			return c, true
		}
	} else if v, isConst := e.foldConstVal(arg.ast); isConst {
		// A constant of a float64 type to a float32 is the float32 Go converts it
		// to, a literal of C's float, which every float32 type is; to an integer it
		// folds as any does.
		if lit, ok := floatSpelling(v, "float"); ok && dst == "float" {
			return e.parenNegative(lit), true
		}
		iv := constant.ToInt(v)
		if _, isInt := cIntWidths[dst]; !isInt || iv.Kind() != constant.Int {
			return "", false
		}
		x, exact := constant.Int64Val(iv)
		if !exact {
			u, _ := constant.Uint64Val(iv)
			x = int64(u)
		}
		lit := e.constSpelling(x, dst)
		if cIntWidths[dst] != 64 && e.litDepth == 0 && !e.declInit {
			lit = "(" + ct + ")" + lit
		}
		return lit, true
	}
	text := e.captureC(func() { e.emitExpr(arg.ast) })
	// A compound literal inside a conversion's helper and cast is one the target's C
	// compiler cannot do (emitConversion): the operand is bound first.
	if strings.Contains(text, "){") && srcOK && src != "" {
		text = e.hoist(src, func() { e.emit(text) })
	}
	switch {
	case dst == cF64 && src == cF64:
		return text, true
	case dst == cF64 && src == "float":
		return "ogo_f64_from_f32(" + text + ")", true
	case dst == cF64:
		w, isInt := cIntWidths[src]
		if !isInt {
			return "", false
		}
		switch {
		case w == 64 && isUnsignedCType(src):
			return "ogo_f64_from_u64(" + text + ")", true
		case w == 64:
			return "ogo_f64_from_i64(" + text + ")", true
		case isUnsignedCType(src):
			return "ogo_f64_from_u32((uint32_t)(" + text + "))", true
		default:
			return "ogo_f64_from_i32((int32_t)(" + text + "))", true
		}
	case dst == "float":
		if ct != dst {
			return "(" + ct + ")ogo_f64_to_f32(" + text + ")", true
		}
		return "ogo_f64_to_f32(" + text + ")", true
	}
	w, isInt := cIntWidths[dst]
	if !isInt {
		return "", false
	}
	fn := "ogo_f64_to_i32"
	switch {
	case w == 64 && isUnsignedCType(dst):
		fn = "ogo_f64_to_u64"
	case w == 64:
		fn = "ogo_f64_to_i64"
	case isUnsignedCType(dst):
		fn = "ogo_f64_to_u32"
	}
	if w == 64 {
		// The helper's result is of the type already, and a cast to a 64-bit type
		// of a 64-bit expression is what the target's C compiler miscompiles
		// (emitConversion): `(int64_t)ogo_f64_to_i64(f)` had a high word nothing
		// wrote, on the board.
		return fn + "(" + text + ")", true
	}
	return "(" + ct + ")" + fn + "(" + text + ")", true
}

// f64CompoundC is a compound assignment or an increment of a float64, `x += y`,
// `x++`, as the store of the operation's result: `x = ogo_f64_add(x, y)`. The target
// is read and written, so one whose evaluation does something, or holds a nil
// check, has its address named once first, as guardedAssignC names it.
func (e *emitter) f64CompoundC(target func(), t assignTail) string {
	text := e.captureC(target)
	if !t.targetRepeatable && !plainTargetText(text) {
		text = "*" + e.hoist(cF64+"*", func() { e.emit("&" + text) })
	}
	var fn, rhs string
	switch t.op {
	case "++", "--":
		fn, rhs = "ogo_f64_add", e.f64ConstC(1)
		if t.op == "--" {
			fn = "ogo_f64_sub"
		}
	default:
		var ok bool
		if fn, ok = f64Ops[strings.TrimSuffix(t.op, "=")]; !ok {
			e.failAt(t.rhs, "operator %s is not defined on float64", t.op)
			return ""
		}
		rhs = e.f64OperandC(Node{sym: Expression, ast: t.rhs})
	}
	return text + " = " + fn + "(" + text + ", " + rhs + ")"
}

// floatPrintFn names the helper printing a float of the C type ct, and asks for it:
// ogo_print_float for a float32, ogo_print_f64 for a float64 -- the same flags, the
// same layouts.
func (e *emitter) floatPrintFn(ct string) string {
	e.includes["string.h"] = true
	if e.underlyingCType(ct) == cF64 {
		e.usesF64Fmt = true
		return "ogo_print_f64"
	}
	e.usesFloatFmt = true
	return "ogo_print_float"
}

// f64MinMaxC is the two-argument min (or max) of float64s a call folds over, Go's:
// NaN if either argument is one, the negative zero below the positive. Every choice
// is an if: a value of two words chosen by a conditional expression is what the
// target's C compiler gets wrong (f64Runtime).
func f64MinMaxC(name string, isMin bool) string {
	zero, cmp := "b", "ogo_f64_lt(a, b)"
	if !isMin {
		zero, cmp = "a", "ogo_f64_lt(b, a)"
	}
	return "static ogo_f64 " + name + "(ogo_f64 a, ogo_f64 b) {\n" +
		"\tif (OGO_F64_ISNAN(a)) { return a; }\n" +
		"\tif (OGO_F64_ISNAN(b)) { return b; }\n" +
		"\tif (((a.hi | b.hi) << 1 | a.lo | b.lo) == 0) { if ((a.hi >> 31) != 0) { return " + map[bool]string{true: "a", false: "b"}[isMin] + "; } return " + zero + "; }\n" +
		"\tif (" + cmp + ") { return a; }\n" +
		"\treturn b;\n}\n"
}

// f64FmtHelper prints a float64 as Go prints one -- print, println, %v, %g, %e and
// %f alike -- from its exact decimal expansion: Go's strconv (decimal.go's shifts of
// up to 28 bits a step, roundShortest's walk with its upperdelta), ported for a
// mantissa of 53 bits in two words, 800 digits holding any float64. Held to Go's
// fmt on 2.2 million values and verbs on the host (2026-10-24). The padding and the
// flags are floatFmtHelper's.
const f64FmtHelper = `// A float64 printed as Go prints one, from its exact decimal expansion: Go's
// strconv (decimal.go's multi-bit shifts, ftoa.go's roundShortest and layouts),
// ported for 53-bit mantissas held in two words. 800 digits hold any float64.
typedef struct { char d[800]; int nd; int dp; int trunc; } ogo_dec64;

static void ogo_d64_trim(ogo_dec64* a) { while (a->nd > 0 && a->d[a->nd - 1] == '0') { a->nd--; } if (a->nd == 0) { a->dp = 0; } }

// The 64-bit value hi:lo, as digits.
static void ogo_d64_assign(ogo_dec64* a, uint32_t hi, uint32_t lo) {
	char buf[24];
	int n = 0;
	uint32_t h = hi, l = lo, r, t, q1, q0;
	a->nd = 0;
	a->trunc = 0;
	while (h != 0 || l != 0) {
		r = h % 10;
		h /= 10;
		t = r << 16 | l >> 16;
		q1 = t / 10;
		r = t % 10;
		t = r << 16 | (l & 0xFFFF);
		q0 = t / 10;
		r = t % 10;
		l = q1 << 16 | q0;
		buf[n++] = (char)('0' + r);
	}
	while (n > 0) { a->d[a->nd++] = buf[--n]; }
	a->dp = a->nd;
	ogo_d64_trim(a);
}

// a shifted right by k bits, k <= 28: Go's decimal rightShift.
static void ogo_d64_rshift(ogo_dec64* a, int k) {
	int r = 0, w = 0;
	uint32_t n = 0, mask = ((uint32_t)1 << k) - 1, c, dig;
	for (; (n >> k) == 0; r++) {
		if (r >= a->nd) {
			if (n == 0) { a->nd = 0; return; }
			while ((n >> k) == 0) { n = n * 10; r++; }
			break;
		}
		c = (uint32_t)(a->d[r] - '0');
		n = n * 10 + c;
	}
	a->dp -= r - 1;
	for (; r < a->nd; r++) {
		c = (uint32_t)(a->d[r] - '0');
		dig = n >> k;
		n &= mask;
		a->d[w++] = (char)(dig + '0');
		n = n * 10 + c;
	}
	while (n > 0) {
		dig = n >> k;
		n &= mask;
		if (w < 800) { a->d[w++] = (char)(dig + '0'); } else if (dig > 0) { a->trunc = 1; }
		n = n * 10;
	}
	a->nd = w;
	ogo_d64_trim(a);
}

// a shifted left by k bits, k <= 28, written right to left into a scratch row and
// copied back.
static void ogo_d64_lshift(ogo_dec64* a, int k) {
	char t[810];
	int r, j = 0, i;
	uint32_t n, carry = 0;
	for (r = a->nd - 1; r >= 0; r--) {
		n = ((uint32_t)(a->d[r] - '0') << k) + carry;
		t[j++] = (char)('0' + n % 10);
		carry = n / 10;
	}
	while (carry > 0) {
		t[j++] = (char)('0' + carry % 10);
		carry /= 10;
	}
	a->dp += j - a->nd;
	if (j > 800) { a->trunc = 1; j = 800; }
	for (i = 0; i < j; i++) { a->d[i] = t[j - 1 - i]; }
	a->nd = j;
	ogo_d64_trim(a);
}

static void ogo_d64_shift(ogo_dec64* a, int k) {
	if (a->nd == 0) { return; }
	if (k > 0) {
		while (k > 28) { ogo_d64_lshift(a, 28); k -= 28; }
		ogo_d64_lshift(a, k);
	} else if (k < 0) {
		while (k < -28) { ogo_d64_rshift(a, 28); k += 28; }
		ogo_d64_rshift(a, -k);
	}
}

static int ogo_d64_up(const ogo_dec64* a, int nd) {
	if (nd < 0 || nd >= a->nd) { return 0; }
	if (a->d[nd] == '5' && nd + 1 == a->nd) {
		if (a->trunc) { return 1; }
		return nd > 0 && (a->d[nd - 1] - '0') % 2 == 1;
	}
	return a->d[nd] >= '5';
}
static void ogo_d64_rounddown(ogo_dec64* a, int nd) { if (nd < 0 || nd >= a->nd) { return; } a->nd = nd; ogo_d64_trim(a); }
static void ogo_d64_roundup(ogo_dec64* a, int nd) {
	int i;
	if (nd < 0 || nd >= a->nd) { return; }
	for (i = nd - 1; i >= 0; i--) { if (a->d[i] < '9') { a->d[i]++; a->nd = i + 1; return; } }
	a->d[0] = '1'; a->nd = 1; a->dp++;
}
static void ogo_d64_round(ogo_dec64* a, int nd) { if (nd < 0 || nd >= a->nd) { return; } if (ogo_d64_up(a, nd)) { ogo_d64_roundup(a, nd); } else { ogo_d64_rounddown(a, nd); } }

// Go's roundShortest: the fewest digits that read back to the float64 of the
// mantissa mh:ml (53 bits) and binary exponent exp.
static void ogo_d64_shortest(ogo_dec64* d, uint32_t mh, uint32_t ml, int exp) {
	ogo_dec64 upper, lower;
	uint32_t uh, ul, lh, ll;
	int explo, inclusive, ui, mi, li, okdown, okup, upperdelta;
	char l, m, u;
	if ((mh | ml) == 0) { d->nd = 0; d->dp = 0; return; }
	if (exp > -1022 && 332 * (d->dp - d->nd) >= 100 * (exp - 52)) { return; }
	// upper = mant*2 + 1, shifted by exp - 52 - 1
	uh = mh << 1 | ml >> 31; ul = ml << 1 | 1;
	ogo_d64_assign(&upper, uh, ul);
	ogo_d64_shift(&upper, exp - 52 - 1);
	// mantlo, explo: mant - 1 at exp, or mant*2 - 1 at exp - 1 where mant is the
	// smallest normal mantissa
	if (mh > 0x00100000 || (mh == 0x00100000 && ml != 0) || exp == -1022) {
		lh = mh - (ml == 0); ll = ml - 1; explo = exp;
	} else {
		lh = mh << 1 | ml >> 31; ll = ml << 1;
		lh -= (ll == 0); ll -= 1;
		explo = exp - 1;
	}
	// lower = mantlo*2 + 1
	lh = lh << 1 | ll >> 31; ll = ll << 1 | 1;
	ogo_d64_assign(&lower, lh, ll);
	ogo_d64_shift(&lower, explo - 52 - 1);
	inclusive = (ml & 1) == 0;
	// upperdelta: 0 while d's digits and upper's agree, 1 after a difference of one
	// followed by nines and zeros only, 2 past that (Go's).
	upperdelta = 0;
	for (ui = 0; ; ui++) {
		mi = ui - upper.dp + d->dp;
		if (mi >= d->nd) { break; }
		li = ui - upper.dp + lower.dp;
		l = '0'; if (li >= 0 && li < lower.nd) { l = lower.d[li]; }
		m = '0'; if (mi >= 0) { m = d->d[mi]; }
		u = '0'; if (ui < upper.nd) { u = upper.d[ui]; }
		okdown = l != m || (inclusive && li + 1 == lower.nd);
		if (upperdelta == 0 && m + 1 < u) { upperdelta = 2; }
		else if (upperdelta == 0 && m != u) { upperdelta = 1; }
		else if (upperdelta == 1 && (m != '9' || u != '0')) { upperdelta = 2; }
		okup = upperdelta > 0 && (inclusive || upperdelta > 1 || ui + 1 < upper.nd);
		if (okdown && okup) { ogo_d64_round(d, mi + 1); return; }
		if (okdown) { ogo_d64_rounddown(d, mi + 1); return; }
		if (okup) { ogo_d64_roundup(d, mi + 1); return; }
	}
}

static char* ogo_d64_fmt_e(char* o, const ogo_dec64* d, int prec, char letter) {
	int i, m, exp;
	*o++ = d->nd != 0 ? d->d[0] : '0';
	if (prec > 0) {
		*o++ = '.';
		i = 1; m = d->nd < prec + 1 ? d->nd : prec + 1;
		for (; i < m; i++) { *o++ = d->d[i]; }
		for (; i <= prec; i++) { *o++ = '0'; }
	}
	*o++ = letter;
	exp = d->nd != 0 ? d->dp - 1 : 0;
	if (exp < 0) { *o++ = '-'; exp = -exp; } else { *o++ = '+'; }
	if (exp < 10) { *o++ = '0'; }
	o += sprintf(o, "%d", exp);
	return o;
}

static char* ogo_d64_fmt_f(char* o, const ogo_dec64* d, int prec) {
	int i, m, j;
	if (d->dp > 0) { m = d->nd < d->dp ? d->nd : d->dp; for (i = 0; i < m; i++) { *o++ = d->d[i]; } for (; m < d->dp; m++) { *o++ = '0'; } } else { *o++ = '0'; }
	if (prec > 0) { *o++ = '.'; for (i = 0; i < prec; i++) { j = d->dp + i; *o++ = (0 <= j && j < d->nd) ? d->d[j] : '0'; } }
	return o;
}

// out takes the text: 330 bytes hold %f of the largest float64 at precision 0, and
// 1100 any precision a program writes up to 760.
static void ogo_fmt_f64(char* out, ogo_f64 v, char verb, int prec) {
	ogo_dec64 d;
	uint32_t mh = v.hi & 0x000FFFFF, ml = v.lo;
	int exp = (int)(v.hi >> 20 & 0x7FF), neg = (v.hi >> 31) != 0, shortest = prec < 0, eprec, n;
	char* o = out;
	if (exp == 0x7FF) { if ((mh | ml) != 0) { strcpy(out, "NaN"); } else { strcpy(out, neg ? "-Inf" : "+Inf"); } return; }
	if (exp == 0) { exp = 1; } else { mh |= 0x00100000; }
	exp -= 1023;
	ogo_d64_assign(&d, mh, ml);
	ogo_d64_shift(&d, exp - 52);
	if (shortest) {
		ogo_d64_shortest(&d, mh, ml, exp);
		switch (verb) {
		case 'e': case 'E': prec = d.nd - 1; break;
		case 'f': prec = d.nd - d.dp; if (prec < 0) { prec = 0; } break;
		default: prec = d.nd; break;
		}
	} else {
		switch (verb) {
		case 'e': case 'E': ogo_d64_round(&d, prec + 1); break;
		case 'f': n = d.dp + prec; if (n < 0) { n = 0; } ogo_d64_round(&d, n); break;
		default: if (prec == 0) { prec = 1; } ogo_d64_round(&d, prec); break;
		}
	}
	if (neg) { *o++ = '-'; }
	switch (verb) {
	case 'e': case 'E': o = ogo_d64_fmt_e(o, &d, prec, verb); break;
	case 'f': o = ogo_d64_fmt_f(o, &d, prec); break;
	default:
		eprec = prec;
		if (eprec > d.nd && d.nd >= d.dp) { eprec = d.nd; }
		if (shortest) { eprec = 6; }
		exp = d.dp - 1;
		if (exp < -4 || exp >= eprec) { if (prec > d.nd) { prec = d.nd; } o = ogo_d64_fmt_e(o, &d, prec - 1, verb == 'G' ? 'E' : 'e'); break; }
		if (prec > d.dp) { prec = d.nd; }
		o = ogo_d64_fmt_f(o, &d, prec - d.dp > 0 ? prec - d.dp : 0);
		break;
	}
	*o = 0;
}

static void ogo_print_f64(int width, int left, int plus, int space, int zero, int sharp, char verb, int prec, ogo_f64 v) {
	char b[1100], t[1102]; const char* s = b; int n, pad, i, special;
	ogo_fmt_f64(b, v, verb, prec);
	special = b[0] == 'N' || b[1] == 'I';
	if (sharp && !special) {
		int digits = (verb == 'g' || verb == 'G') ? (prec < 0 ? 6 : prec) : 0, i0 = b[0] == '-', len = (int)strlen(b), dp = 0, nz = 0, tl = 0;
		char tail[8];
		for (i = i0; i < len; i++) {
			if (b[i] == '.') { dp = 1; continue; }
			if (b[i] == 'e' || b[i] == 'E') { tl = len - i; memcpy(tail, b + i, tl); len = i; break; }
			if (b[i] != '0') { nz = 1; }
			if (nz) { digits--; }
		}
		if (!dp) { if (len - i0 == 1 && b[i0] == '0') { digits--; } b[len++] = '.'; }
		for (; digits > 0; digits--) { b[len++] = '0'; }
		memcpy(b + len, tail, tl);
		b[len + tl] = 0;
	}
	if (b[0] == '+' && space && !plus) { b[0] = ' '; }
	if (b[0] != '-' && b[0] != '+' && b[0] != ' ' && (plus || space)) { t[0] = plus ? '+' : ' '; strcpy(t + 1, b); s = t; }
	n = (int)strlen(s); pad = width - n;
	if (pad <= 0) { printf("%s", s); return; }
	if (left) { printf("%s", s); for (i = 0; i < pad; i++) { putchar(' '); } return; }
	if (zero && !special) { if (s[0] == '-' || s[0] == '+' || s[0] == ' ') { putchar(s[0]); s++; } for (i = 0; i < pad; i++) { putchar('0'); } printf("%s", s); return; }
	for (i = 0; i < pad; i++) { putchar(' '); }
	printf("%s", s);
}
`
