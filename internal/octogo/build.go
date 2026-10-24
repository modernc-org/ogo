// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"fmt"
	"go/token"
	"io/fs"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing/fstest"
)

var (
	noPkg = &Package{Scope: newScope(Universe, PackageScope)}
)

// intrinsicImports are the compiler-known packages that have no .ogo source
// directory: their symbols are provided by the emitter (p2's hardware intrinsics),
// so an import of one resolving to noPkg is expected, not a missing-package error.
var intrinsicImports = map[string]bool{"p2": true, "unsafe": true}

// embeddedPkgs are packages whose source the compiler carries rather than reads
// from a directory. They are ORDINARY OctoGo, compiled and mangled like any other
// package -- nothing about them is intrinsic -- so the day one of them ships as
// source on disk, the only change is where it is read from.
var embeddedPkgs = map[string]string{"testing": testingSrc, "p2": p2Src, "strings": stringsSrc, "bytes": bytesSrc, "math": mathSrc, "unsafe": unsafeSrc, "errors": errorsSrc}

// unsafeSrc is the unsafe package. Its one name, Pointer, is no declaration a source
// can write -- a pointer of no type, which every pointer converts to and from, and
// uintptr too -- so it is the Kind PredeclaredUnsafePointer, put into the package's
// scope by NewPackage (unsafePointerDecl) as the universe's names are put into the
// universe. Nothing of it is emitted: it is intrinsic, and the emitter spells the
// type and its conversions itself.
const unsafeSrc = `// Package unsafe holds Pointer, the pointer of no type through which a program
// reads one type's storage as another's and writes an address as a number.
//
// A pointer of any type converts to a Pointer and back, and a Pointer converts to
// and from uintptr. A Pointer is compared and assigned like any pointer, and is
// neither dereferenced nor computed with: an address is computed as a uintptr and
// converted back. The lifetime rules follow a Pointer as they follow the pointer it
// was converted from, and a number carries nothing they can follow -- so the
// address of storage of a frame is refused as a uintptr, and a function turning a
// pointer parameter into one is taken to keep what it is handed.
//
// Sizeof, Alignof, Offsetof, Add, Slice, String, StringData and SliceData are not
// provided yet.
`

// unsafePointerDecl is unsafe.Pointer's declaration, its token from a source of its
// own as the universe's names' are from builtin.ogo.
var unsafePointerDecl = &PredeclaredType{
	declaration: declaration{token: scanNames("unsafe.ogo", "Pointer")["Pointer"]},
	kinder:      kinder(PredeclaredUnsafePointer),
}

// mathSrc is the math package: Go's own pure Go source over the float64 the
// arithmetic computes in software (float64.go), but for the functions without a
// body, each substituted at the call site by the C it is (mathIntrinsics in
// emit.go): the square root, an operation of the arithmetic, and the four that read
// and write a float's bits.
const mathSrc = `// Package math is the allocation-free part of Go's math: the elementary functions,
// each meaning exactly what Go's of the same name means, computed as Go's are.
//
// A float64 is IEEE 754 binary64 here, in software, and so the functions are Go's
// own: its pure Go implementations, not the target's C library, whose double is 32
// bits. Copyright 2009 The Go Authors; their BSD-style license is the LICENSE file
// of the Go distribution. Only what Go writes in assembly for a 386, Floor, Ceil,
// Trunc and Hypot, is its pure Go form here, which answers the same; Sqrt is the exact
// operation of the arithmetic, and the four functions reading and writing a float's
// bits are the representation's.

// Sqrt returns the square root of x, correctly rounded.
func Sqrt(x float64) float64

// Float64bits returns the IEEE 754 binary representation of f, Float64frombits the
// floating-point number of the representation b.
func Float64bits(f float64) uint64

func Float64frombits(b uint64) float64

// Float32bits and Float32frombits are the same for a float32.
func Float32bits(f float32) uint32

func Float32frombits(b uint32) float32

// mul64, add64, sub64 and leadingZeros64 are math/bits's Mul64, Add64, Sub64 and
// LeadingZeros64, which trigReduce and FMA read.
func sub64(x, y, borrow uint64) (diff, borrowOut uint64) {
	diff = x - y - borrow
	borrowOut = ((^x & y) | (^(x ^ y) & diff)) >> 63
	return
}

func mul64(x, y uint64) (hi, lo uint64) {
	const mask32 = 1<<32 - 1
	x0 := x & mask32
	x1 := x >> 32
	y0 := y & mask32
	y1 := y >> 32
	w0 := x0 * y0
	t := x1*y0 + w0>>32
	w1 := t & mask32
	w2 := t >> 32
	w1 += x0 * y1
	hi = x1*y1 + w2 + w1>>32
	lo = x * y
	return
}

func add64(x, y, carry uint64) (sum, carryOut uint64) {
	sum = x + y + carry
	carryOut = ((x & y) | ((x | y) &^ sum)) >> 63
	return
}

func leadingZeros64(x uint64) int {
	if x == 0 {
		return 64
	}
	n := 0
	for x&(1<<63) == 0 {
		x <<= 1
		n++
	}
	return n
}

// From Go's math/const.go.
// Package math provides basic constants and mathematical functions.
//
// This package does not guarantee bit-identical results across architectures.

// Mathematical constants.
const (
	E   = 2.71828182845904523536028747135266249775724709369995957496696763 // https://oeis.org/A001113
	Pi  = 3.14159265358979323846264338327950288419716939937510582097494459 // https://oeis.org/A000796
	Phi = 1.61803398874989484820458683436563811772030917980576286213544862 // https://oeis.org/A001622

	Sqrt2   = 1.41421356237309504880168872420969807856967187537694807317667974 // https://oeis.org/A002193
	SqrtE   = 1.64872127070012814684865078781416357165377610071014801157507931 // https://oeis.org/A019774
	SqrtPi  = 1.77245385090551602729816748334114518279754945612238712821380779 // https://oeis.org/A002161
	SqrtPhi = 1.27201964951406896425242246173749149171560804184009624861664038 // https://oeis.org/A139339

	Ln2    = 0.693147180559945309417232121458176568075500134360255254120680009 // https://oeis.org/A002162
	Log2E  = 1 / Ln2
	Ln10   = 2.30258509299404568401799145468436420760110148862877297603332790 // https://oeis.org/A002392
	Log10E = 1 / Ln10
)

// Floating-point limit values.
// Max is the largest finite value representable by the type.
// SmallestNonzero is the smallest positive, non-zero value representable by the type.
const (
	MaxFloat32             = 0x1p127 * (1 + (1 - 0x1p-23)) // 3.40282346638528859811704183484516925440e+38
	SmallestNonzeroFloat32 = 0x1p-126 * 0x1p-23            // 1.401298464324817070923729583289916131280e-45

	MaxFloat64             = 0x1p1023 * (1 + (1 - 0x1p-52)) // 1.79769313486231570814527423731704356798070e+308
	SmallestNonzeroFloat64 = 0x1p-1022 * 0x1p-52            // 4.9406564584124654417656879286822137236505980e-324
)

// Integer limit values.
const (
	intSize = 32 // int and uint are 32 bits here

	MaxInt    = 1<<(intSize-1) - 1  // MaxInt32 or MaxInt64 depending on intSize.
	MinInt    = -1 << (intSize - 1) // MinInt32 or MinInt64 depending on intSize.
	MaxInt8   = 1<<7 - 1            // 127
	MinInt8   = -1 << 7             // -128
	MaxInt16  = 1<<15 - 1           // 32767
	MinInt16  = -1 << 15            // -32768
	MaxInt32  = 1<<31 - 1           // 2147483647
	MinInt32  = -1 << 31            // -2147483648
	MaxInt64  = 1<<63 - 1           // 9223372036854775807
	MinInt64  = -1 << 63            // -9223372036854775808
	MaxUint   = 1<<intSize - 1      // MaxUint32 or MaxUint64 depending on intSize.
	MaxUint8  = 1<<8 - 1            // 255
	MaxUint16 = 1<<16 - 1           // 65535
	MaxUint32 = 1<<32 - 1           // 4294967295
	MaxUint64 = 1<<64 - 1           // 18446744073709551615
)

// From Go's math/bits.go.
const (
	uvnan    = 0x7FF8000000000001
	uvinf    = 0x7FF0000000000000
	uvneginf = 0xFFF0000000000000
	uvone    = 0x3FF0000000000000
	mask     = 0x7FF
	shift    = 64 - 11 - 1
	bias     = 1023
	signMask = 1 << 63
	fracMask = 1<<shift - 1
)

// Inf returns positive infinity if sign >= 0, negative infinity if sign < 0.
func Inf(sign int) float64 {
	var v uint64
	if sign >= 0 {
		v = uvinf
	} else {
		v = uvneginf
	}
	return Float64frombits(v)
}

// NaN returns an IEEE 754 “not-a-number” value.
func NaN() float64 { return Float64frombits(uvnan) }

// IsNaN reports whether f is an IEEE 754 “not-a-number” value.
func IsNaN(f float64) (is bool) {
	// IEEE 754 says that only NaNs satisfy f != f.
	// To avoid the floating-point hardware, could use:
	//	x := Float64bits(f);
	//	return uint32(x>>shift)&mask == mask && x != uvinf && x != uvneginf
	return f != f
}

// IsInf reports whether f is an infinity, according to sign.
// If sign > 0, IsInf reports whether f is positive infinity.
// If sign < 0, IsInf reports whether f is negative infinity.
// If sign == 0, IsInf reports whether f is either infinity.
func IsInf(f float64, sign int) bool {
	// Test for infinity by comparing against maximum float.
	// To avoid the floating-point hardware, could use:
	//	x := Float64bits(f);
	//	return sign >= 0 && x == uvinf || sign <= 0 && x == uvneginf;
	if sign == 0 {
		f = Abs(f)
	} else if sign < 0 {
		f = -f
	}
	return f > MaxFloat64
}

// normalize returns a normal number y and exponent exp
// satisfying x == y × 2**exp. It assumes x is finite and non-zero.
func normalize(x float64) (y float64, exp int) {
	const SmallestNormal = 2.2250738585072014e-308 // 2**-1022
	if Abs(x) < SmallestNormal {
		return x * (1 << 52), -52
	}
	return x, 0
}

// From Go's math/abs.go.
// Abs returns the absolute value of x.
//
// Special cases are:
//
//	Abs(±Inf) = +Inf
//	Abs(NaN) = NaN
func Abs(x float64) float64 {
	return Float64frombits(Float64bits(x) &^ signMask)
}

// From Go's math/copysign.go.
// Copysign returns a value with the magnitude of f
// and the sign of sign.
func Copysign(f, sign float64) float64 {
	return Float64frombits(Float64bits(f)&^signMask | Float64bits(sign)&signMask)
}

// From Go's math/signbit.go.
// Signbit reports whether x is negative or negative zero.
func Signbit(x float64) bool {
	return int64(Float64bits(x)) < 0
}

// From Go's math/floor.go.
// Floor returns the greatest integer value less than or equal to x.
//
// Special cases are:
//
//	Floor(±0) = ±0
//	Floor(±Inf) = ±Inf
//	Floor(NaN) = NaN
func Floor(x float64) float64 {
	return floor(x)
}

func floor(x float64) float64 {
	if x == 0 || IsNaN(x) || IsInf(x, 0) {
		return x
	}
	if x < 0 {
		d, fract := Modf(-x)
		if fract != 0.0 {
			d = d + 1
		}
		return -d
	}
	d, _ := Modf(x)
	return d
}

// Ceil returns the least integer value greater than or equal to x.
//
// Special cases are:
//
//	Ceil(±0) = ±0
//	Ceil(±Inf) = ±Inf
//	Ceil(NaN) = NaN
func Ceil(x float64) float64 {
	return ceil(x)
}

func ceil(x float64) float64 {
	return -Floor(-x)
}

// Trunc returns the integer value of x.
//
// Special cases are:
//
//	Trunc(±0) = ±0
//	Trunc(±Inf) = ±Inf
//	Trunc(NaN) = NaN
func Trunc(x float64) float64 {
	return trunc(x)
}

func trunc(x float64) float64 {
	if Abs(x) < 1 {
		return Copysign(0, x)
	}

	b := Float64bits(x)
	e := uint(b>>shift)&mask - bias

	// Keep the top 12+e bits, the integer part; clear the rest.
	if e < 64-12 {
		b &^= 1<<(64-12-e) - 1
	}
	return Float64frombits(b)
}

// Round returns the nearest integer, rounding half away from zero.
//
// Special cases are:
//
//	Round(±0) = ±0
//	Round(±Inf) = ±Inf
//	Round(NaN) = NaN
func Round(x float64) float64 {
	// Round is a faster implementation of:
	//
	// func Round(x float64) float64 {
	//   t := Trunc(x)
	//   if Abs(x-t) >= 0.5 {
	//     return t + Copysign(1, x)
	//   }
	//   return t
	// }
	bits := Float64bits(x)
	e := uint(bits>>shift) & mask
	if e < bias {
		// Round abs(x) < 1 including denormals.
		bits &= signMask // +-0
		if e == bias-1 {
			bits |= uvone // +-1
		}
	} else if e < bias+shift {
		// Round any abs(x) >= 1 containing a fractional component [0,1).
		//
		// Numbers with larger exponents are returned unchanged since they
		// must be either an integer, infinity, or NaN.
		const half = 1 << (shift - 1)
		e -= bias
		bits += half >> e
		bits &^= fracMask >> e
	}
	return Float64frombits(bits)
}

// RoundToEven returns the nearest integer, rounding ties to even.
//
// Special cases are:
//
//	RoundToEven(±0) = ±0
//	RoundToEven(±Inf) = ±Inf
//	RoundToEven(NaN) = NaN
func RoundToEven(x float64) float64 {
	// RoundToEven is a faster implementation of:
	//
	// func RoundToEven(x float64) float64 {
	//   t := math.Trunc(x)
	//   odd := math.Remainder(t, 2) != 0
	//   if d := math.Abs(x - t); d > 0.5 || (d == 0.5 && odd) {
	//     return t + math.Copysign(1, x)
	//   }
	//   return t
	// }
	bits := Float64bits(x)
	e := uint(bits>>shift) & mask
	if e >= bias {
		// Round abs(x) >= 1.
		// - Large numbers without fractional components, infinity, and NaN are unchanged.
		// - Add 0.499.. or 0.5 before truncating depending on whether the truncated
		//   number is even or odd (respectively).
		const halfMinusULP = (1 << (shift - 1)) - 1
		e -= bias
		bits += (halfMinusULP + (bits>>(shift-e))&1) >> e
		bits &^= fracMask >> e
	} else if e == bias-1 && bits&fracMask != 0 {
		// Round 0.5 < abs(x) < 1.
		bits = bits&signMask | uvone // +-1
	} else {
		// Round abs(x) <= 0.5 including denormals.
		bits &= signMask // +-0
	}
	return Float64frombits(bits)
}

// From Go's math/modf.go.
// Modf returns integer and fractional floating-point numbers
// that sum to f. Both values have the same sign as f.
//
// Special cases are:
//
//	Modf(±Inf) = ±Inf, NaN
//	Modf(NaN) = NaN, NaN
func Modf(f float64) (integer float64, fractional float64) {
	integer = Trunc(f)
	fractional = Copysign(f-integer, f)
	return
}

// From Go's math/frexp.go.
// Frexp breaks f into a normalized fraction
// and an integral power of two.
// It returns frac and exp satisfying f == frac × 2**exp,
// with the absolute value of frac in the interval [½, 1).
//
// Special cases are:
//
//	Frexp(±0) = ±0, 0
//	Frexp(±Inf) = ±Inf, 0
//	Frexp(NaN) = NaN, 0
func Frexp(f float64) (frac float64, exp int) {
	return frexp(f)
}

func frexp(f float64) (frac float64, exp int) {
	// special cases
	switch {
	case f == 0:
		return f, 0 // correctly return -0
	case IsInf(f, 0) || IsNaN(f):
		return f, 0
	}
	f, exp = normalize(f)
	x := Float64bits(f)
	exp += int((x>>shift)&mask) - bias + 1
	x &^= mask << shift
	x |= (-1 + bias) << shift
	frac = Float64frombits(x)
	return
}

// From Go's math/ldexp.go.
// Ldexp is the inverse of [Frexp].
// It returns frac × 2**exp.
//
// Special cases are:
//
//	Ldexp(±0, exp) = ±0
//	Ldexp(±Inf, exp) = ±Inf
//	Ldexp(NaN, exp) = NaN
func Ldexp(frac float64, exp int) float64 {
	return ldexp(frac, exp)
}

func ldexp(frac float64, exp int) float64 {
	// special cases
	switch {
	case frac == 0:
		return frac // correctly return -0
	case IsInf(frac, 0) || IsNaN(frac):
		return frac
	}
	frac, e := normalize(frac)
	exp += e
	x := Float64bits(frac)
	exp += int(x>>shift)&mask - bias
	if exp < -1075 {
		return Copysign(0, frac) // underflow
	}
	if exp > 1023 { // overflow
		if frac < 0 {
			return Inf(-1)
		}
		return Inf(1)
	}
	var m float64 = 1
	if exp < -1022 { // denormal
		exp += 53
		m = 1.0 / (1 << 53) // 2**-53
	}
	x &^= mask << shift
	x |= uint64(exp+bias) << shift
	return m * Float64frombits(x)
}

// From Go's math/exp.go.
// Exp returns e**x, the base-e exponential of x.
//
// Special cases are:
//
//	Exp(+Inf) = +Inf
//	Exp(NaN) = NaN
//
// Very large values overflow to 0 or +Inf.
// Very small values underflow to 1.
func Exp(x float64) float64 {
	return exp(x)
}

// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/e_exp.c
// and came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 2004 by Sun Microsystems, Inc. All rights reserved.
//
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
//
// exp(x)
// Returns the exponential of x.
//
// Method
//   1. Argument reduction:
//      Reduce x to an r so that |r| <= 0.5*ln2 ~ 0.34658.
//      Given x, find r and integer k such that
//
//               x = k*ln2 + r,  |r| <= 0.5*ln2.
//
//      Here r will be represented as r = hi-lo for better
//      accuracy.
//
//   2. Approximation of exp(r) by a special rational function on
//      the interval [0,0.34658]:
//      Write
//          R(r**2) = r*(exp(r)+1)/(exp(r)-1) = 2 + r*r/6 - r**4/360 + ...
//      We use a special Remez algorithm on [0,0.34658] to generate
//      a polynomial of degree 5 to approximate R. The maximum error
//      of this polynomial approximation is bounded by 2**-59. In
//      other words,
//          R(z) ~ 2.0 + P1*z + P2*z**2 + P3*z**3 + P4*z**4 + P5*z**5
//      (where z=r*r, and the values of P1 to P5 are listed below)
//      and
//          |                  5          |     -59
//          | 2.0+P1*z+...+P5*z   -  R(z) | <= 2
//          |                             |
//      The computation of exp(r) thus becomes
//                             2*r
//              exp(r) = 1 + -------
//                            R - r
//                                 r*R1(r)
//                     = 1 + r + ----------- (for better accuracy)
//                                2 - R1(r)
//      where
//                               2       4             10
//              R1(r) = r - (P1*r  + P2*r  + ... + P5*r   ).
//
//   3. Scale back to obtain exp(x):
//      From step 1, we have
//         exp(x) = 2**k * exp(r)
//
// Special cases:
//      exp(INF) is INF, exp(NaN) is NaN;
//      exp(-INF) is 0, and
//      for finite argument, only exp(0)=1 is exact.
//
// Accuracy:
//      according to an error analysis, the error is always less than
//      1 ulp (unit in the last place).
//
// Misc. info.
//      For IEEE double
//          if x >  7.09782712893383973096e+02 then exp(x) overflow
//          if x < -7.45133219101941108420e+02 then exp(x) underflow
//
// Constants:
// The hexadecimal values are the intended ones for the following
// constants. The decimal values may be used, provided that the
// compiler will convert from decimal to binary accurately enough
// to produce the hexadecimal values shown.

func exp(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10
		Log2e = 1.44269504088896338700e+00

		Overflow  = 7.09782712893383973096e+02
		Underflow = -7.45133219101941108420e+02
		NearZero  = 1.0 / (1 << 28) // 2**-28
	)

	// special cases
	switch {
	case IsNaN(x):
		return x
	case x > Overflow: // handles case where x is +∞
		return Inf(1)
	case x < Underflow: // handles case where x is -∞
		return 0
	case -NearZero < x && x < NearZero:
		return 1 + x
	}

	// reduce; computed as r = hi - lo for extra precision.
	var k int
	switch {
	case x < 0:
		k = int(Log2e*x - 0.5)
	case x > 0:
		k = int(Log2e*x + 0.5)
	}
	hi := x - float64(k)*Ln2Hi
	lo := float64(k) * Ln2Lo

	// compute
	return expmulti(hi, lo, k)
}

// Exp2 returns 2**x, the base-2 exponential of x.
//
// Special cases are the same as [Exp].
func Exp2(x float64) float64 {
	return exp2(x)
}

func exp2(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10

		Overflow  = 1.0239999999999999e+03
		Underflow = -1.0740e+03
	)

	// special cases
	switch {
	case IsNaN(x):
		return x
	case x > Overflow: // handles case where x is +∞
		return Inf(1)
	case x < Underflow: // handles case where x is -∞
		return 0
	}

	// argument reduction; x = r×lg(e) + k with |r| ≤ ln(2)/2.
	// computed as r = hi - lo for extra precision.
	var k int
	switch {
	case x > 0:
		k = int(x + 0.5)
	case x < 0:
		k = int(x - 0.5)
	}
	t := x - float64(k)
	hi := t * Ln2Hi
	lo := -t * Ln2Lo

	// compute
	return expmulti(hi, lo, k)
}

// exp1 returns e**r × 2**k where r = hi - lo and |r| ≤ ln(2)/2.
func expmulti(hi, lo float64, k int) float64 {
	const (
		P1 = 1.66666666666666657415e-01  /* 0x3FC55555; 0x55555555 */
		P2 = -2.77777777770155933842e-03 /* 0xBF66C16C; 0x16BEBD93 */
		P3 = 6.61375632143793436117e-05  /* 0x3F11566A; 0xAF25DE2C */
		P4 = -1.65339022054652515390e-06 /* 0xBEBBBD41; 0xC5D26BF1 */
		P5 = 4.13813679705723846039e-08  /* 0x3E663769; 0x72BEA4D0 */
	)

	r := hi - lo
	t := r * r
	c := r - t*(P1+t*(P2+t*(P3+t*(P4+t*P5))))
	y := 1 - ((lo - (r*c)/(2-c)) - hi)
	// TODO(rsc): make sure Ldexp can handle boundary k
	return Ldexp(y, k)
}

// From Go's math/log.go.
/*
	Floating-point logarithm.
*/

// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/e_log.c
// and came with this notice. The go code is a simpler
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// __ieee754_log(x)
// Return the logarithm of x
//
// Method :
//   1. Argument Reduction: find k and f such that
//			x = 2**k * (1+f),
//	   where  sqrt(2)/2 < 1+f < sqrt(2) .
//
//   2. Approximation of log(1+f).
//	Let s = f/(2+f) ; based on log(1+f) = log(1+s) - log(1-s)
//		 = 2s + 2/3 s**3 + 2/5 s**5 + .....,
//	     	 = 2s + s*R
//      We use a special Reme algorithm on [0,0.1716] to generate
//	a polynomial of degree 14 to approximate R.  The maximum error
//	of this polynomial approximation is bounded by 2**-58.45. In
//	other words,
//		        2      4      6      8      10      12      14
//	    R(z) ~ L1*s +L2*s +L3*s +L4*s +L5*s  +L6*s  +L7*s
//	(the values of L1 to L7 are listed in the program) and
//	    |      2          14          |     -58.45
//	    | L1*s +...+L7*s    -  R(z) | <= 2
//	    |                             |
//	Note that 2s = f - s*f = f - hfsq + s*hfsq, where hfsq = f*f/2.
//	In order to guarantee error in log below 1ulp, we compute log by
//		log(1+f) = f - s*(f - R)		(if f is not too large)
//		log(1+f) = f - (hfsq - s*(hfsq+R)).	(better accuracy)
//
//	3. Finally,  log(x) = k*Ln2 + log(1+f).
//			    = k*Ln2_hi+(f-(hfsq-(s*(hfsq+R)+k*Ln2_lo)))
//	   Here Ln2 is split into two floating point number:
//			Ln2_hi + Ln2_lo,
//	   where n*Ln2_hi is always exact for |n| < 2000.
//
// Special cases:
//	log(x) is NaN with signal if x < 0 (including -INF) ;
//	log(+INF) is +INF; log(0) is -INF with signal;
//	log(NaN) is that NaN with no signal.
//
// Accuracy:
//	according to an error analysis, the error is always less than
//	1 ulp (unit in the last place).
//
// Constants:
// The hexadecimal values are the intended ones for the following
// constants. The decimal values may be used, provided that the
// compiler will convert from decimal to binary accurately enough
// to produce the hexadecimal values shown.

// Log returns the natural logarithm of x.
//
// Special cases are:
//
//	Log(+Inf) = +Inf
//	Log(0) = -Inf
//	Log(x < 0) = NaN
//	Log(NaN) = NaN
func Log(x float64) float64 {
	return log(x)
}

func log(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01 /* 3fe62e42 fee00000 */
		Ln2Lo = 1.90821492927058770002e-10 /* 3dea39ef 35793c76 */
		L1    = 6.666666666666735130e-01   /* 3FE55555 55555593 */
		L2    = 3.999999999940941908e-01   /* 3FD99999 9997FA04 */
		L3    = 2.857142874366239149e-01   /* 3FD24924 94229359 */
		L4    = 2.222219843214978396e-01   /* 3FCC71C5 1D8E78AF */
		L5    = 1.818357216161805012e-01   /* 3FC74664 96CB03DE */
		L6    = 1.531383769920937332e-01   /* 3FC39A09 D078C69F */
		L7    = 1.479819860511658591e-01   /* 3FC2F112 DF3E5244 */
	)

	// special cases
	switch {
	case IsNaN(x) || IsInf(x, 1):
		return x
	case x < 0:
		return NaN()
	case x == 0:
		return Inf(-1)
	}

	// reduce
	f1, ki := Frexp(x)
	if f1 < Sqrt2/2 {
		f1 *= 2
		ki--
	}
	f := f1 - 1
	k := float64(ki)

	// compute
	s := f / (2 + f)
	s2 := s * s
	s4 := s2 * s2
	t1 := s2 * (L1 + s4*(L3+s4*(L5+s4*L7)))
	t2 := s4 * (L2 + s4*(L4+s4*L6))
	R := t1 + t2
	hfsq := 0.5 * f * f
	return k*Ln2Hi - ((hfsq - (s*(hfsq+R) + k*Ln2Lo)) - f)
}

// From Go's math/log10.go.
// Log10 returns the decimal logarithm of x.
// The special cases are the same as for [Log].
func Log10(x float64) float64 {
	return log10(x)
}

func log10(x float64) float64 {
	return Log(x) * (1 / Ln10)
}

// Log2 returns the binary logarithm of x.
// The special cases are the same as for [Log].
func Log2(x float64) float64 {
	return log2(x)
}

func log2(x float64) float64 {
	frac, exp := Frexp(x)
	// Make sure exact powers of two give an exact answer.
	// Don't depend on Log(0.5)*(1/Ln2)+exp being exactly exp-1.
	if frac == 0.5 {
		return float64(exp - 1)
	}
	return Log(frac)*(1/Ln2) + float64(exp)
}

// From Go's math/pow.go.
func isOddInt(x float64) bool {
	if Abs(x) >= (1 << 53) {
		// 1 << 53 is the largest exact integer in the float64 format.
		// Any number outside this range will be truncated before the decimal point and therefore will always be
		// an even integer.
		// Without this check and if x overflows int64 the int64(xi) conversion below may produce incorrect results
		// on some architectures (and does so on arm64). See issue #57465.
		return false
	}

	xi, xf := Modf(x)
	return xf == 0 && int64(xi)&1 == 1
}

// Special cases taken from FreeBSD's /usr/src/lib/msun/src/e_pow.c
// updated by IEEE Std. 754-2008 "Section 9.2.1 Special values".

// Pow returns x**y, the base-x exponential of y.
//
// Special cases are (in order):
//
//	Pow(x, ±0) = 1 for any x
//	Pow(1, y) = 1 for any y
//	Pow(x, 1) = x for any x
//	Pow(NaN, y) = NaN
//	Pow(x, NaN) = NaN
//	Pow(±0, y) = ±Inf for y an odd integer < 0
//	Pow(±0, -Inf) = +Inf
//	Pow(±0, +Inf) = +0
//	Pow(±0, y) = +Inf for finite y < 0 and not an odd integer
//	Pow(±0, y) = ±0 for y an odd integer > 0
//	Pow(±0, y) = +0 for finite y > 0 and not an odd integer
//	Pow(-1, ±Inf) = 1
//	Pow(x, +Inf) = +Inf for |x| > 1
//	Pow(x, -Inf) = +0 for |x| > 1
//	Pow(x, +Inf) = +0 for |x| < 1
//	Pow(x, -Inf) = +Inf for |x| < 1
//	Pow(+Inf, y) = +Inf for y > 0
//	Pow(+Inf, y) = +0 for y < 0
//	Pow(-Inf, y) = Pow(-0, -y)
//	Pow(x, y) = NaN for finite x < 0 and finite non-integer y
func Pow(x, y float64) float64 {
	return pow(x, y)
}

func pow(x, y float64) float64 {
	switch {
	case y == 0 || x == 1:
		return 1
	case y == 1:
		return x
	case IsNaN(x) || IsNaN(y):
		return NaN()
	case x == 0:
		switch {
		case y < 0:
			if Signbit(x) && isOddInt(y) {
				return Inf(-1)
			}
			return Inf(1)
		case y > 0:
			if Signbit(x) && isOddInt(y) {
				return x
			}
			return 0
		}
	case IsInf(y, 0):
		switch {
		case x == -1:
			return 1
		case (Abs(x) < 1) == IsInf(y, 1):
			return 0
		default:
			return Inf(1)
		}
	case IsInf(x, 0):
		if IsInf(x, -1) {
			// Go's Pow(1/x, -y), Pow(-0, -y), written out: a library function a
			// goroutine may call does not recurse.
			switch {
			case y > 0 && isOddInt(y):
				return Inf(-1)
			case y > 0:
				return Inf(1)
			case isOddInt(y):
				return Copysign(0, -1)
			}
			return 0
		}
		switch {
		case y < 0:
			return 0
		case y > 0:
			return Inf(1)
		}
	case y == 0.5:
		return Sqrt(x)
	case y == -0.5:
		return 1 / Sqrt(x)
	}

	yi, yf := Modf(Abs(y))
	if yf != 0 && x < 0 {
		return NaN()
	}
	if yi >= 1<<63 {
		// yi is a large even int that will lead to overflow (or underflow to 0)
		// for all x except -1 (x == 1 was handled earlier)
		switch {
		case x == -1:
			return 1
		case (Abs(x) < 1) == (y > 0):
			return 0
		default:
			return Inf(1)
		}
	}

	// ans = a1 * 2**ae (= 1 for now).
	a1 := 1.0
	ae := 0

	// ans *= x**yf
	if yf != 0 {
		if yf > 0.5 {
			yf--
			yi++
		}
		a1 = Exp(yf * Log(x))
	}

	// ans *= x**yi
	// by multiplying in successive squarings
	// of x according to bits of yi.
	// accumulate powers of two into exp.
	x1, xe := Frexp(x)
	for i := int64(yi); i != 0; i >>= 1 {
		if xe < -1<<12 || 1<<12 < xe {
			// catch xe before it overflows the left shift below
			// Since i !=0 it has at least one bit still set, so ae will accumulate xe
			// on at least one more iteration, ae += xe is a lower bound on ae
			// the lower bound on ae exceeds the size of a float64 exp
			// so the final call to Ldexp will produce under/overflow (0/Inf)
			ae += xe
			break
		}
		if i&1 == 1 {
			a1 *= x1
			ae += xe
		}
		x1 *= x1
		xe <<= 1
		if x1 < .5 {
			x1 += x1
			xe--
		}
	}

	// ans = a1*2**ae
	// if y < 0 { ans = 1 / ans }
	// but in the opposite order
	if y < 0 {
		a1 = 1 / a1
		ae = -ae
	}
	return Ldexp(a1, ae)
}

// From Go's math/sin.go.
/*
	Floating-point sine and cosine.
*/

// The original C code, the long comment, and the constants
// below were from http://netlib.sandia.gov/cephes/cmath/sin.c,
// available from http://www.netlib.org/cephes/cmath.tgz.
// The go code is a simplified version of the original C.
//
//      sin.c
//
//      Circular sine
//
// SYNOPSIS:
//
// double x, y, sin();
// y = sin( x );
//
// DESCRIPTION:
//
// Range reduction is into intervals of pi/4.  The reduction error is nearly
// eliminated by contriving an extended precision modular arithmetic.
//
// Two polynomial approximating functions are employed.
// Between 0 and pi/4 the sine is approximated by
//      x  +  x**3 P(x**2).
// Between pi/4 and pi/2 the cosine is represented as
//      1  -  x**2 Q(x**2).
//
// ACCURACY:
//
//                      Relative error:
// arithmetic   domain      # trials      peak         rms
//    DEC       0, 10       150000       3.0e-17     7.8e-18
//    IEEE -1.07e9,+1.07e9  130000       2.1e-16     5.4e-17
//
// Partial loss of accuracy begins to occur at x = 2**30 = 1.074e9.  The loss
// is not gradual, but jumps suddenly to about 1 part in 10e7.  Results may
// be meaningless for x > 2**49 = 5.6e14.
//
//      cos.c
//
//      Circular cosine
//
// SYNOPSIS:
//
// double x, y, cos();
// y = cos( x );
//
// DESCRIPTION:
//
// Range reduction is into intervals of pi/4.  The reduction error is nearly
// eliminated by contriving an extended precision modular arithmetic.
//
// Two polynomial approximating functions are employed.
// Between 0 and pi/4 the cosine is approximated by
//      1  -  x**2 Q(x**2).
// Between pi/4 and pi/2 the sine is represented as
//      x  +  x**3 P(x**2).
//
// ACCURACY:
//
//                      Relative error:
// arithmetic   domain      # trials      peak         rms
//    IEEE -1.07e9,+1.07e9  130000       2.1e-16     5.4e-17
//    DEC        0,+1.07e9   17000       3.0e-17     7.2e-18
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// The readme file at http://netlib.sandia.gov/cephes/ says:
//    Some software in this archive may be from the book _Methods and
// Programs for Mathematical Functions_ (Prentice-Hall or Simon & Schuster
// International, 1989) or from the Cephes Mathematical Library, a
// commercial product. In either event, it is copyrighted by the author.
// What you see here may be used freely but it comes with no support or
// guarantee.
//
//   The two known misprints in the book are repaired here in the
// source listings for the gamma function and the incomplete beta
// integral.
//
//   Stephen L. Moshier
//   moshier@na-net.ornl.gov

// sin coefficients
var _sin = [...]float64{
	1.58962301576546568060e-10, // 0x3de5d8fd1fd19ccd
	-2.50507477628578072866e-8, // 0xbe5ae5e5a9291f5d
	2.75573136213857245213e-6,  // 0x3ec71de3567d48a1
	-1.98412698295895385996e-4, // 0xbf2a01a019bfdf03
	8.33333333332211858878e-3,  // 0x3f8111111110f7d0
	-1.66666666666666307295e-1, // 0xbfc5555555555548
}

// cos coefficients
var _cos = [...]float64{
	-1.13585365213876817300e-11, // 0xbda8fa49a0861a9b
	2.08757008419747316778e-9,   // 0x3e21ee9d7b4e3f05
	-2.75573141792967388112e-7,  // 0xbe927e4f7eac4bc6
	2.48015872888517045348e-5,   // 0x3efa01a019c844f5
	-1.38888888888730564116e-3,  // 0xbf56c16c16c14f91
	4.16666666666665929218e-2,   // 0x3fa555555555554b
}

// Cos returns the cosine of the radian argument x.
//
// Special cases are:
//
//	Cos(±Inf) = NaN
//	Cos(NaN) = NaN
func Cos(x float64) float64 {
	return cos(x)
}

func cos(x float64) float64 {
	const (
		PI4A = 7.85398125648498535156e-1  // 0x3fe921fb40000000, Pi/4 split into three parts
		PI4B = 3.77489470793079817668e-8  // 0x3e64442d00000000,
		PI4C = 2.69515142907905952645e-15 // 0x3ce8469898cc5170,
	)
	// special cases
	switch {
	case IsNaN(x) || IsInf(x, 0):
		return NaN()
	}

	// make argument positive
	sign := false
	x = Abs(x)

	var j uint64
	var y, z float64
	if x >= reduceThreshold {
		j, z = trigReduce(x)
	} else {
		j = uint64(x * (4 / Pi)) // integer part of x/(Pi/4), as integer for tests on the phase angle
		y = float64(j)           // integer part of x/(Pi/4), as float

		// map zeros to origin
		if j&1 == 1 {
			j++
			y++
		}
		j &= 7                               // octant modulo 2Pi radians (360 degrees)
		z = ((x - y*PI4A) - y*PI4B) - y*PI4C // Extended precision modular arithmetic
	}

	if j > 3 {
		j -= 4
		sign = !sign
	}
	if j > 1 {
		sign = !sign
	}

	zz := z * z
	if j == 1 || j == 2 {
		y = z + z*zz*((((((_sin[0]*zz)+_sin[1])*zz+_sin[2])*zz+_sin[3])*zz+_sin[4])*zz+_sin[5])
	} else {
		y = 1.0 - 0.5*zz + zz*zz*((((((_cos[0]*zz)+_cos[1])*zz+_cos[2])*zz+_cos[3])*zz+_cos[4])*zz+_cos[5])
	}
	if sign {
		y = -y
	}
	return y
}

// Sin returns the sine of the radian argument x.
//
// Special cases are:
//
//	Sin(±0) = ±0
//	Sin(±Inf) = NaN
//	Sin(NaN) = NaN
func Sin(x float64) float64 {
	return sin(x)
}

func sin(x float64) float64 {
	const (
		PI4A = 7.85398125648498535156e-1  // 0x3fe921fb40000000, Pi/4 split into three parts
		PI4B = 3.77489470793079817668e-8  // 0x3e64442d00000000,
		PI4C = 2.69515142907905952645e-15 // 0x3ce8469898cc5170,
	)
	// special cases
	switch {
	case x == 0 || IsNaN(x):
		return x // return ±0 || NaN()
	case IsInf(x, 0):
		return NaN()
	}

	// make argument positive but save the sign
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	var j uint64
	var y, z float64
	if x >= reduceThreshold {
		j, z = trigReduce(x)
	} else {
		j = uint64(x * (4 / Pi)) // integer part of x/(Pi/4), as integer for tests on the phase angle
		y = float64(j)           // integer part of x/(Pi/4), as float

		// map zeros to origin
		if j&1 == 1 {
			j++
			y++
		}
		j &= 7                               // octant modulo 2Pi radians (360 degrees)
		z = ((x - y*PI4A) - y*PI4B) - y*PI4C // Extended precision modular arithmetic
	}
	// reflect in x axis
	if j > 3 {
		sign = !sign
		j -= 4
	}
	zz := z * z
	if j == 1 || j == 2 {
		y = 1.0 - 0.5*zz + zz*zz*((((((_cos[0]*zz)+_cos[1])*zz+_cos[2])*zz+_cos[3])*zz+_cos[4])*zz+_cos[5])
	} else {
		y = z + z*zz*((((((_sin[0]*zz)+_sin[1])*zz+_sin[2])*zz+_sin[3])*zz+_sin[4])*zz+_sin[5])
	}
	if sign {
		y = -y
	}
	return y
}

// From Go's math/tan.go.
/*
	Floating-point tangent.
*/

// The original C code, the long comment, and the constants
// below were from http://netlib.sandia.gov/cephes/cmath/sin.c,
// available from http://www.netlib.org/cephes/cmath.tgz.
// The go code is a simplified version of the original C.
//
//      tan.c
//
//      Circular tangent
//
// SYNOPSIS:
//
// double x, y, tan();
// y = tan( x );
//
// DESCRIPTION:
//
// Returns the circular tangent of the radian argument x.
//
// Range reduction is modulo pi/4.  A rational function
//       x + x**3 P(x**2)/Q(x**2)
// is employed in the basic interval [0, pi/4].
//
// ACCURACY:
//                      Relative error:
// arithmetic   domain     # trials      peak         rms
//    DEC      +-1.07e9      44000      4.1e-17     1.0e-17
//    IEEE     +-1.07e9      30000      2.9e-16     8.1e-17
//
// Partial loss of accuracy begins to occur at x = 2**30 = 1.074e9.  The loss
// is not gradual, but jumps suddenly to about 1 part in 10e7.  Results may
// be meaningless for x > 2**49 = 5.6e14.
// [Accuracy loss statement from sin.go comments.]
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// The readme file at http://netlib.sandia.gov/cephes/ says:
//    Some software in this archive may be from the book _Methods and
// Programs for Mathematical Functions_ (Prentice-Hall or Simon & Schuster
// International, 1989) or from the Cephes Mathematical Library, a
// commercial product. In either event, it is copyrighted by the author.
// What you see here may be used freely but it comes with no support or
// guarantee.
//
//   The two known misprints in the book are repaired here in the
// source listings for the gamma function and the incomplete beta
// integral.
//
//   Stephen L. Moshier
//   moshier@na-net.ornl.gov

// tan coefficients
var _tanP = [...]float64{
	-1.30936939181383777646e4, // 0xc0c992d8d24f3f38
	1.15351664838587416140e6,  // 0x413199eca5fc9ddd
	-1.79565251976484877988e7, // 0xc1711fead3299176
}
var _tanQ = [...]float64{
	1.00000000000000000000e0,
	1.36812963470692954678e4,  // 0x40cab8a5eeb36572
	-1.32089234440210967447e6, // 0xc13427bc582abc96
	2.50083801823357915839e7,  // 0x4177d98fc2ead8ef
	-5.38695755929454629881e7, // 0xc189afe03cbe5a31
}

// Tan returns the tangent of the radian argument x.
//
// Special cases are:
//
//	Tan(±0) = ±0
//	Tan(±Inf) = NaN
//	Tan(NaN) = NaN
func Tan(x float64) float64 {
	return tan(x)
}

func tan(x float64) float64 {
	const (
		PI4A = 7.85398125648498535156e-1  // 0x3fe921fb40000000, Pi/4 split into three parts
		PI4B = 3.77489470793079817668e-8  // 0x3e64442d00000000,
		PI4C = 2.69515142907905952645e-15 // 0x3ce8469898cc5170,
	)
	// special cases
	switch {
	case x == 0 || IsNaN(x):
		return x // return ±0 || NaN()
	case IsInf(x, 0):
		return NaN()
	}

	// make argument positive but save the sign
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	var j uint64
	var y, z float64
	if x >= reduceThreshold {
		j, z = trigReduce(x)
	} else {
		j = uint64(x * (4 / Pi)) // integer part of x/(Pi/4), as integer for tests on the phase angle
		y = float64(j)           // integer part of x/(Pi/4), as float

		/* map zeros and singularities to origin */
		if j&1 == 1 {
			j++
			y++
		}

		z = ((x - y*PI4A) - y*PI4B) - y*PI4C
	}
	zz := z * z

	if zz > 1e-14 {
		y = z + z*(zz*(((_tanP[0]*zz)+_tanP[1])*zz+_tanP[2])/((((zz+_tanQ[1])*zz+_tanQ[2])*zz+_tanQ[3])*zz+_tanQ[4]))
	} else {
		y = z
	}
	if j&2 == 2 {
		y = -1 / y
	}
	if sign {
		y = -y
	}
	return y
}

// From Go's math/trig_reduce.go.
// reduceThreshold is the maximum value of x where the reduction using Pi/4
// in 3 float64 parts still gives accurate results. This threshold
// is set by y*C being representable as a float64 without error
// where y is given by y = floor(x * (4 / Pi)) and C is the leading partial
// terms of 4/Pi. Since the leading terms (PI4A and PI4B in sin.go) have 30
// and 32 trailing zero bits, y should have less than 30 significant bits.
//
//	y < 1<<30  -> floor(x*4/Pi) < 1<<30 -> x < (1<<30 - 1) * Pi/4
//
// So, conservatively we can take x < 1<<29.
// Above this threshold Payne-Hanek range reduction must be used.
const reduceThreshold = 1 << 29

// trigReduce implements Payne-Hanek range reduction by Pi/4
// for x > 0. It returns the integer part mod 8 (j) and
// the fractional part (z) of x / (Pi/4).
// The implementation is based on:
// "ARGUMENT REDUCTION FOR HUGE ARGUMENTS: Good to the Last Bit"
// K. C. Ng et al, March 24, 1992
// The simulated multi-precision calculation of x*B uses 64-bit integer arithmetic.
func trigReduce(x float64) (j uint64, z float64) {
	const PI4 = Pi / 4
	if x < PI4 {
		return 0, x
	}
	// Extract out the integer and exponent such that,
	// x = ix * 2 ** exp.
	ix := Float64bits(x)
	exp := int(ix>>shift&mask) - bias - shift
	ix &^= mask << shift
	ix |= 1 << shift
	// Use the exponent to extract the 3 appropriate uint64 digits from mPi4,
	// B ~ (z0, z1, z2), such that the product leading digit has the exponent -61.
	// Note, exp >= -53 since x >= PI4 and exp < 971 for maximum float64.
	digit, bitshift := uint(exp+61)/64, uint(exp+61)%64
	z0 := (mPi4[digit] << bitshift) | (mPi4[digit+1] >> (64 - bitshift))
	z1 := (mPi4[digit+1] << bitshift) | (mPi4[digit+2] >> (64 - bitshift))
	z2 := (mPi4[digit+2] << bitshift) | (mPi4[digit+3] >> (64 - bitshift))
	// Multiply mantissa by the digits and extract the upper two digits (hi, lo).
	z2hi, _ := mul64(z2, ix)
	z1hi, z1lo := mul64(z1, ix)
	z0lo := z0 * ix
	lo, c := add64(z1lo, z2hi, 0)
	hi, _ := add64(z0lo, z1hi, c)
	// The top 3 bits are j.
	j = hi >> 61
	// Extract the fraction and find its magnitude.
	hi = hi<<3 | lo>>61
	lz := uint(leadingZeros64(hi))
	e := uint64(bias - (lz + 1))
	// Clear implicit mantissa bit and shift into place.
	hi = (hi << (lz + 1)) | (lo >> (64 - (lz + 1)))
	hi >>= 64 - shift
	// Include the exponent and convert to a float.
	hi |= e << shift
	z = Float64frombits(hi)
	// Map zeros to origin.
	if j&1 == 1 {
		j++
		j &= 7
		z--
	}
	// Multiply the fractional part by pi/4.
	return j, z * PI4
}

// mPi4 is the binary digits of 4/pi as a uint64 array,
// that is, 4/pi = Sum mPi4[i]*2^(-64*i)
// 19 64-bit digits and the leading one bit give 1217 bits
// of precision to handle the largest possible float64 exponent.
var mPi4 = [...]uint64{
	0x0000000000000001,
	0x45f306dc9c882a53,
	0xf84eafa3ea69bb81,
	0xb6c52b3278872083,
	0xfca2c757bd778ac3,
	0x6e48dc74849ba5c0,
	0x0c925dd413a32439,
	0xfc3bd63962534e7d,
	0xd1046bea5d768909,
	0xd338e04d68befc82,
	0x7323ac7306a673e9,
	0x3908bf177bf25076,
	0x3ff12fffbc0b301f,
	0xde5e2316b414da3e,
	0xda6cfd9e4f96136e,
	0x9e8c7ecd3cbfd45a,
	0xea4f758fd7cbe2f6,
	0x7a0e73ef14a525d4,
	0xd7f6bf623f1aba10,
	0xac06608df8f6d757,
}

// From Go's math/asin.go.
/*
	Floating-point arcsine and arccosine.

	They are implemented by computing the arctangent
	after appropriate range reduction.
*/

// Asin returns the arcsine, in radians, of x.
//
// Special cases are:
//
//	Asin(±0) = ±0
//	Asin(x) = NaN if x < -1 or x > 1
func Asin(x float64) float64 {
	return asin(x)
}

func asin(x float64) float64 {
	if x == 0 {
		return x // special case
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	if x > 1 {
		return NaN() // special case
	}

	temp := Sqrt(1 - x*x)
	if x > 0.7 {
		temp = Pi/2 - satan(temp/x)
	} else {
		temp = satan(x / temp)
	}

	if sign {
		temp = -temp
	}
	return temp
}

// Acos returns the arccosine, in radians, of x.
//
// Special case is:
//
//	Acos(x) = NaN if x < -1 or x > 1
func Acos(x float64) float64 {
	return acos(x)
}

func acos(x float64) float64 {
	return Pi/2 - Asin(x)
}

// From Go's math/atan.go.
/*
	Floating-point arctangent.
*/

// The original C code, the long comment, and the constants below were
// from http://netlib.sandia.gov/cephes/cmath/atan.c, available from
// http://www.netlib.org/cephes/cmath.tgz.
// The go code is a version of the original C.
//
// atan.c
// Inverse circular tangent (arctangent)
//
// SYNOPSIS:
// double x, y, atan();
// y = atan( x );
//
// DESCRIPTION:
// Returns radian angle between -pi/2 and +pi/2 whose tangent is x.
//
// Range reduction is from three intervals into the interval from zero to 0.66.
// The approximant uses a rational function of degree 4/5 of the form
// x + x**3 P(x)/Q(x).
//
// ACCURACY:
//                      Relative error:
// arithmetic   domain    # trials  peak     rms
//    DEC       -10, 10   50000     2.4e-17  8.3e-18
//    IEEE      -10, 10   10^6      1.8e-16  5.0e-17
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// The readme file at http://netlib.sandia.gov/cephes/ says:
//    Some software in this archive may be from the book _Methods and
// Programs for Mathematical Functions_ (Prentice-Hall or Simon & Schuster
// International, 1989) or from the Cephes Mathematical Library, a
// commercial product. In either event, it is copyrighted by the author.
// What you see here may be used freely but it comes with no support or
// guarantee.
//
//   The two known misprints in the book are repaired here in the
// source listings for the gamma function and the incomplete beta
// integral.
//
//   Stephen L. Moshier
//   moshier@na-net.ornl.gov

// xatan evaluates a series valid in the range [0, 0.66].
func xatan(x float64) float64 {
	const (
		P0 = -8.750608600031904122785e-01
		P1 = -1.615753718733365076637e+01
		P2 = -7.500855792314704667340e+01
		P3 = -1.228866684490136173410e+02
		P4 = -6.485021904942025371773e+01
		Q0 = +2.485846490142306297962e+01
		Q1 = +1.650270098316988542046e+02
		Q2 = +4.328810604912902668951e+02
		Q3 = +4.853903996359136964868e+02
		Q4 = +1.945506571482613964425e+02
	)
	z := x * x
	z = z * ((((P0*z+P1)*z+P2)*z+P3)*z + P4) / (((((z+Q0)*z+Q1)*z+Q2)*z+Q3)*z + Q4)
	z = x*z + x
	return z
}

// satan reduces its argument (known to be positive)
// to the range [0, 0.66] and calls xatan.
func satan(x float64) float64 {
	const (
		Morebits = 6.123233995736765886130e-17 // pi/2 = PIO2 + Morebits
		Tan3pio8 = 2.41421356237309504880      // tan(3*pi/8)
	)
	if x <= 0.66 {
		return xatan(x)
	}
	if x > Tan3pio8 {
		return Pi/2 - xatan(1/x) + Morebits
	}
	return Pi/4 + xatan((x-1)/(x+1)) + 0.5*Morebits
}

// Atan returns the arctangent, in radians, of x.
//
// Special cases are:
//
//	Atan(±0) = ±0
//	Atan(±Inf) = ±Pi/2
func Atan(x float64) float64 {
	return atan(x)
}

func atan(x float64) float64 {
	if x == 0 {
		return x
	}
	if x > 0 {
		return satan(x)
	}
	return -satan(-x)
}

// From Go's math/atan2.go.
// Atan2 returns the arc tangent of y/x, using
// the signs of the two to determine the quadrant
// of the return value.
//
// Special cases are (in order):
//
//	Atan2(y, NaN) = NaN
//	Atan2(NaN, x) = NaN
//	Atan2(+0, x>=0) = +0
//	Atan2(-0, x>=0) = -0
//	Atan2(+0, x<=-0) = +Pi
//	Atan2(-0, x<=-0) = -Pi
//	Atan2(y>0, 0) = +Pi/2
//	Atan2(y<0, 0) = -Pi/2
//	Atan2(+Inf, +Inf) = +Pi/4
//	Atan2(-Inf, +Inf) = -Pi/4
//	Atan2(+Inf, -Inf) = 3Pi/4
//	Atan2(-Inf, -Inf) = -3Pi/4
//	Atan2(y, +Inf) = 0
//	Atan2(y>0, -Inf) = +Pi
//	Atan2(y<0, -Inf) = -Pi
//	Atan2(+Inf, x) = +Pi/2
//	Atan2(-Inf, x) = -Pi/2
func Atan2(y, x float64) float64 {
	return atan2(y, x)
}

func atan2(y, x float64) float64 {
	// special cases
	switch {
	case IsNaN(y) || IsNaN(x):
		return NaN()
	case y == 0:
		if x >= 0 && !Signbit(x) {
			return Copysign(0, y)
		}
		return Copysign(Pi, y)
	case x == 0:
		return Copysign(Pi/2, y)
	case IsInf(x, 0):
		if IsInf(x, 1) {
			switch {
			case IsInf(y, 0):
				return Copysign(Pi/4, y)
			default:
				return Copysign(0, y)
			}
		}
		switch {
		case IsInf(y, 0):
			return Copysign(3*Pi/4, y)
		default:
			return Copysign(Pi, y)
		}
	case IsInf(y, 0):
		return Copysign(Pi/2, y)
	}

	// Call atan and determine the quadrant.
	q := Atan(y / x)
	if x < 0 {
		if q <= 0 {
			return q + Pi
		}
		return q - Pi
	}
	return q
}

// From Go's math/mod.go.
/*
	Floating-point mod function.
*/

// Mod returns the floating-point remainder of x/y.
// The magnitude of the result is less than y and its
// sign agrees with that of x.
//
// Special cases are:
//
//	Mod(±Inf, y) = NaN
//	Mod(NaN, y) = NaN
//	Mod(x, 0) = NaN
//	Mod(x, ±Inf) = x
//	Mod(x, NaN) = NaN
func Mod(x, y float64) float64 {
	return mod(x, y)
}

func mod(x, y float64) float64 {
	if y == 0 || IsInf(x, 0) || IsNaN(x) || IsNaN(y) {
		return NaN()
	}
	y = Abs(y)

	yfr, yexp := Frexp(y)
	r := x
	if x < 0 {
		r = -x
	}

	for r >= y {
		rfr, rexp := Frexp(r)
		if rfr < yfr {
			rexp = rexp - 1
		}
		r = r - Ldexp(y, rexp-yexp)
	}
	if x < 0 {
		r = -r
	}
	return r
}

// From Go's math/dim.go.
// Dim returns the maximum of x-y or 0.
//
// Special cases are:
//
//	Dim(+Inf, +Inf) = NaN
//	Dim(-Inf, -Inf) = NaN
//	Dim(x, NaN) = Dim(NaN, x) = NaN
func Dim(x, y float64) float64 {
	// The special cases result in NaN after the subtraction:
	//      +Inf - +Inf = NaN
	//      -Inf - -Inf = NaN
	//       NaN - y    = NaN
	//         x - NaN  = NaN
	v := x - y
	if v <= 0 {
		// v is negative or 0
		return 0
	}
	// v is positive or NaN
	return v
}

// Max returns the larger of x or y.
//
// Special cases are:
//
//	Max(x, +Inf) = Max(+Inf, x) = +Inf
//	Max(x, NaN) = Max(NaN, x) = NaN
//	Max(+0, ±0) = Max(±0, +0) = +0
//	Max(-0, -0) = -0
//
// Note that this differs from the built-in function max when called
// with NaN and +Inf.
func Max(x, y float64) float64 {
	return max(x, y)
}

func max(x, y float64) float64 {
	// special cases
	switch {
	case IsInf(x, 1) || IsInf(y, 1):
		return Inf(1)
	case IsNaN(x) || IsNaN(y):
		return NaN()
	case x == 0 && x == y:
		if Signbit(x) {
			return y
		}
		return x
	}
	if x > y {
		return x
	}
	return y
}

// Min returns the smaller of x or y.
//
// Special cases are:
//
//	Min(x, -Inf) = Min(-Inf, x) = -Inf
//	Min(x, NaN) = Min(NaN, x) = NaN
//	Min(-0, ±0) = Min(±0, -0) = -0
//
// Note that this differs from the built-in function min when called
// with NaN and -Inf.
func Min(x, y float64) float64 {
	return min(x, y)
}

func min(x, y float64) float64 {
	// special cases
	switch {
	case IsInf(x, -1) || IsInf(y, -1):
		return Inf(-1)
	case IsNaN(x) || IsNaN(y):
		return NaN()
	case x == 0 && x == y:
		if Signbit(x) {
			return x
		}
		return y
	}
	if x < y {
		return x
	}
	return y
}

// From Go's math/hypot.go.
/*
	Hypot -- sqrt(p*p + q*q), but overflows only if the result does.
*/

// Hypot returns [Sqrt](p*p + q*q), taking care to avoid
// unnecessary overflow and underflow.
//
// Special cases are:
//
//	Hypot(±Inf, q) = +Inf
//	Hypot(p, ±Inf) = +Inf
//	Hypot(NaN, q) = NaN
//	Hypot(p, NaN) = NaN
func Hypot(p, q float64) float64 {
	return hypot(p, q)
}

func hypot(p, q float64) float64 {
	p, q = Abs(p), Abs(q)
	// special cases
	switch {
	case IsInf(p, 1) || IsInf(q, 1):
		return Inf(1)
	case IsNaN(p) || IsNaN(q):
		return NaN()
	}
	if p < q {
		p, q = q, p
	}
	if p == 0 {
		return 0
	}
	q = q / p
	return p * Sqrt(1+q*q)
}

// From Go's math/cbrt.go.
// The go code is a modified version of the original C code from
// http://www.netlib.org/fdlibm/s_cbrt.c and came with this notice.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunSoft, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================

// Cbrt returns the cube root of x.
//
// Special cases are:
//
//	Cbrt(±0) = ±0
//	Cbrt(±Inf) = ±Inf
//	Cbrt(NaN) = NaN
func Cbrt(x float64) float64 {
	return cbrt(x)
}

func cbrt(x float64) float64 {
	const (
		B1             = 715094163                   // (682-0.03306235651)*2**20
		B2             = 696219795                   // (664-0.03306235651)*2**20
		C              = 5.42857142857142815906e-01  // 19/35     = 0x3FE15F15F15F15F1
		D              = -7.05306122448979611050e-01 // -864/1225 = 0xBFE691DE2532C834
		E              = 1.41428571428571436819e+00  // 99/70     = 0x3FF6A0EA0EA0EA0F
		F              = 1.60714285714285720630e+00  // 45/28     = 0x3FF9B6DB6DB6DB6E
		G              = 3.57142857142857150787e-01  // 5/14      = 0x3FD6DB6DB6DB6DB7
		SmallestNormal = 2.22507385850720138309e-308 // 2**-1022  = 0x0010000000000000
	)
	// special cases
	switch {
	case x == 0 || IsNaN(x) || IsInf(x, 0):
		return x
	}

	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	// rough cbrt to 5 bits
	t := Float64frombits(Float64bits(x)/3 + B1<<32)
	if x < SmallestNormal {
		// subnormal number
		t = float64(1 << 54) // set t= 2**54
		t *= x
		t = Float64frombits(Float64bits(t)/3 + B2<<32)
	}

	// new cbrt to 23 bits
	r := t * t / x
	s := C + r*t
	t *= G + F/(s+E+D/s)

	// chop to 22 bits, make larger than cbrt(x)
	t = Float64frombits(Float64bits(t)&(0xFFFFFFFFC<<28) + 1<<30)

	// one step newton iteration to 53 bits with error less than 0.667ulps
	s = t * t // t*t is exact
	r = x / s
	w := t + t
	r = (r - t) / (w + r) // r-s is exact
	t = t + t*r

	// restore the sign bit
	if sign {
		t = -t
	}
	return t
}

// From Go's math/sinh.go.
/*
	Floating-point hyperbolic sine and cosine.

	The exponential func is called for arguments
	greater in magnitude than 0.5.

	A series is used for arguments smaller in magnitude than 0.5.

	Cosh(x) is computed from the exponential func for
	all arguments.
*/

// Sinh returns the hyperbolic sine of x.
//
// Special cases are:
//
//	Sinh(±0) = ±0
//	Sinh(±Inf) = ±Inf
//	Sinh(NaN) = NaN
func Sinh(x float64) float64 {
	return sinh(x)
}

func sinh(x float64) float64 {
	// The coefficients are #2029 from Hart & Cheney. (20.36D)
	const (
		P0 = -0.6307673640497716991184787251e+6
		P1 = -0.8991272022039509355398013511e+5
		P2 = -0.2894211355989563807284660366e+4
		P3 = -0.2630563213397497062819489e+2
		Q0 = -0.6307673640497716991212077277e+6
		Q1 = 0.1521517378790019070696485176e+5
		Q2 = -0.173678953558233699533450911e+3
	)

	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	var temp float64
	switch {
	case x > 21:
		temp = Exp(x) * 0.5

	case x > 0.5:
		ex := Exp(x)
		temp = (ex - 1/ex) * 0.5

	default:
		sq := x * x
		temp = (((P3*sq+P2)*sq+P1)*sq + P0) * x
		temp = temp / (((sq+Q2)*sq+Q1)*sq + Q0)
	}

	if sign {
		temp = -temp
	}
	return temp
}

// Cosh returns the hyperbolic cosine of x.
//
// Special cases are:
//
//	Cosh(±0) = 1
//	Cosh(±Inf) = +Inf
//	Cosh(NaN) = NaN
func Cosh(x float64) float64 {
	return cosh(x)
}

func cosh(x float64) float64 {
	x = Abs(x)
	if x > 21 {
		return Exp(x) * 0.5
	}
	ex := Exp(x)
	return (ex + 1/ex) * 0.5
}

// From Go's math/tanh.go.
// The original C code, the long comment, and the constants
// below were from http://netlib.sandia.gov/cephes/cmath/sin.c,
// available from http://www.netlib.org/cephes/cmath.tgz.
// The go code is a simplified version of the original C.
//      tanh.c
//
//      Hyperbolic tangent
//
// SYNOPSIS:
//
// double x, y, tanh();
//
// y = tanh( x );
//
// DESCRIPTION:
//
// Returns hyperbolic tangent of argument in the range MINLOG to MAXLOG.
//      MAXLOG = 8.8029691931113054295988e+01 = log(2**127)
//      MINLOG = -8.872283911167299960540e+01 = log(2**-128)
//
// A rational function is used for |x| < 0.625.  The form
// x + x**3 P(x)/Q(x) of Cody & Waite is employed.
// Otherwise,
//      tanh(x) = sinh(x)/cosh(x) = 1  -  2/(exp(2x) + 1).
//
// ACCURACY:
//
//                      Relative error:
// arithmetic   domain     # trials      peak         rms
//    IEEE      -2,2        30000       2.5e-16     5.8e-17
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// The readme file at http://netlib.sandia.gov/cephes/ says:
//    Some software in this archive may be from the book _Methods and
// Programs for Mathematical Functions_ (Prentice-Hall or Simon & Schuster
// International, 1989) or from the Cephes Mathematical Library, a
// commercial product. In either event, it is copyrighted by the author.
// What you see here may be used freely but it comes with no support or
// guarantee.
//
//   The two known misprints in the book are repaired here in the
// source listings for the gamma function and the incomplete beta
// integral.
//
//   Stephen L. Moshier
//   moshier@na-net.ornl.gov
//

var tanhP = [...]float64{
	-9.64399179425052238628e-1,
	-9.92877231001918586564e1,
	-1.61468768441708447952e3,
}
var tanhQ = [...]float64{
	1.12811678491632931402e2,
	2.23548839060100448583e3,
	4.84406305325125486048e3,
}

// Tanh returns the hyperbolic tangent of x.
//
// Special cases are:
//
//	Tanh(±0) = ±0
//	Tanh(±Inf) = ±1
//	Tanh(NaN) = NaN
func Tanh(x float64) float64 {
	return tanh(x)
}

func tanh(x float64) float64 {
	const MAXLOG = 8.8029691931113054295988e+01 // log(2**127)
	z := Abs(x)
	switch {
	case z > 0.5*MAXLOG:
		if x < 0 {
			return -1
		}
		return 1
	case z >= 0.625:
		s := Exp(2 * z)
		z = 1 - 2/(s+1)
		if x < 0 {
			z = -z
		}
	default:
		if x == 0 {
			return x
		}
		s := x * x
		z = x + x*s*((tanhP[0]*s+tanhP[1])*s+tanhP[2])/(((s+tanhQ[0])*s+tanhQ[1])*s+tanhQ[2])
	}
	return z
}

// From Go's math/asinh.go.
// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/s_asinh.c
// and came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
//
// asinh(x)
// Method :
//	Based on
//	        asinh(x) = sign(x) * log [ |x| + sqrt(x*x+1) ]
//	we have
//	asinh(x) := x  if  1+x*x=1,
//	         := sign(x)*(log(x)+ln2) for large |x|, else
//	         := sign(x)*log(2|x|+1/(|x|+sqrt(x*x+1))) if|x|>2, else
//	         := sign(x)*log1p(|x| + x**2/(1 + sqrt(1+x**2)))
//

// Asinh returns the inverse hyperbolic sine of x.
//
// Special cases are:
//
//	Asinh(±0) = ±0
//	Asinh(±Inf) = ±Inf
//	Asinh(NaN) = NaN
func Asinh(x float64) float64 {
	return asinh(x)
}

func asinh(x float64) float64 {
	const (
		Ln2      = 6.93147180559945286227e-01 // 0x3FE62E42FEFA39EF
		NearZero = 1.0 / (1 << 28)            // 2**-28
		Large    = 1 << 28                    // 2**28
	)
	// special cases
	if IsNaN(x) || IsInf(x, 0) {
		return x
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	var temp float64
	switch {
	case x > Large:
		temp = Log(x) + Ln2 // |x| > 2**28
	case x > 2:
		temp = Log(2*x + 1/(Sqrt(x*x+1)+x)) // 2**28 > |x| > 2.0
	case x < NearZero:
		temp = x // |x| < 2**-28
	default:
		temp = Log1p(x + x*x/(1+Sqrt(1+x*x))) // 2.0 > |x| > 2**-28
	}
	if sign {
		temp = -temp
	}
	return temp
}

// From Go's math/acosh.go.
// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/e_acosh.c
// and came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
//
// __ieee754_acosh(x)
// Method :
//	Based on
//	        acosh(x) = log [ x + sqrt(x*x-1) ]
//	we have
//	        acosh(x) := log(x)+ln2,	if x is large; else
//	        acosh(x) := log(2x-1/(sqrt(x*x-1)+x)) if x>2; else
//	        acosh(x) := log1p(t+sqrt(2.0*t+t*t)); where t=x-1.
//
// Special cases:
//	acosh(x) is NaN with signal if x<1.
//	acosh(NaN) is NaN without signal.
//

// Acosh returns the inverse hyperbolic cosine of x.
//
// Special cases are:
//
//	Acosh(+Inf) = +Inf
//	Acosh(x) = NaN if x < 1
//	Acosh(NaN) = NaN
func Acosh(x float64) float64 {
	return acosh(x)
}

func acosh(x float64) float64 {
	const Large = 1 << 28 // 2**28
	// first case is special case
	switch {
	case x < 1 || IsNaN(x):
		return NaN()
	case x == 1:
		return 0
	case x >= Large:
		return Log(x) + Ln2 // x > 2**28
	case x > 2:
		return Log(2*x - 1/(x+Sqrt(x*x-1))) // 2**28 > x > 2
	}
	t := x - 1
	return Log1p(t + Sqrt(2*t+t*t)) // 2 >= x > 1
}

// From Go's math/atanh.go.
// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/e_atanh.c
// and came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
//
// __ieee754_atanh(x)
// Method :
//	1. Reduce x to positive by atanh(-x) = -atanh(x)
//	2. For x>=0.5
//	            1              2x                          x
//	atanh(x) = --- * log(1 + -------) = 0.5 * log1p(2 * --------)
//	            2             1 - x                      1 - x
//
//	For x<0.5
//	atanh(x) = 0.5*log1p(2x+2x*x/(1-x))
//
// Special cases:
//	atanh(x) is NaN if |x| > 1 with signal;
//	atanh(NaN) is that NaN with no signal;
//	atanh(+-1) is +-INF with signal.
//

// Atanh returns the inverse hyperbolic tangent of x.
//
// Special cases are:
//
//	Atanh(1) = +Inf
//	Atanh(±0) = ±0
//	Atanh(-1) = -Inf
//	Atanh(x) = NaN if x < -1 or x > 1
//	Atanh(NaN) = NaN
func Atanh(x float64) float64 {
	return atanh(x)
}

func atanh(x float64) float64 {
	const NearZero = 1.0 / (1 << 28) // 2**-28
	// special cases
	switch {
	case x < -1 || x > 1 || IsNaN(x):
		return NaN()
	case x == 1:
		return Inf(1)
	case x == -1:
		return Inf(-1)
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	var temp float64
	switch {
	case x < NearZero:
		temp = x
	case x < 0.5:
		temp = x + x
		temp = 0.5 * Log1p(temp+temp*x/(1-x))
	default:
		temp = 0.5 * Log1p((x+x)/(1-x))
	}
	if sign {
		temp = -temp
	}
	return temp
}

// From Go's math/expm1.go.
// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/s_expm1.c
// and came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// expm1(x)
// Returns exp(x)-1, the exponential of x minus 1.
//
// Method
//   1. Argument reduction:
//      Given x, find r and integer k such that
//
//               x = k*ln2 + r,  |r| <= 0.5*ln2 ~ 0.34658
//
//      Here a correction term c will be computed to compensate
//      the error in r when rounded to a floating-point number.
//
//   2. Approximating expm1(r) by a special rational function on
//      the interval [0,0.34658]:
//      Since
//          r*(exp(r)+1)/(exp(r)-1) = 2+ r**2/6 - r**4/360 + ...
//      we define R1(r*r) by
//          r*(exp(r)+1)/(exp(r)-1) = 2+ r**2/6 * R1(r*r)
//      That is,
//          R1(r**2) = 6/r *((exp(r)+1)/(exp(r)-1) - 2/r)
//                   = 6/r * ( 1 + 2.0*(1/(exp(r)-1) - 1/r))
//                   = 1 - r**2/60 + r**4/2520 - r**6/100800 + ...
//      We use a special Reme algorithm on [0,0.347] to generate
//      a polynomial of degree 5 in r*r to approximate R1. The
//      maximum error of this polynomial approximation is bounded
//      by 2**-61. In other words,
//          R1(z) ~ 1.0 + Q1*z + Q2*z**2 + Q3*z**3 + Q4*z**4 + Q5*z**5
//      where   Q1  =  -1.6666666666666567384E-2,
//              Q2  =   3.9682539681370365873E-4,
//              Q3  =  -9.9206344733435987357E-6,
//              Q4  =   2.5051361420808517002E-7,
//              Q5  =  -6.2843505682382617102E-9;
//      (where z=r*r, and the values of Q1 to Q5 are listed below)
//      with error bounded by
//          |                  5           |     -61
//          | 1.0+Q1*z+...+Q5*z   -  R1(z) | <= 2
//          |                              |
//
//      expm1(r) = exp(r)-1 is then computed by the following
//      specific way which minimize the accumulation rounding error:
//                             2     3
//                            r     r    [ 3 - (R1 + R1*r/2)  ]
//            expm1(r) = r + --- + --- * [--------------------]
//                            2     2    [ 6 - r*(3 - R1*r/2) ]
//
//      To compensate the error in the argument reduction, we use
//              expm1(r+c) = expm1(r) + c + expm1(r)*c
//                         ~ expm1(r) + c + r*c
//      Thus c+r*c will be added in as the correction terms for
//      expm1(r+c). Now rearrange the term to avoid optimization
//      screw up:
//                      (      2                                    2 )
//                      ({  ( r    [ R1 -  (3 - R1*r/2) ]  )  }    r  )
//       expm1(r+c)~r - ({r*(--- * [--------------------]-c)-c} - --- )
//                      ({  ( 2    [ 6 - r*(3 - R1*r/2) ]  )  }    2  )
//                      (                                             )
//
//                 = r - E
//   3. Scale back to obtain expm1(x):
//      From step 1, we have
//         expm1(x) = either 2**k*[expm1(r)+1] - 1
//                  = or     2**k*[expm1(r) + (1-2**-k)]
//   4. Implementation notes:
//      (A). To save one multiplication, we scale the coefficient Qi
//           to Qi*2**i, and replace z by (x**2)/2.
//      (B). To achieve maximum accuracy, we compute expm1(x) by
//        (i)   if x < -56*ln2, return -1.0, (raise inexact if x!=inf)
//        (ii)  if k=0, return r-E
//        (iii) if k=-1, return 0.5*(r-E)-0.5
//        (iv)  if k=1 if r < -0.25, return 2*((r+0.5)- E)
//                     else          return  1.0+2.0*(r-E);
//        (v)   if (k<-2||k>56) return 2**k(1-(E-r)) - 1 (or exp(x)-1)
//        (vi)  if k <= 20, return 2**k((1-2**-k)-(E-r)), else
//        (vii) return 2**k(1-((E+2**-k)-r))
//
// Special cases:
//      expm1(INF) is INF, expm1(NaN) is NaN;
//      expm1(-INF) is -1, and
//      for finite argument, only expm1(0)=0 is exact.
//
// Accuracy:
//      according to an error analysis, the error is always less than
//      1 ulp (unit in the last place).
//
// Misc. info.
//      For IEEE double
//          if x >  7.09782712893383973096e+02 then expm1(x) overflow
//
// Constants:
// The hexadecimal values are the intended ones for the following
// constants. The decimal values may be used, provided that the
// compiler will convert from decimal to binary accurately enough
// to produce the hexadecimal values shown.
//

// Expm1 returns e**x - 1, the base-e exponential of x minus 1.
// It is more accurate than [Exp](x) - 1 when x is near zero.
//
// Special cases are:
//
//	Expm1(+Inf) = +Inf
//	Expm1(-Inf) = -1
//	Expm1(NaN) = NaN
//
// Very large values overflow to -1 or +Inf.
func Expm1(x float64) float64 {
	return expm1(x)
}

func expm1(x float64) float64 {
	const (
		Othreshold = 7.09782712893383973096e+02 // 0x40862E42FEFA39EF
		Ln2X56     = 3.88162421113569373274e+01 // 0x4043687a9f1af2b1
		Ln2HalfX3  = 1.03972077083991796413e+00 // 0x3ff0a2b23f3bab73
		Ln2Half    = 3.46573590279972654709e-01 // 0x3fd62e42fefa39ef
		Ln2Hi      = 6.93147180369123816490e-01 // 0x3fe62e42fee00000
		Ln2Lo      = 1.90821492927058770002e-10 // 0x3dea39ef35793c76
		InvLn2     = 1.44269504088896338700e+00 // 0x3ff71547652b82fe
		Tiny       = 1.0 / (1 << 54)            // 2**-54 = 0x3c90000000000000
		// scaled coefficients related to expm1
		Q1 = -3.33333333333331316428e-02 // 0xBFA11111111110F4
		Q2 = 1.58730158725481460165e-03  // 0x3F5A01A019FE5585
		Q3 = -7.93650757867487942473e-05 // 0xBF14CE199EAADBB7
		Q4 = 4.00821782732936239552e-06  // 0x3ED0CFCA86E65239
		Q5 = -2.01099218183624371326e-07 // 0xBE8AFDB76E09C32D
	)

	// special cases
	switch {
	case IsInf(x, 1) || IsNaN(x):
		return x
	case IsInf(x, -1):
		return -1
	}

	absx := x
	sign := false
	if x < 0 {
		absx = -absx
		sign = true
	}

	// filter out huge argument
	if absx >= Ln2X56 { // if |x| >= 56 * ln2
		if sign {
			return -1 // x < -56*ln2, return -1
		}
		if absx >= Othreshold { // if |x| >= 709.78...
			return Inf(1)
		}
	}

	// argument reduction
	var c float64
	var k int
	if absx > Ln2Half { // if  |x| > 0.5 * ln2
		var hi, lo float64
		if absx < Ln2HalfX3 { // and |x| < 1.5 * ln2
			if !sign {
				hi = x - Ln2Hi
				lo = Ln2Lo
				k = 1
			} else {
				hi = x + Ln2Hi
				lo = -Ln2Lo
				k = -1
			}
		} else {
			if !sign {
				k = int(InvLn2*x + 0.5)
			} else {
				k = int(InvLn2*x - 0.5)
			}
			t := float64(k)
			hi = x - t*Ln2Hi // t * Ln2Hi is exact here
			lo = t * Ln2Lo
		}
		x = hi - lo
		c = (hi - x) - lo
	} else if absx < Tiny { // when |x| < 2**-54, return x
		return x
	} else {
		k = 0
	}

	// x is now in primary range
	hfx := 0.5 * x
	hxs := x * hfx
	r1 := 1 + hxs*(Q1+hxs*(Q2+hxs*(Q3+hxs*(Q4+hxs*Q5))))
	t := 3 - r1*hfx
	e := hxs * ((r1 - t) / (6.0 - x*t))
	if k == 0 {
		return x - (x*e - hxs) // c is 0
	}
	e = (x*(e-c) - c)
	e -= hxs
	switch {
	case k == -1:
		return 0.5*(x-e) - 0.5
	case k == 1:
		if x < -0.25 {
			return -2 * (e - (x + 0.5))
		}
		return 1 + 2*(x-e)
	case k <= -2 || k > 56: // suffice to return exp(x)-1
		y := 1 - (e - x)
		y = Float64frombits(Float64bits(y) + uint64(k)<<52) // add k to y's exponent
		return y - 1
	}
	if k < 20 {
		t := Float64frombits(0x3ff0000000000000 - (0x20000000000000 >> uint(k))) // t=1-2**-k
		y := t - (e - x)
		y = Float64frombits(Float64bits(y) + uint64(k)<<52) // add k to y's exponent
		return y
	}
	t = Float64frombits(uint64(0x3ff-k) << 52) // 2**-k
	y := x - (e + t)
	y++
	y = Float64frombits(Float64bits(y) + uint64(k)<<52) // add k to y's exponent
	return y
}

// From Go's math/log1p.go.
// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/s_log1p.c
// and came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
//
// double log1p(double x)
//
// Method :
//   1. Argument Reduction: find k and f such that
//                      1+x = 2**k * (1+f),
//         where  sqrt(2)/2 < 1+f < sqrt(2) .
//
//      Note. If k=0, then f=x is exact. However, if k!=0, then f
//      may not be representable exactly. In that case, a correction
//      term is need. Let u=1+x rounded. Let c = (1+x)-u, then
//      log(1+x) - log(u) ~ c/u. Thus, we proceed to compute log(u),
//      and add back the correction term c/u.
//      (Note: when x > 2**53, one can simply return log(x))
//
//   2. Approximation of log1p(f).
//      Let s = f/(2+f) ; based on log(1+f) = log(1+s) - log(1-s)
//               = 2s + 2/3 s**3 + 2/5 s**5 + .....,
//               = 2s + s*R
//      We use a special Reme algorithm on [0,0.1716] to generate
//      a polynomial of degree 14 to approximate R The maximum error
//      of this polynomial approximation is bounded by 2**-58.45. In
//      other words,
//                      2      4      6      8      10      12      14
//          R(z) ~ Lp1*s +Lp2*s +Lp3*s +Lp4*s +Lp5*s  +Lp6*s  +Lp7*s
//      (the values of Lp1 to Lp7 are listed in the program)
//      and
//          |      2          14          |     -58.45
//          | Lp1*s +...+Lp7*s    -  R(z) | <= 2
//          |                             |
//      Note that 2s = f - s*f = f - hfsq + s*hfsq, where hfsq = f*f/2.
//      In order to guarantee error in log below 1ulp, we compute log
//      by
//              log1p(f) = f - (hfsq - s*(hfsq+R)).
//
//   3. Finally, log1p(x) = k*ln2 + log1p(f).
//                        = k*ln2_hi+(f-(hfsq-(s*(hfsq+R)+k*ln2_lo)))
//      Here ln2 is split into two floating point number:
//                   ln2_hi + ln2_lo,
//      where n*ln2_hi is always exact for |n| < 2000.
//
// Special cases:
//      log1p(x) is NaN with signal if x < -1 (including -INF) ;
//      log1p(+INF) is +INF; log1p(-1) is -INF with signal;
//      log1p(NaN) is that NaN with no signal.
//
// Accuracy:
//      according to an error analysis, the error is always less than
//      1 ulp (unit in the last place).
//
// Constants:
// The hexadecimal values are the intended ones for the following
// constants. The decimal values may be used, provided that the
// compiler will convert from decimal to binary accurately enough
// to produce the hexadecimal values shown.
//
// Note: Assuming log() return accurate answer, the following
//       algorithm can be used to compute log1p(x) to within a few ULP:
//
//              u = 1+x;
//              if(u==1.0) return x ; else
//                         return log(u)*(x/(u-1.0));
//
//       See HP-15C Advanced Functions Handbook, p.193.

// Log1p returns the natural logarithm of 1 plus its argument x.
// It is more accurate than [Log](1 + x) when x is near zero.
//
// Special cases are:
//
//	Log1p(+Inf) = +Inf
//	Log1p(±0) = ±0
//	Log1p(-1) = -Inf
//	Log1p(x < -1) = NaN
//	Log1p(NaN) = NaN
func Log1p(x float64) float64 {
	return log1p(x)
}

func log1p(x float64) float64 {
	const (
		Sqrt2M1     = 4.142135623730950488017e-01  // Sqrt(2)-1 = 0x3fda827999fcef34
		Sqrt2HalfM1 = -2.928932188134524755992e-01 // Sqrt(2)/2-1 = 0xbfd2bec333018866
		Small       = 1.0 / (1 << 29)              // 2**-29 = 0x3e20000000000000
		Tiny        = 1.0 / (1 << 54)              // 2**-54
		Two53       = 1 << 53                      // 2**53
		Ln2Hi       = 6.93147180369123816490e-01   // 3fe62e42fee00000
		Ln2Lo       = 1.90821492927058770002e-10   // 3dea39ef35793c76
		Lp1         = 6.666666666666735130e-01     // 3FE5555555555593
		Lp2         = 3.999999999940941908e-01     // 3FD999999997FA04
		Lp3         = 2.857142874366239149e-01     // 3FD2492494229359
		Lp4         = 2.222219843214978396e-01     // 3FCC71C51D8E78AF
		Lp5         = 1.818357216161805012e-01     // 3FC7466496CB03DE
		Lp6         = 1.531383769920937332e-01     // 3FC39A09D078C69F
		Lp7         = 1.479819860511658591e-01     // 3FC2F112DF3E5244
	)

	// special cases
	switch {
	case x < -1 || IsNaN(x): // includes -Inf
		return NaN()
	case x == -1:
		return Inf(-1)
	case IsInf(x, 1):
		return Inf(1)
	}

	absx := Abs(x)

	var f float64
	var iu uint64
	k := 1
	if absx < Sqrt2M1 { //  |x| < Sqrt(2)-1
		if absx < Small { // |x| < 2**-29
			if absx < Tiny { // |x| < 2**-54
				return x
			}
			return x - x*x*0.5
		}
		if x > Sqrt2HalfM1 { // Sqrt(2)/2-1 < x
			// (Sqrt(2)/2-1) < x < (Sqrt(2)-1)
			k = 0
			f = x
			iu = 1
		}
	}
	var c float64
	if k != 0 {
		var u float64
		if absx < Two53 { // 1<<53
			u = 1.0 + x
			iu = Float64bits(u)
			k = int((iu >> 52) - 1023)
			// correction term
			if k > 0 {
				c = 1.0 - (u - x)
			} else {
				c = x - (u - 1.0)
			}
			c /= u
		} else {
			u = x
			iu = Float64bits(u)
			k = int((iu >> 52) - 1023)
			c = 0
		}
		iu &= 0x000fffffffffffff
		if iu < 0x0006a09e667f3bcd { // mantissa of Sqrt(2)
			u = Float64frombits(iu | 0x3ff0000000000000) // normalize u
		} else {
			k++
			u = Float64frombits(iu | 0x3fe0000000000000) // normalize u/2
			iu = (0x0010000000000000 - iu) >> 2
		}
		f = u - 1.0 // Sqrt(2)/2 < u < Sqrt(2)
	}
	hfsq := 0.5 * f * f
	var s, R, z float64
	if iu == 0 { // |f| < 2**-20
		if f == 0 {
			if k == 0 {
				return 0
			}
			c += float64(k) * Ln2Lo
			return float64(k)*Ln2Hi + c
		}
		R = hfsq * (1.0 - 0.66666666666666666*f) // avoid division
		if k == 0 {
			return f - R
		}
		return float64(k)*Ln2Hi - ((R - (float64(k)*Ln2Lo + c)) - f)
	}
	s = f / (2.0 + f)
	z = s * s
	R = z * (Lp1 + z*(Lp2+z*(Lp3+z*(Lp4+z*(Lp5+z*(Lp6+z*Lp7))))))
	if k == 0 {
		return f - (hfsq - s*(hfsq+R))
	}
	return float64(k)*Ln2Hi - ((hfsq - (s*(hfsq+R) + (float64(k)*Ln2Lo + c))) - f)
}

// From Go's math/logb.go.
// Logb returns the binary exponent of x.
//
// Special cases are:
//
//	Logb(±Inf) = +Inf
//	Logb(0) = -Inf
//	Logb(NaN) = NaN
func Logb(x float64) float64 {
	// special cases
	switch {
	case x == 0:
		return Inf(-1)
	case IsInf(x, 0):
		return Inf(1)
	case IsNaN(x):
		return x
	}
	return float64(ilogb(x))
}

// Ilogb returns the binary exponent of x as an integer.
//
// Special cases are:
//
//	Ilogb(±Inf) = MaxInt32
//	Ilogb(0) = MinInt32
//	Ilogb(NaN) = MaxInt32
func Ilogb(x float64) int {
	// special cases
	switch {
	case x == 0:
		return MinInt32
	case IsNaN(x):
		return MaxInt32
	case IsInf(x, 0):
		return MaxInt32
	}
	return ilogb(x)
}

// ilogb returns the binary exponent of x. It assumes x is finite and
// non-zero.
func ilogb(x float64) int {
	x, exp := normalize(x)
	return int((Float64bits(x)>>shift)&mask) - bias + exp
}

// From Go's math/remainder.go.
// The original C code and the comment below are from
// FreeBSD's /usr/src/lib/msun/src/e_remainder.c and came
// with this notice. The go code is a simplified version of
// the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// __ieee754_remainder(x,y)
// Return :
//      returns  x REM y  =  x - [x/y]*y  as if in infinite
//      precision arithmetic, where [x/y] is the (infinite bit)
//      integer nearest x/y (in half way cases, choose the even one).
// Method :
//      Based on Mod() returning  x - [x/y]chopped * y  exactly.

// Remainder returns the IEEE 754 floating-point remainder of x/y.
//
// Special cases are:
//
//	Remainder(±Inf, y) = NaN
//	Remainder(NaN, y) = NaN
//	Remainder(x, 0) = NaN
//	Remainder(x, ±Inf) = x
//	Remainder(x, NaN) = NaN
func Remainder(x, y float64) float64 {
	return remainder(x, y)
}

func remainder(x, y float64) float64 {
	const (
		Tiny    = 4.45014771701440276618e-308 // 0x0020000000000000
		HalfMax = MaxFloat64 / 2
	)
	// special cases
	switch {
	case IsNaN(x) || IsNaN(y) || IsInf(x, 0) || y == 0:
		return NaN()
	case IsInf(y, 0):
		return x
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	if y < 0 {
		y = -y
	}
	if x == y {
		if sign {
			zero := 0.0
			return -zero
		}
		return 0
	}
	if y <= HalfMax {
		x = Mod(x, y+y) // now x < 2y
	}
	if y < Tiny {
		if x+x > y {
			x -= y
			if x+x >= y {
				x -= y
			}
		}
	} else {
		yHalf := 0.5 * y
		if x > yHalf {
			x -= y
			if x >= yHalf {
				x -= y
			}
		}
	}
	if sign {
		x = -x
	}
	return x
}

// From Go's math/nextafter.go.
// Nextafter32 returns the next representable float32 value after x towards y.
//
// Special cases are:
//
//	Nextafter32(x, y)   = x when x == y
//	Nextafter32(0, y)   = ±SmallestNonzeroFloat32 towards y, for y ≠ 0
//	Nextafter32(NaN, y) = NaN
//	Nextafter32(x, NaN) = NaN
func Nextafter32(x, y float32) (r float32) {
	switch {
	case IsNaN(float64(x)) || IsNaN(float64(y)): // special case
		r = float32(NaN())
	case x == y:
		r = x
	case x == 0:
		r = float32(Copysign(float64(Float32frombits(1)), float64(y)))
	case (y > x) == (x > 0):
		r = Float32frombits(Float32bits(x) + 1)
	default:
		r = Float32frombits(Float32bits(x) - 1)
	}
	return
}

// Nextafter returns the next representable float64 value after x towards y.
//
// Special cases are:
//
//	Nextafter(x, y)   = x when x == y
//	Nextafter(0, y)   = ±SmallestNonzeroFloat64 towards y, for y ≠ 0
//	Nextafter(NaN, y) = NaN
//	Nextafter(x, NaN) = NaN
func Nextafter(x, y float64) (r float64) {
	switch {
	case IsNaN(x) || IsNaN(y): // special case
		r = NaN()
	case x == y:
		r = x
	case x == 0:
		r = Copysign(Float64frombits(1), y)
	case (y > x) == (x > 0):
		r = Float64frombits(Float64bits(x) + 1)
	default:
		r = Float64frombits(Float64bits(x) - 1)
	}
	return
}

// From Go's math/pow10.go.
// pow10tab stores the pre-computed values 10**i for i < 32.
var pow10tab = [...]float64{
	1e00, 1e01, 1e02, 1e03, 1e04, 1e05, 1e06, 1e07, 1e08, 1e09,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19,
	1e20, 1e21, 1e22, 1e23, 1e24, 1e25, 1e26, 1e27, 1e28, 1e29,
	1e30, 1e31,
}

// pow10postab32 stores the pre-computed value for 10**(i*32) at index i.
var pow10postab32 = [...]float64{
	1e00, 1e32, 1e64, 1e96, 1e128, 1e160, 1e192, 1e224, 1e256, 1e288,
}

// pow10negtab32 stores the pre-computed value for 10**(-i*32) at index i.
var pow10negtab32 = [...]float64{
	1e-00, 1e-32, 1e-64, 1e-96, 1e-128, 1e-160, 1e-192, 1e-224, 1e-256, 1e-288, 1e-320,
}

// Pow10 returns 10**n, the base-10 exponential of n.
//
// Special cases are:
//
//	Pow10(n) =    0 for n < -323
//	Pow10(n) = +Inf for n > 308
func Pow10(n int) float64 {
	if 0 <= n && n <= 308 {
		return pow10postab32[uint(n)/32] * pow10tab[uint(n)%32]
	}

	if -323 <= n && n < 0 {
		return pow10negtab32[uint(-n)/32] / pow10tab[uint(-n)%32]
	}

	// n < -323 || 308 < n
	if n > 0 {
		return Inf(1)
	}

	// n < -323
	return 0
}

// From Go's math/sincos.go.
// Coefficients _sin[] and _cos[] are found in pkg/math/sin.go.

// Sincos returns Sin(x), Cos(x).
//
// Special cases are:
//
//	Sincos(±0) = ±0, 1
//	Sincos(±Inf) = NaN, NaN
//	Sincos(NaN) = NaN, NaN
func Sincos(x float64) (sin, cos float64) {
	const (
		PI4A = 7.85398125648498535156e-1  // 0x3fe921fb40000000, Pi/4 split into three parts
		PI4B = 3.77489470793079817668e-8  // 0x3e64442d00000000,
		PI4C = 2.69515142907905952645e-15 // 0x3ce8469898cc5170,
	)
	// special cases
	switch {
	case x == 0:
		return x, 1 // return ±0.0, 1.0
	case IsNaN(x) || IsInf(x, 0):
		return NaN(), NaN()
	}

	// make argument positive
	sinSign, cosSign := false, false
	if x < 0 {
		x = -x
		sinSign = true
	}

	var j uint64
	var y, z float64
	if x >= reduceThreshold {
		j, z = trigReduce(x)
	} else {
		j = uint64(x * (4 / Pi)) // integer part of x/(Pi/4), as integer for tests on the phase angle
		y = float64(j)           // integer part of x/(Pi/4), as float

		if j&1 == 1 { // map zeros to origin
			j++
			y++
		}
		j &= 7                               // octant modulo 2Pi radians (360 degrees)
		z = ((x - y*PI4A) - y*PI4B) - y*PI4C // Extended precision modular arithmetic
	}
	if j > 3 { // reflect in x axis
		j -= 4
		sinSign, cosSign = !sinSign, !cosSign
	}
	if j > 1 {
		cosSign = !cosSign
	}

	zz := z * z
	cos = 1.0 - 0.5*zz + zz*zz*((((((_cos[0]*zz)+_cos[1])*zz+_cos[2])*zz+_cos[3])*zz+_cos[4])*zz+_cos[5])
	sin = z + z*zz*((((((_sin[0]*zz)+_sin[1])*zz+_sin[2])*zz+_sin[3])*zz+_sin[4])*zz+_sin[5])
	if j == 1 || j == 2 {
		sin, cos = cos, sin
	}
	if cosSign {
		cos = -cos
	}
	if sinSign {
		sin = -sin
	}
	return
}

// From Go's math/erf.go.
/*
	Floating-point error function and complementary error function.
*/

// The original C code and the long comment below are
// from FreeBSD's /usr/src/lib/msun/src/s_erf.c and
// came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
//
// double erf(double x)
// double erfc(double x)
//                           x
//                    2      |\
//     erf(x)  =  ---------  | exp(-t*t)dt
//                 sqrt(pi) \|
//                           0
//
//     erfc(x) =  1-erf(x)
//  Note that
//              erf(-x) = -erf(x)
//              erfc(-x) = 2 - erfc(x)
//
// Method:
//      1. For |x| in [0, 0.84375]
//          erf(x)  = x + x*R(x**2)
//          erfc(x) = 1 - erf(x)           if x in [-.84375,0.25]
//                  = 0.5 + ((0.5-x)-x*R)  if x in [0.25,0.84375]
//         where R = P/Q where P is an odd poly of degree 8 and
//         Q is an odd poly of degree 10.
//                                               -57.90
//                      | R - (erf(x)-x)/x | <= 2
//
//
//         Remark. The formula is derived by noting
//          erf(x) = (2/sqrt(pi))*(x - x**3/3 + x**5/10 - x**7/42 + ....)
//         and that
//          2/sqrt(pi) = 1.128379167095512573896158903121545171688
//         is close to one. The interval is chosen because the fix
//         point of erf(x) is near 0.6174 (i.e., erf(x)=x when x is
//         near 0.6174), and by some experiment, 0.84375 is chosen to
//         guarantee the error is less than one ulp for erf.
//
//      2. For |x| in [0.84375,1.25], let s = |x| - 1, and
//         c = 0.84506291151 rounded to single (24 bits)
//              erf(x)  = sign(x) * (c  + P1(s)/Q1(s))
//              erfc(x) = (1-c)  - P1(s)/Q1(s) if x > 0
//                        1+(c+P1(s)/Q1(s))    if x < 0
//              |P1/Q1 - (erf(|x|)-c)| <= 2**-59.06
//         Remark: here we use the taylor series expansion at x=1.
//              erf(1+s) = erf(1) + s*Poly(s)
//                       = 0.845.. + P1(s)/Q1(s)
//         That is, we use rational approximation to approximate
//                      erf(1+s) - (c = (single)0.84506291151)
//         Note that |P1/Q1|< 0.078 for x in [0.84375,1.25]
//         where
//              P1(s) = degree 6 poly in s
//              Q1(s) = degree 6 poly in s
//
//      3. For x in [1.25,1/0.35(~2.857143)],
//              erfc(x) = (1/x)*exp(-x*x-0.5625+R1/S1)
//              erf(x)  = 1 - erfc(x)
//         where
//              R1(z) = degree 7 poly in z, (z=1/x**2)
//              S1(z) = degree 8 poly in z
//
//      4. For x in [1/0.35,28]
//              erfc(x) = (1/x)*exp(-x*x-0.5625+R2/S2) if x > 0
//                      = 2.0 - (1/x)*exp(-x*x-0.5625+R2/S2) if -6<x<0
//                      = 2.0 - tiny            (if x <= -6)
//              erf(x)  = sign(x)*(1.0 - erfc(x)) if x < 6, else
//              erf(x)  = sign(x)*(1.0 - tiny)
//         where
//              R2(z) = degree 6 poly in z, (z=1/x**2)
//              S2(z) = degree 7 poly in z
//
//      Note1:
//         To compute exp(-x*x-0.5625+R/S), let s be a single
//         precision number and s := x; then
//              -x*x = -s*s + (s-x)*(s+x)
//              exp(-x*x-0.5626+R/S) =
//                      exp(-s*s-0.5625)*exp((s-x)*(s+x)+R/S);
//      Note2:
//         Here 4 and 5 make use of the asymptotic series
//                        exp(-x*x)
//              erfc(x) ~ ---------- * ( 1 + Poly(1/x**2) )
//                        x*sqrt(pi)
//         We use rational approximation to approximate
//              g(s)=f(1/x**2) = log(erfc(x)*x) - x*x + 0.5625
//         Here is the error bound for R1/S1 and R2/S2
//              |R1/S1 - f(x)|  < 2**(-62.57)
//              |R2/S2 - f(x)|  < 2**(-61.52)
//
//      5. For inf > x >= 28
//              erf(x)  = sign(x) *(1 - tiny)  (raise inexact)
//              erfc(x) = tiny*tiny (raise underflow) if x > 0
//                      = 2 - tiny if x<0
//
//      7. Special case:
//              erf(0)  = 0, erf(inf)  = 1, erf(-inf) = -1,
//              erfc(0) = 1, erfc(inf) = 0, erfc(-inf) = 2,
//              erfc/erf(NaN) is NaN

const (
	erx = 8.45062911510467529297e-01 // 0x3FEB0AC160000000
	// Coefficients for approximation to  erf in [0, 0.84375]
	efx  = 1.28379167095512586316e-01  // 0x3FC06EBA8214DB69
	efx8 = 1.02703333676410069053e+00  // 0x3FF06EBA8214DB69
	pp0  = 1.28379167095512558561e-01  // 0x3FC06EBA8214DB68
	pp1  = -3.25042107247001499370e-01 // 0xBFD4CD7D691CB913
	pp2  = -2.84817495755985104766e-02 // 0xBF9D2A51DBD7194F
	pp3  = -5.77027029648944159157e-03 // 0xBF77A291236668E4
	pp4  = -2.37630166566501626084e-05 // 0xBEF8EAD6120016AC
	qq1  = 3.97917223959155352819e-01  // 0x3FD97779CDDADC09
	qq2  = 6.50222499887672944485e-02  // 0x3FB0A54C5536CEBA
	qq3  = 5.08130628187576562776e-03  // 0x3F74D022C4D36B0F
	qq4  = 1.32494738004321644526e-04  // 0x3F215DC9221C1A10
	qq5  = -3.96022827877536812320e-06 // 0xBED09C4342A26120
	// Coefficients for approximation to  erf  in [0.84375, 1.25]
	pa0 = -2.36211856075265944077e-03 // 0xBF6359B8BEF77538
	pa1 = 4.14856118683748331666e-01  // 0x3FDA8D00AD92B34D
	pa2 = -3.72207876035701323847e-01 // 0xBFD7D240FBB8C3F1
	pa3 = 3.18346619901161753674e-01  // 0x3FD45FCA805120E4
	pa4 = -1.10894694282396677476e-01 // 0xBFBC63983D3E28EC
	pa5 = 3.54783043256182359371e-02  // 0x3FA22A36599795EB
	pa6 = -2.16637559486879084300e-03 // 0xBF61BF380A96073F
	qa1 = 1.06420880400844228286e-01  // 0x3FBB3E6618EEE323
	qa2 = 5.40397917702171048937e-01  // 0x3FE14AF092EB6F33
	qa3 = 7.18286544141962662868e-02  // 0x3FB2635CD99FE9A7
	qa4 = 1.26171219808761642112e-01  // 0x3FC02660E763351F
	qa5 = 1.36370839120290507362e-02  // 0x3F8BEDC26B51DD1C
	qa6 = 1.19844998467991074170e-02  // 0x3F888B545735151D
	// Coefficients for approximation to  erfc in [1.25, 1/0.35]
	ra0 = -9.86494403484714822705e-03 // 0xBF843412600D6435
	ra1 = -6.93858572707181764372e-01 // 0xBFE63416E4BA7360
	ra2 = -1.05586262253232909814e+01 // 0xC0251E0441B0E726
	ra3 = -6.23753324503260060396e+01 // 0xC04F300AE4CBA38D
	ra4 = -1.62396669462573470355e+02 // 0xC0644CB184282266
	ra5 = -1.84605092906711035994e+02 // 0xC067135CEBCCABB2
	ra6 = -8.12874355063065934246e+01 // 0xC054526557E4D2F2
	ra7 = -9.81432934416914548592e+00 // 0xC023A0EFC69AC25C
	sa1 = 1.96512716674392571292e+01  // 0x4033A6B9BD707687
	sa2 = 1.37657754143519042600e+02  // 0x4061350C526AE721
	sa3 = 4.34565877475229228821e+02  // 0x407B290DD58A1A71
	sa4 = 6.45387271733267880336e+02  // 0x40842B1921EC2868
	sa5 = 4.29008140027567833386e+02  // 0x407AD02157700314
	sa6 = 1.08635005541779435134e+02  // 0x405B28A3EE48AE2C
	sa7 = 6.57024977031928170135e+00  // 0x401A47EF8E484A93
	sa8 = -6.04244152148580987438e-02 // 0xBFAEEFF2EE749A62
	// Coefficients for approximation to  erfc in [1/.35, 28]
	rb0 = -9.86494292470009928597e-03 // 0xBF84341239E86F4A
	rb1 = -7.99283237680523006574e-01 // 0xBFE993BA70C285DE
	rb2 = -1.77579549177547519889e+01 // 0xC031C209555F995A
	rb3 = -1.60636384855821916062e+02 // 0xC064145D43C5ED98
	rb4 = -6.37566443368389627722e+02 // 0xC083EC881375F228
	rb5 = -1.02509513161107724954e+03 // 0xC09004616A2E5992
	rb6 = -4.83519191608651397019e+02 // 0xC07E384E9BDC383F
	sb1 = 3.03380607434824582924e+01  // 0x403E568B261D5190
	sb2 = 3.25792512996573918826e+02  // 0x40745CAE221B9F0A
	sb3 = 1.53672958608443695994e+03  // 0x409802EB189D5118
	sb4 = 3.19985821950859553908e+03  // 0x40A8FFB7688C246A
	sb5 = 2.55305040643316442583e+03  // 0x40A3F219CEDF3BE6
	sb6 = 4.74528541206955367215e+02  // 0x407DA874E79FE763
	sb7 = -2.24409524465858183362e+01 // 0xC03670E242712D62
)

// Erf returns the error function of x.
//
// Special cases are:
//
//	Erf(+Inf) = 1
//	Erf(-Inf) = -1
//	Erf(NaN) = NaN
func Erf(x float64) float64 {
	return erf(x)
}

func erf(x float64) float64 {
	const (
		VeryTiny = 2.848094538889218e-306 // 0x0080000000000000
		Small    = 1.0 / (1 << 28)        // 2**-28
	)
	// special cases
	switch {
	case IsNaN(x):
		return NaN()
	case IsInf(x, 1):
		return 1
	case IsInf(x, -1):
		return -1
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	if x < 0.84375 { // |x| < 0.84375
		var temp float64
		if x < Small { // |x| < 2**-28
			if x < VeryTiny {
				temp = 0.125 * (8.0*x + efx8*x) // avoid underflow
			} else {
				temp = x + efx*x
			}
		} else {
			z := x * x
			r := pp0 + z*(pp1+z*(pp2+z*(pp3+z*pp4)))
			s := 1 + z*(qq1+z*(qq2+z*(qq3+z*(qq4+z*qq5))))
			y := r / s
			temp = x + x*y
		}
		if sign {
			return -temp
		}
		return temp
	}
	if x < 1.25 { // 0.84375 <= |x| < 1.25
		s := x - 1
		P := pa0 + s*(pa1+s*(pa2+s*(pa3+s*(pa4+s*(pa5+s*pa6)))))
		Q := 1 + s*(qa1+s*(qa2+s*(qa3+s*(qa4+s*(qa5+s*qa6)))))
		if sign {
			return -erx - P/Q
		}
		return erx + P/Q
	}
	if x >= 6 { // inf > |x| >= 6
		if sign {
			return -1
		}
		return 1
	}
	s := 1 / (x * x)
	var R, S float64
	if x < 1/0.35 { // |x| < 1 / 0.35  ~ 2.857143
		R = ra0 + s*(ra1+s*(ra2+s*(ra3+s*(ra4+s*(ra5+s*(ra6+s*ra7))))))
		S = 1 + s*(sa1+s*(sa2+s*(sa3+s*(sa4+s*(sa5+s*(sa6+s*(sa7+s*sa8)))))))
	} else { // |x| >= 1 / 0.35  ~ 2.857143
		R = rb0 + s*(rb1+s*(rb2+s*(rb3+s*(rb4+s*(rb5+s*rb6)))))
		S = 1 + s*(sb1+s*(sb2+s*(sb3+s*(sb4+s*(sb5+s*(sb6+s*sb7))))))
	}
	z := Float64frombits(Float64bits(x) & 0xffffffff00000000) // pseudo-single (20-bit) precision x
	r := Exp(-z*z-0.5625) * Exp((z-x)*(z+x)+R/S)
	if sign {
		return r/x - 1
	}
	return 1 - r/x
}

// Erfc returns the complementary error function of x.
//
// Special cases are:
//
//	Erfc(+Inf) = 0
//	Erfc(-Inf) = 2
//	Erfc(NaN) = NaN
func Erfc(x float64) float64 {
	return erfc(x)
}

func erfc(x float64) float64 {
	const Tiny = 1.0 / (1 << 56) // 2**-56
	// special cases
	switch {
	case IsNaN(x):
		return NaN()
	case IsInf(x, 1):
		return 0
	case IsInf(x, -1):
		return 2
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	if x < 0.84375 { // |x| < 0.84375
		var temp float64
		if x < Tiny { // |x| < 2**-56
			temp = x
		} else {
			z := x * x
			r := pp0 + z*(pp1+z*(pp2+z*(pp3+z*pp4)))
			s := 1 + z*(qq1+z*(qq2+z*(qq3+z*(qq4+z*qq5))))
			y := r / s
			if x < 0.25 { // |x| < 1/4
				temp = x + x*y
			} else {
				temp = 0.5 + (x*y + (x - 0.5))
			}
		}
		if sign {
			return 1 + temp
		}
		return 1 - temp
	}
	if x < 1.25 { // 0.84375 <= |x| < 1.25
		s := x - 1
		P := pa0 + s*(pa1+s*(pa2+s*(pa3+s*(pa4+s*(pa5+s*pa6)))))
		Q := 1 + s*(qa1+s*(qa2+s*(qa3+s*(qa4+s*(qa5+s*qa6)))))
		if sign {
			return 1 + erx + P/Q
		}
		return 1 - erx - P/Q

	}
	if x < 28 { // |x| < 28
		s := 1 / (x * x)
		var R, S float64
		if x < 1/0.35 { // |x| < 1 / 0.35 ~ 2.857143
			R = ra0 + s*(ra1+s*(ra2+s*(ra3+s*(ra4+s*(ra5+s*(ra6+s*ra7))))))
			S = 1 + s*(sa1+s*(sa2+s*(sa3+s*(sa4+s*(sa5+s*(sa6+s*(sa7+s*sa8)))))))
		} else { // |x| >= 1 / 0.35 ~ 2.857143
			if sign && x > 6 {
				return 2 // x < -6
			}
			R = rb0 + s*(rb1+s*(rb2+s*(rb3+s*(rb4+s*(rb5+s*rb6)))))
			S = 1 + s*(sb1+s*(sb2+s*(sb3+s*(sb4+s*(sb5+s*(sb6+s*sb7))))))
		}
		z := Float64frombits(Float64bits(x) & 0xffffffff00000000) // pseudo-single (20-bit) precision x
		r := Exp(-z*z-0.5625) * Exp((z-x)*(z+x)+R/S)
		if sign {
			return 2 - r/x
		}
		return r / x
	}
	if sign {
		return 2
	}
	return 0
}

// From Go's math/erfinv.go.
/*
	Inverse of the floating-point error function.
*/

// This implementation is based on the rational approximation
// of percentage points of normal distribution available from
// https://www.jstor.org/stable/2347330.

const (
	// Coefficients for approximation to erf in |x| <= 0.85
	a0 = 1.1975323115670912564578e0
	a1 = 4.7072688112383978012285e1
	a2 = 6.9706266534389598238465e2
	a3 = 4.8548868893843886794648e3
	a4 = 1.6235862515167575384252e4
	a5 = 2.3782041382114385731252e4
	a6 = 1.1819493347062294404278e4
	a7 = 8.8709406962545514830200e2
	b0 = 1.0000000000000000000e0
	b1 = 4.2313330701600911252e1
	b2 = 6.8718700749205790830e2
	b3 = 5.3941960214247511077e3
	b4 = 2.1213794301586595867e4
	b5 = 3.9307895800092710610e4
	b6 = 2.8729085735721942674e4
	b7 = 5.2264952788528545610e3
	// Coefficients for approximation to erf in 0.85 < |x| <= 1-2*exp(-25)
	c0 = 1.42343711074968357734e0
	c1 = 4.63033784615654529590e0
	c2 = 5.76949722146069140550e0
	c3 = 3.64784832476320460504e0
	c4 = 1.27045825245236838258e0
	c5 = 2.41780725177450611770e-1
	c6 = 2.27238449892691845833e-2
	c7 = 7.74545014278341407640e-4
	d0 = 1.4142135623730950488016887e0
	d1 = 2.9036514445419946173133295e0
	d2 = 2.3707661626024532365971225e0
	d3 = 9.7547832001787427186894837e-1
	d4 = 2.0945065210512749128288442e-1
	d5 = 2.1494160384252876777097297e-2
	d6 = 7.7441459065157709165577218e-4
	d7 = 1.4859850019840355905497876e-9
	// Coefficients for approximation to erf in 1-2*exp(-25) < |x| < 1
	e0 = 6.65790464350110377720e0
	e1 = 5.46378491116411436990e0
	e2 = 1.78482653991729133580e0
	e3 = 2.96560571828504891230e-1
	e4 = 2.65321895265761230930e-2
	e5 = 1.24266094738807843860e-3
	e6 = 2.71155556874348757815e-5
	e7 = 2.01033439929228813265e-7
	f0 = 1.414213562373095048801689e0
	f1 = 8.482908416595164588112026e-1
	f2 = 1.936480946950659106176712e-1
	f3 = 2.103693768272068968719679e-2
	f4 = 1.112800997078859844711555e-3
	f5 = 2.611088405080593625138020e-5
	f6 = 2.010321207683943062279931e-7
	f7 = 2.891024605872965461538222e-15
)

// Erfinv returns the inverse error function of x.
//
// Special cases are:
//
//	Erfinv(1) = +Inf
//	Erfinv(-1) = -Inf
//	Erfinv(x) = NaN if x < -1 or x > 1
//	Erfinv(NaN) = NaN
func Erfinv(x float64) float64 {
	// special cases
	if IsNaN(x) || x <= -1 || x >= 1 {
		if x == -1 || x == 1 {
			return Inf(int(x))
		}
		return NaN()
	}

	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	var ans float64
	if x <= 0.85 { // |x| <= 0.85
		r := 0.180625 - 0.25*x*x
		z1 := ((((((a7*r+a6)*r+a5)*r+a4)*r+a3)*r+a2)*r+a1)*r + a0
		z2 := ((((((b7*r+b6)*r+b5)*r+b4)*r+b3)*r+b2)*r+b1)*r + b0
		ans = (x * z1) / z2
	} else {
		var z1, z2 float64
		r := Sqrt(Ln2 - Log(1.0-x))
		if r <= 5.0 {
			r -= 1.6
			z1 = ((((((c7*r+c6)*r+c5)*r+c4)*r+c3)*r+c2)*r+c1)*r + c0
			z2 = ((((((d7*r+d6)*r+d5)*r+d4)*r+d3)*r+d2)*r+d1)*r + d0
		} else {
			r -= 5.0
			z1 = ((((((e7*r+e6)*r+e5)*r+e4)*r+e3)*r+e2)*r+e1)*r + e0
			z2 = ((((((f7*r+f6)*r+f5)*r+f4)*r+f3)*r+f2)*r+f1)*r + f0
		}
		ans = z1 / z2
	}

	if sign {
		return -ans
	}
	return ans
}

// Erfcinv returns the inverse of [Erfc](x).
//
// Special cases are:
//
//	Erfcinv(0) = +Inf
//	Erfcinv(2) = -Inf
//	Erfcinv(x) = NaN if x < 0 or x > 2
//	Erfcinv(NaN) = NaN
func Erfcinv(x float64) float64 {
	return Erfinv(1 - x)
}

// From Go's math/gamma.go.
// The original C code, the long comment, and the constants
// below are from http://netlib.sandia.gov/cephes/cprob/gamma.c.
// The go code is a simplified version of the original C.
//
//      tgamma.c
//
//      Gamma function
//
// SYNOPSIS:
//
// double x, y, tgamma();
// extern int signgam;
//
// y = tgamma( x );
//
// DESCRIPTION:
//
// Returns gamma function of the argument. The result is
// correctly signed, and the sign (+1 or -1) is also
// returned in a global (extern) variable named signgam.
// This variable is also filled in by the logarithmic gamma
// function lgamma().
//
// Arguments |x| <= 34 are reduced by recurrence and the function
// approximated by a rational function of degree 6/7 in the
// interval (2,3).  Large arguments are handled by Stirling's
// formula. Large negative arguments are made positive using
// a reflection formula.
//
// ACCURACY:
//
//                      Relative error:
// arithmetic   domain     # trials      peak         rms
//    DEC      -34, 34      10000       1.3e-16     2.5e-17
//    IEEE    -170,-33      20000       2.3e-15     3.3e-16
//    IEEE     -33,  33     20000       9.4e-16     2.2e-16
//    IEEE      33, 171.6   20000       2.3e-15     3.2e-16
//
// Error for arguments outside the test range will be larger
// owing to error amplification by the exponential function.
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// The readme file at http://netlib.sandia.gov/cephes/ says:
//    Some software in this archive may be from the book _Methods and
// Programs for Mathematical Functions_ (Prentice-Hall or Simon & Schuster
// International, 1989) or from the Cephes Mathematical Library, a
// commercial product. In either event, it is copyrighted by the author.
// What you see here may be used freely but it comes with no support or
// guarantee.
//
//   The two known misprints in the book are repaired here in the
// source listings for the gamma function and the incomplete beta
// integral.
//
//   Stephen L. Moshier
//   moshier@na-net.ornl.gov

var _gamP = [...]float64{
	1.60119522476751861407e-04,
	1.19135147006586384913e-03,
	1.04213797561761569935e-02,
	4.76367800457137231464e-02,
	2.07448227648435975150e-01,
	4.94214826801497100753e-01,
	9.99999999999999996796e-01,
}
var _gamQ = [...]float64{
	-2.31581873324120129819e-05,
	5.39605580493303397842e-04,
	-4.45641913851797240494e-03,
	1.18139785222060435552e-02,
	3.58236398605498653373e-02,
	-2.34591795718243348568e-01,
	7.14304917030273074085e-02,
	1.00000000000000000320e+00,
}
var _gamS = [...]float64{
	7.87311395793093628397e-04,
	-2.29549961613378126380e-04,
	-2.68132617805781232825e-03,
	3.47222221605458667310e-03,
	8.33333333333482257126e-02,
}

// Gamma function computed by Stirling's formula.
// The pair of results must be multiplied together to get the actual answer.
// The multiplication is left to the caller so that, if careful, the caller can avoid
// infinity for 172 <= x <= 180.
// The polynomial is valid for 33 <= x <= 172; larger values are only used
// in reciprocal and produce denormalized floats. The lower precision there
// masks any imprecision in the polynomial.
func stirling(x float64) (float64, float64) {
	if x > 200 {
		return Inf(1), 1
	}
	const (
		SqrtTwoPi   = 2.506628274631000502417
		MaxStirling = 143.01608
	)
	w := 1 / x
	w = 1 + w*((((_gamS[0]*w+_gamS[1])*w+_gamS[2])*w+_gamS[3])*w+_gamS[4])
	y1 := Exp(x)
	y2 := 1.0
	if x > MaxStirling { // avoid Pow() overflow
		v := Pow(x, 0.5*x-0.25)
		y1, y2 = v, v/y1
	} else {
		y1 = Pow(x, x-0.5) / y1
	}
	return y1, SqrtTwoPi * w * y2
}

// Gamma returns the Gamma function of x.
//
// Special cases are:
//
//	Gamma(+Inf) = +Inf
//	Gamma(+0) = +Inf
//	Gamma(-0) = -Inf
//	Gamma(x) = NaN for integer x < 0
//	Gamma(-Inf) = NaN
//	Gamma(NaN) = NaN
func Gamma(x float64) float64 {
	const Euler = 0.57721566490153286060651209008240243104215933593992 // A001620
	// special cases
	switch {
	case isNegInt(x) || IsInf(x, -1) || IsNaN(x):
		return NaN()
	case IsInf(x, 1):
		return Inf(1)
	case x == 0:
		if Signbit(x) {
			return Inf(-1)
		}
		return Inf(1)
	}
	q := Abs(x)
	p := Floor(q)
	if q > 33 {
		if x >= 0 {
			y1, y2 := stirling(x)
			return y1 * y2
		}
		// Note: x is negative but (checked above) not a negative integer,
		// so x must be small enough to be in range for conversion to int64.
		// If |x| were >= 2⁶³ it would have to be an integer.
		signgam := 1
		if ip := int64(p); ip&1 == 0 {
			signgam = -1
		}
		z := q - p
		if z > 0.5 {
			p = p + 1
			z = q - p
		}
		z = q * Sin(Pi*z)
		if z == 0 {
			return Inf(signgam)
		}
		sq1, sq2 := stirling(q)
		absz := Abs(z)
		d := absz * sq1 * sq2
		if IsInf(d, 0) {
			z = Pi / absz / sq1 / sq2
		} else {
			z = Pi / d
		}
		return float64(signgam) * z
	}

	// Reduce argument
	z := 1.0
	for x >= 3 {
		x = x - 1
		z = z * x
	}
	for x < 0 {
		if x > -1e-09 {
			goto small
		}
		z = z / x
		x = x + 1
	}
	for x < 2 {
		if x < 1e-09 {
			goto small
		}
		z = z / x
		x = x + 1
	}

	if x == 2 {
		return z
	}

	x = x - 2
	p = (((((x*_gamP[0]+_gamP[1])*x+_gamP[2])*x+_gamP[3])*x+_gamP[4])*x+_gamP[5])*x + _gamP[6]
	q = ((((((x*_gamQ[0]+_gamQ[1])*x+_gamQ[2])*x+_gamQ[3])*x+_gamQ[4])*x+_gamQ[5])*x+_gamQ[6])*x + _gamQ[7]
	return z * p / q

small:
	if x == 0 {
		return Inf(1)
	}
	return z / ((1 + Euler*x) * x)
}

func isNegInt(x float64) bool {
	if x < 0 {
		_, xf := Modf(x)
		return xf == 0
	}
	return false
}

// From Go's math/lgamma.go.
/*
	Floating-point logarithm of the Gamma function.
*/

// The original C code and the long comment below are
// from FreeBSD's /usr/src/lib/msun/src/e_lgamma_r.c and
// came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// __ieee754_lgamma_r(x, signgamp)
// Reentrant version of the logarithm of the Gamma function
// with user provided pointer for the sign of Gamma(x).
//
// Method:
//   1. Argument Reduction for 0 < x <= 8
//      Since gamma(1+s)=s*gamma(s), for x in [0,8], we may
//      reduce x to a number in [1.5,2.5] by
//              lgamma(1+s) = log(s) + lgamma(s)
//      for example,
//              lgamma(7.3) = log(6.3) + lgamma(6.3)
//                          = log(6.3*5.3) + lgamma(5.3)
//                          = log(6.3*5.3*4.3*3.3*2.3) + lgamma(2.3)
//   2. Polynomial approximation of lgamma around its
//      minimum (ymin=1.461632144968362245) to maintain monotonicity.
//      On [ymin-0.23, ymin+0.27] (i.e., [1.23164,1.73163]), use
//              Let z = x-ymin;
//              lgamma(x) = -1.214862905358496078218 + z**2*poly(z)
//              poly(z) is a 14 degree polynomial.
//   2. Rational approximation in the primary interval [2,3]
//      We use the following approximation:
//              s = x-2.0;
//              lgamma(x) = 0.5*s + s*P(s)/Q(s)
//      with accuracy
//              |P/Q - (lgamma(x)-0.5s)| < 2**-61.71
//      Our algorithms are based on the following observation
//
//                             zeta(2)-1    2    zeta(3)-1    3
// lgamma(2+s) = s*(1-Euler) + --------- * s  -  --------- * s  + ...
//                                 2                 3
//
//      where Euler = 0.5772156649... is the Euler constant, which
//      is very close to 0.5.
//
//   3. For x>=8, we have
//      lgamma(x)~(x-0.5)log(x)-x+0.5*log(2pi)+1/(12x)-1/(360x**3)+....
//      (better formula:
//         lgamma(x)~(x-0.5)*(log(x)-1)-.5*(log(2pi)-1) + ...)
//      Let z = 1/x, then we approximation
//              f(z) = lgamma(x) - (x-0.5)(log(x)-1)
//      by
//                                  3       5             11
//              w = w0 + w1*z + w2*z  + w3*z  + ... + w6*z
//      where
//              |w - f(z)| < 2**-58.74
//
//   4. For negative x, since (G is gamma function)
//              -x*G(-x)*G(x) = pi/sin(pi*x),
//      we have
//              G(x) = pi/(sin(pi*x)*(-x)*G(-x))
//      since G(-x) is positive, sign(G(x)) = sign(sin(pi*x)) for x<0
//      Hence, for x<0, signgam = sign(sin(pi*x)) and
//              lgamma(x) = log(|Gamma(x)|)
//                        = log(pi/(|x*sin(pi*x)|)) - lgamma(-x);
//      Note: one should avoid computing pi*(-x) directly in the
//            computation of sin(pi*(-x)).
//
//   5. Special Cases
//              lgamma(2+s) ~ s*(1-Euler) for tiny s
//              lgamma(1)=lgamma(2)=0
//              lgamma(x) ~ -log(x) for tiny x
//              lgamma(0) = lgamma(inf) = inf
//              lgamma(-integer) = +-inf
//
//

var _lgamA = [...]float64{
	7.72156649015328655494e-02, // 0x3FB3C467E37DB0C8
	3.22467033424113591611e-01, // 0x3FD4A34CC4A60FAD
	6.73523010531292681824e-02, // 0x3FB13E001A5562A7
	2.05808084325167332806e-02, // 0x3F951322AC92547B
	7.38555086081402883957e-03, // 0x3F7E404FB68FEFE8
	2.89051383673415629091e-03, // 0x3F67ADD8CCB7926B
	1.19270763183362067845e-03, // 0x3F538A94116F3F5D
	5.10069792153511336608e-04, // 0x3F40B6C689B99C00
	2.20862790713908385557e-04, // 0x3F2CF2ECED10E54D
	1.08011567247583939954e-04, // 0x3F1C5088987DFB07
	2.52144565451257326939e-05, // 0x3EFA7074428CFA52
	4.48640949618915160150e-05, // 0x3F07858E90A45837
}
var _lgamR = [...]float64{
	1.0,                        // placeholder
	1.39200533467621045958e+00, // 0x3FF645A762C4AB74
	7.21935547567138069525e-01, // 0x3FE71A1893D3DCDC
	1.71933865632803078993e-01, // 0x3FC601EDCCFBDF27
	1.86459191715652901344e-02, // 0x3F9317EA742ED475
	7.77942496381893596434e-04, // 0x3F497DDACA41A95B
	7.32668430744625636189e-06, // 0x3EDEBAF7A5B38140
}
var _lgamS = [...]float64{
	-7.72156649015328655494e-02, // 0xBFB3C467E37DB0C8
	2.14982415960608852501e-01,  // 0x3FCB848B36E20878
	3.25778796408930981787e-01,  // 0x3FD4D98F4F139F59
	1.46350472652464452805e-01,  // 0x3FC2BB9CBEE5F2F7
	2.66422703033638609560e-02,  // 0x3F9B481C7E939961
	1.84028451407337715652e-03,  // 0x3F5E26B67368F239
	3.19475326584100867617e-05,  // 0x3F00BFECDD17E945
}
var _lgamT = [...]float64{
	4.83836122723810047042e-01,  // 0x3FDEF72BC8EE38A2
	-1.47587722994593911752e-01, // 0xBFC2E4278DC6C509
	6.46249402391333854778e-02,  // 0x3FB08B4294D5419B
	-3.27885410759859649565e-02, // 0xBFA0C9A8DF35B713
	1.79706750811820387126e-02,  // 0x3F9266E7970AF9EC
	-1.03142241298341437450e-02, // 0xBF851F9FBA91EC6A
	6.10053870246291332635e-03,  // 0x3F78FCE0E370E344
	-3.68452016781138256760e-03, // 0xBF6E2EFFB3E914D7
	2.25964780900612472250e-03,  // 0x3F6282D32E15C915
	-1.40346469989232843813e-03, // 0xBF56FE8EBF2D1AF1
	8.81081882437654011382e-04,  // 0x3F4CDF0CEF61A8E9
	-5.38595305356740546715e-04, // 0xBF41A6109C73E0EC
	3.15632070903625950361e-04,  // 0x3F34AF6D6C0EBBF7
	-3.12754168375120860518e-04, // 0xBF347F24ECC38C38
	3.35529192635519073543e-04,  // 0x3F35FD3EE8C2D3F4
}
var _lgamU = [...]float64{
	-7.72156649015328655494e-02, // 0xBFB3C467E37DB0C8
	6.32827064025093366517e-01,  // 0x3FE4401E8B005DFF
	1.45492250137234768737e+00,  // 0x3FF7475CD119BD6F
	9.77717527963372745603e-01,  // 0x3FEF497644EA8450
	2.28963728064692451092e-01,  // 0x3FCD4EAEF6010924
	1.33810918536787660377e-02,  // 0x3F8B678BBF2BAB09
}
var _lgamV = [...]float64{
	1.0,
	2.45597793713041134822e+00, // 0x4003A5D7C2BD619C
	2.12848976379893395361e+00, // 0x40010725A42B18F5
	7.69285150456672783825e-01, // 0x3FE89DFBE45050AF
	1.04222645593369134254e-01, // 0x3FBAAE55D6537C88
	3.21709242282423911810e-03, // 0x3F6A5ABB57D0CF61
}
var _lgamW = [...]float64{
	4.18938533204672725052e-01,  // 0x3FDACFE390C97D69
	8.33333333333329678849e-02,  // 0x3FB555555555553B
	-2.77777777728775536470e-03, // 0xBF66C16C16B02E5C
	7.93650558643019558500e-04,  // 0x3F4A019F98CF38B6
	-5.95187557450339963135e-04, // 0xBF4380CB8C0FE741
	8.36339918996282139126e-04,  // 0x3F4B67BA4CDAD5D1
	-1.63092934096575273989e-03, // 0xBF5AB89D0B9E43E4
}

// Lgamma returns the natural logarithm and sign (-1 or +1) of [Gamma](x).
//
// Special cases are:
//
//	Lgamma(+Inf) = +Inf
//	Lgamma(0) = +Inf
//	Lgamma(-integer) = +Inf
//	Lgamma(-Inf) = -Inf
//	Lgamma(NaN) = NaN
func Lgamma(x float64) (lgamma float64, sign int) {
	const (
		Ymin  = 1.461632144968362245
		Two52 = 1 << 52                     // 0x4330000000000000 ~4.5036e+15
		Two53 = 1 << 53                     // 0x4340000000000000 ~9.0072e+15
		Two58 = 1 << 58                     // 0x4390000000000000 ~2.8823e+17
		Tiny  = 1.0 / (1 << 70)             // 0x3b90000000000000 ~8.47033e-22
		Tc    = 1.46163214496836224576e+00  // 0x3FF762D86356BE3F
		Tf    = -1.21486290535849611461e-01 // 0xBFBF19B9BCC38A42
		// Tt = -(tail of Tf)
		Tt = -3.63867699703950536541e-18 // 0xBC50C7CAA48A971F
	)
	// special cases
	sign = 1
	switch {
	case IsNaN(x):
		lgamma = x
		return
	case IsInf(x, 0):
		lgamma = x
		return
	case x == 0:
		lgamma = Inf(1)
		return
	}

	neg := false
	if x < 0 {
		x = -x
		neg = true
	}

	if x < Tiny { // if |x| < 2**-70, return -log(|x|)
		if neg {
			sign = -1
		}
		lgamma = -Log(x)
		return
	}
	var nadj float64
	if neg {
		if x >= Two52 { // |x| >= 2**52, must be -integer
			lgamma = Inf(1)
			return
		}
		t := sinPi(x)
		if t == 0 {
			lgamma = Inf(1) // -integer
			return
		}
		nadj = Log(Pi / Abs(t*x))
		if t < 0 {
			sign = -1
		}
	}

	switch {
	case x == 1 || x == 2: // purge off 1 and 2
		lgamma = 0
		return
	case x < 2: // use lgamma(x) = lgamma(x+1) - log(x)
		var y float64
		var i int
		if x <= 0.9 {
			lgamma = -Log(x)
			switch {
			case x >= (Ymin - 1 + 0.27): // 0.7316 <= x <=  0.9
				y = 1 - x
				i = 0
			case x >= (Ymin - 1 - 0.27): // 0.2316 <= x < 0.7316
				y = x - (Tc - 1)
				i = 1
			default: // 0 < x < 0.2316
				y = x
				i = 2
			}
		} else {
			lgamma = 0
			switch {
			case x >= (Ymin + 0.27): // 1.7316 <= x < 2
				y = 2 - x
				i = 0
			case x >= (Ymin - 0.27): // 1.2316 <= x < 1.7316
				y = x - Tc
				i = 1
			default: // 0.9 < x < 1.2316
				y = x - 1
				i = 2
			}
		}
		lgamma += lgammaSmall(y, i)
	case x < 8: // 2 <= x < 8
		lgamma = lgammaMid(x)
	case x < Two58: // 8 <= x < 2**58
		lgamma = lgammaLarge(x)
	default: // 2**58 <= x <= Inf
		lgamma = x * (Log(x) - 1)
	}
	if neg {
		lgamma = nadj - lgamma
	}
	return
}

// lgammaSmall, lgammaMid and lgammaLarge are three arms of Go's Lgamma, each the
// same operations in the same order, made functions of their own here: Go's one
// function took 151 of the cog's registers, and no program calling it fitted one.
func lgammaSmall(y float64, i int) float64 {
	const (
		Tf = -1.21486290535849611461e-01 // 0xBFBF19B9BCC38A42
		Tt = -3.63867699703950536541e-18 // 0xBC50C7CAA48A971F
	)
	switch i {
	case 0:
		z := y * y
		p1 := _lgamA[0] + z*(_lgamA[2]+z*(_lgamA[4]+z*(_lgamA[6]+z*(_lgamA[8]+z*_lgamA[10]))))
		p2 := z * (_lgamA[1] + z*(+_lgamA[3]+z*(_lgamA[5]+z*(_lgamA[7]+z*(_lgamA[9]+z*_lgamA[11])))))
		p := y*p1 + p2
		return p - 0.5*y
	case 1:
		z := y * y
		w := z * y
		p1 := _lgamT[0] + w*(_lgamT[3]+w*(_lgamT[6]+w*(_lgamT[9]+w*_lgamT[12]))) // parallel comp
		p2 := _lgamT[1] + w*(_lgamT[4]+w*(_lgamT[7]+w*(_lgamT[10]+w*_lgamT[13])))
		p3 := _lgamT[2] + w*(_lgamT[5]+w*(_lgamT[8]+w*(_lgamT[11]+w*_lgamT[14])))
		p := z*p1 - (Tt - w*(p2+y*p3))
		return Tf + p
	}
	p1 := y * (_lgamU[0] + y*(_lgamU[1]+y*(_lgamU[2]+y*(_lgamU[3]+y*(_lgamU[4]+y*_lgamU[5])))))
	p2 := 1 + y*(_lgamV[1]+y*(_lgamV[2]+y*(_lgamV[3]+y*(_lgamV[4]+y*_lgamV[5]))))
	return -0.5*y + p1/p2
}

func lgammaMid(x float64) float64 {
	i := int(x)
	y := x - float64(i)
	p := y * (_lgamS[0] + y*(_lgamS[1]+y*(_lgamS[2]+y*(_lgamS[3]+y*(_lgamS[4]+y*(_lgamS[5]+y*_lgamS[6]))))))
	q := 1 + y*(_lgamR[1]+y*(_lgamR[2]+y*(_lgamR[3]+y*(_lgamR[4]+y*(_lgamR[5]+y*_lgamR[6])))))
	lgamma := 0.5*y + p/q
	z := 1.0 // Lgamma(1+s) = Log(s) + Lgamma(s)
	switch i {
	case 7:
		z *= (y + 6)
		fallthrough
	case 6:
		z *= (y + 5)
		fallthrough
	case 5:
		z *= (y + 4)
		fallthrough
	case 4:
		z *= (y + 3)
		fallthrough
	case 3:
		z *= (y + 2)
		lgamma += Log(z)
	}
	return lgamma
}

func lgammaLarge(x float64) float64 {
	t := Log(x)
	z := 1 / x
	y := z * z
	w := _lgamW[0] + z*(_lgamW[1]+y*(_lgamW[2]+y*(_lgamW[3]+y*(_lgamW[4]+y*(_lgamW[5]+y*_lgamW[6])))))
	return (x-0.5)*(t-1) + w
}

// sinPi(x) is a helper function for negative x
func sinPi(x float64) float64 {
	const (
		Two52 = 1 << 52 // 0x4330000000000000 ~4.5036e+15
		Two53 = 1 << 53 // 0x4340000000000000 ~9.0072e+15
	)
	if x < 0.25 {
		return -Sin(Pi * x)
	}

	// argument reduction
	z := Floor(x)
	var n int
	if z != x { // inexact
		x = Mod(x, 2)
		n = int(x * 4)
	} else {
		if x >= Two53 { // x must be even
			x = 0
			n = 0
		} else {
			if x < Two52 {
				z = x + Two52 // exact
			}
			n = int(1 & Float64bits(z))
			x = float64(n)
			n <<= 2
		}
	}
	switch n {
	case 0:
		x = Sin(Pi * x)
	case 1, 2:
		x = Cos(Pi * (0.5 - x))
	case 3, 4:
		x = Sin(Pi * (1 - x))
	case 5, 6:
		x = -Cos(Pi * (x - 1.5))
	default:
		x = Sin(Pi * (x - 2))
	}
	return -x
}

// From Go's math/j0.go.
/*
	Bessel function of the first and second kinds of order zero.
*/

// The original C code and the long comment below are
// from FreeBSD's /usr/src/lib/msun/src/e_j0.c and
// came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// __ieee754_j0(x), __ieee754_y0(x)
// Bessel function of the first and second kinds of order zero.
// Method -- j0(x):
//      1. For tiny x, we use j0(x) = 1 - x**2/4 + x**4/64 - ...
//      2. Reduce x to |x| since j0(x)=j0(-x),  and
//         for x in (0,2)
//              j0(x) = 1-z/4+ z**2*R0/S0,  where z = x*x;
//         (precision:  |j0-1+z/4-z**2R0/S0 |<2**-63.67 )
//         for x in (2,inf)
//              j0(x) = sqrt(2/(pi*x))*(p0(x)*cos(x0)-q0(x)*sin(x0))
//         where x0 = x-pi/4. It is better to compute sin(x0),cos(x0)
//         as follow:
//              cos(x0) = cos(x)cos(pi/4)+sin(x)sin(pi/4)
//                      = 1/sqrt(2) * (cos(x) + sin(x))
//              sin(x0) = sin(x)cos(pi/4)-cos(x)sin(pi/4)
//                      = 1/sqrt(2) * (sin(x) - cos(x))
//         (To avoid cancellation, use
//              sin(x) +- cos(x) = -cos(2x)/(sin(x) -+ cos(x))
//         to compute the worse one.)
//
//      3 Special cases
//              j0(nan)= nan
//              j0(0) = 1
//              j0(inf) = 0
//
// Method -- y0(x):
//      1. For x<2.
//         Since
//              y0(x) = 2/pi*(j0(x)*(ln(x/2)+Euler) + x**2/4 - ...)
//         therefore y0(x)-2/pi*j0(x)*ln(x) is an even function.
//         We use the following function to approximate y0,
//              y0(x) = U(z)/V(z) + (2/pi)*(j0(x)*ln(x)), z= x**2
//         where
//              U(z) = u00 + u01*z + ... + u06*z**6
//              V(z) = 1  + v01*z + ... + v04*z**4
//         with absolute approximation error bounded by 2**-72.
//         Note: For tiny x, U/V = u0 and j0(x)~1, hence
//              y0(tiny) = u0 + (2/pi)*ln(tiny), (choose tiny<2**-27)
//      2. For x>=2.
//              y0(x) = sqrt(2/(pi*x))*(p0(x)*cos(x0)+q0(x)*sin(x0))
//         where x0 = x-pi/4. It is better to compute sin(x0),cos(x0)
//         by the method mentioned above.
//      3. Special cases: y0(0)=-inf, y0(x<0)=NaN, y0(inf)=0.
//

// J0 returns the order-zero Bessel function of the first kind.
//
// Special cases are:
//
//	J0(±Inf) = 0
//	J0(0) = 1
//	J0(NaN) = NaN
func J0(x float64) float64 {
	const (
		Huge   = 1e300
		TwoM27 = 1.0 / (1 << 27) // 2**-27 0x3e40000000000000
		TwoM13 = 1.0 / (1 << 13) // 2**-13 0x3f20000000000000
		Two129 = 1 << 129        // 2**129 0x4800000000000000
		// R0/S0 on [0, 2]
		R02 = 1.56249999999999947958e-02  // 0x3F8FFFFFFFFFFFFD
		R03 = -1.89979294238854721751e-04 // 0xBF28E6A5B61AC6E9
		R04 = 1.82954049532700665670e-06  // 0x3EBEB1D10C503919
		R05 = -4.61832688532103189199e-09 // 0xBE33D5E773D63FCE
		S01 = 1.56191029464890010492e-02  // 0x3F8FFCE882C8C2A4
		S02 = 1.16926784663337450260e-04  // 0x3F1EA6D2DD57DBF4
		S03 = 5.13546550207318111446e-07  // 0x3EA13B54CE84D5A9
		S04 = 1.16614003333790000205e-09  // 0x3E1408BCF4745D8F
	)
	// special cases
	switch {
	case IsNaN(x):
		return x
	case IsInf(x, 0):
		return 0
	case x == 0:
		return 1
	}

	x = Abs(x)
	if x >= 2 {
		s, c := Sincos(x)
		ss := s - c
		cc := s + c

		// make sure x+x does not overflow
		if x < MaxFloat64/2 {
			z := -Cos(x + x)
			if s*c < 0 {
				cc = z / ss
			} else {
				ss = z / cc
			}
		}

		// j0(x) = 1/sqrt(pi) * (P(0,x)*cc - Q(0,x)*ss) / sqrt(x)
		// y0(x) = 1/sqrt(pi) * (P(0,x)*ss + Q(0,x)*cc) / sqrt(x)

		var z float64
		if x > Two129 { // |x| > ~6.8056e+38
			z = (1 / SqrtPi) * cc / Sqrt(x)
		} else {
			u := pzero(x)
			v := qzero(x)
			z = (1 / SqrtPi) * (u*cc - v*ss) / Sqrt(x)
		}
		return z // |x| >= 2.0
	}
	if x < TwoM13 { // |x| < ~1.2207e-4
		if x < TwoM27 {
			return 1 // |x| < ~7.4506e-9
		}
		return 1 - 0.25*x*x // ~7.4506e-9 < |x| < ~1.2207e-4
	}
	z := x * x
	r := z * (R02 + z*(R03+z*(R04+z*R05)))
	s := 1 + z*(S01+z*(S02+z*(S03+z*S04)))
	if x < 1 {
		return 1 + z*(-0.25+(r/s)) // |x| < 1.00
	}
	u := 0.5 * x
	return (1+u)*(1-u) + z*(r/s) // 1.0 < |x| < 2.0
}

// Y0 returns the order-zero Bessel function of the second kind.
//
// Special cases are:
//
//	Y0(+Inf) = 0
//	Y0(0) = -Inf
//	Y0(x < 0) = NaN
//	Y0(NaN) = NaN
func Y0(x float64) float64 {
	const (
		TwoM27 = 1.0 / (1 << 27)             // 2**-27 0x3e40000000000000
		Two129 = 1 << 129                    // 2**129 0x4800000000000000
		U00    = -7.38042951086872317523e-02 // 0xBFB2E4D699CBD01F
		U01    = 1.76666452509181115538e-01  // 0x3FC69D019DE9E3FC
		U02    = -1.38185671945596898896e-02 // 0xBF8C4CE8B16CFA97
		U03    = 3.47453432093683650238e-04  // 0x3F36C54D20B29B6B
		U04    = -3.81407053724364161125e-06 // 0xBECFFEA773D25CAD
		U05    = 1.95590137035022920206e-08  // 0x3E5500573B4EABD4
		U06    = -3.98205194132103398453e-11 // 0xBDC5E43D693FB3C8
		V01    = 1.27304834834123699328e-02  // 0x3F8A127091C9C71A
		V02    = 7.60068627350353253702e-05  // 0x3F13ECBBF578C6C1
		V03    = 2.59150851840457805467e-07  // 0x3E91642D7FF202FD
		V04    = 4.41110311332675467403e-10  // 0x3DFE50183BD6D9EF
	)
	// special cases
	switch {
	case x < 0 || IsNaN(x):
		return NaN()
	case IsInf(x, 1):
		return 0
	case x == 0:
		return Inf(-1)
	}

	if x >= 2 { // |x| >= 2.0

		// y0(x) = sqrt(2/(pi*x))*(p0(x)*sin(x0)+q0(x)*cos(x0))
		//     where x0 = x-pi/4
		// Better formula:
		//     cos(x0) = cos(x)cos(pi/4)+sin(x)sin(pi/4)
		//             =  1/sqrt(2) * (sin(x) + cos(x))
		//     sin(x0) = sin(x)cos(3pi/4)-cos(x)sin(3pi/4)
		//             =  1/sqrt(2) * (sin(x) - cos(x))
		// To avoid cancellation, use
		//     sin(x) +- cos(x) = -cos(2x)/(sin(x) -+ cos(x))
		// to compute the worse one.

		s, c := Sincos(x)
		ss := s - c
		cc := s + c

		// j0(x) = 1/sqrt(pi) * (P(0,x)*cc - Q(0,x)*ss) / sqrt(x)
		// y0(x) = 1/sqrt(pi) * (P(0,x)*ss + Q(0,x)*cc) / sqrt(x)

		// make sure x+x does not overflow
		if x < MaxFloat64/2 {
			z := -Cos(x + x)
			if s*c < 0 {
				cc = z / ss
			} else {
				ss = z / cc
			}
		}
		var z float64
		if x > Two129 { // |x| > ~6.8056e+38
			z = (1 / SqrtPi) * ss / Sqrt(x)
		} else {
			u := pzero(x)
			v := qzero(x)
			z = (1 / SqrtPi) * (u*ss + v*cc) / Sqrt(x)
		}
		return z // |x| >= 2.0
	}
	if x <= TwoM27 {
		return U00 + (2/Pi)*Log(x) // |x| < ~7.4506e-9
	}
	z := x * x
	u := U00 + z*(U01+z*(U02+z*(U03+z*(U04+z*(U05+z*U06)))))
	v := 1 + z*(V01+z*(V02+z*(V03+z*V04)))
	return u/v + (2/Pi)*J0(x)*Log(x) // ~7.4506e-9 < |x| < 2.0
}

// The asymptotic expansions of pzero is
//      1 - 9/128 s**2 + 11025/98304 s**4 - ..., where s = 1/x.
// For x >= 2, We approximate pzero by
// 	pzero(x) = 1 + (R/S)
// where  R = pR0 + pR1*s**2 + pR2*s**4 + ... + pR5*s**10
// 	  S = 1 + pS0*s**2 + ... + pS4*s**10
// and
//      | pzero(x)-1-R/S | <= 2  ** ( -60.26)

// for x in [inf, 8]=1/[0,0.125]
var p0R8 = [6]float64{
	0.00000000000000000000e+00,  // 0x0000000000000000
	-7.03124999999900357484e-02, // 0xBFB1FFFFFFFFFD32
	-8.08167041275349795626e+00, // 0xC02029D0B44FA779
	-2.57063105679704847262e+02, // 0xC07011027B19E863
	-2.48521641009428822144e+03, // 0xC0A36A6ECD4DCAFC
	-5.25304380490729545272e+03, // 0xC0B4850B36CC643D
}
var p0S8 = [5]float64{
	1.16534364619668181717e+02, // 0x405D223307A96751
	3.83374475364121826715e+03, // 0x40ADF37D50596938
	4.05978572648472545552e+04, // 0x40E3D2BB6EB6B05F
	1.16752972564375915681e+05, // 0x40FC810F8F9FA9BD
	4.76277284146730962675e+04, // 0x40E741774F2C49DC
}

// for x in [8,4.5454]=1/[0.125,0.22001]
var p0R5 = [6]float64{
	-1.14125464691894502584e-11, // 0xBDA918B147E495CC
	-7.03124940873599280078e-02, // 0xBFB1FFFFE69AFBC6
	-4.15961064470587782438e+00, // 0xC010A370F90C6BBF
	-6.76747652265167261021e+01, // 0xC050EB2F5A7D1783
	-3.31231299649172967747e+02, // 0xC074B3B36742CC63
	-3.46433388365604912451e+02, // 0xC075A6EF28A38BD7
}
var p0S5 = [5]float64{
	6.07539382692300335975e+01, // 0x404E60810C98C5DE
	1.05125230595704579173e+03, // 0x40906D025C7E2864
	5.97897094333855784498e+03, // 0x40B75AF88FBE1D60
	9.62544514357774460223e+03, // 0x40C2CCB8FA76FA38
	2.40605815922939109441e+03, // 0x40A2CC1DC70BE864
}

// for x in [4.547,2.8571]=1/[0.2199,0.35001]
var p0R3 = [6]float64{
	-2.54704601771951915620e-09, // 0xBE25E1036FE1AA86
	-7.03119616381481654654e-02, // 0xBFB1FFF6F7C0E24B
	-2.40903221549529611423e+00, // 0xC00345B2AEA48074
	-2.19659774734883086467e+01, // 0xC035F74A4CB94E14
	-5.80791704701737572236e+01, // 0xC04D0A22420A1A45
	-3.14479470594888503854e+01, // 0xC03F72ACA892D80F
}
var p0S3 = [5]float64{
	3.58560338055209726349e+01, // 0x4041ED9284077DD3
	3.61513983050303863820e+02, // 0x40769839464A7C0E
	1.19360783792111533330e+03, // 0x4092A66E6D1061D6
	1.12799679856907414432e+03, // 0x40919FFCB8C39B7E
	1.73580930813335754692e+02, // 0x4065B296FC379081
}

// for x in [2.8570,2]=1/[0.3499,0.5]
var p0R2 = [6]float64{
	-8.87534333032526411254e-08, // 0xBE77D316E927026D
	-7.03030995483624743247e-02, // 0xBFB1FF62495E1E42
	-1.45073846780952986357e+00, // 0xBFF736398A24A843
	-7.63569613823527770791e+00, // 0xC01E8AF3EDAFA7F3
	-1.11931668860356747786e+01, // 0xC02662E6C5246303
	-3.23364579351335335033e+00, // 0xC009DE81AF8FE70F
}
var p0S2 = [5]float64{
	2.22202997532088808441e+01, // 0x40363865908B5959
	1.36206794218215208048e+02, // 0x4061069E0EE8878F
	2.70470278658083486789e+02, // 0x4070E78642EA079B
	1.53875394208320329881e+02, // 0x40633C033AB6FAFF
	1.46576176948256193810e+01, // 0x402D50B344391809
}

func pzero(x float64) float64 {
	var p *[6]float64
	var q *[5]float64
	if x >= 8 {
		p = &p0R8
		q = &p0S8
	} else if x >= 4.5454 {
		p = &p0R5
		q = &p0S5
	} else if x >= 2.8571 {
		p = &p0R3
		q = &p0S3
	} else if x >= 2 {
		p = &p0R2
		q = &p0S2
	}
	z := 1 / (x * x)
	r := p[0] + z*(p[1]+z*(p[2]+z*(p[3]+z*(p[4]+z*p[5]))))
	s := 1 + z*(q[0]+z*(q[1]+z*(q[2]+z*(q[3]+z*q[4]))))
	return 1 + r/s
}

// For x >= 8, the asymptotic expansions of qzero is
//      -1/8 s + 75/1024 s**3 - ..., where s = 1/x.
// We approximate pzero by
//      qzero(x) = s*(-1.25 + (R/S))
// where R = qR0 + qR1*s**2 + qR2*s**4 + ... + qR5*s**10
//       S = 1 + qS0*s**2 + ... + qS5*s**12
// and
//      | qzero(x)/s +1.25-R/S | <= 2**(-61.22)

// for x in [inf, 8]=1/[0,0.125]
var q0R8 = [6]float64{
	0.00000000000000000000e+00, // 0x0000000000000000
	7.32421874999935051953e-02, // 0x3FB2BFFFFFFFFE2C
	1.17682064682252693899e+01, // 0x402789525BB334D6
	5.57673380256401856059e+02, // 0x40816D6315301825
	8.85919720756468632317e+03, // 0x40C14D993E18F46D
	3.70146267776887834771e+04, // 0x40E212D40E901566
}
var q0S8 = [6]float64{
	1.63776026895689824414e+02,  // 0x406478D5365B39BC
	8.09834494656449805916e+03,  // 0x40BFA2584E6B0563
	1.42538291419120476348e+05,  // 0x4101665254D38C3F
	8.03309257119514397345e+05,  // 0x412883DA83A52B43
	8.40501579819060512818e+05,  // 0x4129A66B28DE0B3D
	-3.43899293537866615225e+05, // 0xC114FD6D2C9530C5
}

// for x in [8,4.5454]=1/[0.125,0.22001]
var q0R5 = [6]float64{
	1.84085963594515531381e-11, // 0x3DB43D8F29CC8CD9
	7.32421766612684765896e-02, // 0x3FB2BFFFD172B04C
	5.83563508962056953777e+00, // 0x401757B0B9953DD3
	1.35111577286449829671e+02, // 0x4060E3920A8788E9
	1.02724376596164097464e+03, // 0x40900CF99DC8C481
	1.98997785864605384631e+03, // 0x409F17E953C6E3A6
}
var q0S5 = [6]float64{
	8.27766102236537761883e+01,  // 0x4054B1B3FB5E1543
	2.07781416421392987104e+03,  // 0x40A03BA0DA21C0CE
	1.88472887785718085070e+04,  // 0x40D267D27B591E6D
	5.67511122894947329769e+04,  // 0x40EBB5E397E02372
	3.59767538425114471465e+04,  // 0x40E191181F7A54A0
	-5.35434275601944773371e+03, // 0xC0B4EA57BEDBC609
}

// for x in [4.547,2.8571]=1/[0.2199,0.35001]
var q0R3 = [6]float64{
	4.37741014089738620906e-09, // 0x3E32CD036ADECB82
	7.32411180042911447163e-02, // 0x3FB2BFEE0E8D0842
	3.34423137516170720929e+00, // 0x400AC0FC61149CF5
	4.26218440745412650017e+01, // 0x40454F98962DAEDD
	1.70808091340565596283e+02, // 0x406559DBE25EFD1F
	1.66733948696651168575e+02, // 0x4064D77C81FA21E0
}
var q0S3 = [6]float64{
	4.87588729724587182091e+01,  // 0x40486122BFE343A6
	7.09689221056606015736e+02,  // 0x40862D8386544EB3
	3.70414822620111362994e+03,  // 0x40ACF04BE44DFC63
	6.46042516752568917582e+03,  // 0x40B93C6CD7C76A28
	2.51633368920368957333e+03,  // 0x40A3A8AAD94FB1C0
	-1.49247451836156386662e+02, // 0xC062A7EB201CF40F
}

// for x in [2.8570,2]=1/[0.3499,0.5]
var q0R2 = [6]float64{
	1.50444444886983272379e-07, // 0x3E84313B54F76BDB
	7.32234265963079278272e-02, // 0x3FB2BEC53E883E34
	1.99819174093815998816e+00, // 0x3FFFF897E727779C
	1.44956029347885735348e+01, // 0x402CFDBFAAF96FE5
	3.16662317504781540833e+01, // 0x403FAA8E29FBDC4A
	1.62527075710929267416e+01, // 0x403040B171814BB4
}
var q0S2 = [6]float64{
	3.03655848355219184498e+01,  // 0x403E5D96F7C07AED
	2.69348118608049844624e+02,  // 0x4070D591E4D14B40
	8.44783757595320139444e+02,  // 0x408A664522B3BF22
	8.82935845112488550512e+02,  // 0x408B977C9C5CC214
	2.12666388511798828631e+02,  // 0x406A95530E001365
	-5.31095493882666946917e+00, // 0xC0153E6AF8B32931
}

func qzero(x float64) float64 {
	var p, q *[6]float64
	if x >= 8 {
		p = &q0R8
		q = &q0S8
	} else if x >= 4.5454 {
		p = &q0R5
		q = &q0S5
	} else if x >= 2.8571 {
		p = &q0R3
		q = &q0S3
	} else if x >= 2 {
		p = &q0R2
		q = &q0S2
	}
	z := 1 / (x * x)
	r := p[0] + z*(p[1]+z*(p[2]+z*(p[3]+z*(p[4]+z*p[5]))))
	s := 1 + z*(q[0]+z*(q[1]+z*(q[2]+z*(q[3]+z*(q[4]+z*q[5])))))
	return (-0.125 + r/s) / x
}

// From Go's math/j1.go.
/*
	Bessel function of the first and second kinds of order one.
*/

// The original C code and the long comment below are
// from FreeBSD's /usr/src/lib/msun/src/e_j1.c and
// came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// __ieee754_j1(x), __ieee754_y1(x)
// Bessel function of the first and second kinds of order one.
// Method -- j1(x):
//      1. For tiny x, we use j1(x) = x/2 - x**3/16 + x**5/384 - ...
//      2. Reduce x to |x| since j1(x)=-j1(-x),  and
//         for x in (0,2)
//              j1(x) = x/2 + x*z*R0/S0,  where z = x*x;
//         (precision:  |j1/x - 1/2 - R0/S0 |<2**-61.51 )
//         for x in (2,inf)
//              j1(x) = sqrt(2/(pi*x))*(p1(x)*cos(x1)-q1(x)*sin(x1))
//              y1(x) = sqrt(2/(pi*x))*(p1(x)*sin(x1)+q1(x)*cos(x1))
//         where x1 = x-3*pi/4. It is better to compute sin(x1),cos(x1)
//         as follow:
//              cos(x1) =  cos(x)cos(3pi/4)+sin(x)sin(3pi/4)
//                      =  1/sqrt(2) * (sin(x) - cos(x))
//              sin(x1) =  sin(x)cos(3pi/4)-cos(x)sin(3pi/4)
//                      = -1/sqrt(2) * (sin(x) + cos(x))
//         (To avoid cancellation, use
//              sin(x) +- cos(x) = -cos(2x)/(sin(x) -+ cos(x))
//         to compute the worse one.)
//
//      3 Special cases
//              j1(nan)= nan
//              j1(0) = 0
//              j1(inf) = 0
//
// Method -- y1(x):
//      1. screen out x<=0 cases: y1(0)=-inf, y1(x<0)=NaN
//      2. For x<2.
//         Since
//              y1(x) = 2/pi*(j1(x)*(ln(x/2)+Euler)-1/x-x/2+5/64*x**3-...)
//         therefore y1(x)-2/pi*j1(x)*ln(x)-1/x is an odd function.
//         We use the following function to approximate y1,
//              y1(x) = x*U(z)/V(z) + (2/pi)*(j1(x)*ln(x)-1/x), z= x**2
//         where for x in [0,2] (abs err less than 2**-65.89)
//              U(z) = U0[0] + U0[1]*z + ... + U0[4]*z**4
//              V(z) = 1  + v0[0]*z + ... + v0[4]*z**5
//         Note: For tiny x, 1/x dominate y1 and hence
//              y1(tiny) = -2/pi/tiny, (choose tiny<2**-54)
//      3. For x>=2.
//               y1(x) = sqrt(2/(pi*x))*(p1(x)*sin(x1)+q1(x)*cos(x1))
//         where x1 = x-3*pi/4. It is better to compute sin(x1),cos(x1)
//         by method mentioned above.

// J1 returns the order-one Bessel function of the first kind.
//
// Special cases are:
//
//	J1(±Inf) = 0
//	J1(NaN) = NaN
func J1(x float64) float64 {
	const (
		TwoM27 = 1.0 / (1 << 27) // 2**-27 0x3e40000000000000
		Two129 = 1 << 129        // 2**129 0x4800000000000000
		// R0/S0 on [0, 2]
		R00 = -6.25000000000000000000e-02 // 0xBFB0000000000000
		R01 = 1.40705666955189706048e-03  // 0x3F570D9F98472C61
		R02 = -1.59955631084035597520e-05 // 0xBEF0C5C6BA169668
		R03 = 4.96727999609584448412e-08  // 0x3E6AAAFA46CA0BD9
		S01 = 1.91537599538363460805e-02  // 0x3F939D0B12637E53
		S02 = 1.85946785588630915560e-04  // 0x3F285F56B9CDF664
		S03 = 1.17718464042623683263e-06  // 0x3EB3BFF8333F8498
		S04 = 5.04636257076217042715e-09  // 0x3E35AC88C97DFF2C
		S05 = 1.23542274426137913908e-11  // 0x3DAB2ACFCFB97ED8
	)
	// special cases
	switch {
	case IsNaN(x):
		return x
	case IsInf(x, 0) || x == 0:
		return 0
	}

	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	if x >= 2 {
		s, c := Sincos(x)
		ss := -s - c
		cc := s - c

		// make sure x+x does not overflow
		if x < MaxFloat64/2 {
			z := Cos(x + x)
			if s*c > 0 {
				cc = z / ss
			} else {
				ss = z / cc
			}
		}

		// j1(x) = 1/sqrt(pi) * (P(1,x)*cc - Q(1,x)*ss) / sqrt(x)
		// y1(x) = 1/sqrt(pi) * (P(1,x)*ss + Q(1,x)*cc) / sqrt(x)

		var z float64
		if x > Two129 {
			z = (1 / SqrtPi) * cc / Sqrt(x)
		} else {
			u := pone(x)
			v := qone(x)
			z = (1 / SqrtPi) * (u*cc - v*ss) / Sqrt(x)
		}
		if sign {
			return -z
		}
		return z
	}
	if x < TwoM27 { // |x|<2**-27
		return 0.5 * x // inexact if x!=0 necessary
	}
	z := x * x
	r := z * (R00 + z*(R01+z*(R02+z*R03)))
	s := 1.0 + z*(S01+z*(S02+z*(S03+z*(S04+z*S05))))
	r *= x
	z = 0.5*x + r/s
	if sign {
		return -z
	}
	return z
}

// Y1 returns the order-one Bessel function of the second kind.
//
// Special cases are:
//
//	Y1(+Inf) = 0
//	Y1(0) = -Inf
//	Y1(x < 0) = NaN
//	Y1(NaN) = NaN
func Y1(x float64) float64 {
	const (
		TwoM54 = 1.0 / (1 << 54)             // 2**-54 0x3c90000000000000
		Two129 = 1 << 129                    // 2**129 0x4800000000000000
		U00    = -1.96057090646238940668e-01 // 0xBFC91866143CBC8A
		U01    = 5.04438716639811282616e-02  // 0x3FA9D3C776292CD1
		U02    = -1.91256895875763547298e-03 // 0xBF5F55E54844F50F
		U03    = 2.35252600561610495928e-05  // 0x3EF8AB038FA6B88E
		U04    = -9.19099158039878874504e-08 // 0xBE78AC00569105B8
		V00    = 1.99167318236649903973e-02  // 0x3F94650D3F4DA9F0
		V01    = 2.02552581025135171496e-04  // 0x3F2A8C896C257764
		V02    = 1.35608801097516229404e-06  // 0x3EB6C05A894E8CA6
		V03    = 6.22741452364621501295e-09  // 0x3E3ABF1D5BA69A86
		V04    = 1.66559246207992079114e-11  // 0x3DB25039DACA772A
	)
	// special cases
	switch {
	case x < 0 || IsNaN(x):
		return NaN()
	case IsInf(x, 1):
		return 0
	case x == 0:
		return Inf(-1)
	}

	if x >= 2 {
		s, c := Sincos(x)
		ss := -s - c
		cc := s - c

		// make sure x+x does not overflow
		if x < MaxFloat64/2 {
			z := Cos(x + x)
			if s*c > 0 {
				cc = z / ss
			} else {
				ss = z / cc
			}
		}
		// y1(x) = sqrt(2/(pi*x))*(p1(x)*sin(x0)+q1(x)*cos(x0))
		// where x0 = x-3pi/4
		//     Better formula:
		//         cos(x0) = cos(x)cos(3pi/4)+sin(x)sin(3pi/4)
		//                 =  1/sqrt(2) * (sin(x) - cos(x))
		//         sin(x0) = sin(x)cos(3pi/4)-cos(x)sin(3pi/4)
		//                 = -1/sqrt(2) * (cos(x) + sin(x))
		// To avoid cancellation, use
		//     sin(x) +- cos(x) = -cos(2x)/(sin(x) -+ cos(x))
		// to compute the worse one.

		var z float64
		if x > Two129 {
			z = (1 / SqrtPi) * ss / Sqrt(x)
		} else {
			u := pone(x)
			v := qone(x)
			z = (1 / SqrtPi) * (u*ss + v*cc) / Sqrt(x)
		}
		return z
	}
	if x <= TwoM54 { // x < 2**-54
		return -(2 / Pi) / x
	}
	z := x * x
	u := U00 + z*(U01+z*(U02+z*(U03+z*U04)))
	v := 1 + z*(V00+z*(V01+z*(V02+z*(V03+z*V04))))
	return x*(u/v) + (2/Pi)*(J1(x)*Log(x)-1/x)
}

// For x >= 8, the asymptotic expansions of pone is
//      1 + 15/128 s**2 - 4725/2**15 s**4 - ..., where s = 1/x.
// We approximate pone by
//      pone(x) = 1 + (R/S)
// where R = pr0 + pr1*s**2 + pr2*s**4 + ... + pr5*s**10
//       S = 1 + ps0*s**2 + ... + ps4*s**10
// and
//      | pone(x)-1-R/S | <= 2**(-60.06)

// for x in [inf, 8]=1/[0,0.125]
var p1R8 = [6]float64{
	0.00000000000000000000e+00, // 0x0000000000000000
	1.17187499999988647970e-01, // 0x3FBDFFFFFFFFFCCE
	1.32394806593073575129e+01, // 0x402A7A9D357F7FCE
	4.12051854307378562225e+02, // 0x4079C0D4652EA590
	3.87474538913960532227e+03, // 0x40AE457DA3A532CC
	7.91447954031891731574e+03, // 0x40BEEA7AC32782DD
}
var p1S8 = [5]float64{
	1.14207370375678408436e+02, // 0x405C8D458E656CAC
	3.65093083420853463394e+03, // 0x40AC85DC964D274F
	3.69562060269033463555e+04, // 0x40E20B8697C5BB7F
	9.76027935934950801311e+04, // 0x40F7D42CB28F17BB
	3.08042720627888811578e+04, // 0x40DE1511697A0B2D
}

// for x in [8,4.5454] = 1/[0.125,0.22001]
var p1R5 = [6]float64{
	1.31990519556243522749e-11, // 0x3DAD0667DAE1CA7D
	1.17187493190614097638e-01, // 0x3FBDFFFFE2C10043
	6.80275127868432871736e+00, // 0x401B36046E6315E3
	1.08308182990189109773e+02, // 0x405B13B9452602ED
	5.17636139533199752805e+02, // 0x40802D16D052D649
	5.28715201363337541807e+02, // 0x408085B8BB7E0CB7
}
var p1S5 = [5]float64{
	5.92805987221131331921e+01, // 0x404DA3EAA8AF633D
	9.91401418733614377743e+02, // 0x408EFB361B066701
	5.35326695291487976647e+03, // 0x40B4E9445706B6FB
	7.84469031749551231769e+03, // 0x40BEA4B0B8A5BB15
	1.50404688810361062679e+03, // 0x40978030036F5E51
}

// for x in[4.5453,2.8571] = 1/[0.2199,0.35001]
var p1R3 = [6]float64{
	3.02503916137373618024e-09, // 0x3E29FC21A7AD9EDD
	1.17186865567253592491e-01, // 0x3FBDFFF55B21D17B
	3.93297750033315640650e+00, // 0x400F76BCE85EAD8A
	3.51194035591636932736e+01, // 0x40418F489DA6D129
	9.10550110750781271918e+01, // 0x4056C3854D2C1837
	4.85590685197364919645e+01, // 0x4048478F8EA83EE5
}
var p1S3 = [5]float64{
	3.47913095001251519989e+01, // 0x40416549A134069C
	3.36762458747825746741e+02, // 0x40750C3307F1A75F
	1.04687139975775130551e+03, // 0x40905B7C5037D523
	8.90811346398256432622e+02, // 0x408BD67DA32E31E9
	1.03787932439639277504e+02, // 0x4059F26D7C2EED53
}

// for x in [2.8570,2] = 1/[0.3499,0.5]
var p1R2 = [6]float64{
	1.07710830106873743082e-07, // 0x3E7CE9D4F65544F4
	1.17176219462683348094e-01, // 0x3FBDFF42BE760D83
	2.36851496667608785174e+00, // 0x4002F2B7F98FAEC0
	1.22426109148261232917e+01, // 0x40287C377F71A964
	1.76939711271687727390e+01, // 0x4031B1A8177F8EE2
	5.07352312588818499250e+00, // 0x40144B49A574C1FE
}
var p1S2 = [5]float64{
	2.14364859363821409488e+01, // 0x40356FBD8AD5ECDC
	1.25290227168402751090e+02, // 0x405F529314F92CD5
	2.32276469057162813669e+02, // 0x406D08D8D5A2DBD9
	1.17679373287147100768e+02, // 0x405D6B7ADA1884A9
	8.36463893371618283368e+00, // 0x4020BAB1F44E5192
}

func pone(x float64) float64 {
	var p *[6]float64
	var q *[5]float64
	if x >= 8 {
		p = &p1R8
		q = &p1S8
	} else if x >= 4.5454 {
		p = &p1R5
		q = &p1S5
	} else if x >= 2.8571 {
		p = &p1R3
		q = &p1S3
	} else if x >= 2 {
		p = &p1R2
		q = &p1S2
	}
	z := 1 / (x * x)
	r := p[0] + z*(p[1]+z*(p[2]+z*(p[3]+z*(p[4]+z*p[5]))))
	s := 1.0 + z*(q[0]+z*(q[1]+z*(q[2]+z*(q[3]+z*q[4]))))
	return 1 + r/s
}

// For x >= 8, the asymptotic expansions of qone is
//      3/8 s - 105/1024 s**3 - ..., where s = 1/x.
// We approximate qone by
//      qone(x) = s*(0.375 + (R/S))
// where R = qr1*s**2 + qr2*s**4 + ... + qr5*s**10
//       S = 1 + qs1*s**2 + ... + qs6*s**12
// and
//      | qone(x)/s -0.375-R/S | <= 2**(-61.13)

// for x in [inf, 8] = 1/[0,0.125]
var q1R8 = [6]float64{
	0.00000000000000000000e+00,  // 0x0000000000000000
	-1.02539062499992714161e-01, // 0xBFBA3FFFFFFFFDF3
	-1.62717534544589987888e+01, // 0xC0304591A26779F7
	-7.59601722513950107896e+02, // 0xC087BCD053E4B576
	-1.18498066702429587167e+04, // 0xC0C724E740F87415
	-4.84385124285750353010e+04, // 0xC0E7A6D065D09C6A
}
var q1S8 = [6]float64{
	1.61395369700722909556e+02,  // 0x40642CA6DE5BCDE5
	7.82538599923348465381e+03,  // 0x40BE9162D0D88419
	1.33875336287249578163e+05,  // 0x4100579AB0B75E98
	7.19657723683240939863e+05,  // 0x4125F65372869C19
	6.66601232617776375264e+05,  // 0x412457D27719AD5C
	-2.94490264303834643215e+05, // 0xC111F9690EA5AA18
}

// for x in [8,4.5454] = 1/[0.125,0.22001]
var q1R5 = [6]float64{
	-2.08979931141764104297e-11, // 0xBDB6FA431AA1A098
	-1.02539050241375426231e-01, // 0xBFBA3FFFCB597FEF
	-8.05644828123936029840e+00, // 0xC0201CE6CA03AD4B
	-1.83669607474888380239e+02, // 0xC066F56D6CA7B9B0
	-1.37319376065508163265e+03, // 0xC09574C66931734F
	-2.61244440453215656817e+03, // 0xC0A468E388FDA79D
}
var q1S5 = [6]float64{
	8.12765501384335777857e+01,  // 0x405451B2FF5A11B2
	1.99179873460485964642e+03,  // 0x409F1F31E77BF839
	1.74684851924908907677e+04,  // 0x40D10F1F0D64CE29
	4.98514270910352279316e+04,  // 0x40E8576DAABAD197
	2.79480751638918118260e+04,  // 0x40DB4B04CF7C364B
	-4.71918354795128470869e+03, // 0xC0B26F2EFCFFA004
}

// for x in [4.5454,2.8571] = 1/[0.2199,0.35001] ???
var q1R3 = [6]float64{
	-5.07831226461766561369e-09, // 0xBE35CFA9D38FC84F
	-1.02537829820837089745e-01, // 0xBFBA3FEB51AEED54
	-4.61011581139473403113e+00, // 0xC01270C23302D9FF
	-5.78472216562783643212e+01, // 0xC04CEC71C25D16DA
	-2.28244540737631695038e+02, // 0xC06C87D34718D55F
	-2.19210128478909325622e+02, // 0xC06B66B95F5C1BF6
}
var q1S3 = [6]float64{
	4.76651550323729509273e+01,  // 0x4047D523CCD367E4
	6.73865112676699709482e+02,  // 0x40850EEBC031EE3E
	3.38015286679526343505e+03,  // 0x40AA684E448E7C9A
	5.54772909720722782367e+03,  // 0x40B5ABBAA61D54A6
	1.90311919338810798763e+03,  // 0x409DBC7A0DD4DF4B
	-1.35201191444307340817e+02, // 0xC060E670290A311F
}

// for x in [2.8570,2] = 1/[0.3499,0.5]
var q1R2 = [6]float64{
	-1.78381727510958865572e-07, // 0xBE87F12644C626D2
	-1.02517042607985553460e-01, // 0xBFBA3E8E9148B010
	-2.75220568278187460720e+00, // 0xC006048469BB4EDA
	-1.96636162643703720221e+01, // 0xC033A9E2C168907F
	-4.23253133372830490089e+01, // 0xC04529A3DE104AAA
	-2.13719211703704061733e+01, // 0xC0355F3639CF6E52
}
var q1S2 = [6]float64{
	2.95333629060523854548e+01,  // 0x403D888A78AE64FF
	2.52981549982190529136e+02,  // 0x406F9F68DB821CBA
	7.57502834868645436472e+02,  // 0x4087AC05CE49A0F7
	7.39393205320467245656e+02,  // 0x40871B2548D4C029
	1.55949003336666123687e+02,  // 0x40637E5E3C3ED8D4
	-4.95949898822628210127e+00, // 0xC013D686E71BE86B
}

func qone(x float64) float64 {
	var p, q *[6]float64
	if x >= 8 {
		p = &q1R8
		q = &q1S8
	} else if x >= 4.5454 {
		p = &q1R5
		q = &q1S5
	} else if x >= 2.8571 {
		p = &q1R3
		q = &q1S3
	} else if x >= 2 {
		p = &q1R2
		q = &q1S2
	}
	z := 1 / (x * x)
	r := p[0] + z*(p[1]+z*(p[2]+z*(p[3]+z*(p[4]+z*p[5]))))
	s := 1 + z*(q[0]+z*(q[1]+z*(q[2]+z*(q[3]+z*(q[4]+z*q[5])))))
	return (0.375 + r/s) / x
}

// From Go's math/jn.go.
/*
	Bessel function of the first and second kinds of order n.
*/

// The original C code and the long comment below are
// from FreeBSD's /usr/src/lib/msun/src/e_jn.c and
// came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// __ieee754_jn(n, x), __ieee754_yn(n, x)
// floating point Bessel's function of the 1st and 2nd kind
// of order n
//
// Special cases:
//      y0(0)=y1(0)=yn(n,0) = -inf with division by zero signal;
//      y0(-ve)=y1(-ve)=yn(n,-ve) are NaN with invalid signal.
// Note 2. About jn(n,x), yn(n,x)
//      For n=0, j0(x) is called,
//      for n=1, j1(x) is called,
//      for n<x, forward recursion is used starting
//      from values of j0(x) and j1(x).
//      for n>x, a continued fraction approximation to
//      j(n,x)/j(n-1,x) is evaluated and then backward
//      recursion is used starting from a supposed value
//      for j(n,x). The resulting value of j(0,x) is
//      compared with the actual value to correct the
//      supposed value of j(n,x).
//
//      yn(n,x) is similar in all respects, except
//      that forward recursion is used for all
//      values of n>1.

// Jn returns the order-n Bessel function of the first kind.
//
// Special cases are:
//
//	Jn(n, ±Inf) = 0
//	Jn(n, NaN) = NaN
func Jn(n int, x float64) float64 {
	const (
		TwoM29 = 1.0 / (1 << 29) // 2**-29 0x3e10000000000000
		Two302 = 1 << 302        // 2**302 0x52D0000000000000
	)
	// special cases
	switch {
	case IsNaN(x):
		return x
	case IsInf(x, 0):
		return 0
	}
	// J(-n, x) = (-1)**n * J(n, x), J(n, -x) = (-1)**n * J(n, x)
	// Thus, J(-n, x) = J(n, -x)

	if n == 0 {
		return J0(x)
	}
	if x == 0 {
		return 0
	}
	if n < 0 {
		n, x = -n, -x
	}
	if n == 1 {
		return J1(x)
	}
	sign := false
	if x < 0 {
		x = -x
		if n&1 == 1 {
			sign = true // odd n and negative x
		}
	}
	var b float64
	if float64(n) <= x {
		// Safe to use J(n+1,x)=2n/x *J(n,x)-J(n-1,x)
		if x >= Two302 { // x > 2**302

			// (x >> n**2)
			//          Jn(x) = cos(x-(2n+1)*pi/4)*sqrt(2/x*pi)
			//          Yn(x) = sin(x-(2n+1)*pi/4)*sqrt(2/x*pi)
			//          Let s=sin(x), c=cos(x),
			//              xn=x-(2n+1)*pi/4, sqt2 = sqrt(2),then
			//
			//                 n    sin(xn)*sqt2    cos(xn)*sqt2
			//              ----------------------------------
			//                 0     s-c             c+s
			//                 1    -s-c            -c+s
			//                 2    -s+c            -c-s
			//                 3     s+c             c-s

			var temp float64
			switch s, c := Sincos(x); n & 3 {
			case 0:
				temp = c + s
			case 1:
				temp = -c + s
			case 2:
				temp = -c - s
			case 3:
				temp = c - s
			}
			b = (1 / SqrtPi) * temp / Sqrt(x)
		} else {
			b = J1(x)
			for i, a := 1, J0(x); i < n; i++ {
				a, b = b, b*(float64(i+i)/x)-a // avoid underflow
			}
		}
	} else {
		if x < TwoM29 { // x < 2**-29
			// x is tiny, return the first Taylor expansion of J(n,x)
			// J(n,x) = 1/n!*(x/2)**n  - ...

			if n > 33 { // underflow
				b = 0
			} else {
				temp := x * 0.5
				b = temp
				a := 1.0
				for i := 2; i <= n; i++ {
					a *= float64(i) // a = n!
					b *= temp       // b = (x/2)**n
				}
				b /= a
			}
		} else {
			// use backward recurrence
			//                      x      x**2      x**2
			//  J(n,x)/J(n-1,x) =  ----   ------   ------   .....
			//                      2n  - 2(n+1) - 2(n+2)
			//
			//                      1      1        1
			//  (for large x)   =  ----  ------   ------   .....
			//                      2n   2(n+1)   2(n+2)
			//                      -- - ------ - ------ -
			//                       x     x         x
			//
			// Let w = 2n/x and h=2/x, then the above quotient
			// is equal to the continued fraction:
			//                  1
			//      = -----------------------
			//                     1
			//         w - -----------------
			//                        1
			//              w+h - ---------
			//                     w+2h - ...
			//
			// To determine how many terms needed, let
			// Q(0) = w, Q(1) = w(w+h) - 1,
			// Q(k) = (w+k*h)*Q(k-1) - Q(k-2),
			// When Q(k) > 1e4	good for single
			// When Q(k) > 1e9	good for double
			// When Q(k) > 1e17	good for quadruple

			// determine k
			w := float64(n+n) / x
			h := 2 / x
			q0 := w
			z := w + h
			q1 := w*z - 1
			k := 1
			for q1 < 1e9 {
				k++
				z += h
				q0, q1 = q1, z*q1-q0
			}
			m := n + n
			t := 0.0
			for i := 2 * (n + k); i >= m; i -= 2 {
				t = 1 / (float64(i)/x - t)
			}
			a := t
			b = 1
			//  estimate log((2/x)**n*n!) = n*log(2/x)+n*ln(n)
			//  Hence, if n*(log(2n/x)) > ...
			//  single 8.8722839355e+01
			//  double 7.09782712893383973096e+02
			//  long double 1.1356523406294143949491931077970765006170e+04
			//  then recurrent value may overflow and the result is
			//  likely underflow to zero

			tmp := float64(n)
			v := 2 / x
			tmp = tmp * Log(Abs(v*tmp))
			if tmp < 7.09782712893383973096e+02 {
				for i := n - 1; i > 0; i-- {
					di := float64(i + i)
					a, b = b, b*di/x-a
				}
			} else {
				for i := n - 1; i > 0; i-- {
					di := float64(i + i)
					a, b = b, b*di/x-a
					// scale b to avoid spurious overflow
					if b > 1e100 {
						a /= b
						t /= b
						b = 1
					}
				}
			}
			b = t * J0(x) / b
		}
	}
	if sign {
		return -b
	}
	return b
}

// Yn returns the order-n Bessel function of the second kind.
//
// Special cases are:
//
//	Yn(n, +Inf) = 0
//	Yn(n ≥ 0, 0) = -Inf
//	Yn(n < 0, 0) = +Inf if n is odd, -Inf if n is even
//	Yn(n, x < 0) = NaN
//	Yn(n, NaN) = NaN
func Yn(n int, x float64) float64 {
	const Two302 = 1 << 302 // 2**302 0x52D0000000000000
	// special cases
	switch {
	case x < 0 || IsNaN(x):
		return NaN()
	case IsInf(x, 1):
		return 0
	}

	if n == 0 {
		return Y0(x)
	}
	if x == 0 {
		if n < 0 && n&1 == 1 {
			return Inf(1)
		}
		return Inf(-1)
	}
	sign := false
	if n < 0 {
		n = -n
		if n&1 == 1 {
			sign = true // sign true if n < 0 && |n| odd
		}
	}
	if n == 1 {
		if sign {
			return -Y1(x)
		}
		return Y1(x)
	}
	var b float64
	if x >= Two302 { // x > 2**302
		// (x >> n**2)
		//	    Jn(x) = cos(x-(2n+1)*pi/4)*sqrt(2/x*pi)
		//	    Yn(x) = sin(x-(2n+1)*pi/4)*sqrt(2/x*pi)
		//	    Let s=sin(x), c=cos(x),
		//		xn=x-(2n+1)*pi/4, sqt2 = sqrt(2),then
		//
		//		   n	sin(xn)*sqt2	cos(xn)*sqt2
		//		----------------------------------
		//		   0	 s-c		 c+s
		//		   1	-s-c 		-c+s
		//		   2	-s+c		-c-s
		//		   3	 s+c		 c-s

		var temp float64
		switch s, c := Sincos(x); n & 3 {
		case 0:
			temp = s - c
		case 1:
			temp = -s - c
		case 2:
			temp = -s + c
		case 3:
			temp = s + c
		}
		b = (1 / SqrtPi) * temp / Sqrt(x)
	} else {
		a := Y0(x)
		b = Y1(x)
		// quit if b is -inf
		for i := 1; i < n && !IsInf(b, -1); i++ {
			a, b = b, (float64(i+i)/x)*b-a
		}
	}
	if sign {
		return -b
	}
	return b
}

// From Go's math/fma.go.
func zero(x uint64) uint64 {
	if x == 0 {
		return 1
	}
	return 0
	// branchless:
	// return ((x>>1 | x&1) - 1) >> 63
}

func nonzero(x uint64) uint64 {
	if x != 0 {
		return 1
	}
	return 0
	// branchless:
	// return 1 - ((x>>1|x&1)-1)>>63
}

func shl(u1, u2 uint64, n uint) (r1, r2 uint64) {
	r1 = u1<<n | u2>>(64-n) | u2<<(n-64)
	r2 = u2 << n
	return
}

func shr(u1, u2 uint64, n uint) (r1, r2 uint64) {
	r2 = u2>>n | u1<<(64-n) | u1>>(n-64)
	r1 = u1 >> n
	return
}

// shrcompress compresses the bottom n+1 bits of the two-word
// value into a single bit. the result is equal to the value
// shifted to the right by n, except the result's 0th bit is
// set to the bitwise OR of the bottom n+1 bits.
func shrcompress(u1, u2 uint64, n uint) (r1, r2 uint64) {
	// TODO: Performance here is really sensitive to the
	// order/placement of these branches. n == 0 is common
	// enough to be in the fast path. Perhaps more measurement
	// needs to be done to find the optimal order/placement?
	switch {
	case n == 0:
		return u1, u2
	case n == 64:
		return 0, u1 | nonzero(u2)
	case n >= 128:
		return 0, nonzero(u1 | u2)
	case n < 64:
		r1, r2 = shr(u1, u2, n)
		r2 |= nonzero(u2 & (1<<n - 1))
	case n < 128:
		r1, r2 = shr(u1, u2, n)
		r2 |= nonzero(u1&(1<<(n-64)-1) | u2)
	}
	return
}

func lz(u1, u2 uint64) (l int32) {
	l = int32(leadingZeros64(u1))
	if l == 64 {
		l += int32(leadingZeros64(u2))
	}
	return l
}

// split splits b into sign, biased exponent, and mantissa.
// It adds the implicit 1 bit to the mantissa for normal values,
// and normalizes subnormal values.
func split(b uint64) (sign uint32, exp int32, mantissa uint64) {
	sign = uint32(b >> 63)
	exp = int32(b>>52) & mask
	mantissa = b & fracMask

	if exp == 0 {
		// Normalize value if subnormal.
		shift := uint(leadingZeros64(mantissa) - 11)
		mantissa <<= shift
		exp = 1 - int32(shift)
	} else {
		// Add implicit 1 bit
		mantissa |= 1 << 52
	}
	return
}

// FMA returns x * y + z, computed with only one rounding.
// (That is, FMA returns the fused multiply-add of x, y, and z.)
func FMA(x, y, z float64) float64 {
	bx, by, bz := Float64bits(x), Float64bits(y), Float64bits(z)

	// Inf or NaN or zero involved. At most one rounding will occur.
	if x == 0.0 || y == 0.0 || bx&uvinf == uvinf || by&uvinf == uvinf {
		return x*y + z
	}
	// Handle z == 0.0 separately.
	// Adding zero usually does not change the original value.
	// However, there is an exception with negative zero. (e.g. (-0) + (+0) = (+0))
	// This applies when x * y is negative and underflows.
	if z == 0.0 {
		return x * y
	}
	// Handle non-finite z separately. Evaluating x*y+z where
	// x and y are finite, but z is infinite, should always result in z.
	if bz&uvinf == uvinf {
		return z
	}

	// Inputs are (sub)normal.
	// Split x, y, z into sign, exponent, mantissa.
	xs, xe, xm := split(bx)
	ys, ye, ym := split(by)
	zs, ze, zm := split(bz)

	// Compute product p = x*y as sign, exponent, two-word mantissa.
	// Start with exponent. "is normal" bit isn't subtracted yet.
	pe := xe + ye - bias + 1

	// pm1:pm2 is the double-word mantissa for the product p.
	// Shift left to leave top bit in product. Effectively
	// shifts the 106-bit product to the left by 21.
	pm1, pm2 := mul64(xm<<10, ym<<11)
	zm1, zm2 := zm<<10, uint64(0)
	ps := xs ^ ys // product sign

	// normalize to 62nd bit
	is62zero := uint((^pm1 >> 62) & 1)
	pm1, pm2 = shl(pm1, pm2, is62zero)
	pe -= int32(is62zero)

	// Swap addition operands so |p| >= |z|
	if pe < ze || pe == ze && pm1 < zm1 {
		ps, pe, pm1, pm2, zs, ze, zm1, zm2 = zs, ze, zm1, zm2, ps, pe, pm1, pm2
	}

	// Special case: if p == -z the result is always +0 since neither operand is zero.
	if ps != zs && pe == ze && pm1 == zm1 && pm2 == zm2 {
		return 0
	}

	// Align significands
	zm1, zm2 = shrcompress(zm1, zm2, uint(pe-ze))

	// Compute resulting significands, normalizing if necessary.
	var m, c uint64
	if ps == zs {
		// Adding (pm1:pm2) + (zm1:zm2)
		pm2, c = add64(pm2, zm2, 0)
		pm1, _ = add64(pm1, zm1, c)
		pe -= int32(^pm1 >> 63)
		pm1, m = shrcompress(pm1, pm2, uint(64+pm1>>63))
	} else {
		// Subtracting (pm1:pm2) - (zm1:zm2)
		// TODO: should we special-case cancellation?
		pm2, c = sub64(pm2, zm2, 0)
		pm1, _ = sub64(pm1, zm1, c)
		nz := lz(pm1, pm2)
		pe -= nz
		m, pm2 = shl(pm1, pm2, uint(nz-1))
		m |= nonzero(pm2)
	}

	// Round and break ties to even
	if pe > 1022+bias || pe == 1022+bias && (m+1<<9)>>63 == 1 {
		// rounded value overflows exponent range
		return Float64frombits(uint64(ps)<<63 | uvinf)
	}
	if pe < 0 {
		n := uint(-pe)
		m = m>>n | nonzero(m&(1<<n-1))
		pe = 0
	}
	m = ((m + 1<<9) >> 10) & ^zero((m&(1<<10-1))^1<<9)
	pe &= -int32(nonzero(m))
	return Float64frombits(uint64(ps)<<63 + uint64(pe)<<52 + m)
}
`

// p2Src is the p2 package: the Propeller 2's hardware, as declarations. Every
// function here is BODYLESS -- the form the grammar provides for a function
// implemented elsewhere -- because each is one flexcc intrinsic and the emitter
// substitutes the call. What the source buys is that the checker sees real
// signatures and real constants: an argument is checked, a misspelt name is caught
// by the compiler rather than by C, and a constant is usable wherever a constant is.
//
// The constants are the pin-configuration bits, from flexcc's smartpins.h, which is
// embedded in this repository and is the authority. They are named rather than
// written as hex because the hex is unforgiving in a way that looks like working
// code: _examples/gopher was first written with a mode word missing P_OE, which
// compiles, runs and drives nothing.
var p2Src = `// Package p2 is the Propeller 2's hardware: its pins, its timing, its random
// number generator and its sixteen hardware locks. Each function is one intrinsic
// of the C backend, so a call costs what the instruction costs and nothing more.
//
// There is no init and nothing to configure. A pin is a number, 0..63 on a P2.

` + p2ConstDecls() + `
// PinHigh drives a pin high, PinLow low, PinToggle inverts it and PinFloat releases
// it.
func PinHigh(pin int)

func PinLow(pin int)

func PinToggle(pin int)

func PinFloat(pin int)

// PinIn reads a pin's input.
func PinIn(pin int) int

// PinWrite drives a pin to v.
func PinWrite(pin int, v int)

// WaitMs, WaitUs and WaitCycles pause the calling cog. Only this cog: there is no
// scheduler, so the other seven are unaffected.
func WaitMs(ms int)

func WaitUs(us int)

func WaitCycles(n int)

// WaitUntil pauses until the system counter reaches t, where WaitCycles pauses for a
// number of cycles. That is the difference between a loop that keeps time and one
// that drifts: the work a loop body does is inside the wait rather than after it.
//
//	next := p2.GetCt() + period
//	for {
//		p2.WaitUntil(next)
//		next += period
//		...                    // however long this takes, the period holds
//	}
//
// t is an absolute counter value, so it wraps with the counter; a wait for a t
// already past returns at once rather than waiting a whole wrap around.
func WaitUntil(t uint32)

// PinStart brings a smart pin up: its mode, its X and Y registers and its direction
// bit in one call. WritePinMode, WritePinX and WritePinY set the parts separately.
func PinStart(pin int, mode int, x int, y int)

func WritePinMode(pin int, mode int)

func WritePinX(pin int, x int)

func WritePinY(pin int, y int)

// ReadPin reads a smart pin's result and acknowledges it; AckPin acknowledges
// without reading.
func ReadPin(pin int) uint32

func AckPin(pin int)

// GetCt is the system counter, GetMs, GetUs and GetSec the milliseconds,
// microseconds and seconds since reset. GetCt is the one to measure with: it counts
// every clock, and is what WaitUntil takes.
func GetCt() uint32

func GetMs() uint32

func GetSec() uint32

func GetUs() uint32

// ClockFreq is how many clocks a second has: the frequency the program was built
// for, 160 MHz where ` + "`ogo build --clock`" + ` did not ask for another. It is what turns
// a time into what GetCt, WaitUntil and WaitCycles count, and into the period a
// smart pin is given:
//
//	tick := p2.ClockFreq() / 60 // a sixtieth of a second, at any clock
//	next := p2.GetCt() + tick
//	for {
//		p2.WaitUntil(next)
//		next += tick
//		...
//	}
//
// It is read where it is called, so a loop asks once and keeps the answer.
func ClockFreq() uint32

// Rnd is the hardware random number generator. Rev reverses the bits of x.
func Rnd() uint32

func Rev(x uint32) uint32

// SetBaud sets the baud rate of the serial link the console -- print, println and
// printf -- goes out on. The loader leaves it at 230400; a host expecting another
// rate needs this called before anything is written, and the host must be reading
// at the new rate by then.
func SetBaud(baud int)

// ReadByte reads one byte from the serial link the console goes out on -- the other
// direction of SetBaud's -- and is the only way in. It returns the byte as 0..255,
// or -1 if the wait ran out, so a caller tests for a negative rather than for a
// value no byte can take.
//
// timeout is how long to wait. Zero waits FOREVER, which parks the cog until a byte
// arrives; any other value gives up after that many milliseconds. They are BINARY
// milliseconds, 1/1024 of a second rather than 1/1000: the toolchain divides the
// clock by a shift, so a wait measures about 2.4% short of what its number says.
// Near enough for a protocol timeout and not near enough to keep time with.
//
// NOTHING IS BUFFERED BEHIND THIS, and at speed that is not a caution but the
// governing fact. A byte arriving while the cog is doing anything else is gone.
// Measured on a P2-EDGE at 230400 baud, where a byte takes 43us: a loop reading into
// an array caught all eight of "PING\rOK\r" in order, and the same loop with one
// printf per byte in it caught "PNO\r" -- half of them, silently, with no error
// anywhere.
//
// So a program that must not miss a byte gives a cog to reading and to nothing else.
// AND IT MUST NOT HAND THEM OVER ON A CHANNEL, which is the obvious thing to reach
// for and does not work: a channel here is a rendezvous, so the send parks the
// reader until the far side arrives, and the line does not wait -- measured, that
// costs every byte of the next two commands while the worker is printing the answer
// to the first. The handover is a single-producer/single-consumer ring, the reader
// only ever storing and bumping head, the worker only ever reading and bumping tail.
// That reads all of "PING\rSTATUS\rGO\r" where the channel version read one command
// and two fragments.
func ReadByte(timeout int) int

// WriteByte puts one byte on the serial link exactly as given, which is what a
// protocol carrying anything but text needs and what none of print, println and
// printf will do.
//
// printf's %c is the near miss. It writes a RUNE, so it is right for text and wrong
// for a packet: every value from 0x80 up comes out UTF-8 encoded as two bytes, and a
// length field of 200 becomes 0xC3 0x88. Measured on a P2-EDGE, this writes 1..255
// as exactly those 255 bytes.
//
// The C beneath is not putchar, which is the other near miss and a worse one,
// because it looks right until a byte happens to be 10: putchar TRANSLATES that to a
// carriage return and a newline, so the same 255 values come out as 256 bytes with a
// 13 inserted. A framing byte, a length or a checksum that lands on 10 would be
// corrupted and nothing would say so.
func WriteByte(b byte)

// Reboot restarts the board.
func Reboot()

// ReadLUT reads the long at addr of the LUT RAM of the cog it runs on, and
// WriteLUT writes v there. LUT RAM is memory of each cog's own, beside its
// registers: a cog reads and writes its LUT in 3 and 2 clocks, where a long of
// hub RAM takes about 15 to read and 8 to 15 to write, so a value a hot loop
// reads and writes again and again -- an emulated machine's registers -- is
// faster kept there. It is not a
// variable: every cog has a LUT of its own, so a value one cog wrote is not what
// another reads, and what a cog finds at an address it has not written is
// whatever was there.
//
// addr is 0 to 255, the first half of the LUT, which the C backend leaves to the
// program while nothing is placed in LUT RAM, and nothing OctoGo builds is; the
// second half is the backend's. An address past 255 panics "LUT address out of
// range" where the checks are on.
func ReadLUT(addr uint32) uint32

func WriteLUT(addr uint32, v uint32)

// The sixteen hardware locks, the same pool the channel runtime draws from.
// TryLock is the only way to take one -- the hardware offers no blocking acquire --
// so a caller that must wait spins on it.
//
// NewLock does NOT report exhaustion: after handing out 0..15 it returns 15 for
// every further call, measured on a P2-EDGE. Two logically distinct locks can
// therefore alias, which costs contention where they are shared and hangs where a
// program nests two it believes are independent. See doc/locknew-never-fails.c.
func NewLock() int

func TryLock(l int) bool

func Unlock(l int)

func FreeLock(l int)
`

// p2ConstDecls renders the p2 package's constants from p2Constants, which is the
// one place their values live: the checker reads them from this source and the
// emitter substitutes them at the use, and two tables would drift.
func p2ConstDecls() string {
	var b strings.Builder
	b.WriteString("// The pin-configuration bits, from flexcc's smartpins.h. OutputEnable is the one\n")
	b.WriteString("// that is easy to leave out of a hex constant: a mode without it configures a pin\n")
	b.WriteString("// that is switched off, which on a scope looks like a bug in whatever fed it.\nconst (\n")
	names := make([]string, 0, len(p2Constants))
	for k := range p2Constants {
		names = append(names, k)
	}
	sort.Strings(names)
	w := 0
	for _, nm := range names {
		if len(nm) > w {
			w = len(nm)
		}
	}
	for _, nm := range names {
		fmt.Fprintf(&b, "\t%-*s = %s\n", w, nm, p2Constants[nm])
	}
	b.WriteString(")\n")
	return b.String()
}

// testingSrc is the testing package: what a test and a benchmark need, and no more
// than this target can provide. Its formatting methods -- Log, Logf, Error, Errorf,
// Fatal, Fatalf, Skip, Skipf, of T and of B alike -- are declared without bodies and
// written at each call by the emitter as printf writes its builtin
// (emitTestingCall): a format here is a constant, and nothing is formatted into
// memory. So are run, runB and exit, the C that leaves a test or a benchmark from
// any depth, which is what FailNow and SkipNow need, and clock and clockHz, the
// system counter's 64 bits and its rate (emitTestingIntrinsic).
const testingSrc = `// Package testing provides the state a test reports its outcome through. A test is
// a function named Test<Something> taking a *testing.T, in a file whose name ends
// _test.ogo; "ogo test" builds them into a program of their own and runs it.
//
//	func TestPop(t *testing.T) {
//		if got := pop(); got != 3 {
//			t.Errorf("pop: got %d, want 3", got)
//		}
//	}
//
// Log and Logf print as go test prints them, "    file_test.ogo:12: " ahead of
// the message: Log's operands as println separates them, Logf's by a format, which
// is a constant, as printf's is. Error and Errorf print so and fail the test;
// Fatal and Fatalf print, fail it and stop it; Skip and Skipf print and stop it,
// marked skipped.
//
// Run runs a subtest, named as go test names one -- the parent's name, a slash,
// the name given with each space written as an underscore, and "#01", "#02" after
// a name already used beside it -- and prints and reports it as go test -v does.
// A subtest's name is kept to the end of the test, so it is a constant, or a string
// that lives as long. Name's answer is cut at 128 bytes, and a subtest's own part of
// it at 128 where it is printed. A test runs 256 subtests at most, eight deep.
// "ogo test -run" selects subtests as go test's does, each level of the pattern
// after a slash matched against that level of a subtest's name, its own part
// within its first 128 bytes; a subtest not selected is not run, and counts toward
// the 256 and toward a name used again beside it, as go test counts it.
//
// FailNow and SkipNow stop the test where they are called, however deep, as Go's
// do, but run no deferred call on the way out -- as a panic runs none here. Called
// on another goroutine, they end that goroutine and the test runs on, which is what
// Go's do too. Helper does nothing: a message names the line it is written on.
//
// A benchmark is a function named Benchmark<Something> taking a *testing.B, which
// "ogo test -bench" runs as go test does: once with b.N 1, then with b.N grown
// until a run takes -benchtime, a second unless asked otherwise, and prints its
// line as go test -v -bench prints it. The time is the system clock's, read in all
// its 64 bits, and the time an operation took is computed from it exactly.
//
//	func BenchmarkPop(b *testing.B) {
//		for i := 0; i < b.N; i++ {
//			push(3)
//			pop()
//		}
//	}
//
// Loop is Go's too, the loop a benchmark of Go 1.24 on is written with, which
// times only what it runs and grows its own count, calling the benchmark once:
//
//	func BenchmarkPop(b *testing.B) {
//		fill()
//		for b.Loop() {
//			push(3)
//			pop()
//		}
//	}
//
// Run runs a sub-benchmark, named as a subtest is and run and printed as go test
// runs and prints one; a benchmark that calls Run is run once, with b.N 1, and
// prints no line of its own. A benchmark runs 256 sub-benchmarks at most, eight
// deep, as a test runs subtests, and "ogo test -bench" selects them as -run selects
// subtests: one it matches only in part runs once, for its sub-benchmarks alone.
//
// ResetTimer, StopTimer, StartTimer, SetBytes, ReportAllocs -- nothing allocates,
// so it reports none -- and ReportMetric are Go's, and so are the methods it shares
// with T, Log to Skipf, FailNow, SkipNow, Name and Helper. A benchmark reports 16
// metrics at most, a metric's unit is a constant or lives as long, and RunParallel
// and Elapsed are not provided.

const nameMax = 128

const maxSubs = 256

const maxDepth = 8

type T struct {
	failed  bool
	skipped bool
	name    string
	id      int
	depth   int
	slot    int
	began   uint64 // the system clock where the test began, for its time
	buf     [nameMax]byte
}

// sub is a subtest's line of the report its test prints when it ends: run in order
// of their start, which is the order go test prints them in, each under its parent.
type sub struct {
	parent  int // the slot of the subtest it was run in, -1 for the test itself
	name    string
	sfx     int // the count after its name plus one, 0 for none (unique)
	depth   int
	verdict int
	took    int32 // the time it took, in hundredths of a second
}

var (
	subs   [maxSubs]sub
	nsubs  int
	ids    int
	lastID int
	top    string
	rewA   [nameMax]byte
)

// Fail marks the test as failed and lets it keep running.
func (t *T) Fail() { t.failed = true }

// Failed reports whether the test has failed.
func (t *T) Failed() bool { return t.failed }

// FailNow marks the test as failed and stops it.
func (t *T) FailNow() {
	t.failed = true
	t.exit()
}

// SkipNow marks the test as skipped and stops it.
func (t *T) SkipNow() {
	t.skipped = true
	t.exit()
}

// Skipped reports whether the test was skipped.
func (t *T) Skipped() bool { return t.skipped }

// Name is the test's name, a subtest's its full one.
func (t *T) Name() string { return t.name }

// Helper does nothing here.
func (t *T) Helper() {}

// Log prints its operands, separated by spaces.
func (t *T) Log(args ...any)

// Logf prints by a constant format.
func (t *T) Logf(format string, args ...any)

// Error is Log and Fail.
func (t *T) Error(args ...any)

// Errorf is Logf and Fail.
func (t *T) Errorf(format string, args ...any)

// Fatal is Log and FailNow.
func (t *T) Fatal(args ...any)

// Fatalf is Logf and FailNow.
func (t *T) Fatalf(format string, args ...any)

// Skip is Log and SkipNow.
func (t *T) Skip(args ...any)

// Skipf is Logf and SkipNow.
func (t *T) Skipf(format string, args ...any)

// Run runs f as a subtest of t called name, and reports whether it passed or was
// skipped. A subtest that fails fails t.
//
// Run and RunTest stand on the call chain of every test, and a cog's registers are
// shared along a chain: what they do besides calling run is in functions of their
// own, beside it, or a test near the cog's limit no longer builds.
func (t *T) Run(name string, f func(t *T)) bool {
	var s T
	if !t.begin(&s, name) {
		return s.skipped
	}
	run(&s, f)
	return t.end(&s)
}

// begin sets s up as t's subtest called name and prints its RUN line, or reports
// that it does not run: no more fit, or, s marked skipped, -run does not select it.
func (t *T) begin(s *T, name string) bool {
	if nsubs == maxSubs {
		t.named()
		printf("    testing: more than %d subtests in %s\n", maxSubs, top)
		t.failed = true
		return false
	}
	s.depth = t.depth + 1
	s.slot = nsubs
	subs[nsubs] = sub{parent: t.slot, name: name, depth: s.depth}
	nsubs++
	unique(s.slot)
	if m := matcher; m != nil && m(t.slot, s.slot, ownName(s.slot)) == 0 {
		subs[s.slot].verdict = notRun
		s.skipped = true
		return false
	}
	ids++
	s.id = ids
	b := NewBuilder(s.buf[:])
	w := nameWriter{mode: nameWrite, b: &b}
	writeName(&w, s.slot)
	s.name = b.String()
	print("=== RUN   ")
	FullName(s.slot, true)
	println()
	lastID = s.id
	s.began = clock()
	return true
}

// end records how s, t's subtest, ended.
func (t *T) end(s *T) bool {
	subs[s.slot].took = hundredths(clock() - s.began)
	subs[s.slot].verdict = verdict(s)
	if s.failed {
		t.failed = true
	}
	return !s.failed
}

// named prints go test's "=== NAME" line ahead of a message where the last line
// printed was another test's.
func (t *T) named() {
	if lastID != t.id {
		print("=== NAME  ")
		t.printName()
		println()
		lastID = t.id
	}
}

// printName prints the test's full name, which Name may have cut.
func (t *T) printName() {
	if t.slot >= 0 {
		names(t.slot, true)
	} else {
		print(t.name)
	}
}

// exit leaves the test: to RunTest on its cog, and on another the goroutine ends.
func (t *T) exit()

// RunTest runs f as the test name, with t its state, and prints what go test -v
// prints of it, its subtests' results under its own. It is what the runner "ogo
// test" generates calls; a test does not call it.
func RunTest(t *T, name string, f func(*T)) {
	start(t, name)
	run(t, f)
	finish(t)
}

func start(t *T, name string) {
	ids++
	t.id = ids
	t.name = name
	t.slot = -1
	top = name
	nsubs = 0
	printf("=== RUN   %s\n", name)
	lastID = t.id
	t.began = clock()
}

func finish(t *T) {
	h := hundredths(clock() - t.began)
	report(verdict(t), 0)
	print(t.name)
	took(h)
	for i := 0; i < nsubs; i++ {
		if v := subs[i].verdict; v != notRun {
			report(v, subs[i].depth)
			names(i, true)
			took(subs[i].took)
		}
	}
}

// hundredths is a count of the system clock in hundredths of a second, rounded.
func hundredths(c uint64) int32 {
	hz := uint64(clockHz())
	return int32((c*100 + hz/2) / hz)
}

// took ends a result line with the time a test took, as go test -v ends it.
func took(h int32) {
	printf(" (%d.%02ds)\n", h/100, h%100)
}

func verdict(t *T) int {
	switch {
	case t.failed:
		return 1
	case t.skipped:
		return 2
	}
	return 0
}

func report(v, depth int) {
	for i := 0; i < depth; i++ {
		print("    ")
	}
	switch v {
	case 1:
		print("--- FAIL: ")
	case 2:
		print("--- SKIP: ")
	default:
		print("--- PASS: ")
	}
}

// The names of subtests and sub-benchmarks are go test's, which its matcher's
// unique and rewrite give (testing/match.go). Only a program running one carries
// what makes them: what every test runs reaches it through names, which the runner
// "ogo test" sets where the program names Run (SetSubtests), and the table of the
// runes that do not print is the runner's too -- the backend keeps every package
// array, used or not. Nothing here recurses: a goroutine's message prints a name,
// and a goroutine's stack is bounded by its calls, a recursion having no bound to
// read.

// names is FullName where the runner set it (SetSubtests), nil in a program that
// runs no subtest; notPrint is the table it was handed.
var (
	names    func(slot int, out bool) int
	notPrint []uint8
)

// SetSubtests hands in what naming a subtest or a sub-benchmark takes: tab, where
// the runes past U+00FF change from printing to not and back, as ogo test lays it
// out -- none where tab is empty -- and f, which is FullName. It is what the runner
// "ogo test" generates calls where the program may run one; a test does not call
// it.
func SetSubtests(tab []uint8, f func(slot int, out bool) int) {
	notPrint, names = tab, f
}

// FullName prints the full name of the subtest, or sub-benchmark, in slot -- -1 for
// the test -- where out says so, and returns its length. It is what SetSubtests is
// handed; a test does not call it.
func FullName(slot int, out bool) int {
	w := nameWriter{}
	if out {
		w.mode = namePrint
	}
	writeName(&w, slot)
	return w.n
}

// unique gives the subtest in slot j, its parent and its name set, the full name go
// test's unique gives it: its base -- its parent's full name, a slash and its own
// name rewritten -- or, where that was tried before or its name is empty, the base
// and a count, "#%02d". go test counts the times each base was tried, and the first
// count it tries is that, the next one up where a name was written as a base and a
// count, or where the base itself was so written with a count that was taken
// already. The count a base was left at is the latest subtest of that base's
// (baseTries), so no table of counts is kept, which every test program would carry.
func unique(j int) {
	p, s := subs[j].parent, subs[j].name
	base := baseKey{p: p, s: s, n: -1, c: -2}
	n := baseTries(j, &base)
	for ; ; n++ {
		if n == 0 && s != "" {
			at, nn := countAfter(s)
			if at < 0 {
				return
			}
			pre := baseKey{p: p, s: s[:at], n: -1, c: -2}
			if nn >= baseTries(j, &pre) {
				return
			}
			continue
		}
		next := baseKey{p: p, s: s, n: n, c: -2}
		taken := false
		for i := 0; i < j && !taken; i++ {
			taken = isBase(i, &next)
		}
		if !taken {
			subs[j].sfx = n + 1
			return
		}
	}
}

// baseTries is how many times the base k names was tried by the subtests before
// slot j: as many as the count the latest of that base took, plus one.
func baseTries(j int, k *baseKey) int {
	for i := j - 1; i >= 0; i-- {
		if isBase(i, k) {
			if n := subs[i].sfx; n != 0 {
				return n
			}
			return 1
		}
	}
	return 0
}

// A baseKey is a base a subtest's is compared with (isBase): the full name of p, a
// slash, s rewritten and the count n after it, -1 for none. ref is the first bytes
// of it past the full name of c, kept for the next comparison past the same, and n
// how long it is.
type baseKey struct {
	p, n, c int
	s       string
	buf     [nameMax]byte
	ref     string
	len     int
}

// countAfter is go test's parseSubtestNumber of a base whose name is s: where in s
// a count "#%02d" could have made begins, and the count, k -1 where none could.
func countAfter(s string) (k, n int) {
	for k = len(s) - 1; k >= 0 && s[k] != '#'; k-- {
	}
	i := k + 1
	switch {
	case k < 0 || len(s)-i < 2:
		return -1, 0
	case s[i] == '0' && (len(s)-i > 2 || s[i+1] == '0' && k != 0):
		return -1, 0
	}
	neg := s[i] == '-'
	if neg || s[i] == '+' {
		i++
	}
	for ; i < len(s); i++ {
		c := int(s[i]) - '0'
		if c < 0 || c > 9 || n > 214748364 || (n == 214748364 && c > 7) {
			return -1, 0
		}
		n = 10*n + c
	}
	if neg && n != 0 {
		return -1, 0
	}
	return k, n
}

// isBase reports whether the base of the subtest in slot i is the one k names.
// What the two begin with (common) is not compared.
func isBase(i int, k *baseKey) bool {
	c := common(subs[i].parent, k.p)
	if c != k.c {
		b := NewBuilder(k.buf[:])
		w := nameWriter{mode: nameWrite, b: &b}
		writeBase(&w, k.p, k.s, k.n, c)
		k.c, k.ref, k.len = c, b.String(), w.n
	}
	var buf [nameMax]byte
	for off := 0; ; off += nameMax {
		ref := k.ref
		if off != 0 {
			b := NewBuilder(buf[:])
			w := nameWriter{mode: nameWrite, b: &b, skip: off}
			writeBase(&w, k.p, k.s, k.n, c)
			ref = b.String()
		}
		w := nameWriter{mode: nameCompare, ref: ref, skip: off, next: -1}
		writeBase(&w, subs[i].parent, subs[i].name, -1, c)
		if w.diff || w.n < off+len(ref) {
			return false
		}
		if k.len <= off+nameMax {
			return w.next < 0
		}
	}
}

// common is the subtest nearest both whose full name is that of the one in slot a's
// and b's, or of their parent's, -1 for the test's: what the two begin with.
func common(a, b int) int {
	for x := a; x >= 0; x = subs[x].parent {
		for y := b; y >= 0; y = subs[y].parent {
			if x == y {
				return x
			}
		}
	}
	return -1
}

// ownName is the subtest in slot j's part of its full name, past its parent's and
// the slash, cut at 128 bytes: what -run matches below its parent's levels.
func ownName(j int) string {
	b := NewBuilder(rewA[:])
	w := nameWriter{mode: nameWrite, b: &b, skip: FullName(subs[j].parent, false) + 1}
	writeName(&w, j)
	return b.String()
}

// A nameWriter is given a name a piece at a time and does with it what its mode
// says: counts its bytes, prints them, writes those from skip on into b, or compares
// those from skip on with ref's, stopping at one that differs (diff) or at the one
// after ref's last (next).
type nameWriter struct {
	mode int
	b    *Builder
	ref  string
	skip int
	n    int  // the bytes given
	stop bool // compared: one differed, or the one after ref's was given
	diff bool
	next int // compared: the byte after ref's last, -1 where none was given
}

// The nameWriter's modes.
const (
	nameCount = iota
	namePrint
	nameWrite
	nameCompare
)

func (w *nameWriter) put(s string) {
	from := w.n
	w.n += len(s)
	switch w.mode {
	case namePrint:
		print(s)
	case nameWrite:
		// Byte by byte: the backend returns WriteString's (int, error) in a
		// copy it allocates, which brings its collector and heap along.
		for k := max(w.skip-from, 0); k < len(s); k++ {
			w.b.WriteByte(s[k])
		}
	case nameCompare:
		for k := 0; k < len(s) && !w.stop; k++ {
			p := from + k - w.skip
			switch {
			case p < 0:
			case p < len(w.ref):
				if s[k] != w.ref[p] {
					w.diff, w.stop = true, true
				}
			default:
				w.next, w.stop = int(s[k]), true
			}
		}
	}
}

// writeName gives w the full name of the subtest in slot i, -1 for the test.
func writeName(w *nameWriter, i int) {
	w.put(top)
	render(w, i, -1)
}

// render gives w the full name of the subtest in slot i past that of c, i itself or
// a subtest it runs in, -1 for the test: how the name of each subtest between ends,
// outermost first.
func render(w *nameWriter, i, c int) {
	for last := c; last != i && !w.stop; {
		x := i
		for subs[x].parent != last {
			x = subs[x].parent
		}
		writeTail(w, subs[x].name, subs[x].sfx-1)
		last = x
	}
}

// writeBase gives w the full name of p past that of c, a slash, s rewritten, and the
// count n after it where n is not -1.
func writeBase(w *nameWriter, p int, s string, n, c int) {
	render(w, p, c)
	writeTail(w, s, n)
}

// writeTail gives w a slash, s rewritten, and the count n after it where n is not
// -1.
func writeTail(w *nameWriter, s string, n int) {
	w.put("/")
	writeRewritten(w, s)
	if n >= 0 {
		w.put("#")
		if n < 10 {
			w.put("0")
		}
		d := 1
		for d*10 <= n {
			d *= 10
		}
		for ; d > 0; d /= 10 {
			k := n / d % 10
			w.put(hexDigits[k : k+1])
		}
	}
}

const hexDigits = "0123456789abcdef"

// writeRewritten gives w the name s as go test's rewrite writes it: each space an
// underscore and each rune that does not print escaped as Go quotes it, a byte of
// no UTF-8 read as U+FFFD, as a range reads it.
func writeRewritten(w *nameWriter, s string) {
	for i := 0; i < len(s) && !w.stop; {
		r, n := decodeRune(s, i)
		switch {
		case isSpace(r):
			w.put("_")
		case r == 0xfffd:
			w.put("\ufffd")
		case !isPrint(r):
			writeEscape(w, r)
		default:
			w.put(s[i : i+n])
		}
		i += n
	}
}

// decodeRune is the rune at s[i] and its length in bytes, as a range over s reads
// them: U+FFFD and 1 where no UTF-8 begins there. A range's own decoder costs a
// test program more of the cog's registers.
func decodeRune(s string, i int) (rune, int) {
	r := rune(s[i])
	switch {
	case r < 0x80:
		return r, 1
	case r < 0xc2 || r > 0xf4:
		return 0xfffd, 1
	}
	n, lo, hi := 2, rune(0x80), rune(0xbf)
	switch {
	case r >= 0xf0:
		n, r = 4, r&7
		if r == 0 {
			lo = 0x90
		} else if r == 4 {
			hi = 0x8f
		}
	case r >= 0xe0:
		n, r = 3, r&15
		if r == 0 {
			lo = 0xa0
		} else if r == 13 {
			hi = 0x9f
		}
	default:
		r &= 0x1f
	}
	for k := 1; k < n; k++ {
		if i+k >= len(s) {
			return 0xfffd, 1
		}
		c := rune(s[i+k])
		if c < lo || c > hi {
			return 0xfffd, 1
		}
		r, lo, hi = r<<6|c&0x3f, 0x80, 0xbf
	}
	return r, n
}

// writeEscape gives w the rune r, which does not print, as strconv.QuoteRune writes
// it between its quotes; the ones it writes otherwise are spaces to rewrite.
func writeEscape(w *nameWriter, r rune) {
	n := 8
	switch {
	case r == 7:
		w.put("\\a")
		return
	case r == 8:
		w.put("\\b")
		return
	case r < 0x20 || r == 0x7f:
		w.put("\\x")
		n = 2
	case r < 0x10000:
		w.put("\\u")
		n = 4
	default:
		w.put("\\U")
	}
	for k := n - 1; k >= 0; k-- {
		d := int(r>>uint(4*k)) & 15
		w.put(hexDigits[d : d+1])
	}
}

// isSpace is go test's: not the same as Unicode's Z class.
func isSpace(r rune) bool {
	if r < 0x2000 {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xa0, 0x1680:
			return true
		}
		return false
	}
	if r <= 0x200a {
		return true
	}
	switch r {
	case 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return false
}

// isPrint is strconv.IsPrint: Latin-1 here, and past it by the table (widePrint).
func isPrint(r rune) bool {
	switch {
	case r < 0x20:
		return false
	case r < 0x7f:
		return true
	case r < 0xa1:
		return false
	case r < 0x100:
		return r != 0xad
	}
	return widePrint(r)
}

// widePrint reports whether r, past U+00FF, prints, by the table SetSubtests was
// handed, or, with none, says it does: the runes where printing changes, from
// U+0100 on, which prints, so a rune that has passed an even number prints. The
// table is a count of blocks of an even number of changes, two bytes; for each, its
// first change, three bytes, and where the rest of it is, two; and the rest, each
// change its distance from the one before, seven bits a byte from the lowest, the
// last byte's high bit clear.
func widePrint(r rune) bool {
	t := notPrint
	if len(t) == 0 {
		return true
	}
	nb := int(t[0]) | int(t[1])<<8
	lo, hi := 0, nb // the first block whose first change r has not passed
	for lo < hi {
		m := (lo + hi) / 2
		if k := 2 + 5*m; rune(t[k])|rune(t[k+1])<<8|rune(t[k+2])<<16 <= r {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo == 0 {
		return true
	}
	k := 2 + 5*(lo-1)
	at := rune(t[k]) | rune(t[k+1])<<8 | rune(t[k+2])<<16
	p, end := int(t[k+3])|int(t[k+4])<<8, len(t)
	if lo < nb {
		end = int(t[k+8]) | int(t[k+9])<<8
	}
	odd := true // passed the block's first change, after an even number
	for p < end {
		var d rune
		for shift := uint(0); ; shift += 7 {
			c := t[p]
			p++
			d |= rune(c&0x7f) << shift
			if c < 0x80 {
				break
			}
		}
		if at+d > r {
			break
		}
		at += d
		odd = !odd
	}
	return !odd
}

// spaceLen is the length in bytes of a space at s[i] as go test's isSpace has one --
// not the same as Unicode's Z class -- or 0.
func spaceLen(s string, i int) int {
	c := s[i]
	switch {
	case c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' || c == ' ':
		return 1
	case c == 0xc2 && i+1 < len(s) && (s[i+1] == 0x85 || s[i+1] == 0xa0):
		return 2
	case c == 0xe1 && i+2 < len(s) && s[i+1] == 0x9a && s[i+2] == 0x80:
		return 3
	case c == 0xe2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] <= 0x8a || s[i+2] == 0xa8 || s[i+2] == 0xa9 || s[i+2] == 0xaf):
		return 3
	case c == 0xe2 && i+2 < len(s) && s[i+1] == 0x81 && s[i+2] == 0x9f:
		return 3
	case c == 0xe3 && i+2 < len(s) && s[i+1] == 0x80 && s[i+2] == 0x80:
		return 3
	}
	return 0
}

// notRun is the verdict of a subtest -run does not select, which takes its slot,
// counting toward a name used again beside it as go test counts it, and prints no
// line.
const notRun = 3

// The subtest filter: -run's pattern, or -bench's, where it names a level below a
// test's own (SetFilter). matcher is MatchSub where one is set and nil otherwise.
var (
	matcher     func(parent, slot int, own string) int
	filterProg  []int32
	filterState []int32
	filterAlive uint32
)

// SetFilter has prog choose the subtests, or sub-benchmarks, of the test or
// benchmark the runner runs next: -run's pattern, or -bench's, compiled by ogo test
// as go test reads one -- split at each slash and each bar outside brackets and
// parentheses into alternatives of one regular expression for each level of a
// name, each compiled to a program of Go's regexp machine. alive is the
// alternatives the test's own name matched, a bit each; state is the runner's room
// for what each subtest's levels matched, two words a slot; and m is MatchSub, a
// value the runner gives, so a program run with no such pattern carries none of
// it. It is what the runner "ogo test" generates calls; a test does not call it.
func SetFilter(prog, state []int32, alive uint32, m func(parent, slot int, own string) int) {
	filterProg, filterState, filterAlive, matcher = prog, state, alive, m
}

// OnlySubs has b run only to run its sub-benchmarks, as go test runs a benchmark
// -bench matches only in part: once, with b.N 1, printing no line of its own. It is
// what the runner "ogo test" generates calls; a benchmark does not call it.
func OnlySubs(b *B) { b.hasSub = true }

// MatchSub is what SetFilter's m is: 0 where the subtest, or sub-benchmark, in
// slot, of the one in slot parent -- -1 for the test -- does not run; 2 where it runs
// only to run its own, the first alternative it matches naming a level below it;
// and 1 where it runs. What its levels matched is kept in slot, for its own. Its
// levels are own, its part of its full name as go test writes it (ownName), split
// at each slash, as go test splits a full name: a slash in its name is a level of
// its own.
func MatchSub(parent, slot int, own string) int {
	alive, n := filterAlive, int32(1)
	if parent >= 0 {
		alive, n = uint32(filterState[2*parent]), filterState[2*parent+1]
	}
	for i := 0; ; {
		j := i
		for j < len(own) && own[j] != '/' {
			j++
		}
		alive = matchLevel(alive, int(n), own[i:j])
		n++
		if j == len(own) {
			break
		}
		i = j + 1
	}
	filterState[2*slot], filterState[2*slot+1] = int32(alive), n
	if alive == 0 {
		return 0
	}
	a := 0
	for alive&(uint32(1)<<uint(a)) == 0 {
		a++
	}
	if n < filterProg[filterProg[1+a]] {
		return 2
	}
	return 1
}

// matchLevel is alive less the alternatives whose level k does not match s.
func matchLevel(alive uint32, k int, s string) uint32 {
	for a := 0; a < int(filterProg[0]); a++ {
		bit := uint32(1) << uint(a)
		if alive&bit == 0 {
			continue
		}
		at := int(filterProg[1+a])
		if k < int(filterProg[at]) && !match(int(filterProg[at+1+k]), s) {
			alive &^= bit
		}
	}
	return alive
}

// The regular expression machine's operations: Go's regexp/syntax instructions, a
// capture and a no-op one, and every one matching a rune a list of ranges.
const (
	opMatch = iota
	opAlt
	opNop
	opEmpty
	opRune
	opFail
)

// The machine runs 256 instructions at most, its sets of threads a bit each.
const (
	maxInsts = 256
	setWords = maxInsts / 32
)

// The empty-width conditions, syntax.EmptyOp's.
const (
	emptyBeginLine      = 1
	emptyEndLine        = 2
	emptyBeginText      = 4
	emptyEndText        = 8
	emptyWordBoundary   = 16
	emptyNoWordBoundary = 32
)

// match reports whether the program at filterProg[p] matches somewhere in s, as
// regexp.MatchString does: a Pike machine over the runes of s, a thread started at
// each of them and at its end.
func match(p int, s string) bool {
	var pend, seen [setWords]uint32
	prev := rune(-1)
	for _, r := range s {
		if closure(p, &pend, &seen, emptyContext(prev, r)) {
			return true
		}
		pend = [setWords]uint32{}
		for i := 0; i < int(filterProg[p]); i++ {
			if seen[i>>5]&(uint32(1)<<uint(i&31)) == 0 {
				continue
			}
			at := p + 2 + 4*i
			if filterProg[at] == opRune && inRanges(at, r) {
				out := int(filterProg[at+1])
				pend[out>>5] |= uint32(1) << uint(out&31)
			}
		}
		prev = r
	}
	return closure(p, &pend, &seen, emptyContext(prev, -1))
}

// closure collects in seen what the threads in pend and one starting reach without
// reading a rune, at a position where ctx holds, and reports whether one matches.
func closure(p int, pend, seen *[setWords]uint32, ctx int32) bool {
	var stack [2*maxInsts + 1]int16
	*seen = [setWords]uint32{}
	stack[0] = int16(filterProg[p+1])
	top := 1
	for i := 0; i < int(filterProg[p]); i++ {
		if pend[i>>5]&(uint32(1)<<uint(i&31)) != 0 {
			stack[top] = int16(i)
			top++
		}
	}
	for top > 0 {
		top--
		i := int(stack[top])
		bit := uint32(1) << uint(i&31)
		if seen[i>>5]&bit != 0 {
			continue
		}
		seen[i>>5] |= bit
		at := p + 2 + 4*i
		switch filterProg[at] {
		case opMatch:
			return true
		case opAlt:
			stack[top] = int16(filterProg[at+1])
			stack[top+1] = int16(filterProg[at+2])
			top += 2
		case opNop:
			stack[top] = int16(filterProg[at+1])
			top++
		case opEmpty:
			if filterProg[at+2]&^ctx == 0 {
				stack[top] = int16(filterProg[at+1])
				top++
			}
		}
	}
	return false
}

// inRanges reports whether r is in a range of the rune instruction at filterProg[at].
func inRanges(at int, r rune) bool {
	k := int(filterProg[at+2])
	for end := k + 2*int(filterProg[at+3]); k < end; k += 2 {
		if int32(r) >= filterProg[k] && int32(r) <= filterProg[k+1] {
			return true
		}
	}
	return false
}

// emptyContext is syntax.EmptyOpContext: the empty-width conditions holding
// between r1 and r2, -1 for the text's start or end.
func emptyContext(r1, r2 rune) int32 {
	op := int32(emptyNoWordBoundary)
	boundary := false
	switch {
	case isWordChar(r1):
		boundary = true
	case r1 == '\n':
		op |= emptyBeginLine
	case r1 < 0:
		op |= emptyBeginText | emptyBeginLine
	}
	switch {
	case isWordChar(r2):
		boundary = !boundary
	case r2 == '\n':
		op |= emptyEndLine
	case r2 < 0:
		op |= emptyEndText | emptyEndLine
	}
	if boundary {
		op ^= emptyWordBoundary | emptyNoWordBoundary
	}
	return op
}

// isWordChar is syntax.IsWordChar: an ASCII letter, digit or underscore.
func isWordChar(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '_'
}

// run calls f(t) where exit returns to.
func run(t *T, f func(*T))

// B is a benchmark's state: N, how many times the benchmark is to do what it
// measures, the timer, and its outcome, which it reports as a test reports one.
type B struct {
	N        int
	loopI    int // the count Loop has run, loopPoison set while the timer is stopped
	loopN    int // the count Loop runs to, 0 before its first call
	failed   bool
	skipped  bool
	hasSub   bool // it called Run, and is not measured itself
	name     string
	slot     int // its line in subs, -1 for a benchmark the runner runs
	timerOn  bool
	start    uint64 // the clock where the timer last started
	clocks   uint64 // the clocks timed in this run of the benchmark
	bytes    int64
	allocs   bool
	nextra   int
	units    [maxMetrics]string
	values   [maxMetrics]float64
	loopDone bool // Loop has returned false
	buf      [nameMax]byte
}

const maxMetrics = 16

// loopPoison is set in loopI while the timer is stopped, so that Loop leaves its
// fast path, where it finds the timer stopped and fails: a count is 1e9 at most.
const loopPoison = 1 << 30

var (
	benchWidth int
	benchTime  int64 // in nanoseconds, where benchCount is 0
	benchCount int
	benchMem   bool
)

// Fail marks the benchmark as failed and lets it keep running.
func (b *B) Fail() { b.failed = true }

// Failed reports whether the benchmark has failed.
func (b *B) Failed() bool { return b.failed }

// FailNow marks the benchmark as failed and stops it.
func (b *B) FailNow() {
	b.failed = true
	b.exit()
}

// SkipNow marks the benchmark as skipped and stops it.
func (b *B) SkipNow() {
	b.skipped = true
	b.exit()
}

// Skipped reports whether the benchmark was skipped.
func (b *B) Skipped() bool { return b.skipped }

// Name is the benchmark's name.
func (b *B) Name() string { return b.name }

// Helper does nothing here.
func (b *B) Helper() {}

// Log prints its operands, separated by spaces.
func (b *B) Log(args ...any)

// Logf prints by a constant format.
func (b *B) Logf(format string, args ...any)

// Error is Log and Fail.
func (b *B) Error(args ...any)

// Errorf is Logf and Fail.
func (b *B) Errorf(format string, args ...any)

// Fatal is Log and FailNow.
func (b *B) Fatal(args ...any)

// Fatalf is Logf and FailNow.
func (b *B) Fatalf(format string, args ...any)

// Skip is Log and SkipNow.
func (b *B) Skip(args ...any)

// Skipf is Logf and SkipNow.
func (b *B) Skipf(format string, args ...any)

// named does nothing: go test -v names no benchmark ahead of what it logs.
func (b *B) named() {}

// exit leaves the benchmark: to RunBenchmark on its cog, and on another the
// goroutine ends.
func (b *B) exit()

// StartTimer starts timing, as the benchmark begins with it started. It resumes
// what StopTimer stopped.
func (b *B) StartTimer() {
	if !b.timerOn {
		b.start = clock()
		b.timerOn = true
		b.loopI &^= loopPoison
	}
}

// StopTimer stops timing, so what follows is not measured.
func (b *B) StopTimer() {
	if b.timerOn {
		b.clocks += clock() - b.start
		b.timerOn = false
		b.loopI |= loopPoison
	}
}

// ResetTimer zeroes the time measured and forgets the metrics ReportMetric reported.
// It does not start or stop the timer.
func (b *B) ResetTimer() {
	if b.timerOn {
		b.start = clock()
	}
	b.clocks = 0
	b.nextra = 0
}

// SetBytes records the bytes one operation processes, which reports MB/s too.
func (b *B) SetBytes(n int64) { b.bytes = n }

// ReportAllocs reports the allocations of each operation, which are none: nothing
// allocates.
func (b *B) ReportAllocs() { b.allocs = true }

// ReportMetric adds "n unit" to what the benchmark reports, replacing what was
// reported for unit before; "ns/op", "MB/s", "B/op" and "allocs/op" replace the
// measured values, and an "ns/op" of 0 leaves the time out. A benchmark reports 16
// metrics at most.
func (b *B) ReportMetric(n float64, unit string) {
	if unit == "" {
		panic("metric unit must not be empty")
	}
	for i := 0; i < len(unit); i++ {
		if spaceLen(unit, i) != 0 {
			panic("metric unit must not contain whitespace")
		}
	}
	k := b.metric(unit)
	if k < 0 {
		if b.nextra == maxMetrics {
			panic("testing: more than 16 metrics reported")
		}
		k = b.nextra
		b.nextra++
		b.units[k] = unit
	}
	b.values[k] = n
}

func (b *B) metric(unit string) int {
	for i := 0; i < b.nextra; i++ {
		if b.units[i] == unit {
			return i
		}
	}
	return -1
}

// Loop reports whether the benchmark is to run its loop's body once more. Its
// first call resets the timer and the one that returns false stops it, setting b.N
// to the count it ran; until then it grows the count as go test grows b.N, until
// the time its loop took reaches -benchtime, or runs the count -benchtime gave.
//
// Its fast path is what a benchmark's loop costs each time round, and is inlined:
// the fields it reads follow N, which a loop to b.N reads, at the head of the
// struct, and its first statement reads one, which is what checks b, once.
func (b *B) Loop() bool {
	i := b.loopI
	if i < b.loopN {
		b.loopI = i + 1
		return true
	}
	return b.loopNext()
}

// loopNext is Loop where its count has run out, where it begins, and where the
// timer was stopped, which is a failure: go test's loopSlowPath.
func (b *B) loopNext() bool {
	if !b.timerOn {
		b.Fatalf("B.Loop called with timer stopped")
	}
	if b.loopN == 0 {
		b.loopN = 1
		if benchCount != 0 {
			b.loopN = benchCount
		}
		b.N = 0
		b.ResetTimer()
		b.loopI++
		return true
	}
	if benchCount == 0 && b.loopMore() {
		b.loopI++
		return true
	}
	b.StopTimer()
	b.N = b.loopN
	b.loopDone = true
	return false
}

// loopMore reports whether Loop is to run on, the count grown, where the time its
// loop took has not reached -benchtime: go test's stopOrScaleBLoop.
func (b *B) loopMore() bool {
	c := b.clocks + clock() - b.start
	ns := int64(nanos(c))
	if ns >= benchTime {
		return false
	}
	prev := b.loopN
	b.loopN = int(predictN(benchTime, int64(prev), ns, int64(prev)))
	return prev < b.loopN
}

// Run runs f as a sub-benchmark of b called name, as go test runs a benchmark, and
// reports whether it did not fail. A sub-benchmark that fails fails b, and b, having
// one, is not measured: run once with b.N 1, it prints no line of its own.
func (b *B) Run(name string, f func(b *B)) bool {
	b.hasSub = true
	var s B
	if !b.begin(&s, name) {
		return s.skipped
	}
	s.bench(f)
	if s.failed {
		b.failed = true
	}
	return !s.failed
}

// begin sets s up as b's sub-benchmark called name, named as a subtest is, and
// prints its name, or reports that it does not run: no more fit, or, s marked
// skipped, -bench does not select it. One -bench selects only in part runs only to
// run its own.
func (b *B) begin(s *B, name string) bool {
	if nsubs == maxSubs {
		printf("    testing: more than %d sub-benchmarks in %s\n", maxSubs, top)
		b.failed = true
		return false
	}
	s.slot = nsubs
	subs[nsubs] = sub{parent: b.slot, name: name}
	nsubs++
	unique(s.slot)
	if m := matcher; m != nil {
		switch m(b.slot, s.slot, ownName(s.slot)) {
		case 0:
			s.skipped = true
			return false
		case 2:
			s.hasSub = true
		}
	}
	w := NewBuilder(s.buf[:])
	nw := nameWriter{mode: nameWrite, b: &w}
	writeName(&nw, s.slot)
	s.name = w.String()
	FullName(s.slot, true)
	println()
	return true
}

// StartBenchmarks prints what go test prints ahead of the first benchmark, the
// labels, and sets how the benchmarks after it run: each until it has run for d
// nanoseconds, or n times where n is not 0, its result's name padded to width, its
// allocations reported where mem says so. It is what the runner "ogo test"
// generates calls; a benchmark does not call it.
func StartBenchmarks(labels string, width int, d int64, n int, mem bool) {
	print(labels)
	benchWidth, benchTime, benchCount, benchMem = width, d, n, mem
}

// RunBenchmark runs f as the benchmark name, with b its state, as go test does, and
// prints what go test -v prints of it (bench). It is what the runner "ogo test"
// generates calls; a benchmark does not call it.
func RunBenchmark(b *B, name string, f func(*B)) {
	b.name = name
	b.slot = -1
	top = name
	nsubs = 0
	println(name)
	b.bench(f)
}

// bench runs f with b its state as go test runs a benchmark: once with b.N 1, and
// where that run neither failed, stopped nor ran a sub-benchmark, again with b.N
// grown until a run takes the time StartBenchmarks set, or once more with the count
// it set -- unless the first run's Loop grew its own -- and prints the result. A run
// after the first that stops the benchmark with SkipNow prints the result of none,
// as go test's does.
func (b *B) bench(f func(*B)) {
	if n := fullLen(b.slot) + 1; n > benchWidth {
		benchWidth = n + 8
	}
	b.runN(f, 1)
	switch {
	case b.failed:
		b.verdict("--- FAIL: ")
		return
	case b.skipped:
		b.verdict("--- SKIP: ")
		return
	case b.hasSub:
		return
	case b.loopN != 0:
	case benchCount > 1:
		b.runN(f, benchCount)
	case benchCount == 0:
		for n := int64(1); !b.failed && !b.skipped && int64(nanos(b.clocks)) < benchTime && n < 1000000000; {
			last := n
			n = predictN(benchTime, int64(b.N), int64(nanos(b.clocks)), last)
			b.runN(f, int(n))
		}
	}
	switch {
	case b.failed:
		b.verdict("--- FAIL: ")
	case b.skipped:
		b.none()
	default:
		b.result()
	}
}

func (b *B) verdict(v string) {
	print(v)
	printFull(b.slot)
	println()
}

func (b *B) runN(f func(*B), n int) {
	b.N = n
	b.loopN, b.loopI, b.loopDone = 0, 0, false
	b.ResetTimer()
	b.StartTimer()
	runB(b, f)
	b.StopTimer()
	if b.loopN != 0 && !b.loopDone && !b.failed && !b.skipped {
		b.Errorf("benchmark function returned without B.Loop() == false (break or return in loop?)")
	}
}

// predictN is go test's: the next b.N, 1.2 times what the last run says reaches
// goalns, growing at most a hundredfold and at least by one, and 1e9 at most.
func predictN(goalns, prevIters, prevns, last int64) int64 {
	if prevns == 0 {
		prevns = 1
	}
	n := goalns * prevIters / prevns
	n += n / 5
	if n > 100*last {
		n = 100 * last
	}
	if n < last+1 {
		n = last + 1
	}
	if n > 1000000000 {
		n = 1000000000
	}
	return n
}

// nanos is clocks of the system clock in nanoseconds, truncated as Go's clock is.
func nanos(clocks uint64) uint64 {
	hz := uint64(clockHz())
	return clocks/hz*1000000000 + clocks%hz*1000000000/hz
}

// result prints a benchmark's line as go test does: the name, b.N, the time an
// operation took, MB/s where SetBytes said what one processes, the metrics
// ReportMetric reported in the order of their units, and the allocations where
// asked for.
func (b *B) result() {
	b.printPadded()
	printf("\t%8d", b.N)
	ns := nanos(b.clocks)
	if k := b.metric("ns/op"); k >= 0 {
		if b.values[k] != 0 {
			print("\t")
			prettyPrint(b.values[k], "ns/op")
		}
	} else if ns != 0 {
		print("\t")
		printNsPerOp(ns, uint64(b.N))
	}
	var mbs float64
	if k := b.metric("MB/s"); k >= 0 {
		mbs = b.values[k]
	} else if b.bytes > 0 && ns > 0 && b.N > 0 {
		mbs = float64(b.bytes) * float64(b.N) / 1e6 / (float64(ns) / 1e9)
	}
	if mbs != 0 {
		printf("\t%7.2f MB/s", mbs)
	}
	var order [maxMetrics]int
	n := 0
	for i := 0; i < b.nextra; i++ {
		switch b.units[i] {
		case "ns/op", "MB/s", "B/op", "allocs/op":
			continue
		}
		j := n
		for ; j > 0 && b.units[i] < b.units[order[j-1]]; j-- {
			order[j] = order[j-1]
		}
		order[j] = i
		n++
	}
	for i := 0; i < n; i++ {
		print("\t")
		prettyPrint(b.values[order[i]], b.units[order[i]])
	}
	if benchMem || b.allocs {
		var bop, aop int64
		if k := b.metric("B/op"); k >= 0 {
			bop = int64(b.values[k])
		}
		if k := b.metric("allocs/op"); k >= 0 {
			aop = int64(b.values[k])
		}
		printf("\t%8d B/op\t%8d allocs/op", bop, aop)
	}
	println()
}

// none prints go test's line for a benchmark stopped by SkipNow after its first run,
// which reports no result at all: b.N 0 and an ns/op of 0/0.
func (b *B) none() {
	b.printPadded()
	printf("\t       0\t               NaN ns/op")
	if benchMem || b.allocs {
		printf("\t       0 B/op\t       0 allocs/op")
	}
	println()
}

// printPadded prints the benchmark's full name, padded to its column.
func (b *B) printPadded() {
	printFull(b.slot)
	for i := fullLen(b.slot); i < benchWidth; i++ {
		print(" ")
	}
}

// printFull prints the full name of the benchmark in slot, -1 for the one the
// runner runs, which every benchmark program prints and only one running a
// sub-benchmark prints through names.
func printFull(slot int) {
	if slot < 0 {
		print(top)
	} else {
		names(slot, true)
	}
}

// fullLen is the length of what printFull(slot) prints.
func fullLen(slot int) int {
	if slot < 0 {
		return len(top)
	}
	return names(slot, false)
}

// prettyPrint is go test's: ten places before the point, and four significant
// digits for a number below 1000.
func prettyPrint(x float64, unit string) {
	y := x
	if y < 0 {
		y = -y
	}
	switch {
	case y == 0 || y >= 999.95:
		printf("%10.0f %s", x, unit)
	case y >= 99.995:
		printf("%12.1f %s", x, unit)
	case y >= 9.9995:
		printf("%13.2f %s", x, unit)
	case y >= 0.99995:
		printf("%14.3f %s", x, unit)
	case y >= 0.099995:
		printf("%15.4f %s", x, unit)
	case y >= 0.0099995:
		printf("%16.5f %s", x, unit)
	case y >= 0.00099995:
		printf("%17.6f %s", x, unit)
	default:
		printf("%18.7f %s", x, unit)
	}
}

// printNsPerOp prints ns/n as prettyPrint lays it out, " ns/op" after it. It is
// computed exactly in integers, the places prettyPrint's bounds choose, rounded half
// to even as Go rounds a float it prints: written while a float64 had 32 bits, whose
// seven digits would round a long benchmark's time, and kept, integers costing the
// target less than a float64 in software.
func printNsPerOp(ns, n uint64) {
	digits := 7
	for k, scale := 0, uint64(100); k < 7; k, scale = k+1, scale*10 {
		if ns*scale >= n*99995 {
			digits = k
			break
		}
	}
	scale := uint64(1)
	for i := 0; i < digits; i++ {
		scale *= 10
	}
	v, r := ns*scale/n, ns*scale%n
	if r*2 > n || r*2 == n && v%2 == 1 {
		v++
	}
	whole, frac := v/scale, v%scale
	width := 10
	if digits > 0 {
		width = 11 + digits
	}
	w := digits
	if digits > 0 {
		w++
	}
	for q := whole; ; q /= 10 {
		w++
		if q < 10 {
			break
		}
	}
	for ; w < width; w++ {
		print(" ")
	}
	print(whole)
	if digits > 0 {
		print(".")
		for p := scale / 10; p > 1 && frac < p; p /= 10 {
			print(0)
		}
		print(frac)
	}
	print(" ns/op")
}

// runB calls f(b) where exit returns to.
func runB(t *B, f func(*B))

// clock is the system clock's count of all 64 bits.
func clock() uint64

// clockHz is the rate the system clock counts at.
func clockHz() uint32
`

// errorsSrc is the errors package: Go's, but for Join. Two of its functions are
// written by the compiler and have no body here. New is the address of an
// errorString made where the call stands (errorsNewC), static in a package
// variable's initializer and the block's in a function, as make makes a channel --
// so a function's error is refused where it would outlive the function, by the rules
// that refuse a composite literal's address. And asTarget, As's question whether an
// error's concrete value is assignable to what target points at, which Go's asks of
// reflection and the compiler answers by testing target's table against every table
// of the empty interface the program makes (mintErrorsAsTarget). The rest -- Is, As's
// walk of the tree, Unwrap -- is ordinary OctoGo over interfaces, as Go's is.
const errorsSrc = `// Package errors is Go's errors but for Join: New, Is, As, Unwrap and
// ErrUnsupported, each meaning what Go's of the same name means.
//
// New makes its error where the call stands, as make makes a channel, since there
// is no heap to make it on: in a package variable's initializer it is static, the
// sentinel idiom,
//
//	var ErrTimeout = errors.New("timeout")
//
// and in a function it is the block's, so the lifetime rules keep it from being
// returned or kept, as they keep the address of a composite literal; the error a
// function returns is declared at package level. Join is not provided: the error it
// returns holds a copy of its arguments, which needs somewhere to live.

type errorString struct {
	s string
}

func (e *errorString) Error() string {
	return e.s
}

// New returns an error whose Error method returns text. Each call of New is a
// distinct error, even if the text is identical. The compiler writes it at the
// call, as the address of an errorString made where the call stands.
func New(text string) error

// ErrUnsupported indicates that a requested operation cannot be performed,
// because it is unsupported.
var ErrUnsupported = New("unsupported operation")

type wrapper interface {
	Unwrap() error
}

type multiWrapper interface {
	Unwrap() []error
}

type iser interface {
	Is(error) bool
}

type aser interface {
	As(any) bool
}

// Unwrap returns the result of calling the Unwrap method on err, if err's type
// contains an Unwrap method returning error. Otherwise, Unwrap returns nil.
//
// Unwrap only calls a method of the form "Unwrap() error". In particular Unwrap
// does not unwrap errors returned by Join.
func Unwrap(err error) error {
	u, ok := err.(wrapper)
	if !ok {
		return nil
	}
	return u.Unwrap()
}

// Is reports whether any error in err's tree matches target.
//
// The tree consists of err itself, followed by the errors obtained by repeatedly
// calling its Unwrap() error or Unwrap() []error method. When err wraps multiple
// errors, Is examines err followed by a depth-first traversal of its children.
//
// An error is considered to match a target if it is equal to that target or if it
// implements a method Is(error) bool such that Is(target) returns true.
//
// An interface holds a pointer here, which is always comparable, so the equality is
// asked of every error in the tree.
func Is(err, target error) bool {
	if err == nil || target == nil {
		return err == target
	}
	var w walk
	for ; err != nil; err = w.next(err) {
		if err == target {
			return true
		}
		if x, ok := err.(iser); ok && x.Is(target) {
			return true
		}
	}
	return false
}

// maxJoinDepth is how deeply errors of the form Unwrap() []error may nest in a tree
// Is and As walk. Go's walk recurses there, and a goroutine whose calls recurse has
// no stack bound the build can read, so this one keeps the lists it is in the middle
// of in a stack of its own, of this many. A chain of Unwrap() error is no deeper
// for any length.
const maxJoinDepth = 8

// walk is the depth-first order Is and As visit an error tree in, Go's: an error,
// then what its Unwrap() error returns, or each error its Unwrap() []error returns
// with that error's own tree before the next.
type walk struct {
	lists [maxJoinDepth][]error
	n     int
}

// next answers the error to visit after err, nil when the tree is done. Each
// Unwrap is called once, where Go's walk calls it, and a nil in a list is skipped.
func (w *walk) next(err error) error {
	switch x := err.(type) {
	case wrapper:
		if u := x.Unwrap(); u != nil {
			return u
		}
	case multiWrapper:
		if w.n == maxJoinDepth {
			panic("errors: Unwrap() []error nested too deeply")
		}
		w.lists[w.n] = x.Unwrap()
		w.n++
	}
	for w.n != 0 {
		list := w.lists[w.n-1]
		if len(list) == 0 {
			w.n--
			continue
		}
		w.lists[w.n-1] = list[1:]
		if list[0] != nil {
			return list[0]
		}
	}
	return nil
}

// As finds the first error in err's tree that matches target, and if one is found,
// sets target to that error value and returns true. Otherwise, it returns false.
//
// The tree consists of err itself, followed by the errors obtained by repeatedly
// calling its Unwrap() error or Unwrap() []error method. When err wraps multiple
// errors, As examines err followed by a depth-first traversal of its children.
//
// An error matches target if the error's concrete value is assignable to the value
// pointed to by target, or if the error has a method As(any) bool such that
// As(target) returns true. In the latter case, the As method is responsible for
// setting target.
//
// As panics if target is not a non-nil pointer to either a type that implements
// error, or to any interface type; the compiler refuses such a target where it can
// see its type.
func As(err error, target any) bool {
	if err == nil {
		return false
	}
	if target == nil {
		panic("errors: target cannot be nil")
	}
	var w walk
	for ; err != nil; err = w.next(err) {
		if asTarget(err, target) {
			return true
		}
		if x, ok := err.(aser); ok && x.As(target) {
			return true
		}
	}
	return false
}

// asTarget reports whether err's concrete value is assignable to what target points
// at, and if it is, stores it there. It panics as As does for a target that is no
// non-nil pointer to an interface or to a type implementing error. The compiler
// writes it, testing target's table against every table of the empty interface the
// program makes.
func asTarget(err error, target any) bool
`

// stringsSrc is the strings package: the allocation-free part of Go's. It is
// ordinary OctoGo, compiled like any other package -- nothing in it is intrinsic
// and nothing in it is C -- which is the point as much as the functions are. A
// standard library a language cannot express is a standard library written in
// something else.
// bytesSrc is the bytes package: the allocation-free part of Go's. It is the
// strings package over a byte slice, which is what a program reading a device has
// -- the string conversion of a received buffer allocates, and comparing bytes by
// hand is where a parser gets its edges wrong.
const bytesSrc = `// Package bytes is the allocation-free part of Go's bytes.
//
// It is the strings package over a byte slice: everything here either answers a
// question about one -- a bool, an int -- or returns a SUBSLICE of it, which costs
// nothing, a slice being a pointer and a length. What is missing is what allocates.
// Split and Fields want a slice of slices; Join, Repeat, Replace and ToUpper want
// bytes that did not exist before; there is no heap here for either, and a program
// that must build bytes writes into memory it owns.
//
// This is what a program reading a device reaches for: what arrives is bytes, and
// string(b) is a copy the target cannot make. Each function means exactly what Go's
// of the same name means, including for an empty argument, for a nil one -- which
// is an empty one, as in Go -- and for invalid UTF-8.

// Compare returns -1 if a sorts before b, 0 if they are equal, and 1 if a sorts
// after b: the bytes in order, and where one is a prefix of the other, the shorter
// first.
func Compare(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

// Equal reports whether a and b hold the same bytes.
func Equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Contains reports whether subslice is within b.
func Contains(b, subslice []byte) bool {
	return Index(b, subslice) >= 0
}

// ContainsAny reports whether any rune of chars is within b.
func ContainsAny(b []byte, chars string) bool {
	return IndexAny(b, chars) >= 0
}

// ContainsRune reports whether r is within b.
func ContainsRune(b []byte, r rune) bool {
	return IndexRune(b, r) >= 0
}

// Count counts the non-overlapping instances of sep in s. If sep is empty it
// returns 1 plus the number of runes in s, as Go's does.
func Count(s, sep []byte) int {
	if len(sep) == 0 {
		n := 1
		i := 0
		for i < len(s) {
			_, w := decodeRune(s[i:])
			i = i + w
			n = n + 1
		}
		return n
	}
	n := 0
	i := 0
	for i+len(sep) <= len(s) {
		if Equal(s[i:i+len(sep)], sep) {
			n = n + 1
			i = i + len(sep)
		} else {
			i = i + 1
		}
	}
	return n
}

// Cut slices s around the first instance of sep, returning what precedes it and
// what follows it. found reports whether sep appears at all; if it does not, Cut
// returns s, nil, false.
func Cut(s, sep []byte) ([]byte, []byte, bool) {
	i := Index(s, sep)
	if i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	var none []byte
	return s, none, false
}

// CutPrefix returns s without its leading prefix and reports whether it had one.
// If it did not, CutPrefix returns s, false.
func CutPrefix(s, prefix []byte) ([]byte, bool) {
	if HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// CutSuffix returns s without its trailing suffix and reports whether it had one.
func CutSuffix(s, suffix []byte) ([]byte, bool) {
	if HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}

// HasPrefix reports whether s begins with prefix.
func HasPrefix(s, prefix []byte) bool {
	return len(s) >= len(prefix) && Equal(s[:len(prefix)], prefix)
}

// HasSuffix reports whether s ends with suffix.
func HasSuffix(s, suffix []byte) bool {
	return len(s) >= len(suffix) && Equal(s[len(s)-len(suffix):], suffix)
}

// Index returns the index of the first instance of sep in s, or -1. An empty sep
// is at 0, as Go has it.
func Index(s, sep []byte) int {
	n := len(sep)
	if n == 0 {
		return 0
	}
	if n > len(s) {
		return -1
	}
	for i := 0; i+n <= len(s); i++ {
		if Equal(s[i:i+n], sep) {
			return i
		}
	}
	return -1
}

// IndexAny returns the byte index of the first rune of s that is also in chars, or
// -1 if there is none. An invalid encoding in s is the rune U+FFFD, so chars
// holding U+FFFD finds the first bad byte -- which is what Go's does.
func IndexAny(s []byte, chars string) int {
	if len(chars) == 0 {
		return -1
	}
	i := 0
	for i < len(s) {
		r, w := decodeRune(s[i:])
		for _, c := range chars {
			if c == r {
				return i
			}
		}
		i = i + w
	}
	return -1
}

// IndexByte returns the index of the first instance of c in b, or -1.
func IndexByte(b []byte, c byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// IndexRune returns the byte index of the first instance of r in s, or -1. Asking
// for U+FFFD finds the first byte of an invalid encoding, as it does in Go, and a
// rune that is no code point at all is nowhere.
func IndexRune(s []byte, r rune) int {
	i := 0
	for i < len(s) {
		c, w := decodeRune(s[i:])
		if c == r {
			return i
		}
		i = i + w
	}
	return -1
}

// LastIndex returns the index of the last instance of sep in s, or -1. An empty
// sep is at len(s), as Go has it.
func LastIndex(s, sep []byte) int {
	n := len(sep)
	if n == 0 {
		return len(s)
	}
	for i := len(s) - n; i >= 0; i-- {
		if Equal(s[i:i+n], sep) {
			return i
		}
	}
	return -1
}

// LastIndexByte returns the index of the last instance of c in s, or -1.
func LastIndexByte(s []byte, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// TrimPrefix returns s without its leading prefix. If it has none, s is returned
// unchanged.
func TrimPrefix(s, prefix []byte) []byte {
	if HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

// TrimSuffix returns s without its trailing suffix.
func TrimSuffix(s, suffix []byte) []byte {
	if HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// TrimSpace returns s without leading and trailing white space, as Unicode defines
// it -- not merely as ASCII does, which would leave a non-breaking space behind and
// look right until it did not.
func TrimSpace(s []byte) []byte {
	start := 0
	for start < len(s) {
		r, w := decodeRune(s[start:])
		if !isSpace(r) {
			break
		}
		start = start + w
	}
	end := start
	i := start
	for i < len(s) {
		r, w := decodeRune(s[i:])
		i = i + w
		if !isSpace(r) {
			end = i
		}
	}
	return s[start:end]
}

// decodeRune decodes the UTF-8 encoding at the start of b: the code point and the
// bytes it took. An invalid, overlong, surrogate or out-of-range encoding is U+FFFD
// and one byte, and an empty b is U+FFFD and no bytes -- which is what Go's
// utf8.DecodeRune answers and what ranging a string here does.
func decodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return 0xFFFD, 0
	}
	b0 := b[0]
	if b0 < 0x80 {
		return rune(b0), 1
	}
	if b0 < 0xC0 {
		return 0xFFFD, 1
	}
	if b0 < 0xE0 {
		if len(b) < 2 || b[1]&0xC0 != 0x80 {
			return 0xFFFD, 1
		}
		r := rune(b0&0x1F)<<6 | rune(b[1]&0x3F)
		if r < 0x80 {
			return 0xFFFD, 1
		}
		return r, 2
	}
	if b0 < 0xF0 {
		if len(b) < 3 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 {
			return 0xFFFD, 1
		}
		r := rune(b0&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
		if r < 0x800 || (r >= 0xD800 && r <= 0xDFFF) {
			return 0xFFFD, 1
		}
		return r, 3
	}
	if b0 < 0xF8 {
		if len(b) < 4 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 || b[3]&0xC0 != 0x80 {
			return 0xFFFD, 1
		}
		r := rune(b0&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
		if r < 0x10000 || r > 0x10FFFF {
			return 0xFFFD, 1
		}
		return r, 4
	}
	return 0xFFFD, 1
}

// isSpace is unicode.IsSpace for the code points Go's bytes package trims.
func isSpace(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' {
		return true
	}
	if r == 0x85 || r == 0xA0 || r == 0x1680 {
		return true
	}
	if r >= 0x2000 && r <= 0x200A {
		return true
	}
	return r == 0x2028 || r == 0x2029 || r == 0x202F || r == 0x205F || r == 0x3000
}
`

const stringsSrc = `// Package strings is the allocation-free part of Go's strings.
//
// Everything here either answers a question about a string -- a bool, an int -- or
// returns a SUBSTRING of one, which costs nothing: a string is a pointer and a
// length, so a slice of one points into the same bytes and allocates nothing at
// all. What is missing is what allocates. Split, Join, Repeat, Replace, ToUpper and
// the rest need somewhere to put a string that did not exist before, and there is
// no heap here to put it; Builder is how a program makes one, over memory it owns.
//
// Each function here means exactly what Go's of the same name means, including for
// an empty argument and for invalid UTF-8. That is worth more than breadth: a
// function that is nearly Go's is worse than one that is missing, because it
// compiles.

// Compare returns -1 if a sorts before b, 0 if they are equal, and 1 if a sorts
// after b. It is included for symmetry with Go's; use ==, < and > directly, which
// is what Go says too.
func Compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// Contains reports whether substr is within s.
func Contains(s, substr string) bool {
	return Index(s, substr) >= 0
}

// ContainsAny reports whether any rune of chars is within s.
func ContainsAny(s, chars string) bool {
	return IndexAny(s, chars) >= 0
}

// ContainsRune reports whether r is within s.
func ContainsRune(s string, r rune) bool {
	return IndexRune(s, r) >= 0
}

// Count counts the non-overlapping instances of substr in s. If substr is empty it
// returns 1 plus the number of runes in s, as Go's does.
func Count(s, substr string) int {
	if len(substr) == 0 {
		n := 1
		for range s {
			n = n + 1
		}
		return n
	}
	n := 0
	i := 0
	for i+len(substr) <= len(s) {
		if s[i:i+len(substr)] == substr {
			n = n + 1
			i = i + len(substr)
		} else {
			i = i + 1
		}
	}
	return n
}

// Cut slices s around the first instance of sep, returning what precedes it and
// what follows it. found reports whether sep appears at all; if it does not, Cut
// returns s, "", false.
func Cut(s, sep string) (string, string, bool) {
	i := Index(s, sep)
	if i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// CutPrefix returns s without its leading prefix and reports whether it had one.
// If it did not, CutPrefix returns s, false.
func CutPrefix(s, prefix string) (string, bool) {
	if HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// CutSuffix returns s without its trailing suffix and reports whether it had one.
func CutSuffix(s, suffix string) (string, bool) {
	if HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}

// HasPrefix reports whether s begins with prefix.
func HasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// HasSuffix reports whether s ends with suffix.
func HasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// Index returns the byte index of the first instance of substr in s, or -1. An
// empty substr is at 0, as Go has it.
func Index(s, substr string) int {
	n := len(substr)
	if n == 0 {
		return 0
	}
	if n > len(s) {
		return -1
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == substr {
			return i
		}
	}
	return -1
}

// IndexAny returns the byte index of the first rune of s that is also in chars, or
// -1 if there is none.
func IndexAny(s, chars string) int {
	if len(chars) == 0 {
		return -1
	}
	for i, c := range s {
		if ContainsRune(chars, c) {
			return i
		}
	}
	return -1
}

// IndexByte returns the index of the first instance of c in s, or -1.
func IndexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// IndexRune returns the byte index of the first instance of r in s, or -1. Ranging
// a string yields U+FFFD for each byte of an invalid encoding, so asking for
// U+FFFD finds the first such byte -- which is what Go's does.
func IndexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

// LastIndex returns the byte index of the last instance of substr in s, or -1. An
// empty substr is at len(s), as Go has it.
func LastIndex(s, substr string) int {
	n := len(substr)
	if n == 0 {
		return len(s)
	}
	for i := len(s) - n; i >= 0; i-- {
		if s[i:i+n] == substr {
			return i
		}
	}
	return -1
}

// LastIndexByte returns the index of the last instance of c in s, or -1.
func LastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// TrimPrefix returns s without its leading prefix. If it has none, s is returned
// unchanged.
func TrimPrefix(s, prefix string) string {
	if HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

// TrimSuffix returns s without its trailing suffix.
func TrimSuffix(s, suffix string) string {
	if HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// TrimSpace returns s without leading and trailing white space, as Unicode defines
// it -- not merely as ASCII does, which would leave a non-breaking space behind and
// look right until it did not.
func TrimSpace(s string) string {
	start := len(s)
	for i, c := range s {
		if !isSpace(c) {
			start = i
			break
		}
	}
	if start == len(s) {
		return ""
	}
	// end is just past the last rune that is not a space: the index the range hands
	// the rune after it, or the length. A rune's own width cannot say where it ends
	// -- an invalid byte ranges as U+FFFD, which is three bytes when it is valid --
	// and "a\xff" was cut to s[0:4] of a string of two bytes, a panic.
	end, last := start, false
	for i, c := range s {
		if last {
			end = i
		}
		last = i >= start && !isSpace(c)
	}
	if last {
		end = len(s)
	}
	return s[start:end]
}

// isSpace reports whether r is white space, which is Unicode's White_Space
// property: a short, closed list, so this is exact rather than an approximation of
// what unicode.IsSpace answers.
func isSpace(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' {
		return true
	}
	if r == 0x85 || r == 0xA0 || r == 0x1680 {
		return true
	}
	if r >= 0x2000 && r <= 0x200A {
		return true
	}
	return r == 0x2028 || r == 0x2029 || r == 0x202F || r == 0x205F || r == 0x3000
}
`

type importTask struct {
	sync.Mutex
	p     *Package
	ready chan struct{}
}

// BuildContext coordinates creating a package tree.
type BuildContext struct {
	errMu     sync.Mutex
	importsMu sync.Mutex

	errList     ErrList
	fsys        fs.FS
	modulePath  string                 // the module import paths are written against, "" when there is none
	mainDir     string                 // the main package's directory within fsys
	importTasks map[string]*importTask // package identity (see importIdent): importTask
	importGraph map[string]map[string]bool
	limit       int

	// srcFiles maps a source to the file parsed from it, for every file of every
	// package this build reads, so that a token -- which carries its source --
	// leads back to the file that wrote it, and a name written there resolves in
	// that file's scope (see File.fileOfToken). Packages are parsed concurrently,
	// hence the lock.
	filesMu  sync.Mutex
	srcFiles map[*source]*File

	noDeclarationChecks bool
}

// NewBuildContext returns a newly created BuildContext. 'limit' is the maximum
// desired concurrency for individual package building when > 0.
func NewBuildContext(fsys fs.FS, limit int) (c *BuildContext) {
	return &BuildContext{
		fsys:        fsys,
		srcFiles:    map[*source]*File{},
		importTasks: map[string]*importTask{},
		importGraph: map[string]map[string]bool{},
		limit:       limit,
	}
}

// importDir is the directory within c.fsys that an import path names, and whether
// the path belongs to the module being built at all. Without a module the path IS
// the directory, which is what every build did before ogo.mod existed: a program of
// several packages is one directory tree and the one being built is its root.
//
// With a module the path is written as Go writes one, module path and all, and the
// prefix is stripped here. That is what makes an import mean the same thing in every
// file that writes it, whichever directory the build was started from.
func (c *BuildContext) importDir(importPath string) (dir string, ok bool) {
	if c.modulePath == "" {
		return importPath, true
	}

	switch {
	case importPath == c.modulePath:
		return ".", true
	case strings.HasPrefix(importPath, c.modulePath+"/"):
		return importPath[len(c.modulePath)+1:], true
	}

	return "", false
}

// pkgPath is a package's identity inside the compiler: its directory within the
// module. The module path is surface syntax and is stripped before this, so a
// dotted or hyphenated one never reaches the C mangler and example.com/proj/sensor
// is the same package, and the same C symbols, that sensor was before modules.
//
// The module root is a package too, and takes the module path's last element -- the
// name a Go program would import it under. A directory of that name beside it would
// claim the same identity, which importCollision refuses rather than silently merge.
func (c *BuildContext) pkgPath(dir string) string {
	if dir == "." {
		return path.Base(c.modulePath)
	}

	return dir
}

// notInModule explains an import path the module does not contain. A path that
// names a real directory of the module, written without the module prefix, is the
// mistake a Go programmer does not make and everyone else does, so it is named.
func (c *BuildContext) notInModule(importPath string) string {
	if fi, err := fs.Stat(c.fsys, importPath); err == nil && fi.IsDir() {
		return fmt.Sprintf("cannot find package %q in module %q, did you mean %q?", importPath, c.modulePath, c.modulePath+"/"+importPath)
	}

	return fmt.Sprintf("package %q is not in module %q", importPath, c.modulePath)
}

func (c *BuildContext) syncErr(pos token.Position, s string, args ...any) {
	c.errMu.Lock()

	defer c.errMu.Unlock()

	c.err(pos, s, args...)
}

func (c *BuildContext) err(pos token.Position, s string, args ...any) {
	c.errList.AddErr(pos, s, args...)
}

// findCycle performs a DFS to see if 'target' is reachable from 'current'.
// It returns the path of the cycle if one exists.
func (c *BuildContext) findCycle(current, target string, visited map[string]bool) []string {
	if current == target {
		return []string{current}
	}
	visited[current] = true

	for next := range c.importGraph[current] {
		if !visited[next] {
			if cycle := c.findCycle(next, target, visited); cycle != nil {
				return append([]string{current}, cycle...)
			}
		}
	}
	return nil
}

// importIdent is a package's identity within a build -- what the import graph, the
// cycle check and the one-build-per-package map must agree to call it -- together
// with the directory to read it from. The identity is the directory, not the path
// written to reach it: the module prefix is surface syntax, and 'fromPath' arrives
// here as a package's ImportPath, which is already stripped of it. A graph with one
// spelling on one side of an edge and the other on the other side connects nothing,
// and an import cycle then deadlocks on its own task instead of being reported.
//
// An embedded or intrinsic package (testing, p2, strings) has no directory and
// stands for itself, module or no module, as std does in Go.
func (c *BuildContext) importIdent(importPath string) (key, dir string, ok bool) {
	if _, embedded := embeddedPkgs[importPath]; embedded || intrinsicImports[importPath] {
		return importPath, "", true
	}

	if dir, ok = c.importDir(importPath); !ok {
		return "", "", false
	}

	return c.pkgPath(dir), dir, true
}

// windowsReserved are the MS-DOS device names Windows still reserves. No file or
// directory may carry one, in any case, with or without an extension -- NUL.txt is
// NUL -- so a repository holding con/ or aux.ogo cannot be checked out on Windows
// at all. Git reports the failure against the checkout, far from whoever wrote it,
// and on Linux there is nothing to notice: the names are ordinary words there, and
// con, aux and prn are plausible package names on a microcontroller.
//
// The list is Microsoft's own (Naming Files, Paths, and Namespaces), COM0 and LPT0
// included. The superscript variants COM1 and so on are outside the ASCII a package
// directory is already held to.
var windowsReserved = func() map[string]bool {
	r := map[string]bool{"con": true, "prn": true, "aux": true, "nul": true}
	for c := '0'; c <= '9'; c++ {
		r["com"+string(c)] = true
		r["lpt"+string(c)] = true
	}
	return r
}()

// reservedFileName returns the device name a source file's name collides with on
// Windows, or "". The extension does not save it: aux.ogo is aux.
func reservedFileName(name string) string {
	base := strings.TrimSuffix(path.Base(name), ".ogo")
	if windowsReserved[strings.ToLower(base)] {
		return base
	}

	return ""
}

// validPkgDir reports whether a module-relative package DIRECTORY can exist, beside
// its siblings, on every filesystem this compiler targets. It is a narrower rule
// than the one an import path as a whole obeys, and deliberately so: the module
// prefix of a path is a repository name that never reaches a filesystem, and
// example.com/BurntSushi/toml has to remain spellable, while what follows it is
// created by `mkdir` on macOS and Windows too.
//
// Lower case is the load-bearing part. A module holding both foo/ and Foo/ cannot be
// checked out on a case-insensitive filesystem at all, let alone built, and the
// developer who wrote it -- on Linux, where both exist happily -- learns this from
// whoever clones it. Refusing the capital reports it on the first build instead.
func validPkgDir(dir string) (bad, why string) {
	if dir == "" || dir == "." {
		return "", ""
	}

	for _, v := range strings.Split(dir, "/") {
		if strings.HasPrefix(v, "-") {
			return v, "must not begin with '-'"
		}

		for _, c := range v {
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
				// ok
			case c >= 'A' && c <= 'Z':
				return v, "must be lower case: a directory differing from another only in case is the SAME directory on macOS and Windows"
			default:
				return v, "must be made of lower-case ASCII letters, digits, '_' and '-'"
			}
		}

		if windowsReserved[v] {
			return v, "is a device name Windows reserves, where no directory may carry it"
		}
	}

	return "", ""
}

func (c *BuildContext) importPkg(fromPath, importPath string, importPathToken Token) (p *Package) {
	if c == nil {
		return noPkg
	}

	key, dir, inModule := c.importIdent(importPath)
	if !inModule {
		// An intrinsic import (p2) has no source directory by design and no module
		// prefix either, and is handled by the emitter.
		if !intrinsicImports[importPath] {
			c.syncErr(importPathToken.Position(), "%s", c.notInModule(importPath))
		}
		return noPkg
	}

	if bad, why := validPkgDir(dir); bad != "" {
		c.syncErr(importPathToken.Position(), "invalid import path %q: package directory %q %s", importPath, bad, why)
		return noPkg
	}

	// The main package is a program, not a library. Importing it would compile its
	// files a second time under a package name and send its own imports back round
	// to whoever imported it, which is a cycle reported as one somewhere far from
	// the mistake.
	if dir != "" && dir == c.mainDir {
		c.syncErr(importPathToken.Position(), "import of main package %q", importPath)
		return noPkg
	}

	c.importsMu.Lock()

	if c.importGraph[fromPath] == nil {
		c.importGraph[fromPath] = make(map[string]bool)
	}
	c.importGraph[fromPath][key] = true

	if cycle := c.findCycle(key, fromPath, make(map[string]bool)); cycle != nil {
		c.importsMu.Unlock()

		// To complete the visual circle for the error message, put 'fromPath' at the front.
		fullCycle := append([]string{fromPath}, cycle...)
		c.syncErr(importPathToken.Position(), "import cycle not allowed: %s", strings.Join(fullCycle, " -> "))
		return noPkg
	}

	task := c.importTasks[key]
	if task == nil {
		task = &importTask{}
		c.importTasks[key] = task
	}

	c.importsMu.Unlock()

	task.Lock()

	if task.ready == nil {
		task.ready = make(chan struct{})
		go func() {
			defer close(task.ready)

			if src, embedded := embeddedPkgs[importPath]; embedded {
				fn := path.Join(importPath, importPath+".ogo")
				task.p = c.NewPackage(importPath, []string{fn}, fstest.MapFS{fn: &fstest.MapFile{Data: []byte(src)}})
				return
			}

			dirEntries, err := fs.ReadDir(c.fsys, dir)
			if err != nil {
				task.p = noPkg
				// A non-intrinsic import that names no readable directory is a
				// mistake -- a typo, or a package that is not present. An intrinsic
				// import (p2) has no source directory by design and is handled by the
				// emitter, so it is not reported here.
				if !intrinsicImports[importPath] {
					c.syncErr(importPathToken.Position(), "cannot find package %q", importPath)
				}
				return
			}

			if dir == "." {
				if fi, err := fs.Stat(c.fsys, path.Base(c.modulePath)); err == nil && fi.IsDir() {
					task.p = noPkg
					c.syncErr(importPathToken.Position(), "cannot import module root %q: directory %s beside it claims the same package name %q", importPath, path.Base(c.modulePath), path.Base(c.modulePath))
					return
				}
			}

			var files []string
			for _, v := range dirEntries {
				if v.IsDir() {
					continue
				}

				switch nm := v.Name(); path.Ext(nm) {
				case ".ogo":
					if strings.HasSuffix(nm, "_test.ogo") {
						break
					}

					// The importing statement is the position there is: a file
					// has none of its own, and this is what pulled it in.
					if bad := reservedFileName(nm); bad != "" {
						c.syncErr(importPathToken.Position(), "package %q has file %s: %q is a device name Windows reserves, where no file may carry it", importPath, nm, bad)
						task.p = noPkg
						return
					}

					files = append(files, path.Join(dir, nm))
				}
			}

			task.p = c.NewPackage(key, files, c.fsys)
		}()
	}

	task.Unlock()
	<-task.ready
	return task.p
}

func consolidateErrors(use ErrList, errors ...error) (e ErrList) {
	e = use
	for _, v := range errors {
		switch x := v.(type) {
		case nil:
			// nop
		case ErrList:
			e = append(e, x...)
		default:
			e = append(e, ErrWithPosition{Err: x})
		}
	}
	return e
}

// Build builds the main package consisting of files in 'files' within 'fsys'.
// 'limit' is the maximum desired concurrency for individual package building
// when > 0.
//
// 'files' must be base names within fsys. Build resolves an import path a/b/c as
// the path a/b/c within fsys, which is the module-less form: fsys is the package
// being built and every package of the program is a directory in it. See
// BuildModule for a program that declares one.
func Build(limit int, files []string, fsys fs.FS) (main *Package, err error) {
	return BuildModule(limit, "", ".", files, fsys)
}

// BuildModule builds the main package of a module: 'modulePath' is what its import
// paths are prefixed with, 'dir' is the main package's own directory within 'fsys'
// and 'files' are its base names there. An import path is resolved by stripping
// 'modulePath' and reading the rest as a directory of 'fsys', so an import means the
// same directory in every file that writes it, whichever package is being built.
//
// An empty 'modulePath' with dir "." is the module-less form Build passes.
func BuildModule(limit int, modulePath, dir string, files []string, fsys fs.FS) (main *Package, err error) {
	for _, v := range files {
		if path.Base(v) != v {
			return noPkg, fmt.Errorf("not a base name: %s", v)
		}

		if bad := reservedFileName(v); bad != "" {
			return noPkg, fmt.Errorf("%s: %q is a device name Windows reserves, where no file may carry it", v, bad)
		}
	}

	c := NewBuildContext(fsys, limit)
	c.modulePath = modulePath
	c.mainDir = path.Clean(dir)

	defer func() {
		var errs ErrList
		for _, v := range c.importTasks {
			errs = v.p.consolidateErrors(errs)
		}
		if main != nil {
			errs = main.consolidateErrors(errs)
		}
		errs = consolidateErrors(errs, c.errList)
		// Establish stable order
		sort.Slice(errs, func(i, j int) bool { return errs[i].less(errs[j]) })
		// A file the parser could not read is not checked: every checker error it
		// yields is derived from a tree that is not what was written, and says so
		// somewhere the reader has to work to connect to the cause. `a := [3]int(q)`,
		// a conversion the grammar did not accept until 2026-09-21, reported "undefined:
		// Row" against an unrelated declaration three lines above the syntax error, the
		// broken parse having cost the whole file its type declarations. Go stops
		// after parsing for the same reason.
		//
		// Only that file is silenced: another file in the package parsed fine, and
		// its errors are about what it says.
		broken := map[string]bool{}
		for _, v := range errs {
			if v.Parse && v.Pos.IsValid() {
				broken[v.Pos.Filename] = true
			}
		}
		if len(broken) != 0 {
			w := 0
			for _, v := range errs {
				if !v.Parse && v.Pos.IsValid() && broken[v.Pos.Filename] {
					continue
				}
				errs[w] = v
				w++
			}
			errs = errs[:w]
		}
		// Remove multiple errors for the same line, keeping the parser's where there
		// is one -- the same rule within a line, for the files that DID parse: a
		// checker error there is a consequence hiding its cause. `if v, ok := f(); ok`
		// said "undefined: v" and swallowed the parse error at the comma that
		// explains why v was never declared.
		w := 0
		for _, v := range errs {
			if w == 0 {
				errs[w] = v
				w++
				continue
			}

			if !v.sameFileAndLine(errs[w-1]) {
				errs[w] = v
				w++
				continue
			}
			if v.Parse && !errs[w-1].Parse {
				errs[w-1] = v
			}
		}
		errs = errs[:w]
		err = errs.Err()
	}()

	// The main package has no import path: its C symbols are unprefixed, whether it
	// sits at the module root or in a subdirectory of one.
	main = c.NewPackage("", joinDir(dir, files), fsys)
	return main, nil
}

// joinDir places a package's base names in its directory within the module. The
// module-less build passes ".", where the names are already where they belong.
func joinDir(dir string, files []string) (r []string) {
	if dir == "" || dir == "." {
		return files
	}

	r = make([]string, len(files))
	for i, v := range files {
		r[i] = path.Join(dir, v)
	}
	return r
}

type limiter chan struct{}

func newLimiter(limit int) limiter {
	if limit > 0 {
		return make(limiter, limit)
	}

	return nil
}

func (n limiter) limit() func() {
	if n == nil {
		return func() {}
	}

	n <- struct{}{}
	return func() { <-n }
}

// Package represents a single OctoGo package.
type Package struct {
	Files      []*File
	ImportPath string
	Scope      *Scope
	ctx        *BuildContext
	// spin2 are the Spin2 objects the package carries (spin2Objects), and
	// spin2Funcs the functions declared without a body that their PUB methods
	// implement, by name (bindSpin2).
	spin2      []*spin2Object
	spin2Funcs map[string]*spin2Binding
	spin2Mu    sync.Mutex
	// mainRef caches whether the package names its func main anywhere but where it
	// is declared (mainReferenced).
	mainRefOnce sync.Once
	mainRef     bool
}

// fileOf is the file parsed from src, or nil for a source no package of this
// build was read from (the universe's own declarations).
func (c *BuildContext) fileOf(src *source) *File {
	c.filesMu.Lock()
	defer c.filesMu.Unlock()
	return c.srcFiles[src]
}

// NewPackage returns a newly created Package consisting of files in 'files'
// within 'fsys'.
func (c *BuildContext) NewPackage(importPath string, files []string, fsys fs.FS) (p *Package) {
	p = &Package{
		Files:      make([]*File, len(files)),
		ImportPath: importPath,
		Scope:      newScope(Universe, PackageScope),
		ctx:        c,
	}
	if importPath == "unsafe" {
		p.Scope.add(unsafePointerDecl)
	}
	// The Spin2 objects beside the package's files, which its functions declared
	// without a body are bound to. An embedded package's are the emitter's
	// intrinsics instead, and it has no directory to carry one in.
	if _, embedded := embeddedPkgs[importPath]; !embedded && len(files) != 0 {
		p.spin2 = spin2Objects(fsys, path.Dir(files[0]))
	}

	// Phase 1: Local Scope Population (Parallel)
	limiter := newLimiter(c.limit)
	var wg sync.WaitGroup
	for i, v := range files {
		release := limiter.limit()

		wg.Add(1)
		go func(i int, fn string) {
			defer release()
			defer wg.Done()

			p.Files[i] = p.newFile(fn, fsys)
		}(i, v)
	}
	wg.Wait()
	c.filesMu.Lock()
	for _, f := range p.Files {
		if f != nil && f.parser.tok.source != nil {
			c.srcFiles[f.parser.tok.source] = f
		}
	}
	c.filesMu.Unlock()
	if c.noDeclarationChecks { // Testing support
		return p
	}

	// Phase 2: Package Scope Merging (Serial)
	for _, f := range p.Files {
		for _, spec := range f.ImportSpecs {
			// An import that resolves to a real package is eligible for the
			// later unused-import report; a failed import (missing directory,
			// cycle) resolves to noPkg and is reported at import time instead. The
			// resolved package is retained on the spec so the checker can resolve
			// qualified names against it and the emitter can emit its declarations.
			spec.Pkg = c.importPkg(p.ImportPath, spec.ImportPath, spec.ImportPathToken)
			spec.resolved = spec.Pkg != noPkg
		}
		// Merge file top level declarations into package scope.
		for _, nm := range slices.Sorted(maps.Keys(f.tld.Declarations)) {
			d := f.tld.Declarations[nm]
			if err := p.Scope.add(d); err != nil {
				c.syncErr(d.Token().Position(), "%v", err)
			}
		}
		f.tld.Declarations = nil
		f.Scope.Parent = p.Scope // Rewire/repair the scope hierarchy (Block->File->Package->Universe)
	}
	// Ensure "no identifier may be declared in both the file and package block".
	for _, v := range p.Files {
		for _, nm := range slices.Sorted(maps.Keys(v.Scope.Declarations)) {
			if ex := p.Scope.Declarations[nm]; ex != nil {
				d := v.Scope.Declarations[nm]
				c.err(ex.Token().Position(), "cannot declare %v both in package and file scope (%v:)", nm, d.Token().Position())
			}
		}
	}

	// Phase 3: Top-Level Type & Constant Evaluation (Serial)
	//
	// Every file's type BODIES first: a declaration may use a type declared below it
	// or in another file, and one resolved in source order had none yet -- `var g =
	// Sq{2}` above `type Sq struct{...}` was "Sq is not a struct type". A body that
	// needs a constant evaluates it on demand, as it always could.
	for _, v := range p.Files {
		for n := range it(v.AST) {
			if n.sym == SourceFile {
				v.typeBodies(p.Scope, n)
			}
		}
	}
	// Then every METHOD is attached to its type, for the same reason: a package
	// variable declared above a method, `var f = (*B).get` or a table of method
	// expressions, found the type with no such method -- "(*B).get undefined" --
	// where a function body, checked after all of this, always found it.
	for _, v := range p.Files {
		for n := range it(v.AST) {
			if n.sym == SourceFile {
				v.methodDecls(p.Scope, n)
			}
		}
	}
	for _, v := range p.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.sourceFile(p.Scope, n)
			}
		}
	}

	// Phase 4: Body Checking & Hardware Constraints (Parallel)
	//
	// Bodies are fully walked and statement type checking runs here (checkBlock
	// and everything under it: expression and assignment typing, reachability,
	// goto's jump rules, the block-lifetime rule). The hardware-side refusals --
	// escape analysis across cogs, the ABI walls, lifetime of what a frame owns
	// -- live in the emitter's own passes rather than in this phase, because they
	// need the C-level shapes emission decides.
	for _, v := range p.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.checkBodies(p.Scope, n)
			}
		}
	}

	// Phase 5: Deep Initialization Cycle Detection (Serial)
	//
	// Value-recursive types (infinite size) are reported here. Global
	// initialization-order cycles are refused by the emitter's ordering pass
	// (initorder.go), which reads function and method bodies for the dependency
	// walk and so runs where those are already resolved and mangled.
	for _, v := range p.Files {
		for n := range it(v.AST) {
			switch n.sym {
			case SourceFile:
				v.checkTypeCycles(p.Scope, n)
			}
		}
	}

	// Semantic import diagnostics. Dot imports are unsupported and rejected in
	// every package. Unused imports are reported only for the package under
	// compilation (import path ""); a recursively-built dependency's own unused
	// imports are reported when that dependency is compiled directly, not here.
	// This runs after body checking, so usage in function bodies has been seen,
	// and below the noDeclarationChecks early return, so a parse-only pass does
	// not see these semantic diagnostics.
	p.reportNeverMadeChans()
	for _, v := range p.Files {
		v.rejectDotImports()
		if p.ImportPath == "" {
			v.reportUnusedImports()
		}
	}
	return p
}

func (p *Package) consolidateErrors(use ErrList) (e ErrList) {
	e = use
	for _, v := range p.Files {
		e = v.consolidateErrors(e)
	}
	return e
}
